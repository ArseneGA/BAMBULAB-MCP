package filament

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var hexRe = regexp.MustCompile(`^#?([0-9a-fA-F]{6})([0-9a-fA-F]{2})?$`)

// normHex renvoie « #RRGGBB » en majuscules, ou "" si la valeur n'est pas une couleur hexadécimale.
func normHex(s string) string {
	m := hexRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return ""
	}
	return "#" + strings.ToUpper(m[1])
}

func rgb(hex string) (r, g, b float64, ok bool) {
	h := normHex(hex)
	if h == "" {
		return 0, 0, 0, false
	}
	v, _ := strconv.ParseUint(h[1:], 16, 32)
	return float64(v >> 16 & 0xff), float64(v >> 8 & 0xff), float64(v & 0xff), true
}

func lin(c float64) float64 {
	c /= 255
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func lab(hex string) (L, a, b float64, ok bool) {
	r, g, bl, ok := rgb(hex)
	if !ok {
		return 0, 0, 0, false
	}
	R, G, B := lin(r), lin(g), lin(bl)
	x := (0.4124*R + 0.3576*G + 0.1805*B) / 0.95047
	y := 0.2126*R + 0.7152*G + 0.0722*B
	z := (0.0193*R + 0.1192*G + 0.9505*B) / 1.08883
	f := func(t float64) float64 {
		if t > 0.008856 {
			return math.Cbrt(t)
		}
		return 7.787*t + 16.0/116
	}
	fx, fy, fz := f(x), f(y), f(z)
	return 116*fy - 16, 500 * (fx - fy), 200 * (fy - fz), true
}

// colorDistance est l'écart ΔE (CIE76) entre deux couleurs hexadécimales ; 999 si l'une est illisible.
// Repères : < 6 indiscernable, < 15 très proche, < 30 même famille, > 50 autre couleur.
func colorDistance(a, b string) float64 {
	l1, a1, b1, ok1 := lab(a)
	l2, a2, b2, ok2 := lab(b)
	if !ok1 || !ok2 {
		return 999
	}
	return math.Sqrt((l1-l2)*(l1-l2) + (a1-a2)*(a1-a2) + (b1-b2)*(b1-b2))
}

// colorQuality traduit un écart en verdict lisible.
func colorQuality(d float64) string {
	switch {
	case d <= 12:
		return "exact"
	case d <= 30:
		return "proche"
	case d <= 55:
		return "approximatif"
	}
	return "aucune"
}

// materialFamily ramène « PLA Basic », « PLA-CF », « Bambu PETG HF » à la famille d'AMS (PLA, PETG, ...).
func materialFamily(s string) string {
	u := strings.ToUpper(strings.TrimSpace(s))
	toks := strings.FieldsFunc(u, func(r rune) bool { return r == ' ' || r == '-' || r == '_' || r == '/' || r == '+' })
	for _, tok := range toks {
		for _, fam := range []string{"PETG", "PAHT", "PA12", "PA6", "PLA", "ABS", "ASA", "TPU", "PVA", "HIPS", "PPS", "PET", "PC", "PA", "PP"} {
			if tok == fam || (len(fam) >= 3 && strings.HasPrefix(tok, fam)) {
				return fam
			}
		}
	}
	if len(toks) > 0 {
		return toks[0]
	}
	return u
}

func sameMaterial(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return materialFamily(a) == materialFamily(b)
}

func fmtG(g float64) string { return fmt.Sprintf("%.1f g", g) }
