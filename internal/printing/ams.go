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
