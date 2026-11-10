package slicer

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
)

// Liste BLANCHE des clés surchargeables. Une surcharge ne touche que les clés listées ici, avec leur type et
// leurs bornes ; tout le reste (en particulier tout paramètre *_gcode, qui est du code exécuté par
// l'imprimante) est refusé explicitement. Les bornes viennent, dans cet ordre, de :
//   - températures : profil filament (nozzle_temperature_range_low/high) et maxima du modèle d'imprimante
//     lus dans l'installation de Bambu Studio (resources/printers/*.json) ;
//   - vitesses et accélérations : maxima du profil machine (machine_max_speed_x/y,
//     machine_max_acceleration_extruding/travel) ;
//   - hauteur de couche : diamètre de la buse du profil machine ;
//   - le reste : bornes statiques de bon sens (documentées dans docs/UTILISATION.md).

type keyKind int

const (
	kNum    keyKind = iota // nombre décimal
	kInt                   // entier
	kPct                   // pourcentage (« 15% » ; « 15 » devient « 15% »)
	kNumPct                // nombre ou pourcentage, tel qu'écrit dans le profil
	kBool                  // 0 / 1
	kEnum                  // valeur d'une énumération
)

type boundKind int

const (
	bndNone        boundKind = iota
	bndSpeed                 // <= vitesse max de la machine
	bndAccel                 // <= accélération max en extrusion de la machine
	bndAccelTravel           // <= accélération max en déplacement de la machine
	bndNozzleTemp            // [range_low, min(range_high, maximum buse du modèle)] du filament
	bndBedTemp               // [0, maximum plateau du modèle]
	bndLayer                 // <= diamètre de buse
)

// KeySpec décrit une clé surchargeable.
type KeySpec struct {
	Key    string
	Target string // process, filament ou machine
	Kind   keyKind
	Min    float64
	Max    float64
	Enum   []string
	Bound  boundKind
	Doc    string
}

// Caps absolus, jamais dépassés même si le profil machine annonce plus (ou ne dit rien).
const (
	absSpeedCap = 1000.0
	absAccCap   = 50000.0
)

var specs = map[string]*KeySpec{}

func add(s KeySpec) { sp := s; specs[s.Key] = &sp }

func numP(key string, min, max float64, doc string) {
	add(KeySpec{Key: key, Target: TargetProcess, Kind: kNum, Min: min, Max: max, Doc: doc})
}
func intP(key string, min, max float64, doc string) {
	add(KeySpec{Key: key, Target: TargetProcess, Kind: kInt, Min: min, Max: max, Doc: doc})
}
func boolK(key, target, doc string) {
	add(KeySpec{Key: key, Target: target, Kind: kBool, Min: 0, Max: 1, Doc: doc})
}
func enumP(key string, doc string, vals ...string) {
	add(KeySpec{Key: key, Target: TargetProcess, Kind: kEnum, Enum: vals, Doc: doc})
}
func speedP(key string, min float64, doc string) {
	add(KeySpec{Key: key, Target: TargetProcess, Kind: kNum, Min: min, Max: absSpeedCap, Bound: bndSpeed, Doc: doc})
}
func accP(key string, bound boundKind, doc string) {
	add(KeySpec{Key: key, Target: TargetProcess, Kind: kNum, Min: 100, Max: absAccCap, Bound: bound, Doc: doc})
}

var infillPatternVals = []string{"concentric", "zig-zag", "grid", "line", "cubic", "triangles", "tri-hexagon", "gyroid",
	"honeycomb", "adaptivecubic", "alignedrectilinear", "3dhoneycomb", "hilbertcurve", "archimedeanchords",
	"octagramspiral", "supportcubic", "lightning", "crosshatch"}

var surfacePatternVals = []string{"concentric", "zig-zag", "monotonic", "monotonicline", "alignedrectilinear",
	"hilbertcurve", "archimedeanchords", "octagramspiral"}

