package strength

import (
	"bytes"
	"context"
	"image/png"
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/fem"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

func p3(x, y, z float64) mesh.Vec3 { return mesh.Vec3{X: x, Y: y, Z: z} }

// boxMesh : parallélépipède [0,x]×[0,y]×[0,z] fermé, normales sortantes.
func boxMesh(x, y, z float64) *mesh.Mesh {
	v := []mesh.Vec3{p3(0, 0, 0), p3(x, 0, 0), p3(x, y, 0), p3(0, y, 0), p3(0, 0, z), p3(x, 0, z), p3(x, y, z), p3(0, y, z)}
	t := []mesh.Tri{{0, 2, 1}, {0, 3, 2}, {4, 5, 6}, {4, 6, 7}, {0, 1, 5}, {0, 5, 4}, {1, 2, 6}, {1, 6, 5}, {2, 3, 7}, {2, 7, 6}, {3, 0, 4}, {3, 4, 7}}
	return &mesh.Mesh{V: v, T: t}
}

// extrude : polygone (x, z) extrudé sur [0, depth] en y, découpé en triangles par coupage d'oreilles.
func extrude(poly [][2]float64, depth float64) *mesh.Mesh {
	n := len(poly)
	m := &mesh.Mesh{}
	for _, p := range poly {
		m.V = append(m.V, p3(p[0], 0, p[1]))
	}
	for _, p := range poly {
		m.V = append(m.V, p3(p[0], depth, p[1]))
	}
	// orientation du polygone (aire signée) : on veut antihoraire dans le plan (x, z)
	area := 0.0
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		area += poly[i][0]*poly[j][1] - poly[j][0]*poly[i][1]
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	if area < 0 {
		for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
			idx[i], idx[j] = idx[j], idx[i]
		}
	}
	cross := func(a, b, c int) float64 {
		return (poly[b][0]-poly[a][0])*(poly[c][1]-poly[a][1]) - (poly[b][1]-poly[a][1])*(poly[c][0]-poly[a][0])
	}
	inside := func(a, b, c, p int) bool {
		return cross(a, b, p) >= 0 && cross(b, c, p) >= 0 && cross(c, a, p) >= 0
	}
	rem := append([]int(nil), idx...)
	var tris [][3]int
	for len(rem) > 3 {
		ok := false
		for i := range rem {
			a, b, c := rem[(i+len(rem)-1)%len(rem)], rem[i], rem[(i+1)%len(rem)]
			if cross(a, b, c) <= 0 {
				continue
			}
			bad := false
			for _, p := range rem {
				if p != a && p != b && p != c && inside(a, b, c, p) {
					bad = true
					break
				}
			}
			if bad {
				continue
			}
			tris = append(tris, [3]int{a, b, c})
			rem = append(rem[:i], rem[i+1:]...)
			ok = true
			break
		}
		if !ok {
			break
		}
	}
	tris = append(tris, [3]int{rem[0], rem[1], rem[2]})
	// face y = 0 : normale -y ; (x, z) antihoraire vu de +y... on teste l'orientation par le produit vectoriel
	for _, t := range tris {
		a, b, c := t[0], t[1], t[2]
		m.T = append(m.T, mesh.Tri{a + n, c + n, b + n}) // y = depth
		m.T = append(m.T, mesh.Tri{a, b, c})
	}
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		m.T = append(m.T, mesh.Tri{i, j, j + n}, mesh.Tri{i, j + n, i + n})
	}
	// orientation globale : volume algébrique positif
	vol := 0.0
	for _, t := range m.T {
		vol += m.V[t[0]].Dot(m.V[t[1]].Cross(m.V[t[2]]))
	}
	if vol < 0 {
		for i := range m.T {
			m.T[i][1], m.T[i][2] = m.T[i][2], m.T[i][1]
		}
	}
	return m
}

// subdivide : chaque triangle en quatre (plus de triangles, même forme).
func subdivide(m *mesh.Mesh) *mesh.Mesh {
	out := &mesh.Mesh{V: append([]mesh.Vec3(nil), m.V...)}
	mid := map[[2]int]int{}
	get := func(a, b int) int {
		if a > b {
			a, b = b, a
		}
		if i, ok := mid[[2]int{a, b}]; ok {
			return i
		}
		out.V = append(out.V, out.V[a].Add(out.V[b]).Scale(0.5))
		mid[[2]int{a, b}] = len(out.V) - 1
		return len(out.V) - 1
	}
	for _, t := range m.T {
		ab, bc, ca := get(t[0], t[1]), get(t[1], t[2]), get(t[2], t[0])
		out.T = append(out.T, mesh.Tri{t[0], ab, ca}, mesh.Tri{ab, t[1], bc}, mesh.Tri{ca, bc, t[2]}, mesh.Tri{ab, bc, ca})
	}
	return out
}

