// Package strength : test de résistance d'une pièce imprimée (zones candidates, questionnaire guidé, calcul par
// éléments finis via internal/fem, cartes de contraintes, historique).
//
// Tout ici est une AIDE : calcul linéaire élastique simplifié, jamais une certification ni un essai réel.
package strength

import (
	"fmt"
	"math"
	"sort"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// Genres de zones (ordre = ordre de numérotation).
const (
	KindBase       = "base"            // face tournée vers le bas au niveau du plan le plus bas
	KindDessus     = "dessus"          // face plane tournée vers le haut
	KindParoiXm    = "paroi_x-"        // face plane tournée vers -X
	KindParoiXp    = "paroi_x+"        // face plane tournée vers +X
	KindParoiYm    = "paroi_y-"        // face plane tournée vers -Y
	KindParoiYp    = "paroi_y+"        // face plane tournée vers +Y
	KindFaceBasse  = "face_basse"      // face plane tournée vers le bas, au-dessus de la base
	KindInclineH   = "incline_haut"    // surface inclinée ou courbe tournée vers le haut (crochet, berceau)
	KindInclineB   = "incline_bas"     // surface inclinée ou courbe tournée vers le bas
	KindInclineLat = "incline_lateral" // surface inclinée ou courbe latérale
)

var kindOrder = map[string]int{KindBase: 0, KindDessus: 1, KindParoiXm: 2, KindParoiXp: 3, KindParoiYm: 4, KindParoiYp: 5,
	KindInclineH: 6, KindFaceBasse: 7, KindInclineB: 8, KindInclineLat: 9}

// Zone est une région de la surface proposée comme appui ou comme zone de charge.
type Zone struct {
	ID         int        `json:"id" jsonschema:"numéro de la zone (stable pour un même maillage) : celui dessiné sur l'image"`
	Kind       string     `json:"kind"`
	Label      string     `json:"label" jsonschema:"description en français"`
	AreaMM2    float64    `json:"area_mm2"`
	Centroid   [3]float64 `json:"centroid_mm"`
	BBoxMin    [3]float64 `json:"bbox_min_mm"`
	BBoxMax    [3]float64 `json:"bbox_max_mm"`
	Normal     [3]float64 `json:"normal"`
	Candidates []string   `json:"candidates" jsonschema:"rôles plausibles : support_pose (posée dessus), support_fixe (vissée / collée), charge"`
	Extremity  bool       `json:"extremity,omitempty" jsonschema:"extrémité éloignée des appuis probables (bout de bras, crochet)"`
	Ground     bool       `json:"ground_plane,omitempty" jsonschema:"plan de pose détecté automatiquement : surface de contact avec le sol (ou la table), regroupée en une seule zone même si elle est morcelée"`
	GroundZMM  float64    `json:"ground_z_mm,omitempty" jsonschema:"altitude Z du plan de pose (mm)"`
	NormalAxis int        `json:"-"`
	tris       []int      // triangles du maillage d'analyse
}

// ZoneSet : zones et maillage d'analyse (soudé, éventuellement décimé) sur lequel elles ont été calculées.
type ZoneSet struct {
	Zones      []Zone
	Mesh       *mesh.Mesh
	Decimated  bool
	TotalArea  float64
	SourceTris int
}

const (
	maxAnalysisTris = 200_000
	maxZones        = 12
	minZoneAreaFrac = 0.015
)

// analysisMesh soude les sommets et, si besoin, décime de façon déterministe (ne dépend que du maillage).
func analysisMesh(m *mesh.Mesh) (*mesh.Mesh, bool) {
	bb := m.Bounds()
	diag := bb.Size.Len()
	out := m
	decimated := false
	if len(m.T) > maxAnalysisTris && diag > 0 {
		g := math.Max(4, math.Sqrt(float64(maxAnalysisTris)/2))
		for i := 0; i < 40; i++ {
			out = mesh.Decimate(m, diag/g)
			if len(out.T) <= maxAnalysisTris {
				break
			}
			g *= 0.8
		}
		decimated = true
	}
	// soudure
	remap, _ := mesh.Weld(out, 1e-5*math.Max(diag, 1))
	w := &mesh.Mesh{}
	idx := map[int]int{}
	for _, t := range out.T {
		var nt mesh.Tri
		for k := 0; k < 3; k++ {
			r := remap[t[k]]
			id, ok := idx[r]
			if !ok {
				id = len(w.V)
				idx[r] = id
				w.V = append(w.V, out.V[t[k]])
			}
			nt[k] = id
		}
		if nt[0] == nt[1] || nt[1] == nt[2] || nt[0] == nt[2] {
			continue
		}
		w.T = append(w.T, nt)
	}
	return w, decimated
}

type triInfo struct {
	n     mesh.Vec3
	area  float64
	class int
}

const classGround = 9 // triangles du plan de pose

// groundPlane cherche le plan de pose : parmi les faces tournées vers le bas (≈ -Z), le plan le plus bas dont l'aire
// compte (au moins 25 % de celle du plus grand plan horizontal tourné vers le bas, et un minimum absolu). Une base
// coupée à Z = 2 mm est ainsi trouvée même si de petits détails descendent plus bas ou si la face est morcelée.
// Sans plan strictement horizontal, repli sur la matière tournée vers le bas la plus basse (appui approximatif).
func groundPlane(m *mesh.Mesh, info []triInfo, total float64) (z, tol float64, ok bool) {
	bb := m.Bounds()
	tol = math.Max(0.25, 0.005*bb.Size.Z)
	minArea := math.Max(2, 0.002*total)
	areaAt := map[int]float64{}
	sumZ := map[int]float64{}
	for i, t := range m.T {
		if info[i].class != 1 {
			continue
		}
		hi := math.Max(m.V[t[0]].Z, math.Max(m.V[t[1]].Z, m.V[t[2]].Z))
		lo := math.Min(m.V[t[0]].Z, math.Min(m.V[t[1]].Z, m.V[t[2]].Z))
		if hi-lo > tol { // face inclinée ou à cheval sur plusieurs plans
			continue
		}
		k := int(math.Floor((lo + hi) / 2 / tol))
		areaAt[k] += info[i].area
		sumZ[k] += info[i].area * (lo + hi) / 2
	}
	maxA := 0.0
	for _, a := range areaAt {
		maxA = math.Max(maxA, a)
	}
	best, found := 0, false
	for k, a := range areaAt {
		if a >= math.Max(minArea, 0.25*maxA) && (!found || k < best) {
			best, found = k, true
		}
	}
	if found {
		return sumZ[best] / areaAt[best], tol * 1.5, true
	}
	slab := math.Max(1, 0.02*bb.Size.Z) // repli : faces tournées vers le bas dans la tranche la plus basse
	a := 0.0
	for i, t := range m.T {
		if info[i].n.Z <= -0.5 && math.Max(m.V[t[0]].Z, math.Max(m.V[t[1]].Z, m.V[t[2]].Z)) <= bb.Min.Z+slab {
			a += info[i].area
		}
	}
	if a >= minArea {
		return bb.Min.Z + slab/2, slab, true
	}
	return 0, 0, false
}

func classOf(n mesh.Vec3) int {
	ax, ay, az := math.Abs(n.X), math.Abs(n.Y), math.Abs(n.Z)
	const tol = 0.94
	switch {
	case az >= tol && az >= ax && az >= ay:
		if n.Z > 0 {
			return 0 // +Z
		}
		return 1 // -Z
	case ax >= tol && ax >= ay:
		if n.X > 0 {
			return 2
		}
		return 3
	case ay >= tol:
		if n.Y > 0 {
			return 4
		}
		return 5
	}
	switch {
	case n.Z >= 0.3:
		return 6
	case n.Z <= -0.3:
		return 7
	}
	return 8
}

// DetectZones découpe la surface en régions planes (une par direction axiale) ou courbes (haut / bas / latérales),
// connexes par arêtes, garde les plus grandes et les numérote de façon STABLE (ne dépend pas de l'ordre des triangles).
func DetectZones(src *mesh.Mesh) *ZoneSet {
	zs := &ZoneSet{SourceTris: len(src.T)}
	m, dec := analysisMesh(src)
	zs.Mesh, zs.Decimated = m, dec
	nt := len(m.T)
	if nt == 0 {
		return zs
	}
	info := make([]triInfo, nt)
	for i := range m.T {
		n, a := m.Normal(i)
		info[i] = triInfo{n, a, classOf(n)}
		zs.TotalArea += a
	}
	// plan de pose : surface de contact avec le sol, détectée avant le découpage général (voir groundPlane)
	gz, gTol, groundOK := groundPlane(m, info, zs.TotalArea)
	if groundOK {
		for i, t := range m.T {
			hi := math.Max(m.V[t[0]].Z, math.Max(m.V[t[1]].Z, m.V[t[2]].Z))
			lo := math.Min(m.V[t[0]].Z, math.Min(m.V[t[1]].Z, m.V[t[2]].Z))
			if info[i].n.Z <= -0.5 && hi <= gz+gTol && lo >= gz-gTol {
				info[i].class = classGround
			}
		}
	}
	// adjacence par arêtes (même classe) -> composantes
	parent := make([]int32, nt)
	for i := range parent {
		parent[i] = int32(i)
	}
	var find func(x int32) int32
	find = func(x int32) int32 {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	type ekey struct{ a, b int32 }
	edges := make(map[ekey]int32, nt*3/2)
	for i, t := range m.T {
		for k := 0; k < 3; k++ {
			a, b := int32(t[k]), int32(t[(k+1)%3])
			if a > b {
				a, b = b, a
			}
			key := ekey{a, b}
			if j, ok := edges[key]; ok {
				if info[j].class == info[i].class {
					ra, rb := find(j), find(int32(i))
					if ra != rb {
						parent[ra] = rb
					}
				}
			} else {
				edges[key] = int32(i)
			}
		}
	}
	comps := map[int32][]int{}
	for i := 0; i < nt; i++ {
		r := find(int32(i))
		if info[i].class == classGround { // toutes les facettes du plan de pose forment UNE zone, même morcelée
			r = -1
		}
		comps[r] = append(comps[r], i)
	}
	bb := m.Bounds()
	minArea := math.Max(minZoneAreaFrac*zs.TotalArea, 1e-9)
	var zones []Zone
	for _, tris := range comps {
		area := 0.0
		var c, nsum mesh.Vec3
		lo := mesh.Vec3{X: math.Inf(1), Y: math.Inf(1), Z: math.Inf(1)}
		hi := mesh.Vec3{X: math.Inf(-1), Y: math.Inf(-1), Z: math.Inf(-1)}
		for _, ti := range tris {
			t := m.T[ti]
			a := info[ti].area
			area += a
			cen := m.V[t[0]].Add(m.V[t[1]]).Add(m.V[t[2]]).Scale(1.0 / 3)
			c = c.Add(cen.Scale(a))
			nsum = nsum.Add(info[ti].n.Scale(a))
			for k := 0; k < 3; k++ {
				v := m.V[t[k]]
				lo = mesh.Vec3{X: math.Min(lo.X, v.X), Y: math.Min(lo.Y, v.Y), Z: math.Min(lo.Z, v.Z)}
				hi = mesh.Vec3{X: math.Max(hi.X, v.X), Y: math.Max(hi.Y, v.Y), Z: math.Max(hi.Z, v.Z)}
			}
		}
		if area < minArea && info[tris[0]].class != classGround {
			continue
		}
		c = c.Scale(1 / area)
		cls := info[tris[0]].class
		z := Zone{AreaMM2: area, Centroid: [3]float64{c.X, c.Y, c.Z}, BBoxMin: [3]float64{lo.X, lo.Y, lo.Z}, BBoxMax: [3]float64{hi.X, hi.Y, hi.Z},
			Normal: [3]float64{nsum.Norm().X, nsum.Norm().Y, nsum.Norm().Z}, tris: tris}
		nearBottom := lo.Z-bb.Min.Z <= math.Max(0.6, 0.02*bb.Size.Z)
		switch cls {
		case classGround:
			z.Kind, z.NormalAxis, z.Ground, z.GroundZMM = KindBase, 2, true, math.Round(c.Z*100)/100
		case 0:
			z.Kind, z.NormalAxis = KindDessus, 2
		case 1:
			z.NormalAxis = 2
			if nearBottom {
				z.Kind = KindBase
			} else {
				z.Kind = KindFaceBasse
			}
		case 2:
			z.Kind, z.NormalAxis = KindParoiXp, 0
		case 3:
			z.Kind, z.NormalAxis = KindParoiXm, 0
		case 4:
			z.Kind, z.NormalAxis = KindParoiYp, 1
		case 5:
			z.Kind, z.NormalAxis = KindParoiYm, 1
		case 6:
			z.Kind, z.NormalAxis = KindInclineH, 2
		case 7:
			z.Kind, z.NormalAxis = KindInclineB, 2
		default:
			z.Kind, z.NormalAxis = KindInclineLat, 0
		}
		zones = append(zones, z)
	}
	r2 := func(v float64) float64 { return math.Round(v*100) / 100 }
	sort.Slice(zones, func(i, j int) bool {
		a, b := zones[i], zones[j]
		if kindOrder[a.Kind] != kindOrder[b.Kind] {
			return kindOrder[a.Kind] < kindOrder[b.Kind]
		}
		if math.Abs(a.AreaMM2-b.AreaMM2) > 0.05*math.Max(a.AreaMM2, b.AreaMM2) {
			return a.AreaMM2 > b.AreaMM2
		}
		for k := 0; k < 3; k++ {
			if r2(a.Centroid[k]) != r2(b.Centroid[k]) {
				return r2(a.Centroid[k]) < r2(b.Centroid[k])
			}
		}
		return a.AreaMM2 > b.AreaMM2
	})
	// garder les plus grandes (en conservant l'ordre de numérotation)
	if len(zones) > maxZones {
		bySize := make([]int, len(zones))
		for i := range bySize {
			bySize[i] = i
		}
		sort.SliceStable(bySize, func(a, b int) bool { return zones[bySize[a]].AreaMM2 > zones[bySize[b]].AreaMM2 })
		keep := map[int]bool{}
		for i, z := range zones { // le plan de pose est toujours conservé
			if z.Ground {
				keep[i] = true
			}
		}
		for _, i := range bySize[:maxZones] {
			if len(keep) >= maxZones {
				break
			}
			keep[i] = true
		}
		var kept []Zone
		for i, z := range zones {
			if keep[i] {
				kept = append(kept, z)
			}
		}
		zones = kept
	}
	// rôles
	var baseC mesh.Vec3
	baseArea := 0.0
	for _, z := range zones {
		if z.Kind == KindBase {
			baseC = baseC.Add(mesh.Vec3{X: z.Centroid[0], Y: z.Centroid[1], Z: z.Centroid[2]}.Scale(z.AreaMM2))
			baseArea += z.AreaMM2
		}
	}
	anchor := mesh.Vec3{X: bb.Min.X + bb.Size.X/2, Y: bb.Min.Y + bb.Size.Y/2, Z: bb.Min.Z}
	if baseArea > 0 {
		anchor = baseC.Scale(1 / baseArea)
	}
	maxD := 0.0
	dist := make([]float64, len(zones))
	for i, z := range zones {
		d := mesh.Vec3{X: z.Centroid[0], Y: z.Centroid[1], Z: z.Centroid[2]}.Sub(anchor).Len()
		dist[i] = d
		maxD = math.Max(maxD, d)
	}
	for i := range zones {
		z := &zones[i]
		z.ID = i + 1
		z.Extremity = maxD > 0 && dist[i] >= 0.7*maxD && z.Kind != KindBase
		bigWall := z.AreaMM2 >= 0.05*zs.TotalArea
		switch z.Kind {
		case KindBase:
			z.Label = "face basse au niveau du plan le plus bas (appui au sol probable)"
			if z.Ground {
				z.Label = fmt.Sprintf("plan de pose détecté automatiquement (surface de contact avec le sol à Z = %.1f mm, %.0f mm²) : appui posé probable", z.GroundZMM, z.AreaMM2)
			}
			z.Candidates = []string{"support_pose", "support_fixe"}
		case KindDessus:
			z.Label = "face plane tournée vers le haut"
			z.Candidates = []string{"charge", "support_pose"}
		case KindParoiXm, KindParoiXp, KindParoiYm, KindParoiYp:
			side := map[string]string{KindParoiXm: "-X (gauche)", KindParoiXp: "+X (droite)", KindParoiYm: "-Y (avant)", KindParoiYp: "+Y (arrière)"}[z.Kind]
			z.Label = "face plane tournée vers " + side
			if bigWall {
				z.Label += ", assez grande pour une fixation murale"
				z.Candidates = []string{"support_fixe", "charge"}
			} else {
				z.Candidates = []string{"charge"}
			}
		case KindFaceBasse:
			z.Label = "face plane tournée vers le bas, au-dessus de la base (dessous d'un bras)"
			z.Candidates = []string{"support_pose", "charge"}
		case KindInclineH:
			z.Label = "surface inclinée ou courbe tournée vers le haut (crochet, berceau, bras)"
			z.Candidates = []string{"charge"}
		case KindInclineB:
			z.Label = "surface inclinée ou courbe tournée vers le bas"
			z.Candidates = []string{"support_pose"}
		default:
			z.Label = "surface latérale inclinée ou courbe"
			z.Candidates = []string{"charge"}
		}
		if z.Extremity {
			z.Label += " ; extrémité éloignée des appuis"
		}
	}
	zs.Zones = zones
	return zs
}

// ByID cherche une zone.
func (zs *ZoneSet) ByID(id int) (*Zone, bool) {
	for i := range zs.Zones {
		if zs.Zones[i].ID == id {
			return &zs.Zones[i], true
		}
	}
	return nil, false
}

// Ground : le plan de pose détecté automatiquement (surface de contact avec le sol), s'il existe.
func (zs *ZoneSet) Ground() (*Zone, bool) {
	for i := range zs.Zones {
		if zs.Zones[i].Ground {
			return &zs.Zones[i], true
		}
	}
	return nil, false
}

// SamplePoints : points répartis sur la zone (≈ un par spacing² mm²), avec l'aire que chacun représente.
func (zs *ZoneSet) SamplePoints(z *Zone, spacing float64) (pts []mesh.Vec3, area []float64) {
	m := zs.Mesh
	for _, ti := range z.tris {
		t := m.T[ti]
		a, b, c := m.V[t[0]], m.V[t[1]], m.V[t[2]]
		_, ar := m.Normal(ti)
		if ar <= 0 {
			continue
		}
		n := int(math.Ceil(math.Sqrt(ar) / spacing))
		if n < 1 {
			n = 1
		}
		if n > 40 {
			n = 40
		}
		cnt := n * n
		for i := 0; i < n; i++ {
			for j := 0; j < n-i; j++ {
				for up := 0; up < 2; up++ {
					if up == 1 && j == n-i-1 {
						continue
					}
					var u, v float64
					if up == 0 {
						u, v = (float64(i)+1.0/3)/float64(n), (float64(j)+1.0/3)/float64(n)
					} else {
						u, v = (float64(i)+2.0/3)/float64(n), (float64(j)+2.0/3)/float64(n)
					}
					w := 1 - u - v
					pts = append(pts, a.Scale(w).Add(b.Scale(u)).Add(c.Scale(v)))
					area = append(area, ar/float64(cnt))
				}
			}
		}
	}
	return
}

// Describe : ligne lisible d'une zone.
func (z *Zone) Describe() string {
	return fmt.Sprintf("zone %d : %s, %.0f mm², centre (%.1f ; %.1f ; %.1f) mm", z.ID, z.Label, z.AreaMM2, z.Centroid[0], z.Centroid[1], z.Centroid[2])
}
