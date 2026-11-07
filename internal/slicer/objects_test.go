package slicer

import (
	"archive/zip"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
)

func fakeArchive(t *testing.T, entries map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.gcode.3mf")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for n, c := range entries {
		w, _ := zw.Create(n)
		w.Write([]byte(c))
	}
	zw.Close()
	f.Close()
	return p
}

func TestObjectReportSplitsByLabel(t *testing.T) {
	g := strings.Join([]string{
		"; HEADER", "G1 X1 Y1 E5", // démarrage : hors objet
		"; CHANGE_LAYER", "; Z_HEIGHT: 0.2",
		"; start printing object, unique label id: 100", "G1 X10 Y10 E1.0", "G1 X20 Y10 E2.0", "G1 X20 Y10 E-0.8", "; stop printing object, unique label id: 100",
		"; start printing object, unique label id: 101", "G1 X50 Y10 E1.0", "; stop printing object, unique label id: 101",
		"; CHANGE_LAYER", "; Z_HEIGHT: 0.4",
		"; start printing object, unique label id: 100", "G1 X10 Y20 E3.0", "; stop printing object, unique label id: 100",
	}, "\n")
	si := `<config><plate><metadata key="index" value="1"/><object identify_id="100" name="dense" skipped="false"/><object identify_id="101" name="light" skipped="false"/></plate></config>`
	ps := `{"filament_diameter":["2.0"],"filament_density":["1.0"]}`
	p := fakeArchive(t, map[string]string{"Metadata/plate_1.gcode": g, "Metadata/slice_info.config": si, "Metadata/project_settings.config": ps})
	rep, err := ObjectReport(p, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep) != 2 {
		t.Fatalf("%+v", rep)
	}
	d, l := rep[0], rep[1]
	if d.Name != "dense" || math.Abs(d.ExtrudedMM-6.0) > 1e-9 || d.Layers != 2 || d.MaxZ != 0.4 {
		t.Errorf("dense %+v", d)
	}
	if l.Name != "light" || math.Abs(l.ExtrudedMM-1.0) > 1e-9 || l.Layers != 1 {
		t.Errorf("light %+v", l)
	}
	// filament de 2 mm (section π mm²) et densité 1 : 6 mm -> 6π mm³ = 0,01885 g
	if math.Abs(d.Grams-6*math.Pi/1000) > 1e-9 {
		t.Errorf("masse %.6f", d.Grams)
	}
	if _, err := ObjectReport(p, 2); err == nil {
		t.Error("plaque absente acceptée")
	}
	q := fakeArchive(t, map[string]string{"Metadata/plate_1.gcode": "G1 X1 Y1 E1\n"})
	if _, err := ObjectReport(q, 1); err == nil {
		t.Error("G-code sans marqueurs d'objet accepté")
	}
}

func TestPlateOriginsFollowStudioGrid(t *testing.T) {
	// 1 plaque : origine ; 2 : côte à côte ; 3 et 4 : grille 2 colonnes, ligne suivante vers les Y négatifs ; 5 : 3 colonnes
	cases := []struct {
		n, plate int
		x, y     float64
	}{{1, 1, 0, 0}, {2, 2, 307.2, 0}, {3, 3, 0, -307.2}, {4, 4, 307.2, -307.2}, {5, 4, 0, -307.2}, {5, 3, 614.4, 0}}
	for _, c := range cases {
		x, y := PlateOrigins(256, 256, c.n)(c.plate)
		if math.Abs(x-c.x) > 1e-6 || math.Abs(y-c.y) > 1e-6 {
			t.Errorf("n=%d plaque %d : (%.1f, %.1f), attendu (%.1f, %.1f)", c.n, c.plate, x, y, c.x, c.y)
		}
	}
}

func TestProjectSettingsMergesFilaments(t *testing.T) {
	mk := func(k profiles.Kind, cfg map[string]any) *profiles.Resolved {
		return &profiles.Resolved{Kind: k, Config: cfg}
	}
	m := mk(profiles.KindMachine, map[string]any{"printer_model": "P1S"})
	p := mk(profiles.KindProcess, map[string]any{"layer_height": "0.2"})
	f1 := mk(profiles.KindFilament, map[string]any{"filament_type": []any{"PLA"}})
	f2 := mk(profiles.KindFilament, map[string]any{"filament_type": []any{"PETG"}})
	b, err := projectSettings(m, p, []*profiles.Resolved{f1, f2})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"printer_model":"P1S"`, `"layer_height":"0.2"`, `"filament_type":["PLA","PETG"]`} {
		if !strings.Contains(s, want) {
			t.Errorf("project_settings sans %s : %s", want, s)
		}
	}
	// le projet écrit se relit
	dir := t.TempDir()
	path, err := writeProject(dir, []mesh.ProjectObject{{Name: "a", Mesh: mesh.Cube(10), Plate: 1, X: 5, Y: 5}}, nil, profiles.Variant{BedX: 256, BedY: 256}, m, p, []*profiles.Resolved{f1})
	if err != nil {
		t.Fatal(err)
	}
	if md, err := mesh.ReadFile(path); err != nil || len(md.Objects) != 1 {
		t.Errorf("projet illisible : %v", err)
	}
}
