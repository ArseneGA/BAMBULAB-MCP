package humanauth

// Passkeys (WebAuthn) : approbation du lancement et de l'arrêt par une clé que seul l'humain détient
// (docs/SECURITY.md § 13).
//
// Principe : l'humain enregistre une ou plusieurs passkeys depuis /setup/passkeys (navigateur déjà enregistré).
// Sur /confirm et /stop, la cérémonie WebAuthn signe un challenge ALÉATOIRE de 256 bits émis par le serveur et
// gardé côté serveur avec ce qu'il approuve (confirmation ou demande d'arrêt, empreinte de la liaison, navigateur
// enregistré, origine). Le serveur vérifie la signature (ES256, EdDSA, RS256… via go-webauthn), l'origine, l'identifiant
// du site (RP ID), la présence ET la vérification de l'utilisateur, et un compteur qui doit progresser ; le challenge
// est à usage unique (consommé à la première tentative, réussie ou non), valable 5 minutes.
//
// Rien de secret n'est stocké : identifiant public et clé publique seulement. Les passkeys ne SUPPRIMENT aucun
// mécanisme existant : le bouton, le code de lancement et le lien navigateur restent disponibles tant que
// l'humain n'a pas activé « exiger une passkey ».

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

const (
	// PasskeyChallengeTTL : durée de vie d'un challenge (la page d'approbation doit être utilisée dans ce délai).
	PasskeyChallengeTTL = 5 * time.Minute
	// MaxPasskeys : nombre maximal de passkeys actives.
	MaxPasskeys = 10

	passkeyMaxSessions   = 256
	passkeyMaxPerSubject = 4
	passkeyNonceTTL      = 15 * time.Minute
	// PasskeyMaxBody : taille maximale d'un champ « credential » ou « assertion » soumis par les pages.
	PasskeyMaxBody = 32 << 10
)

// Erreurs des passkeys. Aucune ne contient de secret.
var (
	ErrPasskeyInsecure  = errors.New("les passkeys exigent HTTPS (ou http://localhost) : ouvre bambud par son adresse https")
	ErrPasskeyIPHost    = errors.New("une passkey ne peut pas être liée à une adresse IP : ouvre bambud par un nom (https, ou http://localhost)")
	ErrPasskeyOrigin    = errors.New("cette adresse n'est pas une origine déclarée pour les passkeys (BAMBU_PUBLIC_URL, en https)")
	ErrPasskeyNone      = errors.New("aucune passkey enregistrée")
	ErrPasskeyRequired  = errors.New("une passkey est exigée (réglage « exiger une passkey ») : approuve depuis la page du navigateur avec ta passkey")
	ErrPasskeyChallenge = errors.New("challenge de passkey inconnu, périmé, déjà utilisé ou lié à une autre demande : recharge la page")
	ErrPasskeyInvalid   = errors.New("passkey refusée : signature, origine, identifiant de site ou vérification de l'utilisateur invalide")
	ErrPasskeyCounter   = errors.New("passkey refusée : le compteur de signatures ne progresse pas (clé clonée ou rejeu)")
	ErrPasskeyLimit     = fmt.Errorf("au plus %d passkeys actives : en révoquer une d'abord", MaxPasskeys)
	ErrPasskeyLastKept  = errors.New("« exiger une passkey » est actif : on ne révoque pas la dernière passkey (désactive d'abord l'exigence)")
)

// PasskeyConfig : réglage du service. PublicURLs est la liste de BAMBU_PUBLIC_URL ; Secure dit si la requête arrive en
// https du point de vue du navigateur (même fonction que pour le cookie).
type PasskeyConfig struct {
	PublicURLs []string
	Secure     func(*http.Request) bool
	RPName     string
}

// PasskeyEnv : identifiant du site (RP ID) et origine sous lesquels la cérémonie se déroule.
type PasskeyEnv struct {
	RPID   string
	Origin string
}

// PasskeyInfo : état public d'une passkey (aucun secret).
type PasskeyInfo struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitzero"`
	SignCount  uint32    `json:"sign_count"`
	RPID       string    `json:"rp_id"`
	Revoked    bool      `json:"revoked"`
	Synced     bool      `json:"synced"` // sauvegardée / synchronisée par le fournisseur (BS)
}

