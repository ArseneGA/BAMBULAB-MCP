package mesh

import (
	"fmt"
	"math"
	"slices"
)

// RepairOptions règle la réparation minimale d'un maillage.
type RepairOptions struct {
	// WeldTol : tolérance de soudure en mm (0 = comme Analyze : max(1e-6, 1e-6 × diagonale)).
	WeldTol float64
	// MaxHoleEdges : un trou (boucle de bord) est comblé s'il compte au plus ce nombre d'arêtes (défaut 32 ;
	// < 0 désactive le comblement).
	MaxHoleEdges int
	// RemoveFloating supprime les petits composants flottants : volume < FloatingFraction × la plus grosse
	// composante (fermée), ou surface < FloatingFraction/10 × la plus grande surface (ouverte).
	RemoveFloating bool
	// FloatingFraction : défaut 1e-3 (0,1 %).
	FloatingFraction float64
}

// RepairSummary est l'état d'un maillage avant ou après réparation.
type RepairSummary struct {
	Triangles           int     `json:"triangles"`
	Watertight          bool    `json:"watertight"`
	OpenEdges           int     `json:"open_edges"`
	NonManifoldEdges    int     `json:"non_manifold_edges"`
	InconsistentEdges   int     `json:"inconsistent_edges"`
	DegenerateTriangles int     `json:"degenerate_triangles"`
	DuplicateTriangles  int     `json:"duplicate_triangles"`
	FloatingFragments   int     `json:"floating_fragments"`
	Components          int     `json:"components"`
	VolumeMM3           float64 `json:"volume_mm3"`
	// Defects : total des défauts de topologie (arêtes ouvertes, non manifold, incohérentes, dégénérés, dupliqués).
	Defects int `json:"defects"`
}

// RepairActions compte ce que la réparation a fait.
type RepairActions struct {
	VerticesMerged       int `json:"vertices_merged"`
	DegenerateRemoved    int `json:"degenerate_removed"`
	DuplicatesRemoved    int `json:"duplicates_removed"`
	TrianglesFlipped     int `json:"triangles_flipped"`
	ComponentsInverted   int `json:"components_inverted"`
	OrientationConflicts int `json:"orientation_conflicts"`
	HolesFilled          int `json:"holes_filled"`
	HolesSkipped         int `json:"holes_skipped"`
	TrianglesAdded       int `json:"triangles_added"`
	FragmentsRemoved     int `json:"fragments_removed"`
	NonManifoldRemaining int `json:"non_manifold_edges_remaining"`
}

// RepairReport : rapport avant / après.
type RepairReport struct {
	Before   RepairSummary `json:"before"`
	After    RepairSummary `json:"after"`
	Actions  RepairActions `json:"actions"`
	Changed  bool          `json:"changed"`
	Warnings []string      `json:"warnings,omitempty"`
}

func summarize(r *Report) RepairSummary {
	return RepairSummary{Triangles: r.Triangles, Watertight: r.Watertight, OpenEdges: r.OpenEdges, NonManifoldEdges: r.NonManifoldEdges,
		InconsistentEdges: r.InconsistentEdges, DegenerateTriangles: r.DegenerateTriangles, DuplicateTriangles: r.DuplicateTriangles,
		FloatingFragments: r.FloatingFragments, Components: r.Components, VolumeMM3: r.Volume,
		Defects: r.OpenEdges + r.NonManifoldEdges + r.InconsistentEdges + r.DegenerateTriangles + r.DuplicateTriangles}
}

type edgeRef struct {
	key uint64 // min<<32 | max
	tri int32
	fwd bool // parcourue dans le sens min → max
}

func edgeRefs(tris []Tri32) []edgeRef {
	refs := make([]edgeRef, 0, 3*len(tris))
	for i, t := range tris {
		for k := 0; k < 3; k++ {
			a, b := uint64(t[k]), uint64(t[(k+1)%3])
			if a < b {
				refs = append(refs, edgeRef{a<<32 | b, int32(i), true})
			} else {
				refs = append(refs, edgeRef{b<<32 | a, int32(i), false})
			}
		}
	}
	slices.SortFunc(refs, func(x, y edgeRef) int {
		switch {
		case x.key < y.key:
			return -1
		case x.key > y.key:
			return 1
		}
		return int(x.tri) - int(y.tri)
	})
	return refs
}

