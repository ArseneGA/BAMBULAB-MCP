package mesh

import "math"

// Weld soude les sommets distants de moins de tol. Retourne le tableau de correspondance
// sommet → représentant renuméroté et le nombre de sommets soudés.
func Weld(m *Mesh, tol float64) (remap []int, n int) {
	r32, n := weld32(m.V, tol)
	remap = make([]int, len(r32))
	for i, v := range r32 {
		remap[i] = int(v)
	}
	return remap, n
}

// weld32 : soudure en structures plates (table de hachage à adressage ouvert sur des cellules de 2·tol,
// chaînage par tableau d'int32) : pas de map ni de slice par cellule, mémoire proportionnelle au nombre de sommets.
// Un sommet est rattaché au premier représentant à distance ≤ tol, comme avant.
func weld32(vs []Vec3, tol float64) (remap []int32, n int) {
	remap = make([]int32, len(vs))
	if len(vs) == 0 {
		return remap, 0
	}
	if tol <= 0 {
		tol = 1e-9
	}
	cs := 2 * tol
	inv := 1 / cs
	size := 1
	for size < 2*len(vs) {
		size <<= 1
	}
	mask := uint64(size - 1)
	head := make([]int32, size)
	for i := range head {
		head[i] = -1
	}
	reps := make([]Vec3, 0, len(vs)/2+8)
	next := make([]int32, 0, len(vs)/2+8)
	hash := func(x, y, z int64) uint64 {
		h := uint64(x)*0x9E3779B97F4A7C15 ^ uint64(y)*0xC2B2AE3D27D4EB4F ^ uint64(z)*0x165667B19E3779F9
		h ^= h >> 29
		h *= 0xBF58476D1CE4E5B9
		h ^= h >> 32
		return h & mask
	}
	t2 := tol * tol
	for i, v := range vs {
		// cellules couvertes par la boule de rayon tol : au plus 2 par axe
		fx0, fx1 := math.Floor((v.X-tol)*inv), math.Floor((v.X+tol)*inv)
		fy0, fy1 := math.Floor((v.Y-tol)*inv), math.Floor((v.Y+tol)*inv)
		fz0, fz1 := math.Floor((v.Z-tol)*inv), math.Floor((v.Z+tol)*inv)
		found := int32(-1)
	search:
		for x := int64(fx0); x <= int64(fx1); x++ {
			for y := int64(fy0); y <= int64(fy1); y++ {
				for z := int64(fz0); z <= int64(fz1); z++ {
					for r := head[hash(x, y, z)]; r >= 0; r = next[r] {
						d := reps[r].Sub(v)
						if d.Dot(d) <= t2 {
							found = r
							break search
						}
					}
				}
			}
		}
		if found < 0 {
			found = int32(len(reps))
			reps = append(reps, v)
			h := hash(int64(math.Floor(v.X*inv)), int64(math.Floor(v.Y*inv)), int64(math.Floor(v.Z*inv)))
			next = append(next, head[h])
			head[h] = found
		}
		remap[i] = found
	}
	return remap, len(reps)
}

// dsu : union-find sur des int32 (compression de chemin).
type dsu []int32

func newDSU(n int) dsu {
	d := make(dsu, n)
	for i := range d {
		d[i] = int32(i)
	}
	return d
}

func (d dsu) find(x int32) int32 {
	for d[x] != x {
		d[x] = d[d[x]]
		x = d[x]
	}
	return x
}

func (d dsu) union(a, b int32) {
	ra, rb := d.find(a), d.find(b)
	if ra != rb {
		d[ra] = rb
	}
}
