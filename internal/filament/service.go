// Package filament gère l'inventaire des bobines, l'état de l'AMS, le multicolore (affectation couleur ->
// emplacement, purge) et la file d'attente d'impressions.
//
// Règle de sécurité (docs/SECURITY.md §9) : ce paquet ne lance, ne confirme ni n'arrête JAMAIS une impression.
// Il prépare (alerte de matière, affectation, découpe/validation/envoi du fichier d'un élément de file) et
// informe ; chaque démarrage exige une confirmation humaine fraîche sur /confirm. Il n'importe aucune fonction
// de lancement ou d'approbation de internal/printing (vérifié par TestQueueNeverStartsOrApproves).
package filament

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

const actor = "filament"

var (
	// ErrInvalid : entrée refusée (message destiné à l'utilisateur).
	ErrInvalid = errors.New("entrée invalide")
	// ErrNotFound : bobine ou élément de file introuvable.
	ErrNotFound = errors.New("introuvable")
	// ErrConflict : état incompatible (emplacement déjà occupé, impression déjà en file...).
	ErrConflict = errors.New("conflit")
)

func invalidf(format string, a ...any) error {
	return fmt.Errorf("%w : %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Event est un événement interne publié pour les notifications (agent D : ntfy / webhook / e-mail).
type Event struct {
	Kind       string    `json:"kind"` // queue_next_ready | spool_low | queue_item_failed
	At         time.Time `json:"at"`
	QueueID    string    `json:"queue_id,omitempty"`
	PrintJobID string    `json:"print_job_id,omitempty"`
	PrinterID  string    `json:"printer_id,omitempty"`
	SpoolID    string    `json:"spool_id,omitempty"`
	ConfirmURL string    `json:"confirm_url,omitempty"`
	Message    string    `json:"message"`
}

// Deps : ce que le cœur fournit.
type Deps struct {
	Store    *store.Store
	Blobs    *store.BlobStore
	Printing *printing.Service
	Log      *slog.Logger
	Now      func() time.Time
	// QueueInterval : période de la préparation de la file en arrière-plan (défaut 10 s).
	QueueInterval time.Duration
}

// CostFunc calcule le coût d'une masse de matière (chantier C, à brancher au merge) ; ok=false si inconnu.
type CostFunc func(ctx context.Context, material string, grams float64) (cost float64, ok bool)

// Service est le service bobines / AMS / file.
type Service struct {
	d   Deps
	log *slog.Logger

	mu     sync.Mutex
	subs   []func(Event)
	costFn CostFunc

	prepMu sync.Mutex // sérialise la préparation de la file
	wg     sync.WaitGroup
}

// New construit le service et branche l'alerte de matière avant envoi et le décompte en fin d'impression.
func New(d Deps) (*Service, error) {
	if d.Store == nil {
		return nil, errors.New("filament : base requise")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if d.QueueInterval <= 0 {
		d.QueueInterval = 10 * time.Second
	}
	s := &Service{d: d, log: d.Log}
	if d.Printing != nil {
		d.Printing.SetFilamentCheck(s.validationWarnings)
		d.Printing.SetFilamentPreview(s.previewLines)
		d.Printing.AddJobEndHook(s.onJobEnd)
		d.Printing.SetSpoolSource(s.loadedForPrinting)
	}
	return s, nil
}

func (s *Service) now() time.Time { return s.d.Now().UTC() }

// Subscribe enregistre un observateur d'événements (appelé dans une goroutine dédiée à chaque événement).
func (s *Service) Subscribe(fn func(Event)) {
	s.mu.Lock()
	s.subs = append(s.subs, fn)
	s.mu.Unlock()
}

// SetCostFunc branche le calcul de coût du chantier C (purge). Sans lui, le prix/kg de la bobine sert de base.
func (s *Service) SetCostFunc(f CostFunc) {
	s.mu.Lock()
	s.costFn = f
	s.mu.Unlock()
}

func (s *Service) publish(e Event) {
	e.At = s.now()
	s.mu.Lock()
	subs := append([]func(Event){}, s.subs...)
	s.mu.Unlock()
	s.log.Info("événement filament", "kind", e.Kind, "queue", e.QueueID, "print_job", e.PrintJobID, "message", e.Message)
	for _, fn := range subs {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() { _ = recover() }()
			fn(e)
		}()
	}
}

// Run prépare la file d'attente périodiquement jusqu'à l'annulation du contexte. La préparation ne démarre
// jamais d'impression (voir le commentaire du paquet).
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(s.d.QueueInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.wg.Wait()
			return
		case <-t.C:
			if err := s.PrepareQueue(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("préparation de la file", "err", err)
			}
		}
	}
}

// Close attend les observateurs en cours.
func (s *Service) Close() { s.wg.Wait() }
