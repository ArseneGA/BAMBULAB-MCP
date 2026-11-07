package slicer

import (
	"archive/zip"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
)

// Tests de sûreté contre les vrais profils et le vrai Bambu Studio (ignorés si absent).

func realProfiles(t *testing.T) *profiles.Store {
	t.Helper()
	bin, err := Locate("")
	if err != nil {
		t.Skipf("Bambu Studio absent: %v", err)
	}
	d := bin.ProfilesDir()
	if d == "" {
		t.Skip("profils BBL introuvables")
	}
	st, err := profiles.Load(d)
	if err != nil {
		t.Skip(err)
	}
	return st
}

func TestRealProfilesBoundsAndRefusals(t *testing.T) {
	st := realProfiles(t)
	try := func(machine, process, filament string, ov Override) error {
		_, _, _, _, err := ResolveWithOverrides(st, machine, process, []string{filament}, []Override{ov})
		return err
	}
	// maxima lus dans resources/printers/*.json
	tr := NewTrust(st)
	if l := tr.Limits("Bambu Lab P1S"); !l.Known || l.NozzleMaxC != 300 || l.BedMaxC != 100 {
		t.Fatalf("P1S: %+v", l)
	}
	if l := tr.Limits("Bambu Lab A1 mini"); !l.Known || l.NozzleMaxC != 300 || l.BedMaxC != 80 {
		t.Fatalf("A1 mini: %+v", l)
	}
	refused := map[string]Override{
		"gcode de démarrage":          {Key: "machine_start_gcode", Value: "M104 S350"},
		"gcode de fin":                {Key: "machine_end_gcode", Value: "M104 S350"},
		"changement de couche":        {Key: "layer_change_gcode", Value: "M104 S350"},
		"changement de filament":      {Key: "change_filament_gcode", Value: "M104 S350"},
		"400 °C":                      {Key: "nozzle_temperature", Value: 400},
		"350 °C":                      {Key: "nozzle_temperature", Value: 350},
		"280 °C (plage PLA: 240)":     {Key: "nozzle_temperature", Value: 280},
		"1re couche 320 °C":           {Key: "nozzle_temperature_initial_layer", Value: 320},
		"plateau 110 °C (P1S: 100)":   {Key: "textured_plate_temp", Value: 110},
		"vitesse 600 (P1S max 500)":   {Key: "outer_wall_speed", Value: 600},
		"vitesse 9999":                {Key: "travel_speed", Value: 9999},
		"accélération 30000 (20000)":  {Key: "default_acceleration", Value: 30000},
		"déplacement 12000 (9000)":    {Key: "travel_acceleration", Value: 12000},
		"couche de 0,5 mm sur 0,4":    {Key: "layer_height", Value: 0.5},
		"clé hors liste":              {Key: "machine_max_speed_x", Value: 5000},
		"plage haute du filament":     {Key: "nozzle_temperature_range_high", Value: 400},
		"script de post-traitement":   {Key: "post_process", Value: "calc.exe"},
		"substitution de G-code":      {Key: "gcode_substitutions", Value: "x"},
		"énumération inconnue":        {Key: "seam_position", Value: "partout"},
		"densité de remplissage 150%": {Key: "sparse_infill_density", Value: "150%"},
	}
	for name, ov := range refused {
		if err := try(p1sMachine, p1sProcess, p1sFilament, ov); !errors.Is(err, ErrOverride) {
			t.Errorf("%s: refus attendu, reçu %v", name, err)
		}
	}
	accepted := map[string]Override{
		"PLA 235 °C (plage 190-240)": {Key: "nozzle_temperature", Value: 235},
		"vitesse 400 (max 500)":      {Key: "outer_wall_speed", Value: 400},
		"accélération 15000":         {Key: "default_acceleration", Value: 15000},
		"plateau 100 °C":             {Key: "textured_plate_temp", Value: 100},
		"couche de 0,3 mm":           {Key: "layer_height", Value: 0.3},
		"bordure absente du profil":  {Key: "brim_type", Value: "outer_only"},
		"couture":                    {Key: "seam_position", Value: "aligned_back"},
		"rétraction":                 {Key: "retraction_length", Value: 1.0, Target: TargetMachine},
	}
	for name, ov := range accepted {
		if err := try(p1sMachine, p1sProcess, p1sFilament, ov); err != nil {
			t.Errorf("%s: refus inattendu: %v", name, err)
		}
	}
	// A1 mini : plateau limité à 80 °C
	if err := try(a1mMachine, a1mProcess, a1mFilament, Override{Key: "textured_plate_temp", Value: 90}); !errors.Is(err, ErrOverride) {
		t.Errorf("A1 mini 90 °C: %v", err)
	}
	if err := try(a1mMachine, a1mProcess, a1mFilament, Override{Key: "textured_plate_temp", Value: 75}); err != nil {
		t.Errorf("A1 mini 75 °C: %v", err)
	}
}

