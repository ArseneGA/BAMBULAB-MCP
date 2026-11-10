package projects

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/insights"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Lectures : get_project (fiche) et list_projects (arbre). Tout est recalculé à la demande depuis le journal : aucune valeur
// d'avancement, de coût ou de temps n'est stockée.

type rawPart struct {
	modelID, name string
	qty           int
	role          string
	implicit      bool
	trashed       bool
}

type snapshot struct {
	projects map[string]Project
	children map[string][]string
	parts    map[string][]rawPart
}

func (s *Service) loadSnapshot(ctx context.Context) (*snapshot, error) {
	db := s.cfg.Store.DB()
	sn := &snapshot{projects: map[string]Project{}, children: map[string][]string{}, parts: map[string][]rawPart{}}
	rows, err := db.QueryContext(ctx, `SELECT `+projectCols+` FROM projects`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		sn.projects[p.ID] = p
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for id, p := range sn.projects {
		if p.ParentID != "" {
			sn.children[p.ParentID] = append(sn.children[p.ParentID], id)
		}
	}
	for _, ids := range sn.children {
		sort.Slice(ids, func(i, j int) bool {
			a, b := sn.projects[ids[i]], sn.projects[ids[j]]
			if a.Name != b.Name {
				return a.Name < b.Name
			}
			return a.ID < b.ID
		})
	}
	prow, err := db.QueryContext(ctx, `SELECT pp.project_id, pp.model_id, COALESCE(m.name,''), pp.quantity, pp.role, COALESCE(m.deleted_at,'')
		FROM project_parts pp LEFT JOIN models m ON m.id=pp.model_id ORDER BY pp.project_id, pp.position, pp.added_at, pp.model_id`)
	if err != nil {
		return nil, err
	}
	for prow.Next() {
		var pid string
		var rp rawPart
		var del string
		if err := prow.Scan(&pid, &rp.modelID, &rp.name, &rp.qty, &rp.role, &del); err != nil {
			prow.Close()
			return nil, err
		}
		rp.trashed = del != ""
		sn.parts[pid] = append(sn.parts[pid], rp)
	}
	if err := prow.Err(); err != nil {
		prow.Close()
		return nil, err
	}
	prow.Close()
	// pièces adoptées avec une collection : quantité 1, sauf si le projet cite déjà le modèle
	for id, p := range sn.projects {
		if p.CollectionID == "" {
			continue
		}
		cited := map[string]bool{}
		for _, rp := range sn.parts[id] {
			cited[rp.modelID] = true
		}
		crow, err := db.QueryContext(ctx, `SELECT id, name FROM models WHERE collection_id=? AND deleted_at IS NULL ORDER BY name, id LIMIT ?`, p.CollectionID, MaxPartsOut)
		if err != nil {
			return nil, err
		}
		for crow.Next() {
			var rp rawPart
			if err := crow.Scan(&rp.modelID, &rp.name); err != nil {
				crow.Close()
				return nil, err
			}
			if !cited[rp.modelID] {
				rp.qty, rp.implicit = 1, true
				sn.parts[id] = append(sn.parts[id], rp)
			}
		}
		crow.Close()
	}
	return sn, nil
}

// subtreeModels : modèles cités par un projet et ses descendants (parcours borné, gardé contre les cycles).
func (sn *snapshot) subtreeModels(id string, includeArchived bool, into map[string]bool, depth int, path map[string]bool) {
	if depth > maxTraversal || path[id] {
		return
	}
	path[id] = true
	for _, rp := range sn.parts[id] {
		into[rp.modelID] = true
	}
	for _, c := range sn.children[id] {
		if includeArchived || !sn.projects[c].Archived {
			sn.subtreeModels(c, includeArchived, into, depth+1, path)
		}
	}
	delete(path, id)
}

type treeNode struct {
	p         Project
	parts     []PartProgress
	truncated bool
	own, agg  Progress
	kids      []*treeNode
	models    map[string]bool // modèles distincts du sous-arbre
}

func (s *Service) build(sn *snapshot, counts map[string]*insights.PrintCounts, id string, depth int, includeArchived bool, path map[string]bool) *treeNode {
	n := &treeNode{p: sn.projects[id], models: map[string]bool{}}
	path[id] = true
	raw := sn.parts[id]
	if len(raw) > MaxPartsOut {
		raw, n.truncated = raw[:MaxPartsOut], true
	}
	for _, rp := range raw {
		pp := ComputePart(rp.modelID, rp.name, rp.qty, rp.role, rp.implicit, counts[rp.modelID])
		pp.Trashed = rp.trashed
		n.parts = append(n.parts, pp)
		n.models[rp.modelID] = true
	}
	n.own = Summarize(n.parts)
	n.agg = n.own
	if depth < maxTraversal {
		for _, c := range sn.children[id] {
			if path[c] || (!includeArchived && sn.projects[c].Archived) {
				continue
			}
			k := s.build(sn, counts, c, depth+1, includeArchived, path)
			n.kids = append(n.kids, k)
			n.agg = AddProgress(n.agg, k.agg)
			for m := range k.models {
				n.models[m] = true
			}
		}
	}
	delete(path, id)
	return n
}

// Totals : coût et temps réels cumulés des pièces distinctes d'un projet (sous-projets compris).
type Totals struct {
	Available     bool    `json:"available" jsonschema:"false : aucune impression au journal pour ces pièces, coût et temps non disponibles"`
	Reason        string  `json:"reason,omitempty" jsonschema:"pourquoi les totaux ne sont pas disponibles"`
	ActualCost    float64 `json:"actual_cost" jsonschema:"somme des coûts réels (calculés) des impressions Printalk"`
	Currency      string  `json:"currency,omitempty"`
	ActualSeconds float64 `json:"actual_seconds" jsonschema:"somme des durées réelles (mesurées)"`
	ActualGrams   float64 `json:"actual_grams" jsonschema:"somme des grammes (pesés ou estimés selon la fiche)"`
	PlannedCost   float64 `json:"planned_cost,omitempty" jsonschema:"coût estimé des tentatives en cours, à part du coût réel"`
	InProgress    int     `json:"in_progress"`
	Attempts      int     `json:"attempts"`
	Basis         string  `json:"basis" jsonschema:"base du calcul"`
}

func totalsOf(models map[string]bool, counts map[string]*insights.PrintCounts) (Totals, int, int) {
	t := Totals{Basis: "impressions de chaque pièce distincte du projet, comptées une fois même si la pièce figure dans plusieurs sous-projets (une même pièce partagée avec un autre projet y compte aussi)"}
	ids := make([]string, 0, len(models))
	for m := range models {
		ids = append(ids, m)
	}
	sort.Strings(ids)
	for _, id := range ids {
		c := counts[id]
		if c == nil {
			continue
		}
		t.Attempts += c.Attempts
		t.InProgress += c.InProgress
		t.ActualCost += c.ActualCost
		t.ActualSeconds += c.ActualSeconds
		t.ActualGrams += c.ActualGrams
		t.PlannedCost += c.PlannedCost
		if t.Currency == "" {
			t.Currency = c.Currency
		}
	}
	t.ActualCost, t.ActualSeconds, t.ActualGrams, t.PlannedCost = r2(t.ActualCost), r2(t.ActualSeconds), r2(t.ActualGrams), r2(t.PlannedCost)
	t.Available = t.Attempts > 0
	if !t.Available {
		t.Reason = "aucune impression de ces pièces au journal"
		if t.InProgress > 0 {
			t.Reason = "aucune impression terminée : seules des tentatives en cours existent (coût estimé à part)"
		}
	}
	return t, t.Attempts, t.InProgress
}

func r2(v float64) float64 {
	if v < 0 {
		return v
	}
	return float64(int64(v*100+0.5)) / 100
}

// SubProject : un sous-projet avec son avancement propre (ses pièces et celles de ses sous-projets).
type SubProject struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Status     string   `json:"status"`
	Archived   bool     `json:"archived,omitempty"`
	Own        Progress `json:"own" jsonschema:"avancement de ses seules pièces"`
	Progress   Progress `json:"progress" jsonschema:"avancement de ses pièces et de ses sous-projets"`
	PartsCount int      `json:"parts_count"`
	ParentID   string   `json:"parent_id" jsonschema:"projet parent (le projet de la fiche pour un sous-projet direct)"`
	Depth      int      `json:"depth" jsonschema:"1 = sous-projet direct, 2 = son propre sous-projet..."`
}

