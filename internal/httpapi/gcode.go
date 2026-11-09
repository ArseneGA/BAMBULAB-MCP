package httpapi

import (
	"net/http"
	"strconv"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// registerGcode : trajectoires 3D du G-code découpé pour la visionneuse couche par couche de l'interface web (même
// composant que la vue MCP App ; mêmes données que les outils réservés get_gcode_layers / get_gcode_slice). Lecture seule,
// derrière le garde d'/api/* comme le reste.
//
//	GET /api/gcode/layers?print_job_id=|artifact_id=&plate=                         résumé par couche
//	GET /api/gcode/slice?print_job_id=|artifact_id=&plate=&from_layer=&with_travel=  tranche de couches (≤ 12 000 points)
func registerGcode(mux *http.ServeMux, c *core.Core) {
	p := c.Printing
	if p == nil {
		return
	}
	base := func(r *http.Request) (printing.GcodeInput, bool) {
		q := r.URL.Query()
		plate, _ := strconv.Atoi(q.Get("plate"))
		return printing.GcodeInput{PrintJobID: q.Get("print_job_id"), ArtifactID: q.Get("artifact_id"), Plate: plate}, true
	}
	mux.HandleFunc("GET /api/gcode/layers", func(w http.ResponseWriter, r *http.Request) {
		in, _ := base(r)
		o, err := p.GcodeIndex(r.Context(), in)
		w.Header().Set("Cache-Control", "private, max-age=60")
		printingReply(c, w, o, err, http.StatusOK)
	})
	mux.HandleFunc("GET /api/gcode/slice", func(w http.ResponseWriter, r *http.Request) {
		in, _ := base(r)
		q := r.URL.Query()
		from, _ := strconv.Atoi(q.Get("from_layer"))
		max, _ := strconv.Atoi(q.Get("max_points"))
		o, err := p.GcodeSlice(r.Context(), printing.GcodeSliceInput{ArtifactID: in.ArtifactID, PrintJobID: in.PrintJobID, Plate: in.Plate,
			FromLayer: from, MaxPoints: max, WithTravel: q.Get("with_travel") == "1" || q.Get("with_travel") == "true"})
		w.Header().Set("Cache-Control", "private, max-age=60")
		printingReply(c, w, o, err, http.StatusOK)
	})
}
