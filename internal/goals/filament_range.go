package goals

import (
	"strconv"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
)

// NozzleTempRange lit la plage de température de buse (nozzle_temperature_range_low / nozzle_temperature_range_high) du profil
// filament installé de ce nom. ok=false si les profils ou la plage sont indisponibles (profil inconnu, clé absente ou vide).
// Utilisé par internal/calibration pour répartir une série de températures sur la plage réelle du filament.
func (s *Service) NozzleTempRange(filament string) (lo, hi float64, ok bool) {
	s.load()
	if s.prof == nil || filament == "" {
		return 0, 0, false
	}
	r, err := s.prof.Resolve(profiles.KindFilament, filament)
	if err != nil || r == nil {
		return 0, 0, false
	}
	lo, ok1 := firstNumber(r.Config["nozzle_temperature_range_low"])
	hi, ok2 := firstNumber(r.Config["nozzle_temperature_range_high"])
	if !ok1 || !ok2 || lo <= 0 || hi <= lo {
		return 0, 0, false
	}
	return lo, hi, true
}

// firstNumber extrait le premier nombre d'un paramètre BBL (scalaire ou liste de chaînes, une par extrudeur).
func firstNumber(v any) (float64, bool) {
	var raw string
	switch x := v.(type) {
	case string:
		raw = x
	case []any:
		if len(x) == 0 {
			return 0, false
		}
		s, isStr := x[0].(string)
		if !isStr {
			return 0, false
		}
		raw = s
	default:
		return 0, false
	}
	f, err := strconv.ParseFloat(raw, 64)
	return f, err == nil
}
