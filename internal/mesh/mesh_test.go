package mesh

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
)

func unweld(m *Mesh) *Mesh {
	o := &Mesh{}
	for _, t := range m.T {
		b := len(o.V)
		o.V = append(o.V, m.V[t[0]], m.V[t[1]], m.V[t[2]])
		o.T = append(o.T, Tri{b, b + 1, b + 2})
	}
	return o
}

func TestCubeAnalysis(t *testing.T) {
	r := Analyze(Cube(10), AnalyzeOptions{})
	near(t, "volume", r.Volume, 1000, 1e-9)
	near(t, "aire", r.Area, 600, 1e-9)
	if r.Triangles != 12 || r.WeldedVertices != 8 || !r.Watertight || r.Components != 1 {
		t.Errorf("%+v", r)
	}
	if r.OpenEdges != 0 || r.NonManifoldEdges != 0 || r.InconsistentEdges != 0 || r.DegenerateTriangles != 0 || r.InvertedNormals {
		t.Errorf("topologie: %+v", r)
	}
	near(t, "bbox", r.BBox.Size.Z, 10, 0)
	if len(r.Warnings) != 0 {
		t.Errorf("avertissements inattendus %v", r.Warnings)
	}
}

func TestUnweldedCubeIsWatertightAfterWeld(t *testing.T) {
	m := unweld(Cube(10))
	if len(m.V) != 36 {
		t.Fatal("setup")
	}
	r := Analyze(m, AnalyzeOptions{})
	if r.WeldedVertices != 8 || !r.Watertight {
		t.Errorf("soudure: %d sommets, étanche=%v", r.WeldedVertices, r.Watertight)
	}
	// Bruit sous la tolérance : toujours étanche.
	for i := range m.V {
		m.V[i].X += 1e-8 * float64(i%3)
	}
	if r := Analyze(m, AnalyzeOptions{WeldTol: 1e-5}); !r.Watertight {
		t.Error("bruit 1e-8 avec tolérance 1e-5 doit rester étanche")
	}
	// Tolérance trop fine : non étanche.
	if r := Analyze(m, AnalyzeOptions{WeldTol: 1e-12}); r.Watertight {
		t.Error("tolérance 1e-12 ne doit pas souder du bruit 1e-8")
	}
}

func TestCylinder(t *testing.T) {
	n := 64
	r := Analyze(Cylinder(5, 10, n), AnalyzeOptions{})
	want := 0.5 * float64(n) * 25 * math.Sin(2*math.Pi/float64(n)) * 10
	near(t, "volume", r.Volume, want, 1e-6)
	if !r.Watertight || r.InconsistentEdges != 0 || r.Components != 1 || r.WeldedVertices != 2*n {
		t.Errorf("%+v", r)
	}
	if len(Cylinder(5, 10, n).T) != 4*n-4 {
		t.Errorf("triangles = %d", len(Cylinder(5, 10, n).T))
	}
}

func TestSquareHole(t *testing.T) {
	m := BoxWithSquareHole(10, 4)
	r := Analyze(m, AnalyzeOptions{})
	near(t, "volume", r.Volume, 840, 1e-9)
	near(t, "aire", r.Area, 600-2*16+4*4*10, 1e-9)
	if !r.Watertight || r.InconsistentEdges != 0 || r.Components != 1 {
		t.Errorf("%+v", r)
	}
}

func TestTwoCubes(t *testing.T) {
	r := Analyze(TwoCubes(10, 5), AnalyzeOptions{})
	if r.Components != 2 || len(r.ComponentList) != 2 {
		t.Fatalf("composantes = %d", r.Components)
	}
	near(t, "volume", r.Volume, 2000, 1e-9)
	near(t, "volume composante", r.ComponentList[0].Volume, 1000, 1e-9)
	near(t, "bbox x", r.BBox.Size.X, 25, 1e-9)
	if !containsStr(r.Warnings, "composantes connexes") {
		t.Error("avertissement composantes")
	}
}

