package mesh

import (
	"errors"
	"math"
	"testing"
)

func mustRepair(t *testing.T, m *Mesh, o RepairOptions) (*Mesh, *RepairReport) {
	t.Helper()
	out, rep, err := Repair(m, o)
	if err != nil {
		t.Fatal(err)
	}
	return out, rep
}

func TestRepairFillsSquareHole(t *testing.T) {
	m := withHole()
	out, rep := mustRepair(t, m, RepairOptions{})
	if rep.Before.Watertight || rep.Before.OpenEdges != 4 {
		t.Fatalf("avant : %+v", rep.Before)
	}
	if !rep.After.Watertight || rep.After.Defects != 0 || rep.Actions.HolesFilled != 1 || rep.Actions.TrianglesAdded != 2 || !rep.Changed {
		t.Fatalf("après : %+v %+v", rep.After, rep.Actions)
	}
	near(t, "volume", rep.After.VolumeMM3, 1000, 1e-6)
	if len(m.T) != 10 {
		t.Fatal("l'entrée ne doit pas être modifiée")
	}
	if a := Analyze(out, AnalyzeOptions{}); !a.Watertight || a.InconsistentEdges != 0 || a.InvertedNormals {
		t.Fatalf("maillage réparé : %+v", a)
	}
}

func TestRepairFlipsInvertedCube(t *testing.T) {
	_, rep := mustRepair(t, flipAll(dirtyCube()), RepairOptions{})
	if rep.Actions.ComponentsInverted != 1 || rep.After.VolumeMM3 < 999 || !rep.After.Watertight {
		t.Fatalf("%+v %+v", rep.After, rep.Actions)
	}
}

func TestRepairReorientsOneFlippedFace(t *testing.T) {
	_, rep := mustRepair(t, flipOne(dirtyCube(), 4), RepairOptions{})
	if rep.Before.InconsistentEdges != 3 || rep.After.InconsistentEdges != 0 || rep.Actions.TrianglesFlipped != 1 {
		t.Fatalf("%+v %+v", rep.After, rep.Actions)
	}
	near(t, "volume", rep.After.VolumeMM3, 1000, 1e-6)
}

func TestRepairWeldsSoupAndRemovesDegenerateAndDuplicates(t *testing.T) {
	m := soup(dirtyCube())
	out, rep := mustRepair(t, m, RepairOptions{})
	if len(out.V) != 8 || rep.Actions.VerticesMerged != 28 || !rep.After.Watertight {
		t.Fatalf("soupe : %d sommets %+v", len(out.V), rep.Actions)
	}
	_, rep = mustRepair(t, degenerate(), RepairOptions{})
	if rep.Actions.DegenerateRemoved != 2 || rep.After.DegenerateTriangles != 0 {
		t.Fatalf("dégénérés : %+v", rep.Actions)
	}
	_, rep = mustRepair(t, duplicatedTriangle(), RepairOptions{})
	if rep.Actions.DuplicatesRemoved != 1 || !rep.After.Watertight || rep.After.NonManifoldEdges != 0 {
		t.Fatalf("doublons : %+v %+v", rep.Actions, rep.After)
	}
}

func TestRepairFloatingFragmentIsOptional(t *testing.T) {
	_, rep := mustRepair(t, floating(), RepairOptions{})
	if rep.After.Components != 2 || rep.Actions.FragmentsRemoved != 0 {
		t.Fatalf("sans option : %+v", rep.After)
	}
	out, rep := mustRepair(t, floating(), RepairOptions{RemoveFloating: true})
	if rep.After.Components != 1 || rep.Actions.FragmentsRemoved != 1 || len(out.T) != 12 || rep.After.FloatingFragments != 0 {
		t.Fatalf("avec option : %+v %+v", rep.After, rep.Actions)
	}
	// une pièce disjointe légitime n'est pas supprimée
	_, rep = mustRepair(t, TwoCubes(10, 5), RepairOptions{RemoveFloating: true})
	if rep.Actions.FragmentsRemoved != 0 || rep.After.Components != 2 {
		t.Fatalf("deux cubes : %+v", rep.Actions)
	}
}

