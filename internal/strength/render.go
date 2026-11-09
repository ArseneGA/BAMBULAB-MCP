package strength

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// Rendus PNG en Go pur (z-buffer logiciel, suréchantillonnage 2x, aucun GPU).

const (
	panelW  = 560
	panelH  = 430
	headerH = 34
	ss      = 2
)

var (
	bgPanel  = color.RGBA{0xf3, 0xf1, 0xed, 255}
	bgDark   = color.RGBA{0x17, 0x1a, 0x1f, 255}
	fgLight  = color.RGBA{0xe6, 0xe8, 0xeb, 255}
	colLoad  = color.RGBA{0xd6, 0x00, 0x8f, 255}
	colSupp  = color.RGBA{0x00, 0x7a, 0x3d, 255}
	colMax   = color.RGBA{0x10, 0x10, 0x10, 255}
	colGreen = color.RGBA{0x4c, 0xc3, 0x6b, 255}
	colAmber = color.RGBA{0xf0, 0xa8, 0x30, 255}
	colRed   = color.RGBA{0xf0, 0x55, 0x4c, 255}
)

var zonePalette = []color.RGBA{{0xd6, 0x28, 0x28, 255}, {0x1f, 0x77, 0xb4, 255}, {0x2c, 0xa0, 0x2c, 255}, {0xff, 0x7f, 0x0e, 255},
	{0x94, 0x67, 0xbd, 255}, {0x8c, 0x56, 0x4b, 255}, {0xe3, 0x77, 0xc2, 255}, {0x17, 0xbe, 0xcf, 255},
	{0xbc, 0xbd, 0x22, 255}, {0x7f, 0x7f, 0x7f, 255}, {0x39, 0x3b, 0x79, 255}, {0xad, 0x49, 0x4a, 255}}

// ---- caméra et rastérisation ----

type camera struct {
	cy, sy, ce, se float64
	ctr            mesh.Vec3
	sc, ox, oy     float64
	w, h           int
}

func newCamera(m *mesh.Mesh, yaw, elev float64, w, h int) camera {
	c := camera{cy: math.Cos(yaw * math.Pi / 180), sy: math.Sin(yaw * math.Pi / 180), ce: math.Cos(elev * math.Pi / 180), se: math.Sin(elev * math.Pi / 180), w: w, h: h}
	bb := m.Bounds()
	c.ctr = bb.Min.Add(bb.Size.Scale(0.5))
	minX, maxX, minY, maxY := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
	for _, v := range m.V {
		x, y, _ := c.proj(v)
		minX, maxX, minY, maxY = math.Min(minX, x), math.Max(maxX, x), math.Min(minY, y), math.Max(maxY, y)
	}
	c.sc = math.Min(float64(w)*0.80/math.Max(maxX-minX, 1e-9), float64(h)*0.80/math.Max(maxY-minY, 1e-9))
	c.ox, c.oy = float64(w)/2-(minX+maxX)/2*c.sc, float64(h)/2+(minY+maxY)/2*c.sc
	return c
}

// proj : (x, y, profondeur) avant mise à l'échelle.
func (c camera) proj(v mesh.Vec3) (x, y, d float64) {
	px, py, pz := v.X-c.ctr.X, v.Y-c.ctr.Y, v.Z-c.ctr.Z
	x1, y1 := px*c.cy-py*c.sy, px*c.sy+py*c.cy
	return x1, y1*c.se + pz*c.ce, -y1*c.ce + pz*c.se
}

// pix : position en pixels (origine en haut à gauche, à la taille w×h de la caméra) et profondeur.
func (c camera) pix(v mesh.Vec3) (float64, float64, float64) {
	x, y, d := c.proj(v)
	return x*c.sc + c.ox, c.oy - y*c.sc, d
}

type raster struct {
	w, h  int
	pix   []color.RGBA
	depth []float64
}

