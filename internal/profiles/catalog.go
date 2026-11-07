package profiles

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Point est un point 2D du plateau en mm.
type Point struct{ X, Y float64 }

// Variant est une configuration machine concrète (modèle + buse).
type Variant struct {
	Profile      string    `json:"profile"` // nom du preset machine, ex. "Bambu Lab P1S 0.4 nozzle"
	Nozzle       float64   `json:"nozzle"`  // diamètre de la première buse, mm
	Extruders    int       `json:"extruders"`
	BedArea      []Point   `json:"bed_area"` // polygone printable_area
	BedX         float64   `json:"bed_x"`    // étendue du polygone
	BedY         float64   `json:"bed_y"`
	PrintableZ   float64   `json:"printable_height"`
	ExcludeAreas [][]Point `json:"exclude_areas,omitempty"`
	// ExtruderAreas : zone imprimable propre à chaque buse (machines à double buse comme la H2D),
	// dans l'ordre des extrudeurs ; vide pour une machine à buse unique.
	ExtruderAreas [][]Point `json:"extruder_areas,omitempty"`
	// ExtruderHeights : hauteur imprimable par buse (extruder_printable_height) quand le profil la donne.
	ExtruderHeights []float64 `json:"extruder_heights,omitempty"`
	BedTypes        []string  `json:"bed_types,omitempty"` // plateaux supportés si le profil les déclare
	DefaultProces   string    `json:"default_process,omitempty"`
	DefaultFilmts   []string  `json:"default_filaments,omitempty"`
}

// Printer est un modèle d'imprimante Bambu et ses variantes de buse.
type Printer struct {
	Model            string    `json:"model"` // "Bambu Lab P1S"
	ModelID          string    `json:"model_id,omitempty"`
	Family           string    `json:"family,omitempty"`
	Tech             string    `json:"tech,omitempty"`
	Nozzles          []float64 `json:"nozzles"` // diamètres proposés par le modèle
	DefaultBedType   string    `json:"default_bed_type,omitempty"`
	DefaultMaterials []string  `json:"default_materials,omitempty"`
	Variants         []Variant `json:"variants"`
}

// Printers liste les modèles d'imprimante, avec leurs variantes de buse
// (presets machine instanciables dont printer_model correspond), triés par nom.
func (s *Store) Printers() ([]Printer, error) {
	var out []Printer
	for name, mp := range s.byKind[KindMachineModel] {
		p := Printer{Model: name}
		p.ModelID = str(mp.Raw["model_id"])
		p.Family = str(mp.Raw["family"])
		p.Tech = str(mp.Raw["machine_tech"])
		p.DefaultBedType = str(mp.Raw["default_bed_type"])
		for _, f := range strings.Split(str(mp.Raw["nozzle_diameter"]), ";") {
			if v, err := strconv.ParseFloat(strings.TrimSpace(f), 64); err == nil {
				p.Nozzles = append(p.Nozzles, v)
			}
		}
		for _, f := range strings.Split(str(mp.Raw["default_materials"]), ";") {
			if f = strings.TrimSpace(f); f != "" {
				p.DefaultMaterials = append(p.DefaultMaterials, f)
			}
		}
		out = append(out, p)
	}
	idx := map[string]int{}
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	for i := range out {
		idx[out[i].Model] = i
	}
	for _, mn := range s.Names(KindMachine) {
		r, err := s.Resolve(KindMachine, mn)
		if err != nil {
			return nil, err
		}
		model, _ := r.String("printer_model")
		i, ok := idx[model]
		if !ok {
			continue
		}
		v, err := variantOf(r)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", mn, err)
		}
		out[i].Variants = append(out[i].Variants, v)
	}
	for i := range out {
		sort.Slice(out[i].Variants, func(a, b int) bool {
			va, vb := out[i].Variants[a], out[i].Variants[b]
			if va.Nozzle != vb.Nozzle {
				return va.Nozzle < vb.Nozzle
			}
			return va.Profile < vb.Profile
		})
	}
	return out, nil
}

