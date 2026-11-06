package mesh

import (
	"math"
	"testing"
)

func TestFitsBed(t *testing.T) {
	r, err := Fabricability(Cube(10), FabOptions{Target: TargetP1S})
	if err != nil {
		t.Fatal(err)
	}
	if !r.FitsBed || len(r.Warnings) != 0 {
		t.Errorf("%+v", r.Warnings)
	}
	near(t, "marge x", r.MarginMM.X, 246, 1e-9)

	big := Box(0, 0, 0, 300, 100, 50)
	r, _ = Fabricability(big, FabOptions{Target: TargetP1S, SkipOrientation: true})
	if r.FitsBed || r.FitsBedRotatedZ || !containsStr(r.Warnings, "dépasse") {
		t.Errorf("trop grand : %+v", r)
	}
	// Tient seulement tournée : 200×250 sur A1 mini (180) ne tient pas ; 250×200 sur P1S tient directement ;
	// utilisons une cible rectangulaire.
	tgt := Target{"rect", 300, 100, 100, 0.4}
	r, _ = Fabricability(Box(0, 0, 0, 90, 280, 10), FabOptions{Target: tgt, SkipOrientation: true})
	if r.FitsBed || !r.FitsBedRotatedZ || !containsStr(r.Warnings, "90°") {
		t.Errorf("rotation : %+v", r)
	}
	r, _ = Fabricability(Box(0, 0, 0, 100, 100, 300), FabOptions{Target: TargetP1S, SkipOrientation: true})
	if r.FitsBed {
		t.Error("hauteur 300 > 256")
	}
	if _, err := Fabricability(&Mesh{}, FabOptions{}); err == nil {
		t.Error("maillage vide")
	}
}

func TestOverhangT(t *testing.T) {
	// T debout : fût 4×4, hauteur 10, barre 20 de large, 3 d'épaisseur, profondeur 10.
	m := TShape(4, 10, 20, 3, 10)
	a := Analyze(m, AnalyzeOptions{})
	if !a.Watertight || a.InconsistentEdges != 0 || a.Volume <= 0 {
		t.Fatalf("T invalide : %+v", a)
	}
	// Volume = fût 4×10 profil + barre 20×3, profondeur 10.
	near(t, "volume T", a.Volume, (4*10+20*3)*10, 1e-6)
	r, err := Fabricability(m, FabOptions{Target: TargetP1S, SkipOrientation: true})
	if err != nil {
		t.Fatal(err)
	}
	// Dessous de la barre de chaque côté du fût : (20−4)×10 = 160 mm².
	near(t, "aire de surplomb", r.Overhang.AreaMM2, 160, 1e-6)
	if r.Overhang.RegionCount != 2 || len(r.Overhang.Regions) != 2 {
		t.Fatalf("régions = %d", r.Overhang.RegionCount)
	}
	near(t, "région", r.Overhang.Regions[0].AreaMM2, 80, 1e-6)
	near(t, "angle", r.Overhang.Regions[0].MaxAngleDeg, 90, 1e-6)
	near(t, "z région", r.Overhang.Regions[0].BBox.Min.Z, 10, 1e-9)
	if !containsStr(r.Warnings, "surplomb") {
		t.Errorf("avertissement : %v", r.Warnings)
	}
	// Seuil à 89° : les plafonds horizontaux (90°) restent des surplombs ; seuil > 90 impossible.
	r2, _ := Fabricability(m, FabOptions{OverhangAngleDeg: 89, SkipOrientation: true})
	near(t, "seuil 89", r2.Overhang.AreaMM2, 160, 1e-6)
}

func TestNoOverhangShapes(t *testing.T) {
	for name, m := range map[string]*Mesh{"cube": Cube(10), "cylindre": Cylinder(5, 10, 32), "trou": BoxWithSquareHole(10, 4)} {
		r, _ := Fabricability(m, FabOptions{SkipOrientation: true})
		if r.Overhang.AreaMM2 != 0 {
			t.Errorf("%s : surplomb %.3f", name, r.Overhang.AreaMM2)
		}
	}
	// Cube incliné de 45° sur une arête : faces à ~45° — pas de surplomb au seuil 50°.
	rot := Cube(10).Transform(Matrix{1, 0, 0, 0, math.Sqrt2 / 2, math.Sqrt2 / 2, 0, -math.Sqrt2 / 2, math.Sqrt2 / 2, 0, 0, 0})
	rot = rot.Transform(Translation(0, 0, -rot.Bounds().Min.Z))
	r, _ := Fabricability(rot, FabOptions{OverhangAngleDeg: 50, SkipOrientation: true})
	if r.Overhang.AreaMM2 != 0 {
		t.Errorf("45° à seuil 50° : %.3f", r.Overhang.AreaMM2)
	}
	r, _ = Fabricability(rot, FabOptions{OverhangAngleDeg: 40, SkipOrientation: true})
	near(t, "45° à seuil 40° (deux faces basses)", r.Overhang.AreaMM2, 200, 1e-6)
}