func newRaster(w, h int, bg color.RGBA) *raster {
	r := &raster{w: w, h: h, pix: make([]color.RGBA, w*h), depth: make([]float64, w*h)}
	for i := range r.pix {
		r.pix[i] = bg
		r.depth[i] = math.Inf(-1)
	}
	return r
}

type rv struct{ x, y, d float64 }

func (r *raster) tri(a, b, c rv, ca, cb, cc [3]float64) {
	minX := int(math.Floor(math.Min(a.x, math.Min(b.x, c.x))))
	maxX := int(math.Ceil(math.Max(a.x, math.Max(b.x, c.x))))
	minY := int(math.Floor(math.Min(a.y, math.Min(b.y, c.y))))
	maxY := int(math.Ceil(math.Max(a.y, math.Max(b.y, c.y))))
	if minX < 0 {
		minX = 0
	}
	if minY < 0 {
		minY = 0
	}
	if maxX >= r.w {
		maxX = r.w - 1
	}
	if maxY >= r.h {
		maxY = r.h - 1
	}
	den := (b.y-c.y)*(a.x-c.x) + (c.x-b.x)*(a.y-c.y)
	if math.Abs(den) < 1e-12 {
		return
	}
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			u := ((b.y-c.y)*(px-c.x) + (c.x-b.x)*(py-c.y)) / den
			v := ((c.y-a.y)*(px-c.x) + (a.x-c.x)*(py-c.y)) / den
			w := 1 - u - v
			if u < -1e-9 || v < -1e-9 || w < -1e-9 {
				continue
			}
			d := u*a.d + v*b.d + w*c.d
			i := y*r.w + x
			if d > r.depth[i] {
				r.depth[i] = d
				r.pix[i] = color.RGBA{clamp8(u*ca[0] + v*cb[0] + w*cc[0]), clamp8(u*ca[1] + v*cb[1] + w*cc[1]), clamp8(u*ca[2] + v*cb[2] + w*cc[2]), 255}
			}
		}
	}
}

func clamp8(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func (r *raster) down() *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, r.w/ss, r.h/ss))
	for y := 0; y < r.h/ss; y++ {
		for x := 0; x < r.w/ss; x++ {
			var sr, sg, sb int
			for dy := 0; dy < ss; dy++ {
				for dx := 0; dx < ss; dx++ {
					p := r.pix[(y*ss+dy)*r.w+x*ss+dx]
					sr, sg, sb = sr+int(p.R), sg+int(p.G), sb+int(p.B)
				}
			}
			n := ss * ss
			out.SetRGBA(x, y, color.RGBA{uint8(sr / n), uint8(sg / n), uint8(sb / n), 255})
		}
	}
	return out
}

// visible : le point est-il devant la surface rendue (non masqué) ?
func (r *raster) visible(c camera, p mesh.Vec3, tol float64) bool {
	x, y, d := c.pix(p)
	ix, iy := int(x), int(y) // c est la caméra du tampon (déjà à l'échelle du suréchantillonnage)
	if ix < 0 || iy < 0 || ix >= r.w || iy >= r.h {
		return false
	}
	return d >= r.depth[iy*r.w+ix]-tol
}

// drawMesh : rend le maillage ; colorOf(vertex index, triangle index) donne la couleur de base du sommet.
func drawMesh(m *mesh.Mesh, c camera, vcol func(v int, t int) [3]float64) (*raster, camera) {
	big := camera{cy: c.cy, sy: c.sy, ce: c.ce, se: c.se, ctr: c.ctr, sc: c.sc * ss, ox: c.ox * ss, oy: c.oy * ss, w: c.w * ss, h: c.h * ss}
	r := newRaster(big.w, big.h, bgPanel)
	pv := make([]rv, len(m.V))
	for i, v := range m.V {
		x, y, d := big.pix(v)
		pv[i] = rv{x, y, d}
	}
	light := mesh.Vec3{X: -0.4, Y: 0.5, Z: 0.77}.Norm()
	for ti, t := range m.T {
		n, _ := m.Normal(ti)
		cam := mesh.Vec3{X: n.X*c.cy - n.Y*c.sy, Y: (n.X*c.sy+n.Y*c.cy)*c.se + n.Z*c.ce, Z: -(n.X*c.sy+n.Y*c.cy)*c.ce + n.Z*c.se}
		if cam.Z < -0.02 {
			continue
		}
		lam := math.Max(0, cam.Dot(light))
		k := 0.62 + 0.38*lam
		var cs [3][3]float64
		for j := 0; j < 3; j++ {
			base := vcol(t[j], ti)
			cs[j] = [3]float64{base[0] * k, base[1] * k, base[2] * k}
		}
		r.tri(pv[t[0]], pv[t[1]], pv[t[2]], cs[0], cs[1], cs[2])
	}
	return r, big
}

