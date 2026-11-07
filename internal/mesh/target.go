package mesh

import (
	"math"
	"sort"
	"strings"
)

// Rect est un rectangle du plateau (mm), dans le repère du profil machine.
type Rect struct {
	X0 float64 `json:"x0"`
	Y0 float64 `json:"y0"`
	X1 float64 `json:"x1"`
	Y1 float64 `json:"y1"`
}

// W et H : largeur (X) et profondeur (Y).
func (r Rect) W() float64 { return r.X1 - r.X0 }
func (r Rect) H() float64 { return r.Y1 - r.Y0 }

func (r Rect) empty() bool { return r.W() <= 1e-9 || r.H() <= 1e-9 }

func (r Rect) intersect(o Rect) Rect {
	return Rect{math.Max(r.X0, o.X0), math.Max(r.Y0, o.Y0), math.Min(r.X1, o.X1), math.Min(r.Y1, o.Y1)}
}

// Sources d'une cible machine.
const (
	TargetFromProfile = "profile" // volumes lus dans les profils machine de Bambu Studio
	TargetNominal     = "nominal" // repli : valeurs génériques à confirmer, profils absents
)

// NozzleVolume est la zone imprimable propre à une buse (machines à double buse).
type NozzleVolume struct {
	Index    int     `json:"index"` // 1 = première buse
	Area     Rect    `json:"area"`
	HeightMM float64 `json:"height_mm"`
}

// Target décrit la machine visée : volume utile, buse, zones d'exclusion et, pour une
// machine à double buse, le volume de chaque buse. Source dit d'où viennent les valeurs.
type Target struct {
	Name   string  `json:"name"`
	BedX   float64 `json:"bed_x_mm"`
	BedY   float64 `json:"bed_y_mm"`
	BedZ   float64 `json:"bed_z_mm"`
	Nozzle float64 `json:"nozzle_mm"`

	// Source : "profile" (profil Bambu Studio) ou "nominal" (repli à confirmer).
	Source string `json:"source,omitempty"`
	// Profile : preset machine d'où viennent les valeurs.
	Profile string `json:"profile,omitempty"`
	// Bed : rectangle du plateau dans le repère du profil (zéro = 0,0 → BedX,BedY).
	Bed Rect `json:"bed,omitempty"`
	// Exclusions : zones où la pièce ne doit pas passer (boîtes englobantes des zones du profil).
	Exclusions []Rect `json:"exclusions,omitempty"`
	// Extruders : nombre de buses ; Volumes : volume imprimable de chaque buse quand le profil le donne.
	Extruders int            `json:"extruders,omitempty"`
	Volumes   []NozzleVolume `json:"nozzle_volumes,omitempty"`
	// Notes : réserves sur les valeurs (ex. volume par buse non fourni).
	Notes []string `json:"notes,omitempty"`
}

func (t Target) bed() Rect {
	if t.Bed.W() > 0 && t.Bed.H() > 0 {
		return t.Bed
	}
	return Rect{0, 0, t.BedX, t.BedY}
}

// freeRects retourne des rectangles libres maximaux (hors zones d'exclusion) dans la zone donnée :
// une pièce rectangulaire est posable si elle tient dans l'un d'eux.
func freeRects(area Rect, excl []Rect) []Rect {
	cands := []Rect{area}
	for _, e := range excl {
		var next []Rect
		for _, c := range cands {
			ov := c.intersect(e)
			if ov.empty() {
				next = append(next, c)
				continue
			}
			for _, s := range []Rect{
				{c.X0, c.Y0, e.X0, c.Y1}, {e.X1, c.Y0, c.X1, c.Y1},
				{c.X0, c.Y0, c.X1, e.Y0}, {c.X0, e.Y1, c.X1, c.Y1},
			} {
				if !s.empty() {
					next = append(next, s)
				}
			}
		}
		// élaguer les rectangles contenus dans un autre
		cands = cands[:0:0]
		for i, a := range next {
			dom := false
			for j, b := range next {
				if i != j && b.X0 <= a.X0+1e-9 && b.Y0 <= a.Y0+1e-9 && b.X1 >= a.X1-1e-9 && b.Y1 >= a.Y1-1e-9 && (a != b || j < i) {
					dom = true
					break
				}
			}
			if !dom {
				cands = append(cands, a)
			}
		}
		if len(cands) > 64 {
			sort.Slice(cands, func(i, j int) bool { return cands[i].W()*cands[i].H() > cands[j].W()*cands[j].H() })
			cands = cands[:64]
		}
	}
	return cands
}

// fitsFootprint indique si l'empreinte sx × sy tient dans l'un des rectangles libres, et si c'est tournée de 90°.
func fitsFootprint(free []Rect, sx, sy float64) (fits, rotated bool) {
	for _, r := range free {
		if sx <= r.W()+1e-9 && sy <= r.H()+1e-9 {
			return true, false
		}
	}
	for _, r := range free {
		if sy <= r.W()+1e-9 && sx <= r.H()+1e-9 {
			return true, true
		}
	}
	return false, false
}

