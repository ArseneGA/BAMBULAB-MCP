// Package oauth est le serveur d'autorisation OAuth 2.1 intégré à bambud : il permet de déclarer bambud comme
// connecteur distant de Claude (claude.ai, mobile, Desktop). Profil minimal et strict :
//
//   - clients publics seulement (enregistrement dynamique RFC 7591, PKCE S256 obligatoire) ;
//   - redirections limitées à la liste blanche (callback de Claude, boucle locale) ;
//   - une seule portée, « mcp » : le jeton ouvre /mcp et rien d'autre, avec les droits du Bearer statique
//     (jamais l'approbation d'un lancement ou d'un arrêt, qui reste sur /confirm et /stop) ;
//   - jetons d'accès courts (1 h) et jeton de rafraîchissement à rotation (30 j) avec détection de rejeu ;
//   - jetons stockés hachés, révocables (bambud oauth --list / --revoke) ;
//   - le consentement exige un navigateur enregistré (humanauth) : voir internal/httpapi/oauth.go.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Scope est l'unique portée : accès à /mcp.
const Scope = "mcp"

// Durées.
const (
	AccessTTL  = time.Hour
	RefreshTTL = 30 * 24 * time.Hour
	CodeTTL    = 60 * time.Second
	PendingTTL = 10 * time.Minute

	// ClientTTL : un client enregistré qui n'a obtenu aucune autorisation est purgé après ce délai.
	ClientTTL = 15 * time.Minute

	maxClients     = 200
	maxPending     = 200
	maxRedirects   = 5
	maxNameLen     = 100
	maxURILen      = 512
	usedCodeMemory = 10 * time.Minute
)

// DefaultRedirects : callbacks des applications hébergées de Claude (claude.ai web, Desktop, mobile, Cowork).
// Source : https://claude.com/docs/connectors/building/authentication (« Callback URLs »).
var DefaultRedirects = []string{
	"https://claude.ai/api/mcp/auth_callback",
	"https://claude.com/api/mcp/auth_callback",
}

// Error est une erreur OAuth (RFC 6749 §5.2).
type Error struct {
	Code   string
	Desc   string
	Status int
}

func (e *Error) Error() string { return e.Code + ": " + e.Desc }

func oerr(status int, code, desc string) *Error {
	return &Error{Code: code, Desc: desc, Status: status}
}

// Options du service.
type Options struct {
	Now func() time.Time
	// ExtraRedirects : URI de redirection exactes autorisées en plus de DefaultRedirects et de la boucle locale.
	ExtraRedirects []string
	// Log reçoit les erreurs d'audit et de mise à jour (défaut : journal par défaut).
	Log *slog.Logger
	// Limites de débit PAR ADRESSE (défauts : enregistrement 30 puis un toutes les 3 s ; autorisation 30 puis un toutes les 2 s)
	// et plafond de clients récents par adresse (défaut 30).
	RegisterBurst, AuthorizeBurst, MaxClientsPerIP int
	RegisterEvery, AuthorizeEvery                  time.Duration
}

type bucket struct {
	tokens float64
	at     time.Time
}

// Service est le serveur d'autorisation.
type Service struct {
	st   *store.Store
	o    Options
	mu   sync.Mutex
	pen  map[string]*Pending
	cod  map[string]*authCode
	use  map[string]usedCode
	lim  map[string]*bucket
	regs map[string][]time.Time // enregistrements récents par adresse
	log  *slog.Logger
}

