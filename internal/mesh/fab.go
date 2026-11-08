package mesh

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// FabOptions règle l'analyse de fabricabilité.
type FabOptions struct {
	Target Target
	// OverhangAngleDeg : seuil de surplomb depuis la verticale (0° = paroi, 90° = plafond). Défaut 45.
	OverhangAngleDeg float64
	// ThinThresholdMM : épaisseur considérée mince. Défaut 2 × buse.
	ThinThresholdMM float64
	// RaySamples : nombre maximal de triangles échantillonnés pour l'épaisseur. Défaut 2000.
	RaySamples int
	// SkipOrientation désactive la proposition d'orientation.
	SkipOrientation bool
	WeldTol         float64
	// SupportThresholdDeg : seuil des profils process Bambu (support_threshold_angle), pente mesurée depuis
	// l'HORIZONTALE (0° = plafond plat ; 30° par défaut dans Bambu Studio) ; équivaut à un surplomb de
	// 90° − seuil depuis la verticale. Ignoré si OverhangAngleDeg est fixé. ThresholdSource dit d'où il vient.
	SupportThresholdDeg float64
	ThresholdSource     string
	// MaxFullTriangles : au-delà (défaut 1 000 000), la notation des orientations travaille sur un échantillon
	// de triangles, annoncé dans FabReport.Sampling.
	MaxFullTriangles int
	// SelfIntersectBudget : durée maximale du contrôle des auto-intersections (défaut 5 s) ;
	// SkipSelfIntersections le désactive.
	SelfIntersectBudget   time.Duration
	SkipSelfIntersections bool

	overhangSource string
}

// Integrity résume l'état topologique du maillage analysé en même temps que la fabricabilité.
type Integrity struct {
	Watertight          bool    `json:"watertight"`
	OpenEdges           int     `json:"open_edges"`
	NonManifoldEdges    int     `json:"non_manifold_edges"`
	InconsistentEdges   int     `json:"inconsistent_edges"`
	DegenerateTriangles int     `json:"degenerate_triangles"`
	DuplicateTriangles  int     `json:"duplicate_triangles"`
	FloatingFragments   int     `json:"floating_fragments"`
	Components          int     `json:"components"`
	VolumeMM3           float64 `json:"volume_mm3"`
}

// OverhangRegion est un groupe de triangles en surplomb connexes.
type OverhangRegion struct {
	Triangles   int     `json:"triangles"`
	AreaMM2     float64 `json:"area_mm2"`
	BBox        BBox    `json:"bbox"`
	MaxAngleDeg float64 `json:"max_angle_deg"`
}

// OverhangReport : géométrie des surplombs dans l'orientation donnée.
type OverhangReport struct {
	ThresholdDeg float64 `json:"threshold_deg"` // depuis la verticale (0° = paroi, 90° = plafond)
	// SupportThresholdDeg : le même seuil dans la convention des profils Bambu (pente depuis l'horizontale).
	SupportThresholdDeg float64          `json:"support_threshold_angle_deg"`
	Source              string           `json:"threshold_source,omitempty"` // d'où vient le seuil (profil process, réglage, défaut)
	AreaMM2             float64          `json:"area_mm2"`
	Fraction            float64          `json:"fraction_of_surface"`
	Regions             []OverhangRegion `json:"regions"` // 20 plus grandes
	RegionCount         int              `json:"region_count"`
}

// ThinReport : épaisseur estimée par lancers de rayons opposés à la normale.
type ThinReport struct {
	Samples      int     `json:"samples"`
	Hits         int     `json:"hits"`
	MinMM        float64 `json:"min_mm"`
	P5MM         float64 `json:"p5_mm"`
	MedianMM     float64 `json:"median_mm"`
	ThresholdMM  float64 `json:"threshold_mm"`
	ThinCount    int     `json:"thin_samples"`
	ThinFraction float64 `json:"thin_fraction"`
	ThinPoints   []Vec3  `json:"thin_points,omitempty"` // jusqu'à 10 exemples
	Confidence   string  `json:"confidence"`            // low|medium|high
	Level        string  `json:"level"`                 // toujours "heuristic"
	// Rays : rayons par échantillon (axe + cône) ; ConeHalfAngleDeg : demi-angle du cône ; Agreement : part des
	// échantillons où le cône et le rayon axial s'accordent à 25 % près ; Sampling : mode de tirage.
	Rays             int     `json:"rays_per_sample"`
	ConeHalfAngleDeg float64 `json:"cone_half_angle_deg"`
	Agreement        float64 `json:"ray_agreement"`
	Sampling         string  `json:"sampling"`
	Note             string  `json:"note"`
}

