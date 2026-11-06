package mesh

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"path"
	"sort"
	"strconv"
	"strings"
)

type xMeta struct {
	Name  string `xml:"name,attr"`
	Value string `xml:",chardata"`
}

type xVertex struct {
	X float64 `xml:"x,attr"`
	Y float64 `xml:"y,attr"`
	Z float64 `xml:"z,attr"`
}

type xTriangle struct {
	V1 int `xml:"v1,attr"`
	V2 int `xml:"v2,attr"`
	V3 int `xml:"v3,attr"`
}

type xMesh struct {
	Vertices  []xVertex   `xml:"vertices>vertex"`
	Triangles []xTriangle `xml:"triangles>triangle"`
}

type xComponent struct {
	ObjectID  string `xml:"objectid,attr"`
	Path      string `xml:"path,attr"`
	Transform string `xml:"transform,attr"`
}

type xObject struct {
	ID         string       `xml:"id,attr"`
	Type       string       `xml:"type,attr"`
	Name       string       `xml:"name,attr"`
	PartNumber string       `xml:"partnumber,attr"`
	Meta       []xMeta      `xml:"metadatagroup>metadata"`
	Mesh       *xMesh       `xml:"mesh"`
	Components []xComponent `xml:"components>component"`
}

type xItem struct {
	ObjectID   string `xml:"objectid,attr"`
	Path       string `xml:"path,attr"`
	Transform  string `xml:"transform,attr"`
	PartNumber string `xml:"partnumber,attr"`
}

type xModel struct {
	XMLName   xml.Name  `xml:"model"`
	Unit      string    `xml:"unit,attr"`
	Metadata  []xMeta   `xml:"metadata"`
	Objects   []xObject `xml:"resources>object"`
	BaseMats  []xMeta   `xml:"resources>basematerials"`
	ColorGrps []xMeta   `xml:"resources>colorgroup"`
	Textures  []xMeta   `xml:"resources>texture2d"`
	Items     []xItem   `xml:"build>item"`
}

var unitScale = map[string]float64{
	"": 1, "micron": 0.001, "millimeter": 1, "centimeter": 10, "inch": 25.4, "foot": 304.8, "meter": 1000,
}

func parseTransform(s string) (Matrix, error) {
	if strings.TrimSpace(s) == "" {
		return Identity, nil
	}
	f := strings.Fields(s)
	if len(f) != 12 {
		return Identity, fmt.Errorf("transformation 3MF invalide (%d valeurs au lieu de 12) : %q", len(f), s)
	}
	var m Matrix
	for i, t := range f {
		v, err := strconv.ParseFloat(t, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return Identity, fmt.Errorf("transformation 3MF invalide : %q", t)
		}
		m[i] = v
	}
	return m, nil
}

type tmfReader struct {
	zr      *zip.Reader
	files   map[string]*zip.File
	models  map[string]*xModel
	total   int64
	scale   float64
	nodes   int // résolutions d'objets effectuées (mémoïsées comprises)
	vtot    int // sommets cumulés produits
	ttot    int // triangles cumulés produits
	memo    map[string]*memoEntry
	used    map[string]bool
	warns   []string
	warnSet map[string]bool
}

type memoEntry struct {
	m   *Mesh
	obj *xObject
}

// charge comptabilise de la géométrie produite (cumul global) et échoue au-delà
// des plafonds : borne le travail même si l'arborescence de composants explose.
func (r *tmfReader) charge(nv, nt int) error {
	r.vtot += nv
	r.ttot += nt
	if r.ttot > MaxTriangles || r.vtot > MaxVertices {
		return fmt.Errorf("%w : géométrie 3MF cumulée trop volumineuse (limites : %d triangles, %d sommets)", ErrFormat, MaxTriangles, MaxVertices)
	}
	return nil
}

func (r *tmfReader) warn(s string) {
	if !r.warnSet[s] {
		r.warnSet[s] = true
		r.warns = append(r.warns, s)
	}
}

func normPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	return strings.TrimPrefix(path.Clean("/"+p), "/")
}

// safeEntryName valide le nom d'une entrée de l'archive : pas de chemin absolu,
// de lettre de lecteur, d'octet nul ni de segment « .. » (un nom comme « a..b »
// reste légitime). Renvoie le nom normalisé.
func safeEntryName(name string) (string, error) {
	p := strings.ReplaceAll(name, "\\", "/")
	if p == "" || strings.ContainsRune(p, 0) || strings.HasPrefix(p, "/") || (len(p) >= 2 && p[1] == ':') {
		return "", fmt.Errorf("%w : chemin d'entrée suspect %q", ErrFormat, name)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", fmt.Errorf("%w : chemin d'entrée suspect %q", ErrFormat, name)
		}
	}
	return normPath(p), nil
}

func (r *tmfReader) read(name string) ([]byte, error) {
	f := r.files[name]
	if f == nil {
		return nil, fmt.Errorf("%w : entrée %q absente de l'archive 3MF", ErrFormat, name)
	}
	if f.UncompressedSize64 > maxZipEntryBytes {
		return nil, fmt.Errorf("%w : entrée %q trop volumineuse (%d octets)", ErrFormat, name, f.UncompressedSize64)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%w : %s : %v", ErrFormat, name, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, maxZipEntryBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w : %s : %v", ErrTruncated, name, err)
	}
	if int64(len(b)) > maxZipEntryBytes {
		return nil, fmt.Errorf("%w : entrée %q trop volumineuse", ErrFormat, name)
	}
	r.total += int64(len(b))
	if r.total > maxZipTotalBytes {
		return nil, fmt.Errorf("%w : archive décompressée trop volumineuse", ErrFormat)
	}
	return b, nil
}

func (r *tmfReader) model(name string) (*xModel, error) {
	if m, ok := r.models[name]; ok {
		return m, nil
	}
	b, err := r.read(name)
	if err != nil {
		return nil, err
	}
	var m xModel
	dec := xml.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%w : XML invalide dans %s : %v", ErrFormat, name, err)
	}
	if m.XMLName.Local != "model" {
		return nil, fmt.Errorf("%w : %s n'est pas un document <model>", ErrFormat, name)
	}
	r.models[name] = &m
	r.used[name] = true
	return &m, nil
}

