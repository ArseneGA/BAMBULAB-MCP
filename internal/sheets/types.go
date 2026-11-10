// Package sheets assemble les fiches de lecture du suivi de projet (G9) : la fiche pièce (PJ5, get_part_sheet) et la
// fiche d'impression (PJ10, get_print_sheet). Lecture seule : calcul déterministe sur le journal, la caméra, les bobines,
// l'audit et les tests de résistance ; aucune IA, aucune commande d'imprimante, aucune confirmation créée ou approuvée.
//
// Convention de lecture des fiches (CONTRAT figé pour les pages web et les cartes) :
//   - un champ numérique inconnu est ABSENT (pointeur nul) et la raison figure dans `unavailable` (clé = nom du champ) ;
//     l'affichage dit alors « non disponible » avec cette raison, jamais 0 inventé ;
//   - `levels` donne le niveau de chaque grande valeur : "measured" (relevé : imprimante, bobines, archive découpée,
//     audit), "computed" (calcul : coût, écart, médianes) ou "declared" (humain : verdict, note, impression hors Printalk).
package sheets

import (
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/insights"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/projects"
)

// Niveaux d'une valeur (règle D6 : mesuré / calculé / déclaré).
const (
	LevelMeasured = "measured"
	LevelComputed = "computed"
	LevelDeclared = "declared"
)

// Statuts d'un palier du journal photo.
const (
	StepOK          = "ok"          // photo rangée
	StepUnavailable = "unavailable" // tentative consignée sans photo (reason dit pourquoi) : « photo non disponible »
	StepAbsent      = "absent"      // aucun relevé pour ce palier (non atteint, sauté ou impression d'avant le journal photo)
)

// Statuts de la lecture des alertes de caméra.
const (
	AlertsNone        = "none"        // caméra disponible, aucune alerte levée
	AlertsListed      = "listed"      // au moins une alerte
	AlertsUnavailable = "unavailable" // caméra non disponible : on ne peut pas dire « aucune alerte »
)

// Statuts de l'accord humain de lancement (lu dans l'audit).
const (
	ApprovalApproved      = "approved"       // l'humain a approuvé le lancement
	ApprovalRequested     = "requested_only" // demande de confirmation sans accord relevé
	ApprovalNone          = "none"           // aucune trace dans l'audit
	ApprovalNotApplicable = "not_applicable" // impression déclarée hors Printalk : aucun accord à relever
)

// PhotoStep : un palier (10, 20, … 100 %) du journal photo d'une impression.
type PhotoStep struct {
	Percent int    `json:"percent" jsonschema:"palier de progression, 10 à 100"`
	Status  string `json:"status" jsonschema:"ok | unavailable (photo non disponible, voir reason) | absent (aucun relevé)"`
	Reason  string `json:"reason,omitempty" jsonschema:"pourquoi la photo n'est pas disponible"`
	SHA256  string `json:"sha256,omitempty"`
	// URL : route existante qui sert l'image (cookie ou jeton d'API de l'interface). Jamais une URL signée longue durée.
	URL string `json:"url,omitempty"`
}

// PhotoStrip : le bandeau de paliers d'une impression.
type PhotoStrip struct {
	// Steps : les 10 paliers (10 … 100) quand au moins un relevé existe ; vide sinon (voir Note).
	Steps []PhotoStep `json:"steps"`
	// Thumbnail : miniature de la photo finale (palier 100 %, sinon photo de fin, sinon dernier palier avec photo).
	Thumbnail *PhotoStep `json:"thumbnail,omitempty"`
	Available int        `json:"available" jsonschema:"nombre de paliers avec photo"`
	Note      string     `json:"note,omitempty" jsonschema:"explication quand il n'y a aucun palier (photo non disponible, impression déclarée...)"`
}

// CountsBlock : compteurs de la pièce (D7) et leur phrase.
type CountsBlock struct {
	insights.PrintCounts
	NeverPrinted bool   `json:"never_printed" jsonschema:"aucune impression au journal"`
	Text         string `json:"text" jsonschema:"ex. imprimée 5 fois (100 % atteint), 4 jugées réussies, 1 jugée ratée (...)"`
}

// PartModel : l'identité de la pièce.
type PartModel struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Tags         []string   `json:"tags,omitempty"`
	CollectionID string     `json:"collection_id,omitempty"`
	Trashed      bool       `json:"trashed,omitempty" jsonschema:"le modèle est dans la corbeille"`
	Dimensions   *Dimension `json:"dimensions_mm,omitempty"`
}

// Dimension en millimètres.
type Dimension struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

