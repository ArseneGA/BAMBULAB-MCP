package library

import (
	"context"
	"io"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/calibration"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Tests contre le VRAI worker (uv + CadQuery) : ignorés avec -short, sans uv, ou si CadQuery n'est pas utilisable.

var (
	realOnce sync.Once
	realDir  string
	realR    *realRig
	realErr  string
)

type realRig struct {
	lib   *Service
	cal   *calibration.Service
	blobs *store.BlobStore
}

func TestMain(m *testing.M) {
	code := m.Run()
	if realDir != "" {
		os.RemoveAll(realDir)
	}
	os.Exit(code)
}

func realWorker(t *testing.T) *realRig {
	t.Helper()
	if testing.Short() {
		t.Skip("-short")
	}
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv absent")
	}
	realOnce.Do(func() {
		var err error
		if realDir, err = os.MkdirTemp("", "bambu-lib-real-"); err != nil {
			realErr = err.Error()
			return
		}
		st, err := store.Open(filepath.Join(realDir, "t.db"), store.Options{})
		if err != nil {
			realErr = err.Error()
			return
		}
		bl, err := store.NewBlobStore(filepath.Join(realDir, "blobs"))
		if err != nil {
			realErr = err.Error()
			return
		}
		st.AttachBlobs(bl)
		cat, err := catalog.New(catalog.Deps{Store: st, Blobs: bl, Queue: jobs.NewQueue(st, jobs.QueueConfig{}), Registry: jobs.NewRegistry(),
			Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DataDir: realDir},
			catalog.Config{WorkerDir: filepath.Join("..", "..", "workers", "geometry")})
		if err != nil {
			realErr = err.Error()
			return
		}
		if ok, note := cat.CADStatus(); !ok {
			realErr = "CadQuery indisponible : " + note
			return
		}
		cal, err := calibration.New(calibration.Deps{Store: st, Blobs: bl, Catalog: cat, WorkRoot: filepath.Join(realDir, "wc")})
		if err != nil {
			realErr = err.Error()
			return
		}
		lib, err := New(Deps{Store: st, Blobs: bl, Catalog: cat, Calibrations: cal, WorkRoot: filepath.Join(realDir, "wl")})
		if err != nil {
			realErr = err.Error()
			return
		}
		realR = &realRig{lib: lib, cal: cal, blobs: bl}
	})
	if realR == nil {
		t.Skip(realErr)
	}
	return realR
}

// stlReport lit le STL enregistré d'un modèle généré et l'analyse avec internal/mesh.
func (r *realRig) stlReport(t *testing.T, files []contracts.SourceFile) *mesh.Report {
	t.Helper()
	for _, f := range files {
		if f.Format != contracts.FormatSTL {
			continue
		}
		fh, err := r.blobs.Open(f.SHA256)
		if err != nil {
			t.Fatal(err)
		}
		defer fh.Close()
		data, err := io.ReadAll(fh)
		if err != nil {
			t.Fatal(err)
		}
		md, err := mesh.Parse(f.Name, data)
		if err != nil {
			t.Fatal(err)
		}
		return md.Analyze(mesh.AnalyzeOptions{}).Combined
	}
	t.Fatal("aucun STL")
	return nil
}

func requireWatertight(t *testing.T, what string, rep *mesh.Report) {
	t.Helper()
	if !rep.Watertight || rep.OpenEdges != 0 || rep.NonManifoldEdges != 0 || rep.InconsistentEdges != 0 || rep.InvertedNormals || rep.Volume <= 0 {
		t.Errorf("%s : maillage non étanche : %+v", what, rep)
	}
}

