package mesh

import "fmt"

// Surplombs à croiser avec le réglage de supports : HEURISTIQUE tirée de la géométrie seule.
//
// Seuils d'avertissement (documentés, ajustables ici) : sans supports, la pièce est signalée dès que
//   - la surface cumulée des zones en surplomb dépasse overhangTotalWarnMM2 (200 mm²), OU
//   - une seule zone en surplomb dépasse overhangRegionWarnMM2 (100 mm²).
//
// Le seuil d'angle est celui du profil de découpe (support_threshold_angle : pente depuis l'horizontale en dessous
// de laquelle Bambu Studio soutient) ; sans valeur exploitable (0 = automatique), le défaut est de 45°, comme
// pour analyze_geometry.
const (
	overhangTotalWarnMM2  = 200.0
	overhangRegionWarnMM2 = 100.0
)

// OverhangSupportCheck : surplombs d'une pièce posée, au seuil d'angle d'un profil.
type OverhangSupportCheck struct {
	SupportThresholdDeg float64 `json:"support_threshold_angle_deg" jsonschema:"seuil du profil : pente depuis l'horizontale sous laquelle Studio soutient"`
	AreaMM2             float64 `json:"overhang_area_mm2" jsonschema:"surface cumulée des faces en surplomb au-delà du seuil"`
	LargestRegionMM2    float64 `json:"largest_region_mm2" jsonschema:"plus grande zone en surplomb"`
	Regions             int     `json:"regions"`
	Significant         bool    `json:"significant" jsonschema:"surface cumulée > 200 mm² ou une zone > 100 mm² : sans supports, avertissement fort"`
}

// OverhangsForSupport mesure les surplombs d'une pièce (posée Z vers le haut) au seuil support_threshold_angle d'un
// profil Bambu (degrés depuis l'horizontale ; ≤ 0 ou ≥ 90 : défaut de 45°).
func OverhangsForSupport(m *Mesh, supportThresholdDeg float64) *OverhangSupportCheck {
	if m == nil || len(m.T) == 0 {
		return nil
	}
	if supportThresholdDeg <= 0 || supportThresholdDeg >= 90 {
		supportThresholdDeg = 90 - defaultOverhangFromVerticalDeg
	}
	bb := m.Bounds()
	tol := 1e-6 * bb.Size.Len()
	if tol < 1e-6 {
		tol = 1e-6
	}
	remap, nw := weld32(m.V, tol)
	rep := overhangs(m, remap, nw, Vec3{0, 0, 1}, 90-supportThresholdDeg, true, 1)
	c := &OverhangSupportCheck{SupportThresholdDeg: supportThresholdDeg, AreaMM2: round1(rep.AreaMM2), Regions: rep.RegionCount}
	if len(rep.Regions) > 0 {
		c.LargestRegionMM2 = round1(rep.Regions[0].AreaMM2)
	}
	c.Significant = c.AreaMM2 > overhangTotalWarnMM2 || c.LargestRegionMM2 > overhangRegionWarnMM2
	return c
}

// UnsupportedSummary : phrase d'avertissement quand les surplombs sont significatifs et que les supports manquent
// (vide si aucun risque).
func (c *OverhangSupportCheck) UnsupportedSummary(supportsOn bool) string {
	if c == nil || !c.Significant || supportsOn {
		return ""
	}
	return fmt.Sprintf("Surplombs SANS supports : %.0f mm² de faces en surplomb au-delà de %.0f° (%d zone(s), la plus grande %.0f mm²) alors que les supports sont désactivés (seuils d'alerte : > %.0f mm² cumulés ou une zone > %.0f mm²). "+
		"Ces zones risquent de s'affaisser ou de faire des fils : activer les supports (enable_support) ou réorienter la pièce.",
		c.AreaMM2, c.SupportThresholdDeg, c.Regions, c.LargestRegionMM2, overhangTotalWarnMM2, overhangRegionWarnMM2)
}
