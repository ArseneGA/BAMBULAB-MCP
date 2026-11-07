package calibration

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/goals"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

func f(v float64) *float64 { return &v }

func near(t *testing.T, got, want, tol float64, what string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s : %v, attendu %v ± %v", what, got, want, tol)
	}
}

// ---- calculs purs ----

func TestComputeHoleLinearFit(t *testing.T) {
	// trous qui sortent trop petits de 0,10 + 1 % du diamètre
	var holes []DiameterMeasure
	for _, d := range []float64{3, 4, 5, 6, 8, 10} {
		holes = append(holes, DiameterMeasure{Nominal: d, Measured: d - (0.10 + 0.01*d)})
	}
	cal, notes, err := Compute(Measures{Holes: holes})
	if err != nil {
		t.Fatal(err)
	}
	h := cal.HoleVertical
	near(t, h.Offset, 0.10, 1e-3, "ordonnée")
	near(t, h.Slope, 0.01, 1e-4, "pente")
	if h.N != 6 || h.Residual > 1e-3 || cal.HoleHorizontal != nil || cal.Shaft != nil {
		t.Fatalf("%+v", h)
	}
	near(t, h.At(7), 0.17, 2e-3, "décalage à 7 mm")
	if len(notes) != 1 || !strings.Contains(notes[0], "Trous verticaux") {
		t.Fatalf("notes : %v", notes)
	}
}

