package printing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// Points d'accroche pour le chantier « bobines / AMS / file d'attente » (internal/filament) et
// pour les notifications. Aucun n'a de pouvoir sur le lancement : ils OBSERVENT (fin d'impression)
// ou AVERTISSENT (alerte de matière avant l'envoi). Un lancement exige toujours la confirmation humaine.

// FilamentWarning est une alerte de matière ajoutée au rapport de validation.
type FilamentWarning struct {
	Name   string `json:"name"`
	Level  string `json:"level"` // warn | error
	Detail string `json:"detail"`
}

// RemainingDecl : restant déclaré (grammes) pour un filament de l'impression (par numéro de filament ou par emplacement).
// Une déclaration est une information donnée par l'utilisateur, jamais une preuve : elle ne débloque rien.
type RemainingDecl struct {
	FilamentSlot int     `json:"filament_slot,omitempty" jsonschema:"numéro du filament de l'impression (1, 2…) ; ou tray"`
	Tray         string  `json:"tray,omitempty" jsonschema:"emplacement (ex. ams0:0, ext) ; ou filament_slot"`
	Grams        float64 `json:"grams" jsonschema:"restant déclaré en grammes (0 à 10000)"`
}

// FilamentLine : contrôle de matière d'un filament, montré à l'humain avec la confirmation (FB12).
type FilamentLine struct {
	FilamentSlot   int     `json:"filament_slot"`
	Tray           string  `json:"tray"`
	NeededG        float64 `json:"needed_g"`
	RequiredG      float64 `json:"required_with_margin_g"`
	RemainingG     float64 `json:"remaining_g,omitempty"`
	Source         string  `json:"remaining_source" jsonschema:"inventory | ams_estimate | declared | unknown"`
	Verdict        string  `json:"verdict" jsonschema:"ok | marginal | insufficient | unknown"`
	Detail         string  `json:"detail"`
	NeedsRemaining bool    `json:"needs_remaining,omitempty" jsonschema:"vrai si le restant est inconnu : demander les grammes restants à l'utilisateur"`
}

// FilamentPreview calcule le contrôle de matière d'une impression (restants déclarés compris). Informatif : jamais bloquant.
type FilamentPreview func(ctx context.Context, j Job) []FilamentLine

// SetFilamentPreview branche le contrôle de matière affiché à la confirmation (nil le retire).
func (s *Service) SetFilamentPreview(f FilamentPreview) {
	s.hookMu.Lock()
	s.filamentPreview = f
	s.hookMu.Unlock()
}

func (s *Service) filamentLines(ctx context.Context, j Job) []FilamentLine {
	s.hookMu.Lock()
	f := s.filamentPreview
	s.hookMu.Unlock()
	if f == nil {
		return nil
	}
	return f(ctx, j)
}

// FilamentCheck calcule les alertes de matière d'une impression prévue.
type FilamentCheck func(ctx context.Context, printerID string, plate *slicer.PlateResult, as []FilamentAssignment) []FilamentWarning

// JobEndHook est appelé (dans sa propre goroutine) quand une impression atteint COMPLETED, FAILED ou CANCELLED.
type JobEndHook func(j Job)

// SetFilamentCheck branche la vérification de matière (nil la retire).
func (s *Service) SetFilamentCheck(f FilamentCheck) {
	s.hookMu.Lock()
	s.filamentCheck = f
	s.hookMu.Unlock()
}

// AddJobEndHook enregistre un observateur de fin d'impression.
func (s *Service) AddJobEndHook(h JobEndHook) {
	s.hookMu.Lock()
	s.endHooks = append(s.endHooks, h)
	s.hookMu.Unlock()
}

func (s *Service) fireJobEnd(j Job) {
	switch j.State {
	case contracts.PrintCompleted, contracts.PrintFailed, contracts.PrintCancelled:
	default:
		return
	}
	s.hookMu.Lock()
	if s.hooksClosed { // arrêt en cours : wg.Wait a peut-être déjà commencé, on n'ajoute plus rien
		s.hookMu.Unlock()
		return
	}
	hs := append([]JobEndHook(nil), s.endHooks...)
	s.wg.Add(len(hs)) // sous le verrou, avant la fermeture de la porte : jamais après wg.Wait
	s.hookMu.Unlock()
	for _, h := range hs {
		go func() {
			defer s.wg.Done()
			defer func() {
				if r := recover(); r != nil {
					s.log.Error("observateur de fin d'impression en panne", "print_job", j.ID, "panic", fmt.Sprint(r))
				}
			}()
			h(j)
		}()
	}
}

func (s *Service) filamentWarnings(ctx context.Context, printerID string, plate *slicer.PlateResult, as []FilamentAssignment) []FilamentWarning {
	s.hookMu.Lock()
	f := s.filamentCheck
	s.hookMu.Unlock()
	if f == nil || len(as) == 0 {
		return nil
	}
	return f(ctx, printerID, plate, as)
}

// BuildAMSMapping dérive ams_mapping / use_ams des affectations (même calcul que le dépôt du fichier :
// position = numéro de filament - 1, valeur = unité*4 + bac, 254 = bobine externe, -1 = inutilisé).
func BuildAMSMapping(as []FilamentAssignment) (mapping []int, useAMS bool) { return buildAMS(as) }

// PrinterAMS lit l'état AMS du dernier rapport de l'imprimante (lecture seule ; aucune commande envoyée).
// nil si le rapport ne contient pas d'AMS ni de bobine externe.
func (s *Service) PrinterAMS(ctx context.Context, printerID string) (*printer.AMSState, printer.Status, error) {
	ad, _, err := s.adapter(ctx, printerID)
	if err != nil {
		return nil, printer.Status{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	st, err := ad.Status(cctx)
	if err != nil && !errors.Is(err, printer.ErrStaleStatus) {
		return st.AMS, st, err
	}
	return st.AMS, st, nil
}