func userLoad(zone int, n float64) LoadIn { return LoadIn{Zone: zone, ForceN: n, Source: SourceUser} }

func zoneOfKind(zs *ZoneSet, kind string) int {
	for _, z := range zs.Zones {
		if z.Kind == kind {
			return z.ID
		}
	}
	return 0
}

// Console : flèche et contrainte maximale contre la résistance des matériaux, par le chemin complet (zones, matière).
func TestAssessCantileverVsBeamTheory(t *testing.T) {
	const L, b, h, P = 100.0, 12.0, 10.0, 60.0
	m := boxMesh(L, b, h)
	zs := DetectZones(m)
	wall, tip := zoneOfKind(zs, KindParoiXm), zoneOfKind(zs, KindParoiXp)
	if wall == 0 || tip == 0 {
		t.Fatalf("zones paroi -X / +X absentes : %+v", zs.Zones)
	}
	in := AssessInput{ModelID: "m", Material: "PETG", Infill: 100, Walls: 10, Duration: "occasional", SafetyFactor: 2, LayerOrientation: "z",
		Loads:    []LoadIn{userLoad(tip, P)},
		Supports: []SupportIn{{Zone: wall, Type: "fixed", Source: SourceUser}}}
	an, err := Run(context.Background(), m, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := an.Result
	e := r.Effective
	if math.Abs(e.SolidFrac-1) > 1e-9 {
		t.Fatalf("fraction de matière %v, attendu 1", e.SolidFrac)
	}
	I := b * h * h * h / 12
	G := e.EMPa / (2 * (1 + e.Nu))
	thDefl := P*L*L*L/(3*e.EMPa*I) + P*L/((5.0/6)*G*b*h)
	hv := r.Mesh.VoxelMM
	thSigma := 6 * P * (L - 1.5*hv) / (b * h * h) // à une couche et demie du mur (zone d'appui écartée)
	t.Logf("pas %.2f mm, %d cubes, %d it, %.2f s : flèche %.3f (théorie %.3f), σ %.2f (théorie %.2f), marge ×%.2f",
		hv, r.Mesh.Elements, r.Solver.Iterations, r.Solver.Seconds, r.MaxDisplacementMM, thDefl, r.MaxVonMisesMPa, thSigma, r.SafetyAchieved)
	if math.Abs(r.MaxDisplacementMM/thDefl-1) > 0.15 {
		t.Errorf("flèche %.3f mm, théorie %.3f mm (hors ±15 %%)", r.MaxDisplacementMM, thDefl)
	}
	if math.Abs(r.MaxVonMisesMPa/thSigma-1) > 0.15 {
		t.Errorf("contrainte %.2f MPa, théorie %.2f MPa (hors ±15 %%)", r.MaxVonMisesMPa, thSigma)
	}
	if !r.Solver.Converged || r.Verdict == "" || r.Disclaimer == "" {
		t.Errorf("résultat incomplet : %+v", r.Solver)
	}
	if rel := math.Abs(r.ReactionN-r.AppliedN) / r.AppliedN; rel > 0.01 {
		t.Errorf("équilibre : réaction %.2f N pour %.2f N appliqués", r.ReactionN, r.AppliedN)
	}
	if r.BasedOnAssumed {
		t.Error("toutes les entrées sont de l'humain : pas d'hypothèse attendue")
	}
	// anisotropie : la flexion tire dans le sens X ; si les couches sont perpendiculaires à X, c'est la liaison entre
	// couches qui travaille : bien moins favorable que couches perpendiculaires à Z (dans le plan)
	if !(r.Orientations["x"] < 0.8*r.Orientations["z"]) {
		t.Errorf("orientations : %+v (couches ⟂ X doivent être nettement plus faibles que ⟂ Z)", r.Orientations)
	}
	in.LayerOrientation = "x"
	if an2, err := Run(context.Background(), m, in, nil); err != nil {
		t.Fatal(err)
	} else if !(an2.Result.SafetyAchieved < 0.8*r.SafetyAchieved) || an2.Result.LimitingCriteria == "dans_le_plan" {
		t.Errorf("couches ⟂ X : marge ×%.2f (critère %s) contre ×%.2f couches ⟂ Z", an2.Result.SafetyAchieved, an2.Result.LimitingCriteria, r.SafetyAchieved)
	}
	in.LayerOrientation = "z"
	// rendus
	pngBytes, err := StressImage(an, "console")
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil || img.Bounds().Dx() < 800 {
		t.Fatalf("PNG invalide : %v", err)
	}
	if _, err := PlanImage(zs, "console"); err != nil {
		t.Fatal(err)
	}
}

func TestRefusesWithoutLoadsOrSupports(t *testing.T) {
	m := boxMesh(40, 10, 10)
	zs := DetectZones(m)
	wall := zoneOfKind(zs, KindParoiXm)
	cases := map[string]AssessInput{
		"sans charge":   {ModelID: "m", Supports: []SupportIn{{Zone: wall, Type: "fixed", Source: SourceUser}}},
		"sans appui":    {ModelID: "m", Loads: []LoadIn{userLoad(wall, 10)}},
		"sans rien":     {ModelID: "m"},
		"sans source":   {ModelID: "m", Loads: []LoadIn{{Zone: 1, ForceN: 10}}, Supports: []SupportIn{{Zone: wall, Type: "fixed", Source: SourceUser}}},
		"force nulle":   {ModelID: "m", Loads: []LoadIn{{Zone: 1, ForceN: 0, Source: SourceUser}}, Supports: []SupportIn{{Zone: wall, Type: "fixed", Source: SourceUser}}},
		"force absurde": {ModelID: "m", Loads: []LoadIn{{Zone: 1, ForceN: 1e9, Source: SourceUser}}, Supports: []SupportIn{{Zone: wall, Type: "fixed", Source: SourceUser}}},
		"appui inconnu": {ModelID: "m", Loads: []LoadIn{userLoad(1, 10)}, Supports: []SupportIn{{Zone: wall, Type: "mou", Source: SourceUser}}},
	}
	for name, in := range cases {
		if _, err := Run(context.Background(), m, in, nil); err == nil {
			t.Errorf("%s : refus attendu", name)
		}
	}
	if _, err := Run(context.Background(), m, AssessInput{ModelID: "m"}, nil); err != ErrNoInput {
		t.Errorf("refus sans charge ni hypothèse : %v", err)
	}
	// zone inexistante
	in := AssessInput{ModelID: "m", Loads: []LoadIn{userLoad(99, 10)}, Supports: []SupportIn{{Zone: wall, Type: "fixed", Source: SourceUser}}}
	if _, err := Run(context.Background(), m, in, nil); err == nil || !strings.Contains(err.Error(), "inexistante") {
		t.Errorf("zone inexistante : %v", err)
	}
}

// Les valeurs supposées sont acceptées, marquées et annoncées dans le verdict.
func TestAssumedInputsAreMarked(t *testing.T) {
	m := boxMesh(60, 10, 10)
	zs := DetectZones(m)
	wall, tip := zoneOfKind(zs, KindParoiXm), zoneOfKind(zs, KindParoiXp)
	in := AssessInput{ModelID: "m", // matière, remplissage, parois, orientation : défauts signalés
		Loads:    []LoadIn{{Zone: tip, MassKg: 4.5, Source: SourceAssumed, Note: "guitare 4,5 kg"}},
		Supports: []SupportIn{{Zone: wall, Type: "fixed", Source: SourceAssumed}}}
	an, err := Run(context.Background(), m, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := an.Result
	if !r.BasedOnAssumed || !strings.Contains(r.VerdictTxt, "HYPOTHÈSES") {
		t.Errorf("verdict sans mention des hypothèses : %s", r.VerdictTxt)
	}
	nAssumed, nDefault := 0, 0
	for _, in := range r.Inputs {
		switch in.Source {
		case SourceAssumed:
			nAssumed++
		case "default":
			nDefault++
		}
	}
	if nAssumed < 2 || nDefault < 4 {
		t.Errorf("entrées : %d supposées, %d par défaut\n%+v", nAssumed, nDefault, r.Inputs)
	}
	if math.Abs(r.AppliedN-4.5*Gravity) > 1e-6 {
		t.Errorf("masse non convertie en force : %v", r.AppliedN)
	}
	for _, w := range []string{"mesuré", "calculé", "estimé"} {
		if len(r.Measured)+len(r.Calculated)+len(r.Estimated) == 0 {
			t.Errorf("rien de classé (%s)", w)
		}
	}
	if !strings.Contains(r.Disclaimer, "ne remplace pas un essai réel") {
		t.Error("mention « ne remplace pas un essai réel » absente")
	}
}

// Numérotation stable : indépendante de l'ordre des triangles et des sommets.
func TestZoneNumberingStable(t *testing.T) {
	prof := [][2]float64{{0, 0}, {60, 0}, {60, 8}, {8, 8}, {8, 70}, {0, 70}}
	m := extrude(prof, 20)
	a := DetectZones(m)
	rng := rand.New(rand.NewSource(7))
	for k := 0; k < 5; k++ {
		sh := &mesh.Mesh{V: append([]mesh.Vec3(nil), m.V...), T: append([]mesh.Tri(nil), m.T...)}
		rng.Shuffle(len(sh.T), func(i, j int) { sh.T[i], sh.T[j] = sh.T[j], sh.T[i] })
		perm := rng.Perm(len(sh.V))
		nv := make([]mesh.Vec3, len(sh.V))
		for i, p := range perm {
			nv[p] = sh.V[i]
		}
		for i := range sh.T {
			for c := 0; c < 3; c++ {
				sh.T[i][c] = perm[sh.T[i][c]]
			}
		}
		sh.V = nv
		b := DetectZones(sh)
		if len(a.Zones) != len(b.Zones) {
			t.Fatalf("nombre de zones %d vs %d", len(a.Zones), len(b.Zones))
		}
		for i := range a.Zones {
			za, zb := a.Zones[i], b.Zones[i]
			if za.ID != zb.ID || za.Kind != zb.Kind || math.Abs(za.AreaMM2-zb.AreaMM2) > 1e-6 || math.Abs(za.Centroid[0]-zb.Centroid[0]) > 1e-6 {
				t.Fatalf("zone %d instable : %+v vs %+v", i+1, za, zb)
			}
		}
	}
	if len(a.Zones) < 5 {
		t.Errorf("équerre : %d zones seulement", len(a.Zones))
	}
	if zoneOfKind(a, KindBase) == 0 {
		t.Error("base non détectée")
	}
}

// Plafonds : le nombre de cubes pleins et la grille englobante restent bornés.
func TestSizeCaps(t *testing.T) {
	m := boxMesh(300, 200, 100) // 6e6 mm³ : pas ≈ 3,4 mm pour 150 k cubes
	zs := DetectZones(m)
	in := AssessInput{ModelID: "m", Material: "PLA", Infill: 100, Walls: 5, Duration: "occasional",
		Loads: []LoadIn{userLoad(zoneOfKind(zs, KindDessus), 50)}, Supports: []SupportIn{{Zone: zoneOfKind(zs, KindBase), Type: "resting", Source: SourceUser}}}
	an, err := Run(context.Background(), m, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := an.Result
	if r.Mesh.Elements > MaxSolidVoxels*12/10 || r.Mesh.GridCells > MaxGridCells {
		t.Errorf("plafond dépassé : %d cubes, grille %d", r.Mesh.Elements, r.Mesh.GridCells)
	}
	if len(an.Result.Warnings) > 0 {
		t.Logf("avertissements : %v", an.Result.Warnings)
	}
	t.Logf("bloc posé : %d cubes de %.2f mm, %d it, marge ×%.0f", r.Mesh.Elements, r.Mesh.VoxelMM, r.Solver.Iterations, r.SafetyAchieved)
	// plafond de grille englobante sur une pièce très creuse et très étendue : le pas est agrandi
	thin := boxMesh(2000, 2000, 2)
	pl := fem.ChoosePitch(thin, MaxSolidVoxels, MaxGridCells)
	if pl.CellsTotal > MaxGridCells {
		t.Errorf("grille englobante %d > %d", pl.CellsTotal, MaxGridCells)
	}
}

func TestLooksLoadBearing(t *testing.T) {
	yes := []string{"Support guitare", "porte-guitare mural", "crochet", "équerre 90", "étagère", "Bras de fixation", "charnière", "bracket", "Je veux qu'elle supporte 5 kg"}
	no := []string{"cube de calibration", "vase", "boîte à bijoux", "importer", "standard", "figurine dragon", "tour de température"}
	for _, s := range yes {
		if !LooksLoadBearing(s) {
			t.Errorf("%q devrait sembler porteuse", s)
		}
	}
	for _, s := range no {
		if LooksLoadBearing(s) {
			t.Errorf("%q ne devrait pas sembler porteuse", s)
		}
	}
}

func TestMaterialsAndExamples(t *testing.T) {
	for _, n := range []string{"PLA", "Bambu PETG HF", "ABS", "asa", "PA12-CF", "nylon", "TPU 95A", "pla basic"} {
		if _, ok := LookupMaterial(n); !ok {
			t.Errorf("matière %q non reconnue", n)
		}
	}
	if _, ok := LookupMaterial("bois"); ok {
		t.Error("« bois » ne doit pas être reconnu")
	}
	for _, m := range Materials {
		if m.Level != "estimation" || m.Source == "" || m.SigmaZ >= m.SigmaXY || m.TauZ <= 0 {
			t.Errorf("matière %s : niveau/source/anisotropie incohérents : %+v", m.Key, m)
		}
	}
	e, ok := MatchExample("porte-guitare")
	if !ok || e.Key != "guitare" || math.Abs(e.SuggestedForceN()-4.5*9.81*1.5) > 0.2 {
		t.Errorf("guitare : %+v", e)
	}
	// l'orientation des couches change la résistance retenue : Z plus faible
	eff := EffectiveProps(Materials[1], 100, 6, 1000, 600, false)
	if !(eff.SigmaZ < eff.SigmaXY) {
		t.Error("σz doit être inférieure à σxy")
	}
	low := EffectiveProps(Materials[1], 10, 2, 1e5, 1e4, true)
	if !(low.SigmaXY < eff.SigmaXY && low.EMPa < eff.EMPa) {
		t.Errorf("faible remplissage + fluage : résistance et rigidité doivent baisser (%v vs %v)", low, eff)
	}
}

func TestPlanProposalGuitarHolder(t *testing.T) {
	prof := [][2]float64{{0, 0}, {80, 0}, {80, 10}, {10, 10}, {10, 120}, {0, 120}}
	m := extrude(prof, 30)
	zs := DetectZones(m)
	ex, _ := MatchExample("porte-guitare")
	prop := BuildProposal(zs, "porte-guitare mural", &ex)
	if len(prop.Loads) != 1 || prop.Loads[0].Source != SourceAssumed || prop.Loads[0].ForceN < 60 {
		t.Errorf("charge proposée : %+v", prop.Loads)
	}
	if len(prop.Supports) != 1 || prop.Supports[0].Source != SourceAssumed || prop.Supports[0].Type != "fixed" {
		t.Errorf("appui mural attendu : %+v", prop.Supports)
	}
	qs := BuildQuestions(zs, "porte-guitare", KnownSettings{}, &ex)
	if len(qs) < 6 {
		t.Errorf("%d questions", len(qs))
	}
	var ids []string
	for _, q := range qs {
		ids = append(ids, q.ID)
	}
	for _, want := range []string{"usage", "charge", "ou_charge", "tenue", "duree_chocs", "matiere_reglages", "orientation"} {
		if !strings.Contains(strings.Join(ids, ","), want) {
			t.Errorf("question %s absente", want)
		}
	}
	// sans objet reconnu : aucune force inventée
	p2 := BuildProposal(zs, "pièce quelconque", nil)
	if len(p2.Loads) != 0 || p2.ForceN != 0 {
		t.Errorf("force inventée : %+v", p2)
	}
	if _, err := PlanImage(zs, "porte-guitare"); err != nil {
		t.Fatal(err)
	}
}

// Annulation : le calcul respecte le contexte.
func TestAssessCancel(t *testing.T) {
	m := boxMesh(100, 20, 20)
	zs := DetectZones(m)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	in := AssessInput{ModelID: "m", Material: "PLA", Infill: 100, Walls: 5, Duration: "occasional",
		Loads: []LoadIn{userLoad(zoneOfKind(zs, KindParoiXp), 50)}, Supports: []SupportIn{{Zone: zoneOfKind(zs, KindParoiXm), Type: "fixed", Source: SourceUser}}}
	done := make(chan error, 1)
	go func() { _, err := Run(ctx, m, in, nil); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("annulation ignorée")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("annulation trop lente")
	}
}
