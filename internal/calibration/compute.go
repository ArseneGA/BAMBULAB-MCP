// Package calibration génère des pièces de calibration pour un couple imprimante + buse + bobine, enregistre les mesures
// de l'utilisateur et en déduit des corrections (jeu effectif, compensation de trou, facteur de retrait, meilleure
// température) stockées par (imprimante, buse, matériau, bobine). internal/engineering et la bibliothèque de gabarits les
// utilisent en priorité (provenance « calibré le … »), sinon les valeurs génériques s'appliquent.
package calibration

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/engineering"
)

// ErrInvalid enveloppe toute entrée invalide (mesure implausible, champ manquant...).
var ErrInvalid = errors.New("entrée invalide")

func invalidf(format string, a ...any) error {
	return fmt.Errorf("%w : %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// DiameterMeasure : une mesure au pied à coulisse d'un trou ou d'un arbre.
type DiameterMeasure struct {
	Nominal  float64 `json:"nominal" jsonschema:"diamètre nominal modélisé, mm"`
	Measured float64 `json:"measured" jsonschema:"diamètre mesuré sur la pièce imprimée, mm (moyenne de deux mesures croisées)"`
	Axis     string  `json:"axis,omitempty" jsonschema:"trous : vertical (plaque à plat, défaut) | horizontal (bloc à trous traversants selon Y)"`
}

// Measures : toutes les mesures possibles ; seules celles fournies sont exploitées.
type Measures struct {
	FitSlidesGapMM  *float64          `json:"fit_slides_gap_mm,omitempty" jsonschema:"peigne de jeux : plus petit jeu (mm) où la languette coulisse librement"`
	FitSnugGapMM    *float64          `json:"fit_snug_gap_mm,omitempty" jsonschema:"peigne de jeux : plus petit jeu où elle entre avec un léger serrage"`
	Holes           []DiameterMeasure `json:"holes,omitempty" jsonschema:"trous de calibration mesurés"`
	Shafts          []DiameterMeasure `json:"shafts,omitempty" jsonschema:"arbres de calibration mesurés"`
	CubeMM          []float64         `json:"cube_mm,omitempty" jsonschema:"cube de retrait : [X, Y, Z] mesurés en mm"`
	CubeNominalMM   float64           `json:"cube_nominal_mm,omitempty" jsonschema:"arête nominale du cube (défaut 50)"`
	BestTempC       *float64          `json:"best_temp_c,omitempty" jsonschema:"température de buse de la pièce / de l'étage le plus propre"`
	BridgeMaxSpanMM *float64          `json:"bridge_max_span_mm,omitempty" jsonschema:"plus grande portée de pont sans affaissement"`
	OverhangMaxDeg  *float64          `json:"overhang_max_deg,omitempty" jsonschema:"plus grand angle de surplomb propre, depuis la verticale"`
}

func (m *Measures) empty() bool {
	return m.FitSlidesGapMM == nil && m.FitSnugGapMM == nil && len(m.Holes) == 0 && len(m.Shafts) == 0 &&
		len(m.CubeMM) == 0 && m.BestTempC == nil && m.BridgeMaxSpanMM == nil && m.OverhangMaxDeg == nil
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// Compute valide les mesures et calcule les corrections ; notes explique chaque calcul (français, lisible par l'utilisateur).
func Compute(m Measures) (cal engineering.Calibration, notes []string, err error) {
	if m.empty() {
		return cal, nil, invalidf("aucune mesure fournie (fit_slides_gap_mm, holes, shafts, cube_mm, best_temp_c, bridge_max_span_mm ou overhang_max_deg)")
	}
	if m.FitSlidesGapMM != nil {
		v := *m.FitSlidesGapMM
		if !finite(v) || v <= 0 || v > 1.5 {
			return cal, nil, invalidf("fit_slides_gap_mm %v hors de ]0 ; 1,5] mm", v)
		}
		cal.SlidesGapMM = m.FitSlidesGapMM
		notes = append(notes, fmt.Sprintf("Jeu glissant effectif : %.2f mm (la languette de 10 mm coulisse dans une encoche de %.2f mm).", v, 10+v))
	}
	if m.FitSnugGapMM != nil {
		v := *m.FitSnugGapMM
		if !finite(v) || v <= 0 || v > 1.5 {
			return cal, nil, invalidf("fit_snug_gap_mm %v hors de ]0 ; 1,5] mm", v)
		}
		if m.FitSlidesGapMM != nil && v > *m.FitSlidesGapMM {
			return cal, nil, invalidf("fit_snug_gap_mm (%.2f) ne peut pas dépasser fit_slides_gap_mm (%.2f) : le serré est plus étroit que le glissant", v, *m.FitSlidesGapMM)
		}
		cal.SnugGapMM = m.FitSnugGapMM
		notes = append(notes, fmt.Sprintf("Jeu serré effectif : %.2f mm.", v))
	}
	var holeV, holeH []DiameterMeasure
	for i, h := range m.Holes {
		if err := checkDia("holes", i, h); err != nil {
			return cal, nil, err
		}
		switch strings.ToLower(strings.TrimSpace(h.Axis)) {
		case "", "vertical", "z":
			holeV = append(holeV, h)
		case "horizontal", "xy", "y", "x":
			holeH = append(holeH, h)
		default:
			return cal, nil, invalidf("holes[%d].axis %q inconnu (vertical|horizontal)", i, h.Axis)
		}
	}
	for i, s := range m.Shafts {
		if err := checkDia("shafts", i, s); err != nil {
			return cal, nil, err
		}
	}
	if cal.HoleVertical = fitOffsets(holeV); cal.HoleVertical != nil {
		notes = append(notes, describeOffset("Trous verticaux", cal.HoleVertical))
	}
	if cal.HoleHorizontal = fitOffsets(holeH); cal.HoleHorizontal != nil {
		notes = append(notes, describeOffset("Trous horizontaux", cal.HoleHorizontal))
	}
	if cal.Shaft = fitOffsets(m.Shafts); cal.Shaft != nil {
		notes = append(notes, describeOffset("Arbres", cal.Shaft))
	}
	if len(m.CubeMM) > 0 {
		nom := m.CubeNominalMM
		if nom == 0 {
			nom = 50
		}
		if len(m.CubeMM) != 3 || !finite(nom) || nom < 10 || nom > 200 {
			return cal, nil, invalidf("cube_mm doit contenir 3 mesures [X, Y, Z] et cube_nominal_mm être dans [10 ; 200]")
		}
		for i, v := range m.CubeMM {
			if !finite(v) || math.Abs(v-nom) > 0.1*nom {
				return cal, nil, invalidf("cube_mm[%d] = %v mm : écart > 10 %% au nominal %v mm, mesure implausible (unité ? pied d'éléphant ?)", i, v, nom)
			}
		}
		xy := nom / ((m.CubeMM[0] + m.CubeMM[1]) / 2)
		z := nom / m.CubeMM[2]
		cal.ShrinkXY, cal.ShrinkZ = &xy, &z
		notes = append(notes, fmt.Sprintf("Retrait : échelle à appliquer ×%.4f en XY (%+.2f %%) et ×%.4f en Z (%+.2f %%).", xy, (xy-1)*100, z, (z-1)*100))
		if math.Abs(m.CubeMM[0]-m.CubeMM[1]) > 0.3 {
			notes = append(notes, fmt.Sprintf("X et Y diffèrent de %.2f mm : imprimante mal étalonnée en flux/dimensions ? Vérifier avant de compenser.", math.Abs(m.CubeMM[0]-m.CubeMM[1])))
		}
	}
	if m.BestTempC != nil {
		v := *m.BestTempC
		if !finite(v) || v < 150 || v > 330 {
			return cal, nil, invalidf("best_temp_c %v hors de [150 ; 330] °C", v)
		}
		cal.BestTempC = m.BestTempC
		notes = append(notes, fmt.Sprintf("Meilleure température de buse : %.0f °C.", v))
	}
	if m.BridgeMaxSpanMM != nil {
		v := *m.BridgeMaxSpanMM
		if !finite(v) || v <= 0 || v > 200 {
			return cal, nil, invalidf("bridge_max_span_mm %v hors de ]0 ; 200] mm", v)
		}
		cal.BridgeMaxSpanMM = m.BridgeMaxSpanMM
		notes = append(notes, fmt.Sprintf("Pont propre jusqu'à %.0f mm.", v))
	}
	if m.OverhangMaxDeg != nil {
		v := *m.OverhangMaxDeg
		if !finite(v) || v <= 0 || v >= 90 {
			return cal, nil, invalidf("overhang_max_deg %v hors de ]0 ; 90[ °", v)
		}
		cal.OverhangMaxDeg = m.OverhangMaxDeg
		notes = append(notes, fmt.Sprintf("Surplomb propre jusqu'à %.0f° depuis la verticale.", v))
	}
	return cal, notes, nil
}

func checkDia(what string, i int, d DiameterMeasure) error {
	if !finite(d.Nominal) || !finite(d.Measured) || d.Nominal < 1 || d.Nominal > 100 {
		return invalidf("%s[%d] : nominal dans [1 ; 100] mm et mesure finie requis", what, i)
	}
	if d.Measured < 0.5*d.Nominal || d.Measured > 1.5*d.Nominal {
		return invalidf("%s[%d] : mesuré %v mm pour un nominal de %v mm — mesure implausible (unité ? trou bouché ?)", what, i, d.Measured, d.Nominal)
	}
	return nil
}

// fitOffsets ajuste off(d) = a + b·d aux écarts (nominal − mesuré). Moins de 3 points, étendue < 2 mm ou pente > 0,05 :
// moyenne simple (pente nulle).
func fitOffsets(ms []DiameterMeasure) *engineering.LinearOffset {
	n := len(ms)
	if n == 0 {
		return nil
	}
	var sx, sy, sxx, sxy float64
	minD, maxD := math.Inf(1), math.Inf(-1)
	for _, m := range ms {
		y := m.Nominal - m.Measured
		sx += m.Nominal
		sy += y
		sxx += m.Nominal * m.Nominal
		sxy += m.Nominal * y
		minD, maxD = math.Min(minD, m.Nominal), math.Max(maxD, m.Nominal)
	}
	fn := float64(n)
	mean := sy / fn
	out := &engineering.LinearOffset{Offset: mean, N: n}
	if n >= 3 && maxD-minD >= 2 {
		den := fn*sxx - sx*sx
		if den > 1e-9 {
			b := (fn*sxy - sx*sy) / den
			if math.Abs(b) <= 0.05 {
				out.Slope, out.Offset = b, (sy-b*sx)/fn
			}
		}
	}
	for _, m := range ms {
		out.Residual = math.Max(out.Residual, math.Abs((m.Nominal-m.Measured)-(out.Offset+out.Slope*m.Nominal)))
	}
	out.Offset = round(out.Offset, 4)
	out.Slope = round(out.Slope, 5)
	out.Residual = round(out.Residual, 4)
	return out
}

func describeOffset(label string, l *engineering.LinearOffset) string {
	if l.Slope == 0 {
		return fmt.Sprintf("%s : %+.3f mm à ajouter au diamètre modélisé (moyenne de %d mesure(s), écart max %.3f mm).", label, l.Offset, l.N, l.Residual)
	}
	return fmt.Sprintf("%s : décalage = %+.3f %+.4f·d mm (régression sur %d mesures, écart max %.3f mm).", label, l.Offset, l.Slope, l.N, l.Residual)
}

func round(v float64, d int) float64 {
	p := math.Pow(10, float64(d))
	return math.Round(v*p) / p
}

// Merge superpose over sur base : un champ non nul de over remplace celui de base.
func Merge(base, over *engineering.Calibration) *engineering.Calibration {
	out := *base
	o := over
	if o.SlidesGapMM != nil {
		out.SlidesGapMM = o.SlidesGapMM
	}
	if o.SnugGapMM != nil {
		out.SnugGapMM = o.SnugGapMM
	}
	if o.HoleVertical != nil {
		out.HoleVertical = o.HoleVertical
	}
	if o.HoleHorizontal != nil {
		out.HoleHorizontal = o.HoleHorizontal
	}
	if o.Shaft != nil {
		out.Shaft = o.Shaft
	}
	if o.ShrinkXY != nil {
		out.ShrinkXY = o.ShrinkXY
	}
	if o.ShrinkZ != nil {
		out.ShrinkZ = o.ShrinkZ
	}
	if o.BestTempC != nil {
		out.BestTempC = o.BestTempC
	}
	if o.BridgeMaxSpanMM != nil {
		out.BridgeMaxSpanMM = o.BridgeMaxSpanMM
	}
	if o.OverhangMaxDeg != nil {
		out.OverhangMaxDeg = o.OverhangMaxDeg
	}
	if o.Provenance != "" {
		out.Provenance, out.CalibratedAt = o.Provenance, o.CalibratedAt
	}
	return &out
}