// StrengthRef : un test de résistance (ou son absence) rattaché à UNE version.
type StrengthRef struct {
	TestID       string    `json:"test_id"`
	CreatedAt    time.Time `json:"created_at"`
	Material     string    `json:"material,omitempty"`
	Verdict      string    `json:"verdict,omitempty"`
	SafetyFactor float64   `json:"safety_factor,omitempty"`
	Assumed      bool      `json:"assumed" jsonschema:"une valeur d'entrée du calcul est une hypothèse, non une donnée de l'humain"`
}

// PartVersion : une version immuable de la pièce ; l'ordre suit parent_id (parents avant enfants).
type PartVersion struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	Note      string    `json:"note,omitempty"`
	ParentID  string    `json:"parent_id,omitempty"`
	Depth     int       `json:"depth" jsonschema:"profondeur dans la chaîne parent_id (0 = version d'origine)"`
	IsLatest  bool      `json:"is_latest"`
	CreatedAt time.Time `json:"created_at"`
	// Strength : dernier test de résistance de CETTE version (nul : jamais testée, voir StrengthStatus).
	Strength       *StrengthRef `json:"strength,omitempty"`
	StrengthStatus string       `json:"strength_status" jsonschema:"tested | never_tested"`
}

// PartProfile : un profil testé = une signature de réglages effectifs relevés à la découpe.
type PartProfile struct {
	Signature     string                     `json:"signature" jsonschema:"empreinte courte des réglages effectifs (identique pour deux impressions aux mêmes réglages)"`
	Label         string                     `json:"label"`
	Settings      insights.EffectiveSettings `json:"settings"`
	Attempts      int                        `json:"attempts"`
	Printed       int                        `json:"printed" jsonschema:"impressions à 100 %"`
	Success       int                        `json:"judged_success"`
	Failed        int                        `json:"judged_failed"`
	Redo          int                        `json:"judged_redo"`
	Unjudged      int                        `json:"unjudged"`
	External      int                        `json:"external" jsonschema:"déclarées hors Printalk : exclues des médianes mesurées"`
	MedianSeconds *float64                   `json:"median_seconds,omitempty"`
	MedianGrams   *float64                   `json:"median_grams,omitempty"`
	MedianCost    *float64                   `json:"median_cost,omitempty"`
	Currency      string                     `json:"currency,omitempty"`
	LastPrintAt   time.Time                  `json:"last_print_at,omitzero"`
	LastSuccessAt time.Time                  `json:"last_success_at,omitzero"`
	Level         string                     `json:"level" jsonschema:"niveau des médianes : computed (sur des mesures)"`
}

// PartPrint : une impression de la pièce (ligne de l'onglet Impressions).
type PartPrint struct {
	PrintJobID  string     `json:"print_job_id"`
	LogID       string     `json:"log_id"`
	Name        string     `json:"name,omitempty"`
	StartedAt   time.Time  `json:"started_at,omitzero"`
	FinishedAt  time.Time  `json:"finished_at"`
	State       string     `json:"state" jsonschema:"COMPLETED | FAILED | CANCELLED"`
	Outcome     string     `json:"outcome"`
	Reached100  bool       `json:"reached_100"`
	PartCount   int        `json:"part_count"`
	Origin      string     `json:"origin" jsonschema:"printalk | external"`
	Declared    bool       `json:"declared" jsonschema:"impression déclarée hors Printalk (niveau déclaré, exclue du coût mesuré)"`
	Level       string     `json:"level" jsonschema:"measured | declared"`
	ProfileSig  string     `json:"profile_signature"`
	ProfileText string     `json:"profile_label"`
	Verdict     string     `json:"verdict,omitempty" jsonschema:"success | failed | redo ; vide = non jugée"`
	VerdictText string     `json:"verdict_text" jsonschema:"réussie | ratée | à refaire | non jugée"`
	FailReason  string     `json:"fail_reason,omitempty"`
	Seconds     *float64   `json:"actual_seconds,omitempty"`
	Grams       *float64   `json:"actual_grams,omitempty"`
	Cost        *float64   `json:"actual_cost,omitempty"`
	Currency    string     `json:"currency,omitempty"`
	Photos      PhotoStrip `json:"photos"`
}

