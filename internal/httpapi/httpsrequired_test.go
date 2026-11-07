package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHumanPageHTTPSRequired(t *testing.T) {
	opts := Options{APIToken: "0123456789abcdef-secret", PublicURLs: []string{"http://192.168.0.8:7878", "https://bambu.ts.net"}}
	h := NewHandlerWithOptions(newCore(t), opts)
	get := func(method, path, host string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.Host = host
		req.RemoteAddr = "127.0.0.1:5555"
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for _, m := range []string{"GET", "POST"} {
		rec := get(m, "/enroll?next=%2Fx", "192.168.0.8:7878", map[string]string{"Origin": "null"})
		body := rec.Body.String()
		if rec.Code != http.StatusForbidden || !strings.Contains(body, "exige HTTPS") || !strings.Contains(body, `href="https://bambu.ts.net/enroll?next=%2Fx"`) {
			t.Errorf("%s http LAN : %d %s", m, rec.Code, body)
		}
	}
	// https annoncé par un proxy de confiance (loopback) : la page est servie normalement
	if rec := get("GET", "/enroll", "192.168.0.8:7878", map[string]string{"X-Forwarded-Proto": "https"}); rec.Code != 200 {
		t.Errorf("https via proxy : %d", rec.Code)
	}
	// loopback : jamais d'interception
	if rec := get("GET", "/enroll", "localhost:7878", nil); rec.Code != 200 {
		t.Errorf("localhost : %d", rec.Code)
	}
	// l'état de session JSON (SPA) et l'API restent accessibles en http
	if rec := get("GET", "/api/session", "192.168.0.8:7878", nil); strings.Contains(rec.Body.String(), "exige HTTPS") || rec.Header().Get(HTTPSNeededHeader) != "https://bambu.ts.net" {
		t.Errorf("/api/session : %q %v", rec.Body.String(), rec.Header())
	}
	if rec := get("GET", "/api/session", "localhost:7878", nil); len(rec.Header().Values(HTTPSNeededHeader)) != 0 {
		t.Errorf("en-tête https présent sur localhost")
	}
	if rec := get("GET", "/api/capabilities", "192.168.0.8:7878", map[string]string{"Authorization": "Bearer 0123456789abcdef-secret"}); rec.Code != 200 {
		t.Errorf("API http avec jeton : %d", rec.Code)
	}
}

func TestHumanPageHTTPSRequiredNoHTTPSURL(t *testing.T) {
	h := NewHandlerWithOptions(newCore(t), Options{APIToken: "0123456789abcdef-secret", PublicURL: "http://192.168.0.8:7878"})
	req := httptest.NewRequest("GET", "/confirm/abc", nil)
	req.Host = "192.168.0.8:7878"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "tailscale serve") {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
}
