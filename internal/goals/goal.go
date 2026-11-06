package goals

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/engineering"
)

// ErrInvalid enveloppe les erreurs de validation d'entrée.
var ErrInvalid = errors.New("entrée invalide")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Threshold est un seuil exprimé par l'utilisateur (avant normalisation).
type Threshold struct {
	Op    string  `json:"op" jsonschema:"opérateur de comparaison : inférieur ou égal, supérieur ou égal, égal"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit,omitempty" jsonschema:"s, min, h pour une durée ; g, kg pour une masse"`
}

// NormThreshold est le seuil converti dans l'unité de la métrique.
type NormThreshold struct {
	Metric string  `json:"metric"`
	Op     string  `json:"op"`
	Value  float64 `json:"value"`
	Unit   string  `json:"unit"`
}

// CriterionInput est un critère extrait par le modèle appelant.
type CriterionInput struct {
	ID            string         `json:"id,omitempty" jsonschema:"identifiant libre ; généré si absent"`
	Type          string         `json:"type" jsonschema:"identifiant du catalogue (voir list_goal_criteria) ; un type inconnu est accepté comme custom, non évaluable"`
	Text          string         `json:"text,omitempty" jsonschema:"formulation d'origine de l'utilisateur pour ce critère"`
	Priority      int            `json:"priority,omitempty" jsonschema:"1 (la plus haute) à 5 ; défaut 3"`
	NonNegotiable bool           `json:"non_negotiable,omitempty"`
	Zone          string         `json:"zone,omitempty" jsonschema:"zone de la pièce concernée, texte libre ; les réglages restent globaux"`
	Threshold     *Threshold     `json:"threshold,omitempty"`
	Params        map[string]any `json:"params,omitempty" jsonschema:"paramètres du critère (voir list_goal_criteria), ex. load_direction"`
}

// GoalInput est l'entrée de set_manufacturing_goals.
type GoalInput struct {
	OriginalText string           `json:"original_text" jsonschema:"texte original de la demande, conservé tel quel"`
	ModelID      string           `json:"model_id,omitempty"`
	Material     string           `json:"material,omitempty" jsonschema:"matériau disponible : famille (PLA, PETG...) ou nom de filament"`
	Function     string           `json:"function,omitempty" jsonschema:"fonction de la pièce"`
	Finish       string           `json:"finish,omitempty" jsonschema:"finition recherchée, texte libre"`
	Constraints  []string         `json:"constraints,omitempty" jsonschema:"contraintes de production (machine, délai, budget...)"`
	Criteria     []CriterionInput `json:"criteria"`
}

// Criterion est un critère validé et enrichi.
type Criterion struct {
	CriterionInput
	Known       bool           `json:"known"`
	Label       string         `json:"label"`
	Status      string         `json:"status" jsonschema:"evaluable | non_evaluable"`
	EvaluableBy []string       `json:"evaluable_by,omitempty" jsonschema:"slicer, engineering, geometry"`
	Metric      string         `json:"metric,omitempty"`
	Indicators  []string       `json:"indicators,omitempty" jsonschema:"grandeurs mesurables qui renseignent le critère sans le prouver"`
	Reason      string         `json:"reason,omitempty"`
	Test        string         `json:"test,omitempty" jsonschema:"essai physique à prévoir"`
	Norm        *NormThreshold `json:"normalized_threshold,omitempty"`
}

// Ambiguity est une question dont la réponse change effectivement le choix.
type Ambiguity struct {
	Criterion string   `json:"criterion"`
	Param     string   `json:"param,omitempty"`
	Question  string   `json:"question"`
	Why       string   `json:"why"`
	Options   []string `json:"options,omitempty"`
}

// NonEvaluable signale un critère qui ne peut être jugé que par un essai physique.
type NonEvaluable struct {
	Criterion string `json:"criterion"`
	Label     string `json:"label"`
	Reason    string `json:"reason"`
	Test      string `json:"test,omitempty"`
}

