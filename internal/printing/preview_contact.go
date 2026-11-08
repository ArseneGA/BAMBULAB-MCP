package printing

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// Mode first_layer : la couche 1 RÉELLE (îlots de contact avec le plateau). Deux sources :
//   - le G-code de l'artefact : segments extrudés de la couche 1, filets de 0,45 mm, rastérisés à 0,1 mm ;
//   - AVANT découpe : section du maillage à Z≈0 (faces tournées vers le bas à moins de 0,25 mm du plan bas).
// Les îlots (composantes connexes) sont numérotés du plus grand au plus petit, avec leur aire en mm².

const (
	contactLineWidth = 0.45 // mm : largeur de filet supposée pour estimer l'aire depuis le G-code
	contactBand      = 0.25 // mm : épaisseur sous laquelle une face du maillage est « au contact »
	contactMinArea   = 0.2  // mm² : en dessous, composante ignorée (bruit numérique)
	contactMaxCells  = 4_000_000
	contactListed    = 12 // îlots détaillés dans la légende
	contactLabelMax  = 40 // îlots numérotés sur l'image
)

// ContactIsland : un îlot de contact avec le plateau.
type ContactIsland struct {
	Index   int        `json:"index" jsonschema:"numéro affiché sur l'image (1 = le plus grand)"`
	AreaMM2 float64    `json:"area_mm2"`
	CenterX float64    `json:"center_x_mm"`
	CenterY float64    `json:"center_y_mm"`
	BBox    [4]float64 `json:"bbox_mm" jsonschema:"[x0,y0,x1,y1] en mm"`
}

var islandPalette = []color.RGBA{
	{0xe6, 0x39, 0x46, 255}, {0xff, 0x9f, 0x1c, 255}, {0x2e, 0xc4, 0xb6, 255}, {0x83, 0x38, 0xec, 255}, {0x3a, 0x86, 0xff, 255}, {0x06, 0xd6, 0xa0, 255},
	{0xff, 0x00, 0x6e, 255}, {0xfb, 0x56, 0x07, 255}, {0x00, 0x96, 0xc7, 255}, {0xe0, 0xb0, 0x00, 255}, {0x7b, 0xc6, 0x1d, 255}, {0xc7, 0x3e, 0xe0, 255},
}

func islandColor(i int) color.RGBA { return islandPalette[(i-1)%len(islandPalette)] }

// ---- grille de contact ----

type contactGrid struct {
	x0, y0, cell float64
	w, h         int
	lab          []int32 // 0 = vide ; avant étiquetage : 1 = couvert
}

func newContactGrid(vb viewBox) *contactGrid {
	cell := 0.1
	if n := vb.w() / cell * vb.h() / cell; n > contactMaxCells {
		cell = math.Sqrt(vb.w() * vb.h() / contactMaxCells)
	}
	g := &contactGrid{x0: vb.minX, y0: vb.minY, cell: cell, w: int(vb.w()/cell) + 2, h: int(vb.h()/cell) + 2}
	g.lab = make([]int32, g.w*g.h)
	return g
}

func (g *contactGrid) at(x, y float64) (int, int) {
	return int(math.Floor((x - g.x0) / g.cell)), int(math.Floor((y - g.y0) / g.cell))
}

func (g *contactGrid) set(ix, iy int) {
	if ix >= 0 && iy >= 0 && ix < g.w && iy < g.h {
		g.lab[iy*g.w+ix] = 1
	}
}

// stamp couvre le disque de centre (x,y) et de rayon r.
func (g *contactGrid) stamp(x, y, r float64) {
	cx, cy := g.at(x, y)
	n := int(math.Ceil(r/g.cell)) + 1
	for dy := -n; dy <= n; dy++ {
		for dx := -n; dx <= n; dx++ {
			px, py := g.x0+(float64(cx+dx)+0.5)*g.cell, g.y0+(float64(cy+dy)+0.5)*g.cell
			if (px-x)*(px-x)+(py-y)*(py-y) <= r*r {
				g.set(cx+dx, cy+dy)
			}
		}
	}
}

