package mesh

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
)

// WriteSTLBinary écrit un STL binaire (normales recalculées). Le maillage est écrit tel quel,
// sans réparation.
func WriteSTLBinary(w io.Writer, m *Mesh) error {
	if err := m.Validate(); err != nil {
		return err
	}
	hdr := make([]byte, 80)
	copy(hdr, "BAMBULAB-MCP mesh export (binary STL, mm)")
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	var cnt [4]byte
	binary.LittleEndian.PutUint32(cnt[:], uint32(len(m.T)))
	if _, err := w.Write(cnt[:]); err != nil {
		return err
	}
	rec := make([]byte, 50)
	put := func(o int, v float64) { binary.LittleEndian.PutUint32(rec[o:], math.Float32bits(float32(v))) }
	for i, t := range m.T {
		n, _ := m.Normal(i)
		put(0, n.X)
		put(4, n.Y)
		put(8, n.Z)
		for k := 0; k < 3; k++ {
			v := m.V[t[k]]
			put(12+12*k, v.X)
			put(16+12*k, v.Y)
			put(20+12*k, v.Z)
		}
		rec[48], rec[49] = 0, 0
		if _, err := w.Write(rec); err != nil {
			return err
		}
	}
	return nil
}

// STLBytes retourne le STL binaire en mémoire.
func STLBytes(m *Mesh) ([]byte, error) {
	var b bytes.Buffer
	if err := WriteSTLBinary(&b, m); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// GLBOptions règle l'export glTF binaire.
type GLBOptions struct {
	// MaxTriangles > 0 : décimation par regroupement de sommets en grille jusqu'à ce seuil (aperçu).
	MaxTriangles int
}

// GLBResult est un GLB et ce qu'il représente réellement.
type GLBResult struct {
	Data              []byte `json:"-"`
	SHA256            string `json:"sha256"`
	SourceTriangles   int    `json:"source_triangles"`
	ExportedTriangles int    `json:"exported_triangles"`
	// Decimated : le GLB est un aperçu simplifié ; il ne doit jamais servir à fabriquer.
	Decimated bool    `json:"decimated"`
	CellMM    float64 `json:"decimation_cell_mm,omitempty"`
	Note      string  `json:"note"`
}

// Decimate simplifie par regroupement de sommets : chaque sommet est affecté à une cellule d'une
// grille de pas cell (mm), chaque cellule devient un sommet (moyenne), les triangles dégénérés sont retirés.
// Grossier et sans conservation de la topologie : réservé à l'aperçu.
func Decimate(m *Mesh, cell float64) *Mesh {
	if cell <= 0 {
		return m.Clone()
	}
	bb := m.Bounds()
	type key [3]int64
	ids := map[key]int{}
	var sums []Vec3
	var cnts []int
	remap := make([]int, len(m.V))
	for i, v := range m.V {
		k := key{int64(math.Floor((v.X - bb.Min.X) / cell)), int64(math.Floor((v.Y - bb.Min.Y) / cell)), int64(math.Floor((v.Z - bb.Min.Z) / cell))}
		id, ok := ids[k]
		if !ok {
			id = len(sums)
			ids[k] = id
			sums = append(sums, Vec3{})
			cnts = append(cnts, 0)
		}
		sums[id] = sums[id].Add(v)
		cnts[id]++
		remap[i] = id
	}
	out := &Mesh{V: make([]Vec3, len(sums))}
	for i := range sums {
		out.V[i] = sums[i].Scale(1 / float64(cnts[i]))
	}
	for _, t := range m.T {
		a, b, c := remap[t[0]], remap[t[1]], remap[t[2]]
		if a == b || b == c || a == c {
			continue
		}
		out.T = append(out.T, Tri{a, b, c})
	}
	return compact(out)
}

// decimateTo cherche un pas de grille donnant au plus max triangles.
func decimateTo(m *Mesh, max int) (*Mesh, float64) {
	bb := m.Bounds()
	diag := bb.Size.Len()
	if diag == 0 {
		return m, 0
	}
	g := math.Max(4, math.Sqrt(float64(max)/2))
	var out *Mesh
	var cell float64
	for i := 0; i < 40; i++ {
		cell = diag / g
		out = Decimate(m, cell)
		if len(out.T) <= max {
			return out, cell
		}
		g *= 0.8
	}
	return out, cell
}

// GLB exporte les objets d'un modèle en glTF 2.0 binaire pour le viewer : un nœud par objet,
// positions float32 indexées en uint32, sans normales (ombrage plat par le viewer). Conversion Z-haut
// (impression) vers Y-haut (glTF) et mm vers mètres ; extras.asset documente ces conversions.
func (md *Model) GLB(opt GLBOptions) (*GLBResult, error) {
	var meshes []*Mesh
	var names []string
	for _, o := range md.Objects {
		if o.Mesh != nil && len(o.Mesh.T) > 0 {
			meshes = append(meshes, o.Mesh)
			names = append(names, o.Name)
		}
	}
	return buildGLB(meshes, names, opt)
}

// GLB exporte un seul maillage.
func (m *Mesh) GLB(name string, opt GLBOptions) (*GLBResult, error) {
	return buildGLB([]*Mesh{m}, []string{name}, opt)
}

func buildGLB(meshes []*Mesh, names []string, opt GLBOptions) (*GLBResult, error) {
	if len(meshes) == 0 {
		return nil, ErrEmpty
	}
	res := &GLBResult{}
	total := 0
	for _, m := range meshes {
		if err := m.Validate(); err != nil {
			return nil, err
		}
		total += len(m.T)
	}
	res.SourceTriangles = total
	use := meshes
	if opt.MaxTriangles > 0 && total > opt.MaxTriangles {
		use = make([]*Mesh, len(meshes))
		var cell float64
		for i, m := range meshes {
			budget := int(math.Max(4, float64(opt.MaxTriangles)*float64(len(m.T))/float64(total)))
			d, c := decimateTo(m, budget)
			use[i] = d
			cell = math.Max(cell, c)
		}
		res.Decimated, res.CellMM = true, cell
	}

	var bin bytes.Buffer
	type bufView struct {
		ByteOffset int `json:"byteOffset"`
		ByteLength int `json:"byteLength"`
		Target     int `json:"target"`
		Buffer     int `json:"buffer"`
	}
	type accessor struct {
		BufferView    int       `json:"bufferView"`
		ComponentType int       `json:"componentType"`
		Count         int       `json:"count"`
		Type          string    `json:"type"`
		Min           []float64 `json:"min,omitempty"`
		Max           []float64 `json:"max,omitempty"`
	}
	var views []bufView
	var accs []accessor
	var gmeshes, nodes []map[string]any
	exported := 0
	for i, m := range use {
		if len(m.T) == 0 {
			continue
		}
		exported += len(m.T)
		lo := [3]float32{float32(math.Inf(1)), float32(math.Inf(1)), float32(math.Inf(1))}
		hi := [3]float32{float32(math.Inf(-1)), float32(math.Inf(-1)), float32(math.Inf(-1))}
		pv := bufView{ByteOffset: bin.Len(), Target: 34962}
		for _, v := range m.V {
			// Z-haut → Y-haut : (x, y, z) → (x, z, −y) ; mm → m
			p := [3]float32{float32(v.X * 0.001), float32(v.Z * 0.001), float32(-v.Y * 0.001)}
			for k := 0; k < 3; k++ {
				lo[k] = min(lo[k], p[k])
				hi[k] = max(hi[k], p[k])
				_ = binary.Write(&bin, binary.LittleEndian, p[k])
			}
		}
		pv.ByteLength = bin.Len() - pv.ByteOffset
		views = append(views, pv)
		accs = append(accs, accessor{BufferView: len(views) - 1, ComponentType: 5126, Count: len(m.V), Type: "VEC3",
			Min: []float64{float64(lo[0]), float64(lo[1]), float64(lo[2])}, Max: []float64{float64(hi[0]), float64(hi[1]), float64(hi[2])}})
		posAcc := len(accs) - 1
		iv := bufView{ByteOffset: bin.Len(), Target: 34963}
		for _, t := range m.T {
			for k := 0; k < 3; k++ {
				_ = binary.Write(&bin, binary.LittleEndian, uint32(t[k]))
			}
		}
		iv.ByteLength = bin.Len() - iv.ByteOffset
		views = append(views, iv)
		accs = append(accs, accessor{BufferView: len(views) - 1, ComponentType: 5125, Count: len(m.T) * 3, Type: "SCALAR"})
		idxAcc := len(accs) - 1
		name := fmt.Sprintf("object_%d", i)
		if i < len(names) && names[i] != "" {
			name = names[i]
		}
		gmeshes = append(gmeshes, map[string]any{
			"name":       name,
			"primitives": []any{map[string]any{"attributes": map[string]any{"POSITION": posAcc}, "indices": idxAcc, "material": 0, "mode": 4}},
		})
		nodes = append(nodes, map[string]any{"name": name, "mesh": len(gmeshes) - 1})
	}
	if len(gmeshes) == 0 {
		return nil, ErrEmpty
	}
	res.ExportedTriangles = exported
	sceneNodes := make([]int, len(nodes))
	for i := range sceneNodes {
		sceneNodes[i] = i
	}
	res.Note = "GLB pour visualisation uniquement ; le fichier source reste le modèle de fabrication."
	if res.Decimated {
		res.Note = fmt.Sprintf("APERÇU décimé (%d → %d triangles, grille %.3g mm) : ne pas utiliser pour fabriquer ni mesurer. ", res.SourceTriangles, res.ExportedTriangles, res.CellMM) + res.Note
	}
	doc := map[string]any{
		"asset": map[string]any{"version": "2.0", "generator": "BAMBULAB-MCP internal/mesh",
			"extras": map[string]any{"source_unit": "millimeter", "scale_applied": 0.001, "axis_conversion": "Z-up to Y-up (x,z,-y)",
				"decimated": res.Decimated, "source_triangles": res.SourceTriangles, "exported_triangles": res.ExportedTriangles, "note": res.Note}},
		"scene":       0,
		"scenes":      []any{map[string]any{"nodes": sceneNodes}},
		"nodes":       nodes,
		"meshes":      gmeshes,
		"materials":   []any{map[string]any{"name": "default", "pbrMetallicRoughness": map[string]any{"baseColorFactor": []float64{0.75, 0.75, 0.78, 1}, "metallicFactor": 0.0, "roughnessFactor": 0.8}}},
		"accessors":   accs,
		"bufferViews": views,
		"buffers":     []any{map[string]any{"byteLength": bin.Len()}},
	}
	js, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	for len(js)%4 != 0 {
		js = append(js, ' ')
	}
	binData := bin.Bytes()
	for len(binData)%4 != 0 {
		binData = append(binData, 0)
	}
	var out bytes.Buffer
	w32 := func(v uint32) { _ = binary.Write(&out, binary.LittleEndian, v) }
	w32(0x46546C67) // "glTF"
	w32(2)
	w32(uint32(12 + 8 + len(js) + 8 + len(binData)))
	w32(uint32(len(js)))
	w32(0x4E4F534A) // JSON
	out.Write(js)
	w32(uint32(len(binData)))
	w32(0x004E4942) // BIN
	out.Write(binData)
	res.Data = out.Bytes()
	res.SHA256 = SHA256Hex(res.Data)
	return res, nil
}
