package engineering

import (
	"math"
	"strings"
)

// Section d'une poutre prismatique. rect : B = largeur, H = hauteur dans le plan de flexion (mm).
// circle : D. tube : Do, Di.
type Section struct {
	Shape string // rect|circle|tube
	B, H  float64
	D     float64
	Do    float64
	Di    float64
}

// BeamParams décrit un cas de poutre.
type BeamParams struct {
	Support     string  // cantilever|simple (console, appuis simples)
	LoadType    string  // point|distributed
	Load        float64 // N si point (en bout pour la console, au centre pour appuis simples), N/mm si répartie
	Length      float64 // mm
	Section     Section
	Material    string
	Orientation string  // xy : contrainte de traction extrême dans le plan des couches ; z : perpendiculaire aux couches
	ZFactor     float64 // facteur d'anisotropie de repli σ_z/σ_xy si la donnée Z est inconnue (défaut 0,5)
}

// BeamResult : Value = contrainte maximale σ = M·c/I en MPa.
type BeamResult struct {
	Result
	MomentNmm      float64  `json:"max_moment_Nmm"`
	InertiaMM4     float64  `json:"inertia_mm4"`
	CMM            float64  `json:"c_mm"`
	DeflectionMM   float64  `json:"max_deflection_mm"`
	AllowableMPa   float64  `json:"allowable_stress_MPa"`
	ModulusMPa     float64  `json:"modulus_MPa"`
	SafetyFactor   float64  `json:"safety_factor"`
	SafetyFactorXY *float64 `json:"safety_factor_xy,omitempty"`
	SafetyFactorZ  *float64 `json:"safety_factor_z,omitempty"`
	Anisotropy     *float64 `json:"anisotropy_ratio_z_over_xy,omitempty"`
	Verdict        string   `json:"verdict"` // fail|insufficient|acceptable
	Orientation    string   `json:"orientation"`
}

func (s Section) props() (I, c float64, err error) {
	switch strings.ToLower(s.Shape) {
	case "rect", "rectangle":
		if err = positive("b", s.B); err != nil {
			return
		}
		if err = positive("h", s.H); err != nil {
			return
		}
		return s.B * math.Pow(s.H, 3) / 12, s.H / 2, nil
	case "circle", "circular", "cercle":
		if err = positive("d", s.D); err != nil {
			return
		}
		return math.Pi * math.Pow(s.D, 4) / 64, s.D / 2, nil
	case "tube":
		if err = positive("Do", s.Do); err != nil {
			return
		}
		if err = positive("Di", s.Di); err != nil {
			return
		}
		if s.Di >= s.Do {
			err = invalid("tube : Di (%v) doit être < Do (%v)", s.Di, s.Do)
			return
		}
		return math.Pi * (math.Pow(s.Do, 4) - math.Pow(s.Di, 4)) / 64, s.Do / 2, nil
	}
	return 0, 0, invalid("section %q inconnue (rect|circle|tube)", s.Shape)
}

func (s Section) depth() float64 {
	switch strings.ToLower(s.Shape) {
	case "rect", "rectangle":
		return s.H
	case "circle", "circular", "cercle":
		return s.D
	}
	return s.Do
}

// strength retourne la résistance (flexion sinon traction) et le module (flexion sinon Young) pour une direction.
func strengthModulus(m *Material, dir string, zFactor float64, u *propUsed) (sig, E float64, warns []string, err error) {
	get := func(keys ...string) (float64, bool) {
		for _, k := range keys {
			if p, ok := m.Prop(k); ok {
				u.add(m, p)
				return *p.Value, true
			}
		}
		return 0, false
	}
	sig, ok := get("bending_strength_"+dir, "tensile_"+dir)
	if !ok && dir == "z" {
		sx, ok2 := get("bending_strength_xy", "tensile_xy")
		if ok2 {
			sig, ok = sx*zFactor, true
			warns = append(warns, "Résistance Z inconnue : σ_xy × facteur d'anisotropie "+ftoa(zFactor)+" (hypothèse indicative).")
			u.level = worst(u.level, LevelIndicative)
		}
	}
	if !ok {
		return 0, 0, nil, errUnknown(m, "bending_strength_"+dir+" / tensile_"+dir)
	}
	E, ok = get("bending_modulus_"+dir, "modulus_"+dir)
	if !ok && dir == "z" {
		ex, ok2 := get("bending_modulus_xy", "modulus_xy")
		if ok2 {
			E, ok = ex*zFactor, true
			warns = append(warns, "Module Z inconnu : E_xy × facteur d'anisotropie.")
			u.level = worst(u.level, LevelIndicative)
		}
	}
	if !ok {
		return 0, 0, nil, errUnknown(m, "bending_modulus_"+dir+" / modulus_"+dir)
	}
	return sig, E, warns, nil
}

// isElastomer : matériau pour lequel l'élasticité linéaire en petites déformations n'a pas de sens
// (TPU/TPE...), d'après la famille déclarée ou un allongement à la rupture très élevé.
func isElastomer(m *Material) bool {
	f := strings.ToUpper(m.Family + " " + m.ID)
	for _, k := range []string{"TPU", "TPE", "ELASTOMER", "ÉLASTOMÈRE", "FLEX", "RUBBER"} {
		if strings.Contains(f, k) {
			return true
		}
	}
	if p, ok := m.Prop("elongation_xy"); ok && p.Value != nil && *p.Value >= 150 {
		return true
	}
	return false
}