func TestRepairLargeHoleRespectsLimit(t *testing.T) {
	s := uvSphere(10, 20, 40) // les 40 premiers triangles forment l'éventail du pôle
	m := &Mesh{V: s.V, T: s.T[40:]}
	_, rep := mustRepair(t, m, RepairOptions{})
	if rep.Actions.HolesFilled != 0 || rep.Actions.HolesSkipped != 1 || rep.After.Watertight || len(rep.Warnings) == 0 {
		t.Fatalf("trou de 40 arêtes > 32 : %+v %v", rep.Actions, rep.Warnings)
	}
	_, rep = mustRepair(t, m, RepairOptions{MaxHoleEdges: 64})
	if rep.Actions.HolesFilled != 1 || !rep.After.Watertight || rep.After.VolumeMM3 < 3800 {
		t.Fatalf("trou de 40 arêtes avec limite 64 : %+v %+v", rep.Actions, rep.After)
	}
	_, rep = mustRepair(t, m, RepairOptions{MaxHoleEdges: -1})
	if rep.Actions.HolesFilled != 0 || rep.Actions.HolesSkipped != 0 {
		t.Fatalf("comblement désactivé : %+v", rep.Actions)
	}
}

func TestRepairConcaveHoleByEarClipping(t *testing.T) {
	// prisme en L : on retire les triangles de la face haute (z = 5), trou concave de 6 arêtes
	l := Extrude([]Vec2{{0, 0}, {20, 0}, {20, 10}, {10, 10}, {10, 20}, {0, 20}}, 5)
	want := Analyze(l, AnalyzeOptions{}).Volume
	var drop []int
	for i := range l.T {
		n, a := l.Normal(i)
		if a > 0 && n.Z > 0.99 && l.V[l.T[i][0]].Z == 5 {
			drop = append(drop, i)
		}
	}
	if len(drop) != 4 {
		t.Fatalf("face haute en %d triangles", len(drop))
	}
	out, rep := mustRepair(t, dropTris(l, drop...), RepairOptions{})
	if rep.Actions.HolesFilled != 1 || rep.Actions.TrianglesAdded != 4 || !rep.After.Watertight {
		t.Fatalf("%+v %+v", rep.Actions, rep.After)
	}
	near(t, "volume du L", rep.After.VolumeMM3, want, 1e-6)
	// les triangles de comblement regardent vers le haut (prolongent l'orientation des voisins)
	up := 0.0
	for i := range out.T {
		n, a := out.Normal(i)
		if out.V[out.T[i][0]].Z == 5 && out.V[out.T[i][1]].Z == 5 && out.V[out.T[i][2]].Z == 5 && n.Z > 0.99 {
			up += a
		}
	}
	near(t, "aire de la face haute", up, 300, 1e-6)
}

func TestRepairCombinedDefects(t *testing.T) {
	m := soup(flipOne(withHole(), 3))
	m.Append(Cube(0.5).Transform(Translation(40, 40, 30)))
	out, rep := mustRepair(t, m, RepairOptions{RemoveFloating: true})
	if !rep.After.Watertight || rep.After.Defects != 0 || rep.After.Components != 1 || rep.After.Defects >= rep.Before.Defects {
		t.Fatalf("avant %+v après %+v actions %+v", rep.Before, rep.After, rep.Actions)
	}
	near(t, "volume", rep.After.VolumeMM3, 1000, 1e-6)
	if a := Analyze(out, AnalyzeOptions{}); a.Defects() != 0 {
		t.Fatalf("%+v", a)
	}
}

func TestRepairLeavesNonManifoldAndReportsIt(t *testing.T) {
	_, rep := mustRepair(t, nonManifold(), RepairOptions{})
	if rep.After.NonManifoldEdges == 0 || len(rep.Warnings) == 0 || rep.After.Watertight {
		t.Fatalf("non manifold : %+v %v", rep.After, rep.Warnings)
	}
}

func TestRepairEmptyAndIdempotent(t *testing.T) {
	if _, _, err := Repair(&Mesh{}, RepairOptions{}); !errors.Is(err, ErrEmpty) {
		t.Fatalf("maillage vide : %v", err)
	}
	out, _ := mustRepair(t, withHole(), RepairOptions{})
	out2, rep2 := mustRepair(t, out, RepairOptions{})
	if rep2.Changed || len(out2.T) != len(out.T) || math.Abs(rep2.After.VolumeMM3-1000) > 1e-6 {
		t.Fatalf("une seconde réparation ne doit rien changer : %+v", rep2.Actions)
	}
}
