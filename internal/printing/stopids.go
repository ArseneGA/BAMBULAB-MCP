package printing

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

// goneStop : demande d'arrêt qui n'existe plus (expirée ou utilisée), gardée 24 h pour expliquer le refus.
type goneStop struct {
	reason string // "expired" | "used"
	at     time.Time
}

// newStopID : identifiant de lien /stop/{id}. Alphanumérique uniquement après le préfixe (hexadécimal) : un
// lien collé dans un chat ne perd jamais de caractère final (le « _ » ou « - » final d'un jeton base64 était
// retiré à l'affichage et cassait le lien).
func newStopID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "stp_" + hex.EncodeToString(b)
}

// trimLinkPunct retire la ponctuation finale qu'un affichage de chat ou une phrase ajoute ou retire.
func trimLinkPunct(id string) string {
	return strings.TrimRight(strings.TrimSpace(id), "._-,;:!?)»\"'")
}

// lookupStop retrouve une demande d'arrêt, avec tolérance pour l'ancien format (jeton base64 dont le dernier
// caractère « _ » ou « - » a été retiré par l'affichage). Retourne l'identifiant réel, la demande, et une
// phrase d'explication si elle n'est pas utilisable ("" si valide).
func (s *Service) lookupStop(id string) (string, stopReq, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id = strings.TrimSpace(id)
	r, ok := s.stops[id]
	real := id
	if !ok { // ancien format : l'identifiant reçu est un préfixe de l'identifiant stocké, le reste = ponctuation
		for k, v := range s.stops {
			if strings.HasPrefix(k, id) && strings.Trim(k[len(id):], "_-.") == "" && len(k) > len(id) {
				r, ok, real = v, true, k
				break
			}
		}
	}
	if !ok {
		for _, cand := range []string{id, trimLinkPunct(id)} {
			for k, g := range s.stopGone {
				if k == cand || (strings.HasPrefix(k, cand) && strings.Trim(k[len(cand):], "_-.") == "") {
					return k, stopReq{}, stopGoneMessage(g.reason, "")
				}
			}
		}
		return id, stopReq{}, stopGoneMessage("unknown", "")
	}
	if !s.now().Before(r.expires) {
		return real, stopReq{}, stopGoneMessage("expired", "")
	}
	return real, r, ""
}

// stopGoneMessage : explication précise d'un lien d'arrêt inutilisable.
func stopGoneMessage(reason string, state contracts.PrintState) string {
	switch reason {
	case "expired":
		return "ce lien d'arrêt a expiré (il vaut 10 minutes) ; redemande l'arrêt avec cancel_print pour en obtenir un nouveau"
	case "used":
		return "ce lien d'arrêt a déjà été utilisé : l'arrêt a été effectué"
	case "finished":
		return "l'impression n'est plus en cours (état " + string(state) + ") : il n'y a plus rien à arrêter"
	}
	return "lien d'arrêt inconnu de ce serveur (identifiant mal recopié, ou service redémarré depuis la demande) ; redemande l'arrêt avec cancel_print"
}
