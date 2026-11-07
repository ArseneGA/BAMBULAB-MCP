package printing

import (
	"context"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

// Évènements d'impression à destination de paquets voisins (caméra, notifications) : ils OBSERVENT les transitions, ils ne
// peuvent ni les provoquer ni les empêcher. Les abonnés sont appelés dans une goroutine, avec récupération de panique.

// Event est une transition de la machine d'états d'un PrintJob.
type Event struct {
	JobID     string
	PrinterID string
	Name      string // contracts.PrintEvent
	From      contracts.PrintState
	To        contracts.PrintState
	Note      string
	JobName   string
	At        time.Time
}

// OnEvent abonne fn aux transitions des impressions.
func (s *Service) OnEvent(fn func(Event)) {
	s.evMu.Lock()
	s.evSubs = append(s.evSubs, fn)
	s.evMu.Unlock()
}

func (s *Service) emit(j Job, ev contracts.PrintEvent, from contracts.PrintState, note string) {
	s.evMu.Lock()
	subs := make([]func(Event), len(s.evSubs))
	copy(subs, s.evSubs)
	s.evMu.Unlock()
	if len(subs) == 0 {
		return
	}
	e := Event{JobID: j.ID, PrinterID: j.PrinterID, Name: string(ev), From: from, To: j.State, Note: note, JobName: j.Name, At: s.now()}
	for _, fn := range subs {
		fn := fn
		go func() {
			defer func() {
				if r := recover(); r != nil {
					s.log.Warn("abonné d'évènement d'impression en panique", "panic", r)
				}
			}()
			fn(e)
		}()
	}
}

// PlatePhoto est la photo du plateau montrée sur la page de confirmation, pour que l'humain vérifie qu'il est vide.
type PlatePhoto struct {
	URL     string    // adresse de l'image (route servie derrière le cookie d'un navigateur enregistré)
	TakenAt time.Time // horodatage de la prise de vue
	Err     string    // raison, si la photo est indisponible
}

// SetPlatePhotoProvider branche la prise de photo à l'ouverture de /confirm/{id}. Sans fournisseur la page n'en montre pas.
// La photo aide l'humain à décider ; elle ne remplace jamais son attestation « plateau vide » (l'imprimante, elle, ne sait pas).
func (s *Service) SetPlatePhotoProvider(fn func(ctx context.Context, printerID string) PlatePhoto) {
	s.evMu.Lock()
	s.platePhoto = fn
	s.evMu.Unlock()
}

func (s *Service) platePhotoFor(ctx context.Context, printerID string) *PlatePhoto {
	s.evMu.Lock()
	fn := s.platePhoto
	s.evMu.Unlock()
	if fn == nil {
		return nil
	}
	p := fn(ctx, printerID)
	return &p
}
