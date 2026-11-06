package mesh

import (
	"fmt"
	"math"
	"sort"
)

// Target décrit la machine visée (volume utile et buse). Valeurs nominales : à vérifier
// sur la machine réelle (zones d'exclusion, purge, bords du plateau).
type Target struct {
	Name   string  `json:"name"`
	BedX   float64 `json:"bed_x_mm"`
	BedY   float64 `json:"bed_y_mm"`
	BedZ   float64 `json:"bed_z_mm"`
	Nozzle float64 `json:"nozzle_mm"`
}

// Cibles nominales Bambu Lab.
var (
	TargetP1S    = Target{"P1S", 256, 256, 256, 0.4}
	TargetP1P    = Target{"P1P", 256, 256, 256, 0.4}
	TargetX1C    = Target{"X1C", 256, 256, 256, 0.4}
	TargetA1     = Target{"A1", 256, 256, 256, 0.4}
	TargetA1Mini = Target{"A1 mini", 180, 180, 180, 0.4}
)

// FabOptions règle l'analyse de fabricabilité.
type FabOptions struct {
	Target Target
	// OverhangAngleDeg : seuil de surplomb depuis la verticale (0° = paroi, 90° = plafond). Défaut 45.
	OverhangAngleDeg float64
	// ThinThresholdMM : épaisseur considérée mince. Défaut 2 × buse.
	ThinThresholdMM float64
	// RaySamples : nombre maximal de triangles échantillonnés pour l'épaisseur. Défaut 2000.
	RaySamples int
	// SkipOrientation désactive la proposition d'orientation.
	SkipOrientation bool
	WeldTol         float64
}

// OverhangRegion est un groupe de triangles en surplomb connexes.
type OverhangRegion struct {
	Triangles   int     `json:"triangles"`
	AreaMM2     float64 `json:"area_mm2"`
	BBox        BBox    `json:"bbox"`
	MaxAngleDeg float64 `json:"max_angle_deg"`
}

// OverhangReport : géométrie des surplombs dans l'orientation donnée.
type OverhangReport struct {
	ThresholdDeg float64          `json:"threshold_deg"`
	AreaMM2      float64          `json:"area_mm2"`
	Fraction     float64          `json:"fraction_of_surface"`
	Regions      []OverhangRegion `json:"regions"` // 20 plus grandes
	RegionCount  int              `json:"region_count"`
}

// ThinReport : épaisseur estimée par lancers de rayons opposés à la normale.
type ThinReport struct {
	Samples      int     `json:"samples"`
	Hits         int     `json:"hits"`
	MinMM        float64 `json:"min_mm"`
	P5MM         float64 `json:"p5_mm"`
	MedianMM     float64 `json:"median_mm"`
	ThresholdMM  float64 `json:"threshold_mm"`
	ThinCount    int     `json:"thin_samples"`
	ThinFraction float64 `json:"thin_fraction"`
	ThinPoints   []Vec3  `json:"thin_points,omitempty"` // jusqu'à 10 exemples
	Confidence   string  `json:"confidence"`            // low|medium|high
	Level        string  `json:"level"`                 // toujours "heuristic"
	Note         string  `json:"note"`
}

// OrientationCandidate est une orientation d'impression proposée (heuristique).
type OrientationCandidate struct {
	Label        string  `json:"label"`
	Up           Vec3    `json:"up"`       // direction du modèle qui devient +Z
	Rotation     Matrix  `json:"rotation"` // à appliquer au modèle (convention 3MF) pour l'obtenir
	HeightMM     float64 `json:"height_mm"`
	SizeXMM      float64 `json:"size_x_mm"`
	SizeYMM      float64 `json:"size_y_mm"`
	ContactMM2   float64 `json:"bed_contact_mm2"`
	OverhangMM2  float64 `json:"overhang_mm2"`
	OverhangFrac float64 `json:"overhang_fraction"`
	Fits         bool    `json:"fits"`
	Score        float64 `json:"score"`  // plus bas = mieux
	Source       string  `json:"source"` // axis|hull|current
}

// OrientationReport : classement des orientations.
type OrientationReport struct {
	Level        string                 `json:"level"` // "heuristic"
	ScoreFormula string                 `json:"score_formula"`
	Candidates   []OrientationCandidate `json:"candidates"`
	Best         int                    `json:"best_index"`
	Note         string                 `json:"note"`
}

// FabReport est le rapport de fabricabilité. Aucune de ces valeurs n'est une certification.
type FabReport struct {
	Target          Target             `json:"target"`
	Size            Vec3               `json:"size_mm"`
	FitsBed         bool               `json:"fits_bed"`
	FitsBedRotatedZ bool               `json:"fits_bed_rotated_90_about_z"`
	MarginMM        Vec3               `json:"margin_mm"`
	Overhang        OverhangReport     `json:"overhang"`
	Thin            ThinReport         `json:"thin_walls"`
	Orientation     *OrientationReport `json:"orientation,omitempty"`
	Warnings        []string           `json:"warnings,omitempty"`
}