// Tri32 : triangle d'index int32 (travail interne).
type Tri32 [3]int32

// Repair répare un maillage de façon minimale et reproductible : soudure des sommets, suppression des triangles
// dégénérés et dupliqués, orientation cohérente des normales (propagation par les arêtes), comblement des petits
// trous, et suppression facultative des petits composants flottants. L'entrée n'est jamais modifiée.
// Les arêtes non manifold, les auto-intersections et les grands trous ne sont pas réparés (signalés).
func Repair(m *Mesh, opt RepairOptions) (*Mesh, *RepairReport, error) {
	if len(m.T) == 0 {
		return nil, nil, ErrEmpty
	}
	if err := m.Validate(); err != nil {
		return nil, nil, err
	}
	if opt.MaxHoleEdges == 0 {
		opt.MaxHoleEdges = 32
	}
	if opt.FloatingFraction <= 0 {
		opt.FloatingFraction = 1e-3
	}
	rep := &RepairReport{}
	rep.Before = summarize(Analyze(m, AnalyzeOptions{SelfIntersectBudget: -1, WeldTol: opt.WeldTol}))

	bb := m.Bounds()
	diag := bb.Size.Len()
	tol := opt.WeldTol
	if tol <= 0 {
		tol = math.Max(1e-6, 1e-6*diag)
	}
	degEps := math.Max(1e-12, (1e-6*diag)*(1e-6*diag))

	// 1. soudure : sommets uniques (première occurrence), triangles remappés
	remap, nw := weld32(m.V, tol)
	verts := make([]Vec3, nw)
	seen := make([]bool, nw)
	for i, r := range remap {
		if !seen[r] {
			seen[r] = true
			verts[r] = m.V[i]
		}
	}
	rep.Actions.VerticesMerged = len(m.V) - nw

	// 2. triangles dégénérés puis doublons
	tris := make([]Tri32, 0, len(m.T))
	for _, t := range m.T {
		a, b, c := remap[t[0]], remap[t[1]], remap[t[2]]
		if a == b || b == c || a == c {
			rep.Actions.DegenerateRemoved++
			continue
		}
		area := verts[b].Sub(verts[a]).Cross(verts[c].Sub(verts[a])).Len() / 2
		if area <= degEps {
			rep.Actions.DegenerateRemoved++
			continue
		}
		tris = append(tris, Tri32{a, b, c})
	}
	{
		type keyed struct {
			k [3]int32
			i int32
		}
		ks := make([]keyed, len(tris))
		for i, t := range tris {
			s := t
			slices.Sort(s[:])
			ks[i] = keyed{s, int32(i)}
		}
		slices.SortFunc(ks, func(x, y keyed) int {
			for d := 0; d < 3; d++ {
				if x.k[d] != y.k[d] {
					return int(x.k[d]) - int(y.k[d])
				}
			}
			return int(x.i) - int(y.i)
		})
		drop := make([]bool, len(tris))
		for i := 1; i < len(ks); i++ {
			if ks[i].k == ks[i-1].k {
				drop[ks[i].i] = true
				rep.Actions.DuplicatesRemoved++
			}
		}
		if rep.Actions.DuplicatesRemoved > 0 {
			kept := tris[:0]
			for i, t := range tris {
				if !drop[i] {
					kept = append(kept, t)
				}
			}
			tris = kept
		}
	}
	if len(tris) == 0 {
		return nil, nil, fmt.Errorf("%w : plus aucun triangle valide après nettoyage", ErrEmpty)
	}

	// 3. orientation cohérente
	flipped, conflicts := orientConsistently(tris)
	rep.Actions.TrianglesFlipped, rep.Actions.OrientationConflicts = flipped, conflicts

	// 4. comblement des petits trous
	if opt.MaxHoleEdges > 0 {
		var filled, skipped, added int
		tris, filled, skipped, added = fillHoles(tris, verts, opt.MaxHoleEdges)
		rep.Actions.HolesFilled, rep.Actions.HolesSkipped, rep.Actions.TrianglesAdded = filled, skipped, added
	}

	// 5. composantes : volume négatif (normales vers l'intérieur) => inversion ; petits composants flottants
	comp, ncomp, closed := componentsOf(tris, len(verts))
	vol := make([]float64, ncomp)
	area := make([]float64, ncomp)
	origin := bb.Min.Add(bb.Size.Scale(0.5))
	for i, t := range tris {
		a, b, c := verts[t[0]].Sub(origin), verts[t[1]].Sub(origin), verts[t[2]].Sub(origin)
		vol[comp[i]] += a.Dot(b.Cross(c)) / 6
		area[comp[i]] += verts[t[1]].Sub(verts[t[0]]).Cross(verts[t[2]].Sub(verts[t[0]])).Len() / 2
	}
	for c := 0; c < ncomp; c++ {
		if closed[c] && vol[c] < 0 {
			rep.Actions.ComponentsInverted++
		}
	}
	if rep.Actions.ComponentsInverted > 0 {
		for i := range tris {
			if c := comp[i]; closed[c] && vol[c] < 0 {
				tris[i][1], tris[i][2] = tris[i][2], tris[i][1]
			}
		}
		for c := 0; c < ncomp; c++ {
			if closed[c] && vol[c] < 0 {
				vol[c] = -vol[c]
			}
		}
	}
	keepComp := make([]bool, ncomp)
	for i := range keepComp {
		keepComp[i] = true
	}
	if opt.RemoveFloating && ncomp > 1 {
		maxVol, maxArea := 0.0, 0.0
		for c := 0; c < ncomp; c++ {
			maxVol = math.Max(maxVol, math.Abs(vol[c]))
			maxArea = math.Max(maxArea, area[c])
		}
		for c := 0; c < ncomp; c++ {
			small := false
			if closed[c] {
				small = maxVol > 0 && math.Abs(vol[c]) < opt.FloatingFraction*maxVol
			} else {
				small = maxArea > 0 && area[c] < opt.FloatingFraction/10*maxArea
			}
			if small {
				keepComp[c] = false
				rep.Actions.FragmentsRemoved++
			}
		}
	}

	// 6. maillage de sortie : sommets utilisés seulement
	used := make([]int32, len(verts))
	for i := range used {
		used[i] = -1
	}
	out := &Mesh{}
	for i, t := range tris {
		if !keepComp[comp[i]] {
			continue
		}
		var nt Tri
		for k := 0; k < 3; k++ {
			if used[t[k]] < 0 {
				used[t[k]] = int32(len(out.V))
				out.V = append(out.V, verts[t[k]])
			}
			nt[k] = int(used[t[k]])
		}
		out.T = append(out.T, nt)
	}
	if len(out.T) == 0 {
		return nil, nil, fmt.Errorf("%w : la réparation supprimerait toute la géométrie", ErrEmpty)
	}
	rep.After = summarize(Analyze(out, AnalyzeOptions{SelfIntersectBudget: -1, WeldTol: opt.WeldTol}))
	rep.Actions.NonManifoldRemaining = rep.After.NonManifoldEdges
	a := rep.Actions
	rep.Changed = a.VerticesMerged+a.DegenerateRemoved+a.DuplicatesRemoved+a.TrianglesFlipped+a.ComponentsInverted+a.HolesFilled+a.FragmentsRemoved > 0
	if rep.After.NonManifoldEdges > 0 {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d arête(s) non manifold restent : à corriger dans un logiciel de modélisation", rep.After.NonManifoldEdges))
	}
	if rep.Actions.HolesSkipped > 0 {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d trou(s) non comblé(s) (plus de %d arêtes ou bord ambigu)", rep.Actions.HolesSkipped, opt.MaxHoleEdges))
	}
	if rep.Actions.OrientationConflicts > 0 {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d conflit(s) d'orientation irréductible(s) (surface non orientable ou arêtes non manifold)", rep.Actions.OrientationConflicts))
	}
	if !rep.After.Watertight {
		rep.Warnings = append(rep.Warnings, "le maillage réparé n'est toujours pas étanche")
	}
	if rep.After.Defects > rep.Before.Defects {
		rep.Warnings = append(rep.Warnings, "le nombre de défauts a augmenté : vérifier le résultat avant d'imprimer")
	}
	return out, rep, nil
}

