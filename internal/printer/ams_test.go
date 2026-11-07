package printer

import (
	"encoding/json"
	"testing"
)

// Rapport P1S plausible (forme décrite par la communauté : à confirmer sur le matériel, voir docs/UTILISATION.md).
const p1sAMSReport = `{
 "tray_now": "1",
 "ams": {
  "ams": [
   {"id": "0", "humidity": "4", "temp": "24.5", "tray": [
     {"id": "0", "remain": 100, "tray_type": "PLA", "tray_color": "FFFFFFFF", "tray_sub_brands": "Bambu PLA Basic", "tray_uuid": "A1B2C3D4E5F60718293A4B5C6D7E8F01", "tray_weight": "1000", "tray_diameter": "1.75", "nozzle_temp_min": "190", "nozzle_temp_max": "230", "tray_info_idx": "GFA00"},
     {"id": "1", "remain": 85, "tray_type": "PETG", "tray_color": "d01010ff", "tray_uuid": "00000000000000000000000000000000"},
     {"id": "2"},
     {"id": "3", "remain": -1, "tray_type": "TPU", "tray_color": "00000000"}
   ]},
   {"id": "1", "humidity": "2", "tray": [{"id": "0", "remain": 10, "tray_type": "PLA", "tray_color": "112233FF"}]},
   {"id": "7", "tray": [{"id": "0", "tray_type": "PLA"}]}
  ],
  "tray_exist_bits": "b"
 },
 "vt_tray": {"id": "254", "tray_type": "ABS", "tray_color": "FF8800FF", "remain": 100}
}`

func mergedAMS(t *testing.T, reports ...string) *AMSState {
	t.Helper()
	m := newReportMerger()
	for _, r := range reports {
		var d map[string]any
		if err := json.Unmarshal([]byte(r), &d); err != nil {
			t.Fatal(err)
		}
		m.merge(d)
	}
	return m.toStatus().AMS
}

func TestParseAMSFromReport(t *testing.T) {
	a := mergedAMS(t, p1sAMSReport)
	if a == nil || !a.Present || len(a.Units) != 2 {
		t.Fatalf("AMS : %+v", a)
	}
	if a.TrayNow != "1" || a.ActiveSlot != "ams1:2" {
		t.Fatalf("emplacement actif : %q %q", a.TrayNow, a.ActiveSlot)
	}
	u0 := a.Units[0]
	if u0.Unit != 1 || u0.Humidity != 4 || u0.TempC != 24.5 || len(u0.Trays) != 4 {
		t.Fatalf("unité 1 : %+v", u0)
	}
	t0, t1, t2, t3 := u0.Trays[0], u0.Trays[1], u0.Trays[2], u0.Trays[3]
	if t0.Slot != "ams1:1" || t0.Type != "PLA" || t0.Color != "#FFFFFF" || t0.RemainPercent != 100 || t0.WeightG != 1000 || t0.UUID == "" || t0.Empty || t0.Active {
		t.Fatalf("ams1:1 : %+v", t0)
	}
	if t1.Slot != "ams1:2" || t1.Color != "#D01010" || t1.UUID != "" || !t1.Active {
		t.Fatalf("ams1:2 (étiquette à zéros = pas de RFID) : %+v", t1)
	}
	if !t2.Empty || t2.RemainPercent != -1 || t2.Slot != "ams1:3" {
		t.Fatalf("bac vide : %+v", t2)
	}
	if t3.Type != "TPU" || t3.Color != "" || t3.Empty || t3.RemainPercent != -1 {
		t.Fatalf("couleur nulle : %+v", t3)
	}
	if a.Units[1].Unit != 2 || a.Units[1].Trays[0].Slot != "ams2:1" {
		t.Fatalf("unité 2 : %+v", a.Units[1])
	}
	// L'unité d'identifiant 7 (hors 0..3) est ignorée sans erreur.
	if a.External == nil || a.External.Slot != "ext" || a.External.Type != "ABS" || a.External.Color != "#FF8800" {
		t.Fatalf("bobine externe : %+v", a.External)
	}
	if n := len(a.Trays()); n != 6 {
		t.Fatalf("bacs à plat : %d", n)
	}
}

func TestAMSActiveSlotAndPartialMerge(t *testing.T) {
	// tray_now dans l'objet ams ; "254" = bobine externe ; un rapport partiel ne perd pas l'AMS déjà connu.
	a := mergedAMS(t, p1sAMSReport, `{"tray_now": "254"}`)
	if a.ActiveSlot != "ext" || !a.External.Active {
		t.Fatalf("externe active : %+v", a)
	}
	a = mergedAMS(t, p1sAMSReport, `{"gcode_state": "RUNNING"}`)
	if a == nil || len(a.Units) != 2 {
		t.Fatal("un rapport partiel sans AMS ne doit pas effacer l'AMS")
	}
	a = mergedAMS(t, `{"ams": {"tray_now": "255", "ams": [{"id":"0","tray":[{"id":"0","tray_type":"PLA","tray_color":"00AE42FF"}]}]}}`)
	if a.ActiveSlot != "" || a.Units[0].Trays[0].Color != "#00AE42" {
		t.Fatalf("aucun actif : %+v", a)
	}
	if mergedAMS(t, `{"gcode_state": "IDLE"}`) != nil {
		t.Fatal("sans AMS ni bobine externe : nil")
	}
	// Valeurs hors plage / types inattendus : pas de panique.
	a = mergedAMS(t, `{"ams": {"ams": [{"id": 0, "tray": [{"id": 0, "remain": 250, "tray_type": 5}]}]}}`)
	if a.Units[0].Trays[0].RemainPercent != -1 {
		t.Fatalf("remain hors plage : %+v", a.Units[0].Trays[0])
	}
}
