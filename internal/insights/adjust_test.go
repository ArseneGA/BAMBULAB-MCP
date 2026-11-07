package insights

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// seedLogs crée n impressions terminées avec les défauts et la note donnés (PLA, P1S).
func (e *env) rated(id string, day int, rating int, defects []string, model string) {
	e.t.Helper()
	e.seedJob(jobSpec{ID: id, State: "COMPLETED", Printer: "pr1", Model: model, Seconds: 3600, Grams: 20, Start: t0.AddDate(0, 0, day), End: t0.AddDate(0, 0, day).Add(time.Hour), Percent: 100,
		Overrides: []slicer.Override{{Key: "nozzle_temperature", Value: "215", Target: "filament"}}})
	if _, err := e.svc.UpdateLog(context.Background(), UpdateInput{ID: id, Rating: &rating, Defects: defects}); err != nil {
		e.t.Fatal(err)
	}
}

func TestSuggestAdjustmentsRecurrenceAndReference(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seedPrinter("pr1", "p1s", "P1S")
	e.seedModel("mod1", "Support", "", "")
	e.rated("j1", 1, 3, []string{"stringing"}, "mod1")
	e.rated("j2", 2, 2, []string{"stringing", "warping"}, "mod1")
	e.rated("j3", 3, 5, nil, "mod1")
	e.rated("j4", 4, 4, nil, "")

	out, err := e.svc.SuggestAdjustments(ctx, AdjustInput{MaterialFamily: "PLA", PrinterModel: "p1s", ModelID: "mod1"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Automatic || out.ComparableLogs != 4 || out.Level != "heuristic" || !strings.Contains(out.HowToApply, "Rien n'est appliqué") {
		t.Fatalf("sortie : %+v", out)
	}
	if len(out.Proposals) != 1 || out.Proposals[0].Defect != "stringing" || out.Proposals[0].Occurrences != 2 || !strings.HasPrefix(out.Proposals[0].Basis, "récurrent") {
		t.Fatalf("propositions (warping ne figure qu'une fois : non récurrent) : %+v", out.Proposals)
	}
	p := out.Proposals[0]
	if len(p.Adjustments) < 2 || p.Tradeoffs == "" || len(p.Sources) == 0 || p.Sources[0].Verified {
		t.Errorf("justification incomplète : %+v", p)
	}
	for _, a := range p.Adjustments {
		if a.Key == "" || a.Why == "" || a.Target == "" {
			t.Errorf("ajustement incomplet : %+v", a)
		}
		if a.Key == "retraction_length" && len(a.Families) > 0 && hasStr(a.Families, "TPU") {
			t.Errorf("la règle TPU ne s'applique pas au PLA : %+v", a)
		}
	}
	// meilleure fiche : note 5 du même modèle
	if out.BestReference == nil || out.BestReference.LogID == "" || out.BestReference.Rating != 5 || !out.BestReference.SameModel || len(out.BestReference.Overrides) != 1 {
		t.Errorf("référence : %+v", out.BestReference)
	}

	// défauts demandés explicitement + fiche de départ
	l2, _ := e.svc.GetLog(ctx, "j2")
	out, err = e.svc.SuggestAdjustments(ctx, AdjustInput{LogID: l2.ID, Defects: []string{"dimensions"}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range out.Proposals {
		got[p.Defect] = p.Basis
	}
	if len(got) != 3 || !strings.HasPrefix(got["warping"], "coché sur la fiche") || got["dimensions"] != "demandé" || !strings.HasPrefix(got["stringing"], "coché") {
		t.Errorf("bases : %v", got)
	}
	if out.MaterialFamily != "PLA" || out.PrinterModel != "Bambu Lab P1S" {
		t.Errorf("contexte déduit de la fiche : %s %s", out.MaterialFamily, out.PrinterModel)
	}
}

func TestSuggestAdjustmentsFamilyFilteringAndErrors(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tpu, err := e.svc.SuggestAdjustments(ctx, AdjustInput{MaterialFamily: "TPU", Defects: []string{"stringing"}})
	if err != nil {
		t.Fatal(err)
	}
	if tpu.ComparableLogs != 0 || len(tpu.Proposals) != 1 || !strings.Contains(strings.Join(tpu.Notes, "|"), "Aucune fiche comparable") {
		t.Fatalf("TPU : %+v", tpu)
	}
	var retr []Adjustment
	for _, a := range tpu.Proposals[0].Adjustments {
		if a.Key == "retraction_length" {
			retr = append(retr, a)
		}
	}
	if len(retr) != 1 || !strings.Contains(retr[0].Amount, "0,1 à 0,2") {
		t.Errorf("TPU : une seule règle de rétraction, plus douce : %+v", retr)
	}
	pla, _ := e.svc.SuggestAdjustments(ctx, AdjustInput{MaterialFamily: "pla", Defects: []string{"stringing"}})
	for _, a := range pla.Proposals[0].Adjustments {
		if a.Key == "retraction_length" && !strings.Contains(a.Amount, "0,2 à 0,4") {
			t.Errorf("PLA : %+v", a)
		}
	}
	// ABS : la règle de ventilation du warping s'applique, pas au PLA
	abs, _ := e.svc.SuggestAdjustments(ctx, AdjustInput{MaterialFamily: "ABS", Defects: []string{"warping"}})
	if !hasKey(abs.Proposals[0].Adjustments, "close_fan_the_first_x_layers") {
		t.Error("ABS : ventilateur coupé au début attendu")
	}
	pla, _ = e.svc.SuggestAdjustments(ctx, AdjustInput{MaterialFamily: "PLA", Defects: []string{"warping"}})
	if hasKey(pla.Proposals[0].Adjustments, "close_fan_the_first_x_layers") {
		t.Error("PLA : pas de règle ABS")
	}
	for name, in := range map[string]AdjustInput{
		"sans matière":   {},
		"défaut inconnu": {MaterialFamily: "PLA", Defects: []string{"licorne"}},
	} {
		if _, err := e.svc.SuggestAdjustments(ctx, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s : %v", name, err)
		}
	}
	if _, err := e.svc.SuggestAdjustments(ctx, AdjustInput{LogID: "inconnu"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("fiche inconnue : %v", err)
	}
	// défaut sans règle : signalé sans proposition inventée
	other, _ := e.svc.SuggestAdjustments(ctx, AdjustInput{MaterialFamily: "PLA", Defects: []string{"other"}})
	if len(other.Proposals) != 0 || !strings.Contains(strings.Join(other.Notes, "|"), "aucune règle") {
		t.Errorf("défaut sans règle : %+v", other)
	}
}

func hasKey(l []Adjustment, k string) bool {
	for _, a := range l {
		if a.Key == k {
			return true
		}
	}
	return false
}

func TestSuggestIsNotMixingOtherMachinesOrMaterials(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seedPrinter("pr1", "p1s", "P1S")
	e.rated("j1", 1, 2, []string{"stringing"}, "")
	e.rated("j2", 2, 2, []string{"stringing"}, "")
	// autre famille de matière
	out, _ := e.svc.SuggestAdjustments(ctx, AdjustInput{MaterialFamily: "PETG"})
	if out.ComparableLogs != 0 || len(out.Proposals) != 0 {
		t.Errorf("PETG ne reprend pas les fiches PLA : %+v", out)
	}
	// autre machine
	out, _ = e.svc.SuggestAdjustments(ctx, AdjustInput{MaterialFamily: "PLA", PrinterModel: "x1c"})
	if out.ComparableLogs != 0 || len(out.Proposals) != 0 {
		t.Errorf("X1C ne reprend pas les fiches P1S : %+v", out)
	}
	out, _ = e.svc.SuggestAdjustments(ctx, AdjustInput{MaterialFamily: "PLA", PrinterModel: "p1s"})
	if out.ComparableLogs != 2 || len(out.Proposals) != 1 {
		t.Errorf("P1S : %+v", out)
	}
}