// ApprovalSubject : ce qu'une approbation par passkey couvre. Binding est l'empreinte de la liaison (imprimante,
// empreinte du fichier, plaque, matières, options) pour un lancement ; l'identifiant du travail pour un arrêt.
type ApprovalSubject struct {
	Kind    string // "print" ou "stop"
	ID      string
	Binding string
}

func (a ApprovalSubject) key() string { return a.Kind + ":" + a.ID }

type passkeySession struct {
	purpose string // "register" ou "approve"
	subject string // clé de l'approbation, ou "" pour un enregistrement
	binding string
	browser string // empreinte du cookie du navigateur enregistré qui a ouvert la page
	env     PasskeyEnv
	data    webauthn.SessionData
	expires time.Time
}

// Passkeys gère les passkeys d'une base. Une seule instance par base (PasskeysFor).
type Passkeys struct {
	st       *store.Store
	mu       sync.Mutex
	cfg      PasskeyConfig
	sessions map[string]*passkeySession // clé : challenge (base64url)
	nonces   map[string]passkeyNonce
	now      func() time.Time
}

type passkeyNonce struct {
	browser string
	expires time.Time
}

var passkeyRegistry sync.Map // *store.Store -> *Passkeys

// PasskeysFor renvoie l'instance unique liée à cette base (les pages et la chaîne d'impression la partagent).
func PasskeysFor(st *store.Store) *Passkeys {
	if v, ok := passkeyRegistry.Load(st); ok {
		return v.(*Passkeys)
	}
	v, _ := passkeyRegistry.LoadOrStore(st, &Passkeys{st: st, sessions: map[string]*passkeySession{}, nonces: map[string]passkeyNonce{},
		now: func() time.Time { return st.Now() }})
	return v.(*Passkeys)
}

// Configure fixe l'URL publique et la détection https (appelé au montage du serveur HTTP).
func (p *Passkeys) Configure(cfg PasskeyConfig) {
	p.mu.Lock()
	p.cfg = cfg
	p.mu.Unlock()
}

// SetClock remplace l'horloge (tests).
func (p *Passkeys) SetClock(now func() time.Time) {
	p.mu.Lock()
	p.now = now
	p.mu.Unlock()
}

func (p *Passkeys) clock() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.now().UTC()
}

// BrowserKey identifie le navigateur enregistré d'une requête (empreinte du cookie de session, jamais le cookie).
// Vide s'il n'y a pas de cookie. À n'appeler qu'après Require.
func BrowserKey(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return ""
	}
	return hashOf(c.Value)
}

// ---- environnement : RP ID et origines ----

func hostOfRequest(r *http.Request) string {
	h := strings.ToLower(strings.TrimSpace(r.Host))
	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}
	return strings.Trim(h, "[]")
}

func isLoopbackName(h string) bool { return h == "localhost" || strings.HasSuffix(h, ".localhost") }

// publicHTTPS renvoie, parmi les URL publiques, celles en https avec un nom (pas une adresse IP) : leur hôte, et
// l'origine (schéma://hôte[:port]).
func publicHTTPS(urls []string) (hosts, origins []string) {
	for _, raw := range urls {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Hostname() == "" || net.ParseIP(u.Hostname()) != nil {
			continue
		}
		hosts = append(hosts, strings.ToLower(u.Hostname()))
		origins = append(origins, "https://"+strings.ToLower(u.Host))
	}
	return hosts, origins
}

func underRP(host, rpid string) bool { return host == rpid || strings.HasSuffix(host, "."+rpid) }

