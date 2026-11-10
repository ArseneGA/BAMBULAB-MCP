package sheets

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/insights"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/projects"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

const (
	defaultPrints = 20
	maxPrints     = 100
)

// PartSheet assemble la fiche d'une pièce (un modèle) : compteurs (D7), versions suivant parent_id, profils testés par
// signature de réglages, impressions avec leur bandeau de paliers, coût et temps cumulés (mesuré contre déclaré), dernier
// profil réussi, dernier test de résistance PAR VERSION, projets qui citent la pièce. Lecture seule. Une pièce jamais
// imprimée donne des zéros et « jamais imprimée », sans erreur.
func (s *Service) PartSheet(ctx context.Context, in PartSheetInput) (*PartSheet, error) {
	id := strings.TrimSpace(in.ModelID)
	if id == "" {
		return nil, invalid("model_id requis")
	}
	include, err := cleanInclude(in.Include)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, k := range include {
		want[k] = true
	}
	limit := in.Limit
	if limit <= 0 {
		limit = defaultPrints
	}
	if limit > maxPrints {
		limit = maxPrints
	}

	sh := &PartSheet{Include: include, Versions: []PartVersion{}, Profiles: []PartProfile{}, Prints: []PartPrint{}, Projects: []projects.ProjectRef{},
		Levels:      map[string]string{"counts": LevelComputed, "verdict": LevelDeclared, "cost": LevelComputed, "time": LevelMeasured, "grams": LevelMeasured, "profiles": LevelComputed, "strength": LevelComputed},
		Unavailable: map[string]string{}}
	if err := s.loadModel(ctx, id, sh); err != nil {
		return nil, err
	}
	counts, err := s.cfg.Insights.PrintCountsByModel(ctx, []string{id})
	if err != nil {
		return nil, err
	}
	c := counts[id]
	if c == nil {
		c = &insights.PrintCounts{ModelID: id}
	}
	sh.Counts = CountsBlock{PrintCounts: *c, NeverPrinted: c.Attempts == 0 && c.InProgress == 0, Text: projects.DescribeCounts(*c)}
	sh.Note = sh.Counts.Text

	entries, err := s.cfg.Insights.LogsForModel(ctx, id)
	if err != nil {
		return nil, err
	}
	s.totals(sh, c, entries)
	sh.Profiles, sh.LastSuccess = profiles(entries)
	if !want["profiles"] {
		sh.Profiles = []PartProfile{}
	}
	if sh.LastSuccess == nil {
		if sh.Counts.NeverPrinted {
			sh.Unavailable["last_success"] = "jamais imprimée"
		} else {
			sh.Unavailable["last_success"] = "aucune impression jugée réussie"
		}
	}
	sh.PrintsTotal = len(entries)
	if want["prints"] {

		show := entries
		if len(show) > limit {
			show, sh.Truncated = show[:limit], true
		}
		for _, e := range show {
			sh.Prints = append(sh.Prints, s.partPrint(ctx, e))
		}
	}
	if want["versions"] || want["strength"] {
		if err := s.versions(ctx, id, sh, want["strength"]); err != nil {
			return nil, err
		}
		if !want["versions"] {
			sh.Versions = []PartVersion{}
		}
	}
	if want["projects"] && s.cfg.Projects != nil {
		refs, err := s.cfg.Projects.ProjectsOfModel(ctx, id)
		if err != nil {
			return nil, err
		}
		if refs != nil {
			sh.Projects = refs
		}
	}
	if len(sh.Unavailable) == 0 {
		sh.Unavailable = nil
	}
	return sh, nil
}

func (s *Service) loadModel(ctx context.Context, id string, sh *PartSheet) error {
	var name, tags string
	var coll, dims, deleted sql.NullString
	err := s.cfg.Store.DB().QueryRowContext(ctx, `SELECT name, tags, collection_id, dimensions, deleted_at FROM models WHERE id=?`, id).Scan(&name, &tags, &coll, &dims, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("pièce %q inconnue (modèle absent du catalogue) : %w", id, store.ErrNotFound)
	}
	if err != nil {
		return err
	}
	sh.Model = PartModel{ID: id, Name: name, CollectionID: coll.String, Trashed: deleted.Valid && deleted.String != ""}
	_ = json.Unmarshal([]byte(tags), &sh.Model.Tags)
	if dims.Valid {
		var d Dimension
		if json.Unmarshal([]byte(dims.String), &d) == nil && (d.X > 0 || d.Y > 0 || d.Z > 0) {
			sh.Model.Dimensions = &d
		}
	}
	return nil
}

