// Package camera : instantanés des caméras d'imprimante, détection d'échec locale et explicable, photos rattachées
// aux impressions. Rien ici n'arrête ni ne lance jamais une impression : une alerte renvoie une photo et un lien vers
// la page humaine /stop (docs/SECURITY.md).
package camera

// Détecteur d'échec léger, sans modèle appris, dont chaque décision est expliquée (signaux, valeurs, poids).
//
// Principe : on compare l'image courante à une image de RÉFÉRENCE (le plateau vide, prise à l'ouverture de /confirm ou
// au début de l'impression), sur une grille réduite de luminance (160x90). Les signaux :
//
//	off_footprint   masse de pixels qui diffèrent de la référence HORS de la colonne au-dessus de l'empreinte de la pièce
//	                (« spaghetti »), persistante sur plusieurs instantanés (une tête qui passe n'est pas persistante) ;
//	footprint_loss  l'empreinte apprise à l'ouverture des premières couches s'est vidée (pièce décollée / emportée) ;
//	layer_stall     la couche n'avance plus alors que l'imprimante dit imprimer ;
//	printer_error   l'imprimante rapporte une erreur (HMS ou print_error) ;
//	scene_motion    changement brutal de toute la scène d'un instantané à l'autre (caméra déplacée, porte ouverte, main).
//
// Un changement d'éclairage global est compensé (décalage médian retiré). Limites assumées : un spaghetti qui reste dans
// la colonne de la pièce n'est pas vu ; quelqu'un qui reste devant la caméra plusieurs minutes peut déclencher une alerte
// (c'est expliqué dans le rapport) ; les seuils sont à régler sur la vraie P1S.

import (
	"fmt"
	"image"
	"math"
	"sort"
	"time"
)

// Dimensions de la grille d'analyse.
const (
	gridW = 160
	gridH = 90
)

// DetectorConfig règle le détecteur (valeurs par défaut sûres, modifiables par configure_failure_detection).
type DetectorConfig struct {
	Threshold     float64 `json:"threshold" jsonschema:"score (0..1) à partir duquel une alerte est levée"`
	DiffThreshold float64 `json:"diff_threshold" jsonschema:"écart de luminance (0..255) à partir duquel une cellule est dite changée"`
	Persist       int     `json:"persist_frames" jsonschema:"nombre d'instantanés consécutifs sur lesquels une anomalie doit persister"`
	StallMinutes  float64 `json:"stall_minutes" jsonschema:"minutes sans changement de couche avant que l'arrêt de progression compte"`
	Consecutive   int     `json:"consecutive_alerts" jsonschema:"nombre d'analyses consécutives au-dessus du seuil avant d'alerter"`
}

// DefaultDetectorConfig : valeurs par défaut.
func DefaultDetectorConfig() DetectorConfig {
	return DetectorConfig{Threshold: 0.6, DiffThreshold: 14, Persist: 3, StallMinutes: 15, Consecutive: 2}
}

func (c *DetectorConfig) normalize() {
	d := DefaultDetectorConfig()
	if c.Threshold <= 0 || c.Threshold > 1 {
		c.Threshold = d.Threshold
	}
	if c.DiffThreshold < 4 || c.DiffThreshold > 100 {
		c.DiffThreshold = d.DiffThreshold
	}
	if c.Persist < 1 || c.Persist > 10 {
		c.Persist = d.Persist
	}
	if c.StallMinutes <= 0 {
		c.StallMinutes = d.StallMinutes
	}
	if c.Consecutive < 1 || c.Consecutive > 20 {
		c.Consecutive = d.Consecutive
	}
}

// Observation : ce que l'imprimante dit au moment de l'instantané.
type Observation struct {
	At          time.Time
	Layer       int
	TotalLayers int
	Printing    bool // l'imprimante est en train d'imprimer (pas en pause, pas en préparation)
	HMSCount    int
	PrintError  int
}

// Signal est un indice de défaillance expliqué.
type Signal struct {
	Name         string  `json:"name"`
	Value        float64 `json:"value" jsonschema:"mesure brute (fraction, minutes...)"`
	Score        float64 `json:"score" jsonschema:"0..1 : gravité de ce signal"`
	Weight       float64 `json:"weight"`
	Contribution float64 `json:"contribution" jsonschema:"score x poids : part dans le score total"`
	Detail       string  `json:"detail"`
}

