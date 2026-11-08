package agentv2_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentv2"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer/sim"
)

const (
	testPrinterID = "prn_e2e"
	testAgentID   = "agt_e2e"
	testSecret    = testAgentID + ".s3cr3t-e2e"
	testToken     = "pair-e2e-token"
)

// fakeAuth joue le registre existant : un jeton d'appairage à usage unique, un secret d'agent.
type fakeAuth struct {
	mu      sync.Mutex
	used    bool
	revoked bool
}

func (a *fakeAuth) Authenticate(_ context.Context, bearer string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if bearer != "Bearer "+testSecret || a.revoked {
		return "", &agentv2.Error{Status: 401, Code: "unauthorized", Message: "refusé"}
	}
	return testAgentID, nil
}

func (a *fakeAuth) Pair(_ context.Context, req agentv2.PairRequest) (string, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if req.Token != testToken || a.used {
		return "", "", &agentv2.Error{Status: 403, Code: "pairing_invalid", Message: "jeton inconnu ou déjà utilisé"}
	}
	a.used = true
	return testAgentID, testSecret, nil
}

// verifier : confirmations à usage unique, comme le mécanisme réel du service.
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
		return errors.New("jeton inconnu ou déjà utilisé")
	}
	delete(v.valid, p.Token)
	return nil
}

func (v *verifier) issue(tok string) {
	v.mu.Lock()
	v.valid[tok] = true
	v.mu.Unlock()
}

func (v *verifier) count() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.calls
}

type stack struct {
	t      *testing.T
	sim    *sim.Sim
	cam    *sim.Camera
	hub    *agentv2.Hub
	srv    *httptest.Server
	runner *agentv2.Runner
	link   *agentv2.HubLink
	ver    *verifier
	ro     atomic.Bool
	cfg    *agentv2.LinkConfig

	mu     sync.Mutex
	orders []agentv2.Order
}

type stackOpts struct {
	hub      func(*agentv2.HubConfig)
	runner   func(*agentv2.RunnerConfig)
	sim      func(*sim.Config)
	chunk    int64
	camera   bool
	skipRun  bool // ne lance pas Run (l'ordre est exécuté à la main)
	deviceMo func(*printer.BambuConfig)
}

func splitAddr(t *testing.T, a string) (string, int) {
	t.Helper()
	i := strings.LastIndex(a, ":")
	var p int
	fmt.Sscan(a[i+1:], &p)
	return a[:i], p
}

