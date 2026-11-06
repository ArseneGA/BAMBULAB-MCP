package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
)

func newCore(t *testing.T) *core.Core {
	t.Helper()
	c, err := core.Open(context.Background(), core.Config{DataDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func do(h http.Handler, method, path, host string, hdr map[string]string) int {
	req := httptest.NewRequest(method, path, nil)
	req.Host = host
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestHostValidation(t *testing.T) {
	h := NewHandlerWithOptions(newCore(t), Options{AllowedHosts: []string{"bambu.lan"}})
	for host, want := range map[string]int{
		"127.0.0.1:7878": 200, "localhost:7878": 200, "[::1]:7878": 200, "localhost": 200,
		"bambu.lan:7878": 200, "BAMBU.LAN": 200,
		"evil.example.com": 403, "evil.example.com:7878": 403, "10.0.0.5:7878": 403, "127.0.0.1.evil.com": 403,
	} {
		if got := do(h, "GET", "/api/capabilities", host, nil); got != want {
			t.Errorf("Host %q : %d, attendu %d", host, got, want)
		}
	}
}

func TestCrossOriginProtection(t *testing.T) {
	h := NewHandler(newCore(t))
	const host = "127.0.0.1:7878"
	cases := []struct {
		name string
		hdr  map[string]string
		want int // 404 = la requête a atteint le handler (job inconnu)
	}{
		{"client non navigateur", nil, 404},
		{"même origine", map[string]string{"Sec-Fetch-Site": "same-origin"}, 404},
		{"cross-site", map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
		{"Origin étrangère", map[string]string{"Origin": "http://evil.example.com"}, 403},
		{"Origin identique à Host", map[string]string{"Origin": "http://127.0.0.1:7878"}, 404},
	}
	for _, c := range cases {
		if got := do(h, "POST", "/api/jobs/job_x/cancel", host, c.hdr); got != c.want {
			t.Errorf("%s : %d, attendu %d", c.name, got, c.want)
		}
	}
	// Les lectures (GET) ne sont pas bloquées par la protection cross-origin.
	if got := do(h, "GET", "/api/capabilities", host, map[string]string{"Sec-Fetch-Site": "cross-site"}); got != 200 {
		t.Errorf("GET cross-site : %d", got)
	}
}

func TestAPIToken(t *testing.T) {
	const tok = "0123456789abcdef-secret"
	h := NewHandlerWithOptions(newCore(t), Options{APIToken: tok})
	const host = "127.0.0.1:7878"
	if got := do(h, "GET", "/api/capabilities", host, nil); got != 401 {
		t.Errorf("sans jeton : %d", got)
	}
	if got := do(h, "GET", "/api/capabilities", host, map[string]string{"Authorization": "Bearer faux"}); got != 401 {
		t.Errorf("mauvais jeton : %d", got)
	}
	if got := do(h, "GET", "/api/capabilities", host, map[string]string{"Authorization": tok}); got != 401 {
		t.Errorf("jeton sans schéma Bearer : %d", got)
	}
	if got := do(h, "GET", "/api/capabilities", host, map[string]string{"Authorization": "Bearer " + tok}); got != 200 {
		t.Errorf("bon jeton : %d", got)
	}
	if got := do(h, "POST", "/mcp", host, nil); got != 401 {
		t.Errorf("/mcp sans jeton : %d", got)
	}
	if got := do(h, "GET", "/health", host, nil); got != 200 {
		t.Errorf("/health doit rester ouvert : %d", got)
	}
}

func TestValidateListen(t *testing.T) {
	ok := []struct{ addr, tok string }{
		{"127.0.0.1:7878", ""}, {"localhost:7878", ""}, {"[::1]:7878", ""}, {"127.0.0.1:0", ""},
		{"0.0.0.0:7878", "0123456789abcdef"}, {":7878", "0123456789abcdef"}, {"192.168.1.5:7878", "0123456789abcdef"},
	}
	for _, c := range ok {
		if err := ValidateListen(c.addr, c.tok); err != nil {
			t.Errorf("%q refusé : %v", c.addr, err)
		}
	}
	bad := []struct{ addr, tok string }{
		{"0.0.0.0:7878", ""}, {":7878", ""}, {"[::]:7878", ""}, {"192.168.1.5:7878", ""}, {"bambu.lan:7878", ""},
		{"127.0.0.1:7878", "court"}, {"pas-une-adresse", ""},
	}
	for _, c := range bad {
		if err := ValidateListen(c.addr, c.tok); err == nil {
			t.Errorf("%q / %q accepté", c.addr, c.tok)
		}
	}
}
