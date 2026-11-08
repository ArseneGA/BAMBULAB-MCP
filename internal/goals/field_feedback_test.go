package goals

import (
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// plateauEnPorteAFaux : socle posé + plateau horizontal de 100 × 100 mm en l'air (≈ 10 000 mm² de surplomb plat).
func plateauEnPorteAFaux() *mesh.Mesh {
	m := mesh.Box(0, 0, 0, 100, 10, 5)
	m.Append(mesh.Box(0, 0, 30, 100, 100, 40))
	return m
}

func crit(id string, params map[string]any) Criterion {
	return Criterion{CriterionInput: CriterionInput{ID: id, Type: "strength", Params: params}}
}

func resolved(cfg map[string]any) *profiles.Resolved {
	return &profiles.Resolved{Config: cfg, Source: map[string]string{}}
}

// ---- point 5b : levier écarté, message précis ----

func TestWhenReasonNamesTheParameterAndExpectedValue(t *testing.T) {
	rule := mustRules(t).Criteria["strength"]
	if rule == nil {
		t.Skip("critère strength absent")
	}
	w := &When{Param: "load_direction", In: []string{"z"}}
	// paramètre absent
	ok, why := whenReason(rule, crit("crit1", nil), w)
	if ok || !strings.Contains(why, "load_direction") || !strings.Contains(why, "« z »") || !strings.Contains(why, "params.load_direction") {
		t.Fatalf("paramètre absent : %q", why)
	}
	// mauvaise valeur
	ok, why = whenReason(rule, crit("crit1", map[string]any{"load_direction": "xy"}), w)
	if ok || !strings.Contains(why, "« xy »") || !strings.Contains(why, "« z »") {
		t.Fatalf("valeur incorrecte : %q", why)
	}
	// condition remplie
	if ok, why := whenReason(rule, crit("c", map[string]any{"load_direction": "z"}), w); !ok || why != "" {
		t.Fatalf("condition remplie : %v %q", ok, why)
	}
	// not_in
	ok, why = whenReason(rule, crit("c", map[string]any{"load_direction": "z"}), &When{Param: "load_direction", NotIn: []string{"z"}})
	if ok || !strings.Contains(why, "sauf") {
		t.Fatalf("not_in : %q", why)
	}
}

// ---- point 5c : variantes personnalisées, changements réels ----

func TestCustomChangesListsRealSettings(t *testing.T) {
	p := resolved(map[string]any{"enable_support": "0", "layer_height": "0.2"})
	f := resolved(map[string]any{"fan_max_speed": []any{"100"}, "nozzle_temperature": []any{"230", "230"}})
	ch := customChanges(p, f, []slicer.Override{
		{Key: "fan_max_speed", Value: 40}, {Key: "nozzle_temperature", Value: 260.0}, {Key: "enable_support", Value: true}, {Key: "brim_type", Value: "outer_only"},
	})
	if len(ch) != 4 {
		t.Fatalf("4 changements attendus, reçu %d : %+v", len(ch), ch)
	}
	by := map[string]Change{}
	for _, c := range ch {
		by[c.Key] = c
	}
	if c := by["fan_max_speed"]; c.From != "100" || c.To != "40" || c.Target != "filament" {
		t.Errorf("ventilateur : %+v", c)
	}
	if c := by["nozzle_temperature"]; c.From != "230" || c.To != "260" {
		t.Errorf("température : %+v", c)
	}
	if c := by["enable_support"]; c.From != "0" || c.To != "true" || c.Target != "process" {
		t.Errorf("supports : %+v", c)
	}
	if c := by["brim_type"]; !strings.Contains(c.From.(string), "auto_brim") || c.To != "outer_only" {
		t.Errorf("bordure absente du profil : le « avant » est le défaut de Bambu Studio : %+v", c)
	}
	for _, c := range ch {
		if c.Lever != "custom" || c.Effect == "" {
			t.Errorf("changement incomplet : %+v", c)
		}
	}
}

// ---- points 1 et 5a : surplombs sans supports, ventilation réduite ----

func TestOverhangInsightWarnsWithoutSupportsAndExplainsCooling(t *testing.T) {
	base := resolved(map[string]any{"fan_min_speed": []any{"100"}, "fan_max_speed": []any{"100"}, "nozzle_temperature": []any{"230"}})
	translucide := resolved(map[string]any{"fan_min_speed": []any{"20"}, "fan_max_speed": []any{"40"}, "nozzle_temperature": []any{"260"}})
	noSupport := resolved(map[string]any{"enable_support": "0", "support_threshold_angle": "30"})
	withSupport := resolved(map[string]any{"enable_support": "1", "support_threshold_angle": "30"})

	mt, warns := overhangInsight(plateauEnPorteAFaux(), base, translucide, noSupport)
	if mt == nil || mt.Value < 9000 || mt.Unit != "mm²" {
		t.Fatalf("surface non supportée attendue ≈ 10 000 mm² : %+v", mt)
	}
	joined := strings.Join(warns, "\n")
	for _, want := range []string{"SANS supports", "ventilation réduite", "surplombs dégradés", "température de buse relevée"} {
		if !strings.Contains(joined, want) {
			t.Errorf("avertissement %q absent : %s", want, joined)
		}
	}
	// supports activés : surface non supportée nulle, pas d'avertissement « sans supports », mais le refroidissement reste expliqué
	mt, warns = overhangInsight(plateauEnPorteAFaux(), base, translucide, withSupport)
	if mt == nil || mt.Value != 0 || strings.Contains(strings.Join(warns, "\n"), "SANS supports") {
		t.Fatalf("avec supports : %+v %v", mt, warns)
	}
	if !strings.Contains(strings.Join(warns, "\n"), "ventilation réduite") {
		t.Errorf("la ventilation réduite doit rester signalée : %v", warns)
	}
	// pièce sans surplomb : rien à dire
	mt, warns = overhangInsight(mesh.Cube(40), base, translucide, noSupport)
	if mt == nil || mt.Value != 0 || len(warns) != 0 {
		t.Fatalf("cube : %+v %v", mt, warns)
	}
	// même réglage que la base : aucune mise en garde de refroidissement
	if _, w := overhangInsight(plateauEnPorteAFaux(), base, base, withSupport); len(w) != 0 {
		t.Fatalf("variante identique à la base : %v", w)
	}
}

// compare_variants (profils Bambu réels) : changements réels d'une variante personnalisée, colonne « surface en
// surplomb non supportée », avertissements de la variante « translucide ».
func TestRealCompareCustomVariantChangesAndOverhang(t *testing.T) {
	st := installProfiles(t)
	e := newEnv(t, st, nil, false)
	mdl, _ := e.addModel("plateau", plateauEnPorteAFaux())
	g, err := e.svc.SetGoals(e.ctx, strengthSpeed("xy", false))
	if err != nil {
		t.Fatal(err)
	}
	x, err := e.svc.CreateExperiment(e.ctx, CreateExperimentInput{ModelID: mdl, GoalID: g.ID, Candidates: 1,
		Base: BaseSpec{Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}},
		Custom: []CustomCandidate{
			{Label: "translucide", Overrides: []slicer.Override{{Key: "fan_min_speed", Value: 20}, {Key: "fan_max_speed", Value: 40}, {Key: "nozzle_temperature", Value: 235}}},
			{Label: "avec supports", Overrides: []slicer.Override{{Key: "enable_support", Value: true}}},
		}})
	if err != nil {
		t.Fatal(err)
	}
	cmp, err := e.svc.CompareVariants(e.ctx, x.ID)
	if err != nil {
		t.Fatal(err)
	}
	var tr, sp *CompareCandidate
	for i := range cmp.Candidates {
		switch cmp.Candidates[i].Label {
		case "translucide":
			tr = &cmp.Candidates[i]
		case "avec supports":
			sp = &cmp.Candidates[i]
		}
	}
	if tr == nil || sp == nil {
		t.Fatalf("variantes personnalisées absentes : %+v", cmp.Candidates)
	}
	if len(tr.Changes) != 3 || !strings.Contains(strings.Join(tr.Changes, "|"), "fan_max_speed : ") || !strings.Contains(strings.Join(tr.Changes, "|"), "→ 40") {
		t.Fatalf("changements réels attendus (pas []) : %v", tr.Changes)
	}
	if m, ok := tr.Metrics[MetricUnsupportedOverhang]; !ok || m.Value < 9000 {
		t.Fatalf("colonne de surface non supportée : %+v", tr.Metrics)
	}
	if j := strings.Join(tr.Warnings, "\n"); !strings.Contains(j, "SANS supports") || !strings.Contains(j, "ventilation réduite : surplombs dégradés") && !strings.Contains(j, "ventilation réduite") {
		t.Fatalf("avertissements de la variante translucide : %s", j)
	}
	if m := sp.Metrics[MetricUnsupportedOverhang]; m.Value != 0 || strings.Contains(strings.Join(sp.Warnings, "\n"), "SANS supports") {
		t.Fatalf("variante avec supports : %+v %v", m, sp.Warnings)
	}
}