// Les valeurs par défaut des profils officiels ne sont jamais refusées par leurs propres bornes : on réapplique
// à l'identique une clé de chaque famille, sur toutes les machines P1/A1 et leurs filaments PLA/PETG.
func TestRealProfilesDefaultsWithinBounds(t *testing.T) {
	st := realProfiles(t)
	for _, mach := range []string{p1sMachine, a1mMachine, "Bambu Lab A1 0.4 nozzle", "Bambu Lab P1P 0.4 nozzle"} {
		m, err := st.Resolve(profiles.KindMachine, mach)
		if err != nil {
			continue
		}
		def, _ := m.String("default_print_profile")
		fils, _ := st.CompatibleFilaments(mach)
		if def == "" || len(fils) == 0 {
			continue
		}
		n := 0
		for _, f := range fils {
			if !strings.Contains(f, "PLA Basic") && !strings.Contains(f, "PETG Basic") && !strings.Contains(f, "ABS") {
				continue
			}
			rf, err := st.Resolve(profiles.KindFilament, f)
			if err != nil {
				continue
			}
			for _, k := range []string{"nozzle_temperature", "nozzle_temperature_initial_layer", "hot_plate_temp", "textured_plate_temp", "cool_plate_temp", "eng_plate_temp"} {
				if v, ok := rf.String(k); ok {
					if _, _, _, _, err := ResolveWithOverrides(st, mach, def, []string{f}, []Override{{Key: k, Value: v, Target: TargetFilament}}); err != nil {
						t.Errorf("%s / %s: %s=%s refusé par ses propres bornes: %v", mach, f, k, v, err)
					}
				}
			}
			n++
		}
		rp, _ := st.Resolve(profiles.KindProcess, def)
		for _, k := range []string{"outer_wall_speed", "inner_wall_speed", "sparse_infill_speed", "travel_speed", "initial_layer_speed", "top_surface_speed", "bridge_speed",
			"default_acceleration", "outer_wall_acceleration", "initial_layer_acceleration", "travel_acceleration", "layer_height", "initial_layer_print_height"} {
			if v, ok := rp.String(k); ok {
				if _, _, _, _, err := ResolveWithOverrides(st, mach, def, []string{fils[0]}, []Override{{Key: k, Value: v, Target: TargetProcess}}); err != nil {
					t.Errorf("%s / %s: %s=%s refusé par ses propres bornes: %v", mach, def, k, v, err)
				}
			}
		}
		t.Logf("%s: %d filaments, process %s vérifiés", mach, n, def)
	}
}

func gcodeOf(t *testing.T, archive string) string {
	t.Helper()
	zr, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == "Metadata/plate_1.gcode" {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			return string(b)
		}
	}
	t.Fatal("plate_1.gcode absent")
	return ""
}