func TestThinWalls(t *testing.T) {
	plate := Box(0, 0, 0, 20, 20, 0.5)
	r, _ := Fabricability(plate, FabOptions{Target: TargetP1S, SkipOrientation: true})
	near(t, "épaisseur min", r.Thin.MinMM, 0.5, 1e-6)
	near(t, "médiane pondérée par l'aire", r.Thin.MedianMM, 0.5, 1e-6)
	if r.Thin.ThinFraction < 0.9 || !containsStr(r.Warnings, "épaisseur estimée") {
		t.Errorf("plaque : %+v", r.Thin)
	}
	if r.Thin.Level != "heuristic" || r.Thin.Confidence == "" {
		t.Errorf("niveau/confiance : %+v", r.Thin)
	}
	r, _ = Fabricability(Cube(10), FabOptions{SkipOrientation: true})
	near(t, "cube médiane", r.Thin.MedianMM, 10, 1e-6)
	if r.Thin.ThinCount != 0 || r.Thin.Confidence != "medium" { // 12 échantillons : confiance moyenne
		t.Errorf("cube : %+v", r.Thin)
	}
	// Cube troué : paroi de 3 mm sur les faces du trou.
	h, _ := Fabricability(BoxWithSquareHole(10, 4), FabOptions{SkipOrientation: true})
	near(t, "trou min", h.Thin.MinMM, 3, 1e-6)
	// Normales inversées : même résultat.
	inv := Cube(10)
	for i := range inv.T {
		inv.T[i][1], inv.T[i][2] = inv.T[i][2], inv.T[i][1]
	}
	ri, _ := Fabricability(inv, FabOptions{SkipOrientation: true})
	near(t, "inversé médiane", ri.Thin.MedianMM, 10, 1e-6)
	// Maillage ouvert : confiance basse ou moyenne, jamais haute, avertissement.
	open := Cube(10)
	open.T = open.T[2:]
	ro, _ := Fabricability(open, FabOptions{SkipOrientation: true})
	if ro.Thin.Confidence == "high" || !containsStr(ro.Warnings, "non étanche") {
		t.Errorf("ouvert : %+v %v", ro.Thin, ro.Warnings)
	}
	// Échantillonnage borné.
	cyl := Cylinder(10, 20, 500)
	rc, _ := Fabricability(cyl, FabOptions{RaySamples: 100, SkipOrientation: true})
	if rc.Thin.Samples > 110 || rc.Thin.Samples < 50 {
		t.Errorf("échantillons = %d", rc.Thin.Samples)
	}
}

func TestOrientationT(t *testing.T) {
	m := TShape(4, 10, 20, 3, 10)
	r, err := Fabricability(m, FabOptions{Target: TargetP1S})
	if err != nil {
		t.Fatal(err)
	}
	o := r.Orientation
	if o == nil || o.Level != "heuristic" || o.ScoreFormula == "" || len(o.Candidates) < 6 {
		t.Fatalf("orientation : %+v", o)
	}
	best := o.Candidates[o.Best]
	// La meilleure orientation n'a aucun surplomb (T couché ou retourné), contrairement à l'actuelle.
	near(t, "surplomb de la meilleure", best.OverhangMM2, 0, 1e-9)
	if best.Source == "current" {
		t.Errorf("meilleure = actuelle (%s)", best.Label)
	}
	// Le T retourné (barre sur le plateau) est candidat : contact 20×10, hauteur 13.
	flipped := -1
	for i, c := range o.Candidates {
		if c.Up.Z < -0.99 {
			flipped = i
		}
	}
	if flipped < 0 {
		t.Fatal("T retourné absent")
	}
	near(t, "contact retourné", o.Candidates[flipped].ContactMM2, 200, 1e-6)
	near(t, "hauteur retournée", o.Candidates[flipped].HeightMM, 13, 1e-9)
	near(t, "surplomb retourné", o.Candidates[flipped].OverhangMM2, 0, 1e-9)
	// L'orientation actuelle est présente et plus mal classée.
	cur := -1
	for i, c := range o.Candidates {
		if c.Source == "current" {
			cur = i
		}
	}
	if cur <= o.Best {
		t.Errorf("actuelle en %d, meilleure en %d", cur, o.Best)
	}
	// La rotation proposée envoie bien « up » sur +Z.
	p := best.Rotation.Apply(best.Up)
	near(t, "rotation up→z", p.Z, 1, 1e-9)
	near(t, "rotation x", p.X, 0, 1e-9)
	rm := m.Transform(best.Rotation)
	near(t, "hauteur après rotation", rm.Bounds().Size.Z, best.HeightMM, 1e-9)
	// Trié par score croissant.
	for i := 1; i < len(o.Candidates); i++ {
		if o.Candidates[i].Score < o.Candidates[i-1].Score {
			t.Error("non trié")
		}
	}
}

