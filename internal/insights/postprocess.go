package insights

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/engineering"
)

//go:embed data/postprocess.json
var postprocessJSON []byte

type ppFamily struct {
	Applies    string            `json:"applies"`
	Reason     string            `json:"reason"`
	Steps      []string          `json:"steps"`
	Parameters map[string]string `json:"parameters"`
	Hazards    []string          `json:"hazards"`
	Notes      []string          `json:"notes"`
}

type ppTechnique struct {
	ID       string              `json:"id"`
	Name     string              `json:"name"`
	Order    int                 `json:"order"`
	Triggers []string            `json:"triggers"`
	ByFamily map[string]ppFamily `json:"by_family"`
	Common   struct {
		Steps   []string `json:"steps"`
		Hazards []string `json:"hazards"`
		Notes   []string `json:"notes"`
	} `json:"common"`
}

type ppUsageRule struct {
	Match   []string `json:"match"`
	Warning string   `json:"warning"`
}

type ppData struct {
	Version        string                `json:"version"`
	Level          string                `json:"level"`
	Note           string                `json:"note"`
	SourceStatus   string                `json:"source_status"`
	Sources        map[string]RuleSource `json:"sources"`
	FinishKeywords map[string][]string   `json:"finish_keywords"`
	UsageRules     []ppUsageRule         `json:"usage_rules"`
	GeneralWarns   []string              `json:"general_warnings"`
	NoPost         map[string]string     `json:"families_without_post_processing"`
	Techniques     []ppTechnique         `json:"techniques"`
	Honesty        map[string]string     `json:"honesty"`
}

var (
	ppOnce sync.Once
	ppVal  *ppData
)

func loadPP() *ppData {
	ppOnce.Do(func() {
		var d ppData
		if err := json.Unmarshal(postprocessJSON, &d); err != nil {
			panic("insights/data/postprocess.json : " + err.Error())
		}
		sort.SliceStable(d.Techniques, func(i, j int) bool { return d.Techniques[i].Order < d.Techniques[j].Order })
		ppVal = &d
	})
	return ppVal
}

// PostProcessInput décrit la pièce à finir.
type PostProcessInput struct {
	Material    string   `json:"material" jsonschema:"matière (PLA, PETG, ABS, ASA, TPU, PC, PA, PLA-CF, PETG-CF, PA-CF...) ou nom du filament"`
	Finish      string   `json:"finish,omitempty" jsonschema:"finition voulue en mots libres : lisse, mat, peinte, brillante/vernis/résine, transparente, recuit, collage, inserts filetés..."`
	Geometry    string   `json:"geometry,omitempty" jsonschema:"particularités de forme : parois fines, détails fins, cavités, grandes faces planes..."`
	Usage       string   `json:"usage,omitempty" jsonschema:"usage : extérieur, chaleur, charge, tolérances serrées, flexible, contact alimentaire..."`
	InsertSizes []string `json:"insert_sizes,omitempty" jsonschema:"tailles d'inserts thermo-sertis demandées (M2, M2.5, M3, M4, M5, M6, M8)"`
}

// AdviceStep est une étape ordonnée.
type AdviceStep struct {
	Order      int               `json:"order"`
	Technique  string            `json:"technique"`
	Title      string            `json:"title"`
	Applies    string            `json:"applies" jsonschema:"yes | limited (résultat partiel ou à tester)"`
	Steps      []string          `json:"steps"`
	Parameters map[string]string `json:"parameters,omitempty"`
	Hazards    []string          `json:"hazards,omitempty"`
	Notes      []string          `json:"notes,omitempty"`
}

// Rejected est une technique que la matière ne permet pas (ou pas de façon sûre), avec la raison.
type Rejected struct {
	Technique string `json:"technique"`
	Title     string `json:"title"`
	Reason    string `json:"reason"`
}

// InsertSpec donne les dimensions d'un insert thermo-serti (internal/engineering ScrewBoss).
type InsertSpec struct {
	Size             string   `json:"size"`
	HoleDiameterMM   float64  `json:"hole_diameter_mm"`
	HoleDepthMM      float64  `json:"hole_depth_mm"`
	TypicalInsertLen float64  `json:"typical_insert_length_mm"`
	BossOuterMM      float64  `json:"boss_outer_diameter_mm"`
	MinWallMM        float64  `json:"min_wall_around_hole_mm"`
	TipStartTemp     string   `json:"soldering_iron_start_temperature,omitempty"`
	Level            string   `json:"level"`
	Warnings         []string `json:"warnings,omitempty"`
}

