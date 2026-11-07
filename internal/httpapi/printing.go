package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// registerPrinting monte la chaîne d'impression (équivalent des outils MCP) et les deux pages HTML
// du canal humain. Aucune route ne renvoie un code d'accès ni un jeton de confirmation, et AUCUNE
// route JSON n'approuve un lancement : l'approbation passe par le formulaire /confirm/{id}.
//
//	GET  /api/printer-models                    GET  /api/printers            POST /api/printers
//	GET  /api/printers/{id}                     GET  /api/printers/{id}/status
//	GET  /api/print-jobs?state=&limit=          POST /api/print-jobs          (execute_headless_slice)
//	GET  /api/print-jobs/{id}                   (get_print_status)
//	POST /api/print-jobs/validate               POST /api/print-jobs/upload
//	POST /api/print-jobs/{id}/confirmation      (request_print_confirmation)
//	POST /api/print-jobs/start                  POST /api/print-jobs/{id}/cancel   (PRINTING : crée une demande d'arrêt /stop/{id}, rien n'est arrêté)
//	POST /api/print-jobs/{id}/reconcile
//	GET  /api/confirmations/{id}                POST /api/previews     GET /api/previews/{sha256}
//	POST /api/models/parametric                 POST /api/models/modify-geometry
//	GET  /setup/printer/{id}  POST ...          GET  /confirm/{id}  POST ...   GET /stop/{id}  POST ...   (pages HTML, cookie de navigateur enregistré)
func registerPrinting(mux *http.ServeMux, c *core.Core, human *humanauth.Service) {
	p := c.Printing
	if p == nil {
		return
	}
	p.RegisterPages(mux, human.Require) // pages HTML : navigateur enregistré (cookie) obligatoire
	mux.HandleFunc("GET /api/printer-models", func(w http.ResponseWriter, r *http.Request) {
		ms, err := p.ListPrinterModels(r.Context())
		printingReply(c, w, map[string]any{"models": ms}, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/printers", func(w http.ResponseWriter, r *http.Request) {
		ps, err := p.ListPrinters(r.Context())
		printingReply(c, w, map[string]any{"printers": ps}, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/printers", func(w http.ResponseWriter, r *http.Request) {
		var in printing.AddPrinterInput
		if !decode(w, r, &in) {
			return
		}
		v, err := p.AddPrinter(r.Context(), in)
		printingReply(c, w, v, err, http.StatusCreated)
	})
	mux.HandleFunc("GET /api/printers/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := p.GetPrinter(r.Context(), r.PathValue("id"))
		printingReply(c, w, v, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/printers/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		v, err := p.GetPrinterStatus(r.Context(), r.PathValue("id"))
		printingReply(c, w, v, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/print-jobs", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		q := r.URL.Query()
		if q.Get("state") != "" { // ancien filtre exact par état
			js, err := p.ListJobs(r.Context(), contracts.PrintState(q.Get("state")), limit)
			printingReply(c, w, map[string]any{"print_jobs": js}, err, http.StatusOK)
			return
		}
		h, err := p.ListHistory(r.Context(), printing.HistoryFilter{View: q.Get("view"), ModelID: q.Get("model_id"), Limit: limit}, q.Get("group") == "model")
		if err != nil {
			printingReply(c, w, nil, err, http.StatusOK)
			return
		}
		printingReply(c, w, map[string]any{"print_jobs": h.Jobs, "view": h.View, "counts": h.Counts, "groups": h.Groups, "progress": h.Progress}, nil, http.StatusOK)
	})
	mux.HandleFunc("POST /api/print-jobs/history", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Action      string   `json:"action"`
			PrintJobIDs []string `json:"print_job_ids"`
		}
		if !decode(w, r, &in) {
			return
		}
		res, err := p.ArchiveJobs(r.Context(), in.Action, in.PrintJobIDs)
		printingReply(c, w, res, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/print-jobs", func(w http.ResponseWriter, r *http.Request) {
		var in printing.SliceInput
		if !decode(w, r, &in) {
			return
		}
		j, err := p.ExecuteHeadlessSlice(r.Context(), in)
		printingReply(c, w, j, err, http.StatusAccepted)
	})
	mux.HandleFunc("GET /api/print-jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		st, err := p.GetPrintStatus(r.Context(), r.PathValue("id"))
		printingReply(c, w, st, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/print-jobs/validate", func(w http.ResponseWriter, r *http.Request) {
		var in printing.ValidateInput
		if !decode(w, r, &in) {
			return
		}
		o, err := p.ValidateArtifact(r.Context(), in)
		printingReply(c, w, o, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/print-jobs/upload", func(w http.ResponseWriter, r *http.Request) {
		var in printing.UploadInput
		if !decode(w, r, &in) {
			return
		}
		o, err := p.UploadArtifact(r.Context(), in)
		printingReply(c, w, o, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/print-jobs/{id}/confirmation", func(w http.ResponseWriter, r *http.Request) {
		v, err := p.RequestConfirmation(r.Context(), printing.RequestConfirmationInput{PrintJobID: r.PathValue("id")})
		printingReply(c, w, v, err, http.StatusCreated)
	})
	mux.HandleFunc("POST /api/print-jobs/start", func(w http.ResponseWriter, r *http.Request) {
		var in printing.StartInput
		if !decode(w, r, &in) {
			return
		}
		o, err := p.StartPrint(r.Context(), in)
		printingReply(c, w, o, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/print-jobs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		o, err := p.CancelPrint(r.Context(), printing.CancelInput{PrintJobID: r.PathValue("id")})
		printingReply(c, w, o, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/print-jobs/{id}/reconcile", func(w http.ResponseWriter, r *http.Request) {
		o, err := p.Reconcile(r.Context(), r.PathValue("id"))
		printingReply(c, w, o, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/confirmations/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := p.GetConfirmation(r.Context(), r.PathValue("id"))
		printingReply(c, w, v, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/previews", func(w http.ResponseWriter, r *http.Request) {
		var in printing.PreviewInput
		if !decode(w, r, &in) {
			return
		}
		o, err := p.CapturePreview(r.Context(), in)
		printingReply(c, w, o, err, http.StatusOK) // l'image se récupère par GET /api/previews/{png_sha256}
	})
	mux.HandleFunc("GET /api/previews/{sha}", func(w http.ResponseWriter, r *http.Request) {
		// Seuls les PNG enregistrés comme aperçus sont servis (jamais un blob quelconque du stockage).
		if !p.IsPreview(r.Context(), r.PathValue("sha")) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "aperçu introuvable", "code": "not_found"})
			return
		}
		f, err := c.Blobs.Open(r.PathValue("sha"))
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "aperçu introuvable", "code": "not_found"})
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "private, max-age=3600")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		st, _ := f.Stat()
		http.ServeContent(w, r, "preview.png", st.ModTime(), f)
	})
	mux.HandleFunc("POST /api/models/parametric", func(w http.ResponseWriter, r *http.Request) {
		var in printing.CreateParametricInput
		if !decode(w, r, &in) {
			return
		}
		o, err := p.CreateParametricModel(r.Context(), in)
		printingReply(c, w, o, err, http.StatusCreated)
	})
	mux.HandleFunc("POST /api/models/modify-geometry", func(w http.ResponseWriter, r *http.Request) {
		var in printing.ModifyGeometryInput
		if !decode(w, r, &in) {
			return
		}
		o, err := p.ModifyGeometry(r.Context(), in)
		printingReply(c, w, o, err, http.StatusCreated)
	})
}

// printingReply traduit les erreurs de la chaîne d'impression en statuts HTTP.
func printingReply(c *core.Core, w http.ResponseWriter, v any, err error, ok int) {
	if err == nil {
		writeJSON(w, ok, v)
		return
	}
	status, code := http.StatusInternalServerError, "internal"
	switch {
	case errors.Is(err, printing.ErrInvalid):
		status, code = http.StatusBadRequest, "invalid"
	case errors.Is(err, printing.ErrNotFound), errors.Is(err, store.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, printing.ErrNeedsApproval), errors.Is(err, printing.ErrNotConfirmed):
		status, code = http.StatusForbidden, "needs_human_approval"
	case errors.Is(err, printing.ErrState), errors.Is(err, printing.ErrIncompatible), errors.Is(err, printing.ErrPrinterBusy), errors.Is(err, printer.ErrNotReady):
		status, code = http.StatusConflict, "conflict"
	case errors.Is(err, printing.ErrUnavailable):
		status, code = http.StatusServiceUnavailable, "unavailable"
	case errors.Is(err, contracts.ErrConfirmationExpired), errors.Is(err, contracts.ErrConfirmationUsed), errors.Is(err, contracts.ErrConfirmationMismatch):
		status, code = http.StatusGone, "confirmation_invalid"
	}
	body := map[string]any{"code": code}
	if status == http.StatusInternalServerError {
		c.Log.Error("erreur API impression", "err", err)
		body["error"] = "erreur interne"
	} else {
		body["error"] = err.Error()
	}
	writeJSON(w, status, body)
}