// BeamStress calcule contrainte maximale, flèche et facteur de sécurité (poutre d'Euler-Bernoulli).
func BeamStress(p BeamParams) (*BeamResult, error) {
	if err := positive("longueur", p.Length); err != nil {
		return nil, err
	}
	if err := positive("charge", p.Load); err != nil {
		return nil, err
	}
	sup := strings.ToLower(p.Support)
	if sup != "cantilever" && sup != "simple" {
		return nil, invalid("appui %q inconnu (cantilever|simple)", p.Support)
	}
	lt := strings.ToLower(p.LoadType)
	if lt != "point" && lt != "distributed" {
		return nil, invalid("type de charge %q inconnu (point|distributed)", p.LoadType)
	}
	dir := strings.ToLower(p.Orientation)
	if dir == "" {
		dir = "xy"
	}
	if dir != "xy" && dir != "z" {
		return nil, invalid("orientation %q inconnue (xy|z)", p.Orientation)
	}
	zf := p.ZFactor
	if zf == 0 {
		zf = 0.5
	}
	if zf <= 0 || zf > 1 {
		return nil, invalid("facteur d'anisotropie %v hors ]0 ; 1]", zf)
	}
	I, c, err := p.Section.props()
	if err != nil {
		return nil, err
	}
	m, err := GetMaterial(p.Material)
	if err != nil {
		return nil, err
	}
	if isElastomer(m) {
		return nil, notApplicable("%s est un élastomère : la poutre linéaire élastique en petites déformations ne s'applique pas (hyperélasticité, grandes flèches)", m.ID)
	}
	u := newUsed()
	sig, E, warns, err := strengthModulus(m, dir, zf, u)
	if err != nil {
		return nil, err
	}
	L, W := p.Length, p.Load
	var M, defl float64
	switch {
	case sup == "cantilever" && lt == "point":
		M = W * L
		defl = W * L * L * L / (3 * E * I)
	case sup == "cantilever":
		M = W * L * L / 2
		defl = W * math.Pow(L, 4) / (8 * E * I)
	case lt == "point":
		M = W * L / 4
		defl = W * L * L * L / (48 * E * I)
	default:
		M = W * L * L / 8
		defl = 5 * W * math.Pow(L, 4) / (384 * E * I)
	}
	if defl > L/10 {
		return nil, notApplicable("flèche calculée %.3g mm > L/10 (%.3g mm) : hypothèse de petits déplacements invalide, le résultat linéaire serait trompeur (raidir la section ou réduire la charge)", defl, L/10)
	}
	stress := M * c / I
	sf := sig / stress

	res := &BeamResult{
		MomentNmm: M, InertiaMM4: I, CMM: c, DeflectionMM: defl, AllowableMPa: sig, ModulusMPa: E,
		SafetyFactor: sf, Orientation: dir,
	}
	// Facteurs de sécurité des deux directions, si calculables.
	for _, d := range []string{"xy", "z"} {
		u2 := newUsed()
		s2, _, _, err := strengthModulus(m, d, zf, u2)
		if err == nil {
			v := s2 / stress
			if d == "xy" {
				res.SafetyFactorXY = &v
			} else {
				res.SafetyFactorZ = &v
			}
		}
	}
	if res.SafetyFactorXY != nil && res.SafetyFactorZ != nil {
		a := *res.SafetyFactorZ / *res.SafetyFactorXY
		res.Anisotropy = &a
	}
	switch {
	case sf < 1:
		res.Verdict = "fail"
		warns = append(warns, "Facteur de sécurité < 1 : rupture prévue.")
	case sf < 2:
		res.Verdict = "insufficient"
		warns = append(warns, "Facteur de sécurité entre 1 et 2 : insuffisant pour une pièce fonctionnelle (seuil heuristique).")
	default:
		res.Verdict = "acceptable"
	}
	if dir == "z" {
		warns = append(warns, "Contrainte perpendiculaire aux couches : rupture inter-couches, résistance dépendante du refroidissement et de la température de chambre ; privilégier une orientation où la charge est dans le plan XY.")
	}
	if L/p.Section.depth() < 5 {
		warns = append(warns, "L/h < 5 : poutre courte, le cisaillement n'est pas négligeable et la théorie d'Euler-Bernoulli est optimiste.")
	}
	if defl > L/50 {
		warns = append(warns, "Flèche > L/50 : l'hypothèse de petits déplacements est discutable (refus au-delà de L/10).")
	}
	level := u.level
	if level == LevelIndicative {
		level = LevelHeuristic
		warns = append(warns, "Une donnée matériau indicative est utilisée : résultat heuristique.")
	}
	res.Result = Result{
		Value: stress, Unit: "MPa",
		Assumptions: []string{
			"Poutre prismatique, petits déplacements, matériau linéaire élastique homogène, charge statique",
			"Aucune concentration de contrainte (congés, trous), aucun fluage, aucune température (voir HDT)",
			"Résistance admissible = résistance en flexion du matériau (sinon traction) dans la direction " + dir + " ; E dans la même direction (valeurs typiques fabricant, éprouvettes pleines)",
			"σ = M·c/I ; " + map[bool]string{true: "console", false: "appuis simples"}[sup == "cantilever"] + ", charge " + lt,
		},
		Validity: "L/h ≥ 5, flèche ≤ L/10 (refus au-delà), matériau non élastomère, SF < 1 échec, 1–2 insuffisant, ≥ 2 acceptable en statique (seuils heuristiques) ; pièce pleine à remplissage dense. " + Disclaimer,
		Level:    level, Sources: u.sources, Warnings: warns,
	}
	return res, nil
}
