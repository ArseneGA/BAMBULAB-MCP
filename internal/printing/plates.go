package printing

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// Plusieurs pièces sur un plateau (chantier B) : plan_plate construit et range le plan (rapport « tient / ne tient
// pas » AVANT toute découpe), execute_plate_slice le découpe — une impression (PrintJob) par plaque, chacune liée à son
// artefact et à son numéro de plaque comme n'importe quelle impression. Les réglages PAR OBJET sont écrits dans
// Metadata/model_settings.config du 3MF (voir mesh.WriteBambuProject) et relus par Bambu Studio : prouvé par
// TestRealPerObjectInfill / TestRealPerObjectKeys (internal/slicer).

const (
	maxPlanObjects = 200
	maxPlanBlob    = 64 << 20 // un modèle de plus de 64 Mio n'est pas lu pour un plan
)

// maxPlanTriangle : plafond des triangles cumulés d'un plan (variable : abaissé par les tests).
var maxPlanTriangle = 3_000_000

const (
	defaultMaxPlate = 16
)

// PlateItemInput : une pièce (ou N exemplaires) à poser sur le plateau.
type PlateItemInput struct {
	ModelID     string            `json:"model_id" jsonschema:"modèle du catalogue"`
	VersionID   string            `json:"version_id,omitempty" jsonschema:"version du modèle (défaut : la plus récente)"`
	Quantity    int               `json:"quantity,omitempty" jsonschema:"nombre d'exemplaires (défaut 1, au plus 100)"`
	Label       string            `json:"label,omitempty" jsonschema:"nom affiché (défaut : nom du modèle)"`
	Settings    map[string]any    `json:"settings,omitempty" jsonschema:"réglages PROPRES à cette pièce, relus par Bambu Studio : sparse_infill_density (0-100 ou \"15%\"), sparse_infill_pattern, wall_loops, layer_height, top_shell_layers, bottom_shell_layers, brim_type, brim_width, enable_support"`
	LayerRanges []LayerRangeInput `json:"layer_ranges,omitempty" jsonschema:"réglages par plage de hauteur (Z, mm) de cette pièce"`
}

// LayerRangeInput : une plage de hauteurs avec ses réglages.
type LayerRangeInput struct {
	FromMM   float64        `json:"from_mm"`
	ToMM     float64        `json:"to_mm"`
	Settings map[string]any `json:"settings" jsonschema:"mêmes clés que settings (layer_height surtout)"`
}

// PlanPlateInput est l'entrée de plan_plate.
type PlanPlateInput struct {
	PrinterID       string           `json:"printer_id" jsonschema:"imprimante cible (dimensions réelles du plateau et zones interdites)"`
	Items           []PlateItemInput `json:"items" jsonschema:"pièces à poser"`
	Name            string           `json:"name,omitempty" jsonschema:"nom du plan (sert de base au nom des fichiers)"`
	ProfileRevision string           `json:"profile_revision,omitempty" jsonschema:"révision de profil de découpe (comme execute_headless_slice) ; défaut : profils par défaut de la machine"`
	Process         string           `json:"process,omitempty"`
	Filaments       []string         `json:"filaments,omitempty"`
	Arrangement     string           `json:"arrangement,omitempty" jsonschema:"studio (Bambu Studio --arrange 1, par défaut si installé) | estimate (rectangles englobants, sans Studio)"`
	GroupBySettings bool             `json:"group_by_settings,omitempty" jsonschema:"ne jamais mélanger deux jeux de réglages différents sur une même plaque"`
	SpacingMM       float64          `json:"spacing_mm,omitempty" jsonschema:"marge entre pièces et au bord pour le rangement estimé (défaut 8)"`
	MaxPlates       int              `json:"max_plates,omitempty" jsonschema:"nombre maximal de plaques (défaut 16)"`
}

// PlanRange est une plage de hauteurs normalisée.
type PlanRange struct {
	MinZ     float64           `json:"from_mm"`
	MaxZ     float64           `json:"to_mm"`
	Settings map[string]string `json:"settings"`
}

// PlanObject est une pièce placée.
type PlanObject struct {
	Index      int               `json:"index"`
	Name       string            `json:"name"`
	ModelID    string            `json:"model_id"`
	VersionID  string            `json:"version_id,omitempty"`
	Copy       int               `json:"copy"`
	SourceSHA  string            `json:"source_sha256"`
	SourceName string            `json:"source_name"`
	Plate      int               `json:"plate"`
	X          float64           `json:"x_mm" jsonschema:"coin minimal de la boîte englobante, repère de la plaque"`
	Y          float64           `json:"y_mm"`
	W          float64           `json:"width_mm"`
	D          float64           `json:"depth_mm"`
	H          float64           `json:"height_mm"`
	Rotated    bool              `json:"rotated_90,omitempty"`
	Settings   map[string]string `json:"settings,omitempty"`
	Ranges     []PlanRange       `json:"layer_ranges,omitempty"`
	Fits       bool              `json:"fits"`
	Problem    string            `json:"problem,omitempty"`
}

// PlanPlate résume l'occupation d'une plaque.
type PlanPlate struct {
	Index        int      `json:"index"`
	Objects      []string `json:"objects"`
	FootprintMM2 float64  `json:"footprint_mm2"`
	UsagePercent float64  `json:"usage_percent" jsonschema:"empreinte des pièces / surface du plateau"`
	MaxHeightMM  float64  `json:"max_height_mm"`
}

