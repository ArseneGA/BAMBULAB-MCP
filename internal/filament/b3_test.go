package filament_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// B3 : catalogue officiel des filaments Bambu : outils en lecture seule, rattachement facultatif d'une bobine.
func TestB3CatalogToolsAndSpoolLink(t *testing.T) {
	r := newRig(t)
	r.setup(0)

	// Les deux outils existent, en lecture seule, avec Title et outputSchema, sans champ d'approbation.
	tools, err := r.cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, tl := range tools.Tools {
		if tl.Name != "search_filament_catalog" && tl.Name != "get_filament_catalog_entry" {
			continue
		}
		found++
		if tl.Title == "" || tl.OutputSchema == nil || tl.Annotations == nil || !tl.Annotations.ReadOnlyHint || !tl.Annotations.IdempotentHint {
			t.Fatalf("%s : Title, outputSchema et annotations lecture seule attendus : %+v", tl.Name, tl)
		}
		raw, _ := json.Marshal(tl.InputSchema)
		for _, bad := range []string{"approve", "token", "nonce", "confirmation", "printer_id"} {
			if strings.Contains(string(raw), `"`+bad) {
				t.Fatalf("%s : champ %q interdit : %s", tl.Name, bad, raw)
			}
		}
	}
	if found != 2 {
		t.Fatalf("outils du catalogue : %d/2", found)
	}

	// Couleur officielle la plus proche d'un orange.
	s := r.call("search_filament_catalog", map[string]any{"hex": "#FF6B14", "family": "PLA Basic", "limit": 2})
	cols := s["colors"].([]any)
	first := cols[0].(map[string]any)
	if first["color"].(map[string]any)["id"] != "GFA00-10300" || first["quality"] != "exact" {
		t.Fatalf("plus proche : %v", first)
	}
	// Sans critère : aperçu des gammes, aucune couleur.
	ov := r.call("search_filament_catalog", map[string]any{})
	if len(ov["colors"].([]any)) != 0 || len(ov["families"].([]any)) < 40 {
		t.Fatalf("aperçu : %d couleurs, %d gammes", len(ov["colors"].([]any)), len(ov["families"].([]any)))
	}
	// Buse laiton : les gammes à fibres sont écartées.
	cf := r.call("search_filament_catalog", map[string]any{"family": "PA", "nozzle_diameter_mm": "0.4", "limit": 100})
	for _, f := range cf["families"].([]any) {
		if f.(map[string]any)["hardened_nozzle_required"] == true {
			t.Fatalf("gamme abrasive proposée sans acier trempé : %v", f)
		}
	}
	if msg := r.callErr("search_filament_catalog", map[string]any{"hex": "pas-une-couleur"}); !strings.Contains(msg, "invalide") {
		t.Fatalf("hex invalide : %s", msg)
	}
	e := r.call("get_filament_catalog_entry", map[string]any{"id": "GFN05", "nozzle_diameter_mm": "0.4"})
	if e["nozzle_compat"].(map[string]any)["ok"] != false || e["family"].(map[string]any)["confidence"] != "official_profile" {
		t.Fatalf("PA6-CF sur laiton : %v", e)
	}
	if r.callErr("get_filament_catalog_entry", map[string]any{"id": "GFZ99"}) == "" {
		t.Fatal("entrée inconnue acceptée")
	}

	// Rattachement d'une bobine : préremplit marque, nom, couleur, matière.
	sp := r.createSpool(map[string]any{"catalog_color_id": "GFA00-10300"})
	if sp["catalog_color_id"] != "GFA00-10300" || sp["color_hex"] != "#FF6A13" || sp["brand"] != "Bambu Lab" || sp["material"] != "PLA" || sp["name"] != "Orange" {
		t.Fatalf("bobine rattachée : %v", sp)
	}
	off := sp["official_color"].(map[string]any)
	if off["linked"] != true || off["id"] != "GFA00-10300" {
		t.Fatalf("official_color : %v", off)
	}
	// Une saisie explicite n'est pas écrasée ; sans rattachement, suggestion non liée.
	sp2 := r.createSpool(map[string]any{"material": "PLA", "name": "Mon orange", "color_hex": "#FF6A14"})
	o2 := sp2["official_color"].(map[string]any)
	if sp2["name"] != "Mon orange" || o2["linked"] != false || o2["id"] != "GFA00-10300" {
		t.Fatalf("suggestion : %v", sp2)
	}
	if _, has := sp2["catalog_color_id"]; has {
		t.Fatalf("aucun rattachement attendu : %v", sp2)
	}
	if msg := r.callErr("update_spool", map[string]any{"material": "PLA", "catalog_color_id": "XXX"}); !strings.Contains(msg, "inconnu") {
		t.Fatalf("id inconnu : %s", msg)
	}
	// Détacher.
	up := r.call("update_spool", map[string]any{"spool_id": sp["id"], "catalog_color_id": "-"})["spool"].(map[string]any)
	if _, has := up["catalog_color_id"]; has {
		t.Fatalf("détachement : %v", up)
	}
}
