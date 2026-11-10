package strength

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/fem"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// Valeurs de `source` d'une charge ou d'un appui.
const (
	SourceUser    = "user"    // donnée par l'humain
	SourceAssumed = "assumed" // hypothèse proposée par Claude et annoncée à l'humain
)

// Plafonds de taille du calcul.
const (
	MaxSolidVoxels = 60_000 // maillage « standard » : rapide, converge en quelques secondes (FB9)
	// MaxSolidVoxelsFine : maillage « fin » (resolution = fine), à demander explicitement : plus lent, parfois non convergé.
	MaxSolidVoxelsFine = 150_000
	MaxGridCells       = 3_000_000
	// DefaultSolveSeconds : temps maximal du calcul (voxélisation, résolution, reprises) avant de rendre un résultat « non convergé ».
	DefaultSolveSeconds = 150.0
	MaxSolveSeconds     = 1800.0
	MaxForceN           = 100_000
)

// LoadIn : une charge.
type LoadIn struct {
	Zone            int       `json:"zone,omitempty" jsonschema:"numéro de zone (celui de l'image de plan_strength_test) où la charge s'applique"`
	Point           []float64 `json:"point,omitempty" jsonschema:"à la place de zone : point [x, y, z] en mm (repère du modèle)"`
	RadiusMM        float64   `json:"radius_mm,omitempty" jsonschema:"avec point : rayon de la zone d'application (défaut : quelques mm)"`
	ForceN          float64   `json:"force_N,omitempty" jsonschema:"intensité TOTALE de la charge en newtons (kg × 9,81)"`
	MassKg          float64   `json:"mass_kg,omitempty" jsonschema:"à la place de force_N : masse en kg (convertie en newtons)"`
	Direction       string    `json:"direction,omitempty" jsonschema:"down (défaut, vers le bas) | up | +x | -x | +y | -y | +z | -z"`
	DirectionVector []float64 `json:"direction_vector,omitempty" jsonschema:"à la place de direction : vecteur [x, y, z]"`
	Source          string    `json:"source" jsonschema:"user (donnée par l'humain) | assumed (hypothèse annoncée à l'humain)"`
	Note            string    `json:"note,omitempty" jsonschema:"origine de la valeur, ex. guitare 4,5 kg × 1,5"`
}

// SupportIn : un appui.
type SupportIn struct {
	Zone   int    `json:"zone" jsonschema:"numéro de zone qui tient la pièce ; 0 avec type resting = plan de pose (surface de contact au sol) détecté automatiquement"`
	Type   string `json:"type" jsonschema:"fixed (vissée, collée, encastrée) | resting (posée dessus : ne retient que dans un sens)"`
	Source string `json:"source" jsonschema:"user | assumed"`
	Note   string `json:"note,omitempty"`
}

// AssessInput : demande de calcul.
type AssessInput struct {
	ModelID          string      `json:"model_id"`
	VersionID        string      `json:"version_id,omitempty" jsonschema:"version du modèle (défaut : la plus récente)"`
	Loads            []LoadIn    `json:"loads" jsonschema:"charges : au moins une (fournie par l'humain ou hypothèse annoncée)"`
	Supports         []SupportIn `json:"supports" jsonschema:"appuis : au moins un"`
	Material         string      `json:"material,omitempty" jsonschema:"PLA, PETG, ABS, ASA, PA ou TPU (absent : PLA supposé, signalé)"`
	Infill           float64     `json:"infill,omitempty" jsonschema:"remplissage en % (défaut 15, signalé)"`
	Walls            int         `json:"walls,omitempty" jsonschema:"nombre de parois (défaut 2, signalé)"`
	LayerOrientation string      `json:"layer_orientation,omitempty" jsonschema:"axe du modèle perpendiculaire aux couches à l'impression : z (défaut : imprimée comme modélisée) | x | y"`
	SafetyFactor     float64     `json:"safety_factor,omitempty" jsonschema:"coefficient de sécurité visé (défaut 2)"`
	LoadKind         string      `json:"load_kind,omitempty" jsonschema:"static (défaut) | impact (charge lâchée : forces doublées)"`
	Duration         string      `json:"duration,omitempty" jsonschema:"permanent (défaut : fluage pris en compte) | occasional"`
	Resolution       string      `json:"resolution,omitempty" jsonschema:"standard (défaut : ≈ 60 000 cubes, rapide) | fine (≈ 150 000 cubes, plus lent, risque de ne pas converger dans le temps imparti)"`
	MaxSeconds       float64     `json:"max_seconds,omitempty" jsonschema:"temps maximal du calcul en secondes (défaut 150, max 1800) ; à l'échéance le résultat est rendu « non convergé », jamais présenté comme fiable"`
	maxSolidOverride int         `json:"-"` // tests : cible de cubes pleins plus basse (calculs rapides)
}

// Marked : valeur avec sa provenance (pour les entrées enregistrées et affichées).
type Marked struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Source string `json:"source" jsonschema:"user | assumed | default"`
}

// Result : résultat du calcul.
type Result struct {
	ModelID    string `json:"model_id"`
	VersionID  string `json:"version_id"`
	Verdict    string `json:"verdict" jsonschema:"ok | a_surveiller | risque | non_converge (le solveur n'a pas convergé : le résultat n'est PAS fiable)"`
	VerdictTxt string `json:"verdict_text"`

	SafetyAchieved    float64    `json:"safety_factor_achieved"`
	SafetyRequired    float64    `json:"safety_factor_required"`
	MaxVonMisesMPa    float64    `json:"max_von_mises_mpa" jsonschema:"contrainte de von Mises maximale du champ (hors voisinage immédiat des appuis et des charges)"`
	MaxVMPoint        [3]float64 `json:"max_von_mises_point_mm"`
	CriticalStressMPa float64    `json:"critical_von_mises_mpa" jsonschema:"von Mises au point critique (celui où contrainte / résistance est la plus élevée)"`
	MaxUtilization    float64    `json:"max_utilization" jsonschema:"contrainte / résistance admissible au point critique (1 = rupture estimée)"`
	LimitingCriteria  string     `json:"limiting_criterion" jsonschema:"dans_le_plan | entre_couches_traction | entre_couches_cisaillement"`
	AllowableXYMPa    float64    `json:"allowable_xy_mpa"`
	AllowableZMPa     float64    `json:"allowable_interlayer_mpa"`

	CriticalPoint [3]float64 `json:"critical_point_mm"`
	CriticalZone  int        `json:"critical_zone,omitempty"`
	CriticalNote  string     `json:"critical_note"`
	LocalThickMM  float64    `json:"local_thickness_mm,omitempty"`

	MaxDisplacementMM float64    `json:"max_displacement_mm"`
	MaxDispPoint      [3]float64 `json:"max_displacement_point_mm"`

	Orientations map[string]float64 `json:"safety_by_layer_orientation" jsonschema:"coefficient de sécurité obtenu selon l'axe perpendiculaire aux couches (même champ de contraintes)"`

	Inputs          []Marked `json:"inputs" jsonschema:"toutes les entrées avec leur provenance (user | assumed | default)"`
	BasedOnAssumed  bool     `json:"based_on_assumptions"`
	Assumptions     []string `json:"assumptions"`
	Recommendations []string `json:"recommendations"`
	Warnings        []string `json:"warnings"`
	Measured        []string `json:"measured"`
	Calculated      []string `json:"calculated"`
	Estimated       []string `json:"estimated"`

	Mesh   MeshInfo `json:"mesh"`
	Solver Solver   `json:"solver"`

	Material  Material  `json:"material"`
	Effective Effective `json:"effective"`
	ReactionN float64   `json:"reaction_total_n"`
	AppliedN  float64   `json:"applied_total_n"`
	// Écart d'équilibre : les réactions aux appuis doivent reprendre exactement les charges ; un écart important
	// signale un calcul non convergé (ou des appuis mal posés).
	EquilibriumGapN   float64      `json:"equilibrium_gap_n"`
	EquilibriumGapPct float64      `json:"equilibrium_gap_pct"`
	NotConverged      bool         `json:"not_converged,omitempty" jsonschema:"vrai : le résultat n'est pas fiable, ne pas en tirer de verdict"`
	Contact           *ContactInfo `json:"contact,omitempty" jsonschema:"appui posé unilatéral : décollement et surface qui porte réellement"`
	Disclaimer        string       `json:"disclaimer"`
	TotalSeconds      float64      `json:"total_seconds"`
	LayerAxisName     string       `json:"layer_axis"`
}

