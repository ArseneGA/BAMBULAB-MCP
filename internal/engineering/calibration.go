package engineering

import (
	"math"
)

// Calibration regroupe les corrections MESURÉES par l'utilisateur sur sa propre machine (imprimante + buse +
// matériau/bobine), issues de pièces de calibration imprimées (voir internal/calibration). Quand une calibration est
// fournie, elle PRIME sur les valeurs génériques des fonctions *Cal ci-dessous ; les champs absents (nil) retombent sur
// le générique. Aucune importation de base de données ici : ce paquet reste pur.
type Calibration struct {
	// Provenance : « calibré le 2026-11-07 (P1S, buse 0,4, PLA, Bambu PLA Basic) ».
	Provenance   string `json:"provenance"`
	CalibratedAt string `json:"calibrated_at"`

	// Jeux d'ajustement (peigne de jeux) : écart de largeur entre encoche et languette de 10 mm.
	SlidesGapMM *float64 `json:"slides_gap_mm,omitempty"`
	SnugGapMM   *float64 `json:"snug_gap_mm,omitempty"`

	// Compensation de trou : d_modélisé = d + off0 + slope·d (mm), par orientation de l'axe.
	HoleVertical   *LinearOffset `json:"hole_vertical,omitempty"`
	HoleHorizontal *LinearOffset `json:"hole_horizontal,omitempty"`
	// Arbres/pions : d_modélisé = d + off0 + slope·d (mm), typiquement ≈ 0 ou légèrement négatif.
	Shaft *LinearOffset `json:"shaft,omitempty"`

	// Retrait : facteur d'échelle à appliquer au modèle (nominal / mesuré), > 1 si la pièce sort trop petite.
	ShrinkXY *float64 `json:"shrink_scale_xy,omitempty"`
	ShrinkZ  *float64 `json:"shrink_scale_z,omitempty"`

	// Retrait mesuré sur DEUX tailles ou plus : le facteur d'échelle (proportionnel à la taille) est séparé du décalage de
	// contour (constant). Décalages par côté, au sens de xy_contour_compensation / xy_hole_compensation des profils BBL
	// (positif = le contour / le trou est agrandi). Jamais appliqués automatiquement.
	ContourCompMM *float64 `json:"xy_contour_compensation_mm,omitempty"`
	HoleCompMM    *float64 `json:"xy_hole_compensation_mm,omitempty"`

	BestTempC       *float64 `json:"best_temp_c,omitempty"`
	BridgeMaxSpanMM *float64 `json:"bridge_max_span_mm,omitempty"`
	OverhangMaxDeg  *float64 `json:"overhang_max_deg,omitempty"`

	// Calibrations d'extrusion : valeur RETENUE sur la série de pièces (une par valeur). Proposées, jamais appliquées.
	BestFlowRatio          *float64       `json:"best_flow_ratio,omitempty"`
	BestPressureAdvance    *float64       `json:"best_pressure_advance,omitempty"`
	BestRetractionMM       *float64       `json:"best_retraction_mm,omitempty"`
	BestMaxVolumetricSpeed *float64       `json:"best_max_volumetric_speed,omitempty"`
	FirstLayer             *FirstLayerCal `json:"first_layer,omitempty"`
}

// FirstLayerCal : réglages de première couche retenus (chaque champ est facultatif, un par série).
type FirstLayerCal struct {
	HeightMM *float64 `json:"initial_layer_print_height,omitempty"`
	SpeedMMS *float64 `json:"initial_layer_speed,omitempty"`
	TempC    *float64 `json:"nozzle_temperature_initial_layer,omitempty"`
}

// LinearOffset : décalage en fonction du diamètre nominal d, off = Offset + Slope·d (mm).
type LinearOffset struct {
	Offset float64 `json:"offset_mm"`
	Slope  float64 `json:"slope"`
	// N : nombre de mesures ayant servi à l'ajustement ; Residual : écart max du modèle aux mesures (mm).
	N        int     `json:"n"`
	Residual float64 `json:"max_residual_mm"`
}

