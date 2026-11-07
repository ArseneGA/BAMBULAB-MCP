package mesh

import (
	"math"
)

// bvh est une hiérarchie de boîtes englobantes sur les triangles (partition médiane par sélection
// rapide : construction en O(n log n) sans tri). Les indices sont des int32 pour limiter la mémoire.
type bvh struct {
	m     *Mesh
	nodes []bvhNode
	order []int32 // indices de triangles, rangés par feuille
	cent  []Vec3  // centroïde de chaque triangle (indexé par triangle)
}

type bvhNode struct {
	lo, hi      Vec3
	left, right int32 // -1 si feuille
	start, n    int32 // plage dans order pour une feuille
}

const bvhLeaf = 8

// buildBVH construit la hiérarchie sur tous les triangles.
func buildBVH(m *Mesh) *bvh { return buildBVHFiltered(m, nil) }

// buildBVHFiltered ne retient que les triangles pour lesquels keep[i] est vrai (nil = tous).
func buildBVHFiltered(m *Mesh, keep []bool) *bvh {
	b := &bvh{m: m, cent: make([]Vec3, len(m.T))}
	b.order = make([]int32, 0, len(m.T))
	for i := range m.T {
		t := m.T[i]
		b.cent[i] = m.V[t[0]].Add(m.V[t[1]]).Add(m.V[t[2]]).Scale(1.0 / 3)
		if keep == nil || keep[i] {
			b.order = append(b.order, int32(i))
		}
	}
	if len(b.order) > 0 {
		b.nodes = make([]bvhNode, 0, 2*len(b.order)/bvhLeaf+8)
		b.build(0, len(b.order))
	}
	return b
}

func (b *bvh) triBounds(i int32) (lo, hi Vec3) {
	t := b.m.T[i]
	a, c, d := b.m.V[t[0]], b.m.V[t[1]], b.m.V[t[2]]
	lo = Vec3{math.Min(a.X, math.Min(c.X, d.X)), math.Min(a.Y, math.Min(c.Y, d.Y)), math.Min(a.Z, math.Min(c.Z, d.Z))}
	hi = Vec3{math.Max(a.X, math.Max(c.X, d.X)), math.Max(a.Y, math.Max(c.Y, d.Y)), math.Max(a.Z, math.Max(c.Z, d.Z))}
	return
}

func (b *bvh) centroid(i int) Vec3 { return b.cent[i] }