// totals : coût et temps cumulés. Le « réel mesuré » exclut les impressions déclarées hors Printalk (listées à part).
func (s *Service) totals(sh *PartSheet, c *insights.PrintCounts, entries []insights.LogEntry) {
	t := PartTotals{Currency: c.Currency, PlannedCost: c.PlannedCost}
	measured := 0
	for _, e := range entries {
		if e.IsExternal() {
			t.DeclaredCount++
			t.DeclaredSeconds += e.Actual.Seconds
			t.DeclaredGrams += e.Actual.Grams
			continue
		}
		measured++
	}
	t.DeclaredSeconds, t.DeclaredGrams = r2(t.DeclaredSeconds), r2(t.DeclaredGrams)
	if measured == 0 {
		t.Reason = "aucune impression Printalk mesurée"
		if len(entries) == 0 {
			t.Reason = "jamais imprimée : aucun coût ni temps mesuré"
		}
		sh.Unavailable["cost"] = t.Reason
		sh.Unavailable["time"] = t.Reason
	} else {
		t.Available = true
		t.ActualCost, t.ActualSeconds, t.ActualGrams = c.ActualCost, c.ActualSeconds, c.ActualGrams
		if c.Success > 0 {
			t.CostPerSuccess = fp(c.ActualCost / float64(c.Success))
		} else {
			sh.Unavailable["cost_per_judged_success"] = "aucune impression jugée réussie"
		}
	}
	if t.DeclaredCount > 0 {
		sh.Levels["declared_prints"] = LevelDeclared
	}
	sh.Totals = t
}

// profiles regroupe les impressions par signature de réglages effectifs (relevés à la découpe) et trouve la dernière
// impression jugée réussie (Printalk : les réglages d'une impression déclarée ne sont pas relevés).
func profiles(entries []insights.LogEntry) ([]PartProfile, *LastSuccess) {
	type agg struct {
		p              PartProfile
		sec, grams, cs []float64
	}
	by := map[string]*agg{}
	var order []string
	var last *LastSuccess
	for _, e := range entries { // plus récentes d'abord
		sig := insights.SettingsSignature(e.Settings)
		a := by[sig]
		if a == nil {
			a = &agg{p: PartProfile{Signature: sig, Label: insights.SettingsLabel(e.Settings), Settings: e.Settings, Level: LevelComputed}}
			by[sig] = a
			order = append(order, sig)
		}
		p := &a.p
		p.Attempts++
		if p.LastPrintAt.IsZero() {
			p.LastPrintAt = e.FinishedAt
		}
		if !e.IsPrinted() {
			continue
		}
		p.Printed++
		switch e.Verdict {
		case insights.VerdictSuccess:
			p.Success++
			if p.LastSuccessAt.IsZero() {
				p.LastSuccessAt = e.FinishedAt
			}
			if last == nil && !e.IsExternal() {
				last = &LastSuccess{PrintJobID: e.PrintJobID, FinishedAt: e.FinishedAt, ProfileSig: sig, Label: p.Label}
			}
		case insights.VerdictFailed:
			p.Failed++
		case insights.VerdictRedo:
			p.Redo++
		default:
			p.Unjudged++
		}
		if e.IsExternal() {
			p.External++
			continue
		}
		if e.Actual.Seconds > 0 {
			a.sec = append(a.sec, e.Actual.Seconds)
		}
		if e.Actual.Grams > 0 {
			a.grams = append(a.grams, e.Actual.Grams)
		}
		if e.ActualCost != nil {
			a.cs = append(a.cs, e.ActualCost.Total)
			if p.Currency == "" {
				p.Currency = e.ActualCost.Currency
			}
		}
	}
	out := make([]PartProfile, 0, len(order))
	for _, sig := range order {
		a := by[sig]
		a.p.MedianSeconds, a.p.MedianGrams, a.p.MedianCost = median(a.sec), median(a.grams), median(a.cs)
		out = append(out, a.p)
	}
	// Les plus utilisées d'abord ; égalités départagées par la signature (sortie déterministe).
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Attempts != out[j].Attempts {
			return out[i].Attempts > out[j].Attempts
		}
		return out[i].Signature < out[j].Signature
	})
	return out, last
}

func (s *Service) partPrint(ctx context.Context, e insights.LogEntry) PartPrint {
	declared := e.IsExternal()
	origin := e.Origin
	if origin == "" {
		origin = insights.OriginPrintalk
	}
	pc := e.PartCount
	if pc < 1 {
		pc = 1
	}
	sig := insights.SettingsSignature(e.Settings)
	p := PartPrint{PrintJobID: e.PrintJobID, LogID: e.ID, Name: e.Name, StartedAt: e.StartedAt, FinishedAt: e.FinishedAt, State: e.State, Outcome: e.Outcome,
		Reached100: e.IsPrinted(), PartCount: pc, Origin: origin, Declared: declared, Level: LevelMeasured, ProfileSig: sig, ProfileText: insights.SettingsLabel(e.Settings),
		Verdict: e.Verdict, VerdictText: verdictText(e.Verdict), FailReason: e.FailReason, Photos: s.photoStrip(ctx, e.PrintJobID, declared)}
	if declared {
		p.Level = LevelDeclared
	}
	if e.Actual.Seconds > 0 {
		p.Seconds = fp(e.Actual.Seconds)
	}
	if e.Actual.Grams > 0 {
		p.Grams = fp(e.Actual.Grams)
	}
	if e.ActualCost != nil && !declared {
		p.Cost, p.Currency = fp(e.ActualCost.Total), e.ActualCost.Currency
	}
	return p
}

