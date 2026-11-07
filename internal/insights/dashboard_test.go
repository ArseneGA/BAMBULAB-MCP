package insights

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"strings"
	"testing"
	"time"
)

func TestDashboardAggregates(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seedPrinter("pr1", "p1s", "Atelier P1S")
	e.seedPrinter("pr2", "a1mini", "Petite A1")
	e.seedModel("mod1", "Boîtier", "colA", "Projet Alpha")
	e.seedModel("mod2", "Support", "", "")
	e.done("j1", 1, "COMPLETED")
	e.done("j2", 1, "COMPLETED")
	e.done("j3", 8, "FAILED")
	e.done("j4", 9, "CANCELLED")
	// sur l'autre imprimante, PETG, 1 h
	st := t0.AddDate(0, 0, 10)
	e.seedJob(jobSpec{ID: "j5", State: "COMPLETED", Printer: "pr2", Model: "mod2", Type: "PETG", Seconds: 3600, Grams: 50, Start: st, End: st.Add(time.Hour), Percent: 100})
	// une impression préparée pas terminée, du projet Alpha
	e.seedJob(jobSpec{ID: "jp", State: "PREPARED", Printer: "pr1", Model: "mod1", Seconds: 3600, Grams: 50})
	// un job hors période (trop ancien)
	old := t0.AddDate(0, -3, 0)
	e.seedJob(jobSpec{ID: "jold", State: "COMPLETED", Printer: "pr1", Model: "mod1", Seconds: 3600, Grams: 50, Start: old, End: old.Add(time.Hour), Percent: 100})

	d, err := e.svc.GetDashboard(ctx, DashboardQuery{From: "2026-11-01", To: "2026-11-20"})
	if err != nil {
		t.Fatal(err)
	}
	tt := d.Totals
	if tt.Prints != 5 || tt.Succeeded != 3 || tt.Failed != 1 || tt.Cancelled != 1 || tt.Partial != 0 {
		t.Fatalf("compteurs : %+v", tt)
	}
	if tt.SuccessRate == nil || *tt.SuccessRate != 0.75 { // 3 / (3+1), l'annulation est exclue
		t.Errorf("taux : %v", tt.SuccessRate)
	}
	if !near(tt.MachineHours, 9, 0.001) { // 4 × 2 h + 1 h
		t.Errorf("heures : %v", tt.MachineHours)
	}
	if tt.Cost <= 0 || d.Currency != "EUR" || !strings.Contains(d.Label, "ESTIMATIONS") {
		t.Errorf("coût : %v %q", tt.Cost, d.Label)
	}
	if d.Period != "day" {
		t.Errorf("découpage : %s", d.Period)
	}
	// par matière : PLA et PETG
	mats := map[string]DashRow{}
	for _, r := range d.ByMaterial {
		mats[r.Key] = r
	}
	if mats["PLA"].Prints != 4 || mats["PETG"].Prints != 1 || mats["PETG"].Grams != 50 {
		t.Errorf("matières : %+v", mats)
	}
	// par imprimante (nom lu dans la table des imprimantes)
	if len(d.ByPrinter) != 2 || d.ByPrinter[0].Name != "Atelier P1S" || d.ByPrinter[0].Prints != 4 || d.ByPrinter[0].Hours != 8 {
		t.Errorf("imprimantes : %+v", d.ByPrinter)
	}
	// par période : toutes les journées de la plage y figurent, même vides
	if len(d.ByPeriod) != 20 {
		t.Errorf("périodes : %d", len(d.ByPeriod))
	}
	per := map[string]DashRow{}
	for _, r := range d.ByPeriod {
		per[r.Key] = r
	}
	if per["2026-11-02"].Prints != 2 || per["2026-11-05"].Prints != 0 || per["2026-11-09"].FailedHours != 2 {
		t.Errorf("jours : %+v / %+v / %+v", per["2026-11-02"], per["2026-11-05"], per["2026-11-09"])
	}
	// par projet : sans collection -> « hors projet » ; l'impression préparée est chiffrée à part
	var alpha, none *DashRow
	for i := range d.ByProject {
		switch d.ByProject[i].Key {
		case "colA":
			alpha = &d.ByProject[i]
		case "(aucun)":
			none = &d.ByProject[i]
		}
	}
	if none == nil || none.Prints != 1 {
		t.Errorf("hors projet : %+v", d.ByProject)
	}
	if alpha == nil || alpha.Prints != 4 || alpha.PendingJobs != 1 || alpha.PendingEstimate <= 0 || alpha.Name != "Projet Alpha" {
		t.Errorf("projet Alpha : %+v", alpha)
	}
	txt := d.Text()
	for _, want := range []string{"5 impression(s)", "75 %", "ESTIMÉ", "Par matière", "Par imprimante"} {
		if !strings.Contains(txt, want) {
			t.Errorf("texte sans %q :\n%s", want, txt)
		}
	}
	// filtre imprimante
	d2, _ := e.svc.GetDashboard(ctx, DashboardQuery{From: "2026-11-01", To: "2026-11-20", PrinterID: "pr2"})
	if d2.Totals.Prints != 1 {
		t.Errorf("filtre imprimante : %+v", d2.Totals)
	}
	// découpages
	for _, p := range []string{"week", "month"} {
		if dd, err := e.svc.GetDashboard(ctx, DashboardQuery{From: "2026-08-01", To: "2026-11-20", Period: p}); err != nil || len(dd.ByPeriod) == 0 {
			t.Errorf("%s : %v", p, err)
		}
	}
}

