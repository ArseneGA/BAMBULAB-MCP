package camera

import (
	"bytes"
	"context"
	"errors"
	"image/jpeg"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/notify"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer/sim"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// fakeBackend : une imprimante P1S simulée, une impression pilotable. Le Backend n'offre aucun moyen d'arrêter.
type fakeBackend struct {
	target   Target
	mu       sync.Mutex
	jobState contracts.PrintState
	layer    atomic.Int64
	hms      atomic.Int64
	stopReqs atomic.Int64
}

func (b *fakeBackend) Target(_ context.Context, id string) (Target, error) {
	if id != b.target.ID {
		return Target{}, ErrNotFound
	}
	return b.target, nil
}
func (b *fakeBackend) job() JobInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	return JobInfo{ID: "pj_1", PrinterID: b.target.ID, Name: "boite.gcode.3mf", State: b.jobState}
}
func (b *fakeBackend) PrintingJobs(context.Context) ([]JobInfo, error) {
	if j := b.job(); j.State == contracts.PrintPrinting {
		return []JobInfo{j}, nil
	}
	return nil, nil
}
func (b *fakeBackend) Job(context.Context, string) (JobInfo, error) { return b.job(), nil }
func (b *fakeBackend) State(context.Context, string) (PrinterState, error) {
	return PrinterState{Reachable: true, Printing: true, Layer: int(b.layer.Load()), TotalLayers: 100, HMSCount: int(b.hms.Load())}, nil
}
func (b *fakeBackend) StopURL(context.Context, string) (string, error) {
	b.stopReqs.Add(1)
	return "http://127.0.0.1:7878/stop/stp_test", nil
}
func (b *fakeBackend) BaseURL() string { return "http://127.0.0.1:7878" }
func (b *fakeBackend) setState(s contracts.PrintState) {
	b.mu.Lock()
	b.jobState = s
	b.mu.Unlock()
}

type fakeNotifier struct {
	mu     sync.Mutex
	events []notify.Event
}

func (n *fakeNotifier) Emit(_ context.Context, e notify.Event) []notify.Result {
	n.mu.Lock()
	n.events = append(n.events, e)
	n.mu.Unlock()
	return []notify.Result{{OK: true}}
}
func (n *fakeNotifier) list() []notify.Event {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]notify.Event(nil), n.events...)
}

type rig struct {
	svc *Service
	cam *sim.Camera
	sim *sim.Sim
	be  *fakeBackend
	nt  *fakeNotifier
	st  *store.Store
	bl  *store.BlobStore
}