// Report est le résultat d'une analyse.
type Report struct {
	At             time.Time `json:"at"`
	Score          float64   `json:"score" jsonschema:"0..1 ; alerte quand score >= threshold"`
	Threshold      float64   `json:"threshold"`
	Level          string    `json:"level" jsonschema:"ok | watch | alert"`
	Alert          bool      `json:"alert"`
	Signals        []Signal  `json:"signals"`
	Notes          []string  `json:"notes,omitempty"`
	ReferenceSet   bool      `json:"reference_set"`
	FootprintKnown bool      `json:"footprint_known"`
	Layer          int       `json:"layer"`
}

var signalWeights = map[string]float64{"off_footprint": 0.9, "footprint_loss": 0.8, "layer_stall": 0.7, "printer_error": 0.5, "scene_motion": 0.2}

type grid struct {
	v [gridW * gridH]float32
}

// toGrid réduit une image en luminance 160x90 (moyenne par bloc).
func toGrid(img image.Image) *grid {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	g := &grid{}
	if w < gridW || h < gridH {
		return g
	}
	var sum [gridW * gridH]float64
	var cnt [gridW * gridH]int32
	if yc, ok := img.(*image.YCbCr); ok {
		for y := 0; y < h; y++ {
			gy := y * gridH / h
			row := yc.Y[yc.YOffset(b.Min.X, b.Min.Y+y):]
			for x := 0; x < w; x++ {
				i := gy*gridW + x*gridW/w
				sum[i] += float64(row[x])
				cnt[i]++
			}
		}
	} else {
		for y := 0; y < h; y++ {
			gy := y * gridH / h
			for x := 0; x < w; x++ {
				r, gg, bb, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
				l := (0.299*float64(r) + 0.587*float64(gg) + 0.114*float64(bb)) / 257
				i := gy*gridW + x*gridW/w
				sum[i] += l
				cnt[i]++
			}
		}
	}
	for i := range sum {
		if cnt[i] > 0 {
			g.v[i] = float32(sum[i] / float64(cnt[i]))
		}
	}
	return g
}

type mask [gridW * gridH]bool

func (m *mask) count() int {
	n := 0
	for _, b := range m {
		if b {
			n++
		}
	}
	return n
}

// Detector garde l'état d'UNE impression (référence, empreinte, historique). Non concurrent : le service le sérialise.
type Detector struct {
	cfg DetectorConfig

	ref       *grid
	refW      int
	prev      *grid
	ring      []*mask // dernières cartes d'anomalies (taille <= Persist)
	learn     []*mask // cartes recueillies pour apprendre l'empreinte
	learnTry  int
	footprint *mask // cellules de la pièce (intersection des cartes d'apprentissage)
	band      struct{ x0, x1, y1 int }
	footLayer int

	lastLayer       int
	lastLayerChange time.Time
	keepRing        []float64
}

// NewDetector crée un détecteur vierge.
func NewDetector(cfg DetectorConfig) *Detector {
	cfg.normalize()
	return &Detector{cfg: cfg}
}

// SetConfig change les réglages en cours d'impression.
func (d *Detector) SetConfig(cfg DetectorConfig) { cfg.normalize(); d.cfg = cfg }

// HasReference dit si une référence (plateau vide) est posée.
func (d *Detector) HasReference() bool { return d.ref != nil }

// SetReference pose l'image de référence (plateau vide) et remet l'apprentissage à zéro.
func (d *Detector) SetReference(img image.Image) {
	d.ref = toGrid(img)
	d.refW = img.Bounds().Dx()
	d.prev, d.ring, d.learn, d.footprint, d.keepRing = nil, nil, nil, nil, nil
	d.learnTry = 0
}