func TestRealEveryTemplateIsWatertight(t *testing.T) {
	r := realWorker(t)
	l, err := r.lib.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"clamp", "bracket", "box", "spacer", "hook", "hinge_print_in_place", "hinge_pin", "snap_clip", "enclosure", "wall_mount", "spur_gear", "gear_pair", "thread", "washer", "handle", "hook_reinforced", "mounting_plate"}
	have := map[string]TemplateInfo{}
	for _, tpl := range l.Templates {
		have[tpl.ID] = tpl
	}
	for _, id := range want {
		tpl, ok := have[id]
		if !ok {
			t.Errorf("gabarit %s absent", id)
			continue
		}
		if tpl.Version == "" || tpl.Description == "" || len(tpl.Params) == 0 {
			t.Errorf("fiche incomplète : %s", id)
		}
		for _, p := range tpl.Params {
			if p.Description == "" || p.Type == "" {
				t.Errorf("%s.%s : paramètre non décrit", id, p.Name)
			}
		}
	}
	for _, id := range want {
		if _, ok := have[id]; !ok {
			continue
		}
		in := CreateInput{Template: id, Name: "real_" + id}
		if id == "thread" {
			in.Params = map[string]any{"size": "M5", "length": 8.0}
		}
		o, err := r.lib.Create(context.Background(), in)
		if err != nil {
			t.Errorf("%s : %v", id, err)
			continue
		}
		requireWatertight(t, id, r.stlReport(t, o.Files))
		if len(o.Parts) == 0 || o.TemplateVersion == "" || o.ThumbnailURL == "" || len(o.Assumptions) == 0 {
			t.Errorf("%s : sortie incomplète %+v", id, o)
		}
		for _, p := range o.Parts {
			if !p.Watertight || p.Solids < 1 {
				t.Errorf("%s/%s : %+v", id, p.Name, p)
			}
		}
		if _, err := r.lib.ModelThumbnail(context.Background(), o.ModelID); err != nil {
			t.Errorf("%s : vignette : %v", id, err)
		}
	}
}

