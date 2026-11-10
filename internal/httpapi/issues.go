package httpapi

import (
	"errors"
	"net/http"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/issues"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// registerIssues ajoute les routes du carnet de bord des dysfonctionnements (équivalentes aux outils MCP du même nom ;
// aucune ne lance, n'arrête ni ne reprend d'impression) :
//
//	GET   /api/issues               list_issues (state, severity, tool, limit, offset)
//	POST  /api/issues               report_issue
//	GET   /api/issues/export        export_issues (Markdown, text/markdown)
//	GET   /api/issues/{id}          un constat
//	PATCH /api/issues/{id}          update_issue (state, fix_commit, severity)
func registerIssues(mux *http.ServeMux, c *core.Core) {
	svc := c.Issues
	mux.HandleFunc("GET /api/issues", func(w http.ResponseWriter, r *http.Request) {
		o, err := svc.List(r.Context(), listQuery(r))
		issuesReply(c, w, o, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/issues", func(w http.ResponseWriter, r *http.Request) {
		var in issues.ReportInput
		if !decode(w, r, &in) {
			return
		}
		o, err := svc.Report(r.Context(), in)
		status := http.StatusOK
		if err == nil && o.Created {
			status = http.StatusCreated
		}
		issuesReply(c, w, o, err, status)
	})
	mux.HandleFunc("GET /api/issues/export", func(w http.ResponseWriter, r *http.Request) {
		md, _, err := svc.ExportMarkdown(r.Context(), listQuery(r))
		if err != nil {
			issuesReply(c, w, nil, err, 0)
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(md))
	})
	mux.HandleFunc("GET /api/issues/{id}", func(w http.ResponseWriter, r *http.Request) {
		i, err := svc.Get(r.Context(), r.PathValue("id"))
		issuesReply(c, w, i, err, http.StatusOK)
	})
	mux.HandleFunc("PATCH /api/issues/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in issues.UpdateInput
		if !decode(w, r, &in) {
			return
		}
		if in.ID != "" && in.ID != r.PathValue("id") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id du corps différent de celui de l'URL", "code": "invalid"})
			return
		}
		in.ID = r.PathValue("id")
		i, err := svc.Update(r.Context(), in)
		issuesReply(c, w, i, err, http.StatusOK)
	})
}

func listQuery(r *http.Request) issues.ListQuery {
	q := r.URL.Query()
	return issues.ListQuery{State: q.Get("state"), Severity: q.Get("severity"), Tool: q.Get("tool"), Limit: atoi(q.Get("limit")), Offset: atoi(q.Get("offset"))}
}

// issuesReply traduit les erreurs en statuts HTTP (une erreur interne n'expose pas son détail).
func issuesReply(c *core.Core, w http.ResponseWriter, v any, err error, ok int) {
	if err == nil {
		writeJSON(w, ok, v)
		return
	}
	status, code := http.StatusInternalServerError, "internal"
	body := map[string]any{}
	switch {
	case errors.Is(err, issues.ErrInvalid):
		status, code = http.StatusBadRequest, "invalid"
	case errors.Is(err, store.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	}
	if status == http.StatusInternalServerError {
		c.Log.Error("erreur API carnet des dysfonctionnements", "err", err)
		body["error"] = "erreur interne"
	} else {
		body["error"] = err.Error()
	}
	body["code"] = code
	writeJSON(w, status, body)
}
