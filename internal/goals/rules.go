// Package goals traduit des objectifs de fabrication en candidats de réglage
// bornés, les fait trancher par Bambu Studio, compare les résultats mesurables
// et garde la mémoire des essais réels.
//
// Principes (dossier §8) : l'interprétation du texte libre est faite par le
// modèle appelant (qui fournit des critères structurés) ; le service valide,
// signale les ambiguïtés et ce qui n'est pas évaluable. Aucun gagnant
// automatique : un classement par critère seulement quand le critère est
// mesurable, jamais de score global.
package goals

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
)

//go:embed data/rules.json
var rulesJSON []byte

// Rules est le jeu de règles versionné.
type Rules struct {
	Version    string                    `json:"version"`
	Note       string                    `json:"note"`
	Provenance string                    `json:"provenance"`
	Families   []string                  `json:"families"`
	FamilyNote string                    `json:"family_notes"`
	Criteria   map[string]*CriterionRule `json:"criteria"`
	Conflicts  []ConflictRule            `json:"conflicts"`
}

// ParamSpec décrit un paramètre d'un critère.
type ParamSpec struct {
	Values      []string `json:"values,omitempty"`
	Type        string   `json:"type,omitempty"`
	Required    bool     `json:"required"`
	Description string   `json:"description"`
}

// Evaluation dit par quoi un critère est évaluable.
type Evaluation struct {
	Status     string   `json:"status"` // evaluable | non_evaluable
	By         []string `json:"by"`     // slicer | engineering | geometry
	Metric     string   `json:"metric,omitempty"`
	Indicators []string `json:"indicators,omitempty"`
}

// When conditionne un levier à la valeur d'un paramètre du critère.
type When struct {
	Param string   `json:"param"`
	In    []string `json:"in,omitempty"`
	NotIn []string `json:"not_in,omitempty"`
}

// Lever est un levier de réglage ou d'orientation.
type Lever struct {
	ID     string   `json:"id"`
	Kind   string   `json:"kind,omitempty"`   // "" (réglage) | orientation
	Mode   string   `json:"mode,omitempty"`   // orientation : load_in_plane | max_contact | min_overhang
	Key    string   `json:"key,omitempty"`    // clé réelle du profil Bambu Studio
	Target string   `json:"target,omitempty"` // process | filament | machine
	Op     string   `json:"op,omitempty"`     // set | add | scale
	Value  any      `json:"value,omitempty"`
	Min    *float64 `json:"min,omitempty"`
	Max    *float64 `json:"max,omitempty"`
	// MaxNozzleFraction : borne haute = fraction du diamètre de buse (hauteur de couche).
	MaxNozzleFraction float64 `json:"max_nozzle_fraction,omitempty"`
	// BoundKey : clé du profil (même cible) donnant une borne haute propre au matériau.
	BoundKey string   `json:"bound_key,omitempty"`
	Families []string `json:"families"`
	When     *When    `json:"when,omitempty"`
	Effect   string   `json:"effect"`
	Tradeoff string   `json:"tradeoff"`
}

// CriterionRule est la règle d'un critère connu.
type CriterionRule struct {
	ID             string               `json:"id"`
	Label          string               `json:"label"`
	Description    string               `json:"description"`
	Params         map[string]ParamSpec `json:"params"`
	EvaluableIf    []string             `json:"evaluable_if_params,omitempty"`
	Evaluation     Evaluation           `json:"evaluation"`
	Test           string               `json:"test,omitempty"`
	NoLeversReason string               `json:"no_levers_reason,omitempty"`
	Levers         []Lever              `json:"levers"`
}

// ConflictRule décrit une opposition connue entre deux critères.
type ConflictRule struct {
	A         string   `json:"a"`
	B         string   `json:"b"`
	Why       string   `json:"why"`
	Proposals []string `json:"proposals"`
}

var (
	rulesOnce sync.Once
	rules     *Rules
	rulesErr  error
)

