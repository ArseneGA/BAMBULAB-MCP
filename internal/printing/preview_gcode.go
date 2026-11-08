package printing

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strconv"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

// Lecture du G-code d'une plaque (partagée par les aperçus de couche, la planche-contact, l'animation, la couche 1
// et la visionneuse de trajectoires de la vue MCP App).

type gcodeLayer struct {
	Segs []seg
	Z    float64
}

type gcodeScan struct {
	Total  int
	Layers map[int]*gcodeLayer // couches demandées seulement
	Feats  []string            // types (FEATURE) dans l'ordre de première apparition ; seg.feat est un index dans cette liste
}

// openPlateGcode ouvre Metadata/plate_N.gcode dans l'archive. Le lecteur zip doit être fermé par l'appelant.
func openPlateGcode(archive string, plate int) (*zip.ReadCloser, *zip.File, error) {
	if plate <= 0 {
		plate = 1
	}
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return nil, nil, invalidf("archive illisible : %v", err)
	}
	entry := fmt.Sprintf("Metadata/plate_%d.gcode", plate)
	for _, f := range zr.File {
		if f.Name == entry {
			return zr, f, nil
		}
	}
	zr.Close()
	return nil, nil, fmt.Errorf("%w : %s absent de l'archive", ErrNotFound, entry)
}

// countGcodeLayers compte les marqueurs « ; CHANGE_LAYER ».
func countGcodeLayers(zf *zip.File) (int, error) {
	rc, err := zf.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	br := bufio.NewReaderSize(rc, 1<<20)
	n := 0
	for {
		line, rerr := br.ReadString('\n')
		if len(line) > 4096 {
			line = line[:4096]
		}
		if strings.HasPrefix(strings.TrimSpace(line), "; CHANGE_LAYER") {
			n++
		}
		if rerr != nil {
			break
		}
	}
	return n, nil
}

// scanGcode lit le G-code une fois et rend les segments extrudés des couches demandées (numérotées à partir de 1).
// La lecture s'arrête après la plus haute couche demandée.
func scanGcode(zf *zip.File, want map[int]bool) (*gcodeScan, error) {
	maxWant := 0
	for l := range want {
		maxWant = max(maxWant, l)
	}
	rc, err := zf.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	br := bufio.NewReaderSize(rc, 1<<20)
	gs := &gcodeScan{Layers: map[int]*gcodeLayer{}}
	cur, abs, absE := 0, true, false
	var x, y, e float64
	feat := -1
	featIdx := map[string]int{}
	var layer *gcodeLayer
	for {
		line, rerr := br.ReadString('\n')
		if len(line) > 4096 { // lignes de configuration énormes
			line = line[:4096]
		}
		l := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(l, "; CHANGE_LAYER"):
			cur++
			layer = nil
			if cur > maxWant {
				gs.Total = cur - 1
				return gs, nil // plus rien à lire
			}
			if want[cur] {
				layer = &gcodeLayer{}
				gs.Layers[cur] = layer
			}
		case strings.HasPrefix(l, "; Z_HEIGHT:") && layer != nil:
			layer.Z, _ = strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(l, "; Z_HEIGHT:")), 64)
		case strings.HasPrefix(l, "; FEATURE:"):
			name := strings.TrimSpace(strings.TrimPrefix(l, "; FEATURE:"))
			i, ok := featIdx[name]
			if !ok {
				i = len(gs.Feats)
				featIdx[name] = i
				gs.Feats = append(gs.Feats, name)
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
		case strings.HasPrefix(l, "G1 ") || strings.HasPrefix(l, "G2 ") || strings.HasPrefix(l, "G3 "):
			if i := strings.IndexByte(l, ';'); i >= 0 {
				l = l[:i]
			}
			nx, hx := parseCoord(l, 'X')
			ny, hy := parseCoord(l, 'Y')
			ne, he := parseCoord(l, 'E')
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
			if extr && (hx || hy) && layer != nil {
				layer.Segs = append(layer.Segs, seg{px, py, x, y, feat})
			}
		}
		if rerr != nil {
			break
		}
	}
	gs.Total = cur
	return gs, nil
}

