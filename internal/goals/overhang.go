package goals

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// MetricUnsupportedOverhang : surface en surplomb sans supports (mm²), colonne du tableau de compare_variants.
const MetricUnsupportedOverhang = "unsupported_overhang_mm2"

// minOverhangForCoolingNoteMM2 : en dessous, la pièce n'a pas de surplombs qui justifient d'expliquer l'effet de la
// ventilation ou de la température.
const minOverhangForCoolingNoteMM2 = 50.0

// fanKeys : réglages de refroidissement de la pièce (baisser l'un d'eux dégrade les surplombs et les ponts).
var fanKeys = []string{"fan_min_speed", "fan_max_speed", "overhang_fan_speed", "additional_cooling_fan_speed"}

func firstNum(r *profiles.Resolved, key string) (float64, bool) {
	if r == nil {
		return 0, false
	}
	for _, s := range r.Strings(key) {
		if f, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%")), 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

func supportsEnabled(p *profiles.Resolved) bool {
	v, _ := slicer.Effective(p, "enable_support")
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// overhangInsight croise les surplombs de la pièce avec le réglage de supports d'un candidat (surface non supportée,
// avertissement fort) et explique l'effet d'une ventilation réduite ou d'une température relevée sur ces surplombs.
// baseF / candF : premier filament de la base et du candidat ; candP : process du candidat.
func overhangInsight(m *mesh.Mesh, baseF, candF, candP *profiles.Resolved) (metric *Metric, warnings []string) {
	if m == nil || len(m.T) == 0 || candP == nil {
		return nil, nil
	}
	angle := 0.0
	if v, _ := slicer.Effective(candP, "support_threshold_angle"); v != "" {
		angle, _ = strconv.ParseFloat(strings.TrimSpace(v), 64)
	}
	ov := mesh.OverhangsForSupport(m, angle)
	if ov == nil {
		return nil, nil
	}
	on := supportsEnabled(candP)
	unsupported := 0.0
	note := fmt.Sprintf("supports activés : surplombs au-delà de %.0f° soutenus (%.0f mm² en surplomb)", ov.SupportThresholdDeg, ov.AreaMM2)
	if !on {
		unsupported = ov.AreaMM2
		note = fmt.Sprintf("supports désactivés : %.0f mm² de faces en surplomb au-delà de %.0f° (%d zone(s), la plus grande %.0f mm²), seuil du profil", ov.AreaMM2, ov.SupportThresholdDeg, ov.Regions, ov.LargestRegionMM2)
	}
	metric = &Metric{Value: round4(unsupported), Unit: "mm²", Label: LabelGeometry, Note: "surface en surplomb non supportée : " + note + ", heuristique de internal/mesh"}
	if s := ov.UnsupportedSummary(on); s != "" {
		warnings = append(warnings, "ATTENTION : "+s)
	}
	if ov.AreaMM2 >= minOverhangForCoolingNoteMM2 {
		var cut []string
		for _, k := range fanKeys {
			b, okb := firstNum(baseF, k)
			c, okc := firstNum(candF, k)
			if okb && okc && c < b {
				cut = append(cut, fmt.Sprintf("%s %.0f → %.0f %%", k, b, c))
			}
		}
		if len(cut) > 0 {
			warnings = append(warnings, fmt.Sprintf("ventilation réduite (%s) : surplombs dégradés (%.0f mm² en surplomb sur cette pièce) ; un refroidissement plus faible refroidit moins vite la matière déposée dans le vide", strings.Join(cut, ", "), ov.AreaMM2))
		}
		if b, okb := firstNum(baseF, "nozzle_temperature"); okb {
			if c, okc := firstNum(candF, "nozzle_temperature"); okc && c > b {
				warnings = append(warnings, fmt.Sprintf("température de buse relevée (%.0f → %.0f °C) : matière plus fluide, surplombs et ponts dégradés (%.0f mm² en surplomb sur cette pièce)", b, c, ov.AreaMM2))
			}
		}
	}
	return metric, warnings
}

// sourceMesh lit le maillage source figé de l'expérience (nil si illisible).
func (s *Service) sourceMesh(exp *Experiment) *mesh.Mesh {
	if s.cfg.Blobs == nil || exp.Details.Source.SHA256 == "" {
		return nil
	}
	data, err := s.readBlob(exp.Details.Source.SHA256)
	if err != nil {
		return nil
	}
	md, err := mesh.Parse(exp.Details.Source.Name, data)
	if err != nil {
		return nil
	}
	return md.Combined()
}

// candidateInsight applique overhangInsight à un candidat de l'expérience (orientation proposée comprise).
func (s *Service) candidateInsight(exp *Experiment, src *mesh.Mesh, c CandidateView) (*Metric, []string) {
	if src == nil || s.prof == nil {
		return nil, nil
	}
	base := exp.Details.Base
	_, _, bfs, _, err := slicer.ResolveWithOverrides(s.prof, base.Machine, base.Process, base.Filaments, base.Overrides)
	if err != nil || len(bfs) == 0 {
		return nil, nil
	}
	all := append(append([]slicer.Override{}, base.Overrides...), c.Overrides...)
	_, cp, cfs, _, err := slicer.ResolveWithOverrides(s.prof, base.Machine, base.Process, base.Filaments, all)
	if err != nil || len(cfs) == 0 {
		return nil, nil
	}
	m := src
	if c.Orientation != nil {
		m = src.Transform(mesh.Matrix(c.Orientation.Rotation))
	}
	return overhangInsight(m, bfs[0], cfs[0], cp)
}