// PlanJob lie une plaque à son impression.
type PlanJob struct {
	Plate      int    `json:"plate"`
	PrintJobID string `json:"print_job_id"`
}

// PlatePlan est le plan persisté.
type PlatePlan struct {
	ID              string       `json:"id"`
	Name            string       `json:"name"`
	PrinterID       string       `json:"printer_id"`
	PrinterModel    string       `json:"printer_model"`
	NozzleMM        float64      `json:"nozzle_mm"`
	Bed             PlateBed     `json:"bed"`
	ProfileRevision string       `json:"profile_revision,omitempty"`
	Process         string       `json:"process,omitempty"`
	Filaments       []string     `json:"filaments,omitempty"`
	Arrangement     string       `json:"arrangement" jsonschema:"studio | estimate"`
	SpacingMM       float64      `json:"spacing_mm"`
	GroupBySettings bool         `json:"group_by_settings,omitempty"`
	Objects         []PlanObject `json:"objects"`
	Plates          []PlanPlate  `json:"plates"`
	Fits            bool         `json:"fits" jsonschema:"vrai si chaque pièce tient et si le nombre de plaques respecte max_plates"`
	Report          []string     `json:"report" jsonschema:"rapport lisible « tient / ne tient pas » établi AVANT la découpe"`
	Warnings        []string     `json:"warnings,omitempty"`
	Jobs            []PlanJob    `json:"jobs,omitempty"`
	CreatedAt       time.Time    `json:"created_at"`
}

// PlateSliceSpec fige le projet à découper : de quoi reconstruire le 3MF multi-objets (positions comprises).
type PlateSliceSpec struct {
	PlanID  string          `json:"plan_id"`
	Plates  int             `json:"plates"`
	Objects []PlateSliceObj `json:"objects"`
}

// PlateSliceObj est un objet du projet figé.
type PlateSliceObj struct {
	Name      string            `json:"name"`
	SourceSHA string            `json:"source_sha256"`
	SourceNm  string            `json:"source_name"`
	Plate     int               `json:"plate"`
	X         float64           `json:"x_mm"`
	Y         float64           `json:"y_mm"`
	Rotated   bool              `json:"rotated_90,omitempty"`
	Settings  map[string]string `json:"settings,omitempty"`
	Ranges    []PlanRange       `json:"layer_ranges,omitempty"`
}

// ---- réglages par objet ----

// ObjectSettingKeys : clés acceptées par pièce (toutes vérifiées contre un vrai Bambu Studio 02.08 : l'effet
// est mesuré dans le G-code par TestRealPerObjectKeys).
var ObjectSettingKeys = []string{"sparse_infill_density", "sparse_infill_pattern", "wall_loops", "layer_height",
	"top_shell_layers", "bottom_shell_layers", "brim_type", "brim_width", "enable_support"}

var infillPatterns = map[string]bool{"concentric": true, "zig-zag": true, "grid": true, "line": true, "cubic": true, "triangles": true,
	"tri-hexagon": true, "gyroid": true, "honeycomb": true, "adaptivecubic": true, "alignedrectilinear": true, "3dhoneycomb": true,
	"hilbertcurve": true, "archimedeanchords": true, "octagramspiral": true, "supportcubic": true, "lightning": true, "crosshatch": true}

var brimTypes = map[string]bool{"auto_brim": true, "brim_ears": true, "no_brim": true, "outer_only": true, "inner_only": true, "outer_and_inner": true}

// num convertit une valeur de réglage en nombre FINI : NaN et ±Inf (y compris « NaN », « Inf » en chaîne) sont refusés.
func num(v any) (float64, bool) {
	var f float64
	switch t := v.(type) {
	case float64:
		f = t
	case int:
		f = float64(t)
	case json.Number:
		var err error
		if f, err = t.Float64(); err != nil {
			return 0, false
		}
	case string:
		var err error
		if f, err = strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(t), "%")), 64); err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

func fnum(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// normalizeSettings valide et convertit en valeurs de configuration Studio (chaînes).
func normalizeSettings(in map[string]any, allowed []string) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	ok := map[string]bool{}
	for _, k := range allowed {
		ok[k] = true
	}
	for k, v := range in {
		if !ok[k] {
			return nil, invalidf("réglage par pièce %q inconnu ; clés acceptées : %s", k, strings.Join(allowed, ", "))
		}
		switch k {
		case "sparse_infill_density":
			f, good := num(v)
			if !good || f < 0 || f > 100 {
				return nil, invalidf("sparse_infill_density : pourcentage 0 à 100 attendu, reçu %v", v)
			}
			out[k] = fnum(f) + "%"
		case "wall_loops", "top_shell_layers", "bottom_shell_layers":
			f, good := num(v)
			if !good || f != math.Trunc(f) || f < 0 || f > 50 || (k == "wall_loops" && f < 1) {
				return nil, invalidf("%s : entier 0 à 50 attendu (parois : au moins 1), reçu %v", k, v)
			}
			out[k] = fnum(f)
		case "layer_height":
			f, good := num(v)
			if !good || f < 0.04 || f > 0.8 {
				return nil, invalidf("layer_height : 0,04 à 0,8 mm attendu, reçu %v", v)
			}
			out[k] = fnum(f)
		case "brim_width":
			f, good := num(v)
			if !good || f < 0 || f > 30 {
				return nil, invalidf("brim_width : 0 à 30 mm attendu, reçu %v", v)
			}
			out[k] = fnum(f)
		case "sparse_infill_pattern":
			s, _ := v.(string)
			if !infillPatterns[s] {
				return nil, invalidf("sparse_infill_pattern %q inconnu", s)
			}
			out[k] = s
		case "brim_type":
			s, _ := v.(string)
			if !brimTypes[s] {
				return nil, invalidf("brim_type %q inconnu (no_brim, outer_only, inner_only, outer_and_inner, auto_brim, brim_ears)", s)
			}
			out[k] = s
		case "enable_support":
			switch t := v.(type) {
			case bool:
				out[k] = map[bool]string{true: "1", false: "0"}[t]
			case string:
				if t != "0" && t != "1" {
					return nil, invalidf("enable_support : booléen attendu")
				}
				out[k] = t
			default:
				return nil, invalidf("enable_support : booléen attendu")
			}
		}
	}
	return out, nil
}