func init() {
	// ---- process : forme, parois, remplissage ----
	add(KeySpec{Key: "layer_height", Target: TargetProcess, Kind: kNum, Min: 0.04, Max: 0.8, Bound: bndLayer, Doc: "hauteur de couche (mm), au plus le diamètre de buse"})
	add(KeySpec{Key: "initial_layer_print_height", Target: TargetProcess, Kind: kNum, Min: 0.04, Max: 0.8, Bound: bndLayer, Doc: "hauteur de la première couche (mm)"})
	intP("wall_loops", 1, 50, "nombre de parois")
	intP("top_shell_layers", 0, 50, "couches pleines du dessus")
	intP("bottom_shell_layers", 0, 50, "couches pleines du dessous")
	numP("top_shell_thickness", 0, 20, "épaisseur minimale du dessus (mm)")
	numP("bottom_shell_thickness", 0, 20, "épaisseur minimale du dessous (mm)")
	add(KeySpec{Key: "sparse_infill_density", Target: TargetProcess, Kind: kPct, Min: 0, Max: 100, Doc: "densité de remplissage (%)"})
	enumP("sparse_infill_pattern", "motif de remplissage", infillPatternVals...)
	enumP("top_surface_pattern", "motif de la surface du dessus", surfacePatternVals...)
	enumP("bottom_surface_pattern", "motif de la surface du dessous", surfacePatternVals...)
	enumP("seam_position", "position de la couture", "nearest", "aligned", "aligned_back", "back", "random")
	enumP("wall_generator", "générateur de parois", "classic", "arachne")
	enumP("ironing_type", "repassage (ironing)", "no ironing", "top", "topmost", "solid")
	add(KeySpec{Key: "ironing_flow", Target: TargetProcess, Kind: kPct, Min: 0, Max: 100, Doc: "débit du repassage (%)"})
	numP("ironing_spacing", 0.05, 5, "espacement du repassage (mm)")
	speedP("ironing_speed", 1, "vitesse du repassage (mm/s)")
	enumP("draft_shield", "bouclier thermique", "disabled", "enabled")
	enumP("curr_bed_type", "plateau installé (type de plaque) : sa température vient du filament (cool_plate_temp, eng_plate_temp, hot_plate_temp, textured_plate_temp, supertack_plate_temp)", BedTypes...)
	enumP("brim_type", "bordure (brim)", "auto_brim", "brim_ears", "no_brim", "outer_only", "inner_only", "outer_and_inner")
	numP("brim_width", 0, 30, "largeur de bordure (mm)")
	numP("brim_object_gap", 0, 5, "écart bordure/pièce (mm)")
	intP("skirt_loops", 0, 20, "tours de jupe")
	numP("skirt_distance", 0, 30, "distance de la jupe (mm)")
	intP("skirt_height", 1, 50, "couches de jupe")
	intP("raft_layers", 0, 20, "couches de radeau")
	numP("elefant_foot_compensation", 0, 2, "compensation du pied d'éléphant (mm)")
	numP("xy_contour_compensation", -1, 1, "compensation XY des contours (mm)")
	numP("xy_hole_compensation", -1, 1, "compensation XY des trous (mm)")
	enumP("fuzzy_skin", "peau irrégulière", "none", "external", "all", "allwalls")
	numP("fuzzy_skin_thickness", 0, 5, "amplitude de la peau irrégulière (mm)")
	numP("fuzzy_skin_point_distance", 0.1, 5, "pas de la peau irrégulière (mm)")
	enumP("print_sequence", "ordre d'impression", "by layer", "by object")
	numP("bridge_flow", 0.5, 2, "ratio de débit des ponts")
	numP("top_solid_infill_flow_ratio", 0.5, 1.5, "ratio de débit du dessus plein")
	numP("infill_direction", 0, 360, "angle du remplissage (°)")
	boolK("detect_thin_wall", TargetProcess, "détecter les parois fines")
	boolK("enable_arc_fitting", TargetProcess, "ajustement d'arcs")
	boolK("spiral_mode", TargetProcess, "mode vase")
	boolK("adaptive_layer_height", TargetProcess, "hauteur de couche adaptative")
	boolK("only_one_wall_top", TargetProcess, "une seule paroi sur le dessus")
	boolK("reduce_crossing_wall", TargetProcess, "réduire les croisements de parois")
	boolK("enable_prime_tower", TargetProcess, "tour de purge")
	boolK("enable_overhang_speed", TargetProcess, "ralentir les surplombs")

	// ---- process : supports ----
	boolK("enable_support", TargetProcess, "supports")
	enumP("support_type", "type de supports", "normal(auto)", "tree(auto)", "normal(manual)", "tree(manual)")
	enumP("support_style", "style de supports", "default", "grid", "snug", "tree_slim", "tree_strong", "tree_hybrid", "tree_organic")
	enumP("support_base_pattern", "motif de base des supports", "default", "rectilinear", "rectilinear-grid", "honeycomb", "lightning", "hollow")
	intP("support_threshold_angle", 0, 90, "angle de surplomb à soutenir (°)")
	numP("tree_support_branch_diameter", 1, 20, "diamètre des branches des supports en arbre (mm) : des troncs plus gros résistent mieux à l'élancement")
	intP("tree_support_wall_count", 0, 5, "nombre de parois des branches des supports en arbre (0 = automatique)")
	boolK("support_on_build_plate_only", TargetProcess, "supports sur le plateau seulement")
	numP("support_base_pattern_spacing", 0, 50, "espacement du motif de supports (mm)")
	numP("support_top_z_distance", 0, 2, "distance Z dessus des supports (mm)")
	numP("support_bottom_z_distance", 0, 2, "distance Z dessous des supports (mm)")
	numP("support_object_xy_distance", 0, 5, "distance XY supports/pièce (mm)")
	intP("support_interface_top_layers", 0, 10, "couches d'interface du dessus")
	intP("support_interface_bottom_layers", 0, 10, "couches d'interface du dessous")
	numP("support_interface_spacing", 0, 5, "espacement d'interface (mm)")

	// ---- process : vitesses (mm/s), bornées par la machine ----
	for _, k := range []string{"outer_wall_speed", "inner_wall_speed", "sparse_infill_speed", "internal_solid_infill_speed",
		"top_surface_speed", "initial_layer_speed", "initial_layer_infill_speed", "travel_speed", "bridge_speed",
		"gap_infill_speed", "support_speed", "support_interface_speed"} {
		speedP(k, 1, "vitesse (mm/s), au plus le maximum de la machine")
	}
	add(KeySpec{Key: "small_perimeter_speed", Target: TargetProcess, Kind: kNumPct, Min: 1, Max: absSpeedCap, Bound: bndSpeed, Doc: "vitesse des petits périmètres (mm/s ou %)"})
	for _, k := range []string{"overhang_1_4_speed", "overhang_2_4_speed", "overhang_3_4_speed", "overhang_4_4_speed", "overhang_totally_speed"} {
		speedP(k, 0, "vitesse des surplombs (mm/s), 0 = sans ralentissement")
	}
	// ---- process : accélérations (mm/s²), bornées par la machine ----
	for _, k := range []string{"default_acceleration", "initial_layer_acceleration", "outer_wall_acceleration", "inner_wall_acceleration",
		"top_surface_acceleration", "sparse_infill_acceleration", "bridge_acceleration"} {
		accP(k, bndAccel, "accélération (mm/s²), au plus le maximum en extrusion de la machine")
	}
	for _, k := range []string{"travel_acceleration", "initial_layer_travel_acceleration"} {
		accP(k, bndAccelTravel, "accélération de déplacement (mm/s²), au plus le maximum en déplacement de la machine")
	}

	// ---- filament : températures, débit, refroidissement ----
	for _, k := range []string{"nozzle_temperature", "nozzle_temperature_initial_layer"} {
		add(KeySpec{Key: k, Target: TargetFilament, Kind: kInt, Min: 0, Max: 400, Bound: bndNozzleTemp,
			Doc: "température de buse (°C), dans la plage du filament et sous le maximum du modèle d'imprimante"})
	}
	for _, p := range []string{"hot_plate_temp", "cool_plate_temp", "eng_plate_temp", "textured_plate_temp", "supertack_plate_temp"} {
		for _, k := range []string{p, p + "_initial_layer"} {
			add(KeySpec{Key: k, Target: TargetFilament, Kind: kInt, Min: 0, Max: 200, Bound: bndBedTemp,
				Doc: "température de plateau (°C), sous le maximum du modèle d'imprimante"})
		}
	}
	add(KeySpec{Key: "filament_flow_ratio", Target: TargetFilament, Kind: kNum, Min: 0.5, Max: 1.5, Doc: "ratio de débit du filament"})
	add(KeySpec{Key: "filament_max_volumetric_speed", Target: TargetFilament, Kind: kNum, Min: 0.5, Max: 60, Doc: "débit volumique max (mm³/s)"})
	for _, k := range []string{"fan_min_speed", "fan_max_speed", "overhang_fan_speed", "additional_cooling_fan_speed",
		"during_print_exhaust_fan_speed", "complete_print_exhaust_fan_speed"} {
		add(KeySpec{Key: k, Target: TargetFilament, Kind: kInt, Min: 0, Max: 100, Doc: "vitesse de ventilateur (%)"})
	}
	add(KeySpec{Key: "close_fan_the_first_x_layers", Target: TargetFilament, Kind: kInt, Min: 0, Max: 50, Doc: "couches sans ventilateur"})
	add(KeySpec{Key: "full_fan_speed_layer", Target: TargetFilament, Kind: kInt, Min: 0, Max: 200, Doc: "couche de ventilation maximale"})
	add(KeySpec{Key: "slow_down_layer_time", Target: TargetFilament, Kind: kInt, Min: 0, Max: 100, Doc: "durée minimale d'une couche avant ralentissement (s) : ralentit les petites couches pour qu'elles refroidissent"})
	add(KeySpec{Key: "fan_cooling_layer_time", Target: TargetFilament, Kind: kNum, Min: 0, Max: 1000, Doc: "temps de couche déclenchant le ventilateur (s)"})
	add(KeySpec{Key: "slow_down_min_speed", Target: TargetFilament, Kind: kNum, Min: 0, Max: absSpeedCap, Bound: bndSpeed, Doc: "vitesse minimale de ralentissement (mm/s)"})
	boolK("slow_down_for_layer_cooling", TargetFilament, "ralentir pour refroidir la couche")
	boolK("reduce_fan_stop_start_freq", TargetFilament, "réduire les arrêts/démarrages du ventilateur")

	// ---- machine : rétraction ----
	for _, k := range []struct {
		key      string
		min, max float64
		doc      string
	}{
		{"retraction_length", 0, 15, "longueur de rétraction (mm)"},
		{"retraction_speed", 0, 100, "vitesse de rétraction (mm/s)"},
		{"deretraction_speed", 0, 100, "vitesse de ré-extrusion (mm/s)"},
		{"retraction_minimum_travel", 0, 50, "déplacement minimal avant rétraction (mm)"},
		{"retract_restart_extra", 0, 5, "supplément de ré-extrusion (mm)"},
		{"z_hop", 0, 5, "levée en Z (mm)"},
		{"wipe_distance", 0, 20, "distance d'essuyage (mm)"},
	} {
		add(KeySpec{Key: k.key, Target: TargetMachine, Kind: kNum, Min: k.min, Max: k.max, Doc: k.doc})
	}
	boolK("retract_when_changing_layer", TargetMachine, "rétracter au changement de couche")
	boolK("wipe", TargetMachine, "essuyage de la buse")
}

