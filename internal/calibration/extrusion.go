package calibration

import (
	"fmt"
	"math"
	"strconv"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/engineering"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// Bornes des séries et des valeurs retenues. Elles sont reprises côté worker (workers/geometry calibration.py) : une
// révision de profil de calibration ne sort jamais de ces plages.
const (
	flowMin, flowMax       = 0.85, 1.10
	paMin, paMax           = 0.0, 0.2
	retractMin, retractMax = 0.0, 3.0
	volMin, volMax         = 1.0, 40.0
	maxCompMM              = 0.3 // |décalage de contour / de trou| par côté
	minSizeSpanMM          = 20.0
)

// firstLayerBounds : bornes de chaque paramètre de première couche.
var firstLayerBounds = map[string][2]float64{
	"height": {0.08, 0.4}, "speed": {5, 150}, "line_width": {0.3, 1.5}, "temperature": {150, 320},
}

func inRange(v, lo, hi float64) bool { return finite(v) && v >= lo && v <= hi }

// linfit ajuste y = a + b·x ; retourne a, b et l'écart maximal aux points. Au moins 2 x distincts.
func linfit(xs, ys []float64) (a, b, resid float64, ok bool) {
	n := float64(len(xs))
	var sx, sy, sxx, sxy float64
	for i := range xs {
		sx += xs[i]
		sy += ys[i]
		sxx += xs[i] * xs[i]
		sxy += xs[i] * ys[i]
	}
	den := n*sxx - sx*sx
	if den < 1e-12 {
		return 0, 0, 0, false
	}
	b = (n*sxy - sx*sy) / den
	a = (sy - b*sx) / n
	for i := range xs {
		resid = math.Max(resid, math.Abs(ys[i]-(a+b*xs[i])))
	}
	return a, b, resid, true
}

func ptr(v float64) *float64 { return &v }

// computeExtrusion traite le retrait sur plusieurs tailles et les calibrations d'extrusion.
func computeExtrusion(m Measures, cal *engineering.Calibration) (notes []string, err error) {
	if len(m.ShrinkSizes) > 0 {
		ns, err := computeSizes(m.ShrinkSizes, cal)
		if err != nil {
			return nil, err
		}
		notes = append(notes, ns...)
	}
	if m.Flow != nil && m.BestFlowRatio != nil {
		return nil, invalidf("flow et best_flow_ratio sont exclusifs : fournir la série mesurée OU la valeur choisie à l'oeil")
	}
	if m.Flow != nil {
		f := m.Flow
		if len(f.Series) < 2 || len(f.Series) > 12 {
			return nil, invalidf("flow.series : de 2 à 12 pièces mesurées requises")
		}
		if !inRange(f.TargetWallMM, 0.2, 1.5) {
			return nil, invalidf("flow.target_wall_mm %v hors de [0,2 ; 1,5] mm", f.TargetWallMM)
		}
		var xs, ys []float64
		for i, p := range f.Series {
			if !inRange(p.Ratio, flowMin, flowMax) {
				return nil, invalidf("flow.series[%d].ratio %v hors de [%g ; %g]", i, p.Ratio, flowMin, flowMax)
			}
			if !inRange(p.WallMM, 0.1, 2) || math.Abs(p.WallMM-f.TargetWallMM) > 0.5*f.TargetWallMM {
				return nil, invalidf("flow.series[%d].wall_mm %v mm : écart > 50 %% à la paroi visée %v mm, mesure implausible (unité ? mode vase absent ?)", i, p.WallMM, f.TargetWallMM)
			}
			xs, ys = append(xs, p.Ratio), append(ys, p.WallMM)
		}
		a, b, resid, ok := linfit(xs, ys)
		if !ok {
			return nil, invalidf("flow.series : au moins deux débits différents requis")
		}
		if b <= 0 {
			return nil, invalidf("flow.series : la paroi doit croître avec le débit (pente %.3f mm par unité) ; mesures incohérentes", b)
		}
		best := (f.TargetWallMM - a) / b
		if !inRange(best, flowMin, flowMax) {
			return nil, invalidf("débit déduit %.3f hors de [%g ; %g] : refaire une série plus étendue (le profil de calibration reste borné)", best, flowMin, flowMax)
		}
		cal.BestFlowRatio = ptr(round(best, 3))
		notes = append(notes, fmt.Sprintf("Débit : régression paroi = %.3f + %.3f·ratio (écart max %.3f mm) ; la paroi visée de %.2f mm donne filament_flow_ratio = %.3f.", a, b, resid, f.TargetWallMM, best))
	}
	for _, c := range []struct {
		name   string
		v      *float64
		lo, hi float64
		set    **float64
		unit   string
	}{
		{"best_flow_ratio", m.BestFlowRatio, flowMin, flowMax, &cal.BestFlowRatio, ""},
		{"best_pressure_advance", m.BestPressureAdvance, paMin, paMax, &cal.BestPressureAdvance, ""},
		{"best_retraction_mm", m.BestRetractionMM, retractMin, retractMax, &cal.BestRetractionMM, " mm"},
		{"best_max_volumetric_speed", m.BestMaxVolumetricSpeed, volMin, volMax, &cal.BestMaxVolumetricSpeed, " mm³/s"},
	} {
		if c.v == nil {
			continue
		}
		if !inRange(*c.v, c.lo, c.hi) {
			return nil, invalidf("%s %v hors de [%g ; %g]", c.name, *c.v, c.lo, c.hi)
		}
		*c.set = c.v
		notes = append(notes, fmt.Sprintf("%s retenu : %g%s.", c.name, *c.v, c.unit))
	}
	if fl := m.FirstLayer; fl != nil {
		b, ok := firstLayerBounds[fl.Param]
		if !ok {
			return nil, invalidf("first_layer.param %q inconnu (height|speed|line_width|temperature)", fl.Param)
		}
		if !inRange(fl.Value, b[0], b[1]) {
			return nil, invalidf("first_layer.value %v hors de [%g ; %g] pour %s", fl.Value, b[0], b[1], fl.Param)
		}
		out := engineering.FirstLayerCal{}
		switch fl.Param {
		case "height":
			out.HeightMM = ptr(fl.Value)
		case "speed":
			out.SpeedMMS = ptr(fl.Value)
		case "line_width":
			out.LineWidthMM = ptr(fl.Value)
		case "temperature":
			out.TempC = ptr(fl.Value)
		}
		cal.FirstLayer = &out
		notes = append(notes, fmt.Sprintf("Première couche retenue : %s = %g.", fl.Param, fl.Value))
	}
	if cal.BestMaxVolumetricSpeed != nil {
		notes = append(notes, fmt.Sprintf("Débit volumétrique : %.1f mm³/s proposé (90 %% du maximum régulier observé, marge de sécurité).", safeVolumetric(*cal.BestMaxVolumetricSpeed)))
	}
	return notes, nil
}

// safeVolumetric : 90 % du maximum régulier observé, arrondi au 0,5 mm³/s inférieur.
func safeVolumetric(v float64) float64 { return math.Floor(v*0.9*2) / 2 }

// computeSizes sépare le facteur d'échelle (proportionnel à la taille) du décalage de contour (constant) :
// mesure = a·nominal + b. L'échelle est 1/a ; le contour à corriger est −b/2 par côté. Les cotes intérieures (qui se
// comportent comme des trous) donnent xy_hole_compensation.
func computeSizes(ms []SizeMeasure, cal *engineering.Calibration) ([]string, error) {
	if len(ms) < 2 || len(ms) > 4 {
		return nil, invalidf("shrink_sizes : de 2 à 4 tailles requises (une seule taille ne sépare pas échelle et décalage de contour)")
	}
	var nom, xy, zz []float64
	minN, maxN := math.Inf(1), math.Inf(-1)
	for i, s := range ms {
		if !inRange(s.NominalMM, 15, 150) {
			return nil, invalidf("shrink_sizes[%d].nominal_mm %v hors de [15 ; 150] mm", i, s.NominalMM)
		}
		for _, v := range []float64{s.XMM, s.YMM, s.ZMM} {
			if !finite(v) || math.Abs(v-s.NominalMM) > 0.1*s.NominalMM {
				return nil, invalidf("shrink_sizes[%d] : cote %v mm à plus de 10 %% du nominal %v mm, mesure implausible (unité ? pied d'éléphant ?)", i, v, s.NominalMM)
			}
		}
		nom = append(nom, s.NominalMM)
		xy = append(xy, (s.XMM+s.YMM)/2)
		zz = append(zz, s.ZMM)
		minN, maxN = math.Min(minN, s.NominalMM), math.Max(maxN, s.NominalMM)
	}
	if maxN-minN < minSizeSpanMM {
		return nil, invalidf("shrink_sizes : au moins %g mm entre la plus petite et la plus grande taille (reçu %g mm)", minSizeSpanMM, maxN-minN)
	}
	b0, a, resid, ok := linfit(nom, xy)
	if !ok {
		return nil, invalidf("shrink_sizes : tailles nominales distinctes requises")
	}
	if a < 0.97 || a > 1.03 {
		return nil, invalidf("pente mesure/nominal %.4f hors de [0,97 ; 1,03] : mesures incohérentes", a)
	}
	contour := -b0 / 2
	if math.Abs(contour) > maxCompMM {
		return nil, invalidf("décalage de contour déduit %.3f mm par côté (> %.1f mm) : mesures implausibles", contour, maxCompMM)
	}
	scaleXY := 1 / a
	cal.ShrinkXY = ptr(round(scaleXY, 5))
	cal.ContourCompMM = ptr(round(contour, 3))
	notes := []string{fmt.Sprintf("Retrait XY sur %d tailles : mesure = %.5f·nominal %+.3f mm (écart max %.3f mm). Facteur d'échelle ×%.4f (%+.2f %%) ; décalage de contour constant %+.3f mm, soit xy_contour_compensation = %+.3f mm par côté (proposé, jamais appliqué).",
		len(ms), a, b0, resid, scaleXY, (scaleXY-1)*100, b0, contour)}
	if len(ms) == 2 {
		notes = append(notes, "Deux tailles seulement : aucun contrôle de linéarité ; une troisième taille permettrait de le vérifier.")
	}
	if az := slopeOnly(nom, zz); az != 0 {
		cal.ShrinkZ = ptr(round(1/az, 5))
		notes = append(notes, fmt.Sprintf("Retrait Z : ×%.4f (hauteur mesurée / nominale, pente sur les tailles).", 1/az))
	}
	// cotes intérieures -> trous
	var inN, inM []float64
	for i, s := range ms {
		if s.InnerXMM == nil && s.InnerYMM == nil {
			continue
		}
		if s.InnerXMM == nil || s.InnerYMM == nil || !inRange(s.InnerNominalMM, 5, 145) {
			return nil, invalidf("shrink_sizes[%d] : inner_nominal_mm [5 ; 145] mm, inner_x_mm et inner_y_mm requis ensemble", i)
		}
		for _, v := range []float64{*s.InnerXMM, *s.InnerYMM} {
			if !finite(v) || math.Abs(v-s.InnerNominalMM) > 0.1*s.InnerNominalMM {
				return nil, invalidf("shrink_sizes[%d] : cote intérieure %v mm à plus de 10 %% du nominal %v mm, mesure implausible", i, v, s.InnerNominalMM)
			}
		}
		inN, inM = append(inN, s.InnerNominalMM), append(inM, (*s.InnerXMM+*s.InnerYMM)/2)
	}
	if len(inN) > 0 {
		var c float64
		for i := range inN {
			c += inM[i] - a*inN[i]
		}
		c /= float64(len(inN))
		hole := -c / 2
		if math.Abs(hole) > maxCompMM {
			return nil, invalidf("décalage de trou déduit %.3f mm par côté (> %.1f mm) : mesures implausibles", hole, maxCompMM)
		}
		cal.HoleCompMM = ptr(round(hole, 3))
		notes = append(notes, fmt.Sprintf("Cote intérieure (se comporte comme un trou) : écart constant %+.3f mm après échelle, soit xy_hole_compensation = %+.3f mm par côté (proposé, jamais appliqué).", c, hole))
	}
	return notes, nil
}

// slopeOnly : pente de y(x) ou 0.
func slopeOnly(xs, ys []float64) float64 {
	_, b, _, ok := linfit(xs, ys)
	if !ok || b < 0.97 || b > 1.03 {
		return 0
	}
	return b
}

// Proposal : une valeur retenue qui PEUT être placée dans un profil de découpe. Jamais appliquée automatiquement : à passer
// à modify_slicing_profile (champ modifications) après accord de l'utilisateur.
type Proposal struct {
	Key      string `json:"key" jsonschema:"clé de profil BBL (existe dans les profils Bambu Studio)"`
	Value    string `json:"value"`
	Target   string `json:"target" jsonschema:"machine | process | filament"`
	Reason   string `json:"reason"`
	Optional bool   `json:"optional,omitempty" jsonschema:"clé facultative : ignorée si elle n'existe pas dans le profil résolu"`
}

// ManualSetting : valeur retenue sans clé de profil (réglage à faire dans Bambu Studio ou sur le modèle).
type ManualSetting struct {
	Setting string `json:"setting"`
	Value   string `json:"value"`
	Reason  string `json:"reason"`
}

// Proposals regroupe ce qui peut être proposé pour une calibration active.
type Proposals struct {
	Overrides []Proposal      `json:"overrides,omitempty"`
	Manual    []ManualSetting `json:"manual,omitempty"`
	Note      string          `json:"note"`
}

func fnum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// ProposalsFor déduit les propositions de profil d'une calibration. Ne modifie rien.
func ProposalsFor(c *engineering.Calibration) *Proposals {
	if c == nil {
		return nil
	}
	p := &Proposals{}
	add := func(key, val, target, reason string, optional bool) {
		p.Overrides = append(p.Overrides, Proposal{Key: key, Value: val, Target: target, Reason: reason, Optional: optional})
	}
	if c.BestTempC != nil {
		add("nozzle_temperature", fnum(*c.BestTempC), "filament", "meilleure température mesurée sur la série", false)
	}
	if c.ContourCompMM != nil {
		add("xy_contour_compensation", fnum(*c.ContourCompMM), "process", "décalage de contour constant mesuré sur deux tailles (plutôt qu'une mise à l'échelle globale)", false)
	}
	if c.HoleCompMM != nil {
		add("xy_hole_compensation", fnum(*c.HoleCompMM), "process", "décalage des cotes intérieures mesuré sur deux tailles", false)
	}
	if c.BestFlowRatio != nil {
		add("filament_flow_ratio", fnum(*c.BestFlowRatio), "filament", "débit retenu sur la série de parois d'une ligne", false)
	}
	if c.BestRetractionMM != nil {
		add("retraction_length", fnum(*c.BestRetractionMM), "machine", "plus courte rétraction sans fils", false)
		add("filament_retraction_length", fnum(*c.BestRetractionMM), "filament", "idem côté filament (prime sur la machine quand il est renseigné)", true)
	}
	if c.BestMaxVolumetricSpeed != nil {
		add("filament_max_volumetric_speed", fnum(safeVolumetric(*c.BestMaxVolumetricSpeed)), "filament",
			fmt.Sprintf("90 %% du maximum régulier observé (%g mm³/s)", *c.BestMaxVolumetricSpeed), false)
	}
	if fl := c.FirstLayer; fl != nil {
		if fl.HeightMM != nil {
			add("initial_layer_print_height", fnum(*fl.HeightMM), "process", "hauteur de première couche retenue", false)
		}
		if fl.SpeedMMS != nil {
			add("initial_layer_speed", fnum(*fl.SpeedMMS), "process", "vitesse de première couche retenue", false)
		}
		if fl.LineWidthMM != nil {
			add("initial_layer_line_width", fnum(*fl.LineWidthMM), "process", "largeur de ligne de première couche retenue", false)
		}
		if fl.TempC != nil {
			add("nozzle_temperature_initial_layer", fnum(*fl.TempC), "filament", "température de première couche retenue", false)
		}
	}
	if c.BestPressureAdvance != nil {
		p.Manual = append(p.Manual, ManualSetting{Setting: "Bambu Studio > Filament > Pressure advance (K)", Value: fnum(*c.BestPressureAdvance),
			Reason: "les profils Bambu n'ont ni pressure_advance ni enable_pressure_advance : bambud ne peut pas surcharger cette valeur ; Bambu Studio propose son propre étalonnage Flow Dynamics (interface graphique)"})
	}
	if c.ShrinkXY != nil && math.Abs(*c.ShrinkXY-1) > 0.002 {
		p.Manual = append(p.Manual, ManualSetting{Setting: "mise à l'échelle XY du modèle (modify_geometry scale)", Value: fnum(*c.ShrinkXY),
			Reason: "facteur d'échelle proportionnel (|écart| > 0,2 %) ; à n'appliquer qu'aux cotes extérieures critiques, pas au décalage de contour"})
	}
	if len(p.Overrides) == 0 && len(p.Manual) == 0 {
		return nil
	}
	p.Note = "Propositions seulement : bambud n'applique jamais une calibration à un profil de lui-même. Pour les retenir, passer `overrides` à modify_slicing_profile (champ modifications) après accord ; les clés « optional » peuvent être omises."
	return p
}

// seriesKeys : seules clés qu'une pièce de calibration peut surcharger, avec leurs bornes (jamais de *_gcode). La liste est
// volontairement fermée : une révision de profil de calibration est bornée par construction.
var seriesKeys = map[string][2]float64{
	"filament_flow_ratio":              {flowMin, flowMax},
	"retraction_length":                {retractMin, retractMax},
	"filament_retraction_length":       {retractMin, retractMax},
	"filament_max_volumetric_speed":    {volMin, volMax},
	"initial_layer_print_height":       firstLayerBounds["height"],
	"initial_layer_speed":              firstLayerBounds["speed"],
	"initial_layer_line_width":         firstLayerBounds["line_width"],
	"nozzle_temperature_initial_layer": firstLayerBounds["temperature"],
	"nozzle_temperature":               {150, 330},
	"outer_wall_speed":                 {10, 500},
	"spiral_mode":                      {1, 1},
}

// checkSeriesBounds refuse toute surcharge hors liste ou hors bornes (défense en profondeur : les valeurs viennent du worker).
func checkSeriesBounds(ovs []slicer.Override) error {
	for _, o := range ovs {
		b, ok := seriesKeys[o.Key]
		if !ok {
			return invalidf("surcharge de calibration %q non prévue (clés autorisées : débit, rétraction, débit volumétrique, première couche, température, spiral_mode)", o.Key)
		}
		v, err := strconv.ParseFloat(fmt.Sprint(o.Value), 64)
		if err != nil || !inRange(v, b[0], b[1]) {
			return invalidf("surcharge de calibration %s = %v hors de [%g ; %g]", o.Key, o.Value, b[0], b[1])
		}
	}
	return nil
}
