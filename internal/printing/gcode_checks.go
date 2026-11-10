package printing

import (
	"archive/zip"
	"bufio"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// Contrôles de validation tirés du G-CODE RÉEL de la plaque (jamais du maillage seul) :
//   - FB5 : la couche 1 réelle (îlots et surface de contact, bordure et bases de supports comprises) pour le risque
//     de décollement ;
//   - FB4 : l'élancement des colonnes (troncs de supports, piliers de la pièce) : hauteur / section.
// Ces contrôles sont du « meilleur effort » : si le G-code n'est pas lisible (archive factice, plaque absente), ils
// se taisent et le contrôle sur le maillage reste en vigueur.

// ---- FB5 : couche 1 réelle ----

// realContact : ce que la couche 1 du G-code pose réellement sur le plateau.
type realContact struct {
	AreasMM2 []float64
	Brim     bool // des filets de bordure (feature « brim ») sont dans la couche 1
}

// realFirstLayerContact lit la couche 1 de la plaque et rend les îlots de contact (jupe et tour d'essuyage exclues,
// bordure et bases de supports comprises : ce sont de vrais filets sur le plateau).
func realFirstLayerContact(archive string, plate int) (*realContact, error) {
	zr, zf, err := openPlateGcode(archive, plate)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	gs, err := scanGcode(zf, map[int]bool{1: true})
	if err != nil {
		return nil, err
	}
	l1 := gs.Layers[1]
	if l1 == nil || len(l1.Segs) == 0 {
		return nil, fmt.Errorf("couche 1 sans extrusion")
	}
	rc := &realContact{}
	vb := viewBox{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	var part []seg
	for _, sg := range l1.Segs {
		if sg.feat >= 0 {
			name := gs.Feats[sg.feat]
			if excludedFeature(name) {
				continue
			}
			if strings.Contains(strings.ToLower(name), "brim") {
				rc.Brim = true
			}
		}
		part = append(part, sg)
		vb.minX, vb.maxX = math.Min(vb.minX, math.Min(sg.x0, sg.x1)), math.Max(vb.maxX, math.Max(sg.x0, sg.x1))
		vb.minY, vb.maxY = math.Min(vb.minY, math.Min(sg.y0, sg.y1)), math.Max(vb.maxY, math.Max(sg.y0, sg.y1))
	}
	if len(part) == 0 {
		return nil, fmt.Errorf("couche 1 sans pièce (jupe ou tour seulement)")
	}
	vb = viewBox{vb.minX - contactLineWidth, vb.minY - contactLineWidth, vb.maxX + contactLineWidth, vb.maxY + contactLineWidth}
	g := newContactGrid(vb)
	for _, sg := range part {
		g.stroke(sg.x0, sg.y0, sg.x1, sg.y1, contactLineWidth)
	}
	for _, isl := range g.islands() {
		rc.AreasMM2 = append(rc.AreasMM2, isl.AreaMM2)
	}
	if len(rc.AreasMM2) == 0 {
		return nil, fmt.Errorf("couche 1 sans îlot mesurable")
	}
	return rc, nil
}

// realAdhesion : risque de décollement fondé sur la couche 1 réelle. height / footprint : hauteur et emprise au
// sol de la pièce (maillage), faute de mieux ; nil si le G-code n'est pas lisible.
func realAdhesion(archive string, plate int, heightMM, footprintMM2 float64) *mesh.AdhesionReport {
	rc, err := realFirstLayerContact(archive, plate)
	if err != nil {
		return nil
	}
	return mesh.AdhesionFromContact(heightMM, footprintMM2, rc.AreasMM2, rc.Brim)
}

// ---- FB4 : élancement ----

// Seuil d'élancement (hauteur / section) à partir duquel une colonne est signalée. 10 : règle d'atelier classique pour
// l'impression FDM (un pilier plus haut que 10 fois sa largeur, sans appui latéral, fléchit sous la pression de la
// buse, puis casse ou se couche). Pour mémoire, le contrôle d'adhérence de la pièce entière signale déjà ≥ 4 (modéré)
// et ≥ 6 (élevé) sur sa surface de contact. Configurable : voir slenderRatio (plancher 2).
const (
	DefaultSlenderRatio = 10.0
	slenderMinRatio     = 2.0  // plancher accepté pour le réglage
	slenderMinHeightMM  = 20.0 // en dessous, une colonne n'est jamais signalée (bruit, petits détails)
	slenderCellMM       = 0.5  // résolution de la grille de projection
	slenderMinCells     = 4    // composante plus petite : bruit ignoré
	slenderWorstListed  = 5
)

// SlenderElement : une colonne élancée (tronc de support ou pilier de la pièce).
type SlenderElement struct {
	Kind      string  `json:"kind" jsonschema:"support | part"`
	HeightMM  float64 `json:"height_mm" jsonschema:"hauteur de la colonne (Z du plus haut moins Z du plus bas)"`
	SectionMM float64 `json:"section_mm" jsonschema:"section typique : plus petite dimension (mm) de son emprise, médiane sur les couches"`
	Ratio     float64 `json:"ratio" jsonschema:"hauteur / section"`
	BaseZMM   float64 `json:"base_z_mm"`
	X         float64 `json:"x_mm" jsonschema:"position de son pied sur le plateau"`
	Y         float64 `json:"y_mm"`
}

// SlenderReport : colonnes élancées du G-code réel.
type SlenderReport struct {
	Limit    float64          `json:"limit" jsonschema:"seuil d'élancement utilisé (hauteur / section)"`
	Supports int              `json:"supports" jsonschema:"colonnes de supports au-dessus du seuil"`
	Parts    int              `json:"parts" jsonschema:"piliers de la pièce au-dessus du seuil"`
	Worst    []SlenderElement `json:"worst,omitempty" jsonschema:"les pires colonnes, du plus élancé au moins élancé (5 premières)"`
}

// Flagged : au moins une colonne dépasse le seuil.
func (r *SlenderReport) Flagged() bool { return r != nil && (r.Supports+r.Parts) > 0 }

// Summary : phrase pour l'humain et le modèle (vide si rien à signaler).
func (r *SlenderReport) Summary() string {
	if !r.Flagged() {
		return ""
	}
	var what []string
	if r.Supports > 0 {
		what = append(what, fmt.Sprintf("%d tronc(s) de support", r.Supports))
	}
	if r.Parts > 0 {
		what = append(what, fmt.Sprintf("%d pilier(s) de la pièce", r.Parts))
	}
	w := r.Worst[0]
	k := "tronc de support"
	if w.Kind == "part" {
		k = "pilier de la pièce"
	}
	return fmt.Sprintf("Élancement : %s plus haut(s) que %.0f × leur section. Le pire (%s vers X=%.0f Y=%.0f mm) fait %.0f mm de haut pour %.1f mm de section, soit %.0f × : il risque de fléchir sous la buse, de se coucher et de provoquer des spaghettis. "+
		"Pistes : épaissir les troncs (tree_support_branch_diameter, tree_support_wall_count), passer en supports normaux ou réduire les surplombs, réorienter la pièce, ou surveiller de près les premières heures.",
		strings.Join(what, " et "), r.Limit, k, w.X, w.Y, w.HeightMM, w.SectionMM, w.Ratio)
}

// slenderRatio : seuil effectif. Priorité : demande de l'appel, puis réglage du service (Deps.SlenderRatio), puis
// variable d'environnement BAMBU_SLENDER_RATIO, puis DefaultSlenderRatio.
func (s *Service) slenderRatio(perCall float64) float64 {
	pick := func(v float64) (float64, bool) {
		if v >= slenderMinRatio && !math.IsNaN(v) && !math.IsInf(v, 0) {
			return v, true
		}
		return 0, false
	}
	if v, ok := pick(perCall); ok {
		return v
	}
	if v, ok := pick(s.d.SlenderRatio); ok {
		return v
	}
	if e := strings.TrimSpace(os.Getenv("BAMBU_SLENDER_RATIO")); e != "" {
		if f, err := strconv.ParseFloat(e, 64); err == nil {
			if v, ok := pick(f); ok {
				return v
			}
		}
	}
	return DefaultSlenderRatio
}

type cellKey [2]int32

type slNode struct {
	layer  int
	z      float64
	class  int // 0 pièce, 1 support
	minDim float64
	cx, cy float64
	x0, y0 float64
	parent int
}

// analyzeSlenderness lit tout le G-code de la plaque et mesure les colonnes (composantes connexes en 3D, par type :
// supports d'un côté, pièce de l'autre ; jupe, tour d'essuyage et bordure exclues). Chaque couche est projetée sur une
// grille de 0,5 mm ; les composantes de couches successives qui se recouvrent forment une colonne.
func analyzeSlenderness(archive string, plate int, limit float64) (*SlenderReport, error) {
	zr, zf, err := openPlateGcode(archive, plate)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	var nodes []slNode
	var find func(i int) int
	find = func(i int) int {
		for nodes[i].parent != i {
			nodes[i].parent = nodes[nodes[i].parent].parent
			i = nodes[i].parent
		}
		return i
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			nodes[ra].parent = rb
		}
	}
	var prev [2]map[cellKey]int // cellule -> nœud, couche précédente, par classe

	handle := func(layerIdx int, z float64, segs []seg, feats []string) {
		var cells [2]map[cellKey]struct{}
		for _, sg := range segs {
			if sg.feat < 0 {
				continue
			}
			name := strings.ToLower(feats[sg.feat])
			if excludedFeature(name) || strings.Contains(name, "brim") {
				continue
			}
			cl := 0
			if strings.Contains(name, "support") {
				cl = 1
			}
			if cells[cl] == nil {
				cells[cl] = map[cellKey]struct{}{}
			}
			d := math.Hypot(sg.x1-sg.x0, sg.y1-sg.y0)
			n := int(d/(slenderCellMM/2)) + 1
			for i := 0; i <= n; i++ {
				t := float64(i) / float64(n)
				x, y := sg.x0+(sg.x1-sg.x0)*t, sg.y0+(sg.y1-sg.y0)*t
				cells[cl][cellKey{int32(math.Floor(x / slenderCellMM)), int32(math.Floor(y / slenderCellMM))}] = struct{}{}
			}
		}
		for cl := 0; cl < 2; cl++ {
			cur := map[cellKey]int{}
			seen := map[cellKey]bool{}
			var stack []cellKey
			for start := range cells[cl] {
				if seen[start] {
					continue
				}
				id := len(nodes)
				nd := slNode{layer: layerIdx, z: z, class: cl, parent: id}
				minX, minY, maxX, maxY := start[0], start[1], start[0], start[1]
				var comp []cellKey
				stack = append(stack[:0], start)
				seen[start] = true
				for len(stack) > 0 {
					c := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					comp = append(comp, c)
					minX, maxX = min(minX, c[0]), max(maxX, c[0])
					minY, maxY = min(minY, c[1]), max(maxY, c[1])
					for dx := int32(-1); dx <= 1; dx++ {
						for dy := int32(-1); dy <= 1; dy++ {
							q := cellKey{c[0] + dx, c[1] + dy}
							if _, ok := cells[cl][q]; ok && !seen[q] {
								seen[q] = true
								stack = append(stack, q)
							}
						}
					}
				}
				if len(comp) < slenderMinCells {
					continue
				}
				w := float64(maxX-minX+1) * slenderCellMM
				h := float64(maxY-minY+1) * slenderCellMM
				nd.minDim = math.Min(w, h)
				nd.x0, nd.y0 = float64(minX)*slenderCellMM, float64(minY)*slenderCellMM
				nd.cx, nd.cy = nd.x0+w/2, nd.y0+h/2
				nodes = append(nodes, nd)
				for _, c := range comp {
					cur[c] = id
					if p, ok := prev[cl][c]; ok {
						union(id, p)
					}
				}
			}
			prev[cl] = cur
		}
	}
	if err := streamGcodeLayers(zf, handle); err != nil {
		return nil, err
	}

	// regroupement par colonne (racine de l'union-find)
	groups := map[int][]int{}
	for i := range nodes {
		r := find(i)
		groups[r] = append(groups[r], i)
	}
	rep := &SlenderReport{Limit: limit}
	var all []SlenderElement
	for _, idxs := range groups {
		zmin, zmax := math.Inf(1), math.Inf(-1)
		byLayer := map[int][]float64{}
		low := idxs[0]
		for _, i := range idxs {
			n := nodes[i]
			zmin, zmax = math.Min(zmin, n.z), math.Max(zmax, n.z)
			byLayer[n.layer] = append(byLayer[n.layer], n.minDim)
			if n.layer < nodes[low].layer {
				low = i
			}
		}
		height := zmax - zmin
		if height < slenderMinHeightMM {
			continue
		}
		// section de chaque couche = la plus mince des composantes présentes ; section typique = médiane des couches
		per := make([]float64, 0, len(byLayer))
		for _, ds := range byLayer {
			m := ds[0]
			for _, d := range ds {
				m = math.Min(m, d)
			}
			per = append(per, m)
		}
		sort.Float64s(per)
		section := per[len(per)/2]
		if section <= 0 {
			continue
		}
		ratio := height / section
		if ratio < limit {
			continue
		}
		kind := "part"
		if nodes[low].class == 1 {
			kind = "support"
			rep.Supports++
		} else {
			rep.Parts++
		}
		all = append(all, SlenderElement{Kind: kind, HeightMM: round1f(height), SectionMM: round1f(section), Ratio: round1f(ratio),
			BaseZMM: round1f(zmin), X: round1f(nodes[low].cx), Y: round1f(nodes[low].cy)})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Ratio != all[j].Ratio {
			return all[i].Ratio > all[j].Ratio
		}
		return all[i].HeightMM > all[j].HeightMM
	})
	if len(all) > slenderWorstListed {
		all = all[:slenderWorstListed]
	}
	rep.Worst = all
	return rep, nil
}