// ---- sélection de couches ----

const (
	defaultTiles  = 9
	maxSheetTiles = 16
	maxGifFrames  = 48
)

// pickLayers choisit les couches d'une planche : liste explicite (validée), une sur N, ou répartition régulière.
func pickLayers(total int, layers []int, everyN, maxTiles int, gif bool) (sel []int, note string, err error) {
	limit := maxSheetTiles
	if gif {
		limit = maxGifFrames
	}
	if maxTiles <= 0 {
		maxTiles = min(defaultTiles, limit)
		if gif {
			maxTiles = min(16, limit)
		}
	}
	if maxTiles > limit {
		maxTiles = limit
		note = fmt.Sprintf("max_tiles ramené à %d (limite)", limit)
	}
	switch {
	case len(layers) > 0:
		seen := map[int]bool{}
		for _, l := range layers {
			if l < 1 || l > total {
				return nil, "", invalidf("couche %d hors plage (1..%d)", l, total)
			}
			if !seen[l] {
				seen[l] = true
				sel = append(sel, l)
			}
		}
		sortInts(sel)
		if len(sel) > maxTiles {
			return nil, "", invalidf("%d couches demandées, max_tiles = %d : réduire la liste ou relever max_tiles (limite %d)", len(sel), maxTiles, limit)
		}
	case everyN > 0:
		for l := 1; l <= total; l += everyN {
			sel = append(sel, l)
		}
		if len(sel) > maxTiles {
			k := len(sel)
			sel = spread(total, maxTiles)
			note = strings.TrimSpace(note + fmt.Sprintf(" every_n=%d donnerait %d vignettes : %d couches réparties régulièrement (max_tiles)", everyN, k, len(sel)))
		}
	default:
		sel = spread(total, maxTiles)
	}
	return sel, note, nil
}

