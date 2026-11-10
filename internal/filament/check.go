package filament

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// CheckInput : l'impression prévue à confronter aux bobines.
type CheckInput struct {
	PrintJobID    string                        `json:"print_job_id" jsonschema:"impression découpée (execute_headless_slice) ; ses mesures par filament (purge comprise) servent de besoin"`
	PrinterID     string                        `json:"printer_id,omitempty" jsonschema:"imprimante visée (défaut : celle de l'impression)"`
	Plate         int                           `json:"plate,omitempty"`
	Assignments   []printing.FilamentAssignment `json:"assignments,omitempty" jsonschema:"affectation filament -> emplacement (défaut : celle déjà enregistrée sur l'impression)"`
	MarginPercent *float64                      `json:"margin_percent,omitempty" jsonschema:"marge d'alerte en % du besoin (défaut : réglage enregistré, 10 %)"`
	MarginGrams   *float64                      `json:"margin_grams,omitempty" jsonschema:"marge fixe en grammes (défaut : réglage enregistré, 5 g)"`
	RemainingG    []printing.RemainingDecl      `json:"remaining_g,omitempty" jsonschema:"restant déclaré par l'utilisateur (grammes) pour une bobine non inventoriée ; s'ajoute à ceux déjà donnés à request_print_confirmation ; comparé à la masse prévue + marge"`
}

// CheckSlot est le verdict pour un filament de l'impression.
type CheckSlot struct {
	FilamentSlot int      `json:"filament_slot"`
	Tray         string   `json:"tray"`
	SpoolID      string   `json:"spool_id,omitempty"`
	SpoolLabel   string   `json:"spool_label,omitempty"`
	NeededG      float64  `json:"needed_g" jsonschema:"masse que l'impression va extruder pour ce filament (pièce + purge)"`
	RemainingG   float64  `json:"remaining_g" jsonschema:"restant retenu (inventaire, sinon estimation de l'AMS) ; 0 si inconnu"`
	RemainSource string   `json:"remaining_source" jsonschema:"inventory | ams_estimate | declared (restant donné par l'utilisateur) | unknown"`
	RequiredG    float64  `json:"required_with_margin_g"`
	ShortfallG   float64  `json:"shortfall_g,omitempty"`
	Verdict      string   `json:"verdict" jsonschema:"ok | marginal (suffisant mais sous la marge) | insufficient (manque) | unknown (restant inconnu) | no_spool"`
	Detail       string   `json:"detail"`
	Alternatives []string `json:"alternatives,omitempty" jsonschema:"autres bobines de l'inventaire de même matière et couleur proche, assez remplies"`
}

// CheckOutput : verdict global.
type CheckOutput struct {
	PrintJobID    string      `json:"print_job_id"`
	PrinterID     string      `json:"printer_id"`
	Plate         int         `json:"plate"`
	OK            bool        `json:"ok" jsonschema:"vrai si aucune bobine ne risque de manquer (marginal compris : à surveiller)"`
	Verdict       string      `json:"verdict" jsonschema:"ok | marginal | insufficient | unknown"`
	MarginPercent float64     `json:"margin_percent"`
	MarginGrams   float64     `json:"margin_grams"`
	Slots         []CheckSlot `json:"slots"`
	Summary       string      `json:"summary"`
	// Warning : avertissement clair quand le restant d'une bobine est inconnu (FB12). À acquitter par l'humain ; ne bloque jamais seul.
	Warning string `json:"warning,omitempty" jsonschema:"présent quand un restant est inconnu : quoi demander à l'utilisateur ; avertissement à acquitter, jamais un blocage"`
}

var verdictRank = map[string]int{"ok": 0, "no_spool": 1, "unknown": 1, "marginal": 2, "insufficient": 3}

