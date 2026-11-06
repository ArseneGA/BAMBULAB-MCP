package goals

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
)

// ExperimentSummary est la ligne de liste d'une expérience passée.
type ExperimentSummary struct {
	ID             string    `json:"id"`
	GoalID         string    `json:"goal_id,omitempty"`
	ModelID        string    `json:"model_id"`
	ModelVersionID string    `json:"model_version_id"`
	CreatedAt      time.Time `json:"created_at"`
	// State : running (découpes en cours ou en attente) | complete (toutes terminées avec succès) |
	// partial (terminée, au moins une découpe échouée ou annulée).
	State          string `json:"state"`
	Candidates     int    `json:"candidates"`
	Succeeded      int    `json:"succeeded"`
	Failed         int    `json:"failed"`
	Machine        string `json:"machine,omitempty"`
	MaterialFamily string `json:"material_family,omitempty"`
}

// ListExperiments liste les expériences d'un modèle (ou toutes si modelID est vide), la plus récente d'abord.
func (s *Service) ListExperiments(ctx context.Context, modelID string, limit int) ([]ExperimentSummary, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT id FROM experiments`
	var args []any
	if modelID != "" {
		q += ` WHERE model_id=?`
		args = append(args, modelID)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.cfg.Store.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]ExperimentSummary, 0, len(ids))
	for _, id := range ids {
		x, err := s.GetExperiment(ctx, id) // rafraîchit aussi l'état depuis la file
		if err != nil {
			return nil, err
		}
		sum := ExperimentSummary{ID: x.ID, GoalID: x.GoalID, ModelID: x.ModelID, ModelVersionID: x.ModelVersionID, CreatedAt: x.CreatedAt,
			Candidates: len(x.Candidates), Machine: x.Details.Base.Machine, MaterialFamily: x.Details.Base.Family}
		active := false
		for _, c := range x.Candidates {
			switch c.State {
			case "succeeded":
				sum.Succeeded++
			case "failed", "cancelled":
				sum.Failed++
			default:
				active = true
			}
		}
		switch {
		case active:
			sum.State = "running"
		case sum.Failed > 0:
			sum.State = "partial"
		default:
			sum.State = "complete"
		}
		out = append(out, sum)
	}
	return out, nil
}

// MemoryRequest regroupe les façons de désigner les conditions d'une recherche d'essais (outil get_print_memory).
type MemoryRequest struct {
	ExperimentID   string
	MaterialFamily string
	Machine        string
	Filament       string
	ModelID        string
	SizeMM         []float64
}

// MemoryResponse : conditions retenues et essais comparables.
type MemoryResponse struct {
	Query MemoryQuery `json:"query"`
	Hits  []MemoryHit `json:"hits"`
	Note  string      `json:"note"`
}

// MemoryFor résout les conditions (depuis une expérience ou explicites) et renvoie les essais comparables.
func (s *Service) MemoryFor(ctx context.Context, in MemoryRequest, limit int) (*MemoryResponse, error) {
	q := MemoryQuery{MaterialFamily: in.MaterialFamily, Machine: in.Machine, Filament: in.Filament, ModelID: in.ModelID}
	if in.ExperimentID != "" {
		x, err := s.GetExperiment(ctx, in.ExperimentID)
		if err != nil {
			return nil, err
		}
		q = MemoryQuery{MaterialFamily: x.Details.Base.Family, Machine: x.Details.Base.Machine, ModelID: x.ModelID, DimsSortedMM: x.Details.Geometry.DimsSortedMM}
		if len(x.Details.Base.Filaments) > 0 {
			q.Filament = x.Details.Base.Filaments[0]
		}
	} else if len(in.SizeMM) == 3 {
		d := [3]float64{in.SizeMM[0], in.SizeMM[1], in.SizeMM[2]}
		sort.Sort(sort.Reverse(sort.Float64Slice(d[:])))
		q.DimsSortedMM = d
	}
	hits, err := s.Memory(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	if hits == nil {
		hits = []MemoryHit{}
	}
	return &MemoryResponse{Query: q, Hits: hits,
		Note: "Seuls les essais de même famille de matériau et même machine, de géométrie similaire (±25 %) ou du même modèle, sont renvoyés ; la colonne comparability dit à quel point ils sont comparables."}, nil
}

// FilamentPresets liste les presets filament de Bambu Studio compatibles avec une machine (tous si machine est vide).
func (s *Service) FilamentPresets(machine string) ([]string, error) {
	if err := s.needProfiles(); err != nil {
		return nil, err
	}
	if machine == "" {
		return s.prof.Names(profiles.KindFilament), nil
	}
	if _, ok := s.prof.Get(profiles.KindMachine, machine); !ok {
		return nil, fmt.Errorf("%w: preset machine inconnu %q", ErrInvalid, machine)
	}
	names, err := s.prof.CompatibleFilaments(machine)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return names, nil
}