func (r *tmfReader) rootModelPath() string {
	if b, err := r.read("_rels/.rels"); err == nil {
		var rels struct {
			Rel []struct {
				Target string `xml:"Target,attr"`
				Type   string `xml:"Type,attr"`
			} `xml:"Relationship"`
		}
		if xml.Unmarshal(b, &rels) == nil {
			for _, rl := range rels.Rel {
				if strings.HasSuffix(rl.Type, "/3dmodel") || strings.HasSuffix(rl.Type, "3dmodel") {
					p := normPath(rl.Target)
					if r.files[p] != nil {
						r.used["_rels/.rels"] = true
						return p
					}
				}
			}
		}
	}
	for _, c := range []string{"3D/3dmodel.model", "3D/3DModel.model"} {
		if r.files[c] != nil {
			return c
		}
	}
	// dernier recours : premier .model
	var names []string
	for n := range r.files {
		if strings.HasSuffix(strings.ToLower(n), ".model") {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	if len(names) > 0 {
		return names[0]
	}
	return ""
}

// meshOf construit le maillage (en unités du fichier) de l'objet id du fichier file,
// composants résolus récursivement. Le résultat est mémoïsé (le maillage renvoyé ne
// doit jamais être modifié) ; chaque résolution et chaque copie de géométrie sont
// comptabilisées globalement (maxVisitedNodes, MaxTriangles, MaxVertices).
func (r *tmfReader) meshOf(file, id string, depth int, visiting map[string]bool) (*Mesh, *xObject, error) {
	r.nodes++
	if r.nodes > maxVisitedNodes {
		return nil, nil, fmt.Errorf("%w : trop d'objets/composants 3MF à résoudre (limite %d)", ErrFormat, maxVisitedNodes)
	}
	if depth > 16 {
		return nil, nil, fmt.Errorf("%w : imbrication de composants trop profonde", ErrFormat)
	}
	key := file + "#" + id
	if e, ok := r.memo[key]; ok {
		return e.m, e.obj, nil
	}
	if visiting[key] {
		return nil, nil, fmt.Errorf("%w : composants circulaires (%s)", ErrFormat, key)
	}
	visiting[key] = true
	defer delete(visiting, key)
	md, err := r.model(file)
	if err != nil {
		return nil, nil, err
	}
	var obj *xObject
	for i := range md.Objects {
		if md.Objects[i].ID == id {
			obj = &md.Objects[i]
			break
		}
	}
	if obj == nil {
		return nil, nil, fmt.Errorf("%w : objet %q introuvable dans %s", ErrFormat, id, file)
	}
	out := &Mesh{}
	if obj.Mesh != nil {
		if err := r.charge(len(obj.Mesh.Vertices), len(obj.Mesh.Triangles)); err != nil {
			return nil, nil, err
		}
		out.V = make([]Vec3, 0, len(obj.Mesh.Vertices))
		for i, v := range obj.Mesh.Vertices {
			if !finite3(v.X, v.Y, v.Z) {
				return nil, nil, fmt.Errorf("%w : objet %s, sommet %d non fini", ErrFormat, id, i)
			}
			out.V = append(out.V, Vec3{v.X, v.Y, v.Z})
		}
		out.T = make([]Tri, 0, len(obj.Mesh.Triangles))
		for i, t := range obj.Mesh.Triangles {
			for _, k := range []int{t.V1, t.V2, t.V3} {
				if k < 0 || k >= len(out.V) {
					return nil, nil, fmt.Errorf("%w : objet %s, triangle %d : index de sommet %d hors limites", ErrFormat, id, i, k)
				}
			}
			out.T = append(out.T, Tri{t.V1, t.V2, t.V3})
		}
	}
	for _, c := range obj.Components {
		cf := file
		if c.Path != "" {
			cf = normPath(c.Path)
		}
		sub, _, err := r.meshOf(cf, c.ObjectID, depth+1, visiting)
		if err != nil {
			return nil, nil, err
		}
		tm, err := parseTransform(c.Transform)
		if err != nil {
			return nil, nil, fmt.Errorf("%w : %v", ErrFormat, err)
		}
		if err := r.charge(len(sub.V), len(sub.T)); err != nil {
			return nil, nil, err
		}
		out.Append(sub.Transform(tm))
	}
	r.memo[key] = &memoEntry{m: out, obj: obj}
	return out, obj, nil
}

// ParseThreeMF lit une archive 3MF (3D Manufacturing Format Core + extension Production
// de Bambu Studio / OrcaSlicer). Les transformations de composants et d'items de build sont
// appliquées, les coordonnées converties en mm. Les fichiers non interprétés (Metadata/*.config,
// miniatures, G-code embarqué) sont listés dans Extras.
func ParseThreeMF(data []byte) (*Model, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%w : archive zip invalide : %v", ErrFormat, err)
	}
	if len(zr.File) > maxZipEntries {
		return nil, fmt.Errorf("%w : trop d'entrées dans l'archive (%d)", ErrFormat, len(zr.File))
	}
	r := &tmfReader{zr: zr, files: map[string]*zip.File{}, models: map[string]*xModel{}, memo: map[string]*memoEntry{}, used: map[string]bool{}, warnSet: map[string]bool{}}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		n, err := safeEntryName(f.Name)
		if err != nil {
			return nil, err
		}
		if r.files[n] != nil {
			return nil, fmt.Errorf("%w : entrée d'archive dupliquée %q", ErrFormat, n)
		}
		r.files[n] = f
	}
	rootPath := r.rootModelPath()
	if rootPath == "" {
		return nil, fmt.Errorf("%w : aucun fichier .model dans l'archive 3MF", ErrFormat)
	}
	root, err := r.model(rootPath)
	if err != nil {
		return nil, err
	}
	scale, ok := unitScale[strings.ToLower(root.Unit)]
	if !ok {
		return nil, fmt.Errorf("%w : unité 3MF %q inconnue", ErrFormat, root.Unit)
	}
	unit := strings.ToLower(root.Unit)
	if unit == "" {
		unit = "millimeter"
	}
	md := &Model{Format: "3mf", Unit: unit, ScaleToMM: scale, Metadata: map[string]string{}}
	for _, m := range root.Metadata {
		if m.Name != "" {
			md.Metadata[m.Name] = strings.TrimSpace(m.Value)
		}
	}
	if len(root.BaseMats)+len(root.ColorGrps)+len(root.Textures) > 0 {
		md.Warnings = append(md.Warnings, "3MF : matériaux/couleurs/textures présents mais non représentés par le maillage lu")
	}

	type item struct {
		file, id, transform, part string
	}
	var items []item
	for _, it := range root.Items {
		f := rootPath
		if it.Path != "" {
			f = normPath(it.Path)
		}
		items = append(items, item{f, it.ObjectID, it.Transform, it.PartNumber})
	}
	if len(items) == 0 {
		md.Warnings = append(md.Warnings, "3MF sans élément <build> : tous les objets de la ressource sont pris sans transformation")
		for _, o := range root.Objects {
			items = append(items, item{rootPath, o.ID, "", ""})
		}
	}
	for _, it := range items {
		sub, obj, err := r.meshOf(it.file, it.id, 0, map[string]bool{})
		if err != nil {
			return nil, err
		}
		if len(sub.T) == 0 {
			md.Warnings = append(md.Warnings, fmt.Sprintf("3MF : objet %s sans triangles, ignoré", it.id))
			continue
		}
		tm, err := parseTransform(it.transform)
		if err != nil {
			return nil, fmt.Errorf("%w : %v", ErrFormat, err)
		}
		if err := r.charge(len(sub.V), len(sub.T)); err != nil {
			return nil, err
		}
		world := sub.Transform(tm)
		if scale != 1 {
			world = world.Transform(Scaling(scale))
		}
		name := obj.Name
		if name == "" {
			name = "object_" + it.id
		}
		o := Object{ID: it.id, Name: name, Type: obj.Type, Mesh: world}
		if o.Type == "" {
			o.Type = "model"
		}
		if obj.PartNumber != "" || it.part != "" {
			o.Metadata = map[string]string{}
			if obj.PartNumber != "" {
				o.Metadata["partnumber"] = obj.PartNumber
			}
			if it.part != "" {
				o.Metadata["item_partnumber"] = it.part
			}
		}
		for _, m := range obj.Meta {
			if o.Metadata == nil {
				o.Metadata = map[string]string{}
			}
			o.Metadata[m.Name] = strings.TrimSpace(m.Value)
		}
		md.Objects = append(md.Objects, o)
	}
	if len(md.Objects) == 0 {
		return nil, ErrEmpty
	}
	if err := validateModel(md); err != nil {
		return nil, err
	}
	// Extras : tout ce qui n'est pas un .model lu ni un fichier de structure OPC.
	var extras []string
	for n := range r.files {
		if r.used[n] || n == "[Content_Types].xml" || strings.HasPrefix(n, "_rels/") {
			continue
		}
		extras = append(extras, n)
	}
	sort.Strings(extras)
	md.Extras = extras
	for _, e := range extras {
		l := strings.ToLower(e)
		if strings.HasSuffix(l, "slice_info.config") || strings.HasSuffix(l, "model_settings.config") || strings.Contains(l, "plate_") {
			md.Warnings = append(md.Warnings, "3MF projet Bambu/Orca : réglages, plaques et métadonnées (Metadata/*) non interprétés ; conserver l'original intact")
			break
		}
	}
	md.Warnings = append(md.Warnings, r.warns...)
	return md, nil
}