// OverridableKey décrit une clé surchargeable, pour la documentation et les outils.
type OverridableKey struct {
	Key    string   `json:"key"`
	Target string   `json:"target"`
	Type   string   `json:"type"`
	Min    *float64 `json:"min,omitempty"`
	Max    *float64 `json:"max,omitempty"`
	Enum   []string `json:"enum,omitempty"`
	Bound  string   `json:"bound,omitempty"`
	Doc    string   `json:"doc"`
}

// OverridableKeys retourne la liste blanche triée.
func OverridableKeys() []OverridableKey {
	out := make([]OverridableKey, 0, len(specs))
	for _, s := range specs {
		o := OverridableKey{Key: s.Key, Target: s.Target, Enum: s.Enum, Doc: s.Doc}
		switch s.Kind {
		case kNum:
			o.Type = "nombre"
		case kInt:
			o.Type = "entier"
		case kPct:
			o.Type = "pourcentage"
		case kNumPct:
			o.Type = "nombre ou pourcentage"
		case kBool:
			o.Type = "booléen"
		case kEnum:
			o.Type = "énumération"
		}
		if s.Kind != kEnum {
			mn, mx := s.Min, s.Max
			o.Min, o.Max = &mn, &mx
		}
		o.Bound = map[boundKind]string{bndSpeed: "vitesse max machine", bndAccel: "accélération max machine (extrusion)",
			bndAccelTravel: "accélération max machine (déplacement)", bndNozzleTemp: "plage du filament et maximum buse du modèle",
			bndBedTemp: "maximum plateau du modèle", bndLayer: "diamètre de buse"}[s.Bound]
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// IsOverridable indique si une clé est dans la liste blanche.
func IsOverridable(key string) bool { _, ok := specs[strings.TrimSpace(key)]; return ok }

// SpecFor retourne la définition d'une clé de la liste blanche.
func SpecFor(key string) (KeySpec, bool) {
	s, ok := specs[strings.TrimSpace(key)]
	if !ok {
		return KeySpec{}, false
	}
	return *s, true
}

// RejectKey retourne l'erreur de refus d'une clé hors liste blanche (nil si elle est permise).
func RejectKey(key string) error {
	key = strings.TrimSpace(key)
	if strings.HasSuffix(key, "_gcode") || key == "post_process" || key == "gcode_substitutions" {
		return fmt.Errorf("%w: la clé %q est refusée : les paramètres de G-code (*_gcode, post_process, gcode_substitutions) sont du code exécuté par l'imprimante ou par un script et ne se surchargent jamais", ErrOverride, key)
	}
	if _, ok := specs[key]; !ok {
		return fmt.Errorf("%w: la clé %q n'est pas dans la liste blanche des clés surchargeables (voir docs/UTILISATION.md)", ErrOverride, key)
	}
	return nil
}

// CheckObjectSettings valide des réglages PAR OBJET (écrits dans Metadata/model_settings.config) avec la même
// liste blanche et les mêmes bornes que les surcharges de profil : seules les clés de process sont permises
// pour un objet. machine fournit le diamètre de buse (hauteur de couche) et les maxima de vitesse.
func CheckObjectSettings(settings map[string]string, machine *profiles.Resolved) error {
	for k, v := range settings {
		if err := RejectKey(k); err != nil {
			return fmt.Errorf("%w (réglage par objet)", err)
		}
		sp := specs[k]
		if sp.Target != TargetProcess {
			return fmt.Errorf("%w: %q n'est pas un réglage de pièce (cible %s)", ErrOverride, k, sp.Target)
		}
		if _, err := sp.enforce([]string{v}, boundCtx{machine: machine}); err != nil {
			return fmt.Errorf("%w (réglage par objet)", err)
		}
	}
	return nil
}

// maxNum : plus grande valeur numérique d'une valeur de configuration (scalaire ou liste), 0 si aucune.
func maxNum(v any) float64 {
	best := 0.0
	one := func(x any) {
		if s, ok := x.(string); ok {
			if f, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%")), 64); err == nil {
				best = math.Max(best, f)
			}
		}
	}
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			one(e)
		}
	default:
		one(v)
	}
	return best
}

