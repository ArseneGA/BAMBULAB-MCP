// Package pagetest : contrôles partagés par les tests des pages HUMAINES rendues côté serveur. Chaque page doit utiliser le
// gabarit commun (humanauth.Layout), sans JavaScript en ligne ni ressource externe (CSP default-src 'none').
package pagetest

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
)

var (
	scriptTag   = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`)
	handlerAttr = regexp.MustCompile(`(?i)\son[a-z]+\s*=`)
	extSrc      = regexp.MustCompile(`(?i)\b(src|srcset|poster|data)\s*=\s*["']?\s*(https?:)?//`)
	extLink     = regexp.MustCompile(`(?i)<link\b[^>]*\bhref\s*=\s*["']?(https?:)?//`)
	extCSS      = regexp.MustCompile(`(?i)(@import|url\()\s*["']?\s*(https?:)?//`)
	styleAttr   = regexp.MustCompile(`(?i)<[a-z][^>]*\sstyle\s*=`)
	jsURL       = regexp.MustCompile(`(?i)(href|action)\s*=\s*["']?\s*javascript:`)
)

// Problems liste ce qui s'écarte du gabarit commun et des règles de la CSP des pages humaines (vide si tout va bien).
func Problems(body string) []string {
	var p []string
	if !strings.Contains(body, humanauth.PageMarker) || !strings.Contains(body, `class="pt-card"`) || !strings.Contains(body, `class="pt-head"`) {
		p = append(p, "gabarit commun absent (en-tête Printalk et carte centrale)")
	}
	if !strings.Contains(body, "<footer class=\"pt-foot\">") || !strings.Contains(body, humanauth.ProductName) || !strings.Contains(body, humanauth.Tagline) {
		p = append(p, "nom, baseline ou pied du gabarit absents")
	}
	if !strings.Contains(body, "prefers-color-scheme:dark") || !strings.Contains(body, `name="viewport"`) {
		p = append(p, "thème sombre ou viewport mobile absents")
	}
	for _, m := range scriptTag.FindAllStringSubmatch(body, -1) {
		if !strings.Contains(m[1], `src="/`) || strings.TrimSpace(m[2]) != "" {
			p = append(p, "script en ligne ou non statique : "+m[0])
		}
	}
	if handlerAttr.MatchString(body) {
		p = append(p, "gestionnaire d'évènement en ligne (on…=)")
	}
	if jsURL.MatchString(body) {
		p = append(p, "URL javascript:")
	}
	if extSrc.MatchString(body) || extLink.MatchString(body) || extCSS.MatchString(body) {
		p = append(p, "ressource externe")
	}
	if styleAttr.MatchString(body) {
		p = append(p, "attribut style en ligne")
	}
	if strings.Contains(body, "#f6f5f2") || strings.Contains(body, "#1f4f8f") {
		p = append(p, "ancien style (fond beige, bouton bleu)")
	}
	return p
}

// Check échoue le test pour chaque écart de Problems.
func Check(t testing.TB, name, body string) {
	t.Helper()
	for _, p := range Problems(body) {
		t.Errorf("page %s : %s", name, p)
	}
}

// CheckCSP : la CSP d'une page humaine ne contient jamais de directive qui permettrait un script ou une ressource
// externes. Elle doit exactement valoir want (valeur d'avant l'habillage).
func CheckCSP(t testing.TB, name string, h http.Header, want string) {
	t.Helper()
	if got := h.Get("Content-Security-Policy"); got != want {
		t.Errorf("CSP de %s modifiée :\n  obtenue  %q\n  attendue %q", name, got, want)
	}
}

// Dump écrit le rendu d'une page dans le dossier donné par BAMBU_PAGES_DUMP (contrôle visuel local), sinon ne fait rien.
func Dump(t testing.TB, name string, rec *httptest.ResponseRecorder) {
	t.Helper()
	dir := os.Getenv("BAMBU_PAGES_DUMP")
	if dir == "" {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s.html", name)), rec.Body.Bytes(), 0o644)
}

// DumpBody est Dump pour un corps déjà lu.
func DumpBody(t testing.TB, name, body string) {
	t.Helper()
	dir := os.Getenv("BAMBU_PAGES_DUMP")
	if dir == "" {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, name+".html"), []byte(body), 0o644)
}
