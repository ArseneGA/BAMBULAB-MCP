package printing

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
)

// Vues de l'historique des impressions.
const (
	ViewAll      = "all"      // tout sauf archivées et corbeille (défaut)
	ViewActive   = "active"   // découpe, envoi, confirmation, impression en cours
	ViewFinished = "finished" // terminées avec succès
	ViewFailed   = "failed"   // échecs, annulées, lancements abandonnés
	ViewArchived = "archived" // archivées (masquées de la vue par défaut)
	ViewTrash    = "trash"    // corbeille (récupérable)
)

func isFailedState(st contracts.PrintState) bool {
	return st == contracts.PrintFailed || st == contracts.PrintCancelled || st == contracts.PrintStartAbortedBeforeSend
}

func inView(j Job, view string) bool {
	switch view {
	case ViewTrash:
		return j.Trashed
	case ViewArchived:
		return j.Archived && !j.Trashed
	}
	if j.Trashed || j.Archived {
		return false
	}
	switch view {
	case ViewActive:
		return !j.State.Terminal()
	case ViewFinished:
		return j.State == contracts.PrintCompleted
	case ViewFailed:
		return isFailedState(j.State)
	}
	return true
}

// HistoryFilter filtre l'historique.
type HistoryFilter struct {
	View    string
	ModelID string
	Limit   int
}

// ModelGroup regroupe les impressions d'un même modèle.
type ModelGroup struct {
	ModelID   string `json:"model_id"`
	ModelName string `json:"model_name"`
	Count     int    `json:"count"`
	Failed    int    `json:"failed"`
	Jobs      []Job  `json:"jobs"`
}

// History est la réponse de list_print_history : liste filtrée, compteurs par vue et, à la demande, groupes.
type History struct {
	View     string                    `json:"view"`
	Jobs     []Job                     `json:"jobs"`
	Groups   []ModelGroup              `json:"groups,omitempty"`
	Counts   map[string]int            `json:"counts" jsonschema:"nombre d'impressions par vue : all, active, finished, failed, archived, trash"`
	Progress map[string]*jobs.Progress `json:"progress,omitempty" jsonschema:"avancement des impressions en découpe / envoi / impression, dont la position dans la file"`
}

// ListHistory liste l'historique selon une vue (actives / terminées / échecs / archivées / corbeille) et
// regroupe éventuellement par modèle. Chaque lecture réconcilie d'abord les impressions orphelines.
func (s *Service) ListHistory(ctx context.Context, f HistoryFilter, groupByModel bool) (*History, error) {
	if f.View == "" {
		f.View = ViewAll
	}
	switch f.View {
	case ViewAll, ViewActive, ViewFinished, ViewFailed, ViewArchived, ViewTrash:
	default:
		return nil, invalidf("vue %q inconnue (all, active, finished, failed, archived, trash)", f.View)
	}
	s.SweepOrphans(ctx)
	all, err := s.ListJobs(ctx, "", 0)
	if err != nil {
		return nil, err
	}
	h := &History{View: f.View, Jobs: []Job{}, Counts: map[string]int{}, Progress: map[string]*jobs.Progress{}}
	for _, j := range all {
		for _, v := range []string{ViewAll, ViewActive, ViewFinished, ViewFailed, ViewArchived, ViewTrash} {
			if inView(j, v) {
				h.Counts[v]++
			}
		}
		if !inView(j, f.View) || (f.ModelID != "" && j.ModelID != f.ModelID) {
			continue
		}
		if f.Limit > 0 && len(h.Jobs) >= f.Limit {
			continue
		}
		h.Jobs = append(h.Jobs, j)
		if p := s.ProgressFor(ctx, j); p != nil {
			h.Progress[j.ID] = p
		}
	}
	if groupByModel {
		idx := map[string]*ModelGroup{}
		var order []string
		for _, j := range h.Jobs {
			g := idx[j.ModelID]
			if g == nil {
				name := "(sans modèle)"
				if j.ModelID != "" {
					name = j.ModelID
					if m, err := s.d.Store.GetModel(ctx, j.ModelID); err == nil && m.Name != "" {
						name = m.Name
					}
				}
				g = &ModelGroup{ModelID: j.ModelID, ModelName: name}
				idx[j.ModelID] = g
				order = append(order, j.ModelID)
			}
			g.Count++
			if isFailedState(j.State) {
				g.Failed++
			}
			g.Jobs = append(g.Jobs, j)
		}
		sort.SliceStable(order, func(a, b int) bool { return idx[order[a]].Count > idx[order[b]].Count })
		for _, k := range order {
			h.Groups = append(h.Groups, *idx[k])
		}
	}
	return h, nil
}

// HistoryAction : archive | trash | restore.
type HistoryAction string

// HistoryResult : résultat d'une action d'historique.
type HistoryResult struct {
	Action  string   `json:"action"`
	Done    []string `json:"done"`
	Skipped []string `json:"skipped,omitempty" jsonschema:"impressions non touchées, avec la raison (active, introuvable…)"`
	Note    string   `json:"note"`
}

// ArchiveJobs archive, met à la corbeille ou restaure des impressions TERMINÉES (réussies, échouées, annulées).
// Une impression active n'est jamais touchée. La corbeille est récupérable (restore) : rien n'est supprimé
// définitivement, ni le fichier découpé, ni la trace d'audit.
func (s *Service) ArchiveJobs(ctx context.Context, action string, ids []string) (*HistoryResult, error) {
	switch action {
	case "archive", "trash", "restore":
	default:
		return nil, invalidf("action %q inconnue (archive, trash, restore)", action)
	}
	if len(ids) == 0 {
		return nil, invalidf("print_job_ids requis")
	}
	if len(ids) > 500 {
		return nil, invalidf("500 impressions au plus par appel")
	}
	res := &HistoryResult{Action: action}
	for _, id := range ids {
		_, err := s.update(ctx, id, func(x *Job) error {
			if action != "restore" && !x.State.Terminal() {
				return fmt.Errorf("%w : %s est en %s, active : on ne l'archive pas", ErrState, x.ID, x.State)
			}
			switch action {
			case "archive":
				x.Archived, x.Trashed = true, false
			case "trash":
				x.Trashed = true
			case "restore":
				x.Archived, x.Trashed = false, false
			}
			return nil
		})
		if err != nil {
			res.Skipped = append(res.Skipped, id+" : "+strings.TrimPrefix(err.Error(), ErrState.Error()+" : "))
			continue
		}
		res.Done = append(res.Done, id)
		_ = s.d.Store.Audit(ctx, actor, "print.history_"+action, "print_job", id, nil)
	}
	switch action {
	case "trash":
		res.Note = "Mises à la corbeille (récupérables avec l'action restore). Rien n'est supprimé définitivement."
	case "archive":
		res.Note = "Archivées : masquées de la vue par défaut, consultables dans la vue archived."
	default:
		res.Note = "Restaurées dans l'historique."
	}
	return res, nil
}
