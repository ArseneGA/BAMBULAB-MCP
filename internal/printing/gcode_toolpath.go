package printing

import (
	"archive/zip"
	"bufio"
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Trajectoires 3D du G-code découpé, pour la visionneuse couche par couche (vue MCP App ui://bambu/gcode.html et
// interface web). Le G-code d'une plaque est lu UNE fois (flux), chaque couche est chaînée en polylignes par type
// (FEATURE) et par vitesse, simplifiée (Douglas-Peucker) pour tenir dans un budget de points, puis gardée en mémoire
// (cache borné). Les outils ne font que découper ce résultat en tranches : aucune relecture du fichier par tranche.
//
// BUDGETS (variables pour les tests, valeurs d'exploitation ci-dessous) :
//   - tpMaxTotalPoints  : 2 000 000 points au plus pour toute la plaque (extrusions + déplacements à vide) ;
//   - tpMaxLayerPoints  : 10 000 points d'extrusion au plus par couche (+ 2 000 de déplacements à vide) ;
//   - budget par couche = clamp(tpMaxTotalPoints / nombre de couches, tpMinLayerPoints, tpMaxLayerPoints) ;
//   - tpMaxLayers       : 6 000 couches au plus ;
//   - une tranche d'outil (get_gcode_slice) ≤ 12 000 points, soit environ 100 000 caractères JSON (coordonnées en
//     entiers de 0,05 mm, les points suivants le premier codés en différences).
var (
	tpMaxTotalPoints = 2_000_000
	tpMaxLayerPoints = 10_000
	tpMinLayerPoints = 200
	tpMaxLayers      = 6_000
	// TPSlicePoints : points au plus par tranche servie à la vue (une couche entière n'est jamais coupée).
	TPSlicePoints = 12_000
)

// tpTravelDiv : le budget des déplacements à vide est le budget d'extrusion divisé par ce facteur.
const tpTravelDiv = 5

// TravelFeature : nom du pseudo-type des déplacements à vide dans features.
const TravelFeature = "Travel"

// TPPath : une polyligne d'un même type et d'une même vitesse (arrondie à 5 mm/s). P = x0,y0 puis les DIFFÉRENCES
// (dx,dy) de chaque point suivant, en unités de LayerUnitMM (0,05 mm).
type TPPath struct {
	Feature int   `json:"f" jsonschema:"index dans features"`
	Speed   int   `json:"s,omitempty" jsonschema:"vitesse en mm/s (arrondie à 5)"`
	Points  []int `json:"p" jsonschema:"x0,y0 absolus puis dx,dy de chaque point suivant, en unités de unit_mm"`
}

type tpLayer struct {
	Z, TimeS, ExtrudedMM float64
	Paths                []TPPath
	Points, Segs         int
}

// Toolpath : trajectoires de toute une plaque.
type Toolpath struct {
	SHA         string
	Plate       int
	Layers      []tpLayer
	Feats       []string
	TravelFeat  int // index de « Travel » dans Feats, -1 si aucun déplacement
	TotalTimeS  float64
	TimeSource  string // "slicer" (durée annoncée par le G-code) | "estimation" (distance / vitesse programmée)
	FilamentMM  float64
	BBox        [4]float64
	Points      int
	Simplified  bool // au moins une couche a été simplifiée pour tenir dans son budget
	MaxTolMM    float64
	LayerBudget int
}

type tpSeg struct {
	x0, y0, x1, y1 float64
	feat, spd      int
	travel         bool
}

var estTimeRe = regexp.MustCompile(`(?i)total estimated time:\s*(?:(\d+)d\s*)?(?:(\d+)h\s*)?(?:(\d+)m\s*)?(?:(\d+)s)?`)

func parseSlicerTime(l string) (float64, bool) {
	m := estTimeRe.FindStringSubmatch(l)
	if m == nil || (m[1] == "" && m[2] == "" && m[3] == "" && m[4] == "") {
		return 0, false
	}
	n := func(s string) float64 { v, _ := strconv.Atoi(s); return float64(v) }
	return n(m[1])*86400 + n(m[2])*3600 + n(m[3])*60 + n(m[4]), true
}

// layerBudget : points d'extrusion permis par couche pour un nombre total de couches.
func layerBudget(layers int) int {
	if layers < 1 {
		layers = 1
	}
	return min(tpMaxLayerPoints, max(tpMinLayerPoints, tpMaxTotalPoints/layers))
}

// parseToolpath lit toute la plaque. total = nombre de couches (déjà compté).
func parseToolpath(zf *zip.File, sha string, plate, total int) (*Toolpath, error) {
	rc, err := zf.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	br := bufio.NewReaderSize(rc, 1<<20)
	tp := &Toolpath{SHA: sha, Plate: plate, TravelFeat: -1, TimeSource: "estimation", LayerBudget: layerBudget(total),
		BBox: [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}}
	featIdx := map[string]int{}
	feat, spdNow := -1, 0
	cur, abs, absE := 0, true, false
	var x, y, e, zNow, feed float64
	var segs []tpSeg
	var layer *tpLayer
	slicerTime := 0.0

	travelIdx := func() int {
		if tp.TravelFeat < 0 {
			tp.TravelFeat = len(tp.Feats)
			tp.Feats = append(tp.Feats, TravelFeature)
			featIdx[TravelFeature] = tp.TravelFeat
		}
		return tp.TravelFeat
	}
	flush := func() {
		if layer == nil {
			return
		}
		tp.finishLayer(layer, segs)
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
			if cur > tpMaxLayers {
				return nil, invalidf("plus de %d couches : non pris en charge par la visionneuse", tpMaxLayers)
			}
			tp.Layers = append(tp.Layers, tpLayer{Z: zNow})
			layer = &tp.Layers[len(tp.Layers)-1]
		case strings.HasPrefix(l, "; Z_HEIGHT:") && layer != nil:
			layer.Z, _ = strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(l, "; Z_HEIGHT:")), 64)
		case strings.HasPrefix(l, "; FEATURE:"):
			name := strings.TrimSpace(strings.TrimPrefix(l, "; FEATURE:"))
			i, ok := featIdx[name]
			if !ok {
				i = len(tp.Feats)
				featIdx[name] = i
				tp.Feats = append(tp.Feats, name)
			}
			feat = i
		case slicerTime == 0 && strings.HasPrefix(l, ";") && strings.Contains(l, "total estimated time"):
			if v, ok := parseSlicerTime(l); ok {
				slicerTime = v
			}
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
			if nf, hf := parseCoord(l, 'F'); hf && nf > 0 {
				feed = nf / 60 // mm/s
				spdNow = int(math.Round(feed/5)) * 5
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
			dE := 0.0
			if he {
				if absE {
					dE = ne - e
					e = ne
				} else {
					dE = ne
				}
			}
			if (hx || hy) && layer != nil {
				d := math.Hypot(x-px, y-py)
				v := feed
				if v <= 0 {
					v = 50
				}
				layer.TimeS += d / v
				if dE > 1e-9 {
					layer.ExtrudedMM += dE
					tp.FilamentMM += dE
					segs = append(segs, tpSeg{px, py, x, y, feat, spdNow, false})
					tp.BBox[0], tp.BBox[2] = math.Min(tp.BBox[0], math.Min(px, x)), math.Max(tp.BBox[2], math.Max(px, x))
					tp.BBox[1], tp.BBox[3] = math.Min(tp.BBox[1], math.Min(py, y)), math.Max(tp.BBox[3], math.Max(py, y))
				} else if d > 0.05 && !strings.HasPrefix(l, "G2") && !strings.HasPrefix(l, "G3") {
					segs = append(segs, tpSeg{px, py, x, y, travelIdx(), spdNow, true})
				}
			}
		}
		if rerr != nil {
			break
		}
	}
	flush()
	if len(tp.Layers) == 0 {
		return nil, invalidf("aucun marqueur CHANGE_LAYER dans le G-code de la plaque %d", plate)
	}
	est := 0.0
	for _, ly := range tp.Layers {
		est += ly.TimeS
	}
	tp.TotalTimeS, tp.TimeSource = est, "estimation"
	if slicerTime > 0 && est > 0 { // les temps par couche sont ramenés à la durée annoncée par le slicer
		k := slicerTime / est
		for i := range tp.Layers {
			tp.Layers[i].TimeS *= k
		}
		tp.TotalTimeS, tp.TimeSource = slicerTime, "slicer"
	}
	if math.IsInf(tp.BBox[0], 1) {
		tp.BBox = [4]float64{}
	}
	return tp, nil
}