const orientationFormula = "0,6·fraction_surplomb + 0,25·hauteur/diagonale + 0,15·(1 − min(1, contact/(10 % de la surface)))"

func (o *FabOptions) defaults() {
	if o.Target.BedX == 0 {
		o.Target = TargetP1S
	}
	if o.OverhangAngleDeg <= 0 {
		o.OverhangAngleDeg = 45
	}
	if o.Target.Nozzle <= 0 {
		o.Target.Nozzle = 0.4
	}
	if o.ThinThresholdMM <= 0 {
		o.ThinThresholdMM = 2 * o.Target.Nozzle
	}
	if o.RaySamples <= 0 {
		o.RaySamples = 2000
	}
}

// Fabricability analyse la pièce posée telle quelle (Z vers le haut, plan Z=min sur le plateau)
// pour la cible donnée. Les surplombs, épaisseurs et orientations sont des heuristiques.
func Fabricability(m *Mesh, opt FabOptions) (*FabReport, error) {
	opt.defaults()
	if len(m.T) == 0 {
		return nil, ErrEmpty
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	bb := m.Bounds()
	r := &FabReport{Target: opt.Target, Size: bb.Size}
	t := opt.Target
	r.FitsBed = bb.Size.X <= t.BedX && bb.Size.Y <= t.BedY && bb.Size.Z <= t.BedZ
	r.FitsBedRotatedZ = bb.Size.Y <= t.BedX && bb.Size.X <= t.BedY && bb.Size.Z <= t.BedZ
	r.MarginMM = Vec3{t.BedX - bb.Size.X, t.BedY - bb.Size.Y, t.BedZ - bb.Size.Z}
	switch {
	case r.FitsBed:
	case r.FitsBedRotatedZ:
		r.Warnings = append(r.Warnings, "la pièce ne tient que tournée de 90° autour de Z")
	default:
		r.Warnings = append(r.Warnings, fmt.Sprintf("la pièce (%.1f × %.1f × %.1f mm) dépasse le volume %s (%.0f × %.0f × %.0f mm) dans cette orientation : réorienter, découper ou changer de machine",
			bb.Size.X, bb.Size.Y, bb.Size.Z, t.Name, t.BedX, t.BedY, t.BedZ))
	}

	tol := opt.WeldTol
	if tol <= 0 {
		tol = math.Max(1e-6, 1e-6*bb.Size.Len())
	}
	remap, nw := Weld(m, tol)
	up := Vec3{0, 0, 1}
	r.Overhang = overhangs(m, remap, nw, up, opt.OverhangAngleDeg, true)
	if r.Overhang.Fraction > 0.05 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%.0f %% de la surface en surplomb > %.0f° : supports probables", 100*r.Overhang.Fraction, opt.OverhangAngleDeg))
	}

	rep := Analyze(m, AnalyzeOptions{WeldTol: tol})
	r.Thin = thickness(m, rep, opt)
	if r.Thin.ThinFraction > 0.02 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("épaisseur estimée < %.2f mm sur %.0f %% des échantillons : parois possiblement trop minces pour la buse %.1f mm (confiance %s)",
			opt.ThinThresholdMM, 100*r.Thin.ThinFraction, t.Nozzle, r.Thin.Confidence))
	}
	if !rep.Watertight {
		r.Warnings = append(r.Warnings, "maillage non étanche : épaisseurs et volume peu fiables, réparation nécessaire avant impression")
	}
	if !opt.SkipOrientation {
		r.Orientation = proposeOrientations(m, remap, nw, opt)
	}
	return r, nil
}