// ---- primitives sur l'image finale ----

func setPx(img *image.RGBA, x, y int, c color.RGBA) {
	if image.Pt(x, y).In(img.Rect) {
		img.SetRGBA(x, y, c)
	}
}

func fillCircle(img *image.RGBA, cx, cy, r int, c color.RGBA) {
	for y := -r; y <= r; y++ {
		for x := -r; x <= r; x++ {
			if x*x+y*y <= r*r {
				setPx(img, cx+x, cy+y, c)
			}
		}
	}
}

func ring(img *image.RGBA, cx, cy, r int, c color.RGBA) {
	for y := -r - 1; y <= r+1; y++ {
		for x := -r - 1; x <= r+1; x++ {
			d := x*x + y*y
			if d <= (r+1)*(r+1) && d >= (r-1)*(r-1) {
				setPx(img, cx+x, cy+y, c)
			}
		}
	}
}

func thickLine(img *image.RGBA, x0, y0, x1, y1 float64, c color.RGBA, th int) {
	n := int(math.Max(math.Abs(x1-x0), math.Abs(y1-y0))) + 1
	for i := 0; i <= n; i++ {
		t := float64(i) / float64(n)
		cx, cy := int(x0+(x1-x0)*t), int(y0+(y1-y0)*t)
		for ox := -th / 2; ox <= th/2; ox++ {
			for oy := -th / 2; oy <= th/2; oy++ {
				setPx(img, cx+ox, cy+oy, c)
			}
		}
	}
}

func arrow(img *image.RGBA, x0, y0, x1, y1 float64, c color.RGBA) {
	thickLine(img, x0-1, y0-1, x1-1, y1-1, color.RGBA{255, 255, 255, 255}, 5)
	thickLine(img, x0, y0, x1, y1, c, 3)
	ang := math.Atan2(y1-y0, x1-x0)
	for _, s := range []float64{0.5, -0.5} {
		thickLine(img, x1, y1, x1-16*math.Cos(ang+s), y1-16*math.Sin(ang+s), c, 3)
	}
}

func triMarker(img *image.RGBA, cx, cy, s int, c color.RGBA) {
	for y := 0; y <= s; y++ {
		half := y * 6 / 10
		for x := -half; x <= half; x++ {
			setPx(img, cx+x, cy+y, c)
		}
	}
}

func text(img *image.RGBA, x, y, scale int, c color.RGBA, s string) {
	printing.DrawText(img, x, y, scale, c, s)
}

// labelled : texte sur fond blanc translucide pour rester lisible sur le rendu.
func labelled(img *image.RGBA, x, y int, c color.RGBA, s string) {
	w := printing.TextWidth(s, 2)
	if x+w+6 > img.Rect.Dx() {
		x = img.Rect.Dx() - w - 6
	}
	if x < 2 {
		x = 2
	}
	for yy := y - 2; yy < y+16; yy++ {
		for xx := x - 3; xx < x+w+3; xx++ {
			if image.Pt(xx, yy).In(img.Rect) {
				o := img.RGBAAt(xx, yy)
				img.SetRGBA(xx, yy, color.RGBA{uint8((int(o.R) + 255*3) / 4), uint8((int(o.G) + 255*3) / 4), uint8((int(o.B) + 255*3) / 4), 255})
			}
		}
	}
	text(img, x, y, 2, c, s)
}