// Env détermine le RP ID et l'origine de la requête, ou refuse proprement.
//
//   - Une passkey exige un contexte sécurisé : https, ou http://localhost. Le reste (http sur le LAN, adresse IP) est
//     refusé avec un message qui dit quoi faire.
//   - Avec des URL publiques en https (BAMBU_PUBLIC_URL), le RP ID est l'hôte de la première ; la requête doit venir
//     d'une origine déclarée (plusieurs origines possibles, tant que leur hôte est ce RP ID ou un de ses sous-domaines).
//   - Sans URL publique https : le RP ID est l'hôte de la requête (https, ou localhost).
func (p *Passkeys) Env(r *http.Request) (PasskeyEnv, error) {
	p.mu.Lock()
	cfg := p.cfg
	p.mu.Unlock()
	secure := r.TLS != nil
	if cfg.Secure != nil {
		secure = cfg.Secure(r)
	}
	host := hostOfRequest(r)
	if host == "" {
		return PasskeyEnv{}, ErrPasskeyOrigin
	}
	if net.ParseIP(host) != nil {
		return PasskeyEnv{}, ErrPasskeyIPHost
	}
	scheme := "http"
	if secure {
		scheme = "https"
	}
	if scheme == "http" && !isLoopbackName(host) {
		return PasskeyEnv{}, ErrPasskeyInsecure
	}
	origin := scheme + "://" + strings.ToLower(strings.TrimSpace(r.Host))
	hosts, origins := publicHTTPS(cfg.PublicURLs)
	if len(hosts) == 0 {
		return PasskeyEnv{RPID: host, Origin: origin}, nil
	}
	rpid := hosts[0]
	for i, o := range origins {
		if underRP(hosts[i], rpid) && strings.EqualFold(o, origin) {
			return PasskeyEnv{RPID: rpid, Origin: o}, nil
		}
	}
	return PasskeyEnv{}, fmt.Errorf("%w : ouvre %s", ErrPasskeyOrigin, origins[0]+"/setup/passkeys")
}

func (p *Passkeys) webauthnFor(env PasskeyEnv) (*webauthn.WebAuthn, error) {
	p.mu.Lock()
	name := p.cfg.RPName
	p.mu.Unlock()
	if name == "" {
		name = "Atelier Bambu Lab"
	}
	return webauthn.New(&webauthn.Config{
		RPID: env.RPID, RPDisplayName: name, RPOrigins: []string{env.Origin},
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationRequired,
		},
	})
}

// ---- utilisateur unique ----

type passkeyUser struct{ creds []webauthn.Credential }

func (passkeyUser) WebAuthnID() []byte          { return []byte("bambud-human-owner-v1") }
func (passkeyUser) WebAuthnName() string        { return "bambud" }
func (passkeyUser) WebAuthnDisplayName() string { return "Propriétaire de l'atelier" }
func (u passkeyUser) WebAuthnCredentials() []webauthn.Credential {
	return u.creds
}

type passkeyRow struct {
	info PasskeyInfo
	cred webauthn.Credential
}

func (p *Passkeys) loadRows(ctx context.Context, activeOnly bool) ([]passkeyRow, error) {
	q := `SELECT id,label,credential,sign_count,rp_id,created_at,last_used_at,revoked_at FROM passkeys`
	if activeOnly {
		q += ` WHERE revoked_at IS NULL`
	}
	q += ` ORDER BY created_at, id`
	rows, err := p.st.DB().QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []passkeyRow
	for rows.Next() {
		var id, label, credJSON, rpid, created string
		var count int64
		var used, revoked sql.NullString
		if err := rows.Scan(&id, &label, &credJSON, &count, &rpid, &created, &used, &revoked); err != nil {
			return nil, err
		}
		var c webauthn.Credential
		if err := json.Unmarshal([]byte(credJSON), &c); err != nil {
			return nil, fmt.Errorf("passkey %s illisible : %w", id, err)
		}
		c.Authenticator.SignCount = uint32(count)
		ct, _ := store.ParseTime(created)
		info := PasskeyInfo{ID: id, Label: label, CreatedAt: ct, SignCount: uint32(count), RPID: rpid, Revoked: revoked.Valid, Synced: c.Flags.BackupState}
		if used.Valid {
			info.LastUsedAt, _ = store.ParseTime(used.String)
		}
		out = append(out, passkeyRow{info: info, cred: c})
	}
	return out, rows.Err()
}

// List renvoie les passkeys (actives et révoquées), sans secret.
func (p *Passkeys) List(ctx context.Context) ([]PasskeyInfo, error) {
	rows, err := p.loadRows(ctx, false)
	if err != nil {
		return nil, err
	}
	out := make([]PasskeyInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.info)
	}
	return out, nil
}

