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
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// PreviewInput est l'entrée de capture_preview.
type PreviewInput struct {
	ModelID    string `json:"model_id,omitempty" jsonschema:"modèle à représenter (maillage, vue isométrique)"`
	VersionID  string `json:"version_id,omitempty" jsonschema:"version du modèle (défaut : la plus récente)"`
	ArtifactID string `json:"artifact_id,omitempty" jsonschema:"empreinte sha256 d'un .gcode.3mf : aperçu d'une couche du G-code"`
	PrintJobID string `json:"print_job_id,omitempty" jsonschema:"à la place d'artifact_id : l'artefact de cette impression"`
	PrinterID  string `json:"printer_id,omitempty" jsonschema:"pour dessiner le contour du plateau dans l'aperçu de couche"`
	Plate      int    `json:"plate,omitempty" jsonschema:"plaque du G-code (défaut 1)"`
	Layer      int    `json:"layer,omitempty" jsonschema:"couche à tracer, à partir de 1 (défaut : couche médiane)"`
	Width      int    `json:"width,omitempty" jsonschema:"largeur en pixels (200 à 2000, défaut 900)"`
	Height     int    `json:"height,omitempty" jsonschema:"hauteur en pixels (200 à 2000, défaut 700)"`
}

// PreviewOutput décrit l'image et ce qu'elle représente (l'image elle-même est le contenu image du résultat).
type PreviewOutput struct {
	Kind           string         `json:"kind" jsonschema:"mesh_iso | gcode_layer"`
	Width          int            `json:"width"`
	Height         int            `json:"height"`
	PNGSHA256      string         `json:"png_sha256" jsonschema:"empreinte du PNG conservé dans le stockage"`
	Legend         []string       `json:"legend"`
	Represented    []string       `json:"represented"`
	NotRepresented []string       `json:"not_represented"`
	Render         map[string]any `json:"render" jsonschema:"paramètres de rendu"`
	PNG            []byte         `json:"-"`
}

var palette = []color.RGBA{{0x2a, 0x4d, 0x8f, 255}, {0xc2, 0x57, 0x2d, 255}, {0x3b, 0x8a, 0x5a, 255}, {0x8a, 0x3b, 0x7a, 255}, {0xb0, 0x92, 0x1f, 255}, {0x1f, 0x8a, 0x92, 255}, {0x6b, 0x6b, 0x6b, 255}}

func clampDim(v, def int) int {
	if v == 0 {
		return def
	}
	if v < 200 {
		return 200
	}
	if v > 2000 {
		return 2000
	}
	return v
}

// CapturePreview rend un PNG logiciel (Go pur) d'un modèle ou d'une couche de G-code.
func (s *Service) CapturePreview(ctx context.Context, in PreviewInput) (*PreviewOutput, error) {
	w, h := clampDim(in.Width, 900), clampDim(in.Height, 700)
	var out *PreviewOutput
	var err error
	switch {
	case in.ArtifactID != "" || in.PrintJobID != "":
		sha := in.ArtifactID
		var j Job
		if in.PrintJobID != "" {
			if j, err = s.GetJob(ctx, in.PrintJobID); err != nil {
				return nil, err
			}
			sha = j.ArtifactSHA256
			if in.Plate == 0 {
				in.Plate = j.Plate
			}
		}
		if sha == "" || !s.d.Blobs.Has(sha) {
			return nil, fmt.Errorf("%w : artefact absent du stockage", ErrNotFound)
		}
		path, perr := s.d.Blobs.Path(sha)
		if perr != nil {
			return nil, perr
		}
		var bed *contracts.Dimensions
		pid := in.PrinterID
		if pid == "" {
			pid = j.PrinterID
		}
		if pid != "" {
			if rec, e := s.GetPrinterRecord(ctx, pid); e == nil {
				if m, e := s.GetPrinterModel(ctx, rec.ModelID); e == nil {
					bed = m.BuildVolume
				}
			}
		}
		out, err = renderLayer(path, sha, in.Plate, in.Layer, w, h, bed)
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
		out = renderIso(md.Combined(), src.Name, src.SHA256, w, h)
	default:
		return nil, invalidf("model_id, artifact_id ou print_job_id requis")
	}
	if err != nil {
		return nil, err
	}
	sha, _, perr := s.d.Blobs.Put(bytes.NewReader(out.PNG), "")
	if perr == nil {
		out.PNGSHA256 = sha
		if _, err := s.d.Store.DB().ExecContext(ctx, `INSERT OR IGNORE INTO print_previews(sha256, created_at) VALUES(?,?)`, sha, s.now().Format(time.RFC3339Nano)); err != nil {
			s.log.Warn("aperçu non enregistré", "sha", sha, "err", err)
		}
	}
	return out, nil
}

