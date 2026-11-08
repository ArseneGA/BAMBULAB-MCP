package mesh

import (
	"strings"
	"testing"
)

// quatreIlotsDe3mm : cas réel de terrain (porte-guitare 201 × 215 × 204 mm) : un bloc posé sur quatre petits
// plots cylindriques de ≈ 3 mm de diamètre (≈ 7 mm² chacun) qui sont les seuls contacts avec le plateau.
func quatreIlotsDe3mm() *Mesh {
	m := Box(0, 0, 30, 201, 215, 204+30)
	for _, p := range [][2]float64{{20, 20}, {180, 20}, {20, 190}, {180, 190}} {
		m.Append(Cylinder(1.5, 30, 24).Transform(Translation(p[0], p[1], 0)))
	}
	return m
}

func TestAdhesionFourTinyIslandsUnderTallPart(t *testing.T) {
	r := Adhesion(quatreIlotsDe3mm())
	if r.Islands != 4 || r.TinyIslands != 4 {
		t.Fatalf("4 îlots minuscules attendus : %+v", r)
	}
	if r.SmallestIslandMM2 < 6 || r.SmallestIslandMM2 > 8 {
		t.Fatalf("îlot d'environ 7 mm² attendu : %+v", r)
	}
	if r.Level != AdhesionHigh {
		t.Fatalf("niveau high attendu : %+v", r)
	}
	var tiny bool
	for _, why := range r.Reasons {
		if strings.Contains(why, "de moins de 50 mm²") {
			tiny = true
		}
	}
	if !tiny {
		t.Fatalf("le critère d'aire minimale d'îlot doit figurer dans les raisons : %v", r.Reasons)
	}
	if !strings.Contains(r.Summary(), "raft_layers") {
		t.Fatalf("la recommandation doit citer le radeau : %q", r.Summary())
	}
}

// Un îlot minuscule seul (assez de contact par ailleurs pour que l'élancement soit faible) lève le critère
// d'aire minimale, mais pas sous une pièce basse.
func TestAdhesionTinyIslandCriterionIsRelativeToHeight(t *testing.T) {
	grand := Box(0, 0, 0, 100, 100, 60) // contact plein
	grand.Append(Box(110, 0, 0, 116, 6, 60))
	if r := Adhesion(grand); r.TinyIslands != 1 || r.Level != AdhesionHigh {
		t.Fatalf("îlot de 36 mm² sous 60 mm : %+v", r)
	}
	bas := Box(0, 0, 0, 100, 100, 30)
	bas.Append(Box(110, 0, 0, 116, 6, 30))
	if r := Adhesion(bas); r.Level != AdhesionOK {
		t.Fatalf("pièce basse (30 mm) : aucun risque attendu : %+v", r)
	}
}
