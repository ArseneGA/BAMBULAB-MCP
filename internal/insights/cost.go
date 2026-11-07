package insights

import (
	"context"
	"fmt"
	"math"
	"strings"
)

// FilamentInput décrit la matière d'un emplacement dans un plan saisi à la main.
type FilamentInput struct {
	Material   string  `json:"material" jsonschema:"famille ou libellé de la matière (PLA, PETG, « Bambu PLA Basic »...)"`
	Grams      float64 `json:"grams" jsonschema:"masse de filament prévue pour cet emplacement, en grammes"`
	PricePerKg float64 `json:"price_per_kg,omitempty" jsonschema:"prix du kg de CETTE bobine (prioritaire sur les réglages) ; omis : réglage de la bobine puis de la famille"`
	Label      string  `json:"label,omitempty" jsonschema:"libellé de la bobine, pour retrouver son prix dans les réglages (spool_per_kg)"`
}

// EstimateInput demande une estimation de coût sur une impression existante OU sur un plan saisi.
type EstimateInput struct {
	PrintJobID      string          `json:"print_job_id,omitempty" jsonschema:"impression (artefact découpé) à chiffrer : masses et durée lues dans l'archive découpée"`
	Plate           int             `json:"plate,omitempty" jsonschema:"plaque à chiffrer (défaut : celle de l'impression)"`
	PrinterModel    string          `json:"printer_model,omitempty" jsonschema:"plan saisi : modèle d'imprimante (p1s, x1c, a1, a1mini, h2d...) ou preset machine"`
	DurationMinutes float64         `json:"duration_minutes,omitempty" jsonschema:"plan saisi : durée d'impression estimée, chauffe comprise"`
	Filaments       []FilamentInput `json:"filaments,omitempty" jsonschema:"plan saisi : matière et masse par emplacement"`
}

// FilamentCost est la ligne de coût d'un emplacement.
type FilamentCost struct {
	Slot        int     `json:"slot,omitempty"`
	Material    string  `json:"material"`
	Family      string  `json:"family,omitempty"`
	Grams       float64 `json:"grams"`
	PricePerKg  float64 `json:"price_per_kg"`
	PriceSource string  `json:"price_source" jsonschema:"saisi | réglage de la bobine | réglage de la famille | valeur par défaut indicative"`
	Amount      float64 `json:"amount"`
}

// ElectricityCost détaille l'énergie.
type ElectricityCost struct {
	Model          string  `json:"model"`
	PowerSource    string  `json:"power_source" jsonschema:"indicatif | réglage utilisateur"`
	PreheatMinutes float64 `json:"preheat_minutes"`
	PreheatW       float64 `json:"preheat_w"`
	PrintMinutes   float64 `json:"print_minutes"`
	PrintW         float64 `json:"print_w"`
	KWh            float64 `json:"kwh"`
	PricePerKWh    float64 `json:"price_per_kwh"`
	Amount         float64 `json:"amount"`
}

// WearCost détaille l'usure et le temps machine.
type WearCost struct {
	MachineHours float64 `json:"machine_hours"`
	MachineWear  float64 `json:"machine_wear_amount"`
	NozzleWear   float64 `json:"nozzle_wear_amount"`
	Amount       float64 `json:"amount"`
}

// CostEstimate est une estimation de coût. Kind vaut « estimation » (durée et masses prévues) ou
// « réel » (durée observée et masse déclarée ou déduite) ; dans les deux cas ce n'est PAS une facture.
type CostEstimate struct {
	Kind        string          `json:"kind" jsonschema:"estimation | réel (durée observée)"`
	Label       string          `json:"label"`
	Currency    string          `json:"currency"`
	Filament    []FilamentCost  `json:"filament"`
	Electricity ElectricityCost `json:"electricity"`
	Wear        WearCost        `json:"wear"`
	FilamentSum float64         `json:"filament_total"`
	ElectricSum float64         `json:"electricity_total"`
	WearSum     float64         `json:"wear_total"`
	Total       float64         `json:"total"`
	Seconds     float64         `json:"machine_seconds"`
	Grams       float64         `json:"grams_total"`
	Assumptions []string        `json:"assumptions"`
}

