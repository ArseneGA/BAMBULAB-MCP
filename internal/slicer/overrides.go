package slicer

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
)

// Cibles possibles d'une surcharge.
const (
	TargetMachine  = "machine"
	TargetProcess  = "process"
	TargetFilament = "filament"
)

// Override est une surcharge typée d'un paramètre d'un profil. La clé doit
// exister dans la configuration résolue (machine, process ou filament) ; la
// valeur est validée contre le type de la valeur existante. Target peut rester
// vide si la clé n'existe que dans une seule configuration. Une surcharge de
// filament s'applique à tous les emplacements filament.
//
// Value accepte string, bool (devient 1/0), entiers, flottants, ou une liste
// de ceux-ci. Un scalaire est diffusé sur toutes les entrées d'un paramètre en
// liste (valeurs par extrudeur).
type Override struct {
	Key    string `json:"key"`
	Value  any    `json:"value"`
	Target string `json:"target,omitempty"`
}

// AppliedOverride documente une surcharge appliquée (valeur avant/après).
type AppliedOverride struct {
	Target string `json:"target"`
	Key    string `json:"key"`
	Old    any    `json:"old"`
	New    any    `json:"new"`
}

// protectedKeys désigne l'identité des profils et la cohérence machine : on
// ne les surcharge pas (changer de machine ou de buse passe par la requête).
var protectedKeys = map[string]bool{
	"name": true, "inherits": true, "from": true, "type": true, "instantiation": true,
	"setting_id": true, "version": true, "printer_model": true, "printer_variant": true,
	"printer_settings_id": true, "nozzle_diameter": true, "printable_area": true,
	"printable_height": true, "bed_exclude_area": true, "compatible_printers": true,
	"compatible_printers_condition": true, "compatible_prints": true,
	"compatible_prints_condition": true, "filament_id": true, "print_settings_id": true,
	"filament_settings_id": true, "inherits_group": true, "gcode_flavor": true,
}

var (
	numRe = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	pctRe = regexp.MustCompile(`^-?\d+(\.\d+)?%$`)
)

type valClass int

const (
	classEmpty valClass = iota
	classNum
	classPct
	classStr
)

func classify(s string) valClass {
	switch {
	case s == "":
		return classEmpty
	case numRe.MatchString(s):
		return classNum
	case pctRe.MatchString(s):
		return classPct
	}
	return classStr
}

// applyOverrides valide et applique les surcharges sur des copies des
// configurations. machine, process et fils sont modifiés en place (ce sont des
// clones privés).
func applyOverrides(st *profiles.Store, machine, process *profiles.Resolved, fils []*profiles.Resolved, ovs []Override) ([]AppliedOverride, error) {
	trust := NewTrust(st)
	model, _ := machine.String("printer_model")
	lim := trust.Limits(model)
	var applied []AppliedOverride
	for i, o := range ovs {
		key := strings.TrimSpace(o.Key)
		if key == "" {
			return nil, fmt.Errorf("%w: surcharge %d sans clé", ErrOverride, i)
		}
		if protectedKeys[key] {
			return nil, fmt.Errorf("%w: la clé %q est protégée (identité ou cohérence machine)", ErrOverride, key)
		}
		// Liste blanche : toute clé *_gcode et toute clé hors liste sont refusées, avant même de regarder les profils.
		if err := RejectKey(key); err != nil {
			return nil, err
		}
		spec := specs[key]
		type holder struct {
			target string
			cfgs   []*profiles.Resolved
		}
		var cands []holder
		has := func(rs ...*profiles.Resolved) bool {
			for _, r := range rs {
				if _, ok := r.Config[key]; !ok {
					return false
				}
			}
			return true
		}
		if has(machine) {
			cands = append(cands, holder{TargetMachine, []*profiles.Resolved{machine}})
		}
		if has(process) {
			cands = append(cands, holder{TargetProcess, []*profiles.Resolved{process}})
		}
		if len(fils) > 0 && has(fils...) {
			cands = append(cands, holder{TargetFilament, fils})
		}
		if o.Target != "" {
			switch o.Target {
			case TargetMachine, TargetProcess, TargetFilament:
			default:
				return nil, fmt.Errorf("%w: cible %q inconnue (machine, process, filament)", ErrOverride, o.Target)
			}
			var f []holder
			for _, c := range cands {
				if c.target == o.Target {
					f = append(f, c)
				}
			}
			cands = f
		}
		// la clé ne s'applique qu'à la configuration que sa définition désigne
		{
			var f []holder
			for _, c := range cands {
				if c.target == spec.Target {
					f = append(f, c)
				}
			}
			cands = f
		}
		if len(cands) == 0 && spec.Target == TargetProcess && (o.Target == "" || o.Target == TargetProcess) {
			// clé valide mais absente du profil résolu (ex. brim_type) : la définition donne son type, on l'ajoute
			cands = append(cands, holder{TargetProcess, []*profiles.Resolved{process}})
		}
		switch len(cands) {
		case 0:
			return nil, fmt.Errorf("%w: clé %q inconnue dans les profils résolus", ErrOverride, key)
		case 1:
		default:
			var t []string
			for _, c := range cands {
				t = append(t, c.target)
			}
			return nil, fmt.Errorf("%w: clé %q ambiguë (%s), préciser la cible", ErrOverride, key, strings.Join(t, ", "))
		}
		h := cands[0]
		for _, cfg := range h.cfgs {
			old, present := cfg.Config[key]
			if !present {
				old = ""
			}
			nv, err := coerce(key, old, o.Value)
			if err != nil {
				return nil, err
			}
			if nv, err = enforceValue(spec, nv, boundCtx{machine: machine, fil: filOf(h.target, cfg), lim: lim, cur: maxNum(old)}); err != nil {
				return nil, err
			}
			if !present {
				old = nil
			}
			cfg.Config[key] = nv
			cfg.Source[key] = "override"
			applied = append(applied, AppliedOverride{Target: h.target, Key: key, Old: old, New: nv})
		}
	}
	return applied, nil
}