// stroke couvre un filet de largeur width le long du segment.
func (g *contactGrid) stroke(x0, y0, x1, y1, width float64) {
	r := width / 2
	step := g.cell * 0.5
	d := math.Hypot(x1-x0, y1-y0)
	n := int(d/step) + 1
	for i := 0; i <= n; i++ {
		t := float64(i) / float64(n)
		g.stamp(x0+(x1-x0)*t, y0+(y1-y0)*t, r)
	}
}

// fillTri couvre les cellules dont le centre est dans le triangle (vue de dessus).
func (g *contactGrid) fillTri(a, b, c [2]float64) {
	minX, maxX := math.Min(a[0], math.Min(b[0], c[0])), math.Max(a[0], math.Max(b[0], c[0]))
	minY, maxY := math.Min(a[1], math.Min(b[1], c[1])), math.Max(a[1], math.Max(b[1], c[1]))
	ix0, iy0 := g.at(minX, minY)
	ix1, iy1 := g.at(maxX, maxY)
	den := (b[1]-c[1])*(a[0]-c[0]) + (c[0]-b[0])*(a[1]-c[1])
	if math.Abs(den) < 1e-12 {
		return
	}
	for iy := iy0; iy <= iy1; iy++ {
		for ix := ix0; ix <= ix1; ix++ {
			px, py := g.x0+(float64(ix)+0.5)*g.cell, g.y0+(float64(iy)+0.5)*g.cell
			u := ((b[1]-c[1])*(px-c[0]) + (c[0]-b[0])*(py-c[1])) / den
			v := ((c[1]-a[1])*(px-c[0]) + (a[0]-c[0])*(py-c[1])) / den
			if u >= -1e-9 && v >= -1e-9 && 1-u-v >= -1e-9 {
				g.set(ix, iy)
			}
		}
	}
}

// islands étiquette les composantes connexes (8 voisins), ignore les bruits < contactMinArea et renumérote du plus
// grand au plus petit : g.lab contient ensuite l'index d'îlot (0 = aucun).
func (g *contactGrid) islands() []ContactIsland {
	type comp struct {
		cells          int
		sx, sy         float64
		x0, y0, x1, y1 int
		first          int
	}
	var comps []comp
	tmp := make([]int32, len(g.lab))
	var stack []int
	for start, v := range g.lab {
		if v != 1 || tmp[start] != 0 {
			continue
		}
		id := int32(len(comps) + 1)
		c := comp{x0: g.w, y0: g.h, x1: -1, y1: -1, first: start}
		stack = append(stack[:0], start)
		tmp[start] = id
		for len(stack) > 0 {
			p := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			ix, iy := p%g.w, p/g.w
			c.cells++
			c.sx += float64(ix) + 0.5
			c.sy += float64(iy) + 0.5
			c.x0, c.y0, c.x1, c.y1 = min(c.x0, ix), min(c.y0, iy), max(c.x1, ix), max(c.y1, iy)
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					nx, ny := ix+dx, iy+dy
					if nx < 0 || ny < 0 || nx >= g.w || ny >= g.h {
						continue
					}
					q := ny*g.w + nx
					if g.lab[q] == 1 && tmp[q] == 0 {
						tmp[q] = id
						stack = append(stack, q)
					}
				}
			}
		}
		comps = append(comps, c)
	}
	area := func(c comp) float64 { return float64(c.cells) * g.cell * g.cell }
	order := make([]int, 0, len(comps))
	for i, c := range comps {
		if area(c) >= contactMinArea {
			order = append(order, i)
		}
	}
	sort.SliceStable(order, func(a, b int) bool { return comps[order[a]].cells > comps[order[b]].cells })
	rank := make([]int32, len(comps)+1)
	out := make([]ContactIsland, 0, len(order))
	for r, i := range order {
		c := comps[i]
		rank[i+1] = int32(r + 1)
		out = append(out, ContactIsland{
			Index: r + 1, AreaMM2: round2(area(c)),
			CenterX: round2(g.x0 + c.sx/float64(c.cells)*g.cell), CenterY: round2(g.y0 + c.sy/float64(c.cells)*g.cell),
			BBox: [4]float64{round2(g.x0 + float64(c.x0)*g.cell), round2(g.y0 + float64(c.y0)*g.cell), round2(g.x0 + float64(c.x1+1)*g.cell), round2(g.y0 + float64(c.y1+1)*g.cell)},
		})
	}
	for p, t := range tmp {
		g.lab[p] = 0
		if t != 0 {
			g.lab[p] = rank[t]
		}
	}
	return out
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// labelAt : index d'îlot à (x,y), ou autour (3x3 cellules) ; 0 si aucun.
func (g *contactGrid) labelAt(x, y float64) int {
	ix, iy := g.at(x, y)
	for _, r := range []int{0, 1, 2} {
		for dy := -r; dy <= r; dy++ {
			for dx := -r; dx <= r; dx++ {
				nx, ny := ix+dx, iy+dy
				if nx >= 0 && ny >= 0 && nx < g.w && ny < g.h && g.lab[ny*g.w+nx] > 0 {
					return int(g.lab[ny*g.w+nx])
				}
			}
		}
	}
	return 0
}

