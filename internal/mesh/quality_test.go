package mesh

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

// prismXZ extrude un profil (x, z) sur depth selon Y (le profil est dans le plan XZ, Z vers le haut).
func prismXZ(poly []Vec2, depth float64) *Mesh {
	return Extrude(poly, depth).Transform(RotateX90).Transform(Translation(0, depth, 0))
}

func TestWeld32Properties(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const tol = 0.01
	var vs []Vec3
	for i := 0; i < 3000; i++ {
		base := Vec3{float64(rng.Intn(20)), float64(rng.Intn(20)), float64(rng.Intn(20))}
		// bruit < tol/2 autour d'un nœud entier : doit se souder ; certains pile sur les frontières de cellule
		vs = append(vs, base.Add(Vec3{(rng.Float64() - 0.5) * tol * 0.8, (rng.Float64() - 0.5) * tol * 0.8, (rng.Float64() - 0.5) * tol * 0.8}))
	}
	remap, n := weld32(vs, tol)
	reps := map[int32]Vec3{}
	for i, r := range remap {
		if _, ok := reps[r]; !ok {
			reps[r] = vs[i]
		}
		if d := reps[r].Sub(vs[i]); d.Len() > tol+1e-12 {
			t.Fatalf("sommet %d à %.4f de son représentant (> tol)", i, d.Len())
		}
	}
	if len(reps) != n {
		t.Fatalf("représentants %d != n %d", len(reps), n)
	}
	var all []Vec3
	for _, v := range reps {
		all = append(all, v)
	}
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			if all[i].Sub(all[j]).Len() <= tol {
				t.Fatalf("deux représentants à ≤ tol : %v %v", all[i], all[j])
			}
		}
	}
	if n > 20*20*20 || n < 100 {
		t.Fatalf("nombre de sommets soudés invraisemblable : %d", n)
	}
	// l'API publique reste équivalente
	r2, n2 := Weld(&Mesh{V: vs}, tol)
	if n2 != n || len(r2) != len(vs) {
		t.Fatalf("Weld public : %d vs %d", n2, n)
	}
}

func TestBVHNearestMatchesBruteForce(t *testing.T) {
	m := uvSphere(20, 30, 60)
	b := buildBVH(m)
	rng := rand.New(rand.NewSource(3))
	for k := 0; k < 300; k++ {
		o := Vec3{rng.Float64()*6 - 3, rng.Float64()*6 - 3, rng.Float64()*6 - 3}
		d := Vec3{rng.NormFloat64(), rng.NormFloat64(), rng.NormFloat64()}.Norm()
		got, _, gok := b.nearest(o, d, -1)
		want, wok := math.Inf(1), false
		for _, tr := range m.T {
			if h, ok := rayTri(o, d, m.V[tr[0]], m.V[tr[1]], m.V[tr[2]]); ok && h < want {
				want, wok = h, true
			}
		}
		if gok != wok || (gok && math.Abs(got-want) > 1e-9) {
			t.Fatalf("rayon %d : bvh %v/%v, brute %v/%v", k, got, gok, want, wok)
		}
	}
}

func TestSelfIntersectionCleanSphere(t *testing.T) {
	m := uvSphere(20, 40, 80)
	r := Analyze(m, AnalyzeOptions{SelfIntersectBudget: 10 * time.Second})
	if !r.SelfIntersectionsChecked || !r.SelfIntersectionsComplete || r.SelfIntersectingPairs != 0 {
		t.Fatalf("une sphère saine n'a pas d'auto-intersection : %+v", r.SelfIntersectingPairs)
	}
	// voisins par un sommet seulement (éventails des pôles) : pas de faux positif
	rep := FindSelfIntersections(m, SelfIntersectionOptions{})
	if rep.Pairs != 0 || !rep.Complete {
		t.Fatalf("FindSelfIntersections : %+v", rep)
	}
}

func TestSelfIntersectionRegionsAndBudget(t *testing.T) {
	// deux paires de cubes interpénétrés, loin l'une de l'autre : deux régions
	a := selfIntersecting()
	b := selfIntersecting().Transform(Translation(100, 0, 0))
	a.Append(b)
	rep := FindSelfIntersections(a, SelfIntersectionOptions{})
	if rep.RegionCount != 2 || !rep.Complete {
		t.Fatalf("2 régions attendues : %+v", rep)
	}
	// budget épuisé => rapport partiel annoncé
	big := uvSphere(20, 300, 600)
	rep = FindSelfIntersections(big, SelfIntersectionOptions{Budget: time.Nanosecond})
	if rep.Complete || rep.Note == "" || rep.Checked >= rep.Total {
		t.Fatalf("le budget doit borner le contrôle : %+v", rep)
	}
}

