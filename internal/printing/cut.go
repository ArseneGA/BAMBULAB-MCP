package printing

import (
	"errors"
	"fmt"
	"math"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

func comp(v mesh.Vec3, axis int) float64 {
	switch axis {
	case 0:
		return v.X
	case 1:
		return v.Y
	}
	return v.Z
}

type cutPt struct {
	p     mesh.Vec3
	fresh bool
}

type segPair struct{ a, b mesh.Vec3 }

type qkey [3]int64

func keyOf(v mesh.Vec3) qkey {
	const q = 1e5
	return qkey{int64(math.Round(v.X * q)), int64(math.Round(v.Y * q)), int64(math.Round(v.Z * q))}
}

// cutPlane conserve la partie du maillage du côté demandé du plan axe = offset, puis
// referme la coupe (capuchon) quand la section est faite de contours simples non imbriqués.
func cutPlane(m *mesh.Mesh, axis int, offset float64, keepAbove bool) (*mesh.Mesh, string, []string, error) {
	sgn := 1.0
	if !keepAbove {
		sgn = -1
	}
	dist := func(v mesh.Vec3) float64 { return (comp(v, axis) - offset) * sgn }
	b := m.Bounds()
	lo, hi := comp(b.Min, axis), comp(b.Max, axis)
	if offset <= lo-1e-9 && keepAbove || offset >= hi+1e-9 && !keepAbove {
		return nil, "", nil, errors.New("le plan ne coupe rien : tout le modèle est conservé (opération sans effet)")
	}
	if offset >= hi-1e-9 && keepAbove || offset <= lo+1e-9 && !keepAbove {
		return nil, "", nil, errors.New("le plan est au-delà du modèle : il ne resterait rien")
	}
	out := &mesh.Mesh{}
	var segs []segPair
	addTri := func(a, b, c mesh.Vec3) {
		if b.Sub(a).Cross(c.Sub(a)).Len() < 1e-12 {
			return
		}
		i := len(out.V)
		out.V = append(out.V, a, b, c)
		out.T = append(out.T, mesh.Tri{i, i + 1, i + 2})
	}
	for _, t := range m.T {
		vs := [3]mesh.Vec3{m.V[t[0]], m.V[t[1]], m.V[t[2]]}
		var d [3]float64
		allIn, allOut := true, true
		for i := range vs {
			d[i] = dist(vs[i])
			if d[i] < 0 {
				allIn = false
			} else {
				allOut = false
			}
		}
		if allIn {
			addTri(vs[0], vs[1], vs[2])
			continue
		}
		if allOut {
			continue
		}
		// découpe de Sutherland-Hodgman contre le demi-espace d >= 0
		var poly []cutPt
		for i := 0; i < 3; i++ {
			j := (i + 1) % 3
			if d[i] >= 0 {
				poly = append(poly, cutPt{p: vs[i]})
			}
			if (d[i] >= 0) != (d[j] >= 0) {
				tt := d[i] / (d[i] - d[j])
				poly = append(poly, cutPt{p: vs[i].Add(vs[j].Sub(vs[i]).Scale(tt)), fresh: true})
			}
		}
		for i := 1; i+1 < len(poly); i++ {
			addTri(poly[0].p, poly[i].p, poly[i+1].p)
		}
		var nf []mesh.Vec3
		for _, p := range poly {
			if p.fresh {
				nf = append(nf, p.p)
			}
		}
		if len(nf) == 2 && nf[0].Sub(nf[1]).Len() > 1e-9 {
			segs = append(segs, segPair{nf[0], nf[1]})
		}
	}
	if len(out.T) == 0 {
		return nil, "", nil, errors.New("il ne resterait rien après la coupe")
	}
	var warns []string
	detail := fmt.Sprintf("coupe par le plan %c = %.4g mm, partie conservée : %s", "xyz"[axis], offset, map[bool]string{true: "au-dessus", false: "en dessous"}[keepAbove])
	loops, open := chainLoops(segs)
	switch {
	case len(segs) == 0:
		detail += " ; coupe le long de faces existantes, aucun capuchon nécessaire"
	case open > 0:
		warns = append(warns, fmt.Sprintf("la section contient %d chaîne(s) ouverte(s) (maillage d'origine non étanche) : coupe non refermée", open))
		detail += " ; capuchon NON posé"
	default:
		u, v := (axis+1)%3, (axis+2)%3
		to2 := func(p mesh.Vec3) [2]float64 { return [2]float64{comp(p, u), comp(p, v)} }
		polys := make([][][2]float64, len(loops))
		for i, l := range loops {
			for _, p := range l {
				polys[i] = append(polys[i], to2(p))
			}
		}
		if nested(polys) {
			warns = append(warns, "la section a plusieurs contours imbriqués (pièce creuse) : le capuchon n'est pas posé, la coupe reste ouverte — fermer la coupe dans le logiciel de CAO")
			detail += " ; capuchon NON posé (section creuse)"
		} else {
			capped := 0
			for i, pl := range polys {
				tris, ok := earClip(pl)
				if !ok {
					warns = append(warns, "triangulation du capuchon approximative sur un contour difficile")
				}
				for _, tr := range tris {
					a, b, c := loops[i][tr[0]], loops[i][tr[1]], loops[i][tr[2]]
					// (u,v) direct => normale +axe ; on veut la normale côté retiré
					if keepAbove {
						b, c = c, b
					}
					addTri(a, b, c)
				}
				capped++
			}
			detail += fmt.Sprintf(" ; capuchon posé sur %d contour(s)", capped)
		}
	}
	return out, detail, warns, nil
}

// chainLoops relie les segments en contours fermés ; open compte les chaînes non refermées.
func chainLoops(segs []segPair) (loops [][]mesh.Vec3, open int) {
	adj := map[qkey][]int{}
	for i, s := range segs {
		adj[keyOf(s.a)] = append(adj[keyOf(s.a)], i)
		adj[keyOf(s.b)] = append(adj[keyOf(s.b)], i)
	}
	used := make([]bool, len(segs))
	for i := range segs {
		if used[i] {
			continue
		}
		used[i] = true
		loop := []mesh.Vec3{segs[i].a}
		cur := segs[i].b
		start := keyOf(segs[i].a)
		closed := false
		for steps := 0; steps < len(segs)+2; steps++ {
			if keyOf(cur) == start {
				closed = true
				break
			}
			loop = append(loop, cur)
			next := -1
			for _, j := range adj[keyOf(cur)] {
				if !used[j] {
					next = j
					break
				}
			}
			if next < 0 {
				break
			}
			used[next] = true
			if keyOf(segs[next].a) == keyOf(cur) {
				cur = segs[next].b
			} else {
				cur = segs[next].a
			}
		}
		if closed && len(loop) >= 3 {
			loops = append(loops, loop)
		} else {
			open++
		}
	}
	return loops, open
}

func area2(p [][2]float64) float64 {
	a := 0.0
	for i := range p {
		j := (i + 1) % len(p)
		a += p[i][0]*p[j][1] - p[j][0]*p[i][1]
	}
	return a / 2
}

func inPoly(pt [2]float64, p [][2]float64) bool {
	in := false
	for i, j := 0, len(p)-1; i < len(p); j, i = i, i+1 {
		if (p[i][1] > pt[1]) != (p[j][1] > pt[1]) && pt[0] < (p[j][0]-p[i][0])*(pt[1]-p[i][1])/(p[j][1]-p[i][1])+p[i][0] {
			in = !in
		}
	}
	return in
}

func nested(polys [][][2]float64) bool {
	for i := range polys {
		for j := range polys {
			if i != j && len(polys[i]) > 0 && inPoly(polys[i][0], polys[j]) {
				return true
			}
		}
	}
	return false
}

// earClip triangule un contour simple ; renvoie des index dans le contour d'origine,
// orientés dans le sens antihoraire (repère direct).
func earClip(p [][2]float64) ([][3]int, bool) {
	n := len(p)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	if area2(p) < 0 {
		for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
			idx[i], idx[j] = idx[j], idx[i]
		}
	}
	cross := func(a, b, c int) float64 {
		return (p[b][0]-p[a][0])*(p[c][1]-p[a][1]) - (p[b][1]-p[a][1])*(p[c][0]-p[a][0])
	}
	var out [][3]int
	for guard := 0; len(idx) > 3 && guard < 4*n*n; guard++ {
		found := false
		for i := range idx {
			a, b, c := idx[(i+len(idx)-1)%len(idx)], idx[i], idx[(i+1)%len(idx)]
			if cross(a, b, c) <= 1e-12 {
				continue
			}
			ok := true
			for _, o := range idx {
				if o == a || o == b || o == c {
					continue
				}
				if cross(a, b, o) >= -1e-12 && cross(b, c, o) >= -1e-12 && cross(c, a, o) >= -1e-12 {
					ok = false
					break
				}
			}
			if ok {
				out = append(out, [3]int{a, b, c})
				idx = append(idx[:i], idx[i+1:]...)
				found = true
				break
			}
		}
		if !found { // sommets colinéaires/dégénérés : retire le plus plat
			best, bv := 0, math.Inf(1)
			for i := range idx {
				a, b, c := idx[(i+len(idx)-1)%len(idx)], idx[i], idx[(i+1)%len(idx)]
				if v := math.Abs(cross(a, b, c)); v < bv {
					best, bv = i, v
				}
			}
			idx = append(idx[:best], idx[best+1:]...)
		}
	}
	if len(idx) == 3 {
		out = append(out, [3]int{idx[0], idx[1], idx[2]})
		return out, true
	}
	return out, false
}
