package printer_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer/sim"
	"github.com/spf13/afero"
)

const printerID = "p1s-test"

type env struct {
	t   *testing.T
	sim *sim.Sim
	ad  *printer.Bambu
	ver *verifier
}

// verifier : jetons à usage unique, comme le mécanisme de confirmation réel.
type verifier struct {
	mu    sync.Mutex
	valid map[string]bool
	calls int
}

func (v *verifier) verify(_ context.Context, p printer.ConfirmationProof) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls++
	if !v.valid[p.Token] {
		return errors.New("unknown or used token")
	}
	delete(v.valid, p.Token)
	return nil
}

func (v *verifier) issue(tok string) {
	v.mu.Lock()
	v.valid[tok] = true
	v.mu.Unlock()
}

func newEnv(t *testing.T, sc sim.Config, mod func(*printer.BambuConfig)) *env {
	t.Helper()
	sc.ControlPort = -1
	sc.Tick = 20 * time.Millisecond
	sc.HeartbeatInterval = 150 * time.Millisecond
	if sc.PrepareDuration == 0 {
		sc.PrepareDuration = 150 * time.Millisecond
	}
	if sc.PrintDuration == 0 {
		sc.PrintDuration = 900 * time.Millisecond
	}
	s, err := sim.New(sc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	v := &verifier{valid: map[string]bool{}}
	host, mport := splitAddr(t, s.MQTTAddr())
	_, fport := splitAddr(t, s.FTPAddr())
	cfg := printer.BambuConfig{
		PrinterID: printerID, Host: host, Serial: s.Serial(), MQTTPort: mport, FTPPort: fport,
		Secrets:             printer.StaticSecret(s.AccessCode()),
		TLS:                 printer.TLSOptions{PinnedSHA256: s.Fingerprint()},
		Verifier:            v.verify,
		ReconnectDelay:      100 * time.Millisecond,
		StartObserveTimeout: 5 * time.Second,
		PushAllWait:         2 * time.Second,
	}
	if mod != nil {
		mod(&cfg)
	}
	ad, err := printer.NewBambu(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := ad.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ad.Close() })
	return &env{t: t, sim: s, ad: ad, ver: v}
}

func splitAddr(t *testing.T, a string) (string, int) {
	t.Helper()
	i := strings.LastIndex(a, ":")
	var p int
	fmt.Sscan(a[i+1:], &p)
	return a[:i], p
}

type artefact struct {
	data []byte
	sha  string
	name string
}

func newArtefact(n int) artefact {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	sum := sha256.Sum256(b)
	return artefact{data: b, sha: hex.EncodeToString(sum[:]), name: "benchy.gcode.3mf"}
}

func (e *env) upload(a artefact) printer.RemoteRef {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ref, err := e.ad.Upload(ctx, bytes.NewReader(a.data), a.name, int64(len(a.data)), a.sha)
	if err != nil {
		e.t.Fatalf("upload: %v", err)
	}
	return ref
}

// request construit une demande avec une preuve valide et émet son jeton.
func (e *env) request(ref printer.RemoteRef, tok string) printer.StartRequest {
	e.ver.issue(tok)
	return printer.StartRequest{
		Remote: ref, Plate: 1, UseAMS: true, AMSMapping: []int{0, -1, -1, -1, -1}, PlateClear: true,
		Options: printer.DefaultStartOptions(),
		Proof: printer.ConfirmationProof{
			PrinterID: printerID, SHA256: ref.SHA256, Plate: 1, AMSMapping: []int{0, -1, -1, -1, -1},
			Token: tok, ExpiresAt: time.Now().Add(time.Minute),
		},
	}
}

func ctx5(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return c
}

