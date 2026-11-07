package mesh

import (
	"archive/zip"
	"bytes"
	"io"
	"math"
	"strings"
	"testing"
)

func zipEntry(t *testing.T, data []byte, name string) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			return string(b)
		}
	}
	return ""
}

func TestWriteBambuProjectRoundTrip(t *testing.T) {
	origin := func(p int) (float64, float64) { return float64(p-1) * 300, 0 }
	objs := []ProjectObject{
		{Name: "dense <a&b>", Mesh: Cube(20), Plate: 1, X: 10, Y: 20, Settings: map[string]string{"sparse_infill_density": "100%", "wall_loops": "4"}},
		{Name: "light", Mesh: Box(5, 5, 5, 45, 25, 15), Plate: 2, X: 50, Y: 60, Settings: map[string]string{"sparse_infill_density": "15%"},
			Ranges: []LayerRange{{MinZ: 0, MaxZ: 4, Settings: map[string]string{"layer_height": "0.1"}}}},
	}
	data, err := WriteBambuProject(objs, origin)
	if err != nil {
		t.Fatal(err)
	}
	md, err := ParseThreeMF(data)
	if err != nil {
		t.Fatalf("relecture : %v", err)
	}
	if len(md.Objects) != 2 {
		t.Fatalf("%d objets relus", len(md.Objects))
	}
	b0, b1 := md.Objects[0].Mesh.Bounds(), md.Objects[1].Mesh.Bounds()
	if math.Abs(b0.Min.X-10) > 1e-9 || math.Abs(b0.Min.Y-20) > 1e-9 || b0.Min.Z != 0 {
		t.Errorf("objet 1 : %+v", b0)
	}
	// objet 2 : coin minimal ramené à (50,60) + décalage de la plaque 2 ; posé sur Z=0 malgré son Z initial de 5
	if math.Abs(b1.Min.X-350) > 1e-9 || math.Abs(b1.Min.Y-60) > 1e-9 || math.Abs(b1.Min.Z) > 1e-9 || math.Abs(b1.Size.X-40) > 1e-9 {
		t.Errorf("objet 2 : %+v", b1)
	}
	cfg := zipEntry(t, data, "Metadata/model_settings.config")
	for _, want := range []string{`key="sparse_infill_density" value="100%"`, `key="wall_loops" value="4"`, `value="dense &lt;a&amp;b&gt;"`, `<metadata key="plater_id" value="2"/>`} {
		if !strings.Contains(cfg, want) {
			t.Errorf("model_settings.config sans %q :\n%s", want, cfg)
		}
	}
	if r := zipEntry(t, data, "Metadata/layer_config_ranges.xml"); !strings.Contains(r, `min_z="0" max_z="4"`) || !strings.Contains(r, `opt_key="layer_height">0.1<`) {
		t.Errorf("plages : %s", r)
	}
	if _, err := WriteBambuProject(nil, origin); err == nil {
		t.Error("projet vide accepté")
	}
	if _, err := WriteBambuProject([]ProjectObject{{Name: "x", Mesh: Cube(1), Plate: 0}}, origin); err == nil {
		t.Error("plaque 0 acceptée")
	}
}

func TestAddZipEntryReplaces(t *testing.T) {
	data, err := WriteBambuProject([]ProjectObject{{Name: "a", Mesh: Cube(5), Plate: 1}}, func(int) (float64, float64) { return 0, 0 })
	if err != nil {
		t.Fatal(err)
	}
	out, err := AddZipEntry(data, "Metadata/project_settings.config", []byte(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	out, err = AddZipEntry(out, "Metadata/project_settings.config", []byte(`{"a":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := zipEntry(t, out, "Metadata/project_settings.config"); got != `{"a":2}` {
		t.Errorf("entrée : %q", got)
	}
	zr, _ := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	n := 0
	for _, f := range zr.File {
		if f.Name == "Metadata/project_settings.config" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d entrées dupliquées", n)
	}
	if _, err := ParseThreeMF(out); err != nil {
		t.Errorf("archive illisible après ajout : %v", err)
	}
}