// CheckFilament confronte le besoin de l'impression au restant des bobines chargées.
func (s *Service) CheckFilament(ctx context.Context, in CheckInput) (*CheckOutput, error) {
	if s.d.Printing == nil {
		return nil, fmt.Errorf("%w : chaîne d'impression indisponible", ErrConflict)
	}
	if in.PrintJobID == "" {
		return nil, invalidf("print_job_id requis")
	}
	j, err := s.d.Printing.GetJob(ctx, in.PrintJobID)
	if err != nil {
		return nil, fmt.Errorf("%w : %v", ErrNotFound, err)
	}
	if in.Plate > 0 {
		j.Plate = in.Plate
	}
	if j.Plate == 0 {
		j.Plate = 1
	}
	pid := in.PrinterID
	if pid == "" {
		pid = j.PrinterID
	}
	if pid == "" { // l'impression n'a pas d'imprimante : l'unique imprimante enregistrée, sinon erreur claire
		if pid, err = s.d.Printing.ResolvePrinterID(ctx, ""); err != nil {
			return nil, err
		}
	}
	as := in.Assignments
	if len(as) == 0 {
		as = j.Assignments
	}
	if len(as) == 0 {
		return nil, invalidf("aucune affectation filament -> emplacement : en passer (assignments) ou utiliser plan_colors")
	}
	p := plateUse(j)
	if p == nil {
		return nil, fmt.Errorf("%w : l'impression %s n'a pas encore de mesures par plaque (état %s) : attendre la fin de la découpe", ErrConflict, j.ID, j.State)
	}
	st := s.GetSettings(ctx)
	if in.MarginPercent != nil {
		st.MarginPercent = *in.MarginPercent
	}
	if in.MarginGrams != nil {
		st.MarginGrams = *in.MarginGrams
	}
	if st.MarginPercent < 0 || st.MarginGrams < 0 {
		return nil, invalidf("marges négatives")
	}
	out := &CheckOutput{PrintJobID: j.ID, PrinterID: pid, Plate: j.Plate, MarginPercent: st.MarginPercent, MarginGrams: st.MarginGrams, Slots: s.checkSlots(ctx, pid, p, as, st, declaredMap(j.DeclaredRemaining, in.RemainingG, as))}
	out.Verdict = "ok"
	for _, c := range out.Slots {
		if verdictRank[c.Verdict] > verdictRank[out.Verdict] {
			out.Verdict = c.Verdict
		}
	}
	if out.Verdict == "no_spool" {
		out.Verdict = "unknown"
	}
	out.OK = out.Verdict == "ok" || out.Verdict == "marginal" || out.Verdict == "unknown"
	var bad []string
	for _, c := range out.Slots {
		if c.Verdict == "insufficient" || c.Verdict == "marginal" {
			bad = append(bad, fmt.Sprintf("filament %d (%s) : %s", c.FilamentSlot, c.Tray, c.Detail))
		}
	}
	switch out.Verdict {
	case "ok":
		out.Summary = fmt.Sprintf("Toutes les bobines suffisent (marge %.0f %% + %.0f g).", st.MarginPercent, st.MarginGrams)
	case "unknown":
		out.Warning = unknownWarning(out.Slots)
		out.Summary = out.Warning
	default:
		out.Summary = strings.Join(bad, " ; ")
	}
	return out, nil
}

// declaredMap fusionne les restants déclarés (enregistrés sur l'impression, puis ceux de l'appel) par numéro de filament.
func declaredMap(saved, extra []printing.RemainingDecl, as []printing.FilamentAssignment) map[int]float64 {
	m := map[int]float64{}
	for _, d := range append(append([]printing.RemainingDecl(nil), saved...), extra...) {
		slot := d.FilamentSlot
		if slot == 0 {
			for _, a := range as {
				if d.Tray != "" && a.Tray == d.Tray {
					slot = a.FilamentSlot
				}
			}
		}
		if slot != 0 && d.Grams >= 0 {
			m[slot] = d.Grams
		}
	}
	return m
}

// unknownWarning : avertissement explicite pour les filaments dont le restant est inconnu (FB12).
func unknownWarning(slots []CheckSlot) string {
	var parts []string
	for _, c := range slots {
		if c.Verdict == "no_spool" || c.Verdict == "unknown" {
			parts = append(parts, fmt.Sprintf("filament %d (%s) : il faut au moins %s (besoin %s + marge) mais la bobine n'est pas déclarée et l'AMS ne donne pas de restant", c.FilamentSlot, c.Tray, fmtG(c.RequiredG), fmtG(c.NeededG)))
		}
	}
	return "Restant inconnu, impossible de conclure : " + strings.Join(parts, " ; ") + ". Demander à l'utilisateur combien de grammes restent (à passer en remaining_g à request_print_confirmation ou check_filament_for_print), ou inscrire la bobine (update_spool). Avertissement à acquitter ; il ne bloque pas le lancement."
}

