package mesh

import "math"

// Générateurs de maillages de test et d'exemple (fermés, normales sortantes).

// Vec2 est un point 2D du profil d'extrusion.
type Vec2 struct{ X, Y float64 }

func area2(p []Vec2) float64 {
	var a float64
	for i := range p {
		j := (i + 1) % len(p)
		a += p[i].X*p[j].Y - p[j].X*p[i].Y
	}
	return a / 2
}

// triangulate découpe un polygone simple (CCW, sans trou) par oreilles.
func triangulate(p []Vec2) [][3]int {
	n := len(p)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	cross := func(a, b, c Vec2) float64 { return (b.X-a.X)*(c.Y-a.Y) - (b.Y-a.Y)*(c.X-a.X) }
	inside := func(a, b, c, q Vec2) bool {
		return cross(a, b, q) >= 0 && cross(b, c, q) >= 0 && cross(c, a, q) >= 0
	}
	var out [][3]int
	guard := 0
	for len(idx) > 3 && guard < 10000 {
		guard++
		clipped := false
		for i := range idx {
			a, b, c := idx[(i+len(idx)-1)%len(idx)], idx[i], idx[(i+1)%len(idx)]
			if cross(p[a], p[b], p[c]) <= 1e-12 {
				continue
			}
			ear := true
			for _, k := range idx {
				if k == a || k == b || k == c {
					continue
				}
				if inside(p[a], p[b], p[c], p[k]) {
					ear = false
					break
				}
			}
			if ear {
				out = append(out, [3]int{a, b, c})
				idx = append(idx[:i], idx[i+1:]...)
				clipped = true
				break
			}
		}
		if !clipped {
			break
		}
	}
	if len(idx) == 3 {
		out = append(out, [3]int{idx[0], idx[1], idx[2]})
	}
	return out
}

// Extrude extrude un polygone simple (CCW ou CW) selon Z de 0 à h.
func Extrude(poly []Vec2, h float64) *Mesh {
	p := append([]Vec2(nil), poly...)
	if area2(p) < 0 {
		for i, j := 0, len(p)-1; i < j; i, j = i+1, j-1 {
			p[i], p[j] = p[j], p[i]
		}
	}
	n := len(p)
	m := &Mesh{}
	for _, q := range p {
		m.V = append(m.V, Vec3{q.X, q.Y, 0})
	}
	for _, q := range p {
		m.V = append(m.V, Vec3{q.X, q.Y, h})
	}
	for _, t := range triangulate(p) {
		m.T = append(m.T, Tri{t[0] + n, t[1] + n, t[2] + n}) // dessus, normale +Z
		m.T = append(m.T, Tri{t[0], t[2], t[1]})             // dessous, normale -Z
	}
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		m.T = append(m.T, Tri{i, j, j + n}, Tri{i, j + n, i + n})
	}
	return m
}

// Box retourne un parallélépipède [x0,x1]×[y0,y1]×[z0,z1] (12 triangles, 8 sommets).
func Box(x0, y0, z0, x1, y1, z1 float64) *Mesh {
	return &Mesh{
		V: []Vec3{
			{x0, y0, z0}, {x1, y0, z0}, {x1, y1, z0}, {x0, y1, z0},
			{x0, y0, z1}, {x1, y0, z1}, {x1, y1, z1}, {x0, y1, z1},
		},
		T: []Tri{
			{0, 2, 1}, {0, 3, 2}, // -Z
			{4, 5, 6}, {4, 6, 7}, // +Z
			{0, 1, 5}, {0, 5, 4}, // -Y
			{1, 2, 6}, {1, 6, 5}, // +X
			{2, 3, 7}, {2, 7, 6}, // +Y
			{3, 0, 4}, {3, 4, 7}, // -X
		},
	}
}

// Cube retourne un cube d'arête s posé sur Z=0 à l'origine.
func Cube(s float64) *Mesh { return Box(0, 0, 0, s, s, s) }

// Cylinder retourne un cylindre facetté (n pans) de rayon r et hauteur h, axe Z, base à Z=0.
func Cylinder(r, h float64, n int) *Mesh {
	poly := make([]Vec2, n)
	for i := range poly {
		a := 2 * math.Pi * float64(i) / float64(n)
		poly[i] = Vec2{r * math.Cos(a), r * math.Sin(a)}
	}
	return Extrude(poly, h)
}