// Conflict est une opposition entre deux critères, avec compromis proposés.
type Conflict struct {
	A             string   `json:"a"`
	B             string   `json:"b"`
	TypeA         string   `json:"type_a"`
	TypeB         string   `json:"type_b"`
	Why           string   `json:"why"`
	Proposals     []string `json:"proposals"`
	NeedsPriority bool     `json:"needs_priority" jsonschema:"vrai si la priorité entre les deux change le choix et n'est pas tranchée"`
	Note          string   `json:"note,omitempty"`
}

// Goal est un objectif structuré enregistré.
type Goal struct {
	ID           string         `json:"id"`
	CreatedAt    time.Time      `json:"created_at"`
	RulesVersion string         `json:"rules_version"`
	OriginalText string         `json:"original_text"`
	ModelID      string         `json:"model_id,omitempty"`
	Material     string         `json:"material,omitempty"`
	MaterialFam  string         `json:"material_family,omitempty" jsonschema:"famille reconnue ; vide si inconnue"`
	Function     string         `json:"function,omitempty"`
	Finish       string         `json:"finish,omitempty"`
	Constraints  []string       `json:"constraints,omitempty"`
	Criteria     []Criterion    `json:"criteria"`
	Ambiguities  []Ambiguity    `json:"ambiguities"`
	NonEvaluable []NonEvaluable `json:"non_evaluable"`
	Conflicts    []Conflict     `json:"conflicts"`
	Warnings     []string       `json:"warnings,omitempty"`
}

var idRe = regexp.MustCompile(`[^a-z0-9_]+`)

func normType(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("-", "_", " ", "_").Replace(s)
	return idRe.ReplaceAllString(s, "")
}

// BeamSpec est le calcul de poutre optionnel d'un critère de résistance.
type BeamSpec struct {
	Support  string  `json:"support"`
	LoadType string  `json:"load_type"`
	Load     float64 `json:"load"`
	Length   float64 `json:"length_mm"`
	Section  struct {
		Shape string  `json:"shape"`
		B     float64 `json:"b,omitempty"`
		H     float64 `json:"h,omitempty"`
		D     float64 `json:"d,omitempty"`
		Do    float64 `json:"do,omitempty"`
		Di    float64 `json:"di,omitempty"`
	} `json:"section"`
	Material string `json:"material,omitempty"`
}

func (b BeamSpec) params(material, orientation string) engineering.BeamParams {
	m := b.Material
	if m == "" {
		m = material
	}
	return engineering.BeamParams{
		Support: b.Support, LoadType: b.LoadType, Load: b.Load, Length: b.Length,
		Section:  engineering.Section{Shape: b.Section.Shape, B: b.Section.B, H: b.Section.H, D: b.Section.D, Do: b.Section.Do, Di: b.Section.Di},
		Material: m, Orientation: orientation,
	}
}

// Beam extrait le calcul de poutre d'un critère (nil si absent).
func (c Criterion) Beam() (*BeamSpec, error) {
	raw, ok := c.Params["engineering"]
	if !ok || raw == nil {
		return nil, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, invalid("params.engineering illisible : %v", err)
	}
	var spec BeamSpec
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		return nil, invalid("params.engineering : %v", err)
	}
	return &spec, nil
}

// ParamString lit un paramètre texte.
func (c Criterion) ParamString(k string) string {
	if v, ok := c.Params[k]; ok {
		if s, ok := v.(string); ok {
			return strings.ToLower(strings.TrimSpace(s))
		}
	}
	return ""
}

// LoadAxis lit load_axis (vecteur de 3 nombres).
func (c Criterion) LoadAxis() ([3]float64, bool) {
	v, ok := c.Params["load_axis"]
	if !ok {
		return [3]float64{}, false
	}
	arr, ok := v.([]any)
	if !ok || len(arr) != 3 {
		if fl, ok2 := v.([]float64); ok2 && len(fl) == 3 {
			return [3]float64{fl[0], fl[1], fl[2]}, true
		}
		return [3]float64{}, false
	}
	var out [3]float64
	for i, e := range arr {
		f, ok := e.(float64)
		if !ok {
			return [3]float64{}, false
		}
		out[i] = f
	}
	return out, true
}

