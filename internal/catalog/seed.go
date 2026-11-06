package catalog

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// SeedOptions règle la génération des données de démonstration.
type SeedOptions struct {
	// Dir : dossier de génération (défaut <données>/demo-src). Il devient une racine d'import.
	Dir string
	// WithCAD : essayer aussi les gabarits paramétriques du worker Python (pince, équerre, boîtier).
	WithCAD bool
	// WaitIdle : attendre ce délai que analyses et aperçus soient terminés (le runner doit tourner).
	WaitIdle time.Duration
}

// SeedReport résume la démo.
type SeedReport struct {
	Dir         string         `json:"dir"`
	Generated   int            `json:"generated_files"`
	Counts      map[string]int `json:"import_counts"`
	Models      int            `json:"models_in_catalog"`
	CADModels   []string       `json:"cad_models,omitempty"`
	CADSkipped  string         `json:"cad_skipped,omitempty"`
	DerivedIdle bool           `json:"derived_done"`
}

type demoPart struct {
	rel  string
	mesh func() *mesh.Mesh
	tags []string
}

func ring(r, rIn float64, n int, gapDeg float64) *mesh.Mesh {
	// Anneau ouvert (C) : polygone simple extrudé.
	g := gapDeg * math.Pi / 360
	var poly []mesh.Vec2
	for i := 0; i <= n; i++ {
		a := g + (2*math.Pi-2*g)*float64(i)/float64(n)
		poly = append(poly, mesh.Vec2{X: r * math.Cos(a), Y: r * math.Sin(a)})
	}
	for i := n; i >= 0; i-- {
		a := g + (2*math.Pi-2*g)*float64(i)/float64(n)
		poly = append(poly, mesh.Vec2{X: rIn * math.Cos(a), Y: rIn * math.Sin(a)})
	}
	return mesh.Extrude(poly, 15)
}

func gear(teeth int, rRoot, rTip, h float64) *mesh.Mesh {
	var poly []mesh.Vec2
	step := 2 * math.Pi / float64(teeth)
	for i := 0; i < teeth; i++ {
		a := float64(i) * step
		for _, p := range []struct{ da, r float64 }{{0.05, rRoot}, {0.25, rTip}, {0.5, rTip}, {0.7, rRoot}} {
			poly = append(poly, mesh.Vec2{X: p.r * math.Cos(a+p.da*step), Y: p.r * math.Sin(a+p.da*step)})
		}
	}
	return mesh.Extrude(poly, h)
}

func demoParts(clampR float64) []demoPart {
	return []demoPart{
		{"Mecanique/pince_serrage_30mm.stl", func() *mesh.Mesh { return ring(clampR, clampR-3, 40, 70) }, []string{"pince", "mecanique", "demo"}},
		{"Mecanique/equerre_renfort.stl", func() *mesh.Mesh {
			return mesh.Extrude([]mesh.Vec2{{X: 0, Y: 0}, {X: 60, Y: 0}, {X: 60, Y: 5}, {X: 5, Y: 5}, {X: 5, Y: 50}, {X: 0, Y: 50}}, 20)
		}, []string{"equerre", "mecanique", "demo"}},
		{"Mecanique/bouchon_cylindrique.stl", func() *mesh.Mesh { return mesh.Cylinder(6, 10, 48) }, []string{"entretoise", "mecanique", "demo"}},
		{"Mecanique/engrenage_16_dents.stl", func() *mesh.Mesh { return gear(16, 10, 12.5, 6) }, []string{"engrenage", "mecanique", "demo"}},
		{"Mecanique/ecrou_hexagonal.stl", func() *mesh.Mesh {
			var p []mesh.Vec2
			for i := 0; i < 6; i++ {
				a := float64(i) * math.Pi / 3
				p = append(p, mesh.Vec2{X: 8 * math.Cos(a), Y: 8 * math.Sin(a)})
			}
			return mesh.Extrude(p, 5)
		}, []string{"ecrou", "mecanique", "demo"}},
		{"Rangement/boitier_carre_perce.stl", func() *mesh.Mesh { return mesh.BoxWithSquareHole(40, 15) }, []string{"boitier", "rangement", "demo"}},
		{"Rangement/crochet_mural.stl", func() *mesh.Mesh {
			return mesh.Extrude([]mesh.Vec2{{X: 0, Y: 0}, {X: 40, Y: 0}, {X: 40, Y: 30}, {X: 28, Y: 30}, {X: 28, Y: 12}, {X: 4, Y: 12}, {X: 4, Y: 40}, {X: 0, Y: 40}}, 18)
		}, []string{"crochet", "rangement", "demo"}},
		{"Rangement/support_en_T.stl", func() *mesh.Mesh { return mesh.TShape(8, 30, 40, 6, 20) }, []string{"support", "rangement", "demo"}},
		{"Maison/plaque_double.stl", func() *mesh.Mesh { return mesh.TwoCubes(20, 10) }, []string{"plaque", "maison", "demo"}},
		{"Maison/cale_reglable.stl", func() *mesh.Mesh {
			return mesh.Extrude([]mesh.Vec2{{X: 0, Y: 0}, {X: 40, Y: 0}, {X: 40, Y: 12}}, 25)
		}, []string{"cale", "maison", "demo"}},
	}
}