func filOf(target string, cfg *profiles.Resolved) *profiles.Resolved {
	if target == TargetFilament {
		return cfg
	}
	return nil
}

// enforceValue applique les bornes de la liste blanche à une valeur déjà typée par coerce (scalaire ou liste).
func enforceValue(spec *KeySpec, nv any, c boundCtx) (any, error) {
	switch t := nv.(type) {
	case string:
		out, err := spec.enforce([]string{t}, c)
		if err != nil {
			return nil, err
		}
		return out[0], nil
	case []any:
		vals := make([]string, len(t))
		for i, e := range t {
			vals[i], _ = e.(string)
		}
		out, err := spec.enforce(vals, c)
		if err != nil {
			return nil, err
		}
		res := make([]any, len(out))
		for i, s := range out {
			res[i] = s
		}
		return res, nil
	}
	return nv, nil
}

// ---- bornes dépendantes du contexte ----

type boundCtx struct {
	machine *profiles.Resolved
	fil     *profiles.Resolved // filament visé (kind filament)
	lim     PrinterLimits
	cur     float64 // plus grande valeur actuelle de la clé dans le profil officiel (0 : aucune)
}

func maxOfList(r *profiles.Resolved, keys ...string) (float64, bool) {
	best, ok := 0.0, false
	for _, k := range keys {
		for _, s := range r.Strings(k) {
			if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil && f > 0 {
				ok = true
				best = math.Max(best, f)
			}
		}
	}
	return best, ok
}

