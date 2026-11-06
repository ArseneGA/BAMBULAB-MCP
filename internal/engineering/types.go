// Package engineering regroupe des fonctions de calcul pures (tolérances,
// contraintes, compensations) pour pièces imprimées en 3D FDM.
//
// Chaque résultat porte sa valeur, ses hypothèses, son domaine de validité et
// un niveau de confiance. Rien ici n'est une certification : ce sont des aides
// à la conception à valider par un essai d'assemblage ou de charge.
package engineering

import (
	"errors"
	"fmt"
	"math"
)

// Level est le niveau de confiance d'un résultat.
type Level string

const (
	// LevelMeasured : valeur directement mesurée (ex. fiche fabricant).
	LevelMeasured Level = "measured"
	// LevelCalculated : formule déterministe appliquée à des entrées mesurées.
	LevelCalculated Level = "calculated"
	// LevelHeuristic : règle empirique ou formule alimentée par des données indicatives.
	LevelHeuristic Level = "heuristic"
	// LevelIndicative : ordre de grandeur seulement.
	LevelIndicative Level = "indicative"
)

// rank classe les niveaux du plus fiable (0) au moins fiable (3).
func (l Level) rank() int {
	switch l {
	case LevelMeasured:
		return 0
	case LevelCalculated:
		return 1
	case LevelHeuristic:
		return 2
	default:
		return 3
	}
}

// worst retourne le moins fiable des niveaux.
func worst(a, b Level) Level {
	if b.rank() > a.rank() {
		return b
	}
	return a
}

// Source référence l'origine d'une donnée ou d'une formule.
type Source struct {
	ID    string `json:"id,omitempty"`
	Title string `json:"title"`
	URL   string `json:"url,omitempty"`
	Date  string `json:"date,omitempty"`
	Note  string `json:"note,omitempty"`
}

// Range est un intervalle [Min, Max] dans l'unité du résultat.
type Range struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// Result est le contrat commun de tous les calculs.
type Result struct {
	Value       float64  `json:"value"`
	Unit        string   `json:"unit"`
	Range       *Range   `json:"range,omitempty"`
	Assumptions []string `json:"assumptions"`
	Validity    string   `json:"validity"`
	Level       Level    `json:"level"`
	Sources     []Source `json:"sources,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

// Disclaimer rappelle que rien n'est une certification.
const Disclaimer = "Aide à la conception : ne constitue pas une certification mécanique ; à valider par un essai."

// ErrInvalidInput est enveloppé par toutes les erreurs de validation d'entrée.
var ErrInvalidInput = errors.New("entrée invalide")

// ErrNotApplicable : le modèle de calcul n'est pas valide pour ce cas (grandes
// déformations, élastomère...). L'erreur enveloppe aussi ErrInvalidInput.
var ErrNotApplicable = errors.New("calcul non applicable")

func notApplicable(format string, a ...any) error {
	return fmt.Errorf("%w (%w): %s", ErrInvalidInput, ErrNotApplicable, fmt.Sprintf(format, a...))
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, a...))
}

func positive(name string, v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return invalid("%s doit être > 0 (reçu %v)", name, v)
	}
	return nil
}

func round(v float64, decimals int) float64 {
	p := math.Pow(10, float64(decimals))
	return math.Round(v*p) / p
}