// At retourne le décalage pour le diamètre nominal d.
func (l *LinearOffset) At(d float64) float64 { return l.Offset + l.Slope*d }

func calSource(c *Calibration) Source {
	return Source{ID: "calibration", Title: c.Provenance, Note: "valeurs mesurées par l'utilisateur sur ses pièces de calibration"}
}

// FitClearanceCal : comme FitClearance, mais avec le jeu mesuré quand il existe pour l'ajustement demandé.
//
//   - glissant : jeu où la languette coulisse librement (SlidesGap) ;
//   - serré    : jeu où elle entre avec un léger serrage (SnugGap), à défaut SlidesGap − 0,15 mm ;
//   - libre    : SlidesGap + 0,15 mm ;
//   - press    : jamais calibré par le peigne (valeur générique).
//
// L'orientation horizontale garde son +0,05 mm (le peigne est imprimé à plat, axe d'ajustement vertical).
func FitClearanceCal(p FitParams, cal *Calibration) (*FitResult, error) {
	base, err := FitClearance(p)
	if err != nil {
		return nil, err
	}
	if cal == nil || cal.SlidesGapMM == nil {
		return base, nil
	}
	fit, _ := parseFit(p.Fit)
	var v float64
	var how string
	switch fit {
	case FitSliding:
		v, how = *cal.SlidesGapMM, "jeu glissant mesuré"
	case FitFree:
		v, how = *cal.SlidesGapMM+0.15, "jeu glissant mesuré + 0,15 mm (ajustement libre)"
	case FitSnug:
		if cal.SnugGapMM != nil {
			v, how = *cal.SnugGapMM, "jeu serré mesuré"
		} else {
			v, how = *cal.SlidesGapMM-0.15, "jeu glissant mesuré − 0,15 mm (serré, non mesuré)"
		}
	default:
		return base, nil // press : non calibrable avec le peigne
	}
	axis, _ := parseAxis(p.Orientation)
	if axis == AxisHorizontal {
		v += 0.05
	}
	dimNote := "Dimension d'ajustement non fournie : le jeu mesuré à 10 mm (languette du peigne) est utilisé tel quel"
	if p.Dimension != 0 {
		dd := FitDimensionDelta(p.Dimension)
		v += dd
		dimNote = "Dimension " + ftoa(p.Dimension) + " mm : " + signed(dd) + " mm par rapport au jeu mesuré à 10 mm (loi en ∛D de l'unité de tolérance ISO 286-1, amplitude heuristique)"
	}
	v = round(v, 3)
	out := &FitResult{PerSide: round(v/2, 4)}
	out.Result = Result{
		Value: v, Unit: "mm", Range: &Range{Min: round(v-0.03, 3), Max: round(v+0.03, 3)},
		Assumptions: []string{
			"Calibré : " + how + " (" + cal.Provenance + ")",
			"Valable pour la même imprimante, buse et bobine ; hors de ce cadre, refaire la calibration",
			dimNote,
		},
		Level: LevelMeasured, Sources: []Source{calSource(cal)},
		Validity: "Jeu mesuré sur le peigne de jeux imprimé à plat ; extrapolé aux autres dimensions d'ajustement 3–100 mm par une loi heuristique (voir FitDimensionDelta). " + Disclaimer,
	}
	if (fit == FitSnug && cal.SnugGapMM == nil) || (p.Dimension != 0 && FitDimensionDelta(p.Dimension) != 0) {
		out.Result.Level = LevelCalculated // partie estimée (serré non mesuré, ou loi de dimension heuristique)
	}
	if axis == AxisHorizontal {
		out.Assumptions = append(out.Assumptions, "Axe horizontal : +0,05 mm (affaissement des voûtes)")
	}
	return out, nil
}

