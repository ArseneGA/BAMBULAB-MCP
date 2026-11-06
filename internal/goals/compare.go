package goals

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/engineering"
)

// EngEntry est un calcul d'ingénierie étiqueté pour un candidat.
type EngEntry struct {
	Criterion          string   `json:"criterion"`
	CandidateID        string   `json:"candidate_id"`
	Label              string   `json:"label"`
	Method             string   `json:"method"`
	OrientationAssumed string   `json:"orientation_assumed" jsonschema:"xy : effort dans le plan des couches ; z : perpendiculaire aux couches"`
	SafetyFactor       float64  `json:"safety_factor"`
	SafetyFactorXY     *float64 `json:"safety_factor_xy,omitempty"`
	SafetyFactorZ      *float64 `json:"safety_factor_z,omitempty"`
	Verdict            string   `json:"verdict"`
	StressMPa          float64  `json:"stress_MPa"`
	DeflectionMM       float64  `json:"deflection_mm"`
	Level              string   `json:"confidence_level"`
	Assumptions        []string `json:"assumptions"`
	Warnings           []string `json:"warnings,omitempty"`
	Note               string   `json:"note,omitempty"`
	Error              string   `json:"error,omitempty"`
}

// ThresholdCheck confronte une mesure estimée à un seuil.
type ThresholdCheck struct {
	Criterion     string  `json:"criterion"`
	Label         string  `json:"label"`
	Metric        string  `json:"metric"`
	Op            string  `json:"op"`
	Limit         float64 `json:"limit"`
	Value         float64 `json:"value"`
	Pass          bool    `json:"pass"`
	NonNegotiable bool    `json:"non_negotiable,omitempty"`
	Basis         string  `json:"basis"`
}

// CompareCandidate est une ligne du tableau.
type CompareCandidate struct {
	ID                string            `json:"id"`
	Label             string            `json:"label"`
	Kind              string            `json:"kind"`
	State             string            `json:"state"`
	Rationale         string            `json:"rationale"`
	Changes           []string          `json:"changes"`
	Orientation       string            `json:"orientation,omitempty"`
	ExpectedTradeoffs []string          `json:"expected_tradeoffs"`
	Notes             []string          `json:"notes,omitempty"`
	Metrics           map[string]Metric `json:"metrics,omitempty"`
	Engineering       []EngEntry        `json:"engineering,omitempty"`
	Thresholds        []ThresholdCheck  `json:"threshold_checks,omitempty"`
	ViolatesNonNeg    bool              `json:"violates_non_negotiable,omitempty"`
	Indicators        map[string]any    `json:"indicators_for_non_evaluable,omitempty"`
	Error             string            `json:"error,omitempty"`
}

// RankEntry est une position dans un classement par critère.
type RankEntry struct {
	CandidateID string  `json:"candidate_id"`
	Value       float64 `json:"value"`
}

// Ranking classe les candidats sur un seul critère mesurable.
type Ranking struct {
	Criterion string      `json:"criterion"`
	Label     string      `json:"label"`
	Metric    string      `json:"metric"`
	Better    string      `json:"better" jsonschema:"lower | higher"`
	Order     []RankEntry `json:"order"`
	Basis     string      `json:"basis"`
	AllTied   bool        `json:"all_tied,omitempty"`
	Note      string      `json:"note,omitempty"`
}

// CompareOutput est le tableau de compare_variants.
type CompareOutput struct {
	ExperimentID   string             `json:"experiment_id"`
	ModelVersionID string             `json:"model_version_id"`
	GoalID         string             `json:"goal_id,omitempty"`
	Complete       bool               `json:"complete" jsonschema:"toutes les découpes sont terminées (réussies ou non)"`
	Pending        []string           `json:"pending,omitempty"`
	Candidates     []CompareCandidate `json:"candidates"`
	Rankings       []Ranking          `json:"rankings"`
	NotEvaluable   []NonEvaluable     `json:"not_evaluable" jsonschema:"critères à vérifier par essai physique"`
	Conflicts      []Conflict         `json:"conflicts,omitempty"`
	Memory         []MemoryHit        `json:"memory,omitempty"`
	SkippedLevers  []Skipped          `json:"skipped_levers,omitempty"`
	Notice         string             `json:"notice"`
}

