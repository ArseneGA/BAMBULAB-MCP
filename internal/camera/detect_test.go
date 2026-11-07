package camera

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer/sim"
)

// jpegImage passe par un vrai JPEG (YCbCr, artefacts de compression) comme le flux de la caméra.
func jpegImage(t testing.TB, sc sim.Scene) image.Image {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, sim.RenderScene(sc), &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(&b)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

var t0 = time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)

type run struct {
	t   *testing.T
	d   *Detector
	n   int
	end time.Time
}

func newRun(t *testing.T, cfg DetectorConfig) *run {
	r := &run{t: t, d: NewDetector(cfg), end: t0}
	r.d.SetReference(jpegImage(t, sim.Scene{Width: 640, Height: 360, TotalLayers: 100, ToolX: -1, Seed: 1}))
	return r
}

// step observe un instantané à la couche donnée, toutes les 60 s.
func (r *run) step(layer int, mut func(*sim.Scene), ob func(*Observation)) Report {
	r.n++
	r.end = r.end.Add(60 * time.Second)
	sc := sim.Scene{Width: 640, Height: 360, TotalLayers: 100, Layer: layer, ObjectPresent: true, Seed: int64(r.n + 10),
		ToolX: 0.2 + 0.6*float64((r.n*37)%10)/10, ToolY: 0.3 + 0.5*float64((r.n*53)%10)/10}
	if mut != nil {
		mut(&sc)
	}
	o := Observation{At: r.end, Layer: layer, TotalLayers: 100, Printing: true}
	if ob != nil {
		ob(&o)
	}
	return r.d.Observe(jpegImage(r.t, sc), o)
}

func sig(rep Report, name string) Signal {
	for _, s := range rep.Signals {
		if s.Name == name {
			return s
		}
	}
	return Signal{Name: "absent"}
}

func TestHealthyPrintStaysQuiet(t *testing.T) {
	r := newRun(t, DetectorConfig{})
	for layer := 1; layer <= 60; layer += 3 {
		rep := r.step(layer, nil, nil)
		if rep.Alert || rep.Score >= 0.3 {
			t.Fatalf("couche %d : score %.2f alerte=%v signaux=%+v notes=%v", layer, rep.Score, rep.Alert, rep.Signals, rep.Notes)
		}
	}
	rep := r.step(61, nil, nil)
	if !rep.FootprintKnown || !rep.ReferenceSet {
		t.Fatalf("empreinte non apprise : %+v", rep)
	}
}

func TestSpaghettiRaisesAlertWithExplanation(t *testing.T) {
	r := newRun(t, DetectorConfig{})
	for layer := 2; layer <= 14; layer += 3 {
		r.step(layer, nil, nil)
	}
	var rep Report
	for layer := 17; layer <= 29; layer += 3 {
		rep = r.step(layer, func(s *sim.Scene) { s.Spaghetti = 1 }, nil)
	}
	if !rep.Alert || rep.Level != "alert" {
		t.Fatalf("spaghetti non détecté : %+v", rep)
	}
	off := sig(rep, "off_footprint")
	if off.Score < 0.8 || off.Detail == "" || off.Contribution <= 0 {
		t.Fatalf("explication du signal : %+v", off)
	}
}

func TestDetachedPartRaisesAlert(t *testing.T) {
	r := newRun(t, DetectorConfig{})
	for layer := 2; layer <= 14; layer += 3 {
		r.step(layer, nil, nil)
	}
	var rep Report
	for layer := 17; layer <= 26; layer += 3 {
		rep = r.step(layer, func(s *sim.Scene) { s.ObjectPresent = false }, nil)
	}
	if sig(rep, "footprint_loss").Score < 0.9 || !rep.Alert {
		t.Fatalf("pièce décollée non détectée : %+v", rep)
	}
}

func TestLightingChangeIsNotAFailure(t *testing.T) {
	r := newRun(t, DetectorConfig{})
	for layer := 2; layer <= 14; layer += 3 {
		r.step(layer, nil, nil)
	}
	var rep Report
	for layer := 17; layer <= 29; layer += 3 {
		rep = r.step(layer, func(s *sim.Scene) { s.Brightness = 0.25 }, nil)
	}
	if rep.Alert || rep.Score >= 0.3 {
		t.Fatalf("faux positif sur changement d'éclairage : %+v", rep)
	}
}