// PartTotals : coûts et temps cumulés. Le bloc « mesuré » exclut les impressions déclarées hors Printalk.
type PartTotals struct {
	Available       bool     `json:"available" jsonschema:"au moins une impression Printalk mesurée"`
	Reason          string   `json:"reason,omitempty"`
	ActualCost      float64  `json:"actual_cost" jsonschema:"coût réel calculé, impressions Printalk seulement"`
	Currency        string   `json:"currency,omitempty"`
	ActualSeconds   float64  `json:"actual_seconds" jsonschema:"temps machine mesuré cumulé"`
	ActualGrams     float64  `json:"actual_grams"`
	CostPerSuccess  *float64 `json:"cost_per_judged_success,omitempty" jsonschema:"coût réel / impressions jugées réussies (absent sans réussie jugée)"`
	PlannedCost     float64  `json:"planned_cost,omitempty" jsonschema:"coût estimé des tentatives en cours, à part"`
	DeclaredCount   int      `json:"declared_prints" jsonschema:"impressions déclarées hors Printalk (listées « déclarée »)"`
	DeclaredSeconds float64  `json:"declared_seconds,omitempty"`
	DeclaredGrams   float64  `json:"declared_grams,omitempty"`
}

// LastSuccess : la dernière impression jugée réussie.
type LastSuccess struct {
	PrintJobID string    `json:"print_job_id"`
	FinishedAt time.Time `json:"finished_at"`
	ProfileSig string    `json:"profile_signature"`
	Label      string    `json:"profile_label"`
}

// PartSheet : la fiche pièce (get_part_sheet, GET /api/models/{id}/sheet).
type PartSheet struct {
	Model       PartModel             `json:"model"`
	Counts      CountsBlock           `json:"counts"`
	Versions    []PartVersion         `json:"versions"`
	Profiles    []PartProfile         `json:"profiles"`
	Prints      []PartPrint           `json:"prints" jsonschema:"les plus récentes d'abord, bornées par limit"`
	PrintsTotal int                   `json:"prints_total"`
	Truncated   bool                  `json:"prints_truncated,omitempty"`
	Totals      PartTotals            `json:"totals"`
	LastSuccess *LastSuccess          `json:"last_success,omitempty"`
	Projects    []projects.ProjectRef `json:"projects" jsonschema:"projets qui citent la pièce"`
	// StrengthUnassigned : tests de résistance sans version identifiable (« version non déterminée »).
	StrengthUnassigned []StrengthRef     `json:"strength_unassigned,omitempty"`
	Levels             map[string]string `json:"levels"`
	Unavailable        map[string]string `json:"unavailable,omitempty"`
	Include            []string          `json:"include" jsonschema:"sections servies : versions, profiles, prints, strength, projects"`
	Note               string            `json:"note"`
}

// ---- fiche d'impression ----

// VerdictBlock : le jugement de l'humain (déclaré).
type VerdictBlock struct {
	Value      string    `json:"value,omitempty" jsonschema:"success | failed | redo ; vide = pas encore jugée"`
	Text       string    `json:"text"`
	FailReason string    `json:"fail_reason,omitempty"`
	At         time.Time `json:"at,omitzero"`
	Source     string    `json:"source,omitempty" jsonschema:"human_card | human_web | claude_on_request"`
	Note       string    `json:"note,omitempty"`
	Level      string    `json:"level" jsonschema:"declared"`
	// FailReasons : vocabulaire fermé (sert à la bande verdict de la carte).
	FailReasons []insights.Defect `json:"fail_reasons"`
}

// FrozenProfile : les réglages EFFECTIFS relevés à la découpe. Figés : modifier ensuite un profil ne change pas la fiche.
type FrozenProfile struct {
	Signature string                     `json:"signature"`
	Label     string                     `json:"label"`
	Settings  insights.EffectiveSettings `json:"settings"`
	Frozen    string                     `json:"frozen_note"`
	Level     string                     `json:"level" jsonschema:"measured : relevé dans l'archive découpée"`
}

// SpoolUse : la consommation d'une bobine pour cette impression.
type SpoolUse struct {
	SpoolID    string   `json:"spool_id"`
	Slot       int      `json:"filament_slot"`
	Label      string   `json:"label,omitempty" jsonschema:"matière, marque, couleur de la bobine quand elle est inventoriée"`
	Grams      float64  `json:"grams"`
	Estimated  bool     `json:"estimated" jsonschema:"masse estimée (non pesée) : impression interrompue ou masse du slicer"`
	PricePerKg *float64 `json:"price_per_kg,omitempty"`
	Cost       *float64 `json:"cost,omitempty"`
	Note       string   `json:"note,omitempty"`
	Level      string   `json:"level" jsonschema:"measured | declared"`
}

// MaterialBlock : matière consommée.
type MaterialBlock struct {
	Family         string     `json:"family,omitempty"`
	Spools         []SpoolUse `json:"spools"`
	ActualGrams    *float64   `json:"actual_grams,omitempty"`
	EstimatedGrams *float64   `json:"estimated_grams,omitempty" jsonschema:"masse annoncée par le slicer"`
	Estimated      bool       `json:"estimated" jsonschema:"la masse réelle n'a pas été pesée : estimation"`
	Basis          string     `json:"basis,omitempty"`
	Level          string     `json:"level"`
}