// spread : k couches réparties régulièrement de 1 à total (extrémités incluses).
func spread(total, k int) []int {
	if k >= total {
		out := make([]int, total)
		for i := range out {
			out[i] = i + 1
		}
		return out
	}
	if k <= 1 {
		return []int{1}
	}
	var out []int
	for i := 0; i < k; i++ {
		l := 1 + int(math.Round(float64(i)*float64(total-1)/float64(k-1)))
		if len(out) == 0 || out[len(out)-1] != l {
			out = append(out, l)
		}
	}
	return out
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

// ---- cadrage et dessin d'une vignette ----

type viewBox struct{ minX, minY, maxX, maxY float64 }

func (v viewBox) w() float64 { return v.maxX - v.minX }
func (v viewBox) h() float64 { return v.maxY - v.minY }

// boundsOf : cadre commun aux segments donnés, avec marge ; au moins 20 mm de côté.
func boundsOf(layers []*gcodeLayer) viewBox {
	v := viewBox{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for _, l := range layers {
		if l == nil {
			continue
		}
		for _, s := range l.Segs {
			v.minX, v.maxX = math.Min(v.minX, math.Min(s.x0, s.x1)), math.Max(v.maxX, math.Max(s.x0, s.x1))
			v.minY, v.maxY = math.Min(v.minY, math.Min(s.y0, s.y1)), math.Max(v.maxY, math.Max(s.y0, s.y1))
		}
	}
	if math.IsInf(v.minX, 1) {
		return viewBox{0, 0, 100, 100}
	}
	return v.padded(0.06, 20)
}

func (v viewBox) padded(frac, minSpan float64) viewBox {
	cx, cy := (v.minX+v.maxX)/2, (v.minY+v.maxY)/2
	hw := math.Max(v.w()*(0.5+frac), minSpan/2)
	hh := math.Max(v.h()*(0.5+frac), minSpan/2)
	return viewBox{cx - hw, cy - hh, cx + hw, cy + hh}
}

// drawSegsTile dessine les segments d'une couche dans une vignette tw x th pixels (suréchantillonnage 2x),
// grille 10 mm, filets à leur largeur réelle approchée (0,45 mm).
func drawSegsTile(segs []seg, vb viewBox, tw, th int) *image.RGBA {
	W, H := tw*2, th*2
	cv := newCanvas(W, H, color.RGBA{0xf3, 0xf1, 0xed, 255})
	sc := math.Min(float64(W)/vb.w(), float64(H)/vb.h())
	px := func(x float64) float64 { return float64(W)/2 + (x-(vb.minX+vb.maxX)/2)*sc }
	py := func(y float64) float64 { return float64(H)/2 - (y-(vb.minY+vb.maxY)/2)*sc }
	grid := color.RGBA{0xdd, 0xda, 0xd3, 255}
	if sc >= 2 { // pas de grille illisible quand la vue est très large
		for g := math.Floor(vb.minX/10) * 10; g <= vb.maxX; g += 10 {
			cv.line(px(g), py(vb.minY), px(g), py(vb.maxY), grid, 1)
		}
		for g := math.Floor(vb.minY/10) * 10; g <= vb.maxY; g += 10 {
			cv.line(px(vb.minX), py(g), px(vb.maxX), py(g), grid, 1)
		}
	}
	thick := int(math.Round(0.45 * sc))
	thick = min(max(thick, 2), 6)
	for _, s := range segs {
		col := color.RGBA{0x33, 0x33, 0x33, 255}
		if s.feat >= 0 {
			col = palette[s.feat%len(palette)]
		}
		cv.line(px(s.x0), py(s.y0), px(s.x1), py(s.y1), col, thick)
	}
	return cv.downsample2()
}

// blit copie src dans dst à (x0,y0).
func blit(dst, src *image.RGBA, x0, y0 int) {
	b := src.Bounds()
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			dst.SetRGBA(x0+x, y0+y, src.RGBAAt(b.Min.X+x, b.Min.Y+y))
		}
	}
}

// label dessine un texte lisible sur n'importe quel fond (contour noir).
func label(img *image.RGBA, x, y, scale int, fg color.RGBA, s string) {
	for dx := -1; dx <= 1; dx++ {
		for dy := -1; dy <= 1; dy++ {
			drawText(img, x+dx, y+dy, scale, color.RGBA{0, 0, 0, 255}, s)
		}
	}
	drawText(img, x, y, scale, fg, s)
}

// ---- légende à hauteur automatique ----

type legendLine struct {
	text   string
	swatch *color.RGBA
}

func plain(lines ...string) []legendLine {
	out := make([]legendLine, len(lines))
	for i, l := range lines {
		out[i] = legendLine{text: l}
	}
	return out
}

