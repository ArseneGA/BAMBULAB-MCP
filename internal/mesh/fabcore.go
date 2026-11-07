package mesh

import (
	"fmt"
	"math"
	"slices"
	"time"
)

const (
	// defaultOverhangFromVerticalDeg : seuil de surplomb sans profil ni réglage explicite.
	defaultOverhangFromVerticalDeg = 45
	// defaultMaxFullTriangles : au-delà, les étapes coûteuses (orientations) travaillent sur un échantillon.
	defaultMaxFullTriangles = 1_000_000
	// thinConeHalfAngleDeg : demi-angle du cône de rayons d'épaisseur autour de -normale.
	thinConeHalfAngleDeg = 25.0
	thinRingRays         = 4
	// thinOpposingCos : un rayon du cône ne compte que s'il touche une face dont la normale est opposée à celle de
	// l'échantillon à plus de cos = 0,35 (≈ 70°) près.
	thinOpposingCos = 0.35
)

func (o *FabOptions) defaults() {
	if o.Target.BedX == 0 {
		o.Target, _ = NominalTarget("p1s")
	}
	switch {
	case o.OverhangAngleDeg > 0:
		o.overhangSource = "réglage explicite"
	case o.SupportThresholdDeg > 0 && o.SupportThresholdDeg < 90:
		// support_threshold_angle des profils Bambu : pente depuis l'horizontale ; on mesure depuis la verticale
		o.OverhangAngleDeg = 90 - o.SupportThresholdDeg
		o.overhangSource = o.ThresholdSource
		if o.overhangSource == "" {
			o.overhangSource = "support_threshold_angle"
		}
	default:
		o.OverhangAngleDeg = defaultOverhangFromVerticalDeg
		o.overhangSource = "défaut (45° depuis la verticale)"
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
	if o.MaxFullTriangles <= 0 {
		o.MaxFullTriangles = defaultMaxFullTriangles
	}
	if o.SelfIntersectBudget == 0 {
		o.SelfIntersectBudget = 5 * time.Second
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
	direct, rot1 := t.Fits(bb.Size)
	swapped, rot2 := t.Fits(Vec3{bb.Size.Y, bb.Size.X, bb.Size.Z})
	r.FitsBed = direct && !rot1
	r.FitsBedRotatedZ = swapped && !rot2
	r.MarginMM = Vec3{t.BedX - bb.Size.X, t.BedY - bb.Size.Y, t.BedZ - bb.Size.Z}
	if t.Source == TargetNominal {
		r.Warnings = append(r.Warnings, "machine nominale (profils Bambu Studio indisponibles) : volume et zones d'exclusion à confirmer")
	} else {
		r.Warnings = append(r.Warnings, t.Notes...)
	}
	switch {
	case r.FitsBed:
	case r.FitsBedRotatedZ:
		r.Warnings = append(r.Warnings, "la pièce ne tient que tournée de 90° autour de Z")
	default:
		fw, fh := t.UsableMaxFootprint()
		r.Warnings = append(r.Warnings, fmt.Sprintf("la pièce (%.1f × %.1f × %.1f mm) dépasse le volume utile %s (%.0f × %.0f × %.0f mm hors zones d'exclusion) dans cette orientation : réorienter, découper ou changer de machine",
			bb.Size.X, bb.Size.Y, bb.Size.Z, t.Name, fw, fh, t.BedZ))
	}
	if len(t.Volumes) > 1 && (r.FitsBed || r.FitsBedRotatedZ) {
		all1, _ := t.FitsAllNozzles(bb.Size)
		all2, _ := t.FitsAllNozzles(Vec3{bb.Size.Y, bb.Size.X, bb.Size.Z})
		if !all1 && !all2 {
			c, _, _ := t.CommonArea()
			r.Warnings = append(r.Warnings, fmt.Sprintf("la pièce tient avec une seule buse mais pas dans la zone commune aux %d buses (%.0f × %.0f mm) : pas de multi-matière entre buses", len(t.Volumes), c.W(), c.H()))
		}
	}

	tol := opt.WeldTol
	if tol <= 0 {
		tol = math.Max(1e-6, 1e-6*bb.Size.Len())
	}
	remap, nw := weld32(m.V, tol)
	up := Vec3{0, 0, 1}
	r.Overhang = overhangs(m, remap, nw, up, opt.OverhangAngleDeg, true, 1)
	r.Overhang.Source = opt.overhangSource
	if r.Overhang.Fraction > 0.05 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%.0f %% de la surface en surplomb > %.0f° depuis la verticale (seuil : %s) : supports probables", 100*r.Overhang.Fraction, opt.OverhangAngleDeg, opt.overhangSource))
	}

	// un seul BVH pour l'épaisseur et le contrôle des auto-intersections
	bvh := buildBVH(m)
	aopt := AnalyzeOptions{WeldTol: tol, SelfIntersectBudget: opt.SelfIntersectBudget, bvh: bvh}
	if opt.SkipSelfIntersections {
		aopt.SelfIntersectBudget = -1
	}
	rep, _ := analysisHeader(m, aopt)
	rep = analyzeWelded(m, aopt, rep, remap, nw)
	r.Integrity = integrityOf(rep)
	if rep.SelfIntersectionsChecked {
		r.SelfIntersections = &SelfIntersectionReport{Pairs: rep.SelfIntersectingPairs, Triangles: rep.SelfIntersectingTriangles,
			Regions: rep.SelfIntersectionRegions, RegionCount: rep.SelfIntersectionRegionCount, Total: len(m.T), Complete: rep.SelfIntersectionsComplete}
		if rep.SelfIntersectingPairs > 0 {
			r.Warnings = append(r.Warnings, fmt.Sprintf("%d paire(s) de triangles s'interpénètrent (%d région(s)) : auto-intersections, volume et épaisseur mal définis ; fusionner ou réparer", rep.SelfIntersectingPairs, rep.SelfIntersectionRegionCount))
		}
		if !rep.SelfIntersectionsComplete {
			r.Warnings = append(r.Warnings, "contrôle des auto-intersections partiel (limite de temps) : le compte est un minimum")
		}
	}
	r.Thin = thickness(m, rep, opt, bvh)
	if r.Thin.ThinFraction > 0.02 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("épaisseur estimée < %.2f mm sur %.0f %% de la surface : parois possiblement trop minces pour la buse %.1f mm (confiance %s)",
			opt.ThinThresholdMM, 100*r.Thin.ThinFraction, t.Nozzle, r.Thin.Confidence))
	}
	if !rep.Watertight {
		r.Warnings = append(r.Warnings, "maillage non étanche : épaisseurs et volume peu fiables, réparation nécessaire avant impression (repair_mesh)")
	}
	if rep.DuplicateTriangles > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%d triangle(s) dupliqué(s)", rep.DuplicateTriangles))
	}
	if rep.FloatingFragments > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%d éclat(s) flottant(s) de volume négligeable", rep.FloatingFragments))
	}
	if !opt.SkipOrientation {
		stride := 1
		if len(m.T) > opt.MaxFullTriangles {
			stride = (len(m.T) + opt.MaxFullTriangles - 1) / opt.MaxFullTriangles
			r.Sampling = append(r.Sampling, fmt.Sprintf("maillage de %d triangles (> %d) : les orientations sont notées sur un échantillon d'un triangle sur %d (surfaces extrapolées)", len(m.T), opt.MaxFullTriangles, stride))
		}
		r.Orientation = proposeOrientations(m, remap, nw, opt, stride)
	}
	if len(r.Sampling) > 0 {
		r.Sampled = true
	}
	return r, nil
}