func TestRealCreateAppliesEngineeringAndMeasures(t *testing.T) {
	r := realWorker(t)
	ctx := context.Background()
	// rondelle : trou nominal 8 -> compensé, cotes mesurées sur le STL
	o, err := r.lib.Create(ctx, CreateInput{Template: "washer", Name: "real_washer", Params: map[string]any{"outer_diameter": 20.0, "inner_diameter": 8.0, "thickness": 3.0, "chamfer": 0.0}})
	if err != nil {
		t.Fatal(err)
	}
	inner := o.Params["inner_diameter"].(float64)
	if inner <= 8.05 || inner > 8.5 {
		t.Fatalf("compensation générique : %v", inner)
	}
	wantVol := math.Pi / 4 * (400 - inner*inner) * 3
	vol := o.Parts[0].VolumeMM3
	if math.Abs(vol-wantVol) > 1e-3*wantVol {
		t.Fatalf("volume %v, attendu %v", vol, wantVol)
	}
	bb := o.BBoxMM["size"].([]any)
	if math.Abs(bb[0].(float64)-20) > 0.02 || math.Abs(bb[2].(float64)-3) > 0.02 {
		t.Fatalf("boîte englobante : %v", bb)
	}
	// engrenages : entraxe = module (z1+z2)/2
	g, err := r.lib.Create(ctx, CreateInput{Template: "gear_pair", Name: "real_pair", Params: map[string]any{"module": 2.0, "teeth_1": 18.0, "teeth_2": 30.0}})
	if err != nil {
		t.Fatal(err)
	}
	if cd := g.Derived["center_distance"].(float64); cd != 48 {
		t.Fatalf("entraxe %v", cd)
	}
	if g.Auto[len(g.Auto)-1].Param != "bore_2" && g.Auto[0].Param != "backlash" {
		t.Logf("auto : %+v", g.Auto)
	}
	// clip : calcul SnapFit joint
	c, err := r.lib.Create(ctx, CreateInput{Template: "snap_clip", Name: "real_clip", Material: "PLA"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, cc := range c.Calcs {
		found = found || cc.Name == "snap_fit"
	}
	if !found {
		t.Fatalf("calculs : %+v", c.Calcs)
	}
	// boîtier : trous d'insert issus de ScrewBoss ; STL étanche
	e, err := r.lib.Create(ctx, CreateInput{Template: "enclosure", Name: "real_box", Params: map[string]any{"screw_size": "M3"}})
	if err != nil {
		t.Fatal(err)
	}
	if e.Params["insert_hole_diameter"].(float64) != 4.0 || e.Params["insert_depth"].(float64) != 7.2 {
		t.Fatalf("insert : %v", e.Params)
	}
	requireWatertight(t, "enclosure", r.stlReport(t, e.Files))
	// erreurs du worker traduites
	if _, err := r.lib.Create(ctx, CreateInput{Template: "hinge_print_in_place", Params: map[string]any{"knuckles": 4.0}}); err == nil {
		t.Fatal("charnière à nombre pair de nœuds acceptée")
	}
	// variante réutilisable de bout en bout
	s, err := r.lib.SaveAs(ctx, SaveInput{ModelID: o.ModelID, Name: "Rondelle 20-8 réelle"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := r.lib.Create(ctx, CreateInput{Template: s.Variant.ID, Name: "real_variant"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Params["outer_diameter"].(float64) != 20 {
		t.Fatalf("variante : %v", v.Params)
	}
	// vignette SVG du gabarit
	svg, err := r.lib.Thumbnail(ctx, "gear_pair", nil)
	if err != nil || len(svg) < 500 {
		t.Fatalf("vignette : %d %v", len(svg), err)
	}
}

func TestRealCalibrationEndToEnd(t *testing.T) {
	r := realWorker(t)
	ctx := context.Background()
	key := calibration.Key{Printer: "P1S test", NozzleMM: 0.4, Material: "PLA", Filament: "bobine test"}
	out, err := r.cal.Generate(ctx, calibration.GenerateInput{Key: key, Temperatures: []float64{200, 210}})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, p := range out.Pieces {
		kinds[p.Kind]++
		if p.ModelID == "" || len(p.Measure) == 0 {
			t.Errorf("pièce incomplète : %+v", p)
		}
	}
	if kinds["fit_comb"] != 1 || kinds["hole_shaft"] != 1 || kinds["shrink_cube"] != 1 || kinds["bridge_overhang"] != 1 || kinds["temperature"] != 2 {
		t.Fatalf("pièces : %v", kinds)
	}
	// le cube de retrait fait bien 50 mm
	for _, p := range out.Pieces {
		if p.Kind == "shrink_cube" {
			sz := p.BBoxMM["size"].([]any)
			for _, v := range sz {
				if math.Abs(v.(float64)-50) > 0.01 {
					t.Fatalf("cube : %v", sz)
				}
			}
		}
	}
	// mesures simulées -> calibration active -> prise en compte par la bibliothèque
	_, err = r.cal.Record(ctx, calibration.RecordInput{Key: key, SetID: out.SetID, Measures: calibration.Measures{
		FitSlidesGapMM: f64(0.33), Holes: []calibration.DiameterMeasure{{Nominal: 4, Measured: 3.85}, {Nominal: 6, Measured: 5.84}, {Nominal: 8, Measured: 7.83}},
		CubeMM: []float64{49.9, 49.9, 50.0}}})
	if err != nil {
		t.Fatal(err)
	}
	o, err := r.lib.Create(ctx, CreateInput{Template: "washer", Name: "real_cal_washer", Printer: "P1S test", Filament: "bobine test", Material: "PLA",
		Params: map[string]any{"inner_diameter": 6.0}})
	if err != nil {
		t.Fatal(err)
	}
	if !o.Calibration.Applied || o.Auto[0].Level != "measured" {
		t.Fatalf("calibration non appliquée : %+v %+v", o.Calibration, o.Auto)
	}
	got := o.Params["inner_diameter"].(float64)
	if math.Abs(got-6.16) > 0.01 { // décalage ≈ 0,16 mm mesuré (écart 0,15 à 0,17 selon la droite)
		t.Fatalf("diamètre calibré %v", got)
	}
}
