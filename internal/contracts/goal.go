package contracts

import "strings"

// CriterionOp est l'opérateur d'un critère.
type CriterionOp string

const (
	OpAtMost   CriterionOp = "<="
	OpAtLeast  CriterionOp = ">="
	OpEqual    CriterionOp = "=="
	OpMinimize CriterionOp = "min"
	OpMaximize CriterionOp = "max"
	// OpQualitative : non mesurable, à arbitrer par l'utilisateur.
	OpQualitative CriterionOp = "qualitative"
)

// Criterion est un critère structuré issu d'un objectif en langage naturel.
type Criterion struct {
	Metric string      `json:"metric" jsonschema:"grandeur mesurable, ex. print_time_s, filament_g, max_overhang_deg"`
	Op     CriterionOp `json:"op"`
	Target *float64    `json:"target,omitempty"`
	Unit   string      `json:"unit,omitempty"`
	// Hard : contrainte éliminatoire plutôt que préférence.
	Hard   bool    `json:"hard,omitempty"`
	Weight float64 `json:"weight,omitempty"`
	Note   string  `json:"note,omitempty"`
}

// Evaluable : une métrique est nommée et l'opérateur est mesurable, avec cible si requise.
func (c Criterion) Evaluable() bool {
	if strings.TrimSpace(c.Metric) == "" {
		return false
	}
	switch c.Op {
	case OpMinimize, OpMaximize:
		return true
	case OpAtMost, OpAtLeast, OpEqual:
		return c.Target != nil
	}
	return false
}

// ManufacturingGoal conserve la demande d'origine et sa traduction structurée.
type ManufacturingGoal struct {
	ID           string      `json:"id"`
	OriginalText string      `json:"original_text"`
	Criteria     []Criterion `json:"criteria"`
	Priority     int         `json:"priority" jsonschema:"1 = la plus haute"`
	// Evaluable est dérivé : tous les critères sont mesurables (voir RecomputeEvaluable).
	Evaluable   bool     `json:"evaluable"`
	Ambiguities []string `json:"ambiguities,omitempty"`
}

// RecomputeEvaluable met à jour Evaluable ; faux si aucun critère.
func (g *ManufacturingGoal) RecomputeEvaluable() bool {
	ok := len(g.Criteria) > 0
	for _, c := range g.Criteria {
		if !c.Evaluable() {
			ok = false
			break
		}
	}
	g.Evaluable = ok
	return ok
}