// MeshInfo : discrétisation.
type MeshInfo struct {
	VoxelMM     float64    `json:"voxel_mm"`
	Elements    int        `json:"elements"`
	Nodes       int        `json:"nodes"`
	GridCells   int        `json:"grid_cells"`
	Capped      bool       `json:"capped"`
	CapReason   string     `json:"cap_reason,omitempty"`
	SourceTris  int        `json:"source_triangles"`
	VolumeMM3   float64    `json:"volume_mm3"`
	AreaMM2     float64    `json:"area_mm2"`
	BBoxMM      [3]float64 `json:"bbox_mm"`
	LeakyColumn int        `json:"leaky_columns,omitempty"`
}

// Solver : convergence.
type Solver struct {
	Iterations int     `json:"iterations"`
	Residual   float64 `json:"relative_residual"`
	Converged  bool    `json:"converged"`
	Seconds    float64 `json:"solve_seconds"`
	StopReason string  `json:"stop_reason,omitempty" jsonschema:"converged | time_limit | stagnation | max_iterations"`
	TimedOut   bool    `json:"timed_out,omitempty"`
	Precond    string  `json:"preconditioner,omitempty"`
	BudgetSec  float64 `json:"time_budget_seconds,omitempty"`
	Retried    bool    `json:"retried_coarser,omitempty" jsonschema:"premier calcul non convergé : repris avec un maillage plus grossier"`
	Passes     int     `json:"solve_passes,omitempty"`
}

// ContactInfo : bilan du contact unilatéral des appuis posés (une pièce posée ne peut pas être tirée vers le bas :
// les points d'appui qui tireraient sont relâchés et le calcul repris).
type ContactInfo struct {
	Passes           int     `json:"passes"`
	ReleasedNodes    int     `json:"released_nodes"`
	TensionBeforePct float64 `json:"tension_before_pct" jsonschema:"part de la réaction qui tirait sur la pièce au premier calcul (appui bilatéral, irréaliste)"`
	TensionAfterPct  float64 `json:"tension_after_pct" jsonschema:"même part après relâchement des points qui tiraient"`
	ContactAreaPct   float64 `json:"contact_area_pct" jsonschema:"part de la surface d'appui posé qui porte réellement la pièce"`
	TippingSuspected bool    `json:"tipping_suspected,omitempty" jsonschema:"plus des trois quarts de la surface d'appui décollent : la pièce bascule ou n'est pas assez stable posée"`
}

// Disclaimer : mention obligatoire.
const Disclaimer = "Calcul simplifié (élasticité linéaire, matière isotrope, maillage de cubes) : il ne remplace pas un essai réel. Faire un essai de charge sur une pièce imprimée avant tout usage où une casse serait dangereuse."

// Analysis : le résultat et ce qu'il faut pour le dessiner.
type Analysis struct {
	Result *Result
	Grid   *fem.Grid
	Sol    *fem.Solution
	Zones  *ZoneSet
	VM     []float64 // von Mises aux nœuds compacts (MPa)
	Util   []float64
	Scale  float64 // borne haute de l'échelle de couleur (MPa)
	Loads  []resolvedLoad
	Supp   []resolvedSupport
	Mesh   *mesh.Mesh
}

type resolvedLoad struct {
	In     LoadIn
	Dir    mesh.Vec3
	N      float64
	Anchor mesh.Vec3
	Zone   int
}

type resolvedSupport struct {
	In     SupportIn
	Anchor mesh.Vec3
}

type restInfo struct {
	z     *Zone
	nodes []int
	axis  int
	w     map[int]float64
}

// ErrNoInput : refus quand ni charge ni hypothèse n'est fournie.
var ErrNoInput = errors.New("aucune charge ni appui fournis : poser les questions de plan_strength_test à l'humain, ou proposer des hypothèses raisonnables (source « assumed ») qu'il confirme ; ne jamais inventer en silence")

func parseDir(l LoadIn) (mesh.Vec3, error) {
	if len(l.DirectionVector) > 0 {
		if len(l.DirectionVector) != 3 {
			return mesh.Vec3{}, errors.New("direction_vector : trois nombres [x, y, z]")
		}
		v := mesh.Vec3{X: l.DirectionVector[0], Y: l.DirectionVector[1], Z: l.DirectionVector[2]}
		if v.Len() == 0 {
			return v, errors.New("direction_vector nul")
		}
		return v.Norm(), nil
	}
	switch strings.ToLower(strings.TrimSpace(l.Direction)) {
	case "", "down", "bas", "vers le bas", "-z":
		return mesh.Vec3{Z: -1}, nil
	case "up", "haut", "vers le haut", "+z", "z":
		return mesh.Vec3{Z: 1}, nil
	case "+x", "x":
		return mesh.Vec3{X: 1}, nil
	case "-x":
		return mesh.Vec3{X: -1}, nil
	case "+y", "y":
		return mesh.Vec3{Y: 1}, nil
	case "-y":
		return mesh.Vec3{Y: -1}, nil
	}
	return mesh.Vec3{}, fmt.Errorf("direction %q inconnue : down, up, +x, -x, +y, -y, +z, -z ou direction_vector", l.Direction)
}