// judge fixe verdict et détail d'un emplacement d'après le besoin, le restant et la marge.
func judge(cs *CheckSlot, need float64, a printing.FilamentAssignment) {
	switch {
	case cs.RemainSource == "unknown":
		cs.Verdict = "no_spool"
		cs.Detail = fmt.Sprintf("aucune bobine inscrite à %s et l'AMS ne donne pas de restant : besoin %s (%s avec marge), restant inconnu ; indiquer le restant en grammes ou inscrire la bobine", a.Tray, fmtG(need), fmtG(cs.RequiredG))
	case cs.RemainingG < need:
		cs.Verdict, cs.ShortfallG = "insufficient", round2(need-cs.RemainingG)
		cs.Detail = fmt.Sprintf("il reste %s sur %s mais l'impression en demande %s : il manque %s", fmtG(cs.RemainingG), nameOf(*cs), fmtG(need), fmtG(cs.ShortfallG))
	case cs.RemainingG < cs.RequiredG:
		cs.Verdict, cs.ShortfallG = "marginal", round2(cs.RequiredG-cs.RemainingG)
		cs.Detail = fmt.Sprintf("il reste %s sur %s pour un besoin de %s : sous la marge de sécurité (%s requis)", fmtG(cs.RemainingG), nameOf(*cs), fmtG(need), fmtG(cs.RequiredG))
	default:
		cs.Verdict = "ok"
		cs.Detail = fmt.Sprintf("%s restant sur %s pour %s", fmtG(cs.RemainingG), nameOf(*cs), fmtG(need))
	}
}

func (s *Service) checkSlots(ctx context.Context, printerID string, p *slicer.PlateResult, as []printing.FilamentAssignment, st Settings, declared map[int]float64) []CheckSlot {
	var ams map[string]float64 // restant estimé par l'AMS pour les emplacements sans bobine inscrite
	slots := append([]printing.FilamentAssignment(nil), as...)
	sort.Slice(slots, func(i, j int) bool { return slots[i].FilamentSlot < slots[j].FilamentSlot })
	var out []CheckSlot
	for _, a := range slots {
		need, ok := gramsFor(p, a.FilamentSlot)
		cs := CheckSlot{FilamentSlot: a.FilamentSlot, Tray: a.Tray, NeededG: round2(need), RemainSource: "unknown"}
		if !ok {
			cs.Verdict, cs.Detail = "ok", "ce filament n'est pas consommé par la plaque"
			out = append(out, cs)
			continue
		}
		cs.RequiredG = round2(need*(1+st.MarginPercent/100) + st.MarginGrams)
		sp, _ := s.SpoolAt(ctx, printerID, a.Tray)
		switch {
		case sp != nil:
			cs.SpoolID, cs.SpoolLabel, cs.RemainingG, cs.RemainSource = sp.ID, sp.Label(), round2(sp.RemainingG), "inventory"
		default:
			if ams == nil {
				ams = s.amsEstimates(ctx, printerID)
			}
			if g, ok := ams[a.Tray]; ok && g > 0 {
				cs.RemainingG, cs.RemainSource = round2(g), "ams_estimate"
			}
		}
		if cs.RemainSource == "unknown" { // restant déclaré par l'utilisateur (information, pas une preuve)
			if g, ok := declared[a.FilamentSlot]; ok {
				cs.RemainingG, cs.RemainSource = round2(g), "declared"
				cs.SpoolLabel = fmt.Sprintf("déclarée (%s)", fmtG(g))
			}
		}
		judge(&cs, need, a)
		if cs.Verdict == "insufficient" || cs.Verdict == "marginal" {
			cs.Alternatives = s.alternatives(ctx, sp, cs, p, a)
		}
		out = append(out, cs)
	}
	return out
}

func nameOf(c CheckSlot) string {
	if c.SpoolLabel != "" {
		return "la bobine " + c.SpoolLabel + " (" + c.Tray + ")"
	}
	return c.Tray
}