// BuildGoal valide l'entrée et construit l'objectif structuré (sans l'enregistrer).
func BuildGoal(r *Rules, in GoalInput) (*Goal, error) {
	if strings.TrimSpace(in.OriginalText) == "" {
		return nil, invalid("original_text est requis (texte original de la demande)")
	}
	if len(in.Criteria) == 0 {
		return nil, invalid("au moins un critère structuré est requis (voir list_goal_criteria) ; extraire les critères du texte puis les passer dans criteria")
	}
	g := &Goal{
		RulesVersion: r.Version, OriginalText: in.OriginalText, ModelID: in.ModelID,
		Material: strings.TrimSpace(in.Material), Function: in.Function, Finish: in.Finish,
		Constraints: in.Constraints, Ambiguities: []Ambiguity{}, NonEvaluable: []NonEvaluable{}, Conflicts: []Conflict{},
	}
	if g.Material != "" {
		if fam := strings.ToUpper(g.Material); r.KnownFamily(fam) {
			g.MaterialFam = fam
		} else {
			g.Warnings = append(g.Warnings, fmt.Sprintf("matériau %q : famille non reconnue par les règles (%s). Seuls les leviers valables pour toute famille seront proposés ; aucun paramètre d'un autre matériau ne lui sera appliqué.", g.Material, strings.Join(r.Families, ", ")))
		}
	}
	ids := map[string]bool{}
	for i, ci := range in.Criteria {
		c, err := buildCriterion(r, ci, g.MaterialFam, i)
		if err != nil {
			return nil, err
		}
		if ids[c.ID] {
			return nil, invalid("identifiant de critère en double : %q", c.ID)
		}
		ids[c.ID] = true
		g.Criteria = append(g.Criteria, *c)
	}
	g.finish(r)
	return g, nil
}

func buildCriterion(r *Rules, ci CriterionInput, family string, idx int) (*Criterion, error) {
	typ := normType(ci.Type)
	if typ == "" {
		return nil, invalid("critère %d : type requis", idx+1)
	}
	c := &Criterion{CriterionInput: ci}
	c.Type = typ
	if c.Priority == 0 {
		c.Priority = 3
	}
	if c.Priority < 1 || c.Priority > 5 {
		return nil, invalid("critère %q : priorité %d hors de 1..5", typ, c.Priority)
	}
	if c.ID == "" {
		c.ID = fmt.Sprintf("c%d_%s", idx+1, typ)
	}
	rule := r.Criteria[typ]
	if rule == nil {
		// Critère inconnu : accepté en custom, non évaluable, signalé.
		if strings.TrimSpace(ci.Text) == "" {
			return nil, invalid("critère inconnu %q : fournir text (formulation d'origine) pour l'enregistrer comme custom", typ)
		}
		c.Known, c.Label = false, "Critère personnalisé"
		c.Status = "non_evaluable"
		c.Reason = fmt.Sprintf("type %q absent du catalogue : enregistré comme custom, aucun réglage associé, à vérifier par un essai physique à définir", typ)
		c.Test = "À définir avec l'utilisateur d'après la formulation d'origine."
		c.Type = "custom"
		c.Params = mergeParams(c.Params, map[string]any{"original_type": typ})
		return c, nil
	}
	c.Known, c.Label = true, rule.Label
	for k, v := range c.Params {
		if k == "original_type" {
			continue
		}
		sp, ok := rule.Params[k]
		if !ok {
			continue // paramètre libre toléré, jamais interprété
		}
		if len(sp.Values) > 0 {
			s, _ := v.(string)
			if !contains(sp.Values, strings.ToLower(strings.TrimSpace(s))) {
				return nil, invalid("critère %q : params.%s=%v hors de %v", typ, k, v, sp.Values)
			}
		}
	}
	c.Status = rule.Evaluation.Status
	c.EvaluableBy = rule.Evaluation.By
	c.Metric = rule.Evaluation.Metric
	c.Indicators = rule.Evaluation.Indicators
	c.Test = rule.Test
	if len(rule.EvaluableIf) > 0 {
		// Évaluable seulement si les paramètres de calcul sont fournis et valides.
		ok := true
		for _, p := range rule.EvaluableIf {
			if _, has := c.Params[p]; !has {
				ok = false
			}
		}
		if ok && typ == "strength" {
			spec, err := c.Beam()
			if err != nil {
				return nil, err
			}
			mat := family
			if _, err := engineering.BeamStress(spec.params(mat, "xy")); err != nil {
				return nil, invalid("critère strength : calcul de poutre impossible : %v", err)
			}
			c.Status = "evaluable"
			c.Metric = "safety_factor"
			c.Reason = "évalué par calcul de poutre (hypothèses et niveau de confiance affichés avec le résultat) ; le remplissage n'est pas modélisé"
		} else {
			c.Status = "non_evaluable"
			c.Reason = "paramètres de calcul absents (params.engineering) : seul un essai physique permet de juger"
		}
	}
	if c.Status == "non_evaluable" && c.Reason == "" {
		c.Reason = "propriété physique : ni le slicer ni la géométrie ne peuvent la prouver"
	}
	if th := ci.Threshold; th != nil {
		n, err := normalizeThreshold(rule, th)
		if err != nil {
			return nil, invalid("critère %q : %v", typ, err)
		}
		c.Norm = n
	}
	return c, nil
}

