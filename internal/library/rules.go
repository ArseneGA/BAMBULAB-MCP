package library

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/engineering"
)

// resolver applique les règles d'ingénierie « auto » du schéma d'un gabarit :
//
//	fit:<type>         jeu diamétral d'ajustement (libre|glissant|serré|press), calibré si possible ;
//	fitside:<type>     idem, par côté (moitié du jeu diamétral) ;
//	gap_pip            jeu radial/axial d'une pièce imprimée en place (calibré : jeu glissant mesuré) ;
//	hole:<orientation> compensation du diamètre nominal d'un trou (vertical|horizontal), même fourni ;
//	insert:diameter|depth|boss  trou, profondeur et plot d'un insert à chaud pour la vis `screw_size` / `insert_size`.
//
// Les valeurs fournies par l'appelant priment (sauf hole:* qui compense le nominal fourni, sauf compensate=false).
type resolver struct {
	cal        *engineering.Calibration
	material   string // identifiant matériau reconnu par engineering, "" sinon
	nozzle     float64
	compensate bool
}

func (r *resolver) src(res engineering.Result) string {
	if res.Level == engineering.LevelMeasured && r.cal != nil {
		return "calibration — " + r.cal.Provenance
	}
	return "valeur générique (aucune calibration applicable)"
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

func wrapEng(err error) error {
	if errors.Is(err, engineering.ErrInvalidInput) {
		return fmt.Errorf("%w : %v", ErrInvalid, err)
	}
	return err
}

// apply calcule les paramètres auto ; req n'est pas modifié. Retourne les paramètres complets à envoyer au worker.
func (r *resolver) apply(t TemplateInfo, req map[string]any) (map[string]any, []AutoParam, []Calc, []string, error) {
	params := make(map[string]any, len(req))
	for k, v := range req {
		params[k] = v
	}
	var autos []AutoParam
	var calcs []Calc
	var warns []string
	boss := false
	for _, sp := range t.Params {
		if sp.Auto == "" {
			continue
		}
		_, given := req[sp.Name]
		rule := strings.Split(sp.Auto, ":")
		switch rule[0] {
		case "fit", "fitside":
			if given {
				continue
			}
			if len(rule) < 2 {
				continue
			}
			res, err := engineering.FitClearanceCal(engineering.FitParams{Fit: engineering.FitType(rule[1]), Material: r.material, Nozzle: r.nozzle, Orientation: "vertical"}, r.cal)
			if err != nil {
				return nil, nil, nil, nil, wrapEng(err)
			}
			v := res.Value
			if rule[0] == "fitside" {
				v = res.PerSide
			}
			params[sp.Name] = v
			autos = append(autos, AutoParam{Param: sp.Name, Rule: sp.Auto, Value: v, Unit: "mm", Level: string(res.Level), Source: r.src(res.Result)})
			calcs = append(calcs, Calc{Name: "fit_clearance", Param: sp.Name, Result: res.Result, Extra: map[string]any{"fit": rule[1], "diametral_mm": res.Value, "per_side_mm": res.PerSide}})
		case "gap_pip":
			if given {
				continue
			}
			var v float64
			var res engineering.Result
			if r.cal != nil && r.cal.SlidesGapMM != nil {
				v = *r.cal.SlidesGapMM
				res = engineering.Result{Value: v, Unit: "mm", Level: engineering.LevelMeasured,
					Assumptions: []string{"Jeu glissant mesuré utilisé comme jeu de la pièce imprimée en place (" + r.cal.Provenance + ")"},
					Validity:    "Même imprimante/buse/bobine. " + engineering.Disclaimer}
			} else {
				fr, err := engineering.FitClearanceCal(engineering.FitParams{Fit: engineering.FitFree, Material: r.material, Nozzle: r.nozzle, Orientation: "vertical"}, nil)
				if err != nil {
					return nil, nil, nil, nil, wrapEng(err)
				}
				v = math.Round(fr.Value*0.75*1000) / 1000
				res = fr.Result
				res.Value = v
				res.Assumptions = append([]string{"Jeu d'une pièce imprimée en place = 0,75 × jeu « libre » (0,30 mm pour une buse de 0,4 mm) : assez large pour ne pas souder, assez étroit pour rester guidé"}, res.Assumptions...)
				res.Warnings = append(res.Warnings, "Pièce imprimée en place : sans calibration, un essai d'impression est indispensable (les jeux < 0,2 mm soudent souvent).")
			}
			params[sp.Name] = v
			autos = append(autos, AutoParam{Param: sp.Name, Rule: sp.Auto, Value: v, Unit: "mm", Level: string(res.Level), Source: r.src(res)})
			calcs = append(calcs, Calc{Name: "print_in_place_gap", Param: sp.Name, Result: res})
		case "hole":
			if !r.compensate || len(rule) < 2 {
				continue
			}
			nom, ok := toFloat(params[sp.Name])
			if !given {
				nom, ok = toFloat(sp.Default)
			}
			if !ok || nom == 0 {
				continue
			}
			res, err := engineering.HoleCompensationCal(engineering.HoleParams{Diameter: nom, Axis: rule[1], Nozzle: r.nozzle, Material: r.material}, r.cal)
			if err != nil {
				warns = append(warns, fmt.Sprintf("%s : compensation non appliquée (%v).", sp.Name, err))
				continue
			}
			v := res.Value
			if sp.Max != nil && v > *sp.Max {
				warns = append(warns, fmt.Sprintf("%s : valeur compensée %.3f > maximum %g, plafonnée.", sp.Name, v, *sp.Max))
				v = *sp.Max
			}
			params[sp.Name] = v
			autos = append(autos, AutoParam{Param: sp.Name, Rule: sp.Auto, Nominal: nom, Value: v, Unit: "mm", Level: string(res.Level), Source: r.src(*res)})
			calcs = append(calcs, Calc{Name: "hole_compensation", Param: sp.Name, Result: *res, Extra: map[string]any{"nominal_mm": nom, "axis": rule[1]}})
		case "insert":
			if len(rule) < 2 || given {
				continue
			}
			if eff(t, params, "lid_mode") == "clip" {
				continue
			}
			if t.ID == "thread" && eff(t, params, "mode") != "insert_boss" {
				continue
			}
			size := sizeParam(t, params)
			sb, err := engineering.ScrewBoss(size, engineering.ScrewHeatSet, r.material)
			if err != nil {
				return nil, nil, nil, nil, wrapEng(err)
			}
			var v float64
			switch rule[1] {
			case "diameter":
				v = sb.Value
			case "depth":
				v = sb.HoleDepth
			case "boss":
				v = sb.BossOD
			default:
				continue
			}
			params[sp.Name] = v
			autos = append(autos, AutoParam{Param: sp.Name, Rule: sp.Auto, Value: v, Unit: "mm", Level: string(sb.Level), Source: "table d'inserts à chaud (" + size + ")"})
			if !boss {
				boss = true
				calcs = append(calcs, Calc{Name: "screw_boss", Param: size, Result: sb.Result,
					Extra: map[string]any{"size": size, "hole_mm": sb.Value, "depth_mm": sb.HoleDepth, "boss_od_mm": sb.BossOD, "min_wall_mm": sb.MinWallMM, "typical_insert_length_mm": sb.InsertLen}})
			}
		}
	}
	return params, autos, calcs, warns, nil
}

// eff retourne la valeur texte effective d'un paramètre : fournie, sinon défaut du schéma.
func eff(t TemplateInfo, params map[string]any, name string) string {
	if v, ok := params[name].(string); ok {
		return v
	}
	for _, sp := range t.Params {
		if sp.Name == name {
			d, _ := sp.Default.(string)
			return d
		}
	}
	return ""
}

func sizeParam(t TemplateInfo, params map[string]any) string {
	for _, n := range []string{"screw_size", "insert_size"} {
		if v, ok := params[n].(string); ok && v != "" {
			return v
		}
		for _, sp := range t.Params {
			if sp.Name == n {
				if d, ok := sp.Default.(string); ok {
					return d
				}
			}
		}
	}
	return "M3"
}

// postCalcs : calculs propres à un gabarit, sur les paramètres finaux.
func (r *resolver) postCalcs(t TemplateInfo, given map[string]any) ([]Calc, []string) {
	p := make(map[string]any, len(t.Params))
	for _, sp := range t.Params {
		p[sp.Name] = sp.Default
	}
	for k, v := range given {
		p[k] = v
	}
	switch t.ID {
	case "snap_clip":
		if r.material == "" {
			return nil, []string{"Clip non vérifié : matériau inconnu du moteur d'ingénierie (donner un identifiant connu : PLA, PETG, ABS, ASA, TPU-95A, PC, PA-CF...)."}
		}
		g := func(n string) float64 { v, _ := toFloat(p[n]); return v }
		res, err := engineering.SnapFit(engineering.SnapParams{Length: g("beam_length"), Thickness: g("beam_thickness"), Width: g("beam_width"),
			Deflection: g("hook_depth"), Material: r.material, Orientation: "xy", InsertAngle: g("insertion_angle")})
		if err != nil {
			return nil, []string{"Clip non vérifié : " + err.Error()}
		}
		c := Calc{Name: "snap_fit", Result: res.Result, Extra: map[string]any{
			"strain": res.Value, "strain_allowable": res.StrainAllowable, "max_deflection_mm": res.MaxDeflectionMM,
			"bending_force_N": res.BendingForceN, "insertion_force_N": res.InsertionForceN, "insertion_force_unbounded": res.InsertionForceUnbounded, "ok": res.OK}}
		var w []string
		if !res.OK {
			w = append(w, fmt.Sprintf("SnapFit : déformation %.3f > admissible %.3f — le bras risque de casser ; l'allonger, l'amincir ou réduire hook_depth (flèche max %.2f mm).", res.Value, res.StrainAllowable, res.MaxDeflectionMM))
		}
		return []Calc{c}, w
	}
	return nil, nil
}