func TestRealArchivesSafe(t *testing.T) {
	s := realSlicer(t)
	model := writeSTL(t, "cube.stl", mesh.Cube(20))
	fils, _ := s.Profiles().CompatibleFilaments(p1sMachine)
	petg := ""
	for _, f := range fils {
		if strings.Contains(f, "PETG Basic") {
			petg = f
		}
	}
	if petg == "" {
		t.Skip("aucun PETG Basic compatible")
	}
	cases := []struct {
		name, machine, process, filament string
		bedMax                           float64
		nozzleHi                         float64
	}{
		{"P1S PLA", p1sMachine, p1sProcess, p1sFilament, 100, 240},
		{"P1S PETG", p1sMachine, p1sProcess, petg, 100, 270},
		{"A1 mini PLA", a1mMachine, a1mProcess, a1mFilament, 80, 240},
	}
	for _, c := range cases {
		res := sliceOK(t, s, Request{Model: model, Machine: c.machine, Process: c.process, Filaments: []string{c.filament}, Label: "safe"})
		if !res.Validation.OK {
			t.Fatalf("%s: validation: %+v", c.name, res.Validation.Failed())
		}
		p := res.Plates[0]
		sf := p.Safety
		if sf == nil {
			t.Fatalf("%s: analyse de sûreté absente", c.name)
		}
		rf, _ := s.Profiles().Resolve(profiles.KindFilament, c.filament)
		first, _ := firstFloat(rf, "nozzle_temperature_initial_layer")
		if sf.NozzleFirstLayerC != first || sf.NozzleFirstLayerC <= 0 {
			t.Errorf("%s: buse 1re couche %.0f, profil %.0f", c.name, sf.NozzleFirstLayerC, first)
		}
		if sf.NozzlePrintMaxC > c.nozzleHi || sf.NozzleMaxC > 300 || sf.BedMaxC > c.bedMax || sf.BedFirstLayerC <= 0 || sf.BedMinC <= 0 {
			t.Errorf("%s: températures lues hors bornes: %+v", c.name, sf)
		}
		if len(sf.Unexpected) != 0 || len(sf.Dangerous) != 0 || len(sf.BadTemps) != 0 {
			t.Errorf("%s: commandes hors ensemble attendu dans un fichier produit par Studio: %+v %+v", c.name, sf.Unexpected, sf.Dangerous)
		}
		gp := res.Archive.GcodeProfile
		if gp == nil || !gp.Official || len(gp.Checked) < 8 {
			t.Errorf("%s: G-code de démarrage non reconnu comme officiel: %+v", c.name, gp)
		}
		t.Logf("%s: buse démarrage ≤%.0f, 1re couche %.0f, impression %.0f-%.0f ; plateau %.0f ; %d paramètres *_gcode officiels (%s)",
			c.name, sf.NozzleStartMaxC, sf.NozzleFirstLayerC, sf.NozzlePrintMinC, sf.NozzlePrintMaxC, sf.BedFirstLayerC, len(gp.Checked), gp.Fingerprint)
	}
}

func TestRealWhitelistedKeysSliceInStudio(t *testing.T) {
	s := realSlicer(t)
	model := writeSTL(t, "cube.stl", mesh.Cube(20))
	res := sliceOK(t, s, Request{Model: model, Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}, Label: "liste-blanche",
		Overrides: []Override{
			{Key: "brim_type", Value: "outer_only"}, {Key: "brim_width", Value: 6}, {Key: "seam_position", Value: "aligned_back"},
			{Key: "wall_generator", Value: "arachne"}, {Key: "ironing_type", Value: "top"}, {Key: "sparse_infill_pattern", Value: "gyroid"},
			{Key: "outer_wall_speed", Value: 120}, {Key: "default_acceleration", Value: 6000}, {Key: "nozzle_temperature", Value: 225},
			{Key: "textured_plate_temp", Value: 60}, {Key: "textured_plate_temp_initial_layer", Value: 60}, {Key: "nozzle_temperature_initial_layer", Value: 225}, {Key: "retraction_length", Value: 1.0, Target: TargetMachine},
		}})
	if !res.Validation.OK {
		t.Fatalf("validation: %+v", res.Validation.Failed())
	}
	g := gcodeOf(t, res.ArtifactPath)
	for _, want := range []string{"; FEATURE: Brim", "; FEATURE: Ironing", "M104 S225", "M190 S60"} {
		if !strings.Contains(g, want) {
			t.Errorf("effet absent du G-code: %q", want)
		}
	}
	if sf := res.Plates[0].Safety; sf == nil || sf.NozzleFirstLayerC != 225 || sf.BedFirstLayerC != 60 {
		t.Errorf("températures lues: %+v", res.Plates[0].Safety)
	}
}