func encode(img *image.RGBA) ([]byte, error) {
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func fill(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			setPx(img, x, y, c)
		}
	}
}

func blit(dst, src *image.RGBA, ox, oy int) {
	for y := 0; y < src.Rect.Dy(); y++ {
		for x := 0; x < src.Rect.Dx(); x++ {
			setPx(dst, ox+x, oy+y, src.RGBAAt(x, y))
		}
	}
}

// ---- échelle de couleurs ----

type stop struct {
	t       float64
	r, g, b float64
}

var cmap = []stop{{0, 38, 70, 200}, {0.25, 30, 165, 220}, {0.5, 250, 225, 60}, {0.75, 245, 140, 40}, {1, 205, 25, 30}}

// StressColor : bleu → bleu clair → jaune → orange → rouge.
func StressColor(t float64) [3]float64 {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	for i := 1; i < len(cmap); i++ {
		if t <= cmap[i].t {
			a, b := cmap[i-1], cmap[i]
			f := (t - a.t) / (b.t - a.t)
			return [3]float64{a.r + (b.r-a.r)*f, a.g + (b.g-a.g)*f, a.b + (b.b-a.b)*f}
		}
	}
	return [3]float64{205, 25, 30}
}

// ---- zones ----

func zoneAnchor(zs *ZoneSet, z *Zone) mesh.Vec3 {
	cen := mesh.Vec3{X: z.Centroid[0], Y: z.Centroid[1], Z: z.Centroid[2]}
	best, bd := cen, math.Inf(1)
	for _, ti := range z.tris {
		t := zs.Mesh.T[ti]
		p := zs.Mesh.V[t[0]].Add(zs.Mesh.V[t[1]]).Add(zs.Mesh.V[t[2]]).Scale(1.0 / 3)
		if d := p.Sub(cen).Len(); d < bd {
			best, bd = p, d
		}
	}
	return best
}

func zoneColor(id int) color.RGBA { return zonePalette[(id-1)%len(zonePalette)] }

var kindShort = map[string]string{KindBase: "BASE", KindDessus: "DESSUS", KindParoiXm: "PAROI -X", KindParoiXp: "PAROI +X",
	KindParoiYm: "PAROI -Y", KindParoiYp: "PAROI +Y", KindFaceBasse: "DESSOUS BRAS", KindInclineH: "INCLINEE HAUT",
	KindInclineB: "INCLINEE BAS", KindInclineLat: "INCLINEE LAT"}

// wrapLines coupe les lignes trop longues aux espaces (police bitmap : largeur fixe).
func wrapLines(in []string, max int) []string {
	var out []string
	for _, l := range in {
		for len(l) > max {
			cut := strings.LastIndex(l[:max], " ")
			if cut < max/2 {
				cut = max
			}
			out = append(out, strings.TrimSpace(l[:cut]))
			l = "  " + strings.TrimSpace(l[cut:])
		}
		out = append(out, l)
	}
	return out
}

