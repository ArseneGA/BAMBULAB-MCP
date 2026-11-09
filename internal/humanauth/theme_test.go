package humanauth_test

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth/pagetest"
)

// Valeurs de CSP d'AVANT l'habillage Printalk : l'habillage ne doit pas les élargir d'un octet.
const (
	cspBase   = "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"
	cspImg    = "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"
	cspImgJS  = "default-src 'none'; img-src 'self'; script-src 'self'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"
	cspJS     = "default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"
	cspFormFA = "default-src 'none'; style-src 'unsafe-inline'; form-action 'self' https://claude.ai; frame-ancestors 'none'; base-uri 'none'"
)

func TestPageHeadersCSPUnchanged(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(http.ResponseWriter)
		want string
	}{
		{"PageHeaders", humanauth.PageHeaders, cspBase},
		{"PageHeadersWithImages", humanauth.PageHeadersWithImages, cspImg},
		{"PageHeadersWith(images)", func(w http.ResponseWriter) { humanauth.PageHeadersWith(w, true, false) }, cspImg},
		{"PageHeadersWith(images,script)", func(w http.ResponseWriter) { humanauth.PageHeadersWith(w, true, true) }, cspImgJS},
		{"PageHeadersWith(script)", func(w http.ResponseWriter) { humanauth.PageHeadersWith(w, false, true) }, cspJS},
		{"PageHeadersFormAction", func(w http.ResponseWriter) { humanauth.PageHeadersFormAction(w, "https://claude.ai") }, cspFormFA},
	} {
		rec := httptest.NewRecorder()
		c.set(rec)
		pagetest.CheckCSP(t, c.name, rec.Header(), c.want)
	}
}

func TestLayoutIsPrintalk(t *testing.T) {
	if humanauth.ProductName != "Printalk" || humanauth.Tagline != "Fabriquer en parlant" {
		t.Fatalf("identité : %q %q", humanauth.ProductName, humanauth.Tagline)
	}
	// la feuille embarquée reprend les jetons de la DA web (web/src/design/tokens.css)
	for _, tok := range []string{"#16a06b", "#3ee39c", "#095a40", "#f4f7f5", "#0f1714", "DM Sans", "min-height:34px", "prefers-color-scheme:dark", ":focus-visible", "max-width:540px"} {
		if !strings.Contains(humanauth.Style, tok) {
			t.Errorf("la feuille commune ne contient pas %q", tok)
		}
	}
}

// Toutes les pages humaines de ce paquet (enroll, done/erreurs 403, code de lancement, passkeys) : gabarit commun,
// pas de JS en ligne, pas de ressource externe, CSP identique à celle d'avant.
func TestHumanPagesUseCommonTheme(t *testing.T) {
	st0, _ := launchStore(t)
	svc := humanauth.New(st0, humanauth.Options{})
	mux := http.NewServeMux()
	svc.RegisterPages(mux)
	svc.RegisterLaunchCodePages(mux)
	svc.RegisterPasskeyPages(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	cl := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string) (int, string, http.Header) { return nav(cl, "GET", ts.URL+path, nil, "none") }

	// /enroll (formulaire), puis POST refusé (403 « Requête refusée »), puis POST sans navigateur enregistré
	st, body, h := get("/enroll")
	if st != 200 {
		t.Fatalf("/enroll : %d", st)
	}
	pagetest.Check(t, "/enroll", body)
	pagetest.CheckCSP(t, "/enroll", h, cspBase)
	pagetest.DumpBody(t, "enroll", body)
	if !strings.Contains(body, `name="code"`) || !strings.Contains(body, `name="next"`) {
		t.Error("champs du formulaire /enroll modifiés")
	}
	st, body, h = nav(cl, "POST", ts.URL+"/enroll", nil, "cross-site")
	if st != 403 {
		t.Fatalf("POST cross-site : %d", st)
	}
	pagetest.Check(t, "/enroll 403", body)
	pagetest.CheckCSP(t, "/enroll 403", h, cspBase)
	pagetest.DumpBody(t, "erreur-403", body)
	st, body, _ = nav(cl, "POST", ts.URL+"/setup/launch-code", nil, "same-origin")
	if st != 403 {
		t.Fatalf("POST sans session : %d", st)
	}
	pagetest.Check(t, "403 navigateur non enregistré", body)

	enrolled(t, ts, svc, cl)
	st, body, h = get("/enroll")
	pagetest.Check(t, "/enroll (déjà enregistré)", body)

	st, body, h = get("/setup/launch-code")
	if st != 200 {
		t.Fatalf("/setup/launch-code : %d", st)
	}
	pagetest.Check(t, "/setup/launch-code", body)
	pagetest.CheckCSP(t, "/setup/launch-code", h, cspBase)
	pagetest.DumpBody(t, "setup-launch-code", body)
	for _, f := range []string{`name="nonce"`, `name="action" value="set"`, `name="code"`, `name="code2"`, "Ne tape jamais ce code dans la conversation"} {
		if !strings.Contains(body, f) {
			t.Errorf("/setup/launch-code : %s absent", f)
		}
	}

	st, body, h = get("/setup/passkeys")
	if st != 200 {
		t.Fatalf("/setup/passkeys : %d", st)
	}
	pagetest.Check(t, "/setup/passkeys", body)
	pagetest.CheckCSP(t, "/setup/passkeys", h, cspJS)
	pagetest.DumpBody(t, "setup-passkeys", body)
	if !strings.Contains(body, `<script src="`+humanauth.PasskeyScriptPath+`" defer></script>`) {
		t.Error("le script statique des passkeys n'est plus chargé")
	}
}

// Aucun gabarit HTML de page humaine n'échappe au gabarit commun : tout fichier Go non-test de internal/ qui écrit un
// document complet (<!doctype html) doit être listé ici, avec sa raison.
func TestNoHumanPageOutsideCommonLayout(t *testing.T) {
	allowed := map[string]string{
		"theme.go":     "gabarit commun",
		"app_views.go": "vue MCP App affichée dans Claude (iframe), pas une page humaine du navigateur",
	}
	root := filepath.Join("..")
	re := regexp.MustCompile(`(?i)<!doctype html`)
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		b, _ := os.ReadFile(p)
		if re.Match(b) {
			if _, ok := allowed[filepath.Base(p)]; !ok {
				t.Errorf("%s écrit un document HTML complet hors du gabarit commun (humanauth.Layout)", p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