// coerce convertit la valeur demandée vers la forme (scalaire ou liste de
// chaînes) de la valeur existante, en validant le type.
func coerce(key string, old any, v any) (any, error) {
	news, fromString, err := toStrings(v)
	if err != nil {
		return nil, fmt.Errorf("%w: %q: %v", ErrOverride, key, err)
	}
	if len(news) == 0 {
		return nil, fmt.Errorf("%w: %q: valeur vide", ErrOverride, key)
	}
	var olds []string
	isList := false
	switch t := old.(type) {
	case string:
		olds = []string{t}
	case []any:
		isList = true
		for _, e := range t {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("%w: %q n'est pas un paramètre scalaire ou liste de chaînes", ErrOverride, key)
			}
			olds = append(olds, s)
		}
	default:
		return nil, fmt.Errorf("%w: %q n'est pas surchargeable (type %T)", ErrOverride, key, old)
	}
	// type attendu d'après l'existant
	exp := classEmpty
	for _, o := range olds {
		c := classify(o)
		if c > exp {
			exp = c
		}
	}
	for _, n := range news {
		c := classify(n)
		switch exp {
		case classNum, classPct:
			if c != classNum && c != classPct {
				return nil, fmt.Errorf("%w: %q attend un nombre ou un pourcentage (valeur actuelle %q), reçu %q", ErrOverride, key, olds[0], n)
			}
		case classStr:
			// énumération ou texte : un nombre brut fourni en type non-chaîne est suspect
			if !fromString && (c == classNum || c == classPct) {
				return nil, fmt.Errorf("%w: %q attend un texte (valeur actuelle %q), reçu le nombre %q", ErrOverride, key, olds[0], n)
			}
		}
		if strings.ContainsAny(n, "\r") {
			return nil, fmt.Errorf("%w: %q: caractère interdit", ErrOverride, key)
		}
	}
	if !isList {
		if len(news) != 1 {
			return nil, fmt.Errorf("%w: %q est un scalaire, %d valeurs fournies", ErrOverride, key, len(news))
		}
		return news[0], nil
	}
	out := make([]any, len(olds))
	switch {
	case len(news) == 1:
		for i := range out {
			out[i] = news[0]
		}
	case len(news) == len(olds):
		for i := range out {
			out[i] = news[i]
		}
	default:
		return nil, fmt.Errorf("%w: %q attend 1 ou %d valeurs, %d fournies", ErrOverride, key, len(olds), len(news))
	}
	return out, nil
}

// toStrings convertit une valeur Go en liste de chaînes de configuration.
// fromString est vrai si toutes les valeurs étaient déjà des chaînes.
func toStrings(v any) (out []string, fromString bool, err error) {
	fromString = true
	one := func(x any) error {
		switch t := x.(type) {
		case string:
			out = append(out, t)
		case bool:
			fromString = false
			if t {
				out = append(out, "1")
			} else {
				out = append(out, "0")
			}
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			fromString = false
			out = append(out, fmt.Sprint(t))
		case float32:
			fromString = false
			out = append(out, strconv.FormatFloat(float64(t), 'f', -1, 32))
		case float64:
			fromString = false
			out = append(out, strconv.FormatFloat(t, 'f', -1, 64))
		default:
			return fmt.Errorf("type de valeur non supporté %T", x)
		}
		return nil
	}
	rv := reflect.ValueOf(v)
	if v == nil {
		return nil, false, fmt.Errorf("valeur nil")
	}
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		for i := 0; i < rv.Len(); i++ {
			if err := one(rv.Index(i).Interface()); err != nil {
				return nil, false, err
			}
		}
		return out, fromString, nil
	}
	if err := one(v); err != nil {
		return nil, false, err
	}
	return out, fromString, nil
}
