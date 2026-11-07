package filament_test

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

const (
	p1sMachine  = "Bambu Lab P1S 0.4 nozzle"
	p1sProcess  = "0.20mm Standard @BBL X1C"
	p1sPLABasic = "Bambu PLA Basic @BBL P1S 0.4 nozzle"
	p1sPLAMatte = "Bambu PLA Matte @BBL P1S 0.4 nozzle"
)

const nsModel = `unit="millimeter" xml:lang="en-US" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:BambuStudio="http://schemas.bambulab.com/package/2021" xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06" requiredextensions="p"`

// realStudio renvoie un vrai découpeur ou ignore le test (-short, Studio absent ou inutilisable).
func realStudio(t testing.TB) *slicer.Slicer {
	t.Helper()
	if testing.Short() {
		t.Skip("-short")
	}
	if _, err := slicer.Locate(""); err != nil {
		t.Skipf("Bambu Studio absent : %v", err)
	}
	s, err := slicer.New(slicer.Config{WorkRoot: t.TempDir(), Timeout: 5 * time.Minute})
	if err != nil {
		t.Skipf("Bambu Studio inutilisable : %v", err)
	}
	return s
}

// twoColor3MF fabrique un projet Bambu Studio à deux pièces (deux cubes) dont chacune est affectée à un
// filament différent (clé « extruder » de model_settings.config). Un 3MF purement synthétique fait planter
// Studio (voir internal/slicer TestReal3MFSyntheticCrashIsNamed) : on part donc d'une archive produite par
// Studio lui-même (un cube découpé), dont on garde les réglages de projet, et on remplace le modèle et la
// configuration des objets.
func twoColor3MF(t testing.TB, s *slicer.Slicer, size float64) string {
	t.Helper()
	b, err := mesh.STLBytes(mesh.Cube(size))
	if err != nil {
		t.Fatal(err)
	}
	stl := filepath.Join(t.TempDir(), "base.stl")
	if err := os.WriteFile(stl, b, 0o644); err != nil {
		t.Fatal(err)
	}
	base, err := s.Slice(context.Background(), slicer.Request{Model: stl, Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sPLABasic, p1sPLAMatte}, Label: "base"})
	if err != nil {
		t.Skipf("découpe de base impossible : %v", err)
	}
	zr, err := zip.OpenReader(base.ArtifactPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	read := func(name string) string {
		for _, f := range zr.File {
			if f.Name == name {
				rc, _ := f.Open()
				defer rc.Close()
				b, _ := io.ReadAll(rc)
				return string(b)
			}
		}
		t.Fatalf("entrée %s absente de l'archive de base", name)
		return ""
	}
	cube := read("3D/Objects/object_1.model")
	files := map[string]string{}
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, ".png") || strings.HasPrefix(f.Name, "Metadata/plate_1.gcode") {
			continue // aperçus et G-code périmés : Studio les recalcule
		}
		files[f.Name] = read(f.Name)
	}
	for k, v := range map[string]string{
		"3D/Objects/object_1.model": cube,
		"3D/Objects/object_2.model": strings.ReplaceAll(cube, "00010000-81cb", "00020000-81cb"),
		"3D/_rels/3dmodel.model.rels": `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
 <Relationship Target="/3D/Objects/object_1.model" Id="rel-1" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/>
 <Relationship Target="/3D/Objects/object_2.model" Id="rel-2" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/>
</Relationships>`,
	} {
		files[k] = v
	}
	var root strings.Builder
	root.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<model " + nsModel + ">\n <metadata name=\"Application\">BambuStudio-02.08.02.61</metadata>\n <metadata name=\"BambuStudio:3mfVersion\">1</metadata>\n <resources>\n")
	var cfg strings.Builder
	cfg.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<config>\n")
	for i := 1; i <= 2; i++ {
		fmt.Fprintf(&root, "  <object id=\"%d\" p:UUID=\"0000000%d-61cb-4c03-9d28-80fed5dfa1dc\" type=\"model\">\n   <components>\n    <component p:path=\"/3D/Objects/object_%d.model\" objectid=\"1\" p:UUID=\"0001000%d-b206-40ff-9872-83e8017abed1\" transform=\"1 0 0 0 1 0 0 0 1 0 0 0\"/>\n   </components>\n  </object>\n", i+2, i, i, i)
		fmt.Fprintf(&cfg, "  <object id=\"%d\">\n    <metadata key=\"name\" value=\"piece%d\"/>\n    <metadata key=\"extruder\" value=\"%d\"/>\n    <part id=\"1\" subtype=\"normal_part\">\n      <metadata key=\"name\" value=\"piece%d\"/>\n      <metadata key=\"matrix\" value=\"1 0 0 0 0 1 0 0 0 0 1 0 0 0 0 1\"/>\n    </part>\n  </object>\n", i+2, i, i, i)
	}
	root.WriteString(" </resources>\n <build p:UUID=\"2c7c17d8-22b5-4d84-8835-1976022ea369\">\n")
	root.WriteString("  <item objectid=\"3\" p:UUID=\"00000003-b1ec-4553-aec9-835e5b724bb4\" transform=\"1 0 0 0 1 0 0 0 1 100 118 0\" printable=\"1\"/>\n")
	root.WriteString("  <item objectid=\"4\" p:UUID=\"00000004-b1ec-4553-aec9-835e5b724bb4\" transform=\"1 0 0 0 1 0 0 0 1 140 118 0\" printable=\"1\"/>\n")
	root.WriteString(" </build>\n</model>\n")
	cfg.WriteString("</config>\n")
	// Les réglages de projet doivent décrire deux filaments (tableaux « filament* » à deux éléments), sinon Studio plante.
	var ps map[string]any
	if err := json.Unmarshal([]byte(files["Metadata/project_settings.config"]), &ps); err != nil {
		t.Fatal(err)
	}
	for k, v := range ps {
		if a, ok := v.([]any); ok && len(a) == 1 && strings.HasPrefix(k, "filament") {
			ps[k] = []any{a[0], a[0]}
		}
	}
	ps["filament_colour"] = []any{"#00AE42", "#D01010"}
	ps["flush_volumes_matrix"] = []any{"0", "280", "280", "0"}
	ps["flush_volumes_vector"] = []any{"140", "140", "140", "140"}
	pb, _ := json.Marshal(ps)
	files["Metadata/project_settings.config"] = string(pb)
	files["3D/3dmodel.model"] = root.String()
	files["Metadata/model_settings.config"] = cfg.String()

	p := filepath.Join(t.TempDir(), "deux_couleurs.3mf")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for n, content := range files {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}