func (b *bvh) build(start, end int) int32 {
	lo := Vec3{math.Inf(1), math.Inf(1), math.Inf(1)}
	hi := Vec3{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	clo, chi := lo, hi
	for _, ti := range b.order[start:end] {
		l, h := b.triBounds(ti)
		lo = Vec3{math.Min(lo.X, l.X), math.Min(lo.Y, l.Y), math.Min(lo.Z, l.Z)}
		hi = Vec3{math.Max(hi.X, h.X), math.Max(hi.Y, h.Y), math.Max(hi.Z, h.Z)}
		c := b.cent[ti]
		clo = Vec3{math.Min(clo.X, c.X), math.Min(clo.Y, c.Y), math.Min(clo.Z, c.Z)}
		chi = Vec3{math.Max(chi.X, c.X), math.Max(chi.Y, c.Y), math.Max(chi.Z, c.Z)}
	}
	idx := int32(len(b.nodes))
	b.nodes = append(b.nodes, bvhNode{lo: lo, hi: hi, left: -1, right: -1, start: int32(start), n: int32(end - start)})
	if end-start <= bvhLeaf {
		return idx
	}
	ext := chi.Sub(clo)
	axis := 0
	if ext.Y > ext.X && ext.Y >= ext.Z {
		axis = 1
	} else if ext.Z > ext.X && ext.Z > ext.Y {
		axis = 2
	}
	mid := start + (end-start)/2
	// Centroïdes tous identiques : aucune sélection utile, mais on coupe quand même en deux
	// pour que les feuilles restent bornées.
	if [3]float64{ext.X, ext.Y, ext.Z}[axis] != 0 {
		b.selectNth(start, end, mid, axis)
	}
	l := b.build(start, mid)
	r := b.build(mid, end)
	b.nodes[idx].left, b.nodes[idx].right, b.nodes[idx].n = l, r, 0
	return idx
}

func (b *bvh) key(ti int32, axis int) float64 {
	c := b.cent[ti]
	switch axis {
	case 0:
		return c.X
	case 1:
		return c.Y
	}
	return c.Z
}

// selectNth place dans order[k] l'élément de rang k (clé = centroïde sur axis) ; les éléments avant ont une clé
// ≤, ceux après ≥ (sélection rapide de Hoare, pivot médian de trois).
func (b *bvh) selectNth(lo, hi, k, axis int) {
	o := b.order
	l, r := lo, hi-1
	for l < r {
		m := l + (r-l)/2
		a, c, d := b.key(o[l], axis), b.key(o[m], axis), b.key(o[r], axis)
		piv := c
		if (a <= c && c <= d) || (d <= c && c <= a) {
			piv = c
		} else if (c <= a && a <= d) || (d <= a && a <= c) {
			piv = a
		} else {
			piv = d
		}
		i, j := l, r
		for i <= j {
			for b.key(o[i], axis) < piv {
				i++
			}
			for b.key(o[j], axis) > piv {
				j--
			}
			if i <= j {
				o[i], o[j] = o[j], o[i]
				i++
				j--
			}
		}
		switch {
		case k <= j:
			r = j
		case k >= i:
			l = i
		default:
			return
		}
	}
}

func rayBox(o, invD, lo, hi Vec3, tmax float64) bool {
	t1 := (lo.X - o.X) * invD.X
	t2 := (hi.X - o.X) * invD.X
	tmin, tm := math.Min(t1, t2), math.Max(t1, t2)
	t1 = (lo.Y - o.Y) * invD.Y
	t2 = (hi.Y - o.Y) * invD.Y
	tmin, tm = math.Max(tmin, math.Min(t1, t2)), math.Min(tm, math.Max(t1, t2))
	t1 = (lo.Z - o.Z) * invD.Z
	t2 = (hi.Z - o.Z) * invD.Z
	tmin, tm = math.Max(tmin, math.Min(t1, t2)), math.Min(tm, math.Max(t1, t2))
	return tm >= math.Max(tmin, 0) && tmin <= tmax
}

// rayTri : Möller–Trumbore, retourne t > eps.
func rayTri(o, d, a, b, c Vec3) (float64, bool) {
	e1, e2 := b.Sub(a), c.Sub(a)
	p := d.Cross(e2)
	det := e1.Dot(p)
	if math.Abs(det) < 1e-18 {
		return 0, false
	}
	inv := 1 / det
	s := o.Sub(a)
	u := s.Dot(p) * inv
	if u < 0 || u > 1 {
		return 0, false
	}
	q := s.Cross(e1)
	v := d.Dot(q) * inv
	if v < 0 || u+v > 1 {
		return 0, false
	}
	t := e2.Dot(q) * inv
	return t, t > 0
}

// nearest retourne la distance au premier triangle touché (hors skip) et son index.
func (b *bvh) nearest(o, d Vec3, skip int) (float64, int, bool) {
	if len(b.nodes) == 0 {
		return 0, -1, false
	}
	inv := Vec3{1 / nz(d.X), 1 / nz(d.Y), 1 / nz(d.Z)}
	best := math.Inf(1)
	bi := -1
	var stackArr [96]int32
	stack := stackArr[:1]
	for len(stack) > 0 {
		ni := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := &b.nodes[ni]
		if !rayBox(o, inv, n.lo, n.hi, best) {
			continue
		}
		if n.left < 0 {
			for _, ti := range b.order[n.start : n.start+n.n] {
				if int(ti) == skip {
					continue
				}
				t := b.m.T[ti]
				if h, ok := rayTri(o, d, b.m.V[t[0]], b.m.V[t[1]], b.m.V[t[2]]); ok && h < best {
					best, bi = h, int(ti)
				}
			}
			continue
		}
		stack = append(stack, n.left, n.right)
	}
	return best, bi, bi >= 0
}

// overlapping appelle f pour chaque triangle dont la boîte chevauche [lo, hi] ; f retourne faux pour arrêter.
func (b *bvh) overlapping(lo, hi Vec3, f func(ti int32) bool) {
	if len(b.nodes) == 0 {
		return
	}
	var stackArr [96]int32
	stack := stackArr[:1]
	for len(stack) > 0 {
		ni := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := &b.nodes[ni]
		if n.lo.X > hi.X || n.hi.X < lo.X || n.lo.Y > hi.Y || n.hi.Y < lo.Y || n.lo.Z > hi.Z || n.hi.Z < lo.Z {
			continue
		}
		if n.left < 0 {
			for _, ti := range b.order[n.start : n.start+n.n] {
				tlo, thi := b.triBounds(ti)
				if tlo.X > hi.X || thi.X < lo.X || tlo.Y > hi.Y || thi.Y < lo.Y || tlo.Z > hi.Z || thi.Z < lo.Z {
					continue
				}
				if !f(ti) {
					return
				}
			}
			continue
		}
		stack = append(stack, n.left, n.right)
	}
}

func nz(x float64) float64 {
	if x == 0 {
		return 1e-300
	}
	return x
}