func TestDashboardValidationAndEmpty(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for name, q := range map[string]DashboardQuery{
		"période": {Period: "year"}, "date": {From: "hier"}, "vide": {From: "2026-11-10", To: "2026-11-01"},
		"jour trop long": {From: "2024-01-01", To: "2026-11-01", Period: "day"},
	} {
		if _, err := e.svc.GetDashboard(ctx, q); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s : %v", name, err)
		}
	}
	d, err := e.svc.GetDashboard(ctx, DashboardQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Totals.Prints != 0 || d.Totals.SuccessRate != nil || !strings.Contains(d.Text(), "Aucune impression") || len(d.ByPeriod) == 0 {
		t.Errorf("vide : %+v", d.Totals)
	}
}

func TestChartIsAValidPNG(t *testing.T) {
	e := newEnv(t)
	e.seedPrinter("pr1", "p1s", "P1S")
	for i := 0; i < 6; i++ {
		e.done("j"+string(rune('a'+i)), i*3, "COMPLETED")
	}
	e.done("jf", 4, "FAILED")
	d, err := e.svc.GetDashboard(context.Background(), DashboardQuery{From: "2026-11-01", To: "2026-11-30"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := RenderChart(d)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil || img.Bounds().Dx() < 400 || len(b) < 1500 {
		t.Fatalf("PNG : %v %v %d octets", err, img.Bounds(), len(b))
	}
	// jeu de données vide : le graphe se dessine quand même
	empty, _ := newEnv(t).svc.GetDashboard(context.Background(), DashboardQuery{})
	if b, err := RenderChart(empty); err != nil || len(b) == 0 {
		t.Errorf("vide : %v", err)
	}
}

func TestApplyToHistoryRecomputesCosts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.done("j1", 1, "COMPLETED")
	d1, _ := e.svc.GetDashboard(ctx, DashboardQuery{From: "2026-11-01", To: "2026-11-30"})
	before := d1.Totals.Cost
	// prix modifié sans recalcul : l'historique garde l'ancien coût
	if _, err := e.svc.SetSettings(ctx, SettingsPatch{FilamentPerKg: map[string]float64{"PLA": 200}}); err != nil {
		t.Fatal(err)
	}
	d2, _ := e.svc.GetDashboard(ctx, DashboardQuery{From: "2026-11-01", To: "2026-11-30"})
	if d2.Totals.Cost != before {
		t.Errorf("l'historique ne doit pas bouger sans apply_to_history : %v vs %v", d2.Totals.Cost, before)
	}
	if _, err := e.svc.SetSettings(ctx, SettingsPatch{ApplyToHistory: true}); err != nil {
		t.Fatal(err)
	}
	d3, _ := e.svc.GetDashboard(ctx, DashboardQuery{From: "2026-11-01", To: "2026-11-30"})
	if d3.Totals.Cost < 20 { // 100 g à 200/kg
		t.Errorf("après recalcul : %v", d3.Totals.Cost)
	}
}
