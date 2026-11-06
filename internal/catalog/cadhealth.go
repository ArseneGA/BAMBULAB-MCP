package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Vérification RÉELLE du backend CAO : la commande `ping` du worker importe CadQuery/OCP (voir
// workers/geometry/PROTOCOL.md). Le résultat est mis en cache (long si succès, court si échec) et rafraîchi
// en arrière-plan : GET /api/capabilities ne bloque jamais sur un démarrage à froid de uv.
const (
	cadOKTTL      = 10 * time.Minute
	cadFailTTL    = 30 * time.Second
	cadPingBudget = 2 * time.Minute
)

type cadHealth struct {
	mu      sync.Mutex
	known   bool
	ok      bool
	note    string
	at      time.Time
	running bool
}

// CADHealth dit si le backend CAO fonctionne VRAIMENT (uv, projet, CadQuery importable), sans bloquer. Tant
// que le premier ping n'a pas répondu : (false, « vérification en cours »). Un worker injecté (tests) est
// considéré disponible.
func (s *Service) CADHealth() (available bool, note string) {
	if s.cfg.Worker != nil {
		return true, "worker injecté"
	}
	if _, _, err := s.stepBackend(); err != nil {
		return false, err.Error() // uv ou projet absent : inutile de lancer un ping
	}
	h := &s.cad
	h.mu.Lock()
	defer h.mu.Unlock()
	ttl := cadFailTTL
	if h.ok {
		ttl = cadOKTTL
	}
	if (!h.known || time.Since(h.at) > ttl) && !h.running {
		h.running = true
		go s.runCADPing()
	}
	if !h.known {
		return false, "vérification de CadQuery en cours (commande ping du worker)"
	}
	return h.ok, h.note
}

// WarmCAD lance la vérification sans attendre (démarrage du service).
func (s *Service) WarmCAD() { s.CADHealth() }

func (s *Service) runCADPing() {
	ctx, cancel := context.WithTimeout(context.Background(), cadPingBudget)
	defer cancel()
	ok, note := false, ""
	raw, err := s.worker()(ctx, "ping", map[string]any{})
	if err != nil {
		note = "CadQuery indisponible : " + err.Error()
	} else {
		var res struct {
			CadQuery string `json:"cadquery"`
		}
		if json.Unmarshal(raw, &res) == nil && res.CadQuery != "" {
			ok, note = true, fmt.Sprintf("CadQuery %s vérifié (ping du worker)", res.CadQuery)
		} else {
			note = "CadQuery indisponible : réponse du ping inattendue"
		}
	}
	h := &s.cad
	h.mu.Lock()
	h.known, h.ok, h.note, h.at, h.running = true, ok, note, time.Now(), false
	h.mu.Unlock()
}