// orientConsistently oriente les triangles par propagation à travers les arêtes manifold (2 triangles) ;
// retourne le nombre de triangles retournés et de conflits (arêtes qui restent incohérentes).
func orientConsistently(tris []Tri32) (flipped, conflicts int) {
	refs := edgeRefs(tris)
	type nb struct {
		tri  int32
		same bool // les deux triangles parcourent l'arête dans le même sens : il faut retourner l'un des deux
	}
	adj := make([][]nb, len(tris))
	for i := 0; i < len(refs); {
		j := i
		for j < len(refs) && refs[j].key == refs[i].key {
			j++
		}
		if j-i == 2 {
			a, b := refs[i], refs[i+1]
			same := a.fwd == b.fwd
			adj[a.tri] = append(adj[a.tri], nb{b.tri, same})
			adj[b.tri] = append(adj[b.tri], nb{a.tri, same})
		}
		i = j
	}
	flip := make([]bool, len(tris))
	visited := make([]bool, len(tris))
	var stack []int32
	for s := range tris {
		if visited[s] {
			continue
		}
		visited[s] = true
		stack = append(stack[:0], int32(s))
		for len(stack) > 0 {
			t := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, n := range adj[t] {
				want := flip[t] != n.same // même sens d'arête => états de retournement opposés
				if !visited[n.tri] {
					visited[n.tri] = true
					flip[n.tri] = want
					stack = append(stack, n.tri)
				} else if flip[n.tri] != want {
					conflicts++
				}
			}
		}
	}
	for i, f := range flip {
		if f {
			tris[i][1], tris[i][2] = tris[i][2], tris[i][1]
			flipped++
		}
	}
	return flipped, conflicts / 2
}

