package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/sheets"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// registerSheets ajoute les deux routes de lecture des fiches (G9, PJ5 et PJ10). Elles renvoient EXACTEMENT les mêmes
// structures que les outils MCP get_part_sheet et get_print_sheet. Lecture seule : aucune route ne lance, n'arrête ni ne
// reprend d'impression ; aucune URL signée ni secret dans la réponse (les images passent par /api/camera/photos/{sha256}).
//
//	GET /api/models/{id}/sheet    get_part_sheet  (include=versions,profiles,prints,strength,projects ; limit)
//	GET /api/prints/{id}/sheet    get_print_sheet
func registerSheets(mux *http.ServeMux, c *core.Core) {
	mux.HandleFunc("GET /api/models/{id}/sheet", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var inc []string
		if v := strings.TrimSpace(q.Get("include")); v != "" {
			inc = strings.Split(v, ",")
		}
		o, err := c.Sheets.PartSheet(r.Context(), sheets.PartSheetInput{ModelID: r.PathValue("id"), Include: inc, Limit: atoi(q.Get("limit"))})
		sheetsReply(c, w, o, err)
	})
	mux.HandleFunc("GET /api/prints/{id}/sheet", func(w http.ResponseWriter, r *http.Request) {
		o, err := c.Sheets.PrintSheet(r.Context(), sheets.PrintSheetInput{PrintJobID: r.PathValue("id")})
		sheetsReply(c, w, o, err)
	})
}

func sheetsReply(c *core.Core, w http.ResponseWriter, v any, err error) {
	if err == nil {
		writeJSON(w, http.StatusOK, v)
		return
	}
	status, code := http.StatusInternalServerError, "internal"
	body := map[string]any{}
	switch {
	case errors.Is(err, sheets.ErrInvalid):
		status, code = http.StatusBadRequest, "invalid"
	case errors.Is(err, store.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	}
	if status == http.StatusInternalServerError {
		c.Log.Error("erreur API fiches", "err", err)
		body["error"] = "erreur interne"
	} else {
		body["error"] = err.Error()
	}
	body["code"] = code
	writeJSON(w, status, body)
}
