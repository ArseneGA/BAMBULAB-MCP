package printing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// ---- create_parametric_model ----

// CreateParametricInput est l'entrée de create_parametric_model (worker Python).
type CreateParametricInput struct {
	Template string         `json:"template" jsonschema:"clamp | bracket | box | spacer | hook"`
	Params   map[string]any `json:"params" jsonschema:"dimensions en mm, voir le protocole du worker (ex. clamp : tube_diameter, clearance, wall...)"`
	Name     string         `json:"name,omitempty" jsonschema:"nom du modèle (lettres, chiffres, . _ -)"`
	NozzleMM float64        `json:"nozzle_diameter,omitempty" jsonschema:"buse visée (mm) pour les jeux et parois, si le gabarit en tient compte"`
	Tags     []string       `json:"tags,omitempty"`
}

// CreateParametricOutput : modèle créé, hypothèses et mesures du worker.
type CreateParametricOutput struct {
	ModelID     string                 `json:"model_id"`
	VersionID   string                 `json:"version_id"`
	Files       []contracts.SourceFile `json:"files"`
	Template    string                 `json:"template"`
	Params      map[string]any         `json:"params"`
	Assumptions []string               `json:"assumptions"`
	Warnings    []string               `json:"warnings"`
	BBoxMM      map[string]any         `json:"bbox_mm,omitempty"`
	Derived     map[string]any         `json:"derived,omitempty"`
	Level       string                 `json:"level,omitempty" jsonschema:"niveau de confiance annoncé par le worker (calculé)"`
	Note        string                 `json:"note"`
}