func settingsSig(m map[string]string, rs []PlanRange) string {
	b, _ := json.Marshal(struct {
		S map[string]string
		R []PlanRange
	}{m, rs})
	return string(b)
}

// ---- plateau de l'imprimante ----

func (s *Service) plateBed(rec PrinterRecord, model contracts.PrinterModel) (PlateBed, error) {
	if sl, err := s.slicerInst(); err == nil {
		if p, err := sl.Profiles().Printer(model.Name); err == nil {
			best := -1
			for i, v := range p.Variants {
				if math.Abs(v.Nozzle-rec.NozzleMM) < 1e-6 {
					best = i
					break
				}
			}
			if best < 0 && len(p.Variants) > 0 {
				best = 0
			}
			if best >= 0 {
				v := p.Variants[best]
				if v.BedX > 0 && v.BedY > 0 {
					bed := PlateBed{X: v.BedX, Y: v.BedY, Z: v.PrintableZ, Source: "profil machine " + v.Profile}
					for _, poly := range v.ExcludeAreas {
						x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
						for _, pt := range poly {
							x0, y0, x1, y1 = math.Min(x0, pt.X), math.Min(y0, pt.Y), math.Max(x1, pt.X), math.Max(y1, pt.Y)
						}
						bed.Exclusions = append(bed.Exclusions, [4]float64{x0, y0, x1, y1})
					}
					return bed, nil
				}
			}
		}
	}
	if bv := model.BuildVolume; bv != nil && bv.X > 0 && bv.Y > 0 {
		return PlateBed{X: bv.X, Y: bv.Y, Z: bv.Z, Source: "volume du modèle d'imprimante " + model.Name}, nil
	}
	return PlateBed{}, fmt.Errorf("%w : dimensions du plateau de %s inconnues (profils Bambu Studio absents)", ErrUnavailable, model.Name)
}

// ---- construction du plan ----

// srcMesh ne garde que les bornes et le nombre de triangles : le maillage lui-même est relâché dès le chargement.
type srcMesh struct {
	bb        mesh.BBox
	tris      int
	sha, nm   string // empreinte et nom du fichier source
	versionID string
	name      string // nom du modèle
}

// loadSource charge un modèle source ; budget cumule les triangles de tout ce qui est chargé pour le plan et le refuse
// DÈS le dépassement (avant de charger la source suivante).
func (s *Service) loadSource(ctx context.Context, modelID, versionID string, budget *int) (*srcMesh, error) {
	ver, src, err := s.sourceFor(ctx, modelID, versionID)
	if err != nil {
		return nil, err
	}
	m, err := s.meshOfBlob(src.SHA256, src.Name)
	if err != nil {
		return nil, err
	}
	if *budget += len(m.T); *budget > maxPlanTriangle {
		return nil, invalidf("plan trop lourd (plus de %d triangles cumulés) : réduire les quantités ou décimer les modèles", maxPlanTriangle)
	}
	name := modelID
	if mm, e := s.d.Store.GetModel(ctx, modelID); e == nil {
		name = mm.Name
	}
	return &srcMesh{bb: m.Bounds(), tris: len(m.T), sha: src.SHA256, nm: src.Name, versionID: ver.ID, name: name}, nil
}

func (s *Service) meshOfBlob(sha, name string) (*mesh.Mesh, error) {
	// la taille est contrôlée AVANT toute lecture ou analyse
	if n, err := s.d.Blobs.Size(sha); err == nil && n > maxPlanBlob {
		return nil, invalidf("modèle %s trop volumineux pour un plan (%d Mio, maximum %d Mio)", name, n>>20, maxPlanBlob>>20)
	}
	f, err := s.d.Blobs.Open(sha)
	if err != nil {
		return nil, err
	}
	data, err := readAll(f, maxPlanBlob)
	f.Close()
	if err != nil {
		return nil, err
	}
	md, err := mesh.Parse(name, data)
	if err != nil {
		return nil, invalidf("modèle illisible (%s) : %v", name, err)
	}
	return md.Combined(), nil
}

var labelClean = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ", "<", "", ">", "", "&", "+", "\"", "'")

