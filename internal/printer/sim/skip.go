package sim

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Ignorer un objet (skip_objects, SK0) : le simulateur accepte la commande pendant l'impression (RUNNING) et la refuse
// sinon, met à jour print.s_obj dans le rapport, et l'objet ignoré cesse de « consommer » (ObjectConsumed). Il sait
// aussi ignorer la commande sans rien répondre (FaultIgnoreSkip) pour tester le statut « envoyée, non confirmée ».

// FaultIgnoreSkip : skip_objects est reçu et ignoré (aucun effet, aucune réponse, s_obj inchangé).
const FaultIgnoreSkip = "ignore_skip_objects"

// Object décrit un objet de la plaque simulée (identifiant d'étiquetage, masse prévue en grammes).
type Object struct {
	ID    int
	Name  string
	Grams float64
}

// SetObjects déclare les objets de la plaque : skip_objects refuse alors un identifiant inconnu. Sans appel, tout
// identifiant est accepté.
func (s *Sim) SetObjects(objs []Object) {
	s.mu.Lock()
	s.p.objects = append([]Object(nil), objs...)
	s.mu.Unlock()
}

// SkippedObjects renvoie les identifiants ignorés (ordre d'ignorance).
func (s *Sim) SkippedObjects() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int(nil), s.p.skipped...)
}

// ObjectConsumed : grammes déjà « imprimés » de l'objet ; figés au moment où il est ignoré.
func (s *Sim) ObjectConsumed(id int) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range s.p.objects {
		if o.ID != id {
			continue
		}
		if f, ok := s.p.skipFrac[id]; ok {
			return o.Grams * f
		}
		return o.Grams * float64(s.p.percent) / 100
	}
	return 0
}

// SkipPayloads : les charges utiles skip_objects reçues, en JSON (clés triées), pour vérifier leur forme exacte.
func (s *Sim) SkipPayloads() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.skipRaw...)
}

func (s *Sim) onSkipObjects(seq string, p map[string]any) {
	s.mu.Lock()
	b, _ := json.Marshal(map[string]any{"print": p})
	s.skipRaw = append(s.skipRaw, string(b))
	ignore := s.faults[FaultIgnoreSkip]
	s.mu.Unlock()
	if ignore {
		return
	}
	var ids []int
	if l, ok := p["obj_list"].([]any); ok {
		for _, v := range l {
			if f, ok := v.(float64); ok {
				ids = append(ids, int(f))
			}
		}
	}
	s.mu.Lock()
	err := s.p.skipObjects(ids)
	s.mu.Unlock()
	s.respond("skip_objects", seq, err)
	s.publishDelta(false)
}

func (m *model) skipObjects(ids []int) error {
	if m.gcodeState != "RUNNING" {
		return errors.New("not printing")
	}
	if len(ids) == 0 {
		return errors.New("empty obj_list")
	}
	known := func(id int) bool {
		if len(m.objects) == 0 {
			return true
		}
		for _, o := range m.objects {
			if o.ID == id {
				return true
			}
		}
		return false
	}
	for _, id := range ids {
		if !known(id) {
			return fmt.Errorf("unknown object %d", id)
		}
	}
	if m.skipFrac == nil {
		m.skipFrac = map[int]float64{}
	}
	for _, id := range ids {
		if _, done := m.skipFrac[id]; done {
			continue
		}
		m.skipFrac[id] = float64(m.percent) / 100
		m.skipped = append(m.skipped, id)
	}
	return nil
}