// finishLayer chaîne les segments d'une couche en polylignes, simplifie pour tenir dans le budget, et range le résultat.
func (tp *Toolpath) finishLayer(ly *tpLayer, segs []tpSeg) {
	type poly struct {
		f, s   int
		travel bool
		pts    [][2]float64
	}
	var polys []poly
	for _, sg := range segs {
		if n := len(polys); n > 0 {
			last := &polys[n-1]
			e := last.pts[len(last.pts)-1]
			if last.f == sg.feat && last.s == sg.spd && last.travel == sg.travel && math.Abs(e[0]-sg.x0) < 1e-6 && math.Abs(e[1]-sg.y0) < 1e-6 {
				last.pts = append(last.pts, [2]float64{sg.x1, sg.y1})
				continue
			}
		}
		polys = append(polys, poly{sg.feat, sg.spd, sg.travel, [][2]float64{{sg.x0, sg.y0}, {sg.x1, sg.y1}}})
		if !sg.travel {
			ly.Segs++
		}
	}
	tolFor := func(travel bool, budget int) float64 {
		count := func(tol float64) int {
			n := 0
			for _, p := range polys {
				if p.travel == travel {
					n += len(simplify(p.pts, tol))
				}
			}
			return n
		}
		if count(0) <= budget {
			return 0
		}
		tol := LayerUnitMM
		for ; tol < 8; tol *= 2 {
			if count(tol) <= budget {
				break
			}
		}
		return tol
	}
	tolE, tolT := tolFor(false, tp.LayerBudget), tolFor(true, max(50, tp.LayerBudget/tpTravelDiv))
	if tolE > 0 || tolT > 0 {
		tp.Simplified = true
		tp.MaxTolMM = math.Max(tp.MaxTolMM, math.Max(tolE, tolT))
	}
	for _, p := range polys {
		tol := tolE
		if p.travel {
			tol = tolT
		}
		pts := simplify(p.pts, tol)
		q := make([]int, 0, 2*len(pts))
		px, py := 0, 0
		for i, v := range pts {
			ix, iy := int(math.Round(v[0]/LayerUnitMM)), int(math.Round(v[1]/LayerUnitMM))
			if i == 0 {
				q = append(q, ix, iy)
			} else {
				q = append(q, ix-px, iy-py)
			}
			px, py = ix, iy
		}
		ly.Points += len(pts)
		tp.Points += len(pts)
		ly.Paths = append(ly.Paths, TPPath{Feature: p.f, Speed: p.s, Points: q})
	}
}

