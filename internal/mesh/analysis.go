package mesh

import (
	"fmt"
	"math"
	"time"
)

// AnalyzeOptions règle l'analyse.
type AnalyzeOptions struct {
	// WeldTol : tolérance de soudure des sommets en mm. 0 = max(1e-6, 1e-6 × diagonale).
	WeldTol float64
	// DegenerateAreaEps : aire (mm²) sous laquelle un triangle est dégénéré. 0 = (1e-6 × diagonale)².
	DegenerateAreaEps float64
	// SelfIntersectBudget : durée allouée au contrôle des auto-intersections. 0 = automatique (2 s, seulement
	// jusqu'à 100 000 triangles), > 0 = contrôle borné à ce délai quelle que soit la taille, < 0 = désactivé.
	SelfIntersectBudget time.Duration

	bvh *bvh // BVH déjà construit sur le maillage (partagé avec le calcul d'épaisseur), optionnel
}

// ComponentInfo décrit une composante connexe (par arêtes/sommets soudés).
type ComponentInfo struct {
	Triangles int     `json:"triangles"`
	Volume    float64 `json:"volume_mm3"`
	BBox      BBox    `json:"bbox"`
}

// Report est le résultat d'analyse géométrique d'un maillage. Ce sont des
// mesures géométriques : elles ne disent rien de la résistance mécanique.
type Report struct {
	Triangles      int  `json:"triangles"`
	RawVertices    int  `json:"raw_vertices"`
	WeldedVertices int  `json:"welded_vertices"`
	BBox           BBox `json:"bbox"`
	// Volume signé en mm³ (négatif si les normales pointent vers l'intérieur).
	Volume    float64 `json:"volume_mm3"`
	Area      float64 `json:"area_mm2"`
	WeldTol   float64 `json:"weld_tolerance_mm"`
	Edges     int     `json:"edges"`
	OpenEdges int     `json:"open_edges"`
	// NonManifoldEdges : arêtes partagées par plus de deux triangles.
	NonManifoldEdges int `json:"non_manifold_edges"`
	// InconsistentEdges : arêtes entre deux triangles d'orientation incohérente (normales mal orientées).
	InconsistentEdges   int `json:"inconsistent_edges"`
	DegenerateTriangles int `json:"degenerate_triangles"`
	// DuplicateTriangles : copies en trop de triangles portés par les mêmes trois sommets (hors le premier).
	DuplicateTriangles int `json:"duplicate_triangles,omitempty"`
	// FloatingFragments : composantes de volume négligeable (< 0,1 % de la plus grosse) : éclats flottants.
	FloatingFragments int `json:"floating_fragments,omitempty"`
	// SelfIntersectingPairs : paires de triangles qui se traversent (hors voisins d'une arête). Voir
	// SelfIntersectionsChecked : le contrôle est borné (maillage trop gros ou temps épuisé => non fait / partiel).
	SelfIntersectingPairs    int  `json:"self_intersecting_pairs,omitempty"`
	SelfIntersectionsChecked bool `json:"self_intersections_checked"`
	// SelfIntersectionsComplete : vrai si tous les triangles ont été confrontés avant la limite de temps.
	SelfIntersectionsComplete bool `json:"self_intersections_complete,omitempty"`
	// SelfIntersectingTriangles, SelfIntersectionRegionCount et SelfIntersectionRegions : détail (10 plus grandes régions).
	SelfIntersectingTriangles   int                      `json:"self_intersecting_triangles,omitempty"`
	SelfIntersectionRegionCount int                      `json:"self_intersection_region_count,omitempty"`
	SelfIntersectionRegions     []SelfIntersectionRegion `json:"self_intersection_regions,omitempty"`
	Watertight                  bool                     `json:"watertight"`
	// InvertedNormals : maillage fermé et cohérent dont le volume signé est négatif.
	InvertedNormals bool            `json:"inverted_normals"`
	Components      int             `json:"components"`
	ComponentList   []ComponentInfo `json:"component_list,omitempty"`
	Warnings        []string        `json:"warnings,omitempty"`
}

// ObjectReport associe un objet du modèle à son analyse.
type ObjectReport struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Report *Report `json:"report"`
}

// ModelReport est l'analyse d'un modèle complet (tous les objets).
type ModelReport struct {
	Format      string         `json:"format"`
	Unit        string         `json:"unit"`
	UnitAssumed bool           `json:"unit_assumed"`
	UnitNote    string         `json:"unit_note,omitempty"`
	Objects     []ObjectReport `json:"objects"`
	Combined    *Report        `json:"combined"`
	Extras      []string       `json:"extras,omitempty"`
	Warnings    []string       `json:"warnings,omitempty"`
}

// Analyze analyse chaque objet puis l'ensemble. Pour un fichier sans unité (STL, OBJ),
// les mm sont une hypothèse signalée avec une indication de plausibilité d'après l'échelle.
func (md *Model) Analyze(opt AnalyzeOptions) *ModelReport {
	mr := &ModelReport{Format: md.Format, Unit: md.Unit, UnitAssumed: md.UnitAssumed, Extras: md.Extras}
	mr.Warnings = append(mr.Warnings, md.Warnings...)
	for _, o := range md.Objects {
		mr.Objects = append(mr.Objects, ObjectReport{ID: o.ID, Name: o.Name, Report: Analyze(o.Mesh, opt)})
	}
	mr.Combined = Analyze(md.Combined(), opt)
	if md.UnitAssumed {
		mr.UnitNote = unitPlausibility(mr.Combined.BBox.Size)
	}
	return mr
}

func unitPlausibility(size Vec3) string {
	mx := math.Max(size.X, math.Max(size.Y, size.Z))
	note := "unité inconnue : millimètres supposés"
	switch {
	case mx == 0:
		return note
	case mx < 1:
		return note + fmt.Sprintf(" ; plus grande dimension %.4g mm : le fichier est peut-être en mètres (×1000) ou en pouces (×25,4) — à confirmer", mx)
	case mx < 5:
		return note + fmt.Sprintf(" ; plus grande dimension %.3g mm : peut-être des pouces ou des centimètres — à confirmer", mx)
	case mx > 1000:
		return note + fmt.Sprintf(" ; plus grande dimension %.0f mm : peut-être des microns ou un modèle très grand — à confirmer", mx)
	}
	return note + fmt.Sprintf(" ; plus grande dimension %.1f mm : plausible pour une pièce imprimable", mx)
}
