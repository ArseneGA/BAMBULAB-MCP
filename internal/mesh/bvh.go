package mesh

import (
	"math"
	"sort"
)

// bvh est une hiérarchie de boîtes englobantes simple sur les triangles (partition médiane).
type bvh struct {
	m     *Mesh
	nodes []bvhNode
	order []int // indices de triangles
}

type bvhNode struct {
	lo, hi      Vec3
	left, right int // -1 si feuille
	start, n    int // plage dans order pour une feuille
}

func buildBVH(m *Mesh) *bvh {
	b := &bvh{m: m, order: make([]int, len(m.T))}
	for i := range b.order {
		b.order[i] = i
	}
	if len(m.T) > 0 {
		b.build(0, len(m.T))
	}
	return b
}

func (b *bvh) triBounds(i int) (lo, hi Vec3) {
	t := b.m.T[i]
	a, c, d := b.m.V[t[0]], b.m.V[t[1]], b.m.V[t[2]]
	lo = Vec3{math.Min(a.X, math.Min(c.X, d.X)), math.Min(a.Y, math.Min(c.Y, d.Y)), math.Min(a.Z, math.Min(c.Z, d.Z))}
	hi = Vec3{math.Max(a.X, math.Max(c.X, d.X)), math.Max(a.Y, math.Max(c.Y, d.Y)), math.Max(a.Z, math.Max(c.Z, d.Z))}
	return
}

func (b *bvh) centroid(i int) Vec3 {
	t := b.m.T[i]
	return b.m.V[t[0]].Add(b.m.V[t[1]]).Add(b.m.V[t[2]]).Scale(1.0 / 3)
}

func (b *bvh) build(start, end int) int {
	lo := Vec3{math.Inf(1), math.Inf(1), math.Inf(1)}
	hi := Vec3{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	clo, chi := lo, hi
	for _, ti := range b.order[start:end] {
		l, h := b.triBounds(ti)
		lo = Vec3{math.Min(lo.X, l.X), math.Min(lo.Y, l.Y), math.Min(lo.Z, l.Z)}
		hi = Vec3{math.Max(hi.X, h.X), math.Max(hi.Y, h.Y), math.Max(hi.Z, h.Z)}
		c := b.centroid(ti)
		clo = Vec3{math.Min(clo.X, c.X), math.Min(clo.Y, c.Y), math.Min(clo.Z, c.Z)}
		chi = Vec3{math.Max(chi.X, c.X), math.Max(chi.Y, c.Y), math.Max(chi.Z, c.Z)}
	}
	idx := len(b.nodes)
	b.nodes = append(b.nodes, bvhNode{lo: lo, hi: hi, left: -1, right: -1, start: start, n: end - start})
	if end-start <= 4 {
		return idx
	}
	ext := chi.Sub(clo)
	axis := 0
	if ext.Y > ext.X && ext.Y >= ext.Z {
		axis = 1
	} else if ext.Z > ext.X && ext.Z > ext.Y {
		axis = 2
	}
	key := func(ti int) float64 {
		c := b.centroid(ti)
		return [3]float64{c.X, c.Y, c.Z}[axis]
	}
	if [3]float64{ext.X, ext.Y, ext.Z}[axis] == 0 {
		return idx
	}
	seg := b.order[start:end]
	sort.Slice(seg, func(i, j int) bool { return key(seg[i]) < key(seg[j]) })
	mid := start + (end-start)/2
	l := b.build(start, mid)
	r := b.build(mid, end)
	b.nodes[idx].left, b.nodes[idx].right, b.nodes[idx].n = l, r, 0
	return idx
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
	stack := []int{0}
	for len(stack) > 0 {
		ni := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := &b.nodes[ni]
		if !rayBox(o, inv, n.lo, n.hi, best) {
			continue
		}
		if n.left < 0 {
			for _, ti := range b.order[n.start : n.start+n.n] {
				if ti == skip {
					continue
				}
				t := b.m.T[ti]
				if h, ok := rayTri(o, d, b.m.V[t[0]], b.m.V[t[1]], b.m.V[t[2]]); ok && h < best {
					best, bi = h, ti
				}
			}
			continue
		}
		stack = append(stack, n.left, n.right)
	}
	return best, bi, bi >= 0
}

func nz(x float64) float64 {
	if x == 0 {
		return 1e-300
	}
	return x
}