// HoleCompensationCal : comme HoleCompensation, avec l'ajustement linéaire mesuré sur les trous de calibration.
// L'écart mesuré inclut déjà le retrait du matériau aux diamètres testés : il n'est pas ajouté une seconde fois.
func HoleCompensationCal(p HoleParams, cal *Calibration) (*Result, error) {
	base, err := HoleCompensation(p)
	if err != nil {
		return nil, err
	}
	if cal == nil {
		return base, nil
	}
	axis, _ := parseAxis(p.Axis)
	off := cal.HoleVertical
	extra := 0.0
	note := ""
	if axis == AxisHorizontal {
		if cal.HoleHorizontal != nil {
			off = cal.HoleHorizontal
		} else if off != nil {
			extra, note = 0.10, "orientation horizontale non mesurée : décalage vertical mesuré + 0,10 mm"
		}
	}
	if off == nil {
		return base, nil
	}
	v := round(p.Diameter+off.At(p.Diameter)+extra, 3)
	lo, hi := math.Min(p.Diameter, 3), 10.0
	asm := []string{
		"Calibré : d modélisé = d + " + ftoa(round(off.Offset, 3)) + " + " + ftoa(round(off.Slope, 4)) + "·d (" + cal.Provenance + ")",
		"Le retrait du matériau est inclus dans la mesure (non ajouté en plus)",
	}
	if note != "" {
		asm = append(asm, note)
	}
	var warns []string
	if p.Diameter < lo*0.8 || p.Diameter > hi*1.2 {
		warns = append(warns, "Diamètre hors de la plage testée (≈ 3–10 mm) : extrapolation de la droite de calibration.")
	}
	res := &Result{
		Value: v, Unit: "mm", Range: &Range{Min: round(v-math.Max(0.03, off.Residual), 3), Max: round(v+math.Max(0.03, off.Residual), 3)},
		Assumptions: asm, Level: LevelMeasured, Sources: []Source{calSource(cal)}, Warnings: warns,
		Validity: "Trous cylindriques de la plage de calibration, même imprimante/buse/bobine. " + Disclaimer,
	}
	if extra != 0 {
		res.Level = LevelCalculated
	}
	return res, nil
}

// ShaftCompensationCal : diamètre à modéliser pour un arbre/pion de diamètre nominal d (sans calibration : d inchangé).
func ShaftCompensationCal(d float64, cal *Calibration) (*Result, error) {
	if err := positive("diamètre", d); err != nil {
		return nil, err
	}
	if cal == nil || cal.Shaft == nil {
		return &Result{Value: round(d, 3), Unit: "mm", Assumptions: []string{"Aucune calibration d'arbres : diamètre inchangé"},
			Level: LevelIndicative, Validity: "Générique. " + Disclaimer}, nil
	}
	v := round(d+cal.Shaft.At(d), 3)
	return &Result{Value: v, Unit: "mm", Range: &Range{Min: round(v-0.03, 3), Max: round(v+0.03, 3)},
		Assumptions: []string{"Calibré : d modélisé = d + " + ftoa(round(cal.Shaft.Offset, 3)) + " + " + ftoa(round(cal.Shaft.Slope, 4)) + "·d (" + cal.Provenance + ")"},
		Level:       LevelMeasured, Sources: []Source{calSource(cal)}, Validity: "Arbres de la plage de calibration. " + Disclaimer}, nil
}

// ScaleCal : facteurs d'échelle de compensation du retrait [xy, z] (1 = aucune correction), avec provenance.
func ScaleCal(material string, cal *Calibration) (xy, z float64, level Level, note string, err error) {
	if cal != nil && (cal.ShrinkXY != nil || cal.ShrinkZ != nil) {
		xy, z = 1, 1
		if cal.ShrinkXY != nil {
			xy = *cal.ShrinkXY
		}
		if cal.ShrinkZ != nil {
			z = *cal.ShrinkZ
		}
		return xy, z, LevelMeasured, "facteurs mesurés sur le cube de 50 mm (" + cal.Provenance + ")", nil
	}
	if material == "" {
		return 1, 1, LevelIndicative, "aucune calibration ni matériau : pas de correction", nil
	}
	r, err := ScaleForShrinkage(material, 100)
	if err != nil {
		return 0, 0, "", "", err
	}
	f := r.Value / 100
	return f, f, r.Level, "retrait générique du matériau " + material, nil
}
