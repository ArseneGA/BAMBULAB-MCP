package engineering

import (
	"math"
	"strconv"
	"strings"
)

var litSource = Source{ID: "lit_indicative", Title: "Ordres de grandeur de la pratique FDM (calibrations de jeux, compensation de trous) ; à calibrer par essai d'impression", Date: "2026-11-06"}

// FitType est le type d'ajustement.
type FitType string

const (
	FitFree    FitType = "libre"    // rotation / jeu large
	FitSliding FitType = "glissant" // coulisse à la main
	FitSnug    FitType = "serré"    // serrage léger
	FitPress   FitType = "press"    // interférence (emmanchement)
)

// Orientation de l'axe de la fonction (trou, arbre, tenon).
type Axis string

const (
	AxisVertical   Axis = "vertical"   // axe // Z, section circulaire dans le plan XY
	AxisHorizontal Axis = "horizontal" // axe dans le plan XY, affaissement des voûtes
)

func parseAxis(s string) (Axis, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "vertical", "z":
		return AxisVertical, nil
	case "horizontal", "xy":
		return AxisHorizontal, nil
	}
	return "", invalid("orientation %q inconnue (vertical|horizontal)", s)
}

func parseFit(f FitType) (FitType, error) {
	switch FitType(strings.ToLower(strings.TrimSpace(string(f)))) {
	case "libre", "free":
		return FitFree, nil
	case "glissant", "sliding":
		return FitSliding, nil
	case "serré", "serre", "snug":
		return FitSnug, nil
	case "press", "pressé", "presse":
		return FitPress, nil
	}
	return "", invalid("ajustement %q inconnu (libre|glissant|serré|press)", f)
}

func checkNozzle(n float64) error {
	if err := positive("buse", n); err != nil {
		return err
	}
	if n < 0.1 || n > 1.2 {
		return invalid("buse %.2f mm hors du domaine 0,1–1,2 mm", n)
	}
	return nil
}

// FitParams décrit un calcul de jeu.
type FitParams struct {
	Fit         FitType
	Material    string  // id matériau (optionnel : "" = pas de correction de retrait)
	Nozzle      float64 // mm
	Orientation string  // vertical|horizontal
	Dimension   float64 // dimension nominale de l'ajustement en mm (optionnel, pour le retrait)
}

// FitResult : Value = jeu DIAMÉTRAL total recommandé en mm (négatif = interférence).
type FitResult struct {
	Result
	PerSide float64 `json:"per_side_mm"`
}

// FitClearance recommande un jeu diamétral (cote trou − cote arbre).
// Base buse 0,4 mm : libre 0,40 ; glissant 0,25 ; serré 0,10 ; press −0,10 mm.
func FitClearance(p FitParams) (*FitResult, error) {
	fit, err := parseFit(p.Fit)
	if err != nil {
		return nil, err
	}
	if err := checkNozzle(p.Nozzle); err != nil {
		return nil, err
	}
	axis, err := parseAxis(p.Orientation)
	if err != nil {
		return nil, err
	}
	if p.Dimension < 0 {
		return nil, invalid("dimension négative")
	}
	base := map[FitType]float64{FitFree: 0.40, FitSliding: 0.25, FitSnug: 0.10, FitPress: -0.10}[fit]
	v := base + 0.25*(p.Nozzle-0.4)
	asm := []string{
		"Jeu de base pour buse 0,4 mm : " + string(fit),
		"Correction buse : +0,25 mm par mm d'écart à 0,4 mm",
		"Dimensions par le slicer standard, paramètres Bambu par défaut, pièce refroidie",
	}
	if axis == AxisHorizontal {
		v += 0.05
		asm = append(asm, "Axe horizontal : +0,05 mm (affaissement des voûtes, polygonisation)")
	}
	level := LevelHeuristic
	var srcs = []Source{litSource}
	var warns = []string{"Valeur à valider par un essai d'assemblage (pièce test imprimée avec le même matériau et la même buse)."}
	if p.Material != "" && p.Dimension > 0 {
		m, err := GetMaterial(p.Material)
		if err != nil {
			return nil, err
		}
		u := newUsed()
		s, err := m.need("shrinkage_pct", u)
		if err != nil {
			warns = append(warns, "Retrait inconnu : non pris en compte.")
		} else {
			corr := p.Dimension * s / 100
			v += corr
			asm = append(asm, "Retrait "+m.ID+" "+ftoa(s)+" % appliqué à la dimension "+ftoa(p.Dimension)+" mm (+"+ftoa(round(corr, 3))+" mm)")
			srcs = append(srcs, u.sources...)
		}
	} else if p.Material != "" {
		if _, err := GetMaterial(p.Material); err != nil {
			return nil, err
		}
	}
	if fit == FitPress {
		warns = append(warns, "Emmanchement : interférence sur plastique imprimé, risque de fissuration entre couches ; préférer un insert ou un ajustement serré.")
	}
	v = round(v, 3)
	res := &FitResult{PerSide: round(v/2, 4)}
	res.Result = Result{
		Value: v, Unit: "mm", Range: &Range{Min: round(v-0.05, 3), Max: round(v+0.05, 3)},
		Assumptions: asm, Level: level, Sources: srcs, Warnings: warns,
		Validity: "Buse 0,1–1,2 mm ; dimensions d'ajustement ~3–100 mm ; FDM, hauteur de couche 0,1–0,3 mm. " + Disclaimer,
	}
	return res, nil
}

