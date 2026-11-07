package printing

import (
	"strings"
	"testing"
)

func TestNormalizeSettings(t *testing.T) {
	got, err := normalizeSettings(map[string]any{"sparse_infill_density": 100, "wall_loops": 4.0, "layer_height": "0.12",
		"sparse_infill_pattern": "gyroid", "brim_type": "outer_only", "brim_width": 5, "enable_support": true, "top_shell_layers": 6}, ObjectSettingKeys)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"sparse_infill_density": "100%", "wall_loops": "4", "layer_height": "0.12", "sparse_infill_pattern": "gyroid",
		"brim_type": "outer_only", "brim_width": "5", "enable_support": "1", "top_shell_layers": "6"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, attendu %q", k, got[k], v)
		}
	}
	if g, _ := normalizeSettings(map[string]any{"sparse_infill_density": "15%"}, ObjectSettingKeys); g["sparse_infill_density"] != "15%" {
		t.Errorf("pourcentage texte : %v", g)
	}
	for name, in := range map[string]map[string]any{
		"clé inconnue":     {"speed": 3},
		"densité > 100":    {"sparse_infill_density": 101},
		"densité texte":    {"sparse_infill_density": "beaucoup"},
		"parois décimales": {"wall_loops": 2.5},
		"parois zéro":      {"wall_loops": 0},
		"couche 3 mm":      {"layer_height": 3},
		"motif inconnu":    {"sparse_infill_pattern": "spirale"},
		"bordure inconnue": {"brim_type": "enorme"},
		"support texte":    {"enable_support": "oui"},
	} {
		if _, err := normalizeSettings(in, ObjectSettingKeys); err == nil {
			t.Errorf("%s : accepté", name)
		}
	}
	if _, err := normalizeSettings(map[string]any{"speed": 1}, ObjectSettingKeys); err == nil || !strings.Contains(err.Error(), "sparse_infill_density") {
		t.Errorf("le message doit lister les clés acceptées : %v", err)
	}
}

func TestPackEstimate(t *testing.T) {
	bed := PlateBed{X: 256, Y: 256, Z: 250, Exclusions: [][4]float64{{0, 0, 20, 28}}}
	// 9 carrés de 80 mm : 3 par rangée à l'écart de 8 mm (3*80+2*8 = 256 > 248 utile) -> 2 par rangée seulement
	var items []packItem
	for i := 0; i < 9; i++ {
		items = append(items, packItem{idx: i, w: 80, d: 80, canRot: true})
	}
	pos := packEstimate(items, bed, 8, 16)
	plates := map[int]int{}
	for i, p := range pos {
		if p.plate < 1 {
			t.Fatalf("pièce %d non placée", i)
		}
		plates[p.plate]++
		if p.x < 4-1e-9 || p.y < 4-1e-9 || p.x+80 > 252+1e-9 || p.y+80 > 252+1e-9 {
			t.Errorf("pièce %d hors plateau utile : %+v", i, p)
		}
		// jamais dans la zone interdite
		if p.x < 20 && p.y < 28 {
			t.Errorf("pièce %d dans la zone interdite : %+v", i, p)
		}
	}
	// pas de chevauchement (écart respecté) dans chaque plaque
	for i := range items {
		for j := i + 1; j < len(items); j++ {
			a, b := pos[i], pos[j]
			if a.plate != b.plate {
				continue
			}
			if a.x < b.x+80+8-1e-9 && b.x < a.x+80+8-1e-9 && a.y < b.y+80+8-1e-9 && b.y < a.y+80+8-1e-9 {
				t.Errorf("pièces %d et %d trop proches : %+v %+v", i, j, a, b)
			}
		}
	}
	if len(plates) < 2 {
		t.Errorf("9 carrés de 80 mm ne tiennent pas sur une plaque : %v", plates)
	}
	// plafond de plaques : la pièce en trop n'est pas placée
	one := packEstimate(items, bed, 8, 1)
	n := 0
	for _, p := range one {
		if p.plate == 0 {
			n++
		}
	}
	if n == 0 {
		t.Error("max_plates=1 devrait laisser des pièces non placées")
	}
	// rotation : 240 x 20 tient dans 256 x 256 ; 20 x 240 aussi
	if ok, _, _ := fitsAlone(240, 20, 10, bed, 8); !ok {
		t.Error("240 x 20 devrait tenir")
	}
	if ok, rot, _ := fitsAlone(300, 20, 10, bed, 8); ok || rot {
		t.Error("300 mm ne tient pas")
	}
	if ok, _, why := fitsAlone(20, 20, 300, bed, 8); ok || !strings.Contains(why, "hauteur") {
		t.Errorf("hauteur : %v %q", ok, why)
	}
}