// flattenSubs range les descendants en liste à plat, dans l'ordre de l'arbre (parent avant ses enfants) : un schéma
// d'outil ne peut pas être récursif, la carte reconstruit l'arbre avec parent_id.
func flattenSubs(n *treeNode, depth int, out *[]SubProject) {
	for _, k := range n.kids {
		*out = append(*out, SubProject{ID: k.p.ID, Name: k.p.Name, Status: k.p.Status, Archived: k.p.Archived, Own: k.own, Progress: k.agg,
			PartsCount: len(k.parts), ParentID: n.p.ID, Depth: depth})
		flattenSubs(k, depth+1, out)
	}
}

// VariantRef : une déclinaison du projet (PJ13, vague 3) ; la liste est vide tant que PJ13 n'est pas livré.
type VariantRef struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	GoalKind string    `json:"goal_kind"`
	ChosenAt time.Time `json:"chosen_at,omitzero"`
}

// Detail : la fiche d'un projet (get_project).
type Detail struct {
	Project         Project           `json:"project"`
	Level           int               `json:"level" jsonschema:"niveau dans la hiérarchie (projet racine = 1)"`
	Path            []ProjectRef      `json:"path,omitempty" jsonschema:"ancêtres, de la racine au parent"`
	Parts           []PartProgress    `json:"parts"`
	PartsTruncated  bool              `json:"parts_truncated,omitempty"`
	Own             Progress          `json:"own" jsonschema:"avancement des seules pièces de ce projet"`
	Progress        Progress          `json:"progress" jsonschema:"avancement agrégé : ses pièces et celles de ses sous-projets (chaque pièce comptée une fois par chemin d'appartenance)"`
	SubProjects     []SubProject      `json:"sub_projects"`
	Totals          Totals            `json:"totals"`
	Variants        []VariantRef      `json:"variants"`
	SuggestedStatus string            `json:"suggested_status,omitempty" jsonschema:"statut suggéré par le calcul, JAMAIS appliqué seul (update_project)"`
	SuggestedReason string            `json:"suggested_reason,omitempty"`
	NextAction      string            `json:"next_action"`
	Levels          map[string]string `json:"levels" jsonschema:"niveau de chaque valeur : mesuré | calculé | déclaré"`
	Note            string            `json:"note"`
}