// integrityOf résume les défauts d'intégrité du rapport d'analyse que Fabricability a calculé.
func integrityOf(rep *Report) *Integrity {
	return &Integrity{Watertight: rep.Watertight, OpenEdges: rep.OpenEdges, NonManifoldEdges: rep.NonManifoldEdges,
		InconsistentEdges: rep.InconsistentEdges, DegenerateTriangles: rep.DegenerateTriangles, DuplicateTriangles: rep.DuplicateTriangles,
		FloatingFragments: rep.FloatingFragments, Components: rep.Components, VolumeMM3: rep.Volume}
}

// Defects : nombre total de défauts topologiques (somme de tous les compteurs de défauts).
func (r *Report) Defects() int {
	return r.OpenEdges + r.NonManifoldEdges + r.InconsistentEdges + r.DegenerateTriangles + r.DuplicateTriangles + r.SelfIntersectingPairs
}

// overhangs calcule les surplombs pour une direction "haut" donnée. Les triangles posés sur le plateau
// (tous sommets à moins de 0,05 mm du minimum) ne sont pas des surplombs. stride > 1 : un triangle sur stride
// (échantillon, surfaces extrapolées) ; les régions exigent stride = 1.
func overhangs(m *Mesh, remap []int32, nw int, up Vec3, thrDeg float64, regions bool, stride int) OverhangReport {
	if stride < 1 || regions {
		stride = 1
	}
	rep := OverhangReport{ThresholdDeg: thrDeg, SupportThresholdDeg: 90 - thrDeg}
	minH := math.Inf(1)
	for _, v := range m.V {
		minH = math.Min(minH, v.Dot(up))
	}
	var total float64
	var over []bool
	var angle, areas []float32
	if regions {
		over = make([]bool, len(m.T))
		angle = make([]float32, len(m.T))
		areas = make([]float32, len(m.T))
	}
	thr := math.Sin(thrDeg * math.Pi / 180)
	for i := 0; i < len(m.T); i += stride {
		t := m.T[i]
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
		rep.AreaMM2 += a
		if regions {
			over[i] = true
			angle[i] = float32(math.Asin(math.Min(1, s)) * 180 / math.Pi)
			areas[i] = float32(a)
		}
	}
	if total > 0 {
		rep.Fraction = rep.AreaMM2 / total
	}
	rep.AreaMM2 *= float64(stride)
	if !regions {
		return rep
	}
	ds := newDSU(nw)
	for i, t := range m.T {
		if over[i] {
			ds.union(remap[t[0]], remap[t[1]])
			ds.union(remap[t[1]], remap[t[2]])
		}
	}
	type acc struct {
		reg    OverhangRegion
		lo, hi Vec3
	}
	groups := map[int32]*acc{}
	for i, t := range m.T {
		if !over[i] {
			continue
		}
		root := ds.find(remap[t[0]])
		g := groups[root]
		if g == nil {
			g = &acc{lo: Vec3{math.Inf(1), math.Inf(1), math.Inf(1)}, hi: Vec3{math.Inf(-1), math.Inf(-1), math.Inf(-1)}}
			groups[root] = g
		}
		g.reg.Triangles++
		g.reg.AreaMM2 += float64(areas[i])
		g.reg.MaxAngleDeg = math.Max(g.reg.MaxAngleDeg, float64(angle[i]))
		for _, k := range t {
			g.lo, g.hi = minMax(g.lo, g.hi, m.V[k])
		}
	}
	for _, g := range groups {
		g.reg.BBox = BBox{Min: g.lo, Max: g.hi, Size: g.hi.Sub(g.lo)}
		rep.Regions = append(rep.Regions, g.reg)
	}
	rep.RegionCount = len(rep.Regions)
	slices.SortFunc(rep.Regions, func(a, b OverhangRegion) int {
		switch {
		case a.AreaMM2 > b.AreaMM2:
			return -1
		case a.AreaMM2 < b.AreaMM2:
			return 1
		}
		return 0
	})
	if len(rep.Regions) > 20 {
		rep.Regions = rep.Regions[:20]
	}
	return rep
}

