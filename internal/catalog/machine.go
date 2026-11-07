package catalog

import (
	"errors"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
)

// SetProfiles branche la source des profils Bambu Studio (chargée à la demande par le cœur) :
// cibles machine (volumes, buses, zones d'exclusion) et seuil de surplomb des presets process.
// f peut renvoyer nil (profils indisponibles) : les cibles passent alors au repli nominal.
func (s *Service) SetProfiles(f func() *profiles.Store) {
	s.mu.Lock()
	s.profFn = f
	s.mu.Unlock()
}

func (s *Service) profileStore() *profiles.Store {
	s.mu.RLock()
	f := s.profFn
	s.mu.RUnlock()
	if f == nil {
		return nil
	}
	return f()
}

// machineTarget résout la cible machine de l'analyse : profils Bambu Studio d'abord (tous les modèles
// de l'installation), repli nominal explicitement marqué quand les profils sont absents.
func (s *Service) machineTarget(name string, nozzle float64) (mesh.Target, string, error) {
	id := mesh.NormalizeMachineName(name)
	if id == "" {
		name = "p1s"
		id = "p1s"
	}
	if ps := s.profileStore(); ps != nil {
		t, err := ps.TargetFor(name, nozzle)
		if err == nil {
			return t, id, nil
		}
		if errors.Is(err, profiles.ErrNotFound) {
			return mesh.Target{}, "", invalidf("machine inconnue %q : %v", name, err)
		}
		// profils illisibles : repli nominal (signalé dans le rapport)
	}
	t, ok := mesh.NominalTarget(name)
	if !ok {
		return mesh.Target{}, "", invalidf("machine inconnue %q (profils Bambu Studio indisponibles ; repli nominal : %s)", name, strings.Join(mesh.NominalNames(), ", "))
	}
	if nozzle > 0 {
		t.Nozzle = nozzle
	}
	return t, id, nil
}
