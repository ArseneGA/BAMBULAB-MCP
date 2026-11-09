package mcpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/localtime"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// L'image rendue à la vue reste bornée même pour une image de caméra pleine de bruit (le pire cas pour un JPEG).
func TestLiveFrameIsBoundedEvenForNoisyImages(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	img := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	for y := 0; y < 1080; y++ {
		for x := 0; x < 1920; x++ {
			img.Set(x, y, color.RGBA{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), 255})
		}
	}
	var raw bytes.Buffer
	if err := jpeg.Encode(&raw, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	small, err := shrinkJPEGMax(raw.Bytes(), LiveFrameMaxWidth, 70, LiveFrameMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(small))
	if err != nil || cfg.Width > LiveFrameMaxWidth {
		t.Fatalf("dimensions : %+v %v", cfg, err)
	}
	if len(small) > LiveFrameMaxBytes {
		t.Fatalf("image trop lourde : %d octets (max %d)", len(small), LiveFrameMaxBytes)
	}
	if n := base64.StdEncoding.EncodedLen(len(small)); n > 100_000 {
		t.Fatalf("%d caractères base64 : trop pour un résultat d'outil", n)
	}
}

func TestEndLocalUsesTheConfiguredTimezone(t *testing.T) {
	t.Setenv("BAMBU_TIMEZONE", "Europe/Paris")
	localtime.Reload()
	t.Cleanup(localtime.Reload)
	now := time.Date(2026, 11, 9, 18, 0, 0, 0, time.UTC) // 19:00 à Paris (CET)
	if got := endLocal(time.Date(2026, 11, 9, 20, 5, 0, 0, time.UTC), now); got != "21:05" {
		t.Fatalf("même jour : %q", got)
	}
	if got := endLocal(time.Date(2026, 11, 10, 0, 30, 0, 0, time.UTC), now); got != "01:30 (10/11/2026)" {
		t.Fatalf("jour suivant : %q", got)
	}
	t.Setenv("BAMBU_TIMEZONE", "Europe/Paris")
	if got := fmtDuration(7500); got != "2 h 05 min" {
		t.Fatalf("durée : %q", got)
	}
}

func printingStatus(state contracts.PrintState, layer, total, remaining int) *printing.PrintStatus {
	return &printing.PrintStatus{Job: printing.Job{
		ID: "pj_1", State: state, PrinterID: "pr_1", Plate: 1,
		Summary: &printing.ArtifactSummary{Plates: []slicer.PlateResult{{Index: 1, MaxZ: 24.8, WeightGrams: 18.4}}},
		Print:   &printing.PrintProgress{Percent: 40, Layer: layer, TotalLayers: total, RemainingMin: remaining},
	}}
}

// Hauteur ≈ hauteur totale × couche / couches ; heure de fin = maintenant + temps restant ; tolérant aux champs manquants.
func TestBuildLiveStateDerivesHeightAndEnd(t *testing.T) {
	t.Setenv("BAMBU_TIMEZONE", "Europe/Paris")
	localtime.Reload()
	t.Cleanup(localtime.Reload)
	ctx := context.Background()
	c := &core.Core{}
	ls := buildLiveState(ctx, c, nil, printingStatus(contracts.PrintPrinting, 50, 124, 90), false)
	if ls.Terminal || ls.Final != nil || ls.CameraAvailable {
		t.Fatalf("état : %+v", ls)
	}
	if ls.HeightMM == nil || *ls.HeightMM != 10 || ls.HeightTotalMM == nil || *ls.HeightTotalMM != 24.8 {
		t.Fatalf("hauteur : %v / %v", ls.HeightMM, ls.HeightTotalMM)
	}
	if ls.EndsAtLocal == "" || ls.EndsAt.IsZero() {
		t.Fatalf("heure de fin absente : %+v", ls)
	}
	if d := time.Until(ls.EndsAt); d < 89*time.Minute || d > 91*time.Minute {
		t.Fatalf("fin dans %v, attendu ~90 min", d)
	}

	// rien de connu : aucune hauteur ni heure de fin inventée
	bare := &printing.PrintStatus{Job: printing.Job{ID: "pj_2", State: contracts.PrintPrinting}}
	ls = buildLiveState(ctx, c, nil, bare, false)
	if ls.HeightMM != nil || ls.EndsAtLocal != "" || !ls.EndsAt.IsZero() {
		t.Fatalf("champs inventés : %+v", ls)
	}
}

// Impression terminée : résumé final (durée réelle d'après les évènements, grammes estimés par la découpe à défaut du journal).
func TestBuildLiveStateFinalSummary(t *testing.T) {
	ctx := context.Background()
	st := printingStatus(contracts.PrintCompleted, 124, 124, 0)
	t0 := time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC)
	st.Job.Events = []printing.JobEvent{
		{At: t0.Add(-time.Hour), Event: "upload", To: contracts.PrintUploaded},
		{At: t0, Event: "start", To: contracts.PrintPrinting},
		{At: t0.Add(2*time.Hour + 5*time.Minute), Event: "done", To: contracts.PrintCompleted},
	}
	ls := buildLiveState(ctx, &core.Core{}, nil, st, true)
	if !ls.Terminal || ls.Final == nil {
		t.Fatalf("résumé final absent : %+v", ls)
	}
	f := ls.Final
	if f.Outcome != "completed" || f.DurationSeconds != 7500 || f.Duration != "2 h 05 min" || f.Grams != 18.4 || f.GramsBasis == "" {
		t.Fatalf("résumé : %+v", f)
	}
	if f.Photo != nil || f.PhotoNote == "" {
		t.Fatalf("sans caméra : pas de photo, une note : %+v", f)
	}
	if ls.EndsAtLocal != "" {
		t.Fatalf("une impression terminée n'a plus d'heure de fin : %q", ls.EndsAtLocal)
	}
	// échec : durée inconnue sans évènements, jamais inventée
	bad := printingStatus(contracts.PrintFailed, 30, 124, 0)
	bad.Job.Summary = nil
	lf := buildLiveState(ctx, &core.Core{}, nil, bad, false).Final
	if lf == nil || lf.Outcome != "failed" || lf.DurationSeconds != 0 || lf.Duration != "" || lf.Grams != 0 {
		t.Fatalf("échec : %+v", lf)
	}
}

func TestWantStatusPhotoDefaultsToPrinting(t *testing.T) {
	yes, no := true, false
	if !wantStatusPhoto(nil, printingStatus(contracts.PrintPrinting, 1, 2, 3)) {
		t.Fatal("défaut : photo pendant l'impression")
	}
	if wantStatusPhoto(nil, printingStatus(contracts.PrintCompleted, 1, 2, 3)) || wantStatusPhoto(nil, printingStatus(contracts.PrintValidated, 1, 2, 3)) {
		t.Fatal("défaut : pas de photo hors impression")
	}
	if wantStatusPhoto(&no, printingStatus(contracts.PrintPrinting, 1, 2, 3)) || !wantStatusPhoto(&yes, printingStatus(contracts.PrintCompleted, 1, 2, 3)) {
		t.Fatal("with_photo explicite prime")
	}
}
