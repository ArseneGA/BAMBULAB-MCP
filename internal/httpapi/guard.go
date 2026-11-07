package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// Options de sécurité du handler HTTP.
type Options struct {
	// AllowedHosts : noms d'hôte (sans port) acceptés en plus du loopback
	// (localhost, 127.0.0.0/8, ::1). Utile derrière un nom DNS interne.
	AllowedHosts []string
	// TrustedOrigins : origines (schéma://hôte[:port]) autorisées pour les requêtes
	// navigateur d'une autre origine. Vide = aucune.
	TrustedOrigins []string
	// APIToken : si non vide, l'API (/api/*) et /mcp exigent « Authorization: Bearer <jeton> » ; /api/* accepte
	// aussi la session d'un navigateur enregistré (cookie humanauth, voir apiAuth). /mcp reste Bearer seulement.
	// GET/HEAD /health, la SPA et ses fichiers statiques, les pages humaines et /agent/v1/* ont leur propre régime.
	// Obligatoire quand l'écoute n'est pas sur loopback ou que le service est exposé (voir ValidateServe).
	APIToken string
	// PublicURL : URL sous laquelle l'humain atteint le service (ex. https://bambu.lan:7878). Sert aux liens
	// affichés au modèle (/confirm, /stop, /setup), autorise automatiquement son nom d'hôte et décide de
	// l'attribut Secure du cookie de session (https). Vide : adresse d'écoute (jamais « [::] »).
	PublicURL string
	// PublicURLs : toutes les URL publiques (LAN en http, Funnel en https…). Leurs noms d'hôte sont autorisés ;
	// l'attribut Secure du cookie suit le schéma de celle dont l'hôte correspond au Host de la requête.
	// Si vide, PublicURL seule compte. PublicURL (la première) sert aux liens générés.
	PublicURLs []string
	// TrustedProxies : adresses/réseaux dont X-Forwarded-For et X-Forwarded-Proto sont crus. nil : loopback
	// seulement ; liste vide non nil : aucun. Voir ParseTrustedProxies.
	TrustedProxies []netip.Prefix
	// OAuthRedirects : URI de redirection exactes acceptées par le serveur OAuth intégré en plus des callbacks de
	// Claude et de la boucle locale (voir internal/oauth).
	OAuthRedirects []string

	// Renseignés par NewHandlerWithOptions (serveur OAuth intégré) : jeton d'accès OAuth valide sur /mcp, et
	// valeur de WWW-Authenticate du 401 de /mcp (pointeur resource_metadata, RFC 9728).
	extraBearer func(*http.Request) bool
	challenge   func(*http.Request) string
}

// isHumanPath : pages HTML du canal humain et leur état de session. Elles ne passent PAS par le jeton
// d'API (un navigateur ne sait pas l'envoyer) : elles exigent le cookie d'un navigateur enregistré
// (humanauth.Require). Tout le reste (API JSON, /mcp) exige le Bearer quand un jeton est configuré.
func isHumanPath(p string) bool {
	switch {
	case p == "/enroll" || strings.HasPrefix(p, "/enroll/"), p == "/api/session",
		strings.HasPrefix(p, "/confirm/"), strings.HasPrefix(p, "/stop/"), strings.HasPrefix(p, "/setup/"),
		p == "/agents/pair", strings.HasPrefix(p, "/oauth/consent/"):
		return true
	}
	return false
}

// isPublicStatic : la SPA (index.html, repli de routage client) et ses fichiers /assets/*. Ils ne contiennent
// aucune donnée de l'utilisateur ; toutes les données passent par /api/*, protégé. Lecture seule, hors API,
// /mcp, /health et /agent, et sans segment « . », « .. » ou « // » (que le routeur redirige de toute façon).
func isPublicStatic(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	p := r.URL.Path
	for _, pre := range []string{"/api", "/mcp", "/health", "/agent"} {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return false
		}
	}
	for _, seg := range strings.Split(p, "/")[1:] {
		if seg == "." || seg == ".." {
			return false
		}
	}
	return !strings.Contains(p, "//")
}