// Observe analyse un instantané.
func (d *Detector) Observe(img image.Image, ob Observation) Report {
	rep := Report{At: ob.At, Threshold: d.cfg.Threshold, Layer: ob.Layer, ReferenceSet: d.ref != nil}
	add := func(name string, value, score float64, detail string) {
		score = math.Max(0, math.Min(1, score))
		w := signalWeights[name]
		rep.Signals = append(rep.Signals, Signal{Name: name, Value: round3(value), Score: round3(score), Weight: w, Contribution: round3(score * w), Detail: detail})
	}
	cur := toGrid(img)
	b := img.Bounds()
	if b.Dx() < gridW || b.Dy() < gridH {
		rep.Notes = append(rep.Notes, "image trop petite pour l'analyse visuelle")
		cur = nil
	}
	if d.ref != nil && b.Dx() != d.refW {
		rep.Notes = append(rep.Notes, "la taille de l'image a changé depuis la référence : référence abandonnée")
		d.ref = nil
		rep.ReferenceSet = false
	}

	var anom *mask
	if cur != nil && d.ref != nil {
		// Compensation d'éclairage : décalage médian de l'écart (hors bordure de 3 %).
		diffs := make([]float32, 0, gridW*gridH)
		for i := range cur.v {
			diffs = append(diffs, cur.v[i]-d.ref.v[i])
		}
		sorted := append([]float32(nil), diffs...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		med := sorted[len(sorted)/2]
		if math.Abs(float64(med)) > 6 {
			rep.Notes = append(rep.Notes, fmt.Sprintf("éclairage global compensé (%+.0f niveaux)", med))
		}
		anom = &mask{}
		thr := float32(d.cfg.DiffThreshold)
		for i, v := range diffs {
			dv := v - med
			if dv > thr || dv < -thr {
				anom[i] = true
			}
		}
		for x := 0; x < gridW; x++ { // bordure ignorée (vignettage, bords de l'objectif)
			for _, y := range []int{0, 1, gridH - 2, gridH - 1} {
				anom[y*gridW+x] = false
			}
		}
		for y := 0; y < gridH; y++ {
			for _, x := range []int{0, 1, gridW - 2, gridW - 1} {
				anom[y*gridW+x] = false
			}
		}
		d.ring = append(d.ring, anom)
		if len(d.ring) > d.cfg.Persist {
			d.ring = d.ring[len(d.ring)-d.cfg.Persist:]
		}
		d.learnFootprint(anom, ob, &rep)
	} else if d.ref == nil {
		rep.Notes = append(rep.Notes, "pas d'image de référence (plateau vide) : l'analyse visuelle est désactivée, seuls la progression des couches et les erreurs de l'imprimante comptent")
	}

	// 1. masse hors empreinte
	if anom != nil && d.footprint != nil {
		rep.FootprintKnown = true
		if len(d.ring) < 2 && d.cfg.Persist > 1 {
			add("off_footprint", 0, 0, "confirmation en cours : une anomalie doit persister sur plusieurs instantanés")
		} else {
			persist := d.persistent()
			out, bad := 0, 0
			for y := 0; y < gridH; y++ {
				for x := 0; x < gridW; x++ {
					if x >= d.band.x0 && x <= d.band.x1 && y <= d.band.y1 {
						continue // colonne au-dessus de l'empreinte : la pièce grandit vers le haut
					}
					out++
					if persist[y*gridW+x] {
						bad++
					}
				}
			}
			frac := 0.0
			if out > 0 {
				frac = float64(bad) / float64(out)
			}
			s := (frac - 0.008) / (0.04 - 0.008)
			if bad < 12 {
				s = 0
			}
			add("off_footprint", frac, s, fmt.Sprintf("%.1f %% de la scène hors de la colonne de la pièce diffère de la référence, de façon persistante sur %d instantané(s) (%d cellules)", frac*100, len(d.ring), bad))
		}
		// 2. empreinte vidée
		keep := 0.0
		n := 0
		for i, f := range d.footprint {
			if f {
				n++
				if anom[i] {
					keep++
				}
			}
		}
		if n > 0 {
			keep /= float64(n)
			d.keepRing = append(d.keepRing, keep)
			if len(d.keepRing) > d.cfg.Persist {
				d.keepRing = d.keepRing[len(d.keepRing)-d.cfg.Persist:]
			}
			best := 0.0
			for _, k := range d.keepRing {
				best = math.Max(best, k)
			}
			s := 0.0
			if len(d.keepRing) >= 2 && ob.Layer > d.footLayer+2 {
				s = (0.5 - best) / 0.35
			}
			add("footprint_loss", 1-best, s, fmt.Sprintf("%.0f %% de l'empreinte apprise (couche %d) est encore occupée", best*100, d.footLayer))
		}
	} else if anom != nil {
		add("off_footprint", 0, 0, "empreinte de la pièce pas encore apprise (premières couches)")
	}

	// 3. arrêt de progression
	stallMin := 0.0
	if ob.Printing && ob.Layer > 0 {
		if ob.Layer != d.lastLayer || d.lastLayerChange.IsZero() {
			d.lastLayer, d.lastLayerChange = ob.Layer, ob.At
		}
		stallMin = ob.At.Sub(d.lastLayerChange).Minutes()
	} else {
		d.lastLayer, d.lastLayerChange = ob.Layer, ob.At
	}
	s3 := 0.0
	if stallMin >= d.cfg.StallMinutes {
		s3 = math.Min(1, 0.6+0.4*(stallMin-d.cfg.StallMinutes)/d.cfg.StallMinutes)
	}
	add("layer_stall", stallMin, s3, fmt.Sprintf("couche %d inchangée depuis %.1f min (seuil %.0f min)", ob.Layer, stallMin, d.cfg.StallMinutes))

	// 4. erreurs de l'imprimante
	s4 := 0.0
	if ob.HMSCount > 0 || ob.PrintError != 0 {
		s4 = 1
	}
	add("printer_error", float64(ob.HMSCount), s4, fmt.Sprintf("%d erreur(s) HMS, print_error=%d", ob.HMSCount, ob.PrintError))

	// 5. mouvement brutal de la scène
	if cur != nil && d.prev != nil && d.ref != nil {
		moved := 0
		thr := float32(d.cfg.DiffThreshold)
		for i := range cur.v {
			if v := cur.v[i] - d.prev.v[i]; v > thr || v < -thr {
				moved++
			}
		}
		frac := float64(moved) / float64(gridW*gridH)
		add("scene_motion", frac, (frac-0.35)/0.4, fmt.Sprintf("%.0f %% de l'image a changé depuis l'instantané précédent", frac*100))
	}
	if cur != nil {
		d.prev = cur
	}

	total := 0.0
	for _, s := range rep.Signals {
		total += s.Contribution
	}
	rep.Score = round3(math.Min(1, total))
	switch {
	case rep.Score >= d.cfg.Threshold:
		rep.Level, rep.Alert = "alert", true
	case rep.Score >= d.cfg.Threshold/2:
		rep.Level = "watch"
	default:
		rep.Level = "ok"
	}
	return rep
}

// persistent : intersection des cartes d'anomalies récentes.
func (d *Detector) persistent() *mask {
	out := &mask{}
	if len(d.ring) == 0 {
		return out
	}
	*out = *d.ring[0]
	for _, m := range d.ring[1:] {
		for i := range out {
			out[i] = out[i] && m[i]
		}
	}
	return out
}

// learnFootprint apprend l'empreinte sur les premiers instantanés où la couche >= 2 : intersection de `Persist` cartes
// (la tête qui passe en est retirée), plus grandes composantes connexes, boîte englobante.
func (d *Detector) learnFootprint(anom *mask, ob Observation, rep *Report) {
	if d.footprint != nil || ob.Layer < 2 || !ob.Printing {
		return
	}
	d.learn = append(d.learn, anom)
	if len(d.learn) < d.cfg.Persist {
		return
	}
	inter := &mask{}
	*inter = *d.learn[0]
	for _, m := range d.learn[1:] {
		for i := range inter {
			inter[i] = inter[i] && m[i]
		}
	}
	d.learn = nil
	comps := components(inter)
	if len(comps) == 0 || len(comps[0]) < 6 {
		d.learnTry++
		rep.Notes = append(rep.Notes, fmt.Sprintf("empreinte de la pièce introuvable (tentative %d) : contraste insuffisant avec la référence", d.learnTry))
		return
	}
	foot := &mask{}
	x0, x1, y1 := gridW, 0, 0
	for _, c := range comps {
		if len(c)*100 < len(comps[0])*15 {
			continue // bruit : < 15 % de la plus grande pièce
		}
		for _, i := range c {
			foot[i] = true
			x, y := i%gridW, i/gridW
			x0, x1, y1 = min(x0, x), max(x1, x), max(y1, y)
		}
	}
	d.footprint = foot
	const margin = 4
	d.band.x0, d.band.x1, d.band.y1 = max(0, x0-margin), min(gridW-1, x1+margin), min(gridH-1, y1+margin)
	d.footLayer = ob.Layer
}

// components renvoie les composantes connexes (4-voisinage) triées par taille décroissante.
func components(m *mask) [][]int {
	seen := &mask{}
	var out [][]int
	for i := range m {
		if !m[i] || seen[i] {
			continue
		}
		var comp []int
		stack := []int{i}
		seen[i] = true
		for len(stack) > 0 {
			c := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			comp = append(comp, c)
			x, y := c%gridW, c/gridW
			for _, nb := range [4][2]int{{x - 1, y}, {x + 1, y}, {x, y - 1}, {x, y + 1}} {
				if nb[0] < 0 || nb[0] >= gridW || nb[1] < 0 || nb[1] >= gridH {
					continue
				}
				j := nb[1]*gridW + nb[0]
				if m[j] && !seen[j] {
					seen[j] = true
					stack = append(stack, j)
				}
			}
		}
		out = append(out, comp)
	}
	sort.Slice(out, func(a, b int) bool { return len(out[a]) > len(out[b]) })
	return out
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