// Count renvoie le nombre de passkeys actives.
func (p *Passkeys) Count(ctx context.Context) (int, error) {
	var n int
	err := p.st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM passkeys WHERE revoked_at IS NULL`).Scan(&n)
	return n, err
}

// Required dit si « exiger une passkey » est actif. En cas d'erreur de lecture : faux (le défaut historique), car la
// passkey est facultative ; l'erreur est remontée par RequiredErr.
func (p *Passkeys) Required(ctx context.Context) bool {
	on, _ := p.RequiredErr(ctx)
	return on
}

// RequiredErr est Required avec l'erreur de lecture.
func (p *Passkeys) RequiredErr(ctx context.Context) (bool, error) {
	var v int
	err := p.st.DB().QueryRowContext(ctx, `SELECT require_passkey FROM passkey_settings WHERE id=1`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return v == 1, err
}

// SetRequired active ou désactive « exiger une passkey ». L'activer exige au moins une passkey active (sinon plus
// aucune approbation ne serait possible). actor : « human:web:<session> » ou « human:cli ».
func (p *Passkeys) SetRequired(ctx context.Context, on bool, actor string) error {
	if on {
		if n, err := p.Count(ctx); err != nil {
			return err
		} else if n == 0 {
			return ErrPasskeyNone
		}
	}
	v := 0
	if on {
		v = 1
	}
	now := store.FormatTime(p.clock())
	return p.st.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO passkey_settings(id,require_passkey,updated_at) VALUES(1,?,?)
			ON CONFLICT(id) DO UPDATE SET require_passkey=excluded.require_passkey, updated_at=excluded.updated_at`, v, now); err != nil {
			return err
		}
		return p.st.AuditTx(ctx, tx, actor, "human.passkey_required_set", "passkey_settings", "1", map[string]any{"require_passkey": on})
	})
}

// Revoke révoque une passkey (identifiant ou préfixe non ambigu). Refusé pour la dernière passkey si l'exigence est active.
func (p *Passkeys) Revoke(ctx context.Context, id, actor string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("identifiant de passkey requis")
	}
	rows, err := p.loadRows(ctx, true)
	if err != nil {
		return "", err
	}
	var hit []string
	for _, r := range rows {
		if r.info.ID == id {
			hit = []string{r.info.ID}
			break
		}
		if strings.HasPrefix(r.info.ID, id) {
			hit = append(hit, r.info.ID)
		}
	}
	switch len(hit) {
	case 0:
		return "", errors.New("passkey inconnue ou déjà révoquée")
	case 1:
	default:
		return "", errors.New("préfixe ambigu : donner l'identifiant complet")
	}
	if len(rows) == 1 && p.Required(ctx) {
		return "", ErrPasskeyLastKept
	}
	now := store.FormatTime(p.clock())
	err = p.st.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, `UPDATE passkeys SET revoked_at=? WHERE id=? AND revoked_at IS NULL`, now, hit[0]); e != nil {
			return e
		}
		return p.st.AuditTx(ctx, tx, actor, "human.passkey_revoked", "passkey", hit[0], nil)
	})
	return hit[0], err
}

// ---- sessions de cérémonie (mémoire, à usage unique) ----

func newPasskeyChallenge() ([]byte, error) {
	b, err := protocol.CreateChallenge()
	return []byte(b), err
}

func (p *Passkeys) putSession(s *passkeySession) {
	now := p.now().UTC()
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, v := range p.sessions {
		if !now.Before(v.expires) {
			delete(p.sessions, k)
		}
	}
	for k, v := range p.nonces {
		if !now.Before(v.expires) {
			delete(p.nonces, k)
		}
	}
	// au plus passkeyMaxPerSubject challenges vivants par approbation : le plus ancien est écarté.
	if s.subject != "" || s.purpose == "register" {
		key := s.purpose + "|" + s.subject + "|" + s.browser
		var oldest string
		var oldestAt time.Time
		n := 0
		for k, v := range p.sessions {
			if v.purpose+"|"+v.subject+"|"+v.browser == key {
				n++
				if oldest == "" || v.expires.Before(oldestAt) {
					oldest, oldestAt = k, v.expires
				}
			}
		}
		if n >= passkeyMaxPerSubject {
			delete(p.sessions, oldest)
		}
	}
	if len(p.sessions) >= passkeyMaxSessions { // garde-fou mémoire : le plus ancien d'abord
		var oldest string
		var oldestAt time.Time
		for k, v := range p.sessions {
			if oldest == "" || v.expires.Before(oldestAt) {
				oldest, oldestAt = k, v.expires
			}
		}
		delete(p.sessions, oldest)
	}
	p.sessions[s.data.Challenge] = s
}

