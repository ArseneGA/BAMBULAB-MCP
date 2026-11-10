package camera

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer/sim"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

func startProgressRig(t *testing.T) (*rig, context.Context) {
	t.Helper()
	r := newRig(t, "p1s", nil)
	// détection d'échec coupée : seul le journal photo travaille
	f := false
	if _, err := r.svc.UpdateSettings(context.Background(), SettingsPatch{Enabled: &f}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.svc.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return r, ctx
}

func waitPalier(t *testing.T, r *rig, pct int) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for r.svc.paliers(context.Background(), "pj_1", pct) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("palier %d %% jamais consigné", pct)
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func percents(ph []ProgressPhoto) []int {
	out := []int{}
	for _, p := range ph {
		out = append(out, p.Percent)
	}
	return out
}

// CA10.5 : impression simulée menée à 100 % → 10 photos (10, 20, … 100 %), chacune avec son palier ; le 100 % est la photo
// de fin existante (kind final), sans doublon.
func TestProgressPhotosOnePerPalierUpTo100(t *testing.T) {
	r, _ := startProgressRig(t)
	ctx := context.Background()
	for p := 1; p <= 99; p++ {
		r.be.pct.Store(int64(p))
		if p%10 == 0 && p <= 90 {
			waitPalier(t, r, p)
		}
		time.Sleep(2 * time.Millisecond)
	}
	r.svc.AttachEvents(func(fn func(PrintEvent)) {
		r.be.setState(contracts.PrintCompleted)
		r.be.pct.Store(100)
		fn(PrintEvent{JobID: "pj_1", PrinterID: "pr_1", To: contracts.PrintCompleted})
	})
	deadline := time.Now().Add(8 * time.Second)
	for r.svc.paliers(ctx, "pj_1", 100) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("palier 100 % jamais consigné")
		}
		time.Sleep(15 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond) // aucune rafale ni doublon après coup
	ph, err := r.svc.ProgressPhotos(ctx, "pj_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(ph) != 10 {
		t.Fatalf("10 paliers attendus, obtenu %v", percents(ph))
	}
	for i, p := range ph {
		if p.Percent != (i+1)*10 || p.Status != ProgressOK || p.URL == "" || p.SHA256 == "" {
			t.Fatalf("palier %d : %+v", i, p)
		}
		if _, err := r.svc.OpenPhoto(ctx, p.SHA256); err != nil {
			t.Fatalf("blob du palier %d : %v", p.Percent, err)
		}
	}
	if ph[9].Kind != "final" || ph[0].Kind != "progress" {
		t.Fatalf("100 %% = photo de fin : %+v / %+v", ph[9], ph[0])
	}
	var n int
	_ = r.st.DB().QueryRow(`SELECT COUNT(*) FROM camera_photos WHERE print_job_id='pj_1'`).Scan(&n)
	if n != 10 {
		t.Fatalf("%d lignes pour l'impression, 10 attendues (pas de doublon)", n)
	}
	if r.be.stopReqs.Load() != 0 {
		t.Fatal("le journal photo ne doit rien demander à l'imprimante")
	}
	// les photos de palier sont redimensionnées : largeur bornée
	b, _ := r.svc.OpenPhoto(ctx, ph[0].SHA256)
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width > 1280 {
		t.Fatalf("%v %+v", err, cfg)
	}
}

// CA10.6 : caméra en panne au palier 40 % → l'impression continue, le palier 40 % est « unavailable » avec sa raison, les
// autres paliers sont présents.
func TestProgressCameraFailureIsNeverBlocking(t *testing.T) {
	r, _ := startProgressRig(t)
	ctx := context.Background()
	clearCache := func() {
		r.svc.mu.Lock()
		r.svc.lastFrame = map[string]Frame{}
		r.svc.mu.Unlock()
	}
	for _, p := range []int{10, 20, 30} {
		r.be.pct.Store(int64(p))
		waitPalier(t, r, p)
	}
	_ = r.cam.SetFault(sim.CameraFaultDown, true)
	clearCache()
	r.be.pct.Store(40)
	waitPalier(t, r, 40)
	_ = r.cam.SetFault(sim.CameraFaultDown, false)
	for _, p := range []int{50, 60} {
		r.be.pct.Store(int64(p))
		waitPalier(t, r, p)
	}
	ph, _ := r.svc.ProgressPhotos(ctx, "pj_1")
	if len(ph) != 6 {
		t.Fatalf("%v", percents(ph))
	}
	for _, p := range ph {
		switch {
		case p.Percent == 40 && (p.Status != ProgressUnavailable || p.Reason == "" || p.URL != "" || p.SHA256 != ""):
			t.Fatalf("palier 40 %% : %+v", p)
		case p.Percent != 40 && p.Status != ProgressOK:
			t.Fatalf("palier %d : %+v", p.Percent, p)
		}
	}
	if len(r.svc.mons) < 0 || r.be.job().State != contracts.PrintPrinting {
		t.Fatal("l'impression doit continuer")
	}
	// un palier indisponible ne fait pas apparaître de photo vide dans la liste classique
	if l, _ := r.svc.PhotosForJob(ctx, "pj_1"); len(l) != 5 {
		t.Fatalf("PhotosForJob : %d", len(l))
	}
}