// ---- sources ----

// excludedFeature : types de trajectoires de la couche 1 qui ne sont PAS la pièce (jupe, tour d'essuyage).
func excludedFeature(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "skirt") || strings.Contains(n, "prime tower") || strings.Contains(n, "wipe tower") || strings.Contains(n, "purge")
}

func (s *Service) previewFirstLayer(ctx context.Context, in PreviewInput, w, h int) (*PreviewOutput, error) {
	switch {
	case in.ArtifactID != "" || in.PrintJobID != "":
		path, sha, plate, j, err := s.resolveGcode(ctx, in)
		if err != nil {
			return nil, err
		}
		var foot []*mesh.Mesh
		var fnote string
		pid := in.PrinterID
		if pid == "" {
			pid = j.PrinterID
		}
		if pid == "" {
			fnote = "emprise grise non dessinee : printer_id absent (le plateau fixe l'origine des pieces dans le 3MF)"
		} else if rec, e := s.GetPrinterRecord(ctx, pid); e != nil {
			fnote = "emprise grise non dessinee : imprimante inconnue"
		} else if pm, e := s.GetPrinterModel(ctx, rec.ModelID); e != nil {
			fnote = "emprise grise non dessinee : modele d'imprimante inconnu"
		} else if bed, e := s.plateBed(rec, pm); e != nil {
			fnote = "emprise grise non dessinee : plateau inconnu"
		} else if objs, e := archivePlateObjects(path, plate, bed); e != nil {
			fnote = "emprise grise non dessinee : pieces illisibles dans le 3MF"
		} else {
			for _, o := range objs {
				foot = append(foot, o.m)
			}
		}
		return renderFirstLayerGcode(path, sha, plate, foot, fnote, w, h)
	case in.ModelID != "":
		_, src, serr := s.sourceFor(ctx, in.ModelID, in.VersionID)
		if serr != nil {
			return nil, serr
		}
		f, oerr := s.d.Blobs.Open(src.SHA256)
		if oerr != nil {
			return nil, oerr
		}
		data, rerr := io.ReadAll(io.LimitReader(f, 512<<20))
		f.Close()
		if rerr != nil {
			return nil, rerr
		}
		md, perr := mesh.Parse(src.Name, data)
		if perr != nil {
			return nil, invalidf("modèle illisible : %v", perr)
		}
		return renderFirstLayerMesh(md.Combined(), src.Name, src.SHA256, w, h)
	}
	return nil, invalidf("mode first_layer : print_job_id ou artifact_id (couche 1 du G-code), ou model_id (section du maillage avant découpe)")
}