func mergeParams(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func normalizeThreshold(rule *CriterionRule, th *Threshold) (*NormThreshold, error) {
	switch th.Op {
	case "<=", ">=", "==":
	default:
		return nil, fmt.Errorf("seuil : opérateur %q inconnu (<=, >=, ==)", th.Op)
	}
	if rule.Evaluation.Status != "evaluable" || rule.Evaluation.Metric == "" {
		// Seuil conservé tel quel, non vérifiable automatiquement.
		return nil, nil
	}
	n := &NormThreshold{Metric: rule.Evaluation.Metric, Op: th.Op}
	u := strings.ToLower(strings.TrimSpace(th.Unit))
	switch rule.Evaluation.Metric {
	case "print_time_s":
		switch u {
		case "", "s":
			n.Value = th.Value
		case "min":
			n.Value = th.Value * 60
		case "h":
			n.Value = th.Value * 3600
		default:
			return nil, fmt.Errorf("seuil de durée : unité %q inconnue (s, min, h)", th.Unit)
		}
		n.Unit = "s"
	case "filament_g", "support_g":
		switch u {
		case "", "g":
			n.Value = th.Value
		case "kg":
			n.Value = th.Value * 1000
		default:
			return nil, fmt.Errorf("seuil de masse : unité %q inconnue (g, kg)", th.Unit)
		}
		n.Unit = "g"
	case "safety_factor":
		n.Value, n.Unit = th.Value, ""
	default:
		return nil, nil
	}
	if n.Value < 0 {
		return nil, errors.New("seuil négatif")
	}
	return n, nil
}

// finish calcule ambiguïtés, éléments non évaluables et conflits.
func (g *Goal) finish(r *Rules) {
	for _, c := range g.Criteria {
		rule := r.Criteria[c.Type]
		if rule != nil {
			for name, sp := range rule.Params {
				if !sp.Required {
					continue
				}
				v := c.ParamString(name)
				if v == "" || v == "unknown" {
					g.Ambiguities = append(g.Ambiguities, Ambiguity{
						Criterion: c.ID, Param: name,
						Question: fmt.Sprintf("%s : %s ?", rule.Label, sp.Description),
						Why:      ambiguityWhy(c.Type, name),
						Options:  sp.Values,
					})
				}
			}
			if len(rule.Levers) == 0 {
				g.Warnings = append(g.Warnings, fmt.Sprintf("%s : %s", rule.Label, rule.NoLeversReason))
			}
		}
		if c.Zone != "" {
			g.Warnings = append(g.Warnings, fmt.Sprintf("%s : zone « %s » notée, mais les réglages générés sont globaux à l'objet ; une variante par zone doit être construite sur la géométrie (non générée automatiquement).", c.Label, c.Zone))
		}
		if c.Status == "non_evaluable" {
			g.NonEvaluable = append(g.NonEvaluable, NonEvaluable{Criterion: c.ID, Label: c.Label, Reason: c.Reason, Test: c.Test})
			if c.NonNegotiable {
				g.Warnings = append(g.Warnings, fmt.Sprintf("%s est non négociable mais non évaluable : seul un essai physique pourra la valider.", c.Label))
			}
		}
	}
	// Conflits connus entre critères présents.
	for _, cf := range r.Conflicts {
		for _, a := range g.Criteria {
			if a.Type != cf.A {
				continue
			}
			for _, b := range g.Criteria {
				if b.Type != cf.B {
					continue
				}
				c := Conflict{A: a.ID, B: b.ID, TypeA: a.Type, TypeB: b.Type, Why: cf.Why, Proposals: cf.Proposals}
				switch {
				case a.NonNegotiable && b.NonNegotiable:
					c.Note = "Les deux sont non négociables : elles ne pourront pas être garanties ensemble avec certitude ; les estimations du slicer diront ce qui est atteignable."
				case a.Priority == b.Priority && !a.NonNegotiable && !b.NonNegotiable:
					c.NeedsPriority = true
					c.Note = "Priorités égales : préciser laquelle prime pour choisir entre les variantes."
					g.Ambiguities = append(g.Ambiguities, Ambiguity{
						Criterion: a.ID,
						Question:  fmt.Sprintf("%s et %s s'opposent : laquelle prime ?", a.Label, b.Label),
						Why:       "la priorité décide quelle variante est favorisée ; sans réponse, une variante de compromis est proposée",
						Options:   []string{a.ID, b.ID},
					})
				default:
					win := a
					if b.NonNegotiable || b.Priority < a.Priority {
						win = b
					}
					c.Note = fmt.Sprintf("Priorité à %s ; l'autre est traitée en compromis.", win.Label)
				}
				g.Conflicts = append(g.Conflicts, c)
			}
		}
	}
	sort.SliceStable(g.Ambiguities, func(i, j int) bool { return g.Ambiguities[i].Criterion < g.Ambiguities[j].Criterion })
}

func ambiguityWhy(typ, param string) string {
	switch {
	case typ == "strength" && param == "load_direction":
		return "la direction de l'effort par rapport aux couches change l'orientation et les réglages (rupture inter-couches)"
	case typ == "transparency" && param == "mode":
		return "vision nette et diffusion demandent des réglages opposés"
	}
	return "la réponse change les réglages proposés"
}

// CriterionInfo décrit un critère du catalogue pour guider le modèle.
type CriterionInfo struct {
	ID            string               `json:"id"`
	Label         string               `json:"label"`
	Description   string               `json:"description"`
	Params        map[string]ParamSpec `json:"params"`
	Evaluation    Evaluation           `json:"evaluation"`
	Levers        []LeverInfo          `json:"levers"`
	NoLevers      string               `json:"no_levers_reason,omitempty"`
	Test          string               `json:"test,omitempty"`
	ConflictsWith []string             `json:"conflicts_with,omitempty"`
}

// LeverInfo résume un levier.
type LeverInfo struct {
	ID       string   `json:"id"`
	Key      string   `json:"key,omitempty"`
	Kind     string   `json:"kind"`
	Families []string `json:"families"`
	Effect   string   `json:"effect"`
	Tradeoff string   `json:"tradeoff"`
}

// Catalog renvoie le catalogue des critères connus.
func (r *Rules) Catalog() []CriterionInfo {
	var out []CriterionInfo
	for _, id := range r.CriterionIDs() {
		c := r.Criteria[id]
		ci := CriterionInfo{ID: id, Label: c.Label, Description: c.Description, Params: c.Params, Evaluation: c.Evaluation, NoLevers: c.NoLeversReason, Test: c.Test}
		if ci.Params == nil {
			ci.Params = map[string]ParamSpec{}
		}
		for _, l := range c.Levers {
			k := "setting"
			if l.Kind != "" {
				k = l.Kind
			}
			ci.Levers = append(ci.Levers, LeverInfo{ID: l.ID, Key: l.Key, Kind: k, Families: l.Families, Effect: l.Effect, Tradeoff: l.Tradeoff})
		}
		for _, cf := range r.Conflicts {
			if cf.A == id {
				ci.ConflictsWith = append(ci.ConflictsWith, cf.B)
			} else if cf.B == id {
				ci.ConflictsWith = append(ci.ConflictsWith, cf.A)
			}
		}
		out = append(out, ci)
	}
	return out
}

// ensure contracts import is used for documentation of relation with legacy type.
var _ = contracts.OpAtMost
