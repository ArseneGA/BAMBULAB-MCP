package camera

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testJPEG(t *testing.T, seed int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 320, 180))
	for y := 0; y < 180; y++ {
		for x := 0; x < 320; x++ {
			img.Set(x, y, color.RGBA{uint8(x + seed), uint8(y * 2), uint8(seed * 9), 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

// Trois vues ouvertes en même temps : UNE capture, la même image pour tous ; après la fenêtre, une nouvelle.
func TestLiveFrameSharesOneCapturePerWindow(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)}
	r := newRig(t, "p1s", func(c *Config) { c.Now = clk.now; c.RateBurst = 100 })
	var snaps atomic.Int64
	r.be.target.Snap = func(ctx context.Context) ([]byte, error) {
		n := snaps.Add(1)
		time.Sleep(60 * time.Millisecond) // une capture lente : les autres appelants attendent, ne capturent pas
		return testJPEG(t, int(n)), nil
	}
	ctx := context.Background()
	var wg sync.WaitGroup
	shas := make([]string, 3)
	cached := make([]bool, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, c, err := r.svc.LiveFrame(ctx, "pr_1")
			if err != nil {
				t.Error(err)
				return
			}
			shas[i], cached[i] = p.SHA256, c
		}()
	}
	wg.Wait()
	if n := snaps.Load(); n != 1 {
		t.Fatalf("captures pour 3 appels concurrents : %d, attendu 1", n)
	}
	if shas[0] == "" || shas[0] != shas[1] || shas[1] != shas[2] {
		t.Fatalf("les trois appelants doivent recevoir la même image : %v", shas)
	}
	nc := 0
	for _, c := range cached {
		if c {
			nc++
		}
	}
	if nc != 2 {
		t.Fatalf("appels servis par la capture partagée : %d, attendu 2", nc)
	}

	clk.add(time.Second) // dans la fenêtre : toujours la même capture
	if _, c, err := r.svc.LiveFrame(ctx, "pr_1"); err != nil || !c || snaps.Load() != 1 {
		t.Fatalf("dans la fenêtre : cached=%v err=%v captures=%d", c, err, snaps.Load())
	}
	clk.add(LiveWindow) // fenêtre écoulée : une seule nouvelle capture
	p, c, err := r.svc.LiveFrame(ctx, "pr_1")
	if err != nil || c || snaps.Load() != 2 || p.SHA256 == shas[0] {
		t.Fatalf("après la fenêtre : cached=%v err=%v captures=%d", c, err, snaps.Load())
	}
	if p.Kind != "snapshot" || len(p.JPEG) == 0 || p.Width != 320 {
		t.Fatalf("photo : %+v", p)
	}
	if n := countPhotos(t, r, `1=1`); n != 0 {
		t.Fatalf("le suivi en direct ne doit rien enregistrer : %d photo(s)", n)
	}
}

// Une caméra en panne n'est pas martelée : l'échec est rendu tel quel pendant quelques secondes ; imprimante inconnue ou sans caméra lisible : refus.
func TestLiveFrameRefusesAndDoesNotHammerAFailingCamera(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)}
	r := newRig(t, "p1s", func(c *Config) { c.Now = clk.now; c.RateBurst = 100 })
	var snaps atomic.Int64
	r.be.target.Snap = func(context.Context) ([]byte, error) { snaps.Add(1); return nil, errors.New("liveview désactivé") }
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, _, err := r.svc.LiveFrame(ctx, "pr_1"); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("appel %d : %v", i, err)
		}
	}
	if n := snaps.Load(); n != 1 {
		t.Fatalf("une caméra en panne a été interrogée %d fois pour 3 appels", n)
	}
	clk.add(4 * time.Second)
	_, _, _ = r.svc.LiveFrame(ctx, "pr_1")
	if n := snaps.Load(); n != 2 {
		t.Fatalf("nouvel essai attendu après la fenêtre d'échec : %d", n)
	}
	if _, _, err := r.svc.LiveFrame(ctx, "pr_inconnu"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("imprimante inconnue : %v", err)
	}
	rx := newRig(t, "x1c", nil) // RTSP : non pris en charge
	rx.be.target.Snap = func(context.Context) ([]byte, error) { return testJPEG(t, 1), nil }
	if _, _, err := rx.svc.LiveFrame(ctx, "pr_1"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("modèle sans caméra JPEG : %v", err)
	}
}