// overhangs calcule les surplombs pour une direction "haut" donnée. Les triangles posés sur le plateau
// (tous sommets à moins de 0,05 mm du minimum) ne sont pas des surplombs.
func overhangs(m *Mesh, remap []int, nw int, up Vec3, thrDeg float64, regions bool) OverhangReport {
	rep := OverhangReport{ThresholdDeg: thrDeg}
	minH := math.Inf(1)
	for _, v := range m.V {
		minH = math.Min(minH, v.Dot(up))
	}
	var total float64
	over := make([]bool, len(m.T))
	angle := make([]float64, len(m.T))
	areas := make([]float64, len(m.T))
	thr := math.Sin(thrDeg * math.Pi / 180)
	for i, t := range m.T {
		n, a := m.Normal(i)
		total += a
		if a == 0 {
			continue
		}
		s := -n.Dot(up) // composante vers le bas
		if s <= thr {
			continue
		}
		if m.V[t[0]].Dot(up) <= minH+0.05 && m.V[t[1]].Dot(up) <= minH+0.05 && m.V[t[2]].Dot(up) <= minH+0.05 {
			continue
		}
		over[i] = true
		angle[i] = math.Asin(math.Min(1, s)) * 180 / math.Pi
		areas[i] = a
		rep.AreaMM2 += a
	}
	if total > 0 {
		rep.Fraction = rep.AreaMM2 / total
	}
	if !regions {
		return rep
	}
	ds := newDSU(nw)
	for i, t := range m.T {
		if !over[i] {
			continue
		}
		ds.union(remap[t[0]], remap[t[1]])
		ds.union(remap[t[1]], remap[t[2]])
	}
	groups := map[int][]int{}
	for i, t := range m.T {
		if over[i] {
			groups[ds.find(remap[t[0]])] = append(groups[ds.find(remap[t[0]])], i)
		}
	}
	for _, tris := range groups {
		reg := OverhangRegion{Triangles: len(tris)}
		sub := &Mesh{V: m.V}
		for _, i := range tris {
			reg.AreaMM2 += areas[i]
			reg.MaxAngleDeg = math.Max(reg.MaxAngleDeg, angle[i])
			sub.T = append(sub.T, m.T[i])
		}
		reg.BBox = sub.usedBounds()
		rep.Regions = append(rep.Regions, reg)
	}
	rep.RegionCount = len(rep.Regions)
	sort.Slice(rep.Regions, func(a, b int) bool { return rep.Regions[a].AreaMM2 > rep.Regions[b].AreaMM2 })
	if len(rep.Regions) > 20 {
		rep.Regions = rep.Regions[:20]
	}
	return rep
}

// thickness estime l'épaisseur locale par un rayon partant du centre du triangle, dirigé contre la normale.
func thickness(m *Mesh, rep *Report, opt FabOptions) ThinReport {
	tr := ThinReport{ThresholdMM: opt.ThinThresholdMM, Level: "heuristic"}
	tr.Note = "Distance au premier triangle rencontré contre la normale ; surestime les parois obliques, sous-estime près des arêtes ; n'est pas une mesure de conception."
	flip := rep.Volume < 0
	b := buildBVH(m)
	diag := m.Bounds().Size.Len()
	eps := 1e-6 * math.Max(diag, 1)
	step := 1
	if len(m.T) > opt.RaySamples {
		step = len(m.T) / opt.RaySamples
	}
	type sample struct{ d, w float64 }
	var ds []sample
	var wAll, wThin float64
	for i := 0; i < len(m.T); i += step {
		n, a := m.Normal(i)
		if a == 0 {
			continue
		}
		if flip {
			n = n.Scale(-1)
		}
		tr.Samples++
		wAll += a
		o := b.centroid(i).Sub(n.Scale(eps))
		d, _, ok := b.nearest(o, n.Scale(-1), i)
		if !ok {
			continue
		}
		d += eps // l'origine a été décalée de eps vers l'intérieur
		tr.Hits++
		ds = append(ds, sample{d, a})
		if d < opt.ThinThresholdMM {
			tr.ThinCount++
			wThin += a
			if len(tr.ThinPoints) < 10 {
				tr.ThinPoints = append(tr.ThinPoints, b.centroid(i))
			}
		}
	}
	if len(ds) > 0 {
		sort.Slice(ds, func(i, j int) bool { return ds[i].d < ds[j].d })
		tr.MinMM = ds[0].d
		var tot float64
		for _, s := range ds {
			tot += s.w
		}
		var acc float64
		gotP5, gotMed := false, false
		for _, s := range ds {
			acc += s.w
			if !gotP5 && acc >= 0.05*tot {
				tr.P5MM, gotP5 = s.d, true
			}
			if !gotMed && acc >= 0.5*tot {
				tr.MedianMM, gotMed = s.d, true
			}
		}
	}
	if wAll > 0 {
		tr.ThinFraction = wThin / wAll
	}
	hitFrac := 0.0
	if tr.Samples > 0 {
		hitFrac = float64(tr.Hits) / float64(tr.Samples)
	}
	switch {
	case rep.Watertight && rep.InconsistentEdges == 0 && hitFrac > 0.9 && tr.Samples >= 50:
		tr.Confidence = "high"
	case hitFrac > 0.6 && tr.Samples >= 12:
		tr.Confidence = "medium"
	default:
		tr.Confidence = "low"
	}
	return tr
}