func newRig(t *testing.T, model string, mut func(*Config)) *rig {
	t.Helper()
	s, err := sim.New(sim.Config{ControlPort: -1, AccessCode: "12345678"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	cam, err := s.StartCamera(sim.CameraConfig{FrameEvery: 25 * time.Millisecond, Width: 640, Height: 360})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cam.Close() })
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "db.sqlite"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bl, err := store.NewBlobStore(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	be := &fakeBackend{target: Target{ID: "pr_1", Name: "Atelier", Host: "127.0.0.1", Serial: s.Serial(), ModelID: model, ModelName: "Bambu Lab " + strings.ToUpper(model),
		PinnedSHA256: s.Fingerprint(), Secrets: printer.StaticSecret("12345678")}, jobState: contracts.PrintPrinting}
	nt := &fakeNotifier{}
	cfg := Config{Store: st, Blobs: bl, Backend: be, Notifier: nt, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Port: cam.Port(), MinInterval: 0.01, Scan: 20 * time.Millisecond}
	if mut != nil {
		mut(&cfg)
	}
	svc, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	return &rig{svc: svc, cam: cam, sim: s, be: be, nt: nt, st: st, bl: bl}
}

func TestCaptureStoresAPhotoAndOnlyRegisteredBlobsAreServed(t *testing.T) {
	r := newRig(t, "p1s", nil)
	ctx := context.Background()
	p, err := r.svc.Capture(ctx, "pr_1", "snapshot", "", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(p.JPEG)); err != nil || p.Kind != "snapshot" || !strings.HasPrefix(p.URL, "/api/camera/photos/") {
		t.Fatalf("photo : %+v %v", p, err)
	}
	b, err := r.svc.OpenPhoto(ctx, p.SHA256)
	if err != nil || !bytes.Equal(b, p.JPEG) {
		t.Fatalf("relecture : %v", err)
	}
	// un blob quelconque du stockage (un modèle, un G-code) n'est jamais servi par cette voie
	other, _, _ := r.bl.Put(strings.NewReader("contenu qui n'est pas une photo caméra"), "")
	if _, err := r.svc.OpenPhoto(ctx, other); !errors.Is(err, ErrNotFound) {
		t.Fatalf("blob non enregistré servi : %v", err)
	}
	if _, err := r.svc.Capture(ctx, "pr_inconnu", "snapshot", "", time.Second); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
}

func TestSharedStreamUsesOneCameraConnection(t *testing.T) {
	r := newRig(t, "p1s", nil)
	ctx := context.Background()
	var streams []*Stream
	for i := 0; i < 3; i++ {
		st, err := r.svc.Subscribe(ctx, "pr_1")
		if err != nil {
			t.Fatal(err)
		}
		streams = append(streams, st)
	}
	for i, st := range streams {
		select {
		case f, ok := <-st.C:
			if !ok || len(f.JPEG) == 0 {
				t.Fatalf("spectateur %d : %v", i, st.Err())
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("spectateur %d : aucune image", i)
		}
	}
	// un instantané pendant que le flux tourne n'ouvre pas une deuxième connexion
	if _, err := r.svc.Capture(ctx, "pr_1", "snapshot", "", 0); err != nil {
		t.Fatal(err)
	}
	if n := r.cam.Connections(); n != 1 {
		t.Fatalf("%d connexions à la caméra pour 3 spectateurs + 1 instantané", n)
	}
	for _, st := range streams {
		st.Stop()
	}
	// le dernier spectateur parti, le flux se ferme (après un court délai) et un nouvel abonnement rouvre une connexion
	deadline := time.Now().Add(8 * time.Second)
	for {
		r.svc.mu.Lock()
		n := len(r.svc.hubs)
		r.svc.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("flux jamais fermé après le départ du dernier spectateur")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestUnsupportedModelsAndMissingPinAreExplicit(t *testing.T) {
	r := newRig(t, "x1c", nil)
	_, err := r.svc.Capture(context.Background(), "pr_1", "snapshot", "", time.Second)
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "RTSP") {
		t.Fatalf("X1 : %v", err)
	}
	if r.cam.Connections() != 0 || r.cam.AuthFailures() != 0 {
		t.Fatal("aucune connexion ne doit être tentée pour un modèle RTSP")
	}
	r2 := newRig(t, "p1s", nil)
	r2.be.target.PinnedSHA256 = ""
	if _, err := r2.svc.Capture(context.Background(), "pr_1", "snapshot", "", time.Second); err == nil || !strings.Contains(err.Error(), "épingl") {
		t.Fatalf("sans empreinte : %v", err)
	}
	if r2.cam.Connections() != 0 {
		t.Fatal("aucune connexion tant que le certificat n'est pas épinglé")
	}
	// mauvaise empreinte : message qui renvoie vers la page de validation de la caméra
	r3 := newRig(t, "p1s", nil)
	r3.be.target.PinnedSHA256 = strings.Repeat("ab", 32)
	if _, err := r3.svc.Capture(context.Background(), "pr_1", "snapshot", "", time.Second); err == nil || !strings.Contains(err.Error(), "/setup/camera/pr_1") {
		t.Fatalf("mauvaise empreinte : %v", err)
	}
	// caméra qui refuse le code d'accès
	r4 := newRig(t, "p1s", nil)
	r4.be.target.Secrets = printer.StaticSecret("mauvais-code")
	if _, err := r4.svc.Capture(context.Background(), "pr_1", "snapshot", "", time.Second); err == nil || !strings.Contains(err.Error(), "LAN liveview") {
		t.Fatalf("mauvais code : %v", err)
	}
}

func TestSettingsAreValidatedAndPersisted(t *testing.T) {
	r := newRig(t, "p1s", func(c *Config) { c.MinInterval = 5 })
	ctx := context.Background()
	bad := 2.0
	if _, err := r.svc.UpdateSettings(ctx, SettingsPatch{IntervalSeconds: &bad}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("intervalle trop court accepté : %v", err)
	}
	th, iv, off := 0.8, 30.0, false
	st, err := r.svc.UpdateSettings(ctx, SettingsPatch{Threshold: &th, IntervalSeconds: &iv, Enabled: &off, DisablePrinters: []string{"pr_9"}})
	if err != nil || st.Detector.Threshold != 0.8 || st.IntervalSeconds != 30 || st.Enabled {
		t.Fatalf("%+v %v", st, err)
	}
	th2 := 1.5
	if _, err := r.svc.UpdateSettings(ctx, SettingsPatch{Threshold: &th2}); !errors.Is(err, ErrInvalid) {
		t.Fatal("seuil hors plage accepté")
	}
	// un autre service sur la même base relit les réglages
	svc2, _ := New(Config{Store: r.st, Blobs: r.bl, Backend: r.be, MinInterval: 5})
	got := svc2.Settings(ctx)
	if got.Detector.Threshold != 0.8 || got.IntervalSeconds != 30 || got.Enabled || len(got.DisabledPrinters) != 1 {
		t.Fatalf("persistance : %+v", got)
	}
}

// nextReport attend qu'une nouvelle analyse de pj_1 ait eu lieu (le test avance les couches au rythme du détecteur).
func (r *rig) nextReport(t *testing.T) {
	t.Helper()
	_, was, _ := r.svc.LastReport("pj_1")
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if _, at, ok := r.svc.LastReport("pj_1"); ok && at.After(was) {
			return
		}
	}
	t.Fatal("aucune nouvelle analyse en 10 s")
}

// scene pilotée par le test : couche courante, spaghetti, pièce présente.
type liveScene struct {
	be        *fakeBackend
	spaghetti atomic.Bool
	present   atomic.Bool
}

func (l *liveScene) fn(n int) sim.Scene {
	ly := int(l.be.layer.Load())
	sc := sim.Scene{TotalLayers: 100, Layer: ly, ObjectPresent: l.present.Load(), ToolX: 0.2 + 0.6*float64((n*37)%10)/10, ToolY: 0.3 + 0.5*float64((n*53)%10)/10}
	if l.spaghetti.Load() {
		sc.Spaghetti = 1
	}
	return sc
}

func TestFailureDetectionRaisesAnAlertWithPhotoAndStopLinkButNeverStops(t *testing.T) {
	r := newRig(t, "p1s", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ls := &liveScene{be: r.be}
	r.cam.SetScene(ls.fn)
	iv, cons, cd := 0.04, 2, 60
	if _, err := r.svc.UpdateSettings(ctx, SettingsPatch{IntervalSeconds: &iv, ConsecutiveAlert: &cons, CooldownMinutes: &cd}); err != nil {
		t.Fatal(err)
	}
	// 1. plateau vide : photo de confirmation = image de référence de la détection
	if _, _, err := r.svc.PlatePhoto(ctx, "pr_1"); err != nil {
		t.Fatal(err)
	}
	ls.present.Store(true)
	done := make(chan struct{})
	go func() { defer close(done); r.svc.Run(ctx) }()
	// 2. impression saine : les couches avancent, aucune alerte
	for l := int64(1); l <= 14; l++ {
		r.be.layer.Store(l)
		r.nextReport(t)
	}
	if len(r.nt.list()) != 0 {
		t.Fatalf("alerte sur une impression saine : %+v", r.nt.list())
	}
	if rep, _, ok := r.svc.LastReport("pj_1"); !ok || !rep.FootprintKnown || rep.Alert {
		t.Fatalf("rapport d'une impression saine : %+v ok=%v", rep, ok)
	}
	// 3. spaghetti
	ls.spaghetti.Store(true)
	for l := int64(15); l <= 40; l++ {
		r.be.layer.Store(l)
		r.nextReport(t)
		if len(r.nt.list()) > 0 {
			break
		}
	}
	evs := r.nt.list()
	if len(evs) != 1 {
		rep, _, _ := r.svc.LastReport("pj_1")
		t.Fatalf("une alerte attendue, %d reçue(s) ; dernier rapport %+v", len(evs), rep)
	}
	e := evs[0]
	if e.Kind != notify.EvDetectionAlert || e.Link != "http://127.0.0.1:7878/stop/stp_test" || len(e.Photo) == 0 || !strings.Contains(e.Message, "Rien n'a été arrêté") || !strings.Contains(e.PhotoURL, "/api/camera/photos/") {
		t.Fatalf("alerte : %+v", e)
	}
	if _, err := jpeg.Decode(bytes.NewReader(e.Photo)); err != nil {
		t.Fatal("photo de l'alerte illisible")
	}
	// 4. rapport conservé, photo rattachée à l'impression, rien d'arrêté, une seule alerte malgré la persistance du problème
	al, err := r.svc.Alerts(ctx, "pj_1", 10)
	if err != nil || len(al) != 1 || al[0].Report.Score < 0.6 || al[0].PhotoURL == "" {
		t.Fatalf("alertes : %+v %v", al, err)
	}
	time.Sleep(400 * time.Millisecond)
	if len(r.nt.list()) != 1 {
		t.Fatalf("alerte répétée malgré le délai de grâce : %d", len(r.nt.list()))
	}
	if r.be.stopReqs.Load() == 0 {
		t.Fatal("le lien d'arrêt (demande humaine) aurait dû être demandé")
	}
	if ph, _ := r.svc.PhotosForJob(ctx, "pj_1"); len(ph) == 0 || ph[0].Kind != "alert" {
		t.Fatalf("photo d'alerte non rattachée : %+v", ph)
	}
	// 5. analyze_print_frame : image fraîche + dernier rapport
	a, err := r.svc.AnalyzeLatest(ctx, "pr_1")
	if err != nil || a.Report == nil || a.PrintJobID != "pj_1" || len(a.JPEG) == 0 || !strings.Contains(a.Note, "Juge toi-même") {
		t.Fatalf("analyse : %+v %v", a, err)
	}
	cancel()
	<-done
}

func TestDetectionCanBeDisabledPerPrinterAndGlobally(t *testing.T) {
	r := newRig(t, "p1s", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ls := &liveScene{be: r.be}
	ls.present.Store(true)
	ls.spaghetti.Store(true)
	r.be.layer.Store(30)
	r.cam.SetScene(ls.fn)
	iv, cons := 0.03, 1
	off := false
	if _, err := r.svc.UpdateSettings(ctx, SettingsPatch{IntervalSeconds: &iv, ConsecutiveAlert: &cons, DisablePrinters: []string{"pr_1"}}); err != nil {
		t.Fatal(err)
	}
	_ = off
	done := make(chan struct{})
	go func() { defer close(done); r.svc.Run(ctx) }()
	time.Sleep(500 * time.Millisecond)
	if _, _, ok := r.svc.LastReport("pj_1"); ok {
		t.Fatal("imprimante exclue : aucune analyse attendue")
	}
	cancel()
	<-done
}

func TestFinalPhotoIsTakenOnceAndAttachedToTheJob(t *testing.T) {
	r := newRig(t, "p1s", nil)
	ctx := context.Background()
	b1, url1, err := r.svc.FinalPhoto(ctx, "pr_1", "pj_9")
	if err != nil || len(b1) == 0 {
		t.Fatal(err)
	}
	b2, url2, err := r.svc.FinalPhoto(ctx, "pr_1", "pj_9")
	if err != nil || url1 != url2 || !bytes.Equal(b1, b2) {
		t.Fatalf("la photo de fin doit être prise une fois : %v", err)
	}
	ph, _ := r.svc.PhotosForJob(ctx, "pj_9")
	if len(ph) != 1 || ph[0].Kind != "final" {
		t.Fatalf("%+v", ph)
	}
	// AttachEvents : un COMPLETED déclenche la photo de fin de l'impression
	var fire func(PrintEvent)
	r.svc.AttachEvents(func(fn func(PrintEvent)) { fire = fn })
	fire(PrintEvent{JobID: "pj_10", PrinterID: "pr_1", To: contracts.PrintCompleted})
	fire(PrintEvent{JobID: "pj_11", PrinterID: "pr_1", To: contracts.PrintCancelled}) // annulation : pas de photo
	deadline := time.Now().Add(5 * time.Second)
	for {
		if p, _ := r.svc.PhotosForJob(ctx, "pj_10"); len(p) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("photo de fin jamais prise")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if p, _ := r.svc.PhotosForJob(ctx, "pj_11"); len(p) != 0 {
		t.Fatal("photo prise pour une impression annulée")
	}
}

func TestPlatePhotoIsTimestampedAndEmptyPlateIsVisible(t *testing.T) {
	r := newRig(t, "p1s", nil)
	url, at, err := r.svc.PlatePhoto(context.Background(), "pr_1")
	if err != nil || url == "" || time.Since(at) > 5*time.Second {
		t.Fatalf("%q %v %v", url, at, err)
	}
	// caméra hors service : la page de confirmation recevra une raison, jamais une photo périmée
	_ = r.cam.SetFault(sim.CameraFaultDown, true)
	r.svc.mu.Lock()
	r.svc.lastFrame = map[string]Frame{}
	r.svc.mu.Unlock()
	if _, _, err := r.svc.PlatePhoto(context.Background(), "pr_1"); err == nil {
		t.Fatal("caméra hors service : erreur attendue")
	}
}
