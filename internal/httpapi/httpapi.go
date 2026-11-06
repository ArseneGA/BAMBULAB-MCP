// Package httpapi expose l'API HTTP du service (net/http, routeur Go 1.22+).
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

// NewHandler assemble les routes : /health, /api/*, /mcp (MCP Streamable HTTP).
//
// Sécurité (jalon 1) : aucune authentification ; bambud n'écoute par défaut que
// sur 127.0.0.1 et le handler MCP du SDK refuse les en-têtes Host non locaux
// (protection DNS rebinding). Ne pas exposer tel quel hors machine.
func NewHandler(c *core.Core) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.SchemaVersion(r.Context())
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "degraded", "error": "base inaccessible"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": core.Version, "schema_version": v})
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
		writeJSON(w, http.StatusOK, j)
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

	registerCatalog(mux, c)
	registerGoals(mux, c)

	server := mcpserver.New(c)
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Logger: c.Log,
	}))
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
