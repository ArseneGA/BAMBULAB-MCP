package httpapi

import (
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/insights"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// registerInsights ajoute les routes du coût, du journal, du tableau de bord et du post-traitement
// (équivalentes aux outils MCP du même nom ; aucune ne lance ni n'arrête d'impression) :
//
//	POST  /api/cost/estimate               estimate_cost
//	GET   /api/cost/settings               get_cost_settings
//	PUT   /api/cost/settings               set_cost_settings
//	GET   /api/print-log                   list_print_log
//	GET   /api/print-log/{id}              get_print_log
//	PATCH /api/print-log/{id}              update_print_log
//	POST  /api/print-log/{id}/photo        ajoute une photo (corps = image JPEG, PNG ou WebP)
//	GET   /api/print-log/{id}/photos/{sha} lit une photo de la fiche
//	POST  /api/print-log/adjustments       suggest_print_adjustments
//	GET   /api/usage/dashboard             get_usage_dashboard
//	GET   /api/usage/chart.png             graphe des heures machine
//	POST  /api/post-processing             post_processing_advice
func registerInsights(mux *http.ServeMux, c *core.Core) {
	svc := c.Insights
	mux.HandleFunc("POST /api/cost/estimate", func(w http.ResponseWriter, r *http.Request) {
		var in insights.EstimateInput
		if !decode(w, r, &in) {
			return
		}
		o, err := svc.EstimateCost(r.Context(), in)
		insightsReply(c, w, o, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/cost/settings", func(w http.ResponseWriter, r *http.Request) {
		st, err := svc.GetSettings(r.Context())
		insightsReply(c, w, st, err, http.StatusOK)
	})
	mux.HandleFunc("PUT /api/cost/settings", func(w http.ResponseWriter, r *http.Request) {
		var in insights.SettingsPatch
		if !decode(w, r, &in) {
			return
		}
		st, err := svc.SetSettings(r.Context(), in)
		insightsReply(c, w, st, err, http.StatusOK)
	})

	mux.HandleFunc("GET /api/print-log", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		o, err := svc.ListLogs(r.Context(), insights.ListQuery{PrinterID: q.Get("printer_id"), MaterialFamily: q.Get("material_family"),
			Outcome: q.Get("outcome"), ModelID: q.Get("model_id"), Limit: atoi(q.Get("limit")), Offset: atoi(q.Get("offset"))})
		insightsReply(c, w, o, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/print-log/adjustments", func(w http.ResponseWriter, r *http.Request) {
		var in insights.AdjustInput
		if !decode(w, r, &in) {
			return
		}
		o, err := svc.SuggestAdjustments(r.Context(), in)
		insightsReply(c, w, o, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/print-log/{id}", func(w http.ResponseWriter, r *http.Request) {
		e, err := svc.GetLog(r.Context(), r.PathValue("id"))
		insightsReply(c, w, e, err, http.StatusOK)
	})
	mux.HandleFunc("PATCH /api/print-log/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in insights.UpdateInput
		if !decode(w, r, &in) {
			return
		}
		if in.ID != "" && in.ID != r.PathValue("id") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id du corps différent de celui de l'URL", "code": "invalid"})
			return
		}
		in.ID = r.PathValue("id")
		e, err := svc.UpdateLog(r.Context(), in)
		insightsReply(c, w, e, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/print-log/{id}/photo", func(w http.ResponseWriter, r *http.Request) {
		body := http.MaxBytesReader(w, r.Body, insights.MaxPhotoBytes+1)
		e, sha, err := svc.AddPhoto(r.Context(), r.PathValue("id"), body)
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "photo trop volumineuse", "code": "invalid"})
				return
			}
			insightsReply(c, w, nil, err, 0)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"photo": sha, "entry": e})
	})
	mux.HandleFunc("GET /api/print-log/{id}/photos/{sha}", func(w http.ResponseWriter, r *http.Request) {
		f, ct, err := svc.OpenPhoto(r.Context(), r.PathValue("id"), r.PathValue("sha"))
		if err != nil {
			insightsReply(c, w, nil, err, 0)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", ct)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		w.Header().Set("Cache-Control", "private, max-age=3600")
		_, _ = io.Copy(w, f)
	})

	dash := func(q url.Values) insights.DashboardQuery {
		return insights.DashboardQuery{From: q.Get("from"), To: q.Get("to"), Period: q.Get("period"), PrinterID: q.Get("printer_id")}
	}
	mux.HandleFunc("GET /api/usage/dashboard", func(w http.ResponseWriter, r *http.Request) {
		d, err := svc.GetDashboard(r.Context(), dash(r.URL.Query()))
		insightsReply(c, w, d, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/usage/chart.png", func(w http.ResponseWriter, r *http.Request) {
		d, err := svc.GetDashboard(r.Context(), dash(r.URL.Query()))
		if err != nil {
			insightsReply(c, w, nil, err, 0)
			return
		}
		png, err := insights.RenderChart(d)
		if err != nil {
			insightsReply(c, w, nil, err, 0)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(png)
	})
	mux.HandleFunc("POST /api/post-processing", func(w http.ResponseWriter, r *http.Request) {
		var in insights.PostProcessInput
		if !decode(w, r, &in) {
			return
		}
		a, err := insights.PostProcessingAdvice(in)
		insightsReply(c, w, a, err, http.StatusOK)
	})
}

// insightsReply traduit les erreurs en statuts HTTP (une erreur interne n'expose pas son détail).
func insightsReply(c *core.Core, w http.ResponseWriter, v any, err error, ok int) {
	if err == nil {
		writeJSON(w, ok, v)
		return
	}
	status, code := http.StatusInternalServerError, "internal"
	body := map[string]any{}
	switch {
	case errors.Is(err, insights.ErrInvalid):
		status, code = http.StatusBadRequest, "invalid"
	case errors.Is(err, store.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	}
	if status == http.StatusInternalServerError {
		c.Log.Error("erreur API journal/coût", "err", err)
		body["error"] = "erreur interne"
	} else {
		body["error"] = err.Error()
	}
	body["code"] = code
	writeJSON(w, status, body)
}
