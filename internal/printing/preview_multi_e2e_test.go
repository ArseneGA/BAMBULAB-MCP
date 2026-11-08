package printing_test

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// e2eTable : plateau 30 x 30 x 2 mm sur quatre pieds de 3 x 3 mm.
func e2eTable() *mesh.Mesh {
	m := mesh.Box(0, 0, 10, 30, 30, 12)
	for _, o := range [][2]float64{{0, 0}, {27, 0}, {0, 27}, {27, 27}} {
		m.Append(mesh.Box(o[0], o[1], 0, o[0]+3, o[1]+3, 10))
	}
	return m
}

// e2eGcode écrit un .gcode.3mf : feet=false -> n couches simples ; feet=true -> couche 1 = quatre pieds de 3 x 3 mm et une jupe.
func e2eGcode(t *testing.T, n int, feet bool) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("G90\nM83\n")
	if feet {
		b.WriteString("; CHANGE_LAYER\n; Z_HEIGHT: 0.2\n; FEATURE: Skirt\nG1 X10 Y10 F3000\nG1 X120 Y10 E1\nG1 X120 Y120 E1\n; FEATURE: Bottom surface\n")
		for _, o := range [][2]float64{{50, 50}, {80, 50}, {50, 80}, {80, 80}} {
			for y := 0.0; y <= 3.0001; y += 0.4 {
				fmt.Fprintf(&b, "G1 X%.2f Y%.2f F3000\nG1 X%.2f Y%.2f E0.1\n", o[0], o[1]+y, o[0]+3, o[1]+y)
			}
		}
	}
	for l := 1; l <= n; l++ {
		fmt.Fprintf(&b, "; CHANGE_LAYER\n; Z_HEIGHT: %.2f\n; FEATURE: Outer wall\nG1 X20 Y20 F3000\nG1 X%d Y20 E1\nG1 X%d Y%d E1\n", float64(l)*0.2, 20+l*5, 20+l*5, 20+l*5)
	}
	p := filepath.Join(t.TempDir(), "a.gcode.3mf")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("Metadata/plate_1.gcode")
	w.Write([]byte(b.String()))
	zw.Close()
	f.Close()
	return p
}

func firstImage(t *testing.T, res *mcp.CallToolResult) *mcp.ImageContent {
	t.Helper()
	for _, c := range res.Content {
		if ic, ok := c.(*mcp.ImageContent); ok {
			return ic
		}
	}
	t.Fatalf("aucun contenu image : %+v", res.Content)
	return nil
}

