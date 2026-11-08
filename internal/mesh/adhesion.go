package mesh

import (
	"fmt"
	"math"
	"sort"
)

// Risque de décollement (adhérence au plateau) : HEURISTIQUE tirée de la géométrie seule, jamais une garantie.
//
// Surface de contact = aire (projetée sur le plateau) des faces tournées vers le bas situées dans la première couche
// de la pièce posée sur Z = min (tolérance contactBandMM). Un îlot de contact est un groupe de ces faces qui se touchent.
//
// Seuils (documentés, ajustables ici) :
//   - élancement = hauteur / √(aire de contact totale) ;
//     ≥ slenderModerate (4) : risque « moderate » ; ≥ slenderHigh (6) : « high ».
//     Repères : cube posé = 1 ; vase de 20 cm sur 6 cm de diamètre ≈ 3,8 ; pièce de 204 mm sur trois petits appuis
//     de 15 × 15 mm ≈ 7,9 (cas réel décollé en PETG, chambre à 5 °C, sans bordure).
//   - îlot « petit » : aire < smallIslandMM2 (100 mm², ≈ 10 × 10 mm) ; il compte dès que la pièce dépasse
//     tallPartMM (40 mm) de haut : risque « moderate » au moins.
//   - aucune face plane au contact (sphère, pointe) sur une pièce de plus de 20 mm : « high ».
const (
	contactBandMM   = 0.25
	downFacingNz    = -0.7
	slenderModerate = 4.0
	slenderHigh     = 6.0
	smallIslandMM2  = 100.0
	tallPartMM      = 40.0
	noContactMinH   = 20.0
	vertexSnapMM    = 0.01
)

// Niveaux de risque.
const (
	AdhesionOK       = "ok"
	AdhesionModerate = "moderate"
	AdhesionHigh     = "high"
)

// AdhesionReport est le résultat de Adhesion.
type AdhesionReport struct {
	Level            string    `json:"level" jsonschema:"ok | moderate | high : risque de décollement estimé d'après la géométrie seule (heuristique)"`
	HeightMM         float64   `json:"height_mm"`
	ContactAreaMM2   float64   `json:"contact_area_mm2" jsonschema:"aire de contact de la première couche avec le plateau (faces tournées vers le bas à moins de 0,25 mm du plan Z min)"`
	FootprintAreaMM2 float64   `json:"footprint_area_mm2" jsonschema:"aire du rectangle englobant au sol"`
	ContactRatio     float64   `json:"contact_ratio" jsonschema:"contact / rectangle englobant au sol (1 = pose pleine)"`
	Islands          int       `json:"islands" jsonschema:"nombre d'îlots de contact distincts"`
	SmallIslands     int       `json:"small_islands" jsonschema:"îlots de moins de 100 mm²"`
	IslandAreasMM2   []float64 `json:"island_areas_mm2,omitempty" jsonschema:"aires des îlots, du plus grand au plus petit (10 premiers)"`
	Slenderness      float64   `json:"slenderness" jsonschema:"hauteur / racine carrée de l'aire de contact (plafonné à 999)"`
	VolumeCM3        float64   `json:"volume_cm3,omitempty"`
	Reasons          []string  `json:"reasons,omitempty"`
	Recommendation   string    `json:"recommendation,omitempty"`
}

// Risky dit si un avertissement doit être émis.
func (r *AdhesionReport) Risky() bool { return r != nil && r.Level != AdhesionOK && r.Level != "" }

// Summary : une phrase pour l'humain et le modèle (vide si aucun risque).
func (r *AdhesionReport) Summary() string {
	if !r.Risky() {
		return ""
	}
	lv := "modéré"
	if r.Level == AdhesionHigh {
		lv = "ÉLEVÉ"
	}
	return fmt.Sprintf("Risque de décollement %s : pièce de %.0f mm de haut posée sur %.0f mm² de contact (%d îlot(s)). %s",
		lv, r.HeightMM, r.ContactAreaMM2, r.Islands, r.Recommendation)
}

const adhesionAdvice = "Ajouter une bordure : brim_type outer_only, brim_width 5 à 8 mm (surcharges de découpe), et chauffer/fermer la chambre pour les matières qui voilent (PETG, ABS, ASA)."

