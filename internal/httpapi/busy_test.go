package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

func busyReq(h http.Handler, remote string, hdr map[string]string, bearer string) (int, map[string]any) {
	req := httptest.NewRequest("GET", "/api/system/busy", nil)
	req.Host = "127.0.0.1:7878"
	req.RemoteAddr = remote
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// /api/system/busy : lecture seule, loopback direct seulement, jeton exigé quand il est configuré, absent de /health.
func TestSystemBusyEndpoint(t *testing.T) {
	c := newCore(t)
	const tok = "0123456789abcdef0123"
	h := NewHandlerWithOptions(c, Options{APIToken: tok})

	if st, out := busyReq(h, "127.0.0.1:50000", nil, tok); st != 200 || out["busy"] != false {
		t.Fatalf("loopback + jeton, service au repos : %d %v", st, out)
	}
	if st, _ := busyReq(h, "127.0.0.1:50000", nil, ""); st != 401 {
		t.Fatalf("sans jeton : %d, attendu 401", st)
	}
	if st, _ := busyReq(h, "100.64.0.7:50000", nil, tok); st != 403 {
		t.Fatalf("hôte distant : %d, attendu 403", st)
	}
	for _, hdr := range []map[string]string{{"X-Forwarded-For": "100.64.0.7"}, {"Forwarded": "for=1.2.3.4"}, {"X-Real-IP": "1.2.3.4"},
		{"Tailscale-User-Login": "x@y"}, {"Via": "1.1 proxy"}} {
		if st, _ := busyReq(h, "127.0.0.1:50000", hdr, tok); st != 403 {
			t.Errorf("proxy local %v : %d, attendu 403", hdr, st)
		}
	}
	if st, _ := busyReq(h, "[::1]:50000", nil, tok); st != 200 {
		t.Fatalf("::1 : %d", st)
	}

	// /health reste public et n'annonce rien de l'activité d'impression.
	req := httptest.NewRequest("GET", "/health", nil)
	req.Host = "127.0.0.1:7878"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var health map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &health)
	if _, leaked := health["busy"]; leaked || health["status"] != "ok" {
		t.Fatalf("/health : %v", health)
	}

	// Une impression active : busy = true avec la raison.
	if err := c.Store.PutDoc(context.Background(), "test", "print_jobs", "pj-1", printing.Job{ID: "pj-1", State: "PRINTING"}); err != nil {
		t.Fatal(err)
	}
	st, out := busyReq(h, "127.0.0.1:50000", nil, tok)
	if st != 200 || out["busy"] != true {
		t.Fatalf("impression active : %d %v", st, out)
	}
	rs, _ := out["reasons"].([]any)
	if len(rs) == 0 || rs[0].(map[string]any)["code"] != printing.BusyPrintRunning {
		t.Fatalf("raisons : %v", out["reasons"])
	}
}
