package fem

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
)

// BC fixe un degré de liberté (0 = x, 1 = y, 2 = z) d'un nœud (index de grille complète) à zéro.
type BC struct{ Node, Dof int }

// Load est une force nodale (N).
type Load struct {
	Node int
	F    [3]float64
}

// Problem est un problème d'élasticité linéaire.
type Problem struct {
	Grid     *Grid
	E, Nu    float64 // MPa, sans unité
	Fixed    []BC
	Loads    []Load
	Tol      float64 // résidu relatif visé (défaut 1e-7)
	MaxIter  int     // défaut 20000
	Progress func(iter int, rel float64)
	Ctx      context.Context
}

// Solution : déplacements et contraintes aux nœuds actifs.
type Solution struct {
	Grid       *Grid
	NodeOf     []int32 // nœud de grille complète -> nœud compact (-1 : inactif)
	Nodes      []int32 // nœud compact -> nœud de grille complète
	U          []float64
	Stress     []float64 // 6 composantes par nœud compact (xx yy zz xy yz zx), MPa, moyennées entre éléments voisins
	Reaction   []float64 // 3 par nœud compact : réaction aux appuis (N), nulle ailleurs
	Iterations int
	Residual   float64
	Converged  bool
	Dropped    int // éléments flottants (sans appui) écartés
	Elements   int
}

// Compact renvoie l'index compact d'un nœud de grille complète (-1 si hors pièce).
func (s *Solution) Compact(full int) int {
	if full < 0 || full >= len(s.NodeOf) {
		return -1
	}
	return int(s.NodeOf[full])
}

type model struct {
	g      *Grid
	elems  []int32    // cubes retenus
	conn   [][8]int32 // nœuds compacts
	colors [8][]int32 // indices dans elems par couleur
	nodes  []int32
	nodeOf []int32
}

func buildModel(g *Grid, keep func(cell int) bool) *model {
	m := &model{g: g, nodeOf: make([]int32, g.NumNodes())}
	for i := range m.nodeOf {
		m.nodeOf[i] = -1
	}
	for iz := 0; iz < g.NZ; iz++ {
		for iy := 0; iy < g.NY; iy++ {
			for ix := 0; ix < g.NX; ix++ {
				c := g.Cell(ix, iy, iz)
				if !g.Solid[c] || (keep != nil && !keep(c)) {
					continue
				}
				var cn [8]int32
				for n := 0; n < 8; n++ {
					full := g.NodeIndex(ix+n&1, iy+(n>>1)&1, iz+(n>>2)&1)
					if m.nodeOf[full] < 0 {
						m.nodeOf[full] = int32(len(m.nodes))
						m.nodes = append(m.nodes, int32(full))
					}
					cn[n] = m.nodeOf[full]
				}
				col := (ix & 1) | (iy&1)<<1 | (iz&1)<<2
				m.colors[col] = append(m.colors[col], int32(len(m.elems)))
				m.elems = append(m.elems, int32(c))
				m.conn = append(m.conn, cn)
			}
		}
	}
	return m
}

func workers() int {
	n := runtime.GOMAXPROCS(0)
	if n > 8 { // mesuré : au-delà de 8 fils, le calcul ralentit (cœurs hybrides, bande passante mémoire)
		n = 8
	}
	if n < 1 {
		n = 1
	}
	return n
}

// forEachColor exécute f sur tous les éléments, couleur par couleur ; deux éléments de même couleur ne partagent
// aucun nœud : les écritures nodales ne se marchent pas dessus.
func (m *model) forEachColor(f func(e int)) {
	nw := workers()
	for c := 0; c < 8; c++ {
		list := m.colors[c]
		if len(list) == 0 {
			continue
		}
		if len(list) < 2000 || nw == 1 {
			for _, e := range list {
				f(int(e))
			}
			continue
		}
		// distribution dynamique par petits blocs : les cœurs d'un processeur hybride (P et E) n'ont pas la même vitesse
		const block = 256
		var next int64
		var wg sync.WaitGroup
		for w := 0; w < nw; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					lo := int(atomic.AddInt64(&next, block) - block)
					if lo >= len(list) {
						return
					}
					hi := lo + block
					if hi > len(list) {
						hi = len(list)
					}
					for _, e := range list[lo:hi] {
						f(int(e))
					}
				}
			}()
		}
		wg.Wait()
	}
}

// parFor découpe [0,n) en tranches traitées en parallèle (série si petit).
func parFor(n int, f func(lo, hi int)) {
	nw := workers()
	if n < 60000 || nw == 1 {
		f(0, n)
		return
	}
	var wg sync.WaitGroup
	chunk := (n + nw - 1) / nw
	for lo := 0; lo < n; lo += chunk {
		hi := lo + chunk
		if hi > n {
			hi = n
		}
		wg.Add(1)
		go func(lo, hi int) { defer wg.Done(); f(lo, hi) }(lo, hi)
	}
	wg.Wait()
}