// allow applique une limite de débit par adresse et par catégorie.
func (s *Service) allow(cat, ip string, burst int, every time.Duration) bool {
	if ip == "" {
		return true
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.lim) > 10000 { // table bornée : on oublie les adresses inactives
		for k, b := range s.lim {
			if now.Sub(b.at) > 10*time.Minute {
				delete(s.lim, k)
			}
		}
		if len(s.lim) > 10000 {
			s.lim = map[string]*bucket{}
		}
	}
	k := cat + "|" + ip
	b := s.lim[k]
	if b == nil {
		b = &bucket{tokens: float64(burst), at: now}
		s.lim[k] = b
	}
	b.tokens += float64(now.Sub(b.at)) / float64(every)
	if b.tokens > float64(burst) {
		b.tokens = float64(burst)
	}
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (s *Service) audit(ctx context.Context, actor, action, kind, id string, detail any) {
	if err := s.st.Audit(ctx, actor, action, kind, id, detail); err != nil {
		s.log.Error("oauth : écriture du journal d'audit impossible", "action", action, "err", err)
	}
}

func (s *Service) exec(ctx context.Context, what, q string, args ...any) {
	if _, err := s.st.DB().ExecContext(ctx, q, args...); err != nil {
		s.log.Error("oauth : mise à jour impossible", "what", what, "err", err)
	}
}

// New construit le service au-dessus d'une base migrée.
func New(st *store.Store, o Options) *Service {
	if o.Now == nil {
		o.Now = func() time.Time { return st.Now() }
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.RegisterBurst <= 0 {
		o.RegisterBurst = 30
	}
	if o.RegisterEvery <= 0 {
		o.RegisterEvery = 3 * time.Second
	}
	if o.AuthorizeBurst <= 0 {
		o.AuthorizeBurst = 30
	}
	if o.AuthorizeEvery <= 0 {
		o.AuthorizeEvery = 2 * time.Second
	}
	if o.MaxClientsPerIP <= 0 {
		o.MaxClientsPerIP = 30
	}
	return &Service{st: st, o: o, log: o.Log, pen: map[string]*Pending{}, cod: map[string]*authCode{}, use: map[string]usedCode{},
		lim: map[string]*bucket{}, regs: map[string][]time.Time{}}
}

func (s *Service) now() time.Time { return s.o.Now().UTC() }

// Pending est une demande d'autorisation validée, en attente du consentement de l'humain.
type Pending struct {
	ID          string
	Nonce       string
	ClientID    string
	ClientName  string
	RedirectURI string
	State       string
	Challenge   string
	Resource    string
	Scope       string
	Expires     time.Time
	// Éléments affichés à l'humain pour qu'il reconnaisse SA demande.
	RemoteIP    string
	RequestedAt time.Time
	bindHash    string // empreinte du cookie de liaison posé au navigateur qui a ouvert /oauth/authorize
}

type authCode struct {
	clientID, redirectURI, challenge, resource, scope, humanSession string
	expires                                                         time.Time
}

type usedCode struct {
	grantID string
	expires time.Time
}

func randToken(prefix string, n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // pas de source d'aléa : ne jamais émettre de jeton prévisible
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

func hashOf(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }

// CanonResource normalise l'identifiant de ressource pour la comparaison (RFC 8707) : sans « / » final.
func CanonResource(r string) string { return strings.TrimRight(strings.TrimSpace(r), "/") }

func isLoopbackHost(h string) bool {
	h = strings.Trim(strings.ToLower(h), "[]")
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// checkRedirect valide une URI de redirection à l'enregistrement.
func (s *Service) checkRedirect(raw string) error {
	if raw == "" || len(raw) > maxURILen {
		return errors.New("URI de redirection vide ou trop longue")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Fragment != "" || u.User != nil || u.Host == "" {
		return errors.New("URI de redirection invalide (absolue, sans fragment ni identifiants)")
	}
	for _, d := range append(append([]string{}, DefaultRedirects...), s.o.ExtraRedirects...) {
		if raw == d {
			return nil
		}
	}
	if u.Scheme == "http" && isLoopbackHost(u.Hostname()) {
		return nil // RFC 8252 : application native sur la boucle locale (Claude Code)
	}
	return fmt.Errorf("URI de redirection non autorisée : %s (callbacks de Claude et boucle locale seulement)", u.Redacted())
}

// redirectMatches : correspondance exacte, sauf la boucle locale où le port est ignoré (RFC 8252 §7.3).
func redirectMatches(registered []string, got string) bool {
	gu, gerr := url.Parse(got)
	for _, r := range registered {
		if r == got {
			return true
		}
		ru, err := url.Parse(r)
		if err != nil || gerr != nil {
			continue
		}
		if ru.Scheme == "http" && gu.Scheme == "http" && isLoopbackHost(ru.Hostname()) && strings.EqualFold(ru.Hostname(), gu.Hostname()) &&
			ru.Path == gu.Path && ru.RawQuery == gu.RawQuery {
			return true
		}
	}
	return false
}

// Client est un client enregistré.
type Client struct {
	ID           string
	Name         string
	RedirectURIs []string
	CreatedAt    time.Time
}

func cleanName(n string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(n) {
		if r >= ' ' && r != 0x7f {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len([]rune(out)) > maxNameLen {
		out = string([]rune(out)[:maxNameLen])
	}
	return out
}

// Register traite un enregistrement dynamique (RFC 7591) et renvoie la réponse JSON.
func (s *Service) Register(ctx context.Context, body []byte, ip string) (map[string]any, *Error) {
	if !s.allow("register", ip, s.o.RegisterBurst, s.o.RegisterEvery) {
		return nil, oerr(429, "temporarily_unavailable", "trop d'enregistrements depuis cette adresse : réessaie dans un instant")
	}
	var req struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
		GrantTypes   []string `json:"grant_types"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, oerr(400, "invalid_client_metadata", "corps JSON illisible")
	}
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > maxRedirects {
		return nil, oerr(400, "invalid_redirect_uri", fmt.Sprintf("1 à %d redirect_uris attendues", maxRedirects))
	}
	for _, r := range req.RedirectURIs {
		if err := s.checkRedirect(r); err != nil {
			return nil, oerr(400, "invalid_redirect_uri", err.Error())
		}
	}
	if m := req.AuthMethod; m != "" && m != "none" {
		return nil, oerr(400, "invalid_client_metadata", "seuls les clients publics sont acceptés (token_endpoint_auth_method=none) ; PKCE S256 les protège")
	}
	for _, g := range req.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			return nil, oerr(400, "invalid_client_metadata", "grant_type non pris en charge : "+g)
		}
	}
	now := s.now()
	c := Client{ID: randToken("bc_", 16), Name: cleanName(req.ClientName), RedirectURIs: req.RedirectURIs, CreatedAt: now}
	uris, _ := json.Marshal(c.RedirectURIs)
	err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		// Enregistrement non authentifié : on borne la table (clients sans autorisation purgés après ClientTTL).
		if _, e := tx.ExecContext(ctx, `DELETE FROM oauth_clients WHERE created_at < ? AND NOT EXISTS (SELECT 1 FROM oauth_grants g WHERE g.client_id=oauth_clients.client_id)`,
			store.FormatTime(now.Add(-ClientTTL))); e != nil {
			return e
		}
		if ip != "" { // plafond de clients récents par adresse
			s.mu.Lock()
			recent := s.regs[ip][:0:0]
			for _, t := range s.regs[ip] {
				if now.Sub(t) < ClientTTL {
					recent = append(recent, t)
				}
			}
			if len(recent) >= s.o.MaxClientsPerIP {
				s.regs[ip] = recent
				s.mu.Unlock()
				return errTooMany
			}
			if len(s.regs) > 10000 {
				s.regs = map[string][]time.Time{}
			}
			s.regs[ip] = append(recent, now)
			s.mu.Unlock()
		}
		var n int
		if e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM oauth_clients`).Scan(&n); e != nil {
			return e
		}
		if n >= maxClients {
			return errTooMany
		}
		_, e := tx.ExecContext(ctx, `INSERT INTO oauth_clients(client_id,client_name,redirect_uris,created_at) VALUES(?,?,?,?)`,
			c.ID, c.Name, string(uris), store.FormatTime(now))
		return e
	})
	if errors.Is(err, errTooMany) {
		return nil, oerr(429, "temporarily_unavailable", "trop de clients enregistrés")
	}
	if err != nil {
		return nil, oerr(500, "server_error", "erreur interne")
	}
	return map[string]any{
		"client_id": c.ID, "client_id_issued_at": now.Unix(), "client_name": c.Name,
		"redirect_uris": c.RedirectURIs, "grant_types": []string{"authorization_code", "refresh_token"},
		"response_types": []string{"code"}, "token_endpoint_auth_method": "none", "scope": Scope,
	}, nil
}

var errTooMany = errors.New("trop de clients")

func (s *Service) client(ctx context.Context, id string) (Client, error) {
	var c Client
	var uris, created string
	err := s.st.DB().QueryRowContext(ctx, `SELECT client_id,client_name,redirect_uris,created_at FROM oauth_clients WHERE client_id=?`, id).Scan(&c.ID, &c.Name, &uris, &created)
	if err != nil {
		return Client{}, err
	}
	_ = json.Unmarshal([]byte(uris), &c.RedirectURIs)
	c.CreatedAt, _ = store.ParseTime(created)
	return c, nil
}

// AuthzResult : issue de la validation d'une demande d'autorisation.
type AuthzResult struct {
	// Pending est renseigné quand la demande est valide (en attente de consentement).
	Pending *Pending
	// Fatal : erreur à afficher à l'humain SANS redirection (client ou redirect_uri non fiable).
	Fatal string
	// RedirectError : erreur à renvoyer au client par redirection (RedirectURI, State déjà valides).
	RedirectError *Error
	RedirectURI   string
	State         string
	// Limited : trop de demandes depuis cette adresse (HTTP 429).
	Limited bool
	// Bind : valeur du cookie de liaison à poser au navigateur qui a ouvert la demande (exigée au consentement).
	Bind string
}

// Authorize valide une demande d'autorisation (paramètres de la requête GET). resource est l'identifiant canonique
// de /mcp pour l'hôte atteint (« https://hôte/mcp »).
func (s *Service) Authorize(ctx context.Context, q url.Values, resource, ip string) AuthzResult {
	if !s.allow("authorize", ip, s.o.AuthorizeBurst, s.o.AuthorizeEvery) {
		return AuthzResult{Limited: true, Fatal: "Trop de demandes depuis cette adresse : réessaie dans un instant."}
	}
	cid := q.Get("client_id")
	c, err := s.client(ctx, cid)
	if err != nil {
		return AuthzResult{Fatal: "client_id inconnu : l'application doit s'enregistrer d'abord (ou l'enregistrement a expiré : relancer la connexion dans Claude)."}
	}
	ru := q.Get("redirect_uri")
	if ru == "" || !redirectMatches(c.RedirectURIs, ru) {
		return AuthzResult{Fatal: "redirect_uri absente ou différente de celle enregistrée."}
	}
	state := q.Get("state")
	fail := func(code, desc string) AuthzResult {
		return AuthzResult{RedirectError: oerr(302, code, desc), RedirectURI: ru, State: state}
	}
	if q.Get("response_type") != "code" {
		return fail("unsupported_response_type", "response_type=code requis")
	}
	ch := q.Get("code_challenge")
	if q.Get("code_challenge_method") != "S256" || len(ch) < 43 || len(ch) > 128 {
		return fail("invalid_request", "PKCE obligatoire : code_challenge et code_challenge_method=S256")
	}
	if r := q.Get("resource"); r != "" && CanonResource(r) != CanonResource(resource) {
		return fail("invalid_target", "resource inconnue (attendu "+resource+")")
	}
	for _, sc := range strings.Fields(q.Get("scope")) {
		if sc != Scope && sc != "offline_access" {
			return fail("invalid_scope", "portée non prise en charge : "+sc)
		}
	}
	if len(state) > 2048 {
		return fail("invalid_request", "state trop long")
	}
	now := s.now()
	bind := randToken("", 24)
	p := &Pending{ID: randToken("", 32), Nonce: randToken("", 24), ClientID: c.ID, ClientName: c.Name, RedirectURI: ru, State: state,
		Challenge: ch, Resource: CanonResource(resource), Scope: Scope, Expires: now.Add(PendingTTL), RemoteIP: ip, RequestedAt: now, bindHash: hashOf(bind)}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	if len(s.pen) >= maxPending {
		return fail("temporarily_unavailable", "trop de demandes en cours")
	}
	s.pen[p.ID] = p
	return AuthzResult{Pending: p, Bind: bind}
}

// Bound dit si le jeton de liaison présenté (cookie) est celui du navigateur qui a ouvert la demande. Sans cette
// liaison, une page de consentement transmise à un tiers pourrait être approuvée à la place de l'initiateur.
func (p *Pending) Bound(token string) bool {
	return token != "" && subtle.ConstantTimeCompare([]byte(hashOf(token)), []byte(p.bindHash)) == 1
}

func (s *Service) pruneLocked(now time.Time) {
	for k, p := range s.pen {
		if !now.Before(p.Expires) {
			delete(s.pen, k)
		}
	}
	for k, c := range s.cod {
		if !now.Before(c.expires) {
			delete(s.cod, k)
		}
	}
	for k, u := range s.use {
		if !now.Before(u.expires) {
			delete(s.use, k)
		}
	}
}

// PendingByID renvoie la demande en attente (non expirée).
func (s *Service) PendingByID(id string) (*Pending, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pen[id]
	if !ok || !s.now().Before(p.Expires) {
		return nil, false
	}
	return p, true
}

func addQuery(base string, kv map[string]string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if kv[k] != "" {
			q.Set(k, kv[k])
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// ErrorRedirect construit l'URL de redirection d'une erreur (avec iss, RFC 9207).
func ErrorRedirect(redirectURI, state, issuer string, e *Error) string {
	return addQuery(redirectURI, map[string]string{"error": e.Code, "error_description": e.Desc, "state": state, "iss": issuer})
}

// Decide traite la décision de l'humain sur une demande en attente : à usage unique (la demande est retirée).
// nonce = celui du formulaire ; humanSession = identifiant du navigateur enregistré qui a consenti.
func (s *Service) Decide(ctx context.Context, id, nonce, issuer, humanSession, bind string, approve bool) (redirectURL string, err error) {
	s.mu.Lock()
	p, ok := s.pen[id]
	if !ok || !s.now().Before(p.Expires) {
		s.mu.Unlock()
		return "", errors.New("demande expirée ou déjà traitée : relancer la connexion dans Claude")
	}
	if subtle.ConstantTimeCompare([]byte(nonce), []byte(p.Nonce)) != 1 {
		s.mu.Unlock()
		return "", errors.New("formulaire invalide")
	}
	if !p.Bound(bind) {
		s.mu.Unlock()
		return "", errors.New("cette demande a été ouverte dans un autre navigateur : relance la connexion depuis Claude, dans le navigateur où tu l'as lancée")
	}
	delete(s.pen, id)
	if !approve {
		s.mu.Unlock()
		s.audit(ctx, "human:web", "oauth.denied", "oauth_client", p.ClientID, nil)
		return ErrorRedirect(p.RedirectURI, p.State, issuer, oerr(302, "access_denied", "refusé par l'utilisateur")), nil
	}
	code := randToken("", 32)
	s.cod[hashOf(code)] = &authCode{clientID: p.ClientID, redirectURI: p.RedirectURI, challenge: p.Challenge, resource: p.Resource,
		scope: p.Scope, humanSession: humanSession, expires: s.now().Add(CodeTTL)}
	s.mu.Unlock()
	s.audit(ctx, "human:web", "oauth.approved", "oauth_client", p.ClientID, map[string]any{"client_name": p.ClientName})
	return addQuery(p.RedirectURI, map[string]string{"code": code, "state": p.State, "iss": issuer}), nil
}

// Token traite POST /oauth/token (formulaire x-www-form-urlencoded).
func (s *Service) Token(ctx context.Context, f url.Values) (map[string]any, *Error) {
	switch f.Get("grant_type") {
	case "authorization_code":
		return s.exchangeCode(ctx, f)
	case "refresh_token":
		return s.refresh(ctx, f)
	}
	return nil, oerr(400, "unsupported_grant_type", "authorization_code ou refresh_token")
}

func pkceOK(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	for _, r := range verifier {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.' || r == '_' || r == '~') {
			return false
		}
	}
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

func (s *Service) exchangeCode(ctx context.Context, f url.Values) (map[string]any, *Error) {
	code := f.Get("code")
	if code == "" || f.Get("client_id") == "" {
		return nil, oerr(400, "invalid_request", "code et client_id requis")
	}
	h := hashOf(code)
	now := s.now()
	s.mu.Lock()
	s.pruneLocked(now)
	ac, ok := s.cod[h]
	if ok {
		delete(s.cod, h) // à usage unique, même en cas d'échec ensuite
	}
	used, wasUsed := s.use[h]
	s.mu.Unlock()
	if !ok {
		if wasUsed && used.grantID != "" { // rejeu d'un code déjà échangé : on révoque ce qu'il a produit
			s.exec(ctx, "révocation après rejeu de code", `UPDATE oauth_grants SET revoked_at=? WHERE id=? AND revoked_at IS NULL`, store.FormatTime(now), used.grantID)
			s.audit(ctx, "oauth", "oauth.code_replay_revoked", "oauth_grant", used.grantID, nil)
		}
		return nil, oerr(400, "invalid_grant", "code inconnu, expiré ou déjà utilisé")
	}
	s.mu.Lock()
	s.use[h] = usedCode{expires: now.Add(usedCodeMemory)}
	s.mu.Unlock()
	if !now.Before(ac.expires) || ac.clientID != f.Get("client_id") {
		return nil, oerr(400, "invalid_grant", "code expiré ou émis pour un autre client")
	}
	if f.Get("redirect_uri") != ac.redirectURI {
		return nil, oerr(400, "invalid_grant", "redirect_uri différente de la demande d'autorisation")
	}
	if !pkceOK(f.Get("code_verifier"), ac.challenge) {
		return nil, oerr(400, "invalid_grant", "code_verifier invalide (PKCE)")
	}
	if r := f.Get("resource"); r != "" && CanonResource(r) != ac.resource {
		return nil, oerr(400, "invalid_target", "resource différente de la demande d'autorisation")
	}
	grantID := randToken("g_", 9)
	access, refresh := randToken("bat_", 32), randToken("brt_", 32)
	err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, `INSERT INTO oauth_grants(id,client_id,scope,resource,human_session_id,created_at,last_used_at,refresh_hash,refresh_expires_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			grantID, ac.clientID, ac.scope, ac.resource, ac.humanSession, store.FormatTime(now), store.FormatTime(now), hashOf(refresh), store.FormatTime(now.Add(RefreshTTL))); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, `INSERT INTO oauth_access_tokens(token_hash,grant_id,expires_at) VALUES(?,?,?)`, hashOf(access), grantID, store.FormatTime(now.Add(AccessTTL))); e != nil {
			return e
		}
		return s.st.AuditTx(ctx, tx, "oauth", "oauth.grant_created", "oauth_grant", grantID, map[string]any{"client_id": ac.clientID})
	})
	if err != nil {
		return nil, oerr(500, "server_error", "erreur interne")
	}
	s.mu.Lock()
	s.use[h] = usedCode{grantID: grantID, expires: now.Add(usedCodeMemory)}
	s.mu.Unlock()
	return tokenResponse(access, refresh, ac.scope), nil
}

func tokenResponse(access, refresh, scope string) map[string]any {
	return map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": int(AccessTTL.Seconds()), "refresh_token": refresh, "scope": scope}
}

func (s *Service) refresh(ctx context.Context, f url.Values) (map[string]any, *Error) {
	rt, cid := f.Get("refresh_token"), f.Get("client_id")
	if rt == "" || cid == "" {
		return nil, oerr(400, "invalid_request", "refresh_token et client_id requis")
	}
	h, now := hashOf(rt), s.now()
	var out map[string]any
	var oe *Error
	err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		var id, client, scope, resource, exp string
		var rev sql.NullString
		e := tx.QueryRowContext(ctx, `SELECT id,client_id,scope,resource,refresh_expires_at,revoked_at FROM oauth_grants WHERE refresh_hash=?`, h).Scan(&id, &client, &scope, &resource, &exp, &rev)
		if errors.Is(e, sql.ErrNoRows) {
			// Rejeu d'un ancien jeton de rafraîchissement (rotation) : le jeton a fuité, on révoque l'autorisation.
			var pid string
			if e2 := tx.QueryRowContext(ctx, `SELECT id FROM oauth_grants WHERE prev_refresh_hash=? AND revoked_at IS NULL`, h).Scan(&pid); e2 == nil {
				if _, e3 := tx.ExecContext(ctx, `UPDATE oauth_grants SET revoked_at=? WHERE id=?`, store.FormatTime(now), pid); e3 != nil {
					return e3
				}
				if e4 := s.st.AuditTx(ctx, tx, "oauth", "oauth.refresh_replay_revoked", "oauth_grant", pid, nil); e4 != nil {
					s.log.Error("oauth : audit du rejeu de jeton impossible", "err", e4)
				}
			}
			oe = oerr(400, "invalid_grant", "jeton de rafraîchissement invalide")
			return nil
		}
		if e != nil {
			return e
		}
		expAt, _ := store.ParseTime(exp)
		if rev.Valid || !now.Before(expAt) || client != cid {
			oe = oerr(400, "invalid_grant", "jeton de rafraîchissement révoqué, expiré ou d'un autre client")
			return nil
		}
		if sc := f.Get("scope"); sc != "" {
			for _, x := range strings.Fields(sc) {
				if x != Scope {
					oe = oerr(400, "invalid_scope", "portée supérieure à celle accordée")
					return nil
				}
			}
		}
		if r := f.Get("resource"); r != "" && CanonResource(r) != resource {
			oe = oerr(400, "invalid_target", "resource différente de l'autorisation")
			return nil
		}
		access, refresh := randToken("bat_", 32), randToken("brt_", 32)
		if _, e := tx.ExecContext(ctx, `UPDATE oauth_grants SET refresh_hash=?, prev_refresh_hash=?, refresh_expires_at=?, last_used_at=? WHERE id=?`,
			hashOf(refresh), h, store.FormatTime(now.Add(RefreshTTL)), store.FormatTime(now), id); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, `DELETE FROM oauth_access_tokens WHERE grant_id=?`, id); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, `INSERT INTO oauth_access_tokens(token_hash,grant_id,expires_at) VALUES(?,?,?)`, hashOf(access), id, store.FormatTime(now.Add(AccessTTL))); e != nil {
			return e
		}
		out = tokenResponse(access, refresh, scope)
		return nil
	})
	if err != nil {
		return nil, oerr(500, "server_error", "erreur interne")
	}
	return out, oe
}

// Verify dit si token est un jeton d'accès valide pour la ressource (identifiant canonique de /mcp).
func (s *Service) Verify(ctx context.Context, token, resource string) bool {
	if !strings.HasPrefix(token, "bat_") || len(token) > 200 {
		return false
	}
	var gid, gres, exp, lastUsed string
	var rev sql.NullString
	err := s.st.DB().QueryRowContext(ctx, `SELECT g.id,g.resource,t.expires_at,g.last_used_at,g.revoked_at FROM oauth_access_tokens t JOIN oauth_grants g ON g.id=t.grant_id WHERE t.token_hash=?`,
		hashOf(token)).Scan(&gid, &gres, &exp, &lastUsed, &rev)
	if err != nil || rev.Valid {
		return false
	}
	now := s.now()
	if e, _ := store.ParseTime(exp); !now.Before(e) {
		return false
	}
	if gres != CanonResource(resource) {
		return false // jeton émis pour une autre ressource (RFC 8707)
	}
	if lu, _ := store.ParseTime(lastUsed); now.Sub(lu) > time.Minute {
		s.exec(ctx, "dernier usage", `UPDATE oauth_grants SET last_used_at=? WHERE id=?`, store.FormatTime(now), gid)
	}
	return true
}

// GrantInfo décrit une autorisation pour la CLI.
type GrantInfo struct {
	ID, ClientID, ClientName, Scope  string
	CreatedAt, LastUsedAt, ExpiresAt time.Time
	Revoked                          bool
}

// List renvoie les autorisations, récentes d'abord.
func (s *Service) List(ctx context.Context) ([]GrantInfo, error) {
	rows, err := s.st.DB().QueryContext(ctx, `SELECT g.id,g.client_id,c.client_name,g.scope,g.created_at,g.last_used_at,g.refresh_expires_at,g.revoked_at
		FROM oauth_grants g JOIN oauth_clients c ON c.client_id=g.client_id ORDER BY g.created_at DESC, g.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GrantInfo
	for rows.Next() {
		var g GrantInfo
		var cr, lu, ex string
		var rev sql.NullString
		if err := rows.Scan(&g.ID, &g.ClientID, &g.ClientName, &g.Scope, &cr, &lu, &ex, &rev); err != nil {
			return nil, err
		}
		g.CreatedAt, _ = store.ParseTime(cr)
		g.LastUsedAt, _ = store.ParseTime(lu)
		g.ExpiresAt, _ = store.ParseTime(ex)
		g.Revoked = rev.Valid
		out = append(out, g)
	}
	return out, rows.Err()
}

// Revoke révoque une autorisation par identifiant (préfixe non ambigu accepté) ; ses jetons cessent de valoir.
func (s *Service) Revoke(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("identifiant requis")
	}
	rows, err := s.st.DB().QueryContext(ctx, `SELECT id FROM oauth_grants WHERE revoked_at IS NULL AND id LIKE ? ESCAPE '\'`,
		strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(id)+"%")
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var x string
		if rows.Scan(&x) == nil {
			ids = append(ids, x)
		}
	}
	rows.Close()
	switch {
	case len(ids) == 0:
		return fmt.Errorf("aucune autorisation active ne commence par %q", id)
	case len(ids) > 1:
		return fmt.Errorf("%q est ambigu (%d autorisations) : donner l'identifiant complet", id, len(ids))
	}
	if _, err := s.st.DB().ExecContext(ctx, `UPDATE oauth_grants SET revoked_at=? WHERE id=? AND revoked_at IS NULL`, store.FormatTime(s.now()), ids[0]); err != nil {
		return err
	}
	s.audit(ctx, "human:cli", "oauth.grant_revoked", "oauth_grant", ids[0], nil)
	return nil
}

// RevokeAll révoque toutes les autorisations actives.
func (s *Service) RevokeAll(ctx context.Context) (int64, error) {
	res, err := s.st.DB().ExecContext(ctx, `UPDATE oauth_grants SET revoked_at=? WHERE revoked_at IS NULL`, store.FormatTime(s.now()))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		s.audit(ctx, "human:cli", "oauth.grants_revoked_all", "oauth_grant", "", map[string]any{"count": n})
	}
	return n, nil
}
