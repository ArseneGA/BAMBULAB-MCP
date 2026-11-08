package printing

import (
	"context"
	"math"
)

// Trajectoires d'une couche pour la visionneuse de la vue MCP App (outil réservé à l'application) : polylignes par
// type (FEATURE), coordonnées entières en 1/20 mm, bornées en taille (la vue reçoit le résultat par callServerTool,
// seuil d'environ 150 000 caractères au-delà duquel certains hôtes le remplacent par un fichier).

// LayerUnitMM : pas des coordonnées entières (mm).
const LayerUnitMM = 0.05

// maxLayerPoints : points au plus par réponse (environ 100 000 caractères JSON).
const maxLayerPoints = 16000

// LayerPathsInput désigne une couche du G-code d'un artefact.
type LayerPathsInput struct {
	ArtifactID  string `json:"artifact_id,omitempty" jsonschema:"empreinte sha256 d'un .gcode.3mf"`
	PrintJobID  string `json:"print_job_id,omitempty" jsonschema:"à la place d'artifact_id : l'artefact de cette impression"`
	Plate       int    `json:"plate,omitempty" jsonschema:"plaque (défaut : celle de l'impression, sinon 1)"`
	Layer       int    `json:"layer,omitempty" jsonschema:"couche à partir de 1 (défaut 1)"`
	LayersTotal int    `json:"layers_total,omitempty" jsonschema:"nombre total de couches s'il est déjà connu (évite un second passage sur le G-code)"`
}

// LayerPath : une polyligne extrudée d'un même type.
type LayerPath struct {
	Feature int   `json:"f" jsonschema:"index dans features"`
	Points  []int `json:"p" jsonschema:"x0,y0,x1,y1,… en unités de unit_mm"`
}

// LayerPathsOutput : trajectoires extrudées d'une couche.
type LayerPathsOutput struct {
	ArtifactSHA256 string      `json:"artifact_sha256"`
	Plate          int         `json:"plate"`
	Layer          int         `json:"layer"`
	LayersTotal    int         `json:"layers_total"`
	ZMM            float64     `json:"z_mm,omitempty"`
	UnitMM         float64     `json:"unit_mm" jsonschema:"taille d'une unité de coordonnée en mm"`
	Features       []string    `json:"features" jsonschema:"types de trajectoires (FEATURE) ; Paths[].f en est l'index"`
	Paths          []LayerPath `json:"paths"`
	BBoxMM         [4]float64  `json:"bbox_mm" jsonschema:"[x0,y0,x1,y1] de la couche"`
	Segments       int         `json:"segments" jsonschema:"segments extrudés lus dans le G-code"`
	Points         int         `json:"points" jsonschema:"points envoyés"`
	ToleranceMM    float64     `json:"tolerance_mm,omitempty" jsonschema:"si > 0 : tracé simplifié (Douglas-Peucker) pour tenir dans la taille bornée"`
}

// LayerPaths lit une couche du G-code et la rend en polylignes. Lecture seule.
func (s *Service) LayerPaths(ctx context.Context, in LayerPathsInput) (*LayerPathsOutput, error) {
	if in.ArtifactID == "" && in.PrintJobID == "" {
		return nil, invalidf("print_job_id ou artifact_id requis")
	}
	path, sha, plate, _, err := s.resolveGcode(ctx, PreviewInput{ArtifactID: in.ArtifactID, PrintJobID: in.PrintJobID, Plate: in.Plate})
	if err != nil {
		return nil, err
	}
	return buildLayerPaths(path, sha, plate, in.Layer, in.LayersTotal)
}