func writeSTL(path string, m *mesh.Mesh) error {
	b, err := mesh.STLBytes(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// SeedDemo génère une dizaine de modèles réalistes (pince, équerre, boîtier…) avec
// internal/mesh (et, si demandé et disponible, le worker paramétrique), les range
// en collections et les importe par le chemin normal. Idempotent : une seconde
// exécution n'ajoute rien.
func (s *Service) SeedDemo(ctx context.Context, opt SeedOptions) (*SeedReport, error) {
	dir := opt.Dir
	if dir == "" {
		dir = filepath.Join(s.d.DataDir, "demo-src")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := s.AddRoot(dir); err != nil {
		return nil, err
	}
	rep := &SeedReport{Dir: dir, Counts: map[string]int{}}

	parts := demoParts(18)
	for _, p := range parts {
		full := filepath.Join(dir, filepath.FromSlash(p.rel))
		if _, err := os.Stat(full); err == nil {
			continue // déjà généré : ne pas réécrire (idempotence)
		}
		if err := writeSTL(full, p.mesh()); err != nil {
			return nil, err
		}
		rep.Generated++
	}

	if opt.WithCAD {
		rep.CADModels, rep.CADSkipped = s.seedParametric(ctx, dir, &rep.Generated)
	}

	in := ImportInput{Paths: []string{dir}, Collection: "Démo", License: "CC0-1.0",
		Source: "données de démonstration générées par bambud seed-demo", Tags: []string{"demo"}}
	imp, err := s.RunImport(ctx, in, nil, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range imp.Counts {
		rep.Counts[k] += v
	}

	// Étiquettes propres à chaque pièce.
	for _, p := range parts {
		name := stem(p.rel)
		res, err := s.Search(ctx, SearchQuery{Text: name, Limit: 5})
		if err != nil {
			return nil, err
		}
		for _, m := range res.Models {
			if m.Name == name {
				if _, err := s.TagModels(ctx, TagInput{ModelIDs: []string{m.ID}, Add: p.tags}); err != nil {
					return nil, err
				}
			}
		}
	}

	// Historique de versions : la pince est re-générée une fois avec un autre diamètre.
	if res, err := s.Search(ctx, SearchQuery{Text: "pince_serrage_30mm", Limit: 1}); err == nil && len(res.Models) == 1 && res.Models[0].Versions == 1 {
		if err := writeSTL(filepath.Join(dir, "Mecanique", "pince_serrage_30mm.stl"), ring(19, 16, 40, 70)); err != nil {
			return nil, err
		}
		imp2, err := s.RunImport(ctx, in, nil, nil)
		if err != nil {
			return nil, err
		}
		rep.Counts[StatusNewVersion] += imp2.Counts[StatusNewVersion]
	}

	all, err := s.Search(ctx, SearchQuery{Limit: 1})
	if err != nil {
		return nil, err
	}
	rep.Models = all.Total
	if opt.WaitIdle > 0 {
		rep.DerivedIdle = s.Idle(ctx, opt.WaitIdle)
	}
	return rep, nil
}

func stem(rel string) string {
	b := filepath.Base(rel)
	return b[:len(b)-len(filepath.Ext(b))]
}

// seedParametric essaie les gabarits CadQuery du worker. Tout échec est non fatal.
func (s *Service) seedParametric(ctx context.Context, dir string, generated *int) (names []string, skipped string) {
	if ok, note := s.CADStatus(); !ok {
		return nil, "backend CAO absent : " + note
	}
	type tpl struct {
		name, template string
		params         map[string]any
	}
	for _, t := range []tpl{
		{"pince_parametrique_25mm", "clamp", map[string]any{"tube_diameter": 25.0}},
		{"equerre_parametrique", "bracket", map[string]any{"leg_a": 50.0, "leg_b": 40.0}},
		{"boitier_parametrique", "box", map[string]any{"inner_x": 60.0, "inner_y": 40.0, "inner_z": 25.0}},
	} {
		dst := filepath.Join(dir, "CAO", t.name+".stl")
		if _, err := os.Stat(dst); err == nil {
			names = append(names, t.name)
			continue
		}
		tmp, err := os.MkdirTemp(dir, ".cad-")
		if err != nil {
			skipped = err.Error()
			continue
		}
		raw, err := s.worker()(ctx, "parametric", map[string]any{"template": t.template, "params": t.params, "out_dir": tmp, "name": t.name, "nozzle_diameter": 0.4})
		if err == nil {
			err = moveWorkerOutputs(raw, tmp, filepath.Join(dir, "CAO"), t.name)
		}
		os.RemoveAll(tmp)
		if err != nil {
			skipped = fmt.Sprintf("gabarit %s : %v", t.template, err)
			continue
		}
		*generated += 1
		names = append(names, t.name)
	}
	return names, skipped
}