// CA10.7 : un palier sauté (38 → 52 %) donne une seule photo, celle du palier 50 : ni doublon ni rafale.
func TestProgressSkippedPalierMakesNoBurst(t *testing.T) {
	r, _ := startProgressRig(t)
	for _, p := range []int{10, 20, 30} {
		r.be.pct.Store(int64(p) + 8)
		waitPalier(t, r, p)
	}
	r.be.pct.Store(52)
	waitPalier(t, r, 50)
	time.Sleep(200 * time.Millisecond)
	ph, _ := r.svc.ProgressPhotos(context.Background(), "pj_1")
	got := percents(ph)
	if len(got) != 4 || got[0] != 10 || got[1] != 20 || got[2] != 30 || got[3] != 50 {
		t.Fatalf("attendu 10,20,30,50 ; obtenu %v", got)
	}
	var n int
	_ = r.st.DB().QueryRow(`SELECT COUNT(*) FROM camera_photos WHERE print_job_id='pj_1'`).Scan(&n)
	if n != 4 {
		t.Fatalf("%d lignes", n)
	}
}

// Reprise après redémarrage : un palier déjà consigné n'est pas repris.
func TestProgressNoSecondAttemptAfterRestart(t *testing.T) {
	r := newRig(t, "p1s", nil)
	ctx := context.Background()
	if err := r.svc.insertProgress(ctx, "pr_1", "pj_1", "progress", 30, ProgressUnavailable, "caméra hors ligne", "", 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := r.svc.lastRecordedPalier(ctx, "pj_1"); got != 30 {
		t.Fatalf("%d", got)
	}
	r.svc.captureProgress(ctx, JobInfo{ID: "pj_1", PrinterID: "pr_1"}, 30, r.svc.Settings(ctx))
	if n := r.svc.paliers(ctx, "pj_1", 30); n != 1 {
		t.Fatalf("%d tentatives pour le même palier", n)
	}
}

func fakeBlob(t *testing.T, r *rig, size int) (string, int) {
	t.Helper()
	b := make([]byte, size)
	_, _ = rand.Read(b)
	sum := sha256.Sum256(b)
	sha := hex.EncodeToString(sum[:])
	if _, _, err := r.bl.Put(bytes.NewReader(b), sha); err != nil {
		t.Fatal(err)
	}
	return sha, size
}

func addProgress(t *testing.T, r *rig, job, kind string, pct int, age time.Duration, size int) string {
	t.Helper()
	sha, n := fakeBlob(t, r, size)
	at := time.Now().Add(-age)
	if err := r.svc.insertProgress(context.Background(), "pr_1", job, kind, pct, ProgressOK, "", sha, n, at); err != nil {
		t.Fatal(err)
	}
	return sha
}

func rowExists(r *rig, sha string) bool {
	var n int
	_ = r.st.DB().QueryRow(`SELECT COUNT(*) FROM camera_photos WHERE sha256=?`, sha).Scan(&n)
	return n > 0
}

// CA10.8 : rétention. Paliers intermédiaires purgés après la durée réglée ; photo 100 %, impression jugée success et photo
// référencée par une galerie conservées.
func TestProgressRetentionKeepsFinalSuccessAndReferenced(t *testing.T) {
	ref := ""
	r := newRig(t, "p1s", func(c *Config) {
		c.PhotoReferenced = func(_ context.Context, sha string) bool { return sha == ref }
	})
	ctx := context.Background()
	// la colonne verdict est posée par G9 PJ11 (migration 0300) : simulée ici
	if _, err := r.st.DB().Exec(`ALTER TABLE print_logs ADD COLUMN verdict TEXT NOT NULL DEFAULT ''`); err != nil {
		t.Fatal(err)
	}
	now := store.FormatTime(time.Now())
	if _, err := r.st.DB().Exec(`INSERT INTO print_logs(id,print_job_id,outcome,finished_at,doc,created_at,updated_at,verdict) VALUES('pl1','pj_ok','success',?,'{}',?,?,'success')`, now, now, now); err != nil {
		t.Fatal(err)
	}
	old := 100 * 24 * time.Hour
	oldMid := addProgress(t, r, "pj_old", "progress", 50, old, 2000)
	recent := addProgress(t, r, "pj_old", "progress", 60, 24*time.Hour, 2000)
	hundred := addProgress(t, r, "pj_old", "final", 100, old, 2000)
	success := addProgress(t, r, "pj_ok", "progress", 30, old, 2000)
	ref = addProgress(t, r, "pj_old", "progress", 20, old, 2000)
	n, err := r.svc.PurgeOldPhotos(ctx)
	if err != nil || n != 1 {
		t.Fatalf("purgées %d err %v", n, err)
	}
	if rowExists(r, oldMid) || r.bl.Has(oldMid) {
		t.Fatal("le palier intermédiaire ancien aurait dû être purgé (ligne et blob)")
	}
	for name, sha := range map[string]string{"récent": recent, "100 %": hundred, "success": success, "référencé": ref} {
		if !rowExists(r, sha) || !r.bl.Has(sha) {
			t.Fatalf("photo %s supprimée à tort", name)
		}
	}
	// rétention illimitée : plus rien n'est purgé
	zero := 0
	if _, err := r.svc.UpdateSettings(ctx, SettingsPatch{ProgressRetention: &zero, ProgressCapMB: &zero}); err != nil {
		t.Fatal(err)
	}
	if n, _ := r.svc.PurgeProgressPhotos(ctx); n != 0 {
		t.Fatalf("%d", n)
	}
}

// CA10.8 : plafond de volume respecté et signalé ; les plus anciens paliers intermédiaires partent d'abord.
func TestProgressCapPurgesOldestIntermediateFirstAndWarns(t *testing.T) {
	r := newRig(t, "p1s", nil)
	ctx := context.Background()
	one := 1
	if _, err := r.svc.UpdateSettings(ctx, SettingsPatch{ProgressCapMB: &one}); err != nil {
		t.Fatal(err)
	}
	const sz = 400 * 1024
	a := addProgress(t, r, "pj_a", "progress", 10, 5*time.Hour, sz)
	b := addProgress(t, r, "pj_a", "progress", 20, 4*time.Hour, sz)
	fin := addProgress(t, r, "pj_a", "final", 100, 6*time.Hour, sz) // le plus ancien, mais protégé
	c := addProgress(t, r, "pj_a", "progress", 30, time.Hour, sz)
	ps, err := r.svc.ProgressStorage(ctx)
	if err != nil || ps.Level != "full" || ps.Message == "" || ps.Photos != 4 {
		t.Fatalf("%+v %v", ps, err)
	}
	n, err := r.svc.PurgeProgressPhotos(ctx)
	if err != nil || n != 2 {
		t.Fatalf("purgées %d err %v", n, err)
	}
	if rowExists(r, a) || rowExists(r, b) {
		t.Fatal("les plus anciens paliers intermédiaires devaient partir")
	}
	if !rowExists(r, c) || !rowExists(r, fin) {
		t.Fatal("palier récent ou photo 100 % supprimé à tort")
	}
	ps, _ = r.svc.ProgressStorage(ctx)
	if ps.UsedBytes > ps.CapBytes {
		t.Fatalf("plafond dépassé après purge : %+v", ps)
	}
}

func TestProgressStorageWarnsNearCap(t *testing.T) {
	r := newRig(t, "p1s", nil)
	ctx := context.Background()
	two := 2
	if _, err := r.svc.UpdateSettings(ctx, SettingsPatch{ProgressCapMB: &two}); err != nil {
		t.Fatal(err)
	}
	addProgress(t, r, "pj_a", "progress", 10, time.Hour, 1700*1024) // 83 % de 2 Mo
	ps, _ := r.svc.ProgressStorage(ctx)
	if ps.Level != "warning" || ps.Message == "" {
		t.Fatalf("%+v", ps)
	}
}

func TestResizeJPEGBoundsWidthAndNeverEnlarges(t *testing.T) {
	mk := func(w, h int) []byte {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				img.Set(x, y, color.RGBA{uint8(x), uint8(y), 90, 255})
			}
		}
		var b bytes.Buffer
		_ = jpeg.Encode(&b, img, nil)
		return b.Bytes()
	}
	out, err := ResizeJPEG(mk(1920, 1080), 1280, 80)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := jpeg.DecodeConfig(bytes.NewReader(out))
	if cfg.Width != 1280 || cfg.Height != 720 {
		t.Fatalf("%+v", cfg)
	}
	out, _ = ResizeJPEG(mk(640, 360), 1280, 80)
	cfg, _ = jpeg.DecodeConfig(bytes.NewReader(out))
	if cfg.Width != 640 {
		t.Fatalf("agrandissement : %+v", cfg)
	}
	if _, err := ResizeJPEG(nil, 1280, 80); err == nil {
		t.Fatal("image vide : erreur attendue")
	}
}
