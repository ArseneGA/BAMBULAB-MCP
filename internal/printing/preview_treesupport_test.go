package printing

import (
	"fmt"
	"strings"
	"testing"
)

// gcodeTreeSupports : 20 couches de pièce (Z 0,2 à 4,0) ; entre les couches 5 à 14, une couche intercalaire de
// supports seuls (Z + 0,1), comme les supports en arbre à hauteur de couche propre. Les couches 3 et 4 portent
// pièce ET supports (elles restent des couches de la pièce). Total brut : 30 marqueurs CHANGE_LAYER.
func gcodeTreeSupports(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("G90\nM83\n")
	for l := 1; l <= 20; l++ {
		z := float64(l) * 0.2
		fmt.Fprintf(&b, "; CHANGE_LAYER\n; Z_HEIGHT: %.2f\n; FEATURE: Outer wall\nG1 X20 Y20 F3000\nG1 X%d Y20 E1\n", z, 20+l*3)
		if l == 3 || l == 4 {
			b.WriteString("; FEATURE: Support\nG1 X90 Y90 F3000\nG1 X95 Y90 E1\n")
		}
		if l >= 5 && l <= 14 {
			fmt.Fprintf(&b, "; CHANGE_LAYER\n; Z_HEIGHT: %.2f\n; FEATURE: Support\nG1 X90 Y90 F3000\nG1 X95 Y90 E1\n; FEATURE: Support interface\nG1 X90 Y92 F3000\nG1 X95 Y92 E1\n", z+0.1)
		}
	}
	return zipGcode(t, b.String())
}

func TestLayerSheetIgnoresSupportOnlyLayers(t *testing.T) {
	p := gcodeTreeSupports(t)
	o, err := renderLayerSheet(p, strings.Repeat("e", 64), 1, sheetParams{MaxTiles: 5}, 900, 700)
	if err != nil {
		t.Fatal(err)
	}
	if o.Render["layers_total"] != 20 || o.Render["layers_raw_total"] != 30 || o.Render["support_only_layers_skipped"] != 10 {
		t.Fatalf("comptes : %+v", o.Render)
	}
	if len(o.Layers) != 5 || o.Layers[0] != 1 || o.Layers[4] != 20 {
		t.Fatalf("couches choisies : %v", o.Layers)
	}
	leg := strings.Join(o.Legend, "\n")
	if !strings.Contains(leg, "sur 20 -") || !strings.Contains(leg, "supports seuls") {
		t.Fatalf("légende :\n%s", leg)
	}
}

func TestLayerSheetTreeSupportZMatchesPart(t *testing.T) {
	p := gcodeTreeSupports(t)
	zr, zf, err := openPlateGcode(p, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	metas, err := indexGcodeLayers(zf)
	if err != nil {
		t.Fatal(err)
	}
	raw, skipped := partLayers(metas)
	if len(metas) != 30 || len(raw) != 20 || skipped != 10 {
		t.Fatalf("metas=%d part=%d skipped=%d", len(metas), len(raw), skipped)
	}
	gs, err := scanGcode(zf, map[int]bool{raw[5]: true})
	if err != nil {
		t.Fatal(err)
	}
	if z := gs.Layers[raw[5]].Z; z < 1.19 || z > 1.21 {
		t.Fatalf("Z de la couche 6 de la pièce = %.2f, attendu 1,20", z)
	}
	o, err := renderLayerSheet(p, strings.Repeat("e", 64), 1, sheetParams{Layers: []int{6, 20}}, 900, 700)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Layers) != 2 || o.Layers[0] != 6 {
		t.Fatalf("%v", o.Layers)
	}
	if _, err := renderLayerSheet(p, strings.Repeat("e", 64), 1, sheetParams{Layers: []int{21}}, 900, 700); err == nil {
		t.Fatal("la couche 21 n'existe pas (20 couches de pièce)")
	}
}

func TestLayerSheetWithoutSupportsUnchanged(t *testing.T) {
	o, err := renderLayerSheet(gcodeLayers(t, 12), strings.Repeat("f", 64), 1, sheetParams{}, 900, 700)
	if err != nil {
		t.Fatal(err)
	}
	if o.Render["layers_total"] != 12 || o.Render["support_only_layers_skipped"] != 0 {
		t.Fatalf("%+v", o.Render)
	}
}
