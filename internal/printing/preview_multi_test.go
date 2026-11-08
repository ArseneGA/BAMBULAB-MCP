package printing

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// table4Feet : plateau 30 x 30 x 2 mm posé sur quatre pieds de 3 x 3 mm (hauts de 10 mm).
func table4Feet() *mesh.Mesh {
	m := mesh.Box(0, 0, 10, 30, 30, 12)
	for _, o := range [][2]float64{{0, 0}, {27, 0}, {0, 27}, {27, 27}} {
		m.Append(mesh.Box(o[0], o[1], 0, o[0]+3, o[1]+3, 10))
	}
	return m
}

func TestFirstLayerMeshSectionFourFeet(t *testing.T) {
	o, err := renderFirstLayerMesh(table4Feet(), "table.stl", strings.Repeat("c", 64), 800, 600)
	if err != nil {
		t.Fatal(err)
	}
	if o.Kind != "first_layer" || len(o.Islands) != 4 {
		t.Fatalf("attendu 4 îlots, obtenu %d (%+v)", len(o.Islands), o.Islands)
	}
	for _, i := range o.Islands {
		if i.AreaMM2 < 8.5 || i.AreaMM2 > 9.5 {
			t.Errorf("aire de l'îlot %d : %.2f mm², attendu ~9", i.Index, i.AreaMM2)
		}
	}
	if o.ContactAreaMM2 < 34 || o.ContactAreaMM2 > 38 {
		t.Errorf("aire totale %.2f, attendu ~36", o.ContactAreaMM2)
	}
	leg := strings.Join(o.Legend, "\n")
	for _, want := range []string{"4 ilot(s)", "EMPRISE projetee", "PAS la surface en contact", "mm2"} {
		if !strings.Contains(leg, want) {
			t.Errorf("légende sans %q :\n%s", want, leg)
		}
	}
	img, err := png.Decode(bytes.NewReader(o.PNG))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() <= 0 || img.Bounds().Dx() > 800 || img.Bounds().Dy() <= 600-60 {
		t.Errorf("dimensions %v", img.Bounds())
	}
	if o.Render["source"] != "mesh_section" {
		t.Errorf("source %v", o.Render["source"])
	}
}

func TestFirstLayerMeshNoBottomFace(t *testing.T) {
	m := mesh.Box(0, 0, 0, 10, 10, 10)
	m.T = m.T[2:] // sans la face du bas
	if _, err := renderFirstLayerMesh(m, "x", "", 400, 300); err == nil {
		t.Fatal("section vide : erreur attendue")
	}
}

// gcodeFeet : couche 1 = quatre pieds de 3 x 3 mm remplis de filets, plus une jupe ; couche 2 sans rapport.
func gcodeFeet(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("; HEADER\nG90\nM83\n; CHANGE_LAYER\n; Z_HEIGHT: 0.2\n; FEATURE: Skirt\nG1 X10 Y10 F3000\nG1 X120 Y10 E1\nG1 X120 Y120 E1\nG1 X10 Y120 E1\nG1 X10 Y10 E1\n; FEATURE: Bottom surface\n")
	for _, o := range [][2]float64{{50, 50}, {80, 50}, {50, 80}, {80, 80}} {
		for y := 0.0; y <= 3.0001; y += 0.4 {
			fmt.Fprintf(&b, "G1 X%.2f Y%.2f F3000\nG1 X%.2f Y%.2f E0.1\n", o[0], o[1]+y, o[0]+3, o[1]+y)
		}
	}
	b.WriteString("; CHANGE_LAYER\n; Z_HEIGHT: 0.4\n; FEATURE: Outer wall\nG1 X60 Y60\nG1 X70 Y60 E1\n")
	return zipGcode(t, b.String())
}

func zipGcode(t *testing.T, g string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "a.gcode.3mf")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("Metadata/plate_1.gcode")
	w.Write([]byte(g))
	zw.Close()
	f.Close()
	return p
}