func renderFirstLayerGcode(archive, sha string, plate int, foot []*mesh.Mesh, footNote string, w, h int) (*PreviewOutput, error) {
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
		return nil, invalidf("la couche 1 du G-code ne contient aucun segment extrudé")
	}
	var part, skip []seg
	skipNames := map[string]bool{}
	for _, sg := range l1.Segs {
		if sg.feat >= 0 && excludedFeature(gs.Feats[sg.feat]) {
			skip = append(skip, sg)
			skipNames[gs.Feats[sg.feat]] = true
			continue
		}
		part = append(part, sg)
	}
	if len(part) == 0 {
		return nil, invalidf("la couche 1 ne contient que de la jupe ou une tour d'essuyage (aucun contact de la pièce)")
	}
	vbC := viewBox{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)} // contenu seul, marge d'un filet
	for _, sg := range part {
		vbC.minX, vbC.maxX = math.Min(vbC.minX, math.Min(sg.x0, sg.x1)), math.Max(vbC.maxX, math.Max(sg.x0, sg.x1))
		vbC.minY, vbC.maxY = math.Min(vbC.minY, math.Min(sg.y0, sg.y1)), math.Max(vbC.maxY, math.Max(sg.y0, sg.y1))
	}
	vbC = viewBox{vbC.minX - contactLineWidth, vbC.minY - contactLineWidth, vbC.maxX + contactLineWidth, vbC.maxY + contactLineWidth}
	g := newContactGrid(vbC)
	for _, sg := range part {
		g.stroke(sg.x0, sg.y0, sg.x1, sg.y1, contactLineWidth)
	}
	isl := g.islands()
	d := &contactDrawing{g: g, islands: isl, foot: foot, skip: skip, part: part}
	lines := []legendLine{}
	title := fmt.Sprintf("Couche 1 REELLE du G-code - %d ilot(s) de contact avec le plateau, aire totale %.1f mm2%s", len(isl), sumArea(isl), zText(l1.Z))
	lines = append(lines, legendLine{text: title})
	lines = append(lines, islandLines(isl)...)
	lines = append(lines, legendLine{text: "Gris clair = EMPRISE projetee vue de dessus de la piece (silhouette), PAS la surface en contact ; couleurs vives = ce qui est reellement pose sur le plateau a la couche 1"})
	if footNote != "" {
		lines = append(lines, legendLine{text: "Note : " + footNote})
	}
	if len(skip) > 0 {
		var ns []string
		for n := range skipNames {
			ns = append(ns, n)
		}
		sort.Strings(ns)
		lines = append(lines, legendLine{text: "Traits gris fins = " + strings.Join(ns, ", ") + " (hors pièce, non comptes dans les ilots)"})
	}
	lines = append(lines, legendLine{text: fmt.Sprintf("Aire estimee d'apres des filets de %.2f mm (rasterisation %.2f mm, ilots < %.1f mm2 ignores) ; jupe et tour d'essuyage non comptees", contactLineWidth, g.cell, contactMinArea)})
	img, scale, vb := d.render(w, h)
	lines = append(lines, legendLine{text: fmt.Sprintf("Vue cadree sur la piece (%.0f x %.0f mm), grille 10 mm, echelle %.2f px/mm", vb.w(), vb.h(), scale)})
	panel := withLegendPanel(img, lines)
	b, err := encodePNG(panel)
	if err != nil {
		return nil, err
	}
	pl := make([]string, len(lines))
	for i, l := range lines {
		pl[i] = l.text
	}
	return &PreviewOutput{Kind: "first_layer", Width: panel.Bounds().Dx(), Height: panel.Bounds().Dy(), Legend: pl, PNG: b, Islands: isl, ContactAreaMM2: round2(sumArea(isl)),
		Represented:    []string{"îlots de contact de la couche 1 réelle du G-code (filets de la pièce, brim compris), numérotés avec leur aire en mm²", "emprise projetée de la pièce en gris clair quand l'imprimante est connue"},
		NotRepresented: []string{"jupe et tour d'essuyage (exclues du compte)", "écrasement réel du premier filet (aire estimée par des filets de 0,45 mm)", "adhérence réelle (état du plateau, matière, température)", "couches supérieures"},
		Render:         map[string]any{"source": "gcode", "plate": plate, "z_height_mm": l1.Z, "islands": len(isl), "segments": len(part), "line_width_mm": contactLineWidth, "cell_mm": g.cell, "artifact_sha256": sha, "px_per_mm": scale}}, nil
}

