package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/export"
)

// registerExport : export de projet (équivalent des outils export_project et get_project_export).
//
//	POST /api/exports                  lance l'export (corps : {collection_id | model_ids, options}) -> 202 {export_id, job_id, plan}
//	GET  /api/exports                  exports récents
//	GET  /api/exports/{id}             état de l'export et de son travail
//	POST /api/exports/{id}/link        lien de téléchargement signé à durée limitée (export prêt)
//	GET  /api/exports/{id}.zip?exp&sig téléchargement (URL signée : lisible sans jeton, vérifiée ici)
//
// Rien n'est publié : l'archive est un fichier à télécharger.
func registerExport(mux *http.ServeMux, c *core.Core) {
	svc := c.Export
	if svc == nil {
		return
	}
	mux.HandleFunc("POST /api/exports", func(w http.ResponseWriter, r *http.Request) {
		var in export.Input
		if !decode(w, r, &in) {
			return
		}
		st, err := svc.Start(r.Context(), in)
		reply(c, w, st, err, http.StatusAccepted)
	})
	mux.HandleFunc("GET /api/exports", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		recs, err := svc.List(r.Context(), limit)
		reply(c, w, map[string]any{"exports": recs}, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/exports/{file}", func(w http.ResponseWriter, r *http.Request) {
		file := r.PathValue("file")
		if id, ok := strings.CutSuffix(file, ".zip"); ok {
			serveExport(c, w, r, id)
			return
		}
		rec, err := svc.Get(r.Context(), file)
		if err != nil {
			reply(c, w, nil, err, 0)
			return
		}
		out := map[string]any{"export": rec}
		if rec.JobID != "" {
			if j, err := c.Queue.Get(r.Context(), rec.JobID); err == nil {
				out["job"] = j.Redacted()
			}
		}
		if rec.State == export.StateReady {
			out["publish_steps"] = export.PublishSteps
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /api/exports/{id}/link", func(w http.ResponseWriter, r *http.Request) {
		l, err := svc.Link(r.Context(), r.PathValue("id"))
		reply(c, w, l, err, http.StatusOK)
	})
}

// exportIDRe : forme exacte d'un identifiant d'export (contracts.NewID("exp") : « exp_ » + 24 hexadécimaux).
var exportIDRe = regexp.MustCompile(`^exp_[0-9a-f]{24}$`)

// isPublicExportDownload : GET|HEAD /api/exports/exp_<24 hex>.zip?exp=…&sig=…, le SEUL chemin d'export ouvert sur
// la surface publique restreinte (FB13). La signature (HMAC à portée « export », expiration) est vérifiée par
// serveExport ; sans elle, ou avec une forme inattendue, la liste blanche répond 404.
func isPublicExportDownload(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	p, ok := strings.CutPrefix(r.URL.Path, "/api/exports/")
	if !ok {
		return false
	}
	id, ok := strings.CutSuffix(p, ".zip")
	if !ok || !exportIDRe.MatchString(id) {
		return false
	}
	q := r.URL.Query()
	return q.Get("exp") != "" && q.Get("sig") != ""
}

// registerPublicExport : téléchargement d'une archive par URL signée sur l'écouteur public. Aucune autre route
// d'export (liste, état, création de lien) n'y existe.
func registerPublicExport(mux *http.ServeMux, c *core.Core) {
	if c.Export == nil {
		return
	}
	mux.HandleFunc("GET /api/exports/{file}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := strings.CutSuffix(r.PathValue("file"), ".zip")
		if !ok {
			publicNotFound(w, r)
			return
		}
		serveExport(c, w, r, id)
	})
}

// serveExport sert l'archive d'un export si l'URL est correctement signée et non expirée.
func serveExport(c *core.Core, w http.ResponseWriter, r *http.Request, id string) {
	q := r.URL.Query()
	if !c.Export.VerifyLink(id, q.Get("exp"), q.Get("sig")) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "lien de téléchargement invalide ou expiré : redemander un lien (get_project_export ou bouton « Exporter le projet »)", "code": "link_expired"})
		return
	}
	f, rec, err := c.Export.Open(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, catalog.ErrNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error(), "code": "not_found"})
		case errors.Is(err, catalog.ErrInvalid):
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "code": "not_ready"})
		default:
			c.Log.Error("ouverture de l'export", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "erreur interne"})
		}
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "erreur interne"})
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", contentDisposition(rec.FileName))
	h.Set("ETag", `"`+rec.SHA256+`"`)
	h.Set("Cache-Control", "private, no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, rec.FileName, st.ModTime(), f)
}

// contentDisposition : pièce jointe, nom ASCII de repli + nom UTF-8 (RFC 6266).
func contentDisposition(name string) string {
	var ascii strings.Builder
	for _, r := range name {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' || r == '%' {
			ascii.WriteByte('_')
		} else {
			ascii.WriteRune(r)
		}
	}
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, ascii.String(), url.PathEscape(name))
}
