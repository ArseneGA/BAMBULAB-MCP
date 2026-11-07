package insights

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// DashboardQuery borne le tableau de bord.
type DashboardQuery struct {
	From      string `json:"from,omitempty" jsonschema:"début de période, AAAA-MM-JJ ou RFC 3339 (défaut : 30 jours avant la fin)"`
	To        string `json:"to,omitempty" jsonschema:"fin de période incluse, AAAA-MM-JJ ou RFC 3339 (défaut : maintenant)"`
	Period    string `json:"period,omitempty" jsonschema:"découpage : day | week | month (défaut : selon la durée)"`
	PrinterID string `json:"printer_id,omitempty" jsonschema:"restreindre à une imprimante"`
}

// DashTotals sont les totaux de la période.
type DashTotals struct {
	Prints        int      `json:"prints"`
	Succeeded     int      `json:"succeeded"`
	Partial       int      `json:"partial"`
	Failed        int      `json:"failed"`
	Cancelled     int      `json:"cancelled"`
	SuccessRate   *float64 `json:"success_rate,omitempty" jsonschema:"réussies / (réussies + partielles + ratées), 0 à 1 ; les annulations par l'utilisateur sont exclues ; absent s'il n'y a rien à mesurer"`
	MachineHours  float64  `json:"machine_hours"`
	FilamentGrams float64  `json:"filament_grams"`
	Cost          float64  `json:"cost" jsonschema:"somme des coûts réels ESTIMÉS des fiches (prix au moment de la fiche, ou recalculés)"`
	AvgRating     *float64 `json:"avg_rating,omitempty"`
	RatedCount    int      `json:"rated_count"`
}

// DashRow est une ligne de ventilation.
type DashRow struct {
	Key             string   `json:"key"`
	Name            string   `json:"name"`
	Prints          int      `json:"prints"`
	Succeeded       int      `json:"succeeded"`
	Failed          int      `json:"failed" jsonschema:"partielles et ratées"`
	SuccessRate     *float64 `json:"success_rate,omitempty"`
	Hours           float64  `json:"hours"`
	FailedHours     float64  `json:"failed_hours" jsonschema:"heures machine perdues (impressions ratées, partielles ou annulées)"`
	Grams           float64  `json:"grams"`
	Cost            float64  `json:"cost"`
	PendingJobs     int      `json:"pending_jobs,omitempty" jsonschema:"projets seulement : impressions préparées pas encore terminées"`
	PendingEstimate float64  `json:"pending_estimate,omitempty" jsonschema:"projets seulement : ESTIMATION du coût des impressions préparées pas encore terminées"`
}

// Dashboard est le tableau de bord d'usage de la machine.
type Dashboard struct {
	Label      string     `json:"label"`
	From       time.Time  `json:"from"`
	To         time.Time  `json:"to"`
	Period     string     `json:"period"`
	Currency   string     `json:"currency"`
	Totals     DashTotals `json:"totals"`
	ByMaterial []DashRow  `json:"by_material"`
	ByPrinter  []DashRow  `json:"by_printer"`
	ByPeriod   []DashRow  `json:"by_period"`
	ByProject  []DashRow  `json:"by_project"`
	Notes      []string   `json:"notes"`
	Created    int        `json:"logs_created_from_finished_prints"`
}

func parseWhen(s string, endOfDay bool) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		if endOfDay {
			return t.Add(24*time.Hour - time.Nanosecond), nil
		}
		return t, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, invalid("date %q : AAAA-MM-JJ ou RFC 3339 attendu", s)
	}
	return t.UTC(), nil
}

func bucketKey(t time.Time, period string) string {
	switch period {
	case "week":
		y, w := t.ISOWeek()
		return fmt.Sprintf("%04d-S%02d", y, w)
	case "month":
		return t.Format("2006-01")
	}
	return t.Format("2006-01-02")
}

func rate(ok, bad int) *float64 {
	if ok+bad == 0 {
		return nil
	}
	v := round3(float64(ok) / float64(ok+bad))
	return &v
}

type acc struct {
	DashRow
	partial int
}