// componentsOf : composantes connexes par sommets partagés ; closed[c] vrai si la composante n'a aucune arête de bord.
func componentsOf(tris []Tri32, nv int) (comp []int32, n int, closed []bool) {
	ds := newDSU(nv)
	for _, t := range tris {
		ds.union(t[0], t[1])
		ds.union(t[1], t[2])
	}
	idx := map[int32]int32{}
	comp = make([]int32, len(tris))
	for i, t := range tris {
		r := ds.find(t[0])
		c, ok := idx[r]
		if !ok {
			c = int32(len(idx))
			idx[r] = c
		}
		comp[i] = c
	}
	n = len(idx)
	closed = make([]bool, n)
	for i := range closed {
		closed[i] = true
	}
	refs := edgeRefs(tris)
	for i := 0; i < len(refs); {
		j := i
		for j < len(refs) && refs[j].key == refs[i].key {
			j++
		}
		if j-i != 2 {
			closed[comp[refs[i].tri]] = false
		}
		i = j
	}
	return comp, n, closed
}

// fillHoles comble les boucles de bord d'au plus maxEdges arêtes (ear clipping dans le plan moyen du trou,
// éventail en dernier recours). Les nouveaux triangles prolongent l'orientation des voisins.
func fillHoles(tris []Tri32, verts []Vec3, maxEdges int) (out []Tri32, filled, skipped, added int) {
	refs := edgeRefs(tris)
	// arêtes de bord orientées (a → b dans le triangle qui les porte)
	next := map[int32][]int32{}
	for i := 0; i < len(refs); {
		j := i
		for j < len(refs) && refs[j].key == refs[i].key {
			j++
		}
		if j-i == 1 {
			r := refs[i]
			a, b := int32(r.key>>32), int32(r.key&0xffffffff)
			if !r.fwd {
				a, b = b, a
			}
			next[a] = append(next[a], b)
		}
		i = j
	}
	if len(next) == 0 {
		return tris, 0, 0, 0
	}
	out = tris
	starts := make([]int32, 0, len(next))
	for a := range next {
		starts = append(starts, a)
	}
	slices.Sort(starts)
	used := map[[2]int32]bool{}
	for _, a0 := range starts {
		for _, b0 := range next[a0] {
			if used[[2]int32{a0, b0}] {
				continue
			}
			loop := []int32{a0}
			used[[2]int32{a0, b0}] = true
			prev, cur := a0, b0
			ok := true
			for cur != a0 {
				loop = append(loop, cur)
				if len(loop) > maxEdges {
					ok = false
					break
				}
				outs := next[cur]
				if len(outs) != 1 || used[[2]int32{cur, outs[0]}] {
					ok = false // sommet de bord ambigu (plusieurs sorties) ou boucle déjà prise
					break
				}
				used[[2]int32{cur, outs[0]}] = true
				prev, cur = cur, outs[0]
			}
			_ = prev
			if !ok || len(loop) < 3 {
				// boucle abandonnée : on consomme le reste de ses arêtes pour ne pas la compter plusieurs fois
				for steps := 0; steps < len(refs) && cur != a0; steps++ {
					outs := next[cur]
					if len(outs) == 0 || used[[2]int32{cur, outs[0]}] {
						break
					}
					used[[2]int32{cur, outs[0]}] = true
					cur = outs[0]
				}
				skipped++
				continue
			}
			// le comblement parcourt la boucle à l'envers : il recoud b → a face à a → b
			poly := make([]int32, len(loop))
			for i, v := range loop {
				poly[len(loop)-1-i] = v
			}
			for _, t := range triangulatePolygon(poly, verts) {
				out = append(out, t)
				added++
			}
			filled++
		}
	}
	return out, filled, skipped, added
}