// isSignedAsset : GET /api/assets/<sha>.glb?exp=…&sig=… est une capacité signée (HMAC, expiration) que le
// handler vérifie lui-même ; elle doit rester lisible sans Bearer (iframe d'un hôte MCP Apps).
func isSignedAsset(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	q := r.URL.Query()
	if q.Get("sig") == "" || q.Get("exp") == "" {
		return false
	}
	// archives d'export de projet : même principe (HMAC à portée « export », expiration courte, vérifiés par le handler)
	if p, ok := strings.CutPrefix(r.URL.Path, "/api/exports/"); ok {
		return strings.HasSuffix(p, ".zip") && !strings.Contains(p, "/")
	}
	p, ok := strings.CutPrefix(r.URL.Path, "/api/assets/")
	return ok && strings.HasSuffix(p, ".glb") && !strings.Contains(p, "/")
}

// MinTokenLen : longueur minimale d'un jeton d'API.
const MinTokenLen = 16

// IsLoopbackAddr dit si une adresse d'écoute « hôte:port » est strictement
// locale. Un hôte vide ou une adresse non spécifiée (0.0.0.0, ::) ne l'est pas.
func IsLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	return isLoopbackHost(host)
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Exposure décrit comment le service est joignable au-delà de son adresse d'écoute.
type Exposure struct {
	PublicURLs   []string // --public-url
	AllowedHosts []string // --allowed-hosts
	// RequireToken force le jeton même sur loopback (--require-token) : à utiliser derrière un proxy.
	RequireToken bool
}

// TokenRequiredReason dit pourquoi un jeton d'API est obligatoire ("" si ce n'est pas le cas) : écoute hors
// loopback, --require-token, URL publique non loopback, ou nom d'hôte autorisé non loopback (proxy local).
func TokenRequiredReason(addr string, ex Exposure) string {
	if !IsLoopbackAddr(addr) {
		return "écoute hors loopback (" + addr + ")"
	}
	if ex.RequireToken {
		return "--require-token / BAMBU_REQUIRE_TOKEN"
	}
	for _, p := range ex.PublicURLs {
		if u, err := url.Parse(p); err == nil && u.Hostname() != "" && !isLoopbackHost(u.Hostname()) {
			return "URL publique non loopback (" + p + ")"
		}
	}
	for _, h := range ex.AllowedHosts {
		if h = strings.TrimSpace(h); h != "" && !isLoopbackHost(h) {
			return "hôte autorisé non loopback (" + h + ")"
		}
	}
	return ""
}

// ValidateServe refuse une configuration dangereuse : jeton trop court, ou jeton absent alors que
// TokenRequiredReason en exige un.
func ValidateServe(addr, token string, ex Exposure) error {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return fmt.Errorf("adresse d'écoute invalide %q : %w", addr, err)
	}
	if token != "" && len(token) < MinTokenLen {
		return fmt.Errorf("le jeton d'API doit faire au moins %d caractères", MinTokenLen)
	}
	if reason := TokenRequiredReason(addr, ex); reason != "" && token == "" {
		return errors.New("jeton d'API obligatoire (" + reason + ") : définir --api-token / BAMBU_API_TOKEN (derrière un proxy, toujours un jeton) ; sans exposition, écouter sur 127.0.0.1 sans --public-url ni --allowed-hosts publics")
	}
	return nil
}

// ValidateListen est ValidateServe sans information d'exposition (écoute seule).
func ValidateListen(addr, token string) error { return ValidateServe(addr, token, Exposure{}) }

// authFail écrit un refus JSON.
func authFail(w http.ResponseWriter, status int, code, msg string) {
	if status == http.StatusUnauthorized && w.Header().Get("WWW-Authenticate") == "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="bambud"`)
	}
	writeJSON(w, status, map[string]string{"error": msg, "code": code, "enroll_url": "/enroll"})
}

