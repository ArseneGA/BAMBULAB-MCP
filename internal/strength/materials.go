package strength

import (
	"math"
	"regexp"
	"strings"
)

// Propriétés des matières pour pièces imprimées FDM, remplies à 100 %, mesurées dans le plan des couches (XY) ou
// perpendiculairement aux couches (Z).
//
// SOURCE ET NIVEAU : ce sont des ORDRES DE GRANDEUR ARRONDIS, niveau « estimation ». Ils s'appuient sur les fiches
// techniques des filaments (Bambu Lab : PLA Basic, PETG HF, ABS, ASA, PAHT-CF, TPU 95A HF) et sur la littérature sur les
// pièces FFF (par ex. Ahn et al., Rapid Prototyping Journal 2002 ; Tymrak et al., Materials & Design 2014 : la résistance
// perpendiculaire aux couches vaut environ 40 à 70 % de celle dans le plan). La marque, la couleur, l'humidité, la
// température de buse et le refroidissement les font varier de ±20 % ou plus. Une valeur marquée « estimation » ne doit
// JAMAIS être présentée comme garantie : en cas de doute, mesurer (éprouvette imprimée dans la même orientation).

// Material : propriétés de dimensionnement d'une matière.
type Material struct {
	Key         string  `json:"key"`
	Label       string  `json:"label"`
	EMPa        float64 `json:"e_mpa" jsonschema:"module d'élasticité dans le plan, pièce pleine"`
	Nu          float64 `json:"nu"`
	SigmaXY     float64 `json:"sigma_xy_mpa" jsonschema:"résistance de dimensionnement dans le plan des couches (traction)"`
	SigmaZ      float64 `json:"sigma_z_mpa" jsonschema:"résistance perpendiculaire aux couches (traction entre couches) : la plus faible"`
	TauZ        float64 `json:"tau_interlayer_mpa" jsonschema:"cisaillement entre couches"`
	CreepFactor float64 `json:"creep_factor" jsonschema:"facteur appliqué aux résistances pour une charge PERMANENTE (fluage)"`
	SoftenC     float64 `json:"soften_c" jsonschema:"température approximative de ramollissement : au-delà, ne pas se fier au calcul"`
	Note        string  `json:"note"`
	Level       string  `json:"level" jsonschema:"toujours estimation"`
	Source      string  `json:"source"`
}

const materialSource = "estimation : ordres de grandeur arrondis d'après fiches techniques Bambu Lab et littérature FFF (Ahn 2002 ; Tymrak 2014)"

// Materials : table documentée (ordre stable).
var Materials = []Material{
	{Key: "PLA", Label: "PLA", EMPa: 3300, Nu: 0.36, SigmaXY: 35, SigmaZ: 18, TauZ: 12, CreepFactor: 0.5, SoftenC: 55,
		Note: "rigide mais cassant ; fluage notable sous charge permanente ; se ramollit vers 55 °C (voiture au soleil)"},
	{Key: "PETG", Label: "PETG", EMPa: 1800, Nu: 0.38, SigmaXY: 32, SigmaZ: 18, TauZ: 12, CreepFactor: 0.6, SoftenC: 70,
		Note: "plus tenace que le PLA, supporte les chocs ; moins rigide ; fluage modéré"},
	{Key: "ABS", Label: "ABS", EMPa: 2000, Nu: 0.35, SigmaXY: 33, SigmaZ: 20, TauZ: 13, CreepFactor: 0.6, SoftenC: 90,
		Note: "bonne tenue en température ; sensible au décollement entre couches si l'impression refroidit trop"},
	{Key: "ASA", Label: "ASA", EMPa: 2000, Nu: 0.35, SigmaXY: 32, SigmaZ: 20, TauZ: 13, CreepFactor: 0.6, SoftenC: 90,
		Note: "comme l'ABS, résiste aux UV (extérieur)"},
	{Key: "PA", Label: "PA (nylon, PA12/PA6 non chargé)", EMPa: 1600, Nu: 0.40, SigmaXY: 40, SigmaZ: 25, TauZ: 16, CreepFactor: 0.5, SoftenC: 100,
		Note: "tenace, endurant ; reprend l'humidité (propriétés très variables selon l'état sec ou humide) ; chargé fibres de carbone : bien plus rigide dans le plan, valeurs non couvertes ici"},
	{Key: "TPU", Label: "TPU 95A", EMPa: 80, Nu: 0.45, SigmaXY: 8, SigmaZ: 5, TauZ: 3.5, CreepFactor: 0.5, SoftenC: 80,
		Note: "caoutchouteux : grandes déformations, le calcul linéaire n'est valable que pour de très petites déformations ; limite pratique prise très basse"},
}

