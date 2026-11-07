package profiles

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Les gabarits de G-code « include » des machines BBL s'appliquent par-dessus le parent et sous les clés propres
// du profil : sans eux, le CLI de Studio recevait le G-code de démarrage générique du profil de base.
func TestMachineIncludeTemplatesApplyGcodeOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "BBL")
	w := func(sub, name string, v map[string]any) {
		dir := filepath.Join(root, sub)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(v)
		if err := os.WriteFile(filepath.Join(dir, name+".json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w("machine", "base", map[string]any{"type": "machine", "name": "base", "instantiation": "false", "machine_start_gcode": "G28 ; générique", "machine_end_gcode": "M104 S0 ; base", "z_hop": "0.4"})
	w("machine", "M tpl start", map[string]any{"name": "M tpl start", "instantiation": "false", "machine_start_gcode": "M1002 officiel", "z_hop": "9", "nozzle_diameter": []string{"9"}})
	w("machine", "M tpl end", map[string]any{"name": "M tpl end", "instantiation": "false", "machine_end_gcode": "M1003 officiel"})
	w("machine", "M leaf", map[string]any{"type": "machine", "name": "M leaf", "inherits": "base", "instantiation": "true",
		"include": []string{"M tpl start", "M tpl end", "absent"}, "machine_end_gcode": "propre au profil"})
	w("process", "p", map[string]any{"type": "process", "name": "p", "instantiation": "true"})
	w("filament", "f", map[string]any{"type": "filament", "name": "f", "instantiation": "true"})
	st, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	r, err := st.Resolve(KindMachine, "M leaf")
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := r.String("machine_start_gcode"); v != "M1002 officiel" || r.Source["machine_start_gcode"] != "M tpl start" {
		t.Errorf("gabarit non appliqué : %q (%s)", v, r.Source["machine_start_gcode"])
	}
	if v, _ := r.String("machine_end_gcode"); v != "propre au profil" {
		t.Errorf("la clé propre au profil doit l'emporter sur le gabarit : %q", v)
	}
	if v, _ := r.String("z_hop"); v != "0.4" {
		t.Errorf("un gabarit ne doit appliquer que du G-code : z_hop=%q", v)
	}
	if _, ok := r.Config["nozzle_diameter"]; ok {
		t.Error("nozzle_diameter du gabarit appliqué")
	}
}