// Get rend la fiche d'un projet.
func (s *Service) Get(ctx context.Context, id string) (*Detail, error) {
	if strings.TrimSpace(id) == "" {
		return nil, invalid("id requis")
	}
	sn, err := s.loadSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	if _, ok := sn.projects[id]; !ok {
		return nil, fmt.Errorf("projet %q : %w", id, ErrNotFound)
	}
	counts, err := s.countsFor(ctx, sn, []string{id}, true)
	if err != nil {
		return nil, err
	}
	n := s.build(sn, counts, id, 0, true, map[string]bool{})
	d := &Detail{Project: n.p, Parts: n.parts, PartsTruncated: n.truncated, Own: n.own, Progress: n.agg, SubProjects: []SubProject{}}
	if d.Parts == nil {
		d.Parts = []PartProgress{}
	}
	flattenSubs(n, 1, &d.SubProjects)
	d.Level = 1
	var anc []ProjectRef
	for cur, i := n.p.ParentID, 0; cur != "" && i <= maxTraversal; i++ {
		p := sn.projects[cur]
		anc = append([]ProjectRef{{ID: p.ID, Name: p.Name, Status: p.Status}}, anc...)
		d.Level++
		cur = p.ParentID
	}
	d.Path = anc
	var attempts, inProgress int
	d.Totals, attempts, inProgress = totalsOf(n.models, counts)
	d.SuggestedStatus, d.SuggestedReason = SuggestStatus(n.p.Status, n.p.Archived, n.agg, attempts, inProgress)
	d.NextAction = NextAction(n.p.Status, n.p.Archived, n.parts, len(n.kids), n.agg)
	d.Levels = map[string]string{"avancement": "calculé", "temps": "mesuré", "coût": "calculé", "grammes": "pesé ou estimé selon l'impression", "verdict": "déclaré"}
	d.Variants = s.variantRefs(ctx, id)
	d.Note = detailNote(d)
	return d, nil
}

