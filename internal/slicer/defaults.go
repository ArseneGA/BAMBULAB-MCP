package slicer

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
)

// BedTypes : valeurs de curr_bed_type telles qu'écrites par Bambu Studio (profils machine : default_bed_type, et
// plateaux listés dans la fiche du modèle), avec la clé de température plateau du filament qui leur correspond.
var BedTypes = []string{"Cool Plate", "Engineering Plate", "High Temp Plate", "Textured PEI Plate", "Supertack Plate"}

// bedTempKey : température plateau du filament pour chaque type de plateau.
var bedTempKey = map[string]string{
	"Cool Plate":         "cool_plate_temp",
	"Engineering Plate":  "eng_plate_temp",
	"High Temp Plate":    "hot_plate_temp",
	"Textured PEI Plate": "textured_plate_temp",
	"Supertack Plate":    "supertack_plate_temp",
}

// BedTempKey retourne la clé de température plateau du filament pour un type de plateau (vide si inconnu).
func BedTempKey(bedType string) string { return bedTempKey[strings.TrimSpace(bedType)] }

// checkBedTypeCoherence : le plateau demandé doit être utilisable avec chaque filament. Un filament qui déclare
// 0 °C pour ce plateau (clé présente, valeur nulle) n'est pas prévu pour lui par Bambu : refus explicite, avec les
// plateaux que le filament prévoit.
func checkBedTypeCoherence(bedType string, fils []*profiles.Resolved) error {
	key := bedTempKey[bedType]
	if key == "" {
		return nil
	}
	for _, f := range fils {
		vals := f.Strings(key)
		if len(vals) == 0 {
			continue // le profil ne dit rien de ce plateau : rien à contredire
		}
		t, err := strconv.ParseFloat(strings.TrimSpace(vals[0]), 64)
		if err != nil || t > 0 {
			continue
		}
		var ok []string
		for _, bt := range BedTypes {
			if v := f.Strings(bedTempKey[bt]); len(v) > 0 {
				if x, e := strconv.ParseFloat(strings.TrimSpace(v[0]), 64); e == nil && x > 0 {
					ok = append(ok, bt)
				}
			}
		}
		alt := "aucun plateau n'est renseigné dans ce preset"
		if len(ok) > 0 {
			alt = "plateaux prévus par ce filament : " + strings.Join(ok, ", ")
		}
		return fmt.Errorf("%w: curr_bed_type %q incohérent avec le filament %q : %s = 0 °C (plateau non prévu pour cette matière) ; %s", ErrOverride, bedType, f.Name, key, alt)
	}
	return nil
}

// bambuDefaults : valeurs par défaut de Bambu Studio pour des clés que les profils officiels laissent souvent
// absentes (la valeur effective n'est alors pas « inconnue » : c'est celle du logiciel).
var bambuDefaults = map[string]string{
	"brim_type":               "auto_brim",
	"brim_width":              "0",
	"brim_object_gap":         "0",
	"raft_layers":             "0",
	"enable_support":          "0",
	"support_type":            "normal(auto)",
	"support_threshold_angle": "30",
	"curr_bed_type":           "Textured PEI Plate",
}

// BambuDefault retourne la valeur par défaut de Bambu Studio d'une clé process (ok faux si elle n'est pas connue).
func BambuDefault(key string) (string, bool) {
	v, ok := bambuDefaults[strings.TrimSpace(key)]
	return v, ok
}

// Origines d'une valeur effective.
const (
	SourceProfile  = "profil"
	SourceOverride = "surcharge"
	SourceDefault  = "défaut Bambu Studio"
)

// Effective : valeur effective d'une clé et d'où elle vient. Ordre : surcharge, profil (hérité compris), défaut
// de Bambu Studio. Vide et source « inconnue » si aucune des trois n'a la clé.
func Effective(r *profiles.Resolved, key string) (value string, source string) {
	if r != nil {
		if v, ok := r.Config[key]; ok {
			if s := firstConfigString(v); s != "" || !isEmptyConfig(v) {
				src := SourceProfile
				if r.Source[key] == "override" {
					src = SourceOverride
				}
				return s, src
			}
		}
	}
	if d, ok := bambuDefaults[key]; ok {
		return d, SourceDefault
	}
	return "", "inconnue"
}

func isEmptyConfig(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case []any:
		return len(t) == 0
	}
	return false
}

func firstConfigString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		if len(t) > 0 {
			if s, ok := t[0].(string); ok {
				return s
			}
		}
	case []string:
		if len(t) > 0 {
			return t[0]
		}
	}
	return ""
}
