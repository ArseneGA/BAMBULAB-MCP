// Package library expose la bibliothèque extensible de gabarits paramétriques : registre versionné (côté worker Python /
// CadQuery), schéma de paramètres typé, calculs d'ingénierie associés (jeux, perçages, inserts, clip), variantes nommées
// (save_as_template) et enregistrement des modèles générés au catalogue.
package library

import (
	"errors"
	"fmt"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/engineering"
)

var (
	// ErrInvalid : entrée invalide (paramètre hors bornes, gabarit inconnu...).
	ErrInvalid = errors.New("entrée invalide")
	// ErrUnavailable : worker CAO (uv + CadQuery) absent ou en échec.
	ErrUnavailable = errors.New("indisponible")
	// ErrConflict : variante déjà existante, version de base périmée.
	ErrConflict = errors.New("conflit")
)

func invalidf(format string, a ...any) error {
	return fmt.Errorf("%w : %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// ParamSpec décrit un paramètre typé d'un gabarit.
type ParamSpec struct {
	Name        string   `json:"name"`
	Type        string   `json:"type" jsonschema:"number | integer | enum | boolean"`
	Unit        string   `json:"unit,omitempty"`
	Default     any      `json:"default"`
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`
	Options     []string `json:"options,omitempty"`
	Description string   `json:"description"`
	Auto        string   `json:"auto,omitempty" jsonschema:"règle de calcul d'ingénierie appliquée quand le paramètre n'est pas fourni (ou, pour hole:*, compensation du diamètre fourni)"`
}

// TemplateInfo est la fiche d'un gabarit (intégré ou variante).
type TemplateInfo struct {
	ID           string         `json:"id"`
	Kind         string         `json:"kind" jsonschema:"builtin | variant"`
	Version      string         `json:"version"`
	SchemaHash   string         `json:"schema_hash"`
	Title        string         `json:"title"`
	Category     string         `json:"category"`
	Summary      string         `json:"summary"`
	Description  string         `json:"description"`
	Tags         []string       `json:"tags,omitempty"`
	Params       []ParamSpec    `json:"params"`
	BaseTemplate string         `json:"base_template,omitempty" jsonschema:"variante : gabarit intégré dont elle dérive"`
	Fixed        map[string]any `json:"fixed_params,omitempty" jsonschema:"variante : paramètres figés (les défauts de Params les reflètent)"`
	ThumbnailURL string         `json:"thumbnail_url"`
}

// ListOutput : catalogue des gabarits.
type ListOutput struct {
	Templates  []TemplateInfo `json:"templates"`
	Categories []string       `json:"categories"`
	Note       string         `json:"note"`
}

// CreateInput est l'entrée de create_from_template.
type CreateInput struct {
	Template        string         `json:"template" jsonschema:"identifiant d'un gabarit (list_part_templates) ou d'une variante enregistrée"`
	Params          map[string]any `json:"params,omitempty" jsonschema:"paramètres du gabarit en unités de son schéma (mm, degrés) ; les omis prennent le défaut, ou la valeur calculée par le moteur d'ingénierie pour les paramètres « auto »"`
	Name            string         `json:"name,omitempty" jsonschema:"nom du modèle (lettres, chiffres, . _ -)"`
	ModelID         string         `json:"model_id,omitempty" jsonschema:"si fourni : crée une NOUVELLE VERSION de ce modèle (jamais d'écrasement)"`
	BaseVersionID   string         `json:"base_version_id,omitempty" jsonschema:"requis avec model_id : version de départ ; refus si une version plus récente existe"`
	Material        string         `json:"material,omitempty" jsonschema:"matériau (PLA, PETG, ABS, ASA, TPU-95A, PC, PA-CF...) pour jeux, inserts et clip ; défaut PLA"`
	NozzleMM        float64        `json:"nozzle_diameter,omitempty" jsonschema:"buse en mm (défaut 0.4)"`
	Printer         string         `json:"printer,omitempty" jsonschema:"imprimante (même texte que lors de record_calibration) pour appliquer sa calibration"`
	Filament        string         `json:"filament,omitempty" jsonschema:"bobine (même texte que lors de record_calibration)"`
	CompensateHoles *bool          `json:"compensate_holes,omitempty" jsonschema:"compense les diamètres de trous nominaux (défaut vrai) ; faux = diamètres modélisés tels quels"`
	Tags            []string       `json:"tags,omitempty"`
}

// AutoParam documente un paramètre calculé ou compensé.
type AutoParam struct {
	Param   string  `json:"param"`
	Rule    string  `json:"rule"`
	Nominal any     `json:"nominal,omitempty" jsonschema:"valeur d'entrée (diamètre nominal) avant compensation"`
	Value   float64 `json:"value"`
	Unit    string  `json:"unit"`
	Level   string  `json:"level" jsonschema:"measured (calibré) | calculated | heuristic | indicative"`
	Source  string  `json:"source" jsonschema:"calibration (avec provenance) ou valeur générique"`
}

// Calc est un calcul d'ingénierie lié au gabarit.
type Calc struct {
	Name   string             `json:"name"`
	Param  string             `json:"param,omitempty"`
	Result engineering.Result `json:"result"`
	Extra  map[string]any     `json:"extra,omitempty"`
}

// CalibrationUsed dit si une calibration utilisateur a été appliquée.
type CalibrationUsed struct {
	Applied    bool     `json:"applied"`
	Provenance string   `json:"provenance"`
	Matched    []string `json:"matched,omitempty"`
	Note       string   `json:"note"`
}

// PartInfo : une pièce produite.
type PartInfo struct {
	Name       string  `json:"name"`
	VolumeMM3  float64 `json:"volume_mm3"`
	Solids     int     `json:"solids"`
	Watertight bool    `json:"watertight"`
	Triangles  int     `json:"triangles"`
}

// CreateOutput : modèle créé, hypothèses et calculs.
type CreateOutput struct {
	ModelID         string                 `json:"model_id"`
	VersionID       string                 `json:"version_id"`
	Files           []contracts.SourceFile `json:"files"`
	Template        string                 `json:"template"`
	TemplateVersion string                 `json:"template_version"`
	Variant         string                 `json:"variant,omitempty"`
	Params          map[string]any         `json:"params" jsonschema:"paramètres finalement utilisés (après calculs et compensations)"`
	Requested       map[string]any         `json:"requested" jsonschema:"paramètres demandés"`
	Auto            []AutoParam            `json:"auto" jsonschema:"paramètres calculés ou compensés par le moteur d'ingénierie"`
	Calcs           []Calc                 `json:"calcs"`
	Calibration     CalibrationUsed        `json:"calibration"`
	Parts           []PartInfo             `json:"parts"`
	Assumptions     []string               `json:"assumptions"`
	Warnings        []string               `json:"warnings"`
	BBoxMM          map[string]any         `json:"bbox_mm,omitempty"`
	Derived         map[string]any         `json:"derived,omitempty"`
	ThumbnailURL    string                 `json:"thumbnail_url,omitempty"`
	Level           string                 `json:"level"`
	Note            string                 `json:"note"`
}

// SaveInput est l'entrée de save_as_template.
type SaveInput struct {
	ModelID     string `json:"model_id" jsonschema:"modèle créé par la bibliothèque (ou par create_parametric_model)"`
	VersionID   string `json:"version_id,omitempty" jsonschema:"version dont on reprend les paramètres (défaut : la plus récente générée)"`
	Name        string `json:"name" jsonschema:"nom de la variante, ex. « Collier tube 32 »"`
	Description string `json:"description,omitempty"`
	Frozen      bool   `json:"frozen,omitempty" jsonschema:"vrai = figer aussi les valeurs calculées (jeux, compensations) ; faux (défaut) = ne figer que les paramètres demandés, les calculs restent recalculés avec la calibration du moment"`
	Replace     bool   `json:"replace,omitempty" jsonschema:"remplace une variante du même nom"`
}

// SaveOutput : variante enregistrée.
type SaveOutput struct {
	Variant TemplateInfo `json:"variant"`
	Note    string       `json:"note"`
}