// takeSession consomme le challenge (à usage unique, même si la vérification échoue ensuite).
func (p *Passkeys) takeSession(challenge string) *passkeySession {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sessions[challenge]
	if !ok {
		return nil
	}
	delete(p.sessions, challenge)
	if !p.now().UTC().Before(s.expires) {
		return nil
	}
	return s
}

// IssueNonce délivre un nonce à usage unique pour un formulaire de /setup/passkeys, lié au navigateur.
func (p *Passkeys) IssueNonce(browser string) string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	n := base64.RawURLEncoding.EncodeToString(b)
	now := p.clock()
	p.mu.Lock()
	p.nonces[n] = passkeyNonce{browser: browser, expires: now.Add(passkeyNonceTTL)}
	p.mu.Unlock()
	return n
}

// UseNonce consomme le nonce ; faux s'il est inconnu, périmé, ou délivré à un autre navigateur.
func (p *Passkeys) UseNonce(n, browser string) bool {
	now := p.clock()
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.nonces[n]
	delete(p.nonces, n)
	return ok && now.Before(v.expires) && v.browser == browser
}

// ---- enregistrement ----

// BeginRegistration prépare l'enregistrement d'une passkey : renvoie les options (JSON) que le script passe à
// navigator.credentials.create. browser = BrowserKey(r).
func (p *Passkeys) BeginRegistration(ctx context.Context, r *http.Request, browser string) ([]byte, error) {
	env, err := p.Env(r)
	if err != nil {
		return nil, err
	}
	rows, err := p.loadRows(ctx, true)
	if err != nil {
		return nil, err
	}
	if len(rows) >= MaxPasskeys {
		return nil, ErrPasskeyLimit
	}
	wa, err := p.webauthnFor(env)
	if err != nil {
		return nil, err
	}
	creds := make([]webauthn.Credential, 0, len(rows))
	excl := make([]protocol.CredentialDescriptor, 0, len(rows))
	for _, r := range rows {
		creds = append(creds, r.cred)
		excl = append(excl, r.cred.Descriptor())
	}
	creation, data, err := wa.BeginRegistration(passkeyUser{creds: creds},
		webauthn.WithRegistrationOrigin(env.Origin), webauthn.WithExclusions(excl))
	if err != nil {
		return nil, err
	}
	p.putSession(&passkeySession{purpose: "register", browser: browser, env: env, data: *data, expires: p.clock().Add(PasskeyChallengeTTL)})
	return json.Marshal(creation)
}

