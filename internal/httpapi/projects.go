package httpapi

import (
	"errors"
	"net/http"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/insights"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/projects"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// registerProjects ajoute les routes du suivi de projet (G9). Lecture seule, sauf le verdict d'une impression terminée.
// Aucune route ne lance, n'arrête ni ne reprend d'impression, ne crée ni n'approuve de confirmation ; le verdict n'a aucun
// effet sur l'imprimante, la file ou la confirmation.
//
//	GET  /api/projects                      list_projects (status, parent_id, include_archived, limit)
//	GET  /api/projects/{id}                 get_project
//	GET  /api/verdicts/pending              get_pending_verdicts (limit)
//	POST /api/prints/{id}/verdict           set_print_verdict, source human_web (verdict, reason, note)
func registerProjects(mux *http.ServeMux, c *core.Core) {
	mux.HandleFunc("GET /api/projects", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		o, err := c.Projects.List(r.Context(), projects.ListQuery{Status: q.Get("status"), ParentID: q.Get("parent_id"),
			IncludeArchived: q.Get("include_archived") == "true" || q.Get("include_archived") == "1", Limit: atoi(q.Get("limit"))})
		projectsReply(c, w, o, err)
	})
	mux.HandleFunc("GET /api/projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		d, err := c.Projects.Get(r.Context(), r.PathValue("id"))
		projectsReply(c, w, d, err)
	})
	mux.HandleFunc("GET /api/verdicts/pending", func(w http.ResponseWriter, r *http.Request) {
		o, err := c.Insights.PendingVerdicts(r.Context(), atoi(r.URL.Query().Get("limit")))
		projectsReply(c, w, o, err)
	})
	mux.HandleFunc("POST /api/prints/{id}/verdict", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Verdict string `json:"verdict"`
			Reason  string `json:"reason"`
			Note    string `json:"note"`
		}
		if !decode(w, r, &in) {
			return
		}
		o, err := c.Insights.SetVerdict(r.Context(), insights.SetVerdictInput{PrintJobID: r.PathValue("id"), Verdict: in.Verdict, Reason: in.Reason, Note: in.Note, Source: insights.SourceHumanWeb})
		projectsReply(c, w, o, err)
	})
	// Retire le verdict (bouton « Annuler » de l'Accueil, 8 s après un tap) : aucun effet sur l'imprimante ni sur la file.
	mux.HandleFunc("DELETE /api/prints/{id}/verdict", func(w http.ResponseWriter, r *http.Request) {
		o, err := c.Insights.ClearVerdict(r.Context(), r.PathValue("id"))
		projectsReply(c, w, o, err)
	})
}

// projectsReply traduit les erreurs en statuts HTTP (une erreur interne n'expose pas son détail).
func projectsReply(c *core.Core, w http.ResponseWriter, v any, err error) {
	if err == nil {
		writeJSON(w, http.StatusOK, v)
		return
	}
	status, code := http.StatusInternalServerError, "internal"
	body := map[string]any{}
	switch {
	case errors.Is(err, projects.ErrInvalid), errors.Is(err, insights.ErrInvalid):
		status, code = http.StatusBadRequest, "invalid"
	case errors.Is(err, store.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	}
	if status == http.StatusInternalServerError {
		c.Log.Error("erreur API suivi de projet", "err", err)
		body["error"] = "erreur interne"
	} else {
		body["error"] = err.Error()
	}
	body["code"] = code
	writeJSON(w, status, body)
}