func TestOverhangThresholdFromProcessProfile(t *testing.T) {
	// face inférieure inclinée de ~26,6° depuis l'horizontale (63,4° depuis la verticale)
	wedge := prismXZ([]Vec2{{0, 10}, {20, 0}, {20, 20}, {0, 20}}, 20)
	run := func(o FabOptions) *FabReport {
		o.SkipOrientation = true
		r, err := Fabricability(wedge, o)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := run(FabOptions{SupportThresholdDeg: 30, ThresholdSource: "process « X »"}); r.Overhang.AreaMM2 <= 0 || r.Overhang.ThresholdDeg != 60 ||
		r.Overhang.SupportThresholdDeg != 30 || r.Overhang.Source != "process « X »" {
		t.Errorf("seuil 30° (profil) : %+v", r.Overhang)
	}
	if r := run(FabOptions{SupportThresholdDeg: 20}); r.Overhang.AreaMM2 != 0 || r.Overhang.ThresholdDeg != 70 {
		t.Errorf("seuil 20° (profil, plus tolérant) : %+v", r.Overhang)
	}
	if r := run(FabOptions{}); r.Overhang.ThresholdDeg != 45 || r.Overhang.AreaMM2 <= 0 || r.Overhang.Source == "" {
		t.Errorf("défaut : %+v", r.Overhang)
	}
	if r := run(FabOptions{OverhangAngleDeg: 80, SupportThresholdDeg: 30}); r.Overhang.AreaMM2 != 0 || r.Overhang.ThresholdDeg != 80 || r.Overhang.Source != "réglage explicite" {
		t.Errorf("le réglage explicite l'emporte : %+v", r.Overhang)
	}
}

func TestThicknessConeBeatsSingleRayOnWedge(t *testing.T) {
	const theta = 50.0
	h := 100 * math.Tan(theta*math.Pi/180)
	wedge := prismXZ([]Vec2{{0, 0}, {100, 0}, {100, h}}, 200)
	b := buildBVH(wedge)
	// point de la face inférieure (z=0, normale −Z) à x=60, au milieu de la profondeur
	p := Vec3{60, 100, 0}
	n := Vec3{0, 0, -1}
	trueT := 60 * math.Sin(theta*math.Pi/180) // distance de p à la face oblique
	cone, axis, _, ok := localThickness(b, p, n, -1, 1e-6, thinConeHalfAngleDeg, 0, thinRingRays)
	if !ok {
		t.Fatal("aucun rayon")
	}
	oldErr := (axis - trueT) / trueT
	newErr := (cone - trueT) / trueT
	if oldErr < 0.4 {
		t.Fatalf("le test suppose que l'ancienne méthode surestime fortement : axe %.2f vrai %.2f", axis, trueT)
	}
	if newErr < -0.02 || newErr > 0.2 || newErr >= oldErr/2 {
		t.Errorf("cône %.2f mm (erreur %+.0f %%) vs axe %.2f mm (%+.0f %%), vrai %.2f", cone, 100*newErr, axis, 100*oldErr, trueT)
	}
	// 1 seul rayon retrouve l'ancien comportement
	one, _, _, _ := localThickness(b, p, n, -1, 1e-6, 0, 0, 0)
	if math.Abs(one-axis) > 1e-9 {
		t.Errorf("un rayon : %v != axe %v", one, axis)
	}
}

func TestThicknessReportOnWedgeImproves(t *testing.T) {
	const theta = 50.0
	h := 100 * math.Tan(theta*math.Pi/180)
	wedge := prismXZ([]Vec2{{0, 0}, {100, 0}, {100, h}}, 200)
	r, err := Fabricability(wedge, FabOptions{SkipOrientation: true, ThinThresholdMM: 5})
	if err != nil {
		t.Fatal(err)
	}
	th := r.Thin
	if th.Rays != 1+thinRingRays || th.Sampling != "area_weighted" || th.Samples < 1000 {
		t.Fatalf("échantillonnage : %+v", th)
	}
	// l'arête vive est mince : la médiane (pondérée par l'aire) reste bien en deçà de l'épaisseur verticale maximale
	if th.MedianMM <= 0 || th.MedianMM > h {
		t.Errorf("médiane incohérente : %v", th.MedianMM)
	}
	if th.Confidence == "" || th.Agreement < 0 || th.Agreement > 1 {
		t.Errorf("confiance : %+v", th)
	}
}

func TestThicknessSamplesAreAreaWeighted(t *testing.T) {
	// plaque 100×100×1 + un petit cube 2×2×2 : ~tout l'échantillonnage tombe sur la plaque (aire 20 000 vs 24)
	plate := Box(0, 0, 0, 100, 100, 1)
	cube := Box(200, 0, 0, 202, 2, 2)
	plate.Append(cube)
	r, err := Fabricability(plate, FabOptions{SkipOrientation: true, ThinThresholdMM: 1.5, RaySamples: 500})
	if err != nil {
		t.Fatal(err)
	}
	if r.Thin.Samples < 450 {
		t.Fatalf("échantillons : %d", r.Thin.Samples)
	}
	// l'épaisseur de la plaque (1 mm) domine : ~100 % des échantillons sont minces
	if r.Thin.ThinFraction < 0.95 {
		t.Errorf("fraction mince %.2f (la plaque représente >99 %% de l'aire)", r.Thin.ThinFraction)
	}
}

func TestLargeMeshSamplingIsAnnounced(t *testing.T) {
	m := uvSphere(30, 60, 120) // ≈ 14 000 triangles
	r, err := Fabricability(m, FabOptions{MaxFullTriangles: 5000, SkipSelfIntersections: true})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Sampled || len(r.Sampling) == 0 || r.Orientation == nil {
		t.Fatalf("l'échantillonnage doit être annoncé : %+v", r.Sampling)
	}
	full, _ := Fabricability(m, FabOptions{SkipSelfIntersections: true})
	if full.Sampled {
		t.Error("pas d'échantillonnage sous le seuil")
	}
	// la sphère est isotrope : surface en surplomb et meilleure orientation concordent à quelques % près
	d := r.Orientation.Candidates[0].OverhangFrac - full.Orientation.Candidates[0].OverhangFrac
	if math.Abs(d) > 0.05 {
		t.Errorf("fraction de surplomb échantillonnée trop éloignée : %v", d)
	}
}