func (a *acc) add(e LogEntry, hours, failedHrs float64, grams, cost float64) {
	a.Prints++
	switch e.Outcome {
	case "success":
		a.Succeeded++
	case "partial", "failed":
		a.Failed++
	}
	a.Hours += hours
	a.FailedHours += failedHrs
	a.Grams += grams
	a.Cost += cost
}

func finish(rows map[string]*acc, order func(a, b *acc) bool) []DashRow {
	out := make([]DashRow, 0, len(rows))
	list := make([]*acc, 0, len(rows))
	for _, a := range rows {
		list = append(list, a)
	}
	sort.Slice(list, func(i, j int) bool { return order(list[i], list[j]) })
	for _, a := range list {
		a.Hours, a.FailedHours, a.Grams, a.Cost, a.PendingEstimate = round2(a.Hours), round2(a.FailedHours), round2(a.Grams), round2(a.Cost), round2(a.PendingEstimate)
		a.SuccessRate = rate(a.Succeeded, a.Failed)
		out = append(out, a.DashRow)
	}
	return out
}

// GetDashboard agrège le journal sur la période.
func (s *Service) GetDashboard(ctx context.Context, q DashboardQuery) (*Dashboard, error) {
	sr, err := s.SyncJournal(ctx)
	if err != nil {
		return nil, err
	}
	set, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	to, err := parseWhen(q.To, true)
	if err != nil {
		return nil, err
	}
	if to.IsZero() {
		to = s.now()
	}
	from, err := parseWhen(q.From, false)
	if err != nil {
		return nil, err
	}
	if from.IsZero() {
		from = to.AddDate(0, 0, -30)
	}
	if !to.After(from) {
		return nil, invalid("la période est vide (from doit précéder to)")
	}
	span := to.Sub(from)
	period := q.Period
	switch period {
	case "":
		switch {
		case span <= 45*24*time.Hour:
			period = "day"
		case span <= 200*24*time.Hour:
			period = "week"
		default:
			period = "month"
		}
	case "day", "week", "month":
	default:
		return nil, invalid("period %q inconnue (day, week, month)", q.Period)
	}
	if period == "day" && span > 400*24*time.Hour {
		return nil, invalid("découpage par jour limité à 400 jours : choisir week ou month")
	}

	args := []any{store.FormatTime(from), store.FormatTime(to)}
	where := `finished_at >= ? AND finished_at <= ?`
	if q.PrinterID != "" {
		where += ` AND printer_id=?`
		args = append(args, q.PrinterID)
	}
	rows, err := s.cfg.Store.DB().QueryContext(ctx, `SELECT doc FROM print_logs WHERE `+where+` ORDER BY finished_at`, args...)
	if err != nil {
		return nil, err
	}
	var entries []LogEntry
	for rows.Next() {
		var raw string
		var e LogEntry
		if rows.Scan(&raw) == nil && json.Unmarshal([]byte(raw), &e) == nil {
			entries = append(entries, e)
		}
	}
	rows.Close()

	printerNames := s.printerNames(ctx)
	d := &Dashboard{Label: "TABLEAU DE BORD — coûts = ESTIMATIONS (prix et puissances de vos réglages ; valeurs par défaut indicatives sinon)",
		From: from.UTC(), To: to.UTC(), Period: period, Currency: set.Currency, Created: sr.Created,
		ByMaterial: []DashRow{}, ByPrinter: []DashRow{}, ByPeriod: []DashRow{}, ByProject: []DashRow{}, Notes: []string{}}
	byMat, byPrinter, byPeriod, byProject := map[string]*acc{}, map[string]*acc{}, map[string]*acc{}, map[string]*acc{}
	get := func(m map[string]*acc, key, name string) *acc {
		a, ok := m[key]
		if !ok {
			a = &acc{DashRow: DashRow{Key: key, Name: name}}
			m[key] = a
		}
		return a
	}
	var ratingSum float64
	estimatedDur := 0
	for _, e := range entries {
		hours := e.Actual.Seconds / 3600
		if e.Actual.Seconds <= 0 {
			hours = e.Estimated.Seconds / 3600
			estimatedDur++
		}
		grams := e.Actual.Grams
		if grams <= 0 && e.Outcome == "success" {
			grams = e.Estimated.Grams
		}
		cost := 0.0
		if e.ActualCost != nil {
			cost = e.ActualCost.Total
		}
		failedHrs := 0.0
		if e.Outcome != "success" {
			failedHrs = hours
		}
		t := &d.Totals
		t.Prints++
		switch e.Outcome {
		case "success":
			t.Succeeded++
		case "partial":
			t.Partial++
		case "failed":
			t.Failed++
		case "cancelled":
			t.Cancelled++
		}
		t.MachineHours += hours
		t.FilamentGrams += grams
		t.Cost += cost
		if e.Rating > 0 {
			t.RatedCount++
			ratingSum += float64(e.Rating)
		}
		// par matière : une ligne par famille d'après le détail du coût (multi-matières)
		if e.ActualCost != nil && len(e.ActualCost.Filament) > 0 {
			for _, f := range e.ActualCost.Filament {
				fam := f.Family
				if fam == "" {
					fam = "(inconnue)"
				}
				a := get(byMat, fam, fam)
				a.Grams += f.Grams
				a.Cost += f.Amount
			}
			// compter l'impression une fois par famille présente, heures au prorata des grammes
			seen := map[string]bool{}
			for _, f := range e.ActualCost.Filament {
				fam := f.Family
				if fam == "" {
					fam = "(inconnue)"
				}
				if seen[fam] {
					continue
				}
				seen[fam] = true
				a := byMat[fam]
				a.Prints++
				if e.Outcome == "success" {
					a.Succeeded++
				} else if e.Outcome != "cancelled" {
					a.Failed++
				}
			}
		} else {
			fam := e.MaterialFamily
			if fam == "" {
				fam = "(inconnue)"
			}
			get(byMat, fam, fam).add(e, hours, failedHrs, grams, cost)
		}
		pk := e.PrinterID
		pn := printerNames[pk]
		if pn == "" {
			pn = e.PrinterModel
		}
		if pk == "" {
			pk, pn = "(inconnue)", "(imprimante inconnue)"
		}
		get(byPrinter, pk, pn).add(e, hours, failedHrs, grams, cost)
		get(byPeriod, bucketKey(e.FinishedAt, period), bucketKey(e.FinishedAt, period)).add(e, hours, failedHrs, grams, cost)
		ck, cn := e.CollectionID, e.CollectionName
		if ck == "" {
			ck, cn = "(aucun)", "(hors projet)"
		}
		get(byProject, ck, cn).add(e, hours, failedHrs, grams, cost)
	}
	t := &d.Totals
	t.SuccessRate = rate(t.Succeeded, t.Partial+t.Failed)
	t.MachineHours, t.FilamentGrams, t.Cost = round2(t.MachineHours), round2(t.FilamentGrams), round2(t.Cost)
	if t.RatedCount > 0 {
		v := round2(ratingSum / float64(t.RatedCount))
		t.AvgRating = &v
	}

	// impressions préparées mais pas terminées : estimation du coût par projet (hors période)
	s.addPending(ctx, set, byProject, get)

	byGrams := func(a, b *acc) bool {
		if a.Grams != b.Grams {
			return a.Grams > b.Grams
		}
		return a.Key < b.Key
	}
	d.ByMaterial = finish(byMat, byGrams)
	d.ByPrinter = finish(byPrinter, func(a, b *acc) bool {
		if a.Hours != b.Hours {
			return a.Hours > b.Hours
		}
		return a.Key < b.Key
	})
	// toutes les périodes de la plage, même vides
	for cur := bucketStart(from, period); !cur.After(to); cur = nextBucket(cur, period) {
		k := bucketKey(cur, period)
		get(byPeriod, k, k)
	}
	d.ByPeriod = finish(byPeriod, func(a, b *acc) bool { return a.Key < b.Key })
	d.ByProject = finish(byProject, func(a, b *acc) bool {
		if a.Cost+a.PendingEstimate != b.Cost+b.PendingEstimate {
			return a.Cost+a.PendingEstimate > b.Cost+b.PendingEstimate
		}
		return a.Key < b.Key
	})
	if estimatedDur > 0 {
		d.Notes = append(d.Notes, fmt.Sprintf("%d impression(s) sans durée observée : durée estimée par le slicer utilisée.", estimatedDur))
	}
	if t.Prints == 0 {
		d.Notes = append(d.Notes, "Aucune impression terminée sur la période.")
	}
	d.Notes = append(d.Notes,
		"Taux de réussite = réussies / (réussies + partielles + ratées) ; les impressions annulées par l'utilisateur sont comptées à part. Le statut « réussie » vient de la fin de l'impression et se corrige dans la fiche.",
		"Le coût est la somme des coûts réels estimés de chaque fiche (voir les hypothèses de estimate_cost). Les masses non pesées sont celles du slicer (au prorata pour les impressions interrompues).")
	if set.UsingDefaults || len(set.DefaultedPrices) > 0 {
		d.Notes = append(d.Notes, "Des prix par défaut INDICATIFS sont encore utilisés ("+strings.Join(set.DefaultedPrices, ", ")+") : renseigner les vôtres avec set_cost_settings (apply_to_history pour recalculer l'historique).")
	}
	return d, nil
}