func TestDefects(t *testing.T) {
	// Bord ouvert : on retire les deux triangles du dessus.
	open := Cube(10)
	open.T = append(open.T[:2:2], open.T[4:]...)
	r := Analyze(open, AnalyzeOptions{})
	if r.Watertight || r.OpenEdges != 4 {
		t.Errorf("ouvert : %+v", r)
	}
	// Inversion globale.
	inv := Cube(10)
	for i := range inv.T {
		inv.T[i][1], inv.T[i][2] = inv.T[i][2], inv.T[i][1]
	}
	r = Analyze(inv, AnalyzeOptions{})
	near(t, "volume inversé", r.Volume, -1000, 1e-9)
	if !r.InvertedNormals || !r.Watertight {
		t.Errorf("inversé : %+v", r)
	}
	// Un seul triangle retourné.
	one := Cube(10)
	one.T[0][1], one.T[0][2] = one.T[0][2], one.T[0][1]
	r = Analyze(one, AnalyzeOptions{})
	if r.InconsistentEdges != 3 || r.InvertedNormals {
		t.Errorf("incohérent : %+v", r)
	}
	// Triangle dégénéré (sommets confondus) et d'aire nulle (colinéaires).
	deg := Cube(10)
	deg.V = append(deg.V, Vec3{20, 20, 20}, Vec3{21, 21, 21}, Vec3{22, 22, 22})
	deg.T = append(deg.T, Tri{0, 0, 1}, Tri{8, 9, 10})
	r = Analyze(deg, AnalyzeOptions{})
	if r.DegenerateTriangles != 2 || !r.Watertight {
		t.Errorf("dégénérés : %+v", r)
	}
	// Non manifold : une troisième face sur l'arête 0-1.
	nm := Cube(10)
	nm.V = append(nm.V, Vec3{5, -10, -10})
	nm.T = append(nm.T, Tri{0, 1, 8})
	r = Analyze(nm, AnalyzeOptions{})
	if r.NonManifoldEdges != 1 || r.Watertight {
		t.Errorf("non manifold : %+v", r)
	}
	// Maillage vide.
	if r := Analyze(&Mesh{}, AnalyzeOptions{}); r.Triangles != 0 || len(r.Warnings) == 0 {
		t.Error("vide")
	}
}