// PlanImage : pièce en gris avec les zones numérotées en couleur, vue isométrique avant et arrière.
func PlanImage(zs *ZoneSet, title string) ([]byte, error) {
	m := zs.Mesh
	inZone := make([]int, len(m.T))
	for _, z := range zs.Zones {
		for _, ti := range z.tris {
			inZone[ti] = z.ID
		}
	}
	rows := (len(zs.Zones) + 1) / 2
	if rows < 3 {
		rows = 3
	}
	legendH := 16*rows + 14
	W, H := 2*panelW, headerH+panelH+legendH
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	fill(img, img.Rect, bgDark)
	text(img, 10, 10, 2, fgLight, "ZONES CANDIDATES - "+title)
	grey := [3]float64{176, 180, 186}
	for pi, yaw := range []float64{45, 225} {
		cam := newCamera(m, yaw, 35.264, panelW, panelH)
		r, big := drawMesh(m, cam, func(v, t int) [3]float64 {
			if id := inZone[t]; id > 0 {
				c := zoneColor(id)
				return [3]float64{float64(c.R), float64(c.G), float64(c.B)}
			}
			return grey
		})
		panel := r.down()
		diag := m.Bounds().Size.Len()
		for _, z := range zs.Zones {
			a := zoneAnchor(zs, &z)
			if !r.visible(big, a, 0.03*diag*float64(ss)) {
				continue
			}
			x, y, _ := cam.pix(a)
			fillCircle(panel, int(x), int(y), 13, color.RGBA{255, 255, 255, 255})
			ring(panel, int(x), int(y), 13, zoneColor(z.ID))
			ring(panel, int(x), int(y), 12, zoneColor(z.ID))
			s := fmt.Sprint(z.ID)
			text(panel, int(x)-printing.TextWidth(s, 2)/2+1, int(y)-6, 2, colMax, s)
		}
		name := "ISO AVANT"
		if pi == 1 {
			name = "ISO ARRIERE"
		}
		text(panel, 8, 8, 2, color.RGBA{0x55, 0x55, 0x55, 255}, name)
		blit(img, panel, pi*panelW, headerH)
	}
	y0 := headerH + panelH + 8
	for i, z := range zs.Zones {
		col, row := i/rows, i%rows
		x := 10 + col*panelW
		y := y0 + row*16
		fill(img, image.Rect(x, y+1, x+12, y+13), zoneColor(z.ID))
		role := ""
		for _, c := range z.Candidates {
			switch c {
			case "support_pose":
				role += " pose"
			case "support_fixe":
				role += " fixe"
			case "charge":
				role += " charge"
			}
		}
		ext := ""
		if z.Extremity {
			ext = " EXTREMITE"
		}
		text(img, x+18, y, 2, fgLight, fmt.Sprintf("%d %s %.0f mm2 %s%s", z.ID, kindShort[z.Kind], z.AreaMM2, strings.ReplaceAll(trimLeft(role), " ", "/"), ext))
	}
	return encode(img)
}

func trimLeft(s string) string {
	for len(s) > 0 && s[0] == ' ' {
		s = s[1:]
	}
	return s
}

// ---- carte de contraintes ----

