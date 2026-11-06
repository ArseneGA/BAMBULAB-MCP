package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
)

func getJSON(t *testing.T, method, url string, out any) int {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode
}

func TestServeAndGracefulShutdown(t *testing.T) {
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan string, 1)
	done := make(chan error, 1)
	cfg := core.Config{DataDir: dir, Logger: log, PollInterval: 20 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond}
	go func() { done <- run(ctx, log, "127.0.0.1:0", cfg, serveConfig{}, ready) }()
	var addr string
	select {
	case addr = <-ready:
	case err := <-done:
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("démarrage trop long")
	}
	base := "http://" + addr

	var health map[string]any
	if code := getJSON(t, "GET", base+"/health", &health); code != 200 || health["status"] != "ok" {
		t.Fatalf("health %d %v", code, health)
	}
	var caps map[string]any
	if code := getJSON(t, "GET", base+"/api/capabilities", &caps); code != 200 || caps["service"] != "bambud" {
		t.Fatalf("capabilities %d %v", code, caps)
	}
	if code := getJSON(t, "GET", base+"/api/jobs/job_inconnu", nil); code != 404 {
		t.Fatalf("job inconnu : %d", code)
	}
	if code := getJSON(t, "POST", base+"/api/jobs/job_inconnu/cancel", nil); code != 404 {
		t.Fatalf("cancel inconnu : %d", code)
	}
	// /health n'accepte pas POST (routeur Go 1.22+ : 405).
	if code := getJSON(t, "POST", base+"/health", nil); code != 405 {
		t.Fatalf("POST /health : %d", code)
	}
	// /mcp répond (un GET sans session est refusé proprement, pas de 404).
	resp, err := http.Get(base + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == 404 {
		t.Fatal("/mcp absent")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("arrêt : %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("arrêt propre trop long")
	}
	if _, err := http.Get(base + "/health"); err == nil {
		t.Fatal("le service répond encore après l'arrêt")
	}
}

func TestJobAPIEndToEnd(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c, err := core.Open(context.Background(), core.Config{DataDir: t.TempDir(), Logger: log,
		PollInterval: 20 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	defer func() { cancel(); <-done }()

	j, _, err := c.Queue.Enqueue(ctx, jobsEnqueue(`{"steps":1000,"step_ms":50}`))
	if err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, c)
	defer srv.Close()
	for i := 0; i < 400; i++ {
		var got map[string]any
		getJSON(t, "GET", srv.URL+"/api/jobs/"+j.ID, &got)
		if got["state"] == "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	var out map[string]any
	if code := getJSON(t, "POST", srv.URL+"/api/jobs/"+j.ID+"/cancel", &out); code != 200 || out["outcome"] != "cancel_requested" {
		t.Fatalf("%d %v", code, out)
	}
	var got map[string]any
	for i := 0; i < 400; i++ {
		getJSON(t, "GET", srv.URL+"/api/jobs/"+j.ID, &got)
		if got["state"] == "cancelled" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got["state"] != "cancelled" || !strings.HasPrefix(got["id"].(string), "job_") {
		t.Fatalf("%v", got)
	}
}

func TestRunRefusesNonLoopbackWithoutToken(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := core.Config{DataDir: t.TempDir(), Logger: log}
	err := run(context.Background(), log, "0.0.0.0:0", cfg, serveConfig{}, nil)
	if err == nil || !strings.Contains(err.Error(), "jeton") {
		t.Fatalf("démarrage hors loopback sans jeton non refusé : %v", err)
	}
}

// Le jeton est aussi exigé en écoute loopback dès que le service est exposé (proxy) ou si --require-token.
func TestRunRequiresTokenWhenExposedOrForced(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cases := map[string]serveConfig{
		"--require-token":      {RequireToken: true},
		"url publique":         {HTTP: httpapi.Options{PublicURL: "https://bambu.example.ts.net", PublicURLs: []string{"https://bambu.example.ts.net"}}},
		"hôte autorisé public": {HTTP: httpapi.Options{AllowedHosts: []string{"bambu.example.ts.net"}}},
	}
	for name, sc := range cases {
		err := run(context.Background(), log, "127.0.0.1:0", core.Config{DataDir: t.TempDir(), Logger: log}, sc, nil)
		if err == nil || !strings.Contains(err.Error(), "jeton") || !strings.Contains(err.Error(), "proxy") {
			t.Errorf("%s : démarrage loopback sans jeton non refusé : %v", name, err)
		}
	}
}

func TestPublicURLList(t *testing.T) {
	const l = "https://bambu.example.ts.net/, http://192.168.1.5:7878"
	if got := publicBase(l, "127.0.0.1:7878"); got != "https://bambu.example.ts.net" {
		t.Errorf("la première URL sert aux liens : %q", got)
	}
	if validatePublicURL(l) != nil {
		t.Error("liste valide refusée")
	}
	if validatePublicURL("https://a.example, ftp://b") == nil || validatePublicURL(" , ") == nil {
		t.Error("liste invalide acceptée")
	}
	if got := splitPublicURLs(l); len(got) != 2 || got[1] != "http://192.168.1.5:7878" {
		t.Errorf("découpe : %v", got)
	}
}

// failingListener bloque Accept puis échoue quand fail est fermé.
type failingListener struct {
	net.Listener
	fail chan struct{}
}

func (l *failingListener) Accept() (net.Conn, error) {
	<-l.fail
	return nil, errors.New("panne simulée de l'écoute")
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func fastCore(t *testing.T) *core.Core {
	t.Helper()
	c, err := core.Open(context.Background(), core.Config{DataDir: t.TempDir(), Logger: quietLog(),
		PollInterval: 10 * time.Millisecond, HeartbeatInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// Si le serveur HTTP échoue, le contexte des workers doit être annulé et la base
// ne doit pas être fermée avant la fin des workers.
func TestServeCancelsWorkersOnHTTPFailure(t *testing.T) {
	c := fastCore(t)
	started := make(chan struct{})
	cancelled := make(chan struct{})
	c.Registry.Register("test.wait", true, func(ctx context.Context, jc *jobs.JobCtx) (json.RawMessage, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	})
	if _, _, err := c.Queue.Enqueue(context.Background(), jobs.EnqueueParams{Kind: "test.wait"}); err != nil {
		t.Fatal(err)
	}
	real, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fail := make(chan struct{})
	ln := &failingListener{Listener: real, fail: fail}
	done := make(chan error, 1)
	go func() {
		done <- serve(context.Background(), quietLog(), c, ln, serveConfig{DrainTimeout: 5 * time.Second}, nil)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("le travail n'a pas démarré")
	}
	close(fail) // Accept échoue : chemin d'erreur HTTP, ctx parent jamais annulé
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("l'échec HTTP doit être remonté")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve ne rend pas la main")
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("le contexte des workers n'a pas été annulé")
	}
	if _, err := c.Store.SchemaVersion(context.Background()); err != nil {
		t.Fatalf("la base a été fermée avant l'arrêt des workers : %v", err)
	}
}

// Un handler qui ignore l'annulation ne doit pas bloquer l'arrêt au-delà du délai
// de drain, et la base doit rester ouverte.
func TestServeDrainTimeoutKeepsDBOpen(t *testing.T) {
	c := fastCore(t)
	started := make(chan struct{})
	release := make(chan struct{})
	c.Registry.Register("test.stubborn", true, func(ctx context.Context, jc *jobs.JobCtx) (json.RawMessage, error) {
		close(started)
		<-release // ignore ctx
		return nil, nil
	})
	defer close(release)
	if _, _, err := c.Queue.Enqueue(context.Background(), jobs.EnqueueParams{Kind: "test.stubborn"}); err != nil {
		t.Fatal(err)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, quietLog(), c, ln, serveConfig{DrainTimeout: 300 * time.Millisecond}, ready)
	}()
	<-ready
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, errDrainTimeout) {
			t.Fatalf("attendu errDrainTimeout : %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("l'arrêt n'est pas borné")
	}
	if _, err := c.Store.SchemaVersion(context.Background()); err != nil {
		t.Fatalf("base fermée malgré des travaux en cours : %v", err)
	}
}
