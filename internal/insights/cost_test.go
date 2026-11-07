package insights

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestMaterialFamily(t *testing.T) {
	cases := map[string]string{
		"PLA": "PLA", "Bambu PLA Basic @BBL P1S 0.4 nozzle": "PLA", "Generic PETG": "PETG", "PETG-CF": "PETG-CF", "PETG HF": "PETG",
		"Bambu PA6-CF": "PA-CF", "PAHT-CF": "PA-CF", "ABS": "ABS", "asa": "ASA", "TPU 95A HF": "TPU", "Bambu PC": "PC",
		"Support for PLA": "PLA", "PLA-CF": "PLA-CF", "plastique": "", "": "", "Bambu Support for PA/PET": "PA", "PVA": "PVA", "PET-CF": "PET",
	}
	for in, want := range cases {
		if got := MaterialFamily(in); got != want {
			t.Errorf("MaterialFamily(%q) = %q, attendu %q", in, got, want)
		}
	}
}

func TestModelKey(t *testing.T) {
	cases := map[string]string{"Bambu Lab P1S 0.4 nozzle": "p1s", "p1s": "p1s", "Bambu Lab A1 mini 0.4 nozzle": "a1mini", "Bambu Lab A1": "a1",
		"X1 Carbon": "x1c", "x1c": "x1c", "Bambu Lab H2D": "h2d", "inconnu": "", "": ""}
	for in, want := range cases {
		if got := modelKey(in); got != want {
			t.Errorf("modelKey(%q) = %q, attendu %q", in, got, want)
		}
	}
}

func TestEmbeddedDataLoads(t *testing.T) {
	pw := loadPower()
	if len(pw.Models) < 5 || pw.Level != "indicative" {
		t.Fatalf("puissances : %+v", pw.Level)
	}
	for k, m := range pw.Models {
		if m.PreheatW <= 0 || m.PrintW <= 0 || m.PreheatW < m.PrintW {
			t.Errorf("modèle %s : puissances incohérentes %+v", k, m)
		}
	}
	for k := range pw.Aliases {
		if _, ok := pw.Models[k]; !ok {
			t.Errorf("alias %s sans modèle", k)
		}
	}
	ad := loadAdj()
	if ad.Level != "heuristic" || len(ad.Rules) < 10 {
		t.Fatalf("règles : %d", len(ad.Rules))
	}
	for _, r := range ad.Rules {
		if !validDefect(r.Defect) || len(r.Adjustments) == 0 || r.Tradeoffs == "" {
			t.Errorf("règle %s incomplète", r.Defect)
		}
		for _, a := range r.Adjustments {
			if a.Key == "" || a.Why == "" || a.Amount == "" {
				t.Errorf("règle %s : ajustement incomplet %+v", r.Defect, a)
			}
		}
		for _, s := range r.Sources {
			if _, ok := ad.Sources[s]; !ok {
				t.Errorf("règle %s : source %s inconnue", r.Defect, s)
			}
		}
	}
	pp := loadPP()
	if pp.Level != "heuristic" || len(pp.Techniques) < 8 {
		t.Fatalf("post-traitement : %d techniques", len(pp.Techniques))
	}
	for _, tq := range pp.Techniques {
		if tq.ID == "" || len(tq.Triggers) == 0 || len(tq.ByFamily) == 0 {
			t.Errorf("technique %q incomplète", tq.ID)
		}
		for fam, f := range tq.ByFamily {
			switch f.Applies {
			case "yes", "limited":
				if len(f.Steps) == 0 && len(tq.Common.Steps) == 0 {
					t.Errorf("%s/%s : aucune étape", tq.ID, fam)
				}
			case "no":
				if f.Reason == "" {
					t.Errorf("%s/%s : refus sans raison", tq.ID, fam)
				}
			default:
				t.Errorf("%s/%s : applies %q inconnu", tq.ID, fam, f.Applies)
			}
		}
	}
}

