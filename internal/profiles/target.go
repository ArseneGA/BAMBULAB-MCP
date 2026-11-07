package profiles

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

func bboxOf(pts []Point) mesh.Rect {
	r := mesh.Rect{X0: math.Inf(1), Y0: math.Inf(1), X1: math.Inf(-1), Y1: math.Inf(-1)}
	for _, p := range pts {
		r.X0, r.X1 = math.Min(r.X0, p.X), math.Max(r.X1, p.X)
		r.Y0, r.Y1 = math.Min(r.Y0, p.Y), math.Max(r.Y1, p.Y)
	}
	return r
}

// MeshTarget convertit une variante machine en cible de fabricabilité : volume utile, buse, zones
// d'exclusion et volume imprimable de chaque buse quand le profil le donne (double buse).
func (v Variant) MeshTarget(model string) mesh.Target {
	t := mesh.Target{Name: model, Source: mesh.TargetFromProfile, Profile: v.Profile,
		BedX: v.BedX, BedY: v.BedY, BedZ: v.PrintableZ, Nozzle: v.Nozzle, Extruders: v.Extruders}
	if len(v.BedArea) > 0 {
		t.Bed = bboxOf(v.BedArea)
	}
	for _, e := range v.ExcludeAreas {
		t.Exclusions = append(t.Exclusions, bboxOf(e))
	}
	if len(v.ExtruderAreas) > 1 {
		for i, a := range v.ExtruderAreas {
			nv := mesh.NozzleVolume{Index: i + 1, Area: bboxOf(a), HeightMM: v.PrintableZ}
			if i < len(v.ExtruderHeights) && v.ExtruderHeights[i] > 0 {
				nv.HeightMM = v.ExtruderHeights[i]
			}
			t.Volumes = append(t.Volumes, nv)
		}
	} else if v.Extruders > 1 {
		t.Notes = append(t.Notes, fmt.Sprintf("double buse (%d) : le profil ne donne pas de volume utile par buse, volume global utilisé", v.Extruders))
	}
	return t
}

// modelAliases : alias courts historiques → nom normalisé du modèle Bambu.
var modelAliases = map[string]string{
	"x1c": "x1carbon", "x1": "x1carbon", "a1m": "a1mini", "h2dpro": "h2dpro",
}

// TargetNames liste les noms de modèle d'imprimante connus (pour les messages d'erreur).
func (s *Store) TargetNames() []string {
	var out []string
	for name := range s.byKind[KindMachineModel] {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// TargetFor retourne la cible d'un modèle d'imprimante Bambu (« p1s », « Bambu Lab H2D », « a1 mini »…)
// et d'une buse (0 = 0.4 mm, sinon la première variante). Le nom d'un preset machine complet est accepté.
func (s *Store) TargetFor(name string, nozzle float64) (mesh.Target, error) {
	key := mesh.NormalizeMachineName(name)
	if a, ok := modelAliases[key]; ok {
		key = a
	}
	ps, err := s.Printers()
	if err != nil {
		return mesh.Target{}, err
	}
	var hit *Printer
	var variant *Variant
	for i := range ps {
		if mesh.NormalizeMachineName(ps[i].Model) == key {
			hit = &ps[i]
			break
		}
	}
	if hit == nil { // preset machine complet ("Bambu Lab P1S 0.4 nozzle")
		for i := range ps {
			for j := range ps[i].Variants {
				if mesh.NormalizeMachineName(ps[i].Variants[j].Profile) == key {
					hit, variant = &ps[i], &ps[i].Variants[j]
				}
			}
		}
	}
	if hit == nil {
		names := make([]string, 0, len(ps))
		for _, p := range ps {
			names = append(names, strings.TrimPrefix(p.Model, "Bambu Lab "))
		}
		return mesh.Target{}, fmt.Errorf("%w: imprimante %q (modèles : %s)", ErrNotFound, name, strings.Join(names, ", "))
	}
	if variant == nil {
		want := nozzle
		if want <= 0 {
			want = 0.4
		}
		for j := range hit.Variants {
			if math.Abs(hit.Variants[j].Nozzle-want) < 1e-6 {
				variant = &hit.Variants[j]
				break
			}
		}
		if variant == nil && nozzle > 0 {
			return mesh.Target{}, fmt.Errorf("%w: pas de variante de buse %.2g mm pour %s (buses : %v)", ErrNotFound, nozzle, hit.Model, hit.Nozzles)
		}
		if variant == nil && len(hit.Variants) > 0 {
			variant = &hit.Variants[0]
		}
	}
	if variant == nil {
		return mesh.Target{}, fmt.Errorf("%w: %s sans preset machine", ErrNotFound, hit.Model)
	}
	return variant.MeshTarget(hit.Model), nil
}

// Targets retourne une cible par modèle d'imprimante (buse 0.4 si disponible), triées par nom.
func (s *Store) Targets() ([]mesh.Target, error) {
	ps, err := s.Printers()
	if err != nil {
		return nil, err
	}
	var out []mesh.Target
	for _, p := range ps {
		if len(p.Variants) == 0 {
			continue
		}
		v := p.Variants[0]
		for _, c := range p.Variants {
			if math.Abs(c.Nozzle-0.4) < 1e-6 {
				v = c
				break
			}
		}
		out = append(out, v.MeshTarget(p.Model))
	}
	return out, nil
}

// TargetForMachine retourne la cible d'un preset machine exact.
func (s *Store) TargetForMachine(machine string) (mesh.Target, error) {
	r, err := s.Resolve(KindMachine, machine)
	if err != nil {
		return mesh.Target{}, err
	}
	v, err := variantOf(r)
	if err != nil {
		return mesh.Target{}, err
	}
	model, _ := r.String("printer_model")
	if model == "" {
		model = machine
	}
	return v.MeshTarget(model), nil
}

// SupportThresholdAngle lit support_threshold_angle d'un preset process : angle de pente mesuré depuis
// l'horizontale en dessous duquel Bambu Studio génère des supports (0° = plafond plat, 90° = paroi).
// ok est faux quand la clé est absente ou nulle (0 = détection automatique côté slicer).
func (s *Store) SupportThresholdAngle(process string) (deg float64, ok bool, err error) {
	r, err := s.Resolve(KindProcess, process)
	if err != nil {
		return 0, false, err
	}
	str, found := r.String("support_threshold_angle")
	if !found {
		return 0, false, nil
	}
	v, perr := strconv.ParseFloat(strings.TrimSpace(str), 64)
	if perr != nil || v <= 0 || v >= 90 {
		return 0, false, nil
	}
	return v, true, nil
}

// DefaultProcessOf retourne le preset process par défaut d'un preset machine (default_print_profile).
func (s *Store) DefaultProcessOf(machine string) (string, bool) {
	r, err := s.Resolve(KindMachine, machine)
	if err != nil {
		return "", false
	}
	p, ok := r.String("default_print_profile")
	return p, ok && p != ""
}
