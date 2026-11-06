package httpapi

import (
	"net/http"
	"sort"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/engineering"
)

// MaterialSummary : une ligne du catalogue de matériaux.
type MaterialSummary struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Family     string   `json:"family"`
	Vendor     string   `json:"vendor"`
	TDSSource  string   `json:"tds_source"`
	Known      int      `json:"known_properties"`
	Unknown    []string `json:"unknown_properties"`
	WorstLevel string   `json:"lowest_confidence"`
}

// MaterialDetail : fiche complète, avec les sources réellement citées par ses propriétés.
type MaterialDetail struct {
	ID         string                            `json:"id"`
	Name       string                            `json:"name"`
	Family     string                            `json:"family"`
	Vendor     string                            `json:"vendor"`
	TDSSource  string                            `json:"tds_source"`
	Properties map[string]engineering.Property   `json:"properties"`
	Unknown    []string                          `json:"unknown_properties"`
	Sources    map[string]engineering.DataSource `json:"sources"`
	DBVersion  string                            `json:"db_version"`
	Note       string                            `json:"note"`
	Disclaimer string                            `json:"disclaimer"`
}

var levelRank = map[engineering.Level]int{engineering.LevelMeasured: 0, engineering.LevelCalculated: 1, engineering.LevelHeuristic: 2, engineering.LevelIndicative: 3}

// registerMaterials publie la base de matériaux de internal/engineering (lecture seule) :
//
//	GET /api/materials        liste (famille, fabricant, propriétés connues / inconnues)
//	GET /api/materials/{id}   fiche : chaque propriété avec sa valeur, son unité, sa source (fiche technique), sa date,
//	                          son niveau de confiance et son domaine de validité. Valeur absente = inconnue.
func registerMaterials(mux *http.ServeMux, c *core.Core) {
	mux.HandleFunc("GET /api/materials", func(w http.ResponseWriter, r *http.Request) {
		db, err := engineering.Materials()
		if err != nil {
			c.Log.Error("base de matériaux", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "base de matériaux illisible", "code": "internal"})
			return
		}
		out := make([]MaterialSummary, 0, len(db.Materials))
		for _, id := range engineering.MaterialIDs() {
			m := db.Materials[id]
			s := MaterialSummary{ID: id, Name: m.Name, Family: m.Family, Vendor: m.Vendor, TDSSource: m.TDSSource, Unknown: []string{}}
			worst := -1
			var lvl engineering.Level
			for k, p := range m.Properties {
				if p.Value == nil {
					s.Unknown = append(s.Unknown, k)
					continue
				}
				s.Known++
				if rk := levelRank[p.Level]; rk > worst {
					worst, lvl = rk, p.Level
				}
			}
			sort.Strings(s.Unknown)
			s.WorstLevel = string(lvl)
			out = append(out, s)
		}
		writeJSON(w, http.StatusOK, map[string]any{"version": db.Version, "note": db.Note, "disclaimer": engineering.Disclaimer, "materials": out})
	})
	mux.HandleFunc("GET /api/materials/{id}", func(w http.ResponseWriter, r *http.Request) {
		m, err := engineering.GetMaterial(r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error(), "code": "not_found"})
			return
		}
		db, _ := engineering.Materials()
		d := MaterialDetail{ID: m.ID, Name: m.Name, Family: m.Family, Vendor: m.Vendor, TDSSource: m.TDSSource, Properties: m.Properties,
			Unknown: []string{}, Sources: map[string]engineering.DataSource{}, DBVersion: db.Version, Note: db.Note, Disclaimer: engineering.Disclaimer}
		for k, p := range m.Properties {
			if p.Value == nil {
				d.Unknown = append(d.Unknown, k)
			}
			if s, ok := db.Sources[p.Source]; ok {
				d.Sources[p.Source] = s
			}
		}
		if s, ok := db.Sources[m.TDSSource]; ok {
			d.Sources[m.TDSSource] = s
		}
		sort.Strings(d.Unknown)
		writeJSON(w, http.StatusOK, d)
	})
}
