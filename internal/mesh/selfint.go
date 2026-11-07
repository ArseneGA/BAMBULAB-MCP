package mesh

import (
	"fmt"
	"math"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// SelfIntersectionRegion regroupe des triangles qui se traversent et sont connexes entre eux.
type SelfIntersectionRegion struct {
	Pairs     int  `json:"pairs"`
	Triangles int  `json:"triangles"`
	BBox      BBox `json:"bbox"`
}

// SelfIntersectionReport : paires de triangles qui se traversent (les voisins qui partagent une arête, et les
// contacts sur une arête ou un sommet, sont ignorés). Contrôle borné en temps : voir Complete.
type SelfIntersectionReport struct {
	Pairs       int                      `json:"pairs"`
	Triangles   int                      `json:"triangles"` // triangles distincts concernés (parmi les paires retenues)
	Regions     []SelfIntersectionRegion `json:"regions,omitempty"`
	RegionCount int                      `json:"region_count"`
	Checked     int                      `json:"triangles_checked"`
	Total       int                      `json:"triangles_total"`
	Complete    bool                     `json:"complete"`
	Note        string                   `json:"note,omitempty"`
}

// SelfIntersectionOptions borne le contrôle.
type SelfIntersectionOptions struct {
	// Budget : durée maximale (défaut 5 s). Au-delà, le rapport est partiel (Complete = false).
	Budget time.Duration
	// WeldTol : tolérance de soudure (défaut : celle d'Analyze).
	WeldTol float64
}

const (
	maxStoredPairs = 200_000
	maxSIRegions   = 10
)

// FindSelfIntersections détecte les auto-intersections d'un maillage (BVH + tests triangle-triangle).
func FindSelfIntersections(m *Mesh, opt SelfIntersectionOptions) *SelfIntersectionReport {
	if len(m.T) == 0 {
		return &SelfIntersectionReport{Complete: true}
	}
	tol := opt.WeldTol
	if tol <= 0 {
		tol = math.Max(1e-6, 1e-6*m.Bounds().Size.Len())
	}
	remap, _ := weld32(m.V, tol)
	b := buildBVH(m)
	return selfIntersections(b, nil, remap, tol, opt.Budget)
}

func selfIntersections(b *bvh, valid []bool, remap []int32, tol float64, budget time.Duration) *SelfIntersectionReport {
	if budget <= 0 {
		budget = 5 * time.Second
	}
	deadline := time.Now().Add(budget)
	m := b.m
	order := b.order
	rep := &SelfIntersectionReport{Total: len(order)}
	workers := runtime.GOMAXPROCS(0)
	if workers > 8 {
		workers = 8
	}
	const chunk = 256
	var nextChunk, done, pairCount atomic.Int64
	var mu sync.Mutex
	var pairs [][2]int32
	var wg sync.WaitGroup
	timedOut := atomic.Bool{}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var local [][2]int32
			for {
				c := int(nextChunk.Add(1)-1) * chunk
				if c >= len(order) {
					break
				}
				if time.Now().After(deadline) {
					timedOut.Store(true)
					break
				}
				end := min(c+chunk, len(order))
				for _, ti := range order[c:end] {
					if valid != nil && !valid[ti] {
						continue
					}
					lo, hi := b.triBounds(ti)
					lo = lo.Sub(Vec3{tol, tol, tol})
					hi = hi.Add(Vec3{tol, tol, tol})
					b.overlapping(lo, hi, func(tj int32) bool {
						if tj <= ti || (valid != nil && !valid[tj]) {
							return true
						}
						if trianglesCross(m, remap, int(ti), int(tj), tol) {
							pairCount.Add(1)
							local = append(local, [2]int32{ti, tj})
						}
						return true
					})
				}
				done.Add(int64(end - c))
			}
			mu.Lock()
			pairs = append(pairs, local...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	rep.Checked = int(done.Load())
	rep.Complete = !timedOut.Load() && rep.Checked >= len(order)
	rep.Pairs = int(pairCount.Load())
	if len(pairs) > maxStoredPairs {
		pairs = pairs[:maxStoredPairs]
	}
	rep.regions(b, pairs)
	if !rep.Complete {
		rep.Note = fmt.Sprintf("contrôle partiel : %d triangles sur %d confrontés avant la limite de temps (%s) ; le compte est un minimum", rep.Checked, rep.Total, budget)
	}
	return rep
}

func (rep *SelfIntersectionReport) regions(b *bvh, pairs [][2]int32) {
	if len(pairs) == 0 {
		return
	}
	idx := map[int32]int32{}
	var tris []int32
	id := func(t int32) int32 {
		if k, ok := idx[t]; ok {
			return k
		}
		k := int32(len(tris))
		idx[t] = k
		tris = append(tris, t)
		return k
	}
	for _, p := range pairs {
		id(p[0])
		id(p[1])
	}
	ds := newDSU(len(tris))
	for _, p := range pairs {
		ds.union(idx[p[0]], idx[p[1]])
	}
	rep.Triangles = len(tris)
	type acc struct {
		pairs, tris int
		lo, hi      Vec3
	}
	regs := map[int32]*acc{}
	for k, t := range tris {
		r := ds.find(int32(k))
		a := regs[r]
		if a == nil {
			a = &acc{lo: Vec3{math.Inf(1), math.Inf(1), math.Inf(1)}, hi: Vec3{math.Inf(-1), math.Inf(-1), math.Inf(-1)}}
			regs[r] = a
		}
		a.tris++
		l, h := b.triBounds(t)
		a.lo, _ = minMax(a.lo, a.hi, l)
		_, a.hi = minMax(a.lo, a.hi, h)
	}
	for _, p := range pairs {
		regs[ds.find(idx[p[0]])].pairs++
	}
	for _, a := range regs {
		rep.Regions = append(rep.Regions, SelfIntersectionRegion{Pairs: a.pairs, Triangles: a.tris, BBox: BBox{Min: a.lo, Max: a.hi, Size: a.hi.Sub(a.lo)}})
	}
	slices.SortFunc(rep.Regions, func(a, b SelfIntersectionRegion) int { return b.Pairs - a.Pairs })
	rep.RegionCount = len(rep.Regions)
	if len(rep.Regions) > maxSIRegions {
		rep.Regions = rep.Regions[:maxSIRegions]
	}
}

// trianglesCross : les triangles i et j se traversent-ils ? Les voisins partageant au moins une arête sont ignorés ;
// les contacts qui ne font que se toucher (sur une arête, un sommet, ou à moins de tol) ne comptent pas.
// Les triangles coplanaires qui se recouvrent ne sont pas détectés (limite connue).
func trianglesCross(m *Mesh, remap []int32, i, j int, tol float64) bool {
	ti, tj := m.T[i], m.T[j]
	shared := 0
	for _, a := range ti {
		for _, b := range tj {
			if remap[a] == remap[b] {
				shared++
			}
		}
	}
	if shared >= 2 {
		return false
	}
	a := [3]Vec3{m.V[ti[0]], m.V[ti[1]], m.V[ti[2]]}
	b := [3]Vec3{m.V[tj[0]], m.V[tj[1]], m.V[tj[2]]}
	na := a[1].Sub(a[0]).Cross(a[2].Sub(a[0])).Norm()
	nb := b[1].Sub(b[0]).Cross(b[2].Sub(b[0])).Norm()
	if na == (Vec3{}) || nb == (Vec3{}) {
		return false
	}
	for k := 0; k < 3; k++ {
		if segPiercesTri(a[k], a[(k+1)%3], b, nb, tol) || segPiercesTri(b[k], b[(k+1)%3], a, na, tol) {
			return true
		}
	}
	return false
}

// segPiercesTri : le segment p→q traverse-t-il le triangle t (bord compris, à tol près) (de normale unitaire n) ?
func segPiercesTri(p, q Vec3, t [3]Vec3, n Vec3, tol float64) bool {
	d0, d1 := p.Sub(t[0]).Dot(n), q.Sub(t[0]).Dot(n)
	if !((d0 > tol && d1 < -tol) || (d0 < -tol && d1 > tol)) {
		return false
	}
	s := d0 / (d0 - d1)
	x := p.Add(q.Sub(p).Scale(s))
	for k := 0; k < 3; k++ {
		e := t[(k+1)%3].Sub(t[k])
		l := e.Len()
		if l == 0 || e.Cross(x.Sub(t[k])).Dot(n)/l < -tol {
			return false
		}
	}
	return true
}

// fillSelfIntersections complète un Report (appelé par analyzeWelded).
func fillSelfIntersections(m *Mesh, valid []bool, remap []int32, opt AnalyzeOptions, r *Report) {
	budget := opt.SelfIntersectBudget
	if budget < 0 {
		return
	}
	if budget == 0 {
		if r.Triangles-r.DegenerateTriangles > autoSelfIntersectMax {
			return
		}
		budget = 2 * time.Second
	}
	b := opt.bvh
	if b == nil {
		b = buildBVHFiltered(m, valid)
	}
	rep := selfIntersections(b, valid, remap, r.WeldTol, budget)
	r.SelfIntersectionsChecked = true
	r.SelfIntersectionsComplete = rep.Complete
	r.SelfIntersectingPairs = rep.Pairs
	r.SelfIntersectingTriangles = rep.Triangles
	r.SelfIntersectionRegions = rep.Regions
	r.SelfIntersectionRegionCount = rep.RegionCount
	if rep.Pairs > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%d paire(s) de triangles s'interpénètrent (%d région(s)) : auto-intersections, le volume et l'épaisseur sont mal définis ; fusionner (union booléenne) ou réparer", rep.Pairs, rep.RegionCount))
	}
	if !rep.Complete {
		r.Warnings = append(r.Warnings, rep.Note)
	}
}

// autoSelfIntersectMax : au-delà, Analyze ne contrôle pas les auto-intersections sans budget explicite.
const autoSelfIntersectMax = 100_000