// halton : suite à faible discrépance (base b), pour répartir les points d'échantillonnage.
func halton(i, base int) float64 {
	f, r := 1.0, 0.0
	for i > 0 {
		f /= float64(base)
		r += f * float64(i%base)
		i /= base
	}
	return r
}

// coneBasis retourne deux vecteurs unitaires orthogonaux entre eux et à n.
func coneBasis(n Vec3) (Vec3, Vec3) {
	ref := Vec3{1, 0, 0}
	ax, ay, az := math.Abs(n.X), math.Abs(n.Y), math.Abs(n.Z)
	if ay < ax && ay <= az {
		ref = Vec3{0, 1, 0}
	} else if az < ax && az < ay {
		ref = Vec3{0, 0, 1}
	}
	u := ref.Cross(n).Norm()
	return u, n.Cross(u).Norm()
}

// localThickness estime l'épaisseur au point p de la surface (normale sortante n) par un cône de rayons
// (axe -n plus nbRing directions à coneDeg autour, tournées de phase) : la plus courte distance au premier
// triangle rencontré. Retourne aussi la distance sur l'axe seul (ancienne méthode) pour mesurer l'accord.
func localThickness(b *bvh, p, n Vec3, skip int, eps, coneDeg, phase float64, nbRing int) (min, axis float64, hits int, ok bool) {
	o := p.Sub(n.Scale(eps))
	min = math.Inf(1)
	if d, _, hit := b.nearest(o, n.Scale(-1), skip); hit {
		axis = d + eps
		min = axis
		hits++
	}
	if nbRing > 0 && coneDeg > 0 {
		u, v := coneBasis(n)
		sin, cos := math.Sincos(coneDeg * math.Pi / 180)
		for k := 0; k < nbRing; k++ {
			s, c := math.Sincos(phase + 2*math.Pi*float64(k)/float64(nbRing))
			dir := n.Scale(-cos).Add(u.Scale(c * sin)).Add(v.Scale(s * sin))
			if d, bi, hit := b.nearest(o, dir, skip); hit {
				// seule une face qui regarde la nôtre (paroi opposée) est une épaisseur : un rayon oblique qui sort
				// par une face latérale ne mesure pas la paroi.
				if hn, ha := b.m.Normal(bi); ha > 0 && hn.Dot(n) < -thinOpposingCos {
					hits++
					if d+eps < min {
						min = d + eps
					}
				}
			}
		}
	}
	return min, axis, hits, hits > 0 && !math.IsInf(min, 1)
}

