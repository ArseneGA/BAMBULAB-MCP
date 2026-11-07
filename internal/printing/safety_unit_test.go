package printing

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// Les réglages par pièce acceptés à la planification sont tous dans la liste blanche des surcharges, avec les
// mêmes types : un plan accepté ne peut pas être refusé au tranchage pour une clé hors liste.
func TestObjectSettingKeysAreWhitelisted(t *testing.T) {
	machine := &profiles.Resolved{Config: map[string]any{"nozzle_diameter": []any{"0.4"}}}
	for _, k := range ObjectSettingKeys {
		sp, ok := slicer.SpecFor(k)
		if !ok || sp.Target != slicer.TargetProcess {
			t.Errorf("%s : clé de pièce absente de la liste blanche des surcharges de process", k)
		}
	}
	got, err := normalizeSettings(map[string]any{"sparse_infill_density": 100, "wall_loops": 4.0, "layer_height": "0.12",
		"sparse_infill_pattern": "gyroid", "brim_type": "outer_only", "brim_width": 5, "enable_support": true, "top_shell_layers": 6, "bottom_shell_layers": 5}, ObjectSettingKeys)
	if err != nil {
		t.Fatal(err)
	}
	if err := slicer.CheckObjectSettings(got, machine); err != nil {
		t.Fatalf("réglages valides refusés par le tranchage : %v", err)
	}
	if len(got) != len(ObjectSettingKeys) {
		t.Errorf("l'essai ne couvre pas toutes les clés : %d sur %d", len(got), len(ObjectSettingKeys))
	}
	// aucune clé de G-code ne peut entrer par les réglages de pièce
	for _, k := range []string{"machine_start_gcode", "layer_change_gcode", "change_filament_gcode", "nozzle_temperature", "outer_wall_speed"} {
		if _, err := normalizeSettings(map[string]any{k: "M104 S350"}, ObjectSettingKeys); err == nil {
			t.Errorf("%s accepté comme réglage de pièce", k)
		}
	}
}

func okJob() Job {
	return Job{Plate: 1, Summary: &ArtifactSummary{
		Plates: []slicer.PlateResult{{Index: 1, Filaments: []slicer.FilamentUse{{Slot: 1, Type: "PETG"}},
			Safety: &slicer.GcodeSafety{NozzleStartMaxC: 250, NozzleFirstLayerC: 245, NozzlePrintMinC: 245, NozzlePrintMaxC: 255, BedFirstLayerC: 70, BedMinC: 70, BedMaxC: 70}}},
		GcodeProfile: &slicer.GcodeProfile{Official: true, Verifiable: true, Source: "Bambu Lab P1S 0.4 nozzle"}}}
}

func TestSafetyViewFromJob(t *testing.T) {
	v := safetyOf(okJob())
	if !v.Analysed || !v.GcodeOfficial || len(v.Anomalies) != 0 || v.NozzlePrintMaxC != 255 || v.BedFirstLayerC != 70 ||
		len(v.Materials) != 1 || !strings.Contains(v.Materials[0], "PETG") || v.GcodeProfileLine != "G-code de démarrage : profil officiel ✓" {
		t.Fatalf("vue : %+v", v)
	}
	// profil différent
	j := okJob()
	j.Summary.GcodeProfile = &slicer.GcodeProfile{Verifiable: true, Mismatch: []string{"machine_start_gcode"}}
	v = safetyOf(j)
	if v.GcodeOfficial || len(v.Anomalies) == 0 || !strings.Contains(v.GcodeProfileLine, "DIFFÉRENT") {
		t.Fatalf("écart non signalé : %+v", v)
	}
	// pas d'analyse du tout
	v = safetyOf(Job{})
	if v.Analysed || len(v.Anomalies) < 2 {
		t.Fatalf("absence d'analyse non signalée : %+v", v)
	}
	// contrôle d'archive en échec relevé
	j = okJob()
	j.Validation = &ValidationReport{Archive: []slicer.Check{{Name: "plate_1.gcode_commands", OK: false, Level: "warn", Detail: "M5000 ×1"}, {Name: "plate_1.layers", OK: false, Level: "warn", Detail: "x"}}}
	v = safetyOf(j)
	if len(v.Anomalies) != 1 || !strings.Contains(v.Anomalies[0], "M5000") {
		t.Fatalf("anomalie de commande : %+v", v.Anomalies)
	}
}

func renderConfirm(t *testing.T, j Job) string {
	t.Helper()
	view := &ConfirmationView{ConfirmationID: "cf_x", PrintJobID: "pj", ExpiresAt: time.Now().Add(time.Minute), Safety: safetyOf(j),
		Binding: PromptDetails{File: "pièce.gcode.3mf", PrinterName: "Atelier", PrinterModel: "Bambu Lab P1S", SHA256: strings.Repeat("a", 64), Plate: 1,
			Materials: []FilamentAssignment{{FilamentSlot: 1, Tray: "ext", MaterialID: "PETG"}}, EstimatedMin: 12, WeightG: 4.2},
		State: contracts.PrintAwaitingConfirmation}
	var b bytes.Buffer
	if err := pageTpl.ExecuteTemplate(&b, "confirm", map[string]any{"Title": "Confirmation", "V": view, "Nonce": "n"}); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestConfirmPageShowsTemperaturesAndProfile(t *testing.T) {
	page := renderConfirm(t, okJob())
	for _, want := range []string{"Températures", "démarrage jusqu'à 250 °C", "1re couche 245 °C", "impression 245 à 255 °C",
		"plateau : 1re couche 70 °C", "emplacement 1 : PETG", "G-code de démarrage : profil officiel ✓"} {
		if !strings.Contains(page, want) {
			t.Errorf("la page ne contient pas %q\n%s", want, page)
		}
	}
	if strings.Contains(page, `class="warn bad"`) {
		t.Error("anomalie affichée alors que tout est normal")
	}
	// anomalies : en rouge
	j := okJob()
	j.Summary.GcodeProfile = &slicer.GcodeProfile{Verifiable: true, Mismatch: []string{"machine_start_gcode"}}
	j.Validation = &ValidationReport{Archive: []slicer.Check{{Name: "plate_1.unsafe_gcode", OK: false, Level: "error", Detail: "M104 S350 ligne 12"}}}
	page = renderConfirm(t, j)
	if strings.Count(page, `class="warn bad"`) < 2 || !strings.Contains(page, "DIFFÉRENT du profil officiel") || !strings.Contains(page, "M104 S350 ligne 12") {
		t.Errorf("anomalies non affichées en rouge :\n%s", page)
	}
	if strings.Contains(page, "profil officiel ✓") {
		t.Error("coche « officiel » affichée malgré l'écart")
	}
	// aucune analyse : températures déclarées non lues
	page = renderConfirm(t, Job{Plate: 1})
	if !strings.Contains(page, "G-code non analysé") {
		t.Errorf("absence d'analyse non signalée :\n%s", page)
	}
}