// PlanPlate construit, range et enregistre le plan. Rien n'est découpé ni envoyé.
func (s *Service) PlanPlate(ctx context.Context, in PlanPlateInput) (*PlatePlan, error) {
	if in.PrinterID == "" || len(in.Items) == 0 {
		return nil, invalidf("printer_id et au moins une pièce requis")
	}
	if in.Arrangement != "" && in.Arrangement != "studio" && in.Arrangement != "estimate" {
		return nil, invalidf("arrangement : studio ou estimate")
	}
	if in.SpacingMM == 0 {
		in.SpacingMM = 8
	}
	if in.SpacingMM < 2 || in.SpacingMM > 40 {
		return nil, invalidf("spacing_mm : 2 à 40")
	}
	if in.MaxPlates == 0 {
		in.MaxPlates = defaultMaxPlate
	}
	if in.MaxPlates < 1 || in.MaxPlates > 64 {
		return nil, invalidf("max_plates : 1 à 64")
	}
	rec, err := s.GetPrinterRecord(ctx, in.PrinterID)
	if err != nil {
		return nil, err
	}
	model, err := s.GetPrinterModel(ctx, rec.ModelID)
	if err != nil {
		return nil, err
	}
	bed, err := s.plateBed(rec, model)
	if err != nil {
		return nil, err
	}
	plan := &PlatePlan{ID: contracts.NewID("plan"), Name: strings.TrimSpace(in.Name), PrinterID: in.PrinterID, PrinterModel: model.Name,
		NozzleMM: rec.NozzleMM, Bed: bed, ProfileRevision: in.ProfileRevision, Process: in.Process, Filaments: in.Filaments,
		SpacingMM: in.SpacingMM, GroupBySettings: in.GroupBySettings, CreatedAt: s.now()}
	if plan.ProfileRevision != "" {
		if s.d.Goals == nil {
			return nil, fmt.Errorf("%w : moteur d'objectifs absent", ErrUnavailable)
		}
		if _, err := s.d.Goals.GetProfileRevision(ctx, plan.ProfileRevision); err != nil {
			return nil, fmt.Errorf("%w : révision de profil %q", ErrNotFound, plan.ProfileRevision)
		}
	}

	// 1. pièces : maillages, empreintes, réglages normalisés
	type srcKey struct{ m, v string }
	srcs := map[srcKey]*srcMesh{}
	tri, loaded := 0, 0
	used := map[string]bool{}
	for ii, it := range in.Items {
		if it.ModelID == "" {
			return nil, invalidf("items[%d] : model_id requis", ii)
		}
		q := it.Quantity
		if q == 0 {
			q = 1
		}
		if q < 1 || q > 100 {
			return nil, invalidf("items[%d] : quantity de 1 à 100", ii)
		}
		set, err := normalizeSettings(it.Settings, ObjectSettingKeys)
		if err != nil {
			return nil, fmt.Errorf("items[%d] : %w", ii, err)
		}
		var ranges []PlanRange
		for ri, r := range it.LayerRanges {
			if r.ToMM <= r.FromMM || r.FromMM < 0 {
				return nil, invalidf("items[%d].layer_ranges[%d] : to_mm doit dépasser from_mm", ii, ri)
			}
			rs, err := normalizeSettings(r.Settings, []string{"layer_height", "sparse_infill_density", "wall_loops"})
			if err != nil || len(rs) == 0 {
				return nil, invalidf("items[%d].layer_ranges[%d] : settings (layer_height, sparse_infill_density, wall_loops) requis", ii, ri)
			}
			ranges = append(ranges, PlanRange{MinZ: r.FromMM, MaxZ: r.ToMM, Settings: rs})
		}
		k := srcKey{it.ModelID, it.VersionID}
		sm := srcs[k]
		if sm == nil {
			if sm, err = s.loadSource(ctx, it.ModelID, it.VersionID, &loaded); err != nil {
				return nil, err
			}
			srcs[k] = sm
		}
		bb := sm.bb
		label := strings.TrimSpace(labelClean.Replace(it.Label))
		if label == "" {
			label = sm.name
		}
		if r := []rune(label); len(r) > 40 {
			label = string(r[:40])
		}
		for c := 1; c <= q; c++ {
			if len(plan.Objects) >= maxPlanObjects {
				return nil, invalidf("au plus %d pièces par plan", maxPlanObjects)
			}
			if tri += sm.tris; tri > maxPlanTriangle {
				return nil, invalidf("plan trop lourd (plus de %d triangles cumulés) : réduire les quantités ou décimer les modèles", maxPlanTriangle)
			}
			nm := label
			if q > 1 {
				nm = fmt.Sprintf("%s #%d", label, c)
			}
			for base, n := nm, 2; used[nm]; n++ {
				nm = fmt.Sprintf("%s (%d)", base, n)
			}
			used[nm] = true
			plan.Objects = append(plan.Objects, PlanObject{Index: len(plan.Objects), Name: nm, ModelID: it.ModelID, VersionID: sm.versionID, Copy: c,
				SourceSHA: sm.sha, SourceName: sm.nm, W: bb.Size.X, D: bb.Size.Y, H: bb.Size.Z, Settings: set, Ranges: ranges})
		}
	}
	if plan.Name == "" {
		plan.Name = plan.Objects[0].Name
		if len(plan.Objects) > 1 {
			plan.Name = fmt.Sprintf("plateau %d pièces", len(plan.Objects))
		}
	}

	// 2. chaque pièce tient-elle seule ?
	var fitting []int
	for i := range plan.Objects {
		o := &plan.Objects[i]
		ok, _, why := fitsAlone(o.W, o.D, o.H, bed, in.SpacingMM)
		o.Fits, o.Problem = ok, why
		if ok {
			fitting = append(fitting, i)
		}
	}

	// 3. rangement
	mode := in.Arrangement
	var sl *slicer.Slicer
	if mode != "estimate" {
		var serr error
		if sl, serr = s.slicerInst(); serr != nil {
			if mode == "studio" {
				return nil, serr
			}
			mode = "estimate"
			plan.Warnings = append(plan.Warnings, "Bambu Studio indisponible : rangement estimé par rectangles englobants (le rangement exact se fait à la découpe)")
		} else {
			mode = "studio"
		}
	}
	plan.Arrangement = mode
	groups := [][]int{fitting}
	if in.GroupBySettings {
		bySig := map[string][]int{}
		var order []string
		for _, i := range fitting {
			sig := settingsSig(plan.Objects[i].Settings, plan.Objects[i].Ranges)
			if _, ok := bySig[sig]; !ok {
				order = append(order, sig)
			}
			bySig[sig] = append(bySig[sig], i)
		}
		groups = nil
		for _, sig := range order {
			groups = append(groups, bySig[sig])
		}
	}
	base, err := s.sliceBase(ctx, plan)
	if err != nil {
		return nil, err
	}
	offset := 0
	for _, g := range groups {
		if len(g) == 0 {
			continue
		}
		var n int
		if mode == "studio" {
			n, err = s.arrangeStudio(ctx, sl, plan, base, g, offset)
			if err != nil {
				plan.Warnings = append(plan.Warnings, "disposition par Bambu Studio impossible ("+err.Error()+") : rangement estimé")
				plan.Arrangement = "estimate"
				n, err = s.arrangeEstimate(plan, g, offset, in.SpacingMM, in.MaxPlates)
			}
		} else {
			n, err = s.arrangeEstimate(plan, g, offset, in.SpacingMM, in.MaxPlates)
		}
		if err != nil {
			return nil, err
		}
		offset += n
	}
	s.finishPlan(plan, in.MaxPlates)
	if err := s.putPlan(ctx, plan); err != nil {
		return nil, err
	}
	_ = s.d.Store.Audit(ctx, actor, "plate.planned", "plate_plan", plan.ID, map[string]any{"objects": len(plan.Objects), "plates": len(plan.Plates), "fits": plan.Fits})
	return plan, nil
}

