package mesh

import (
	"fmt"
	"math"
	"sort"
)

// AnalyzeOptions règle l'analyse.
type AnalyzeOptions struct {
	// WeldTol : tolérance de soudure des sommets en mm. 0 = max(1e-6, 1e-6 × diagonale).
	WeldTol float64
	// DegenerateAreaEps : aire (mm²) sous laquelle un triangle est dégénéré. 0 = (1e-6 × diagonale)².
	DegenerateAreaEps float64
}

// ComponentInfo décrit une composante connexe (par arêtes/sommets soudés).
type ComponentInfo struct {
	Triangles int     `json:"triangles"`
	Volume    float64 `json:"volume_mm3"`
	BBox      BBox    `json:"bbox"`
}

// Report est le résultat d'analyse géométrique d'un maillage. Ce sont des
// mesures géométriques : elles ne disent rien de la résistance mécanique.
type Report struct {
	Triangles      int  `json:"triangles"`
	RawVertices    int  `json:"raw_vertices"`
	WeldedVertices int  `json:"welded_vertices"`
	BBox           BBox `json:"bbox"`
	// Volume signé en mm³ (négatif si les normales pointent vers l'intérieur).
	Volume    float64 `json:"volume_mm3"`
	Area      float64 `json:"area_mm2"`
	WeldTol   float64 `json:"weld_tolerance_mm"`
	Edges     int     `json:"edges"`
	OpenEdges int     `json:"open_edges"`
	// NonManifoldEdges : arêtes partagées par plus de deux triangles.
	NonManifoldEdges int `json:"non_manifold_edges"`
	// InconsistentEdges : arêtes entre deux triangles d'orientation incohérente (normales mal orientées).
	InconsistentEdges   int  `json:"inconsistent_edges"`
	DegenerateTriangles int  `json:"degenerate_triangles"`
	Watertight          bool `json:"watertight"`
	// InvertedNormals : maillage fermé et cohérent dont le volume signé est négatif.
	InvertedNormals bool            `json:"inverted_normals"`
	Components      int             `json:"components"`
	ComponentList   []ComponentInfo `json:"component_list,omitempty"`
	Warnings        []string        `json:"warnings,omitempty"`
}

// Weld soude les sommets distants de moins de tol (grille spatiale, voisinage 3x3x3).
// Retourne le tableau de correspondance sommet → représentant renuméroté et le nombre de sommets soudés.
func Weld(m *Mesh, tol float64) (remap []int, n int) {
	remap = make([]int, len(m.V))
	if tol <= 0 {
		tol = 1e-9
	}
	type cell [3]int64
	grid := map[cell][]int{} // cellule → indices de représentants (dans reps)
	var reps []Vec3
	cellOf := func(v Vec3) cell {
		return cell{int64(math.Floor(v.X / tol)), int64(math.Floor(v.Y / tol)), int64(math.Floor(v.Z / tol))}
	}
	t2 := tol * tol
	for i, v := range m.V {
		c := cellOf(v)
		found := -1
	search:
		for dx := int64(-1); dx <= 1; dx++ {
			for dy := int64(-1); dy <= 1; dy++ {
				for dz := int64(-1); dz <= 1; dz++ {
					for _, r := range grid[cell{c[0] + dx, c[1] + dy, c[2] + dz}] {
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
			found = len(reps)
			reps = append(reps, v)
			grid[c] = append(grid[c], found)
		}
		remap[i] = found
	}
	return remap, len(reps)
}

type dsu []int

func newDSU(n int) dsu {
	d := make(dsu, n)
	for i := range d {
		d[i] = i
	}
	return d
}
func (d dsu) find(x int) int {
	for d[x] != x {
		d[x] = d[d[x]]
		x = d[x]
	}
	return x
}
func (d dsu) union(a, b int) {
	ra, rb := d.find(a), d.find(b)
	if ra != rb {
		d[ra] = rb
	}
}

// signedVolume calcule le volume signé d'un ensemble de triangles (origine recentrée pour la précision).
func signedVolume(m *Mesh, tris []int, origin Vec3) float64 {
	var v float64
	for _, i := range tris {
		t := m.T[i]
		a, b, c := m.V[t[0]].Sub(origin), m.V[t[1]].Sub(origin), m.V[t[2]].Sub(origin)
		v += a.Dot(b.Cross(c))
	}
	return v / 6
}

// Analyze calcule les mesures géométriques et la qualité topologique d'un maillage.
func Analyze(m *Mesh, opt AnalyzeOptions) *Report {
	r := &Report{Triangles: len(m.T), RawVertices: len(m.V)}
	r.BBox = m.Bounds()
	diag := r.BBox.Size.Len()
	tol := opt.WeldTol
	if tol <= 0 {
		tol = math.Max(1e-6, 1e-6*diag)
	}
	deg := opt.DegenerateAreaEps
	if deg <= 0 {
		deg = math.Max(1e-12, (1e-6*diag)*(1e-6*diag))
	}
	r.WeldTol = tol
	if len(m.T) == 0 {
		r.Warnings = append(r.Warnings, "maillage vide")
		return r
	}
	remap, nw := Weld(m, tol)
	r.WeldedVertices = nw

	type edge struct{ fwd, bwd int }
	edges := map[[2]int]*edge{}
	ds := newDSU(nw)
	valid := make([]bool, len(m.T))
	var validIdx []int
	for i, t := range m.T {
		_, area := m.Normal(i)
		a, b, c := remap[t[0]], remap[t[1]], remap[t[2]]
		if area <= deg || a == b || b == c || a == c {
			r.DegenerateTriangles++
			continue
		}
		valid[i] = true
		validIdx = append(validIdx, i)
		r.Area += area
		for _, e := range [3][2]int{{a, b}, {b, c}, {c, a}} {
			k := [2]int{e[0], e[1]}
			fwd := true
			if e[0] > e[1] {
				k = [2]int{e[1], e[0]}
				fwd = false
			}
			ed := edges[k]
			if ed == nil {
				ed = &edge{}
				edges[k] = ed
			}
			if fwd {
				ed.fwd++
			} else {
				ed.bwd++
			}
		}
		ds.union(a, b)
		ds.union(b, c)
	}
	r.Edges = len(edges)
	for _, e := range edges {
		total := e.fwd + e.bwd
		switch {
		case total == 1:
			r.OpenEdges++
		case total > 2:
			r.NonManifoldEdges++
		case e.fwd != e.bwd:
			r.InconsistentEdges++
		}
	}
	origin := r.BBox.Min.Add(r.BBox.Size.Scale(0.5))
	r.Volume = signedVolume(m, validIdx, origin)
	r.Watertight = r.OpenEdges == 0 && r.NonManifoldEdges == 0 && len(validIdx) > 0
	r.InvertedNormals = r.Watertight && r.InconsistentEdges == 0 && r.Volume < 0

	// Composantes connexes (par triangle valide).
	comp := map[int][]int{}
	for _, i := range validIdx {
		root := ds.find(remap[m.T[i][0]])
		comp[root] = append(comp[root], i)
	}
	r.Components = len(comp)
	if len(comp) > 0 {
		roots := make([]int, 0, len(comp))
		for k := range comp {
			roots = append(roots, k)
		}
		sort.Slice(roots, func(a, b int) bool { return len(comp[roots[a]]) > len(comp[roots[b]]) })
		if len(roots) > 50 {
			roots = roots[:50]
		}
		for _, k := range roots {
			tris := comp[k]
			sub := &Mesh{V: m.V}
			for _, i := range tris {
				sub.T = append(sub.T, m.T[i])
			}
			ci := ComponentInfo{Triangles: len(tris), Volume: signedVolume(m, tris, origin), BBox: sub.usedBounds()}
			r.ComponentList = append(r.ComponentList, ci)
		}
	}

	if r.DegenerateTriangles > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%d triangle(s) dégénéré(s) (aire nulle ou sommets confondus)", r.DegenerateTriangles))
	}
	if r.OpenEdges > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%d arête(s) ouverte(s) : maillage non étanche (trous)", r.OpenEdges))
	}
	if r.NonManifoldEdges > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%d arête(s) non manifold (plus de 2 triangles)", r.NonManifoldEdges))
	}
	if r.InconsistentEdges > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%d arête(s) d'orientation incohérente : normales retournées sur certaines faces", r.InconsistentEdges))
	}
	if r.InvertedNormals {
		r.Warnings = append(r.Warnings, "volume signé négatif : toutes les normales pointent vers l'intérieur")
	}
	if r.Components > 1 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%d composantes connexes (pièces disjointes)", r.Components))
	}
	return r
}

