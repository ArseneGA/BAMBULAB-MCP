package httpapi

import (
	"net/http"
	"strconv"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// registerPlates : plateau multi-pièces (équivalent des outils plan_plate, get_plate_plan, execute_plate_slice).
// L'aperçu du plateau passe par POST /api/previews (mode « plate », plan_id). Aucune route ici ne dépose, n'approuve
// ni ne lance quoi que ce soit : chaque plaque devient une impression ordinaire (docs/SECURITY.md).
//
//	GET  /api/plate-plans                      (get_plate_plan sans id)
//	GET  /api/plate-plans/settings             clés de réglages par pièce
//	POST /api/plate-plans                      (plan_plate)
//	GET  /api/plate-plans/{id}                 (get_plate_plan)
//	POST /api/plate-plans/{id}/slice           (execute_plate_slice) corps : {"plates":[…],"idempotency_key":"…"}
func registerPlates(mux *http.ServeMux, c *core.Core) {
	p := c.Printing
	if p == nil {
		return
	}
	mux.HandleFunc("GET /api/plate-plans", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		ps, err := p.ListPlatePlans(r.Context(), limit)
		printingReply(c, w, map[string]any{"plans": ps}, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/plate-plans/settings", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"object_setting_keys": printing.ObjectSettingKeys})
	})
	mux.HandleFunc("POST /api/plate-plans", func(w http.ResponseWriter, r *http.Request) {
		var in printing.PlanPlateInput
		if !decode(w, r, &in) {
			return
		}
		pl, err := p.PlanPlate(r.Context(), in)
		printingReply(c, w, pl, err, http.StatusCreated)
	})
	mux.HandleFunc("GET /api/plate-plans/{id}", func(w http.ResponseWriter, r *http.Request) {
		pl, err := p.GetPlatePlan(r.Context(), r.PathValue("id"))
		printingReply(c, w, pl, err, http.StatusOK)
	})
	mux.HandleFunc("POST /api/plate-plans/{id}/slice", func(w http.ResponseWriter, r *http.Request) {
		var in printing.PlateSliceInput
		if r.ContentLength != 0 && !decode(w, r, &in) {
			return
		}
		in.PlanID = r.PathValue("id")
		o, err := p.ExecutePlateSlice(r.Context(), in)
		printingReply(c, w, o, err, http.StatusAccepted)
	})
}
