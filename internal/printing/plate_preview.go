package printing

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// Aperçu du PLATEAU (capture_preview mode « plate ») : vue de dessus du plateau réel de l'imprimante (dimensions
// issues des profils Bambu, grille 10 mm, zones interdites) avec les pièces à leur position et orientation
// réelles — lues dans le plan, ou dans le 3MF d'une plaque découpée.

type drawObj struct {
	name     string
	m        *mesh.Mesh // coordonnées de la plaque
	legend   string
	settings map[string]string
}

func fmtSettings(m map[string]string) string {
	if len(m) == 0 {
		return "réglages du profil"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var p []string
	for _, k := range keys {
		p = append(p, k+"="+m[k])
	}
	return strings.Join(p, " ")
}

// renderPlate dessine une plaque. Le PNG fait environ w pixels de large ; la légende liste chaque pièce numérotée.
func renderPlate(bed PlateBed, title string, objs []drawObj, extra []string, w int) (*PreviewOutput, error) {
	const margin = 34
	if w < 400 {
		w = 400
	}
	sc := float64(w-2*margin) / bed.X // px par mm (échelle finale)
	bw, bh := int(bed.X*sc), int(bed.Y*sc)
	W, H := (bw+2*margin)*2, (bh+2*margin)*2
	cv := newCanvas(W, H, color.RGBA{0xf3, 0xf1, 0xed, 255})
	s2 := sc * 2
	ox, oy := float64(margin*2), float64(margin*2+bh*2) // origine (0,0) du plateau, Y vers le haut
	px := func(x float64) float64 { return ox + x*s2 }
	py := func(y float64) float64 { return oy - y*s2 }
	// plateau
	for y := 0; y < bh*2; y++ {
		for x := 0; x < bw*2; x++ {
			cv.pix[(margin*2+y)*W+margin*2+x] = color.RGBA{0xfb, 0xfa, 0xf7, 255}
		}
	}
	grid, grid50 := color.RGBA{0xe4, 0xe1, 0xda, 255}, color.RGBA{0xc9, 0xc5, 0xbb, 255}
	for g := 0.0; g <= bed.X+1e-9; g += 10 {
		c := grid
		if math.Mod(g, 50) == 0 {
			c = grid50
		}
		cv.line(px(g), py(0), px(g), py(bed.Y), c, 1)
	}
	for g := 0.0; g <= bed.Y+1e-9; g += 10 {
		c := grid
		if math.Mod(g, 50) == 0 {
			c = grid50
		}
		cv.line(px(0), py(g), px(bed.X), py(g), c, 1)
	}
	red := color.RGBA{0xc2, 0x3b, 0x3b, 255}
	for _, e := range bed.Exclusions {
		x0, y0, x1, y1 := e[0], e[1], e[2], e[3]
		cv.line(px(x0), py(y0), px(x1), py(y0), red, 3)
		cv.line(px(x1), py(y0), px(x1), py(y1), red, 3)
		cv.line(px(x1), py(y1), px(x0), py(y1), red, 3)
		cv.line(px(x0), py(y1), px(x0), py(y0), red, 3)
		for d := -(y1 - y0); d < x1-x0; d += 6 { // hachures
			ax, ay := x0+math.Max(d, 0), y0+math.Max(-d, 0)
			bx, by := math.Min(x0+d+(y1-y0), x1), math.Min(y1, y0+(x1-x0)-d)
			cv.line(px(ax), py(ay), px(bx), py(by), red, 1)
		}
	}
	cv.line(px(0), py(0), px(bed.X), py(0), color.RGBA{0x44, 0x44, 0x44, 255}, 4)
	cv.line(px(bed.X), py(0), px(bed.X), py(bed.Y), color.RGBA{0x44, 0x44, 0x44, 255}, 4)
	cv.line(px(bed.X), py(bed.Y), px(0), py(bed.Y), color.RGBA{0x44, 0x44, 0x44, 255}, 4)
	cv.line(px(0), py(bed.Y), px(0), py(0), color.RGBA{0x44, 0x44, 0x44, 255}, 4)
	// pièces, vue de dessus, éclairage doux
	type lab struct {
		x, y float64
		n    int
	}
	var labs []lab
	for i, o := range objs {
		base := palette[i%len(palette)]
		for ti, t := range o.m.T {
			a, b, c := o.m.V[t[0]], o.m.V[t[1]], o.m.V[t[2]]
			n, _ := o.m.Normal(ti)
			if n.Z < -0.02 {
				continue
			}
			k := 0.62 + 0.38*math.Max(n.Z, 0)
			col := color.RGBA{uint8(float64(base.R) * k), uint8(float64(base.G) * k), uint8(float64(base.B) * k), 255}
			cv.tri(px(a.X), py(a.Y), a.Z, px(b.X), py(b.Y), b.Z, px(c.X), py(c.Y), c.Z, col)
		}
		bb := o.m.Bounds()
		labs = append(labs, lab{(bb.Min.X + bb.Max.X) / 2, (bb.Min.Y + bb.Max.Y) / 2, i + 1})
	}
	img := cv.downsample2()
	// repères d'axes tous les 50 mm
	axis := color.RGBA{0x55, 0x55, 0x55, 255}
	for g := 0.0; g <= bed.X+1e-9; g += 50 {
		drawText(img, margin+int(g*sc)-8, margin+bh+6, 1, axis, strconv.Itoa(int(g)))
	}
	for g := 50.0; g <= bed.Y+1e-9; g += 50 {
		drawText(img, 2, margin+bh-int(g*sc)-3, 1, axis, strconv.Itoa(int(g)))
	}
	for _, l := range labs { // numéro de pièce, lisible sur toutes les couleurs
		cx, cy := margin+int(l.x*sc), margin+bh-int(l.y*sc)
		txt := strconv.Itoa(l.n)
		tw := textWidth(txt, 2)
		for dx := -1; dx <= 1; dx++ {
			for dy := -1; dy <= 1; dy++ {
				drawText(img, cx-tw/2+dx, cy-7+dy, 2, color.RGBA{0, 0, 0, 255}, txt)
			}
		}
		drawText(img, cx-tw/2, cy-7, 2, color.RGBA{255, 255, 255, 255}, txt)
	}
	// légende
	lines := []string{title}
	for i, o := range objs {
		lines = append(lines, fmt.Sprintf("%d  %s - %s", i+1, o.name, o.legend))
	}
	lines = append(lines, extra...)
	lines = append(lines, fmt.Sprintf("Plateau %s x %s mm, grille 10 mm (traits forts tous les 50 mm), vue de dessus", trimF(bed.X), trimF(bed.Y)))
	lines = append(lines, plateFootprintCaveat)
	maxChars := (img.Bounds().Dx() - 24) / 12
	var wrapped []struct {
		t   string
		idx int
	}
	for li, l := range lines {
		idx := -1
		if li >= 1 && li <= len(objs) {
			idx = li - 1
		}
		first := true
		for len([]rune(l)) > maxChars {
			rs := []rune(l)
			cut := maxChars
			if i := strings.LastIndex(string(rs[:cut]), " "); i > 10 {
				cut = len([]rune(string(rs[:cut])[:i]))
			}
			wrapped = append(wrapped, struct {
				t   string
				idx int
			}{string(rs[:cut]), map[bool]int{true: idx, false: -1}[first]})
			l, first = "    "+strings.TrimSpace(string(rs[cut:])), false
		}
		wrapped = append(wrapped, struct {
			t   string
			idx int
		}{l, map[bool]int{true: idx, false: -1}[first]})
	}
	const lineH = 18
	panel := len(wrapped)*lineH + 16
	out := image.NewRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()+panel))
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			out.SetRGBA(x, y, img.RGBAAt(x, y))
		}
	}
	for y := img.Bounds().Dy(); y < out.Bounds().Dy(); y++ {
		for x := 0; x < out.Bounds().Dx(); x++ {
			out.SetRGBA(x, y, color.RGBA{0x17, 0x1a, 0x1f, 255})
		}
	}
	y := img.Bounds().Dy() + 8
	for _, wl := range wrapped {
		x := 10
		if wl.idx >= 0 {
			c := palette[wl.idx%len(palette)]
			for dy := 0; dy < 12; dy++ {
				for dx := 0; dx < 12; dx++ {
					out.SetRGBA(x+dx, y+1+dy, c)
				}
			}
		}
		drawText(out, x+16, y, 2, color.RGBA{0xe6, 0xe8, 0xeb, 255}, wl.t)
		y += lineH
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return nil, err
	}
	return &PreviewOutput{Kind: "plate", Width: out.Bounds().Dx(), Height: out.Bounds().Dy(), Legend: lines, PNG: buf.Bytes(),
		Represented:    []string{"plateau réel de l'imprimante (profil machine), grille 10 mm, zones interdites", "EMPRISE PROJETÉE vue de dessus et orientation de chaque pièce (silhouette), pas sa surface de contact", "numéro de pièce = ligne de la légende"},
		NotRepresented: []string{"la surface réellement en contact avec le plateau (pieds, creux, évidements : voir le mode first_layer)", "jupe, bordure (brim) et tour d'essuyage", "supports", "trajectoires d'impression", "temps par pièce (le temps est celui de la plaque entière)"},
		Render:         map[string]any{"bed_mm": []float64{bed.X, bed.Y}, "px_per_mm": sc, "objects": len(objs), "grid_mm": 10, "exclusions": len(bed.Exclusions)}}, nil
}

