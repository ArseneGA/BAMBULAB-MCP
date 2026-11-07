package camera

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

func countPhotos(t *testing.T, r *rig, where string) int {
	t.Helper()
	var n int
	if err := r.st.DB().QueryRow(`SELECT COUNT(*) FROM camera_photos WHERE ` + where).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Un instantané à la demande est renvoyé mais jamais enregistré (ni ligne, ni blob servi).
func TestOnDemandSnapshotIsNotPersisted(t *testing.T) {
	r := newRig(t, "p1s", nil)
	ctx := context.Background()
	p, err := r.svc.Snapshot(ctx, "pr_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.JPEG) == 0 || p.URL != "" || p.Width != 640 || p.Height != 360 {
		t.Fatalf("instantané : url=%q %dx%d taille=%d", p.URL, p.Width, p.Height, len(p.JPEG))
	}
	if n := countPhotos(t, r, `1=1`); n != 0 {
		t.Fatalf("%d photo(s) enregistrée(s) pour un instantané", n)
	}
	if r.bl.Has(p.SHA256) {
		t.Fatal("le contenu de l'instantané a été écrit dans le magasin de blobs")
	}
	if _, err := r.svc.OpenPhoto(ctx, p.SHA256); !errors.Is(err, ErrNotFound) {
		t.Fatalf("instantané servi : %v", err)
	}
	// la photo de fin et celle d'alerte, elles, restent enregistrées
	if _, err := r.svc.Capture(ctx, "pr_1", "final", "pj_1", time.Second); err != nil {
		t.Fatal(err)
	}
	if n := countPhotos(t, r, `kind='final'`); n != 1 {
		t.Fatalf("photo de fin : %d", n)
	}
}

// Limite de débit par imprimante : au-delà de la rafale, 429 côté API ; une autre imprimante n'est pas affectée.
func TestSnapshotAndStreamAreRateLimitedPerPrinter(t *testing.T) {
	r := newRig(t, "p1s", func(c *Config) { c.RateBurst = 3; c.RateEvery = time.Hour; c.MaxViewers = 2 })
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := r.svc.Snapshot(ctx, "pr_1"); err != nil {
			t.Fatalf("instantané %d : %v", i, err)
		}
	}
	if _, err := r.svc.Snapshot(ctx, "pr_1"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("4e instantané : %v", err)
	}
	if _, err := r.svc.AnalyzeLatest(ctx, "pr_1"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("analyse : %v", err)
	}
	var open []*Stream
	for i := 0; i < 2; i++ {
		st, err := r.svc.Stream(ctx, "pr_1")
		if err != nil {
			t.Fatalf("flux %d : %v", i, err)
		}
		open = append(open, st)
	}
	if _, err := r.svc.Stream(ctx, "pr_1"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("3e spectateur : %v", err)
	}
	for _, st := range open {
		st.Stop()
	}
}

// Les photos d'alerte anciennes sont purgées (ligne, contenu, lien de l'alerte) ; la photo de fin reste.
func TestOldAlertPhotosArePurgedButFinalPhotosKept(t *testing.T) {
	r := newRig(t, "p1s", nil)
	ctx := context.Background()
	alert, err := r.svc.Capture(ctx, "pr_1", "alert", "pj_1", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	final, err := r.svc.Capture(ctx, "pr_1", "final", "pj_1", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.st.DB().Exec(`INSERT INTO camera_alerts(id,printer_id,print_job_id,at,score,level,report,photo_sha256) VALUES('al_1','pr_1','pj_1',?,0.9,'alert','{}',?)`,
		store.FormatTime(time.Now().UTC().AddDate(0, 0, -200)), alert.SHA256); err != nil {
		t.Fatal(err)
	}
	old := store.FormatTime(time.Now().UTC().AddDate(0, 0, -200))
	if _, err := r.st.DB().Exec(`UPDATE camera_photos SET taken_at=?`, old); err != nil {
		t.Fatal(err)
	}
	// rétention à 0 : rien n'est supprimé
	zero := 0
	if _, err := r.svc.UpdateSettings(ctx, SettingsPatch{PhotoRetention: &zero}); err != nil {
		t.Fatal(err)
	}
	if n, _ := r.svc.PurgeOldPhotos(ctx); n != 0 || countPhotos(t, r, `kind='alert'`) != 1 {
		t.Fatalf("rétention illimitée : purge de %d", n)
	}
	days := 30
	if _, err := r.svc.UpdateSettings(ctx, SettingsPatch{PhotoRetention: &days}); err != nil {
		t.Fatal(err)
	}
	bad := 5000
	if _, err := r.svc.UpdateSettings(ctx, SettingsPatch{PhotoRetention: &bad}); err == nil {
		t.Fatal("rétention de 5000 jours acceptée")
	}
	n, err := r.svc.PurgeOldPhotos(ctx)
	if err != nil || n != 1 {
		t.Fatalf("purge : %d %v", n, err)
	}
	if countPhotos(t, r, `kind='alert'`) != 0 {
		t.Fatal("photo d'alerte ancienne conservée")
	}
	if alert.SHA256 != final.SHA256 && r.bl.Has(alert.SHA256) {
		t.Fatal("contenu de la photo d'alerte conservé")
	}
	// un contenu partagé avec la photo de fin ne doit surtout pas disparaître
	if countPhotos(t, r, `kind='final'`) != 1 || !r.bl.Has(final.SHA256) {
		t.Fatal("photo de fin supprimée")
	}
	var sha *string
	_ = r.st.DB().QueryRow(`SELECT photo_sha256 FROM camera_alerts WHERE id='al_1'`).Scan(&sha)
	if sha != nil {
		t.Fatal("l'alerte pointe encore vers une photo supprimée")
	}
}

// Une image de plus de 4096 x 4096 est refusée à l'en-tête, avant tout décodage complet.
func TestOversizedJPEGIsRefusedBeforeDecoding(t *testing.T) {
	var big bytes.Buffer
	if err := jpeg.Encode(&big, image.NewGray(image.Rect(0, 0, 5000, 100)), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := checkJPEG(big.Bytes()); err == nil {
		t.Fatal("5000x100 accepté")
	}
	if _, err := decodeJPEG(big.Bytes()); err == nil {
		t.Fatal("decodeJPEG a décodé une image trop large")
	}
	var ok bytes.Buffer
	_ = jpeg.Encode(&ok, image.NewGray(image.Rect(0, 0, 4096, 8)), nil)
	if cfg, err := checkJPEG(ok.Bytes()); err != nil || cfg.Width != 4096 {
		t.Fatalf("4096x8 : %v", err)
	}
	if _, err := checkJPEG([]byte("pas un jpeg")); err == nil {
		t.Fatal("non-JPEG accepté")
	}
}
