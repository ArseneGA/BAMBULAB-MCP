package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/goals"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// registerGoals ajoute les routes du moteur d'objectifs (web app), équivalentes aux outils MCP :
//
//	GET  /api/goal-criteria                catalogue des critères
//	POST /api/goals                        set_manufacturing_goals
//	GET  /api/goals/{id}                   objectif enregistré
//	POST /api/experiments                  create_experiment
//	GET  /api/experiments/{id}             état de l'expérience
//	GET  /api/experiments?model_id=        expériences passées d'un modèle
//	GET  /api/print-memory                 get_print_memory
//	GET  /api/profiles/filaments?machine=  presets filament compatibles
//	GET  /api/experiments/{id}/compare     compare_variants
//	POST /api/variants/apply               apply_variant
//	POST /api/profiles/modify              modify_slicing_profile
//	POST /api/print-results                record_print_result
func registerGoals(mux *http.ServeMux, c *core.Core) {
	svc := c.Goals
	mux.HandleFunc("GET /api/goal-criteria", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, svc.ListCriteria())
	})
	mux.HandleFunc("POST /api/goals", func(w http.ResponseWriter, r *http.Request) {
		var in goals.GoalInput
		if !decode(w, r, &in) {
			return
		}
		g, err := svc.SetGoals(r.Context(), in)
		goalsReply(c, w, g, err, http.StatusCreated)
	})
	mux.HandleFunc("GET /api/goals/{id}", func(w http.ResponseWriter, r *http.Request) {
		g, err := svc.GetGoal(r.Context(), r.PathValue("id"))
		goalsReply(c, w, g, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/experiments", func(w http.ResponseWriter, r *http.Request) {
		var in goals.CreateExperimentInput
		if !decode(w, r, &in) {
			return
		}
		x, err := svc.CreateExperiment(r.Context(), in)
		goalsReply(c, w, x, err, http.StatusCreated)
	})
	mux.HandleFunc("GET /api/experiments", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		list, err := svc.ListExperiments(r.Context(), q.Get("model_id"), atoi(q.Get("limit")))
		goalsReply(c, w, map[string]any{"experiments": list}, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/print-memory", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		req := goals.MemoryRequest{ExperimentID: q.Get("experiment_id"), MaterialFamily: q.Get("material_family"), Machine: q.Get("machine"),
			Filament: q.Get("filament"), ModelID: q.Get("model_id")}
		if s := q.Get("size_mm"); s != "" {
			for _, p := range strings.Split(s, ",") {
				f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
				if err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "size_mm : trois nombres séparés par des virgules", "code": "invalid"})
					return
				}
				req.SizeMM = append(req.SizeMM, f)
			}
			if len(req.SizeMM) != 3 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "size_mm : trois nombres séparés par des virgules", "code": "invalid"})
				return
			}
		}
		o, err := svc.MemoryFor(r.Context(), req, 20)
		goalsReply(c, w, o, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/profiles/filaments", func(w http.ResponseWriter, r *http.Request) {
		names, err := svc.FilamentPresets(r.URL.Query().Get("machine"))
		goalsReply(c, w, map[string]any{"filaments": names}, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/experiments/{id}", func(w http.ResponseWriter, r *http.Request) {
		x, err := svc.GetExperiment(r.Context(), r.PathValue("id"))
		goalsReply(c, w, x, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/experiments/{id}/compare", func(w http.ResponseWriter, r *http.Request) {
		o, err := svc.CompareVariants(r.Context(), r.PathValue("id"))
		goalsReply(c, w, o, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/variants/apply", func(w http.ResponseWriter, r *http.Request) {
		var in goals.ApplyInput
		if !decode(w, r, &in) {
			return
		}
		o, err := svc.ApplyVariant(r.Context(), in)
		goalsReply(c, w, o, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/profiles/modify", func(w http.ResponseWriter, r *http.Request) {
		var in goals.ModifyInput
		if !decode(w, r, &in) {
			return
		}
		o, err := svc.ModifyProfile(r.Context(), in)
		goalsReply(c, w, o, err, http.StatusCreated)
	})
	mux.HandleFunc("POST /api/print-results", func(w http.ResponseWriter, r *http.Request) {
		var in goals.ResultInput
		if !decode(w, r, &in) {
			return
		}
		o, err := svc.RecordResult(r.Context(), in)
		goalsReply(c, w, o, err, http.StatusCreated)
	})
}

// goalsReply traduit les erreurs du moteur en statuts HTTP. Une valeur nil
// avec erreur nil ne doit pas arriver ; les pointeurs nil sont écartés par err.
func goalsReply(c *core.Core, w http.ResponseWriter, v any, err error, ok int) {
	if err == nil {
		writeJSON(w, ok, v)
		return
	}
	status, code := http.StatusInternalServerError, "internal"
	body := map[string]any{}
	var se *goals.StaleError
	switch {
	case errors.As(err, &se):
		status, code = http.StatusConflict, "stale_revision"
		body["current"] = se.Current
	case errors.Is(err, goals.ErrInvalid):
		status, code = http.StatusBadRequest, "invalid"
	case errors.Is(err, store.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, goals.ErrIncompatible), errors.Is(err, goals.ErrNotReady):
		status, code = http.StatusConflict, "conflict"
	case errors.Is(err, goals.ErrUnavailable):
		status, code = http.StatusServiceUnavailable, "slicer_missing"
	}
	if status == http.StatusInternalServerError {
		c.Log.Error("erreur API objectifs", "err", err)
		body["error"] = "erreur interne"
	} else {
		body["error"] = err.Error()
	}
	body["code"] = code
	writeJSON(w, status, body)
}