func TestFirstLayerGcodeIslands(t *testing.T) {
	o, err := renderFirstLayerGcode(gcodeFeet(t), strings.Repeat("d", 64), 1, nil, "emprise grise non dessinee : test", 800, 600)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Islands) != 4 {
		t.Fatalf("attendu 4 îlots (la jupe ne compte pas), obtenu %d : %+v", len(o.Islands), o.Islands)
	}
	for _, i := range o.Islands {
		if i.AreaMM2 < 8 || i.AreaMM2 > 13 {
			t.Errorf("îlot %d : %.2f mm²", i.Index, i.AreaMM2)
		}
	}
	leg := strings.Join(o.Legend, "\n")
	for _, want := range []string{"Couche 1 REELLE", "4 ilot(s)", "Skirt", "PAS la surface en contact", "emprise grise non dessinee"} {
		if !strings.Contains(leg, want) {
			t.Errorf("légende sans %q :\n%s", want, leg)
		}
	}
	if _, err := png.Decode(bytes.NewReader(o.PNG)); err != nil {
		t.Fatal(err)
	}
}

func TestPlateLegendSaysFootprintIsNotContact(t *testing.T) {
	obj := drawObj{name: "table", m: table4Feet().Transform(mesh.Translation(50, 50, 0)), legend: "30 x 30 x 12 mm"}
	o, err := renderPlate(PlateBed{X: 256, Y: 256, Z: 256}, "Plateau test", []drawObj{obj}, nil, 700)
	if err != nil {
		t.Fatal(err)
	}
	leg := strings.Join(o.Legend, "\n")
	if !strings.Contains(leg, "emprise projetee vue de dessus, PAS la surface en contact avec le plateau") || !strings.Contains(leg, "first_layer") {
		t.Fatalf("légende du mode plate :\n%s", leg)
	}
	if !strings.Contains(strings.Join(o.NotRepresented, "|"), "surface réellement en contact") {
		t.Fatalf("not_represented : %v", o.NotRepresented)
	}
}

func gcodeLayers(t *testing.T, n int) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("G90\nM83\n")
	for l := 1; l <= n; l++ {
		fmt.Fprintf(&b, "; CHANGE_LAYER\n; Z_HEIGHT: %.2f\n; FEATURE: Outer wall\nG1 X%d Y20 F3000\nG1 X%d Y20 E1\nG1 X%d Y%d E1\n", float64(l)*0.2, 20, 20+l*5, 20+l*5, 20+l*5)
	}
	return zipGcode(t, b.String())
}

func TestPickLayers(t *testing.T) {
	sel, _, err := pickLayers(100, nil, 0, 0, false)
	if err != nil || len(sel) != 9 || sel[0] != 1 || sel[8] != 100 {
		t.Fatalf("défaut : %v %v", sel, err)
	}
	sel, _, _ = pickLayers(100, nil, 25, 0, false)
	if fmt.Sprint(sel) != "[1 26 51 76]" {
		t.Fatalf("every_n : %v", sel)
	}
	sel, note, _ := pickLayers(1000, nil, 1, 100, false)
	if len(sel) != 16 || !strings.Contains(note, "max_tiles") {
		t.Fatalf("plafond 16 : %d %q", len(sel), note)
	}
	sel, _, _ = pickLayers(1000, nil, 1, 100, true)
	if len(sel) != 48 {
		t.Fatalf("plafond gif 48 : %d", len(sel))
	}
	sel, _, _ = pickLayers(5, nil, 0, 0, false)
	if fmt.Sprint(sel) != "[1 2 3 4 5]" {
		t.Fatalf("moins de couches que de vignettes : %v", sel)
	}
	if _, _, err := pickLayers(10, []int{1, 11}, 0, 0, false); err == nil {
		t.Fatal("couche hors plage : erreur attendue")
	}
	if _, _, err := pickLayers(100, []int{1, 2, 3}, 0, 2, false); err == nil {
		t.Fatal("plus de couches que de vignettes : erreur attendue")
	}
	sel, _, _ = pickLayers(10, []int{5, 3, 3, 1}, 0, 0, false)
	if fmt.Sprint(sel) != "[1 3 5]" {
		t.Fatalf("tri et doublons : %v", sel)
	}
}

