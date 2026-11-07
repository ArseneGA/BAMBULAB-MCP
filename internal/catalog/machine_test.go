package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
)

func installStore(t *testing.T) *profiles.Store {
	t.Helper()
	dir := os.Getenv("BAMBU_PROFILES_DIR")
	if dir == "" {
		dir = `C:\Program Files\Bambu Studio\resources\profiles\BBL`
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("profils Bambu Studio absents : %v", err)
	}
	s, err := profiles.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type fabView struct {
	Target struct {
		Name    string `json:"name"`
		Source  string `json:"source"`
		Volumes []any  `json:"nozzle_volumes"`
		Bed     struct {
			X1 float64 `json:"x1"`
		} `json:"bed"`
	} `json:"target"`
	Overhang struct {
		ThresholdDeg float64 `json:"threshold_deg"`
		Support      float64 `json:"support_threshold_angle_deg"`
		Source       string  `json:"threshold_source"`
	} `json:"overhang"`
}

func analyzeFab(t *testing.T, h *harness, id string, in AnalyzeInput) (*AnalyzeOutput, fabView) {
	t.Helper()
	in.ModelID = id
	a, err := h.svc.AnalyzeGeometry(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	var fv fabView
	if err := json.Unmarshal(a.Fabricability, &fv); err != nil {
		t.Fatalf("fabricabilité : %v (%s)", err, a.Fabricability)
	}
	return a, fv
}

func TestAnalyzeGeometryTargetsFromInstalledProfiles(t *testing.T) {
	st := installStore(t)
	h := newHarness(t, nil)
	h.svc.SetProfiles(func() *profiles.Store { return st })
	rep := h.importAll(ImportInput{Paths: []string{h.put("g/c.stl", cubeSTL(t, 10))}})
	h.idle()
	id := rep.Files[0].ModelID

	// H2D : double buse, volume par buse lu dans le profil (pas dans une table codée en dur)
	_, fv := analyzeFab(t, h, id, AnalyzeInput{Machine: "h2d"})
	if fv.Target.Source != mesh.TargetFromProfile || len(fv.Target.Volumes) != 2 || fv.Target.Bed.X1 != 350 {
		t.Fatalf("H2D : %+v", fv.Target)
	}
	// toutes les imprimantes de l'installation sont acceptées
	for _, m := range []string{"h2s", "x1e", "p2s", "a1", "a1mini", "x1c", "p1p", "Bambu Lab P1S"} {
		if _, fv := analyzeFab(t, h, id, AnalyzeInput{Machine: m}); fv.Target.Source != mesh.TargetFromProfile {
			t.Errorf("%s : %+v", m, fv.Target)
		}
	}
	// seuil de surplomb : support_threshold_angle du process par défaut de la machine
	_, fv = analyzeFab(t, h, id, AnalyzeInput{Machine: "p1s"})
	if fv.Overhang.Support <= 0 || fv.Overhang.ThresholdDeg != 90-fv.Overhang.Support || !strings.Contains(fv.Overhang.Source, "support_threshold_angle") {
		t.Fatalf("seuil du profil : %+v", fv.Overhang)
	}
	// process explicite, puis seuil explicite qui prime
	if _, fv = analyzeFab(t, h, id, AnalyzeInput{Machine: "p1s", Process: "0.20mm Standard @BBL X1C"}); !strings.Contains(fv.Overhang.Source, "0.20mm Standard @BBL X1C") {
		t.Errorf("process demandé : %+v", fv.Overhang)
	}
	if _, fv = analyzeFab(t, h, id, AnalyzeInput{Machine: "p1s", OverhangDeg: 50}); fv.Overhang.ThresholdDeg != 50 || fv.Overhang.Source != "réglage explicite" {
		t.Errorf("seuil explicite : %+v", fv.Overhang)
	}
	// machine inconnue : erreur qui liste les modèles
	if _, err := h.svc.AnalyzeGeometry(context.Background(), AnalyzeInput{ModelID: id, Machine: "ender3"}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "modèles") {
		t.Errorf("machine inconnue : %v", err)
	}
}

func TestAnalyzeGeometryNominalFallbackWithoutProfiles(t *testing.T) {
	h := newHarness(t, nil)
	h.svc.SetProfiles(func() *profiles.Store { return nil })
	rep := h.importAll(ImportInput{Paths: []string{h.put("g/c.stl", cubeSTL(t, 10))}})
	h.idle()
	a, fv := analyzeFab(t, h, rep.Files[0].ModelID, AnalyzeInput{Machine: "a1mini"})
	if fv.Target.Source != mesh.TargetNominal || !strings.Contains(string(a.Fabricability), "nominale") {
		t.Fatalf("le repli doit être marqué nominal : %+v", fv.Target)
	}
	if !strings.Contains(strings.Join(a.Caveats, " "), "profils Bambu Studio indisponibles") {
		t.Errorf("réserve sur le seuil de surplomb : %v", a.Caveats)
	}
}