// ---- cache ----

type tpKey struct {
	sha   string
	plate int
}

var tpCache = struct {
	sync.Mutex
	order []tpKey
	m     map[tpKey]*Toolpath
}{m: map[tpKey]*Toolpath{}}

const tpCacheMax = 3

// toolpathFor rend les trajectoires de la plaque (lecture unique, ensuite en mémoire).
func toolpathFor(archive, sha string, plate int) (*Toolpath, error) {
	if plate <= 0 {
		plate = 1
	}
	k := tpKey{sha, plate}
	tpCache.Lock()
	defer tpCache.Unlock() // une seule lecture à la fois : le reste attend le résultat au lieu de relire le fichier
	if tp, ok := tpCache.m[k]; ok {
		return tp, nil
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
	if total > tpMaxLayers {
		return nil, invalidf("%d couches : au-delà de %d, la visionneuse n'est pas prise en charge", total, tpMaxLayers)
	}
	tp, err := parseToolpath(zf, sha, plate, total)
	if err != nil {
		return nil, err
	}
	if len(tpCache.order) >= tpCacheMax {
		delete(tpCache.m, tpCache.order[0])
		tpCache.order = tpCache.order[1:]
	}
	tpCache.m[k] = tp
	tpCache.order = append(tpCache.order, k)
	return tp, nil
}

// ---- outils ----

// GcodeInput désigne la plaque d'un G-code découpé.
type GcodeInput struct {
	ArtifactID string `json:"artifact_id,omitempty" jsonschema:"empreinte sha256 d'un .gcode.3mf"`
	PrintJobID string `json:"print_job_id,omitempty" jsonschema:"à la place d'artifact_id : l'artefact de cette impression"`
	Plate      int    `json:"plate,omitempty" jsonschema:"plaque (défaut : celle de l'impression, sinon 1)"`
}

// GcodeIndexOutput : résumé de toute la plaque et colonnes par couche (légères).
type GcodeIndexOutput struct {
	ArtifactSHA256 string     `json:"artifact_sha256"`
	Plate          int        `json:"plate"`
	LayersTotal    int        `json:"layers_total"`
	UnitMM         float64    `json:"unit_mm" jsonschema:"taille d'une unité de coordonnée en mm"`
	Features       []string   `json:"features" jsonschema:"types de trajectoires (FEATURE de Bambu Studio) ; « Travel » = déplacements à vide"`
	Z              []float64  `json:"z" jsonschema:"hauteur Z (mm) de chaque couche"`
	TimeS          []float64  `json:"time_s" jsonschema:"durée estimée de chaque couche (s)"`
	ExtrudedMM     []float64  `json:"extruded_mm" jsonschema:"longueur de filament extrudée par couche (mm)"`
	Points         []int      `json:"points" jsonschema:"points servis par couche (extrusions + déplacements)"`
	TotalTimeS     float64    `json:"total_time_s"`
	TimeSource     string     `json:"time_source" jsonschema:"slicer = durée annoncée par le G-code, répartie par couche | estimation = distance / vitesse programmée, sans accélérations"`
	FilamentMM     float64    `json:"filament_mm"`
	BBoxMM         [4]float64 `json:"bbox_mm" jsonschema:"[x0,y0,x1,y1] des extrusions"`
	TotalPoints    int        `json:"total_points"`
	MaxTotalPoints int        `json:"max_total_points" jsonschema:"plafond de points pour toute la plaque"`
	LayerBudget    int        `json:"layer_budget" jsonschema:"points d'extrusion au plus par couche"`
	Simplified     bool       `json:"simplified" jsonschema:"des couches ont été simplifiées (Douglas-Peucker) pour tenir dans leur budget"`
	ToleranceMM    float64    `json:"tolerance_mm,omitempty" jsonschema:"écart maximal toléré par la simplification"`
	SlicePoints    int        `json:"slice_points" jsonschema:"points visés par tranche de get_gcode_slice"`
}

func roundTo(v float64, d int) float64 { p := math.Pow(10, float64(d)); return math.Round(v*p) / p }

// GcodeIndex lit (une fois) le G-code d'une plaque et rend son résumé.
func (s *Service) GcodeIndex(ctx context.Context, in GcodeInput) (*GcodeIndexOutput, error) {
	tp, err := s.toolpath(ctx, in)
	if err != nil {
		return nil, err
	}
	return tp.index(), nil
}

func (s *Service) toolpath(ctx context.Context, in GcodeInput) (*Toolpath, error) {
	if in.ArtifactID == "" && in.PrintJobID == "" {
		return nil, invalidf("print_job_id ou artifact_id requis")
	}
	path, sha, plate, _, err := s.resolveGcode(ctx, PreviewInput{ArtifactID: in.ArtifactID, PrintJobID: in.PrintJobID, Plate: in.Plate})
	if err != nil {
		return nil, err
	}
	return toolpathFor(path, sha, plate)
}

func (tp *Toolpath) index() *GcodeIndexOutput {
	n := len(tp.Layers)
	o := &GcodeIndexOutput{ArtifactSHA256: tp.SHA, Plate: tp.Plate, LayersTotal: n, UnitMM: LayerUnitMM, Features: append([]string{}, tp.Feats...),
		Z: make([]float64, n), TimeS: make([]float64, n), ExtrudedMM: make([]float64, n), Points: make([]int, n),
		TotalTimeS: roundTo(tp.TotalTimeS, 1), TimeSource: tp.TimeSource, FilamentMM: roundTo(tp.FilamentMM, 1), BBoxMM: tp.BBox,
		TotalPoints: tp.Points, MaxTotalPoints: tpMaxTotalPoints, LayerBudget: tp.LayerBudget, Simplified: tp.Simplified, ToleranceMM: tp.MaxTolMM, SlicePoints: TPSlicePoints}
	for i := range tp.BBox {
		o.BBoxMM[i] = roundTo(tp.BBox[i], 2)
	}
	for i, ly := range tp.Layers {
		o.Z[i], o.TimeS[i], o.ExtrudedMM[i], o.Points[i] = roundTo(ly.Z, 3), roundTo(ly.TimeS, 1), roundTo(ly.ExtrudedMM, 1), ly.Points
	}
	return o
}

// GcodeSliceInput : tranche de couches à partir de from_layer.
type GcodeSliceInput struct {
	ArtifactID string `json:"artifact_id,omitempty" jsonschema:"empreinte sha256 d'un .gcode.3mf"`
	PrintJobID string `json:"print_job_id,omitempty" jsonschema:"à la place d'artifact_id : l'artefact de cette impression"`
	Plate      int    `json:"plate,omitempty" jsonschema:"plaque (défaut : celle de l'impression, sinon 1)"`
	FromLayer  int    `json:"from_layer,omitempty" jsonschema:"première couche de la tranche, à partir de 1 (défaut 1)"`
	MaxPoints  int    `json:"max_points,omitempty" jsonschema:"points visés par tranche (défaut 12000, plafonné à 12000 ; une couche n'est jamais coupée)"`
	WithTravel bool   `json:"with_travel,omitempty" jsonschema:"inclure les déplacements à vide (masqués par défaut dans la vue)"`
}

// GcodeSliceLayer : les polylignes d'une couche.
type GcodeSliceLayer struct {
	Layer int      `json:"l" jsonschema:"numéro de couche à partir de 1"`
	Paths []TPPath `json:"paths"`
}

// GcodeSliceOutput : une tranche de couches consécutives.
type GcodeSliceOutput struct {
	ArtifactSHA256 string            `json:"artifact_sha256"`
	Plate          int               `json:"plate"`
	LayersTotal    int               `json:"layers_total"`
	From           int               `json:"from"`
	Next           int               `json:"next" jsonschema:"première couche de la tranche suivante ; 0 quand tout est servi"`
	UnitMM         float64           `json:"unit_mm"`
	Layers         []GcodeSliceLayer `json:"layers"`
	Points         int               `json:"points" jsonschema:"points de cette tranche"`
	Done           bool              `json:"done"`
}

// GcodeSlice rend des couches consécutives à partir de from_layer, dans la limite de max_points.
func (s *Service) GcodeSlice(ctx context.Context, in GcodeSliceInput) (*GcodeSliceOutput, error) {
	tp, err := s.toolpath(ctx, GcodeInput{ArtifactID: in.ArtifactID, PrintJobID: in.PrintJobID, Plate: in.Plate})
	if err != nil {
		return nil, err
	}
	return tp.slice(in.FromLayer, in.MaxPoints, in.WithTravel)
}

func (tp *Toolpath) slice(from, maxPts int, withTravel bool) (*GcodeSliceOutput, error) {
	if from <= 0 {
		from = 1
	}
	if from > len(tp.Layers) {
		return nil, invalidf("couche %d hors plage (1..%d)", from, len(tp.Layers))
	}
	if maxPts <= 0 || maxPts > TPSlicePoints {
		maxPts = TPSlicePoints
	}
	o := &GcodeSliceOutput{ArtifactSHA256: tp.SHA, Plate: tp.Plate, LayersTotal: len(tp.Layers), From: from, UnitMM: LayerUnitMM, Layers: []GcodeSliceLayer{}}
	i := from
	for ; i <= len(tp.Layers); i++ {
		ly := tp.Layers[i-1]
		var paths []TPPath
		pts := 0
		for _, p := range ly.Paths {
			if p.Feature == tp.TravelFeat && !withTravel {
				continue
			}
			paths = append(paths, p)
			pts += len(p.Points) / 2
		}
		if len(o.Layers) > 0 && o.Points+pts > maxPts {
			break
		}
		if paths == nil {
			paths = []TPPath{}
		}
		o.Layers = append(o.Layers, GcodeSliceLayer{Layer: i, Paths: paths})
		o.Points += pts
	}
	if i > len(tp.Layers) {
		o.Done = true
	} else {
		o.Next = i
	}
	return o, nil
}

// GcodeSummaryLine : une ligne lisible pour le texte des outils (durée, filament, couches).
func (o *GcodeIndexOutput) SummaryLine() string {
	t := int(o.TotalTimeS + 0.5)
	dur := fmt.Sprintf("%d min", t/60)
	if t >= 3600 {
		dur = fmt.Sprintf("%d h %02d min", t/3600, (t%3600)/60)
	}
	src := "estimation"
	if o.TimeSource == "slicer" {
		src = "selon le slicer"
	}
	return fmt.Sprintf("%d couches, durée %s (%s), %.1f m de filament", o.LayersTotal, dur, src, o.FilamentMM/1000)
}