// sliceBase résout machine/process/filaments/surcharges d'un plan comme ExecuteHeadlessSlice.
func (s *Service) sliceBase(ctx context.Context, plan *PlatePlan) (*SliceSpec, error) {
	rec, err := s.GetPrinterRecord(ctx, plan.PrinterID)
	if err != nil {
		return nil, err
	}
	model, err := s.GetPrinterModel(ctx, rec.ModelID)
	if err != nil {
		return nil, err
	}
	spec := &SliceSpec{Printer: model.Name, Nozzle: rec.NozzleMM, Process: plan.Process, Filaments: plan.Filaments}
	if plan.ProfileRevision != "" {
		rev, err := s.d.Goals.GetProfileRevision(ctx, plan.ProfileRevision)
		if err != nil {
			return nil, fmt.Errorf("%w : révision de profil %q", ErrNotFound, plan.ProfileRevision)
		}
		spec.Machine, spec.Process, spec.Filaments, spec.Overrides = rev.Machine, rev.Process, rev.Filaments, rev.Overrides
	}
	return spec, nil
}

func rotZ90(m *mesh.Mesh) *mesh.Mesh {
	return m.Transform(mesh.Matrix{0, 1, 0, -1, 0, 0, 0, 0, 1, 0, 0, 0})
}

func (s *Service) projectObjects(plan *PlatePlan, idx []int, meshes map[string]*mesh.Mesh) []mesh.ProjectObject {
	out := make([]mesh.ProjectObject, 0, len(idx))
	for _, i := range idx {
		o := plan.Objects[i]
		m := meshes[o.SourceSHA]
		if o.Rotated {
			m = rotZ90(m)
		}
		out = append(out, mesh.ProjectObject{Name: o.Name, Mesh: m, Plate: max(o.Plate, 1), X: o.X, Y: o.Y, Settings: o.Settings, Ranges: toLayerRanges(o.Ranges)})
	}
	return out
}

func toLayerRanges(rs []PlanRange) []mesh.LayerRange {
	var out []mesh.LayerRange
	for _, r := range rs {
		out = append(out, mesh.LayerRange{MinZ: r.MinZ, MaxZ: r.MaxZ, Settings: r.Settings})
	}
	return out
}

func (s *Service) planMeshes(plan *PlatePlan, idx []int) (map[string]*mesh.Mesh, error) {
	ms := map[string]*mesh.Mesh{}
	for _, i := range idx {
		o := plan.Objects[i]
		if ms[o.SourceSHA] == nil {
			m, err := s.meshOfBlob(o.SourceSHA, o.SourceName)
			if err != nil {
				return nil, err
			}
			ms[o.SourceSHA] = m
		}
	}
	return ms, nil
}

