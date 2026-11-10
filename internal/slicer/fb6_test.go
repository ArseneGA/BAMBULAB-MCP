package slicer

import (
	"errors"
	"strings"
	"testing"
)

// FB6 (retours d'usage du 9/11) : les clés refusées à tort sont dans la liste blanche, avec des bornes de sécurité ;
// aucune règle existante n'est assouplie.
func TestFB6NewKeysAreAllowlistedWithBounds(t *testing.T) {
	st := synthStore(t)
	m, p, fs := resolveSynth(t, st, "Syn PLA")
	fs[0].Config["slow_down_layer_time"] = []any{"4"}

	for _, k := range []string{"slow_down_layer_time", "tree_support_branch_diameter", "tree_support_wall_count", "curr_bed_type"} {
		if !IsOverridable(k) {
			t.Errorf("%s doit être dans la liste blanche", k)
		}
		if err := RejectKey(k); err != nil {
			t.Errorf("%s : %v", k, err)
		}
	}

	good := []Override{
		{Key: "slow_down_layer_time", Value: 8},
		{Key: "tree_support_branch_diameter", Value: 6.5},
		{Key: "tree_support_wall_count", Value: 2},
		{Key: "curr_bed_type", Value: "Textured PEI Plate"},
	}
	ap, err := applyOverrides(st, m, p, fs, good)
	if err != nil {
		t.Fatalf("valeurs dans les bornes : %v", err)
	}
	if len(ap) != 4 {
		t.Fatalf("appliquées : %d", len(ap))
	}
	if v, _ := fs[0].String("slow_down_layer_time"); v != "8" {
		t.Errorf("slow_down_layer_time écrit : %q", v)
	}
	if v, _ := p.String("tree_support_branch_diameter"); v != "6.5" {
		t.Errorf("tree_support_branch_diameter écrit : %q", v)
	}
	if v, _ := p.String("tree_support_wall_count"); v != "2" {
		t.Errorf("tree_support_wall_count écrit : %q", v)
	}

	// refus hors bornes ou mal typées : jamais appliquées
	bad := map[string]Override{
		"slow_down_layer_time négatif":      {Key: "slow_down_layer_time", Value: -1},
		"slow_down_layer_time énorme":       {Key: "slow_down_layer_time", Value: 101},
		"slow_down_layer_time décimal":      {Key: "slow_down_layer_time", Value: 4.5},
		"slow_down_layer_time texte":        {Key: "slow_down_layer_time", Value: "vite"},
		"branche trop fine":                 {Key: "tree_support_branch_diameter", Value: 0.5},
		"branche trop grosse":               {Key: "tree_support_branch_diameter", Value: 21},
		"branche texte":                     {Key: "tree_support_branch_diameter", Value: "gros"},
		"parois négatives":                  {Key: "tree_support_wall_count", Value: -1},
		"trop de parois":                    {Key: "tree_support_wall_count", Value: 6},
		"parois décimales":                  {Key: "tree_support_wall_count", Value: 1.5},
		"plateau inconnu":                   {Key: "curr_bed_type", Value: "Plaque inventée"},
		"plateau en nombre":                 {Key: "curr_bed_type", Value: 3},
		"G-code toujours interdit (début)":  {Key: "machine_start_gcode", Value: "M104 S300"},
		"G-code toujours interdit (couche)": {Key: "layer_change_gcode", Value: "G1 X0"},
	}
	m, p, fs = resolveSynth(t, st, "Syn PLA")
	fs[0].Config["slow_down_layer_time"] = []any{"4"}
	for name, o := range bad {
		if _, err := applyOverrides(st, m, p, fs, []Override{o}); !errors.Is(err, ErrOverride) {
			t.Errorf("%s : refus attendu, reçu %v", name, err)
		}
	}
	if v, _ := fs[0].String("slow_down_layer_time"); v != "4" {
		t.Errorf("config altérée par un refus : %q", v)
	}
}

// Les clés par objet passent par la même liste blanche : les nouvelles clés de process y sont bornées aussi, et
// les clés de filament restent interdites pour un objet.
func TestFB6ObjectSettingsUseSameBounds(t *testing.T) {
	st := synthStore(t)
	m, _, _ := resolveSynth(t, st, "Syn PLA")
	if err := CheckObjectSettings(map[string]string{"tree_support_branch_diameter": "6", "tree_support_wall_count": "2"}, m); err != nil {
		t.Fatalf("réglages par objet valides : %v", err)
	}
	for name, bad := range map[string]map[string]string{
		"branche hors bornes": {"tree_support_branch_diameter": "50"},
		"parois hors bornes":  {"tree_support_wall_count": "9"},
		"filament par objet":  {"slow_down_layer_time": "8"},
	} {
		if err := CheckObjectSettings(bad, m); !errors.Is(err, ErrOverride) {
			t.Errorf("%s : refus attendu, reçu %v", name, err)
		}
	}
}

// Les clés *_gcode restent refusées par RejectKey avec le message dédié, y compris celles qui ressemblent aux nouvelles.
func TestFB6GcodeKeysStillRejected(t *testing.T) {
	for _, k := range []string{"machine_start_gcode", "filament_end_gcode", "tree_support_gcode", "slow_down_layer_time_gcode", "post_process"} {
		err := RejectKey(k)
		if !errors.Is(err, ErrOverride) || !strings.Contains(err.Error(), "G-code") {
			t.Errorf("%s : %v", k, err)
		}
	}
	if err := RejectKey("tree_support_branch_angle"); !errors.Is(err, ErrOverride) {
		t.Errorf("une clé voisine non listée reste refusée : %v", err)
	}
}
