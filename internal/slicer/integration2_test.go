package slicer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

func TestRealWorkDirWithSpacesAndAccents(t *testing.T) {
	s := realSlicer(t)
	s.cfg.WorkRoot = filepath.Join(t.TempDir(), "dossier avec espaces é")
	model := writeSTL(t, "pièce à tester.stl", mesh.Cube(15))
	res := sliceOK(t, s, Request{Model: model, Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}, Label: "pièce"})
	checkResult(t, res, p1sMachine)
}

func TestRealTwoFilamentSlots(t *testing.T) {
	s := realSlicer(t)
	model := writeSTL(t, "cube.stl", mesh.Cube(20))
	fils, err := s.Profiles().CompatibleFilaments(p1sMachine)
	if err != nil {
		t.Fatal(err)
	}
	petg := ""
	for _, f := range fils {
		if strings.Contains(f, "PETG") {
			petg = f
			break
		}
	}
	if petg == "" {
		t.Skip("aucun filament PETG compatible")
	}
	res := sliceOK(t, s, Request{Model: model, Machine: p1sMachine, Process: p1sProcess,
		Filaments: []string{p1sFilament, petg}, Label: "twofil"})
	checkResult(t, res, p1sMachine)
	if len(res.Plates[0].Filaments) != 1 || res.Plates[0].Filaments[0].Slot != 1 {
		t.Errorf("emplacements utilisés: %+v", res.Plates[0].Filaments)
	}
	if len(res.Archive.FilamentIDs) != 2 {
		t.Errorf("filaments de l'archive: %v", res.Archive.FilamentIDs)
	}
}

// Ré-slicer une archive produite par Studio (3MF natif Bambu) avec d'autres
// réglages : le scénario « modifier sur copie puis re-slicer ».
func TestReal3MFRoundTrip(t *testing.T) {
	s := realSlicer(t)
	model := writeSTL(t, "cube.stl", mesh.Cube(20))
	first := sliceOK(t, s, Request{Model: model, Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}, Label: "first"})
	second := sliceOK(t, s, Request{Model: first.ArtifactPath, Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}, Label: "second",
		Overrides: []Override{{Key: "layer_height", Value: 0.12}}})
	checkResult(t, second, p1sMachine)
	if second.Plates[0].Layers <= first.Plates[0].Layers {
		t.Errorf("couches %d puis %d", first.Plates[0].Layers, second.Plates[0].Layers)
	}
	// la source n'a pas été modifiée
	if sum, _ := fileSHA(first.ArtifactPath); sum != first.ArtifactSHA256 {
		t.Error("l'archive source a été modifiée")
	}
}

// Un 3MF synthétique (fixture de internal/mesh, sans les métadonnées Bambu)
// fait planter Studio 02.08.02.61 : l'erreur doit être nommée, jamais un succès.
func TestReal3MFSyntheticCrashIsNamed(t *testing.T) {
	s := realSlicer(t)
	model := filepath.Join("..", "mesh", "testdata", "multi_cm.3mf")
	if _, err := os.Stat(model); err != nil {
		t.Skip(err)
	}
	yes := true
	res, err := s.Slice(context.Background(), Request{Model: model, Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}, Arrange: &yes, Label: "multi"})
	if res != nil {
		defer res.Cleanup()
	}
	if err == nil {
		t.Logf("ce 3MF est accepté par cette version (objets %v)", res.Plates[0].Objects)
		checkResult(t, res, p1sMachine)
		return
	}
	if !errors.Is(err, ErrStudioCrashed) && !errors.Is(err, ErrNoResult) {
		var se *StudioError
		if !errors.As(err, &se) {
			t.Fatalf("erreur non nommée: %v", err)
		}
	}
	t.Logf("erreur nommée: %v", err)
}
