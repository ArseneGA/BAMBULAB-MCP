package slicer

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// Tests d'intégration contre un vrai Bambu Studio. Ignorés si le binaire est
// absent ou avec -short. Variables : BAMBU_STUDIO_BIN, BAMBU_KEEP_WORK=1
// (conserve les répertoires de travail pour inspection).

const (
	p1sMachine  = "Bambu Lab P1S 0.4 nozzle"
	p1sProcess  = "0.20mm Standard @BBL X1C"
	p1sFilament = "Bambu PLA Basic @BBL P1S 0.4 nozzle"

	a1mMachine  = "Bambu Lab A1 mini 0.4 nozzle"
	a1mProcess  = "0.20mm Standard @BBL A1M"
	a1mFilament = "Bambu PLA Basic @BBL A1M"
)

func realSlicer(t *testing.T) *Slicer {
	t.Helper()
	if testing.Short() {
		t.Skip("-short")
	}
	if _, err := Locate(""); err != nil {
		t.Skipf("Bambu Studio absent: %v", err)
	}
	s, err := New(Config{WorkRoot: t.TempDir(), Timeout: 5 * time.Minute})
	if err != nil {
		t.Skipf("Bambu Studio inutilisable: %v", err)
	}
	return s
}

func writeSTL(t *testing.T, name string, m *mesh.Mesh) string {
	t.Helper()
	b, err := mesh.STLBytes(m)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func sliceOK(t *testing.T, s *Slicer, req Request) *Result {
	t.Helper()
	res, err := s.Slice(context.Background(), req)
	if err != nil {
		if res != nil {
			t.Logf("commande: %v\nstderr: %s\nresult.json: %s", res.Command, res.Stderr, res.StudioJSON)
			if res.Validation != nil {
				for _, c := range res.Validation.Checks {
					if !c.OK {
						t.Logf("  check %s [%s]: %s", c.Name, c.Level, c.Detail)
					}
				}
			}
		}
		t.Fatalf("Slice: %v", err)
	}
	if os.Getenv("BAMBU_KEEP_WORK") == "" {
		t.Cleanup(func() { res.Cleanup() })
	}
	return res
}

func checkResult(t *testing.T, res *Result, wantMachine string) {
	t.Helper()
	if res.ReturnCode != 0 || res.ExitCode != 0 {
		t.Fatalf("codes: return=%d exit=%d", res.ReturnCode, res.ExitCode)
	}
	if res.Validation == nil || !res.Validation.OK {
		t.Fatalf("validation: %+v", res.Validation)
	}
	if len(res.Plates) != 1 {
		t.Fatalf("plaques: %d", len(res.Plates))
	}
	p := res.Plates[0]
	if p.PredictedSeconds <= 0 || p.WeightGrams <= 0 || p.Layers <= 0 || p.ExtrudeMoves <= 0 {
		t.Fatalf("plaque: %+v", p)
	}
	if len(res.ArtifactSHA256) != 64 || res.ArtifactSize <= 0 {
		t.Fatalf("artefact: %+v", res.ArtifactSHA256)
	}
	if res.StudioVersion == "" || res.StudioVersion != res.BinaryVersion {
		t.Errorf("versions: archive=%q binaire=%q", res.StudioVersion, res.BinaryVersion)
	}
	if res.Machine.Name != wantMachine || res.Machine.Keys < 50 || len(res.Machine.Chain) < 2 {
		t.Errorf("machine effective: %+v", res.Machine)
	}
	if len(res.Command) < 8 || res.Duration <= 0 {
		t.Errorf("command/durée: %v %v", res.Command, res.Duration)
	}
	// l'archive se relit avec internal/mesh
	data, err := os.ReadFile(res.ArtifactPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mesh.ParseThreeMF(data); err != nil {
		t.Errorf("mesh ne relit pas l'archive: %v", err)
	}
	t.Logf("%s: %.0f s, %.2f g, %d couches, %v, archive %d o, sha %s…, studio %s",
		wantMachine, p.PredictedSeconds, p.WeightGrams, p.Layers, res.Duration.Round(time.Millisecond),
		res.ArtifactSize, res.ArtifactSHA256[:12], res.StudioVersion)
}

func TestRealP1SCube(t *testing.T) {
	s := realSlicer(t)
	model := writeSTL(t, "cube20.stl", mesh.Cube(20))
	res := sliceOK(t, s, Request{Model: model, Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}, Label: "cube20-p1s"})
	checkResult(t, res, p1sMachine)
	p := res.Plates[0]
	if p.Layers != 100 || p.WeightGrams < 3 || p.WeightGrams > 5 {
		t.Errorf("cube 20 mm @0.2: couches=%d poids=%.2f", p.Layers, p.WeightGrams)
	}
	if res.BedType != "Textured PEI Plate" {
		t.Errorf("plateau %q", res.BedType)
	}
	// le modèle d'entrée n'a pas changé et la copie est identique
	if sum, _ := fileSHA(model); sum != res.InputSHA256 {
		t.Error("empreinte d'entrée incohérente")
	}
}

func TestRealP1SCylinder(t *testing.T) {
	s := realSlicer(t)
	model := writeSTL(t, "cyl.stl", mesh.Cylinder(12, 30, 96))
	res := sliceOK(t, s, Request{Model: model, Printer: "Bambu Lab P1S", Nozzle: 0.4, Process: p1sProcess, Filaments: []string{p1sFilament}, Label: "cyl-p1s"})
	checkResult(t, res, p1sMachine)
	if res.Plates[0].Layers != 150 {
		t.Errorf("couches %d", res.Plates[0].Layers)
	}
}

func TestRealA1MiniCubeAndCylinder(t *testing.T) {
	s := realSlicer(t)
	for name, m := range map[string]*mesh.Mesh{"cube": mesh.Cube(25), "cyl": mesh.Cylinder(10, 25, 72)} {
		model := writeSTL(t, name+".stl", m)
		res := sliceOK(t, s, Request{Model: model, Machine: a1mMachine, Process: a1mProcess, Filaments: []string{a1mFilament}, Label: name + "-a1m"})
		checkResult(t, res, a1mMachine)
		if res.Archive.PrinterModel != "Bambu Lab A1 mini" {
			t.Errorf("modèle %q", res.Archive.PrinterModel)
		}
	}
}

func TestRealOverrideChangesOutput(t *testing.T) {
	s := realSlicer(t)
	model := writeSTL(t, "cube.stl", mesh.Cube(20))
	base := sliceOK(t, s, Request{Model: model, Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}, Label: "base"})
	ov := sliceOK(t, s, Request{Model: model, Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}, Label: "ov",
		Overrides: []Override{{Key: "layer_height", Value: 0.28}, {Key: "sparse_infill_density", Value: "5%"}}})
	checkResult(t, ov, p1sMachine)
	if ov.Plates[0].Layers >= base.Plates[0].Layers || ov.Plates[0].WeightGrams >= base.Plates[0].WeightGrams {
		t.Errorf("surcharge sans effet: base %+v / surchargé %+v", base.Plates[0], ov.Plates[0])
	}
	if len(ov.Overrides) != 2 || ov.Process.Source["layer_height"] != "override" {
		t.Errorf("surcharges non consignées: %+v", ov.Overrides)
	}
	if base.Process.SHA256 == ov.Process.SHA256 {
		t.Error("même empreinte de profil")
	}
	if base.Process.Source["layer_height"] == "override" {
		t.Error("provenance polluée")
	}
}