func waitState(t *testing.T, ad printer.Adapter, want printer.State, d time.Duration) printer.Status {
	t.Helper()
	d += 10 * time.Second // marge pour les machines chargées ; on sort dès que l'état voulu est atteint
	deadline := time.Now().Add(d)
	var st printer.Status
	for time.Now().Before(deadline) {
		st, _ = ad.Status(context.Background())
		if st.State == want {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("state %q not reached (last %q)", want, st.State)
	return st
}

func TestStatusIdleAndFirmware(t *testing.T) {
	e := newEnv(t, sim.Config{}, nil)
	st, err := e.ad.Status(ctx5(t))
	if err != nil {
		t.Fatal(err)
	}
	// Le premier rapport peut être partiel ; attendons le pushall complet.
	st = waitState(t, e.ad, printer.StateIdle, 3*time.Second)
	if st.GcodeState != "IDLE" || !st.Connected || !st.FullReportSeen {
		t.Fatalf("unexpected status %+v", st)
	}
	deadline := time.Now().Add(3 * time.Second)
	for st.Firmware == "" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		st, _ = e.ad.Status(context.Background())
	}
	if st.Firmware != "01.08.02.00" {
		t.Fatalf("firmware = %q", st.Firmware)
	}
	if st.Temps.Nozzle <= 0 || st.UpdatedAt.IsZero() || st.Stale {
		t.Fatalf("bad temps/freshness: %+v", st)
	}
}

func TestUploadIdempotentAndVerified(t *testing.T) {
	e := newEnv(t, sim.Config{}, nil)
	a := newArtefact(300 * 1024)
	ref := e.upload(a)
	if ref.Reused {
		t.Fatal("first upload must not be reused")
	}
	fi, err := e.sim.FS().Stat("/" + ref.Name)
	if err != nil || fi.Size() != int64(len(a.data)) {
		t.Fatalf("remote file missing or wrong size: %v", err)
	}
	if !strings.Contains(ref.Name, a.sha[:12]) {
		t.Fatalf("remote name %q should carry the sha prefix", ref.Name)
	}
	ref2 := e.upload(a)
	if !ref2.Reused || ref2.Name != ref.Name {
		t.Fatalf("second upload not idempotent: %+v", ref2)
	}
	// Même contenu, autre nom : autre fichier.
	b := a
	b.name = "other.gcode.3mf"
	if ref3 := e.upload(b); ref3.Reused || ref3.Name == ref.Name {
		t.Fatalf("different name must upload anew: %+v", ref3)
	}
}

func TestUploadRejectsWrongShaOrSize(t *testing.T) {
	e := newEnv(t, sim.Config{}, nil)
	a := newArtefact(10_000)
	bad := strings.Repeat("0", 64)
	_, err := e.ad.Upload(ctx5(t), bytes.NewReader(a.data), a.name, int64(len(a.data)), bad)
	if !errors.Is(err, printer.ErrUploadMismatch) {
		t.Fatalf("wrong sha: want ErrUploadMismatch, got %v", err)
	}
	_, err = e.ad.Upload(ctx5(t), bytes.NewReader(a.data), a.name, int64(len(a.data))-5, a.sha)
	if !errors.Is(err, printer.ErrUploadMismatch) {
		t.Fatalf("wrong size: want ErrUploadMismatch, got %v", err)
	}
	// Aucun fichier orphelin ne doit rester.
	files, _ := afero.ReadDir(e.sim.FS(), "/")
	if len(files) != 0 {
		t.Fatalf("leftover remote files: %d", len(files))
	}
}

func TestStartFollowToFinish(t *testing.T) {
	e := newEnv(t, sim.Config{}, nil)
	a := newArtefact(50_000)
	ref := e.upload(a)
	waitState(t, e.ad, printer.StateIdle, 3*time.Second)
	sub, err := e.ad.Subscribe(ctx5(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ad.Start(ctx5(t), e.request(ref, "tok-ok")); err != nil {
		t.Fatalf("start: %v", err)
	}
	seen := map[printer.State]bool{}
	maxLayer, maxPct, maxNozzle := 0, 0, 0.0
	timeout := time.After(10 * time.Second)
loop:
	for {
		select {
		case st := <-sub:
			seen[st.State] = true
			if st.Layer > maxLayer {
				maxLayer = st.Layer
			}
			if st.Progress > maxPct {
				maxPct = st.Progress
			}
			if st.Temps.Nozzle > maxNozzle {
				maxNozzle = st.Temps.Nozzle
			}
			if st.State == printer.StateFinished {
				if st.Progress != 100 || st.Layer != st.TotalLayers || st.TotalLayers != 100 {
					t.Fatalf("finish status inconsistent: %+v", st)
				}
				break loop
			}
		case <-timeout:
			t.Fatalf("no finish; seen=%v", seen)
		}
	}
	if !seen[printer.StatePreparing] && !seen[printer.StatePrinting] {
		t.Fatalf("never saw preparing/printing: %v", seen)
	}
	if !seen[printer.StatePrinting] || maxLayer < 10 || maxPct < 50 || maxNozzle < 100 {
		t.Fatalf("weak progression: seen=%v layer=%d pct=%d nozzle=%.1f", seen, maxLayer, maxPct, maxNozzle)
	}
	cmds := e.sim.StartCommands()
	if len(cmds) != 1 {
		t.Fatalf("want exactly 1 project_file, got %d", len(cmds))
	}
	p := cmds[0].Payload
	if p["command"] != "project_file" || p["param"] != "Metadata/plate_1.gcode" || p["use_ams"] != true ||
		p["url"] != "ftp:///"+ref.Name || p["subtask_name"] == "" || p["sequence_id"] == "" {
		t.Fatalf("bad project_file payload: %v", p)
	}
	if m, _ := p["ams_mapping"].([]any); len(m) != 5 {
		t.Fatalf("ams_mapping = %v", p["ams_mapping"])
	}
}

func TestStartRefusals(t *testing.T) {
	e := newEnv(t, sim.Config{}, nil)
	a := newArtefact(20_000)
	ref := e.upload(a)
	waitState(t, e.ad, printer.StateIdle, 3*time.Second)

	mk := func(mut func(*printer.StartRequest)) printer.StartRequest {
		r := e.request(ref, fmt.Sprintf("tok-%d", time.Now().UnixNano()))
		mut(&r)
		return r
	}
	cases := []struct {
		name string
		mut  func(*printer.StartRequest)
		want error
	}{
		{"no proof", func(r *printer.StartRequest) { r.Proof = printer.ConfirmationProof{} }, printer.ErrNoProof},
		{"expired", func(r *printer.StartRequest) { r.Proof.ExpiresAt = time.Now().Add(-time.Second) }, printer.ErrProofExpired},
		{"different sha in proof", func(r *printer.StartRequest) { r.Proof.SHA256 = strings.Repeat("a", 64) }, printer.ErrProofMismatch},
		{"different sha in request", func(r *printer.StartRequest) { r.Remote.SHA256 = strings.Repeat("b", 64) }, printer.ErrProofMismatch},
		{"different plate", func(r *printer.StartRequest) { r.Plate = 2 }, printer.ErrProofMismatch},
		{"different mapping", func(r *printer.StartRequest) { r.AMSMapping = []int{1, -1, -1, -1, -1} }, printer.ErrProofMismatch},
		{"different printer", func(r *printer.StartRequest) { r.Proof.PrinterID = "other" }, printer.ErrProofMismatch},
		{"forged token", func(r *printer.StartRequest) { r.Proof.Token = "forged" }, printer.ErrNoProof},
		{"plate not attested", func(r *printer.StartRequest) { r.PlateClear = false }, printer.ErrPlateNotClear},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := e.ad.Start(ctx5(t), mk(c.mut))
			if !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
		})
	}
	if n := len(e.sim.StartCommands()); n != 0 {
		t.Fatalf("printer received %d project_file despite refusals", n)
	}
}

func TestStartRefusedWithoutVerifier(t *testing.T) {
	e := newEnv(t, sim.Config{}, func(c *printer.BambuConfig) { c.Verifier = nil })
	ref := e.upload(newArtefact(1000))
	req := e.request(ref, "x")
	if err := e.ad.Start(ctx5(t), req); !errors.Is(err, printer.ErrNoProof) {
		t.Fatalf("want ErrNoProof, got %v", err)
	}
	if len(e.sim.StartCommands()) != 0 {
		t.Fatal("start must not reach printer")
	}
}

func TestSingleUseToken(t *testing.T) {
	e := newEnv(t, sim.Config{PrintDuration: 400 * time.Millisecond}, nil)
	ref := e.upload(newArtefact(5_000))
	waitState(t, e.ad, printer.StateIdle, 3*time.Second)
	req := e.request(ref, "once")
	if err := e.ad.Start(ctx5(t), req); err != nil {
		t.Fatal(err)
	}
	waitState(t, e.ad, printer.StateFinished, 5*time.Second)
	// Rejouer la même preuve (jeton consommé) : refus, même si l'imprimante est prête.
	e2 := e.ad.Start(ctx5(t), req)
	if e2 == nil {
		t.Fatal("replayed proof accepted")
	}
	if len(e.sim.StartCommands()) != 1 {
		t.Fatalf("replay reached printer")
	}
}

func TestStartRefusedWhenNotReady(t *testing.T) {
	e := newEnv(t, sim.Config{PrintDuration: 3 * time.Second}, nil)
	ref := e.upload(newArtefact(5_000))
	waitState(t, e.ad, printer.StateIdle, 3*time.Second)
	if err := e.ad.Start(ctx5(t), e.request(ref, "t1")); err != nil {
		t.Fatal(err)
	}
	waitState(t, e.ad, printer.StatePrinting, 3*time.Second)
	// Déjà en cours : refus avant tout envoi.
	if err := e.ad.Start(ctx5(t), e.request(ref, "t2")); !errors.Is(err, printer.ErrNotReady) {
		t.Fatalf("want ErrNotReady, got %v", err)
	}
	if len(e.sim.StartCommands()) != 1 {
		t.Fatal("second start reached printer")
	}
}

func TestStartRefusedWithPrinterError(t *testing.T) {
	e := newEnv(t, sim.Config{}, nil)
	ref := e.upload(newArtefact(5_000))
	waitState(t, e.ad, printer.StateIdle, 3*time.Second)
	_ = e.sim.SetFault(sim.FaultHMSError, true)
	deadline := time.Now().Add(3 * time.Second)
	var st printer.Status
	for time.Now().Before(deadline) {
		st, _ = e.ad.Status(context.Background())
		if st.HasError() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !st.HasError() || len(st.HMS) != 1 || st.HMS[0].Code16() == "" {
		t.Fatalf("HMS not surfaced: %+v", st)
	}
	if err := e.ad.Start(ctx5(t), e.request(ref, "t")); !errors.Is(err, printer.ErrPrinterError) {
		t.Fatalf("want ErrPrinterError, got %v", err)
	}
	if len(e.sim.StartCommands()) != 0 {
		t.Fatal("start reached printer")
	}
}

func TestStartRejectedByPrinter(t *testing.T) {
	e := newEnv(t, sim.Config{Faults: []string{sim.FaultRejectStart}}, nil)
	ref := e.upload(newArtefact(5_000))
	waitState(t, e.ad, printer.StateIdle, 3*time.Second)
	err := e.ad.Start(ctx5(t), e.request(ref, "t"))
	if !errors.Is(err, printer.ErrStartRejected) || errors.Is(err, printer.ErrStartUnknown) {
		t.Fatalf("want ErrStartRejected, got %v", err)
	}
}

func TestStartFailsWhenRemoteFileMissingAtPrinter(t *testing.T) {
	e := newEnv(t, sim.Config{Faults: []string{sim.FaultFileMissing}}, nil)
	ref := e.upload(newArtefact(5_000))
	waitState(t, e.ad, printer.StateIdle, 3*time.Second)
	// La commande est acceptée et l'état PREPARE observé : Start réussit. L'échec
	// (fichier introuvable côté imprimante) apparaît ensuite dans le suivi.
	if err := e.ad.Start(ctx5(t), e.request(ref, "t")); err != nil {
		t.Fatalf("start: %v", err)
	}
	st := waitState(t, e.ad, printer.StateFailed, 3*time.Second)
	if st.FailReason == "" {
		t.Fatalf("failure without reason: %+v", st)
	}
}

func TestStartRefusedWhenRemoteFileGone(t *testing.T) {
	e := newEnv(t, sim.Config{}, nil)
	ref := e.upload(newArtefact(5_000))
	waitState(t, e.ad, printer.StateIdle, 3*time.Second)
	if err := e.sim.FS().Remove("/" + ref.Name); err != nil {
		t.Fatal(err)
	}
	err := e.ad.Start(ctx5(t), e.request(ref, "t"))
	if err == nil || errors.Is(err, printer.ErrStartUnknown) {
		t.Fatalf("want clean refusal, got %v", err)
	}
	if len(e.sim.StartCommands()) != 0 {
		t.Fatal("start reached printer")
	}
}

func TestConnectionLostAfterStartThenReconcile(t *testing.T) {
	e := newEnv(t, sim.Config{PrintDuration: 3 * time.Second, Faults: []string{sim.FaultDropAfterStart}}, nil)
	ref := e.upload(newArtefact(5_000))
	waitState(t, e.ad, printer.StateIdle, 3*time.Second)
	err := e.ad.Start(ctx5(t), e.request(ref, "t"))
	var unk *printer.StartUnknownError
	if !errors.Is(err, printer.ErrStartUnknown) || !errors.As(err, &unk) {
		t.Fatalf("want StartUnknown, got %v", err)
	}
	if unk.Stage == printer.StageAccepted {
		t.Fatalf("no acceptance was ever sent, stage=%s", unk.Stage)
	}
	rec, err := e.ad.Reconcile(ctx5(t), printer.StartExpectation{Remote: ref})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != printer.ReconcileStarted {
		t.Fatalf("reconcile = %s (%s), status %+v", rec.Outcome, rec.Reason, rec.Status)
	}
	// Jamais de renvoi automatique : exactement un project_file reçu.
	time.Sleep(500 * time.Millisecond)
	if n := len(e.sim.StartCommands()); n != 1 {
		t.Fatalf("project_file received %d times", n)
	}
}

func TestIgnoredStartReconcilesToNotStarted(t *testing.T) {
	e := newEnv(t, sim.Config{Faults: []string{sim.FaultIgnoreStart}}, func(c *printer.BambuConfig) {
		c.StartObserveTimeout = 700 * time.Millisecond
		c.ReconcileSettle = 200 * time.Millisecond
	})
	ref := e.upload(newArtefact(5_000))
	waitState(t, e.ad, printer.StateIdle, 3*time.Second)
	err := e.ad.Start(ctx5(t), e.request(ref, "t"))
	if !errors.Is(err, printer.ErrStartUnknown) {
		t.Fatalf("want StartUnknown, got %v", err)
	}
	rec, _ := e.ad.Reconcile(ctx5(t), printer.StartExpectation{Remote: ref})
	if rec.Outcome != printer.ReconcileNotStarted {
		t.Fatalf("reconcile = %s (%s)", rec.Outcome, rec.Reason)
	}
}

func TestReconcileUnknownWhileOffline(t *testing.T) {
	e := newEnv(t, sim.Config{}, nil)
	ref := e.upload(newArtefact(1000))
	waitState(t, e.ad, printer.StateIdle, 3*time.Second)
	_ = e.sim.SetFault(sim.FaultOffline, true)
	waitState(t, e.ad, printer.StateOffline, 5*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	rec, _ := e.ad.Reconcile(ctx, printer.StartExpectation{Remote: ref})
	if rec.Outcome != printer.ReconcileUnknown {
		t.Fatalf("offline reconcile = %s", rec.Outcome)
	}
	if err := e.ad.Start(ctx5(t), e.request(ref, "t")); !errors.Is(err, printer.ErrNotConnected) {
		t.Fatalf("want ErrNotConnected, got %v", err)
	}
	// Retour en ligne : reconnexion automatique, état de nouveau lisible.
	_ = e.sim.SetFault(sim.FaultOffline, false)
	waitState(t, e.ad, printer.StateIdle, 8*time.Second)
}

func TestPauseResumeStop(t *testing.T) {
	e := newEnv(t, sim.Config{PrintDuration: 20 * time.Second}, nil)
	ref := e.upload(newArtefact(5_000))
	waitState(t, e.ad, printer.StateIdle, 3*time.Second)
	if err := e.ad.Pause(ctx5(t)); !errors.Is(err, printer.ErrBadState) {
		t.Fatalf("pause while idle: want ErrBadState, got %v", err)
	}
	if err := e.ad.Start(ctx5(t), e.request(ref, "t")); err != nil {
		t.Fatal(err)
	}
	waitState(t, e.ad, printer.StatePrinting, 3*time.Second)
	if err := e.ad.Pause(ctx5(t)); err != nil {
		t.Fatalf("pause: %v", err)
	}
	st := waitState(t, e.ad, printer.StatePaused, 2*time.Second)
	time.Sleep(300 * time.Millisecond)
	st2, _ := e.ad.Status(context.Background())
	if st2.Layer != st.Layer {
		t.Fatalf("progress moved while paused: %d -> %d", st.Layer, st2.Layer)
	}
	if err := e.ad.Resume(ctx5(t)); err != nil {
		t.Fatalf("resume: %v", err)
	}
	waitState(t, e.ad, printer.StatePrinting, 2*time.Second)
	if err := e.ad.Stop(ctx5(t)); err != nil {
		t.Fatalf("stop: %v", err)
	}
	st = waitState(t, e.ad, printer.StateFailed, 2*time.Second)
	if st.FailReason == "" {
		t.Fatalf("stop should leave a fail reason: %+v", st)
	}
}

func TestHMSPauseSurfaced(t *testing.T) {
	e := newEnv(t, sim.Config{PrintDuration: 20 * time.Second}, nil)
	ref := e.upload(newArtefact(5_000))
	waitState(t, e.ad, printer.StateIdle, 3*time.Second)
	if err := e.ad.Start(ctx5(t), e.request(ref, "t")); err != nil {
		t.Fatal(err)
	}
	waitState(t, e.ad, printer.StatePrinting, 3*time.Second)
	_ = e.sim.SetFault(sim.FaultHMSError, true)
	st := waitState(t, e.ad, printer.StatePaused, 3*time.Second)
	deadline := time.Now().Add(2 * time.Second)
	for !st.HasError() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		st, _ = e.ad.Status(context.Background())
	}
	if !st.HasError() || st.PrintError == 0 {
		t.Fatalf("error not surfaced: %+v", st)
	}
}

func TestTLSPolicyAndAuth(t *testing.T) {
	base := printer.BambuConfig{PrinterID: "x", Host: "127.0.0.1", Serial: "S", Secrets: printer.StaticSecret("c")}
	// Aucune politique.
	if _, err := printer.NewBambu(base); !errors.Is(err, printer.ErrTLSPolicy) {
		t.Fatalf("no policy: %v", err)
	}
	// Insecure sans raison.
	c := base
	c.TLS = printer.TLSOptions{InsecureSkipVerify: true}
	if _, err := printer.NewBambu(c); !errors.Is(err, printer.ErrTLSPolicy) {
		t.Fatalf("insecure without reason: %v", err)
	}
	// Deux politiques à la fois.
	c.TLS = printer.TLSOptions{InsecureSkipVerify: true, InsecureReason: "x", PinnedSHA256: "aa"}
	if _, err := printer.NewBambu(c); !errors.Is(err, printer.ErrTLSPolicy) {
		t.Fatalf("two policies: %v", err)
	}

	s, err := sim.New(sim.Config{ControlPort: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	host, mport := splitAddr(t, s.MQTTAddr())
	mk := func(tlsO printer.TLSOptions, code string) *printer.Bambu {
		ad, err := printer.NewBambu(printer.BambuConfig{PrinterID: "x", Host: host, Serial: s.Serial(), MQTTPort: mport,
			Secrets: printer.StaticSecret(code), TLS: tlsO})
		if err != nil {
			t.Fatal(err)
		}
		return ad
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Mauvaise empreinte épinglée.
	if err := mk(printer.TLSOptions{PinnedSHA256: strings.Repeat("0", 64)}, s.AccessCode()).Connect(ctx); err == nil {
		t.Fatal("wrong pin accepted")
	}
	// Mauvais code d'accès.
	if err := mk(printer.TLSOptions{PinnedSHA256: s.Fingerprint()}, "wrong").Connect(ctx); err == nil {
		t.Fatal("wrong access code accepted")
	}
	// Autorité fournie (le certificat du simulateur couvre 127.0.0.1).
	ad := mk(printer.TLSOptions{RootCAPEM: s.CertPEM()}, s.AccessCode())
	if err := ad.Connect(ctx); err != nil {
		t.Fatalf("root CA connect: %v", err)
	}
	ad.Close()
	// Option explicite documentée.
	ad = mk(printer.TLSOptions{InsecureSkipVerify: true, InsecureReason: "test local"}, s.AccessCode())
	if err := ad.Connect(ctx); err != nil {
		t.Fatalf("insecure connect: %v", err)
	}
	ad.Close()
	// Empreinte récupérable pour l'épinglage.
	fp, err := printer.FetchCertFingerprint(ctx, s.MQTTAddr())
	if err != nil || fp != s.Fingerprint() {
		t.Fatalf("fingerprint %q err %v", fp, err)
	}
}

func TestSimRefusesNonLoopback(t *testing.T) {
	if _, err := sim.New(sim.Config{Host: "0.0.0.0"}); err == nil {
		t.Fatal("simulator must refuse non-loopback host")
	}
}

func TestSimControlAPI(t *testing.T) {
	s, err := sim.New(sim.Config{ControlPort: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base := "http://" + s.ControlAddr()
	resp, err := http.Post(base+"/faults/hms_error?on=true", "", nil)
	if err != nil || resp.StatusCode != 204 {
		t.Fatalf("set fault: %v %v", err, resp)
	}
	r, _ := http.Get(base + "/state")
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if !strings.Contains(string(body), "hms_error") {
		t.Fatalf("state lacks fault: %s", body)
	}
	resp, _ = http.Post(base+"/faults/bogus", "", nil)
	if resp.StatusCode != 400 {
		t.Fatalf("unknown fault must be 400, got %d", resp.StatusCode)
	}
	http.Post(base+"/reset", "", nil)
	if len(s.Faults()) != 0 {
		t.Fatal("reset did not clear faults")
	}
}
