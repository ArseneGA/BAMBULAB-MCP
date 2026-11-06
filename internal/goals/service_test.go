package goals

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

func TestSetGoalsPersistsAndRoundTrips(t *testing.T) {
	e := newEnv(t, nil, nil, false)
	mdl, _ := e.addModel("pièce", mesh.Cube(20))
	in := strengthSpeed("xy", false)
	in.ModelID = mdl
	g, err := e.svc.SetGoals(e.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.GetGoal(e.ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OriginalText != in.OriginalText || len(got.Criteria) != 2 || len(got.Conflicts) != 1 {
		t.Fatalf("aller-retour : %+v", got)
	}
	if _, err := e.svc.GetGoal(e.ctx, "goal_inconnu"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("objectif inconnu : %v", err)
	}
	in.ModelID = "mdl_inconnu"
	if _, err := e.svc.SetGoals(e.ctx, in); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("modèle inconnu : %v", err)
	}
}

// experimentNoSlicing crée une expérience sans Studio : les candidats restent « pending ».
func experimentNoSlicing(t *testing.T, e *env, modelID string, goal GoalInput) (*Experiment, *Goal) {
	t.Helper()
	g, err := e.svc.SetGoals(e.ctx, goal)
	if err != nil {
		t.Fatal(err)
	}
	x, err := e.svc.CreateExperiment(e.ctx, CreateExperimentInput{ModelID: modelID, GoalID: g.ID,
		Base: BaseSpec{Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}}, Candidates: 3})
	if err != nil {
		t.Fatal(err)
	}
	return x, g
}

func TestApplyVariantStaleRevision(t *testing.T) {
	st := installProfiles(t)
	e := newEnv(t, st, nil, false)
	mdl, ver := e.addModel("support", mesh.TShape(30, 40, 60, 8, 20))
	x, _ := experimentNoSlicing(t, e, mdl, strengthSpeed("xy", false))
	if x.ModelVersionID != ver || len(x.Candidates) != 4 {
		t.Fatalf("expérience : version %s, %d candidats", x.ModelVersionID, len(x.Candidates))
	}
	cand := x.Candidates[1]
	if cand.State != "pending" || len(cand.Overrides) == 0 {
		t.Fatalf("candidat : %+v", cand)
	}
	// pas encore découpé : refus
	if _, err := e.svc.ApplyVariant(e.ctx, ApplyInput{ExperimentID: x.ID, CandidateID: cand.ID, ExpectedRevision: ver}); !errors.Is(err, ErrNotReady) {
		t.Fatalf("variante non découpée : ErrNotReady attendue, %v", err)
	}
	e.svc.setCandidate(e.ctx, cand.ID, "succeeded", "", &CandMetrics{Values: map[string]Metric{}, Effective: map[string]any{}}, "")
	// mauvaise révision attendue
	_, err := e.svc.ApplyVariant(e.ctx, ApplyInput{ExperimentID: x.ID, CandidateID: cand.ID, ExpectedRevision: "ver_autre"})
	var se *StaleError
	if !errors.Is(err, ErrStale) || !errors.As(err, &se) || se.Current != ver {
		t.Fatalf("révision attendue erronée : ErrStale avec la révision courante, %v", err)
	}
	// le modèle évolue après l'expérience : refus, même avec la bonne révision figée
	_, ver2 := e.addFile("support-v2", "stl", mustSTL(t, mesh.Cube(25)), mdl)
	_, err = e.svc.ApplyVariant(e.ctx, ApplyInput{ExperimentID: x.ID, CandidateID: cand.ID, ExpectedRevision: ver})
	if !errors.As(err, &se) || se.Current != ver2 {
		t.Fatalf("modèle modifié depuis l'expérience : ErrStale (courante %s), %v", ver2, err)
	}
	if a, _ := e.svc.AppliedFor(e.ctx, mdl); a != nil {
		t.Fatal("rien ne doit avoir été appliqué")
	}
	// expérience sur la version courante : l'application réussit
	x2, _ := experimentNoSlicing(t, e, mdl, strengthSpeed("xy", false))
	if x2.ModelVersionID != ver2 {
		t.Fatalf("la nouvelle expérience doit figer %s : %s", ver2, x2.ModelVersionID)
	}
	c2 := x2.Candidates[1]
	e.svc.setCandidate(e.ctx, c2.ID, "succeeded", "", &CandMetrics{Values: map[string]Metric{}, Effective: map[string]any{}}, "")
	out, err := e.svc.ApplyVariant(e.ctx, ApplyInput{ExperimentID: x2.ID, CandidateID: c2.ID, ExpectedRevision: ver2})
	if err != nil {
		t.Fatal(err)
	}
	if out.ProfileRevision == "" || len(out.Overrides) != len(c2.Overrides) {
		t.Fatalf("application : %+v", out)
	}
	if a, _ := e.svc.AppliedFor(e.ctx, mdl); a == nil || a.ID != out.ProfileRevision || a.Origin == nil || a.Origin.CandidateID != c2.ID {
		t.Fatalf("variante appliquée relue : %+v", a)
	}
}