func TestComputeDefaultsIsDeterministicAndLabelled(t *testing.T) {
	set := stored{}.view(false)
	est := compute(costPlan{Kind: "estimation", PrinterHint: "Bambu Lab P1S", Seconds: 7200, Basis: "test",
		Filaments: []planFilament{{Slot: 1, Material: "PLA", Family: "PLA", Grams: 100}}}, set)
	if !strings.HasPrefix(est.Label, "ESTIMATION") {
		t.Errorf("libellé : %q", est.Label)
	}
	// filament : 100 g à 20/kg = 2,00
	if est.FilamentSum != 2.00 || est.Filament[0].PriceSource != "valeur par défaut indicative" {
		t.Errorf("filament : %+v", est.Filament)
	}
	// électricité : 5 min à 450 W + 115 min à 110 W = 37,5 + 210,83 Wh = 0,2483 kWh -> 0,06
	if !near(est.Electricity.KWh, 0.248, 0.001) || est.Electricity.Amount != 0.06 || est.Electricity.PowerSource != "indicatif" {
		t.Errorf("électricité : %+v", est.Electricity)
	}
	if est.Wear.Amount != 0 || !near(est.Wear.MachineHours, 2, 1e-9) {
		t.Errorf("usure : %+v", est.Wear)
	}
	if est.Total != 2.06 {
		t.Errorf("total %v", est.Total)
	}
	joined := strings.Join(est.Assumptions, "\n")
	for _, want := range []string{"INDICATIVE", "INDICATIVES", "non comptée", "purges"} {
		if !strings.Contains(joined, want) {
			t.Errorf("hypothèse manquante %q dans :\n%s", want, joined)
		}
	}
	if !strings.Contains(est.Text(), "ESTIMATION") {
		t.Error("le texte doit rappeler que c'est une estimation")
	}
}

func TestComputePricePrecedenceAndWear(t *testing.T) {
	price := 0.25
	set := stored{ElectricityPerKWh: &price, FilamentPerKg: map[string]float64{"PLA": 30},
		SpoolPerKg: map[string]float64{"bambu pla basic noir": 40}, MachineWearPerHr: 0.10, NozzleWearPerHr: 0.05}.view(true)
	p := costPlan{Kind: "estimation", PrinterHint: "x1c", Seconds: 3600, Basis: "t", Filaments: []planFilament{
		{Slot: 1, Family: "PLA", Grams: 50, Price: 100}, // saisi
		{Slot: 2, Family: "PLA", Grams: 50, Label: "Bambu PLA Basic noir"},
		{Slot: 3, Family: "PLA", Grams: 50},
		{Slot: 4, Family: "ABS", Grams: 50}, // famille non réglée -> défaut indicatif
	}}
	est := compute(p, set)
	src := []string{est.Filament[0].PriceSource, est.Filament[1].PriceSource, est.Filament[2].PriceSource, est.Filament[3].PriceSource}
	want := []string{"saisi", "réglage de la bobine", "réglage de la famille", "valeur par défaut indicative"}
	for i := range want {
		if src[i] != want[i] {
			t.Errorf("source %d : %q, attendu %q", i, src[i], want[i])
		}
	}
	if est.Filament[0].Amount != 5 || est.Filament[1].Amount != 2 || est.Filament[2].Amount != 1.5 {
		t.Errorf("montants %+v", est.Filament)
	}
	if est.Wear.Amount != 0.15 {
		t.Errorf("usure %+v", est.Wear)
	}
	if strings.Contains(strings.Join(est.Assumptions, "|"), "non comptée") {
		t.Error("l'usure est comptée : pas de mention « non comptée »")
	}
	// ABS : préchauffage plus long et facteur de puissance 1,5 pour la matière dominante (égalité : la dernière)
	if est.Electricity.PreheatMinutes != 12 {
		t.Errorf("chauffe ABS : %+v", est.Electricity)
	}
}

