package webui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func readWebFile(t *testing.T, rel ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"..", "..", "web"}, rel...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ruleBody renvoie le corps de la première règle CSS dont le sélecteur est exactement sel.
func ruleBody(css, sel string) string {
	re := regexp.MustCompile(`(?m)(?:^|[}\s])` + regexp.QuoteMeta(sel) + `\s*\{([^}]*)\}`)
	if m := re.FindStringSubmatch(css); m != nil {
		return m[1]
	}
	return ""
}

// G10 CA-N6 / CA-A2 : la barre basse mobile a 5 emplacements, des cibles d'au moins 44 px, la zone sûre respectée,
// et la marge basse du contenu ; prefers-reduced-motion coupe halos et animations (vitest neutralise les feuilles de style : le test est ici).
func TestShellCSSMobileAndMotion(t *testing.T) {
	css := readWebFile(t, "src", "shell.css")
	bar := ruleBody(css, ".tabbar")
	if !strings.Contains(bar, "repeat(5") || !strings.Contains(bar, "env(safe-area-inset-bottom)") || !strings.Contains(bar, "position: fixed") {
		t.Errorf(".tabbar : 5 emplacements, position fixe et zone sûre attendus : %q", bar)
	}
	item := ruleBody(css, ".tabbar a, .tabbar button")
	if m := regexp.MustCompile(`min-height:\s*(\d+)px`).FindStringSubmatch(item); m == nil || m[1] < "44" {
		t.Errorf("cible tactile de la barre basse : min-height ≥ 44 px attendu : %q", item)
	}
	if !strings.Contains(item, "min-width: 44px") {
		t.Errorf("cible tactile de la barre basse : min-width 44 px attendu : %q", item)
	}
	if !strings.Contains(ruleBody(css, ".tabbar .tb-t"), "font-size: 12px") {
		t.Errorf("libellé de la barre basse : toujours visible (non masqué)")
	}
	if strings.Contains(ruleBody(css, ".tabbar .tb-t"), "display: none") {
		t.Errorf("les libellés de la barre basse ne doivent jamais être masqués")
	}
	if !strings.Contains(css, ".main { padding: var(--sp-5) var(--sp-4) calc(56px + env(safe-area-inset-bottom)") {
		t.Errorf("le contenu doit garder une marge basse égale à la barre")
	}
	if !strings.Contains(css, ".crumb ol { display: none; }") || !strings.Contains(css, ".crumb-back") {
		t.Errorf("sous 761 px, le fil d'Ariane doit laisser la place au lien « ← Parent »")
	}
	if !strings.Contains(css, "max-width: 760px") {
		t.Errorf("le seuil mobile (< 761 px) est attendu")
	}
	reduced := regexp.MustCompile(`prefers-reduced-motion: reduce\) \{[^\n]*\.halos \{ display: none; \}[^\n]*\.nav-progress::after \{ animation: none`)
	if !reduced.MatchString(css) {
		t.Errorf("prefers-reduced-motion doit couper les halos et l'animation de la barre de chargement")
	}
}

// Cibles tactiles : sur écran tactile (pointer: coarse), toutes les commandes font au moins 44 px (tokens.css).
func TestTokensCoarsePointerTargets(t *testing.T) {
	css := readWebFile(t, "shared", "tokens.css")
	m := regexp.MustCompile(`@media \(pointer: coarse\) \{[^}]*--control-h:\s*(\d+)px[^}]*--control-h-sm:\s*(\d+)px`).FindStringSubmatch(css)
	if m == nil || m[1] < "44" || m[2] < "44" {
		t.Errorf("tokens.css : cibles ≥ 44 px attendues quand pointer: coarse : %v", m)
	}
}

// Pas de couleur codée en dur dans la nouvelle feuille de coque : jetons seulement (CA-A3, compteur qui ne peut que baisser).
func TestShellCSSUsesTokensOnly(t *testing.T) {
	css := readWebFile(t, "src", "shell.css")
	hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	if found := hex.FindAllString(css, -1); len(found) > 0 {
		t.Errorf("shell.css : couleurs codées en dur interdites : %v", found)
	}
}