func renderFirstLayerMesh(m *mesh.Mesh, name, sha string, w, h int) (*PreviewOutput, error) {
	if len(m.T) == 0 {
		return nil, invalidf("maillage vide")
	}
	bb := m.Bounds()
	zlim := bb.Min.Z + contactBand
	var tris [][3][2]float64
	vb := viewBox{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for ti, t := range m.T {
		a, b, c := m.V[t[0]], m.V[t[1]], m.V[t[2]]
		if a.Z > zlim || b.Z > zlim || c.Z > zlim {
			continue
		}
		if n, _ := m.Normal(ti); n.Z > -0.2 { // seules les faces tournées vers le bas touchent le plateau
			continue
		}
		tr := [3][2]float64{{a.X, a.Y}, {b.X, b.Y}, {c.X, c.Y}}
		tris = append(tris, tr)
		for _, p := range tr {
			vb.minX, vb.maxX, vb.minY, vb.maxY = math.Min(vb.minX, p[0]), math.Max(vb.maxX, p[0]), math.Min(vb.minY, p[1]), math.Max(vb.maxY, p[1])
		}
	}
	if len(tris) == 0 {
		return nil, invalidf("aucune face tournée vers le bas à moins de %.2f mm du plan bas (Z=%.2f mm) : section de contact vide", contactBand, bb.Min.Z)
	}
	vb = viewBox{vb.minX - 0.5, vb.minY - 0.5, vb.maxX + 0.5, vb.maxY + 0.5}
	g := newContactGrid(vb)
	for _, tr := range tris {
		g.fillTri(tr[0], tr[1], tr[2])
	}
	isl := g.islands()
	d := &contactDrawing{g: g, islands: isl, foot: []*mesh.Mesh{m}, tris: tris}
	lines := []legendLine{{text: fmt.Sprintf("Section du maillage a Z=%.2f mm (faces vers le bas a moins de %.2f mm du plan bas) - %d ilot(s) de contact, aire totale %.1f mm2", bb.Min.Z, contactBand, len(isl), sumArea(isl))}}
	lines = append(lines, legendLine{text: "AVANT decoupe, dans l'orientation du fichier : " + name})
	lines = append(lines, islandLines(isl)...)
	lines = append(lines, legendLine{text: "Gris clair = EMPRISE projetee vue de dessus de la piece (silhouette), PAS la surface en contact ; couleurs vives = faces du bas reellement a Z minimal"})
	lines = append(lines, legendLine{text: "Approximation : surfaces inclinees ou creuses a plus de 0.25 mm du plan bas non comptees ; la couche 1 reelle dependra du decoupage (brim, radeau, ecrasement). Pour la couche reelle : decouper puis mode first_layer avec print_job_id"})
	img, scale, vbv := d.render(w, h)
	lines = append(lines, legendLine{text: fmt.Sprintf("Vue cadree sur la piece (%.0f x %.0f mm), grille 10 mm, echelle %.2f px/mm", vbv.w(), vbv.h(), scale)})
	panel := withLegendPanel(img, lines)
	b, err := encodePNG(panel)
	if err != nil {
		return nil, err
	}
	pl := make([]string, len(lines))
	for i, l := range lines {
		pl[i] = l.text
	}
	return &PreviewOutput{Kind: "first_layer", Width: panel.Bounds().Dx(), Height: panel.Bounds().Dy(), Legend: pl, PNG: b, Islands: isl, ContactAreaMM2: round2(sumArea(isl)),
		Represented:    []string{"îlots de contact estimés par la section du maillage au plan bas (faces vers le bas à moins de 0,25 mm), numérotés avec leur aire en mm²", "emprise projetée de la pièce en gris clair"},
		NotRepresented: []string{"la couche 1 réelle du G-code (brim, radeau, écrasement du filet, supports)", "orientation choisie au découpage", "adhérence réelle"},
		Render:         map[string]any{"source": "mesh_section", "z_plane_mm": bb.Min.Z, "band_mm": contactBand, "islands": len(isl), "triangles": len(tris), "cell_mm": g.cell, "source_sha256": sha, "px_per_mm": scale}}, nil
}

func sumArea(isl []ContactIsland) float64 {
	var t float64
	for _, i := range isl {
		t += i.AreaMM2
	}
	return t
}

func islandLines(isl []ContactIsland) []legendLine {
	var out []legendLine
	for k, i := range isl {
		if k >= contactListed {
			var rest float64
			for _, r := range isl[k:] {
				rest += r.AreaMM2
			}
			out = append(out, legendLine{text: fmt.Sprintf("... et %d autre(s) ilot(s), %.1f mm2 au total", len(isl)-k, rest)})
			break
		}
		c := islandColor(i.Index)
		out = append(out, legendLine{
			text:   fmt.Sprintf("%d : %.1f mm2 (%s x %s mm) vers X=%s Y=%s", i.Index, i.AreaMM2, trimF(i.BBox[2]-i.BBox[0]), trimF(i.BBox[3]-i.BBox[1]), trimF(i.CenterX), trimF(i.CenterY)),
			swatch: &c,
		})
	}
	if len(isl) == 0 {
		out = append(out, legendLine{text: "Aucun ilot de contact detecte."})
	}
	return out
}

// ---- dessin ----

type contactDrawing struct {
	g       *contactGrid
	islands []ContactIsland
	foot    []*mesh.Mesh
	part    []seg
	skip    []seg
	tris    [][3][2]float64
}

// render : vue de dessus cadrée sur le contenu, retourne l'image, l'échelle finale (px/mm) et le cadre.
func (d *contactDrawing) render(w, h int) (*image.RGBA, float64, viewBox) {
	// cadre : contact + emprise + jupe
	vb := viewBox{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	ext := func(x, y float64) {
		vb.minX, vb.maxX, vb.minY, vb.maxY = math.Min(vb.minX, x), math.Max(vb.maxX, x), math.Min(vb.minY, y), math.Max(vb.maxY, y)
	}
	for _, i := range d.islands {
		ext(i.BBox[0], i.BBox[1])
		ext(i.BBox[2], i.BBox[3])
	}
	for _, m := range d.foot {
		if len(m.V) > 0 {
			bb := m.Bounds()
			ext(bb.Min.X, bb.Min.Y)
			ext(bb.Max.X, bb.Max.Y)
		}
	}
	if len(d.skip) > 0 && !math.IsInf(vb.minX, 1) {
		// la jupe est cadrée avec la pièce, sauf si elle écrase la pièce (plus de 9 fois sa surface)
		with := vb
		for _, s := range d.skip {
			with.minX, with.maxX = math.Min(with.minX, math.Min(s.x0, s.x1)), math.Max(with.maxX, math.Max(s.x0, s.x1))
			with.minY, with.maxY = math.Min(with.minY, math.Min(s.y0, s.y1)), math.Max(with.maxY, math.Max(s.y0, s.y1))
		}
		if with.w()*with.h() <= 9*math.Max(vb.w()*vb.h(), 100) {
			vb = with
		}
	}
	if math.IsInf(vb.minX, 1) {
		vb = viewBox{0, 0, 100, 100}
	}
	vb = vb.padded(0.08, 30)
	const margin = 30
	iw, ih := w-2*margin, h-2*margin
	sc := math.Min(float64(iw)/vb.w(), float64(ih)/vb.h())
	pw, ph := int(vb.w()*sc), int(vb.h()*sc)
	W, H := (pw+2*margin)*2, (ph+2*margin)*2
	cv := newCanvas(W, H, color.RGBA{0xf3, 0xf1, 0xed, 255})
	s2 := sc * 2
	ox, oy := float64(margin*2), float64(margin*2+ph*2)
	px := func(x float64) float64 { return ox + (x-vb.minX)*s2 }
	py := func(y float64) float64 { return oy - (y-vb.minY)*s2 }
	for y := 0; y < ph*2; y++ {
		for x := 0; x < pw*2; x++ {
			cv.pix[(margin*2+y)*W+margin*2+x] = color.RGBA{0xfb, 0xfa, 0xf7, 255}
		}
	}
	grid := color.RGBA{0xe4, 0xe1, 0xda, 255}
	if sc >= 1.5 {
		for gx := math.Ceil(vb.minX/10) * 10; gx <= vb.maxX; gx += 10 {
			cv.line(px(gx), py(vb.minY), px(gx), py(vb.maxY), grid, 1)
		}
		for gy := math.Ceil(vb.minY/10) * 10; gy <= vb.maxY; gy += 10 {
			cv.line(px(vb.minX), py(gy), px(vb.maxX), py(gy), grid, 1)
		}
	}
	// emprise projetée (gris clair)
	foot := color.RGBA{0xc8, 0xc5, 0xbd, 255}
	for _, m := range d.foot {
		for ti, t := range m.T {
			if n, _ := m.Normal(ti); n.Z < -0.02 {
				continue
			}
			a, b, c := m.V[t[0]], m.V[t[1]], m.V[t[2]]
			cv.tri(px(a.X), py(a.Y), 0, px(b.X), py(b.Y), 0, px(c.X), py(c.Y), 0, foot)
		}
	}
	// jupe et tour (gris fin)
	for _, s := range d.skip {
		cv.line(px(s.x0), py(s.y0), px(s.x1), py(s.y1), color.RGBA{0x9a, 0x97, 0x90, 255}, 2)
	}
	// contact (couleurs vives)
	thick := min(max(int(math.Round(contactLineWidth*s2)), 2), 12)
	for _, s := range d.part {
		k := d.g.labelAt((s.x0+s.x1)/2, (s.y0+s.y1)/2)
		c := color.RGBA{0x77, 0x77, 0x77, 255}
		if k > 0 {
			c = islandColor(k)
		}
		cv.line(px(s.x0), py(s.y0), px(s.x1), py(s.y1), c, thick)
	}
	for _, t := range d.tris {
		k := d.g.labelAt((t[0][0]+t[1][0]+t[2][0])/3, (t[0][1]+t[1][1]+t[2][1])/3)
		if k > 0 {
			cv.tri(px(t[0][0]), py(t[0][1]), 1, px(t[1][0]), py(t[1][1]), 1, px(t[2][0]), py(t[2][1]), 1, islandColor(k))
		}
	}
	img := cv.downsample2()
	// repères d'axes
	step := 10.0
	for _, st := range []float64{10, 20, 50, 100} {
		step = st
		if st*sc >= 60 {
			break
		}
	}
	axis := color.RGBA{0x55, 0x55, 0x55, 255}
	for gx := math.Ceil(vb.minX/step) * step; gx <= vb.maxX; gx += step {
		drawText(img, margin+int((gx-vb.minX)*sc)-8, margin+ph+6, 1, axis, strconv.Itoa(int(gx)))
	}
	for gy := math.Ceil(vb.minY/step) * step; gy <= vb.maxY; gy += step {
		drawText(img, 2, margin+ph-int((gy-vb.minY)*sc)-3, 1, axis, strconv.Itoa(int(gy)))
	}
	if len(d.islands) <= contactLabelMax {
		for _, i := range d.islands {
			cx, cy := margin+int((i.CenterX-vb.minX)*sc), margin+ph-int((i.CenterY-vb.minY)*sc)
			txt := strconv.Itoa(i.Index)
			label(img, cx-textWidth(txt, 2)/2, cy-7, 2, color.RGBA{255, 255, 255, 255}, txt)
		}
	}
	return img, sc, vb
}
