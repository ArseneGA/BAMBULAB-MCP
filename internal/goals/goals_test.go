package goals

import (
	"errors"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

func mustRules(t *testing.T) *Rules {
	t.Helper()
	r, err := LoadRules()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRulesLoadAndSchema(t *testing.T) {
	r := mustRules(t)
	if r.Version == "" || len(r.Criteria) < 10 {
		t.Fatalf("règles incomplètes : %s, %d critères", r.Version, len(r.Criteria))
	}
	cat := r.Catalog()
	if len(cat) != len(r.Criteria) {
		t.Fatal("catalogue incomplet")
	}
	// types exposés par MCP : le schéma doit pouvoir être généré
	contracts.MustSchemaFor[GoalInput]()
	contracts.MustSchemaFor[Goal]()
	contracts.MustSchemaFor[CriteriaCatalog]()
	contracts.MustSchemaFor[Experiment]()
	contracts.MustSchemaFor[CompareOutput]()
	contracts.MustSchemaFor[ModifyOutput]()
	contracts.MustSchemaFor[ApplyOutput]()
	contracts.MustSchemaFor[PrintResult]()
}

func TestRulesKeysExistInRealProfiles(t *testing.T) {
	st := installProfiles(t)
	r := mustRules(t)
	proc, err := st.Resolve(profiles.KindProcess, p1sProcess)
	if err != nil {
		t.Skip(err)
	}
	fil, err := st.Resolve(profiles.KindFilament, p1sFilament)
	if err != nil {
		t.Skip(err)
	}
	if missing := r.CheckKeys(proc, fil); len(missing) > 0 {
		t.Fatalf("clés de leviers absentes des profils BBL réels : %v", missing)
	}
}

func TestGoalContradictoryCriteria(t *testing.T) {
	r := mustRules(t)
	g, err := BuildGoal(r, strengthSpeed("z", false))
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Conflicts) != 1 {
		t.Fatalf("1 conflit attendu, %d", len(g.Conflicts))
	}
	cf := g.Conflicts[0]
	if len(cf.Proposals) < 2 || !cf.NeedsPriority {
		t.Fatalf("compromis et question de priorité attendus : %+v", cf)
	}
	var sawPrio, sawDir bool
	for _, a := range g.Ambiguities {
		if strings.Contains(a.Question, "laquelle prime") {
			sawPrio = true
		}
		if a.Param == "load_direction" {
			sawDir = true
		}
	}
	if !sawPrio {
		t.Fatal("priorités égales : une question de priorité est attendue")
	}
	if sawDir {
		t.Fatal("direction d'effort fournie : pas d'ambiguïté attendue")
	}
	// priorités différentes : plus de question, mais le conflit reste signalé
	in := strengthSpeed("xy", false)
	in.Criteria[0].Priority = 1
	g2, _ := BuildGoal(r, in)
	if g2.Conflicts[0].NeedsPriority || len(g2.Conflicts) != 1 {
		t.Fatalf("priorité tranchée : %+v", g2.Conflicts)
	}
	for _, a := range g2.Ambiguities {
		t.Fatalf("aucune ambiguïté attendue : %+v", a)
	}
	if g.OriginalText != "Un support solide qui encaisse un effort, mais imprimé vite" {
		t.Fatal("texte original non conservé")
	}
}

func TestGoalAmbiguousLoadDirection(t *testing.T) {
	r := mustRules(t)
	g, err := BuildGoal(r, GoalInput{OriginalText: "pièce solide", Criteria: []CriterionInput{{Type: "strength"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Ambiguities) != 1 || g.Ambiguities[0].Param != "load_direction" || len(g.Ambiguities[0].Options) == 0 {
		t.Fatalf("ambiguïté load_direction attendue : %+v", g.Ambiguities)
	}
	if g.Criteria[0].Status != "non_evaluable" || len(g.NonEvaluable) != 1 {
		t.Fatalf("résistance sans données de calcul : non évaluable : %+v", g.Criteria[0])
	}
}

func TestGoalUnknownCriterionIsCustom(t *testing.T) {
	r := mustRules(t)
	g, err := BuildGoal(r, GoalInput{OriginalText: "pièce qui sent la lavande", Criteria: []CriterionInput{
		{Type: "Odeur Lavande", Text: "doit sentir la lavande", NonNegotiable: true}}})
	if err != nil {
		t.Fatal(err)
	}
	c := g.Criteria[0]
	if c.Type != "custom" || c.Known || c.Status != "non_evaluable" || c.Params["original_type"] != "odeur_lavande" {
		t.Fatalf("critère custom attendu : %+v", c)
	}
	if len(g.NonEvaluable) != 1 || len(g.Warnings) == 0 {
		t.Fatalf("non évaluable et non négociable doivent être signalés : %+v / %v", g.NonEvaluable, g.Warnings)
	}
	if _, err := BuildGoal(r, GoalInput{OriginalText: "x", Criteria: []CriterionInput{{Type: "inconnu"}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("critère inconnu sans texte : erreur attendue, %v", err)
	}
}

func TestGoalValidation(t *testing.T) {
	r := mustRules(t)
	cases := map[string]GoalInput{
		"texte vide":   {Criteria: []CriterionInput{{Type: "speed"}}},
		"sans critère": {OriginalText: "x"},
		"priorité":     {OriginalText: "x", Criteria: []CriterionInput{{Type: "speed", Priority: 9}}},
		"énumération":  {OriginalText: "x", Criteria: []CriterionInput{{Type: "strength", Params: map[string]any{"load_direction": "diagonale"}}}},
		"unité":        {OriginalText: "x", Criteria: []CriterionInput{{Type: "speed", Threshold: &Threshold{Op: "<=", Value: 1, Unit: "jours"}}}},
		"opérateur":    {OriginalText: "x", Criteria: []CriterionInput{{Type: "speed", Threshold: &Threshold{Op: "~", Value: 1}}}},
		"identifiant":  {OriginalText: "x", Criteria: []CriterionInput{{ID: "a", Type: "speed"}, {ID: "a", Type: "material_economy"}}},
		"poutre absurde": {OriginalText: "x", Criteria: []CriterionInput{{Type: "strength", Params: map[string]any{"load_direction": "xy",
			"engineering": map[string]any{"support": "cantilever", "load_type": "point", "load": -1.0, "length_mm": 50.0,
				"section": map[string]any{"shape": "rect", "b": 10.0, "h": 5.0}, "material": "PLA"}}}}},
	}
	for name, in := range cases {
		if _, err := BuildGoal(r, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s : ErrInvalid attendue, %v", name, err)
		}
	}
	g, err := BuildGoal(r, GoalInput{OriginalText: "x", Criteria: []CriterionInput{
		{Type: "speed", Threshold: &Threshold{Op: "<=", Value: 2, Unit: "h"}},
		{Type: "material_economy", Threshold: &Threshold{Op: "<=", Value: 0.05, Unit: "kg"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if n := g.Criteria[0].Norm; n == nil || n.Value != 7200 || n.Metric != "print_time_s" {
		t.Fatalf("seuil de durée mal normalisé : %+v", n)
	}
	if n := g.Criteria[1].Norm; n == nil || n.Value != 50 || n.Metric != "filament_g" {
		t.Fatalf("seuil de masse mal normalisé : %+v", n)
	}
}

func TestGoalStrengthEvaluableWithEngineering(t *testing.T) {
	r := mustRules(t)
	g, err := BuildGoal(r, strengthSpeed("z", true))
	if err != nil {
		t.Fatal(err)
	}
	if g.Criteria[0].Status != "evaluable" || g.Criteria[0].Metric != "safety_factor" {
		t.Fatalf("résistance avec calcul de poutre : évaluable : %+v", g.Criteria[0])
	}
}

func TestGoalZoneAndNoLevers(t *testing.T) {
	r := mustRules(t)
	g, err := BuildGoal(r, GoalInput{OriginalText: "extérieur et beau devant", Criteria: []CriterionInput{
		{Type: "outdoor_resistance"}, {Type: "surface_aspect", Zone: "face avant"}}})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(g.Warnings, "|")
	if !strings.Contains(joined, "face avant") || !strings.Contains(joined, "propriété du matériau") && !strings.Contains(joined, "matériau") {
		t.Fatalf("zone et critère sans levier doivent être signalés : %v", g.Warnings)
	}
}

// ---- générateur ----

func genInput(t *testing.T, st *profiles.Store, goal *Goal, count int) GenInput {
	t.Helper()
	proc, err := st.Resolve(profiles.KindProcess, p1sProcess)
	if err != nil {
		t.Skip(err)
	}
	fil, err := st.Resolve(profiles.KindFilament, p1sFilament)
	if err != nil {
		t.Skip(err)
	}
	return GenInput{Goal: goal, Rules: mustRules(t), Process: proc.Clone(), Filament: fil.Clone(), Family: "PLA", Nozzle: 0.4, Count: count, MaxFactors: 3,
		Mesh: mesh.TShape(30, 40, 60, 8, 20), Bed: mesh.TargetP1S}
}

func TestGenerateThreeVariants(t *testing.T) {
	st := installProfiles(t)
	r := mustRules(t)
	goal, err := BuildGoal(r, strengthSpeed("xy", false))
	if err != nil {
		t.Fatal(err)
	}
	in := genInput(t, st, goal, 3)
	out, err := Generate(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Candidates) != 4 || out.Candidates[0].Kind != "reference" {
		t.Fatalf("référence + 3 variantes attendues, %d candidats", len(out.Candidates))
	}
	seen := map[string]bool{}
	for _, c := range out.Candidates[1:] {
		if c.Factors < 1 || c.Factors > 3 {
			t.Errorf("%s : %d facteurs (1 à 3 attendus)", c.Label, c.Factors)
		}
		if len(c.Changes) == 0 || len(c.Tradeoffs) == 0 || c.Rationale == "" {
			t.Errorf("%s : justification incomplète", c.Label)
		}
		for _, ch := range c.Changes {
			if ch.Criterion == "" || ch.Lever == "" || ch.Effect == "" || ch.Tradeoff == "" {
				t.Errorf("%s : changement sans justification %+v", c.Label, ch)
			}
		}
		sig := candSig(c)
		if seen[sig] {
			t.Errorf("variante en double : %s", c.Label)
		}
		seen[sig] = true
		// toutes les surcharges doivent être acceptées par la validation typée de la découpe
		if _, _, _, _, err := slicer.ResolveWithOverrides(st, p1sMachine, p1sProcess, []string{p1sFilament}, c.Overrides); err != nil {
			t.Errorf("%s : surcharges refusées : %v", c.Label, err)
		}
	}
	// la variante de compromis existe et combine les deux critères
	var comp *Candidate
	for i := range out.Candidates {
		if strings.HasPrefix(out.Candidates[i].Label, "Compromis") {
			comp = &out.Candidates[i]
		}
	}
	if comp == nil {
		t.Fatal("variante de compromis attendue pour des critères contradictoires")
	}
	types := map[string]bool{}
	for _, ch := range comp.Changes {
		types[ch.CriterionType] = true
	}
	if !types["strength"] || !types["speed"] {
		t.Fatalf("le compromis doit toucher résistance et vitesse : %v", types)
	}
	// plafond : N=1 -> une seule variante
	in.Count = 1
	one, _ := Generate(in)
	if len(one.Candidates) != 2 {
		t.Fatalf("N=1 : %d candidats", len(one.Candidates))
	}
}

func TestGenerateOrientationForZLoad(t *testing.T) {
	st := installProfiles(t)
	goal, err := BuildGoal(mustRules(t), GoalInput{OriginalText: "effort selon l'axe Z du modèle", Criteria: []CriterionInput{
		{Type: "strength", Params: map[string]any{"load_direction": "z"}}}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := Generate(genInput(t, st, goal, 3))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range out.Candidates {
		if c.Orientation != nil {
			found = true
			if c.Orientation.Up[2] > 0.5 || c.Orientation.Up[2] < -0.5 {
				t.Errorf("l'orientation proposée laisse l'effort perpendiculaire aux couches : %+v", c.Orientation.Up)
			}
		}
	}
	if !found {
		t.Fatalf("une variante avec orientation était attendue pour un effort en Z : %+v", out.Skipped)
	}
}

func TestGenerateUnknownMaterialAppliesNoMaterialParams(t *testing.T) {
	st := installProfiles(t)
	goal, _ := BuildGoal(mustRules(t), strengthSpeed("z", false))
	in := genInput(t, st, goal, 3)
	in.Family = "" // filament d'une famille hors règles
	in.Mesh = nil
	out, err := Generate(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range out.Candidates {
		for _, o := range c.Overrides {
			if o.Target == "filament" || o.Key == "inner_wall_speed" {
				t.Errorf("paramètre dépendant du matériau appliqué à une famille inconnue : %s", o.Key)
			}
		}
	}
	var reason bool
	for _, s := range out.Skipped {
		if s.Lever == "st_temp" && strings.Contains(s.Reason, "famille de matériau inconnue") {
			reason = true
		}
	}
	if !reason {
		t.Fatalf("st_temp doit être écarté avec sa raison : %+v", out.Skipped)
	}
	// autre famille connue : pas de transfert de leviers non validés
	in.Family = "TPU"
	out, _ = Generate(in)
	for _, c := range out.Candidates {
		for _, o := range c.Overrides {
			if o.Key == "nozzle_temperature" || o.Key == "inner_wall_speed" {
				t.Errorf("levier non validé pour TPU appliqué : %s", o.Key)
			}
		}
	}
}

// synthProfiles fabrique des profils minimaux pour tester la fusion sans Studio.
func synthProfiles() (*profiles.Resolved, *profiles.Resolved) {
	p := &profiles.Resolved{Kind: profiles.KindProcess, Name: "p", Config: map[string]any{
		"layer_height": "0.2", "wall_loops": "2", "sparse_infill_density": "15%", "sparse_infill_pattern": "grid",
		"top_shell_layers": "5", "bottom_shell_layers": "3", "inner_wall_speed": []any{"300", "400"},
	}, Source: map[string]string{}}
	f := &profiles.Resolved{Kind: profiles.KindFilament, Name: "f", Config: map[string]any{
		"filament_type": []any{"PLA"}, "nozzle_temperature": []any{"220", "220"}, "nozzle_temperature_range_high": []any{"240"},
	}, Source: map[string]string{}}
	return p, f
}

func TestGenerateCompromiseMergesOpposedLevers(t *testing.T) {
	p, f := synthProfiles()
	goal, err := BuildGoal(mustRules(t), strengthSpeed("xy", false))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Generate(GenInput{Goal: goal, Rules: mustRules(t), Process: p, Filament: f, Family: "PLA", Nozzle: 0.4, Count: 3, MaxFactors: 8})
	if err != nil {
		t.Fatal(err)
	}
	var comp *Candidate
	for i := range out.Candidates {
		if strings.HasPrefix(out.Candidates[i].Label, "Compromis") {
			comp = &out.Candidates[i]
		}
	}
	if comp == nil {
		t.Fatal("compromis attendu")
	}
	var infill *Change
	for i := range comp.Changes {
		if comp.Changes[i].Key == "sparse_infill_density" {
			infill = &comp.Changes[i]
		}
	}
	if infill == nil {
		t.Fatal("remplissage attendu dans le compromis")
	}
	// résistance : 15 % -> 30 % ; vitesse : 15 % -> 5 % ; compromis : 17,5 %
	if infill.To != "17.5%" || !strings.Contains(infill.Note, "compromis") {
		t.Fatalf("valeur intermédiaire attendue (17.5%%) : %+v", infill)
	}
	// aucune clé en double dans les surcharges
	keys := map[string]bool{}
	for _, o := range comp.Overrides {
		if keys[o.Target+o.Key] {
			t.Fatalf("clé en double %s", o.Key)
		}
		keys[o.Target+o.Key] = true
	}
}

func TestGenerateSingleCriterionGradedIntensity(t *testing.T) {
	p, f := synthProfiles()
	goal, _ := BuildGoal(mustRules(t), GoalInput{OriginalText: "rapide", Criteria: []CriterionInput{{Type: "speed"}}})
	out, err := Generate(GenInput{Goal: goal, Rules: mustRules(t), Process: p, Filament: f, Family: "PLA", Nozzle: 0.4, Count: 3, MaxFactors: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Candidates) != 4 {
		t.Fatalf("3 variantes graduées attendues, %d candidats", len(out.Candidates))
	}
	for i := 2; i < len(out.Candidates); i++ {
		if out.Candidates[i].Factors <= out.Candidates[i-1].Factors {
			t.Errorf("intensité croissante attendue : %d puis %d", out.Candidates[i-1].Factors, out.Candidates[i].Factors)
		}
	}
	// la vitesse interne est une liste par extrudeur : mise à l'échelle élément par élément
	for _, c := range out.Candidates {
		for _, o := range c.Overrides {
			if o.Key == "inner_wall_speed" {
				l, ok := o.Value.([]string)
				if !ok || l[0] != "360" || l[1] != "480" {
					t.Errorf("liste attendue [360 480] : %#v", o.Value)
				}
			}
		}
	}
}

func TestNoLeversOnlyReference(t *testing.T) {
	p, f := synthProfiles()
	goal, _ := BuildGoal(mustRules(t), GoalInput{OriginalText: "tient 100 °C", Criteria: []CriterionInput{{Type: "thermal_resistance"}}})
	out, err := Generate(GenInput{Goal: goal, Rules: mustRules(t), Process: p, Filament: f, Family: "PLA", Nozzle: 0.4, Count: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Candidates) != 1 || len(out.Notes) == 0 || !strings.Contains(strings.Join(out.Notes, " "), "aucun réglage") {
		t.Fatalf("seule la référence et une note sont attendues : %+v", out)
	}
}

// ---- calcul d'ingénierie ----

func TestEngineeringOrientationChangesSafetyFactor(t *testing.T) {
	goal, err := BuildGoal(mustRules(t), strengthSpeed("z", true))
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{}
	crit := goal.Criteria[0]
	ref := CandidateView{Candidate: Candidate{ID: "ref"}}
	rot := CandidateView{Candidate: Candidate{ID: "rot", Changes: []Change{{Lever: "st_orient"}}}}
	a := s.engineeringFor(crit, ref, goal, "PLA")
	b := s.engineeringFor(crit, rot, goal, "PLA")
	if a.Error != "" || b.Error != "" {
		t.Fatalf("calcul : %q %q", a.Error, b.Error)
	}
	if a.OrientationAssumed != "z" || b.OrientationAssumed != "xy" {
		t.Fatalf("orientations supposées : %s / %s", a.OrientationAssumed, b.OrientationAssumed)
	}
	if !(b.SafetyFactor > a.SafetyFactor) {
		t.Fatalf("effort dans le plan des couches : facteur de sécurité supérieur attendu (%v vs %v)", b.SafetyFactor, a.SafetyFactor)
	}
	if a.Label != LabelEngineering || len(a.Assumptions) == 0 || a.Level == "" {
		t.Fatalf("étiquette, hypothèses et niveau de confiance attendus : %+v", a)
	}
}

func TestRankingsOnlyForMeasurable(t *testing.T) {
	goal, _ := BuildGoal(mustRules(t), GoalInput{OriginalText: "joli et rapide", Criteria: []CriterionInput{
		{Type: "speed"}, {Type: "surface_aspect"}}})
	cands := []CompareCandidate{
		{ID: "a", State: "succeeded", Metrics: map[string]Metric{"print_time_s": {Value: 500}}},
		{ID: "b", State: "succeeded", Metrics: map[string]Metric{"print_time_s": {Value: 300}}},
		{ID: "c", State: "failed"},
	}
	rk := rankings(goal, cands)
	if len(rk) != 1 || rk[0].Metric != "print_time_s" || rk[0].Order[0].CandidateID != "b" || len(rk[0].Order) != 2 {
		t.Fatalf("un seul classement (vitesse), échec exclu : %+v", rk)
	}
}

func TestGenerateOpposedLeversCancelAndFreeSlot(t *testing.T) {
	p, f := synthProfiles()
	p.Config["seam_position"] = "aligned"
	p.Config["outer_wall_speed"] = []any{"200", "350"}
	p.Config["outer_wall_acceleration"] = []any{"5000", "5000"}
	p.Config["ironing_type"] = "no ironing"
	goal, err := BuildGoal(mustRules(t), GoalInput{OriginalText: "joli et rapide", Criteria: []CriterionInput{
		{Type: "speed", Priority: 1}, {Type: "surface_aspect", Priority: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := Generate(GenInput{Goal: goal, Rules: mustRules(t), Process: p, Filament: f, Family: "PLA", Nozzle: 0.4, Count: 3, MaxFactors: 3})
	if err != nil {
		t.Fatal(err)
	}
	var comp *Candidate
	for i := range out.Candidates {
		if strings.HasPrefix(out.Candidates[i].Label, "Compromis") {
			comp = &out.Candidates[i]
		}
	}
	if comp == nil {
		t.Fatal("compromis attendu")
	}
	for _, o := range comp.Overrides {
		if o.Key == "layer_height" {
			t.Fatalf("0,28 et 0,12 s'annulent : pas de changement de layer_height attendu : %#v", o)
		}
	}
	if !strings.Contains(strings.Join(comp.Notes, "|"), "s'annulent") {
		t.Fatalf("annulation à signaler : %v", comp.Notes)
	}
	if comp.Factors != len(comp.Changes) {
		t.Fatalf("facteurs %d ≠ changements %d", comp.Factors, len(comp.Changes))
	}
}