func TestPowerOverrideAndUnknownModel(t *testing.T) {
	set := DefaultSettings()
	set.PowerOverrides = map[string]PowerOverride{"p1s": {PrintW: 200}}
	pp := powerFor("Bambu Lab P1S", "PLA", set)
	if pp.PrintW != 200 || pp.PreheatW != 450 || pp.Source != "réglage utilisateur" {
		t.Errorf("surcharge : %+v", pp)
	}
	un := powerFor("imprimante maison", "PLA", DefaultSettings())
	if un.ModelKey != "" || un.Source != "indicatif" || un.PrintW <= 0 {
		t.Errorf("modèle inconnu : %+v", un)
	}
}

func TestSettingsLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	st, err := e.svc.GetSettings(ctx)
	if err != nil || !st.UsingDefaults || st.Currency != "EUR" || !hasStr(st.DefaultedPrices, "electricity") || !hasStr(st.DefaultedPrices, "PLA") {
		t.Fatalf("défauts : %+v %v", st, err)
	}
	kwh, cur := 0.18, "usd"
	st, err = e.svc.SetSettings(ctx, SettingsPatch{ElectricityPerKWh: &kwh, Currency: &cur, FilamentPerKg: map[string]float64{"pla": 17.5, "Bambu PETG HF": 19},
		SpoolPerKg: map[string]float64{" Bambu PLA Basic Noir ": 15}, PowerOverrides: map[string]PowerOverride{"Bambu Lab P1S": {PreheatW: 380, PrintW: 95}}})
	if err != nil {
		t.Fatal(err)
	}
	if st.UsingDefaults || st.Currency != "USD" || st.ElectricityPerKWh != 0.18 || st.FilamentPerKg["PLA"] != 17.5 || st.FilamentPerKg["PETG"] != 19 ||
		st.SpoolPerKg["bambu pla basic noir"] != 15 || st.PowerOverrides["p1s"].PrintW != 95 {
		t.Fatalf("réglages : %+v", st)
	}
	if hasStr(st.DefaultedPrices, "electricity") || hasStr(st.DefaultedPrices, "PLA") || !hasStr(st.DefaultedPrices, "ABS") {
		t.Errorf("prix encore par défaut : %v", st.DefaultedPrices)
	}
	// 0 supprime : retour à la valeur par défaut
	st, err = e.svc.SetSettings(ctx, SettingsPatch{FilamentPerKg: map[string]float64{"PLA": 0}, PowerOverrides: map[string]PowerOverride{"p1s": {}}})
	if err != nil || st.FilamentPerKg["PLA"] != 20 || !hasStr(st.DefaultedPrices, "PLA") || len(st.PowerOverrides) != 0 {
		t.Fatalf("retour aux défauts : %+v %v", st, err)
	}
	// persistance
	again, _ := e.svc.GetSettings(ctx)
	if again.ElectricityPerKWh != 0.18 || again.Currency != "USD" {
		t.Errorf("relecture : %+v", again)
	}
	neg, big, bad := -1.0, 99.0, "EURO"
	for name, p := range map[string]SettingsPatch{
		"kWh négatif":      {ElectricityPerKWh: &neg},
		"kWh énorme":       {ElectricityPerKWh: &big},
		"devise":           {Currency: &bad},
		"prix négatif":     {FilamentPerKg: map[string]float64{"PLA": -3}},
		"prix énorme":      {SpoolPerKg: map[string]float64{"x": 1e9}},
		"modèle inconnu":   {PowerOverrides: map[string]PowerOverride{"zzz": {PrintW: 10}}},
		"puissance énorme": {PowerOverrides: map[string]PowerOverride{"p1s": {PrintW: 1e6}}},
		"usure négative":   {MachineWearPerHr: &neg},
	} {
		if _, err := e.svc.SetSettings(ctx, p); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s : erreur attendue, obtenu %v", name, err)
		}
	}
}

func TestEstimateCostFromPlanAndValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	o, err := e.svc.EstimateCost(ctx, EstimateInput{PrinterModel: "a1mini", DurationMinutes: 90,
		Filaments: []FilamentInput{{Material: "PETG", Grams: 40}, {Material: "PLA", Grams: 10, PricePerKg: 50}}})
	if err != nil {
		t.Fatal(err)
	}
	if o.Actual != nil || o.Estimate.Kind != "estimation" || len(o.Estimate.Filament) != 2 || o.Estimate.Electricity.Model != "Bambu Lab A1 mini" {
		t.Fatalf("plan : %+v", o)
	}
	if o.Estimate.Filament[1].Amount != 0.5 || o.Estimate.Filament[0].Amount != 0.88 {
		t.Errorf("montants %+v", o.Estimate.Filament)
	}
	for name, in := range map[string]EstimateInput{
		"vide":           {},
		"sans matière":   {DurationMinutes: 10},
		"sans durée":     {Filaments: []FilamentInput{{Material: "PLA", Grams: 1}}},
		"masse négative": {DurationMinutes: 10, Filaments: []FilamentInput{{Material: "PLA", Grams: -1}}},
		"les deux":       {PrintJobID: "x", DurationMinutes: 10},
	} {
		if _, err := e.svc.EstimateCost(ctx, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s : %v", name, err)
		}
	}
	if _, err := e.svc.EstimateCost(ctx, EstimateInput{PrintJobID: "inconnu"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("impression inconnue : %v", err)
	}
}

func TestEstimateCostFromJobWithActual(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	start := t0
	// durée prévue 2 h, observée 3 h
	e.seedJob(jobSpec{ID: "job1", State: "COMPLETED", Printer: "pr1", Seconds: 7200, Grams: 100, Start: start, End: start.Add(3 * time.Hour), Percent: 100})
	o, err := e.svc.EstimateCost(ctx, EstimateInput{PrintJobID: "job1"})
	if err != nil {
		t.Fatal(err)
	}
	if o.Estimate.Total != 2.06 || o.Estimate.Filament[0].Material != "Bambu PLA Basic noir" || o.Estimate.Seconds != 7200 {
		t.Fatalf("estimation : %+v", o.Estimate)
	}
	if o.Actual == nil || o.Actual.Kind != "réel" || o.Actual.Seconds != 10800 || !strings.Contains(o.Actual.Label, "COÛT RÉEL ESTIMÉ") || !near(o.Actual.Wear.MachineHours, 3, 1e-9) {
		t.Fatalf("réel : %+v", o.Actual)
	}
	if o.Actual.Electricity.KWh <= o.Estimate.Electricity.KWh {
		t.Errorf("trois heures coûtent plus d'énergie que deux : %v vs %v", o.Actual.Electricity.KWh, o.Estimate.Electricity.KWh)
	}
	// impression échouée à 40 % : masse au prorata, signalée dans les hypothèses
	e.seedJob(jobSpec{ID: "job2", State: "FAILED", Printer: "pr1", Seconds: 7200, Grams: 100, Start: start, End: start.Add(time.Hour), Percent: 40})
	o2, err := e.svc.EstimateCost(ctx, EstimateInput{PrintJobID: "job2"})
	if err != nil || o2.Actual == nil {
		t.Fatalf("échec : %v %+v", err, o2)
	}
	if !near(o2.Actual.Grams, 40, 0.01) || !strings.Contains(strings.Join(o2.Actual.Assumptions, "|"), "au prorata de l'avancement") {
		t.Errorf("masse au prorata : %+v", o2.Actual)
	}
	// impression non terminée : pas de coût réel
	e.seedJob(jobSpec{ID: "job3", State: "PRINTING", Printer: "pr1", Seconds: 600, Grams: 5, Start: start})
	o3, err := e.svc.EstimateCost(ctx, EstimateInput{PrintJobID: "job3"})
	if err != nil || o3.Actual != nil {
		t.Errorf("en cours : %v %+v", err, o3.Actual)
	}
}
