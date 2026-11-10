package strength

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// translateZ : copie du maillage décalée en Z (base « coupée à Z = dz »).
func translateZ(m *mesh.Mesh, dz float64) *mesh.Mesh {
	out := &mesh.Mesh{T: append([]mesh.Tri(nil), m.T...)}
	for _, v := range m.V {
		out.V = append(out.V, mesh.Vec3{X: v.X, Y: v.Y, Z: v.Z + dz})
	}
	return out
}

// porteGuitareCoupe : le porte-guitare en L (plaque de pose + montant), coupé à Z = 2 mm (plan de pose au-dessus de l'origine).
func porteGuitareCoupe() *mesh.Mesh {
	return translateZ(extrude([][2]float64{{0, 0}, {80, 0}, {80, 10}, {10, 10}, {10, 120}, {0, 120}}, 30), 2)
}

// FB8 : la surface de contact au sol est détectée comme UNE zone « plan de pose », même coupée à Z = 2 mm.
func TestGroundPlaneDetectedWhenCutAt2mm(t *testing.T) {
	m := porteGuitareCoupe()
	zs := DetectZones(m)
	g, ok := zs.Ground()
	if !ok {
		t.Fatalf("plan de pose non détecté : %+v", zs.Zones)
	}
	if g.Kind != KindBase || math.Abs(g.GroundZMM-2) > 0.3 {
		t.Errorf("plan de pose : %+v", g)
	}
	if want := 80.0 * 30; math.Abs(g.AreaMM2-want) > 0.02*want {
		t.Errorf("aire du plan de pose %.0f mm², attendu ≈ %.0f", g.AreaMM2, want)
	}
	if !strings.Contains(g.Label, "plan de pose détecté automatiquement") || !hasCand(g, "support_pose") {
		t.Errorf("libellé ou rôles : %q %v", g.Label, g.Candidates)
	}
	// numérotation stable et plan de pose proposé comme appui posé
	zs2 := DetectZones(m)
	g2, _ := zs2.Ground()
	if g2.ID != g.ID {
		t.Errorf("numérotation instable : %d / %d", g.ID, g2.ID)
	}
	prop := BuildProposal(zs, "porte-guitare posé au sol", nil)
	if len(prop.Supports) != 1 || prop.Supports[0].Zone != g.ID || prop.Supports[0].Type != "resting" || prop.Supports[0].Source != SourceAssumed {
		t.Errorf("appui proposé : %+v", prop.Supports)
	}
}

// Un plan de pose morcelé (deux pieds) forme une seule zone, et de petits détails plus bas ne le déplacent pas.
func TestGroundPlaneMergesFeet(t *testing.T) {
	prof := [][2]float64{{0, 0}, {20, 0}, {20, 5}, {60, 5}, {60, 0}, {80, 0}, {80, 10}, {0, 10}}
	m := translateZ(extrude(prof, 30), 2)
	zs := DetectZones(m)
	n := 0
	var g *Zone
	for i := range zs.Zones {
		if zs.Zones[i].Ground {
			n++
			g = &zs.Zones[i]
		}
	}
	if n != 1 {
		t.Fatalf("%d plan(s) de pose : %+v", n, zs.Zones)
	}
	if want := 2 * 20.0 * 30; math.Abs(g.AreaMM2-want) > 0.03*want {
		t.Errorf("aire %.0f mm², attendu ≈ %.0f (deux pieds)", g.AreaMM2, want)
	}
	// le dessous du pont (Z + 5) n'est pas le plan de pose
	if math.Abs(g.GroundZMM-2) > 0.3 {
		t.Errorf("Z du plan de pose %.2f", g.GroundZMM)
	}
}

// consoleSurSol : plaque de pose + colonne + bras en porte-à-faux, coupée à Z = 2 mm. Une charge vers le bas au bout du
// bras soulève l'arrière de la plaque : un appui « bilatéral » tirerait dessus, un appui posé doit décoller.
func consoleSurSol() *mesh.Mesh {
	return translateZ(extrude([][2]float64{{0, 0}, {80, 0}, {80, 10}, {10, 10}, {10, 120}, {0, 120}}, 30), 2)
}