// Validate normalise et contrôle l'entrée SANS rien inventer : charges et appuis sont obligatoires et leur origine aussi.
func (in *AssessInput) Validate() error {
	if len(in.Loads) == 0 || len(in.Supports) == 0 {
		return ErrNoInput
	}
	for i := range in.Loads {
		l := &in.Loads[i]
		if l.Source != SourceUser && l.Source != SourceAssumed {
			return fmt.Errorf("charge %d : source obligatoire (user si l'humain l'a donnée, assumed si c'est une hypothèse annoncée)", i+1)
		}
		if l.MassKg > 0 && l.ForceN == 0 {
			l.ForceN = l.MassKg * Gravity
		}
		if !(l.ForceN > 0) || l.ForceN > MaxForceN || math.IsNaN(l.ForceN) {
			return fmt.Errorf("charge %d : force_N doit être comprise entre 0 (exclu) et %d N", i+1, MaxForceN)
		}
		if (l.Zone == 0) == (len(l.Point) == 0) {
			return fmt.Errorf("charge %d : donner exactement un lieu : zone (numéro de l'image) ou point [x, y, z]", i+1)
		}
		if len(l.Point) != 0 && len(l.Point) != 3 {
			return fmt.Errorf("charge %d : point = [x, y, z]", i+1)
		}
		if _, err := parseDir(*l); err != nil {
			return fmt.Errorf("charge %d : %w", i+1, err)
		}
	}
	for i := range in.Supports {
		s := &in.Supports[i]
		if s.Source != SourceUser && s.Source != SourceAssumed {
			return fmt.Errorf("appui %d : source obligatoire (user | assumed)", i+1)
		}
		switch s.Type {
		case "fixed", "resting":
		default:
			return fmt.Errorf("appui %d : type fixed ou resting", i+1)
		}
		if s.Zone < 0 || (s.Zone == 0 && s.Type != "resting") {
			return fmt.Errorf("appui %d : zone obligatoire (0 n'est permis qu'avec type resting : plan de pose détecté automatiquement)", i+1)
		}
	}
	switch in.LayerOrientation {
	case "", "z", "x", "y":
	default:
		return errors.New("layer_orientation : z, x ou y")
	}
	switch in.LoadKind {
	case "", "static", "impact":
	default:
		return errors.New("load_kind : static ou impact")
	}
	switch in.Duration {
	case "", "permanent", "occasional":
	default:
		return errors.New("duration : permanent ou occasional")
	}
	switch in.Resolution {
	case "", "standard", "fine":
	default:
		return errors.New("resolution : standard ou fine")
	}
	if in.MaxSeconds < 0 || in.MaxSeconds > MaxSolveSeconds || math.IsNaN(in.MaxSeconds) {
		return fmt.Errorf("max_seconds hors plage (0 à %.0f)", MaxSolveSeconds)
	}
	if in.SafetyFactor < 0 || in.SafetyFactor > 20 {
		return errors.New("safety_factor hors plage (0 à 20)")
	}
	if in.Infill < 0 || in.Infill > 100 || in.Walls < 0 || in.Walls > 20 {
		return errors.New("infill 0 à 100, walls 0 à 20")
	}
	return nil
}

func axisIndex(o string) int {
	switch o {
	case "x":
		return 0
	case "y":
		return 1
	}
	return 2
}

// surface : nœuds de la frontière (au contact de cubes pleins ET vides).
func surfaceNodes(g *fem.Grid) []bool {
	b := make([]bool, g.NumNodes())
	solid := func(ix, iy, iz int) bool {
		if ix < 0 || iy < 0 || iz < 0 || ix >= g.NX || iy >= g.NY || iz >= g.NZ {
			return false
		}
		return g.Solid[g.Cell(ix, iy, iz)]
	}
	for iz := 0; iz <= g.NZ; iz++ {
		for iy := 0; iy <= g.NY; iy++ {
			for ix := 0; ix <= g.NX; ix++ {
				ns := 0
				for k := 0; k < 8; k++ {
					if solid(ix-1+(k&1), iy-1+((k>>1)&1), iz-1+((k>>2)&1)) {
						ns++
					}
				}
				if ns > 0 && ns < 8 {
					b[g.NodeIndex(ix, iy, iz)] = true
				}
			}
		}
	}
	return b
}

func nearestSurfaceNode(g *fem.Grid, bnd []bool, p mesh.Vec3) (int, bool) {
	cx := int(math.Round((p.X - g.Origin[0]) / g.H))
	cy := int(math.Round((p.Y - g.Origin[1]) / g.H))
	cz := int(math.Round((p.Z - g.Origin[2]) / g.H))
	for r := 1; r <= 4; r++ {
		best, bd := -1, math.Inf(1)
		for iz := cz - r; iz <= cz+r; iz++ {
			for iy := cy - r; iy <= cy+r; iy++ {
				for ix := cx - r; ix <= cx+r; ix++ {
					if ix < 0 || iy < 0 || iz < 0 || ix > g.NX || iy > g.NY || iz > g.NZ {
						continue
					}
					n := g.NodeIndex(ix, iy, iz)
					if !bnd[n] {
						continue
					}
					q := g.NodePos(n)
					d := (q[0]-p.X)*(q[0]-p.X) + (q[1]-p.Y)*(q[1]-p.Y) + (q[2]-p.Z)*(q[2]-p.Z)
					if d < bd || (d == bd && n < best) {
						best, bd = n, d
					}
				}
			}
		}
		if best >= 0 {
			return best, true
		}
	}
	return 0, false
}

// zoneNodes : nœuds de surface de la grille représentant une zone, avec poids (somme = 1).
func zoneNodes(g *fem.Grid, bnd []bool, zs *ZoneSet, z *Zone) map[int]float64 {
	spacing := 0.7 * g.H
	var pts []mesh.Vec3
	var area []float64
	for {
		pts, area = zs.SamplePoints(z, spacing)
		if len(pts) <= 400_000 {
			break
		}
		spacing *= 1.5
	}
	w := map[int]float64{}
	tot := 0.0
	for i, p := range pts {
		if n, ok := nearestSurfaceNode(g, bnd, p); ok {
			w[n] += area[i]
			tot += area[i]
		}
	}
	for n := range w {
		w[n] /= tot
	}
	return w
}

func pointNodes(g *fem.Grid, bnd []bool, p mesh.Vec3, radius float64) map[int]float64 {
	if radius <= 0 {
		radius = math.Max(3*g.H, 3)
	}
	w := map[int]float64{}
	r := int(math.Ceil(radius/g.H)) + 1
	cx := int(math.Round((p.X - g.Origin[0]) / g.H))
	cy := int(math.Round((p.Y - g.Origin[1]) / g.H))
	cz := int(math.Round((p.Z - g.Origin[2]) / g.H))
	for iz := cz - r; iz <= cz+r; iz++ {
		for iy := cy - r; iy <= cy+r; iy++ {
			for ix := cx - r; ix <= cx+r; ix++ {
				if ix < 0 || iy < 0 || iz < 0 || ix > g.NX || iy > g.NY || iz > g.NZ {
					continue
				}
				n := g.NodeIndex(ix, iy, iz)
				if !bnd[n] {
					continue
				}
				q := g.NodePos(n)
				if math.Sqrt((q[0]-p.X)*(q[0]-p.X)+(q[1]-p.Y)*(q[1]-p.Y)+(q[2]-p.Z)*(q[2]-p.Z)) <= radius {
					w[n] = 1
				}
			}
		}
	}
	if len(w) == 0 {
		if n, ok := nearestSurfaceNode(g, bnd, p); ok {
			w[n] = 1
		}
	}
	tot := float64(len(w))
	for n := range w {
		w[n] /= tot
	}
	return w
}

func sortedKeys(m map[int]float64) []int {
	k := make([]int, 0, len(m))
	for n := range m {
		k = append(k, n)
	}
	sort.Ints(k)
	return k
}

func fmtMPa(v float64) string { return fmt.Sprintf("%.1f MPa", v) }

