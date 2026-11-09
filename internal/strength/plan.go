package strength

import (
	"fmt"
	"math"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// Question : une question à poser à l'humain, une à la fois, en français simple.
type Question struct {
	Order    int      `json:"order"`
	ID       string   `json:"id"`
	Ask      string   `json:"ask"`
	Why      string   `json:"why"`
	Examples []string `json:"examples,omitempty"`
	MapsTo   string   `json:"maps_to" jsonschema:"paramètre de assess_strength qui reçoit la réponse"`
	Default  string   `json:"default_if_unknown,omitempty" jsonschema:"hypothèse raisonnable à proposer (et à annoncer) si l'humain ne sait pas"`
}

// Proposal : hypothèses par défaut justifiées. Rien n'est appliqué sans que l'humain en soit informé.
type Proposal struct {
	Recognized    string      `json:"recognized_object,omitempty"`
	Statement     string      `json:"statement" jsonschema:"phrase à dire à l'humain pour confirmation"`
	MassRangeKg   [2]float64  `json:"mass_range_kg,omitempty"`
	DynamicFactor float64     `json:"dynamic_factor,omitempty"`
	ForceN        float64     `json:"suggested_force_n,omitempty"`
	Loads         []LoadIn    `json:"loads,omitempty" jsonschema:"charges pré-sélectionnées (source assumed)"`
	Supports      []SupportIn `json:"supports,omitempty" jsonschema:"appuis pré-sélectionnés (source assumed)"`
	Material      string      `json:"material,omitempty"`
	Why           []string    `json:"why"`
	Source        string      `json:"source" jsonschema:"origine des ordres de grandeur (niveau estimation)"`
}

// KnownSettings : réglages lus dans une découpe existante (mesurés), jamais inventés.
type KnownSettings struct {
	Found     bool     `json:"found"`
	Filaments []string `json:"filaments,omitempty"`
	Material  string   `json:"material,omitempty"`
	Walls     int      `json:"walls,omitempty"`
	InfillPct float64  `json:"infill_pct,omitempty"`
	Source    string   `json:"source,omitempty"`
}

// BuildQuestions : questions guidées, dans l'ordre où les poser.
func BuildQuestions(zs *ZoneSet, usage string, known KnownSettings, ex *ObjectExample) []Question {
	var zlist []string
	for _, z := range zs.Zones {
		zlist = append(zlist, fmt.Sprintf("%d = %s", z.ID, kindShort[z.Kind]))
	}
	exs := []string{"guitare : 3,5 à 4,5 kg", "étagère de livres : 5 à 15 kg", "écran 27\" : 4 à 8 kg", "vélo : 10 à 16 kg", "perceuse : 1 à 2,5 kg"}
	defMass := ""
	if ex != nil {
		defMass = ex.Statement()
		exs = append([]string{fmt.Sprintf("%s : %.3g à %.3g kg", ex.Label, ex.MassLowKg, ex.MassHighKg)}, exs...)
	}
	matQ := Question{ID: "matiere_reglages", Ask: "Dans quelle matière vas-tu l'imprimer (PLA, PETG, ABS, ASA, nylon, TPU) et avec quels réglages (nombre de parois, remplissage en %) ?",
		Why: "Un PLA, un PETG ou un nylon ne tiennent pas pareil, et plus de parois ou de remplissage rend la pièce plus solide.", MapsTo: "material, walls, infill",
		Default: "PETG, 3 parois, 20 % de remplissage : à annoncer comme hypothèse"}
	if known.Found {
		matQ.Ask = fmt.Sprintf("J'ai trouvé les réglages de ta dernière découpe (%s) : %s. On part dessus ? Sinon, donne la matière, les parois et le remplissage voulus.", known.Source, describeKnown(known))
		matQ.Default = "réglages de la découpe existante (mesurés)"
	}
	qs := []Question{
		{ID: "usage", Ask: "À quoi va servir cette pièce, et où sera-t-elle placée (au mur, sur une table, dans une voiture, dehors...) ?",
			Why: "Cela permet de deviner ce qu'elle porte, la température qu'elle subira et comment elle est tenue.", MapsTo: "(contexte)", Default: strings.TrimSpace(usage)},
		{ID: "charge", Ask: "Qu'est-ce qui pèse sur la pièce, et combien (en kg ou en newtons) ? Si tu ne sais pas, dis-moi l'objet et je propose une valeur.",
			Why:      "La force est la donnée la plus importante : je ne l'invente jamais en silence, soit tu la donnes, soit je te propose une hypothèse que tu corriges.",
			Examples: exs, MapsTo: "loads[].force_N ou mass_kg", Default: defMass},
		{ID: "ou_charge", Ask: "Où s'applique la charge (donne le numéro de zone de l'image : " + strings.Join(zlist, " ; ") + ") et dans quelle direction ? (par défaut vers le bas)",
			Why: "Une charge au bout d'un bras fatigue bien plus la pièce qu'au pied.", MapsTo: "loads[].zone, direction", Default: "zone d'extrémité tournée vers le haut, vers le bas"},
		{ID: "tenue", Ask: "Comment la pièce est-elle tenue : posée au sol ou sur une table (zone du dessous), ou vissée / collée au mur (zone de fixation) ? Quelle zone ?",
			Why: "Les appuis décident où la pièce travaille : posée, elle ne retient que dans un sens ; vissée, elle est encastrée.", MapsTo: "supports[].zone, type (resting | fixed)",
			Default: "posée sur la zone de base si elle existe, sinon vissée sur la plus grande paroi plane"},
		{ID: "duree_chocs", Ask: "La charge reste-t-elle en permanence, ou seulement par moments ? Y a-t-il des chocs (on la pose brusquement, elle peut tomber dessus) ?",
			Why: "Le plastique « flue » sous une charge permanente, et un choc multiplie l'effort.", MapsTo: "duration (permanent | occasional), load_kind (static | impact)", Default: "permanente, sans choc, avec un coefficient de sécurité de 2"},
		matQ,
		{ID: "orientation", Ask: "Comment sera-t-elle imprimée : à plat, debout, couchée sur le côté ? (ou je choisis la plus solide)",
			Why: "Une pièce imprimée est plus faible entre les couches : la direction de l'impression compte.", MapsTo: "layer_orientation (x | y | z = axe perpendiculaire aux couches)",
			Default: "telle que modélisée (axe Z perpendiculaire aux couches) : à annoncer comme hypothèse"},
	}
	for i := range qs {
		qs[i].Order = i + 1
	}
	return qs
}

func describeKnown(k KnownSettings) string {
	var p []string
	if k.Material != "" {
		p = append(p, "matière "+k.Material)
	}
	if k.Walls > 0 {
		p = append(p, fmt.Sprintf("%d parois", k.Walls))
	}
	if k.InfillPct > 0 {
		p = append(p, fmt.Sprintf("%.0f %% de remplissage", k.InfillPct))
	}
	if len(p) == 0 {
		return "aucun détail exploitable"
	}
	return strings.Join(p, ", ")
}

// BuildProposal : hypothèses par défaut d'après l'usage décrit, le nom du modèle et la géométrie.
func BuildProposal(zs *ZoneSet, hint string, ex *ObjectExample) Proposal {
	p := Proposal{Why: []string{}}
	h := strings.ToLower(hint)
	wall := strings.Contains(h, "mur") || strings.Contains(h, "wall") || strings.Contains(h, "visse") || strings.Contains(h, "vissé") || strings.Contains(h, "fixé") || strings.Contains(h, "mural")
	// appui
	var base, wallZ *Zone
	for i := range zs.Zones {
		z := &zs.Zones[i]
		switch {
		case z.Kind == KindBase && (base == nil || z.AreaMM2 > base.AreaMM2):
			base = z
		case hasCand(z, "support_fixe") && z.Kind != KindBase && (wallZ == nil || z.AreaMM2 > wallZ.AreaMM2):
			wallZ = z
		}
	}
	switch {
	case wall && wallZ != nil:
		p.Supports = []SupportIn{{Zone: wallZ.ID, Type: "fixed", Source: SourceAssumed, Note: "fixation murale supposée sur la plus grande paroi plane"}}
		p.Why = append(p.Why, fmt.Sprintf("appui : zone %d (grande face plane) vissée au mur", wallZ.ID))
	case base != nil:
		p.Supports = []SupportIn{{Zone: base.ID, Type: "resting", Source: SourceAssumed, Note: "pièce posée sur sa face du dessous"}}
		p.Why = append(p.Why, fmt.Sprintf("appui : zone %d (face du dessous) posée sur un support", base.ID))
	case wallZ != nil:
		p.Supports = []SupportIn{{Zone: wallZ.ID, Type: "fixed", Source: SourceAssumed, Note: "plus grande face plane supposée fixée"}}
		p.Why = append(p.Why, fmt.Sprintf("appui : zone %d (plus grande face plane) supposée fixée", wallZ.ID))
	}
	// charge : surface tournée vers le haut, la plus éloignée de l'appui
	var load *Zone
	score := math.Inf(-1)
	for i := range zs.Zones {
		z := &zs.Zones[i]
		if len(p.Supports) > 0 && z.ID == p.Supports[0].Zone {
			continue
		}
		if z.Kind != KindDessus && z.Kind != KindInclineH {
			continue
		}
		s := z.Centroid[2]
		if z.Extremity {
			s += 1e6
		}
		if z.Kind == KindInclineH {
			s += 1e3 // crochets et berceaux d'abord
		}
		if s > score {
			load, score = z, s
		}
	}
	if load == nil { // à défaut, n'importe quelle zone candidate à une charge, hors appui
		for i := range zs.Zones {
			z := &zs.Zones[i]
			if (len(p.Supports) == 0 || z.ID != p.Supports[0].Zone) && hasCand(z, "charge") && (load == nil || z.AreaMM2 > load.AreaMM2) {
				load = z
			}
		}
	}
	if ex != nil {
		p.Recognized = ex.Label
		p.MassRangeKg = [2]float64{ex.MassLowKg, ex.MassHighKg}
		p.DynamicFactor = ex.DynamicFactor
		p.ForceN = ex.SuggestedForceN()
		p.Statement = ex.Statement()
		p.Source = ex.Source
		p.Why = append(p.Why, fmt.Sprintf("masse : %s (%s)", ex.Statement(), ex.Note))
		if load != nil {
			p.Loads = []LoadIn{{Zone: load.ID, ForceN: p.ForceN, Direction: "down", Source: SourceAssumed,
				Note: fmt.Sprintf("%s : %.3g kg × %.2g (chocs)", ex.Label, ex.MassHighKg, ex.DynamicFactor)}}
			p.Why = append(p.Why, fmt.Sprintf("charge : zone %d (%s), vers le bas", load.ID, kindShort[load.Kind]))
		}
	} else {
		p.Statement = "Je n'ai reconnu aucun objet courant : donne-moi la masse (kg) ou la force (N), je n'invente pas la charge. Repères : " + examplesLine()
		p.Source = exampleSource
		if load != nil {
			p.Why = append(p.Why, fmt.Sprintf("zone de charge probable : %d (%s) ; le montant reste à fournir", load.ID, kindShort[load.Kind]))
		}
	}
	p.Material = "PETG"
	p.Why = append(p.Why, "matière si inconnue : PETG, 3 parois, 20 % de remplissage, couches selon Z : à annoncer comme hypothèse")
	return p
}

func examplesLine() string {
	var s []string
	for _, e := range Examples[:5] {
		s = append(s, fmt.Sprintf("%s %.3g à %.3g kg", e.Key, e.MassLowKg, e.MassHighKg))
	}
	return strings.Join(s, " ; ")
}

func hasCand(z *Zone, c string) bool {
	for _, x := range z.Candidates {
		if x == c {
			return true
		}
	}
	return false
}

// meshOf : maillage d'analyse d'une ZoneSet.
func meshOf(zs *ZoneSet) *mesh.Mesh { return zs.Mesh }
