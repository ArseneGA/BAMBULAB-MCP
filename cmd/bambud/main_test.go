package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
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
	go func() { done <- run(ctx, log, "127.0.0.1:0", cfg, httpapi.Options{}, ready) }()
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
	err := run(context.Background(), log, "0.0.0.0:0", cfg, httpapi.Options{}, nil)
	if err == nil || !strings.Contains(err.Error(), "jeton") {
		t.Fatalf("démarrage hors loopback sans jeton non refusé : %v", err)
	}
}
