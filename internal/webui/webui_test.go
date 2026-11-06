package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func get(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestHandlerNotBuilt(t *testing.T) {
	h := handlerFor(fstest.MapFS{})
	rec := get(h, "GET", "/")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "Interface non construite") {
		t.Fatalf("page de repli attendue : %d %s", rec.Code, rec.Body.String())
	}
	if rec := get(h, "GET", "/bibliotheque"); !strings.Contains(rec.Body.String(), "non construite") {
		t.Fatal("la page de repli doit couvrir toutes les routes")
	}
	// L'API n'est jamais masquée par la page de repli.
	rec = get(h, "GET", "/api/inconnu")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Header().Get("Content-Type"), "json") {
		t.Fatalf("/api inconnu : %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestHandlerBuilt(t *testing.T) {
	h := handlerFor(fstest.MapFS{
		"index.html":       {Data: []byte("<!doctype html><title>Atelier</title>")},
		"assets/app-1.js":  {Data: []byte("console.log(1)")},
		"assets/app-1.css": {Data: []byte("body{}")},
	})
	if rec := get(h, "GET", "/"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Atelier") || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("index : %d %v", rec.Code, rec.Header())
	}
	// Repli SPA pour une route client.
	if rec := get(h, "GET", "/modele/mdl_abc"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Atelier") {
		t.Fatalf("repli SPA : %d", rec.Code)
	}
	rec := get(h, "GET", "/assets/app-1.js")
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") || rec.Body.String() != "console.log(1)" {
		t.Fatalf("asset : %d %v", rec.Code, rec.Header())
	}
	// Un script manquant ne doit pas répondre du HTML.
	if rec := get(h, "GET", "/assets/absent.js"); rec.Code != 404 {
		t.Fatalf("asset absent : %d", rec.Code)
	}
	// /api et /mcp ne reçoivent jamais le repli HTML.
	for _, p := range []string{"/api/models/x", "/mcp", "/health"} {
		if rec := get(h, "GET", p); rec.Code != 404 || strings.Contains(rec.Body.String(), "<html") {
			t.Fatalf("%s : %d", p, rec.Code)
		}
	}
	if rec := get(h, "POST", "/health"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /health : %d", rec.Code)
	}
	if rec := get(h, "POST", "/"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST / : %d", rec.Code)
	}
}

func TestEmbeddedPlaceholder(t *testing.T) {
	// Sans build, le dossier embarqué ne contient que .gitkeep : le handler reste utilisable.
	h := Handler()
	rec := get(h, "GET", "/")
	if Built() {
		if rec.Code != 200 {
			t.Fatalf("interface construite : %d", rec.Code)
		}
		return
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("interface non construite : %d", rec.Code)
	}
}
