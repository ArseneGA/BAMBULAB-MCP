package httpapi

import (
	"errors"
	"net/http"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/filament"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// registerAMS monte l'API des bobines, de l'AMS, du multicolore et de la file d'attente (internal/filament).
// Aucune route ne lance, ne confirme ni n'arrête une impression : la file prépare ; le lancement reste la page
// /confirm d'un navigateur enregistré (docs/SECURITY.md).
//
//	GET  /api/spools?printer_id=&material=&low=1&archived=1      POST /api/spools        (update_spool : création)
//	GET  /api/spools/{id}      PATCH /api/spools/{id}  (update_spool)     GET /api/spools/{id}/price   (prix/kg pour le coût)
//	GET  /api/spools/usage?print_job_id=        consommation décomptée d'une impression
//	GET  /api/filament/price?printer_id=&slot=  prix/kg de la bobine chargée à un emplacement (lecture du calcul de coût)
//	GET  /api/printers/{id}/ams                 (get_ams_state)
//	POST /api/filament/check                    (check_filament_for_print)     POST /api/filament/plan-colors  (plan_colors)
//	GET/PUT /api/filament/settings              marge d'alerte de manque
//	GET  /api/print-queue?finished=1            POST /api/print-queue (queue_print)     POST /api/print-queue/reorder
//	DELETE /api/print-queue/{id}                POST /api/print-queue/{id}/confirmation  (nouveau lien /confirm pour un élément prêt ; n'approuve rien)
func registerAMS(mux *http.ServeMux, c *core.Core) {
	fs := c.Filament
	if fs == nil {
		return
	}
	mux.HandleFunc("GET /api/spools", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		sp, err := fs.ListSpools(r.Context(), filament.ListFilter{PrinterID: q.Get("printer_id"), Material: q.Get("material"),
			LowOnly: q.Get("low") == "1", IncludeArchived: q.Get("archived") == "1"})
		amsReply(c, w, map[string]any{"spools": sp, "alert_margin": fs.GetSettings(r.Context())}, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/spools", func(w http.ResponseWriter, r *http.Request) {
		var in filament.UpdateInput
		if !decode(w, r, &in) {
			return
		}
		in.SpoolID = "" // création
		sp, err := fs.UpdateSpool(r.Context(), in)
		amsReply(c, w, sp, err, http.StatusCreated)
	})
	mux.HandleFunc("GET /api/spools/usage", func(w http.ResponseWriter, r *http.Request) {
		u, err := fs.UsageOf(r.Context(), r.URL.Query().Get("print_job_id"))
		amsReply(c, w, map[string]any{"usage": u}, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/spools/{id}", func(w http.ResponseWriter, r *http.Request) {
		sp, err := fs.GetSpool(r.Context(), r.PathValue("id"))
		amsReply(c, w, sp, err, http.StatusOK)
	})
	mux.HandleFunc("PATCH /api/spools/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in filament.UpdateInput
		if !decode(w, r, &in) {
			return
		}
		in.SpoolID = r.PathValue("id")
		sp, err := fs.UpdateSpool(r.Context(), in)
		amsReply(c, w, sp, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/spools/{id}/price", func(w http.ResponseWriter, r *http.Request) {
		p, ok := fs.PriceForSpool(r.Context(), r.PathValue("id"))
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "bobine ou prix inconnu", "code": "not_found"})
			return
		}
		writeJSON(w, http.StatusOK, p)
	})
	mux.HandleFunc("GET /api/filament/price", func(w http.ResponseWriter, r *http.Request) {
		p, ok := fs.PriceForSlot(r.Context(), r.URL.Query().Get("printer_id"), r.URL.Query().Get("slot"))
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "aucune bobine au prix connu à cet emplacement", "code": "not_found"})
			return
		}
		writeJSON(w, http.StatusOK, p)
	})
	mux.HandleFunc("GET /api/printers/{id}/ams", func(w http.ResponseWriter, r *http.Request) {
		v, err := fs.GetAMS(r.Context(), r.PathValue("id"))
		amsReply(c, w, v, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/filament/check", func(w http.ResponseWriter, r *http.Request) {
		var in filament.CheckInput
		if !decode(w, r, &in) {
			return
		}
		o, err := fs.CheckFilament(r.Context(), in)
		amsReply(c, w, o, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/filament/plan-colors", func(w http.ResponseWriter, r *http.Request) {
		var in filament.PlanColorsInput
		if !decode(w, r, &in) {
			return
		}
		o, err := fs.PlanColors(r.Context(), in)
		amsReply(c, w, o, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/filament/settings", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, fs.GetSettings(r.Context()))
	})
	mux.HandleFunc("PUT /api/filament/settings", func(w http.ResponseWriter, r *http.Request) {
		var in filament.Settings
		if !decode(w, r, &in) {
			return
		}
		st, err := fs.SetSettings(r.Context(), in)
		amsReply(c, w, st, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/print-queue", func(w http.ResponseWriter, r *http.Request) {
		_ = fs.PrepareQueue(r.Context()) // rafraîchit l'état ; ne lance rien
		q, err := fs.ListQueue(r.Context(), r.URL.Query().Get("finished") == "1")
		amsReply(c, w, map[string]any{"queue": q}, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/print-queue", func(w http.ResponseWriter, r *http.Request) {
		var in filament.QueueInput
		if !decode(w, r, &in) {
			return
		}
		e, err := fs.QueuePrint(r.Context(), in)
		amsReply(c, w, e, err, http.StatusCreated)
	})
	mux.HandleFunc("POST /api/print-queue/reorder", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			QueueIDs []string `json:"queue_ids"`
		}
		if !decode(w, r, &in) {
			return
		}
		q, err := fs.ReorderQueue(r.Context(), in.QueueIDs)
		amsReply(c, w, map[string]any{"queue": q}, err, http.StatusOK)
	})
	mux.HandleFunc("DELETE /api/print-queue/{id}", func(w http.ResponseWriter, r *http.Request) {
		e, err := fs.RemoveFromQueue(r.Context(), r.PathValue("id"))
		amsReply(c, w, e, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/print-queue/{id}/confirmation", func(w http.ResponseWriter, r *http.Request) {
		e, err := fs.RenewConfirmation(r.Context(), r.PathValue("id"))
		amsReply(c, w, e, err, http.StatusOK)
	})
}

// amsReply traduit les erreurs du chantier bobines / file en statuts HTTP (celles de la chaîne d'impression restent
// traitées par printingReply).
func amsReply(c *core.Core, w http.ResponseWriter, v any, err error, ok int) {
	switch {
	case err == nil:
		writeJSON(w, ok, v)
	case errors.Is(err, filament.ErrInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error(), "code": "invalid"})
	case errors.Is(err, filament.ErrNotFound), errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error(), "code": "not_found"})
	case errors.Is(err, filament.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "code": "conflict"})
	default:
		printingReply(c, w, v, err, ok)
	}
}

var _ = printing.ErrState
