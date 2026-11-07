package insights

import (
	"fmt"
	"strings"
)

func pct(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.0f %%", *v*100)
}

// Text résume une estimation pour un rendu texte (Claude, journal).
func (e CostEstimate) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", e.Label)
	fmt.Fprintf(&b, "Total : %.2f %s  (filament %.2f + électricité %.2f + usure %.2f) — %.1f h machine, %.0f g.\n",
		e.Total, e.Currency, e.FilamentSum, e.ElectricSum, e.WearSum, e.Wear.MachineHours, e.Grams)
	for _, f := range e.Filament {
		fmt.Fprintf(&b, "- %s : %.1f g à %.2f %s/kg (%s) = %.2f\n", orStr(f.Material, "filament"), f.Grams, f.PricePerKg, e.Currency, f.PriceSource, f.Amount)
	}
	fmt.Fprintf(&b, "- électricité : %.3f kWh à %.4g %s/kWh (%s, puissances %s) = %.2f\n",
		e.Electricity.KWh, e.Electricity.PricePerKWh, e.Currency, e.Electricity.Model, e.Electricity.PowerSource, e.Electricity.Amount)
	b.WriteString("Hypothèses :\n")
	for _, a := range e.Assumptions {
		b.WriteString("  * " + a + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func orStr(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// Text résume le tableau de bord.
func (d *Dashboard) Text() string {
	t := d.Totals
	var b strings.Builder
	fmt.Fprintf(&b, "Tableau de bord du %s au %s (découpage %s).\n", d.From.Format("2006-01-02"), d.To.Format("2006-01-02"), d.Period)
	fmt.Fprintf(&b, "%d impression(s) : %d réussie(s), %d partielle(s), %d ratée(s), %d annulée(s) ; taux de réussite %s.\n", t.Prints, t.Succeeded, t.Partial, t.Failed, t.Cancelled, pct(t.SuccessRate))
	fmt.Fprintf(&b, "%.1f h machine, %.0f g de filament, coût cumulé ESTIMÉ %.2f %s.\n", t.MachineHours, t.FilamentGrams, t.Cost, d.Currency)
	if t.AvgRating != nil {
		fmt.Fprintf(&b, "Note moyenne %.1f/5 sur %d fiche(s) notée(s).\n", *t.AvgRating, t.RatedCount)
	}
	if len(d.ByMaterial) > 0 {
		b.WriteString("Par matière : ")
		var parts []string
		for _, r := range d.ByMaterial {
			parts = append(parts, fmt.Sprintf("%s %.0f g (%.2f %s)", r.Name, r.Grams, r.Cost, d.Currency))
		}
		b.WriteString(strings.Join(parts, " ; ") + ".\n")
	}
	if len(d.ByPrinter) > 0 {
		b.WriteString("Par imprimante : ")
		var parts []string
		for _, r := range d.ByPrinter {
			parts = append(parts, fmt.Sprintf("%s %d impr., %.1f h, réussite %s", r.Name, r.Prints, r.Hours, pct(r.SuccessRate)))
		}
		b.WriteString(strings.Join(parts, " ; ") + ".\n")
	}
	if len(d.ByProject) > 0 {
		b.WriteString("Par projet : ")
		var parts []string
		for _, r := range d.ByProject {
			p := fmt.Sprintf("%s %.2f %s", r.Name, r.Cost, d.Currency)
			if r.PendingJobs > 0 {
				p += fmt.Sprintf(" (+ %.2f estimés pour %d impression(s) préparée(s))", r.PendingEstimate, r.PendingJobs)
			}
			parts = append(parts, p)
		}
		b.WriteString(strings.Join(parts, " ; ") + ".\n")
	}
	for _, n := range d.Notes {
		b.WriteString("Note : " + n + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
