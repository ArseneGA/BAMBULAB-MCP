package printer

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// LinkKind dit par où le serveur atteint une imprimante.
//
//   - direct : bambud parle lui-même à l'imprimante (MQTT TLS 8883, FTPS 990, caméra 6000) et garde le code
//     d'accès LAN. C'est le comportement historique et le défaut.
//   - agent  : un agent local (connexion SORTANTE vers le serveur, protocole /agent/v2) parle à l'imprimante et
//     garde seul le code d'accès ; le serveur ne reçoit jamais ce code.
type LinkKind string

const (
	LinkDirect LinkKind = "direct"
	LinkAgent  LinkKind = "agent"
)

// ParseLinkKind lit le champ link d'une imprimante ; vide = direct.
func ParseLinkKind(s string) (LinkKind, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", string(LinkDirect):
		return LinkDirect, nil
	case string(LinkAgent):
		return LinkAgent, nil
	}
	return "", fmt.Errorf("printer: link %q inconnu (direct | agent)", s)
}

// Link est le transport d'une imprimante : toutes les opérations que le service d'impression attend (état, dépôt,
// lancement, pause/reprise/arrêt, réconciliation) plus l'instantané caméra. Deux implémentations : DirectLink
// (l'adaptateur Bambu LAN, par défaut) et celle « via agent » du paquet agentv2.
type Link interface {
	Adapter
	Reconciler
	Kind() LinkKind
	// Snapshot renvoie une image JPEG de la caméra (ErrCameraUnsupported si le transport ne la sert pas).
	Snapshot(ctx context.Context) ([]byte, error)
}

// DirectLink est le transport direct : l'adaptateur Bambu LAN, inchangé, vu comme un Link.
// Les méthodes de *Bambu (Connect, Close...) sont promues.
type DirectLink struct {
	*Bambu

	camOnce sync.Once
	cam     *CameraClient
	camErr  error
}

// NewDirectLink enveloppe un adaptateur Bambu LAN.
func NewDirectLink(b *Bambu) *DirectLink { return &DirectLink{Bambu: b} }

var _ Link = (*DirectLink)(nil)

// Kind : direct.
func (d *DirectLink) Kind() LinkKind { return LinkDirect }

// Snapshot lit une image sur le port caméra de l'imprimante (flux JPEG des P1/A1), avec la même politique TLS
// épinglée et le même code d'accès que l'adaptateur.
func (d *DirectLink) Snapshot(ctx context.Context) ([]byte, error) {
	d.camOnce.Do(func() {
		d.cam, d.camErr = NewCameraClient(CameraConfig{Host: d.cfg.Host, Secrets: d.cfg.Secrets, TLS: d.cfg.TLS, Logger: d.cfg.Logger})
	})
	if d.camErr != nil {
		return nil, d.camErr
	}
	return d.cam.Snapshot(ctx)
}