func init() {
	for i := range Materials {
		Materials[i].Level, Materials[i].Source = "estimation", materialSource
	}
}

var rePA = regexp.MustCompile(`(^|[^a-z0-9])pa([^a-z]|$)`)

// LookupMaterial retrouve une matière d'après un nom libre (« PETG HF », « Bambu PLA Basic », « nylon »).
func LookupMaterial(name string) (Material, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return Material{}, false
	}
	key := ""
	switch {
	case strings.Contains(n, "petg"):
		key = "PETG"
	case strings.Contains(n, "tpu") || strings.Contains(n, "tpe") || strings.Contains(n, "flex"):
		key = "TPU"
	case strings.Contains(n, "asa"):
		key = "ASA"
	case strings.Contains(n, "abs"):
		key = "ABS"
	case strings.Contains(n, "nylon") || strings.Contains(n, "pa12") || strings.Contains(n, "pa6") || strings.Contains(n, "paht") || rePA.MatchString(n):
		key = "PA"
	case strings.Contains(n, "pla"):
		key = "PLA"
	}
	for _, m := range Materials {
		if m.Key == key {
			return m, true
		}
	}
	return Material{}, false
}

// Effective : propriétés retenues pour le calcul, après réduction par les parois, le remplissage et le fluage.
type Effective struct {
	EMPa      float64 `json:"e_mpa"`
	Nu        float64 `json:"nu"`
	SigmaXY   float64 `json:"sigma_xy_mpa"`
	SigmaZ    float64 `json:"sigma_z_mpa"`
	TauZ      float64 `json:"tau_interlayer_mpa"`
	SolidFrac float64 `json:"solid_fraction" jsonschema:"fraction de matière estimée dans la section (parois + remplissage)"`
	FStrength float64 `json:"strength_factor"`
	FStiff    float64 `json:"stiffness_factor"`
	Creep     float64 `json:"creep_factor_applied"`
	ThickMM   float64 `json:"characteristic_thickness_mm"`
	WallMM    float64 `json:"wall_mm"`
}

// LineWidthMM : largeur de ligne d'extrusion supposée (buse 0,4).
const LineWidthMM = 0.45

// EffectiveProps applique les facteurs de réduction. Estimation, volontairement un peu prudente :
//   - épaisseur caractéristique de la pièce t = 2·V/A (V volume, A aire de surface) ;
//   - épaisseur de paroi s = parois × 0,45 mm ; fraction de coque f = min(1, 2s/t) ;
//   - fraction de matière φ = f + (1 − f) × densité de remplissage ;
//   - résistance × (0,35 + 0,65 φ) : les parois, en surface, portent l'essentiel de la flexion ;
//   - rigidité × (0,25 + 0,75 φ) ;
//   - charge permanente : résistances × facteur de fluage de la matière.
func EffectiveProps(mat Material, infillPct float64, walls int, volumeMM3, areaMM2 float64, permanent bool) Effective {
	t := 1e9
	if areaMM2 > 0 {
		t = 2 * volumeMM3 / areaMM2
	}
	s := float64(walls) * LineWidthMM
	f := math.Min(1, 2*s/math.Max(t, 1e-6))
	rho := math.Max(0, math.Min(1, infillPct/100))
	phi := f + (1-f)*rho
	e := Effective{Nu: mat.Nu, SolidFrac: phi, ThickMM: t, WallMM: s, Creep: 1}
	e.FStrength = 0.35 + 0.65*phi
	e.FStiff = 0.25 + 0.75*phi
	if permanent {
		e.Creep = mat.CreepFactor
	}
	e.EMPa = mat.EMPa * e.FStiff
	e.SigmaXY = mat.SigmaXY * e.FStrength * e.Creep
	e.SigmaZ = mat.SigmaZ * e.FStrength * e.Creep
	e.TauZ = mat.TauZ * e.FStrength * e.Creep
	return e
}