// arrangeStudio : Bambu Studio dispose le groupe (--arrange 1) ; renvoie le nombre de plaques utilisées.
func (s *Service) arrangeStudio(ctx context.Context, sl *slicer.Slicer, plan *PlatePlan, base *SliceSpec, idx []int, offset int) (int, error) {
	ms, err := s.planMeshes(plan, idx)
	if err != nil {
		return 0, err
	}
	req := slicer.Request{ID: cleanStem(plan.ID + "-arr"), Label: "arrange", Objects: s.projectObjects(plan, idx, ms), Machine: base.Machine,
		Process: base.Process, Filaments: base.Filaments, Overrides: base.Overrides}
	if base.Machine == "" {
		req.Printer, req.Nozzle = base.Printer, base.Nozzle
	}
	ar, err := sl.Arrange(ctx, req)
	if err != nil {
		return 0, err
	}
	for _, p := range ar.Placements {
		o := &plan.Objects[idx[p.Index]]
		o.Plate, o.X, o.Y = offset+p.Plate, p.X, p.Y
		o.W, o.D = p.W, p.D
	}
	return ar.Plates, nil
}

func (s *Service) arrangeEstimate(plan *PlatePlan, idx []int, offset int, gap float64, maxPlates int) (int, error) {
	items := make([]packItem, len(idx))
	for k, i := range idx {
		o := plan.Objects[i]
		items[k] = packItem{idx: k, w: o.W, d: o.D, canRot: true}
	}
	pos := packEstimate(items, plan.Bed, gap, max(maxPlates-offset, 1))
	n := 0
	for k, i := range idx {
		p := pos[k]
		o := &plan.Objects[i]
		if p.plate == 0 {
			o.Fits, o.Problem = false, "ne tient pas dans les "+strconv.Itoa(maxPlates)+" plaques autorisées"
			continue
		}
		o.Plate, o.X, o.Y, o.Rotated = offset+p.plate, p.x, p.y, p.rotate
		if p.rotate {
			o.W, o.D = o.D, o.W
		}
		n = max(n, p.plate)
	}
	return n, nil
}

// finishPlan calcule l'occupation par plaque et le rapport « tient / ne tient pas ».
func (s *Service) finishPlan(plan *PlatePlan, maxPlates int) {
	byPlate := map[int]*PlanPlate{}
	nPlates := 0
	for i := range plan.Objects {
		o := &plan.Objects[i]
		if !o.Fits || o.Plate == 0 {
			continue
		}
		p := byPlate[o.Plate]
		if p == nil {
			p = &PlanPlate{Index: o.Plate}
			byPlate[o.Plate] = p
		}
		p.Objects = append(p.Objects, o.Name)
		p.FootprintMM2 += o.W * o.D
		p.MaxHeightMM = math.Max(p.MaxHeightMM, o.H)
		nPlates = max(nPlates, o.Plate)
	}
	for k := 1; k <= nPlates; k++ {
		p := byPlate[k]
		if p == nil {
			p = &PlanPlate{Index: k}
		}
		p.FootprintMM2 = math.Round(p.FootprintMM2*10) / 10
		p.UsagePercent = math.Round(p.FootprintMM2/(plan.Bed.X*plan.Bed.Y)*1000) / 10
		plan.Plates = append(plan.Plates, *p)
	}
	bad := 0
	for _, o := range plan.Objects {
		if !o.Fits {
			bad++
		}
	}
	plan.Fits = bad == 0 && nPlates <= maxPlates && nPlates > 0
	how := map[string]string{"studio": "disposition réelle de Bambu Studio (--arrange 1)", "estimate": "rangement estimé par rectangles englobants"}[plan.Arrangement]
	head := fmt.Sprintf("%d pièce(s) → %d plateau(x) sur %s (plateau %s × %s, hauteur %s ; %s).", len(plan.Objects), nPlates, plan.PrinterModel,
		trimMM(plan.Bed.X), trimMM(plan.Bed.Y), trimMM(plan.Bed.Z), how)
	if plan.Fits {
		plan.Report = append(plan.Report, "TIENT : "+head)
	} else {
		plan.Report = append(plan.Report, "NE TIENT PAS : "+head)
	}
	for _, p := range plan.Plates {
		plan.Report = append(plan.Report, fmt.Sprintf("Plaque %d : %d pièce(s), empreinte %.0f %% du plateau, hauteur max %s.", p.Index, len(p.Objects), p.UsagePercent, trimMM(p.MaxHeightMM)))
	}
	for _, o := range plan.Objects {
		if !o.Fits {
			plan.Report = append(plan.Report, fmt.Sprintf("NE TIENT PAS : %s — %s.", o.Name, o.Problem))
		}
	}
	if nPlates > maxPlates {
		plan.Report = append(plan.Report, fmt.Sprintf("%d plaques nécessaires, %d autorisées (max_plates).", nPlates, maxPlates))
	}
	if plan.Arrangement == "estimate" {
		plan.Report = append(plan.Report, "Le rangement estimé est prudent (rectangles englobants) : Bambu Studio peut faire tenir davantage de pièces par plaque en emboîtant leurs contours.")
	}
}

func trimMM(v float64) string { return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64) + " mm" }

// ---- persistance ----

func (s *Service) putPlan(ctx context.Context, p *PlatePlan) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.d.Store.DB().ExecContext(ctx, `INSERT INTO plate_plans(id, doc, created_at) VALUES(?,?,?)
		ON CONFLICT(id) DO UPDATE SET doc=excluded.doc`, p.ID, string(b), p.CreatedAt.Format(time.RFC3339Nano))
	return err
}