func bucketStart(t time.Time, period string) time.Time {
	t = t.UTC()
	switch period {
	case "week":
		d := t.Truncate(24 * time.Hour)
		wd := int(d.Weekday()+6) % 7 // lundi = 0
		return d.AddDate(0, 0, -wd)
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	return t.Truncate(24 * time.Hour)
}

func nextBucket(t time.Time, period string) time.Time {
	switch period {
	case "week":
		return t.AddDate(0, 0, 7)
	case "month":
		return t.AddDate(0, 1, 0)
	}
	return t.AddDate(0, 0, 1)
}

func (s *Service) printerNames(ctx context.Context) map[string]string {
	out := map[string]string{}
	rows, err := s.cfg.Store.DB().QueryContext(ctx, `SELECT id, doc FROM printers`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, raw string
		var p struct {
			Name string `json:"name"`
		}
		if rows.Scan(&id, &raw) == nil && json.Unmarshal([]byte(raw), &p) == nil {
			out[id] = p.Name
		}
	}
	return out
}

var pendingStates = map[string]bool{"PREPARED": true, "SLICING": true, "VALIDATED": true, "UPLOADING": true, "UPLOADED": true,
	"AWAITING_PRINT_CONFIRMATION": true, "START_REQUESTED": true, "PRINTING": true}

func (s *Service) addPending(ctx context.Context, set Settings, byProject map[string]*acc, get func(map[string]*acc, string, string) *acc) {
	jobs, err := s.listJobs(ctx)
	if err != nil {
		return
	}
	for _, j := range jobs {
		if !pendingStates[j.State] {
			continue
		}
		cp, err := s.planFromJob(ctx, j, 0)
		if err != nil {
			continue
		}
		est := compute(cp, set)
		ck, cn := "(aucun)", "(hors projet)"
		if j.ModelID != "" {
			var cid, name *string
			if s.cfg.Store.DB().QueryRowContext(ctx, `SELECT m.collection_id, c.name FROM models m LEFT JOIN collections c ON c.id=m.collection_id WHERE m.id=?`, j.ModelID).Scan(&cid, &name) == nil && cid != nil {
				ck = *cid
				cn = ck
				if name != nil {
					cn = *name
				}
			}
		}
		a := get(byProject, ck, cn)
		a.PendingJobs++
		a.PendingEstimate += est.Total
	}
}

// ProjectCosts renvoie seulement la ventilation par projet (collection) d'une période.
func (s *Service) ProjectCosts(ctx context.Context, q DashboardQuery) ([]DashRow, error) {
	d, err := s.GetDashboard(ctx, q)
	if err != nil {
		return nil, err
	}
	return d.ByProject, nil
}