// LoadRules charge (une fois) les règles embarquées.
func LoadRules() (*Rules, error) {
	rulesOnce.Do(func() {
		var r Rules
		if err := json.Unmarshal(rulesJSON, &r); err != nil {
			rulesErr = fmt.Errorf("goals: rules.json invalide : %w", err)
			return
		}
		for id, c := range r.Criteria {
			c.ID = id
			if err := c.validate(); err != nil {
				rulesErr = fmt.Errorf("goals: règle %s : %w", id, err)
				return
			}
		}
		for _, cf := range r.Conflicts {
			if r.Criteria[cf.A] == nil || r.Criteria[cf.B] == nil {
				rulesErr = fmt.Errorf("goals: conflit %s/%s : critère inconnu", cf.A, cf.B)
				return
			}
		}
		rules = &r
	})
	return rules, rulesErr
}

func (c *CriterionRule) validate() error {
	seen := map[string]bool{}
	for _, l := range c.Levers {
		if l.ID == "" || seen[l.ID] {
			return fmt.Errorf("levier sans identifiant ou en double (%q)", l.ID)
		}
		seen[l.ID] = true
		if len(l.Families) == 0 {
			return fmt.Errorf("levier %s : familles de matériaux requises", l.ID)
		}
		switch l.Kind {
		case "orientation":
			switch l.Mode {
			case "load_in_plane", "max_contact", "min_overhang":
			default:
				return fmt.Errorf("levier %s : mode d'orientation %q inconnu", l.ID, l.Mode)
			}
			continue
		case "":
		default:
			return fmt.Errorf("levier %s : type %q inconnu", l.ID, l.Kind)
		}
		if l.Key == "" || l.Target == "" {
			return fmt.Errorf("levier %s : clé et cible requises", l.ID)
		}
		switch l.Op {
		case "set", "add", "scale":
		default:
			return fmt.Errorf("levier %s : opération %q inconnue", l.ID, l.Op)
		}
		if l.Value == nil {
			return fmt.Errorf("levier %s : valeur requise", l.ID)
		}
		if l.When != nil {
			sp, ok := c.Params[l.When.Param]
			if !ok {
				return fmt.Errorf("levier %s : paramètre %q inconnu", l.ID, l.When.Param)
			}
			for _, v := range append(append([]string{}, l.When.In...), l.When.NotIn...) {
				if len(sp.Values) > 0 && !contains(sp.Values, v) {
					return fmt.Errorf("levier %s : valeur %q hors de la liste du paramètre %s", l.ID, v, l.When.Param)
				}
			}
		}
	}
	return nil
}

// CriterionIDs liste les critères connus, triés.
func (r *Rules) CriterionIDs() []string {
	ids := make([]string, 0, len(r.Criteria))
	for id := range r.Criteria {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// KnownFamily dit si la famille a des leviers validés.
func (r *Rules) KnownFamily(f string) bool { return contains(r.Families, strings.ToUpper(f)) }

// LeverKeys renvoie toutes les clés de profil utilisées par les règles, par cible.
func (r *Rules) LeverKeys() map[string][]string {
	out := map[string][]string{}
	for _, c := range r.Criteria {
		for _, l := range c.Levers {
			if l.Key != "" {
				out[l.Target] = appendUnique(out[l.Target], l.Key)
				if l.BoundKey != "" {
					out[l.Target] = appendUnique(out[l.Target], l.BoundKey)
				}
			}
		}
	}
	for t := range out {
		sort.Strings(out[t])
	}
	return out
}

// CheckKeys vérifie que chaque clé de levier existe dans les profils résolus
// donnés (process, filament). Renvoie les clés absentes ("cible:clé").
func (r *Rules) CheckKeys(process, filament *profiles.Resolved) []string {
	var missing []string
	for t, keys := range r.LeverKeys() {
		var cfg *profiles.Resolved
		switch t {
		case "process":
			cfg = process
		case "filament":
			cfg = filament
		default:
			continue
		}
		for _, k := range keys {
			if _, ok := cfg.Config[k]; !ok {
				missing = append(missing, t+":"+k)
			}
		}
	}
	sort.Strings(missing)
	return missing
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func appendUnique(l []string, s string) []string {
	if contains(l, s) {
		return l
	}
	return append(l, s)
}