func round1f(v float64) float64 { return math.Round(v*10) / 10 }

// streamGcodeLayers lit le G-code d'une plaque en flux et appelle fn à la fin de chaque couche (numérotée à partir de 1)
// avec ses segments extrudés ; rien n'est gardé en mémoire d'une couche à l'autre. feats : types (FEATURE) connus à ce
// stade, seg.feat en est un index.
func streamGcodeLayers(zf *zip.File, fn func(layer int, z float64, segs []seg, feats []string)) error {
	rc, err := zf.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	br := bufio.NewReaderSize(rc, 1<<20)
	cur, abs, absE := 0, true, false
	var x, y, e, zNow, layerZ float64
	feat := -1
	featIdx := map[string]int{}
	var feats []string
	var segs []seg
	flush := func() {
		if cur > 0 {
			fn(cur, layerZ, segs, feats)
		}
		segs = segs[:0]
	}
	for {
		line, rerr := br.ReadString('\n')
		if len(line) > 4096 {
			line = line[:4096]
		}
		l := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(l, "; CHANGE_LAYER"):
			flush()
			cur++
			layerZ = zNow
		case strings.HasPrefix(l, "; Z_HEIGHT:") && cur > 0:
			layerZ, _ = strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(l, "; Z_HEIGHT:")), 64)
		case strings.HasPrefix(l, "; FEATURE:"):
			name := strings.TrimSpace(strings.TrimPrefix(l, "; FEATURE:"))
			i, ok := featIdx[name]
			if !ok {
				i = len(feats)
				featIdx[name] = i
				feats = append(feats, name)
			}
			feat = i
		case l == "G90":
			abs = true
		case l == "G91":
			abs = false
		case l == "M82":
			absE = true
		case l == "M83":
			absE = false
		case strings.HasPrefix(l, "G92"):
			if ne, ok := parseCoord(l, 'E'); ok {
				e = ne
			}
		case strings.HasPrefix(l, "G0 ") || strings.HasPrefix(l, "G1 ") || strings.HasPrefix(l, "G2 ") || strings.HasPrefix(l, "G3 "):
			if i := strings.IndexByte(l, ';'); i >= 0 {
				l = l[:i]
			}
			nx, hx := parseCoord(l, 'X')
			ny, hy := parseCoord(l, 'Y')
			ne, he := parseCoord(l, 'E')
			if nz, hz := parseCoord(l, 'Z'); hz {
				if abs {
					zNow = nz
				} else {
					zNow += nz
				}
			}
			px, py := x, y
			if hx {
				if abs {
					x = nx
				} else {
					x += nx
				}
			}
			if hy {
				if abs {
					y = ny
				} else {
					y += ny
				}
			}
			extr := false
			if he {
				if absE {
					extr = ne > e+1e-9
					e = ne
				} else {
					extr = ne > 0
				}
			}
			if extr && (hx || hy) && cur > 0 {
				segs = append(segs, seg{px, py, x, y, feat})
			}
		}
		if rerr != nil {
			break
		}
	}
	flush()
	return nil
}
