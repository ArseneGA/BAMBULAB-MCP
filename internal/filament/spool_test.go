package filament_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer/sim"
)

func trayOf(v map[string]any, slot string) map[string]any {
	for _, t := range v["trays"].([]any) {
		if tm := t.(map[string]any); tm["slot"] == slot {
			return tm
		}
	}
	return nil
}

// TestAMSStateAndReconciliation : l'AMS simulé (4 emplacements) est lu, rapproché de l'inventaire, sans jamais
// écraser la saisie de l'humain.
func TestAMSStateAndReconciliation(t *testing.T) {
	r := newRig(t)
	r.setup(0)

	// Inventaire vide : l'AMS est lu et chaque emplacement est « unregistered ».
	v := r.call("get_ams_state", map[string]any{"printer_id": r.printerID})
	if v["ams_present"] != true || len(v["trays"].([]any)) != 4 {
		t.Fatalf("AMS : %v", v)
	}
	t1 := trayOf(v, "ams1:1")
	if t1["type"] != "PLA" || t1["color"] != "#FFFFFF" || t1["match"] != "unregistered" || t1["remain_percent"].(float64) != 100 {
		t.Fatalf("ams1:1 : %v", t1)
	}
	if trayOf(v, "ams1:3")["remain_percent"].(float64) != -1 {
		t.Fatalf("restant inconnu attendu pour la PETG : %v", trayOf(v, "ams1:3"))
	}

	// Une bobine saisie par l'humain (restant mesuré à la balance) dans l'emplacement 2 (PLA noir lu à 85 %).
	sp := r.createSpool(map[string]any{"material": "PLA", "brand": "Bambu", "name": "Noir", "color_hex": "#000000", "net_weight_g": 1000,
		"remaining_g": 640, "price_per_kg": 22.5, "printer_id": r.printerID, "slot": "ams1:2", "opened_at": "2026-10-01"})
	if sp["remaining_source"] != "human" || sp["slot"] != "ams1:2" || sp["remaining_percent"].(float64) != 64 {
		t.Fatalf("bobine : %v", sp)
	}
	v = r.call("get_ams_state", map[string]any{"printer_id": r.printerID})
	t2 := trayOf(v, "ams1:2")
	if t2["match"] != "assigned" || t2["spool"].(map[string]any)["id"] != sp["id"] {
		t.Fatalf("ams1:2 : %v", t2)
	}
	got := t2["spool"].(map[string]any)
	// L'AMS dit 85 % mais l'humain a saisi 640 g : la saisie est conservée, divergence signalée, lecture consignée à part.
	if got["remaining_g"].(float64) != 640 || got["remaining_source"] != "human" {
		t.Fatalf("la saisie humaine a été écrasée : %v", got)
	}
	if obs := got["observed"].(map[string]any); obs["remain_percent"].(float64) != 85 || obs["type"] != "PLA" {
		t.Fatalf("lecture AMS non consignée : %v", got["observed"])
	}
	if !strings.Contains(fmt.Sprint(t2["warnings"]), "diffère") {
		t.Fatalf("divergence non signalée : %v", t2["warnings"])
	}
	if got["rfid_uuid"] == "" || got["rfid_uuid"] == nil {
		t.Fatalf("étiquette RFID non rattachée : %v", got)
	}

	// Une bobine NON saisie (source default) dans l'emplacement 4 : son restant suit le % de l'AMS (40 %).
	sp4 := r.createSpool(map[string]any{"material": "PLA", "name": "Bleu", "color_hex": "#1060D0", "printer_id": r.printerID, "slot": "ams1:4"})
	v = r.call("get_ams_state", map[string]any{"printer_id": r.printerID})
	g4 := trayOf(v, "ams1:4")["spool"].(map[string]any)
	if g4["id"] != sp4["id"] || g4["remaining_g"].(float64) != 400 || g4["remaining_source"] != "ams" {
		t.Fatalf("restant déduit de l'AMS : %v", g4)
	}
	// Puis l'humain re-saisit : l'AMS ne l'écrase plus.
	r.call("update_spool", map[string]any{"spool_id": sp4["id"], "remaining_g": 333})
	r.sim.SetAMSTray(1, 4, sim.AMSTrayConfig{Type: "PLA", Color: "1060D0", RemainPercent: 30, WeightG: 1000})
	time.Sleep(300 * time.Millisecond)
	v = r.call("get_ams_state", map[string]any{"printer_id": r.printerID})
	g4 = trayOf(v, "ams1:4")["spool"].(map[string]any)
	if g4["remaining_g"].(float64) != 333 || g4["remaining_source"] != "human" {
		t.Fatalf("saisie humaine écrasée : %v", g4)
	}

	// Divergence de lecture : l'humain recharge du PETG rouge dans l'emplacement 2 sans mettre l'inventaire à jour.
	r.sim.SetAMSTray(1, 2, sim.AMSTrayConfig{Type: "PETG", Color: "D01010", RemainPercent: 100})
	waitFor(t, 5*time.Second, "lecture PETG", func() bool {
		return trayOf(r.call("get_ams_state", map[string]any{"printer_id": r.printerID}), "ams1:2")["type"] == "PETG"
	})
	v = r.call("get_ams_state", map[string]any{"printer_id": r.printerID})
	t2 = trayOf(v, "ams1:2")
	if t2["match"] != "mismatch" || !strings.Contains(fmt.Sprint(t2["warnings"]), "PETG") {
		t.Fatalf("divergence : %v", t2)
	}
	if after := t2["spool"].(map[string]any); after["material"] != "PLA" || after["color_hex"] != "#000000" {
		t.Fatalf("l'inventaire ne doit pas être modifié par la lecture : %v", after)
	}

	// Bac vidé : « empty », avec avertissement si une bobine y est inscrite.
	r.sim.SetAMSTray(1, 4, sim.AMSTrayConfig{Empty: true, RemainPercent: -1})
	waitFor(t, 5*time.Second, "bac vide", func() bool {
		return trayOf(r.call("get_ams_state", map[string]any{"printer_id": r.printerID}), "ams1:4")["empty"] == true
	})
	if tr := trayOf(r.call("get_ams_state", map[string]any{"printer_id": r.printerID}), "ams1:4"); tr["match"] != "empty" || !strings.Contains(fmt.Sprint(tr["warnings"]), "aucun filament") {
		t.Fatalf("bac vide : %v", tr)
	}
}