func mustSTL(t *testing.T, m *mesh.Mesh) []byte {
	t.Helper()
	b, err := mesh.STLBytes(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCreateExperimentRefusals(t *testing.T) {
	st := installProfiles(t)
	e := newEnv(t, st, nil, false)
	mdl, ver := e.addModel("pièce", mesh.Cube(20))
	g, _ := e.svc.SetGoals(e.ctx, strengthSpeed("xy", false))
	base := BaseSpec{Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}}

	// révision attendue périmée
	_, err := e.svc.CreateExperiment(e.ctx, CreateExperimentInput{ModelID: mdl, GoalID: g.ID, Base: base, ExpectedModelRevision: "ver_vieille"})
	if !errors.Is(err, ErrStale) {
		t.Fatalf("ErrStale attendue : %v", err)
	}
	// objectif PETG, filament de base PLA : aucun transfert entre matériaux
	gp, _ := e.svc.SetGoals(e.ctx, GoalInput{OriginalText: "pièce PETG", Material: "PETG", Criteria: []CriterionInput{{Type: "speed"}}})
	_, err = e.svc.CreateExperiment(e.ctx, CreateExperimentInput{ModelID: mdl, GoalID: gp.ID, Base: base})
	if !errors.Is(err, ErrIncompatible) || !strings.Contains(err.Error(), "aucun paramètre n'est transféré") {
		t.Fatalf("matériau incompatible : %v", err)
	}
	// sans filament explicite, le filament est choisi dans la famille de l'objectif
	x, err := e.svc.CreateExperiment(e.ctx, CreateExperimentInput{ModelID: mdl, GoalID: gp.ID, Base: BaseSpec{Machine: p1sMachine}, Candidates: 2})
	if err != nil {
		t.Fatal(err)
	}
	if x.Details.Base.Family != "PETG" || !strings.Contains(x.Details.Base.Filaments[0], "PETG") {
		t.Fatalf("filament PETG attendu : %+v", x.Details.Base)
	}
	// STEP seul : non découpable
	stepModel, _ := e.addFile("pièce_step", "step", []byte("ISO-10303-21;"), "")
	_, err = e.svc.CreateExperiment(e.ctx, CreateExperimentInput{ModelID: stepModel, GoalID: g.ID, Base: base})
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("STEP seul : ErrNotReady attendue, %v", err)
	}
	// plafond de variantes
	big, err := e.svc.CreateExperiment(e.ctx, CreateExperimentInput{ModelID: mdl, GoalID: g.ID, Base: base, Candidates: 50, Budget: Budget{MaxCandidates: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if len(big.Candidates) > 3 {
		t.Fatalf("budget de 2 variantes : %d candidats", len(big.Candidates))
	}
	_ = ver
	// variante personnalisée invalide
	_, err = e.svc.CreateExperiment(e.ctx, CreateExperimentInput{ModelID: mdl, GoalID: g.ID, Base: base,
		Custom: []CustomCandidate{{Label: "faux", Overrides: []slicer.Override{{Key: "wall_loops", Value: "beaucoup"}}}}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("surcharge de type invalide : %v", err)
	}
}

func TestModifyProfileValidationDiffAndRevisions(t *testing.T) {
	st := installProfiles(t)
	e := newEnv(t, st, nil, false)
	base := &BaseSpec{Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}}
	r1, err := e.svc.ModifyProfile(e.ctx, ModifyInput{Base: base, Modifications: []slicer.Override{{Key: "wall_loops", Value: "4"}, {Key: "layer_height", Value: 0.16}}})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Revision == "" || r1.ParentRevision != "" || len(r1.Diff) != 2 {
		t.Fatalf("première révision : %+v", r1.Diff)
	}
	if r1.Process.Config["wall_loops"] != "4" || r1.Process.Source["wall_loops"] != "override" || r1.Process.Source["layer_height"] != "override" {
		t.Fatalf("profil complet et provenance : %v %v", r1.Process.Config["wall_loops"], r1.Process.Source["wall_loops"])
	}
	if len(r1.Process.Config) < 100 {
		t.Fatalf("profil complet attendu (%d clés)", len(r1.Process.Config))
	}
	for _, d := range r1.Diff {
		if d.Key == "wall_loops" && (d.Old != "2" || d.New != "4" || d.OldSource == "" || d.NewSource != "override") {
			t.Fatalf("diff wall_loops : %+v", d)
		}
	}
	// modification typée invalide / clé inconnue / clé protégée
	for name, ov := range map[string]slicer.Override{
		"type":        {Key: "wall_loops", Value: "beaucoup"},
		"inconnue":    {Key: "cle_qui_nexiste_pas", Value: "1"},
		"protégée":    {Key: "printer_model", Value: "X"},
		"énum/nombre": {Key: "seam_position", Value: 3},
	} {
		if _, err := e.svc.ModifyProfile(e.ctx, ModifyInput{BaseRevision: r1.Revision, AllowBranch: true, Modifications: []slicer.Override{ov}}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s : ErrInvalid attendue, %v", name, err)
		}
	}
	// révision suivante : cumul et diff par rapport à la précédente
	r2, err := e.svc.ModifyProfile(e.ctx, ModifyInput{BaseRevision: r1.Revision, Modifications: []slicer.Override{{Key: "wall_loops", Value: "6"}}})
	if err != nil {
		t.Fatal(err)
	}
	if r2.ParentRevision != r1.Revision || len(r2.Overrides) != 2 || len(r2.Diff) != 1 || r2.Diff[0].Old != "4" || r2.Diff[0].New != "6" {
		t.Fatalf("révision 2 : %+v / %+v", r2.Overrides, r2.Diff)
	}
	// r1 n'est plus la tête : refus (sauf branche explicite)
	_, err = e.svc.ModifyProfile(e.ctx, ModifyInput{BaseRevision: r1.Revision, Modifications: []slicer.Override{{Key: "wall_loops", Value: "3"}}})
	var se *StaleError
	if !errors.As(err, &se) || se.Current != r2.Revision {
		t.Fatalf("base périmée : ErrStale (tête %s), %v", r2.Revision, err)
	}
	if _, err := e.svc.ModifyProfile(e.ctx, ModifyInput{Modifications: []slicer.Override{{Key: "wall_loops", Value: "3"}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("sans base : %v", err)
	}
	// une expérience peut partir d'une révision de profil : surcharges communes figées
	mdl, _ := e.addModel("pièce", mesh.Cube(20))
	g, _ := e.svc.SetGoals(e.ctx, GoalInput{OriginalText: "rapide", Criteria: []CriterionInput{{Type: "speed"}}})
	x, err := e.svc.CreateExperiment(e.ctx, CreateExperimentInput{ModelID: mdl, GoalID: g.ID, BaseProfileRevision: r2.Revision, Candidates: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(x.Details.Base.Overrides) != 2 || x.Details.BaseProfileRevision != r2.Revision {
		t.Fatalf("base issue d'une révision : %+v", x.Details.Base.Overrides)
	}
	// wall_loops=6 en base : le levier « moins de parois » part de 6 et non de 2
	for _, c := range x.Candidates {
		for _, ch := range c.Changes {
			if ch.Key == "wall_loops" && ch.From != "6" {
				t.Fatalf("le levier doit partir de la valeur de base (6) : %+v", ch)
			}
		}
	}
}

func TestRecordPrintResultAndComparableMemory(t *testing.T) {
	st := installProfiles(t)
	e := newEnv(t, st, nil, false)
	mdl, _ := e.addModel("support", mesh.TShape(30, 40, 60, 8, 20))
	x, _ := experimentNoSlicing(t, e, mdl, strengthSpeed("xy", false))
	cand := x.Candidates[1]
	e.svc.setCandidate(e.ctx, cand.ID, "succeeded", "", &CandMetrics{Values: map[string]Metric{}, Effective: map[string]any{"process.wall_loops": "4"}}, "")

	if _, err := e.svc.RecordResult(e.ctx, ResultInput{ExperimentID: x.ID, CandidateID: cand.ID, Outcome: "bof", Observations: "x"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("issue inconnue : %v", err)
	}
	if _, err := e.svc.RecordResult(e.ctx, ResultInput{ExperimentID: x.ID, CandidateID: cand.ID, Outcome: "success"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("résultat vide : %v", err)
	}
	if _, err := e.svc.RecordResult(e.ctx, ResultInput{ExperimentID: x.ID, CandidateID: cand.ID, Outcome: "success", Photos: []string{strings.Repeat("a", 64)}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("photo absente du stockage : %v", err)
	}
	if _, err := e.svc.RecordResult(e.ctx, ResultInput{ExperimentID: x.ID, CandidateID: "inconnu", Outcome: "success", Observations: "x"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("candidat inconnu : %v", err)
	}
	sha, _, err := e.core.Blobs.Put(strings.NewReader("faux jpeg"), "")
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.svc.RecordResult(e.ctx, ResultInput{ExperimentID: x.ID, CandidateID: cand.ID, Outcome: "partial",
		Observations: "se plie sous 15 N, couches visibles", Photos: []string{sha},
		Measurements:   []Measurement{{Criterion: "strength", Name: "charge de rupture", Value: 22, Unit: "N"}},
		ActualSettings: map[string]any{"process.wall_loops": "4", "process.layer_height": "0.16"}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Context.MaterialFamily != "PLA" || r.Context.Machine != p1sMachine || r.Context.Filament != p1sFilament {
		t.Fatalf("contexte : %+v", r.Context)
	}
	if len(r.Deviations) != 1 || r.Deviations[0].Key != "process.layer_height" {
		t.Fatalf("seul l'écart (layer_height) est consigné : %+v", r.Deviations)
	}
	if r.PlannedSettings["process.wall_loops"] != "4" || r.CandidateSignature == "" || r.Reuse == "" {
		t.Fatalf("réglages prévus et conditions de réutilisation : %+v", r)
	}

	dims := x.Details.Geometry.DimsSortedMM
	q := MemoryQuery{MaterialFamily: "PLA", Machine: p1sMachine, Filament: p1sFilament, ModelID: mdl, DimsSortedMM: dims}
	hits, err := e.svc.Memory(e.ctx, q, 10)
	if err != nil || len(hits) != 1 || hits[0].Strength != 3 {
		t.Fatalf("même modèle, même filament : %v %+v", err, hits)
	}
	// autre bobine de la même famille, autre modèle de même taille : indicatif seulement
	q2 := q
	q2.ModelID, q2.Filament = "mdl_autre", "Bambu PLA Matte @BBL P1S 0.4 nozzle"
	hits, _ = e.svc.Memory(e.ctx, q2, 10)
	if len(hits) != 1 || hits[0].Strength != 1 || !strings.Contains(hits[0].Comparability, "autre filament") {
		t.Fatalf("filament différent : comparabilité réduite attendue : %+v", hits)
	}
	// conditions non comparables : rien n'est réutilisé
	for name, mod := range map[string]func(*MemoryQuery){
		"autre famille": func(q *MemoryQuery) { q.MaterialFamily = "PETG" },
		"autre machine": func(q *MemoryQuery) { q.Machine = "Bambu Lab A1 mini 0.4 nozzle" },
		"autre géométrie": func(q *MemoryQuery) {
			q.ModelID = "mdl_autre"
			q.DimsSortedMM = [3]float64{200, 150, 100}
		},
	} {
		qq := q
		mod(&qq)
		if hits, _ := e.svc.Memory(e.ctx, qq, 10); len(hits) != 0 {
			t.Errorf("%s : aucun résultat attendu, %+v", name, hits)
		}
	}
	if _, err := e.svc.Memory(e.ctx, MemoryQuery{}, 5); !errors.Is(err, ErrInvalid) {
		t.Fatalf("requête sans famille ni machine : %v", err)
	}
	// une nouvelle expérience comparable voit l'essai passé et reconnaît la variante déjà essayée
	x2, err := e.svc.CreateExperiment(e.ctx, CreateExperimentInput{ModelID: mdl, GoalID: x.GoalID,
		Base: BaseSpec{Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}}, Candidates: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(x2.Details.Prior) != 1 {
		t.Fatalf("essai antérieur attendu : %+v", x2.Details.Prior)
	}
	var flagged bool
	for _, c := range x2.Candidates {
		for _, n := range c.Notes {
			if strings.Contains(n, "déjà essayé") {
				flagged = true
			}
		}
	}
	if !flagged {
		t.Fatal("la variante identique à un essai passé doit être signalée")
	}
}

// ---- intégration Bambu Studio ----

func TestRealExperimentCube(t *testing.T) {
	sl := realSlicer(t)
	e := newEnv(t, installProfiles(t), sl, true)
	mdl, ver := e.addModel("cube20", mesh.Cube(20))
	goalIn := strengthSpeed("xy", true)
	goalIn.Criteria = append(goalIn.Criteria, CriterionInput{Type: "surface_aspect", Text: "belle finition", Priority: 4})
	g, err := e.svc.SetGoals(e.ctx, goalIn)
	if err != nil {
		t.Fatal(err)
	}
	x, err := e.svc.CreateExperiment(e.ctx, CreateExperimentInput{ModelID: mdl, GoalID: g.ID, Candidates: 3,
		Base: BaseSpec{Printer: "Bambu Lab P1S", Nozzle: 0.4, Process: p1sProcess, Filaments: []string{p1sFilament}}})
	if err != nil {
		t.Fatal(err)
	}
	if x.ModelVersionID != ver || len(x.Candidates) != 4 {
		t.Fatalf("expérience : %+v", x.Details.Base)
	}
	for _, c := range x.Candidates {
		if c.JobID == "" || (c.State != "queued" && c.State != "running") {
			t.Fatalf("découpe non mise en file : %+v", c.State)
		}
	}
	done := e.waitDone(x.ID)
	for _, c := range done.Candidates {
		if c.State != "succeeded" {
			t.Fatalf("%s : %s %s", c.ID, c.State, c.Error)
		}
		if c.ArtifactSHA == "" || !e.core.Blobs.Has(c.ArtifactSHA) {
			t.Fatalf("%s : archive non conservée", c.ID)
		}
	}
	cmp, err := e.svc.CompareVariants(e.ctx, x.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !cmp.Complete || len(cmp.Candidates) != 4 {
		t.Fatalf("comparaison : %+v", cmp)
	}
	times := map[float64]bool{}
	for _, c := range cmp.Candidates {
		pt, ok := c.Metrics["print_time_s"]
		if !ok || pt.Label != LabelSlicer || pt.Value <= 0 {
			t.Fatalf("%s : temps estimé étiqueté attendu : %+v", c.ID, c.Metrics)
		}
		if g, ok := c.Metrics["filament_g"]; !ok || g.Value <= 0 || g.Label != LabelSlicer {
			t.Fatalf("%s : masse : %+v", c.ID, g)
		}
		if _, ok := c.Metrics["layers"]; !ok {
			t.Fatalf("%s : couches absentes", c.ID)
		}
		times[pt.Value] = true
		if len(c.Engineering) != 1 || c.Engineering[0].Label != LabelEngineering {
			t.Fatalf("%s : calcul d'ingénierie étiqueté attendu : %+v", c.ID, c.Engineering)
		}
		if len(c.Thresholds) != 1 || c.Thresholds[0].Metric != "print_time_s" || c.Thresholds[0].Limit != 1200 {
			t.Fatalf("%s : seuil de 20 min attendu : %+v", c.ID, c.Thresholds)
		}
	}
	if len(times) < 3 {
		t.Fatalf("les variantes doivent donner des temps différents : %v", times)
	}
	var speedRank *Ranking
	for i := range cmp.Rankings {
		if cmp.Rankings[i].Metric == "print_time_s" {
			speedRank = &cmp.Rankings[i]
		}
		if cmp.Rankings[i].Criterion == "c3_surface_aspect" {
			t.Fatal("aucun classement pour un critère non mesurable")
		}
	}
	if speedRank == nil || len(speedRank.Order) != 4 || speedRank.Better != "lower" {
		t.Fatalf("classement par temps attendu : %+v", cmp.Rankings)
	}
	if len(cmp.NotEvaluable) != 1 || cmp.NotEvaluable[0].Test == "" {
		t.Fatalf("aspect de surface : à vérifier par essai : %+v", cmp.NotEvaluable)
	}
	if !strings.Contains(cmp.Notice, "Aucun gagnant") {
		t.Fatal("avis « aucun gagnant » attendu")
	}
	for _, c := range cmp.Candidates {
		t.Logf("TABLE %-48s | %5.0f s | %5.2f g | %3.0f couches | SF %.2f (%s) | %s", c.Label, c.Metrics["print_time_s"].Value,
			c.Metrics["filament_g"].Value, c.Metrics["layers"].Value, c.Engineering[0].SafetyFactor, c.Engineering[0].OrientationAssumed, strings.Join(c.Changes, "; "))
	}
	b, _ := json.MarshalIndent(cmp.Rankings, "", "  ")
	t.Logf("classements :\n%s", compact(b))

	// appliquer la variante la plus rapide
	fast := speedRank.Order[0].CandidateID
	if _, err := e.svc.ApplyVariant(e.ctx, ApplyInput{ExperimentID: x.ID, CandidateID: fast, ExpectedRevision: ver}); err != nil {
		t.Fatal(err)
	}
}

func compact(b []byte) string {
	s := string(b)
	if len(s) > 6000 {
		s = s[:6000] + "\n…"
	}
	return s
}

// readConfig lit project_settings.config de l'archive conservée dans les blobs.
func readConfig(t *testing.T, e *env, sha string) map[string]any {
	t.Helper()
	p, err := e.core.Blobs.Path(sha)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == "Metadata/project_settings.config" {
			rc, _ := f.Open()
			defer rc.Close()
			raw, _ := io.ReadAll(rc)
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			return m
		}
	}
	t.Fatal("project_settings.config absent")
	return nil
}

func TestRealLeverValuesAcceptedByStudioAndSupports(t *testing.T) {
	sl := realSlicer(t)
	e := newEnv(t, installProfiles(t), sl, true)
	// pièce avec surplomb (T) : supports mesurables
	mdl, _ := e.addModel("tee", mesh.TShape(30, 40, 60, 8, 20))
	g, err := e.svc.SetGoals(e.ctx, GoalInput{OriginalText: "belle surface et peu de supports", Criteria: []CriterionInput{
		{Type: "surface_aspect", Priority: 1}, {Type: "minimal_supports", Priority: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	// variantes personnalisées qui exercent chaque énumération des règles, supports activés ou non
	custom := []CustomCandidate{
		{Label: "énumérations", Overrides: []slicer.Override{{Key: "seam_position", Value: "back"}, {Key: "ironing_type", Value: "top"},
			{Key: "draft_shield", Value: "enabled"}, {Key: "sparse_infill_pattern", Value: "gyroid"}, {Key: "nozzle_temperature", Value: "225"}}},
		{Label: "supports actifs", Overrides: []slicer.Override{{Key: "enable_support", Value: "1"}}},
	}
	x, err := e.svc.CreateExperiment(e.ctx, CreateExperimentInput{ModelID: mdl, GoalID: g.ID, Candidates: 2, Custom: custom,
		Base: BaseSpec{Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}}})
	if err != nil {
		t.Fatal(err)
	}
	done := e.waitDone(x.ID)
	var enum, supp *CandidateView
	for i := range done.Candidates {
		c := &done.Candidates[i]
		if c.State != "succeeded" {
			t.Fatalf("%s (%s) : %s %s", c.ID, c.Label, c.State, c.Error)
		}
		switch c.Label {
		case "énumérations":
			enum = c
		case "supports actifs":
			supp = c
		}
	}
	cfg := readConfig(t, e, enum.ArtifactSHA)
	for k, want := range map[string]string{"seam_position": "back", "ironing_type": "top", "draft_shield": "enabled", "sparse_infill_pattern": "gyroid"} {
		if got, _ := cfg[k].(string); got != want {
			t.Errorf("Studio n'a pas gardé %s=%s (archive : %v)", k, want, cfg[k])
		}
	}
	if sg, ok := supp.Metrics.Values["support_g"]; !ok || sg.Value <= 0 || sg.Label != LabelGcode {
		t.Fatalf("masse de supports lue dans le G-code attendue : %+v (non lisibles : %v)", supp.Metrics.Values["support_g"], supp.Metrics.Unavailable)
	} else {
		t.Logf("supports : %.3f g sur %.2f g au total", sg.Value, supp.Metrics.Values["filament_g"].Value)
	}
	if !supp.Metrics.SupportUsed {
		t.Fatal("support_used attendu")
	}
	// sans supports activés, aucune masse de support
	for _, c := range done.Candidates {
		if c.Kind == "reference" {
			if sg := c.Metrics.Values["support_g"]; sg.Value != 0 {
				t.Errorf("référence sans supports : %v", sg)
			}
		}
	}
}

func TestRealOrientationCandidateIsSliced(t *testing.T) {
	sl := realSlicer(t)
	e := newEnv(t, installProfiles(t), sl, true)
	mdl, _ := e.addModel("tee", mesh.TShape(30, 40, 60, 8, 20))
	g, err := e.svc.SetGoals(e.ctx, GoalInput{OriginalText: "supporte un effort selon Z", Criteria: []CriterionInput{
		{Type: "strength", Params: map[string]any{"load_direction": "z", "engineering": map[string]any{
			"support": "cantilever", "load_type": "point", "load": 10.0, "length_mm": 40.0,
			"section": map[string]any{"shape": "rect", "b": 20.0, "h": 8.0}, "material": "PLA"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	x, err := e.svc.CreateExperiment(e.ctx, CreateExperimentInput{ModelID: mdl, GoalID: g.ID, Candidates: 3,
		Base: BaseSpec{Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}}})
	if err != nil {
		t.Fatal(err)
	}
	var oriented *CandidateView
	for i := range x.Candidates {
		if x.Candidates[i].Orientation != nil && oriented == nil {
			oriented = &x.Candidates[i]
		}
	}
	if oriented == nil {
		t.Fatal("une variante orientée était attendue")
	}
	done := e.waitDone(x.ID)
	cmp, err := e.svc.CompareVariants(e.ctx, x.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sfRef, sfRot float64
	for _, c := range cmp.Candidates {
		if c.State != "succeeded" {
			t.Fatalf("%s : %s %s", c.ID, c.State, c.Error)
		}
		if c.ID == oriented.ID {
			sfRot = c.Engineering[0].SafetyFactor
			if c.Orientation == "" || c.Engineering[0].OrientationAssumed != "xy" {
				t.Fatalf("orientation et hypothèse xy attendues : %+v", c)
			}
			if _, ok := c.Metrics["bed_contact_mm2"]; !ok {
				t.Fatal("mesure géométrique de contact attendue")
			}
		}
		if c.Kind == "reference" {
			sfRef = c.Engineering[0].SafetyFactor
		}
	}
	if !(sfRot > sfRef) {
		t.Fatalf("facteur de sécurité selon l'orientation : %v (orientée) > %v (référence) attendu", sfRot, sfRef)
	}
	_ = done
}

var _ = bytes.NewReader
var _ = contracts.JobFailed
