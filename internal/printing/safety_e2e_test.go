package printing_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// safeValidator : comme fakeValidator, avec ce que l'analyse du G-code rapporte (températures, origine du démarrage).
func safeValidator(gp *slicer.GcodeProfile, extra ...slicer.Check) printingValidator {
	return func(path string, exp slicer.Expect) (*slicer.ArchiveInfo, *slicer.Validation, error) {
		info, v, err := fakeValidator("0.4")(path, exp)
		info.Plates[0].Safety = &slicer.GcodeSafety{NozzleStartMaxC: 250, NozzleFirstLayerC: 220, NozzlePrintMinC: 220, NozzlePrintMaxC: 225,
			BedFirstLayerC: 55, BedMinC: 55, BedMaxC: 60}
		info.GcodeProfile = gp
		v.Checks = append(v.Checks, extra...)
		return info, v, err
	}
}

// Un G-code qui demande une température hors bornes est bloquant : pas de dépôt sur l'imprimante.
func TestUnsafeGcodeBlocksUpload(t *testing.T) {
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	r.sim = startSim(t)
	r.seedPrinterModel()
	r.c.Printing.SetValidator(func(path string, exp slicer.Expect) (*slicer.ArchiveInfo, *slicer.Validation, error) {
		info, v, _ := safeValidator(&slicer.GcodeProfile{Official: true, Verifiable: true})(path, exp)
		v.Checks = append(v.Checks, slicer.Check{Name: "plate_1.unsafe_gcode", OK: false, Level: "error", Detail: "consigne de buse 350 °C au-delà du maximum du modèle (300 °C)"})
		v.OK = false
		return info, v, fmt.Errorf("%w: plate_1.unsafe_gcode", slicer.ErrInvalidOutput)
	})
	ap := r.addPrinter(nil)
	r.printerID = str(ap, "printer", "id")
	r.humanSetsAccessCode(str(ap, "setup_url"))
	art := r.fakeArtifact()
	v := r.call("validate_print_artifact", map[string]any{"artifact_id": art, "printer_id": r.printerID})
	rep := v["report"].(map[string]any)
	if rep["ok"] != false || !strings.Contains(fmt.Sprint(rep["blocking"]), "unsafe_gcode") {
		t.Fatalf("rapport : %v", rep)
	}
	if txt := r.callErr("upload_print_artifact", map[string]any{"artifact_id": art, "printer_id": r.printerID, "idempotency_key": "cle-depot-unsafe-1", "assignments": anyAssign(ams1)}); !strings.Contains(txt, "unsafe_gcode") {
		t.Fatalf("dépôt autorisé malgré un G-code dangereux : %s", txt)
	}
}

// La page /confirm (canal humain) et la vue de confirmation MCP montrent les températures lues dans le G-code,
// la matière et l'origine du G-code de démarrage ; une anomalie y est signalée en rouge.
func TestConfirmPageAndViewShowGcodeSafety(t *testing.T) {
	for _, tc := range []struct {
		name     string
		gp       *slicer.GcodeProfile
		extra    []slicer.Check
		wantPage []string
		notPage  []string
	}{
		{"officiel", &slicer.GcodeProfile{Official: true, Verifiable: true, Source: "Bambu Lab P1S 0.4 nozzle"}, nil,
			[]string{"Températures", "1re couche 220 °C", "impression 220 à 225 °C", "plateau : 1re couche 55 °C", "emplacement 1 : PLA", "G-code de démarrage : profil officiel ✓"},
			[]string{`class="warn bad"`}},
		{"anomalie", &slicer.GcodeProfile{Verifiable: true, Mismatch: []string{"machine_start_gcode"}},
			[]slicer.Check{{Name: "plate_1.gcode_commands", OK: false, Level: "warn", Detail: "M5000 ×1 (1re : ligne 9)"}},
			[]string{"DIFFÉRENT du profil officiel", "M5000 ×1", `class="warn bad"`}, []string{"profil officiel ✓"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, "Z:/absent/bambu-studio.exe")
			r.sim = startSim(t)
			r.seedPrinterModel()
			r.c.Printing.SetValidator(safeValidator(tc.gp, tc.extra...))
			ap := r.addPrinter(nil)
			r.printerID = str(ap, "printer", "id")
			r.humanSetsAccessCode(str(ap, "setup_url"))
			art := r.fakeArtifact()
			jobID, _ := r.uploadAndAsk(art, "cle-depot-surete-"+tc.name)
			cf := r.call("request_print_confirmation", map[string]any{"print_job_id": jobID})

			st, body, _ := r.humanGet(str(cf, "confirm_url"))
			if st != 200 {
				t.Fatalf("page : %d", st)
			}
			for _, w := range tc.wantPage {
				if !strings.Contains(body, w) {
					t.Errorf("/confirm ne contient pas %q", w)
				}
			}
			for _, w := range tc.notPage {
				if strings.Contains(body, w) {
					t.Errorf("/confirm contient %q", w)
				}
			}
			// vue MCP : les mêmes chiffres, lus par le modèle
			sf, _ := cf["safety"].(map[string]any)
			if sf == nil || sf["analysed"] != true || sf["nozzle_first_layer_c"].(float64) != 220 || sf["bed_first_layer_c"].(float64) != 55 ||
				!strings.Contains(fmt.Sprint(sf["materials"]), "PLA") || !strings.Contains(fmt.Sprint(sf["gcode_profile_line"]), "G-code de démarrage") {
				t.Fatalf("vue MCP : %v", cf["safety"])
			}
			if tc.name == "anomalie" && (sf["gcode_official"] != false || len(fmt.Sprint(sf["anomalies"])) < 10) {
				t.Errorf("anomalies absentes de la vue MCP : %v", sf)
			}
		})
	}
}
