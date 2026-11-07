package printing

import (
	"math"
	"sort"
	"strconv"
)

// Empreinte au sol d'une pièce et rangement « estimé » (rectangles englobants), utilisés AVANT toute découpe
// pour dire si les pièces tiennent, et quand Bambu Studio n'est pas là pour disposer lui-même.

// PlateBed décrit le plateau de l'imprimante cible.
type PlateBed struct {
	X          float64      `json:"x_mm"`
	Y          float64      `json:"y_mm"`
	Z          float64      `json:"max_height_mm"`
	Exclusions [][4]float64 `json:"exclusions,omitempty" jsonschema:"zones interdites [x0,y0,x1,y1] en mm (profil machine)"`
	Source     string       `json:"source,omitempty"`
}

type rect struct{ x0, y0, x1, y1 float64 }

func (r rect) overlaps(o rect) bool {
	return r.x0 < o.x1-1e-9 && o.x0 < r.x1-1e-9 && r.y0 < o.y1-1e-9 && o.y0 < r.y1-1e-9
}

// packItem est une pièce à ranger : boîte W x D (mm) et rotation autorisée.
type packItem struct {
	idx    int
	w, d   float64
	canRot bool
}

type packPos struct {
	plate  int
	x, y   float64
	rotate bool
}

// packEstimate range les pièces plaque par plaque, par positions candidates « bas-gauche » : les coins des
// pièces déjà posées (élargis de l'écart) et le bord utile du plateau. gap = écart entre pièces et au bord.
// Retourne la position de chaque pièce (par idx) ; une pièce qui ne tient sur aucune plaque vide reçoit plate = 0.
func packEstimate(items []packItem, bed PlateBed, gap float64, maxPlates int) map[int]packPos {
	out := map[int]packPos{}
	order := append([]packItem(nil), items...)
	sort.SliceStable(order, func(a, b int) bool { return order[a].w*order[a].d > order[b].w*order[b].d })
	var excl []rect
	for _, e := range bed.Exclusions {
		excl = append(excl, rect{e[0], e[1], e[2], e[3]})
	}
	usable := rect{gap / 2, gap / 2, bed.X - gap/2, bed.Y - gap/2}
	type plate struct{ placed []rect }
	var plates []*plate
	try := func(p *plate, w, d float64) (float64, float64, bool) {
		xs, ys := []float64{usable.x0}, []float64{usable.y0}
		for _, r := range p.placed {
			xs = append(xs, r.x1+gap)
			ys = append(ys, r.y1+gap)
		}
		for _, e := range excl {
			xs = append(xs, e.x1+gap/2)
			ys = append(ys, e.y1+gap/2)
		}
		sort.Float64s(xs)
		sort.Float64s(ys)
		bestX, bestY, found := 0.0, 0.0, false
		for _, y := range ys {
			if y+d > usable.y1+1e-9 {
				continue
			}
			for _, x := range xs {
				if x+w > usable.x1+1e-9 {
					continue
				}
				c := rect{x, y, x + w, y + d}
				ok := true
				for _, e := range excl {
					if c.overlaps(e) {
						ok = false
						break
					}
				}
				for _, r := range p.placed {
					if !ok {
						break
					}
					if c.overlaps(rect{r.x0 - gap, r.y0 - gap, r.x1 + gap, r.y1 + gap}) {
						ok = false
					}
				}
				if ok && (!found || y < bestY-1e-9 || (math.Abs(y-bestY) < 1e-9 && x < bestX)) {
					bestX, bestY, found = x, y, true
				}
			}
		}
		return bestX, bestY, found
	}
	place := func(p *plate, it packItem) (packPos, bool) {
		for _, rot := range []bool{false, true} {
			w, d := it.w, it.d
			if rot {
				if !it.canRot {
					continue
				}
				w, d = d, w
			}
			if x, y, ok := try(p, w, d); ok {
				p.placed = append(p.placed, rect{x, y, x + w, y + d})
				return packPos{x: x, y: y, rotate: rot}, true
			}
		}
		return packPos{}, false
	}
	for _, it := range order {
		var res packPos
		done := false
		for pi, p := range plates {
			if r, ok := place(p, it); ok {
				res, res.plate, done = r, pi+1, true
				break
			}
		}
		if !done && (maxPlates <= 0 || len(plates) < maxPlates) {
			np := &plate{}
			if r, ok := place(np, it); ok {
				plates = append(plates, np)
				res, res.plate, done = r, len(plates), true
			}
		}
		out[it.idx] = res // plate = 0 : ne tient sur aucune plaque
	}
	return out
}

// fitsAlone dit si la pièce tient seule sur le plateau (dans l'une des deux orientations à plat).
func fitsAlone(w, d, h float64, bed PlateBed, gap float64) (ok, rotate bool, why string) {
	if h > bed.Z+1e-9 && bed.Z > 0 {
		return false, false, "hauteur " + fmm(h) + " > hauteur utile " + fmm(bed.Z)
	}
	ux, uy := bed.X-gap, bed.Y-gap
	if w <= ux && d <= uy {
		return true, false, ""
	}
	if d <= ux && w <= uy {
		return true, true, ""
	}
	return false, false, "empreinte " + fmm(w) + " x " + fmm(d) + " > plateau " + fmm(bed.X) + " x " + fmm(bed.Y) + " (marge " + fmm(gap) + ")"
}

func fmm(v float64) string {
	return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64) + " mm"
}
