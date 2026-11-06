package engineering

import (
	"math"
	"strings"
)

// SnapParams décrit un crochet de clipsage en porte-à-faux à section rectangulaire constante.
type SnapParams struct {
	Length      float64 // L, mm
	Thickness   float64 // h, mm, dans le sens de la flexion
	Width       float64 // b, mm
	Deflection  float64 // y, mm : flèche d'insertion (profondeur du retour)
	Material    string
	Orientation string  // xy|z : z = couches perpendiculaires à la contrainte de flexion (crochet imprimé debout)
	InsertAngle float64 // α en degrés, flanc d'insertion (défaut 30°)
	Friction    float64 // μ (défaut 0,3)
}

// SnapResult : Value = déformation maximale ε (sans unité, mm/mm) pour la flèche demandée.
type SnapResult struct {
	Result
	StrainAllowable float64 `json:"strain_allowable"`
	MaxDeflectionMM float64 `json:"max_deflection_mm"`
	BendingForceN   float64 `json:"bending_force_N"`
	// InsertionForceN est nul (null en JSON) quand la force n'est pas bornée (auto-blocage).
	InsertionForceN         *float64 `json:"insertion_force_N"`
	InsertionForceUnbounded bool     `json:"insertion_force_unbounded"`
	OK                      bool     `json:"ok"`
}

// selfLockMinDen : seuil de 1 − μ·tan α sous lequel la force d'insertion est jugée non bornée
// (μ·tan α ≥ 0,95 : auto-blocage pratique).
const selfLockMinDen = 0.05

// SnapFit : ε = 1,5·h·y/L² ; y_max = ε_adm·L²/(1,5·h) ; P = (b·h²/6)·E·ε/L ;
// force d'insertion F = P·(μ + tan α)/(1 − μ·tan α).
// ε_adm = déformation admissible indicative du matériau, ×0,5 si les couches sont perpendiculaires à la flexion.
func SnapFit(p SnapParams) (*SnapResult, error) {
	for name, v := range map[string]float64{"longueur": p.Length, "épaisseur": p.Thickness, "largeur": p.Width, "flèche": p.Deflection} {
		if err := positive(name, v); err != nil {
			return nil, err
		}
	}
	dir := strings.ToLower(p.Orientation)
	if dir == "" {
		dir = "xy"
	}
	if dir != "xy" && dir != "z" {
		return nil, invalid("orientation %q inconnue (xy|z)", p.Orientation)
	}
	ang := p.InsertAngle
	if ang == 0 {
		ang = 30
	}
	if ang <= 0 || ang >= 80 {
		return nil, invalid("angle d'insertion %v hors ]0 ; 80[°", ang)
	}
	mu := p.Friction
	if mu == 0 {
		mu = 0.3
	}
	if mu < 0 || mu >= 1 {
		return nil, invalid("frottement %v hors [0 ; 1[", mu)
	}
	m, err := GetMaterial(p.Material)
	if err != nil {
		return nil, err
	}
	u := newUsed()
	epsAdm, err := m.need("snap_strain_allow", u)
	if err != nil {
		return nil, err
	}
	var warns []string
	asm := []string{
		"Crochet console de section constante, flexion pure, matériau linéaire élastique",
		"ε_adm = déformation admissible indicative du matériau (" + ftoa(epsAdm) + ")",
	}
	if dir == "z" {
		epsAdm *= 0.5
		asm = append(asm, "Couches perpendiculaires à la flexion : ε_adm × 0,5")
		warns = append(warns, "Crochet imprimé debout : rupture inter-couches probable ; préférer le coucher à plat (flexion dans le plan XY).")
	}
	// Module : flexion sinon Young, dans la direction de la contrainte (xy = couches parallèles).
	E, _, werr := func() (float64, bool, error) {
		for _, k := range []string{"bending_modulus_" + dir, "modulus_" + dir, "bending_modulus_xy", "modulus_xy"} {
			if pr, ok := m.Prop(k); ok {
				u.add(m, pr)
				return *pr.Value, true, nil
			}
		}
		return 0, false, errUnknown(m, "bending_modulus/modulus")
	}()
	if werr != nil {
		return nil, werr
	}
	eps := 1.5 * p.Thickness * p.Deflection / (p.Length * p.Length)
	ymax := epsAdm * p.Length * p.Length / (1.5 * p.Thickness)
	P := p.Width * p.Thickness * p.Thickness / 6 * E * eps / p.Length
	t := math.Tan(ang * math.Pi / 180)
	den := 1 - mu*t
	var F *float64
	if den <= selfLockMinDen {
		warns = append(warns, "μ·tan α ≥ 0,95 : auto-blocage à l'insertion, la force n'est pas bornée (non calculée) ; réduire l'angle du flanc.")
	} else {
		v := P * (mu + t) / den
		F = &v
	}
	ok := eps <= epsAdm
	if !ok {
		warns = append(warns, "ε > ε_adm : risque de rupture ou de déformation permanente ; allonger le bras, l'amincir ou réduire le retour.")
	}
	if p.Length/p.Thickness < 4 {
		warns = append(warns, "L/h < 4 : bras court, la théorie des poutres est optimiste.")
	}
	level := u.level
	if level == LevelIndicative || level == LevelCalculated {
		level = LevelHeuristic
	}
	warns = append(warns, "Déformation admissible indicative : valider par un essai de cycle d'insertion.")
	if eps >= 0.05 {
		warns = append(warns, "ε ≥ 5 % : hors du domaine de validité (petites déformations).")
	}
	r := &SnapResult{
		StrainAllowable: epsAdm, MaxDeflectionMM: ymax, BendingForceN: P, InsertionForceN: F, InsertionForceUnbounded: F == nil, OK: ok,
	}
	r.Result = Result{
		Value: eps, Unit: "mm/mm", Assumptions: asm,
		Validity: "Crochet L/h ≥ 4, déformations < 5 %, une insertion à vitesse lente ; ne couvre ni la fatigue ni le fluage. " + Disclaimer,
		Level:    level, Sources: append(u.sources, litSource), Warnings: warns,
	}
	return r, nil
}
