package mesh

import (
	"fmt"
	"math"
	"slices"
)

// signedVolume calcule le volume signé d'un ensemble de triangles (origine recentrée pour la précision).
func signedVolume(m *Mesh, tris []int, origin Vec3) float64 {
	var v float64
	for _, i := range tris {
		t := m.T[i]
		a, b, c := m.V[t[0]].Sub(origin), m.V[t[1]].Sub(origin), m.V[t[2]].Sub(origin)
		v += a.Dot(b.Cross(c))
	}
	return v / 6
}

func (m *Mesh) usedBounds() BBox {
	lo := Vec3{math.Inf(1), math.Inf(1), math.Inf(1)}
	hi := Vec3{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	any := false
	for _, t := range m.T {
		for _, k := range t {
			v := m.V[k]
			lo = Vec3{math.Min(lo.X, v.X), math.Min(lo.Y, v.Y), math.Min(lo.Z, v.Z)}
			hi = Vec3{math.Max(hi.X, v.X), math.Max(hi.Y, v.Y), math.Max(hi.Z, v.Z)}
			any = true
		}
	}
	if !any {
		return BBox{}
	}
	return BBox{Min: lo, Max: hi, Size: hi.Sub(lo)}
}

// maxComponentList : nombre maximal de composantes détaillées dans le rapport.
const maxComponentList = 50

// Analyze calcule les mesures géométriques et la qualité topologique d'un maillage.
func Analyze(m *Mesh, opt AnalyzeOptions) *Report {
	r, tol := analysisHeader(m, opt)
	if len(m.T) == 0 {
		return r
	}
	remap, nw := weld32(m.V, tol)
	return analyzeWelded(m, opt, r, remap, nw)
}

func analysisHeader(m *Mesh, opt AnalyzeOptions) (*Report, float64) {
	r := &Report{Triangles: len(m.T), RawVertices: len(m.V)}
	r.BBox = m.Bounds()
	diag := r.BBox.Size.Len()
	tol := opt.WeldTol
	if tol <= 0 {
		tol = math.Max(1e-6, 1e-6*diag)
	}
	r.WeldTol = tol
	if len(m.T) == 0 {
		r.Warnings = append(r.Warnings, "maillage vide")
	}
	return r, tol
}

// analyzeWelded : cœur de l'analyse sur un maillage déjà soudé (remap, nw). Structures plates : les arêtes sont
// des clés uint64 triées (pas de map), les composantes sont comptées par racine d'union-find.
func analyzeWelded(m *Mesh, opt AnalyzeOptions, r *Report, remap []int32, nw int) *Report {
	deg := opt.DegenerateAreaEps
	if deg <= 0 {
		d := 1e-6 * r.BBox.Size.Len()
		deg = math.Max(1e-12, d*d)
	}
	r.WeldedVertices = nw

	valid := make([]bool, len(m.T))
	nvalid := 0
	ds := newDSU(nw)
	for i, t := range m.T {
		a, b, c := m.V[t[0]], m.V[t[1]], m.V[t[2]]
		n := b.Sub(a).Cross(c.Sub(a))
		area := n.Len() / 2
		ia, ib, ic := remap[t[0]], remap[t[1]], remap[t[2]]
		if area <= deg || ia == ib || ib == ic || ia == ic {
			r.DegenerateTriangles++
			continue
		}
		valid[i] = true
		nvalid++
		r.Area += area
		ds.union(ia, ib)
		ds.union(ib, ic)
	}

	// arêtes : clé = min<<32 | max<<1 | (1 si parcourue dans le sens croissant)
	keys := make([]uint64, 0, 3*nvalid)
	for i, t := range m.T {
		if !valid[i] {
			continue
		}
		a, b, c := uint64(remap[t[0]]), uint64(remap[t[1]]), uint64(remap[t[2]])
		keys = append(keys, edgeKey(a, b), edgeKey(b, c), edgeKey(c, a))
	}
	slices.Sort(keys)
	var nonManifold []uint64
	for i := 0; i < len(keys); {
		j := i
		fwd, bwd := 0, 0
		for j < len(keys) && keys[j]>>1 == keys[i]>>1 {
			if keys[j]&1 == 1 {
				fwd++
			} else {
				bwd++
			}
			j++
		}
		r.Edges++
		switch total := fwd + bwd; {
		case total == 1:
			r.OpenEdges++
		case total > 2:
			r.NonManifoldEdges++
			if len(nonManifold) < 1<<20 {
				nonManifold = append(nonManifold, keys[i]>>1)
			}
		case fwd != bwd:
			r.InconsistentEdges++
		}
		i = j
	}
	keys = nil

	origin := r.BBox.Min.Add(r.BBox.Size.Scale(0.5))
	counts := make([]int32, nw)
	vols := make([]float64, nw)
	var total float64
	for i, t := range m.T {
		if !valid[i] {
			continue
		}
		a, b, c := m.V[t[0]].Sub(origin), m.V[t[1]].Sub(origin), m.V[t[2]].Sub(origin)
		v := a.Dot(b.Cross(c))
		root := ds.find(remap[t[0]])
		counts[root]++
		vols[root] += v
		total += v
	}
	r.Volume = total / 6
	r.Watertight = r.OpenEdges == 0 && r.NonManifoldEdges == 0 && nvalid > 0
	r.InvertedNormals = r.Watertight && r.InconsistentEdges == 0 && r.Volume < 0

	// triangles dupliqués : seulement parmi ceux qui touchent une arête non manifold (cas rare)
	if len(nonManifold) > 0 {
		set := make(map[uint64]struct{}, len(nonManifold))
		for _, k := range nonManifold {
			set[k] = struct{}{}
		}
		seen := map[[3]int32]int{}
		for i, t := range m.T {
			if !valid[i] {
				continue
			}
			a, b, c := uint64(remap[t[0]]), uint64(remap[t[1]]), uint64(remap[t[2]])
			_, e1 := set[edgeKey(a, b)>>1]
			_, e2 := set[edgeKey(b, c)>>1]
			_, e3 := set[edgeKey(c, a)>>1]
			if !(e1 || e2 || e3) {
				continue
			}
			tri := [3]int32{remap[t[0]], remap[t[1]], remap[t[2]]}
			slices.Sort(tri[:])
			seen[tri]++
		}
		for _, n := range seen {
			r.DuplicateTriangles += n - 1
		}
	}

	// composantes connexes
	var roots []int32
	for root, n := range counts {
		if n > 0 {
			roots = append(roots, int32(root))
		}
	}
	r.Components = len(roots)
	if len(roots) > 0 {
		slices.SortFunc(roots, func(a, b int32) int {
			if counts[a] != counts[b] {
				return int(counts[b]) - int(counts[a])
			}
			return int(a) - int(b)
		})
		top := roots
		if len(top) > maxComponentList {
			top = top[:maxComponentList]
		}
		rank := make(map[int32]int, len(top))
		list := make([]ComponentInfo, len(top))
		los := make([]Vec3, len(top))
		his := make([]Vec3, len(top))
		for i, root := range top {
			rank[root] = i
			list[i] = ComponentInfo{Triangles: int(counts[root]), Volume: vols[root] / 6}
			los[i] = Vec3{math.Inf(1), math.Inf(1), math.Inf(1)}
			his[i] = Vec3{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
		}
		if len(roots) == 1 {
			// une seule composante : sa boîte est celle des sommets utilisés des triangles valides
			for i, t := range m.T {
				if valid[i] {
					for _, k := range t {
						los[0], his[0] = minMax(los[0], his[0], m.V[k])
					}
				}
			}
		} else {
			for i, t := range m.T {
				if !valid[i] {
					continue
				}
				if k, ok := rank[ds.find(remap[t[0]])]; ok {
					for _, vi := range t {
						los[k], his[k] = minMax(los[k], his[k], m.V[vi])
					}
				}
			}
		}
		for i := range list {
			list[i].BBox = BBox{Min: los[i], Max: his[i], Size: his[i].Sub(los[i])}
		}
		r.ComponentList = list

		// éclats flottants : volume négligeable devant la plus grosse composante
		maxVol := 0.0
		for _, root := range roots {
			maxVol = math.Max(maxVol, math.Abs(vols[root]/6))
		}
		smallest := math.Inf(1)
		if len(roots) > 1 && maxVol > 0 {
			for _, root := range roots {
				if v := math.Abs(vols[root] / 6); v < 1e-3*maxVol {
					r.FloatingFragments++
					smallest = math.Min(smallest, v)
				}
			}
		}
		if r.FloatingFragments > 0 {
			r.Warnings = append(r.Warnings, fmt.Sprintf("%d éclat(s) flottant(s) : composante(s) de volume négligeable (< 0,1 %% de la pièce principale, plus petit %.3g mm³), souvent un résidu de modélisation à supprimer", r.FloatingFragments, smallest))
		}
	}

	if r.DegenerateTriangles > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%d triangle(s) dégénéré(s) (aire nulle ou sommets confondus)", r.DegenerateTriangles))
	}
	if r.DuplicateTriangles > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%d triangle(s) dupliqué(s) (mêmes trois sommets) : à supprimer, ils créent des arêtes non manifold", r.DuplicateTriangles))
	}
	if r.OpenEdges > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%d arête(s) ouverte(s) : maillage non étanche (trous)", r.OpenEdges))
	}
	if r.NonManifoldEdges > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%d arête(s) non manifold (plus de 2 triangles)", r.NonManifoldEdges))
	}
	if r.InconsistentEdges > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%d arête(s) d'orientation incohérente : normales retournées sur certaines faces", r.InconsistentEdges))
	}
	if r.InvertedNormals {
		r.Warnings = append(r.Warnings, "volume signé négatif : toutes les normales pointent vers l'intérieur")
	}
	if r.Components > 1 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%d composantes connexes (pièces disjointes)", r.Components))
	}
	fillSelfIntersections(m, valid, remap, opt, r)
	return r
}

func edgeKey(a, b uint64) uint64 {
	if a < b {
		return a<<32 | b<<1 | 1
	}
	return b<<32 | a<<1
}

func minMax(lo, hi, v Vec3) (Vec3, Vec3) {
	return Vec3{math.Min(lo.X, v.X), math.Min(lo.Y, v.Y), math.Min(lo.Z, v.Z)},
		Vec3{math.Max(hi.X, v.X), math.Max(hi.Y, v.Y), math.Max(hi.Z, v.Z)}
}