func detailNote(d *Detail) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Projet « %s » (%s) : %s.", d.Project.Name, StatusLabel(d.Project.Status), d.Progress.Text)
	if !d.Totals.Available {
		b.WriteString(" Coût et temps : non disponibles (" + d.Totals.Reason + ").")
	}
	if d.SuggestedStatus != "" {
		fmt.Fprintf(&b, " Statut suggéré : %s (%s) ; il ne change que par update_project.", StatusLabel(d.SuggestedStatus), d.SuggestedReason)
	}
	b.WriteString(" Prochaine étape : " + d.NextAction)
	return b.String()
}

func (s *Service) variantRefs(ctx context.Context, projectID string) []VariantRef {
	out := []VariantRef{}
	rows, err := s.cfg.Store.DB().QueryContext(ctx, `SELECT id, name, goal_kind, COALESCE(chosen_at,'') FROM project_variants WHERE project_id=? ORDER BY created_at, id`, projectID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var v VariantRef
		var chosen string
		if rows.Scan(&v.ID, &v.Name, &v.GoalKind, &chosen) == nil {
			if chosen != "" {
				v.ChosenAt, _ = store.ParseTime(chosen)
			}
			out = append(out, v)
		}
	}
	return out
}

// countsFor charge le comptage du journal pour tous les modèles des sous-arbres demandés (un seul appel).
func (s *Service) countsFor(ctx context.Context, sn *snapshot, roots []string, includeArchived bool) (map[string]*insights.PrintCounts, error) {
	set := map[string]bool{}
	for _, r := range roots {
		sn.subtreeModels(r, includeArchived, set, 0, map[string]bool{})
	}
	ids := make([]string, 0, len(set))
	for m := range set {
		ids = append(ids, m)
	}
	sort.Strings(ids)
	if s.cfg.Counts == nil || len(ids) == 0 {
		return map[string]*insights.PrintCounts{}, nil
	}
	return s.cfg.Counts.PrintCountsByModel(ctx, ids)
}

// ListQuery : filtres de list_projects.
type ListQuery struct {
	Status          string `json:"status,omitempty" jsonschema:"idea | design | printing | done | in_use | abandoned : liste plate de tous les projets de ce statut, à tout niveau"`
	ParentID        string `json:"parent_id,omitempty" jsonschema:"descendre : les sous-projets de ce projet (par défaut, les projets racine avec leurs sous-projets imbriqués)"`
	IncludeArchived bool   `json:"include_archived,omitempty" jsonschema:"inclure les projets archivés (masqués par défaut)"`
	Limit           int    `json:"limit,omitempty" jsonschema:"nombre maximal de projets de premier niveau (défaut 50, max 200)"`
}

// ListItem : un projet de la liste avec son avancement agrégé. La liste est à plat dans l'ordre de l'arbre (un projet
// avant ses sous-projets) ; depth 0 = premier niveau, la carte reconstruit l'arbre avec project.parent_id.
type ListItem struct {
	Project         Project  `json:"project"`
	Depth           int      `json:"depth" jsonschema:"0 = premier niveau de la liste, 1 = ses sous-projets..."`
	Progress        Progress `json:"progress"`
	PartsCount      int      `json:"parts_count"`
	SuggestedStatus string   `json:"suggested_status,omitempty"`
}