// FreeRects : rectangles libres du plateau entier (exclusions retirées).
func (t Target) FreeRects() []Rect { return freeRects(t.bed(), t.Exclusions) }

// Fits indique si une pièce de taille s tient sur la machine (une seule buse si le profil donne un
// volume par buse). rotated : elle ne tient que tournée de 90° autour de Z.
func (t Target) Fits(s Vec3) (fits, rotated bool) {
	if len(t.Volumes) == 0 {
		if s.Z > t.BedZ+1e-9 {
			return false, false
		}
		return fitsFootprint(t.FreeRects(), s.X, s.Y)
	}
	bestRot := false
	for _, v := range t.Volumes {
		if s.Z > v.HeightMM+1e-9 {
			continue
		}
		ok, rot := fitsFootprint(freeRects(v.Area, t.Exclusions), s.X, s.Y)
		if ok && !rot {
			return true, false
		}
		if ok {
			bestRot = true
		}
	}
	return bestRot, bestRot
}

// CommonArea : zone commune à toutes les buses (pièce multi-matière entre buses) ; ok=false si le profil
// ne donne pas de volume par buse.
func (t Target) CommonArea() (Rect, float64, bool) {
	if len(t.Volumes) < 2 {
		return Rect{}, 0, false
	}
	r := t.Volumes[0].Area
	h := t.Volumes[0].HeightMM
	for _, v := range t.Volumes[1:] {
		r = r.intersect(v.Area)
		h = math.Min(h, v.HeightMM)
	}
	return r, h, true
}

// FitsAllNozzles : la pièce tient dans la zone commune à toutes les buses.
func (t Target) FitsAllNozzles(s Vec3) (bool, bool) {
	c, h, ok := t.CommonArea()
	if !ok {
		return false, false
	}
	if s.Z > h+1e-9 {
		return false, false
	}
	return fitsFootprint(freeRects(c, t.Exclusions), s.X, s.Y)
}

// UsableMaxFootprint retourne la plus grande empreinte (par l'aire) libre du plateau entier, pour les messages.
func (t Target) UsableMaxFootprint() (w, h float64) {
	best := -1.0
	for _, r := range t.FreeRects() {
		if a := r.W() * r.H(); a > best {
			best, w, h = a, r.W(), r.H()
		}
	}
	return
}

// NormalizeMachineName simplifie un nom de machine pour la comparaison ("Bambu Lab A1 mini" → "a1mini").
func NormalizeMachineName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "bambu lab")
	s = strings.TrimPrefix(s, "bambulab")
	s = strings.TrimSuffix(s, " nozzle")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// nominalTargets : repli quand les profils Bambu Studio sont absents. Valeurs publiques génériques,
// sans zones d'exclusion ni volume par buse : à confirmer, elles sont marquées « nominal ».
var nominalTargets = []Target{
	{Name: "P1S", BedX: 256, BedY: 256, BedZ: 256, Nozzle: 0.4},
	{Name: "P1P", BedX: 256, BedY: 256, BedZ: 256, Nozzle: 0.4},
	{Name: "P2S", BedX: 256, BedY: 256, BedZ: 256, Nozzle: 0.4},
	{Name: "X1C", BedX: 256, BedY: 256, BedZ: 256, Nozzle: 0.4},
	{Name: "X1E", BedX: 256, BedY: 256, BedZ: 256, Nozzle: 0.4},
	{Name: "A1", BedX: 256, BedY: 256, BedZ: 256, Nozzle: 0.4},
	{Name: "A1 mini", BedX: 180, BedY: 180, BedZ: 180, Nozzle: 0.4},
	{Name: "H2D", BedX: 350, BedY: 320, BedZ: 325, Nozzle: 0.4, Extruders: 2},
	{Name: "H2S", BedX: 340, BedY: 320, BedZ: 340, Nozzle: 0.4},
}

// NominalTarget retourne la cible de repli d'un nom de machine (insensible à la casse et aux espaces).
// Chaque valeur est marquée Source = "nominal" et accompagnée d'une note de réserve.
func NominalTarget(name string) (Target, bool) {
	key := NormalizeMachineName(name)
	if key == "" {
		key = "p1s"
	}
	for _, t := range nominalTargets {
		if NormalizeMachineName(t.Name) == key {
			t.Name = "Bambu Lab " + t.Name
			t.Source = TargetNominal
			t.Notes = []string{"valeurs nominales (profils Bambu Studio indisponibles) : volume, zones d'exclusion et buses à confirmer sur la machine"}
			if t.Extruders > 1 {
				t.Notes = append(t.Notes, "double buse : volume utile par buse non connu, volume global utilisé")
			}
			return t, true
		}
	}
	return Target{}, false
}

// NominalNames liste les noms du repli (pour les messages d'erreur).
func NominalNames() []string {
	out := make([]string, len(nominalTargets))
	for i, t := range nominalTargets {
		out[i] = t.Name
	}
	return out
}
