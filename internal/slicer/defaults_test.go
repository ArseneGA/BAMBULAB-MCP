package slicer

import (
	"errors"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
)

// curr_bed_type est surchargeable, énuméré, et cohérent avec les températures plateau du filament.
func TestCurrBedTypeAllowlistAndFilamentCoherence(t *testing.T) {
	if !IsOverridable("curr_bed_type") {
		t.Fatal("curr_bed_type doit être dans la liste blanche")
	}
	st := synthStore(t)
	m, p, fs := resolveSynth(t, st, "Syn PLA")
	for _, bt := range BedTypes {
		if _, err := applyOverrides(st, m, p, fs, []Override{{Key: "curr_bed_type", Value: bt}}); err != nil {
			t.Errorf("%s : refus inattendu : %v", bt, err)
		}
	}
	if v, _ := p.String("curr_bed_type"); v != "Supertack Plate" {
		t.Errorf("valeur écrite : %q", v)
	}
	if _, err := applyOverrides(st, m, p, fs, []Override{{Key: "curr_bed_type", Value: "Plaque inconnue"}}); !errors.Is(err, ErrOverride) {
		t.Errorf("valeur hors énumération : %v", err)
	}
	// filament qui déclare 0 °C pour la plaque demandée : incohérent
	fs[0].Config["cool_plate_temp"] = []any{"0"}
	fs[0].Config["hot_plate_temp"] = []any{"60"}
	_, err := applyOverrides(st, m, p, fs, []Override{{Key: "curr_bed_type", Value: "Cool Plate"}})
	if !errors.Is(err, ErrOverride) || !strings.Contains(err.Error(), "cool_plate_temp") || !strings.Contains(err.Error(), "High Temp Plate") {
		t.Errorf("incohérence plateau/filament attendue avec les plateaux possibles : %v", err)
	}
	// même filament, plateau prévu : accepté
	if _, err := applyOverrides(st, m, p, fs, []Override{{Key: "curr_bed_type", Value: "High Temp Plate"}}); err != nil {
		t.Errorf("plateau prévu : %v", err)
	}
}

// La valeur effective d'une clé absente du profil est le défaut de Bambu Studio (brim_type : auto_brim), pas « inconnue ».
func TestEffectiveResolvesBambuDefault(t *testing.T) {
	st := synthStore(t)
	_, p, _ := resolveSynth(t, st, "Syn PLA")
	if v, src := Effective(p, "brim_type"); v != "auto_brim" || src != SourceDefault {
		t.Errorf("brim_type : %q (%s)", v, src)
	}
	if v, src := Effective(p, "brim_width"); v != "0" || src != SourceProfile {
		t.Errorf("brim_width lu dans le profil hérité : %q (%s)", v, src)
	}
	if _, src := Effective(p, "cle_inconnue"); src != "inconnue" {
		t.Errorf("clé inconnue : %s", src)
	}
	var _ = profiles.KindProcess
}