// triangulatePolygon triangule un polygone 3D (indices dans verts, ordre = orientation voulue des triangles)
// par ear clipping dans le plan de la normale de Newell ; éventail si aucun sommet d'oreille n'est trouvé.
func triangulatePolygon(poly []int32, verts []Vec3) []Tri32 {
	n := len(poly)
	if n == 3 {
		return []Tri32{{poly[0], poly[1], poly[2]}}
	}
	// normale de Newell
	var nrm Vec3
	for i := range poly {
		p, q := verts[poly[i]], verts[poly[(i+1)%n]]
		nrm.X += (p.Y - q.Y) * (p.Z + q.Z)
		nrm.Y += (p.Z - q.Z) * (p.X + q.X)
		nrm.Z += (p.X - q.X) * (p.Y + q.Y)
	}
	nrm = nrm.Norm()
	idx := append([]int32(nil), poly...)
	var out []Tri32
	convex := func(a, b, c int32) bool {
		return verts[b].Sub(verts[a]).Cross(verts[c].Sub(verts[b])).Dot(nrm) > 1e-12
	}
	inside := func(p, a, b, c Vec3) bool {
		s1 := b.Sub(a).Cross(p.Sub(a)).Dot(nrm)
		s2 := c.Sub(b).Cross(p.Sub(b)).Dot(nrm)
		s3 := a.Sub(c).Cross(p.Sub(c)).Dot(nrm)
		return s1 >= -1e-12 && s2 >= -1e-12 && s3 >= -1e-12
	}
	guard := 0
	for len(idx) > 3 && guard < 4*n*n {
		guard++
		clipped := false
		for i := 0; i < len(idx); i++ {
			a, b, c := idx[(i+len(idx)-1)%len(idx)], idx[i], idx[(i+1)%len(idx)]
			if !convex(a, b, c) {
				continue
			}
			ear := true
			for _, o := range idx {
				if o == a || o == b || o == c {
					continue
				}
				if inside(verts[o], verts[a], verts[b], verts[c]) {
					ear = false
					break
				}
			}
			if !ear {
				continue
			}
			out = append(out, Tri32{a, b, c})
			idx = append(idx[:i], idx[i+1:]...)
			clipped = true
			break
		}
		if !clipped {
			break
		}
	}
	if len(idx) == 3 {
		out = append(out, Tri32{idx[0], idx[1], idx[2]})
	} else { // polygone non simple / sans oreille : éventail
		for i := 1; i+1 < len(idx); i++ {
			out = append(out, Tri32{idx[0], idx[i], idx[i+1]})
		}
	}
	return out
}