// plateFootprintCaveat : légende obligatoire du mode plate (retour terrain : l'emprise était prise pour la surface de contact).
const plateFootprintCaveat = "ATTENTION : emprise projetee vue de dessus, PAS la surface en contact avec le plateau (une piece sur pieds touche peu). Surface de contact reelle : mode first_layer."

func trimF(v float64) string { return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64) }

// ---- sources de l'aperçu ----

func (s *Service) previewPlanPlate(ctx context.Context, planID string, plate, width int) (*PreviewOutput, error) {
	plan, err := s.GetPlatePlan(ctx, planID)
	if err != nil {
		return nil, err
	}
	if plate == 0 {
		plate = 1
	}
	if plate < 1 || plate > max(len(plan.Plates), 1) {
		return nil, invalidf("plaque %d hors plan (1 à %d)", plate, len(plan.Plates))
	}
	var idx []int
	for i, o := range plan.Objects {
		if o.Plate == plate && o.Fits {
			idx = append(idx, i)
		}
	}
	ms, err := s.planMeshes(plan, idx)
	if err != nil {
		return nil, err
	}
	var objs []drawObj
	for _, po := range s.projectObjects(plan, idx, ms) {
		o := plan.Objects[idx[len(objs)]]
		bb := po.Mesh.Bounds()
		m := po.Mesh.Transform(mesh.Translation(po.X-bb.Min.X, po.Y-bb.Min.Y, -bb.Min.Z))
		objs = append(objs, drawObj{name: o.Name, m: m, legend: fmt.Sprintf("%s x %s x %s mm ; %s", trimF(o.W), trimF(o.D), trimF(o.H), fmtSettings(o.Settings))})
	}
	var extra []string
	if len(plan.Plates) > 0 {
		p := plan.Plates[plate-1]
		extra = append(extra, fmt.Sprintf("Plaque %d sur %d : %d pièce(s), empreinte %.0f %% du plateau. Plan %s, %s.", plate, len(plan.Plates), len(p.Objects), p.UsagePercent, plan.ID,
			map[bool]string{true: "TIENT", false: "NE TIENT PAS"}[plan.Fits]))
	}
	for _, o := range plan.Objects {
		if !o.Fits {
			extra = append(extra, "NE TIENT PAS : "+o.Name+" - "+o.Problem)
		}
	}
	out, err := renderPlate(plan.Bed, fmt.Sprintf("Plateau %s - %s (plan, avant découpe)", plan.PrinterModel, plan.Name), objs, extra, width)
	if err == nil {
		out.Render["plan_id"], out.Render["plate"], out.Render["source"] = plan.ID, plate, "plan"
	}
	return out, err
}