func overturnInput(solid int) AssessInput {
	return AssessInput{ModelID: "m", Material: "PETG", Infill: 100, Walls: 6, Duration: "occasional", LayerOrientation: "z", maxSolidOverride: solid,
		Loads:    []LoadIn{{Point: []float64{76, 15, 12}, RadiusMM: 4, ForceN: 60, Direction: "down", Source: SourceAssumed}},
		Supports: []SupportIn{{Zone: 0, Type: "resting", Source: SourceAssumed}}}
}

// FB8 : appui posé sur le plan de pose détecté (zone 0) avec un effort qui fait basculer la pièce : l'appui ne doit pas
// « tirer » sur la pièce ; le contact unilatéral relâche les points qui tireraient et le rapport le dit.
func TestRestingOnGroundIsUnilateral(t *testing.T) {
	m := porteGuitareCoupe()
	zs := DetectZones(m)
	g, ok := zs.Ground()
	if !ok {
		t.Fatal("plan de pose non détecté")
	}
	an, err := Run(context.Background(), m, overturnInput(15000), nil)
	if err != nil {
		t.Fatal(err)
	}
	r := an.Result
	if len(an.Supp) != 1 || an.Supp[0].In.Zone != g.ID {
		t.Errorf("appui résolu sur la zone %+v, plan de pose = %d", an.Supp, g.ID)
	}
	c := r.Contact
	if c == nil {
		t.Fatalf("bilan de contact absent : %+v", r.Solver)
	}
	t.Logf("contact : %+v ; équilibre %.2f %% ; solveur %+v ; avertissements %v", *c, r.EquilibriumGapPct, r.Solver, r.Warnings)
	if c.TensionBeforePct < 5 || c.ReleasedNodes == 0 || c.Passes < 2 {
		t.Errorf("l'appui bilatéral devait tirer au premier calcul puis être relâché : %+v", *c)
	}
	if c.TensionAfterPct > 5 {
		t.Errorf("traction restante %.1f %% après contact unilatéral", c.TensionAfterPct)
	}
	if c.ContactAreaPct >= 100 || c.ContactAreaPct <= 0 {
		t.Errorf("surface de contact %.0f %%", c.ContactAreaPct)
	}
	if !r.Solver.Converged || r.NotConverged || r.Verdict == "non_converge" {
		t.Errorf("calcul converge attendu : %+v / %s", r.Solver, r.Verdict)
	}
	if r.EquilibriumGapPct > 2 {
		t.Errorf("écart d'équilibre %.2f %%", r.EquilibriumGapPct)
	}
	joined := strings.Join(r.Warnings, " | ")
	if !strings.Contains(joined, "contact unilatéral") {
		t.Errorf("le rapport doit signaler la traction aberrante et le contact unilatéral : %v", r.Warnings)
	}
	if !r.BasedOnAssumed {
		t.Error("charges et appuis supposés : le résultat doit rester marqué « assumed »")
	}
}

// Zone 0 refusée pour un appui fixe, et sans plan de pose pour un appui posé.
func TestSupportZoneZeroRules(t *testing.T) {
	m := porteGuitareCoupe()
	in := overturnInput(8000)
	in.Supports = []SupportIn{{Zone: 0, Type: "fixed", Source: SourceUser}}
	if _, err := Run(context.Background(), m, in, nil); err == nil || !strings.Contains(err.Error(), "zone obligatoire") {
		t.Errorf("appui fixe sans zone : %v", err)
	}
	in = overturnInput(8000)
	in.Resolution = "ultra"
	if _, err := Run(context.Background(), m, in, nil); err == nil || !strings.Contains(err.Error(), "resolution") {
		t.Errorf("résolution inconnue : %v", err)
	}
}

