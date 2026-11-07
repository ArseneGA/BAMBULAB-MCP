package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/calibration"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/library"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// registerLibrary ajoute les routes de la bibliothèque de gabarits et de la calibration (équivalentes aux outils MCP) :
//
//	GET  /api/library/templates?category=            list_part_templates
//	GET  /api/library/templates/{id}                 describe_part_template
//	GET  /api/library/templates/{id}/thumbnail.svg   vignette du gabarit (paramètres par défaut)
//	POST /api/library/create                         create_from_template
//	POST /api/library/variants                       save_as_template
//	GET  /api/library/models/{id}/thumbnail.svg      vignette d'un modèle généré
//	POST /api/calibration/sets                       generate_calibration_set
//	POST /api/calibration/records                    record_calibration
//	GET  /api/calibration?printer=&nozzle=&material=&filament=   get_calibration (+ historique)
func registerLibrary(mux *http.ServeMux, c *core.Core) {
	lib, cal := c.Library, c.Calibration
	if lib == nil || cal == nil {
		return
	}
	mux.HandleFunc("GET /api/library/templates", func(w http.ResponseWriter, r *http.Request) {
		o, err := lib.List(r.Context(), r.URL.Query().Get("category"))
		libReply(c, w, o, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/library/templates/{id}", func(w http.ResponseWriter, r *http.Request) {
		o, err := lib.Describe(r.Context(), r.PathValue("id"))
		libReply(c, w, o, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/library/templates/{id}/thumbnail.svg", func(w http.ResponseWriter, r *http.Request) {
		b, err := lib.Thumbnail(r.Context(), r.PathValue("id"), nil)
		svgReply(c, w, b, err)
	})
	mux.HandleFunc("GET /api/library/models/{id}/thumbnail.svg", func(w http.ResponseWriter, r *http.Request) {
		b, err := lib.ModelThumbnail(r.Context(), r.PathValue("id"))
		svgReply(c, w, b, err)
	})
	mux.HandleFunc("POST /api/library/create", func(w http.ResponseWriter, r *http.Request) {
		var in library.CreateInput
		if !decode(w, r, &in) {
			return
		}
		o, err := lib.Create(r.Context(), in)
		libReply(c, w, o, err, http.StatusCreated)
	})
	mux.HandleFunc("POST /api/library/variants", func(w http.ResponseWriter, r *http.Request) {
		var in library.SaveInput
		if !decode(w, r, &in) {
			return
		}
		o, err := lib.SaveAs(r.Context(), in)
		libReply(c, w, o, err, http.StatusCreated)
	})
	mux.HandleFunc("POST /api/calibration/sets", func(w http.ResponseWriter, r *http.Request) {
		var in calibration.GenerateInput
		if !decode(w, r, &in) {
			return
		}
		o, err := cal.Generate(r.Context(), in)
		libReply(c, w, o, err, http.StatusCreated)
	})
	mux.HandleFunc("POST /api/calibration/records", func(w http.ResponseWriter, r *http.Request) {
		var in calibration.RecordInput
		if !decode(w, r, &in) {
			return
		}
		o, err := cal.Record(r.Context(), in)
		libReply(c, w, o, err, http.StatusCreated)
	})
	mux.HandleFunc("GET /api/calibration", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		nozzle, _ := strconv.ParseFloat(q.Get("nozzle"), 64)
		key := calibration.Key{Printer: q.Get("printer"), NozzleMM: nozzle, Material: q.Get("material"), Filament: q.Get("filament")}
		res, err := cal.Resolve(r.Context(), key)
		if err != nil {
			libReply(c, w, nil, err, 0)
			return
		}
		hist, err := cal.History(r.Context(), q.Get("material"), nozzle, atoi(q.Get("limit")))
		libReply(c, w, map[string]any{"resolution": res, "history": hist}, err, http.StatusOK)
	})
}

func libStatus(err error) (int, string) {
	switch {
	case errors.Is(err, library.ErrInvalid), errors.Is(err, calibration.ErrInvalid):
		return http.StatusBadRequest, "invalid"
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, library.ErrConflict):
		return http.StatusConflict, "conflict"
	case errors.Is(err, library.ErrUnavailable), errors.Is(err, calibration.ErrUnavailable):
		return http.StatusServiceUnavailable, "cad_unavailable"
	}
	return http.StatusInternalServerError, "internal"
}

func libReply(c *core.Core, w http.ResponseWriter, v any, err error, ok int) {
	if err == nil {
		writeJSON(w, ok, v)
		return
	}
	status, code := libStatus(err)
	msg := err.Error()
	if status == http.StatusInternalServerError {
		c.Log.Error("erreur API bibliothèque", "err", err)
		msg = "erreur interne"
	}
	writeJSON(w, status, map[string]any{"error": msg, "code": code})
}

func svgReply(c *core.Core, w http.ResponseWriter, b []byte, err error) {
	if err != nil {
		libReply(c, w, nil, err, 0)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}