// sampleVM : contrainte de von Mises interpolée en un point de la surface du maillage d'origine.
func sampleVM(an *Analysis, p mesh.Vec3) float64 {
	g := an.Grid
	ix := int(math.Floor((p.X - g.Origin[0]) / g.H))
	iy := int(math.Floor((p.Y - g.Origin[1]) / g.H))
	iz := int(math.Floor((p.Z - g.Origin[2]) / g.H))
	bx, by, bz, bd := -1, -1, -1, math.Inf(1)
	for dz := -1; dz <= 1; dz++ {
		for dy := -1; dy <= 1; dy++ {
			for dx := -1; dx <= 1; dx++ {
				x, y, z := ix+dx, iy+dy, iz+dz
				if x < 0 || y < 0 || z < 0 || x >= g.NX || y >= g.NY || z >= g.NZ || !g.Solid[g.Cell(x, y, z)] {
					continue
				}
				cx := g.Origin[0] + (float64(x)+0.5)*g.H - p.X
				cy := g.Origin[1] + (float64(y)+0.5)*g.H - p.Y
				cz := g.Origin[2] + (float64(z)+0.5)*g.H - p.Z
				if d := cx*cx + cy*cy + cz*cz; d < bd {
					bx, by, bz, bd = x, y, z, d
				}
			}
		}
	}
	if bx < 0 {
		return 0
	}
	fx := clamp01((p.X-g.Origin[0])/g.H - float64(bx))
	fy := clamp01((p.Y-g.Origin[1])/g.H - float64(by))
	fz := clamp01((p.Z-g.Origin[2])/g.H - float64(bz))
	v := 0.0
	for k := 0; k < 8; k++ {
		wx, wy, wz := fx, fy, fz
		if k&1 == 0 {
			wx = 1 - fx
		}
		if k&2 == 0 {
			wy = 1 - fy
		}
		if k&4 == 0 {
			wz = 1 - fz
		}
		c := an.Sol.Compact(g.NodeIndex(bx+k&1, by+(k>>1)&1, bz+(k>>2)&1))
		if c >= 0 {
			v += wx * wy * wz * an.VM[c]
		}
	}
	return v
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func verdictColor(v string) color.RGBA {
	switch v {
	case "ok":
		return colGreen
	case "a_surveiller":
		return colAmber
	}
	return colRed
}

func verdictShort(v string) string {
	switch v {
	case "ok":
		return "TIENT"
	case "a_surveiller":
		return "A SURVEILLER"
	}
	return "RISQUE DE CASSE"
}

// StressImage : carte de contraintes de von Mises (MPa) en vue isométrique et de face, charges et appuis annotés.
func StressImage(an *Analysis, title string) ([]byte, error) {
	r := an.Result
	zs := an.Zones
	m := zs.Mesh
	vals := make([]float64, len(m.V))
	for i, v := range m.V {
		vals[i] = sampleVM(an, v)
	}
	scale := an.Scale
	lines := []string{
		fmt.Sprintf("Contrainte max (von Mises) %.1f MPa ; point critique a %.0f %% de la resistance admissible (%.1f MPa dans le plan, %.1f MPa entre couches)", r.MaxVonMisesMPa, 100*r.MaxUtilization, r.AllowableXYMPa, r.AllowableZMPa),
		fmt.Sprintf("Marge obtenue x%.1f (visee x%.1f) - deplacement max %.2f mm - cubes de %.2f mm, %d iterations", r.SafetyAchieved, r.SafetyRequired, r.MaxDisplacementMM, r.Mesh.VoxelMM, r.Solver.Iterations),
		"Calcul simplifie (elements finis lineaires, matiere isotrope) : il ne remplace pas un essai reel.",
		"MESURE: geometrie, charges donnees. CALCULE: contraintes, deplacement. ESTIME: matiere, couches, remplissage, fluage.",
	}
	if r.BasedOnAssumed {
		lines = append(lines, "RESULTAT BASE SUR DES HYPOTHESES (charges, appuis ou reglages supposes) - a confirmer.")
	}
	lines = wrapLines(lines, (2*panelW-20)/12)
	legendH := 70 + 18*len(lines)
	W, H := 2*panelW, headerH+panelH+legendH
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	fill(img, img.Rect, bgDark)
	text(img, 10, 10, 2, verdictColor(r.Verdict), verdictShort(r.Verdict))
	text(img, 10+printing.TextWidth(verdictShort(r.Verdict), 2)+16, 10, 2, fgLight, title)
	diag := m.Bounds().Size.Len()
	for pi, v := range [][2]float64{{45, 35.264}, {0, 0}} {
		cam := newCamera(m, v[0], v[1], panelW, panelH)
		rs, big := drawMesh(m, cam, func(vi, _ int) [3]float64 { return StressColor(vals[vi] / scale) })
		panel := rs.down()
		// zones d'appui et de charge
		for _, l := range an.Loads {
			a := l.Anchor
			x, y, _ := cam.pix(a)
			px := cam
			ex, ey, _ := px.pix(a.Add(l.Dir.Scale(diag * 0.12)))
			dx, dy := ex-x, ey-y
			ln := math.Hypot(dx, dy)
			if ln < 8 { // dirigée vers la caméra : cercle pointé
				ring(panel, int(x), int(y), 9, colLoad)
				fillCircle(panel, int(x), int(y), 3, colLoad)
			} else {
				dx, dy = dx/ln*70, dy/ln*70
				arrow(panel, x-dx, y-dy, x, y, colLoad)
				labelled(panel, int(x-dx)-20, int(y-dy)-18, colLoad, fmt.Sprintf("%.0f N", l.N))
			}
		}
		for _, s := range an.Supp {
			x, y, _ := cam.pix(s.Anchor)
			triMarker(panel, int(x), int(y), 16, colSupp)
			lab := "APPUI FIXE"
			if s.In.Type == "resting" {
				lab = "APPUI POSE"
			}
			labelled(panel, int(x)-30, int(y)+20, colSupp, lab)
		}
		mark := func(p [3]float64, lab string, c color.RGBA, dy int) {
			cp := mesh.Vec3{X: p[0], Y: p[1], Z: p[2]}
			if rs.visible(big, cp, 0.05*diag*float64(ss)) {
				x, y, _ := cam.pix(cp)
				ring(panel, int(x), int(y), 12, color.RGBA{255, 255, 255, 255})
				ring(panel, int(x), int(y), 10, c)
				ring(panel, int(x), int(y), 9, c)
				labelled(panel, int(x)+14, int(y)+dy, c, lab)
			}
		}
		mark(r.MaxVMPoint, fmt.Sprintf("MAX %.1f MPa", r.MaxVonMisesMPa), colMax, -20)
		d := math.Sqrt(math.Pow(r.CriticalPoint[0]-r.MaxVMPoint[0], 2) + math.Pow(r.CriticalPoint[1]-r.MaxVMPoint[1], 2) + math.Pow(r.CriticalPoint[2]-r.MaxVMPoint[2], 2))
		if d > 4*r.Mesh.VoxelMM { // le point où la marge est la plus faible (liaison entre couches) n'est pas celui du maximum de von Mises
			mark(r.CriticalPoint, fmt.Sprintf("CRITIQUE %.1f MPa", r.CriticalStressMPa), colLoad, 6)
		}
		name := "ISO"
		if pi == 1 {
			name = "FACE (de face, vers +Y)"
		}
		text(panel, 8, 8, 2, color.RGBA{0x55, 0x55, 0x55, 255}, name)
		blit(img, panel, pi*panelW, headerH)
	}
	// légende : barre de couleurs en MPa
	y0 := headerH + panelH + 14
	bx, bw, bh := 14, 520, 16
	for i := 0; i < bw; i++ {
		c := StressColor(float64(i) / float64(bw-1))
		fill(img, image.Rect(bx+i, y0, bx+i+1, y0+bh), color.RGBA{uint8(c[0]), uint8(c[1]), uint8(c[2]), 255})
	}
	for _, f := range []float64{0, 0.25, 0.5, 0.75, 1} {
		x := bx + int(f*float64(bw-1))
		fill(img, image.Rect(x, y0+bh, x+1, y0+bh+5), fgLight)
		lab := fmt.Sprintf("%.1f", f*scale)
		tx := x - printing.TextWidth(lab, 2)/2
		if f == 0 {
			tx = x
		}
		if f == 1 {
			tx = x - printing.TextWidth(lab, 2)
		}
		text(img, tx, y0+bh+8, 2, fgLight, lab)
	}
	text(img, bx+bw+14, y0, 2, fgLight, "MPa (von Mises)")
	if r.AllowableXYMPa <= scale {
		x := bx + int(r.AllowableXYMPa/scale*float64(bw-1))
		fill(img, image.Rect(x-1, y0-6, x+1, y0+bh+2), color.RGBA{255, 255, 255, 255})
		text(img, x-20, y0-22, 2, fgLight, "LIMITE")
	} else {
		text(img, bx+bw+14, y0+18, 2, fgLight, fmt.Sprintf("limite %.1f hors echelle", r.AllowableXYMPa))
	}
	for i, l := range wrapLines(lines, (W-20)/12) {
		text(img, 10, y0+44+18*i, 2, fgLight, l)
	}
	return encode(img)
}
