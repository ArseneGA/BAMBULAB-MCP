package httpapi

import (
	"embed"
	"html/template"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/oauth"
)

// Surface publique restreinte (docs/OAUTH.md « Surface publique », spec G1 F1-F2).
//
// Second écouteur (--public-listen) destiné à être publié sur Internet (Tailscale Funnel, reverse proxy). Son routeur
// n'expose qu'une liste blanche EXACTE ; tout le reste répond 404 (jamais 401/403 : rien n'est révélé), y compris les
// pages humaines (/confirm, /stop, /action, /setup, /enroll, /agents), l'API (/api/*), les protocoles d'agent
// (/agent/v1, /agent/v2) et l'interface web. Le filtre passe AVANT le garde : un chemin hors liste n'atteint jamais
// l'authentification, le routeur ni la SPA.

//go:embed public/icon.svg public/about.html public/privacy.html
var publicFS embed.FS

var publicTpl = template.Must(template.ParseFS(publicFS, "public/about.html", "public/privacy.html"))

// publicAllowed : la liste blanche exacte (chemin ET méthode).
func publicAllowed(r *http.Request) bool {
	p, m := r.URL.Path, r.Method
	get := m == http.MethodGet || m == http.MethodHead
	switch p {
	case "/mcp":
		return m == http.MethodPost || m == http.MethodGet || m == http.MethodDelete
	case "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-authorization-server",
		"/oauth/authorize", "/health", "/about", "/privacy", "/icon.svg", "/robots.txt":
		return get
	case "/oauth/register", "/oauth/token":
		return m == http.MethodPost
	}
	if id, ok := strings.CutPrefix(p, "/oauth/consent/"); ok && validConsentID(id) {
		return get || m == http.MethodPost
	}
	// Envoi d'un fichier par lien à usage unique (create_upload_link) : le SEUL chemin d'envoi de la surface publique,
	// PUT ou POST, jeton de la forme exacte attendue (le service refuse inconnu, expiré, déjà utilisé).
	if isUploadLinkRequest(r) {
		return true
	}
	return false
}

func validConsentID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func publicNotFound(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "introuvable"})
}

// forPublic : les options de l'écouteur public. L'émetteur OAuth et la ressource /mcp sont ceux de l'URL publique
// (PublicListenURL, placée en premier de la liste pour que baseURL et requestHTTPS la retiennent) ; l'adresse privée
// (PublicURL) reste dans la liste, donc son nom d'hôte reste autorisé.
func (o Options) forPublic() Options {
	p := o
	p.extraBearer, p.challenge = nil, nil
	if o.PublicListenURL != "" {
		p.PublicURL = o.PublicListenURL
		p.PublicURLs = append([]string{o.PublicListenURL}, o.publicURLs()...)
	}
	return p
}

func buildPublic(c *core.Core, opts Options, human *humanauth.Service, svc *oauth.Service, server *mcp.Server, isSession func(*http.Request) bool) http.Handler {
	popts := opts.forPublic()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		// Version réduite : ni version, ni schéma, ni chemins, ni état des vues.
		if _, err := c.Store.SchemaVersion(r.Context()); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "degraded"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	registerOAuthRoutes(mux, svc, human, &popts, true) // renseigne popts.extraBearer / popts.challenge
	mux.Handle("/mcp", mcpHandler(c, server))
	registerPublicPages(mux, popts)
	registerUploadLink(mux, c) // PUT|POST /upload/{jeton} : envoi à usage unique, seule exception d'écriture de la liste blanche
	mux.HandleFunc("/", publicNotFound)

	lim := newPublicLimiter(popts)
	g := guard(popts, isSession, mux)
	limited := lim.wrap(popts, g)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !publicAllowed(r) {
			publicNotFound(w, r)
			return
		}
		limited.ServeHTTP(w, r)
	})
}

// publicConsentGate : sur l'écouteur public, /enroll n'existe pas. Un navigateur non enregistré reçoit donc une
// explication (403), pas la redirection de humanauth.Require vers /enroll (qui y répondrait 404).
func publicConsentGate(human *humanauth.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := human.SessionFromRequest(r); err != nil {
			oauthPage(w, http.StatusForbidden, "msg", map[string]any{"Title": "Navigateur non enregistré",
				"Msg": "Ce navigateur n'est pas enregistré pour cette adresse. Enregistre-le d'abord depuis ton réseau privé : ouvre la page /enroll de l'adresse PRIVÉE de l'atelier (même nom d'hôte, depuis le tailnet), saisis le code donné par « bambud enroll », puis relance la connexion depuis Claude."})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func publicHeaders(w http.ResponseWriter, ctype string) {
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=3600")
}

func registerPublicPages(mux *http.ServeMux, opts Options) {
	page := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			publicHeaders(w, "text/html; charset=utf-8")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
			w.Header().Set("Referrer-Policy", "no-referrer")
			_ = publicTpl.ExecuteTemplate(w, name, map[string]any{"Contact": strings.TrimSpace(opts.ContactEmail)})
		}
	}
	mux.HandleFunc("GET /about", page("about.html"))
	mux.HandleFunc("GET /privacy", page("privacy.html"))
	mux.HandleFunc("GET /icon.svg", func(w http.ResponseWriter, r *http.Request) {
		b, _ := publicFS.ReadFile("public/icon.svg")
		publicHeaders(w, "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, r *http.Request) {
		publicHeaders(w, "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("User-agent: *\nDisallow: /\n"))
	})
}