func TestPassingToolheadIsNotPersistent(t *testing.T) {
	r := newRun(t, DetectorConfig{})
	for layer := 2; layer <= 40; layer += 2 {
		rep := r.step(layer, func(s *sim.Scene) { s.ToolX, s.ToolY = 0.1+0.8*float64(layer%7)/7, 0.15+0.7*float64(layer%5)/5 }, nil)
		if rep.Alert {
			t.Fatalf("couche %d : alerte sur la tête qui passe : %+v", layer, rep)
		}
	}
}

func TestLayerStall(t *testing.T) {
	r := newRun(t, DetectorConfig{StallMinutes: 5})
	var rep Report
	for i := 0; i < 12; i++ {
		rep = r.step(30, nil, nil) // même couche pendant 12 min
	}
	st := sig(rep, "layer_stall")
	if st.Score < 0.6 || st.Value < 10 {
		t.Fatalf("arrêt de progression non vu : %+v", st)
	}
	// en pause : pas de stall
	r2 := newRun(t, DetectorConfig{StallMinutes: 5})
	for i := 0; i < 12; i++ {
		rep = r2.step(30, nil, func(o *Observation) { o.Printing = false })
	}
	if sig(rep, "layer_stall").Score != 0 {
		t.Fatalf("stall compté pendant une pause : %+v", rep.Signals)
	}
}

func TestPrinterErrorAloneIsWatchNotAlert(t *testing.T) {
	r := newRun(t, DetectorConfig{})
	rep := r.step(10, nil, func(o *Observation) { o.HMSCount = 1 })
	if rep.Alert || rep.Level != "watch" || sig(rep, "printer_error").Contribution != 0.5 {
		t.Fatalf("%+v", rep)
	}
	// HMS + pièce décollée => alerte
	r = newRun(t, DetectorConfig{})
	for layer := 2; layer <= 14; layer += 3 {
		r.step(layer, nil, nil)
	}
	for layer := 17; layer <= 26; layer += 3 {
		rep = r.step(layer, func(s *sim.Scene) { s.ObjectPresent = false }, func(o *Observation) { o.HMSCount = 2 })
	}
	if !rep.Alert {
		t.Fatalf("%+v", rep)
	}
}

func TestNoReferenceDisablesVisualAnalysis(t *testing.T) {
	d := NewDetector(DetectorConfig{})
	rep := d.Observe(jpegImage(t, sim.Scene{Width: 640, Height: 360, TotalLayers: 100, Layer: 5, ObjectPresent: true}), Observation{At: t0, Layer: 5, Printing: true})
	if rep.ReferenceSet || len(rep.Notes) == 0 || rep.Alert {
		t.Fatalf("%+v", rep)
	}
	for _, s := range rep.Signals {
		if s.Name == "off_footprint" || s.Name == "footprint_loss" {
			t.Fatalf("signal visuel sans référence : %+v", s)
		}
	}
}

func TestThresholdIsConfigurable(t *testing.T) {
	// seuil à 0.95 : le spaghetti (0.9 max sans autre signal) ne déclenche plus d'alerte
	r := newRun(t, DetectorConfig{Threshold: 0.95})
	for layer := 2; layer <= 14; layer += 3 {
		r.step(layer, nil, nil)
	}
	var rep Report
	for layer := 17; layer <= 29; layer += 3 {
		rep = r.step(layer, func(s *sim.Scene) { s.Spaghetti = 1 }, nil)
	}
	if rep.Alert || rep.Level != "watch" {
		t.Fatalf("%+v", rep)
	}
}

func TestSmallImageDoesNotPanic(t *testing.T) {
	d := NewDetector(DetectorConfig{})
	rep := d.Observe(image.NewRGBA(image.Rect(0, 0, 20, 20)), Observation{At: t0})
	if len(rep.Notes) == 0 {
		t.Fatal("note attendue")
	}
}
