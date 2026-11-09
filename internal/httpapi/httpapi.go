// Package httpapi expose l'API HTTP du service (net/http, routeur Go 1.22+).
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/oauth"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/webui"
)

// NewHandler assemble les routes : /health, /api/*, /mcp (MCP Streamable HTTP).
//
// Sécurité : voir guard.go (Host loopback ou liste blanche, protection
// cross-origin, jeton d'API obligatoire hors loopback). NewHandler applique les
// réglages par défaut (loopback uniquement, sans jeton).
func NewHandler(c *core.Core) http.Handler { return NewHandlerWithOptions(c, Options{}) }

// NewHandlerWithOptions est NewHandler avec des options de sécurité explicites.
func NewHandlerWithOptions(c *core.Core, opts Options) http.Handler {
	main, _ := buildHandlers(c, opts, false)
	return main
}

// Handlers : les deux routeurs du service. Main : l'écouteur principal (inchangé). Public : la surface publique
// restreinte (--public-listen, public.go).
type Handlers struct{ Main, Public http.Handler }

// NewHandlers construit l'écouteur principal et la surface publique restreinte (l'appelant ne branche cette
// dernière que si --public-listen est fourni). Les deux partagent le même service d'autorisation OAuth et les
// mêmes navigateurs enregistrés.
func NewHandlers(c *core.Core, opts Options) Handlers {
	m, p := buildHandlers(c, opts, true)
	return Handlers{Main: m, Public: p}
}

func buildHandlers(c *core.Core, opts Options, withPublic bool) (mainH, publicH http.Handler) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.SchemaVersion(r.Context())
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "degraded", "error": "base inaccessible"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": core.Version, "schema_version": v, "mcp_app_views": mcpserver.ViewsBuilt()})
	})
	// Occupation du service (mise à jour automatique : internal/upgrade). Lecture seule, réservée au serveur lui-même :
	// connexion TCP loopback et aucun en-tête de proxy (un `tailscale serve` ou un reverse proxy local en ajoute),
	// en plus du jeton d'API que le garde exige déjà. Ne fait jamais partie de /health (public).
	mux.HandleFunc("GET /api/system/busy", func(w http.ResponseWriter, r *http.Request) {
		if !localDirectRequest(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "réservé au serveur local (connexion directe en loopback)"})
			return
		}
		writeJSON(w, http.StatusOK, c.Busy(r.Context()))
	})
	mux.HandleFunc("GET /api/capabilities", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, c.Capabilities())
	})
	mux.HandleFunc("GET /api/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		j, err := c.Queue.Get(r.Context(), r.PathValue("id"))
		if errors.Is(err, jobs.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "travail introuvable"})
			return
		}
		if err != nil {
			c.Log.Error("lecture du travail", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "erreur interne"})
			return
		}
		writeJSON(w, http.StatusOK, j.Redacted())
	})
	mux.HandleFunc("POST /api/jobs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		oc, err := c.Queue.RequestCancel(r.Context(), id)
		if errors.Is(err, jobs.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "travail introuvable"})
			return
		}
		if err != nil {
			c.Log.Error("annulation", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "erreur interne"})
			return
		}
		j, _ := c.Queue.Get(r.Context(), id)
		writeJSON(w, http.StatusOK, map[string]any{"job_id": id, "outcome": oc, "state": j.State})
	})

	// Navigateurs « humains » enregistrés (docs/SECURITY.md) : pages /enroll et GET /api/session ; les pages
	// /confirm, /stop, /setup et /agents/pair exigent leur cookie.
	human := humanauth.New(c.Store, humanauth.Options{Secure: opts.requestHTTPS, ClientKey: opts.clientAddr})
	human.RegisterPages(mux)
	human.RegisterLaunchCodePages(mux) // /setup/launch-code : code de lancement personnel (launchcode_web.go)
	// Passkeys (WebAuthn) : RP ID et origines dérivés de BAMBU_PUBLIC_URL, https détecté comme pour le cookie ;
	// /setup/passkeys (navigateur enregistré) et son script statique (passkey_web.go). Facultatif : sans passkey
	// enregistrée, rien ne change.
	humanauth.PasskeysFor(c.Store).Configure(humanauth.PasskeyConfig{PublicURLs: opts.publicURLs(), Secure: opts.requestHTTPS})
	human.RegisterPasskeyPages(mux)

	registerCatalog(mux, c)
	registerUpload(mux, c) // envoi de fichiers, import par lien (upload.go)
	registerAgent(mux, c, human)
	registerGoals(mux, c)
	registerLibrary(mux, c)
	registerMaterials(mux, c)
	registerInsights(mux, c)        // coût, journal, tableau de bord, post-traitement (insights.go)
	registerPrinting(mux, c, human) // chaîne d'impression + pages /setup /confirm /stop (printing.go)
	registerCamera(mux, c, human)   // caméra, détection d'échec, notifications (camera.go)
	registerAMS(mux, c)             // bobines, AMS, multicolore, file d'attente (ams.go)
	registerPlates(mux, c)          // plateau multi-pièces (plates.go)
	registerExport(mux, c)          // export de projet : archive zip partageable (export.go)

	oauthSvc := oauth.New(c.Store, oauth.Options{ExtraRedirects: opts.OAuthRedirects, Log: c.Log})
	registerOAuthRoutes(mux, oauthSvc, human, &opts, false) // jetons OAuth sur /mcp, métadonnées et pages (oauth.go) ; renseigne opts pour la garde

	server := mcpserver.New(c)
	mux.Handle("/mcp", mcpHandler(c, server))
	// Interface web embarquée (repli SPA), derrière le garde de sécurité : en dernier, ne masque ni /api ni /mcp.
	mux.Handle("/", webui.Handler())
	isSession := func(r *http.Request) bool { _, err := human.SessionFromRequest(r); return err == nil }
	mainH = guard(opts, isSession, mux) // point de branchement du middleware de sécurité de l'écouteur principal
	if withPublic {
		publicH = buildPublic(c, opts, human, oauthSvc, server, isSession)
	}
	return mainH, publicH
}

// mcpHandler : le point d'entrée MCP Streamable HTTP, commun aux deux écouteurs.
func mcpHandler(c *core.Core, server *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Logger: c.Log,
		// Le SDK refuse tout Host non loopback quand la connexion arrive sur le loopback (proxy local :
		// Funnel, tailscale serve, nginx). Notre garde contrôle déjà Host (loopback ou liste blanche) en amont.
		DisableLocalhostProtection: true,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