// TestAMSSuggestionAndRFID : bobine rangée proposée d'après matière + couleur ; rattachement automatique par RFID.
func TestAMSSuggestionAndRFID(t *testing.T) {
	r := newRig(t)
	r.setup(0)
	// Rangée, couleur proche du PLA blanc lu en ams1:1 (RFID inconnu) : proposition, pas d'inscription automatique.
	near := r.createSpool(map[string]any{"material": "PLA", "name": "Blanc cassé", "color_hex": "#F4F4F0"})
	v := r.call("get_ams_state", map[string]any{"printer_id": r.printerID})
	t1 := trayOf(v, "ams1:1")
	if t1["match"] != "suggested" || t1["suggested_spool"].(map[string]any)["id"] != near["id"] || t1["spool"] != nil {
		t.Fatalf("proposition : %v", t1)
	}
	if sp := r.call("list_spools", map[string]any{})["spools"].([]any)[0].(map[string]any); sp["slot"] != nil && sp["slot"] != "" {
		t.Fatalf("une proposition ne doit rien inscrire : %v", sp)
	}
	// Une bobine rangée dont l'étiquette RFID est celle de ams1:2 : chargée d'office (emplacement libre).
	rfid := "A1B2C3D4E5F60718293A4B5C6D7E8F02"
	rf := r.createSpool(map[string]any{"material": "PLA", "name": "Noir RFID", "color_hex": "#000000"})
	if err := r.c.Filament.SetRFIDForTest(rf["id"].(string), rfid); err != nil {
		t.Fatal(err)
	}
	v = r.call("get_ams_state", map[string]any{"printer_id": r.printerID})
	t2 := trayOf(v, "ams1:2")
	if t2["match"] != "rfid_assigned" || t2["spool"].(map[string]any)["id"] != rf["id"] || t2["spool"].(map[string]any)["slot"] != "ams1:2" {
		t.Fatalf("RFID : %v", t2)
	}
}