func variantOf(r *Resolved) (Variant, error) {
	v := Variant{Profile: r.Name}
	nz := r.Strings("nozzle_diameter")
	v.Extruders = len(nz)
	if len(nz) > 0 {
		v.Nozzle, _ = strconv.ParseFloat(nz[0], 64)
	}
	area, err := parsePoly(r.Strings("printable_area"))
	if err != nil {
		return v, err
	}
	v.BedArea = area
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, p := range area {
		minX, maxX = math.Min(minX, p.X), math.Max(maxX, p.X)
		minY, maxY = math.Min(minY, p.Y), math.Max(maxY, p.Y)
	}
	if len(area) > 0 {
		v.BedX, v.BedY = maxX-minX, maxY-minY
	}
	if h, ok := r.String("printable_height"); ok {
		v.PrintableZ, _ = strconv.ParseFloat(h, 64)
	}
	// zones d'exclusion : liste plate de "XxY" par rectangle de 4 points, ou
	// plusieurs polygones concaténés ; on les regroupe par 4 points.
	if ex, err := parsePoly(r.Strings("bed_exclude_area")); err == nil {
		for i := 0; i+4 <= len(ex); i += 4 {
			v.ExcludeAreas = append(v.ExcludeAreas, ex[i:i+4])
		}
	}
	// zones imprimables par buse : une chaîne par extrudeur, paires "XxY" séparées par des virgules.
	for _, item := range r.Strings("extruder_printable_area") {
		pts, err := parsePoly(strings.Split(item, ","))
		if err != nil || len(pts) < 3 {
			v.ExtruderAreas = nil
			break
		}
		v.ExtruderAreas = append(v.ExtruderAreas, pts)
	}
	for _, item := range r.Strings("extruder_printable_height") {
		h, err := strconv.ParseFloat(strings.TrimSpace(item), 64)
		if err != nil {
			v.ExtruderHeights = nil
			break
		}
		v.ExtruderHeights = append(v.ExtruderHeights, h)
	}
	v.BedTypes = r.Strings("supported_bed_types")
	v.DefaultProces, _ = r.String("default_print_profile")
	v.DefaultFilmts = r.Strings("default_filament_profile")
	return v, nil
}

func parsePoly(items []string) ([]Point, error) {
	var out []Point
	for _, it := range items {
		a, b, ok := strings.Cut(strings.TrimSpace(it), "x")
		if !ok {
			return nil, fmt.Errorf("point invalide %q", it)
		}
		x, e1 := strconv.ParseFloat(a, 64)
		y, e2 := strconv.ParseFloat(b, 64)
		if e1 != nil || e2 != nil {
			return nil, fmt.Errorf("point invalide %q", it)
		}
		out = append(out, Point{x, y})
	}
	return out, nil
}

// Printer retourne un modèle par nom exact.
func (s *Store) Printer(model string) (*Printer, error) {
	ps, err := s.Printers()
	if err != nil {
		return nil, err
	}
	for i := range ps {
		if ps[i].Model == model {
			return &ps[i], nil
		}
	}
	return nil, fmt.Errorf("%w: imprimante %q", ErrNotFound, model)
}

// MachineProfile retourne le nom du preset machine d'un modèle et d'une buse.
func (s *Store) MachineProfile(model string, nozzle float64) (string, error) {
	p, err := s.Printer(model)
	if err != nil {
		return "", err
	}
	for _, v := range p.Variants {
		if math.Abs(v.Nozzle-nozzle) < 1e-6 {
			return v.Profile, nil
		}
	}
	return "", fmt.Errorf("%w: pas de preset machine %q avec buse %.2g", ErrNotFound, model, nozzle)
}

// CompatibleProcesses liste les presets process utilisables avec un preset machine.
func (s *Store) CompatibleProcesses(machine string) ([]string, error) {
	return s.compatible(KindProcess, machine)
}

// CompatibleFilaments liste les presets filament utilisables avec un preset machine.
func (s *Store) CompatibleFilaments(machine string) ([]string, error) {
	return s.compatible(KindFilament, machine)
}

// IsCompatible indique si un preset process ou filament convient à une machine.
func (s *Store) IsCompatible(k Kind, name, machine string) (bool, error) {
	m, err := s.Resolve(KindMachine, machine)
	if err != nil {
		return false, err
	}
	r, err := s.Resolve(k, name)
	if err != nil {
		return false, err
	}
	return compatibleWith(r, m)
}

func (s *Store) compatible(k Kind, machine string) ([]string, error) {
	m, err := s.Resolve(KindMachine, machine)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, n := range s.Names(k) {
		r, err := s.Resolve(k, n)
		if err != nil {
			return nil, err
		}
		ok, err := compatibleWith(r, m)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", n, err)
		}
		if ok {
			out = append(out, n)
		}
	}
	return out, nil
}

// compatibleWith reproduit la règle de Bambu Studio : si compatible_printers
// est non vide, la machine doit y figurer ; sinon la condition (vide = vrai)
// est évaluée sur la configuration de la machine.
func compatibleWith(r, machine *Resolved) (bool, error) {
	if list := r.Strings("compatible_printers"); len(list) > 0 {
		for _, n := range list {
			if n == machine.Name {
				return true, nil
			}
		}
		return false, nil
	}
	cond, _ := r.String("compatible_printers_condition")
	if strings.TrimSpace(cond) == "" {
		return true, nil
	}
	return EvalCondition(cond, machine.Config)
}
