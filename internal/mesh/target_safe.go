package mesh

import (
	"math"
	"strconv"
	"strings"
)

// SafeArea : zone utile prudente du plateau. Bambu Studio (02.09) refuse (« plaque vide », -50) toute pièce
// qui ne tient pas dans le plateau privé de ses zones d'exclusion de coin sur les DEUX axes : sur la P1S
// (exclusion 0-18 × 0-28 mm) la zone sûre est donc 238 × 228 mm, pas 256 × 256, et une pièce de 239 × 141 mm
// ou de 216 × 236 mm est refusée alors que 202 × 214 passe. Seules les exclusions qui touchent un coin comptent.
func (t Target) SafeArea() Rect {
	b := t.bed()
	r := b
	for _, e := range t.Exclusions {
		left, right := e.X0 <= b.X0+1e-6, e.X1 >= b.X1-1e-6
		bottom, top := e.Y0 <= b.Y0+1e-6, e.Y1 >= b.Y1-1e-6
		switch {
		case left && bottom:
			r.X0, r.Y0 = math.Max(r.X0, e.X1), math.Max(r.Y0, e.Y1)
		case right && bottom:
			r.X1, r.Y0 = math.Min(r.X1, e.X0), math.Max(r.Y0, e.Y1)
		case left && top:
			r.X0, r.Y1 = math.Max(r.X0, e.X1), math.Min(r.Y1, e.Y0)
		case right && top:
			r.X1, r.Y1 = math.Min(r.X1, e.X0), math.Min(r.Y1, e.Y0)
		}
	}
	return r
}

// FootprintCheck est le verdict de fabricabilité d'une empreinte avant découpe.
type FootprintCheck struct {
	Fits     bool `json:"fits"`      // tient géométriquement (zones d'exclusion évitées, rotation de 90° permise)
	FitsSafe bool `json:"fits_safe"` // tient dans la zone utile prudente (SafeArea) : Studio l'acceptera
	Rotated  bool `json:"rotated,omitempty"`
	// SafeNeedsRotation : ne tient dans la zone sûre qu'après une rotation de 90° autour de Z (faite par le service).
	SafeNeedsRotation bool   `json:"safe_needs_rotation,omitempty"`
	TooTall           bool   `json:"too_tall,omitempty"`
	Message           string `json:"message,omitempty"` // phrase honnête quand ça ne passe pas (ou passe de justesse)
}

func fmtMM(v float64) string {
	return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64)
}

// UsableSummary décrit la zone utile : « zone utile 238 × 228 mm (plateau 256 × 256 mm, zones exclues : x 0–18, y 0–28 mm) ».
func (t Target) UsableSummary() string {
	s := t.SafeArea()
	b := t.bed()
	out := "zone utile " + fmtMM(s.W()) + " × " + fmtMM(s.H()) + " mm (plateau " + fmtMM(b.W()) + " × " + fmtMM(b.H()) + " mm"
	if len(t.Exclusions) == 0 {
		return out + ", aucune zone exclue)"
	}
	var ex []string
	for _, e := range t.Exclusions {
		ex = append(ex, "x "+fmtMM(e.X0)+"–"+fmtMM(e.X1)+", y "+fmtMM(e.Y0)+"–"+fmtMM(e.Y1))
	}
	return out + ", zones exclues : " + strings.Join(ex, " ; ") + " mm)"
}

// DimsText : « dimensions 239 × 141 × 50 mm ».
func DimsText(s Vec3) string {
	return "dimensions " + fmtMM(s.X) + " × " + fmtMM(s.Y) + " × " + fmtMM(s.Z) + " mm"
}

// CheckFootprint juge l'empreinte et la hauteur de la pièce (mm) contre la machine.
func (t Target) CheckFootprint(s Vec3) FootprintCheck {
	c := FootprintCheck{}
	if t.BedZ > 0 && s.Z > t.BedZ+1e-9 {
		c.TooTall = true
		c.Message = "pièce hors zone imprimable : " + DimsText(s) + ", hauteur utile " + fmtMM(t.BedZ) + " mm ; " + t.UsableSummary()
		return c
	}
	c.Fits, c.Rotated = t.Fits(Vec3{X: s.X, Y: s.Y, Z: 0})
	if !c.Fits {
		c.Message = "pièce hors zone imprimable : " + DimsText(s) + ", " + t.UsableSummary()
		return c
	}
	// Studio ne tourne pas la pièce de lui-même : le test prudent est sans rotation.
	sa := t.SafeArea()
	if s.X <= sa.W()+1e-9 && s.Y <= sa.H()+1e-9 {
		c.FitsSafe = true
		c.Rotated = false
		return c
	}
	if s.Y <= sa.W()+1e-9 && s.X <= sa.H()+1e-9 {
		c.SafeNeedsRotation = true
		c.Message = "pièce tournée de 90° pour tenir dans la " + t.UsableSummary() + " (" + DimsText(s) + ")"
		return c
	}
	c.Message = "pièce à la limite du plateau : " + DimsText(s) + ", " + t.UsableSummary() +
		" ; elle ne tient qu'en longeant une zone exclue : Bambu Studio la refuse (-50) dès qu'elle déborde dans la zone exclue ou hors plateau"
	return c
}