// De bout en bout par MCP : planche de vues, couche 1 par section du maillage, planche-contact, gif, show_layers.
func TestCapturePreviewSheetsOverMCP(t *testing.T) {
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	r.sim = startSim(t)
	r.seedPrinterModel()
	r.printerID = str(r.addPrinter(nil), "printer", "id")

	data, err := mesh.STLBytes(e2eTable())
	if err != nil {
		t.Fatal(err)
	}
	sha, size, _ := r.c.Blobs.Put(strings.NewReader(string(data)), "")
	m, _, _, err := r.c.Catalog.RegisterGenerated(t.Context(), "test", newModel("table"), "", "v1", "", []catalogFile{{Name: "table.stl", Format: "stl", SHA256: sha, Size: size, Unit: "mm"}})
	if err != nil {
		t.Fatal(err)
	}
	call := func(args map[string]any) *mcp.CallToolResult {
		t.Helper()
		res, err := r.cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "capture_preview", Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("capture_preview %v : %v %+v", args, err, res)
		}
		return res
	}

	// (8a) planche de 4 vues en un appel
	res := call(map[string]any{"model_id": m.ID, "views": []string{"all"}, "width": 800, "height": 600})
	img := firstImage(t, res)
	pic, err := png.Decode(bytes.NewReader(img.Data))
	if err != nil || img.MIMEType != "image/png" || pic.Bounds().Dx() != 800 {
		t.Fatalf("planche de vues : %v %v", err, img.MIMEType)
	}
	if st, _ := res.StructuredContent.(map[string]any); st["kind"] != "mesh_views" {
		t.Fatalf("structuré : %v", res.StructuredContent)
	}

	// (3) couche 1 avant découpe : 4 pieds => 4 îlots
	res = call(map[string]any{"model_id": m.ID, "mode": "first_layer"})
	st, _ := res.StructuredContent.(map[string]any)
	isl, _ := st["islands"].([]any)
	if len(isl) != 4 || st["kind"] != "first_layer" {
		t.Fatalf("first_layer : %v", st)
	}
	txt := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(txt, "4 ilot(s)") || !strings.Contains(txt, "PAS la surface en contact") {
		t.Fatalf("texte : %s", txt)
	}
	firstImage(t, res)

	// artefact G-code : couche 1 réelle, planche-contact, gif
	gp := e2eGcode(t, 12, false)
	gb, _ := os.ReadFile(gp)
	art, _, err := r.c.Blobs.Put(bytes.NewReader(gb), "")
	if err != nil {
		t.Fatal(err)
	}
	res = call(map[string]any{"artifact_id": art, "mode": "layers", "layers": []int{1, 6, 12}, "width": 700, "height": 500})
	st, _ = res.StructuredContent.(map[string]any)
	if st["kind"] != "gcode_layers" || fmt.Sprint(st["layers"]) != "[1 6 12]" {
		t.Fatalf("planche de couches : %v", st)
	}
	if _, err := png.Decode(bytes.NewReader(firstImage(t, res).Data)); err != nil {
		t.Fatal(err)
	}
	res = call(map[string]any{"artifact_id": art, "every_n": 4, "format": "gif", "width": 500, "height": 400})
	gi := firstImage(t, res)
	if gi.MIMEType != "image/gif" {
		t.Fatalf("mime %s", gi.MIMEType)
	}
	if g, err := gif.DecodeAll(bytes.NewReader(gi.Data)); err != nil || len(g.Image) != 3 {
		t.Fatalf("gif : %v", err)
	}
	gf := e2eGcode(t, 0, true)
	fb, _ := os.ReadFile(gf)
	feet, _, _ := r.c.Blobs.Put(bytes.NewReader(fb), "")
	res = call(map[string]any{"artifact_id": feet, "mode": "first_layer"})
	st, _ = res.StructuredContent.(map[string]any)
	if isl, _ := st["islands"].([]any); len(isl) != 4 {
		t.Fatalf("couche 1 du G-code : %v", st)
	}

	// show_layers : texte + planche PNG + structuré pour la vue ; get_layer_paths (outil de la vue)
	sl, err := r.cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "show_layers", Arguments: map[string]any{"artifact_id": art, "max_tiles": 4}})
	if err != nil || sl.IsError {
		t.Fatalf("show_layers : %v %+v", err, sl)
	}
	firstImage(t, sl)
	sst, _ := sl.StructuredContent.(map[string]any)
	if sst["layers_total"].(float64) != 12 || sst["artifact_sha256"] != art {
		t.Fatalf("show_layers : %v", sst)
	}
	if ui, _ := sl.Meta["ui"].(map[string]any); ui["resourceUri"] != "ui://bambu/layers.html" {
		t.Fatalf("résultat sans lien de vue : %v", sl.Meta)
	}
	lp := r.call("get_layer_paths", map[string]any{"artifact_id": art, "layer": 3, "layers_total": 12})
	if lp["layer"].(float64) != 3 || lp["layers_total"].(float64) != 12 || len(lp["paths"].([]any)) == 0 {
		t.Fatalf("get_layer_paths : %v", lp)
	}
	if r.callErr("get_layer_paths", map[string]any{"artifact_id": art, "layer": 99}) == "" {
		t.Fatal("couche hors plage : erreur attendue")
	}
}
