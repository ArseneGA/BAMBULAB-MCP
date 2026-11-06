package mesh

import "math"

type hullFace struct {
	a, b, c int
	n       Vec3
	d       float64
	alive   bool
}

// hullPoints réduit un nuage de sommets à des points extrêmes dans ~k directions (suite de
// Fibonacci sur la sphère) : sous-ensemble exact des sommets de l'enveloppe convexe, d'où une
// enveloppe approchée par l'intérieur (heuristique) mais bornée en coût.
func hullPoints(v []Vec3, k int) []Vec3 {
	if len(v) <= k {
		return append([]Vec3(nil), v...)
	}
	seen := map[int]bool{}
	var out []Vec3
	// axes + diagonales d'abord, pour garantir l'exactitude des boîtes/prismes droits
	dirs := make([]Vec3, 0, k+26)
	for x := -1; x <= 1; x++ {
		for y := -1; y <= 1; y++ {
			for z := -1; z <= 1; z++ {
				if x != 0 || y != 0 || z != 0 {
					dirs = append(dirs, Vec3{float64(x), float64(y), float64(z)}.Norm())
				}
			}
		}
	}
	ga := math.Pi * (3 - math.Sqrt(5))
	for i := 0; i < k; i++ {
		y := 1 - 2*(float64(i)+0.5)/float64(k)
		r := math.Sqrt(1 - y*y)
		dirs = append(dirs, Vec3{math.Cos(ga*float64(i)) * r, y, math.Sin(ga*float64(i)) * r})
	}
	for _, d := range dirs {
		best, bi := math.Inf(-1), -1
		for i, p := range v {
			if s := p.Dot(d); s > best {
				best, bi = s, i
			}
		}
		if bi >= 0 && !seen[bi] {
			seen[bi] = true
			out = append(out, v[bi])
		}
	}
	return out
}

// convexHull calcule l'enveloppe convexe 3D (incrémentale). Retourne nil si les points sont dégénérés (coplanaires).
func convexHull(pts []Vec3, eps float64) (faces []hullFace, ok bool) {
	n := len(pts)
	if n < 4 {
		return nil, false
	}
	i0 := 0
	i1, best := -1, 0.0
	for i, p := range pts {
		if d := p.Sub(pts[i0]).Len(); d > best {
			best, i1 = d, i
		}
	}
	if best < eps {
		return nil, false
	}
	i2, best2 := -1, 0.0
	dir := pts[i1].Sub(pts[i0]).Norm()
	for i, p := range pts {
		if d := p.Sub(pts[i0]).Cross(dir).Len(); d > best2 {
			best2, i2 = d, i
		}
	}
	if best2 < eps {
		return nil, false
	}
	nrm := pts[i1].Sub(pts[i0]).Cross(pts[i2].Sub(pts[i0])).Norm()
	i3, best3 := -1, 0.0
	for i, p := range pts {
		if d := math.Abs(p.Sub(pts[i0]).Dot(nrm)); d > best3 {
			best3, i3 = d, i
		}
	}
	if best3 < eps {
		return nil, false
	}
	center := pts[i0].Add(pts[i1]).Add(pts[i2]).Add(pts[i3]).Scale(0.25)
	mk := func(a, b, c int) hullFace {
		nn := pts[b].Sub(pts[a]).Cross(pts[c].Sub(pts[a])).Norm()
		d := nn.Dot(pts[a])
		if nn.Dot(center)-d > 0 { // le centre doit être derrière la face
			a, b = b, a
			nn = nn.Scale(-1)
			d = -d
		}
		return hullFace{a: a, b: b, c: c, n: nn, d: d, alive: true}
	}
	faces = []hullFace{mk(i0, i1, i2), mk(i0, i1, i3), mk(i0, i2, i3), mk(i1, i2, i3)}
	// mk peut échanger a et b mais pas c : l'orientation (a,b,c) reste cohérente avec n.
	used := map[int]bool{i0: true, i1: true, i2: true, i3: true}
	for pi, p := range pts {
		if used[pi] {
			continue
		}
		var vis []int
		for fi := range faces {
			f := &faces[fi]
			if f.alive && f.n.Dot(p)-f.d > eps {
				vis = append(vis, fi)
			}
		}
		if len(vis) == 0 {
			continue
		}
		edges := map[[2]int]bool{}
		for _, fi := range vis {
			f := faces[fi]
			edges[[2]int{f.a, f.b}] = true
			edges[[2]int{f.b, f.c}] = true
			edges[[2]int{f.c, f.a}] = true
		}
		for _, fi := range vis {
			faces[fi].alive = false
		}
		for e := range edges {
			if edges[[2]int{e[1], e[0]}] {
				continue // arête interne à la zone visible
			}
			nf := hullFace{a: e[0], b: e[1], c: pi, alive: true}
			nf.n = pts[nf.b].Sub(pts[nf.a]).Cross(pts[nf.c].Sub(pts[nf.a])).Norm()
			nf.d = nf.n.Dot(pts[nf.a])
			faces = append(faces, nf)
		}
	}
	var out []hullFace
	for _, f := range faces {
		if f.alive {
			out = append(out, f)
		}
	}
	return out, len(out) >= 4
}

// hullFaceCluster est un groupe de faces de l'enveloppe quasi coplanaires.
type hullFaceCluster struct {
	normal Vec3
	area   float64
}

// principalHullFaces retourne les K plus grandes faces de l'enveloppe, regroupées par normale (cos > cosTol).
func principalHullFaces(pts []Vec3, k int, cosTol float64) []hullFaceCluster {
	if len(pts) == 0 {
		return nil
	}
	m := &Mesh{V: pts}
	diag := m.Bounds().Size.Len()
	faces, ok := convexHull(pts, math.Max(1e-12, 1e-9*diag))
	if !ok {
		return nil
	}
	var cl []hullFaceCluster
	for _, f := range faces {
		a := pts[f.b].Sub(pts[f.a]).Cross(pts[f.c].Sub(pts[f.a])).Len() / 2
		placed := false
		for i := range cl {
			if cl[i].normal.Dot(f.n) > cosTol {
				cl[i].normal = cl[i].normal.Scale(cl[i].area).Add(f.n.Scale(a)).Norm()
				cl[i].area += a
				placed = true
				break
			}
		}
		if !placed {
			cl = append(cl, hullFaceCluster{normal: f.n, area: a})
		}
	}
	// tri décroissant par aire
	for i := 1; i < len(cl); i++ {
		for j := i; j > 0 && cl[j].area > cl[j-1].area; j-- {
			cl[j], cl[j-1] = cl[j-1], cl[j]
		}
	}
	if len(cl) > k {
		cl = cl[:k]
	}
	return cl
}
