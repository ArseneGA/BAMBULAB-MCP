package engineering

import "math"

// WallResult : Value = nombre de périmètres complets (moteur classique).
type WallResult struct {
	Result
	Complete      int       `json:"complete_perimeters"`
	Remainder     float64   `json:"remainder_mm"`
	ArachneLines  int       `json:"arachne_lines"`
	ArachneWidth  float64   `json:"arachne_line_width_mm"`
	ArachneOK     bool      `json:"arachne_width_in_tolerance"`
	NextThickness []float64 `json:"nearby_good_thicknesses_mm"`
}

// WallCheck analyse une épaisseur de paroi pour une largeur de ligne donnée.
// Moteur classique : n = floor(e/lw), reste = e − n·lw (reste > 0,25·lw → trou ou sur-extrusion probable).
// Arachne : n = max(1, round(e/lw)), largeur e/n tolérée dans [0,6·lw ; 1,5·lw].
func WallCheck(thickness, lineWidth float64) (*WallResult, error) {
	if err := positive("épaisseur", thickness); err != nil {
		return nil, err
	}
	if err := positive("largeur de ligne", lineWidth); err != nil {
		return nil, err
	}
	if lineWidth < 0.1 || lineWidth > 2 {
		return nil, invalid("largeur de ligne %.2f mm hors domaine 0,1–2 mm", lineWidth)
	}
	n := int(math.Floor(thickness/lineWidth + 1e-9))
	rem := thickness - float64(n)*lineWidth
	if rem < 1e-9 {
		rem = 0
	}
	an := int(math.Max(1, math.Round(thickness/lineWidth)))
	aw := thickness / float64(an)
	aok := aw >= 0.6*lineWidth && aw <= 1.5*lineWidth
	var warns []string
	if thickness < lineWidth {
		warns = append(warns, "Épaisseur < largeur de ligne : paroi non imprimable de façon fiable (au mieux une ligne amincie).")
	}
	if thickness < 2*lineWidth {
		warns = append(warns, "Épaisseur < 2 lignes : au plus 1 périmètre, pièce fragile.")
	}
	if n >= 1 && rem > 0.25*lineWidth {
		warns = append(warns, "Moteur classique : reste "+ftoa(round(rem, 3))+" mm > 0,25 × largeur de ligne, trou ou sur-extrusion probable.")
	}
	if !aok {
		warns = append(warns, "Arachne : largeur effective "+ftoa(round(aw, 3))+" mm hors de [0,6 ; 1,5] × largeur de ligne.")
	}
	var near []float64
	for k := 1; k <= 6; k++ {
		near = append(near, round(float64(k)*lineWidth, 3))
	}
	r := &WallResult{Complete: n, Remainder: round(rem, 4), ArachneLines: an, ArachneWidth: round(aw, 4), ArachneOK: aok, NextThickness: near}
	r.Result = Result{
		Value: float64(n), Unit: "périmètres",
		Assumptions: []string{
			"Paroi droite d'épaisseur constante remplie uniquement de périmètres",
			"Largeur de ligne effective supposée égale à la valeur fournie (réglage du slicer)",
		},
		Validity: "Parois 0,2–10 mm, buse 0,2–0,8 mm. " + Disclaimer,
		Level:    LevelCalculated, Warnings: warns,
		Sources: []Source{{Title: "Géométrie des périmètres FDM (calcul direct) ; seuils Arachne d'après les tolérances usuelles de Bambu Studio / OrcaSlicer, heuristiques", Date: "2026-11-06"}},
	}
	return r, nil
}

// OverhangStatus est le verdict d'un surplomb ou pont.
type OverhangStatus string

const (
	OverhangOK       OverhangStatus = "ok"
	OverhangDegraded OverhangStatus = "degraded"
	OverhangSupports OverhangStatus = "supports_recommended"
)

// OverhangParams : angle depuis la verticale (0° = paroi verticale, 90° = plafond horizontal).
type OverhangParams struct {
	AngleDeg     float64
	BridgeLength float64 // mm, 0 = pas de pont
	Material     string
}

// OverhangResult : Value = angle maximal conseillé du matériau (°).
type OverhangResult struct {
	Result
	AngleStatus  OverhangStatus `json:"angle_status"`
	BridgeStatus OverhangStatus `json:"bridge_status"`
	Supports     bool           `json:"supports_recommended"`
	MaxBridgeMM  float64        `json:"max_bridge_mm"`
}

// OverhangBridge : OK ≤ 45° ; entre 45° et max matériau : qualité dégradée ; au-delà : supports.
// Pont : OK si longueur ≤ max_bridge du matériau, sinon supports.
func OverhangBridge(p OverhangParams) (*OverhangResult, error) {
	if math.IsNaN(p.AngleDeg) || p.AngleDeg < 0 || p.AngleDeg > 90 {
		return nil, invalid("angle %v hors [0 ; 90]° (depuis la verticale)", p.AngleDeg)
	}
	if math.IsNaN(p.BridgeLength) || p.BridgeLength < 0 {
		return nil, invalid("longueur de pont négative")
	}
	m, err := GetMaterial(p.Material)
	if err != nil {
		return nil, err
	}
	u := newUsed()
	maxA, err := m.need("max_overhang_deg", u)
	if err != nil {
		return nil, err
	}
	maxB, err := m.need("max_bridge_mm", u)
	if err != nil {
		return nil, err
	}
	as := OverhangOK
	switch {
	case p.AngleDeg <= 45:
	case p.AngleDeg <= maxA:
		as = OverhangDegraded
	default:
		as = OverhangSupports
	}
	bs := OverhangOK
	if p.BridgeLength > maxB {
		bs = OverhangSupports
	}
	var warns []string
	if as == OverhangDegraded {
		warns = append(warns, "Surplomb au-delà de 45° : qualité de la face inférieure dégradée (affaissement, fils lâches).")
	}
	if as == OverhangSupports || bs == OverhangSupports {
		warns = append(warns, "Supports recommandés (ou changement d'orientation / chanfrein 45°).")
	}
	level := u.level
	if level == LevelCalculated {
		level = LevelHeuristic // les seuils fabricant sont des réglages, pas des mesures normées
	}
	return &OverhangResult{
		AngleStatus: as, BridgeStatus: bs, Supports: as == OverhangSupports || bs == OverhangSupports, MaxBridgeMM: maxB,
		Result: Result{
			Value: maxA, Unit: "deg",
			Assumptions: []string{
				"Angle mesuré depuis la verticale ; refroidissement standard de la P1S, buse 0,4 mm, hauteur de couche ≤ 0,2 mm",
				"Seuils matériau = réglages recommandés par le fabricant, 45° = seuil de bonne qualité courant",
			},
			Validity: "Surplombs droits et ponts plans non étayés de largeur ≥ 1 ligne. " + Disclaimer,
			Level:    level, Sources: u.sources, Warnings: warns,
		},
	}, nil
}
