package insights

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

//go:embed data/adjustments.json
var adjustmentsJSON []byte

// RuleSource est la provenance d'une règle.
type RuleSource struct {
	Title    string `json:"title"`
	URL      string `json:"url"`
	Verified bool   `json:"verified" jsonschema:"faux : source citée comme provenance générale, non re-consultée pour cette version"`
}

// Adjustment est un changement de réglage proposé (jamais appliqué automatiquement).
type Adjustment struct {
	Key       string   `json:"key" jsonschema:"clé du profil Bambu Studio concernée"`
	Target    string   `json:"target" jsonschema:"machine | process | filament"`
	Direction string   `json:"direction" jsonschema:"increase | decrease | set"`
	Amount    string   `json:"amount" jsonschema:"ordre de grandeur du changement, à partir de la valeur ACTUELLE du profil"`
	Unit      string   `json:"unit,omitempty"`
	Why       string   `json:"why"`
	Families  []string `json:"families,omitempty" jsonschema:"familles de matière concernées (vide : toutes)"`
}

type adjRule struct {
	Defect       string       `json:"defect"`
	Summary      string       `json:"summary"`
	Adjustments  []Adjustment `json:"adjustments"`
	OtherActions []string     `json:"other_actions"`
	Tradeoffs    string       `json:"tradeoffs"`
	Sources      []string     `json:"sources"`
}

type adjData struct {
	Version      string                `json:"version"`
	Level        string                `json:"level"`
	Note         string                `json:"note"`
	SourceStatus string                `json:"source_status"`
	Sources      map[string]RuleSource `json:"sources"`
	Rules        []adjRule             `json:"rules"`
}

var (
	adjOnce sync.Once
	adjVal  *adjData
)

func loadAdj() *adjData {
	adjOnce.Do(func() {
		var d adjData
		if err := json.Unmarshal(adjustmentsJSON, &d); err != nil {
			panic("insights/data/adjustments.json : " + err.Error())
		}
		adjVal = &d
	})
	return adjVal
}

// AdjustInput décrit l'impression à venir pour laquelle chercher des ajustements.
type AdjustInput struct {
	LogID          string   `json:"log_id,omitempty" jsonschema:"fiche (log_...) ou impression dont les défauts et le contexte servent de base"`
	MaterialFamily string   `json:"material_family,omitempty" jsonschema:"PLA, PETG... (requis sans log_id)"`
	PrinterModel   string   `json:"printer_model,omitempty" jsonschema:"modèle d'imprimante (p1s, x1c, a1...) ; omis : toutes les machines de la famille"`
	ModelID        string   `json:"model_id,omitempty" jsonschema:"modèle 3D : ses fiches passées pèsent davantage"`
	Defects        []string `json:"defects,omitempty" jsonschema:"défauts à traiter explicitement, en plus de ceux des fiches"`
}

// ProposalBasis dit pourquoi un défaut est traité.
type DefectProposal struct {
	Defect       string       `json:"defect"`
	Label        string       `json:"label"`
	Basis        string       `json:"basis" jsonschema:"pourquoi ce défaut est traité : coché sur la fiche, demandé, ou récurrent dans les fiches comparables"`
	Occurrences  int          `json:"occurrences_in_comparable_logs"`
	Summary      string       `json:"summary"`
	Adjustments  []Adjustment `json:"adjustments"`
	OtherActions []string     `json:"other_actions,omitempty"`
	Tradeoffs    string       `json:"tradeoffs"`
	Sources      []RuleSource `json:"sources"`
}

// Reference est la meilleure fiche comparable (à reprendre comme base).
type Reference struct {
	LogID     string            `json:"log_id"`
	Rating    int               `json:"rating"`
	SameModel bool              `json:"same_model"`
	Process   string            `json:"process,omitempty"`
	Filaments []string          `json:"filaments,omitempty"`
	Overrides []slicer.Override `json:"overrides,omitempty"`
	Comment   string            `json:"comment,omitempty"`
	Note      string            `json:"note"`
}

// AdjustOutput est la liste des ajustements proposés.
type AdjustOutput struct {
	MaterialFamily string           `json:"material_family"`
	PrinterModel   string           `json:"printer_model,omitempty"`
	ComparableLogs int              `json:"comparable_logs" jsonschema:"fiches de la même famille de matière et de la même machine"`
	Proposals      []DefectProposal `json:"proposals"`
	BestReference  *Reference       `json:"best_reference,omitempty"`
	Automatic      bool             `json:"automatic" jsonschema:"toujours faux : aucun réglage n'est appliqué, vous décidez"`
	HowToApply     string           `json:"how_to_apply"`
	Level          string           `json:"level"`
	RulesVersion   string           `json:"rules_version"`
	Notes          []string         `json:"notes"`
}

// recurrenceMin : nombre de fiches comparables portant le même défaut au-delà duquel il est jugé récurrent.
const recurrenceMin = 2

func labelOf(id string) string {
	for _, d := range Defects {
		if d.ID == id {
			return d.Label
		}
	}
	return id
}

func ruleFor(defect string) *adjRule {
	for i := range loadAdj().Rules {
		if loadAdj().Rules[i].Defect == defect {
			return &loadAdj().Rules[i]
		}
	}
	return nil
}

func familyApplies(fams []string, fam string) bool {
	if len(fams) == 0 || fam == "" {
		return true
	}
	for _, f := range fams {
		if f == fam {
			return true
		}
	}
	return false
}

