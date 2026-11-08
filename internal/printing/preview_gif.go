package printing

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	stdpalette "image/color/palette"
	"image/draw"
	"image/gif"
	"math"
)

// Animation GIF des couches choisies (Go pur, image/gif de la bibliothèque standard). Taille bornée : largeur de
// 640 px au plus, 48 images au plus, 4 Mio au plus.

const (
	gifMaxWidth  = 640
	gifMaxBytes  = 4 << 20
	gifFrameCS   = 12  // centièmes de seconde par image
	gifLastHoldC = 120 // pause sur la dernière image
)

func gifPalette() color.Palette {
	p := make(color.Palette, 0, 256)
	p = append(p, color.RGBA{0xf3, 0xf1, 0xed, 255}, color.RGBA{0xdd, 0xda, 0xd3, 255}, color.RGBA{0x33, 0x33, 0x33, 255},
		color.RGBA{0, 0, 0, 255}, color.RGBA{255, 255, 255, 255}, color.RGBA{0x16, 0xa0, 0x6b, 255}, color.RGBA{0xd7, 0xd3, 0xc8, 255})
	for _, c := range palette { // couleurs des types de trajectoires (preview.go)
		p = append(p, c)
	}
	for _, c := range stdpalette.WebSafe {
		if len(p) >= 256 {
			break
		}
		p = append(p, c)
	}
	return p
}

func renderLayerGIF(sha string, plate, total int, sel []int, gs *gcodeScan, vb viewBox, used map[int]bool, note string, w, h int) (*PreviewOutput, error) {
	tw := min(w, gifMaxWidth)
	th := int(math.Round(float64(tw) * vb.h() / vb.w()))
	if th > min(h, gifMaxWidth) {
		th = min(h, gifMaxWidth)
		tw = int(math.Round(float64(th) * vb.w() / vb.h()))
	}
	if tw < 80 || th < 80 {
		return nil, invalidf("animation trop petite : augmenter width/height")
	}
	pal := gifPalette()
	anim := &gif.GIF{LoopCount: 0}
	for i, l := range sel {
		ld := gs.Layers[l]
		frame := drawSegsTile(ld.Segs, vb, tw, th)
		txt := fmt.Sprintf("L%d/%d", l, total)
		if ld.Z > 0 {
			txt += fmt.Sprintf("  Z=%.2f", ld.Z)
		}
		label(frame, 6, 6, 2, color.RGBA{255, 255, 255, 255}, txt)
		// barre de progression verte en bas de l'image
		done := int(float64(tw) * float64(i+1) / float64(len(sel)))
		for y := th - 4; y < th; y++ {
			for x := 0; x < tw; x++ {
				if x < done {
					frame.SetRGBA(x, y, color.RGBA{0x16, 0xa0, 0x6b, 255})
				} else {
					frame.SetRGBA(x, y, color.RGBA{0xd7, 0xd3, 0xc8, 255})
				}
			}
		}
		pm := image.NewPaletted(frame.Bounds(), pal)
		draw.Draw(pm, pm.Rect, frame, image.Point{}, draw.Src)
		anim.Image = append(anim.Image, pm)
		d := gifFrameCS
		if i == len(sel)-1 {
			d = gifLastHoldC
		}
		anim.Delay = append(anim.Delay, d)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, anim); err != nil {
		return nil, err
	}
	if buf.Len() > gifMaxBytes {
		return nil, invalidf("animation trop lourde (%d Mio, maximum %d) : réduire width ou max_tiles", buf.Len()>>20, gifMaxBytes>>20)
	}
	lines := []string{
		fmt.Sprintf("Animation de %d couche(s) sur %d - plaque %d (artefact %s) : une image par couche, %d x %d px", len(sel), total, plate, shortSHA(sha), tw, th),
		fmt.Sprintf("Couches : %s ; meme cadrage (%.1f x %.1f mm) ; numero et Z en haut a gauche, barre de progression en bas", joinInts(sel), vb.w(), vb.h()),
	}
	if fl := featureLegend(gs.Feats, used); fl != "" {
		lines = append(lines, "Types: "+fl)
	}
	if note != "" {
		lines = append(lines, "Selection: "+note)
	}
	lines = append(lines, "Trajectoires extrudees seulement (G1 avec E>0, arcs ramenes a leur corde): ni deplacements a vide, ni retractions, ni duree")
	nseg := 0
	for _, l := range sel {
		nseg += len(gs.Layers[l].Segs)
	}
	return &PreviewOutput{Kind: "gcode_animation", MIME: "image/gif", Width: tw, Height: th, Legend: lines, PNG: buf.Bytes(), Layers: sel,
		Represented:    []string{"segments d'extrusion de chaque couche choisie, colorés par type (FEATURE), une image par couche", "numéro de couche et hauteur Z dans chaque image"},
		NotRepresented: []string{"déplacements à vide", "rétractions et purges", "épaisseur réelle des filets", "couches non choisies", "temps"},
		Render:         map[string]any{"plate": plate, "layers": sel, "layers_total": total, "frames": len(sel), "segments": nseg, "artifact_sha256": sha, "view_mm": []float64{vb.w(), vb.h()}, "bytes": buf.Len()}}, nil
}