// PostProcessAdvice est la réponse de post_processing_advice.
type PostProcessAdvice struct {
	Material       string       `json:"material"`
	Family         string       `json:"family"`
	Goals          []string     `json:"goals_recognized" jsonschema:"finitions reconnues dans votre demande : smooth, matte, paint, gloss, clear, anneal, bond, inserts"`
	Steps          []AdviceStep `json:"steps"`
	NotRecommended []Rejected   `json:"not_recommended,omitempty"`
	Inserts        []InsertSpec `json:"inserts,omitempty"`
	Warnings       []string     `json:"warnings"`
	Honesty        []string     `json:"honesty,omitempty"`
	Sources        []RuleSource `json:"sources"`
	Level          string       `json:"level"`
	DataVersion    string       `json:"data_version"`
}

func foldAccents(s string) string {
	r := strings.NewReplacer("é", "e", "è", "e", "ê", "e", "ë", "e", "à", "a", "â", "a", "ä", "a", "î", "i", "ï", "i", "ô", "o", "ö", "o",
		"ù", "u", "û", "u", "ü", "u", "ç", "c", "œ", "oe")
	return r.Replace(strings.ToLower(s))
}

func tokens(s string) []string {
	return strings.FieldsFunc(foldAccents(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

func kwMatch(toks []string, kws []string) bool {
	for _, t := range toks {
		for _, k := range kws {
			if t == k || len(k) >= 5 && strings.HasPrefix(t, k) {
				return true
			}
		}
	}
	return false
}

func recognizeGoals(finish string) []string {
	toks := tokens(finish)
	var out []string
	for _, g := range []string{"smooth", "matte", "paint", "gloss", "clear", "anneal", "bond", "inserts"} {
		if kwMatch(toks, loadPP().FinishKeywords[g]) {
			out = append(out, g)
		}
	}
	return out
}

func pickFamily(t ppTechnique, fam string) (ppFamily, bool) {
	if f, ok := t.ByFamily[fam]; ok {
		return f, true
	}
	if f, ok := t.ByFamily[baseFamily(fam)]; ok {
		return f, true
	}
	f, ok := t.ByFamily["default"]
	return f, ok
}

// engineeringMaterial associe une famille à une fiche du catalogue d'ingénierie ("" si aucune).
func engineeringMaterial(fam string) string {
	switch fam {
	case "PLA", "PETG", "ABS", "ASA", "PC", "PA", "PLA-CF", "PETG-CF", "PA-CF", "PVA":
		return fam
	case "TPU":
		return "TPU-95A"
	}
	return ""
}

func hasGoal(goals []string, g string) bool {
	for _, x := range goals {
		if x == g {
			return true
		}
	}
	return false
}

// PostProcessingAdvice compose les conseils de finition pour une matière, une finition et un usage.
func PostProcessingAdvice(in PostProcessInput) (*PostProcessAdvice, error) {
	if strings.TrimSpace(in.Material) == "" {
		return nil, invalid("material requis (ex. PLA, PETG, ABS, ASA, TPU, PC, PA)")
	}
	fam := MaterialFamily(in.Material)
	if fam == "" {
		return nil, invalid("matière %q non reconnue (PLA, PETG, ABS, ASA, TPU, PC, PA, PET et variantes -CF)", in.Material)
	}
	pp := loadPP()
	if reason, ok := pp.NoPost[fam]; ok {
		return nil, invalid("%s : %s", fam, reason)
	}
	goals := recognizeGoals(in.Finish)
	out := &PostProcessAdvice{Material: in.Material, Family: fam, Goals: goals, Steps: []AdviceStep{}, Level: pp.Level, DataVersion: pp.Version}
	if len(goals) == 0 {
		goals = []string{"smooth"}
		out.Warnings = append(out.Warnings, "Finition non reconnue ou non précisée : conseils pour une surface lisse proposés par défaut. Préciser finish (lisse, mat, peinte, brillante, transparente, recuit, collage, inserts).")
	}
	used := map[string]bool{}
	for _, t := range pp.Techniques {
		trig := false
		for _, tr := range t.Triggers {
			if hasGoal(goals, tr) {
				trig = true
			}
		}
		if !trig {
			continue
		}
		f, ok := pickFamily(t, fam)
		if !ok {
			continue
		}
		if f.Applies == "no" {
			out.NotRecommended = append(out.NotRecommended, Rejected{Technique: t.ID, Title: t.Name, Reason: f.Reason})
			continue
		}
		// la peinture et le vernis sur une pièce « transparente » n'ont pas de sens : la finition claire a sa propre étape
		if t.ID == "paint" && hasGoal(goals, "clear") && !hasGoal(goals, "paint") {
			continue
		}
		used[t.ID] = true
		st := AdviceStep{Technique: t.ID, Title: t.Name, Applies: f.Applies, Steps: append(append([]string{}, f.Steps...), t.Common.Steps...),
			Parameters: f.Parameters, Hazards: append(append([]string{}, f.Hazards...), t.Common.Hazards...), Notes: append(append([]string{}, f.Notes...), t.Common.Notes...)}
		if t.ID == "annealing" {
			if m := engineeringMaterial(fam); m != "" {
				if mat, err := engineering.GetMaterial(m); err == nil {
					if p, ok := mat.Properties["hdt_0_45mpa"]; ok && p.Value != nil {
						st.Notes = append(st.Notes, fmt.Sprintf("Tenue thermique de référence avant recuit (fiche fabricant, %s) : fléchissement sous charge (0,45 MPa) %.0f °C. Valeur de fiche pour l'état imprimé non recuit ; le gain d'un recuit n'est pas garanti.", mat.Name, *p.Value))
					}
				}
			}
		}
		out.Steps = append(out.Steps, st)
	}
	for i := range out.Steps {
		out.Steps[i].Order = i + 1
	}

	if used["heatset_inserts"] {
		sizes := in.InsertSizes
		if len(sizes) == 0 {
			sizes = []string{"M3", "M4"}
			out.Warnings = append(out.Warnings, "Aucune taille d'insert indiquée : dimensions données pour M3 et M4 en exemple (insert_sizes pour les autres, M2 à M8).")
		}
		t := pp.techniqueByID("heatset_inserts")
		tf, _ := pickFamily(*t, fam)
		for _, sz := range sizes {
			r, err := engineering.ScrewBoss(sz, engineering.ScrewHeatSet, engineeringMaterial(fam))
			if err != nil {
				out.Warnings = append(out.Warnings, fmt.Sprintf("Insert %s : %v", sz, err))
				continue
			}
			out.Inserts = append(out.Inserts, InsertSpec{Size: r.Size, HoleDiameterMM: r.Value, HoleDepthMM: r.HoleDepth, TypicalInsertLen: r.InsertLen,
				BossOuterMM: r.BossOD, MinWallMM: r.MinWallMM, TipStartTemp: tf.Parameters["temperature de depart du fer"], Level: string(r.Level), Warnings: r.Warnings})
		}
	}

	toks := tokens(in.Usage + " " + in.Geometry)
	for _, ur := range pp.UsageRules {
		if kwMatch(toks, ur.Match) {
			out.Warnings = append(out.Warnings, ur.Warning)
		}
	}
	if strings.HasSuffix(fam, "-CF") {
		out.Warnings = append(out.Warnings, "Matière chargée de fibres de carbone : la poussière de ponçage est irritante (poncer à l'eau, masque FFP2 ou FFP3, gants), et une surface poncée expose les fibres.")
	}
	out.Warnings = append(out.Warnings, pp.GeneralWarns...)
	if (fam == "PETG" || fam == "PC" || fam == "PET" || fam == "PLA" || strings.HasPrefix(fam, "PETG")) && hasGoal(goals, "clear") {
		out.Honesty = append(out.Honesty, pp.Honesty["transparent_petg_pc"])
	}
	if hasGoal(goals, "clear") && (fam == "ABS" || fam == "ASA" || fam == "TPU" || fam == "PA" || strings.HasSuffix(fam, "-CF")) {
		out.Honesty = append(out.Honesty, fam+" : matière opaque ou colorée dans la masse, la transparence n'est pas réalisable en FDM avec cette matière.")
	}
	out.Honesty = append(out.Honesty, pp.Note, pp.SourceStatus)
	seen := map[string]bool{}
	for _, src := range pp.Sources {
		if !seen[src.Title] {
			seen[src.Title] = true
			out.Sources = append(out.Sources, src)
		}
	}
	sort.Slice(out.Sources, func(i, j int) bool { return out.Sources[i].Title < out.Sources[j].Title })
	return out, nil
}

func (d *ppData) techniqueByID(id string) *ppTechnique {
	for i := range d.Techniques {
		if d.Techniques[i].ID == id {
			return &d.Techniques[i]
		}
	}
	return nil
}