// Run calcule. progress peut être nil.
func Run(ctx context.Context, src *mesh.Mesh, in AssessInput, progress func(p float64, msg string)) (*Analysis, error) {
	t0 := time.Now()
	if progress == nil {
		progress = func(float64, string) {}
	}
	if err := in.Validate(); err != nil {
		return nil, err
	}
	var inputs []Marked
	var assumptions []string
	addDefault := func(name, val, why string) {
		inputs = append(inputs, Marked{Name: name, Value: val, Source: "default"})
		assumptions = append(assumptions, fmt.Sprintf("hypothèse : %s = %s (%s)", name, val, why))
	}
	// matière
	mat, ok := LookupMaterial(in.Material)
	if strings.TrimSpace(in.Material) != "" && !ok {
		return nil, fmt.Errorf("matière %q inconnue : PLA, PETG, ABS, ASA, PA ou TPU", in.Material)
	}
	if !ok {
		mat, _ = LookupMaterial("PLA")
		addDefault("matière", "PLA", "matière non précisée ; PLA retenu, la moins favorable au fluage et à la chaleur")
	} else {
		inputs = append(inputs, Marked{Name: "matière", Value: mat.Label, Source: SourceUser})
	}
	if in.Infill == 0 {
		in.Infill = 15
		addDefault("remplissage", "15 %", "défaut du profil Bambu Studio, à confirmer")
	} else {
		inputs = append(inputs, Marked{Name: "remplissage", Value: fmt.Sprintf("%.0f %%", in.Infill), Source: SourceUser})
	}
	if in.Walls == 0 {
		in.Walls = 2
		addDefault("parois", "2", "défaut du profil Bambu Studio, à confirmer")
	} else {
		inputs = append(inputs, Marked{Name: "parois", Value: fmt.Sprint(in.Walls), Source: SourceUser})
	}
	if in.LayerOrientation == "" {
		in.LayerOrientation = "z"
		addDefault("orientation des couches", "z", "pièce imprimée dans la position modélisée (Z vers le haut) ; la direction Z est la plus faible")
	} else {
		inputs = append(inputs, Marked{Name: "orientation des couches", Value: in.LayerOrientation, Source: SourceUser})
	}
	if in.SafetyFactor == 0 {
		in.SafetyFactor = 2
		addDefault("coefficient de sécurité visé", "2", "proposé, à confirmer selon le risque")
	} else {
		inputs = append(inputs, Marked{Name: "coefficient de sécurité visé", Value: fmt.Sprint(in.SafetyFactor), Source: SourceUser})
	}
	permanent := in.Duration != "occasional"
	if in.Duration == "" {
		addDefault("durée de la charge", "permanente", "fluage pris en compte, plus prudent")
	}
	dyn := 1.0
	if in.LoadKind == "impact" {
		dyn = 2
		inputs = append(inputs, Marked{Name: "type de charge", Value: "choc (forces × 2)", Source: SourceUser})
	}

	zs := DetectZones(src)
	if len(zs.Zones) == 0 {
		return nil, errors.New("aucune zone détectée sur ce maillage")
	}
	vol := fem.MeshVolume(src)
	area := 0.0
	for i := range src.T {
		_, a := src.Normal(i)
		area += a
	}
	eff := EffectiveProps(mat, in.Infill, in.Walls, vol, area, permanent)

	budget := in.MaxSeconds
	if budget <= 0 {
		budget = DefaultSolveSeconds
	}
	secs := func(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }
	deadline := t0.Add(secs(budget))
	maxSolid := MaxSolidVoxels
	if in.Resolution == "fine" {
		maxSolid = MaxSolidVoxelsFine
	}
	if in.maxSolidOverride > 0 {
		maxSolid = in.maxSolidOverride
	}
	progress(0.03, "voxélisation du maillage")
	pl := fem.ChoosePitch(src, maxSolid, MaxGridCells)
	if hmin := eff.ThickMM / 8; hmin > pl.H && eff.ThickMM < 1e8 { // 8 cubes dans l'épaisseur suffisent (voir fem_test) : inutile d'aller plus fin
		pl.H = hmin
	}
	// la plus petite dimension de la boîte englobante tombe sur un nombre entier de cubes : l'épaisseur d'une lame ou
	// d'un bras n'est pas faussée par l'arrondi de la voxélisation (erreur de rigidité ∝ épaisseur³)
	if bs := src.Bounds().Size; true {
		minDim := math.Min(bs.X, math.Min(bs.Y, bs.Z))
		if n := math.Floor(minDim / pl.H); n >= 4 && minDim < 40*pl.H { // par défaut seulement : le pas ne diminue jamais, le plafond de cubes reste respecté
			pl.H = minDim / n
		}
	}
	var warnings []string
	type bcSet struct {
		fixedBC      []fem.BC
		loads        []fem.Load
		supp         []resolvedSupport
		rl           []resolvedLoad
		rests        []restInfo
		inputs       []Marked
		assumptions  []string
		warnings     []string
		totalApplied float64
		soft         map[[2]int]bool // degrés de liberté d'appui posé seul (relâchables par le contact unilatéral)
	}
	resolve := func(grid *fem.Grid) (*bcSet, error) {
		var inputs []Marked
		var assumptions, warnings []string
		bnd := surfaceNodes(grid)

		// supports
		var fixedBC []fem.BC
		fixedSet := map[[2]int]bool{}
		soft := map[[2]int]bool{}
		addBC := func(n, d int) {
			k := [2]int{n, d}
			delete(soft, k)
			if !fixedSet[k] {
				fixedSet[k] = true
				fixedBC = append(fixedBC, fem.BC{Node: n, Dof: d})
			}
		}
		addSoft := func(n, d int) {
			if !fixedSet[[2]int{n, d}] {
				addBC(n, d)
				soft[[2]int{n, d}] = true
			}
		}
		var supp []resolvedSupport
		hasFixed := false
		resting := 0
		var rests []restInfo
		usedSupportZones := map[int]bool{}
		for _, s := range in.Supports {
			var z *Zone
			var ok bool
			if s.Zone == 0 { // appui posé sur le plan de pose détecté automatiquement
				if z, ok = zs.Ground(); !ok {
					return nil, errors.New("appui : aucun plan de pose détecté sur cette pièce (aucune face plate tournée vers le bas) : désigner une zone")
				}
				s.Zone = z.ID
				s.Note = strings.TrimSpace("plan de pose détecté automatiquement. " + s.Note)
			} else if z, ok = zs.ByID(s.Zone); !ok {
				return nil, fmt.Errorf("appui : zone %d inexistante (zones 1 à %d ; relancer plan_strength_test pour la carte)", s.Zone, len(zs.Zones))
			}
			if s.Type == "resting" && !z.Ground {
				if gz, has := zs.Ground(); has {
					warnings = append(warnings, fmt.Sprintf("appui posé sur la zone %d, qui n'est pas le plan de pose détecté (zone %d, Z = %.1f mm) : vérifier que la pièce repose bien sur cette zone", z.ID, gz.ID, gz.GroundZMM))
				}
			}
			usedSupportZones[z.ID] = true
			w := zoneNodes(grid, bnd, zs, z)
			if len(w) == 0 {
				return nil, fmt.Errorf("appui : la zone %d ne touche pas la matière voxélisée", s.Zone)
			}
			nodes := sortedKeys(w)
			supp = append(supp, resolvedSupport{In: s, Anchor: mesh.Vec3{X: z.Centroid[0], Y: z.Centroid[1], Z: z.Centroid[2]}})
			if s.Type == "fixed" {
				hasFixed = true
				for _, n := range nodes {
					addBC(n, 0)
					addBC(n, 1)
					addBC(n, 2)
				}
			} else {
				resting++
				for _, n := range nodes {
					addSoft(n, z.NormalAxis)
				}
				rests = append(rests, restInfo{z, nodes, z.NormalAxis, w})
			}
			inputs = append(inputs, Marked{Name: fmt.Sprintf("appui zone %d", z.ID), Value: s.Type, Source: s.Source})
			if s.Source == SourceAssumed {
				assumptions = append(assumptions, fmt.Sprintf("hypothèse : appui %s sur la zone %d (%s)%s", s.Type, z.ID, z.Label, noteSuffix(s.Note)))
			}
		}
		if !hasFixed && len(rests) > 0 { // supprime les mouvements de corps rigide dans le plan du premier appui posé
			r := rests[0]
			cen := mesh.Vec3{X: r.z.Centroid[0], Y: r.z.Centroid[1], Z: r.z.Centroid[2]}
			cn, far := -1, -1
			cd, fd := math.Inf(1), -1.0
			for _, n := range r.nodes {
				q := grid.NodePos(n)
				d := mesh.Vec3{X: q[0], Y: q[1], Z: q[2]}.Sub(cen).Len()
				if d < cd {
					cn, cd = n, d
				}
			}
			qc := grid.NodePos(cn)
			for _, n := range r.nodes {
				q := grid.NodePos(n)
				d := math.Hypot(q[0]-qc[0], math.Hypot(q[1]-qc[1], q[2]-qc[2]))
				if d > fd {
					far, fd = n, d
				}
			}
			u, v := (r.axis+1)%3, (r.axis+2)%3
			addBC(cn, u)
			addBC(cn, v)
			qf := grid.NodePos(far)
			if math.Abs(qf[u]-qc[u]) >= math.Abs(qf[v]-qc[v]) {
				addBC(far, v)
			} else {
				addBC(far, u)
			}
			if fd < 2*grid.H { // appui trop petit pour bloquer la rotation : un seul point
				warnings = append(warnings, "appui posé très petit : la rotation autour de la normale est bloquée arbitrairement")
			}
		}

		// charges
		var loads []fem.Load
		var rl []resolvedLoad
		totalApplied := 0.0
		for i, l := range in.Loads {
			dir, _ := parseDir(l)
			F := l.ForceN * dyn
			var w map[int]float64
			var anchor mesh.Vec3
			zone := 0
			if l.Zone > 0 {
				z, ok := zs.ByID(l.Zone)
				if !ok {
					return nil, fmt.Errorf("charge %d : zone %d inexistante (zones 1 à %d)", i+1, l.Zone, len(zs.Zones))
				}
				if usedSupportZones[z.ID] {
					warnings = append(warnings, fmt.Sprintf("la zone %d sert à la fois d'appui et de charge : la charge qui y est appliquée est reprise directement par l'appui", z.ID))
				}
				w = zoneNodes(grid, bnd, zs, z)
				anchor = mesh.Vec3{X: z.Centroid[0], Y: z.Centroid[1], Z: z.Centroid[2]}
				zone = z.ID
			} else {
				anchor = mesh.Vec3{X: l.Point[0], Y: l.Point[1], Z: l.Point[2]}
				w = pointNodes(grid, bnd, anchor, l.RadiusMM)
			}
			if len(w) == 0 {
				return nil, fmt.Errorf("charge %d : le lieu ne touche pas la matière voxélisée", i+1)
			}
			for _, n := range sortedKeys(w) {
				loads = append(loads, fem.Load{Node: n, F: [3]float64{dir.X * F * w[n], dir.Y * F * w[n], dir.Z * F * w[n]}})
			}
			totalApplied += F
			rl = append(rl, resolvedLoad{In: l, Dir: dir, N: F, Anchor: anchor, Zone: zone})
			where := fmt.Sprintf("zone %d", l.Zone)
			if l.Zone == 0 {
				where = fmt.Sprintf("point (%.1f ; %.1f ; %.1f)", l.Point[0], l.Point[1], l.Point[2])
			}
			inputs = append(inputs, Marked{Name: fmt.Sprintf("charge %d (%s)", i+1, where), Value: fmt.Sprintf("%.1f N, direction (%.2f ; %.2f ; %.2f)", l.ForceN, dir.X, dir.Y, dir.Z), Source: l.Source})
			if l.Source == SourceAssumed {
				assumptions = append(assumptions, fmt.Sprintf("hypothèse : charge de %.0f N sur %s%s", l.ForceN, where, noteSuffix(l.Note)))
			}
		}

		return &bcSet{fixedBC: fixedBC, loads: loads, supp: supp, rl: rl, rests: rests, inputs: inputs, assumptions: assumptions, warnings: warnings, totalApplied: totalApplied, soft: soft}, nil
	}
	// Un calcul : voxélisation au pas h, appuis et charges, résolution avec contact unilatéral. Si le calcul ne converge
	// pas dans le temps imparti, il est repris UNE fois avec des cubes plus gros (jamais présenté comme fiable s'il
	// échoue encore).
	type attemptOut struct {
		grid     *fem.Grid
		leaky    int
		pl       fem.PitchPlan
		bc       *bcSet
		sol      *fem.Solution
		secs     float64
		contact  *ContactInfo
		passes   int
		warnings []string
	}
	attempt := func(h float64, dl time.Time) (*attemptOut, error) {
		a := &attemptOut{pl: pl}
		a.pl.H = h
		var err error
		if a.grid, a.leaky, err = fem.Voxelize(src, a.pl.H); err != nil {
			return nil, err
		}
		// plafond effectif : le volume du maillage peut être faux (normales inversées, coques recouvrantes) : on compte
		// les cubes réellement pleins et on agrandit le pas jusqu'à respecter le plafond
		var capNote string
		for try := 0; try < 4 && a.grid.SolidCount() > maxSolid*115/100; try++ {
			a.pl.H *= math.Cbrt(float64(a.grid.SolidCount())/float64(maxSolid)) * 1.03
			a.pl.Capped, capNote = true, fmt.Sprintf("plafond de %d cubes pleins", maxSolid)
			if a.grid, a.leaky, err = fem.Voxelize(src, a.pl.H); err != nil {
				return nil, err
			}
		}
		if capNote != "" && a.pl.CapReason == "" {
			a.pl.CapReason = capNote
		}
		if ns := a.grid.SolidCount(); ns < 20 {
			return nil, fmt.Errorf("pièce trop petite ou maillage non fermé (%d cubes pleins) : réparer le maillage (repair_mesh)", ns)
		}
		if a.leaky > 0 {
			a.warnings = append(a.warnings, fmt.Sprintf("%d colonne(s) de voxélisation avec un nombre impair de croisements : maillage possiblement non fermé, résultat à prendre avec prudence (repair_mesh)", a.leaky))
		}
		if a.pl.Capped {
			a.warnings = append(a.warnings, "pas de calcul agrandi : "+a.pl.CapReason)
		}
		if eff.ThickMM < 3*a.grid.H {
			a.warnings = append(a.warnings, fmt.Sprintf("épaisseur caractéristique ≈ %.1f mm pour un cube de %.2f mm : moins de 3 cubes dans l'épaisseur, les contraintes de flexion sont mal résolues (sous-estimées)", eff.ThickMM, a.grid.H))
		}
		if a.bc, err = resolve(a.grid); err != nil {
			return nil, err
		}
		progress(0.08, "résolution par éléments finis")
		t1 := time.Now()
		a.sol, a.contact, a.passes, err = solveWithContact(ctx, a.grid, eff, a.bc.fixedBC, a.bc.loads, a.bc.rests, a.bc.soft, a.bc.totalApplied, dl,
			func(it int, rel float64) {
				f := math.Min(1, math.Log10(1/math.Max(rel, 1e-12))/6)
				progress(0.08+0.8*f, fmt.Sprintf("résolution : itération %d, résidu %.1e", it, rel))
			})
		a.secs = time.Since(t1).Seconds()
		return a, err
	}
	var out *attemptOut
	retried := false
	tryH := pl.H
	for att := 0; att < 2; att++ {
		dl := deadline
		if att == 0 {
			dl = t0.Add(secs(0.6 * budget)) // le reste sert à la reprise
		}
		var err error
		if out, err = attempt(tryH, dl); err != nil {
			return nil, err
		}
		if out.sol.Converged || att == 1 || time.Until(deadline) < secs(0.1*budget) {
			break
		}
		retried = true
		tryH *= 1.45
		progress(0.5, fmt.Sprintf("non convergé : reprise avec des cubes plus gros (%.2f mm)", tryH))
	}
	if retried && out.sol.Converged {
		warnings = append(warnings, fmt.Sprintf("le premier calcul n'a pas convergé dans le temps imparti : repris avec des cubes plus gros (%.2f mm au lieu de %.2f mm), résultat moins détaillé", out.grid.H, pl.H))
	}
	grid, pl, leaky := out.grid, out.pl, out.leaky
	warnings = append(warnings, out.warnings...)
	inputs = append(inputs, out.bc.inputs...)
	assumptions = append(assumptions, out.bc.assumptions...)
	warnings = append(warnings, out.bc.warnings...)
	fixedBC, loads, supp, rl, totalApplied := out.bc.fixedBC, out.bc.loads, out.bc.supp, out.bc.rl, out.bc.totalApplied
	sol, solveSec := out.sol, out.secs
	progress(0.9, "contraintes et verdict")
	if sol.Dropped > 0 {
		warnings = append(warnings, fmt.Sprintf("%d cube(s) non reliés à un appui (pièce séparée ou flottante) ignorés dans le calcul", sol.Dropped))
	}
	if out.contact != nil {
		c := out.contact
		if c.ReleasedNodes > 0 {
			warnings = append(warnings, fmt.Sprintf("appui posé : le premier calcul (appui supposé retenir dans les deux sens) tirait sur la pièce à hauteur de %.0f %% de la réaction, ce qui est irréaliste pour une pièce simplement posée ; contact unilatéral appliqué (%d point(s) d'appui relâchés en %d reprise(s)) : %.0f %% de la surface d'appui porte réellement, réaction en traction restante %.0f %%", c.TensionBeforePct, c.ReleasedNodes, c.Passes, c.ContactAreaPct, c.TensionAfterPct))
		}
		if c.TippingSuspected {
			warnings = append(warnings, "appui posé : moins du quart de la surface d'appui reste en contact, la pièce a tendance à basculer ou à se décoller : la fixer (vis, colle) ou élargir/reculer la base")
		}
		if c.TensionAfterPct > 5 {
			warnings = append(warnings, fmt.Sprintf("appui posé : %.0f %% de la réaction tirerait encore sur la pièce après les reprises de contact : appui trop favorable, vérifier la fixation ou le type d'appui", c.TensionAfterPct))
		}
	}
	rests := out.bc.rests
	_ = rests
	_ = leaky

	nn := len(sol.Nodes)
	// zone de singularité : nœuds voisins des appuis et des charges (concentrations artificielles)
	excl := make([]bool, nn)
	mark := func(full int) {
		ix, iy, iz := grid.NodeIJK(full)
		for dz := -1; dz <= 1; dz++ {
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					x, y, z := ix+dx, iy+dy, iz+dz
					if x < 0 || y < 0 || z < 0 || x > grid.NX || y > grid.NY || z > grid.NZ {
						continue
					}
					if c := sol.Compact(grid.NodeIndex(x, y, z)); c >= 0 {
						excl[c] = true
					}
				}
			}
		}
	}
	for _, b := range fixedBC {
		mark(b.Node)
	}
	for _, l := range loads {
		mark(l.Node)
	}
	axis := axisIndex(in.LayerOrientation)
	util := make([]float64, nn)
	vm := make([]float64, nn)
	utilAxis := func(s []float64, a int, e Effective) (u float64, crit string) {
		v := fem.VonMises(s)
		u, crit = v/e.SigmaXY, "dans_le_plan"
		var tau float64
		switch a {
		case 0:
			tau = math.Hypot(s[3], s[5])
		case 1:
			tau = math.Hypot(s[3], s[4])
		default:
			tau = math.Hypot(s[4], s[5])
		}
		if un := math.Max(s[a], 0) / e.SigmaZ; un > u {
			u, crit = un, "entre_couches_traction"
		}
		if ut := tau / e.TauZ; ut > u {
			u, crit = ut, "entre_couches_cisaillement"
		}
		return
	}
	best, bestAll := -1, -1
	crit := ""
	altBest := map[string]float64{"x": 0, "y": 0, "z": 0}
	for c := 0; c < nn; c++ {
		s := sol.Stress[c*6 : c*6+6]
		vm[c] = fem.VonMises(s)
		u, cr := utilAxis(s, axis, eff)
		util[c] = u
		if bestAll < 0 || u > util[bestAll] {
			bestAll = c
		}
		if excl[c] {
			continue
		}
		if best < 0 || u > util[best] {
			best, crit = c, cr
		}
		for a, name := range [3]string{"x", "y", "z"} {
			if ua, _ := utilAxis(s, a, eff); ua > altBest[name] {
				altBest[name] = ua
			}
		}
	}
	if best < 0 { // pièce minuscule : tout est « singulier »
		best = bestAll
		warnings = append(warnings, "la pièce est trop petite pour écarter les zones d'appui et de charge : le maximum inclut les concentrations locales")
		_, crit = utilAxis(sol.Stress[best*6:best*6+6], axis, eff)
	}
	maxVM, bestVM := 0.0, best
	for c := 0; c < nn; c++ {
		if (!excl[c] || best == bestAll) && vm[c] >= maxVM {
			maxVM, bestVM = vm[c], c
		}
	}
	utilMax := util[best]
	if utilMax <= 0 {
		utilMax = 1e-9
	}
	sf := 1 / utilMax
	critPos := grid.NodePos(int(sol.Nodes[best]))
	res := &Result{ModelID: in.ModelID, VersionID: in.VersionID, SafetyAchieved: sf, SafetyRequired: in.SafetyFactor,
		MaxVonMisesMPa: maxVM, MaxVMPoint: grid.NodePos(int(sol.Nodes[bestVM])), CriticalStressMPa: vm[best], MaxUtilization: utilMax, LimitingCriteria: crit,
		AllowableXYMPa: eff.SigmaXY, AllowableZMPa: eff.SigmaZ, CriticalPoint: critPos, Material: mat, Effective: eff,
		Orientations: map[string]float64{}, LayerAxisName: in.LayerOrientation}
	for k, u := range altBest {
		if u > 0 {
			res.Orientations[k] = 1 / u
		}
	}
	// zone critique : la plus proche (boîte englobante)
	bz, bd := 0, math.Inf(1)
	for i := range zs.Zones {
		z := &zs.Zones[i]
		d := 0.0
		for k := 0; k < 3; k++ {
			if critPos[k] < z.BBoxMin[k] {
				d += (z.BBoxMin[k] - critPos[k]) * (z.BBoxMin[k] - critPos[k])
			} else if critPos[k] > z.BBoxMax[k] {
				d += (critPos[k] - z.BBoxMax[k]) * (critPos[k] - z.BBoxMax[k])
			}
		}
		if d < bd {
			bz, bd = z.ID, d
		}
	}
	bb := src.Bounds()
	diag := bb.Size.Len()
	if math.Sqrt(bd) < 0.3*diag {
		res.CriticalZone = bz
		if bd == 0 {
			res.CriticalNote = fmt.Sprintf("dans la zone %d, au point (%.1f ; %.1f ; %.1f) mm", bz, critPos[0], critPos[1], critPos[2])
		} else {
			res.CriticalNote = fmt.Sprintf("à %.1f mm de la zone %d, au point (%.1f ; %.1f ; %.1f) mm", math.Sqrt(bd), bz, critPos[0], critPos[1], critPos[2])
		}
	} else {
		res.CriticalNote = fmt.Sprintf("au point (%.1f ; %.1f ; %.1f) mm", critPos[0], critPos[1], critPos[2])
	}
	res.LocalThickMM = localThickness(grid, critPos)
	// déplacement maximal
	maxU, maxN := 0.0, 0
	for c := 0; c < nn; c++ {
		u := math.Sqrt(sol.U[c*3]*sol.U[c*3] + sol.U[c*3+1]*sol.U[c*3+1] + sol.U[c*3+2]*sol.U[c*3+2])
		if u > maxU {
			maxU, maxN = u, c
		}
	}
	res.MaxDisplacementMM = maxU
	res.MaxDispPoint = grid.NodePos(int(sol.Nodes[maxN]))
	// équilibre
	rx, ry, rz := 0.0, 0.0, 0.0
	for c := 0; c < nn; c++ {
		rx += sol.Reaction[c*3]
		ry += sol.Reaction[c*3+1]
		rz += sol.Reaction[c*3+2]
	}
	res.ReactionN = math.Sqrt(rx*rx + ry*ry + rz*rz)
	res.AppliedN = totalApplied
	// écart d'équilibre : les réactions doivent exactement s'opposer aux charges (R + F = 0)
	var fx, fy, fz float64
	for _, l := range loads {
		fx, fy, fz = fx+l.F[0], fy+l.F[1], fz+l.F[2]
	}
	res.EquilibriumGapN = math.Sqrt((rx+fx)*(rx+fx) + (ry+fy)*(ry+fy) + (rz+fz)*(rz+fz))
	if totalApplied > 0 {
		res.EquilibriumGapPct = 100 * res.EquilibriumGapN / totalApplied
	}
	res.Contact = out.contact
	// fiabilité : convergé, ou résidu faible ET équilibre quasi parfait
	notConv := !sol.Converged && !(sol.Residual < 1e-4 && res.EquilibriumGapPct < 2)
	stop := "converged"
	switch {
	case sol.Converged:
	case sol.TimedOut:
		stop = "time_limit"
	case sol.Stagnated:
		stop = "stagnation"
	default:
		stop = "max_iterations"
	}
	if !sol.Converged {
		warnings = append(warnings, fmt.Sprintf("le solveur n'a pas atteint la précision visée (résidu %.1e après %d itérations, arrêt : %s) ; équilibre des réactions : %.1f N de réaction pour %.1f N appliqués, écart %.0f %%", sol.Residual, sol.Iterations, stopText(stop), res.ReactionN, totalApplied, res.EquilibriumGapPct))
	} else if res.EquilibriumGapPct > 5 {
		warnings = append(warnings, fmt.Sprintf("équilibre des réactions imparfait (écart %.0f %%) : résultat à prendre avec prudence", res.EquilibriumGapPct))
	}

	// verdict
	res.Mesh = MeshInfo{VoxelMM: grid.H, Elements: sol.Elements, Nodes: nn, GridCells: grid.NX * grid.NY * grid.NZ, Capped: pl.Capped, CapReason: pl.CapReason,
		SourceTris: len(src.T), VolumeMM3: vol, AreaMM2: area, BBoxMM: [3]float64{bb.Size.X, bb.Size.Y, bb.Size.Z}, LeakyColumn: leaky}
	res.Solver = Solver{Iterations: sol.Iterations, Residual: sol.Residual, Converged: sol.Converged, Seconds: solveSec, StopReason: stop,
		TimedOut: sol.TimedOut, Precond: sol.Precond, BudgetSec: budget, Retried: retried, Passes: out.passes}
	res.NotConverged = notConv
	res.Inputs = inputs
	for _, m := range inputs {
		if m.Source != SourceUser {
			res.BasedOnAssumed = true
		}
	}
	res.Assumptions = assumptions
	res.Warnings = warnings
	where := res.CriticalNote
	switch {
	case notConv:
		res.Verdict = "non_converge"
		res.VerdictTxt = fmt.Sprintf("NON CONVERGÉ : le calcul n'a pas abouti (arrêt : %s, résidu %.1e après %d itérations) ; les réactions aux appuis (%.1f N) ne reprennent pas la charge appliquée (%.1f N), écart d'équilibre %.0f %% (%.1f N). CE RÉSULTAT N'EST PAS FIABLE : aucun verdict « tient » ou « casse » ne peut en être tiré (valeur purement indicative : marge ×%.2f, contrainte %.1f MPa, déplacement %.2f mm)",
			stopText(stop), sol.Residual, sol.Iterations, res.ReactionN, totalApplied, res.EquilibriumGapPct, res.EquilibriumGapN, sf, maxVM, maxU)
	case sf >= in.SafetyFactor:
		res.Verdict = "ok"
		res.VerdictTxt = fmt.Sprintf("tient avec une marge de ×%.1f (visé ×%.1f) ; point le plus sollicité %s (%s), déplacement max %.2f mm", sf, in.SafetyFactor, where, fmtMPa(res.CriticalStressMPa), maxU)
	case sf >= 1:
		res.Verdict = "a_surveiller"
		res.VerdictTxt = fmt.Sprintf("tient mais avec une marge de seulement ×%.1f, sous le ×%.1f visé : à renforcer ou à valider par un essai ; point faible %s", sf, in.SafetyFactor, where)
	default:
		res.Verdict = "risque"
		res.VerdictTxt = fmt.Sprintf("risque de casse : la contrainte estimée dépasse la limite admissible (marge ×%.2f) %s", sf, where)
	}
	if res.BasedOnAssumed {
		res.VerdictTxt += " — RÉSULTAT BASÉ SUR DES HYPOTHÈSES : " + strings.Join(shortAssumptions(assumptions), " ; ")
	}
	if notConv {
		res.Recommendations = []string{
			"Ne pas utiliser ce résultat. Relancer avec resolution=standard (cubes plus gros), ou un temps plus long (max_seconds), ou en simplifiant les appuis (un appui fixed sur une grande face plane converge bien mieux qu'un appui posé étroit).",
			"Vérifier que les appuis empêchent tout mouvement de la pièce et que la charge est appliquée sur la matière (zone ou point valides).",
		}
	} else {
		res.Recommendations = recommend(res, in, mat, eff, vol, area, permanent, bb)
	}
	res.Measured = []string{
		fmt.Sprintf("géométrie du maillage : %.1f × %.1f × %.1f mm, volume %.0f mm³, aire %.0f mm²", bb.Size.X, bb.Size.Y, bb.Size.Z, vol, area),
	}
	for _, l := range in.Loads {
		if l.Source == SourceUser {
			res.Measured = append(res.Measured, fmt.Sprintf("charge donnée par l'humain : %.1f N", l.ForceN))
		}
	}
	res.Calculated = []string{
		fmt.Sprintf("champ de contraintes et de déplacements par éléments finis hexaédriques (%d cubes de %.2f mm, %d itérations, résidu %.0e)", sol.Elements, grid.H, sol.Iterations, sol.Residual),
		fmt.Sprintf("équilibre : somme des réactions %.1f N pour %.1f N appliqués (écart %.1f N, %.1f %%)", res.ReactionN, totalApplied, res.EquilibriumGapN, res.EquilibriumGapPct),
	}
	res.Estimated = []string{
		"propriétés de la matière (table « estimation », ±20 % ou plus)",
		fmt.Sprintf("réduction par parois et remplissage (fraction de matière ≈ %.0f %%)", 100*eff.SolidFrac),
		"résistance entre couches (direction d'impression la plus faible)",
	}
	if permanent {
		res.Estimated = append(res.Estimated, fmt.Sprintf("fluage sous charge permanente (résistances × %.2g)", mat.CreepFactor))
	}
	if in.LoadKind == "impact" {
		res.Estimated = append(res.Estimated, "effet d'un choc : charge doublée (lâcher sans amortissement)")
	}
	if res.BasedOnAssumed {
		res.Estimated = append(res.Estimated, "valeurs d'entrée supposées (hypothèses ci-dessus)")
	}
	res.Disclaimer = Disclaimer
	res.TotalSeconds = time.Since(t0).Seconds()

	an := &Analysis{Result: res, Grid: grid, Sol: sol, Zones: zs, VM: vm, Util: util, Loads: rl, Supp: supp, Mesh: src}
	an.Scale = math.Max(maxVM, 1e-9)
	return an, nil
}