// HoleParams décrit une compensation de trou.
type HoleParams struct {
	Diameter float64
	Axis     string // vertical|horizontal
	Nozzle   float64
	Material string // optionnel
}

// HoleCompensation retourne le diamètre à modéliser pour obtenir le diamètre nominal imprimé.
// d_modélisé = d + δ(orientation) + d·s/100 ; δ vertical 0,15 mm, horizontal 0,25 mm (buse 0,4),
// +0,05 mm si d < 3 mm.
func HoleCompensation(p HoleParams) (*Result, error) {
	if err := positive("diamètre", p.Diameter); err != nil {
		return nil, err
	}
	if p.Diameter > 500 {
		return nil, invalid("diamètre %.1f mm hors domaine", p.Diameter)
	}
	if err := checkNozzle(p.Nozzle); err != nil {
		return nil, err
	}
	axis, err := parseAxis(p.Axis)
	if err != nil {
		return nil, err
	}
	delta := 0.15
	if axis == AxisHorizontal {
		delta = 0.25
	}
	delta *= 0.5 + 0.5*p.Nozzle/0.4
	asm := []string{
		"Les trous imprimés sortent plus petits (extrusion vers l'intérieur, polygonisation)",
		"δ de base " + ftoa(delta) + " mm pour l'orientation " + string(axis) + " et la buse " + ftoa(p.Nozzle) + " mm",
	}
	warns := []string{"Ordre de grandeur à calibrer par un retour d'impression."}
	if p.Diameter < 3 {
		delta += 0.05
		warns = append(warns, "Trou < 3 mm : très sensible à la buse et au slicer ; prévoir un perçage de reprise.")
	}
	level := LevelHeuristic
	srcs := []Source{litSource}
	shrink := 0.0
	if p.Material != "" {
		m, err := GetMaterial(p.Material)
		if err != nil {
			return nil, err
		}
		u := newUsed()
		if s, err := m.need("shrinkage_pct", u); err == nil {
			shrink = p.Diameter * s / 100
			asm = append(asm, "Retrait "+m.ID+" "+ftoa(s)+" % ajouté (+"+ftoa(round(shrink, 3))+" mm)")
			srcs = append(srcs, u.sources...)
		}
	}
	v := round(p.Diameter+delta+shrink, 3)
	return &Result{
		Value: v, Unit: "mm", Range: &Range{Min: round(v-0.05, 3), Max: round(v+0.1, 3)},
		Assumptions: asm, Level: level, Sources: srcs, Warnings: warns,
		Validity: "Diamètres 1–100 mm, buse 0,2–0,8 mm, trous cylindriques. " + Disclaimer,
	}, nil
}

// Shrinkage retourne le retrait (%) d'un matériau.
func Shrinkage(material string) (*Result, error) {
	m, err := GetMaterial(material)
	if err != nil {
		return nil, err
	}
	u := newUsed()
	s, err := m.need("shrinkage_pct", u)
	if err != nil {
		return nil, err
	}
	return &Result{
		Value: s, Unit: "%", Range: &Range{Min: round(s*0.5, 3), Max: round(s*1.5, 3)},
		Assumptions: []string{"Valeur indicative par famille ; dépend de la géométrie, de la température de chambre, du refroidissement"},
		Validity:    "Pièces de quelques cm à ~25 cm, chambre fermée type P1S. " + Disclaimer,
		Level:       LevelHeuristic, Sources: append(u.sources, litSource),
		Warnings: []string{"Retrait indicatif (aucune mesure fabricant) : calibrer sur une pièce étalon."},
	}, nil
}

// ScaleResult : Value = dimension à modéliser ; Factor = 1/(1−s/100).
type ScaleResult struct {
	Result
	Factor float64 `json:"factor"`
}

// ScaleForShrinkage compense le retrait : D_modélisé = D / (1 − s/100).
func ScaleForShrinkage(material string, nominal float64) (*ScaleResult, error) {
	if err := positive("dimension", nominal); err != nil {
		return nil, err
	}
	s, err := Shrinkage(material)
	if err != nil {
		return nil, err
	}
	if s.Value >= 100 {
		return nil, invalid("retrait ≥ 100 %%")
	}
	f := 1 / (1 - s.Value/100)
	out := &ScaleResult{Factor: f}
	out.Result = *s
	out.Value = nominal * f
	out.Unit = "mm"
	out.Range = &Range{Min: nominal / (1 - s.Value*0.5/100), Max: nominal / (1 - s.Value*1.5/100)}
	out.Assumptions = append([]string{"D_modélisé = D_nominal / (1 − s/100), s = " + ftoa(s.Value) + " %"}, s.Assumptions...)
	return out, nil
}

func ftoa(v float64) string {
	return strconv.FormatFloat(math.Round(v*10000)/10000, 'f', -1, 64)
}