// amsEstimates : restant estimé (g) par emplacement d'après le dernier rapport de l'AMS.
func (s *Service) amsEstimates(ctx context.Context, printerID string) map[string]float64 {
	out := map[string]float64{}
	if s.d.Printing == nil {
		return out
	}
	ams, _, err := s.d.Printing.PrinterAMS(ctx, printerID)
	if err != nil || ams == nil {
		return out
	}
	for _, tr := range ams.Trays() {
		if g := amsEstimate(nil, tr); g > 0 {
			out[tr.Slot] = g
		}
	}
	return out
}

// alternatives propose les autres bobines de même matière et de couleur proche qui suffisent.
func (s *Service) alternatives(ctx context.Context, cur *Spool, cs CheckSlot, p *slicer.PlateResult, a printing.FilamentAssignment) []string {
	all, err := s.ListSpools(ctx, ListFilter{})
	if err != nil {
		return nil
	}
	var wantMat, wantColor string
	if cur != nil {
		wantMat, wantColor = cur.Material, cur.ColorHex
	} else {
		for _, f := range p.Filaments {
			if f.Slot == a.FilamentSlot {
				wantMat, wantColor = f.Type, f.WantedColor()
			}
		}
	}
	var alts []string
	for _, sp := range all {
		if (cur != nil && sp.ID == cur.ID) || sp.Archived || !sameMaterial(sp.Material, wantMat) || sp.RemainingG < cs.RequiredG {
			continue
		}
		if wantColor != "" && sp.ColorHex != "" && colorDistance(sp.ColorHex, wantColor) > 25 {
			continue
		}
		where := "rangée"
		if sp.Slot != "" {
			where = sp.Slot
		}
		alts = append(alts, fmt.Sprintf("%s (%s, %s, %s restant)", sp.Label(), sp.ID, where, fmtG(sp.RemainingG)))
	}
	return alts
}

// validationWarnings branche l'alerte de manque sur la validation d'une impression, donc AVANT le dépôt du fichier.
func (s *Service) validationWarnings(ctx context.Context, printerID string, plate *slicer.PlateResult, as []printing.FilamentAssignment) []printing.FilamentWarning {
	if plate == nil || len(as) == 0 {
		return nil
	}
	// Sans inventaire du tout, l'alerte ne dit rien d'utile : pas de bruit.
	var n int
	if err := s.d.Store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM spools WHERE archived=0`).Scan(&n); err != nil || n == 0 {
		return nil
	}
	var out []printing.FilamentWarning
	for _, c := range s.checkSlots(ctx, printerID, plate, as, s.GetSettings(ctx), nil) {
		switch c.Verdict {
		case "insufficient":
			out = append(out, printing.FilamentWarning{Name: fmt.Sprintf("filament_short_%d", c.FilamentSlot), Level: "warn",
				Detail: "RISQUE DE MANQUE — " + c.Detail + altText(c)})
		case "marginal":
			out = append(out, printing.FilamentWarning{Name: fmt.Sprintf("filament_marginal_%d", c.FilamentSlot), Level: "warn", Detail: c.Detail + altText(c)})
		}
	}
	return out
}

func altText(c CheckSlot) string {
	if len(c.Alternatives) == 0 {
		return ""
	}
	return ". Alternatives : " + strings.Join(c.Alternatives, " ; ")
}

// previewLines : contrôle de matière montré à la confirmation (FB12), avec les restants déclarés sur l'impression.
// Informatif : ni erreur ni blocage ; sans mesures par plaque ou sans affectation, rien.
func (s *Service) previewLines(ctx context.Context, j printing.Job) []printing.FilamentLine {
	p := plateUse(j)
	if p == nil || len(j.Assignments) == 0 || j.PrinterID == "" {
		return nil
	}
	var out []printing.FilamentLine
	for _, c := range s.checkSlots(ctx, j.PrinterID, p, j.Assignments, s.GetSettings(ctx), declaredMap(j.DeclaredRemaining, nil, j.Assignments)) {
		v := c.Verdict
		if v == "no_spool" {
			v = "unknown"
		}
		out = append(out, printing.FilamentLine{FilamentSlot: c.FilamentSlot, Tray: c.Tray, NeededG: c.NeededG, RequiredG: c.RequiredG, RemainingG: c.RemainingG,
			Source: c.RemainSource, Verdict: v, Detail: c.Detail, NeedsRemaining: v == "unknown"})
	}
	return out
}