func TestRealArrangeOrient(t *testing.T) {
	s := realSlicer(t)
	model := writeSTL(t, "two.stl", mesh.TwoCubes(15, 5))
	yes := true
	res := sliceOK(t, s, Request{Model: model, Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}, Arrange: &yes, Orient: true, Label: "arr"})
	checkResult(t, res, p1sMachine)
}

func TestRealRejectsBeforeLaunch(t *testing.T) {
	s := realSlicer(t)
	model := writeSTL(t, "cube.stl", mesh.Cube(20))
	base := Request{Model: model, Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}}

	bad := base
	bad.Overrides = []Override{{Key: "no_such_key", Value: 1}}
	if res, err := s.Slice(context.Background(), bad); !errors.Is(err, ErrOverride) || res != nil {
		t.Fatalf("clé inconnue: %v", err)
	}
	bad.Overrides = []Override{{Key: "layer_height", Value: "épais"}}
	if _, err := s.Slice(context.Background(), bad); !errors.Is(err, ErrOverride) {
		t.Fatalf("type: %v", err)
	}
	bad = base
	bad.Process = a1mProcess // process A1 mini sur P1S
	if _, err := s.Slice(context.Background(), bad); !errors.Is(err, ErrIncompatible) {
		t.Fatalf("incompatible: %v", err)
	}
	bad = base
	bad.Filaments = []string{a1mFilament}
	if _, err := s.Slice(context.Background(), bad); !errors.Is(err, ErrIncompatible) {
		t.Fatalf("filament incompatible: %v", err)
	}
	bad = base
	bad.Model = filepath.Join(t.TempDir(), "absent.stl")
	if _, err := s.Slice(context.Background(), bad); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("modèle absent: %v", err)
	}
}

