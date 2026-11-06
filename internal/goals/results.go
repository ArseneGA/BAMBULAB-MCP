package goals

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Measurement est une mesure faite sur la pièce imprimée.
type Measurement struct {
	Criterion string  `json:"criterion,omitempty" jsonschema:"critère de l'objectif concerné"`
	Name      string  `json:"name" jsonschema:"ex. cote_x, flèche sous charge, rugosité au toucher"`
	Value     float64 `json:"value"`
	Unit      string  `json:"unit,omitempty"`
	Note      string  `json:"note,omitempty"`
}

// ResultInput est l'entrée de record_print_result.
type ResultInput struct {
	ExperimentID   string         `json:"experiment_id"`
	CandidateID    string         `json:"candidate_id"`
	Outcome        string         `json:"outcome" jsonschema:"success | partial | failed"`
	Observations   string         `json:"observations,omitempty"`
	Photos         []string       `json:"photos,omitempty" jsonschema:"références de blobs (sha256) déjà stockés"`
	Measurements   []Measurement  `json:"measurements,omitempty"`
	ActualSettings map[string]any `json:"actual_settings,omitempty" jsonschema:"réglages réellement utilisés quand ils diffèrent du plan, ex. {\"process.layer_height\":\"0.16\"}"`
	Filament       string         `json:"filament,omitempty" jsonschema:"bobine ou référence réellement utilisée si différente du profil de base"`
	Machine        string         `json:"machine,omitempty" jsonschema:"preset machine réellement utilisé si différent du profil de base"`
}

// Deviation est un écart entre réglage prévu et réglage réel.
type Deviation struct {
	Key     string `json:"key"`
	Planned any    `json:"planned,omitempty"`
	Actual  any    `json:"actual"`
}

// ResultContext fixe les conditions dans lesquelles le résultat vaut.
type ResultContext struct {
	MaterialFamily string     `json:"material_family"`
	Filament       string     `json:"filament"`
	Machine        string     `json:"machine"`
	ModelID        string     `json:"model_id"`
	DimsSortedMM   [3]float64 `json:"dims_sorted_mm"`
	Oriented       string     `json:"orientation,omitempty"`
}

// PrintResult est un retour d'expérience enregistré.
type PrintResult struct {
	ID                 string         `json:"id"`
	ExperimentID       string         `json:"experiment_id"`
	CandidateID        string         `json:"candidate_id"`
	CreatedAt          time.Time      `json:"created_at"`
	Outcome            string         `json:"outcome"`
	Observations       string         `json:"observations,omitempty"`
	Photos             []string       `json:"photos,omitempty"`
	Measurements       []Measurement  `json:"measurements,omitempty"`
	PlannedSettings    map[string]any `json:"planned_settings" jsonschema:"réglages effectifs prévus pour le candidat (relevés à la découpe)"`
	Deviations         []Deviation    `json:"deviations,omitempty"`
	CandidateSignature string         `json:"candidate_signature,omitempty"`
	Context            ResultContext  `json:"context"`
	Reuse              string         `json:"reuse_conditions"`
}

const reuseText = "Ce résultat n'est réutilisé que pour la même famille de matériau et la même machine, et seulement pour une géométrie similaire " +
	"(dimensions triées à ±25 %) ou le même modèle ; la qualité de la comparaison (même filament ou non) est indiquée à chaque réutilisation."

// RecordResult lie un essai réel à l'expérience, au candidat et aux réglages.
func (s *Service) RecordResult(ctx context.Context, in ResultInput) (*PrintResult, error) {
	switch in.Outcome {
	case "success", "partial", "failed":
	default:
		return nil, invalid("outcome %q inconnu (success, partial, failed)", in.Outcome)
	}
	if strings.TrimSpace(in.Observations) == "" && len(in.Measurements) == 0 && len(in.Photos) == 0 {
		return nil, invalid("fournir au moins une observation, une mesure ou une photo")
	}
	exp, err := s.GetExperiment(ctx, in.ExperimentID)
	if err != nil {
		return nil, err
	}
	var cand *CandidateView
	for i := range exp.Candidates {
		if exp.Candidates[i].ID == in.CandidateID {
			cand = &exp.Candidates[i]
		}
	}
	if cand == nil {
		return nil, fmt.Errorf("candidat %q de l'expérience %q : %w", in.CandidateID, in.ExperimentID, store.ErrNotFound)
	}
	for _, p := range in.Photos {
		if s.cfg.Blobs == nil || !s.cfg.Blobs.Has(p) {
			return nil, invalid("photo %q : blob introuvable (déposer d'abord le fichier dans le stockage de blobs et passer son sha256)", p)
		}
	}
	r := &PrintResult{
		ID: contracts.NewID("res"), ExperimentID: exp.ID, CandidateID: cand.ID, CreatedAt: s.now(), Outcome: in.Outcome,
		Observations: in.Observations, Photos: in.Photos, Measurements: in.Measurements, PlannedSettings: map[string]any{},
		CandidateSignature: candSig(cand.Candidate), Reuse: reuseText,
	}
	if cand.Metrics != nil {
		r.PlannedSettings = cand.Metrics.Effective
	}
	keys := make([]string, 0, len(in.ActualSettings))
	for k := range in.ActualSettings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		pv, known := r.PlannedSettings[k]
		if known && fmt.Sprint(pv) == fmt.Sprint(in.ActualSettings[k]) {
			continue
		}
		d := Deviation{Key: k, Actual: in.ActualSettings[k]}
		if known {
			d.Planned = pv
		}
		r.Deviations = append(r.Deviations, d)
	}
	fil := in.Filament
	if fil == "" {
		fil = exp.Details.Base.Filaments[0]
	}
	mach := in.Machine
	if mach == "" {
		mach = exp.Details.Base.Machine
	}
	r.Context = ResultContext{MaterialFamily: exp.Details.Base.Family, Filament: fil, Machine: mach, ModelID: exp.ModelID, DimsSortedMM: exp.Details.Geometry.DimsSortedMM}
	if cand.Orientation != nil {
		r.Context.Oriented = cand.Orientation.Label
	}
	doc, _ := json.Marshal(r)
	err = s.cfg.Store.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO print_results(id,experiment_id,candidate_id,model_id,material_family,filament,machine,doc,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			r.ID, r.ExperimentID, r.CandidateID, exp.ModelID, r.Context.MaterialFamily, fil, mach, string(doc), store.FormatTime(r.CreatedAt)); err != nil {
			return err
		}
		return s.cfg.Store.AuditTx(ctx, tx, actor, "result.record", "experiment", exp.ID, map[string]any{"candidate": cand.ID, "outcome": in.Outcome, "photos": len(in.Photos)})
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}