func TestComputeHolesByAxisShaftsAndMean(t *testing.T) {
	cal, _, err := Compute(Measures{
		Holes:  []DiameterMeasure{{Nominal: 5, Measured: 4.85, Axis: "vertical"}, {Nominal: 5, Measured: 4.75, Axis: "horizontal"}, {Nominal: 8, Measured: 7.73, Axis: "horizontal"}},
		Shafts: []DiameterMeasure{{Nominal: 5, Measured: 5.05}, {Nominal: 10, Measured: 10.15}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// moins de 3 points : moyenne simple, pente nulle
	near(t, cal.HoleVertical.Offset, 0.15, 1e-9, "vertical")
	near(t, cal.HoleHorizontal.Offset, 0.26, 1e-9, "horizontal (moyenne de 0,25 et 0,27)")
	if cal.HoleHorizontal.Slope != 0 || cal.HoleHorizontal.N != 2 {
		t.Fatalf("%+v", cal.HoleHorizontal)
	}
	near(t, cal.Shaft.Offset, -0.10, 1e-9, "arbres plus gros de 0,10")
}

func TestComputeShrinkAndGaps(t *testing.T) {
	cal, notes, err := Compute(Measures{CubeMM: []float64{49.8, 49.9, 49.7}, FitSlidesGapMM: f(0.3), FitSnugGapMM: f(0.2), BestTempC: f(215), BridgeMaxSpanMM: f(30), OverhangMaxDeg: f(55)})
	if err != nil {
		t.Fatal(err)
	}
	near(t, *cal.ShrinkXY, 50/49.85, 1e-9, "échelle XY")
	near(t, *cal.ShrinkZ, 50/49.7, 1e-9, "échelle Z")
	if *cal.SlidesGapMM != 0.3 || *cal.SnugGapMM != 0.2 || *cal.BestTempC != 215 || *cal.BridgeMaxSpanMM != 30 || *cal.OverhangMaxDeg != 55 {
		t.Fatalf("%+v", cal)
	}
	if len(notes) != 6 {
		t.Fatalf("notes : %v", notes)
	}
}

func TestComputeRejectsImplausibleMeasures(t *testing.T) {
	for name, m := range map[string]Measures{
		"vide":               {},
		"jeu énorme":         {FitSlidesGapMM: f(5)},
		"jeu négatif":        {FitSlidesGapMM: f(-0.1)},
		"serré > glissant":   {FitSlidesGapMM: f(0.2), FitSnugGapMM: f(0.4)},
		"trou 2x":            {Holes: []DiameterMeasure{{Nominal: 5, Measured: 10}}},
		"nominal hors plage": {Holes: []DiameterMeasure{{Nominal: 0.2, Measured: 0.2}}},
		"axe inconnu":        {Holes: []DiameterMeasure{{Nominal: 5, Measured: 4.9, Axis: "diagonal"}}},
		"cube en pouces":     {CubeMM: []float64{1.96, 1.96, 1.96}},
		"cube 2 valeurs":     {CubeMM: []float64{50, 50}},
		"température folle":  {BestTempC: f(900)},
		"pont":               {BridgeMaxSpanMM: f(0)},
		"surplomb 90":        {OverhangMaxDeg: f(90)},
		"NaN":                {FitSlidesGapMM: f(math.NaN())},
	} {
		if _, _, err := Compute(m); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s : %v", name, err)
		}
	}
}

func TestMergeOverlaysNonNilFields(t *testing.T) {
	a, _, _ := Compute(Measures{FitSlidesGapMM: f(0.3), BestTempC: f(210)})
	a.Provenance = "A"
	b, _, _ := Compute(Measures{FitSlidesGapMM: f(0.4)})
	b.Provenance = "B"
	m := Merge(&a, &b)
	if *m.SlidesGapMM != 0.4 || *m.BestTempC != 210 || m.Provenance != "B" || *a.SlidesGapMM != 0.3 {
		t.Fatalf("%+v (base modifiée ? %v)", m, *a.SlidesGapMM)
	}
}

func TestKeyNormalize(t *testing.T) {
	k, err := Key{Printer: "  P1S   Salon ", NozzleMM: 0.4004, Material: " pla ", Filament: "Bambu  PLA Basic"}.Normalize()
	if err != nil || k.Printer != "p1s salon" || k.NozzleMM != 0.4 || k.Material != "PLA" || k.Filament != "bambu pla basic" {
		t.Fatalf("%+v %v", k, err)
	}
	for _, bad := range []Key{{NozzleMM: 0.4}, {NozzleMM: 0, Material: "PLA"}, {NozzleMM: 3, Material: "PLA"}, {NozzleMM: 0.4, Material: "PLA", Printer: strings.Repeat("x", 200)}} {
		if _, err := bad.Normalize(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v : %v", bad, err)
		}
	}
}

func TestDefaultTemperatures(t *testing.T) {
	if got := DefaultTemperatures("PLA"); got[0] != 190 || got[4] != 240 {
		t.Fatal(got)
	}
	if got := DefaultTemperatures("PETG-HF"); got[0] != 220 {
		t.Fatal(got)
	}
	if got := DefaultTemperatures("ABS"); got[0] != 240 || got[4] != 280 {
		t.Fatal(got)
	}
	if got := DefaultTemperatures("matière-mystère"); got[0] != 190 {
		t.Fatal(got)
	}
}

// ---- service (worker simulé) ----

type fakeCat struct {
	t        *testing.T
	reqs     []map[string]any
	registry int
	pieces   func(req map[string]any) map[string]any
}

func (c *fakeCat) RunWorker(_ context.Context, cmd string, req map[string]any) (json.RawMessage, error) {
	c.reqs = append(c.reqs, req)
	if cmd != "calibration_set" {
		c.t.Fatalf("commande %s", cmd)
	}
	out := req["out_dir"].(string)
	var pieces []map[string]any
	add := func(kind, name string, extra map[string]any) {
		for _, ext := range []string{".stl", ".step"} {
			if err := os.WriteFile(filepath.Join(out, name+ext), []byte("x"+name+ext), 0o644); err != nil {
				c.t.Fatal(err)
			}
		}
		p := map[string]any{"kind": kind, "name": name, "description": "d " + kind, "measure": []string{"mesure " + kind},
			"files": map[string]string{"stl": filepath.Join(out, name+".stl"), "step": filepath.Join(out, name+".step")}, "parts": []map[string]any{}, "bbox_mm": map[string]any{}, "info": map[string]any{}, "warnings": []string{}}
		for k, v := range extra {
			p[k] = v
		}
		pieces = append(pieces, p)
	}
	prefix := req["name_prefix"].(string)
	kinds, _ := req["kinds"].([]string)
	if len(kinds) == 0 {
		kinds = []string{"fit_comb", "hole_shaft", "shrink_cube", "bridge_overhang", "temperature"}
	}
	for _, k := range kinds {
		if k == "temperature" {
			for _, tt := range req["temperatures"].([]float64) {
				add(k, prefix+"_temp_"+strings.TrimSuffix(strings.TrimSuffix(jsonNum(tt), ".0"), "."), map[string]any{"nozzle_temperature_c": tt,
					"slicing_overrides": []map[string]any{{"key": "nozzle_temperature", "value": jsonNum(tt), "target": "filament"}}})
			}
			continue
		}
		add(k, prefix+"_"+k, nil)
	}
	b, _ := json.Marshal(map[string]any{"pieces": pieces})
	return b, nil
}

func jsonNum(v float64) string { b, _ := json.Marshal(v); return string(b) }

func (c *fakeCat) RegisterGenerated(_ context.Context, _ string, m contracts.Model, _, _, _ string, files []catalog.GeneratedFile) (contracts.Model, contracts.ModelVersion, []contracts.SourceFile, error) {
	c.registry++
	if len(files) != 2 {
		c.t.Fatalf("2 fichiers attendus : %d", len(files))
	}
	m.ID = contracts.NewID("mdl")
	return m, contracts.ModelVersion{ID: contracts.NewID("ver"), ModelID: m.ID}, nil, nil
}

type fakeProfiles struct{ calls []goals.ModifyInput }

func (p *fakeProfiles) ModifyProfile(_ context.Context, in goals.ModifyInput) (*goals.ModifyOutput, error) {
	p.calls = append(p.calls, in)
	return &goals.ModifyOutput{Revision: "prof_" + in.Modifications[0].Value.(string)}, nil
}

func newSvc(t *testing.T, cat Catalog, pm ProfileMaker) *Service {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bl, err := store.NewBlobStore(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	st.AttachBlobs(bl)
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	s, err := New(Deps{Store: st, Blobs: bl, Catalog: cat, Profiles: pm, WorkRoot: filepath.Join(dir, "work"), Now: func() time.Time { now = now.Add(time.Minute); return now }})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRecordResolveAndFallbackChain(t *testing.T) {
	s := newSvc(t, nil, nil)
	ctx := context.Background()
	general := Key{NozzleMM: 0.4, Material: "PLA"}
	r0, err := s.Resolve(ctx, Key{Printer: "P1S", NozzleMM: 0.4, Material: "PLA", Filament: "Bambu PLA Basic"})
	if err != nil || r0.Calibration != nil || !strings.Contains(r0.Note, "génériques") {
		t.Fatalf("avant mesure : %+v %v", r0, err)
	}
	// 1. calibration générale (toutes imprimantes)
	o1, err := s.Record(ctx, RecordInput{Key: general, Measures: Measures{FitSlidesGapMM: f(0.30), BestTempC: f(210)}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(o1.Results.Provenance, "calibré le 2026-11-07") || o1.Results.CalibratedAt != "2026-11-07" || len(o1.Effects) != 2 {
		t.Fatalf("%+v", o1)
	}
	// 2. calibration propre à l'imprimante : jeu différent, mais la température générale reste héritée
	if _, err := s.Record(ctx, RecordInput{Key: Key{Printer: "P1S", NozzleMM: 0.4, Material: "PLA"}, Measures: Measures{FitSlidesGapMM: f(0.25)}}); err != nil {
		t.Fatal(err)
	}
	r, err := s.Resolve(ctx, Key{Printer: "p1s", NozzleMM: 0.4, Material: "pla", Filament: "Autre bobine"})
	if err != nil || r.Calibration == nil {
		t.Fatalf("%+v %v", r, err)
	}
	if *r.Calibration.SlidesGapMM != 0.25 || *r.Calibration.BestTempC != 210 || len(r.Matched) != 2 {
		t.Fatalf("fusion : %+v %v", r.Calibration, r.Matched)
	}
	if !strings.Contains(r.Calibration.Provenance, "calibré le") || !strings.Contains(r.Note, "Calibration appliquée") {
		t.Fatalf("provenance : %q", r.Calibration.Provenance)
	}
	// 3. bobine précise : prime sur tout
	if _, err := s.Record(ctx, RecordInput{Key: Key{Printer: "P1S", NozzleMM: 0.4, Material: "PLA", Filament: "Bambu PLA Basic"}, Measures: Measures{FitSlidesGapMM: f(0.22), CubeMM: []float64{50.1, 50.1, 50}}}); err != nil {
		t.Fatal(err)
	}
	r, _ = s.Resolve(ctx, Key{Printer: "P1S", NozzleMM: 0.4, Material: "PLA", Filament: "bambu pla basic"})
	if *r.Calibration.SlidesGapMM != 0.22 || r.Calibration.ShrinkXY == nil || *r.Calibration.BestTempC != 210 || len(r.Matched) != 3 {
		t.Fatalf("%+v %v", r.Calibration, r.Matched)
	}
	// une autre buse, un autre matériau, une autre imprimante : pas de calibration (ou seulement la générale)
	if r, _ := s.Resolve(ctx, Key{NozzleMM: 0.6, Material: "PLA"}); r.Calibration != nil {
		t.Fatal("calibration d'une autre buse appliquée")
	}
	if r, _ := s.Resolve(ctx, Key{NozzleMM: 0.4, Material: "PETG"}); r.Calibration != nil {
		t.Fatal("calibration d'un autre matériau appliquée")
	}
	if r, _ := s.Resolve(ctx, Key{Printer: "x1c", NozzleMM: 0.4, Material: "PLA"}); r.Calibration == nil || *r.Calibration.SlidesGapMM != 0.30 {
		t.Fatalf("repli sur la calibration générale attendu : %+v", r)
	}
	// ForEngineering
	if c, err := s.ForEngineering(ctx, general); err != nil || *c.SlidesGapMM != 0.30 {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestRecordMergesSameKeyAndKeepsHistory(t *testing.T) {
	s := newSvc(t, nil, nil)
	ctx := context.Background()
	k := Key{Printer: "P1S", NozzleMM: 0.4, Material: "PLA"}
	if _, err := s.Record(ctx, RecordInput{Key: k, Measures: Measures{FitSlidesGapMM: f(0.3), BestTempC: f(210)}, Notes: "premier essai"}); err != nil {
		t.Fatal(err)
	}
	o2, err := s.Record(ctx, RecordInput{Key: k, Measures: Measures{FitSlidesGapMM: f(0.28)}})
	if err != nil {
		t.Fatal(err)
	}
	if *o2.Active.SlidesGapMM != 0.28 || *o2.Active.BestTempC != 210 || o2.Results.BestTempC != nil {
		t.Fatalf("fusion sur la même clé : active=%+v results=%+v", o2.Active, o2.Results)
	}
	h, err := s.History(ctx, "pla", 0.4, 10)
	if err != nil || len(h) != 2 || h[0].RecordID != o2.RecordID || h[1].Notes != "premier essai" || *h[1].Results.SlidesGapMM != 0.3 {
		t.Fatalf("historique : %+v %v", h, err)
	}
	// les mesures sont immuables
	if _, err := s.d.Store.DB().Exec(`UPDATE calibration_records SET notes='x'`); err == nil {
		t.Fatal("UPDATE du journal accepté")
	}
	if _, err := s.d.Store.DB().Exec(`DELETE FROM calibration_records`); err == nil {
		t.Fatal("DELETE du journal accepté")
	}
}

func TestRecordValidation(t *testing.T) {
	s := newSvc(t, nil, nil)
	ctx := context.Background()
	k := Key{NozzleMM: 0.4, Material: "PLA"}
	if _, err := s.Record(ctx, RecordInput{Key: k}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("vide : %v", err)
	}
	if _, err := s.Record(ctx, RecordInput{Key: Key{NozzleMM: 0.4}, Measures: Measures{BestTempC: f(210)}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("sans matériau : %v", err)
	}
	if _, err := s.Record(ctx, RecordInput{Key: k, SetID: "cal_inconnu", Measures: Measures{BestTempC: f(210)}}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("série inconnue : %v", err)
	}
	if h, _ := s.History(ctx, "", 0, 0); len(h) != 0 {
		t.Fatalf("rien ne doit être enregistré après un refus : %d", len(h))
	}
}

func TestGenerateRegistersModelsAndProfiles(t *testing.T) {
	cat := &fakeCat{t: t}
	pm := &fakeProfiles{}
	s := newSvc(t, cat, pm)
	ctx := context.Background()
	out, err := s.Generate(ctx, GenerateInput{Key: Key{Printer: "P1S", NozzleMM: 0.4, Material: "PETG", Filament: "Bambu PETG HF"},
		Kinds: []string{"fit_comb", "temperature"}, Base: &goals.BaseSpec{Machine: "Bambu Lab P1S 0.4 nozzle"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Pieces) != 6 || cat.registry != 6 { // peigne + 5 températures
		t.Fatalf("pièces : %d, enregistrées : %d", len(out.Pieces), cat.registry)
	}
	req := cat.reqs[0]
	if temps := req["temperatures"].([]float64); temps[0] != 220 || len(temps) != 5 { // PETG
		t.Fatalf("températures par défaut : %v", temps)
	}
	if req["nozzle_diameter"] != 0.4 || req["name_prefix"] != "calib-0.4-PETG" {
		t.Fatalf("requête : %v", req)
	}
	if len(pm.calls) != 5 || out.Pieces[1].ProfileRevision != "prof_220" || pm.calls[0].Modifications[0].Key != "nozzle_temperature" {
		t.Fatalf("profils : %d %+v", len(pm.calls), out.Pieces[1])
	}
	if out.Pieces[0].ProfileRevision != "" || out.Pieces[0].ModelID == "" || out.Pieces[0].Measure[0] != "mesure fit_comb" {
		t.Fatalf("%+v", out.Pieces[0])
	}
	if _, ok := out.MeasuresTemplate["fit_slides_gap_mm"]; !ok || out.MeasuresTemplate["best_temp_c"] == nil || out.MeasuresTemplate["cube_mm"] != nil {
		t.Fatalf("squelette : %v", out.MeasuresTemplate)
	}
	// la série est mémorisée et peut servir à record_calibration
	if _, err := s.Record(ctx, RecordInput{Key: out.Key, SetID: out.SetID, Measures: Measures{FitSlidesGapMM: f(0.3)}}); err != nil {
		t.Fatalf("record avec set_id : %v", err)
	}
	// dossier de travail nettoyé
	if es, _ := os.ReadDir(s.d.WorkRoot); len(es) != 0 {
		t.Fatalf("dossier de travail non nettoyé : %d", len(es))
	}
}

func TestGenerateWithoutBaseOrCatalog(t *testing.T) {
	s := newSvc(t, &fakeCat{t: t}, &fakeProfiles{})
	out, err := s.Generate(context.Background(), GenerateInput{Key: Key{NozzleMM: 0.4, Material: "PLA"}, Kinds: []string{"temperature"}, Temperatures: []float64{200, 210}})
	if err != nil || len(out.Pieces) != 2 || out.Pieces[0].ProfileRevision != "" || len(out.Pieces[0].Overrides) != 1 {
		t.Fatalf("%+v %v", out, err)
	}
	if _, err := newSvc(t, nil, nil).Generate(context.Background(), GenerateInput{Key: Key{NozzleMM: 0.4, Material: "PLA"}}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("%v", err)
	}
	if _, err := s.Generate(context.Background(), GenerateInput{Key: Key{NozzleMM: 0.4}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("sans matériau : %v", err)
	}
}