// Adhesion estime le risque de décollement de la pièce posée telle quelle (Z vers le haut, plan Z = min sur le plateau).
func Adhesion(m *Mesh) *AdhesionReport {
	if m == nil || len(m.T) == 0 {
		return nil
	}
	bb := m.Bounds()
	r := &AdhesionReport{HeightMM: bb.Size.Z, FootprintAreaMM2: bb.Size.X * bb.Size.Y}
	zlim := bb.Min.Z + contactBandMM

	// volume signé (tétraèdres depuis l'origine)
	var vol float64
	for _, t := range m.T {
		a, b, c := m.V[t[0]], m.V[t[1]], m.V[t[2]]
		vol += a.Dot(b.Cross(c)) / 6
	}
	r.VolumeCM3 = math.Abs(vol) / 1000

	// faces de contact et îlots (union-find sur les sommets accolés)
	type key [2]int64
	snap := func(v Vec3) key {
		return key{int64(math.Round(v.X / vertexSnapMM)), int64(math.Round(v.Y / vertexSnapMM))}
	}
	parent := map[key]key{}
	var find func(k key) key
	find = func(k key) key {
		p, ok := parent[k]
		if !ok {
			parent[k] = k
			return k
		}
		if p == k {
			return k
		}
		root := find(p)
		parent[k] = root
		return root
	}
	union := func(a, b key) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}
	areaOf := map[key]float64{}
	type contact struct {
		k key
		a float64
	}
	var cs []contact
	for i, t := range m.T {
		a, b, c := m.V[t[0]], m.V[t[1]], m.V[t[2]]
		if a.Z > zlim || b.Z > zlim || c.Z > zlim {
			continue
		}
		n, area := m.Normal(i)
		if area == 0 || n.Z > downFacingNz {
			continue
		}
		proj := math.Abs(n.Z) * area // aire projetée sur le plateau
		ka, kb, kc := snap(a), snap(b), snap(c)
		union(ka, kb)
		union(kb, kc)
		cs = append(cs, contact{ka, proj})
		r.ContactAreaMM2 += proj
	}
	for _, c := range cs {
		areaOf[find(c.k)] += c.a
	}
	for _, a := range areaOf {
		r.IslandAreasMM2 = append(r.IslandAreasMM2, round1(a))
		if a < smallIslandMM2 {
			r.SmallIslands++
		}
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(r.IslandAreasMM2)))
	r.Islands = len(r.IslandAreasMM2)
	if len(r.IslandAreasMM2) > 10 {
		r.IslandAreasMM2 = r.IslandAreasMM2[:10]
	}
	r.ContactAreaMM2 = round1(r.ContactAreaMM2)
	if r.FootprintAreaMM2 > 0 {
		r.ContactRatio = round3(math.Min(1, r.ContactAreaMM2/r.FootprintAreaMM2))
	}
	if r.ContactAreaMM2 > 0 {
		r.Slenderness = round1(math.Min(999, r.HeightMM/math.Sqrt(r.ContactAreaMM2)))
	} else {
		r.Slenderness = 999
	}

	lv := AdhesionOK
	raise := func(l string) {
		if l == AdhesionHigh || (l == AdhesionModerate && lv == AdhesionOK) {
			lv = l
		}
	}
	switch {
	case r.ContactAreaMM2 == 0 && r.HeightMM >= noContactMinH:
		raise(AdhesionHigh)
		r.Reasons = append(r.Reasons, "aucune face plane au contact du plateau (contact ponctuel ou courbe)")
	case r.Slenderness >= slenderHigh:
		raise(AdhesionHigh)
		r.Reasons = append(r.Reasons, fmt.Sprintf("hauteur / √(aire de contact) = %.1f (seuil élevé : %.0f) : %.0f mm de haut pour %.0f mm² de contact", r.Slenderness, slenderHigh, r.HeightMM, r.ContactAreaMM2))
	case r.Slenderness >= slenderModerate:
		raise(AdhesionModerate)
		r.Reasons = append(r.Reasons, fmt.Sprintf("hauteur / √(aire de contact) = %.1f (seuil modéré : %.0f)", r.Slenderness, slenderModerate))
	}
	if r.SmallIslands > 0 && r.HeightMM >= tallPartMM {
		raise(AdhesionModerate)
		r.Reasons = append(r.Reasons, fmt.Sprintf("%d îlot(s) de contact de moins de %.0f mm² sous une pièce de %.0f mm de haut", r.SmallIslands, smallIslandMM2, r.HeightMM))
	}
	if r.Islands >= 2 && r.ContactRatio < 0.15 && r.HeightMM >= tallPartMM {
		raise(AdhesionModerate)
		r.Reasons = append(r.Reasons, fmt.Sprintf("le contact ne couvre que %.0f %% de l'emprise au sol, réparti sur %d appuis séparés", 100*r.ContactRatio, r.Islands))
	}
	r.Level = lv
	if lv != AdhesionOK {
		r.Recommendation = adhesionAdvice
	}
	return r
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