// FinishRegistration vérifie la réponse du navigateur et enregistre la passkey. actor sert à l'audit.
func (p *Passkeys) FinishRegistration(ctx context.Context, r *http.Request, browser, label, actor string, credentialJSON []byte) (PasskeyInfo, error) {
	parsed, err := protocol.ParseCredentialCreationResponseBytes(credentialJSON)
	if err != nil {
		return PasskeyInfo{}, fmt.Errorf("%w (%s)", ErrPasskeyInvalid, "réponse illisible")
	}
	sess := p.takeSession(parsed.Response.CollectedClientData.Challenge)
	if sess == nil || sess.purpose != "register" || sess.browser != browser {
		return PasskeyInfo{}, ErrPasskeyChallenge
	}
	env, err := p.Env(r)
	if err != nil {
		return PasskeyInfo{}, err
	}
	if env != sess.env {
		return PasskeyInfo{}, ErrPasskeyChallenge
	}
	rows, err := p.loadRows(ctx, true)
	if err != nil {
		return PasskeyInfo{}, err
	}
	if len(rows) >= MaxPasskeys {
		return PasskeyInfo{}, ErrPasskeyLimit
	}
	creds := make([]webauthn.Credential, 0, len(rows))
	for _, r := range rows {
		creds = append(creds, r.cred)
	}
	wa, err := p.webauthnFor(env)
	if err != nil {
		return PasskeyInfo{}, err
	}
	cred, err := wa.CreateCredential(passkeyUser{creds: creds}, sess.data, parsed)
	if err != nil {
		return PasskeyInfo{}, fmt.Errorf("%w : %s", ErrPasskeyInvalid, safeWAErr(err))
	}
	if !cred.Flags.UserVerified {
		return PasskeyInfo{}, fmt.Errorf("%w : l'authentificateur n'a pas vérifié l'utilisateur", ErrPasskeyInvalid)
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = "Passkey"
	}
	if len(label) > 60 {
		label = label[:60]
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		return PasskeyInfo{}, err
	}
	idb := make([]byte, 6)
	_, _ = rand.Read(idb)
	id := "pk_" + hex.EncodeToString(idb)
	now := p.clock()
	credID := base64.RawURLEncoding.EncodeToString(cred.ID)
	err = p.st.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, `INSERT INTO passkeys(id,credential_id,label,public_key,sign_count,credential,rp_id,created_at,created_by) VALUES(?,?,?,?,?,?,?,?,?)`,
			id, credID, label, cred.PublicKey, int64(cred.Authenticator.SignCount), string(raw), env.RPID, store.FormatTime(now), actor); e != nil {
			return e
		}
		return p.st.AuditTx(ctx, tx, actor, "human.passkey_registered", "passkey", id, map[string]any{"label": label, "rp_id": env.RPID, "synced": cred.Flags.BackupState})
	})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return PasskeyInfo{}, errors.New("cette passkey est déjà enregistrée")
		}
		return PasskeyInfo{}, err
	}
	return PasskeyInfo{ID: id, Label: label, CreatedAt: now, RPID: env.RPID, SignCount: cred.Authenticator.SignCount, Synced: cred.Flags.BackupState}, nil
}

// ---- approbation ----

// BeginApproval prépare l'approbation d'une confirmation ou d'une demande d'arrêt : renvoie les options (JSON) que le
// script passe à navigator.credentials.get. Le challenge, aléatoire, est gardé côté serveur avec l'objet approuvé,
// son empreinte de liaison et le navigateur qui a ouvert la page. ErrPasskeyNone s'il n'y a aucune passkey.
func (p *Passkeys) BeginApproval(ctx context.Context, r *http.Request, subj ApprovalSubject) ([]byte, error) {
	rows, err := p.loadRows(ctx, true)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrPasskeyNone
	}
	env, err := p.Env(r)
	if err != nil {
		return nil, err
	}
	// Seules les passkeys liées à ce RP ID sont proposées (les autres ne pourraient pas signer ici).
	creds := make([]webauthn.Credential, 0, len(rows))
	for _, row := range rows {
		if row.info.RPID == env.RPID {
			creds = append(creds, row.cred)
		}
	}
	if len(creds) == 0 {
		return nil, fmt.Errorf("%w : aucune passkey liée à %s", ErrPasskeyNone, env.RPID)
	}
	wa, err := p.webauthnFor(env)
	if err != nil {
		return nil, err
	}
	chal, err := newPasskeyChallenge()
	if err != nil {
		return nil, err
	}
	assertion, data, err := wa.BeginLogin(passkeyUser{creds: creds}, webauthn.WithChallenge(chal),
		webauthn.WithLoginOrigin(env.Origin), webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, err
	}
	p.putSession(&passkeySession{purpose: "approve", subject: subj.key(), binding: subj.Binding, browser: BrowserKey(r), env: env, data: *data,
		expires: p.clock().Add(PasskeyChallengeTTL)})
	return json.Marshal(assertion)
}