func firstFloat(r *profiles.Resolved, key string) (float64, bool) {
	if r == nil {
		return 0, false
	}
	s, ok := r.String(key)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f, err == nil
}

// effective retourne les bornes [lo, hi] effectives d'une clé, et la source de la borne haute.
func (s *KeySpec) effective(c boundCtx) (lo, hi float64, src string) {
	lo, hi, src = s.Min, s.Max, "borne statique"
	tighten := func(v float64, from string) {
		if v < hi {
			hi, src = v, from
		}
	}
	switch s.Bound {
	// Les profils officiels dépassent parfois eux-mêmes le maximum machine (déplacements : le micrologiciel
	// écrête). Une surcharge ne dépasse donc jamais max(maximum machine, valeur actuelle du profil officiel).
	case bndSpeed:
		if v, ok := maxOfList(c.machine, "machine_max_speed_x", "machine_max_speed_y"); ok {
			tighten(math.Max(v, c.cur), "machine_max_speed_x/y du profil machine")
		}
	case bndAccel:
		if v, ok := maxOfList(c.machine, "machine_max_acceleration_extruding"); ok {
			tighten(math.Max(v, c.cur), "machine_max_acceleration_extruding du profil machine")
		}
	case bndAccelTravel:
		if v, ok := maxOfList(c.machine, "machine_max_acceleration_travel"); ok {
			tighten(math.Max(v, c.cur), "machine_max_acceleration_travel du profil machine")
		}
	case bndLayer:
		if v, ok := firstFloat(c.machine, "nozzle_diameter"); ok && v > 0 {
			tighten(v, "diamètre de buse du profil machine")
		}
	case bndBedTemp:
		tighten(c.lim.BedMaxC, "maximum plateau de "+c.lim.Source)
	case bndNozzleTemp:
		tighten(c.lim.NozzleMaxC, "maximum buse de "+c.lim.Source)
		if c.fil != nil {
			if v, ok := firstFloat(c.fil, "nozzle_temperature_range_high"); ok && v > 0 {
				tighten(v, "nozzle_temperature_range_high du filament")
			}
			if v, ok := firstFloat(c.fil, "nozzle_temperature_range_low"); ok && v > 0 {
				lo = math.Max(lo, v)
			}
		}
	}
	return lo, hi, src
}

