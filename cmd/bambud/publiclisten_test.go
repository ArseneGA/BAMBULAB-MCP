package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
)

func TestServeWithPublicListener(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	c, err := core.Open(ctx, core.Config{DataDir: t.TempDir(), Logger: log, PollInterval: 20 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	pln, _ := net.Listen("tcp", "127.0.0.1:0")
	ready := make(chan string, 1)
	done := make(chan error, 1)
	sc := serveConfig{HTTP: httpapi.Options{APIToken: "0123456789abcdef"}, PublicListen: pln.Addr().String(), publicLn: pln}
	go func() { done <- serve(ctx, log, c, ln, sc, ready) }()
	<-ready

	get := func(base, path string) (int, string) {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := get("http://"+pln.Addr().String(), "/health"); code != 200 || strings.TrimSpace(body) != `{"status":"ok"}` {
		t.Errorf("/health public : %d %s", code, body)
	}
	if code, _ := get("http://"+pln.Addr().String(), "/api/capabilities"); code != 404 {
		t.Errorf("/api/capabilities public : %d", code)
	}
	if code, body := get("http://"+ln.Addr().String(), "/health"); code != 200 || !strings.Contains(body, "schema_version") {
		t.Errorf("/health principal : %d %s", code, body)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("arrêt trop long")
	}
	c.Close()
	if _, err := http.Get("http://" + pln.Addr().String() + "/health"); err == nil {
		t.Error("l'écouteur public doit être fermé à l'arrêt")
	}
}

func TestValidatePublicListenURL(t *testing.T) {
	if u, err := validatePublicListenURL("https://a.ts.net/", "127.0.0.1:7879"); err != nil || u != "https://a.ts.net" {
		t.Errorf("%q %v", u, err)
	}
	if u, err := validatePublicListenURL("", ""); err != nil || u != "" {
		t.Errorf("vide : %q %v", u, err)
	}
	for _, bad := range [][2]string{{"https://a.ts.net", ""}, {"http://a.ts.net", "127.0.0.1:1"}, {"https://a.ts.net/x", "127.0.0.1:1"}, {"ftp://a", "127.0.0.1:1"}} {
		if _, err := validatePublicListenURL(bad[0], bad[1]); err == nil {
			t.Errorf("%v doit être refusé", bad)
		}
	}
	if w := publicListenWarnings("127.0.0.1:7879", "https://a.ts.net", "https://a.ts.net"); len(w) != 1 || !strings.Contains(w[0], "404") {
		t.Errorf("avertissements : %v", w)
	}
	if w := publicListenWarnings("127.0.0.1:7879", "https://a.ts.net", "https://a.ts.net:8443"); len(w) != 0 {
		t.Errorf("réglage sain : %v", w)
	}
}
