package printer

import (
	"errors"
	"testing"
)

func TestFanPWMBounds(t *testing.T) {
	for pct, want := range map[int]int{0: 0, 1: 3, 25: 64, 50: 128, 100: 255} {
		got, err := FanPWM(pct)
		if err != nil || got != want {
			t.Fatalf("FanPWM(%d) = %d, %v (attendu %d)", pct, got, err, want)
		}
	}
	for _, bad := range []int{-1, 101, 1000, -255} {
		if _, err := FanPWM(bad); err == nil {
			t.Fatalf("FanPWM(%d) aurait dû échouer", bad)
		}
	}
}

func TestAccessoryPayloadsAreWhitelisted(t *testing.T) {
	l, err := lightPayload("1", LightChamber, true)
	if err != nil || ValidateAccessoryPayload(l) != nil {
		t.Fatalf("ledctrl nominal : %v %v", l, err)
	}
	if _, ok := l["system"]; !ok {
		t.Fatalf("ledctrl doit être un message system : %v", l)
	}
	for fan, idx := range map[FanKind]string{FanPart: "P1", FanAux: "P2", FanChamber: "P3"} {
		f, err := fanPayload("2", fan, 100)
		if err != nil || ValidateAccessoryPayload(f) != nil {
			t.Fatalf("fan %s : %v %v", fan, f, err)
		}
		if got := f["print"].(map[string]any)["param"]; got != "M106 "+idx+" S255\n" {
			t.Fatalf("fan %s param : %q", fan, got)
		}
	}
	for lv := SpeedLevel(1); lv <= 4; lv++ {
		sp, err := speedPayload("3", lv)
		if err != nil || ValidateAccessoryPayload(sp) != nil {
			t.Fatalf("speed %d : %v %v", lv, sp, err)
		}
	}
	if _, err := speedPayload("3", 0); err == nil {
		t.Fatal("vitesse 0 refusée")
	}
	if _, err := speedPayload("3", 5); err == nil {
		t.Fatal("vitesse 5 refusée")
	}
	if _, err := fanPayload("2", FanPart, 101); err == nil {
		t.Fatal("101 % refusé")
	}
	if _, err := fanPayload("2", FanKind("hotend"), 10); !errors.Is(err, ErrAccessoryNotAllowed) {
		t.Fatalf("ventilateur inconnu : %v", err)
	}
	if _, err := lightPayload("1", LightNode("logo_light"), true); !errors.Is(err, ErrAccessoryNotAllowed) {
		t.Fatalf("lumière inconnue : %v", err)
	}
}

// La liste blanche refuse tout ce qui n'est pas un accessoire : démarrage, arrêt, pause, reprise, G-code libre.
func TestAccessoryWhitelistRejectsEverythingElse(t *testing.T) {
	pr := func(cmd, param string) map[string]any {
		return map[string]any{"print": map[string]any{"sequence_id": "1", "command": cmd, "param": param}}
	}
	bad := []map[string]any{
		pr("stop", ""), pr("pause", ""), pr("resume", ""), pr("project_file", "Metadata/plate_1.gcode"),
		pr("ams_change_filament", ""), pr("print_speed", "5"), pr("print_speed", "0"), pr("print_speed", "2; M104 S300"),
		pr("gcode_line", "M104 S300\n"), pr("gcode_line", "G28\n"), pr("gcode_line", "M140 S120\n"), pr("gcode_line", "M999\n"),
		pr("gcode_line", "M106 P1 S300\n"), pr("gcode_line", "M106 P4 S10\n"), pr("gcode_line", "M106 P1 S10\nM104 S300\n"),
		pr("gcode_line", "M106 P1 S10"), pr("gcode_line", "M106 P1 S-5\n"), pr("gcode_line", ""),
		{"print": map[string]any{"sequence_id": "1", "command": "print_speed", "param": "2", "extra": "x"}},
		{"print": map[string]any{"command": "print_speed", "param": "2"}},
		{"system": map[string]any{"sequence_id": "1", "command": "ledctrl", "led_node": "chamber_light", "led_mode": "flashing",
			"led_on_time": 500, "led_off_time": 500, "loop_times": 0, "interval_time": 0}},
		{"system": map[string]any{"sequence_id": "1", "command": "ledctrl", "led_node": "hotend_light", "led_mode": "on",
			"led_on_time": 500, "led_off_time": 500, "loop_times": 0, "interval_time": 0}},
		{"system": map[string]any{"sequence_id": "1", "command": "reboot"}},
		{"pushing": map[string]any{"sequence_id": "1", "command": "pushall"}},
		{"print": map[string]any{"sequence_id": "1", "command": "stop", "param": ""}, "system": map[string]any{}},
		{},
	}
	for i, p := range bad {
		if err := ValidateAccessoryPayload(p); !errors.Is(err, ErrAccessoryNotAllowed) {
			t.Errorf("commande %d (%v) aurait dû être refusée par la liste blanche : %v", i, p, err)
		}
	}
}