// TestSpoolCRUDAndGuards : validations, emplacement unique, archivage, filtres, marge.
func TestSpoolCRUDAndGuards(t *testing.T) {
	r := newRig(t)
	r.setup(0)
	// Création sans matière : plus une erreur, la carte-formulaire (pré-remplie) est affichée et rien n'est créé.
	if v := r.call("update_spool", map[string]any{"brand": "X"}); v["form"] == nil || v["spool"] != nil {
		t.Fatalf("création sans matière : carte attendue, rien de créé : %v", v)
	}
	if r.callErr("update_spool", map[string]any{"material": "PLA", "color_hex": "rouge"}) == "" {
		t.Fatal("couleur invalide")
	}
	if r.callErr("update_spool", map[string]any{"material": "PLA", "slot": "ams9:1", "printer_id": r.printerID}) == "" {
		t.Fatal("emplacement invalide")
	}
	if r.callErr("update_spool", map[string]any{"material": "PLA", "slot": "ams1:1"}) == "" {
		t.Fatal("emplacement sans imprimante")
	}
	if r.callErr("update_spool", map[string]any{"material": "PLA", "slot": "ams1:1", "printer_id": "pr_inconnue"}) == "" {
		t.Fatal("imprimante inconnue")
	}
	if r.callErr("update_spool", map[string]any{"material": "PLA", "remaining_g": -3}) == "" {
		t.Fatal("restant négatif")
	}
	a := r.createSpool(map[string]any{"material": "PETG", "brand": "Prusament", "net_weight_g": 850, "price_per_kg": 30, "last_dried_at": "today", "drying_note": "65 °C 6 h"})
	if a["remaining_g"].(float64) != 850 || a["remaining_source"] != "default" || a["last_dried_at"] == "" || a["status"] != "ok" {
		t.Fatalf("création : %v", a)
	}
	aid := a["id"].(string)
	b := r.createSpool(map[string]any{"material": "PLA", "printer_id": r.printerID, "slot": "ext"})
	// Emplacement pris : refus, puis remplacement explicite.
	if txt := r.callErr("update_spool", map[string]any{"spool_id": aid, "printer_id": r.printerID, "slot": "ext"}); !strings.Contains(txt, "déjà") {
		t.Fatalf("emplacement pris : %s", txt)
	}
	r.call("update_spool", map[string]any{"spool_id": aid, "printer_id": r.printerID, "slot": "ext", "replace_existing": true})
	if got := r.call("list_spools", map[string]any{"printer_id": r.printerID})["spools"].([]any); len(got) != 1 || got[0].(map[string]any)["id"] != aid {
		t.Fatalf("après remplacement : %v", got)
	}
	// Presque vide, filtre low_only.
	r.call("update_spool", map[string]any{"spool_id": b["id"], "remaining_g": 20})
	low := r.call("list_spools", map[string]any{"low_only": true})["spools"].([]any)
	if len(low) != 1 || low[0].(map[string]any)["status"] != "low" {
		t.Fatalf("low_only : %v", low)
	}
	r.call("update_spool", map[string]any{"spool_id": b["id"], "remaining_g": 0})
	if st := r.call("list_spools", map[string]any{"material": "PLA"})["spools"].([]any)[0].(map[string]any)["status"]; st != "empty" {
		t.Fatalf("empty : %v", st)
	}
	// Archivage : libère l'emplacement, disparaît de la liste.
	r.call("update_spool", map[string]any{"spool_id": aid, "archived": true})
	if n := len(r.call("list_spools", map[string]any{})["spools"].([]any)); n != 1 {
		t.Fatalf("archivée encore listée : %d", n)
	}
	if n := len(r.call("list_spools", map[string]any{"include_archived": true})["spools"].([]any)); n != 2 {
		t.Fatalf("include_archived : %d", n)
	}
	// Marge d'alerte configurable.
	m := r.call("set_filament_margin", map[string]any{"margin_percent": 25, "margin_grams": 12})
	if m["alert_margin"].(map[string]any)["margin_percent"].(float64) != 25 {
		t.Fatalf("marge : %v", m)
	}
	if r.callErr("set_filament_margin", map[string]any{"margin_percent": -1, "margin_grams": 0}) == "" {
		t.Fatal("marge négative")
	}
}
