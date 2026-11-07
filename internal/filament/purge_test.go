package filament

import (
	"archive/zip"
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// G-code synthétique au format de Bambu Studio : rétractions compensées, tour de purge, purge de buse.
const syntheticGcode = `; HEADER_BLOCK_START
; total filament length [mm] : 1000.00,500.00
; total filament weight [g] : 3.00,1.50
; filament_density: 1.26,1.26
; filament_diameter: 1.75,1.75
; HEADER_BLOCK_END
G1 X10.1 Y20 Z0.28 F1500.0 E15 ;Draw the first line
; MACHINE_START_GCODE_END
;VT0 H-1
M83 ; use relative distances for extrusion
; FEATURE: Outer wall
G1 X1 Y1 E100.0
G1 E-2 F1800
G1 E2 F300
G1 X2 Y2 E200.0
; FEATURE: Prime tower
G1 X3 Y3 E50.0
M620 S1A
T1
; FLUSH_START
G1 E20 F523
G1 E0.5 F50
; FLUSH_END
G1 E-2 F1800
G1 E2 F300
G1 X4 Y4 E10.0
; FEATURE: Outer wall
G1 X5 Y5 E300.0
M620 S0A
T0
; FLUSH_START
G1 E30 F523
; FLUSH_END
; FEATURE: Prime tower
G1 X6 Y6 E5.0
; enable_prime_tower = 1
; flush_volumes_matrix = 0,200,300,0
; flush_multiplier = 1
`

func massOf(mm, dia, rho float64) float64 { return mm * math.Pi * dia * dia / 4 * rho / 1000 }

func TestScanPurgeSyntheticGcode(t *testing.T) {
	r, err := scanPurge(strings.NewReader(syntheticGcode), 1)
	if err != nil {
		t.Fatal(err)
	}
	// Outil 0 : tour 50 + 5 = 55 mm ; purge 30 mm. Outil 1 : tour 10 mm (la rétraction/dérétraction de 2 mm est compensée) ; purge 20.5 mm.
	want := func(mm float64) float64 { return round2(massOf(mm, 1.75, 1.26)) }
	if r.PerFilament[0].TowerG != want(55) || r.PerFilament[0].FlushG != want(30) {
		t.Fatalf("filament 1 : %+v (attendu tour %v purge %v)", r.PerFilament[0], want(55), want(30))
	}
	if r.PerFilament[1].TowerG != want(10) || r.PerFilament[1].FlushG != want(20.5) {
		t.Fatalf("filament 2 : %+v", r.PerFilament[1])
	}
	if r.Changes != 2 || !r.TowerEnabled {
		t.Fatalf("changements : %+v", r)
	}
	if math.Abs(r.TotalG-massOf(1500, 1.75, 1.26)) > 0.011 || r.WasteG <= 0 || r.WastePercent <= 0 {
		t.Fatalf("totaux : %+v", r)
	}
	// Recoupement par la matrice : T0->T1 = 200 mm³ puis T1->T0 = 300 mm³ (densité du filament entrant).
	if math.Abs(r.MatrixEstimateG-round2((200+300)*1.26/1000)) > 0.011 {
		t.Fatalf("estimation par la matrice : %v", r.MatrixEstimateG)
	}
	if !strings.Contains(r.Check, "écart") {
		t.Fatalf("contrôle de lecture : %q", r.Check)
	}
}

func TestScanPurgeNeedsHeader(t *testing.T) {
	if _, err := scanPurge(strings.NewReader("G1 X1 E5\n"), 1); err == nil {
		t.Fatal("un G-code sans diamètre ni densité ne doit pas produire de masse inventée")
	}
}

func TestReadPurgeFromArchive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.gcode.3mf")
	f, _ := os.Create(p)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("Metadata/plate_1.gcode")
	_, _ = w.Write([]byte(syntheticGcode))
	zw.Close()
	f.Close()
	r, err := ReadPurge(p, 1)
	if err != nil || r.Plate != 1 || len(r.PerFilament) != 2 {
		t.Fatalf("%v %+v", err, r)
	}
	if _, err := ReadPurge(p, 2); err == nil {
		t.Fatal("plaque absente")
	}
	if _, err := ReadPurge(filepath.Join(t.TempDir(), "absent"), 1); err == nil {
		t.Fatal("archive absente")
	}
}

func TestColorMathAndMaterials(t *testing.T) {
	if d := colorDistance("#FFFFFF", "#FFFFFF"); d != 0 {
		t.Fatal(d)
	}
	if d := colorDistance("#FFFFFF", "#F4F4F0"); d > 8 {
		t.Fatalf("blanc / blanc cassé : %v", d)
	}
	if d := colorDistance("#1060D0", "#000000"); d < 40 {
		t.Fatalf("bleu / noir : %v", d)
	}
	if colorDistance("x", "#000000") != 999 || normHex("00ae42") != "#00AE42" || normHex("#00AE4280") != "#00AE42" || normHex("rouge") != "" {
		t.Fatal("normalisation")
	}
	for _, c := range []struct{ a, b string }{{"PLA Basic", "PLA"}, {"Bambu PETG HF", "PETG"}, {"PLA-CF", "PLA"}, {"pla", "PLA"}} {
		if !sameMaterial(c.a, c.b) {
			t.Fatalf("%s ~ %s", c.a, c.b)
		}
	}
	if sameMaterial("PLA", "PETG") || sameMaterial("", "PLA") || materialFamily("ABS") != "ABS" || materialFamily("Support Foo") != "SUPPORT" {
		t.Fatal("familles")
	}
	if colorQuality(5) != "exact" || colorQuality(20) != "proche" || colorQuality(50) != "approximatif" || colorQuality(80) != "aucune" {
		t.Fatal("qualité")
	}
}

func TestPlateUseAndGrams(t *testing.T) {
	if plateUse(printingJob(nil)) != nil {
		t.Fatal("sans résumé")
	}
	p := &slicer.PlateResult{Index: 2, Filaments: []slicer.FilamentUse{{Slot: 1, Grams: 7}, {Slot: 3, Grams: 2}}}
	if g, ok := gramsFor(p, 3); !ok || g != 2 {
		t.Fatal("gramsFor")
	}
	if _, ok := gramsFor(p, 2); ok {
		t.Fatal("filament absent")
	}
	_ = context.Background()
}