func TestAccessoriesForModels(t *testing.T) {
	cases := []struct {
		model                          string
		chamber, work, part, aux, fanC bool
		known                          bool
	}{
		{"p1p", false, false, true, true, false, true},
		{"p1s", true, false, true, true, true, true},
		{"Bambu Lab P1S", true, false, true, true, true, true},
		{"x1c", true, false, true, true, true, true},
		{"Bambu Lab X1 Carbon", true, false, true, true, true, true},
		{"a1", false, true, true, true, false, true},
		{"Bambu Lab A1 mini", false, true, true, true, false, true},
		{"h2d", true, true, true, true, true, true},
		{"inconnue_2099", false, false, false, false, false, false},
	}
	for _, c := range cases {
		got, known := AccessoriesFor(c.model)
		if known != c.known || got.ChamberLight != c.chamber || got.WorkLight != c.work || got.PartFan != c.part || got.AuxFan != c.aux || got.ChamberFan != c.fanC {
			t.Errorf("%s : %+v known=%v", c.model, got, known)
		}
		if known && !got.SpeedLevels {
			t.Errorf("%s : la vitesse existe partout", c.model)
		}
		if !known && got.SpeedLevels {
			t.Errorf("%s : modèle inconnu, rien n'est proposé", c.model)
		}
	}
	p1p, _ := AccessoriesFor("p1p")
	if HasLight(p1p, LightChamber) || HasLight(p1p, LightWork) || !HasFan(p1p, FanAux) || HasFan(p1p, FanChamber) {
		t.Fatalf("P1P : %+v", p1p)
	}
}

func TestParseAccessories(t *testing.T) {
	if parseAccessories(map[string]any{"gcode_state": "IDLE"}) != nil {
		t.Fatal("aucun champ d'accessoire : nil")
	}
	a := parseAccessories(map[string]any{
		"lights_report":     []any{map[string]any{"node": "chamber_light", "mode": "on"}, map[string]any{"node": "work_light", "mode": "off"}},
		"cooling_fan_speed": "15", "big_fan1_speed": "0", "big_fan2_speed": float64(8), "spd_lvl": float64(3), "spd_mag": float64(124),
	})
	if a == nil || a.ChamberLight != "on" || a.WorkLight != "off" || *a.PartFanPercent != 100 || *a.AuxFanPercent != 0 || *a.ChamberFanPercent != 53 ||
		a.SpeedLevel != 3 || a.SpeedName != "sport" || a.SpeedPercentOfBase != 124 {
		t.Fatalf("%+v", a)
	}
}

func TestParseNames(t *testing.T) {
	for in, want := range map[string]SpeedLevel{"1": 1, "silent": 1, "Silencieux": 1, "normal": 2, "standard": 2, "sport": 3, "4": 4, "ludicrous": 4} {
		if got, err := ParseSpeed(in); err != nil || got != want {
			t.Errorf("ParseSpeed(%q) = %d %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "0", "5", "turbo"} {
		if _, err := ParseSpeed(bad); err == nil {
			t.Errorf("ParseSpeed(%q) aurait dû échouer", bad)
		}
	}
	if n, err := ParseLight("work"); err != nil || n != LightWork {
		t.Fatal(n, err)
	}
	if _, err := ParseLight("logo"); err == nil {
		t.Fatal("lumière inconnue")
	}
	if f, err := ParseFan("aux"); err != nil || f != FanAux {
		t.Fatal(f, err)
	}
	if _, err := ParseFan("hotend"); err == nil {
		t.Fatal("ventilateur inconnu")
	}
}