// parReduce2 : comme parFor, avec deux sommes partielles.
func parReduce2(n int, f func(lo, hi int) (float64, float64)) (float64, float64) {
	nw := workers()
	if n < 60000 || nw == 1 {
		return f(0, n)
	}
	chunk := (n + nw - 1) / nw
	parts := make([][2]float64, nw)
	var wg sync.WaitGroup
	for w := 0; w < nw; w++ {
		lo, hi := w*chunk, (w+1)*chunk
		if lo >= n {
			break
		}
		if hi > n {
			hi = n
		}
		wg.Add(1)
		go func(w, lo, hi int) { defer wg.Done(); parts[w][0], parts[w][1] = f(lo, hi) }(w, lo, hi)
	}
	wg.Wait()
	var a, b float64
	for _, p := range parts {
		a += p[0]
		b += p[1]
	}
	return a, b
}

func (m *model) matvec(em *ElementMatrices, scale float64, x, y []float64) {
	parFor(len(y), func(lo, hi int) {
		for i := lo; i < hi; i++ {
			y[i] = 0
		}
	})
	k0 := &em.K0
	m.forEachColor(func(e int) {
		cn := &m.conn[e]
		var ue, fe [24]float64
		for n := 0; n < 8; n++ {
			b := int(cn[n]) * 3
			ue[n*3], ue[n*3+1], ue[n*3+2] = x[b], x[b+1], x[b+2]
		}
		for i := 0; i < 24; i++ {
			row := (*[24]float64)(k0[i*24 : i*24+24])
			var s0, s1, s2, s3 float64 // quatre accumulateurs : la chaîne de dépendances de l'addition limitait la vitesse
			for j := 0; j < 24; j += 4 {
				s0 += row[j] * ue[j]
				s1 += row[j+1] * ue[j+1]
				s2 += row[j+2] * ue[j+2]
				s3 += row[j+3] * ue[j+3]
			}
			fe[i] = (s0 + s1 + s2 + s3) * scale
		}
		for n := 0; n < 8; n++ {
			b := int(cn[n]) * 3
			y[b] += fe[n*3]
			y[b+1] += fe[n*3+1]
			y[b+2] += fe[n*3+2]
		}
	})
}

type dsu []int32

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