func TestRealTamperedArchivesAreRefused(t *testing.T) {
	s := realSlicer(t)
	model := writeSTL(t, "cube.stl", mesh.Cube(20))
	res := sliceOK(t, s, Request{Model: model, Machine: p1sMachine, Process: p1sProcess, Filaments: []string{p1sFilament}, Label: "tamper"})
	src, err := os.ReadFile(res.ArtifactPath)
	if err != nil {
		t.Fatal(err)
	}
	check := func(name string, b []byte, wantFail string) {
		t.Helper()
		_, val, err := ValidateArchive(writeTemp(t, "t.gcode.3mf", b), Expect{Trust: s.Trust()})
		if wantFail == "" {
			if err != nil {
				t.Errorf("%s: acceptée attendue: %v", name, err)
			}
			return
		}
		if !errors.Is(err, ErrInvalidOutput) {
			t.Errorf("%s: refus attendu, reçu %v", name, err)
			return
		}
		for _, c := range val.Failed() {
			if strings.HasSuffix(c.Name, wantFail) {
				return
			}
		}
		t.Errorf("%s: %q non en échec: %+v", name, wantFail, val.Failed())
	}
	check("intacte (reconstruite)", withGcode(t, src, func(g string) string { return g }), "")
	// M104 S350 inséré à la 3e couche, somme md5 recalculée : seul le contenu trahit le fichier
	hot := func(cmd string) func(string) string {
		return func(g string) string {
			i := strings.Index(g, "; CHANGE_LAYER")
			j := strings.Index(g[i+1:], "; CHANGE_LAYER") + i + 1
			return g[:j] + cmd + "\n" + g[j:]
		}
	}
	check("M104 S350", withGcode(t, src, hot("M104 S350")), "unsafe_gcode")
	check("M109 S400", withGcode(t, src, hot("M109 S400")), "unsafe_gcode")
	check("M140 S150", withGcode(t, src, hot("M140 S150")), "unsafe_gcode")
	check("M104 S290 (plage PLA 240, sous le maximum 300)", withGcode(t, src, hot("M104 S290")), "unsafe_gcode")
	check("M92 X10", withGcode(t, src, hot("M92 X10")), "unsafe_gcode")
	check("M302 S0", withGcode(t, src, hot("M302 S0")), "unsafe_gcode")
	// le démarrage modifié (une chauffe à 350 °C juste avant la fin du G-code de démarrage)
	check("démarrage modifié", withGcode(t, src, func(g string) string {
		return strings.Replace(g, "; MACHINE_START_GCODE_END", "M104 S350\n; MACHINE_START_GCODE_END", 1)
	}), "unsafe_gcode")
	// démarrage modifié sans température : seule l'empreinte du bloc de configuration ne suffit pas, les commandes tranchent
	check("démarrage avec M92", withGcode(t, src, func(g string) string {
		return strings.Replace(g, "; MACHINE_START_GCODE_END", "M92 E5000\n; MACHINE_START_GCODE_END", 1)
	}), "unsafe_gcode")
	// bloc de configuration du G-code modifié : l'empreinte ne correspond plus
	check("bloc de configuration modifié", withGcode(t, src, func(g string) string {
		return strings.Replace(g, "; machine_start_gcode = ", "; machine_start_gcode = M104 S250\\n", 1)
	}), "unsafe_gcode_profile")
	// project_settings.config modifié
	tampered := rewriteArchive(t, src, func(name string, b []byte) []byte {
		if name == "Metadata/project_settings.config" {
			return []byte(strings.Replace(string(b), `"machine_end_gcode": "`, `"machine_end_gcode": "M104 S350\n`, 1))
		}
		return b
	})
	check("project_settings modifié", tampered, "unsafe_gcode_profile")
}
