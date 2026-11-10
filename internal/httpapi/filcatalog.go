package httpapi

import (
	"net/http"
	"strconv"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	filcatalog "github.com/ArseneGA/BAMBULAB-MCP/internal/filament/catalog"
)

// registerFilamentCatalog monte l'API du catalogue officiel des filaments Bambu (B3). Lecture seule, données
// embarquées dans le binaire : aucune action sur l'imprimante, aucun appel réseau.
//
//	GET /api/filament-catalog                  aperçu : gammes + compteurs + sources
//	GET /api/filament-catalog/colors?family=&finish=&text=&hex=&max_delta_e=&nozzle=&hardened=1&limit=   (search_filament_catalog)
//	GET /api/filament-catalog/entry?id=GFA00-10300 | GFA00 | PLA Basic                                   (get_filament_catalog_entry)
func registerFilamentCatalog(mux *http.ServeMux, _ *core.Core) {
	fail := func(w http.ResponseWriter, err error) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error(), "code": "invalid"})
	}
	mux.HandleFunc("GET /api/filament-catalog", func(w http.ResponseWriter, r *http.Request) {
		cat, err := filcatalog.Default()
		if err != nil {
			fail(w, err)
			return
		}
		res, err := cat.RunSearch(filcatalog.Query{})
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
	mux.HandleFunc("GET /api/filament-catalog/colors", func(w http.ResponseWriter, r *http.Request) {
		cat, err := filcatalog.Default()
		if err != nil {
			fail(w, err)
			return
		}
		q := r.URL.Query()
		fq := filcatalog.Query{Family: q.Get("family"), Finish: q.Get("finish"), Text: q.Get("text"), Hex: q.Get("hex")}
		fq.MaxDelta, _ = strconv.ParseFloat(q.Get("max_delta_e"), 64)
		fq.Limit, _ = strconv.Atoi(q.Get("limit"))
		if nz := q.Get("nozzle"); nz != "" || q.Get("hardened") == "1" {
			fq.Nozzle = &filcatalog.Nozzle{DiameterMM: nz, Hardened: q.Get("hardened") == "1", Kind: q.Get("kind")}
		}
		res, err := cat.RunSearch(fq)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
	mux.HandleFunc("GET /api/filament-catalog/entry", func(w http.ResponseWriter, r *http.Request) {
		cat, err := filcatalog.Default()
		if err != nil {
			fail(w, err)
			return
		}
		q := r.URL.Query()
		var nz *filcatalog.Nozzle
		if d := q.Get("nozzle"); d != "" || q.Get("hardened") == "1" {
			nz = &filcatalog.Nozzle{DiameterMM: d, Hardened: q.Get("hardened") == "1", Kind: q.Get("kind")}
		}
		res, err := cat.Entry(q.Get("id"), nz)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error(), "code": "not_found"})
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
}