const noticeText = "Aucun gagnant n'est désigné et aucun score global n'est calculé : chaque critère mesurable est classé séparément (valeurs estimées par le slicer ou calculées), " +
	"les autres critères sont « à vérifier par essai ». Une estimation du slicer ne prouve ni résistance, ni aspect, ni précision réelle."

// CompareVariants construit le tableau comparatif d'une expérience.
func (s *Service) CompareVariants(ctx context.Context, experimentID string) (*CompareOutput, error) {
	exp, err := s.GetExperiment(ctx, experimentID)
	if err != nil {
		return nil, err
	}
	var goal *Goal
	if exp.GoalID != "" {
		if goal, err = s.GetGoal(ctx, exp.GoalID); err != nil {
			return nil, err
		}
	}
	out := &CompareOutput{ExperimentID: exp.ID, ModelVersionID: exp.ModelVersionID, GoalID: exp.GoalID, Complete: true,
		Rankings: []Ranking{}, NotEvaluable: []NonEvaluable{}, Memory: exp.Details.Prior, SkippedLevers: exp.Details.Skipped, Notice: noticeText}
	for _, c := range exp.Candidates {
		cc := CompareCandidate{ID: c.ID, Label: c.Label, Kind: c.Kind, State: c.State, Rationale: c.Rationale,
			ExpectedTradeoffs: c.Tradeoffs, Notes: c.Notes, Error: c.Error, Changes: []string{}}
		for _, ch := range c.Changes {
			if ch.Key == "orientation" {
				cc.Orientation = fmt.Sprint(ch.To)
				cc.Changes = append(cc.Changes, "orientation : "+fmt.Sprint(ch.To))
				continue
			}
			cc.Changes = append(cc.Changes, fmt.Sprintf("%s : %s → %s", ch.Key, fmtAny(ch.From), fmtAny(ch.To)))
		}
		if c.State != "succeeded" && c.State != "failed" && c.State != "cancelled" {
			out.Complete = false
			out.Pending = append(out.Pending, c.ID)
		}
		if c.Metrics != nil {
			cc.Metrics = c.Metrics.Values
		}
		if goal != nil {
			for _, crit := range goal.Criteria {
				if crit.Type == "strength" && crit.Status == "evaluable" {
					cc.Engineering = append(cc.Engineering, s.engineeringFor(crit, c, goal, exp.Details.Base.Family))
				}
			}
			cc.Thresholds = thresholdChecks(goal, cc)
			for _, t := range cc.Thresholds {
				if !t.Pass && t.NonNegotiable {
					cc.ViolatesNonNeg = true
				}
			}
			cc.Indicators = indicatorsFor(goal, c)
		}
		out.Candidates = append(out.Candidates, cc)
	}
	if goal != nil {
		out.NotEvaluable = append(out.NotEvaluable, goal.NonEvaluable...)
		out.Conflicts = goal.Conflicts
		out.Rankings = rankings(goal, out.Candidates)
	}
	return out, nil
}

func fmtAny(v any) string {
	switch t := v.(type) {
	case []string:
		return fmt.Sprint(t)
	case nil:
		return "—"
	}
	return fmt.Sprint(v)
}

func (s *Service) engineeringFor(crit Criterion, c CandidateView, goal *Goal, family string) EngEntry {
	e := EngEntry{Criterion: crit.ID, CandidateID: c.ID, Label: LabelEngineering, Method: "poutre d'Euler-Bernoulli (engineering.BeamStress) ; remplissage et parois non modélisés"}
	spec, err := crit.Beam()
	if err != nil || spec == nil {
		e.Error = "paramètres de calcul illisibles"
		return e
	}
	dir := crit.ParamString("load_direction")
	assumed := dir
	switch dir {
	case "z":
		assumed = "z"
		for _, ch := range c.Changes {
			if ch.Lever == "st_orient" {
				assumed = "xy"
				e.Note = "orientation modifiée : l'effort est supposé dans le plan des couches"
			}
		}
	case "xy":
	default:
		assumed = "z"
		e.Note = "direction d'effort multiple ou inconnue : cas défavorable (perpendiculaire aux couches) retenu"
	}
	e.OrientationAssumed = assumed
	r, err := engineering.BeamStress(spec.params(family, assumed))
	if err != nil {
		e.Error = err.Error()
		return e
	}
	e.SafetyFactor = round4(r.SafetyFactor)
	e.SafetyFactorXY, e.SafetyFactorZ = r.SafetyFactorXY, r.SafetyFactorZ
	e.Verdict, e.StressMPa, e.DeflectionMM = r.Verdict, round4(r.Value), round4(r.DeflectionMM)
	e.Level = string(r.Level)
	e.Assumptions = r.Assumptions
	e.Warnings = r.Warnings
	return e
}

