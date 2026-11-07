package profiles

import (
	"errors"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

func rect(x0, y0, x1, y1 float64) []Point {
	return []Point{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}}
}

func TestVariantMeshTargetDualNozzle(t *testing.T) {
	v := Variant{Profile: "Test D2 0.4 nozzle", Nozzle: 0.4, Extruders: 2, BedX: 350, BedY: 320, PrintableZ: 325,
		BedArea:         rect(0, 0, 350, 320),
		ExtruderAreas:   [][]Point{rect(0, 0, 325, 320), rect(25, 0, 350, 320)},
		ExtruderHeights: []float64{320, 325}}
	tg := v.MeshTarget("Test D2")
	if tg.Source != mesh.TargetFromProfile || len(tg.Volumes) != 2 || tg.Volumes[0].HeightMM != 320 || tg.Volumes[1].Area.X0 != 25 {
		t.Fatalf("cible : %+v", tg)
	}
	if ok, _ := tg.Fits(mesh.Vec3{X: 320, Y: 300, Z: 320}); !ok {
		t.Error("320×300×320 doit tenir sur une buse")
	}
	if ok, _ := tg.Fits(mesh.Vec3{X: 340, Y: 300, Z: 100}); ok {
		t.Error("340 de large ne tient dans aucune buse seule")
	}
	if ok, _ := tg.FitsAllNozzles(mesh.Vec3{X: 320, Y: 320, Z: 100}); ok {
		t.Error("320 de large dépasse la zone commune (300)")
	}
	if ok, _ := tg.FitsAllNozzles(mesh.Vec3{X: 300, Y: 300, Z: 100}); !ok {
		t.Error("300 de large tient dans la zone commune")
	}
}

func TestVariantMeshTargetDualWithoutVolumesIsFlagged(t *testing.T) {
	v := Variant{Profile: "x", Nozzle: 0.4, Extruders: 2, BedX: 300, BedY: 300, PrintableZ: 300, BedArea: rect(0, 0, 300, 300)}
	tg := v.MeshTarget("X")
	if len(tg.Volumes) != 0 || len(tg.Notes) == 0 || !strings.Contains(tg.Notes[0], "double buse") {
		t.Fatalf("le volume par buse manquant doit être signalé : %+v", tg)
	}
}

func TestVariantMeshTargetExclusionCorner(t *testing.T) {
	v := Variant{Profile: "x", Nozzle: 0.4, Extruders: 1, BedX: 256, BedY: 256, PrintableZ: 256, BedArea: rect(0, 0, 256, 256),
		ExcludeAreas: [][]Point{rect(0, 0, 18, 28)}}
	tg := v.MeshTarget("X")
	if len(tg.Exclusions) != 1 {
		t.Fatalf("exclusions : %+v", tg.Exclusions)
	}
	if ok, _ := tg.Fits(mesh.Vec3{X: 256, Y: 228, Z: 10}); !ok {
		t.Error("256×228 tient (bande libre au-dessus du coin exclu)")
	}
	if ok, _ := tg.Fits(mesh.Vec3{X: 256, Y: 256, Z: 10}); ok {
		t.Error("256×256 ne tient pas : le coin est exclu")
	}
	if ok, _ := tg.Fits(mesh.Vec3{X: 238, Y: 256, Z: 10}); !ok {
		t.Error("238×256 tient (bande libre à droite du coin exclu)")
	}
}

// Sur l'installation locale de Bambu Studio : toutes les imprimantes ont une cible lue dans les profils.
func TestInstallTargets(t *testing.T) {
	s := loadInstall(t)
	ts, err := s.Targets()
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) < 10 {
		t.Fatalf("seulement %d cibles", len(ts))
	}
	for _, tg := range ts {
		if tg.Source != mesh.TargetFromProfile || tg.BedX <= 0 || tg.BedY <= 0 || tg.BedZ <= 0 || tg.Nozzle <= 0 {
			t.Errorf("cible incomplète : %+v", tg)
		}
	}
	h2d, err := s.TargetFor("h2d", 0)
	if err != nil {
		t.Fatal(err)
	}
	if h2d.BedX != 350 || h2d.BedY != 320 || h2d.Extruders != 2 || len(h2d.Volumes) != 2 {
		t.Fatalf("H2D : %+v", h2d)
	}
	if h2d.Volumes[0].Area.W() != 325 || h2d.Volumes[1].Area.X0 != 25 {
		t.Errorf("volumes par buse H2D : %+v", h2d.Volumes)
	}
	p1s, err := s.TargetFor("Bambu Lab P1S", 0.4)
	if err != nil || p1s.BedX != 256 || len(p1s.Exclusions) == 0 {
		t.Fatalf("P1S : %+v %v", p1s, err)
	}
	for _, alias := range []string{"x1c", "a1mini", "A1 mini", "x1e", "p2s", "h2s", "bambulab-p1p"} {
		if _, err := s.TargetFor(alias, 0); err != nil {
			t.Errorf("%s : %v", alias, err)
		}
	}
	if _, err := s.TargetFor("ender3", 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("machine inconnue : %v", err)
	}
	if _, err := s.TargetFor("p1s", 0.05); !errors.Is(err, ErrNotFound) {
		t.Errorf("buse inexistante : %v", err)
	}
	via, err := s.TargetForMachine("Bambu Lab P1S 0.4 nozzle")
	if err != nil || via.BedX != 256 {
		t.Errorf("TargetForMachine : %+v %v", via, err)
	}
}