type msObject struct {
	ID   string `xml:"id,attr"`
	Meta []struct {
		Key   string `xml:"key,attr"`
		Value string `xml:"value,attr"`
	} `xml:"metadata"`
}

// archivePlateObjects lit les pièces d'une plaque dans le 3MF découpé (coordonnées de la plaque, comme le G-code).
func archivePlateObjects(path string, plate int, bed PlateBed) ([]drawObj, error) {
	if plate <= 0 {
		plate = 1
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	md, err := mesh.ParseThreeMF(data)
	if err != nil {
		return nil, invalidf("archive illisible : %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Objects []msObject `xml:"object"`
		Plates  []struct {
			Meta []struct {
				Key   string `xml:"key,attr"`
				Value string `xml:"value,attr"`
			} `xml:"metadata"`
			Inst []struct {
				Meta []struct {
					Key   string `xml:"key,attr"`
					Value string `xml:"value,attr"`
				} `xml:"metadata"`
			} `xml:"model_instance"`
		} `xml:"plate"`
	}
	for _, f := range zr.File {
		if f.Name == "Metadata/model_settings.config" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			b, _ := io.ReadAll(io.LimitReader(rc, 64<<20))
			rc.Close()
			if err := xml.Unmarshal(b, &cfg); err != nil {
				return nil, invalidf("model_settings.config illisible : %v", err)
			}
		}
	}
	names, settings := map[string]string{}, map[string]map[string]string{}
	for _, o := range cfg.Objects {
		settings[o.ID] = map[string]string{}
		for _, m := range o.Meta {
			switch m.Key {
			case "name":
				names[o.ID] = m.Value
			case "extruder", "":
			default:
				settings[o.ID][m.Key] = m.Value
			}
		}
	}
	onPlate := map[string]bool{}
	nPlates := len(cfg.Plates)
	for _, p := range cfg.Plates {
		idx := 0
		for _, m := range p.Meta {
			if m.Key == "plater_id" {
				idx, _ = strconv.Atoi(m.Value)
			}
		}
		if idx != plate {
			continue
		}
		for _, in := range p.Inst {
			for _, m := range in.Meta {
				if m.Key == "object_id" {
					onPlate[m.Value] = true
				}
			}
		}
	}
	origin := slicer.PlateOrigins(bed.X, bed.Y, max(nPlates, 1))
	ox, oy := origin(plate)
	use := map[string]slicer.ObjectUse{}
	if rep, err := slicer.ObjectReport(path, plate); err == nil {
		for _, r := range rep {
			use[r.Name] = r
		}
	}
	var objs []drawObj
	var total float64
	for _, r := range use {
		total += r.Grams
	}
	for _, o := range md.Objects {
		if !onPlate[o.ID] {
			continue
		}
		nm := names[o.ID]
		if nm == "" {
			nm = o.Name
		}
		m := o.Mesh.Transform(mesh.Translation(-ox, -oy, 0))
		leg := fmt.Sprintf("%s x %s x %s mm ; %s", trimF(m.Bounds().Size.X), trimF(m.Bounds().Size.Y), trimF(m.Bounds().Size.Z), fmtSettings(settings[o.ID]))
		if u, ok := use[nm]; ok {
			share := 0.0
			if total > 0 {
				share = u.Grams / total * 100
			}
			leg += fmt.Sprintf(" ; %.1f g (%.0f %%), %d couches", u.Grams, share, u.Layers)
		}
		objs = append(objs, drawObj{name: nm, m: m, legend: leg})
	}
	if len(objs) == 0 {
		return nil, fmt.Errorf("%w : aucune pièce sur la plaque %d de l'archive", ErrNotFound, plate)
	}
	sort.SliceStable(objs, func(a, b int) bool { return objs[a].name < objs[b].name })
	return objs, nil
}

// previewArchivePlate lit les pièces dans le 3MF d'une plaque découpée et dessine le plateau.
func (s *Service) previewArchivePlate(ctx context.Context, path, sha string, plate int, bed PlateBed, sum *ArtifactSummary, model string, width int) (*PreviewOutput, error) {
	if plate <= 0 {
		plate = 1
	}
	objs, err := archivePlateObjects(path, plate, bed)
	if err != nil {
		return nil, err
	}
	var extra []string
	if sum != nil {
		for _, p := range sum.Plates {
			if p.Index == plate {
				extra = append(extra, fmt.Sprintf("Plaque %d découpée : %s, %.1f g de filament, %d couches (temps de la plaque entière, pas par pièce).", plate, durText(p.PredictedSeconds), p.WeightGrams, p.Layers))
			}
		}
	}
	extra = append(extra, "Artefact "+shortSHA(sha)+" - pièces lues dans le 3MF découpé (position et orientation réelles).")
	title := fmt.Sprintf("Plateau %s - plaque %d (découpée)", model, plate)
	out, err := renderPlate(bed, title, objs, extra, width)
	if err == nil {
		out.Render["artifact_sha256"], out.Render["plate"], out.Render["source"] = sha, plate, "archive"
	}
	return out, err
}

func durText(sec float64) string {
	s := int(math.Round(sec))
	if s >= 3600 {
		return fmt.Sprintf("%d h %02d min", s/3600, s%3600/60)
	}
	return fmt.Sprintf("%d min %02d s", s/60, s%60)
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