// FinishApproval vérifie l'assertion signée par la passkey pour CETTE approbation. En cas de succès, renvoie la
// passkey utilisée ; sinon une erreur (aucun effet sur l'imprimante : l'appelant n'approuve que sur succès).
//
// Vérifié : le challenge existe, n'a pas expiré, n'a pas déjà servi, a été émis pour le même objet, la même empreinte
// de liaison et le même navigateur ; l'origine et le RP ID sont ceux de la cérémonie ; signature, présence ET
// vérification de l'utilisateur ; le compteur progresse (comparé-et-échangé dans la base : deux assertions
// identiques en parallèle ne passent pas toutes les deux).
func (p *Passkeys) FinishApproval(ctx context.Context, r *http.Request, subj ApprovalSubject, assertionJSON []byte) (PasskeyInfo, error) {
	fail := func(reason string, err error) (PasskeyInfo, error) {
		_ = p.st.Audit(ctx, "human:passkey", "print.passkey_refused", "passkey", "", map[string]any{"subject": subj.key(), "reason": reason})
		return PasskeyInfo{}, err
	}
	if len(assertionJSON) == 0 {
		return fail("absent", ErrPasskeyChallenge)
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(assertionJSON)
	if err != nil {
		return fail("illisible", fmt.Errorf("%w (réponse illisible)", ErrPasskeyInvalid))
	}
	sess := p.takeSession(parsed.Response.CollectedClientData.Challenge)
	if sess == nil {
		return fail("challenge inconnu, périmé ou déjà utilisé", ErrPasskeyChallenge)
	}
	if sess.purpose != "approve" || sess.subject != subj.key() || sess.binding != subj.Binding || sess.browser != BrowserKey(r) {
		return fail("challenge d'une autre demande", ErrPasskeyChallenge)
	}
	env, err := p.Env(r)
	if err != nil {
		return fail("environnement", err)
	}
	if env != sess.env {
		return fail("origine ou RP ID différents de la cérémonie", ErrPasskeyChallenge)
	}
	rows, err := p.loadRows(ctx, true)
	if err != nil {
		return PasskeyInfo{}, err
	}
	var row *passkeyRow
	creds := make([]webauthn.Credential, 0, len(rows))
	for i := range rows {
		if rows[i].info.RPID != env.RPID {
			continue
		}
		creds = append(creds, rows[i].cred)
		if bytes.Equal(rows[i].cred.ID, parsed.RawID) {
			row = &rows[i]
		}
	}
	if row == nil {
		return fail("credential inconnu ou révoqué", ErrPasskeyInvalid)
	}
	wa, err := p.webauthnFor(env)
	if err != nil {
		return PasskeyInfo{}, err
	}
	// Cérémonie liée à ce credential seulement : la liste autorisée de la session est celle de BeginApproval.
	cred, err := wa.ValidateLogin(passkeyUser{creds: creds}, sess.data, parsed)
	if err != nil {
		return fail("vérification : "+safeWAErr(err), fmt.Errorf("%w : %s", ErrPasskeyInvalid, safeWAErr(err)))
	}
	if !parsed.Response.AuthenticatorData.Flags.UserVerified() || !parsed.Response.AuthenticatorData.Flags.UserPresent() {
		return fail("présence ou vérification de l'utilisateur absente", ErrPasskeyInvalid)
	}
	old, count := row.info.SignCount, parsed.Response.AuthenticatorData.Counter
	if (count != 0 || old != 0) && count <= old {
		return fail("compteur régressif", ErrPasskeyCounter)
	}
	cred.Authenticator.SignCount = count
	raw, err := json.Marshal(cred)
	if err != nil {
		return PasskeyInfo{}, err
	}
	now := p.clock()
	var swapped bool
	err = p.st.Tx(ctx, func(tx *sql.Tx) error {
		res, e := tx.ExecContext(ctx, `UPDATE passkeys SET sign_count=?, last_used_at=?, credential=? WHERE id=? AND sign_count=? AND revoked_at IS NULL`,
			int64(count), store.FormatTime(now), string(raw), row.info.ID, int64(old))
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		swapped = n == 1
		return nil
	})
	if err != nil {
		return PasskeyInfo{}, err
	}
	if !swapped {
		return fail("compteur modifié entre-temps (rejeu ou course)", ErrPasskeyCounter)
	}
	info := row.info
	info.SignCount, info.LastUsedAt = count, now
	_ = p.st.Audit(ctx, "human:passkey", "print.passkey_accepted", "passkey", info.ID, map[string]any{"subject": subj.key(), "sign_count": count})
	return info, nil
}

// safeWAErr résume une erreur de la bibliothèque sans reprendre de donnée du client.
func safeWAErr(err error) string {
	var pe *protocol.Error
	if errors.As(err, &pe) {
		return strings.TrimSpace(pe.Type + " " + pe.Details)
	}
	return "échec de vérification"
}