// versions lit les versions du modèle (ordre : parents avant enfants, suivant parent_id) et le dernier test de résistance
// de chaque version. Un test sans version identifiable est rangé à part (« version non déterminée »).
func (s *Service) versions(ctx context.Context, modelID string, sh *PartSheet, withStrength bool) error {
	rows, err := s.cfg.Store.DB().QueryContext(ctx, `SELECT id, label, note, COALESCE(parent_id,''), created_at FROM model_versions WHERE model_id=? ORDER BY created_at, id`, modelID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var vs []PartVersion
	for rows.Next() {
		var v PartVersion
		var at string
		if err := rows.Scan(&v.ID, &v.Label, &v.Note, &v.ParentID, &at); err != nil {
			return err
		}
		v.CreatedAt, _ = store.ParseTime(at)
		v.StrengthStatus = "never_tested"
		vs = append(vs, v)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	idx := map[string]int{}
	for i, v := range vs {
		idx[v.ID] = i
	}
	depth := func(i int) int {
		d := 0
		for cur := vs[i].ParentID; cur != ""; d++ {
			j, ok := idx[cur]
			if !ok || d > 100 {
				break
			}
			cur = vs[j].ParentID
		}
		return d
	}
	for i := range vs {
		vs[i].Depth = depth(i)
	}
	// Parents avant enfants : parcours en profondeur depuis les racines, dans l'ordre de création.
	children := map[string][]int{}
	var roots []int
	for i, v := range vs {
		if _, ok := idx[v.ParentID]; ok && v.ParentID != "" {
			children[v.ParentID] = append(children[v.ParentID], i)
		} else {
			roots = append(roots, i)
		}
	}
	var ordered []PartVersion
	seen := map[int]bool{}
	var walk func(i int)
	walk = func(i int) {
		if seen[i] {
			return
		}
		seen[i] = true
		ordered = append(ordered, vs[i])
		for _, c := range children[vs[i].ID] {
			walk(c)
		}
	}
	for _, r := range roots {
		walk(r)
	}
	for i := range vs { // cycle impossible en pratique : rien n'est perdu
		walk(i)
	}
	if n := len(vs); n > 0 {
		latest := vs[n-1].ID
		for i := range ordered {
			ordered[i].IsLatest = ordered[i].ID == latest
		}
	}
	if withStrength {
		tests, err := s.strengthTests(ctx, modelID)
		if err != nil {
			return err
		}
		known := map[string]bool{}
		for i := range ordered {
			known[ordered[i].ID] = true
			if t, ok := tests[ordered[i].ID]; ok {
				c := t
				ordered[i].Strength, ordered[i].StrengthStatus = &c, "tested"
			}
		}
		var un []StrengthRef
		for vid, t := range tests {
			if !known[vid] {
				un = append(un, t)
			}
		}
		sort.Slice(un, func(i, j int) bool {
			if !un[i].CreatedAt.Equal(un[j].CreatedAt) {
				return un[i].CreatedAt.After(un[j].CreatedAt)
			}
			return un[i].TestID < un[j].TestID
		})
		sh.StrengthUnassigned = un
	}
	if ordered != nil {
		sh.Versions = ordered
	}
	return nil
}

// strengthTests : dernier test de résistance par version (clé = version_id ; « » = test sans version).
func (s *Service) strengthTests(ctx context.Context, modelID string) (map[string]StrengthRef, error) {
	rows, err := s.cfg.Store.DB().QueryContext(ctx, `SELECT id, version_id, created_at, material, verdict, safety_factor, assumed FROM strength_tests WHERE model_id=? ORDER BY created_at, id`, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]StrengthRef{}
	for rows.Next() {
		var r StrengthRef
		var vid, at string
		var assumed int
		if err := rows.Scan(&r.TestID, &vid, &at, &r.Material, &r.Verdict, &r.SafetyFactor, &assumed); err != nil {
			return nil, err
		}
		r.CreatedAt, _ = store.ParseTime(at)
		r.Assumed = assumed == 1
		r.SafetyFactor = r2(r.SafetyFactor)
		out[vid] = r // les plus récents écrasent : le dernier test de la version reste
	}
	return out, rows.Err()
}