// MemoryQuery décrit les conditions pour lesquelles on cherche des essais.
type MemoryQuery struct {
	MaterialFamily string     `json:"material_family"`
	Machine        string     `json:"machine"`
	Filament       string     `json:"filament,omitempty"`
	ModelID        string     `json:"model_id,omitempty"`
	DimsSortedMM   [3]float64 `json:"dims_sorted_mm"`
}

// MemoryHit est un essai antérieur réutilisable, avec la qualité de la comparaison.
type MemoryHit struct {
	ResultID           string      `json:"result_id"`
	ExperimentID       string      `json:"experiment_id"`
	CandidateID        string      `json:"candidate_id"`
	Comparability      string      `json:"comparability"`
	Strength           int         `json:"strength" jsonschema:"3 : même modèle et même filament ; 2 : géométrie similaire et même filament ; 1 : géométrie similaire, autre filament de la famille"`
	Outcome            string      `json:"outcome"`
	Observations       string      `json:"observations,omitempty"`
	Filament           string      `json:"filament"`
	Machine            string      `json:"machine"`
	CandidateSignature string      `json:"candidate_signature,omitempty"`
	Deviations         []Deviation `json:"deviations,omitempty"`
	CreatedAt          time.Time   `json:"created_at"`
}

func similarDims(a, b [3]float64) bool {
	for i := range a {
		if a[i] <= 0 || b[i] <= 0 {
			return false
		}
		if math.Abs(a[i]-b[i])/math.Max(a[i], b[i]) > 0.25 {
			return false
		}
	}
	return true
}

// Memory renvoie les essais comparables aux conditions données.
func (s *Service) Memory(ctx context.Context, q MemoryQuery, limit int) ([]MemoryHit, error) {
	if q.MaterialFamily == "" || q.Machine == "" {
		return nil, invalid("material_family et machine sont requis : aucun résultat n'est réutilisé hors famille de matériau et machine identiques")
	}
	return s.memoryFor(ctx, q, limit), nil
}

func (s *Service) memoryFor(ctx context.Context, q MemoryQuery, limit int) []MemoryHit {
	if q.MaterialFamily == "" || q.Machine == "" {
		return nil
	}
	rows, err := s.cfg.Store.DB().QueryContext(ctx,
		`SELECT doc FROM print_results WHERE material_family=? AND machine=? ORDER BY created_at DESC LIMIT 500`, q.MaterialFamily, q.Machine)
	if err != nil {
		return nil
	}
	var docs []string
	for rows.Next() {
		var d string
		if rows.Scan(&d) == nil {
			docs = append(docs, d)
		}
	}
	rows.Close()
	var hits []MemoryHit
	for _, d := range docs {
		var r PrintResult
		if json.Unmarshal([]byte(d), &r) != nil {
			continue
		}
		sameFil := q.Filament != "" && r.Context.Filament == q.Filament
		sameModel := q.ModelID != "" && r.Context.ModelID == q.ModelID
		similar := sameModel || similarDims(q.DimsSortedMM, r.Context.DimsSortedMM)
		h := MemoryHit{ResultID: r.ID, ExperimentID: r.ExperimentID, CandidateID: r.CandidateID, Outcome: r.Outcome, Observations: truncate(r.Observations, 300),
			Filament: r.Context.Filament, Machine: r.Context.Machine, CandidateSignature: r.CandidateSignature, Deviations: r.Deviations, CreatedAt: r.CreatedAt}
		switch {
		case sameModel && sameFil:
			h.Strength, h.Comparability = 3, "même modèle, même filament, même machine"
		case similar && sameFil:
			h.Strength, h.Comparability = 2, "géométrie similaire (±25 %), même filament, même machine : indicatif"
		case similar:
			h.Strength, h.Comparability = 1, "géométrie similaire, même famille et même machine mais autre filament : très indicatif, la famille ne garantit pas les mêmes propriétés"
		default:
			continue
		}
		hits = append(hits, h)
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Strength != hits[j].Strength {
			return hits[i].Strength > hits[j].Strength
		}
		return hits[i].CreatedAt.After(hits[j].CreatedAt)
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