func TestRealStudioErrorOutOfBed(t *testing.T) {
	s := realSlicer(t)
	model := writeSTL(t, "huge.stl", mesh.Cube(400)) // plateau 256 mm
	res, err := s.Slice(context.Background(), Request{Model: model, Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}})
	if err == nil {
		t.Fatalf("un cube de 400 mm ne devrait pas tenir sur une P1S")
	}
	if os.Getenv("BAMBU_KEEP_WORK") == "" && res != nil {
		defer res.Cleanup()
	}
	var se *StudioError
	if !errors.As(err, &se) {
		t.Fatalf("erreur attendue de Studio, reçu: %v (stderr %q, result.json %s)", err, res.Stderr, res.StudioJSON)
	}
	t.Logf("code %d (%v): %s", se.Code, se.Kind, se.Message)
	if se.Code >= 0 || se.Kind == ErrStudioUnknown {
		t.Errorf("code non mappé: %+v", se)
	}
}

func TestRealTimeout(t *testing.T) {
	s := realSlicer(t)
	model := writeSTL(t, "cube.stl", mesh.Cube(20))
	res, err := s.Slice(context.Background(), Request{Model: model, Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}, Timeout: 150 * time.Millisecond})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("attendu ErrTimeout, reçu %v", err)
	}
	waitGone(t, res.PID)
	res.Cleanup()
}

func TestRealCancelDuringSlicing(t *testing.T) {
	s := realSlicer(t)
	model := writeSTL(t, "cube.stl", mesh.Cube(20))
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(250 * time.Millisecond); cancel() }()
	t0 := time.Now()
	res, err := s.Slice(ctx, Request{Model: model, Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}})
	if !errors.Is(err, ErrCanceled) || !errors.Is(err, context.Canceled) {
		t.Fatalf("attendu ErrCanceled, reçu %v", err)
	}
	if d := time.Since(t0); d > 8*time.Second {
		t.Errorf("annulation trop lente: %v", d)
	}
	if res.PID == 0 {
		t.Fatal("le processus n'a pas été lancé avant l'annulation")
	}
	waitGone(t, res.PID)
	if res.ArtifactPath != "" {
		t.Error("artefact annoncé malgré l'annulation")
	}
	res.Cleanup()
}

// waitGone vérifie que le processus n'existe plus.
func waitGone(t *testing.T, pid int) {
	t.Helper()
	if pid == 0 {
		return
	}
	for i := 0; i < 50; i++ {
		if !pidAlive(pid) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("le processus %d est encore vivant", pid)
}

func pidAlive(pid int) bool {
	if runtime.GOOS == "windows" {
		out, _ := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH").Output()
		return strings.Contains(string(out), " "+strconv.Itoa(pid)+" ")
	}
	p, err := os.FindProcess(pid)
	return err == nil && p.Signal(nil) == nil
}
