package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/calibration"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/library"
)

// Bibliothèque de gabarits paramétriques et calibration (chantier G). Logique dans internal/library et
// internal/calibration, partagée avec les routes HTTP (httpapi/library.go).

// ListTemplatesInput filtre le catalogue de gabarits.
type ListTemplatesInput struct {
	Category string `json:"category,omitempty" jsonschema:"filtre : fixation | support | boîtier | mécanisme | rondelle-entretoise (vide = tous)"`
}

// TemplateIDInput désigne un gabarit ou une variante.
type TemplateIDInput struct {
	Template string `json:"template" jsonschema:"identifiant d'un gabarit ou d'une variante (list_part_templates)"`
}

// GetCalibrationInput : clé d'une calibration.
type GetCalibrationInput struct {
	calibration.Key
}

// GetCalibrationOutput : calibration applicable, ou valeurs génériques.
type GetCalibrationOutput struct {
	calibration.Resolution
	History []calibration.HistoryItem `json:"history,omitempty" jsonschema:"dernières mesures enregistrées pour ce matériau et cette buse"`
}

// registerLibraryTools enregistre les outils de la bibliothèque et de la calibration. Sans service (cœur incomplet), rien n'est exposé.
func registerLibraryTools(s *mcp.Server, c *core.Core) {
	lib, cal := c.Library, c.Calibration
	if lib == nil || cal == nil {
		return
	}
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	f := false
	write := &mcp.ToolAnnotations{DestructiveHint: &f}

	addTool(s, &mcp.Tool{
		Name: "list_part_templates", Title: "Gabarits de pièces paramétriques",
		Description: "Catalogue des gabarits de pièces imprimables (collier, équerre, boîte, entretoise, crochet, crochet renforcé, charnière imprimée en place ou à axe, clip cantilever, boîtier vissé/clipsé " +
			"à inserts, support mural vis/trou de serrure, engrenage droit et paire d'engrenages, vis/écrou métriques ou plot d'insert, poignée, plaque perforée, rondelle) et des variantes enregistrées. " +
			"Chaque fiche donne version, catégorie, paramètres typés (unité, bornes, défaut) et lien de vignette. Étape suivante : describe_part_template puis create_from_template.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ListTemplatesInput) (*mcp.CallToolResult, library.ListOutput, error) {
		o, err := lib.List(ctx, in.Category)
		if err != nil {
			return nil, library.ListOutput{}, err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d gabarit(s)/variante(s) :", len(o.Templates))
		for _, t := range o.Templates {
			fmt.Fprintf(&b, "\n- %s [%s] %s", t.ID, t.Category, t.Summary)
		}
		return textResult(b.String()), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "describe_part_template", Title: "Fiche d'un gabarit",
		Description: "Fiche complète d'un gabarit ou d'une variante : description, version, tous les paramètres (type, unité, bornes, défaut, règle de calcul « auto » : jeu d'ajustement, compensation de trou, " +
			"trou d'insert) et vignette. Les paramètres « auto » sont calculés par le moteur d'ingénierie (calibré si une calibration existe) quand ils sont omis.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in TemplateIDInput) (*mcp.CallToolResult, library.TemplateInfo, error) {
		t, err := lib.Describe(ctx, in.Template)
		if err != nil {
			return nil, library.TemplateInfo{}, err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s v%s — %s\n%s\nParamètres :", t.ID, t.Version, t.Title, t.Description)
		for _, p := range t.Params {
			fmt.Fprintf(&b, "\n- %s (%s%s) défaut %v : %s", p.Name, p.Type, unitSuffix(p.Unit), p.Default, p.Description)
			if p.Auto != "" {
				fmt.Fprintf(&b, " [auto : %s]", p.Auto)
			}
		}
		return textResult(b.String()), *t, nil
	})

	addTool(s, &mcp.Tool{
		Name: "create_from_template", Title: "Créer une pièce depuis un gabarit",
		Description: "Génère la pièce (STEP + STL) depuis un gabarit ou une variante avec les paramètres donnés, l'enregistre au catalogue (nouveau modèle, ou NOUVELLE version d'un modèle existant avec model_id + base_version_id) " +
			"et renvoie : paramètres finalement utilisés, paramètres calculés par l'ingénierie (jeux, compensation de trous, trous d'insert), calculs détaillés (SnapFit pour les clips...), hypothèses, " +
			"avertissements, cotes calculées et provenance de la calibration (« calibré le ... » ou valeurs génériques). Donner material, nozzle_diameter, et printer/filament pour utiliser la calibration de l'utilisateur. " +
			"Chaque pièce est vérifiée étanche. Pour un gabarit non listé, create_parametric_model reste disponible.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in library.CreateInput) (*mcp.CallToolResult, library.CreateOutput, error) {
		o, err := lib.Create(ctx, in)
		if err != nil {
			return nil, library.CreateOutput{}, err
		}
		return textResult(fmt.Sprintf("Modèle %s créé (version %s, gabarit %s v%s) ; %d paramètre(s) calculé(s), %d calcul(s), %d avertissement(s). %s",
			o.ModelID, o.VersionID, o.Template, o.TemplateVersion, len(o.Auto), len(o.Calcs), len(o.Warnings), o.Calibration.Provenance)), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "save_as_template", Title: "Enregistrer une variante de gabarit",
		Description: "À partir d'un modèle généré par create_from_template (ou create_parametric_model), enregistre ses paramètres comme variante nommée réutilisable (ex. « Collier tube 32 »), " +
			"utilisable ensuite comme `template` dans create_from_template. Par défaut seuls les paramètres demandés sont figés (jeux et compensations restent recalculés avec la calibration du moment) ; frozen=true fige aussi les valeurs calculées.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in library.SaveInput) (*mcp.CallToolResult, library.SaveOutput, error) {
		o, err := lib.SaveAs(ctx, in)
		if err != nil {
			return nil, library.SaveOutput{}, err
		}
		return textResult(o.Note), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "generate_calibration_set", Title: "Générer des pièces de calibration",
		Description: "Génère, pour un couple imprimante + buse + matériau (+ bobine), des pièces de calibration enregistrées au catalogue : peigne de jeux 0,1 à 0,6 mm (fit_comb), trous/arbres aux diamètres nominaux (hole_shaft), " +
			"cube de retrait de 50 mm (shrink_cube), ponts et surplombs (bridge_overhang), température (une petite pièce par température, ou une tour). Renvoie, par pièce, quoi mesurer, et un squelette de mesures. " +
			"Séries d'extrusion (sur demande, une petite pièce PAR VALEUR, sans G-code personnalisé) : shrink_sizes (tubes de 20 et 80 mm : échelle et décalage de contour séparés), flow_ratio, retraction, volumetric_speed, first_layer, pressure_advance (réglage manuel : aucune clé de profil BBL). " +
			"Avec `base` (machine/process/filament), une révision de profil découpable bornée est créée pour chaque pièce qui porte des surcharges, et les températures suivent nozzle_temperature_range_low/high du profil filament. Après impression et mesure : record_calibration.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in calibration.GenerateInput) (*mcp.CallToolResult, calibration.GenerateOutput, error) {
		o, err := cal.Generate(ctx, in)
		if err != nil {
			return nil, calibration.GenerateOutput{}, err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Série %s : %d pièce(s) pour %s.", o.SetID, len(o.Pieces), o.Key.Label())
		for _, p := range o.Pieces {
			fmt.Fprintf(&b, "\n- %s (modèle %s) : %s", p.Name, p.ModelID, strings.Join(p.Measure, " ; "))
		}
		b.WriteString("\n" + o.Instructions[len(o.Instructions)-1])
		return textResult(b.String()), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "record_calibration", Title: "Enregistrer des mesures de calibration",
		Description: "Enregistre les mesures de l'utilisateur sur ses pièces de calibration (jeu glissant/serré, trous et arbres mesurés au pied à coulisse, cube de retrait 50 mm, meilleure température, ponts, surplombs) " +
			"et en déduit des corrections : jeu effectif, compensation de trou (droite d'ajustement par orientation), facteurs de retrait XY/Z (un cube) ou échelle + décalages xy_contour_compensation / xy_hole_compensation (shrink_sizes, deux tailles), meilleure température, et la valeur retenue d'une série d'extrusion (débit, rétraction, débit volumétrique, première couche, avance de pression). Elles sont stockées pour (imprimante, buse, matériau, bobine) et " +
			"utilisées en priorité par create_from_template et les calculs d'ingénierie, avec la provenance « calibré le ... ». Les valeurs de profil (`proposals`) sont seulement PROPOSÉES, jamais appliquées : les passer à modify_slicing_profile après accord. Mesures implausibles refusées. Les mesures sont conservées (journal immuable).",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in calibration.RecordInput) (*mcp.CallToolResult, calibration.RecordOutput, error) {
		o, err := cal.Record(ctx, in)
		if err != nil {
			return nil, calibration.RecordOutput{}, err
		}
		return textResult("Calibration enregistrée (" + o.RecordID + ") :\n- " + strings.Join(o.Explanation, "\n- ")), *o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "get_calibration", Title: "Calibration applicable",
		Description: "Calibration active pour (imprimante, buse, matériau, bobine) : valeurs mesurées, leur provenance et les clés utilisées (du plus général au plus spécifique), ou « valeurs génériques » si aucune calibration n'existe. " +
			"Inclut les valeurs de profil proposées (`proposals`, jamais appliquées automatiquement) et l'historique des mesures de ce matériau et de cette buse.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetCalibrationInput) (*mcp.CallToolResult, GetCalibrationOutput, error) {
		r, err := cal.Resolve(ctx, in.Key)
		if err != nil {
			return nil, GetCalibrationOutput{}, err
		}
		h, err := cal.History(ctx, in.Material, in.NozzleMM, 20)
		if err != nil {
			return nil, GetCalibrationOutput{}, err
		}
		return textResult(r.Note), GetCalibrationOutput{Resolution: *r, History: h}, nil
	})
}

func unitSuffix(u string) string {
	if u == "" {
		return ""
	}
	return ", " + u
}