// DurationBlock : durée prévue contre durée réelle (CA10.2).
type DurationBlock struct {
	PlannedSeconds *float64 `json:"planned_seconds,omitempty"`
	ActualSeconds  *float64 `json:"actual_seconds,omitempty"`
	DeltaPct       *float64 `json:"delta_pct,omitempty" jsonschema:"(réel − prévu) / prévu en % ; absent sans durée réelle ou sans prévision"`
	Text           string   `json:"text" jsonschema:"ex. +12,5 % ou « non disponible » avec la raison"`
	Level          string   `json:"level" jsonschema:"measured pour la durée réelle, computed pour l'écart"`
}

// CostBlock : coût prévu et coût réel (calculés).
type CostBlock struct {
	Planned  *float64 `json:"planned,omitempty"`
	Actual   *float64 `json:"actual,omitempty"`
	Currency string   `json:"currency,omitempty"`
	Level    string   `json:"level" jsonschema:"computed ; absent pour une impression déclarée (hors coût mesuré)"`
}

// AlertView : une alerte de la détection d'échec par caméra.
type AlertView struct {
	At       time.Time `json:"at"`
	Score    float64   `json:"score"`
	Level    string    `json:"level"`
	PhotoURL string    `json:"photo_url,omitempty"`
}

// CameraBlock : la caméra pendant l'impression. « Caméra non disponible » (alerts_status = unavailable) est distinct
// de « aucune alerte » (alerts_status = none) : sans caméra, on ne peut pas affirmer l'absence d'alerte (CA10.3).
type CameraBlock struct {
	Available    bool        `json:"available"`
	Reason       string      `json:"reason,omitempty"`
	AlertsStatus string      `json:"alerts_status" jsonschema:"none | listed | unavailable"`
	Alerts       []AlertView `json:"alerts"`
	Text         string      `json:"text"`
	Level        string      `json:"level" jsonschema:"measured"`
}

// ApprovalBlock : l'accord humain de lancement, lu dans l'audit (aucun code, aucun jeton).
type ApprovalBlock struct {
	Status      string    `json:"status" jsonschema:"approved | requested_only | none | not_applicable"`
	RequestedAt time.Time `json:"requested_at,omitzero"`
	ApprovedAt  time.Time `json:"approved_at,omitzero"`
	Via         string    `json:"via,omitempty" jsonschema:"canal de l'accord humain (page, code personnel, clé d'accès)"`
	Text        string    `json:"text"`
	Level       string    `json:"level" jsonschema:"measured"`
}

// PrintHeader : l'en-tête de la fiche d'impression.
type PrintHeader struct {
	Name         string    `json:"name,omitempty"`
	ModelID      string    `json:"model_id,omitempty"`
	ModelName    string    `json:"model_name,omitempty"`
	PrinterID    string    `json:"printer_id,omitempty"`
	PrinterModel string    `json:"printer_model,omitempty"`
	State        string    `json:"state"`
	Outcome      string    `json:"outcome"`
	Origin       string    `json:"origin" jsonschema:"printalk | external"`
	Declared     bool      `json:"declared" jsonschema:"impression déclarée hors Printalk"`
	StartedAt    time.Time `json:"started_at,omitzero"`
	FinishedAt   time.Time `json:"finished_at"`
	Reached100   bool      `json:"reached_100"`
	PartCount    int       `json:"part_count"`
}

// PrintSheet : la fiche d'impression (get_print_sheet, GET /api/prints/{id}/sheet).
type PrintSheet struct {
	PrintJobID  string            `json:"print_job_id"`
	LogID       string            `json:"log_id"`
	Header      PrintHeader       `json:"header"`
	Verdict     VerdictBlock      `json:"verdict"`
	Profile     FrozenProfile     `json:"profile"`
	Material    MaterialBlock     `json:"material"`
	Duration    DurationBlock     `json:"duration"`
	Cost        CostBlock         `json:"cost"`
	Photos      PhotoStrip        `json:"photos"`
	Camera      CameraBlock       `json:"camera"`
	Approval    ApprovalBlock     `json:"approval"`
	Comment     string            `json:"comment,omitempty"`
	Rating      int               `json:"rating,omitempty"`
	Defects     []string          `json:"defects,omitempty"`
	Levels      map[string]string `json:"levels"`
	Unavailable map[string]string `json:"unavailable,omitempty"`
	Text        string            `json:"text" jsonschema:"résumé lisible de la fiche"`
}
