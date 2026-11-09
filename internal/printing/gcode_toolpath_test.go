package printing

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// gcodeTypes : un .gcode.3mf de test avec tous les types de trajectoires de Bambu Studio, des déplacements à vide,
// des vitesses différentes et la durée annoncée par le slicer.
func gcodeTypes(t *testing.T, layers int) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("; HEADER_BLOCK_START\n; total estimated time: 1h 2m 3s\n; HEADER_BLOCK_END\nG90\nM83\n")
	types := []string{"Outer wall", "Inner wall", "Overhang wall", "Sparse infill", "Internal solid infill", "Top surface", "Bottom surface", "Bridge", "Support", "Support interface", "Brim", "Skirt", "Prime tower", "Gap infill"}
	for l := 1; l <= layers; l++ {
		fmt.Fprintf(&b, "; CHANGE_LAYER\n; Z_HEIGHT: %.2f\n; LAYER_HEIGHT: 0.2\nG1 Z%.2f F1200\n", float64(l)*0.2, float64(l)*0.2)
		for i, ty := range types {
			f := 1800 + 600*(i%3) // 30, 40, 50 mm/s
			fmt.Fprintf(&b, "; FEATURE: %s\nG1 X%d Y%d F6000\nG1 X%d Y%d E0.5 F%d\nG1 X%d Y%d E0.5\n", ty, 10+i*3, 10, 10+i*3, 40, f, 20+i*3, 40)
		}
		b.WriteString("G1 X0 Y0 E-0.8 F7200\n")
	}
	return zipGcode(t, b.String())
}