func TestOrientationHullFaces(t *testing.T) {
	// Un prisme incliné : la coque a une grande face oblique absente des 6 axes.
	tilt := Cube(10).Transform(Matrix{1, 0, 0, 0, 0.8, 0.6, 0, -0.6, 0.8, 0, 0, 0})
	r, _ := Fabricability(tilt, FabOptions{Target: TargetP1S})
	hull := 0
	for _, c := range r.Orientation.Candidates {
		if c.Source == "hull" {
			hull++
		}
	}
	if hull == 0 {
		t.Fatal("aucune face de coque candidate")
	}
	best := r.Orientation.Candidates[0]
	near(t, "cube incliné remis à plat : surplomb", best.OverhangMM2, 0, 1e-6)
	near(t, "contact 100", best.ContactMM2, 100, 1e-6)

	cl := principalHullFaces(Cube(10).V, 10, math.Cos(2*math.Pi/180))
	if len(cl) != 6 {
		t.Fatalf("faces de coque du cube : %d", len(cl))
	}
	for _, c := range cl {
		near(t, "aire de face de coque", c.area, 100, 1e-6)
	}
	// Cylindre : bases = plus grandes faces (π r² approx) — haut et bas.
	cyl := principalHullFaces(Cylinder(5, 10, 32).V, 2, math.Cos(2*math.Pi/180))
	if len(cyl) != 2 || math.Abs(cyl[0].normal.Z) < 0.999 {
		t.Errorf("cylindre : %+v", cyl)
	}
	// Points coplanaires : pas d'enveloppe.
	if f, ok := convexHull([]Vec3{{0, 0, 0}, {1, 0, 0}, {0, 1, 0}, {1, 1, 0}}, 1e-9); ok || f != nil {
		t.Error("coplanaire")
	}
	if len(principalHullFaces(nil, 6, 0.99)) != 0 {
		t.Error("vide")
	}
}

func TestOrientationTooBig(t *testing.T) {
	// Poutre 300×20×20 : ne tient qu'avec Z en largeur ; aucune orientation ne tient sur A1 mini.
	r, _ := Fabricability(Box(0, 0, 0, 300, 20, 20), FabOptions{Target: TargetA1Mini})
	for _, c := range r.Orientation.Candidates {
		if c.Fits {
			t.Errorf("%s ne devrait pas tenir", c.Label)
		}
	}
	// Sur P1S (256) : elle ne tient pas non plus en 300 ; en diagonale non plus (heuristique, axes seuls).
	r, _ = Fabricability(Box(0, 0, 0, 20, 20, 300), FabOptions{Target: TargetP1S})
	if r.FitsBed {
		t.Error("hauteur 300")
	}
	best := r.Orientation.Candidates[0]
	if best.Fits {
		t.Error("une poutre de 300 mm ne tient pas sur P1S")
	}
	// Mais une poutre 240×20×20 debout (240 < 256) tient, et la meilleure orientation tient.
	r, _ = Fabricability(Box(0, 0, 0, 20, 20, 240), FabOptions{Target: TargetP1S})
	if !r.Orientation.Candidates[0].Fits {
		t.Error("la meilleure doit tenir")
	}
}

func TestWeldAndDSU(t *testing.T) {
	m := &Mesh{V: []Vec3{{0, 0, 0}, {1e-7, 0, 0}, {1, 0, 0}, {0, 0, 1e-7}}}
	remap, n := Weld(m, 1e-6)
	if n != 2 || remap[0] != remap[1] || remap[0] != remap[3] || remap[2] == remap[0] {
		t.Errorf("weld : n=%d remap=%v", n, remap)
	}
	// Voisinage de cellules : deux points de part et d'autre d'une frontière de cellule.
	m = &Mesh{V: []Vec3{{0.99999999, 0, 0}, {1.00000001, 0, 0}}}
	if _, n := Weld(m, 1e-3); n != 1 {
		t.Error("frontière de cellule")
	}
}