func stopText(r string) string {
	switch r {
	case "time_limit":
		return "temps imparti épuisé"
	case "stagnation":
		return "le résidu n'avance plus"
	case "max_iterations":
		return "nombre maximal d'itérations atteint"
	}
	return "convergé"
}

func noteSuffix(n string) string {
	if strings.TrimSpace(n) == "" {
		return ""
	}
	return " : " + n
}

func shortAssumptions(a []string) []string {
	out := make([]string, 0, len(a))
	for _, s := range a {
		out = append(out, strings.TrimPrefix(s, "hypothèse : "))
	}
	if len(out) > 6 {
		out = append(out[:6], fmt.Sprintf("… (+%d)", len(out)-6))
	}
	return out
}

// localThickness : plus petite épaisseur de matière (le long des trois axes) autour d'un point.
func localThickness(g *fem.Grid, p [3]float64) float64 {
	ix := int(math.Floor((p[0] - g.Origin[0]) / g.H))
	iy := int(math.Floor((p[1] - g.Origin[1]) / g.H))
	iz := int(math.Floor((p[2] - g.Origin[2]) / g.H))
	in := func(x, y, z int) bool {
		return x >= 0 && y >= 0 && z >= 0 && x < g.NX && y < g.NY && z < g.NZ && g.Solid[g.Cell(x, y, z)]
	}
	// cube plein voisin du nœud
	cx, cy, cz := ix, iy, iz
	found := false
	for dz := 0; dz >= -1 && !found; dz-- {
		for dy := 0; dy >= -1 && !found; dy-- {
			for dx := 0; dx >= -1 && !found; dx-- {
				if in(ix+dx, iy+dy, iz+dz) {
					cx, cy, cz, found = ix+dx, iy+dy, iz+dz, true
				}
			}
		}
	}
	if !found {
		return 0
	}
	run := func(dx, dy, dz int) int {
		n := 1
		for k := 1; k < 4000; k++ {
			if !in(cx+dx*k, cy+dy*k, cz+dz*k) {
				break
			}
			n++
		}
		for k := 1; k < 4000; k++ {
			if !in(cx-dx*k, cy-dy*k, cz-dz*k) {
				break
			}
			n++
		}
		return n
	}
	t := math.Min(float64(run(1, 0, 0)), math.Min(float64(run(0, 1, 0)), float64(run(0, 0, 1))))
	return t * g.H
}