// SuggestAdjustments propose des ajustements concrets pour la prochaine impression comparable,
// d'après les défauts cochés (fiche ou demande) et ceux qui reviennent dans les fiches comparables.
func (s *Service) SuggestAdjustments(ctx context.Context, in AdjustInput) (*AdjustOutput, error) {
	if _, err := s.SyncJournal(ctx); err != nil {
		return nil, err
	}
	out := &AdjustOutput{Automatic: false, Level: loadAdj().Level, RulesVersion: loadAdj().Version, Proposals: []DefectProposal{},
		HowToApply: "Rien n'est appliqué. Changer UN réglage à la fois depuis la valeur actuelle du profil (modify_slicing_profile ou create_experiment), refaire une pièce comparable, " +
			"puis noter le résultat dans le journal (update_print_log) pour que la suite s'appuie sur des faits."}
	fam, printerKey, modelID := strings.ToUpper(strings.TrimSpace(in.MaterialFamily)), modelKey(in.PrinterModel), in.ModelID
	if fam != "" && MaterialFamily(fam) != "" {
		fam = MaterialFamily(fam)
	}
	wanted := map[string]string{} // défaut -> raison
	var self *LogEntry
	if in.LogID != "" {
		e, err := s.findEntry(ctx, in.LogID)
		if err != nil {
			return nil, err
		}
		self = e
		if fam == "" {
			fam = e.MaterialFamily
		}
		if printerKey == "" {
			printerKey = modelKey(e.PrinterModel)
		}
		if modelID == "" {
			modelID = e.ModelID
		}
		for _, d := range e.Defects {
			wanted[d] = "coché sur la fiche " + e.ID
		}
	}
	if fam == "" {
		return nil, invalid("material_family requis (ou une fiche log_id dont la matière est connue)")
	}
	for _, d := range in.Defects {
		d = strings.ToLower(strings.TrimSpace(d))
		if !validDefect(d) {
			return nil, invalid("défaut %q inconnu", d)
		}
		if _, ok := wanted[d]; !ok {
			wanted[d] = "demandé"
		}
	}
	out.MaterialFamily = fam
	if printerKey != "" {
		out.PrinterModel = loadPower().Models[printerKey].Name
	}

	all, err := s.ListLogs(ctx, ListQuery{MaterialFamily: fam, Limit: 200})
	if err != nil {
		return nil, err
	}
	var similar []LogEntry
	for _, e := range all.Entries {
		if printerKey != "" && modelKey(e.PrinterModel) != printerKey {
			continue
		}
		similar = append(similar, e)
	}
	out.ComparableLogs = len(similar)
	counts := map[string]int{}
	for _, e := range similar {
		for _, d := range e.Defects {
			counts[d]++
		}
	}
	for d, n := range counts {
		if n >= recurrenceMin {
			if _, ok := wanted[d]; !ok {
				wanted[d] = fmt.Sprintf("récurrent : présent dans %d fiches comparables sur %d", n, len(similar))
			}
		}
	}
	defects := make([]string, 0, len(wanted))
	for d := range wanted {
		defects = append(defects, d)
	}
	// plus fréquents d'abord, puis ordre alphabétique (déterministe)
	sort.Slice(defects, func(i, j int) bool {
		if counts[defects[i]] != counts[defects[j]] {
			return counts[defects[i]] > counts[defects[j]]
		}
		return defects[i] < defects[j]
	})
	srcs := loadAdj().Sources
	for _, d := range defects {
		r := ruleFor(d)
		if r == nil {
			out.Notes = append(out.Notes, fmt.Sprintf("Défaut « %s » : aucune règle (à décrire dans le commentaire ; pas de réglage proposé).", d))
			continue
		}
		p := DefectProposal{Defect: d, Label: labelOf(d), Basis: wanted[d], Occurrences: counts[d], Summary: r.Summary, Tradeoffs: r.Tradeoffs,
			OtherActions: r.OtherActions, Adjustments: []Adjustment{}}
		for _, a := range r.Adjustments {
			if familyApplies(a.Families, fam) {
				p.Adjustments = append(p.Adjustments, a)
			}
		}
		for _, k := range r.Sources {
			if src, ok := srcs[k]; ok {
				p.Sources = append(p.Sources, src)
			}
		}
		out.Proposals = append(out.Proposals, p)
	}

	// meilleure fiche comparable : note >= 4 et réussie, même modèle d'abord
	var best *LogEntry
	for i := range similar {
		e := &similar[i]
		if e.Rating < 4 || e.Outcome != "success" || (self != nil && e.ID == self.ID) {
			continue
		}
		if best == nil {
			best = e
			continue
		}
		bs, es := modelID != "" && best.ModelID == modelID, modelID != "" && e.ModelID == modelID
		if es && !bs || es == bs && e.Rating > best.Rating {
			best = e
		}
	}
	if best != nil {
		out.BestReference = &Reference{LogID: best.ID, Rating: best.Rating, SameModel: modelID != "" && best.ModelID == modelID,
			Process: best.Settings.Process, Filaments: best.Settings.Filaments, Overrides: best.Settings.Overrides, Comment: best.Comment,
			Note: "Meilleure fiche comparable (réussie, bien notée) : une base de départ, pas une garantie pour une autre géométrie."}
	}
	switch {
	case len(similar) == 0:
		out.Notes = append(out.Notes, "Aucune fiche comparable (même famille de matière et même machine) : les propositions viennent seulement des défauts indiqués.")
	case len(out.Proposals) == 0:
		out.Notes = append(out.Notes, "Aucun défaut signalé ni récurrent dans les fiches comparables : rien à corriger d'après le journal.")
	}
	out.Notes = append(out.Notes, loadAdj().Note, loadAdj().SourceStatus)
	return out, nil
}
