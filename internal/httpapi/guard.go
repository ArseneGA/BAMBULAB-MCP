package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
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
	// APIToken : si non vide, toute requête (sauf GET /health) doit porter
	// « Authorization: Bearer <jeton> ». Obligatoire quand l'écoute n'est pas
	// sur loopback (voir ValidateListen).
	APIToken string
	// PublicURL : URL sous laquelle l'humain atteint le service (ex. https://bambu.lan:7878). Sert aux liens
	// affichés au modèle (/confirm, /stop, /setup), autorise automatiquement son nom d'hôte et décide de
	// l'attribut Secure du cookie de session (https). Vide : adresse d'écoute (jamais « [::] »).
	PublicURL string
}

// isHumanPath : pages HTML du canal humain et leur état de session. Elles ne passent PAS par le jeton
// d'API (un navigateur ne sait pas l'envoyer) : elles exigent le cookie d'un navigateur enregistré
// (humanauth.Require). Tout le reste (API JSON, /mcp) exige le Bearer quand un jeton est configuré.
func isHumanPath(p string) bool {
	switch {
	case p == "/enroll" || strings.HasPrefix(p, "/enroll/"), p == "/api/session",
		strings.HasPrefix(p, "/confirm/"), strings.HasPrefix(p, "/stop/"), strings.HasPrefix(p, "/setup/"),
		p == "/agents/pair":
		return true
	}
	return false
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

// ValidateListen refuse une configuration dangereuse : écoute hors loopback sans
// jeton d'API (ou avec un jeton trop court).
func ValidateListen(addr, token string) error {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return fmt.Errorf("adresse d'écoute invalide %q : %w", addr, err)
	}
	if token != "" && len(token) < MinTokenLen {
		return fmt.Errorf("le jeton d'API doit faire au moins %d caractères", MinTokenLen)
	}
	if !IsLoopbackAddr(addr) && token == "" {
		return errors.New("écoute hors loopback (" + addr + ") refusée sans jeton d'API : définir --api-token / BAMBU_API_TOKEN, ou écouter sur 127.0.0.1")
	}
	return nil
}

// guard enveloppe next : (1) en-tête Host loopback ou liste blanche, (2) protection
// cross-origin du navigateur (Sec-Fetch-Site / Origin), (3) jeton d'API.
func guard(opts Options, next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, h := range opts.AllowedHosts {
		allowed[strings.Trim(strings.ToLower(strings.TrimSpace(h)), "[]")] = true
	}
	if u, err := url.Parse(opts.PublicURL); err == nil && u.Hostname() != "" {
		allowed[strings.ToLower(u.Hostname())] = true
	}
	cop := http.NewCrossOriginProtection()
	for _, o := range opts.TrustedOrigins {
		_ = cop.AddTrustedOrigin(o)
	}
	var tokenSum [32]byte
	if opts.APIToken != "" {
		tokenSum = sha256.Sum256([]byte(opts.APIToken))
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
		if opts.APIToken != "" && !(r.Method == http.MethodGet && r.URL.Path == "/health") && !isHumanPath(r.URL.Path) {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			sum := sha256.Sum256([]byte(got))
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare(sum[:], tokenSum[:]) != 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="bambud"`)
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "jeton d'API requis"})
				return
			}
		}
		inner.ServeHTTP(w, r)
	})
}