// EstimateOutput regroupe l'estimation et, pour une impression terminée, le coût réel.
type EstimateOutput struct {
	PrintJobID string        `json:"print_job_id,omitempty"`
	Estimate   CostEstimate  `json:"estimate"`
	Actual     *CostEstimate `json:"actual,omitempty" jsonschema:"présent seulement si l'impression est terminée et que sa durée a été observée"`
	Note       string        `json:"note"`
}

type planFilament struct {
	Slot     int
	Material string // libellé affiché
	Family   string
	Grams    float64
	Price    float64 // 0 = non saisi
	PriceSrc string  // origine du prix quand il est fourni ("saisi" par défaut)
	Label    string
}

type costPlan struct {
	Kind         string
	PrinterHint  string
	Seconds      float64
	Filaments    []planFilament
	Basis        string
	ExtraAssumes []string
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// compute applique les réglages au plan. Fonction pure.
func compute(p costPlan, set Settings) CostEstimate {
	est := CostEstimate{Kind: p.Kind, Currency: set.Currency, Seconds: p.Seconds}
	if p.Kind == "réel" {
		est.Label = "COÛT RÉEL ESTIMÉ (durée observée) — hypothèses ci-dessous ; ce n'est pas une facture"
	} else {
		est.Label = "ESTIMATION — hypothèses ci-dessous ; ce n'est pas une facture"
	}
	var heaviest planFilament
	preheat := 0.0
	usedDefaultPrice := false
	pd := loadPower()
	for _, f := range p.Filaments {
		fc := FilamentCost{Slot: f.Slot, Material: f.Material, Family: f.Family, Grams: round2(f.Grams)}
		switch {
		case f.Price > 0:
			fc.PricePerKg, fc.PriceSource = f.Price, orStr(f.PriceSrc, "saisi")
		default:
			lbl := strings.ToLower(strings.TrimSpace(f.Label))
			if v, ok := set.SpoolPerKg[lbl]; ok && lbl != "" {
				fc.PricePerKg, fc.PriceSource = v, "réglage de la bobine"
			} else if k, v := familyPrice(set, f.Family); k != "" {
				fc.PricePerKg, fc.PriceSource = v, "réglage de la famille"
				if hasStr(set.DefaultedPrices, k) {
					fc.PriceSource = "valeur par défaut indicative"
				}
			} else {
				fc.PricePerKg, fc.PriceSource = set.FilamentPerKg["other"], "valeur par défaut indicative"
			}
		}
		if fc.PriceSource == "valeur par défaut indicative" {
			usedDefaultPrice = true
		}
		fc.Amount = round2(f.Grams / 1000 * fc.PricePerKg)
		est.Filament = append(est.Filament, fc)
		est.FilamentSum += fc.Amount
		est.Grams += f.Grams
		if f.Grams >= heaviest.Grams {
			heaviest = f
		}
		if m, ok := pd.PreheatMinutes[f.Family]; ok && m > preheat {
			preheat = m
		}
	}
	est.Grams = round2(est.Grams)
	est.FilamentSum = round2(est.FilamentSum)
	if est.Filament == nil {
		est.Filament = []FilamentCost{}
	}

	totalMin := p.Seconds / 60
	pw := powerFor(p.PrinterHint, heaviest.Family, set)
	preheatMin := pw.PreheatMinutes
	if preheat > 0 {
		preheatMin = preheat
	}
	if preheatMin > totalMin/2 {
		preheatMin = totalMin / 2
	}
	printMin := totalMin - preheatMin
	printW := pw.PrintW * pw.PrintFactor
	kwh := (pw.PreheatW*preheatMin + printW*printMin) / 60 / 1000
	est.Electricity = ElectricityCost{Model: pw.ModelName, PowerSource: pw.Source, PreheatMinutes: round2(preheatMin), PreheatW: pw.PreheatW,
		PrintMinutes: round2(printMin), PrintW: round2(printW), KWh: round3(kwh), PricePerKWh: set.ElectricityPerKWh, Amount: round2(kwh * set.ElectricityPerKWh)}
	est.ElectricSum = est.Electricity.Amount

	hours := p.Seconds / 3600
	est.Wear = WearCost{MachineHours: round3(hours), MachineWear: round2(hours * set.MachineWearPerHr), NozzleWear: round2(hours * set.NozzleWearPerHr)}
	est.Wear.Amount = round2(est.Wear.MachineWear + est.Wear.NozzleWear)
	est.WearSum = est.Wear.Amount
	est.Total = round2(est.FilamentSum + est.ElectricSum + est.WearSum)

	as := []string{
		fmt.Sprintf("Basé sur : %s.", p.Basis),
		fmt.Sprintf("Devise %s ; prix du kWh %.4g (réglage %s).", set.Currency, set.ElectricityPerKWh, defaultOr(hasStr(set.DefaultedPrices, "electricity"))),
	}
	if usedDefaultPrice {
		as = append(as, "Au moins un prix de filament est une valeur par défaut INDICATIVE (non constatée) : renseigner vos prix avec set_cost_settings.")
	}
	if pw.Source == "indicatif" {
		as = append(as, fmt.Sprintf("Puissances de %s INDICATIVES (chauffe %.0f W, impression %.0f W × %.2g selon la matière), non mesurées : les remplacer par une mesure (power_overrides).",
			pw.ModelName, pw.PreheatW, pw.PrintW, pw.PrintFactor))
	} else {
		as = append(as, fmt.Sprintf("Puissances de %s fixées par vos réglages (chauffe %.0f W, impression %.0f W × %.2g selon la matière).", pw.ModelName, pw.PreheatW, pw.PrintW, pw.PrintFactor))
	}
	as = append(as, pd.Assumptions...)
	if set.MachineWearPerHr == 0 && set.NozzleWearPerHr == 0 {
		as = append(as, "Usure machine et buse non comptée (réglages à 0).")
	} else {
		as = append(as, "Usure = heures machine × taux d'amortissement machine et buse de vos réglages.")
	}
	as = append(as, "La masse annoncée par le slicer peut ne pas inclure les purges/changements de couleur, les jets d'amorçage ni les impressions ratées ; ni le temps humain, la colle, les consommables et le post-traitement.")
	as = append(as, p.ExtraAssumes...)
	est.Assumptions = as
	return est
}

func defaultOr(def bool) string {
	if def {
		return "par défaut, indicatif"
	}
	return "enregistré"
}

// planFromJob construit le plan chiffrable d'une impression à partir de son archive découpée.
func (s *Service) planFromJob(ctx context.Context, j *jobDoc, plateIdx int) (costPlan, error) {
	pl := j.plate()
	if plateIdx > 0 && j.Summary != nil {
		pl = nil
		for i := range j.Summary.Plates {
			if j.Summary.Plates[i].Index == plateIdx {
				pl = &j.Summary.Plates[i]
			}
		}
		if pl == nil {
			return costPlan{}, invalid("plaque %d absente de l'archive de l'impression %s", plateIdx, j.ID)
		}
	}
	if pl == nil {
		return costPlan{}, invalid("impression %s : aucune archive découpée lue (état %s) ; chiffrer avec un plan saisi (durée, matières, grammes)", j.ID, j.State)
	}
	cp := costPlan{Kind: "estimation", PrinterHint: s.printerModelHint(ctx, j), Seconds: pl.PredictedSeconds,
		Basis: fmt.Sprintf("l'archive découpée de l'impression %s, plaque %d (durée et masses annoncées par le slicer)", j.ID, pl.Index)}
	assigned := map[int]string{}
	for _, a := range j.Assignments {
		assigned[a.FilamentSlot] = a.MaterialID
	}
	for _, f := range pl.Filaments {
		label := assigned[f.Slot]
		preset := ""
		if j.Summary != nil && f.Slot >= 1 && f.Slot <= len(j.Summary.Filaments) {
			preset = j.Summary.Filaments[f.Slot-1]
		}
		fam := MaterialFamily(f.Type)
		if fam == "" {
			fam = MaterialFamily(label)
		}
		if fam == "" {
			fam = MaterialFamily(preset)
		}
		name := f.Type
		if label != "" {
			name = label
		} else if preset != "" {
			name = preset
		}
		pf := planFilament{Slot: f.Slot, Material: name, Family: fam, Grams: f.Grams, Label: label}
		if sp := s.spools(); sp != nil {
			for _, a := range j.Assignments {
				if a.FilamentSlot != f.Slot {
					continue
				}
				if p, ok := sp.PriceForSlot(ctx, j.PrinterID, a.Tray); ok && p.PricePerKg > 0 {
					pf.Price, pf.PriceSrc = p.PricePerKg, "bobine de l'inventaire ("+p.Label+")"
				}
			}
		}
		cp.Filaments = append(cp.Filaments, pf)
	}
	if len(cp.Filaments) == 0 && pl.WeightGrams > 0 {
		fam := ""
		if j.Summary != nil && len(j.Summary.Filaments) > 0 {
			fam = MaterialFamily(j.Summary.Filaments[0])
		}
		cp.Filaments = append(cp.Filaments, planFilament{Slot: 1, Material: fam, Family: fam, Grams: pl.WeightGrams})
		cp.ExtraAssumes = append(cp.ExtraAssumes, "Masse totale de la plaque sans détail par emplacement : attribuée à un seul filament.")
	}
	if pl.PredictedSeconds <= 0 {
		return costPlan{}, invalid("impression %s : durée estimée absente de l'archive ; fournir duration_minutes", j.ID)
	}
	return cp, nil
}

func planFromInput(in EstimateInput) (costPlan, error) {
	if in.DurationMinutes <= 0 || in.DurationMinutes > 60*24*30 {
		return costPlan{}, invalid("duration_minutes : valeur positive attendue (durée estimée par le slicer, chauffe comprise)")
	}
	if len(in.Filaments) == 0 {
		return costPlan{}, invalid("filaments : au moins une matière avec sa masse")
	}
	cp := costPlan{Kind: "estimation", PrinterHint: in.PrinterModel, Seconds: in.DurationMinutes * 60, Basis: "le plan saisi (durée et masses fournies)"}
	for i, f := range in.Filaments {
		if f.Grams < 0 || f.Grams > 100000 || math.IsNaN(f.Grams) {
			return costPlan{}, invalid("filaments[%d].grams : masse positive attendue", i)
		}
		if f.PricePerKg < 0 || f.PricePerKg > 10000 {
			return costPlan{}, invalid("filaments[%d].price_per_kg : entre 0 et 10000", i)
		}
		fam := MaterialFamily(f.Material)
		if fam == "" {
			fam = MaterialFamily(f.Label)
		}
		cp.Filaments = append(cp.Filaments, planFilament{Slot: i + 1, Material: f.Material, Family: fam, Grams: f.Grams, Price: f.PricePerKg, Label: f.Label})
	}
	return cp, nil
}

// EstimateCost chiffre une impression (archive découpée) ou un plan saisi. Pour une impression terminée dont la
// durée a été observée, renvoie aussi le coût réel.
func (s *Service) EstimateCost(ctx context.Context, in EstimateInput) (*EstimateOutput, error) {
	set, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := &EstimateOutput{Note: "Estimation : les prix par défaut et les puissances sont indicatifs tant que vous ne les avez pas remplacés (get_cost_settings / set_cost_settings)."}
	if in.PrintJobID != "" {
		if in.DurationMinutes != 0 || len(in.Filaments) != 0 || in.PrinterModel != "" {
			return nil, invalid("print_job_id et plan saisi (durée, matières, modèle) s'excluent")
		}
		j, err := s.loadJob(ctx, in.PrintJobID)
		if err != nil {
			return nil, err
		}
		cp, err := s.planFromJob(ctx, j, in.Plate)
		if err != nil {
			return nil, err
		}
		out.PrintJobID = j.ID
		out.Estimate = compute(cp, set)
		if isTerminal(j.State) {
			if a := s.actualCost(ctx, j, cp, nil, nil, set); a != nil {
				out.Actual = a
			}
		}
		return out, nil
	}
	cp, err := planFromInput(in)
	if err != nil {
		return nil, err
	}
	out.Estimate = compute(cp, set)
	return out, nil
}

// actualCost calcule le coût réel d'une impression terminée : durée observée (événements d'état) et masse
// déclarée (actualGrams) sinon déduite (masse prévue, proportionnelle à l'avancement si l'impression n'est pas allée au bout).
func (s *Service) actualCost(ctx context.Context, j *jobDoc, planned costPlan, actualGrams, actualSeconds *float64, set Settings) *CostEstimate {
	start, end, ok := j.observed()
	cp := planned
	cp.Kind = "réel"
	cp.ExtraAssumes = nil
	switch {
	case actualSeconds != nil && *actualSeconds > 0:
		cp.Seconds = *actualSeconds
		cp.Basis = fmt.Sprintf("la durée réelle déclarée pour l'impression %s (%.0f min)", j.ID, cp.Seconds/60)
	case ok:
		cp.Seconds = end.Sub(start).Seconds()
		cp.Basis = fmt.Sprintf("la durée observée de l'impression %s (%.0f min entre le début et la fin suivis par le service)", j.ID, cp.Seconds/60)
	default:
		return nil
	}
	var plannedTotal float64
	for _, f := range planned.Filaments {
		plannedTotal += f.Grams
	}
	scale := 1.0
	switch {
	case actualGrams != nil && plannedTotal > 0:
		scale = *actualGrams / plannedTotal
		cp.ExtraAssumes = append(cp.ExtraAssumes, "Masse consommée déclarée par vous, répartie au prorata des emplacements prévus.")
	case j.State != "COMPLETED" && j.Print != nil && j.Print.Percent > 0 && j.Print.Percent < 100:
		scale = float64(j.Print.Percent) / 100
		cp.ExtraAssumes = append(cp.ExtraAssumes, fmt.Sprintf("Impression non terminée (%s à %d %%) : masse estimée au prorata de l'avancement, à corriger par la masse réellement pesée.", j.State, j.Print.Percent))
	case j.State != "COMPLETED":
		scale = 0.5
		cp.ExtraAssumes = append(cp.ExtraAssumes, fmt.Sprintf("Impression non terminée (%s), avancement inconnu : masse estimée à 50 %% de la masse prévue, à corriger par la masse réellement pesée.", j.State))
	default:
		cp.ExtraAssumes = append(cp.ExtraAssumes, "Masse consommée = masse prévue par le slicer (non pesée).")
	}
	// masse réellement décomptée par le service des bobines (impression allée au bout), par emplacement
	used := map[int]float64{}
	if sp := s.spools(); sp != nil && actualGrams == nil {
		if us, err := sp.UsageOf(ctx, j.ID); err == nil {
			for _, u := range us {
				if !u.Estimated && u.Grams > 0 {
					used[u.FilamentSlot] += u.Grams
				}
			}
		}
	}
	cp.Filaments = nil
	for _, f := range planned.Filaments {
		if g, ok := used[f.Slot]; ok {
			f.Grams = g
			cp.ExtraAssumes = append(cp.ExtraAssumes, fmt.Sprintf("Emplacement %d : masse décomptée par l'inventaire des bobines (%.1f g).", f.Slot, g))
		} else {
			f.Grams *= scale
		}
		cp.Filaments = append(cp.Filaments, f)
	}
	est := compute(cp, set)
	return &est
}

func familyPrice(set Settings, family string) (string, float64) {
	if family == "" {
		return "", 0
	}
	if v, ok := set.FilamentPerKg[family]; ok {
		return family, v
	}
	if v, ok := set.FilamentPerKg[baseFamily(family)]; ok {
		return baseFamily(family), v
	}
	return "", 0
}

func hasStr(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}