func TestSTLRoundTrip(t *testing.T) {
	src := Cylinder(5, 10, 32)
	bin, err := STLBytes(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(bin) != 84+50*len(src.T) {
		t.Fatalf("taille binaire %d", len(bin))
	}
	md, err := ParseSTL(bin)
	if err != nil {
		t.Fatal(err)
	}
	if md.Format != "stl" || md.Unit != "unknown" || !md.UnitAssumed || len(md.Warnings) == 0 {
		t.Errorf("unités : %+v", md)
	}
	a := Analyze(md.Objects[0].Mesh, AnalyzeOptions{})
	near(t, "volume binaire", a.Volume, Analyze(src, AnalyzeOptions{}).Volume, 1e-3)
	if len(md.Objects[0].Mesh.V) != 64 { // dédoublonnage exact
		t.Errorf("sommets = %d", len(md.Objects[0].Mesh.V))
	}
	asc, err := ParseSTL(stlASCII(src, "cyl"))
	if err != nil {
		t.Fatal(err)
	}
	near(t, "volume ascii", Analyze(asc.Objects[0].Mesh, AnalyzeOptions{}).Volume, a.Volume, 1e-3)
}

func TestSTLDetection(t *testing.T) {
	// Binaire dont l'en-tête commence par "solid" : doit être lu comme binaire.
	bin, _ := STLBytes(Cube(10))
	copy(bin, "solid exporté par un CAO               ")
	md, err := ParseSTL(bin)
	if err != nil || len(md.Objects[0].Mesh.T) != 12 {
		t.Fatalf("binaire 'solid' : %v", err)
	}
	// ASCII avec BOM, CRLF et majuscules.
	asc := string(stlASCII(Cube(1), "x"))
	asc = "\xef\xbb\xbf" + strings.ToUpper(strings.ReplaceAll(asc, "\n", "\r\n"))
	if _, err := ParseSTL([]byte(asc)); err != nil {
		t.Errorf("ascii BOM/CRLF : %v", err)
	}
	// Format via Parse() (sniff).
	if md, err := Parse("", stlASCII(Cube(1), "x")); err != nil || md.Format != "stl" {
		t.Errorf("sniff stl : %v", err)
	}
	if md, err := Parse("a.bin", objCubeQuads()); err != nil || md.Format != "obj" {
		t.Errorf("sniff obj : %v", err)
	}
}

func TestSTLErrors(t *testing.T) {
	bin, _ := STLBytes(Cube(10))
	if _, err := ParseSTL(bin[:len(bin)-20]); !errors.Is(err, ErrTruncated) {
		t.Errorf("binaire tronqué : %v", err)
	}
	if _, err := ParseSTL(bin[:50]); !errors.Is(err, ErrFormat) {
		t.Errorf("trop court : %v", err)
	}
	asc := stlASCII(Cube(10), "c")
	cut := asc[:bytes.LastIndex(asc, []byte("vertex"))]
	if _, err := ParseSTL(cut); !errors.Is(err, ErrTruncated) {
		t.Errorf("ascii tronqué : %v", err)
	}
	bad := strings.Replace(string(asc), "vertex 0 0 0", "vertex 0 abc 0", 1)
	if _, err := ParseSTL([]byte(bad)); !errors.Is(err, ErrFormat) {
		t.Errorf("nombre invalide : %v", err)
	}
	if _, err := ParseSTL([]byte("ce n'est pas un stl, juste du texte assez long pour dépasser quatre-vingt-quatre octets........")); !errors.Is(err, ErrFormat) {
		t.Errorf("garbage : %v", err)
	}
	if _, err := ParseSTL([]byte("solid vide\nendsolid vide\n")); !errors.Is(err, ErrEmpty) {
		t.Errorf("vide : %v", err)
	}
	// Binaire avec NaN.
	nan := append([]byte(nil), bin...)
	binary.LittleEndian.PutUint32(nan[84+12:], 0x7fc00000)
	if _, err := ParseSTL(nan); !errors.Is(err, ErrFormat) {
		t.Errorf("NaN : %v", err)
	}
	// Binaire avec octets en trop : toléré avec avertissement.
	extra := append(append([]byte(nil), bin...), 1, 2, 3)
	md, err := ParseSTL(extra)
	if err != nil || !containsStr(md.Warnings, "octets en trop") {
		t.Errorf("octets en trop : %v", err)
	}
	// ASCII sans endsolid : avertissement.
	noend := bytes.TrimSuffix(asc, []byte("endsolid c\n"))
	md, err = ParseSTL(noend)
	if err != nil || !containsStr(md.Warnings, "endsolid") {
		t.Errorf("sans endsolid : %v", err)
	}
}

func TestOBJ(t *testing.T) {
	md, err := ParseOBJ(objCubeQuads())
	if err != nil {
		t.Fatal(err)
	}
	m := md.Objects[0].Mesh
	if len(m.T) != 12 || md.Objects[0].Name != "cube" {
		t.Errorf("triangles=%d nom=%s", len(m.T), md.Objects[0].Name)
	}
	r := Analyze(m, AnalyzeOptions{})
	near(t, "volume", r.Volume, 1000, 1e-9)
	if !r.Watertight || !md.UnitAssumed {
		t.Errorf("%+v", r)
	}
	// Indices négatifs + v/vt/vn + deux objets.
	src := "v 0 0 0\nv 1 0 0\nv 0 1 0\nv 0 0 1\no a\nf 1/1/1 3/1/1 2/1/1\nf -4 -1 -3\nf 1 2 4\nf 2 3 4\no b\nf 1 2 3\n"
	md, err = ParseOBJ([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(md.Objects) != 2 || len(md.Objects[0].Mesh.T) != 4 || len(md.Objects[1].Mesh.T) != 1 {
		t.Errorf("objets : %d", len(md.Objects))
	}
	for _, bad := range []string{"v 0 0 0\nf 1 2 3\n", "v 0 0 0\nv 1 0 0\nv 0 1 0\nf 1 2 x\n", "v 0 0\n", "v 1 2 3\nf 1 2\n", "v 0 0 0\nv 1 0 0\nv 0 1 0\nf 0 1 2\n"} {
		if _, err := ParseOBJ([]byte(bad)); !errors.Is(err, ErrFormat) {
			t.Errorf("%q : %v", bad, err)
		}
	}
	if _, err := ParseOBJ([]byte("# rien\nv 0 0 0\n")); !errors.Is(err, ErrEmpty) {
		t.Error("obj sans face")
	}
}

func TestThreeMFMulti(t *testing.T) {
	md, err := ParseThreeMF(multi3MF(t))
	if err != nil {
		t.Fatal(err)
	}
	if md.Format != "3mf" || md.Unit != "centimeter" || md.UnitAssumed || md.ScaleToMM != 10 {
		t.Errorf("unités : %+v", md)
	}
	if md.Metadata["Title"] != "Multi" || md.Metadata["BambuStudio:3mfVersion"] != "1" {
		t.Errorf("métadonnées : %v", md.Metadata)
	}
	if len(md.Objects) != 3 {
		t.Fatalf("objets = %d", len(md.Objects))
	}
	asm, one, mir := md.Objects[0], md.Objects[1], md.Objects[2]
	if asm.Name != "assemblage" || one.Name != "cube1cm" || mir.Name != "miroir" {
		t.Errorf("noms : %s %s %s", asm.Name, one.Name, mir.Name)
	}
	ra := Analyze(asm.Mesh, AnalyzeOptions{})
	near(t, "assemblage volume (mm³)", ra.Volume, 2000, 1e-6)
	near(t, "assemblage x min", ra.BBox.Min.X, 100, 1e-9)
	near(t, "assemblage x max", ra.BBox.Max.X, 130, 1e-9)
	near(t, "assemblage y", ra.BBox.Max.Y, 10, 1e-9)
	if ra.Components != 2 {
		t.Errorf("composantes %d", ra.Components)
	}
	r1 := Analyze(one.Mesh, AnalyzeOptions{})
	near(t, "échelle 2 volume", r1.Volume, 8000, 1e-6)
	near(t, "échelle y min", r1.BBox.Min.Y, 50, 1e-9)
	near(t, "échelle y max", r1.BBox.Max.Y, 70, 1e-9)
	rm := Analyze(mir.Mesh, AnalyzeOptions{})
	near(t, "miroir volume positif", rm.Volume, 27000, 1e-6)
	near(t, "miroir x", rm.BBox.Min.X, -30, 1e-9)
	if !rm.Watertight || rm.InvertedNormals || rm.InconsistentEdges != 0 {
		t.Errorf("miroir : %+v", rm)
	}
	// Extras listés, jamais perdus.
	want := []string{"Metadata/model_settings.config", "Metadata/plate_1.png"}
	if strings.Join(md.Extras, ",") != strings.Join(want, ",") {
		t.Errorf("extras = %v", md.Extras)
	}
	if !containsStr(md.Warnings, "projet Bambu") {
		t.Errorf("avertissement Bambu : %v", md.Warnings)
	}
	rep := md.Analyze(AnalyzeOptions{})
	if len(rep.Objects) != 3 || rep.Combined.Components != 4 || rep.UnitNote != "" {
		t.Errorf("rapport modèle : %d objets, %d composantes", len(rep.Objects), rep.Combined.Components)
	}
}

func TestThreeMFFallbacksAndUnits(t *testing.T) {
	// Sans _rels, unité inch, sans <build> : tous les objets pris.
	model := `<model unit="inch" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"><resources><object id="7">` + cubeXML(1) + `</object></resources></model>`
	data := zipOf(t, map[string]string{"3D/3dmodel.model": model})
	md, err := ParseThreeMF(data)
	if err != nil {
		t.Fatal(err)
	}
	near(t, "pouce → mm", Analyze(md.Objects[0].Mesh, AnalyzeOptions{}).Volume, math.Pow(25.4, 3), 1e-6)
	if !containsStr(md.Warnings, "<build>") {
		t.Errorf("avertissement build : %v", md.Warnings)
	}
	// Micron.
	data = zipOf(t, map[string]string{"3D/3dmodel.model": strings.Replace(model, "inch", "micron", 1)})
	md, _ = ParseThreeMF(data)
	near(t, "micron", md.Objects[0].Mesh.Bounds().Size.X, 0.001, 1e-12)
}

func TestThreeMFErrors(t *testing.T) {
	ok := multi3MF(t)
	if _, err := ParseThreeMF(ok[:len(ok)/2]); !errors.Is(err, ErrFormat) {
		t.Errorf("zip tronqué : %v", err)
	}
	if _, err := ParseThreeMF([]byte("PK\x03\x04pas vraiment un zip")); !errors.Is(err, ErrFormat) {
		t.Errorf("zip invalide : %v", err)
	}
	cases := map[string]string{
		"xml invalide":           `<model unit="millimeter"><resources><object id="1"><mesh><vertices><vertex x="1"`,
		"pas un model":           `<autre/>`,
		"unité inconnue":         `<model unit="parsec"><resources/></model>`,
		"index hors limites":     `<model><resources><object id="1"><mesh><vertices><vertex x="0" y="0" z="0"/></vertices><triangles><triangle v1="0" v2="1" v3="2"/></triangles></mesh></object></resources><build><item objectid="1"/></build></model>`,
		"objet manquant":         `<model><resources/><build><item objectid="9"/></build></model>`,
		"transformation":         `<model><resources><object id="1">` + cubeXML(1) + `</object></resources><build><item objectid="1" transform="1 2 3"/></build></model>`,
		"composants circulaires": `<model><resources><object id="1"><components><component objectid="2"/></components></object><object id="2"><components><component objectid="1"/></components></object></resources><build><item objectid="1"/></build></model>`,
		"aucune géométrie":       `<model><resources><object id="1"/></resources><build><item objectid="1"/></build></model>`,
	}
	for name, model := range cases {
		_, err := ParseThreeMF(zipOf(t, map[string]string{"3D/3dmodel.model": model}))
		if err == nil || !(errors.Is(err, ErrFormat) || errors.Is(err, ErrEmpty)) {
			t.Errorf("%s : %v", name, err)
		}
	}
	if _, err := ParseThreeMF(zipOf(t, map[string]string{"readme.txt": "x"})); !errors.Is(err, ErrFormat) {
		t.Error("sans .model")
	}
	if _, err := ParseThreeMF(zipOf(t, map[string]string{"../evil.model": "x"})); !errors.Is(err, ErrFormat) {
		t.Error("chemin '..'")
	}
}

func TestTestdataFiles(t *testing.T) {
	for _, tc := range []struct {
		file   string
		format string
		tris   int
	}{
		{"cube10_ascii.stl", "stl", 12}, {"cube10_binary.stl", "stl", 12}, {"cube10_quads.obj", "obj", 12},
		{"cylinder_binary.stl", "stl", 4*32 - 4}, {"t_shape.stl", "stl", 0}, {"multi_cm.3mf", "3mf", 48},
	} {
		md, err := ReadFile("testdata/" + tc.file)
		if err != nil {
			t.Errorf("%s : %v", tc.file, err)
			continue
		}
		if md.Format != tc.format {
			t.Errorf("%s : format %s", tc.file, md.Format)
		}
		n := len(md.Combined().T)
		if tc.tris > 0 && n != tc.tris {
			t.Errorf("%s : %d triangles, attendu %d", tc.file, n, tc.tris)
		}
	}
	a, _ := ReadFile("testdata/cube10_ascii.stl")
	b, _ := ReadFile("testdata/cube10_binary.stl")
	near(t, "volume ascii/binaire", Analyze(a.Combined(), AnalyzeOptions{}).Volume, Analyze(b.Combined(), AnalyzeOptions{}).Volume, 1e-9)
	if _, err := ReadFile("testdata/inexistant.stl"); !os.IsNotExist(err) {
		t.Errorf("fichier inexistant : %v", err)
	}
}

func TestSTLWriteRoundTripAndHash(t *testing.T) {
	var b bytes.Buffer
	if err := WriteSTLBinary(&b, BoxWithSquareHole(10, 4)); err != nil {
		t.Fatal(err)
	}
	md, err := ParseSTL(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	near(t, "volume", Analyze(md.Combined(), AnalyzeOptions{}).Volume, 840, 1e-4)
	if got := SHA256Hex([]byte("abc")); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf("sha256 = %s", got)
	}
	h, n, err := SHA256Reader(strings.NewReader("abc"))
	if err != nil || n != 3 || h != SHA256Hex([]byte("abc")) {
		t.Error("SHA256Reader")
	}
	bad := &Mesh{V: []Vec3{{}}, T: []Tri{{0, 1, 2}}}
	if err := WriteSTLBinary(&b, bad); err == nil {
		t.Error("index invalide doit échouer")
	}
}

func TestGLB(t *testing.T) {
	md, _ := ParseThreeMF(multi3MF(t))
	g, err := md.GLB(GLBOptions{})
	if err != nil {
		t.Fatal(err)
	}
	checkGLB(t, g, 3, 48)
	if g.Decimated || len(g.SHA256) != 64 {
		t.Errorf("%+v", g)
	}
	// Décimation d'un cylindre dense.
	cyl := Cylinder(10, 20, 2000)
	d, err := cyl.GLB("cyl", GLBOptions{MaxTriangles: 400})
	if err != nil {
		t.Fatal(err)
	}
	if !d.Decimated || d.ExportedTriangles >= d.SourceTriangles || d.ExportedTriangles == 0 || !strings.Contains(d.Note, "APERÇU") {
		t.Errorf("décimation : %+v", d)
	}
	if d.ExportedTriangles > 400 {
		t.Errorf("exporté %d > 400", d.ExportedTriangles)
	}
	// Petit maillage : jamais décimé.
	s, _ := Cube(10).GLB("c", GLBOptions{MaxTriangles: 400})
	if s.Decimated {
		t.Error("cube non décimé")
	}
	if _, err := (&Mesh{}).GLB("v", GLBOptions{}); !errors.Is(err, ErrEmpty) {
		t.Error("GLB vide")
	}
	// Conversion d'axes : un point (0,0,10 mm) doit finir en y = 0,01 m ; max y.
	var doc map[string]any
	js := glbJSON(t, s.Data)
	_ = json.Unmarshal(js, &doc)
	acc := doc["accessors"].([]any)[0].(map[string]any)
	mx := acc["max"].([]any)
	near(t, "max y (hauteur)", mx[1].(float64), 0.01, 1e-7)
	near(t, "max z (−y source)", mx[2].(float64), 0, 1e-7)
}

func glbJSON(t *testing.T, data []byte) []byte {
	t.Helper()
	jl := binary.LittleEndian.Uint32(data[12:])
	return data[20 : 20+jl]
}

func checkGLB(t *testing.T, g *GLBResult, meshes, tris int) {
	t.Helper()
	d := g.Data
	if binary.LittleEndian.Uint32(d[0:]) != 0x46546C67 || binary.LittleEndian.Uint32(d[4:]) != 2 {
		t.Fatal("en-tête GLB")
	}
	if int(binary.LittleEndian.Uint32(d[8:])) != len(d) || len(d)%4 != 0 {
		t.Errorf("longueur déclarée %d, réelle %d", binary.LittleEndian.Uint32(d[8:]), len(d))
	}
	jl := int(binary.LittleEndian.Uint32(d[12:]))
	var doc struct {
		Meshes    []any `json:"meshes"`
		Accessors []struct {
			Count int `json:"count"`
		} `json:"accessors"`
		BufferViews []struct {
			ByteOffset int `json:"byteOffset"`
			ByteLength int `json:"byteLength"`
		} `json:"bufferViews"`
		Buffers []struct {
			ByteLength int `json:"byteLength"`
		} `json:"buffers"`
	}
	if err := json.Unmarshal(d[20:20+jl], &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Meshes) != meshes || g.ExportedTriangles != tris {
		t.Errorf("meshes=%d triangles=%d", len(doc.Meshes), g.ExportedTriangles)
	}
	binStart := 20 + jl + 8
	if string(d[20+jl+4:20+jl+8]) != "BIN\x00" {
		t.Error("chunk BIN")
	}
	if binStart+doc.Buffers[0].ByteLength > len(d) {
		t.Error("buffer hors du GLB")
	}
	for _, v := range doc.BufferViews {
		if v.ByteOffset+v.ByteLength > doc.Buffers[0].ByteLength {
			t.Error("bufferView hors buffer")
		}
	}
	idxTotal := 0
	for i, a := range doc.Accessors {
		if i%2 == 1 {
			idxTotal += a.Count
		}
	}
	if idxTotal != 3*tris {
		t.Errorf("indices = %d", idxTotal)
	}
}

func TestMatrixAndTransform(t *testing.T) {
	m := Cube(1).Transform(Translation(1, 2, 3))
	b := m.Bounds()
	near(t, "tx", b.Min.X, 1, 0)
	near(t, "tz", b.Max.Z, 4, 0)
	rot := Cube(1).Transform(RotateX90)
	near(t, "rot volume", Analyze(rot, AnalyzeOptions{}).Volume, 1, 1e-12)
	near(t, "rot y", rot.Bounds().Min.Y, -1, 1e-12)
	// Composition a puis b.
	c := Scaling(2).Mul(Translation(1, 0, 0))
	p := c.Apply(Vec3{1, 1, 1})
	if p != (Vec3{3, 2, 2}) {
		t.Errorf("composition : %v", p)
	}
	near(t, "det miroir", Matrix{-1, 0, 0, 0, 1, 0, 0, 0, 1}.Det(), -1, 0)
}

func containsStr(ws []string, sub string) bool {
	for _, w := range ws {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}
