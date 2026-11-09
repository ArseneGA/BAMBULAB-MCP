package camera

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"sync"
	"time"
)

// Instantané PARTAGÉ pour le suivi en direct (vue MCP App « print-live », repli de get_print_status).
//
// La caméra d'une P1/A1 (port 6000) n'accepte que très peu de clients : plusieurs vues ouvertes, ou une vue et le modèle
// de langage, ne doivent pas ouvrir chacun une connexion. Tout appel passe donc par LiveFrame : UNE capture est faite
// par fenêtre de LiveWindow et par imprimante, quel que soit le nombre d'appelants ; les autres reçoivent la même image.

// LiveWindow : durée pendant laquelle une capture de suivi en direct est réutilisée.
const LiveWindow = 2 * time.Second

// liveErrWindow : durée pendant laquelle un échec de capture est rendu tel quel (on ne martèle pas une caméra en panne).
const liveErrWindow = 3 * time.Second

type liveEntry struct {
	photo *Photo
	err   error
	at    time.Time
}

// LiveFrame renvoie une image de moins de LiveWindow : celle de la capture partagée, ou une nouvelle (une seule à la fois par
// imprimante, sous le même verrou que Capture). cached vaut true quand l'image vient d'une capture déjà faite par un autre appel.
// L'image n'est jamais enregistrée.
func (s *Service) LiveFrame(ctx context.Context, printerID string) (p *Photo, cached bool, err error) {
	t, err := s.c.Backend.Target(ctx, printerID)
	if err != nil {
		return nil, false, err
	}
	s.mu.Lock()
	mu := s.snap[printerID]
	if mu == nil {
		mu = &sync.Mutex{}
		s.snap[printerID] = mu
	}
	s.mu.Unlock()
	mu.Lock()
	defer mu.Unlock()

	now := s.now()
	s.mu.Lock()
	e, ok := s.live[printerID]
	s.mu.Unlock()
	if ok {
		if e.err == nil && now.Sub(e.at) < LiveWindow {
			cp := *e.photo
			return &cp, true, nil
		}
		if e.err != nil && now.Sub(e.at) < liveErrWindow {
			return nil, true, e.err
		}
	}
	if !s.allow(printerID, "live") {
		return nil, false, ErrRateLimited
	}
	f, err := s.frame(ctx, t, LiveWindow)
	if err == nil {
		var cfg image.Config
		if cfg, err = checkJPEG(f.JPEG); err == nil {
			sum := sha256.Sum256(f.JPEG)
			ph := &Photo{PrinterID: printerID, Kind: "snapshot", SHA256: hex.EncodeToString(sum[:]), Size: len(f.JPEG), TakenAt: f.At, Width: cfg.Width, Height: cfg.Height, JPEG: f.JPEG}
			s.rememberLive(printerID, liveEntry{photo: ph, at: s.now()})
			cp := *ph
			return &cp, false, nil
		}
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) { // l'annulation d'un appelant n'est pas une panne
		s.rememberLive(printerID, liveEntry{err: err, at: s.now()})
	}
	return nil, false, err
}

func (s *Service) rememberLive(printerID string, e liveEntry) {
	s.mu.Lock()
	if s.live == nil {
		s.live = map[string]liveEntry{}
	}
	s.live[printerID] = e
	s.mu.Unlock()
}