// enforce valide (et normalise) les valeurs d'une clé de la liste blanche. vals sont les valeurs de
// configuration en chaînes (une par entrée de liste).
func (s *KeySpec) enforce(vals []string, c boundCtx) ([]string, error) {
	lo, hi, src := s.effective(c)
	out := make([]string, len(vals))
	for i, v := range vals {
		v = strings.TrimSpace(v)
		switch s.Kind {
		case kEnum:
			ok := false
			for _, e := range s.Enum {
				if e == v {
					ok = true
				}
			}
			if !ok {
				return nil, fmt.Errorf("%w: %q: valeur %q hors énumération (%s)", ErrOverride, s.Key, v, strings.Join(s.Enum, ", "))
			}
			out[i] = v
			continue
		case kBool:
			if v != "0" && v != "1" {
				return nil, fmt.Errorf("%w: %q: booléen attendu (0 ou 1), reçu %q", ErrOverride, s.Key, v)
			}
			out[i] = v
			continue
		}
		raw := v
		pct := strings.HasSuffix(v, "%")
		if pct {
			raw = strings.TrimSuffix(v, "%")
			if s.Kind != kPct && s.Kind != kNumPct {
				return nil, fmt.Errorf("%w: %q: un pourcentage n'est pas accepté ici, reçu %q", ErrOverride, s.Key, v)
			}
		}
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("%w: %q: nombre attendu, reçu %q", ErrOverride, s.Key, v)
		}
		if s.Kind == kInt && f != math.Trunc(f) {
			return nil, fmt.Errorf("%w: %q: entier attendu, reçu %q", ErrOverride, s.Key, v)
		}
		l, h := lo, hi
		if pct && s.Kind == kNumPct {
			l, h = 1, 100
		}
		if f < l || f > h {
			what := src
			if pct && s.Kind == kNumPct {
				what = "pourcentage"
			}
			return nil, fmt.Errorf("%w: %q = %s hors bornes [%g ; %g] (%s)", ErrOverride, s.Key, v, l, h, what)
		}
		switch {
		case s.Kind == kPct && !pct:
			out[i] = raw + "%"
		default:
			out[i] = v
		}
	}
	return out, nil
}