func TestLayerSheetNTiles(t *testing.T) {
	p := gcodeLayers(t, 12)
	o, err := renderLayerSheet(p, strings.Repeat("e", 64), 1, sheetParams{Layers: []int{1, 4, 8, 12}}, 800, 600)
	if err != nil {
		t.Fatal(err)
	}
	if o.Kind != "gcode_layers" || fmt.Sprint(o.Layers) != "[1 4 8 12]" || o.Render["tiles"].(int) != 4 || o.Render["columns"].(int) != 2 {
		t.Fatalf("%+v %v", o.Render, o.Layers)
	}
	img, err := png.Decode(bytes.NewReader(o.PNG))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() > 800 || img.Bounds().Dy() < 100 {
		t.Fatalf("taille %v", img.Bounds())
	}
	leg := strings.Join(o.Legend, "\n")
	if !strings.Contains(leg, "4 couche(s) sur 12") || !strings.Contains(leg, "Outer wall") {
		t.Fatalf("légende :\n%s", leg)
	}
	// every_n : 1, 6, 11
	o, err = renderLayerSheet(p, "x", 1, sheetParams{EveryN: 5}, 600, 500)
	if err != nil || fmt.Sprint(o.Layers) != "[1 6 11]" {
		t.Fatalf("every_n : %v %v", o.Layers, err)
	}
	// aucune couche explicite : répartition régulière sur toutes (12 couches, 9 vignettes)
	o, err = renderLayerSheet(p, "x", 1, sheetParams{MaxTiles: 16}, 900, 700)
	if err != nil || len(o.Layers) != 12 || o.Render["columns"].(int) != 4 {
		t.Fatalf("toutes les couches : %v %v", o.Layers, err)
	}
	if _, err := renderLayerSheet(p, "x", 1, sheetParams{Layers: []int{13}}, 600, 500); err == nil {
		t.Fatal("couche hors plage : erreur attendue")
	}
	if _, err := renderLayerSheet(p, "x", 4, sheetParams{}, 600, 500); err == nil {
		t.Fatal("plaque absente : erreur attendue")
	}
}

func TestLayerSheetGIF(t *testing.T) {
	p := gcodeLayers(t, 12)
	o, err := renderLayerSheet(p, strings.Repeat("f", 64), 1, sheetParams{Layers: []int{1, 3, 5, 7}, GIF: true}, 900, 700)
	if err != nil {
		t.Fatal(err)
	}
	if o.MIME != "image/gif" || o.Kind != "gcode_animation" || len(o.PNG) > gifMaxBytes {
		t.Fatalf("%s %s %d octets", o.MIME, o.Kind, len(o.PNG))
	}
	g, err := gif.DecodeAll(bytes.NewReader(o.PNG))
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Image) != 4 || g.Image[0].Bounds().Dx() > gifMaxWidth {
		t.Fatalf("%d images, largeur %d", len(g.Image), g.Image[0].Bounds().Dx())
	}
}