// ListOutput : l'arbre des projets, à plat.
type ListOutput struct {
	Projects []ListItem        `json:"projects"`
	Total    int               `json:"total" jsonschema:"nombre de projets de premier niveau correspondant aux filtres"`
	Applied  map[string]string `json:"applied_filters" jsonschema:"filtres réellement appliqués"`
	Counts   map[string]int    `json:"counts_by_status" jsonschema:"nombre de projets non archivés par statut (tous filtres levés)"`
	Note     string            `json:"note"`
}

// List rend l'arbre des projets (ou la liste plate d'un statut).
func (s *Service) List(ctx context.Context, q ListQuery) (*ListOutput, error) {
	q.Status = strings.TrimSpace(q.Status)
	if q.Status != "" && !validStatus(q.Status) {
		return nil, invalid("status %q inconnu (%s)", q.Status, strings.Join(Statuses, ", "))
	}
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Limit > maxList {
		q.Limit = maxList
	}
	sn, err := s.loadSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	if q.ParentID != "" {
		if _, ok := sn.projects[q.ParentID]; !ok {
			return nil, fmt.Errorf("projet parent %q : %w", q.ParentID, ErrNotFound)
		}
	}
	out := &ListOutput{Projects: []ListItem{}, Applied: map[string]string{}, Counts: map[string]int{}}
	for _, p := range sn.projects {
		if !p.Archived {
			out.Counts[p.Status]++
		}
	}
	if q.Status != "" {
		out.Applied["status"] = q.Status
	}
	if q.ParentID != "" {
		out.Applied["parent_id"] = q.ParentID
	}
	out.Applied["include_archived"] = fmt.Sprint(q.IncludeArchived)
	visible := func(p Project) bool { return q.IncludeArchived || !p.Archived }
	var tops []string
	for id, p := range sn.projects {
		if !visible(p) {
			continue
		}
		switch {
		case q.Status != "":
			if p.Status != q.Status {
				continue
			}
			if q.ParentID != "" && p.ParentID != q.ParentID {
				continue
			}
		case q.ParentID != "":
			if p.ParentID != q.ParentID {
				continue
			}
		default:
			// racines ; un sous-projet dont le parent est archivé (et masqué) remonte au premier niveau : il reste consultable
			if p.ParentID != "" && visible(sn.projects[p.ParentID]) {
				continue
			}
		}
		tops = append(tops, id)
	}
	sort.Slice(tops, func(i, j int) bool {
		a, b := sn.projects[tops[i]], sn.projects[tops[j]]
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.UpdatedAt.After(b.UpdatedAt)
		}
		return a.ID < b.ID
	})
	out.Total = len(tops)
	if len(tops) > q.Limit {
		tops = tops[:q.Limit]
	}
	counts, err := s.countsFor(ctx, sn, tops, q.IncludeArchived)
	if err != nil {
		return nil, err
	}
	for _, id := range tops {
		n := s.build(sn, counts, id, 0, q.IncludeArchived, map[string]bool{})
		out.Projects = s.listItems(out.Projects, n, counts, 0, q.Status != "")
	}
	switch {
	case out.Total == 0:
		out.Note = "Aucun projet ne correspond : créer le premier avec create_project, puis lui ajouter des pièces avec add_project_part."
	case out.Total > len(out.Projects):
		out.Note = fmt.Sprintf("%d projet(s) correspondant (%d affichés) : augmenter limit ou filtrer.", out.Total, len(out.Projects))
	default:
		out.Note = fmt.Sprintf("%d projet(s).", out.Total)
	}
	return out, nil
}

func (s *Service) listItems(out []ListItem, n *treeNode, counts map[string]*insights.PrintCounts, depth int, flat bool) []ListItem {
	_, attempts, inProgress := totalsOf(n.models, counts)
	sug, _ := SuggestStatus(n.p.Status, n.p.Archived, n.agg, attempts, inProgress)
	out = append(out, ListItem{Project: n.p, Depth: depth, Progress: n.agg, PartsCount: len(n.parts), SuggestedStatus: sug})
	if !flat {
		for _, k := range n.kids {
			out = s.listItems(out, k, counts, depth+1, false)
		}
	}
	return out
}

var _ = sql.ErrNoRows
