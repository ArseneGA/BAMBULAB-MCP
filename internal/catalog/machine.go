package catalog

import (
	"errors"
	"fmt"
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

// applyProcessThreshold règle le seuil de surplomb d'après support_threshold_angle du preset process (celui
// demandé, sinon le process par défaut de la machine). Retourne une réserve à afficher, vide si tout va bien.
func (s *Service) applyProcessThreshold(fo *mesh.FabOptions, t mesh.Target, process string) string {
	if fo.OverhangAngleDeg > 0 {
		return ""
	}
	ps := s.profileStore()
	if ps == nil {
		return "seuil de surplomb par défaut (45° depuis la verticale) : profils Bambu Studio indisponibles"
	}
	name := strings.TrimSpace(process)
	if name == "" && t.Profile != "" {
		name, _ = ps.DefaultProcessOf(t.Profile)
	}
	if name == "" {
		return "seuil de surplomb par défaut (45° depuis la verticale) : aucun preset process"
	}
	deg, ok, err := ps.SupportThresholdAngle(name)
	switch {
	case err != nil:
		return fmt.Sprintf("seuil de surplomb par défaut (45° depuis la verticale) : process %q illisible (%v)", name, err)
	case !ok:
		return fmt.Sprintf("seuil de surplomb par défaut (45° depuis la verticale) : le process %q ne fixe pas support_threshold_angle (0 = automatique)", name)
	}
	fo.SupportThresholdDeg = deg
	fo.ThresholdSource = fmt.Sprintf("process « %s » (support_threshold_angle = %g°)", name, deg)
	return ""
}