func loadTP(t *testing.T, path string, layers int) *Toolpath {
	t.Helper()
	zr, zf, err := openPlateGcode(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	tp, err := parseToolpath(zf, strings.Repeat("a", 64), 1, layers)
	if err != nil {
		t.Fatal(err)
	}
	return tp
}

func TestToolpathParsesAllFeatureTypes(t *testing.T) {
	p := gcodeTypes(t, 3)
	tp := loadTP(t, p, 3)
	if len(tp.Layers) != 3 {
		t.Fatalf("couches : %d", len(tp.Layers))
	}
	want := []string{"Outer wall", TravelFeature, "Inner wall", "Overhang wall", "Sparse infill", "Internal solid infill", "Top surface", "Bottom surface", "Bridge", "Support", "Support interface", "Brim", "Skirt", "Prime tower", "Gap infill"}
	if strings.Join(tp.Feats, "|") != strings.Join(want, "|") {
		t.Fatalf("types : %v", tp.Feats)
	}
	if tp.TravelFeat != 1 {
		t.Fatalf("index de Travel : %d", tp.TravelFeat)
	}
	if tp.TimeSource != "slicer" || math.Abs(tp.TotalTimeS-3723) > 0.01 {
		t.Fatalf("durée : %v %v", tp.TotalTimeS, tp.TimeSource)
	}
	sum := 0.0
	for _, ly := range tp.Layers {
		sum += ly.TimeS
	}
	if math.Abs(sum-3723) > 0.5 {
		t.Fatalf("la somme des temps de couche doit valoir la durée du slicer : %v", sum)
	}
	// 14 extrusions de 2 segments (0,5 mm de filament chacun) par couche ; la rétractation n'est pas comptée
	if math.Abs(tp.Layers[0].ExtrudedMM-14*2*0.5) > 1e-6 || math.Abs(tp.FilamentMM-3*14) > 1e-6 {
		t.Fatalf("filament : couche %v total %v", tp.Layers[0].ExtrudedMM, tp.FilamentMM)
	}
	if tp.Layers[1].Z != 0.4 {
		t.Fatalf("Z : %v", tp.Layers[1].Z)
	}
	// vitesses arrondies à 5 mm/s : 30, 40 et 50
	speeds := map[int]bool{}
	for _, p := range tp.Layers[0].Paths {
		if p.Feature != tp.TravelFeat {
			speeds[p.Speed] = true
		}
	}
	if !speeds[30] || !speeds[40] || !speeds[50] {
		t.Fatalf("vitesses : %v", speeds)
	}
	// codage : x0,y0 absolus puis différences ; la première polyligne Outer wall va de (10,40) à (20,40) puis (20,40)
	var outer TPPath
	for _, p := range tp.Layers[0].Paths {
		if p.Feature == 0 {
			outer = p
			break
		}
	}
	if len(outer.Points) != 6 || outer.Points[0] != 200 || outer.Points[1] != 200 || outer.Points[2] != 0 || outer.Points[3] != 600 || outer.Points[4] != 200 {
		t.Fatalf("codage différentiel : %v", outer.Points)
	}
	if tp.BBox[0] != 10 || tp.BBox[3] != 40 {
		t.Fatalf("emprise : %v", tp.BBox)
	}
}

func TestToolpathEstimateWithoutSlicerTime(t *testing.T) {
	p := zipGcode(t, "G90\nM83\n; CHANGE_LAYER\n; Z_HEIGHT: 0.2\n; FEATURE: Outer wall\nG1 X0 Y0 F6000\nG1 X60 Y0 E1 F3000\n")
	tp := loadTP(t, p, 1)
	// déplacement de 0 à (0,0) : nul ; extrusion de 60 mm à 50 mm/s = 1,2 s
	if tp.TimeSource != "estimation" || math.Abs(tp.TotalTimeS-1.2) > 0.05 {
		t.Fatalf("estimation : %v %v", tp.TotalTimeS, tp.TimeSource)
	}
	if len(tp.Feats) == 0 || tp.Feats[0] != "Outer wall" {
		t.Fatalf("types %v", tp.Feats)
	}
}

func TestToolpathSliceChunksAndCaps(t *testing.T) {
	oldT, oldL, oldS := tpMaxTotalPoints, tpMaxLayerPoints, TPSlicePoints
	defer func() { tpMaxTotalPoints, tpMaxLayerPoints, TPSlicePoints = oldT, oldL, oldS }()
	tpMaxTotalPoints, tpMaxLayerPoints, TPSlicePoints = 4000, 400, 1000

	// 20 couches de 1 000 segments en spirale : bien au-dessus du budget par couche (200 = 4000/20)
	var b strings.Builder
	b.WriteString("G90\nM83\n")
	for l := 1; l <= 20; l++ {
		fmt.Fprintf(&b, "; CHANGE_LAYER\n; Z_HEIGHT: %.2f\n; FEATURE: Sparse infill\nG1 X100 Y100 F3000\n", float64(l)*0.2)
		for i := 1; i <= 1000; i++ {
			a := float64(i) * 0.05
			fmt.Fprintf(&b, "G1 X%.3f Y%.3f E0.01\n", 100+30*math.Cos(a)*(1+float64(i)/2000), 100+30*math.Sin(a)*(1+float64(i)/2000))
		}
		// des déplacements à vide en dents de scie
		for i := 0; i < 600; i++ {
			fmt.Fprintf(&b, "G1 X%d Y%d F9000\n", 20+(i%7)*3, 20+(i%5)*4+i/10)
		}
	}
	tp := loadTP(t, zipGcode(t, b.String()), 20)
	if !tp.Simplified || tp.MaxTolMM <= 0 || tp.LayerBudget != 200 {
		t.Fatalf("budget : simplifié=%v tol=%v budget=%d", tp.Simplified, tp.MaxTolMM, tp.LayerBudget)
	}
	for i, ly := range tp.Layers {
		ext := 0
		for _, p := range ly.Paths {
			if p.Feature != tp.TravelFeat {
				ext += len(p.Points) / 2
			}
		}
		if ext > tp.LayerBudget {
			t.Fatalf("couche %d : %d points d'extrusion > budget %d", i+1, ext, tp.LayerBudget)
		}
	}
	// tranches : jamais plus de TPSlicePoints (sauf une couche seule), enchaînement complet sans trou ni doublon
	seen := 0
	next := 1
	for calls := 0; next != 0; calls++ {
		if calls > 100 {
			t.Fatal("trop de tranches")
		}
		sl, err := tp.slice(next, 0, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(sl.Layers) > 1 && sl.Points > TPSlicePoints {
			t.Fatalf("tranche de %d points > %d", sl.Points, TPSlicePoints)
		}
		for _, ly := range sl.Layers {
			seen++
			if ly.Layer != seen {
				t.Fatalf("couche %d attendue, reçu %d", seen, ly.Layer)
			}
			for _, p := range ly.Paths {
				if p.Feature == tp.TravelFeat {
					t.Fatal("déplacement à vide servi sans with_travel")
				}
			}
		}
		if sl.Done != (sl.Next == 0) {
			t.Fatalf("done/next incohérents : %+v", sl.Done)
		}
		next = sl.Next
	}
	if seen != 20 {
		t.Fatalf("couches servies : %d", seen)
	}
	with, _ := tp.slice(1, 0, true)
	travel := 0
	for _, p := range with.Layers[0].Paths {
		if p.Feature == tp.TravelFeat {
			travel++
		}
	}
	if travel == 0 {
		t.Fatal("with_travel : déplacements attendus")
	}
	if _, err := tp.slice(21, 0, false); err == nil {
		t.Fatal("couche hors plage : erreur attendue")
	}
	idx := tp.index()
	if idx.LayersTotal != 20 || len(idx.Z) != 20 || idx.Z[19] != 4 || idx.MaxTotalPoints != 4000 || idx.SlicePoints != 1000 || !idx.Simplified {
		t.Fatalf("index : %+v", idx)
	}
	if !strings.Contains(idx.SummaryLine(), "20 couches") {
		t.Fatal(idx.SummaryLine())
	}
}

func TestToolpathLayerLimit(t *testing.T) {
	old := tpMaxLayers
	defer func() { tpMaxLayers = old }()
	tpMaxLayers = 3
	p := gcodeLayers(t, 5)
	if _, err := toolpathFor(p, strings.Repeat("c", 64), 1); err == nil || !strings.Contains(err.Error(), "couches") {
		t.Fatalf("plafond de couches : %v", err)
	}
}

func TestToolpathCacheReadsOnce(t *testing.T) {
	p := gcodeLayers(t, 4)
	sha := strings.Repeat("d", 64)
	a, err := toolpathFor(p, sha, 1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := toolpathFor(p, sha, 1)
	if a != b {
		t.Fatal("le cache doit rendre le même résultat sans relire le fichier")
	}
}