func TestMeshViewsSheet(t *testing.T) {
	vs, err := normalizeViews([]string{"all"})
	if err != nil || len(vs) != 4 {
		t.Fatalf("all : %v %v", vs, err)
	}
	vs2, err := normalizeViews([]string{"Top", "face", "top", "iso"})
	if err != nil || len(vs2) != 3 || vs2[0].key != "top" || vs2[1].key != "front" {
		t.Fatalf("alias/doublons : %v %v", vs2, err)
	}
	if _, err := normalizeViews([]string{"dessous"}); err == nil {
		t.Fatal("vue inconnue : erreur attendue")
	}
	o, err := renderMeshViews(table4Feet(), "table.stl", strings.Repeat("a", 64), vs, 800, 600)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(o.PNG))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 800 || img.Bounds().Dy() < 600 {
		t.Fatalf("taille %v", img.Bounds())
	}
	if o.Kind != "mesh_views" || fmt.Sprint(o.Render["views"]) != "[iso front side top]" {
		t.Fatalf("%+v", o.Render)
	}
	// Une seule vue : image pleine largeur.
	o1, err := renderMeshViews(table4Feet(), "t", "", vs[:1], 500, 400)
	if err != nil {
		t.Fatal(err)
	}
	img1, _ := png.Decode(bytes.NewReader(o1.PNG))
	if img1.Bounds().Dx() != 500 {
		t.Fatalf("vue seule : %v", img1.Bounds())
	}
	// La vue de dessus d'une table est un carré bleu centré ; les pieds ne dépassent pas : le coin reste du fond.
	top := projectMesh(table4Feet(), 0, 90, 200, 200)
	if c := top.RGBAAt(2, 2); c.R != 0xf3 || c.G != 0xf1 {
		t.Fatalf("coin de la vue de dessus : %v", c)
	}
}

func TestCapturePreviewNewInputsValidated(t *testing.T) {
	svc, cleanup := newBareService(t)
	defer cleanup()
	ctx := context.Background()
	for _, c := range []struct {
		in   PreviewInput
		want string
	}{
		{PreviewInput{Mode: "first_layer"}, "first_layer"},
		{PreviewInput{Format: "webp", ModelID: "m"}, "format"},
		{PreviewInput{Mode: "mesh", ModelID: "m", Layers: []int{1}}, "layers"},
		{PreviewInput{Mode: "layers", ArtifactID: "x", Views: []string{"iso"}}, "views"},
		{PreviewInput{ModelID: "m", Format: "gif"}, "gif"},
		{PreviewInput{Mode: "layers", ArtifactID: "x", MaxTiles: -1, EveryN: 2}, "positifs"},
	} {
		_, err := svc.CapturePreview(ctx, c.in)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v : erreur %v, attendu %q", c.in, err, c.want)
		}
	}
}
func TestLayerPathsBoundedAndSimplified(t *testing.T) {
	// 30 000 points quasi alignés : la réponse doit tenir dans le plafond, simplifiée.
	var b strings.Builder
	b.WriteString("G90\nM83\n; CHANGE_LAYER\n; Z_HEIGHT: 0.2\n; FEATURE: Sparse infill\nG1 X0 Y0 F3000\n")
	for i := 1; i <= 30000; i++ {
		fmt.Fprintf(&b, "G1 X%.3f Y%.3f E0.01\n", float64(i)*0.008, float64(i%2)*0.001)
	}
	b.WriteString("; CHANGE_LAYER\n; Z_HEIGHT: 0.4\nG1 X1 Y1 E1\n")
	p := zipGcode(t, b.String())
	o, err := buildLayerPaths(p, "s", 1, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if o.Points > maxLayerPoints || o.Segments != 30000 || o.ToleranceMM <= 0 || o.LayersTotal != 2 {
		t.Fatalf("points=%d segments=%d tol=%v total=%d", o.Points, o.Segments, o.ToleranceMM, o.LayersTotal)
	}
	// Couche simple : non simplifiée, coordonnées en 1/20 mm.
	o2, err := buildLayerPaths(gcodeLayers(t, 3), "s", 1, 2, 3)
	if err != nil || o2.ToleranceMM != 0 || len(o2.Paths) != 1 || len(o2.Paths[0].Points) != 6 || o2.UnitMM != 0.05 {
		t.Fatalf("%+v %v", o2, err)
	}
	if o2.Paths[0].Points[0] != 400 { // x = 20 mm = 400 unités de 0,05 mm
		t.Fatalf("quantification : %v", o2.Paths[0].Points)
	}
}