// BoxWithSquareHole retourne un bloc [0,s]³ percé d'un trou carré traversant (axe Z) de côté hole, centré.
func BoxWithSquareHole(s, hole float64) *Mesh {
	a, b := (s-hole)/2, (s+hole)/2
	m := &Mesh{}
	// 0..3 : carré extérieur bas ; 4..7 : trou bas ; 8..11 : extérieur haut ; 12..15 : trou haut
	outer := []Vec2{{0, 0}, {s, 0}, {s, s}, {0, s}}
	inner := []Vec2{{a, a}, {b, a}, {b, b}, {a, b}}
	for _, z := range []float64{0, s} {
		for _, q := range outer {
			m.V = append(m.V, Vec3{q.X, q.Y, z})
		}
		for _, q := range inner {
			m.V = append(m.V, Vec3{q.X, q.Y, z})
		}
	}
	lo, li, ho, hi := 0, 4, 8, 12
	for i := 0; i < 4; i++ {
		j := (i + 1) % 4
		// Face haute (normale +Z) : couronne entre extérieur et trou, CCW vue de dessus.
		m.T = append(m.T, Tri{ho + i, ho + j, hi + j}, Tri{ho + i, hi + j, hi + i})
		// Face basse (normale -Z).
		m.T = append(m.T, Tri{lo + i, li + i, li + j}, Tri{lo + i, li + j, lo + j})
		// Paroi extérieure (normale sortante).
		m.T = append(m.T, Tri{lo + i, lo + j, ho + j}, Tri{lo + i, ho + j, ho + i})
		// Paroi du trou (normale vers l'axe du trou).
		m.T = append(m.T, Tri{li + i, hi + i, hi + j}, Tri{li + i, hi + j, li + j})
	}
	return m
}

// TwoCubes retourne deux cubes d'arête s disjoints, séparés de gap sur X.
func TwoCubes(s, gap float64) *Mesh {
	m := Cube(s)
	m.Append(Box(s+gap, 0, 0, 2*s+gap, s, s))
	return m
}

// RotateX90 retourne une rotation de +90° autour de X : (x,y,z) → (x,−z,y).
var RotateX90 = Matrix{1, 0, 0, 0, 0, 1, 0, -1, 0, 0, 0, 0}

// TShape retourne un "T" renversé debout : fût carré stem×stem de hauteur stemH, surmonté d'une
// barre de largeur bar (X) et d'épaisseur barT (Z), profondeur depth (Y). Le fût est centré sous la barre.
// La barre crée des surplombs horizontaux de chaque côté du fût.
func TShape(stem, stemH, bar, barT, depth float64) *Mesh {
	x0 := (bar - stem) / 2
	prof := []Vec2{
		{x0, 0}, {x0 + stem, 0}, {x0 + stem, stemH}, {bar, stemH}, {bar, stemH + barT}, {0, stemH + barT}, {0, stemH}, {x0, stemH},
	}
	return Extrude(prof, depth).Transform(RotateX90).Transform(Translation(0, depth, 0))
}

// UVSphere génère une sphère fermée et étanche (sommets partagés) d'environ 2·rings·segs triangles.
// Sert aux bancs d'essai de volumétrie et aux tests.
func UVSphere(r float64, rings, segs int) *Mesh {
	if rings < 2 {
		rings = 2
	}
	if segs < 3 {
		segs = 3
	}
	m := &Mesh{V: make([]Vec3, 0, 2+(rings-1)*segs), T: make([]Tri, 0, 2*segs*(rings-1))}
	m.V = append(m.V, Vec3{0, 0, r}) // pôle nord = 0
	for i := 1; i < rings; i++ {
		phi := math.Pi * float64(i) / float64(rings)
		for j := 0; j < segs; j++ {
			th := 2 * math.Pi * float64(j) / float64(segs)
			m.V = append(m.V, Vec3{r * math.Sin(phi) * math.Cos(th), r * math.Sin(phi) * math.Sin(th), r * math.Cos(phi)})
		}
	}
	south := len(m.V)
	m.V = append(m.V, Vec3{0, 0, -r})
	ring := func(i, j int) int { return 1 + (i-1)*segs + (j % segs) }
	for j := 0; j < segs; j++ {
		m.T = append(m.T, Tri{0, ring(1, j), ring(1, j+1)})
	}
	for i := 1; i < rings-1; i++ {
		for j := 0; j < segs; j++ {
			a, b, c, d := ring(i, j), ring(i+1, j), ring(i+1, j+1), ring(i, j+1)
			m.T = append(m.T, Tri{a, b, c}, Tri{a, c, d})
		}
	}
	for j := 0; j < segs; j++ {
		m.T = append(m.T, Tri{ring(rings-1, j+1), ring(rings-1, j), south})
	}
	return m
}