// FB9 : un calcul qui n'aboutit pas dans le temps imparti est rendu « non_converge », avec l'écart d'équilibre, et
// n'est jamais présenté comme un verdict.
func TestNonConvergedIsFlaggedNeverReliable(t *testing.T) {
	m := porteGuitareCoupe()
	in := overturnInput(15000)
	in.MaxSeconds = 0.0005 // échéance dépassée dès la voxélisation : une seule itération
	an, err := Run(context.Background(), m, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := an.Result
	t.Logf("%s | équilibre %.1f N / %.1f N (écart %.0f %%) ; %+v", r.VerdictTxt, r.ReactionN, r.AppliedN, r.EquilibriumGapPct, r.Solver)
	if r.Verdict != "non_converge" || !r.NotConverged || r.Solver.Converged {
		t.Fatalf("verdict %q, non convergé=%v, solveur %+v", r.Verdict, r.NotConverged, r.Solver)
	}
	if r.Solver.StopReason != "time_limit" || !r.Solver.TimedOut {
		t.Errorf("cause d'arrêt : %+v", r.Solver)
	}
	if r.EquilibriumGapPct < 2 || r.EquilibriumGapN <= 0 {
		t.Errorf("écart d'équilibre attendu : %.2f %% (%.2f N)", r.EquilibriumGapPct, r.EquilibriumGapN)
	}
	for _, want := range []string{"NON CONVERGÉ", "PAS FIABLE", "écart d'équilibre"} {
		if !strings.Contains(r.VerdictTxt, want) {
			t.Errorf("le verdict doit contenir %q : %s", want, r.VerdictTxt)
		}
	}
	if strings.Contains(r.VerdictTxt, "tient avec") || strings.Contains(r.VerdictTxt, "risque de casse") {
		t.Errorf("verdict présenté comme fiable : %s", r.VerdictTxt)
	}
	if len(r.Recommendations) == 0 || strings.Contains(strings.Join(r.Recommendations, " "), "Épaissir") {
		t.Errorf("recommandations : %v", r.Recommendations)
	}
	if !strings.Contains(strings.Join(r.Warnings, " "), "n'a pas atteint la précision") {
		t.Errorf("avertissement de non-convergence absent : %v", r.Warnings)
	}
	if verdictShort(r.Verdict) == verdictShort("ok") || !strings.Contains(verdictShort(r.Verdict), "NON FIABLE") {
		t.Errorf("rendu de la carte : %q", verdictShort(r.Verdict))
	}
	if _, err := StressImage(an, "non convergé"); err != nil {
		t.Fatal(err)
	}
	if !r.BasedOnAssumed {
		t.Error("hypothèses à conserver dans un résultat non convergé")
	}
}

// FB9 : le maillage standard converge vite sur un porte-guitare (la borne de temps n'est pas atteinte) et reste dans
// le budget ; le maillage fin est plafonné plus haut. Journalise le temps pour suivi.
func TestStandardResolutionConvergesWithinBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("banc de temps du maillage standard : hors -short")
	}
	m := porteGuitareCoupe()
	in := overturnInput(0) // maillage standard
	t0 := time.Now()
	an, err := Run(context.Background(), m, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	el := time.Since(t0)
	r := an.Result
	t.Logf("standard : %d cubes de %.2f mm, %d it, %d reprise(s), %.1f s", r.Mesh.Elements, r.Mesh.VoxelMM, r.Solver.Iterations, r.Solver.Passes, el.Seconds())
	if !r.Solver.Converged || r.Solver.TimedOut || r.Mesh.Elements > MaxSolidVoxels*12/10 {
		t.Errorf("solveur %+v, %d cubes", r.Solver, r.Mesh.Elements)
	}
	if el > 60*time.Second {
		t.Errorf("calcul standard trop long : %v", el)
	}
	if r.Solver.Precond == "" || r.Solver.BudgetSec != DefaultSolveSeconds {
		t.Errorf("préconditionneur / budget non renseignés : %+v", r.Solver)
	}
}
