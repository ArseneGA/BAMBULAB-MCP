package goals

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// Chaque levier de rules.json doit viser une clé de la liste blanche des surcharges, sur la bonne cible : sinon
// le moteur proposerait des variantes que le tranchage refuserait.
func TestRuleLeversAreWhitelisted(t *testing.T) {
	b, err := os.ReadFile("data/rules.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	n := 0
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if k, ok := x["key"].(string); ok {
				if tg, ok := x["target"].(string); ok {
					n++
					sp, found := slicer.SpecFor(k)
					if !found {
						t.Errorf("levier %v : la clé %q n'est pas dans la liste blanche", x["id"], k)
					} else if sp.Target != tg {
						t.Errorf("levier %v : %q est une clé %s dans la liste blanche, le levier vise %s", x["id"], k, sp.Target, tg)
					}
				}
			}
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(doc)
	if n < 20 {
		t.Fatalf("seulement %d leviers trouvés dans rules.json", n)
	}
	t.Logf("%d leviers vérifiés", n)
}

// modify_slicing_profile : le code G, les températures et les vitesses hors bornes sont refusés avant toute
// révision de profil ; les valeurs permises passent.
func TestModifyProfileRefusesGcodeAndOutOfBounds(t *testing.T) {
	st := installProfiles(t)
	e := newEnv(t, st, nil, false)
	base := &BaseSpec{Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}}
	for name, ov := range map[string]slicer.Override{
		"machine_start_gcode":       {Key: "machine_start_gcode", Value: "M104 S350"},
		"machine_end_gcode":         {Key: "machine_end_gcode", Value: "M104 S350"},
		"layer_change_gcode":        {Key: "layer_change_gcode", Value: "M104 S350"},
		"before_layer_change_gcode": {Key: "before_layer_change_gcode", Value: "M104 S350"},
		"change_filament_gcode":     {Key: "change_filament_gcode", Value: "M104 S350"},
		"time_lapse_gcode":          {Key: "time_lapse_gcode", Value: "M104 S350"},
		"template_custom_gcode":     {Key: "template_custom_gcode", Value: "M104 S350"},
		"printing_by_object_gcode":  {Key: "printing_by_object_gcode", Value: "M104 S350"},
		"400 °C":                    {Key: "nozzle_temperature", Value: 400},
		"vitesse au-delà du max":    {Key: "outer_wall_speed", Value: 1500},
		"accélération au-delà":      {Key: "default_acceleration", Value: 90000},
		"hors liste":                {Key: "machine_max_speed_x", Value: 9000},
	} {
		if _, err := e.svc.ModifyProfile(e.ctx, ModifyInput{Base: base, Modifications: []slicer.Override{ov}}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s : ErrInvalid attendue, reçu %v", name, err)
		}
	}
	if _, err := e.svc.ModifyProfile(e.ctx, ModifyInput{Base: base, Modifications: []slicer.Override{{Key: "nozzle_temperature", Value: 230}, {Key: "outer_wall_speed", Value: 180}}}); err != nil {
		t.Errorf("valeurs permises refusées : %v", err)
	}
}