// thickness estime l'épaisseur locale : échantillons répartis proportionnellement à l'aire des triangles
// (points quelconques de la surface, pas seulement les centroïdes), chacun avec un cône de rayons autour de
// -normale ; la valeur retenue est la plus courte distance du cône. La confiance tient compte de l'accord
// entre le rayon axial et le cône.
func thickness(m *Mesh, rep *Report, opt FabOptions, b *bvh) ThinReport {
	nRing := thinRingRays
	tr := ThinReport{ThresholdMM: opt.ThinThresholdMM, Level: "heuristic", Rays: 1 + nRing, ConeHalfAngleDeg: thinConeHalfAngleDeg, Sampling: "area_weighted"}
	tr.Note = fmt.Sprintf("Plus courte distance parmi %d rayons (axe −normale + %d à %.0f° autour) lancés depuis des points tirés au prorata de l'aire ; corrige la surestimation des parois obliques, sous-estime près des arêtes ; n'est pas une mesure de conception.", tr.Rays, nRing, thinConeHalfAngleDeg)
	flip := rep.Volume < 0
	diag := m.Bounds().Size.Len()
	eps := 1e-6 * math.Max(diag, 1)
	// aire totale des triangles non dégénérés
	var totalArea float64
	for i := range m.T {
		_, a := m.Normal(i)
		totalArea += a
	}
	nSamples := opt.RaySamples
	if totalArea == 0 || nSamples <= 0 {
		tr.Confidence = "low"
		return tr
	}
	type sample struct{ d, w float64 }
	var ds []sample
	var wAll, wThin float64
	agree := 0
	step := totalArea / float64(nSamples)
	next := step / 2
	var acc float64
	k := 0
	for i := range m.T {
		n, a := m.Normal(i)
		if a == 0 {
			continue
		}
		acc += a
		for acc >= next && k < nSamples {
			k++
			next += step
			// point tiré dans le triangle (suites de Halton 2 et 3)
			r1, r2 := halton(k, 2), halton(k, 3)
			s := math.Sqrt(r1)
			t := m.T[i]
			p := m.V[t[0]].Scale(1 - s).Add(m.V[t[1]].Scale(s * (1 - r2))).Add(m.V[t[2]].Scale(s * r2))
			nn := n
			if flip {
				nn = n.Scale(-1)
			}
			tr.Samples++
			w := totalArea / float64(nSamples)
			wAll += w
			phase := float64(k) * 2.399963229728653 // angle d'or : azimuts variés d'un échantillon à l'autre
			d, axis, _, ok := localThickness(b, p, nn, i, eps, thinConeHalfAngleDeg, phase, nRing)
			if !ok {
				continue
			}
			tr.Hits++
			ds = append(ds, sample{d, w})
			if axis > 0 && math.Abs(d-axis) <= 0.25*axis {
				agree++
			}
			if d < opt.ThinThresholdMM {
				tr.ThinCount++
				wThin += w
				if len(tr.ThinPoints) < 10 {
					tr.ThinPoints = append(tr.ThinPoints, p)
				}
			}
		}
	}
	if len(ds) > 0 {
		slices.SortFunc(ds, func(a, b sample) int {
			switch {
			case a.d < b.d:
				return -1
			case a.d > b.d:
				return 1
			}
			return 0
		})
		tr.MinMM = ds[0].d
		var tot float64
		for _, s := range ds {
			tot += s.w
		}
		var accw float64
		gotP5, gotMed := false, false
		for _, s := range ds {
			accw += s.w
			if !gotP5 && accw >= 0.05*tot {
				tr.P5MM, gotP5 = s.d, true
			}
			if !gotMed && accw >= 0.5*tot {
				tr.MedianMM, gotMed = s.d, true
			}
		}
		tr.Agreement = float64(agree) / float64(len(ds))
	}
	if wAll > 0 {
		tr.ThinFraction = wThin / wAll
	}
	hitFrac := 0.0
	if tr.Samples > 0 {
		hitFrac = float64(tr.Hits) / float64(tr.Samples)
	}
	clean := rep.Watertight && rep.InconsistentEdges == 0 && rep.SelfIntersectingPairs == 0
	switch {
	case clean && hitFrac > 0.9 && tr.Samples >= 200 && tr.Agreement >= 0.6:
		tr.Confidence = "high"
	case hitFrac > 0.6 && tr.Samples >= 12 && tr.Agreement >= 0.3:
		tr.Confidence = "medium"
	default:
		tr.Confidence = "low"
	}
	return tr
}