// modify_slicing_profile : la bordure absente du profil est résolue (défaut de Bambu Studio), jamais « null ».
func TestRealModifyProfileResolvesBrimDefault(t *testing.T) {
	st := installProfiles(t)
	e := newEnv(t, st, nil, false)
	base := &BaseSpec{Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}}
	out, err := e.svc.ModifyProfile(e.ctx, ModifyInput{Base: base, Modifications: []slicer.Override{{Key: "brim_type", Value: "outer_only"}, {Key: "brim_width", Value: 6}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range out.Applied {
		if a.Key == "brim_type" && (a.Old == nil || a.Old == "") {
			t.Errorf("brim_type : ancienne valeur non résolue (null) : %+v", a)
		}
	}
	for _, d := range out.Diff {
		if d.Key == "brim_type" && (d.Old == nil || d.Old == "") {
			t.Errorf("diff brim_type : ancienne valeur non résolue : %+v", d)
		}
	}
	eff := map[string]EffectiveSetting{}
	for _, s := range out.Effective {
		eff[s.Key] = s
	}
	for _, k := range []string{"brim_type", "brim_width", "raft_layers", "enable_support", "support_type"} {
		if eff[k].Value == "" || eff[k].Source == "" || eff[k].Source == "inconnue" {
			t.Errorf("réglage effectif %s non résolu : %+v", k, eff[k])
		}
	}
	if eff["brim_type"].Value != "outer_only" || eff["brim_type"].Source != "surcharge" || eff["brim_width"].Value != "6" {
		t.Errorf("valeurs après modification : %+v %+v", eff["brim_type"], eff["brim_width"])
	}
	// sans modification de bordure : le défaut ou la valeur du profil est quand même donné
	out2, err := e.svc.ModifyProfile(e.ctx, ModifyInput{Base: base, Modifications: []slicer.Override{{Key: "wall_loops", Value: 3}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range out2.Effective {
		if s.Key == "brim_type" && (s.Value == "" || s.Source == "inconnue") {
			t.Errorf("brim_type effectif non résolu : %+v", s)
		}
	}
}

// Une clé « filament sans suffixe » : « Bambu PETG Translucent » est résolu vers le préréglage de la P1S.
func TestRealResolveBaseFilamentWithoutSuffix(t *testing.T) {
	st := installProfiles(t)
	e := newEnv(t, st, nil, false)
	out, err := e.svc.ModifyProfile(e.ctx, ModifyInput{
		Base:          &BaseSpec{Machine: p1sMachine, Process: p1sProcess, Filaments: []string{"Bambu PETG Translucent"}},
		Modifications: []slicer.Override{{Key: "wall_loops", Value: 3}}})
	if err != nil {
		t.Fatalf("« Bambu PETG Translucent » doit être résolu : %v", err)
	}
	if len(out.Filaments) != 1 || !strings.Contains(out.Filaments[0].Name, "PETG Translucent @BBL") {
		t.Fatalf("préréglage résolu : %+v", out.Filaments)
	}
	if !strings.Contains(strings.Join(out.Notes, "|"), "résolu vers") {
		t.Fatalf("la résolution doit être dite : %v", out.Notes)
	}
}