func buildLayerPaths(archive, sha string, plate, layer, total int) (*LayerPathsOutput, error) {
	if plate <= 0 {
		plate = 1
	}
	if layer <= 0 {
		layer = 1
	}
	zr, zf, err := openPlateGcode(archive, plate)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	if total <= 0 {
		if total, err = countGcodeLayers(zf); err != nil {
			return nil, err
		}
	}
	if total == 0 {
		return nil, invalidf("aucun marqueur CHANGE_LAYER dans le G-code de la plaque %d", plate)
	}
	if layer > total {
		return nil, invalidf("couche %d hors plage (1..%d)", layer, total)
	}
	gs, err := scanGcode(zf, map[int]bool{layer: true})
	if err != nil {
		return nil, err
	}
	ld := gs.Layers[layer]
	out := &LayerPathsOutput{ArtifactSHA256: sha, Plate: plate, Layer: layer, LayersTotal: total, UnitMM: LayerUnitMM, Features: gs.Feats, Paths: []LayerPath{}}
	if ld == nil {
		return out, nil
	}
	out.ZMM, out.Segments = ld.Z, len(ld.Segs)
	if out.Features == nil {
		out.Features = []string{}
	}
	// chaînage : un trajet continu d'un même type devient une polyligne
	type poly struct {
		f   int
		pts [][2]float64
	}
	var polys []poly
	bb := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for _, sg := range ld.Segs {
		bb[0], bb[2] = math.Min(bb[0], math.Min(sg.x0, sg.x1)), math.Max(bb[2], math.Max(sg.x0, sg.x1))
		bb[1], bb[3] = math.Min(bb[1], math.Min(sg.y0, sg.y1)), math.Max(bb[3], math.Max(sg.y0, sg.y1))
		if n := len(polys); n > 0 {
			last := polys[n-1]
			e := last.pts[len(last.pts)-1]
			if last.f == sg.feat && math.Abs(e[0]-sg.x0) < 1e-6 && math.Abs(e[1]-sg.y0) < 1e-6 {
				polys[n-1].pts = append(last.pts, [2]float64{sg.x1, sg.y1})
				continue
			}
		}
		polys = append(polys, poly{sg.feat, [][2]float64{{sg.x0, sg.y0}, {sg.x1, sg.y1}}})
	}
	if len(polys) > 0 {
		for i := range bb {
			bb[i] = math.Round(bb[i]*100) / 100
		}
		out.BBoxMM = bb
	}
	count := func(tol float64) int {
		n := 0
		for _, p := range polys {
			n += len(simplify(p.pts, tol))
		}
		return n
	}
	tol := 0.0
	if count(0) > maxLayerPoints {
		for tol = LayerUnitMM; tol < 4; tol *= 2 {
			if count(tol) <= maxLayerPoints {
				break
			}
		}
		out.ToleranceMM = tol
	}
	for _, p := range polys {
		pts := simplify(p.pts, tol)
		q := make([]int, 0, 2*len(pts))
		for _, v := range pts {
			q = append(q, int(math.Round(v[0]/LayerUnitMM)), int(math.Round(v[1]/LayerUnitMM)))
		}
		out.Points += len(pts)
		out.Paths = append(out.Paths, LayerPath{Feature: p.f, Points: q})
	}
	return out, nil
}

// simplify : Douglas-Peucker itératif ; tol <= 0 = aucune simplification.
func simplify(pts [][2]float64, tol float64) [][2]float64 {
	if tol <= 0 || len(pts) < 3 {
		return pts
	}
	keep := make([]bool, len(pts))
	keep[0], keep[len(pts)-1] = true, true
	type span struct{ a, b int }
	stack := []span{{0, len(pts) - 1}}
	for len(stack) > 0 {
		sp := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		a, b := pts[sp.a], pts[sp.b]
		dx, dy := b[0]-a[0], b[1]-a[1]
		l := math.Hypot(dx, dy)
		far, fi := 0.0, -1
		for i := sp.a + 1; i < sp.b; i++ {
			var d float64
			if l < 1e-12 {
				d = math.Hypot(pts[i][0]-a[0], pts[i][1]-a[1])
			} else {
				d = math.Abs(dx*(a[1]-pts[i][1])-(a[0]-pts[i][0])*dy) / l
			}
			if d > far {
				far, fi = d, i
			}
		}
		if fi >= 0 && far > tol {
			keep[fi] = true
			stack = append(stack, span{sp.a, fi}, span{fi, sp.b})
		}
	}
	out := make([][2]float64, 0, len(pts))
	for i, k := range keep {
		if k {
			out = append(out, pts[i])
		}
	}
	return out
}