// csrfHeader est l'en-tête personnalisé que la SPA ajoute à chaque appel (un site tiers ne peut pas
// l'envoyer sans préliminaire CORS, que bambud ne donne jamais).
const csrfHeader = "X-Atelier-Request"

// browserWriteOK : une écriture authentifiée par cookie doit venir de la SPA elle-même : en-tête
// X-Atelier-Request: 1 ET même origine (Sec-Fetch-Site: same-origin ; à défaut d'en-têtes Sec-Fetch, que les
// navigateurs n'envoient pas en http hors localhost, un Origin identique au Host de la requête).
func browserWriteOK(r *http.Request) bool {
	if r.Header.Get(csrfHeader) != "1" {
		return false
	}
	if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" {
		return sfs == "same-origin"
	}
	o := r.Header.Get("Origin")
	if o == "" {
		return false
	}
	u, err := url.Parse(o)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
}

// guard enveloppe next : (1) en-tête Host loopback ou liste blanche, (2) protection
// cross-origin du navigateur (Sec-Fetch-Site / Origin), (3) authentification : jeton d'API, ou, pour
// /api/*, session d'un navigateur enregistré (isSession).
func guard(opts Options, isSession func(*http.Request) bool, next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, h := range opts.AllowedHosts {
		allowed[strings.Trim(strings.ToLower(strings.TrimSpace(h)), "[]")] = true
	}
	for _, p := range opts.publicURLs() {
		if u, err := url.Parse(p); err == nil && u.Hostname() != "" {
			allowed[strings.ToLower(u.Hostname())] = true
		}
	}
	cop := http.NewCrossOriginProtection()
	for _, o := range opts.TrustedOrigins {
		_ = cop.AddTrustedOrigin(o)
	}
	var tokenSum [32]byte
	if opts.APIToken != "" {
		tokenSum = sha256.Sum256([]byte(opts.APIToken))
	}
	bearerOK := func(r *http.Request) bool {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			return false
		}
		sum := sha256.Sum256([]byte(strings.TrimPrefix(h, "Bearer ")))
		return subtle.ConstantTimeCompare(sum[:], tokenSum[:]) == 1
	}
	inner := cop.Handler(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Protocole de l'agent Windows (distant, pas un navigateur) : il s'authentifie par son propre
		// secret Bearer (ou, pour /pair, par un jeton d'appairage à usage unique), vérifiés par agentlink.
		// Ni garde Host ni CSRF navigateur ni jeton d'API ici. /api/agents/pairing reste sous garde et loopback.
		if strings.HasPrefix(r.URL.Path, "/agent/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(strings.ToLower(host), "[]")
		if !isLoopbackHost(host) && !allowed[host] {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "hôte non autorisé"})
			return
		}
		if opts.APIToken != "" {
			p := r.URL.Path
			open := ((r.Method == http.MethodGet || r.Method == http.MethodHead) && p == "/health") ||
				isHumanPath(p) || isPublicStatic(r) || isOAuthOpen(r) || isSignedAsset(r)
			// /mcp accepte aussi un jeton d'accès OAuth émis par bambud (mêmes droits que le Bearer statique).
			if !open && !bearerOK(r) && !(p == "/mcp" && opts.extraBearer != nil && opts.extraBearer(r)) {
				// Session d'un navigateur enregistré : valable pour /api/* seulement (jamais /mcp).
				if strings.HasPrefix(p, "/api/") && isSession != nil && isSession(r) {
					if r.Method != http.MethodGet && r.Method != http.MethodHead && !browserWriteOK(r) {
						authFail(w, http.StatusForbidden, "csrf", "écriture refusée : l'interface web doit envoyer "+csrfHeader+": 1 depuis la même origine")
						return
					}
				} else {
					if p == "/mcp" && opts.challenge != nil {
						w.Header().Set("WWW-Authenticate", opts.challenge(r))
					}
					authFail(w, http.StatusUnauthorized, "unauthorized", "jeton d'API requis, ou navigateur enregistré (/enroll) pour l'interface web")
					return
				}
			}
		}
		inner.ServeHTTP(w, r)
	})
}