// withLegendPanel colle sous l'image un bandeau de légende dont la hauteur suit le texte (retours à la ligne).
func withLegendPanel(img *image.RGBA, lines []legendLine) *image.RGBA {
	W := img.Bounds().Dx()
	maxChars := max((W-40)/12, 20)
	type wl struct {
		t  string
		sw *color.RGBA
	}
	var wrapped []wl
	for _, l := range lines {
		rest, sw := l.text, l.swatch
		for len([]rune(rest)) > maxChars {
			rs := []rune(rest)
			cut := maxChars
			if i := strings.LastIndex(string(rs[:cut]), " "); i > 10 {
				cut = len([]rune(string(rs[:cut])[:i]))
			}
			wrapped = append(wrapped, wl{string(rs[:cut]), sw})
			rest, sw = "    "+strings.TrimSpace(string(rs[cut:])), nil
		}
		wrapped = append(wrapped, wl{rest, sw})
	}
	const lineH = 18
	panel := len(wrapped)*lineH + 16
	out := image.NewRGBA(image.Rect(0, 0, W, img.Bounds().Dy()+panel))
	blit(out, img, 0, 0)
	bg := color.RGBA{0x17, 0x1a, 0x1f, 255}
	for y := img.Bounds().Dy(); y < out.Bounds().Dy(); y++ {
		for x := 0; x < W; x++ {
			out.SetRGBA(x, y, bg)
		}
	}
	y := img.Bounds().Dy() + 8
	for _, l := range wrapped {
		if l.sw != nil {
			for dy := 0; dy < 12; dy++ {
				for dx := 0; dx < 12; dx++ {
					out.SetRGBA(10+dx, y+1+dy, *l.sw)
				}
			}
		}
		drawText(out, 28, y, 2, color.RGBA{0xe6, 0xe8, 0xeb, 255}, l.t)
		y += lineH
	}
	return out
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---- résolution de la source G-code ----

// resolveGcode retrouve l'archive d'un artefact ou d'une impression.
func (s *Service) resolveGcode(ctx context.Context, in PreviewInput) (path, sha string, plate int, j Job, err error) {
	sha, plate = in.ArtifactID, in.Plate
	if in.PrintJobID != "" {
		if j, err = s.GetJob(ctx, in.PrintJobID); err != nil {
			return "", "", 0, j, err
		}
		sha = j.ArtifactSHA256
		if plate == 0 {
			plate = j.Plate
		}
	}
	if sha == "" || !s.d.Blobs.Has(sha) {
		return "", "", 0, j, fmt.Errorf("%w : artefact absent du stockage", ErrNotFound)
	}
	path, err = s.d.Blobs.Path(sha)
	return path, sha, plate, j, err
}

// printerBed : dimensions du plateau d'une imprimante connue (nil sinon).
func (s *Service) printerBed(ctx context.Context, printerID string) *contracts.Dimensions {
	if printerID == "" {
		return nil
	}
	rec, e := s.GetPrinterRecord(ctx, printerID)
	if e != nil {
		return nil
	}
	m, e := s.GetPrinterModel(ctx, rec.ModelID)
	if e != nil {
		return nil
	}
	return m.BuildVolume
}

// ---- planche-contact et animation ----

type sheetParams struct {
	Layers   []int
	EveryN   int
	MaxTiles int
	GIF      bool
}

func (s *Service) previewLayerSheet(ctx context.Context, in PreviewInput, w, h int) (*PreviewOutput, error) {
	if in.ArtifactID == "" && in.PrintJobID == "" {
		return nil, invalidf("mode layers : print_job_id ou artifact_id requis")
	}
	path, sha, plate, _, err := s.resolveGcode(ctx, in)
	if err != nil {
		return nil, err
	}
	return renderLayerSheet(path, sha, plate, sheetParams{Layers: in.Layers, EveryN: in.EveryN, MaxTiles: in.MaxTiles, GIF: in.Format == "gif"}, w, h)
}

func featureLegend(feats []string, used map[int]bool) string {
	var fl []string
	for i, f := range feats {
		if used[i] {
			c := palette[i%len(palette)]
			fl = append(fl, fmt.Sprintf("%s=#%02X%02X%02X", f, c.R, c.G, c.B))
		}
	}
	return strings.Join(fl, " ; ")
}

// renderLayerSheet rend une planche-contact PNG (grille de vignettes numérotées avec leur Z), ou un GIF animé.
// Toutes les vignettes partagent le même cadrage : les couches sont comparables.
func renderLayerSheet(archive, sha string, plate int, sp sheetParams, w, h int) (*PreviewOutput, error) {
	if plate <= 0 {
		plate = 1
	}
	zr, zf, err := openPlateGcode(archive, plate)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	total, err := countGcodeLayers(zf)
	if err != nil {
		return nil, err
	}
	if total == 0 {
		return nil, invalidf("aucun marqueur CHANGE_LAYER dans le G-code de la plaque %d", plate)
	}
	sel, note, err := pickLayers(total, sp.Layers, sp.EveryN, sp.MaxTiles, sp.GIF)
	if err != nil {
		return nil, err
	}
	want := map[int]bool{}
	for _, l := range sel {
		want[l] = true
	}
	gs, err := scanGcode(zf, want)
	if err != nil {
		return nil, err
	}
	var all []*gcodeLayer
	used := map[int]bool{}
	nseg := 0
	for _, l := range sel {
		ld := gs.Layers[l]
		if ld == nil {
			ld = &gcodeLayer{}
			gs.Layers[l] = ld
		}
		all = append(all, ld)
		nseg += len(ld.Segs)
		for _, sg := range ld.Segs {
			if sg.feat >= 0 {
				used[sg.feat] = true
			}
		}
	}
	vb := boundsOf(all)
	if sp.GIF {
		return renderLayerGIF(sha, plate, total, sel, gs, vb, used, note, w, h)
	}
	n := len(sel)
	cols := int(math.Ceil(math.Sqrt(float64(n))))
	rows := (n + cols - 1) / cols
	const gap = 6
	boxW, boxH := (w-gap*(cols+1))/cols, (h-gap*(rows+1))/rows
	tw := min(boxW, int(float64(boxH)*vb.w()/vb.h()))
	th := int(float64(tw) * vb.h() / vb.w())
	if tw < 40 || th < 40 {
		return nil, invalidf("vignettes trop petites : réduire max_tiles ou augmenter width/height")
	}
	sheet := image.NewRGBA(image.Rect(0, 0, cols*tw+(cols+1)*gap, rows*th+(rows+1)*gap))
	for i := range sheet.Pix {
		sheet.Pix[i] = 0x2a
	}
	for i, l := range sel {
		ld := gs.Layers[l]
		tile := drawSegsTile(ld.Segs, vb, tw, th)
		x0, y0 := gap+(i%cols)*(tw+gap), gap+(i/cols)*(th+gap)
		blit(sheet, tile, x0, y0)
		txt := fmt.Sprintf("L%d/%d", l, total)
		if ld.Z > 0 {
			txt += fmt.Sprintf("  Z=%.2f", ld.Z)
		}
		label(sheet, x0+5, y0+5, 2, color.RGBA{255, 255, 255, 255}, txt)
	}
	lines := []string{
		fmt.Sprintf("Planche-contact de %d couche(s) sur %d - plaque %d (artefact %s)", n, total, plate, shortSHA(sha)),
		fmt.Sprintf("Couches : %s ; chaque vignette porte son numero et son Z ; meme cadrage et meme echelle (%.1f x %.1f mm) pour comparer", joinInts(sel), vb.w(), vb.h()),
	}
	if fl := featureLegend(gs.Feats, used); fl != "" {
		lines = append(lines, "Types: "+fl)
	}
	if note != "" {
		lines = append(lines, "Selection: "+note)
	}
	lines = append(lines, "Trajectoires extrudees seulement (G1 avec E>0, arcs ramenes a leur corde): ni deplacements a vide, ni retractions, ni duree")
	img := withLegendPanel(sheet, plain(lines...))
	b, err := encodePNG(img)
	if err != nil {
		return nil, err
	}
	return &PreviewOutput{Kind: "gcode_layers", Width: img.Bounds().Dx(), Height: img.Bounds().Dy(), Legend: lines, PNG: b, Layers: sel,
		Represented:    []string{"segments d'extrusion de chaque couche choisie, colorés par type (FEATURE)", "numéro de couche et hauteur Z dans chaque vignette"},
		NotRepresented: []string{"déplacements à vide", "rétractions et purges", "épaisseur réelle des filets", "couches non choisies", "temps"},
		Render:         map[string]any{"plate": plate, "layers": sel, "layers_total": total, "tiles": n, "columns": cols, "segments": nseg, "artifact_sha256": sha, "view_mm": []float64{vb.w(), vb.h()}}}, nil
}

func joinInts(a []int) string {
	p := make([]string, len(a))
	for i, v := range a {
		p[i] = strconv.Itoa(v)
	}
	return strings.Join(p, ", ")
}