func recommend(r *Result, in AssessInput, mat Material, eff Effective, vol, area float64, permanent bool, bb mesh.BBox) []string {
	var out []string
	weak := r.Verdict != "ok"
	if weak {
		switch r.LimitingCriteria {
		case "entre_couches_traction", "entre_couches_cisaillement":
			bestK, bestSF := "", 0.0
			for k, v := range r.Orientations {
				if v > bestSF || (v == bestSF && k < bestK) {
					bestK, bestSF = k, v
				}
			}
			if bestK != "" && bestK != in.LayerOrientation && bestSF > 1.15*r.SafetyAchieved {
				out = append(out, fmt.Sprintf("Imprimer la pièce autrement : l'effort tire entre les couches (le point faible de l'impression). Avec les couches perpendiculaires à l'axe %s, la marge passerait de ×%.1f à ×%.1f (layer_orientation=%q ; à poser avec lay_flat / orientation de la découpe).", bestK, r.SafetyAchieved, bestSF, bestK))
			} else {
				out = append(out, "Le point faible est la liaison entre couches : orienter la pièce pour que l'effort principal suive les couches, ou renforcer la section.")
			}
		}
		k := math.Sqrt(in.SafetyFactor / math.Max(r.SafetyAchieved, 1e-9)) // flexion : σ ∝ 1/e²
		if r.LocalThickMM > 0 {
			target := math.Ceil(r.LocalThickMM*k*2) / 2
			out = append(out, fmt.Sprintf("Épaissir la zone critique : épaisseur locale ≈ %.1f mm, viser ≈ %.1f mm (en flexion la contrainte varie en 1/épaisseur² ; en traction pure, il faut ×%.1f de section).", r.LocalThickMM, target, in.SafetyFactor/math.Max(r.SafetyAchieved, 1e-9)))
		}
		out = append(out, "Ajouter un congé (rayon de raccordement ≥ 3 mm) aux angles vifs près du point critique : il réduit les concentrations de contrainte que le maillage en cubes ne voit qu'en partie.")
		if eff.SolidFrac < 0.9 {
			e2 := EffectiveProps(mat, 40, 4, vol, area, permanent)
			out = append(out, fmt.Sprintf("Monter à 4 parois et 40 %% de remplissage : résistance estimée ×%.2f par rapport aux réglages actuels.", e2.FStrength/eff.FStrength))
		}
		var bestMat Material
		bestV := mat.SigmaXY * mat.CreepFactor
		for _, m := range Materials {
			if m.Key == "TPU" {
				continue
			}
			if v := m.SigmaXY * m.CreepFactor; v > bestV*1.1 {
				bestMat, bestV = m, v
			}
		}
		if bestMat.Key != "" && bestMat.Key != mat.Key {
			out = append(out, fmt.Sprintf("Changer de matière (estimation) : %s tient environ ×%.1f mieux que %s sous charge permanente.", bestMat.Label, bestV/(mat.SigmaXY*mat.CreepFactor), mat.Label))
		}
	} else {
		if r.SafetyAchieved > 4*in.SafetyFactor {
			out = append(out, fmt.Sprintf("Marge très large (×%.1f) : on pourrait alléger (moins de matière, moins de remplissage) sans risque apparent, après essai.", r.SafetyAchieved))
		}
	}
	if mat.Key == "PLA" {
		out = append(out, "PLA : se ramollit vers 55 °C et flue sous charge permanente ; éviter un usage près d'une source de chaleur ou au soleil.")
	}
	if r.MaxDisplacementMM > 0.02*bb.Size.Len() {
		out = append(out, fmt.Sprintf("Déplacement maximal %.1f mm, sensible à l'œil : ajouter de la rigidité (épaisseur, nervures) même si la résistance suffit.", r.MaxDisplacementMM))
	}
	out = append(out, "Avant usage réel : imprimer et charger la pièce à 1,5 à 2 fois la charge prévue, en surveillant la déformation.")
	return out
}
