package insights

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

// RenderChart dessine l'histogramme des heures machine par période (réussies en teal, perdues en rouge)
// avec les totaux en légende. PNG simple, sans dépendance : pensé pour être lu par Claude.
func RenderChart(d *Dashboard) ([]byte, error) {
	const w, h = 760, 360
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	bg := color.RGBA{250, 250, 247, 255}
	ink := color.RGBA{34, 38, 45, 255}
	grid := color.RGBA{218, 220, 222, 255}
	ok := color.RGBA{20, 150, 140, 255}
	bad := color.RGBA{205, 70, 60, 255}
	draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)

	drawText(img, 16, 12, 2, ink, "Heures machine par periode ("+d.Period+")")
	total := fmt.Sprintf("%d impressions - %.1f h - %.0f g - %.2f %s (estimation)", d.Totals.Prints, d.Totals.MachineHours, d.Totals.FilamentGrams, d.Totals.Cost, d.Currency)
	drawText(img, 16, 34, 1, ink, total)
	if d.Totals.SuccessRate != nil {
		drawText(img, 16, 46, 1, ink, fmt.Sprintf("Taux de reussite : %.0f %%", *d.Totals.SuccessRate*100))
	}

	left, right, top, bottom := 54, w-16, 70, h-48
	maxH := 0.0
	for _, r := range d.ByPeriod {
		if r.Hours > maxH {
			maxH = r.Hours
		}
	}
	if maxH <= 0 {
		maxH = 1
	}
	// graduation arrondie
	step := niceStep(maxH)
	top0 := step * float64(int(maxH/step)+1)
	for v := 0.0; v <= top0+1e-9; v += step {
		y := bottom - int(float64(bottom-top)*v/top0)
		for x := left; x <= right; x++ {
			img.Set(x, y, grid)
		}
		drawText(img, 8, y-3, 1, ink, fmt.Sprintf("%g", v))
	}
	n := len(d.ByPeriod)
	if n == 0 {
		drawText(img, left+10, (top+bottom)/2, 2, ink, "Aucune donnee")
	}
	slot := float64(right-left) / float64(max(n, 1))
	bw := int(slot * 0.7)
	if bw < 1 {
		bw = 1
	}
	every := 1
	for n/every > 12 {
		every++
	}
	for i, r := range d.ByPeriod {
		x0 := left + int(slot*float64(i)+slot*0.15)
		yOK := bottom - int(float64(bottom-top)*(r.Hours-r.FailedHours)/top0)
		yAll := bottom - int(float64(bottom-top)*r.Hours/top0)
		fill(img, x0, yOK, bw, bottom-yOK, ok)
		fill(img, x0, yAll, bw, yOK-yAll, bad)
		if i%every == 0 {
			lbl := r.Key
			if len(lbl) > 5 && d.Period == "day" {
				lbl = lbl[5:]
			}
			drawText(img, x0-2, bottom+6, 1, ink, lbl)
		}
	}
	for x := left; x <= right; x++ {
		img.Set(x, bottom, ink)
	}
	fill(img, left, h-24, 10, 10, ok)
	drawText(img, left+16, h-23, 1, ink, "heures de pieces reussies")
	fill(img, left+200, h-24, 10, 10, bad)
	drawText(img, left+216, h-23, 1, ink, "heures perdues (ratees, partielles, annulees)")
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func fill(img *image.RGBA, x, y, w, h int, c color.RGBA) {
	if h <= 0 || w <= 0 {
		return
	}
	draw.Draw(img, image.Rect(x, y, x+w, y+h), &image.Uniform{c}, image.Point{}, draw.Src)
}

func niceStep(maxV float64) float64 {
	for _, s := range []float64{0.5, 1, 2, 5, 10, 20, 50, 100, 200, 500, 1000} {
		if maxV/s <= 5 {
			return s
		}
	}
	return 1000
}