// metricValue lit la valeur d'une métrique pour un candidat.
func metricValue(c CompareCandidate, metric string) (float64, bool) {
	if metric == "safety_factor" {
		for _, e := range c.Engineering {
			if e.Error == "" {
				return e.SafetyFactor, true
			}
		}
		return 0, false
	}
	if m, ok := c.Metrics[metric]; ok {
		return m.Value, true
	}
	return 0, false
}

func thresholdChecks(goal *Goal, c CompareCandidate) []ThresholdCheck {
	var out []ThresholdCheck
	if c.State != "succeeded" {
		return out
	}
	for _, crit := range goal.Criteria {
		if crit.Norm == nil {
			continue
		}
		v, ok := metricValue(c, crit.Norm.Metric)
		if !ok {
			continue
		}
		pass := false
		switch crit.Norm.Op {
		case "<=":
			pass = v <= crit.Norm.Value
		case ">=":
			pass = v >= crit.Norm.Value
		case "==":
			pass = math.Abs(v-crit.Norm.Value) <= 1e-9
		}
		basis := LabelSlicer
		if crit.Norm.Metric == "safety_factor" {
			basis = LabelEngineering
		}
		out = append(out, ThresholdCheck{Criterion: crit.ID, Label: crit.Label, Metric: crit.Norm.Metric, Op: crit.Norm.Op,
			Limit: crit.Norm.Value, Value: v, Pass: pass, NonNegotiable: crit.NonNegotiable, Basis: basis})
	}
	return out
}

func indicatorsFor(goal *Goal, c CandidateView) map[string]any {
	out := map[string]any{}
	if c.Metrics == nil {
		return nil
	}
	for _, ne := range goal.NonEvaluable {
		var crit *Criterion
		for i := range goal.Criteria {
			if goal.Criteria[i].ID == ne.Criterion {
				crit = &goal.Criteria[i]
			}
		}
		if crit == nil {
			continue
		}
		vals := map[string]any{}
		for _, ind := range crit.Indicators {
			if m, ok := c.Metrics.Values[ind]; ok {
				vals[ind] = m.Value
			} else if v, ok := c.Metrics.Effective["process."+ind]; ok {
				vals[ind] = v
			}
		}
		if len(vals) > 0 {
			out[crit.ID] = vals
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func rankings(goal *Goal, cands []CompareCandidate) []Ranking {
	out := []Ranking{}
	for _, crit := range goal.Criteria {
		if crit.Status != "evaluable" || crit.Metric == "" {
			continue
		}
		better := "lower"
		if crit.Metric == "safety_factor" {
			better = "higher"
		}
		r := Ranking{Criterion: crit.ID, Label: crit.Label, Metric: crit.Metric, Better: better, Basis: LabelSlicer, Order: []RankEntry{}}
		if crit.Metric == "safety_factor" {
			r.Basis = LabelEngineering
		}
		for _, c := range cands {
			if c.State != "succeeded" {
				continue
			}
			if v, ok := metricValue(c, crit.Metric); ok {
				r.Order = append(r.Order, RankEntry{CandidateID: c.ID, Value: v})
			}
		}
		if len(r.Order) < 2 {
			continue
		}
		sort.SliceStable(r.Order, func(i, j int) bool {
			if better == "higher" {
				return r.Order[i].Value > r.Order[j].Value
			}
			return r.Order[i].Value < r.Order[j].Value
		})
		r.AllTied = true
		for _, e := range r.Order {
			if math.Abs(e.Value-r.Order[0].Value) > 1e-9 {
				r.AllTied = false
			}
		}
		if r.AllTied {
			r.Note = "valeurs identiques : ce critère ne départage pas les candidats"
			if crit.Metric == "safety_factor" {
				r.Note = "le calcul ne modélise que l'orientation, pas les parois ni le remplissage : mêmes valeurs pour une même orientation"
			}
		}
		if crit.Metric == "support_g" {
			r.Note = joinNote(r.Note, "masse de support lue dans le G-code, ordre de grandeur")
		}
		out = append(out, r)
	}
	return out
}