// savePlanJobs écrit plan.Jobs dans une transaction (relecture du plan, remplacement des seules impressions, écriture) :
// ni écriture partielle, ni perte des autres champs du plan.
func (s *Service) savePlanJobs(ctx context.Context, plan *PlatePlan, have map[int]string) error {
	var jobs []PlanJob
	for k := 1; k <= len(plan.Plates); k++ {
		if id, ok := have[k]; ok {
			jobs = append(jobs, PlanJob{Plate: k, PrintJobID: id})
		}
	}
	err := s.d.Store.Tx(ctx, func(tx *sql.Tx) error {
		var doc string
		if err := tx.QueryRowContext(ctx, `SELECT doc FROM plate_plans WHERE id=?`, plan.ID).Scan(&doc); err != nil {
			return err
		}
		var cur PlatePlan
		if err := json.Unmarshal([]byte(doc), &cur); err != nil {
			return err
		}
		cur.Jobs = jobs
		b, err := json.Marshal(&cur)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE plate_plans SET doc=? WHERE id=?`, string(b), plan.ID)
		return err
	})
	if err == nil {
		plan.Jobs = jobs
	}
	return err
}

// GetPlatePlan relit un plan.
func (s *Service) GetPlatePlan(ctx context.Context, id string) (*PlatePlan, error) {
	var doc string
	err := s.d.Store.DB().QueryRowContext(ctx, `SELECT doc FROM plate_plans WHERE id=?`, id).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w : plan de plateau %q", ErrNotFound, id)
	}
	if err != nil {
		return nil, err
	}
	var p PlatePlan
	if err := json.Unmarshal([]byte(doc), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ListPlatePlans liste les plans (récents d'abord).
func (s *Service) ListPlatePlans(ctx context.Context, limit int) ([]PlatePlan, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.d.Store.DB().QueryContext(ctx, `SELECT doc FROM plate_plans ORDER BY created_at DESC, id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlatePlan{}
	for rows.Next() {
		var doc string
		var p PlatePlan
		if err := rows.Scan(&doc); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(doc), &p) == nil {
			out = append(out, p)
		}
	}
	return out, rows.Err()
}

// ---- découpe ----

// PlateSliceInput est l'entrée d'execute_plate_slice.
type PlateSliceInput struct {
	PlanID         string `json:"plan_id"`
	Plates         []int  `json:"plates,omitempty" jsonschema:"plaques à découper (défaut : toutes)"`
	IdempotencyKey string `json:"idempotency_key,omitempty" jsonschema:"un doublon de requête renvoie les mêmes impressions"`
}

// PlateSliceOutput : une impression par plaque.
type PlateSliceOutput struct {
	Plan *PlatePlan `json:"plan"`
	Jobs []Job      `json:"jobs"`
	Note string     `json:"note"`
}

func (p *PlatePlan) sliceSpec(plate int) *PlateSliceSpec {
	sp := &PlateSliceSpec{PlanID: p.ID, Plates: len(p.Plates)}
	for _, o := range p.Objects {
		sp.Objects = append(sp.Objects, PlateSliceObj{Name: o.Name, SourceSHA: o.SourceSHA, SourceNm: o.SourceName, Plate: o.Plate, X: o.X, Y: o.Y,
			Rotated: o.Rotated, Settings: o.Settings, Ranges: o.Ranges})
	}
	return sp
}

// ExecutePlateSlice crée une impression par plaque et met chaque découpe en file. Chaque impression
// suit la chaîne habituelle (validation, dépôt, accord humain) : son artefact ne contient QUE sa plaque, dont
// le numéro et l'empreinte sont ceux que la confirmation lie.
func (s *Service) ExecutePlateSlice(ctx context.Context, in PlateSliceInput) (*PlateSliceOutput, error) {
	if _, err := s.GetPlatePlan(ctx, in.PlanID); err != nil { // plan inconnu : aucun verrou créé
		return nil, err
	}
	// Un seul appel à la fois par plan : un doublon concurrent attend, relit le plan DÉJÀ mis à jour et renvoie les mêmes impressions.
	lk, _ := s.planLocks.LoadOrStore(in.PlanID, &sync.Mutex{})
	lk.(*sync.Mutex).Lock()
	defer lk.(*sync.Mutex).Unlock()
	plan, err := s.GetPlatePlan(ctx, in.PlanID)
	if err != nil {
		return nil, err
	}
	if !plan.Fits {
		return nil, fmt.Errorf("%w : le plan %s ne tient pas (%s)", ErrIncompatible, plan.ID, strings.Join(plan.Report, " "))
	}
	want := map[int]bool{}
	if len(in.Plates) == 0 {
		for _, p := range plan.Plates {
			want[p.Index] = true
		}
	}
	for _, k := range in.Plates {
		if k < 1 || k > len(plan.Plates) {
			return nil, invalidf("plaque %d hors plan (1 à %d)", k, len(plan.Plates))
		}
		want[k] = true
	}
	base, err := s.sliceBase(ctx, plan)
	if err != nil {
		return nil, err
	}
	have := map[int]string{}
	for _, j := range plan.Jobs {
		have[j.Plate] = j.PrintJobID
	}
	single := ""
	for i, o := range plan.Objects {
		if i == 0 {
			single = o.ModelID
		} else if o.ModelID != single {
			single = ""
		}
	}
	out := &PlateSliceOutput{Plan: plan}
	var plates []int
	for k := range want {
		plates = append(plates, k)
	}
	sort.Ints(plates)
	for _, k := range plates {
		if id, ok := have[k]; ok { // déjà découpée : on renvoie l'impression existante
			j, err := s.GetJob(ctx, id)
			if err == nil && j.State != contracts.PrintFailed && j.State != contracts.PrintCancelled {
				out.Jobs = append(out.Jobs, j)
				continue
			}
		}
		spec := *base
		spec.SourceSHA = planDigest(plan)
		spec.SourceName, spec.SourceFmt, spec.Plate = plan.Name, "3mf", k
		spec.Plan = plan.sliceSpec(k)
		j, err := s.createJob(ctx, Job{PrinterID: plan.PrinterID, ModelID: single, ProfileRevision: plan.ProfileRevision, Plate: k,
			Name: cleanStem(plan.Name) + "_plaque" + strconv.Itoa(k) + ".gcode.3mf", Slice: &spec})
		if err != nil {
			return nil, err
		}
		if j, err = s.transition(ctx, j.ID, contracts.EvPrepare, "préparation de la découpe de la plaque "+strconv.Itoa(k)+" du plan "+plan.ID, nil); err != nil {
			return nil, err
		}
		params, _ := json.Marshal(map[string]string{"print_job_id": j.ID})
		ikey := ""
		if k2 := strings.TrimSpace(in.IdempotencyKey); k2 != "" {
			ikey = plan.ID + ":plate" + strconv.Itoa(k) + ":" + k2
		}
		qj, created, err := s.d.Queue.Enqueue(ctx, jobs.EnqueueParams{Kind: JobSlice, Class: contracts.ClassHeavy, Params: params, IdempotencyKey: ikey})
		if err != nil {
			_, _ = s.transition(ctx, j.ID, contracts.EvCancel, "mise en file impossible : "+err.Error(), nil)
			return nil, err
		}
		if !created {
			_, _ = s.transition(ctx, j.ID, contracts.EvCancel, "doublon de la clé d'idempotence "+in.IdempotencyKey, nil)
			var p struct {
				ID string `json:"print_job_id"`
			}
			_ = json.Unmarshal(qj.Params, &p)
			orig, err := s.GetJob(ctx, p.ID)
			if err != nil {
				return nil, err
			}
			out.Jobs = append(out.Jobs, orig)
			have[k] = orig.ID
			continue
		}
		jj, err := s.update(ctx, j.ID, func(x *Job) error { x.SliceJobID = qj.ID; return nil })
		if err != nil {
			return nil, err
		}
		out.Jobs = append(out.Jobs, jj)
		have[k] = jj.ID
		if err := s.savePlanJobs(ctx, plan, have); err != nil { // chaque impression créée est consignée aussitôt
			return nil, err
		}
	}
	if err := s.savePlanJobs(ctx, plan, have); err != nil {
		return nil, err
	}
	out.Note = fmt.Sprintf("%d impression(s) créée(s), une par plaque : suivre chacune avec get_print_status ; valider, déposer puis confirmer chaque plaque séparément (un accord humain par impression).", len(out.Jobs))
	return out, nil
}

func planDigest(p *PlatePlan) string {
	b, _ := json.Marshal(struct {
		O []PlanObject
		P string
	}{p.Objects, p.PrinterID})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// planRequest reconstruit la requête de découpe d'une plaque depuis le projet figé.
func (s *Service) planRequest(spec *SliceSpec) (slicer.Request, error) {
	ps := spec.Plan
	ms := map[string]*mesh.Mesh{}
	var objs []mesh.ProjectObject
	for _, o := range ps.Objects {
		m := ms[o.SourceSHA]
		if m == nil {
			var err error
			if m, err = s.meshOfBlob(o.SourceSHA, o.SourceNm); err != nil {
				return slicer.Request{}, err
			}
			ms[o.SourceSHA] = m
		}
		if o.Rotated {
			m = rotZ90(m)
		}
		objs = append(objs, mesh.ProjectObject{Name: o.Name, Mesh: m, Plate: o.Plate, X: o.X, Y: o.Y, Settings: o.Settings, Ranges: toLayerRanges(o.Ranges)})
	}
	return slicer.Request{Objects: objs}, nil
}

// checkPlateObjects vérifie que la plaque tranchée porte exactement les pièces du plan et joint la matière et les
// couches de chacune (lues dans le G-code) au résumé de l'artefact.
func (s *Service) checkPlateObjects(spec *SliceSpec, res *slicer.Result, sum *ArtifactSummary) error {
	var want []string
	for _, o := range spec.Plan.Objects {
		if o.Plate == spec.Plate {
			want = append(want, o.Name)
		}
	}
	if len(res.Plates) != 1 || res.Plates[0].Index != spec.Plate {
		return fmt.Errorf("l'archive devait contenir la seule plaque %d, elle en porte %d", spec.Plate, len(res.Plates))
	}
	got := append([]string(nil), res.Plates[0].Objects...)
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(want, "\x00") != strings.Join(got, "\x00") {
		return fmt.Errorf("pièces de la plaque %d : plan %v, archive %v", spec.Plate, want, got)
	}
	if rep, err := slicer.ObjectReport(res.ArtifactPath, spec.Plate); err == nil {
		sum.Objects = rep
	} else {
		s.log.Warn("répartition par pièce illisible", "plate", spec.Plate, "err", err)
	}
	return nil
}