// rotationFor construit la matrice (convention 3MF) qui envoie le vecteur up sur +Z,
// avec une base arbitraire stable pour X/Y.
func rotationFor(up Vec3) Matrix {
	u := up.Norm()
	ref := Vec3{1, 0, 0}
	if math.Abs(u.X) > 0.9 {
		ref = Vec3{0, 1, 0}
	}
	e1 := ref.Sub(u.Scale(ref.Dot(u))).Norm()
	e2 := u.Cross(e1)
	// coordonnées tournées : x' = v·e1, y' = v·e2, z' = v·u  →  colonnes de la matrice ligne-vecteur
	return Matrix{e1.X, e2.X, u.X, e1.Y, e2.Y, u.Y, e1.Z, e2.Z, u.Z, 0, 0, 0}
}

func proposeOrientations(m *Mesh, remap []int, nw int, opt FabOptions) *OrientationReport {
	rep := &OrientationReport{
		Level: "heuristic", ScoreFormula: orientationFormula, Best: -1,
		Note: "Classement heuristique (surplomb, appui, hauteur) ; ignore la résistance inter-couches, l'aspect des faces visibles et les supports solubles. Les faces de la coque convexe sont approchées par des sommets extrêmes.",
	}
	type cand struct {
		up     Vec3
		label  string
		source string
	}
	cands := []cand{{Vec3{0, 0, 1}, "actuelle (+Z)", "current"}}
	axes := []struct {
		v Vec3
		l string
	}{{Vec3{0, 0, -1}, "face haute posée (−Z vers le haut)"}, {Vec3{1, 0, 0}, "+X vers le haut"}, {Vec3{-1, 0, 0}, "−X vers le haut"}, {Vec3{0, 1, 0}, "+Y vers le haut"}, {Vec3{0, -1, 0}, "−Y vers le haut"}}
	for _, a := range axes {
		cands = append(cands, cand{a.v, a.l, "axis"})
	}
	for i, hf := range principalHullFaces(hullPoints(m.V, 300), 6, math.Cos(2*math.Pi/180)) {
		cands = append(cands, cand{hf.normal.Scale(-1), fmt.Sprintf("face de coque convexe n°%d posée sur le plateau", i+1), "hull"})
	}
	var kept []cand
	for _, c := range cands {
		dup := false
		for _, k := range kept {
			if k.up.Dot(c.up) > 0.9998 {
				dup = true
				break
			}
		}
		if !dup {
			kept = append(kept, c)
		}
	}
	var area float64
	for i := range m.T {
		_, a := m.Normal(i)
		area += a
	}
	diag := m.Bounds().Size.Len()
	t := opt.Target
	cosFlat := math.Cos(1 * math.Pi / 180)
	for _, c := range kept {
		R := rotationFor(c.up)
		lo := Vec3{math.Inf(1), math.Inf(1), math.Inf(1)}
		hi := Vec3{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
		for _, v := range m.V {
			p := R.Apply(v)
			lo = Vec3{math.Min(lo.X, p.X), math.Min(lo.Y, p.Y), math.Min(lo.Z, p.Z)}
			hi = Vec3{math.Max(hi.X, p.X), math.Max(hi.Y, p.Y), math.Max(hi.Z, p.Z)}
		}
		sz := hi.Sub(lo)
		oc := OrientationCandidate{Label: c.label, Up: c.up, Rotation: R, HeightMM: sz.Z, SizeXMM: sz.X, SizeYMM: sz.Y, Source: c.source}
		oc.Fits = sz.Z <= t.BedZ && ((sz.X <= t.BedX && sz.Y <= t.BedY) || (sz.Y <= t.BedX && sz.X <= t.BedY))
		ov := overhangs(m, remap, nw, c.up, opt.OverhangAngleDeg, false)
		oc.OverhangMM2, oc.OverhangFrac = ov.AreaMM2, ov.Fraction
		for i, tr := range m.T {
			n, a := m.Normal(i)
			if a == 0 || -n.Dot(c.up) < cosFlat {
				continue
			}
			if m.V[tr[0]].Dot(c.up) <= lo.Z+0.05 && m.V[tr[1]].Dot(c.up) <= lo.Z+0.05 && m.V[tr[2]].Dot(c.up) <= lo.Z+0.05 {
				oc.ContactMM2 += a
			}
		}
		contactTerm := 1.0
		if area > 0 {
			contactTerm = 1 - math.Min(1, oc.ContactMM2/(0.1*area))
		}
		hn := 0.0
		if diag > 0 {
			hn = sz.Z / diag
		}
		oc.Score = 0.6*oc.OverhangFrac + 0.25*hn + 0.15*contactTerm
		if !oc.Fits {
			oc.Score += 10 // inutilisable sur cette machine
		}
		rep.Candidates = append(rep.Candidates, oc)
	}
	sort.SliceStable(rep.Candidates, func(i, j int) bool { return rep.Candidates[i].Score < rep.Candidates[j].Score })
	if len(rep.Candidates) > 0 {
		rep.Best = 0
	}
	return rep
}
