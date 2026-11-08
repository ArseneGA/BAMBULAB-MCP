package mesh

import (
	"strings"
	"testing"
)

// grandSurTroisPieds : bloc de 201 x 215 x 204 mm posé sur trois petits appuis de 15 x 15 mm, creux dessous (cas réel décollé).
func grandSurTroisPieds() *Mesh {
	m := Box(0, 0, 30, 201, 215, 204)
	for _, p := range [][2]float64{{10, 10}, {176, 10}, {93, 190}} {
		m.Append(Box(p[0], p[1], 0, p[0]+15, p[1]+15, 30))
	}
	return m
}

func TestAdhesionCubeIsOK(t *testing.T) {
	r := Adhesion(Cube(40))
	if r.Level != AdhesionOK || r.Islands != 1 || r.Slenderness > 1.1 {
		t.Fatalf("cube posé : %+v", r)
	}
	if r.Summary() != "" {
		t.Fatalf("aucun avertissement attendu : %q", r.Summary())
	}
}

func TestAdhesionBigPartOnThreeSmallFeet(t *testing.T) {
	r := Adhesion(grandSurTroisPieds())
	if r.Level != AdhesionHigh {
		t.Fatalf("niveau %s attendu high : %+v", r.Level, r)
	}
	if r.Islands != 3 || r.ContactAreaMM2 < 600 || r.ContactAreaMM2 > 700 {
		t.Fatalf("3 îlots de 225 mm² attendus : %+v", r)
	}
	if r.Slenderness < 7 {
		t.Fatalf("élancement : %+v", r)
	}
	if r.Recommendation == "" || !strings.Contains(r.Summary(), "outer_only") {
		t.Fatalf("recommandation de bordure attendue : %q", r.Summary())
	}
}

func TestAdhesionFlatPlateIsOK(t *testing.T) {
	if r := Adhesion(Box(0, 0, 0, 200, 150, 3)); r.Level != AdhesionOK {
		t.Fatalf("plaque plate : %+v", r)
	}
}

func TestAdhesionSmallTallPeg(t *testing.T) {
	r := Adhesion(Box(0, 0, 0, 8, 8, 60))
	if r.Level != AdhesionHigh || r.SmallIslands != 1 {
		t.Fatalf("pion : %+v", r)
	}
}

func TestFabricabilityReportsAdhesion(t *testing.T) {
	rep, err := Fabricability(grandSurTroisPieds(), FabOptions{SkipOrientation: true, SkipSelfIntersections: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Adhesion == nil || rep.Adhesion.Level != AdhesionHigh {
		t.Fatalf("adhesion : %+v", rep.Adhesion)
	}
	found := false
	for _, w := range rep.Warnings {
		if strings.HasPrefix(w, "Risque de décollement") {
			found = true
		}
	}
	if !found {
		t.Fatalf("avertissement absent : %v", rep.Warnings)
	}
}