var paramName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// CreateParametricModel génère un modèle paramétrique (CadQuery) et l'enregistre au catalogue.
func (s *Service) CreateParametricModel(ctx context.Context, in CreateParametricInput) (*CreateParametricOutput, error) {
	if s.d.Catalog == nil {
		return nil, fmt.Errorf("%w : catalogue absent", ErrUnavailable)
	}
	if in.Template == "" {
		return nil, invalidf("template requis")
	}
	name := in.Name
	if name == "" {
		name = fmt.Sprintf("%s_%s", in.Template, s.now().Format("20060102_150405"))
	}
	if !paramName.MatchString(name) {
		return nil, invalidf("nom invalide : 1 à 64 caractères parmi lettres, chiffres, point, tiret et souligné")
	}
	out := filepath.Join(s.workRoot(), "param-"+contracts.NewID("w"))
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, err
	}
	defer os.RemoveAll(out)
	req := map[string]any{"template": in.Template, "params": in.Params, "out_dir": out, "name": name}
	if in.NozzleMM > 0 {
		req["nozzle_diameter"] = in.NozzleMM
	}
	raw, err := s.d.Catalog.RunWorker(ctx, "parametric", req)
	if err != nil {
		var we *catalog.WorkerError
		if errors.As(err, &we) {
			if we.Code == "invalid_params" {
				return nil, invalidf("%s", we.Message)
			}
			return nil, fmt.Errorf("%w : %s : %s", ErrUnavailable, we.Code, we.Message)
		}
		return nil, fmt.Errorf("%w : worker géométrie : %v", ErrUnavailable, err)
	}
	var res struct {
		Template    string         `json:"template"`
		Params      map[string]any `json:"params"`
		Assumptions []string       `json:"assumptions"`
		Warnings    []string       `json:"warnings"`
		BBox        map[string]any `json:"bbox_mm"`
		Derived     map[string]any `json:"derived"`
		Level       string         `json:"level"`
		Files       struct {
			STL  string `json:"stl"`
			STEP string `json:"step"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.Files.STL == "" {
		return nil, errors.New("réponse du worker sans fichier STL")
	}
	var gen []catalog.GeneratedFile
	for _, f := range []struct {
		path string
		ext  string
		fmt  contracts.FileFormat
	}{{res.Files.STL, ".stl", contracts.FormatSTL}, {res.Files.STEP, ".step", contracts.FormatSTEP}} {
		if f.path == "" {
			continue
		}
		p := f.path
		if !filepath.IsAbs(p) {
			p = filepath.Join(out, p)
		}
		if rel, err := filepath.Rel(out, p); err != nil || strings.HasPrefix(rel, "..") {
			return nil, fmt.Errorf("fichier du worker hors du dossier de travail : %s", p)
		}
		fh, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		sha, size, err := s.d.Blobs.Put(fh, "")
		fh.Close()
		if err != nil {
			return nil, err
		}
		gen = append(gen, catalog.GeneratedFile{Name: name + f.ext, Format: f.fmt, SHA256: sha, Size: size, Unit: "mm"})
	}
	pj, _ := json.Marshal(in.Params)
	m := contracts.Model{Name: name, Tags: append([]string{"parametrique", in.Template}, in.Tags...),
		Description: fmt.Sprintf("Modèle paramétrique « %s » généré par le worker CAO.", in.Template),
		Provenance:  &contracts.Provenance{Source: "generation parametrique", Note: "gabarit " + in.Template + " ; paramètres " + string(pj), ImportedAt: s.now()}}
	m, ver, files, err := s.d.Catalog.RegisterGenerated(ctx, actor, m, "", "v1", "création paramétrique ("+in.Template+")", gen)
	if err != nil {
		return nil, err
	}
	return &CreateParametricOutput{ModelID: m.ID, VersionID: ver.ID, Files: files, Template: res.Template, Params: res.Params,
		Assumptions: nz(res.Assumptions), Warnings: nz(res.Warnings), BBoxMM: res.BBox, Derived: res.Derived, Level: res.Level,
		Note: "Modèle source paramétrique conservé (STEP + STL). Les dimensions sont des mesures calculées par le worker, pas une validation mécanique."}, nil
}

func nz(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ---- modify_geometry ----

// GeometryOp est une opération typée sur le maillage (repère en mm, Z vers le haut).
type GeometryOp struct {
	Op      string    `json:"op" jsonschema:"scale | rotate | mirror | translate | center_on_bed | lay_flat | cut_plane"`
	Factor  float64   `json:"factor,omitempty" jsonschema:"scale : facteur uniforme (> 0)"`
	Factors []float64 `json:"factors,omitempty" jsonschema:"scale : facteurs [x,y,z] (> 0)"`
	Axis    string    `json:"axis,omitempty" jsonschema:"rotate / cut_plane : x | y | z"`
	Degrees float64   `json:"degrees,omitempty" jsonschema:"rotate : angle en degrés (autour du centre de la boîte englobante)"`
	Plane   string    `json:"plane,omitempty" jsonschema:"mirror : xy | yz | xz (symétrie par rapport au plan passant par le centre)"`
	Vector  []float64 `json:"vector,omitempty" jsonschema:"translate : [dx,dy,dz] en mm"`
	Offset  *float64  `json:"offset,omitempty" jsonschema:"cut_plane : position du plan sur l'axe, en mm (repère courant)"`
	Keep    string    `json:"keep,omitempty" jsonschema:"cut_plane : above (côté + de l'axe) | below"`
	// center_on_bed : plateau par imprimante ou par dimensions explicites.
	PrinterID string  `json:"printer_id,omitempty"`
	BedX      float64 `json:"bed_x,omitempty"`
	BedY      float64 `json:"bed_y,omitempty"`
}

// ModifyGeometryInput est l'entrée de modify_geometry.
type ModifyGeometryInput struct {
	ModelID       string       `json:"model_id"`
	BaseVersionID string       `json:"base_version_id" jsonschema:"révision de départ ; refus si une version plus récente existe"`
	Operations    []GeometryOp `json:"operations"`
	Label         string       `json:"label,omitempty"`
}

// MeshStats : mesures géométriques d'un maillage.
type MeshStats struct {
	Triangles  int        `json:"triangles"`
	BBoxMin    [3]float64 `json:"bbox_min_mm"`
	BBoxMax    [3]float64 `json:"bbox_max_mm"`
	SizeMM     [3]float64 `json:"size_mm"`
	VolumeMM3  float64    `json:"volume_mm3"`
	AreaMM2    float64    `json:"area_mm2"`
	Watertight bool       `json:"watertight"`
	OpenEdges  int        `json:"open_edges"`
}

// AppliedOp résume une opération appliquée.
type AppliedOp struct {
	Op     string       `json:"op"`
	Detail string       `json:"detail"`
	Matrix *[12]float64 `json:"matrix,omitempty" jsonschema:"transformation 3x4 (convention 3MF) appliquée"`
}

// GeometryDiff : différences avant/après.
type GeometryDiff struct {
	Before        MeshStats  `json:"before"`
	After         MeshStats  `json:"after"`
	VolumeRatio   float64    `json:"volume_ratio"`
	SizeDeltaMM   [3]float64 `json:"size_delta_mm"`
	TrianglesDiff int        `json:"triangles_delta"`
}

// ModifyGeometryOutput : nouvelle version du modèle.
type ModifyGeometryOutput struct {
	ModelID         string               `json:"model_id"`
	VersionID       string               `json:"version_id"`
	ParentVersionID string               `json:"parent_version_id"`
	File            contracts.SourceFile `json:"file"`
	Applied         []AppliedOp          `json:"applied"`
	Diff            GeometryDiff         `json:"diff"`
	Warnings        []string             `json:"warnings"`
	Note            string               `json:"note"`
}

func stats(m *mesh.Mesh) MeshStats {
	r := mesh.Analyze(m, mesh.AnalyzeOptions{})
	b := m.Bounds()
	return MeshStats{Triangles: len(m.T), BBoxMin: [3]float64{b.Min.X, b.Min.Y, b.Min.Z}, BBoxMax: [3]float64{b.Max.X, b.Max.Y, b.Max.Z},
		SizeMM: [3]float64{b.Size.X, b.Size.Y, b.Size.Z}, VolumeMM3: r.Volume, AreaMM2: r.Area, Watertight: r.Watertight, OpenEdges: r.OpenEdges}
}

type m3 = [3][3]float64

func affine(R m3, c mesh.Vec3) mesh.Matrix {
	var m mesh.Matrix
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			m[i*3+j] = R[j][i]
		}
	}
	rc := mesh.Vec3{X: R[0][0]*c.X + R[0][1]*c.Y + R[0][2]*c.Z, Y: R[1][0]*c.X + R[1][1]*c.Y + R[1][2]*c.Z, Z: R[2][0]*c.X + R[2][1]*c.Y + R[2][2]*c.Z}
	m[9], m[10], m[11] = c.X-rc.X, c.Y-rc.Y, c.Z-rc.Z
	return m
}

func axisIndex(a string) (int, error) {
	switch strings.ToLower(a) {
	case "x":
		return 0, nil
	case "y":
		return 1, nil
	case "z":
		return 2, nil
	}
	return 0, invalidf("axe %q inconnu (x, y ou z)", a)
}

func rotAxis(axis int, deg float64) m3 {
	c, s := math.Cos(deg*math.Pi/180), math.Sin(deg*math.Pi/180)
	switch axis {
	case 0:
		return m3{{1, 0, 0}, {0, c, -s}, {0, s, c}}
	case 1:
		return m3{{c, 0, s}, {0, 1, 0}, {-s, 0, c}}
	}
	return m3{{c, -s, 0}, {s, c, 0}, {0, 0, 1}}
}

// rotBetween : rotation minimale amenant le vecteur unitaire a sur b.
func rotBetween(a, b mesh.Vec3) m3 {
	v := a.Cross(b)
	c := a.Dot(b)
	if c < -1+1e-9 { // opposés : demi-tour autour d'un axe perpendiculaire
		p := mesh.Vec3{X: 1}
		if math.Abs(a.X) > 0.9 {
			p = mesh.Vec3{Y: 1}
		}
		ax := a.Cross(p).Norm()
		return m3{{2*ax.X*ax.X - 1, 2 * ax.X * ax.Y, 2 * ax.X * ax.Z}, {2 * ax.X * ax.Y, 2*ax.Y*ax.Y - 1, 2 * ax.Y * ax.Z}, {2 * ax.X * ax.Z, 2 * ax.Y * ax.Z, 2*ax.Z*ax.Z - 1}}
	}
	k := 1 / (1 + c)
	return m3{
		{1 - k*(v.Y*v.Y+v.Z*v.Z), -v.Z + k*v.X*v.Y, v.Y + k*v.X*v.Z},
		{v.Z + k*v.X*v.Y, 1 - k*(v.X*v.X+v.Z*v.Z), -v.X + k*v.Y*v.Z},
		{-v.Y + k*v.X*v.Z, v.X + k*v.Y*v.Z, 1 - k*(v.X*v.X+v.Y*v.Y)},
	}
}

func center(b mesh.BBox) mesh.Vec3 { return b.Min.Add(b.Size.Scale(0.5)) }

// layFlat pose la plus grande face plane support sur le plateau (Z = 0) et renvoie la matrice.
func layFlat(m *mesh.Mesh) (mesh.Matrix, string, error) {
	type cl struct {
		n    mesh.Vec3
		area float64
		d    float64 // somme pondérée de v·n
	}
	groups := map[[3]int]*cl{}
	for i, t := range m.T {
		n, a := m.Normal(i)
		if a == 0 {
			continue
		}
		k := [3]int{int(math.Round(n.X * 200)), int(math.Round(n.Y * 200)), int(math.Round(n.Z * 200))}
		g := groups[k]
		if g == nil {
			g = &cl{n: n}
			groups[k] = g
		}
		g.area += a
		g.d += a * m.V[t[0]].Dot(n)
	}
	if len(groups) == 0 {
		return mesh.Identity, "", errors.New("aucune face exploitable")
	}
	list := make([]*cl, 0, len(groups))
	for _, g := range groups {
		list = append(list, g)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].area > list[j].area })
	if len(list) > 24 {
		list = list[:24]
	}
	bb := m.Bounds()
	tol := 1e-4 * math.Max(1, bb.Size.Len())
	best := -1
	for i, g := range list {
		d := g.d / g.area
		maxProj := math.Inf(-1)
		for _, v := range m.V {
			maxProj = math.Max(maxProj, v.Dot(g.n))
		}
		if maxProj <= d+tol { // face d'appui : tout le volume est de l'autre côté
			best = i
			break
		}
	}
	note := "plus grande face plane d'appui"
	if best < 0 { // maillage non convexe : face la plus large, sans garantie d'appui
		best, note = 0, "plus grande face plane (non convexe : l'appui n'est pas garanti)"
	}
	R := rotBetween(list[best].n, mesh.Vec3{Z: -1})
	rot := affine(R, mesh.Vec3{})
	rm := m.Transform(rot)
	b := rm.Bounds()
	cb := center(b)
	t := mesh.Translation(-cb.X, -cb.Y, -b.Min.Z) // centre XY ramené à l'origine, pose sur Z = 0
	return rot.Mul(t), note + fmt.Sprintf(" (%.1f mm², normale %.2f,%.2f,%.2f)", list[best].area, list[best].n.X, list[best].n.Y, list[best].n.Z), nil
}

func applyOps(ctx context.Context, s *Service, m *mesh.Mesh, ops []GeometryOp) (*mesh.Mesh, []AppliedOp, []string, error) {
	var applied []AppliedOp
	var warns []string
	for i, op := range ops {
		bb := m.Bounds()
		c := center(bb)
		label := fmt.Sprintf("op %d (%s)", i+1, op.Op)
		var mx mesh.Matrix
		switch op.Op {
		case "scale":
			f := [3]float64{op.Factor, op.Factor, op.Factor}
			if len(op.Factors) == 3 {
				copy(f[:], op.Factors)
			} else if op.Factor == 0 {
				return nil, nil, nil, invalidf("%s : factor ou factors [x,y,z] requis", label)
			}
			for _, v := range f {
				if !(v > 0) || math.IsInf(v, 0) {
					return nil, nil, nil, invalidf("%s : facteur %v invalide (> 0 ; utiliser mirror pour une symétrie)", label, v)
				}
			}
			mx = affine(m3{{f[0], 0, 0}, {0, f[1], 0}, {0, 0, f[2]}}, c)
			applied = append(applied, AppliedOp{Op: "scale", Detail: fmt.Sprintf("facteurs %.4g, %.4g, %.4g autour du centre de la boîte englobante", f[0], f[1], f[2])})
		case "rotate":
			ax, err := axisIndex(op.Axis)
			if err != nil {
				return nil, nil, nil, err
			}
			if math.IsNaN(op.Degrees) || math.IsInf(op.Degrees, 0) {
				return nil, nil, nil, invalidf("%s : angle invalide", label)
			}
			mx = affine(rotAxis(ax, op.Degrees), c)
			applied = append(applied, AppliedOp{Op: "rotate", Detail: fmt.Sprintf("%.4g° autour de l'axe %s, centre de la boîte englobante", op.Degrees, op.Axis)})
		case "mirror":
			R := m3{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
			switch strings.ToLower(op.Plane) {
			case "xy":
				R[2][2] = -1
			case "yz":
				R[0][0] = -1
			case "xz":
				R[1][1] = -1
			default:
				return nil, nil, nil, invalidf("%s : plan %q inconnu (xy, yz, xz)", label, op.Plane)
			}
			mx = affine(R, c)
			applied = append(applied, AppliedOp{Op: "mirror", Detail: "symétrie par rapport au plan " + op.Plane + " passant par le centre ; orientation des triangles corrigée"})
		case "translate":
			if len(op.Vector) != 3 {
				return nil, nil, nil, invalidf("%s : vector [dx,dy,dz] requis", label)
			}
			mx = mesh.Translation(op.Vector[0], op.Vector[1], op.Vector[2])
			applied = append(applied, AppliedOp{Op: "translate", Detail: fmt.Sprintf("(%.4g, %.4g, %.4g) mm", op.Vector[0], op.Vector[1], op.Vector[2])})
		case "center_on_bed":
			bx, by := op.BedX, op.BedY
			if op.PrinterID != "" {
				rec, err := s.GetPrinterRecord(ctx, op.PrinterID)
				if err != nil {
					return nil, nil, nil, err
				}
				pm, err := s.GetPrinterModel(ctx, rec.ModelID)
				if err != nil || pm.BuildVolume == nil {
					return nil, nil, nil, fmt.Errorf("%w : volume d'impression de %s inconnu ; fournir bed_x et bed_y", ErrUnavailable, rec.Name)
				}
				bx, by = pm.BuildVolume.X, pm.BuildVolume.Y
			}
			if bx <= 0 || by <= 0 {
				return nil, nil, nil, invalidf("%s : printer_id ou bed_x/bed_y requis", label)
			}
			mx = mesh.Translation(bx/2-c.X, by/2-c.Y, -bb.Min.Z)
			applied = append(applied, AppliedOp{Op: "center_on_bed", Detail: fmt.Sprintf("centré sur un plateau %.0f x %.0f mm, posé sur Z=0", bx, by)})
			if bb.Size.X > bx || bb.Size.Y > by {
				warns = append(warns, fmt.Sprintf("%s : la pièce (%.1f x %.1f mm) dépasse le plateau", label, bb.Size.X, bb.Size.Y))
			}
		case "lay_flat":
			t, note, err := layFlat(m)
			if err != nil {
				return nil, nil, nil, invalidf("%s : %v", label, err)
			}
			mx = t
			applied = append(applied, AppliedOp{Op: "lay_flat", Detail: note + " ; centré en XY sur l'origine, Z min = 0"})
			if strings.Contains(note, "non convexe") {
				warns = append(warns, label+" : "+note)
			}
		case "cut_plane":
			ax, err := axisIndex(op.Axis)
			if err != nil {
				return nil, nil, nil, err
			}
			if op.Offset == nil {
				return nil, nil, nil, invalidf("%s : offset requis", label)
			}
			keep := strings.ToLower(op.Keep)
			if keep != "above" && keep != "below" {
				return nil, nil, nil, invalidf("%s : keep doit valoir above ou below", label)
			}
			cut, detail, w, err := cutPlane(m, ax, *op.Offset, keep == "above")
			if err != nil {
				return nil, nil, nil, invalidf("%s : %v", label, err)
			}
			m = cut
			applied = append(applied, AppliedOp{Op: "cut_plane", Detail: detail})
			warns = append(warns, w...)
			continue
		default:
			return nil, nil, nil, invalidf("%s : opération inconnue (scale, rotate, mirror, translate, center_on_bed, lay_flat, cut_plane)", label)
		}
		m = m.Transform(mx)
		mm := [12]float64(mx)
		applied[len(applied)-1].Matrix = &mm
	}
	return m, applied, warns, nil
}

// ModifyGeometry applique des opérations typées et enregistre une NOUVELLE version (STL) du modèle.
func (s *Service) ModifyGeometry(ctx context.Context, in ModifyGeometryInput) (*ModifyGeometryOutput, error) {
	if s.d.Catalog == nil {
		return nil, fmt.Errorf("%w : catalogue absent", ErrUnavailable)
	}
	if in.ModelID == "" || in.BaseVersionID == "" || len(in.Operations) == 0 {
		return nil, invalidf("model_id, base_version_id et au moins une opération requis")
	}
	if len(in.Operations) > 32 {
		return nil, invalidf("32 opérations au plus par appel")
	}
	vs, err := s.d.Store.ListVersions(ctx, in.ModelID)
	if err != nil {
		return nil, fmt.Errorf("%w : modèle %q", ErrNotFound, in.ModelID)
	}
	if len(vs) > 0 && vs[0].ID != in.BaseVersionID {
		return nil, fmt.Errorf("%w : révision périmée : base %s, version courante %s — relire le modèle et recommencer", ErrState, in.BaseVersionID, vs[0].ID)
	}
	_, src, err := s.sourceFor(ctx, in.ModelID, in.BaseVersionID)
	if err != nil {
		return nil, err
	}
	f, err := s.d.Blobs.Open(src.SHA256)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, 512<<20))
	f.Close()
	if err != nil {
		return nil, err
	}
	md, err := mesh.Parse(src.Name, data)
	if err != nil {
		return nil, invalidf("maillage illisible : %v", err)
	}
	base := md.Combined()
	var warns []string
	if len(md.Objects) > 1 {
		warns = append(warns, fmt.Sprintf("%d objets fusionnés en un seul maillage", len(md.Objects)))
	}
	if src.Format == contracts.Format3MF {
		warns = append(warns, "le fichier de départ est un 3MF : la nouvelle version est un STL (les métadonnées de projet ne sont pas reprises) ; l'original reste intact")
	}
	before := stats(base)
	out, applied, w, err := applyOps(ctx, s, base, in.Operations)
	if err != nil {
		return nil, err
	}
	warns = append(warns, w...)
	if len(out.T) == 0 {
		return nil, invalidf("le résultat est vide")
	}
	if err := out.Validate(); err != nil {
		return nil, invalidf("maillage résultant invalide : %v", err)
	}
	after := stats(out)
	if !after.Watertight {
		warns = append(warns, fmt.Sprintf("le maillage résultant n'est pas étanche (%d arêtes ouvertes)", after.OpenEdges))
	}
	stl, err := mesh.STLBytes(out)
	if err != nil {
		return nil, err
	}
	sha, size, err := s.d.Blobs.Put(bytes.NewReader(stl), "")
	if err != nil {
		return nil, err
	}
	label := in.Label
	if label == "" {
		label = fmt.Sprintf("v%d", len(vs)+1)
	}
	stem := cleanStem(strings.TrimSuffix(src.Name, filepath.Ext(src.Name)))
	name := fmt.Sprintf("%s_v%d.stl", stem, len(vs)+1)
	ops := make([]string, len(applied))
	for i, a := range applied {
		ops[i] = a.Op
	}
	_, ver, files, err := s.d.Catalog.RegisterGenerated(ctx, actor, contracts.Model{ID: in.ModelID}, in.BaseVersionID, label,
		"modify_geometry : "+strings.Join(ops, ", "), []catalog.GeneratedFile{{Name: name, Format: contracts.FormatSTL, SHA256: sha, Size: size, Unit: "mm"}})
	if err != nil {
		return nil, err
	}
	ratio := 0.0
	if before.VolumeMM3 != 0 {
		ratio = after.VolumeMM3 / before.VolumeMM3
	}
	diff := GeometryDiff{Before: before, After: after, VolumeRatio: ratio, TrianglesDiff: after.Triangles - before.Triangles}
	for i := 0; i < 3; i++ {
		diff.SizeDeltaMM[i] = after.SizeMM[i] - before.SizeMM[i]
	}
	return &ModifyGeometryOutput{ModelID: in.ModelID, VersionID: ver.ID, ParentVersionID: in.BaseVersionID, File: files[0], Applied: applied, Diff: diff,
		Warnings: nz(warns), Note: "Nouvelle version immuable ; la version " + in.BaseVersionID + " et son fichier d'origine sont intacts."}, nil
}

var _ = time.Second
