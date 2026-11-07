package mesh

import (
	"archive/zip"
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func near(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.6f, attendu %.6f (±%g)", name, got, want, tol)
	}
}

// zipOf construit une archive zip en mémoire.
func zipOf(t testing.TB, files map[string]string, order ...string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	names := order
	if len(names) == 0 {
		for n := range files {
			names = append(names, n)
		}
	}
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(files[n])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// cubeXML : maillage <mesh> d'un cube [0,s]³ (normales sortantes).
func cubeXML(s float64) string {
	m := Cube(s)
	var b strings.Builder
	b.WriteString("<mesh><vertices>")
	for _, v := range m.V {
		fmt.Fprintf(&b, `<vertex x="%g" y="%g" z="%g"/>`, v.X, v.Y, v.Z)
	}
	b.WriteString("</vertices><triangles>")
	for _, t := range m.T {
		fmt.Fprintf(&b, `<triangle v1="%d" v2="%d" v3="%d"/>`, t[0], t[1], t[2])
	}
	b.WriteString("</triangles></mesh>")
	return b.String()
}

const rels3mf = `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
 <Relationship Target="/3D/3dmodel.model" Id="rel0" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/>
</Relationships>`

const contentTypes3mf = `<?xml version="1.0" encoding="UTF-8"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
 <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
 <Default Extension="model" ContentType="application/vnd.ms-package.3dmanufacturing-3dmodel+xml"/>
</Types>`

// multi3MF : unité centimètre. Objets : 1 = cube 1 cm ; 2 = assemblage de deux cubes (composants, dont l'un
// dans un fichier séparé avec l'extension Production, et l'autre tourné de 90° autour de Z) ; 3 = cube 3 cm miroir.
// Build : objet 2 translaté de (10,0,0) cm, objet 1 à l'échelle 2 en (0,5,0) cm, objet 3 miroir X.
func multi3MF(t testing.TB) []byte {
	root := `<?xml version="1.0" encoding="UTF-8"?>
<model unit="centimeter" xml:lang="en-US" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06">
 <metadata name="Title">Multi</metadata>
 <metadata name="Application">BambuStudio-01.10.00.00</metadata>
 <metadata name="BambuStudio:3mfVersion">1</metadata>
 <resources>
  <object id="1" name="cube1cm" type="model">` + cubeXML(1) + `</object>
  <object id="2" name="assemblage" type="model">
   <components>
    <component objectid="1" transform="1 0 0 0 1 0 0 0 1 0 0 0"/>
    <component p:path="/3D/Objects/object_4.model" objectid="1" transform="0 1 0 -1 0 0 0 0 1 3 0 0"/>
   </components>
  </object>
  <object id="3" name="miroir" type="model">` + cubeXML(3) + `</object>
 </resources>
 <build>
  <item objectid="2" transform="1 0 0 0 1 0 0 0 1 10 0 0"/>
  <item objectid="1" transform="2 0 0 0 2 0 0 0 2 0 5 0"/>
  <item objectid="3" transform="-1 0 0 0 1 0 0 0 1 0 0 0"/>
 </build>
</model>`
	sub := `<?xml version="1.0" encoding="UTF-8"?>
<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02">
 <resources><object id="1" type="model">` + cubeXML(1) + `</object></resources><build/>
</model>`
	return zipOf(t, map[string]string{
		"[Content_Types].xml":            contentTypes3mf,
		"_rels/.rels":                    rels3mf,
		"3D/3dmodel.model":               root,
		"3D/Objects/object_4.model":      sub,
		"Metadata/model_settings.config": `<config><object id="2"><metadata key="name" value="assemblage"/></object></config>`,
		"Metadata/plate_1.png":           "PNG",
	}, "[Content_Types].xml", "_rels/.rels", "3D/3dmodel.model", "3D/Objects/object_4.model", "Metadata/model_settings.config", "Metadata/plate_1.png")
}

func stlASCII(m *Mesh, name string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "solid %s\n", name)
	for i, t := range m.T {
		n, _ := m.Normal(i)
		fmt.Fprintf(&b, "  facet normal %g %g %g\n    outer loop\n", n.X, n.Y, n.Z)
		for _, k := range t {
			v := m.V[k]
			fmt.Fprintf(&b, "      vertex %g %g %g\n", v.X, v.Y, v.Z)
		}
		b.WriteString("    endloop\n  endfacet\n")
	}
	fmt.Fprintf(&b, "endsolid %s\n", name)
	return []byte(b.String())
}

func objCubeQuads() []byte {
	return []byte(`# cube avec faces quadrangulaires
o cube
v 0 0 0
v 10 0 0
v 10 10 0
v 0 10 0
v 0 0 10
v 10 0 10
v 10 10 10
v 0 10 10
f 1 4 3 2
f 5 6 7 8
f 1 2 6 5
f 2 3 7 6
f 3 4 8 7
f 4 1 5 8
`)
}

// TestGenerateTestdata (re)génère les fichiers de testdata : BAMBU_GEN_TESTDATA=1 go test -run Generate ./internal/mesh
func TestGenerateTestdata(t *testing.T) {
	if os.Getenv("BAMBU_GEN_TESTDATA") == "" {
		t.Skip("génération désactivée")
	}
	dir := "testdata"
	_ = os.MkdirAll(dir, 0o755)
	write := func(n string, b []byte) {
		if err := os.WriteFile(filepath.Join(dir, n), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("cube10_ascii.stl", stlASCII(Cube(10), "cube10"))
	bin, _ := STLBytes(Cube(10))
	write("cube10_binary.stl", bin)
	write("cube10_quads.obj", objCubeQuads())
	write("multi_cm.3mf", multi3MF(t))
	write("cylinder_binary.stl", func() []byte { b, _ := STLBytes(Cylinder(5, 10, 32)); return b }())
	write("t_shape.stl", stlASCII(TShape(4, 10, 20, 3, 10), "t"))
}

var testTarget = Target{Name: "test 256", BedX: 256, BedY: 256, BedZ: 256, Nozzle: 0.4}
