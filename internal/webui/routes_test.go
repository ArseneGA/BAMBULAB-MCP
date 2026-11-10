package webui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

// G10 CA-N4 : aucune route de l'interface (web/src/nav.ts, anciennes URL de web/src/redirects.ts) ne peut commencer
// par un préfixe réservé au serveur (pages humaines, API, MCP). La liste est partagée avec le test vitest (web/reserved-routes.json).
func TestSPARoutesDoNotCollideWithServerPrefixes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "web", "reserved-routes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reservedList struct {
		Prefixes []string `json:"prefixes"`
	}
	if err := json.Unmarshal(raw, &reservedList); err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{"/setup", "/confirm", "/stop", "/action", "/enroll", "/oauth", "/api", "/mcp", "/health", "/agents/pair"} {
		found := false
		for _, p := range reservedList.Prefixes {
			if p == must {
				found = true
			}
		}
		if !found {
			t.Errorf("le préfixe réservé %s manque dans web/reserved-routes.json", must)
		}
	}
	collides := func(p string) string {
		for _, pre := range reservedList.Prefixes {
			if p == pre || strings.HasPrefix(p, pre+"/") {
				return pre
			}
		}
		return ""
	}
	var paths []string
	for _, f := range []struct{ file, re string }{
		{filepath.Join("..", "..", "web", "src", "nav.ts"), `path:\s*'(/[^']*)'`},
		{filepath.Join("..", "..", "web", "src", "redirects.ts"), `from:\s*'(/[^']*)'`},
	} {
		b, err := os.ReadFile(f.file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range regexp.MustCompile(f.re).FindAllStringSubmatch(string(b), -1) {
			paths = append(paths, m[1])
		}
	}
	if len(paths) < 25 {
		t.Fatalf("trop peu de routes lues (%d) : l'expression de lecture est-elle à jour ?", len(paths))
	}
	h := handlerFor(fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>Printalk</title>")}})
	param := regexp.MustCompile(`:[A-Za-z]+`)
	for _, p := range paths {
		if pre := collides(p); pre != "" {
			t.Errorf("la route %s entre dans le préfixe réservé %s", p, pre)
		}
		// Chaque route de l'interface reçoit le repli SPA (statut 200).
		url := param.ReplaceAllString(p, "x")
		if rec := get(h, "GET", url); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Printalk") {
			t.Errorf("%s : repli SPA attendu, reçu %d", url, rec.Code)
		}
	}
	// Une adresse inconnue reçoit aussi le repli (statut 200) : la page « Introuvable » est rendue côté client.
	if rec := get(h, "GET", "/une/adresse/inconnue"); rec.Code != 200 {
		t.Errorf("adresse inconnue : statut 200 attendu, reçu %d", rec.Code)
	}
	// Les préfixes du serveur lui-même restent hors du repli HTML.
	for _, p := range []string{"/api/models/x", "/mcp", "/health"} {
		if rec := get(h, "GET", p); rec.Code != 404 || strings.Contains(rec.Body.String(), "<html") {
			t.Errorf("%s : jamais de repli HTML, reçu %d", p, rec.Code)
		}
	}
}