// IsPreview dit si ce blob a été enregistré comme aperçu PNG (GET /api/previews/{sha} ne sert que ceux-là :
// un autre blob du stockage — modèle, G-code, archive — n'est jamais exposé par cette route).
func (s *Service) IsPreview(ctx context.Context, sha string) bool {
	var n int
	return s.d.Store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM print_previews WHERE sha256=?`, sha).Scan(&n) == nil && n > 0
}

// ---- rasterisation ----

type canvas struct {
	w, h  int
	pix   []color.RGBA
	depth []float64
}

func newCanvas(w, h int, bg color.RGBA) *canvas {
	c := &canvas{w: w, h: h, pix: make([]color.RGBA, w*h), depth: make([]float64, w*h)}
	for i := range c.pix {
		c.pix[i] = bg
		c.depth[i] = math.Inf(-1)
	}
	return c
}

// downsample2 réduit une image 2x2 vers 1 (boîte) : anticrénelage par suréchantillonnage.
func (c *canvas) downsample2() *image.RGBA {
	w, h := c.w/2, c.h/2
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var r, g, b int
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					p := c.pix[(2*y+dy)*c.w+2*x+dx]
					r, g, b = r+int(p.R), g+int(p.G), b+int(p.B)
				}
			}
			img.SetRGBA(x, y, color.RGBA{uint8(r / 4), uint8(g / 4), uint8(b / 4), 255})
		}
	}
	return img
}

func (c *canvas) tri(x0, y0, z0, x1, y1, z1, x2, y2, z2 float64, col color.RGBA) {
	minX, maxX := int(math.Floor(math.Min(x0, math.Min(x1, x2)))), int(math.Ceil(math.Max(x0, math.Max(x1, x2))))
	minY, maxY := int(math.Floor(math.Min(y0, math.Min(y1, y2)))), int(math.Ceil(math.Max(y0, math.Max(y1, y2))))
	if minX < 0 {
		minX = 0
	}
	if minY < 0 {
		minY = 0
	}
	if maxX >= c.w {
		maxX = c.w - 1
	}
	if maxY >= c.h {
		maxY = c.h - 1
	}
	den := (y1-y2)*(x0-x2) + (x2-x1)*(y0-y2)
	if math.Abs(den) < 1e-12 {
		return
	}
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			a := ((y1-y2)*(px-x2) + (x2-x1)*(py-y2)) / den
			b := ((y2-y0)*(px-x2) + (x0-x2)*(py-y2)) / den
			g := 1 - a - b
			if a < -1e-9 || b < -1e-9 || g < -1e-9 {
				continue
			}
			z := a*z0 + b*z1 + g*z2
			i := y*c.w + x
			if z > c.depth[i] {
				c.depth[i] = z
				c.pix[i] = col
			}
		}
	}
}

func (c *canvas) line(x0, y0, x1, y1 float64, col color.RGBA, thick int) {
	dx, dy := x1-x0, y1-y0
	n := int(math.Max(math.Abs(dx), math.Abs(dy))) + 1
	for i := 0; i <= n; i++ {
		t := float64(i) / float64(n)
		cx, cy := int(x0+dx*t), int(y0+dy*t)
		for ox := -thick / 2; ox <= thick/2; ox++ {
			for oy := -thick / 2; oy <= thick/2; oy++ {
				x, y := cx+ox, cy+oy
				if x >= 0 && y >= 0 && x < c.w && y < c.h {
					c.pix[y*c.w+x] = col
				}
			}
		}
	}
}

const legendH = 92

// finish ajoute la bande de légende et encode en PNG.
func finish(img *image.RGBA, legend []string) ([]byte, error) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	out := image.NewRGBA(image.Rect(0, 0, w, h+legendH))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out.SetRGBA(x, y, img.RGBAAt(x, y))
		}
	}
	bg := color.RGBA{0x17, 0x1a, 0x1f, 255}
	for y := h; y < h+legendH; y++ {
		for x := 0; x < w; x++ {
			out.SetRGBA(x, y, bg)
		}
	}
	fg := color.RGBA{0xe6, 0xe8, 0xeb, 255}
	maxChars := (w - 16) / 6 / 2
	y := h + 8
	for _, l := range legend {
		for len([]rune(l)) > maxChars && maxChars > 10 { // coupe proprement
			cut := maxChars
			rs := []rune(l)
			if i := strings.LastIndex(string(rs[:cut]), " "); i > 10 {
				cut = len([]rune(string(rs[:cut])[:i]))
			}
			drawText(out, 8, y, 2, fg, string(rs[:cut]))
			l = "  " + strings.TrimSpace(string(rs[cut:]))
			y += 16
		}
		drawText(out, 8, y, 2, fg, l)
		y += 16
		if y > h+legendH-14 {
			break
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// renderIso : vue isométrique orthographique, Lambert, z-buffer logiciel, suréchantillonnage 2x.
func renderIso(m *mesh.Mesh, name, sha string, w, h int) *PreviewOutput {
	const yaw, elev = 45.0, 35.264 // isométrique vraie
	cy, sy := math.Cos(yaw*math.Pi/180), math.Sin(yaw*math.Pi/180)
	ce, se := math.Cos(elev*math.Pi/180), math.Sin(elev*math.Pi/180)
	bb := m.Bounds()
	ctr := bb.Min.Add(bb.Size.Scale(0.5))
	type pv struct{ x, y, d float64 }
	proj := make([]pv, len(m.V))
	minX, maxX, minY, maxY := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
	for i, v := range m.V {
		x, y, z := v.X-ctr.X, v.Y-ctr.Y, v.Z-ctr.Z
		x1, y1 := x*cy-y*sy, x*sy+y*cy // lacet autour de Z
		p := pv{x: x1, y: y1*se + z*ce, d: -y1*ce + z*se}
		proj[i] = p
		minX, maxX, minY, maxY = math.Min(minX, p.x), math.Max(maxX, p.x), math.Min(minY, p.y), math.Max(maxY, p.y)
	}
	W, H := w*2, h*2
	cv := newCanvas(W, H, color.RGBA{0xf3, 0xf1, 0xed, 255})
	if len(m.V) > 0 {
		sc := math.Min(float64(W)*0.86/math.Max(maxX-minX, 1e-9), float64(H)*0.86/math.Max(maxY-minY, 1e-9))
		ox, oy := float64(W)/2-(minX+maxX)/2*sc, float64(H)/2+(minY+maxY)/2*sc
		light := [3]float64{-0.4, 0.5, 0.77}
		ln := math.Sqrt(light[0]*light[0] + light[1]*light[1] + light[2]*light[2])
		base := [3]float64{0x4a, 0x6f, 0xa5}
		for ti, t := range m.T {
			a, b, c := proj[t[0]], proj[t[1]], proj[t[2]]
			n, _ := m.Normal(ti)
			// normale dans le repère caméra
			nx, ny, nz := n.X*cy-n.Y*sy, n.X*sy+n.Y*cy, n.Z
			cam := [3]float64{nx, ny*se + nz*ce, -ny*ce + nz*se}
			if cam[2] < -0.02 { // face arrière
				continue
			}
			lam := math.Max(0, (cam[0]*light[0]+cam[1]*light[1]+cam[2]*light[2])/ln)
			k := 0.32 + 0.68*lam
			col := color.RGBA{uint8(base[0] * k), uint8(base[1] * k), uint8(base[2] * k), 255}
			cv.tri(a.x*sc+ox, oy-a.y*sc, a.d, b.x*sc+ox, oy-b.y*sc, b.d, c.x*sc+ox, oy-c.y*sc, c.d, col)
		}
	}
	img := cv.downsample2()
	legend := []string{
		"Apercu maillage - " + name,
		fmt.Sprintf("Boite englobante %.1f x %.1f x %.1f mm, %d triangles", bb.Size.X, bb.Size.Y, bb.Size.Z, len(m.T)),
		"Projection orthographique isometrique (lacet 45 deg, elevation 35.3 deg), eclairage Lambert, z-buffer logiciel 2x",
		"Representation de la geometrie seule: ni materiau, ni couleur, ni supports, ni trajectoires",
	}
	b, _ := finish(img, legend)
	return &PreviewOutput{Kind: "mesh_iso", Width: w, Height: h + legendH, Legend: legend, PNG: b,
		Represented:    []string{"surface triangulée du modèle", "volumes relatifs sous éclairage directionnel fixe"},
		NotRepresented: []string{"matériau et couleur réels", "supports et jupe", "trajectoires d'impression", "défauts de maillage (non surlignés)"},
		Render: map[string]any{"projection": "orthographique isométrique", "yaw_deg": yaw, "elevation_deg": elev, "lighting": "Lambert, 1 source", "supersampling": 2,
			"triangles": len(m.T), "source_sha256": sha, "bbox_mm": []float64{bb.Size.X, bb.Size.Y, bb.Size.Z}}}
}

// ---- aperçu de couche (G-code) ----

type seg struct {
	x0, y0, x1, y1 float64
	feat           int
}

func parseCoord(f string, key byte) (float64, bool) {
	for _, tok := range strings.Fields(f) {
		if tok[0] == key && len(tok) > 1 {
			if v, err := strconv.ParseFloat(tok[1:], 64); err == nil {
				return v, true
			}
		}
	}
	return 0, false
}

func renderLayer(archive, sha string, plate, layer, w, h int, bed *contracts.Dimensions) (*PreviewOutput, error) {
	if plate <= 0 {
		plate = 1
	}
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return nil, invalidf("archive illisible : %v", err)
	}
	defer zr.Close()
	entry := fmt.Sprintf("Metadata/plate_%d.gcode", plate)
	var zf *zip.File
	for _, f := range zr.File {
		if f.Name == entry {
			zf = f
		}
	}
	if zf == nil {
		return nil, fmt.Errorf("%w : %s absent de l'archive", ErrNotFound, entry)
	}
	scan := func(want int, collect bool) (total int, segs []seg, z float64, feats []string, err error) {
		rc, err := zf.Open()
		if err != nil {
			return 0, nil, 0, nil, err
		}
		defer rc.Close()
		br := bufio.NewReaderSize(rc, 1<<20)
		cur, abs, absE := 0, true, false
		var x, y, e, curZ float64
		feat := -1
		featIdx := map[string]int{}
		for {
			line, rerr := br.ReadString('\n')
			if len(line) > 4096 { // lignes de configuration énormes
				line = line[:4096]
			}
			l := strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(l, "; CHANGE_LAYER"):
				cur++
			case strings.HasPrefix(l, "; Z_HEIGHT:") && cur == want:
				z, _ = strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(l, "; Z_HEIGHT:")), 64)
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
			case strings.HasPrefix(l, "G1 ") || strings.HasPrefix(l, "G2 ") || strings.HasPrefix(l, "G3 "):
				if i := strings.IndexByte(l, ';'); i >= 0 {
					l = l[:i]
				}
				nx, hx := parseCoord(l, 'X')
				ny, hy := parseCoord(l, 'Y')
				ne, he := parseCoord(l, 'E')
				if zz, ok := parseCoord(l, 'Z'); ok && abs {
					curZ = zz
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
				if extr && (hx || hy) && cur == want && collect {
					segs = append(segs, seg{px, py, x, y, feat})
				}
			}
			if rerr != nil {
				break
			}
		}
		_ = curZ
		return cur, segs, z, feats, nil
	}
	total, _, _, _, err := scan(-1, false)
	if err != nil {
		return nil, err
	}
	if total == 0 {
		return nil, invalidf("aucun marqueur CHANGE_LAYER dans %s", entry)
	}
	if layer <= 0 {
		layer = (total + 1) / 2
	}
	if layer > total {
		return nil, invalidf("couche %d hors plage (1..%d)", layer, total)
	}
	_, segs, z, feats, err := scan(layer, true)
	if err != nil {
		return nil, err
	}
	W, H := w*2, h*2
	cv := newCanvas(W, H, color.RGBA{0xf3, 0xf1, 0xed, 255})
	minX, maxX, minY, maxY := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
	for _, sgm := range segs {
		for _, p := range [][2]float64{{sgm.x0, sgm.y0}, {sgm.x1, sgm.y1}} {
			minX, maxX, minY, maxY = math.Min(minX, p[0]), math.Max(maxX, p[0]), math.Min(minY, p[1]), math.Max(maxY, p[1])
		}
	}
	if bed != nil && bed.X > 0 && bed.Y > 0 {
		minX, minY, maxX, maxY = math.Min(minX, 0), math.Min(minY, 0), math.Max(maxX, bed.X), math.Max(maxY, bed.Y)
	}
	if math.IsInf(minX, 1) {
		minX, maxX, minY, maxY = 0, 100, 0, 100
	}
	sc := math.Min(float64(W)*0.9/math.Max(maxX-minX, 1), float64(H)*0.9/math.Max(maxY-minY, 1))
	px := func(x float64) float64 { return float64(W)/2 + (x-(minX+maxX)/2)*sc }
	py := func(y float64) float64 { return float64(H)/2 - (y-(minY+maxY)/2)*sc }
	grid := color.RGBA{0xdd, 0xda, 0xd3, 255}
	for g := math.Floor(minX/10) * 10; g <= maxX; g += 10 {
		cv.line(px(g), py(minY), px(g), py(maxY), grid, 1)
	}
	for g := math.Floor(minY/10) * 10; g <= maxY; g += 10 {
		cv.line(px(minX), py(g), px(maxX), py(g), grid, 1)
	}
	if bed != nil && bed.X > 0 && bed.Y > 0 {
		bc := color.RGBA{0x55, 0x55, 0x55, 255}
		cv.line(px(0), py(0), px(bed.X), py(0), bc, 3)
		cv.line(px(bed.X), py(0), px(bed.X), py(bed.Y), bc, 3)
		cv.line(px(bed.X), py(bed.Y), px(0), py(bed.Y), bc, 3)
		cv.line(px(0), py(bed.Y), px(0), py(0), bc, 3)
	}
	used := map[int]bool{}
	for _, sgm := range segs {
		col := color.RGBA{0x33, 0x33, 0x33, 255}
		if sgm.feat >= 0 {
			col = palette[sgm.feat%len(palette)]
			used[sgm.feat] = true
		}
		cv.line(px(sgm.x0), py(sgm.y0), px(sgm.x1), py(sgm.y1), col, 3)
	}
	img := cv.downsample2()
	legend := []string{
		fmt.Sprintf("Apercu de couche - plaque %d, couche %d sur %d%s", plate, layer, total, zText(z)),
		fmt.Sprintf("%d segments extrudes ; grille 10 mm ; echelle %.2f px/mm", len(segs), sc/2),
	}
	var fl []string
	for i, f := range feats {
		if used[i] {
			fl = append(fl, fmt.Sprintf("%s=#%02X%02X%02X", f, palette[i%len(palette)].R, palette[i%len(palette)].G, palette[i%len(palette)].B))
		}
	}
	if len(fl) > 0 {
		legend = append(legend, "Types: "+strings.Join(fl, " ; "))
	}
	legend = append(legend, "Trajectoires extrudees seulement (G1 avec E>0, arcs ramenes a leur corde): ni deplacements a vide, ni retractions, ni duree")
	b, err := finish(img, legend)
	if err != nil {
		return nil, err
	}
	return &PreviewOutput{Kind: "gcode_layer", Width: w, Height: h + legendH, Legend: legend, PNG: b,
		Represented:    []string{"segments d'extrusion de la couche choisie, colorés par type (FEATURE)", "contour du plateau si l'imprimante est connue"},
		NotRepresented: []string{"déplacements à vide", "rétractions et purges", "épaisseur réelle des filets", "couches voisines", "temps"},
		Render:         map[string]any{"plate": plate, "layer": layer, "layers_total": total, "z_height_mm": z, "segments": len(segs), "supersampling": 2, "artifact_sha256": sha, "px_per_mm": sc / 2}}, nil
}

func zText(z float64) string {
	if z <= 0 {
		return ""
	}
	return fmt.Sprintf(", Z=%.2f mm", z)
}
