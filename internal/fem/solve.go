package fem

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
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
	// Deadline : borne de temps (zéro = aucune). À l'échéance le calcul s'arrête SANS erreur : la solution rendue
	// porte Converged=false et TimedOut=true, elle ne doit pas être présentée comme fiable.
	Deadline time.Time
	// Hold : nœuds (grille complète) comptés comme appuis pour la connexité sans être bloqués (contact unilatéral :
	// appui relâché dont la pièce ne doit pas devenir « flottante »).
	Hold []int
	// Warm : solution précédente (même grille) dont les déplacements servent de point de départ.
	Warm *Solution
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
	TimedOut   bool   // arrêt à l'échéance de Problem.Deadline
	Stagnated  bool   // arrêt car le résidu n'avançait plus
	Precond    string // préconditionneur utilisé
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
	if len(p.Fixed) == 0 && len(p.Hold) == 0 {
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
	for _, n := range p.Hold {
		if n >= 0 && n < len(m.nodeOf) && m.nodeOf[n] >= 0 {
			supported[ds.find(m.nodeOf[n])] = true
		}
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
	// préconditionneur de Jacobi par blocs 3×3 (les trois degrés de liberté d'un nœud sont inversés ensemble : le
	// couplage entre directions, fort avec ν proche de 0,4, est pris en compte) ; degrés bloqués : identité
	blk := make([]float64, nn*9)
	for _, cn := range m.conn {
		for n := 0; n < 8; n++ {
			b := int(cn[n]) * 9
			for d1 := 0; d1 < 3; d1++ {
				for d2 := 0; d2 < 3; d2++ {
					blk[b+d1*3+d2] += em.K0[(n*3+d1)*24+n*3+d2] * scale
				}
			}
		}
	}
	for i := 0; i < nn; i++ {
		B := blk[i*9 : i*9+9]
		for d := 0; d < 3; d++ {
			if fixed[i*3+d] {
				for k := 0; k < 3; k++ {
					B[d*3+k], B[k*3+d] = 0, 0
				}
				B[d*3+d] = 1
			}
		}
		if !invert3(B) { // bloc dégénéré : repli sur la diagonale
			for k := range B {
				B[k] = 0
			}
			for d := 0; d < 3; d++ {
				B[d*3+d] = 1
			}
		}
	}
	applyPrec := func(r, z []float64, lo, hi int) {
		for i := lo; i < hi; i++ {
			B := blk[i*9 : i*9+9]
			r0, r1, r2 := r[i*3], r[i*3+1], r[i*3+2]
			z[i*3] = B[0]*r0 + B[1]*r1 + B[2]*r2
			z[i*3+1] = B[3]*r0 + B[4]*r1 + B[5]*r2
			z[i*3+2] = B[6]*r0 + B[7]*r1 + B[8]*r2
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
	free := make([]float64, nd) // 1 pour un degré de liberté libre, 0 pour un bloqué
	for i := range free {
		if !fixed[i] {
			free[i] = 1
		}
	}
	sol := &Solution{Grid: g, NodeOf: m.nodeOf, Nodes: m.nodes, U: x, Dropped: dropped, Elements: len(m.elems), Precond: "Jacobi par blocs 3×3"}
	if w := p.Warm; w != nil && w.Grid == g {
		for c, full := range m.nodes {
			if oc := w.Compact(int(full)); oc >= 0 {
				for d := 0; d < 3; d++ {
					if !fixed[c*3+d] {
						x[c*3+d] = w.U[oc*3+d]
					}
				}
			}
		}
		m.matvec(em, scale, x, q)
		for i := range r {
			if !fixed[i] {
				r[i] = f[i] - q[i]
			}
		}
	}
	parFor(nn, func(lo, hi int) { applyPrec(r, z, lo, hi) })
	copy(pv, z)
	rz := dot(r, z)
	rel := math.Sqrt(dot(r, r)) / fn
	it := 0
	var hist [4]float64 // résidu relatif tous les 500 itérations : détection de stagnation
	for it = 0; it < p.MaxIter && rel >= p.Tol; it++ {
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
		rr, _ := parReduce2(nd, func(lo, hi int) (float64, float64) {
			var a float64
			for i := lo; i < hi; i++ {
				x[i] += alpha * pv[i]
				r[i] -= alpha * q[i] * free[i]
				a += r[i] * r[i]
			}
			return a, 0
		})
		_, rzNew := parReduce2(nn, func(lo, hi int) (float64, float64) {
			applyPrec(r, z, lo, hi)
			var b float64
			for i := lo * 3; i < hi*3; i++ {
				b += r[i] * z[i]
			}
			return 0, b
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
			if !p.Deadline.IsZero() && time.Now().After(p.Deadline) {
				sol.TimedOut = true
				it++
				break
			}
		}
		if it > 0 && it%500 == 0 {
			k := (it / 500) % 4
			if it >= 2000 && hist[k] > 0 && rel > 0.8*hist[k] { // moins de 20 % de gain en 2000 itérations
				sol.Stagnated = true
				it++
				break
			}
			hist[k] = rel
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

// invert3 inverse en place une matrice 3×3 (ligne par ligne) ; faux si singulière.
func invert3(m []float64) bool {
	a, b, c, d, e, f, g, h, i := m[0], m[1], m[2], m[3], m[4], m[5], m[6], m[7], m[8]
	det := a*(e*i-f*h) - b*(d*i-f*g) + c*(d*h-e*g)
	if math.Abs(det) < 1e-300 || math.IsNaN(det) {
		return false
	}
	id := 1 / det
	m[0], m[1], m[2] = (e*i-f*h)*id, (c*h-b*i)*id, (b*f-c*e)*id
	m[3], m[4], m[5] = (f*g-d*i)*id, (a*i-c*g)*id, (c*d-a*f)*id
	m[6], m[7], m[8] = (d*h-e*g)*id, (b*g-a*h)*id, (a*e-b*d)*id
	return true
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