// newStack monte : simulateur d'imprimante, Hub + HTTP (httptest), appairage par le protocole v2, agent (Runner)
// branché sur le simulateur, et le Link côté serveur.
func newStack(t *testing.T, o stackOpts) *stack {
	t.Helper()
	sc := sim.Config{ControlPort: -1, Tick: 20 * time.Millisecond, HeartbeatInterval: 150 * time.Millisecond,
		PrepareDuration: 150 * time.Millisecond, PrintDuration: 900 * time.Millisecond}
	if o.sim != nil {
		o.sim(&sc)
	}
	sm, err := sim.New(sc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sm.Close() })
	s := &stack{t: t, sim: sm, ver: &verifier{valid: map[string]bool{}}}
	camPort := 0
	if o.camera {
		if s.cam, err = sm.StartCamera(sim.CameraConfig{FrameEvery: 50 * time.Millisecond}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.cam.Close() })
		camPort = s.cam.Port()
	}

	hc := agentv2.HubConfig{DataDir: t.TempDir(), Log: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxPollWait: 2 * time.Second,
		TapOrder: func(od agentv2.Order) {
			s.mu.Lock()
			s.orders = append(s.orders, od)
			s.mu.Unlock()
		}}
	if o.hub != nil {
		o.hub(&hc)
	}
	s.hub = agentv2.NewHub(hc)
	s.srv = httptest.NewServer(s.hub.Handler(&fakeAuth{}))
	t.Cleanup(s.srv.Close)

	// Appairage par le protocole v2 (jeton à usage unique), clé du serveur épinglée.
	anon, err := agentv2.NewClient(s.srv.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	wantKeyID := ""
	if _, kid, kerr := s.hub.ServerKey(); kerr == nil {
		wantKeyID = kid
	}
	s.cfg, err = agentv2.PairAgent(context.Background(), anon, s.srv.URL, "", testToken, "agent-test", "host-test", testPrinterID, "test", wantKeyID)
	if err != nil {
		t.Fatalf("appairage : %v", err)
	}
	if s.cfg.AgentID != testAgentID || s.cfg.Secret != testSecret {
		t.Fatalf("appairage : %+v", s.cfg)
	}
	pub, err := s.cfg.ServerPublicKey()
	if err != nil {
		t.Fatal(err)
	}
	cl, err := agentv2.NewClient(s.srv.URL, s.cfg.Secret, "")
	if err != nil {
		t.Fatal(err)
	}
	if o.chunk > 0 {
		cl.ChunkSize = o.chunk
	}
	host, mport := splitAddr(t, sm.MQTTAddr())
	_, fport := splitAddr(t, sm.FTPAddr())
	newDevice, snapshot, err := agentv2.BambuDevice{PrinterID: testPrinterID, Host: host, Serial: sm.Serial(), MQTTPort: mport, FTPPort: fport, CameraPort: camPort,
		AccessCode: sm.AccessCode(), PinSHA256: sm.Fingerprint(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Mod: func(c *printer.BambuConfig) {
			c.ReconnectDelay = 100 * time.Millisecond
			c.StartObserveTimeout = 2 * time.Second
			c.PushAllWait = 2 * time.Second
			c.ReconcileSettle = 200 * time.Millisecond
			if o.deviceMo != nil {
				o.deviceMo(c)
			}
		}}.Factory()
	if err != nil {
		t.Fatal(err)
	}
	rc := agentv2.RunnerConfig{Client: cl, AgentID: s.cfg.AgentID, PrinterID: s.cfg.PrinterID, ServerKey: pub, NewDevice: newDevice, Snapshot: snapshot,
		StateDir: t.TempDir(), ReadOnly: func() bool { return s.ro.Load() }, TelemetryEvery: 100 * time.Millisecond, PollWait: 1,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if !o.camera {
		rc.Snapshot = nil
	}
	if o.runner != nil {
		o.runner(&rc)
	}
	s.runner, err = agentv2.NewRunner(rc)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	if o.skipRun {
		close(done)
		go func() { // télémétrie seulement : ordre « status » exécuté en boucle par un faux poller
			for ctx.Err() == nil {
				s.runner.Execute(ctx, agentv2.Order{Kind: agentv2.OrderStatus, PrinterID: testPrinterID})
				time.Sleep(100 * time.Millisecond)
			}
		}()
	} else {
		go func() { defer close(done); _ = s.runner.Run(ctx) }()
	}
	t.Cleanup(func() {
		cancel()
		<-done
		_ = s.runner.Close()
	})
	s.link = s.hub.Link(testPrinterID, testAgentID, s.ver.verify)
	return s
}

func (s *stack) waitReady() printer.Status {
	s.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var st printer.Status
	for time.Now().Before(deadline) {
		st, _ = s.link.Status(context.Background())
		if st.Connected && !st.Stale && st.FullReportSeen && st.State == printer.StateIdle {
			return st
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.t.Fatalf("télémétrie non prête : %+v", st)
	return st
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

func (s *stack) upload(a artefact) printer.RemoteRef {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ref, err := s.link.Upload(ctx, bytes.NewReader(a.data), a.name, int64(len(a.data)), a.sha)
	if err != nil {
		s.t.Fatalf("dépôt : %v", err)
	}
	return ref
}

func (s *stack) request(ref printer.RemoteRef, tok string) printer.StartRequest {
	s.ver.issue(tok)
	return printer.StartRequest{
		Remote: ref, Plate: 1, UseAMS: true, AMSMapping: []int{0, -1, -1, -1, -1}, PlateClear: true,
		Options: printer.DefaultStartOptions(),
		Proof: printer.ConfirmationProof{PrinterID: testPrinterID, SHA256: ref.SHA256, Plate: 1, AMSMapping: []int{0, -1, -1, -1, -1},
			Token: tok, ExpiresAt: time.Now().Add(time.Minute)},
	}
}

func (s *stack) start(req printer.StartRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.link.Start(ctx, req)
}

func (s *stack) startOrders() []agentv2.Order {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []agentv2.Order
	for _, o := range s.orders {
		if o.Kind == agentv2.OrderStart {
			out = append(out, o)
		}
	}
	return out
}

// ---- appairage ----

func TestPairingReusesTokenAndPinsServerKey(t *testing.T) {
	hub := agentv2.NewHub(agentv2.HubConfig{DataDir: t.TempDir()})
	srv := httptest.NewServer(hub.Handler(&fakeAuth{}))
	defer srv.Close()
	cl, _ := agentv2.NewClient(srv.URL, "", "")
	ctx := context.Background()

	_, kid, _ := hub.ServerKey()
	// Une clé attendue différente : appairage refusé côté agent (comparaison hors bande).
	if _, err := agentv2.PairAgent(ctx, cl, srv.URL, "", testToken, "a", "h", testPrinterID, "t", "deadbeefdeadbeef"); err == nil || !strings.Contains(err.Error(), "pas celle attendue") {
		t.Fatalf("clé inattendue acceptée : %v", err)
	}
	// ... mais le jeton a été consommé par ce premier essai (usage unique) : on en refait un autre hub.
	hub2 := agentv2.NewHub(agentv2.HubConfig{DataDir: t.TempDir()})
	srv2 := httptest.NewServer(hub2.Handler(&fakeAuth{}))
	defer srv2.Close()
	cl2, _ := agentv2.NewClient(srv2.URL, "", "")
	_, kid2, _ := hub2.ServerKey()
	cfg, err := agentv2.PairAgent(ctx, cl2, srv2.URL, "", testToken, "a", "h", testPrinterID, "t", kid2)
	if err != nil || cfg.ServerKeyID != kid2 || cfg.AgentID != testAgentID {
		t.Fatalf("appairage : %+v %v", cfg, err)
	}
	if kid == kid2 {
		t.Fatal("deux serveurs ne doivent pas partager de clé")
	}
	// Jeton à usage unique : un second appairage échoue.
	if _, err := cl2.Pair(ctx, agentv2.PairRequest{Token: testToken, Name: "b"}); err == nil {
		t.Fatal("jeton d'appairage réutilisable")
	}
	// Mauvais protocole (v1) refusé sur v2.
	if err := postRaw(srv2.URL+agentv2.BasePath+"/pair", `{"token":"x","name":"n","protocol":1}`); err == nil {
		t.Fatal("protocole 1 accepté sur /agent/v2/pair")
	}
	// Sans Bearer, ni ordres ni télémétrie.
	if resp, err := http.Get(srv2.URL + agentv2.BasePath + "/orders/next?wait=0"); err != nil || resp.StatusCode != 401 {
		t.Fatalf("long-poll sans secret : %v %v", resp, err)
	}
	// La clé de signature persiste (même identifiant après rechargement du même dossier).
	dir := t.TempDir()
	h3 := agentv2.NewHub(agentv2.HubConfig{DataDir: dir})
	_, k3a, _ := h3.ServerKey()
	h4 := agentv2.NewHub(agentv2.HubConfig{DataDir: dir})
	_, k3b, _ := h4.ServerKey()
	if k3a != k3b {
		t.Fatal("la clé du serveur doit survivre à un redémarrage")
	}
	if st, err := os.Stat(filepath.Join(dir, "secrets", "agent-v2-ed25519.key")); err != nil || st.Size() == 0 {
		t.Fatalf("fichier de clé : %v", err)
	}
}

func postRaw(url, body string) error {
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// ---- télémétrie ----

func TestTelemetryRelayedWithServerClock(t *testing.T) {
	s := newStack(t, stackOpts{})
	st := s.waitReady()
	if st.GcodeState != "IDLE" || st.UpdatedAt.IsZero() {
		t.Fatalf("état : %+v", st)
	}
	if age := time.Since(st.UpdatedAt); age < 0 || age > 30*time.Second {
		t.Fatalf("UpdatedAt doit être sur l'horloge du serveur et récent : %v", age)
	}
	ch, err := s.link.Subscribe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case v := <-ch:
		if !v.Connected {
			t.Fatalf("abonnement : %+v", v)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("aucun état reçu par l'abonnement")
	}
	if s.link.Kind() != printer.LinkAgent {
		t.Fatal("kind")
	}
}

func TestOfflineAgentIsOffline(t *testing.T) {
	hub := agentv2.NewHub(agentv2.HubConfig{})
	l := hub.Link("p", "a", func(context.Context, printer.ConfirmationProof) error { return nil })
	st, _ := l.Status(context.Background())
	if st.State != printer.StateOffline || st.Connected || !st.Stale {
		t.Fatalf("sans agent : %+v", st)
	}
	err := l.Start(context.Background(), printer.StartRequest{})
	if err == nil {
		t.Fatal("Start sans agent")
	}
	if _, err := l.Upload(context.Background(), strings.NewReader("x"), "a.3mf", 1, strings.Repeat("0", 64)); !errors.Is(err, printer.ErrNotConnected) {
		t.Fatalf("dépôt sans agent : %v", err)
	}
}

// ---- dépôt par morceaux et lancement signé ----

func TestChunkedUploadThenSignedStartAcceptedOnceReplayRefused(t *testing.T) {
	s := newStack(t, stackOpts{chunk: 64 << 10})
	s.waitReady()
	a := newArtefact(300_000) // 5 morceaux de 64 Kio
	ref := s.upload(a)
	if ref.SHA256 != a.sha || ref.Size != int64(len(a.data)) || !strings.HasSuffix(ref.Name, ".gcode.3mf") {
		t.Fatalf("référence : %+v", ref)
	}
	// Le fichier est bien sur l'imprimante simulée, octet pour octet.
	got, err := afero.ReadFile(s.sim.FS(), ref.Path)
	if err != nil || !bytes.Equal(got, a.data) {
		t.Fatalf("fichier sur l'imprimante : %v (len %d)", err, len(got))
	}
	// Dépôt idempotent : le second dépôt ne retransfère pas.
	if ref2 := s.upload(a); !ref2.Reused {
		t.Fatalf("second dépôt non idempotent : %+v", ref2)
	}

	if err := s.start(s.request(ref, "tok-1")); err != nil {
		t.Fatalf("lancement signé refusé : %v", err)
	}
	if n := len(s.sim.StartCommands()); n != 1 {
		t.Fatalf("l'imprimante a reçu %d commandes de lancement, attendu 1", n)
	}
	if s.ver.count() != 1 {
		t.Fatalf("la confirmation doit être consommée une fois : %d", s.ver.count())
	}
	st, _ := s.link.Status(context.Background())
	_ = st
	orders := s.startOrders()
	if len(orders) != 1 || orders[0].Auth == nil {
		t.Fatalf("ordres de lancement : %d", len(orders))
	}

	// REJEU de l'ordre signé (même nonce) : refusé par l'agent, aucune seconde commande.
	// Laisser l'impression se terminer pour que le seul obstacle soit le nonce.
	waitState(t, s, printer.StateFinished, 20*time.Second)
	res := s.runner.Execute(context.Background(), orders[0])
	if res.State != agentv2.ResultRefused || res.Code != agentv2.CodeReplay {
		t.Fatalf("rejeu : %+v", res)
	}
	if n := len(s.sim.StartCommands()); n != 1 {
		t.Fatalf("après rejeu : %d commandes", n)
	}
	// Le serveur n'a gardé qu'UNE autorisation pour ce lancement.
	if len(s.startOrders()) != 1 {
		t.Fatal("le serveur a renvoyé l'ordre")
	}
}

func waitState(t *testing.T, s *stack, want printer.State, d time.Duration) {
	t.Helper()
	dl := time.Now().Add(d)
	for time.Now().Before(dl) {
		if st, _ := s.link.Status(context.Background()); st.State == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	st, _ := s.link.Status(context.Background())
	t.Fatalf("état %s non atteint : %+v", want, st)
}

func TestExpiredAuthorizationRefused(t *testing.T) {
	// Le serveur signe avec une horloge en retard de 10 minutes : l'autorisation est déjà périmée à l'arrivée.
	s := newStack(t, stackOpts{hub: func(c *agentv2.HubConfig) {
		c.AuthClock = func() time.Time { return time.Now().Add(-10 * time.Minute) }
	}})
	s.waitReady()
	ref := s.upload(newArtefact(50_000))
	err := s.start(s.request(ref, "tok-exp"))
	if err == nil || !errors.Is(err, printer.ErrStartRejected) || !strings.Contains(err.Error(), agentv2.CodeExpired) {
		t.Fatalf("autorisation périmée : %v", err)
	}
	if n := len(s.sim.StartCommands()); n != 0 {
		t.Fatalf("une autorisation périmée a lancé %d commande(s)", n)
	}
	// Direct : une autorisation neuve valable 2 minutes mais présentée 3 minutes plus tard est refusée aussi.
	p, _ := s.hub.SignAuthorization(agentv2.AuthPayload{AgentID: testAgentID, PrinterID: testPrinterID, SHA256: ref.SHA256, Plate: 1, PlateClear: true,
		IssuedAt: time.Now().Add(-3 * time.Minute), ExpiresAt: time.Now().Add(-1 * time.Minute)})
	res := s.runner.Execute(context.Background(), agentv2.Order{Kind: agentv2.OrderStart, PrinterID: testPrinterID, Auth: &p})
	if res.State != agentv2.ResultRefused || res.Code != agentv2.CodeExpired {
		t.Fatalf("exécution directe : %+v", res)
	}
	// Une fenêtre de validité trop longue est refusée même signée.
	long, _ := s.hub.SignAuthorization(agentv2.AuthPayload{AgentID: testAgentID, PrinterID: testPrinterID, SHA256: ref.SHA256, Plate: 1, PlateClear: true,
		IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)})
	res = s.runner.Execute(context.Background(), agentv2.Order{Kind: agentv2.OrderStart, PrinterID: testPrinterID, Auth: &long})
	if res.State != agentv2.ResultRefused || res.Code != agentv2.CodeExpired {
		t.Fatalf("fenêtre trop longue : %+v", res)
	}
	if n := len(s.sim.StartCommands()); n != 0 {
		t.Fatalf("%d commande(s) de lancement", n)
	}
}

func TestDifferentFileFingerprintRefused(t *testing.T) {
	s := newStack(t, stackOpts{})
	s.waitReady()
	a := newArtefact(80_000)
	ref := s.upload(a)

	// 1. Le fichier présent sur l'imprimante est altéré (même taille) après le dépôt : refus, rien n'est lancé.
	tampered := append([]byte(nil), a.data...)
	tampered[len(tampered)/2] ^= 0xFF
	if err := afero.WriteFile(s.sim.FS(), ref.Path, tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	err := s.start(s.request(ref, "tok-h1"))
	if err == nil || !errors.Is(err, printer.ErrStartRejected) || !strings.Contains(err.Error(), agentv2.CodeHashMismatch) {
		t.Fatalf("fichier altéré : %v", err)
	}
	if n := len(s.sim.StartCommands()); n != 0 {
		t.Fatalf("fichier altéré lancé : %d", n)
	}

	// 2. Une autorisation pour une empreinte que l'agent n'a jamais déposée : refus.
	other := newArtefact(1000)
	p, _ := s.hub.SignAuthorization(agentv2.AuthPayload{AgentID: testAgentID, PrinterID: testPrinterID, SHA256: other.sha, Plate: 1, PlateClear: true,
		IssuedAt: time.Now(), ExpiresAt: time.Now().Add(agentv2.AuthTTL)})
	res := s.runner.Execute(context.Background(), agentv2.Order{Kind: agentv2.OrderStart, PrinterID: testPrinterID, Auth: &p})
	if res.State != agentv2.ResultRefused || res.Code != agentv2.CodeFileUnknown {
		t.Fatalf("empreinte inconnue : %+v", res)
	}
	if n := len(s.sim.StartCommands()); n != 0 {
		t.Fatalf("%d commande(s) de lancement", n)
	}
}

func TestForgedOrMisdirectedAuthorizationRefused(t *testing.T) {
	s := newStack(t, stackOpts{})
	s.waitReady()
	ref := s.upload(newArtefact(20_000))
	run := func(p agentv2.SignedAuth) agentv2.OrderResult {
		return s.runner.Execute(context.Background(), agentv2.Order{Kind: agentv2.OrderStart, PrinterID: testPrinterID, Auth: &p})
	}
	now := time.Now()
	base := agentv2.AuthPayload{AgentID: testAgentID, PrinterID: testPrinterID, SHA256: ref.SHA256, Plate: 1, PlateClear: true, IssuedAt: now, ExpiresAt: now.Add(agentv2.AuthTTL)}

	// Signée par une AUTRE clé (faux serveur) : refus.
	evil, _ := agentv2.NewSigner()
	forged, _ := evil.Sign(base)
	if res := run(forged); res.Code != agentv2.CodeBadSignature {
		t.Fatalf("clé étrangère : %+v", res)
	}
	// Charge utile modifiée après signature : refus.
	ok, _ := s.hub.SignAuthorization(base)
	raw := []byte(ok.Payload)
	raw[len(raw)/2] ^= 1
	if res := run(agentv2.SignedAuth{Payload: string(raw), Signature: ok.Signature}); res.Code != agentv2.CodeBadSignature {
		t.Fatalf("charge modifiée : %+v", res)
	}
	// Autre imprimante / autre agent : refus.
	other := base
	other.PrinterID = "prn_autre"
	po, _ := s.hub.SignAuthorization(other)
	if res := run(po); res.Code != agentv2.CodeWrongTarget {
		t.Fatalf("autre imprimante : %+v", res)
	}
	other = base
	other.AgentID = "agt_autre"
	pa, _ := s.hub.SignAuthorization(other)
	if res := run(pa); res.Code != agentv2.CodeWrongTarget {
		t.Fatalf("autre agent : %+v", res)
	}
	// Plateau non attesté vide : refus. Sans autorisation : refus.
	nc := base
	nc.PlateClear = false
	pn, _ := s.hub.SignAuthorization(nc)
	if res := run(pn); res.State != agentv2.ResultRefused {
		t.Fatalf("plateau non attesté : %+v", res)
	}
	if res := s.runner.Execute(context.Background(), agentv2.Order{Kind: agentv2.OrderStart, PrinterID: testPrinterID}); res.Code != agentv2.CodeBadSignature {
		t.Fatalf("sans autorisation : %+v", res)
	}
	// Ordre adressé à une autre imprimante : refus.
	if res := s.runner.Execute(context.Background(), agentv2.Order{Kind: agentv2.OrderStart, PrinterID: "prn_autre", Auth: &ok}); res.Code != agentv2.CodeWrongTarget {
		t.Fatalf("ordre mal adressé : %+v", res)
	}
	if n := len(s.sim.StartCommands()); n != 0 {
		t.Fatalf("%d commande(s) de lancement", n)
	}
}

// ---- kill-switch ----

func TestReadOnlyAgentRefusesAnyLaunch(t *testing.T) {
	s := newStack(t, stackOpts{})
	s.waitReady()
	a := newArtefact(40_000)
	ref := s.upload(a)

	s.ro.Store(true)
	// Attendre que la télémétrie annonce le mode lecture seule : le serveur refuse AVANT de consommer la confirmation.
	dl := time.Now().Add(5 * time.Second)
	for !s.hub.AgentReadOnly(testAgentID, testPrinterID) && time.Now().Before(dl) {
		time.Sleep(20 * time.Millisecond)
	}
	req := s.request(ref, "tok-ro1")
	err := s.start(req)
	if err == nil || !errors.Is(err, printer.ErrNotReady) || !strings.Contains(err.Error(), "lecture seule") {
		t.Fatalf("lecture seule (serveur informé) : %v", err)
	}
	if s.ver.count() != 0 {
		t.Fatalf("la confirmation a été consommée malgré le kill-switch")
	}
	// Même si le serveur l'ignore : l'agent refuse de lui-même une autorisation pourtant valide.
	p, _ := s.hub.SignAuthorization(agentv2.AuthPayload{AgentID: testAgentID, PrinterID: testPrinterID, SHA256: ref.SHA256, Plate: 1, PlateClear: true,
		IssuedAt: time.Now(), ExpiresAt: time.Now().Add(agentv2.AuthTTL)})
	res := s.runner.Execute(context.Background(), agentv2.Order{Kind: agentv2.OrderStart, PrinterID: testPrinterID, Auth: &p})
	if res.State != agentv2.ResultRefused || res.Code != agentv2.CodeReadOnly {
		t.Fatalf("agent en lecture seule : %+v", res)
	}
	// Le dépôt est refusé aussi (le serveur ne peut rien écrire sur l'imprimante).
	if _, err := s.link.Upload(context.Background(), bytes.NewReader([]byte("abc")), "x.3mf", 3, sha256hex([]byte("abc"))); err == nil {
		t.Fatal("dépôt accepté en lecture seule")
	}
	if n := len(s.sim.StartCommands()); n != 0 {
		t.Fatalf("%d commande(s) de lancement en lecture seule", n)
	}
	// Levée du kill-switch (action locale) : le même fichier se lance.
	s.ro.Store(false)
	dl = time.Now().Add(5 * time.Second)
	for s.hub.AgentReadOnly(testAgentID, testPrinterID) && time.Now().Before(dl) {
		time.Sleep(20 * time.Millisecond)
	}
	if err := s.start(s.request(ref, "tok-ro2")); err != nil {
		t.Fatalf("après levée : %v", err)
	}
}

func sha256hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestReadOnlyFlagFileAndOption(t *testing.T) {
	file := filepath.Join(t.TempDir(), "READ_ONLY")
	f := agentv2.ReadOnlyFlag(false, file)
	if f() {
		t.Fatal("fichier absent : pas de lecture seule")
	}
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !f() {
		t.Fatal("fichier présent : lecture seule (sans redémarrage)")
	}
	_ = os.Remove(file)
	if f() {
		t.Fatal("fichier retiré : levée")
	}
	if !agentv2.ReadOnlyFlag(true, "")() {
		t.Fatal("option --read-only")
	}
}

// ---- jamais de renvoi ----

func TestUnknownStartIsNeverResentAndReconciles(t *testing.T) {
	// L'imprimante reçoit project_file et l'ignore : aucun accusé. Le résultat est indéterminé.
	s := newStack(t, stackOpts{sim: func(c *sim.Config) { c.Faults = []string{sim.FaultIgnoreStart} }})
	s.waitReady()
	ref := s.upload(newArtefact(30_000))
	err := s.start(s.request(ref, "tok-unk"))
	var su *printer.StartUnknownError
	if !errors.Is(err, printer.ErrStartUnknown) || !errors.As(err, &su) {
		t.Fatalf("attendu START_UNKNOWN, reçu : %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if n := len(s.sim.StartCommands()); n != 1 {
		t.Fatalf("la commande doit partir UNE fois : %d", n)
	}
	if len(s.startOrders()) != 1 {
		t.Fatal("le serveur a rejoué l'ordre de lancement")
	}
	// Réconciliation : lecture d'état seulement, jamais de nouvelle commande.
	rec, err := s.link.Reconcile(context.Background(), printer.StartExpectation{Remote: ref, Since: time.Now().Add(-5 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome == printer.ReconcileStarted {
		t.Fatalf("faux positif : %+v", rec)
	}
	if n := len(s.sim.StartCommands()); n != 1 {
		t.Fatalf("la réconciliation a émis une commande : %d", n)
	}
}

func TestPrinterRejectionIsReportedNotUnknown(t *testing.T) {
	s := newStack(t, stackOpts{sim: func(c *sim.Config) { c.Faults = []string{sim.FaultRejectStart} }})
	s.waitReady()
	ref := s.upload(newArtefact(10_000))
	err := s.start(s.request(ref, "tok-rej"))
	if !errors.Is(err, printer.ErrStartRejected) || errors.Is(err, printer.ErrStartUnknown) {
		t.Fatalf("refus de l'imprimante : %v", err)
	}
	if n := len(s.sim.StartCommands()); n != 1 {
		t.Fatalf("commandes : %d", n)
	}
}

func TestServerWithdrawsOrderWhenAgentNeverTakesIt(t *testing.T) {
	// Agent « en ligne » (télémétrie) mais qui ne tire jamais d'ordre : le lancement est retiré, rien n'est parti.
	s := newStack(t, stackOpts{skipRun: true, hub: func(c *agentv2.HubConfig) { c.StartWait = 700 * time.Millisecond }})
	s.waitReady()
	a := newArtefact(10_000)
	// Dépôt à la main (aucun poller de l'agent) : un faux poller exécute l'ordre put_file puis disparaît.
	ref := stageAndPut(t, s, a)
	err := s.start(s.request(ref, "tok-wd"))
	if !errors.Is(err, printer.ErrStartRejected) || errors.Is(err, printer.ErrStartUnknown) || !strings.Contains(err.Error(), "rien n'a été envoyé") {
		t.Fatalf("ordre jamais remis : %v", err)
	}
	if n := len(s.sim.StartCommands()); n != 0 {
		t.Fatalf("commandes : %d", n)
	}
}

// stageAndPut dépose un fichier sans poller : un faux agent HTTP tire l'ordre et le confie au Runner.
func stageAndPut(t *testing.T, s *stack, a artefact) printer.RemoteRef {
	t.Helper()
	cl, _ := agentv2.NewClient(s.srv.URL, testSecret, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan printer.RemoteRef, 1)
	go func() {
		for ctx.Err() == nil {
			o, err := cl.NextOrder(ctx, testPrinterID, 1)
			if err != nil || o == nil {
				continue
			}
			res := s.runner.Execute(ctx, *o)
			_ = cl.PostResult(ctx, o.ID, res)
			return
		}
	}()
	go func() {
		ref, err := s.link.Upload(ctx, bytes.NewReader(a.data), a.name, int64(len(a.data)), a.sha)
		if err != nil {
			t.Errorf("dépôt : %v", err)
		}
		done <- ref
	}()
	select {
	case ref := <-done:
		return ref
	case <-time.After(20 * time.Second):
		t.Fatal("dépôt trop long")
	}
	return printer.RemoteRef{}
}

// ---- contrôle et caméra ----

func TestStopAndSnapshotRelayed(t *testing.T) {
	s := newStack(t, stackOpts{camera: true, sim: func(c *sim.Config) { c.PrintDuration = 20 * time.Second }})
	s.waitReady()
	// Instantané caméra relayé par l'agent.
	jpg, err := s.link.Snapshot(context.Background())
	if err != nil || len(jpg) < 100 || jpg[0] != 0xFF || jpg[1] != 0xD8 {
		t.Fatalf("instantané : %v (%d octets)", err, len(jpg))
	}
	ref := s.upload(newArtefact(20_000))
	if err := s.start(s.request(ref, "tok-stop")); err != nil {
		t.Fatal(err)
	}
	waitState(t, s, printer.StatePrinting, 10*time.Second)
	if err := s.link.Pause(context.Background()); err != nil {
		t.Fatalf("pause : %v", err)
	}
	waitState(t, s, printer.StatePaused, 10*time.Second)
	// Même en lecture seule : l'arrêt est un geste de sécurité, il reste permis.
	s.ro.Store(true)
	if err := s.link.Stop(context.Background()); err != nil {
		t.Fatalf("arrêt : %v", err)
	}
	if err := s.link.Resume(context.Background()); err == nil {
		t.Fatal("reprise acceptée en lecture seule")
	}
}

// ---- journal des nonces ----

func TestNonceStoreSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonces.log")
	a, err := agentv2.OpenFileNonceStore(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(time.Minute)
	if ok, err := a.Consume("n1", exp); !ok || err != nil {
		t.Fatalf("premier usage : %v %v", ok, err)
	}
	if ok, _ := a.Consume("n1", exp); ok {
		t.Fatal("rejeu en mémoire")
	}
	b, err := agentv2.OpenFileNonceStore(path, nil) // « redémarrage » de l'agent
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := b.Consume("n1", exp); ok {
		t.Fatal("rejeu après redémarrage")
	}
	if ok, _ := b.Consume("n2", exp); !ok {
		t.Fatal("nonce neuf refusé")
	}
}