// OrientationCandidate est une orientation d'impression proposée (heuristique).
type OrientationCandidate struct {
	Label        string  `json:"label"`
	Up           Vec3    `json:"up"`       // direction du modèle qui devient +Z
	Rotation     Matrix  `json:"rotation"` // à appliquer au modèle (convention 3MF) pour l'obtenir
	HeightMM     float64 `json:"height_mm"`
	SizeXMM      float64 `json:"size_x_mm"`
	SizeYMM      float64 `json:"size_y_mm"`
	ContactMM2   float64 `json:"bed_contact_mm2"`
	OverhangMM2  float64 `json:"overhang_mm2"`
	OverhangFrac float64 `json:"overhang_fraction"`
	Fits         bool    `json:"fits"`
	Score        float64 `json:"score"`  // plus bas = mieux
	Source       string  `json:"source"` // axis|hull|current
}

// OrientationReport : classement des orientations.
type OrientationReport struct {
	Level        string                 `json:"level"` // "heuristic"
	ScoreFormula string                 `json:"score_formula"`
	Candidates   []OrientationCandidate `json:"candidates"`
	Best         int                    `json:"best_index"`
	Note         string                 `json:"note"`
}

// FabReport est le rapport de fabricabilité. Aucune de ces valeurs n'est une certification.
type FabReport struct {
	Target          Target             `json:"target"`
	Size            Vec3               `json:"size_mm"`
	FitsBed         bool               `json:"fits_bed"`
	FitsBedRotatedZ bool               `json:"fits_bed_rotated_90_about_z"`
	MarginMM        Vec3               `json:"margin_mm"`
	Overhang        OverhangReport     `json:"overhang"`
	Thin            ThinReport         `json:"thin_walls"`
	Orientation     *OrientationReport `json:"orientation,omitempty"`
	// Adhesion : risque de décollement estimé d'après la surface de contact de la première couche (adhesion.go).
	Adhesion *AdhesionReport `json:"adhesion,omitempty"`
	// Integrity et SelfIntersections : défauts du maillage constatés pendant l'analyse (réparer avant impression).
	Integrity         *Integrity              `json:"integrity,omitempty"`
	SelfIntersections *SelfIntersectionReport `json:"self_intersections,omitempty"`
	// Sampled et Sampling : étapes réalisées sur un échantillon (gros maillage), avec la raison.
	Sampled  bool     `json:"sampled,omitempty"`
	Sampling []string `json:"sampling,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

const orientationFormula = "0,6·fraction_surplomb + 0,25·hauteur/diagonale + 0,15·(1 − min(1, contact/(10 % de la surface)))"

// rotationFor construit la matrice (convention 3MF) qui envoie le vecteur up sur +Z,
// avec une base arbitraire stable pour X/Y.
func rotationFor(up Vec3) Matrix {
	u := up.Norm()
	ref := Vec3{1, 0, 0}
	if math.Abs(u.X) > 0.9 {
		ref = Vec3{0, 1, 0}
	}
	e1 := ref.Sub(u.Scale(ref.Dot(u))).Norm()
	e2 := u.Cross(e1)
	// coordonnées tournées : x' = v·e1, y' = v·e2, z' = v·u  →  colonnes de la matrice ligne-vecteur
	return Matrix{e1.X, e2.X, u.X, e1.Y, e2.Y, u.Y, e1.Z, e2.Z, u.Z, 0, 0, 0}
}

func proposeOrientations(m *Mesh, remap []int32, nw int, opt FabOptions, stride int) *OrientationReport {
	rep := &OrientationReport{
		Level: "heuristic", ScoreFormula: orientationFormula, Best: -1,
		Note: "Classement heuristique (surplomb, appui, hauteur) ; ignore la résistance inter-couches, l'aspect des faces visibles et les supports solubles. Les faces de la coque convexe sont approchées par des sommets extrêmes.",
	}
	type cand struct {
		up     Vec3
		label  string
		source string
	}
	cands := []cand{{Vec3{0, 0, 1}, "actuelle (+Z)", "current"}}
	axes := []struct {
		v Vec3
		l string
	}{{Vec3{0, 0, -1}, "face haute posée (−Z vers le haut)"}, {Vec3{1, 0, 0}, "+X vers le haut"}, {Vec3{-1, 0, 0}, "−X vers le haut"}, {Vec3{0, 1, 0}, "+Y vers le haut"}, {Vec3{0, -1, 0}, "−Y vers le haut"}}
	for _, a := range axes {
		cands = append(cands, cand{a.v, a.l, "axis"})
	}
	for i, hf := range principalHullFaces(hullPoints(m.V, 300), 6, math.Cos(2*math.Pi/180)) {
		cands = append(cands, cand{hf.normal.Scale(-1), fmt.Sprintf("face de coque convexe n°%d posée sur le plateau", i+1), "hull"})
	}
	var kept []cand
	for _, c := range cands {
		dup := false
		for _, k := range kept {
			if k.up.Dot(c.up) > 0.9998 {
				dup = true
				break
			}
		}
		if !dup {
			kept = append(kept, c)
		}
	}
	var area float64
	for i := 0; i < len(m.T); i += stride {
		_, a := m.Normal(i)
		area += a * float64(stride)
	}
	diag := m.Bounds().Size.Len()
	t := opt.Target
	cosFlat := math.Cos(1 * math.Pi / 180)
	for _, c := range kept {
		R := rotationFor(c.up)
		lo := Vec3{math.Inf(1), math.Inf(1), math.Inf(1)}
		hi := Vec3{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
		for _, v := range m.V {
			p := R.Apply(v)
			lo = Vec3{math.Min(lo.X, p.X), math.Min(lo.Y, p.Y), math.Min(lo.Z, p.Z)}
			hi = Vec3{math.Max(hi.X, p.X), math.Max(hi.Y, p.Y), math.Max(hi.Z, p.Z)}
		}
		sz := hi.Sub(lo)
		oc := OrientationCandidate{Label: c.label, Up: c.up, Rotation: R, HeightMM: sz.Z, SizeXMM: sz.X, SizeYMM: sz.Y, Source: c.source}
		oc.Fits, _ = t.Fits(sz)
		ov := overhangs(m, remap, nw, c.up, opt.OverhangAngleDeg, false, stride)
		oc.OverhangMM2, oc.OverhangFrac = ov.AreaMM2, ov.Fraction
		for i := 0; i < len(m.T); i += stride {
			tr := m.T[i]
			n, a := m.Normal(i)
			if a == 0 || -n.Dot(c.up) < cosFlat {
				continue
			}
			if m.V[tr[0]].Dot(c.up) <= lo.Z+0.05 && m.V[tr[1]].Dot(c.up) <= lo.Z+0.05 && m.V[tr[2]].Dot(c.up) <= lo.Z+0.05 {
				oc.ContactMM2 += a * float64(stride)
			}
		}
		contactTerm := 1.0
		if area > 0 {
			contactTerm = 1 - math.Min(1, oc.ContactMM2/(0.1*area))
		}
		hn := 0.0
		if diag > 0 {
			hn = sz.Z / diag
		}
		oc.Score = 0.6*oc.OverhangFrac + 0.25*hn + 0.15*contactTerm
		if !oc.Fits {
			oc.Score += 10 // inutilisable sur cette machine
		}
		rep.Candidates = append(rep.Candidates, oc)
	}
	sort.SliceStable(rep.Candidates, func(i, j int) bool { return rep.Candidates[i].Score < rep.Candidates[j].Score })
	if len(rep.Candidates) > 0 {
		rep.Best = 0
	}
	return rep
}