// Solve résout le problème. Les éléments non reliés à un appui sont écartés (Dropped).
func Solve(p Problem) (*Solution, error) {
	g := p.Grid
	if g == nil || p.E <= 0 || p.Nu <= -1 || p.Nu >= 0.5 {
		return nil, errors.New("fem : problème invalide (grille, E > 0, 0 ≤ ν < 0,5)")
	}
	if len(p.Fixed) == 0 {
		return nil, errors.New("fem : aucun appui")
	}
	if len(p.Loads) == 0 {
		return nil, errors.New("fem : aucune charge")
	}
	if p.Tol <= 0 {
		p.Tol = 1e-7
	}
	if p.MaxIter <= 0 {
		p.MaxIter = 20000
	}
	ctx := p.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	m := buildModel(g, nil)
	if len(m.elems) == 0 {
		return nil, errors.New("fem : aucun élément plein")
	}
	// composantes connexes (par nœuds partagés) ; seules celles qui portent un appui sont conservées
	ds := make(dsu, len(m.nodes))
	for i := range ds {
		ds[i] = int32(i)
	}
	for _, cn := range m.conn {
		for n := 1; n < 8; n++ {
			ds.union(cn[0], cn[n])
		}
	}
	supported := map[int32]bool{}
	for _, b := range p.Fixed {
		if b.Node < 0 || b.Node >= len(m.nodeOf) || m.nodeOf[b.Node] < 0 {
			return nil, fmt.Errorf("fem : appui hors de la pièce (nœud %d)", b.Node)
		}
		supported[ds.find(m.nodeOf[b.Node])] = true
	}
	dropped := 0
	elemOK := make(map[int32]bool, len(m.elems))
	for i, c := range m.elems {
		if supported[ds.find(m.conn[i][0])] {
			elemOK[c] = true
		} else {
			dropped++
		}
	}
	if dropped > 0 {
		m = buildModel(g, func(c int) bool { return elemOK[int32(c)] })
	}
	nn := len(m.nodes)
	nd := 3 * nn
	for _, l := range p.Loads {
		if l.Node < 0 || l.Node >= len(m.nodeOf) || m.nodeOf[l.Node] < 0 {
			return nil, errors.New("fem : charge appliquée hors de la partie retenue (pièce flottante ou hors matière)")
		}
	}
	f := make([]float64, nd)
	for _, l := range p.Loads {
		b := int(m.nodeOf[l.Node]) * 3
		f[b] += l.F[0]
		f[b+1] += l.F[1]
		f[b+2] += l.F[2]
	}
	fixed := make([]bool, nd)
	for _, b := range p.Fixed {
		fixed[int(m.nodeOf[b.Node])*3+b.Dof] = true
	}
	em := NewElementMatrices(p.Nu)
	scale := p.E * g.H
	// préconditionneur de Jacobi
	diag := make([]float64, nd)
	for _, cn := range m.conn {
		for n := 0; n < 8; n++ {
			b := int(cn[n]) * 3
			for d := 0; d < 3; d++ {
				diag[b+d] += em.K0[(n*3+d)*24+n*3+d] * scale
			}
		}
	}
	inv := make([]float64, nd)
	for i := range inv {
		if !fixed[i] && diag[i] > 0 {
			inv[i] = 1 / diag[i]
		}
	}
	x := make([]float64, nd)
	r := make([]float64, nd)
	z := make([]float64, nd)
	pv := make([]float64, nd)
	q := make([]float64, nd)
	for i := range f {
		if !fixed[i] {
			r[i] = f[i]
		}
	}
	fn := math.Sqrt(dot(r, r))
	if fn == 0 {
		return nil, errors.New("fem : charge nulle sur les degrés de liberté libres (charges entièrement sur les appuis ?)")
	}
	for i := range z {
		z[i] = r[i] * inv[i]
	}
	copy(pv, z)
	rz := dot(r, z)
	sol := &Solution{Grid: g, NodeOf: m.nodeOf, Nodes: m.nodes, U: x, Dropped: dropped, Elements: len(m.elems)}
	rel := 1.0
	it := 0
	free := make([]float64, nd) // 1 pour un degré de liberté libre, 0 pour un bloqué
	for i := range free {
		if !fixed[i] {
			free[i] = 1
		}
	}
	for it = 0; it < p.MaxIter; it++ {
		m.matvec(em, scale, pv, q)
		pq, _ := parReduce2(nd, func(lo, hi int) (float64, float64) {
			s := 0.0
			for i := lo; i < hi; i++ {
				s += pv[i] * q[i] * free[i]
			}
			return s, 0
		})
		if pq <= 0 {
			return nil, errors.New("fem : matrice non définie positive (pièce sous-contrainte ?)")
		}
		alpha := rz / pq
		rr, rzNew := parReduce2(nd, func(lo, hi int) (float64, float64) {
			var a, b float64
			for i := lo; i < hi; i++ {
				x[i] += alpha * pv[i]
				r[i] -= alpha * q[i] * free[i]
				zi := r[i] * inv[i]
				z[i] = zi
				a += r[i] * r[i]
				b += r[i] * zi
			}
			return a, b
		})
		rel = math.Sqrt(rr) / fn
		if p.Progress != nil && it%25 == 0 {
			p.Progress(it, rel)
		}
		if rel < p.Tol {
			it++
			break
		}
		if it%25 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		beta := rzNew / rz
		rz = rzNew
		parFor(nd, func(lo, hi int) {
			for i := lo; i < hi; i++ {
				pv[i] = z[i] + beta*pv[i]
			}
		})
	}
	sol.Iterations, sol.Residual, sol.Converged = it, rel, rel < p.Tol
	// réactions aux appuis : K u - f aux degrés de liberté bloqués
	m.matvec(em, scale, x, q)
	sol.Reaction = make([]float64, nd)
	for i := range q {
		if fixed[i] {
			sol.Reaction[i] = q[i] - f[i]
		}
	}
	// contraintes aux nœuds de chaque élément, moyennées entre éléments voisins
	sum := make([]float64, 6*nn)
	cnt := make([]float64, nn)
	m.forEachColor(func(e int) {
		cn := &m.conn[e]
		var ue [24]float64
		for n := 0; n < 8; n++ {
			b := int(cn[n]) * 3
			ue[n*3], ue[n*3+1], ue[n*3+2] = x[b], x[b+1], x[b+2]
		}
		for n := 0; n < 8; n++ {
			var eps [6]float64
			for i := 0; i < 6; i++ {
				s := 0.0
				for j := 0; j < 24; j++ {
					s += em.B[n][i*24+j] * ue[j]
				}
				eps[i] = s / g.H
			}
			base := int(cn[n]) * 6
			for i := 0; i < 6; i++ {
				s := 0.0
				for k := 0; k < 6; k++ {
					s += em.D[i*6+k] * eps[k]
				}
				sum[base+i] += s * p.E
			}
			cnt[cn[n]]++
		}
	})
	for n := 0; n < nn; n++ {
		if cnt[n] > 0 {
			for i := 0; i < 6; i++ {
				sum[n*6+i] /= cnt[n]
			}
		}
	}
	sol.Stress = sum
	return sol, nil
}

func dot(a, b []float64) float64 {
	s := 0.0
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

// VonMises : contrainte équivalente de von Mises d'un tenseur (xx yy zz xy yz zx).
func VonMises(s []float64) float64 {
	a := s[0] - s[1]
	b := s[1] - s[2]
	c := s[2] - s[0]
	return math.Sqrt(0.5*(a*a+b*b+c*c) + 3*(s[3]*s[3]+s[4]*s[4]+s[5]*s[5]))
}