func (m *Mesh) usedBounds() BBox {
	sub := &Mesh{}
	seen := map[int]bool{}
	for _, t := range m.T {
		for _, k := range t {
			if !seen[k] {
				seen[k] = true
				sub.V = append(sub.V, m.V[k])
			}
		}
	}
	return sub.Bounds()
}

// ObjectReport associe un objet du modèle à son analyse.
type ObjectReport struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Report *Report `json:"report"`
}

// ModelReport est l'analyse d'un modèle complet (tous les objets).
type ModelReport struct {
	Format      string         `json:"format"`
	Unit        string         `json:"unit"`
	UnitAssumed bool           `json:"unit_assumed"`
	UnitNote    string         `json:"unit_note,omitempty"`
	Objects     []ObjectReport `json:"objects"`
	Combined    *Report        `json:"combined"`
	Extras      []string       `json:"extras,omitempty"`
	Warnings    []string       `json:"warnings,omitempty"`
}

// Analyze analyse chaque objet puis l'ensemble. Pour un fichier sans unité (STL, OBJ),
// les mm sont une hypothèse signalée avec une indication de plausibilité d'après l'échelle.
func (md *Model) Analyze(opt AnalyzeOptions) *ModelReport {
	mr := &ModelReport{Format: md.Format, Unit: md.Unit, UnitAssumed: md.UnitAssumed, Extras: md.Extras}
	mr.Warnings = append(mr.Warnings, md.Warnings...)
	for _, o := range md.Objects {
		mr.Objects = append(mr.Objects, ObjectReport{ID: o.ID, Name: o.Name, Report: Analyze(o.Mesh, opt)})
	}
	mr.Combined = Analyze(md.Combined(), opt)
	if md.UnitAssumed {
		mr.UnitNote = unitPlausibility(mr.Combined.BBox.Size)
	}
	return mr
}

func unitPlausibility(size Vec3) string {
	mx := math.Max(size.X, math.Max(size.Y, size.Z))
	note := "unité inconnue : millimètres supposés"
	switch {
	case mx == 0:
		return note
	case mx < 1:
		return note + fmt.Sprintf(" ; plus grande dimension %.4g mm : le fichier est peut-être en mètres (×1000) ou en pouces (×25,4) — à confirmer", mx)
	case mx < 5:
		return note + fmt.Sprintf(" ; plus grande dimension %.3g mm : peut-être des pouces ou des centimètres — à confirmer", mx)
	case mx > 1000:
		return note + fmt.Sprintf(" ; plus grande dimension %.0f mm : peut-être des microns ou un modèle très grand — à confirmer", mx)
	}
	return note + fmt.Sprintf(" ; plus grande dimension %.1f mm : plausible pour une pièce imprimable", mx)
}
