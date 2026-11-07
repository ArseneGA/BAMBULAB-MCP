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
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.SchemaVersion(r.Context())
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "degraded", "error": "base inaccessible"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": core.Version, "schema_version": v, "mcp_app_views": mcpserver.ViewsBuilt()})
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

	registerCatalog(mux, c)
	registerAgent(mux, c, human)
	registerGoals(mux, c)
	registerMaterials(mux, c)
	registerPrinting(mux, c, human) // chaîne d'impression + pages /setup /confirm /stop (printing.go)

	registerOAuth(mux, c, human, &opts) // jetons OAuth sur /mcp, métadonnées et pages (oauth.go) ; renseigne opts pour la garde

	server := mcpserver.New(c)
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Logger: c.Log,
		// Le SDK refuse tout Host non loopback quand la connexion arrive sur le loopback (proxy local :
		// Funnel, tailscale serve, nginx). Notre garde contrôle déjà Host (loopback ou liste blanche) en amont.
		DisableLocalhostProtection: true,
	}))
	// Interface web embarquée (repli SPA), derrière le garde de sécurité : en dernier, ne masque ni /api ni /mcp.
	mux.Handle("/", webui.Handler())
	return guard(opts, func(r *http.Request) bool { _, err := human.SessionFromRequest(r); return err == nil }, mux) // unique point de branchement du middleware de sécurité
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
