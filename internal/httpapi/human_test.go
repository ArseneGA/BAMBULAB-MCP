package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
)

func humanEnv(t *testing.T, opts httpapi.Options) (*core.Core, *httptest.Server) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c, err := core.Open(ctx, core.Config{DataDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		PollInterval: 10 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	srv := httptest.NewServer(httpapi.NewHandlerWithOptions(c, opts))
	c.Catalog.SetBaseURL(srv.URL)
	t.Cleanup(func() { srv.Close(); cancel(); <-done; c.Close() })
	return c, srv
}

// browser : un navigateur de test. nav envoie les en-têtes Sec-Fetch d'une navigation de formulaire.
type browser struct {
	t  *testing.T
	cl *http.Client
}

func newBrowser(t *testing.T, follow bool) *browser {
	jar, _ := cookiejar.New(nil)
	cl := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	if !follow {
		cl.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return &browser{t: t, cl: cl}
}

func (b *browser) get(u string, hdr map[string]string) (*http.Response, string) {
	b.t.Helper()
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := b.cl.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res, string(body)
}

func (b *browser) post(u string, f url.Values, hdr map[string]string) (*http.Response, string) {
	b.t.Helper()
	req, _ := http.NewRequest("POST", u, strings.NewReader(f.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	res, err := b.cl.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res, string(body)
}

func newCode(t *testing.T, c *core.Core) string {
	t.Helper()
	code, _, err := humanauth.New(c.Store, humanauth.Options{}).NewEnrollCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestEnrollFlowAndCookieFlags(t *testing.T) {
	c, srv := humanEnv(t, httpapi.Options{})
	b := newBrowser(t, true)
	// Mauvais code : refusé, aucun cookie.
	if res, _ := b.post(srv.URL+"/enroll", url.Values{"code": {"AAAA-BBBB"}}, nil); res.StatusCode != 403 || len(b.cl.Jar.Cookies(mustURL(srv.URL))) != 0 {
		t.Fatalf("mauvais code : %d", res.StatusCode)
	}
	code := newCode(t, c)
	res, body := b.post(srv.URL+"/enroll", url.Values{"code": {strings.ToLower(humanauth.FormatCode(code))}, "label": {"Chrome du bureau"}}, nil)
	if res.StatusCode != 200 || !strings.Contains(body, "enregistré") {
		t.Fatalf("enregistrement : %d %s", res.StatusCode, body)
	}
	var ck *http.Cookie
	for _, x := range res.Cookies() {
		if x.Name == humanauth.CookieName {
			ck = x
		}
	}
	if ck == nil || !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode || ck.Secure || ck.Path != "/" {
		t.Fatalf("cookie : %+v (HttpOnly, SameSite=Strict attendus ; Secure absent en http)", ck)
	}
	// Le jeton n'est jamais stocké en clair : ni le code ni le cookie ne figurent dans la base.
	for _, q := range []string{`SELECT code_hash FROM human_enroll_codes`, `SELECT token_hash FROM human_sessions`} {
		rows, err := c.Store.DB().Query(q)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var h string
			_ = rows.Scan(&h)
			if strings.Contains(h, code) || strings.Contains(h, ck.Value) {
				t.Fatalf("secret en clair dans %s", q)
			}
		}
		rows.Close()
	}
	// Code à usage unique.
	b2 := newBrowser(t, true)
	if res, _ := b2.post(srv.URL+"/enroll", url.Values{"code": {code}}, nil); res.StatusCode != 403 {
		t.Fatalf("code réutilisé : %d", res.StatusCode)
	}
	// /api/session : enregistré ou non, sans jeton d'API.
	var out map[string]any
	if st := getJSON(t, srv.URL+"/api/session", &out); st != 200 || out["enrolled"] != false {
		t.Fatalf("session anonyme : %d %v", st, out)
	}
	_, sb := b.get(srv.URL+"/api/session", nil)
	if !strings.Contains(sb, `"enrolled":true`) || strings.Contains(sb, ck.Value) {
		t.Fatalf("session enregistrée : %s", sb)
	}
	// Soumission d'enregistrement sans navigation de même origine (CSRF, client non navigateur) : refusée.
	code2 := newCode(t, c)
	if res, _ := newBrowser(t, true).post(srv.URL+"/enroll", url.Values{"code": {code2}}, map[string]string{"Sec-Fetch-Site": "", "Sec-Fetch-Mode": ""}); res.StatusCode != 403 {
		t.Fatalf("POST /enroll sans Sec-Fetch : %d", res.StatusCode)
	}
	// La session est révocable : la CLI révoque, le cookie ne vaut plus rien.
	svc := humanauth.New(c.Store, humanauth.Options{})
	ss, _ := svc.List(context.Background())
	if len(ss) != 1 {
		t.Fatalf("sessions : %+v", ss)
	}
	if _, err := svc.Revoke(context.Background(), ss[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, sb := b.get(srv.URL+"/api/session", nil); !strings.Contains(sb, `"enrolled":false`) {
		t.Fatalf("session révoquée encore valide : %s", sb)
	}
}

func TestSecureCookieWithHTTPSPublicURL(t *testing.T) {
	c, srv := humanEnv(t, httpapi.Options{PublicURL: "https://bambu.example.lan:7878"})
	b := newBrowser(t, true)
	res, _ := b.post(srv.URL+"/enroll", url.Values{"code": {newCode(t, c)}}, nil)
	var ck *http.Cookie
	for _, x := range res.Cookies() {
		ck = x
	}
	if res.StatusCode != 200 || ck == nil || !ck.Secure || !ck.HttpOnly {
		t.Fatalf("cookie Secure attendu avec une URL publique https : %d %+v", res.StatusCode, ck)
	}
	// Le nom d'hôte de l'URL publique est autorisé d'office.
	req, _ := http.NewRequest("GET", srv.URL+"/api/session", nil)
	req.Host = "bambu.example.lan:7878"
	r2, err := http.DefaultClient.Do(req)
	if err != nil || r2.StatusCode != 200 {
		t.Fatalf("hôte de l'URL publique : %v %v", r2, err)
	}
	r2.Body.Close()
}

func TestHumanPagesNeedCookieAPINeedsBearer(t *testing.T) {
	const token = "jeton-api-de-test-0123456789"
	c, srv := humanEnv(t, httpapi.Options{APIToken: token})
	anon := newBrowser(t, false)
	// API JSON : Bearer exigé.
	if res, _ := anon.get(srv.URL+"/api/printers", nil); res.StatusCode != 401 {
		t.Fatalf("API sans Bearer : %d", res.StatusCode)
	}
	if res, _ := anon.get(srv.URL+"/mcp", nil); res.StatusCode != 401 {
		t.Fatalf("/mcp sans Bearer : %d", res.StatusCode)
	}
	// Pages HTML : pas de Bearer, mais le cookie d'un navigateur enregistré (sinon redirection vers /enroll).
	for _, p := range []string{"/confirm/cf_x", "/stop/stp_x", "/setup/printer/prn_x", "/agents/pair"} {
		res, _ := anon.get(srv.URL+p, nil)
		if res.StatusCode != 303 || !strings.HasPrefix(res.Header.Get("Location"), "/enroll?next=") {
			t.Fatalf("%s sans cookie : %d %s", p, res.StatusCode, res.Header.Get("Location"))
		}
	}
	if res, body := anon.get(srv.URL+"/enroll", nil); res.StatusCode != 200 || !strings.Contains(body, "bambud enroll") {
		t.Fatalf("/enroll : %d", res.StatusCode)
	}
	var out map[string]any
	if st := getJSON(t, srv.URL+"/api/session", &out); st != 200 {
		t.Fatalf("/api/session sans Bearer : %d", st)
	}
	// Enregistré : la page répond (404/410 métier, plus de redirection).
	b := newBrowser(t, false)
	if res, _ := b.post(srv.URL+"/enroll", url.Values{"code": {newCode(t, c)}}, nil); res.StatusCode != 200 {
		t.Fatalf("enroll : %d", res.StatusCode)
	}
	if res, _ := b.get(srv.URL+"/confirm/cf_inconnue", nil); res.StatusCode != 410 {
		t.Fatalf("page de confirmation inconnue, navigateur enregistré : %d", res.StatusCode)
	}
	// Le cookie ne remplace pas le Bearer pour l'API.
	if res, _ := b.get(srv.URL+"/api/printers", nil); res.StatusCode != 401 {
		t.Fatalf("cookie utilisé comme Bearer : %d", res.StatusCode)
	}
}

func TestPostNeedsSameOriginNavigation(t *testing.T) {
	c, srv := humanEnv(t, httpapi.Options{})
	b := newBrowser(t, false)
	b.post(srv.URL+"/enroll", url.Values{"code": {newCode(t, c)}}, nil)
	for name, hdr := range map[string]map[string]string{
		"sans Sec-Fetch":      {"Sec-Fetch-Site": "", "Sec-Fetch-Mode": ""},
		"cross-site":          {"Sec-Fetch-Site": "cross-site"},
		"same-site":           {"Sec-Fetch-Site": "same-site"},
		"mode cors (fetch)":   {"Sec-Fetch-Mode": "cors"},
		"mode no-cors":        {"Sec-Fetch-Mode": "no-cors"},
		"navigate, none":      {"Sec-Fetch-Site": "none"},
		"origine étrangère":   {"Origin": "http://evil.example"},
		"referer ignoré mais": {"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate"},
	} {
		if res, _ := b.post(srv.URL+"/stop/stp_x", url.Values{"nonce": {"x"}}, hdr); res.StatusCode != 403 {
			t.Errorf("%s : %d, 403 attendu", name, res.StatusCode)
		}
	}
	// Non enregistré : 403 aussi, même avec de bons en-têtes.
	if res, _ := newBrowser(t, false).post(srv.URL+"/confirm/cf_x", url.Values{"nonce": {"x"}}, nil); res.StatusCode != 403 {
		t.Errorf("POST sans cookie : %d", res.StatusCode)
	}
}

func TestPairingNeedsHumanChannel(t *testing.T) {
	c, srv := humanEnv(t, httpapi.Options{})
	// Plus de jeton par JSON, même depuis la machine locale.
	if st := postJSON(t, srv.URL+"/api/agents/pairing", map[string]any{}, nil); st != 410 {
		t.Fatalf("POST /api/agents/pairing : %d, 410 attendu", st)
	}
	var n int
	_ = c.Store.DB().QueryRow(`SELECT COUNT(*) FROM agent_pairings`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d jeton(s) d'appairage créé(s) sans canal humain", n)
	}
	// Page : seulement avec un navigateur enregistré.
	anon := newBrowser(t, false)
	if res, _ := anon.post(srv.URL+"/agents/pair", url.Values{}, nil); res.StatusCode != 403 {
		t.Fatalf("POST /agents/pair anonyme : %d", res.StatusCode)
	}
	b := newBrowser(t, false)
	b.post(srv.URL+"/enroll", url.Values{"code": {newCode(t, c)}}, nil)
	res, body := b.post(srv.URL+"/agents/pair", url.Values{}, nil)
	i := strings.Index(body, "pair-")
	if res.StatusCode != 200 || i < 0 {
		t.Fatalf("page d'appairage : %d %s", res.StatusCode, body)
	}
	tok := body[i : i+5+32]
	if _, err := agentlink.NewClient(srv.URL, "").Pair(context.Background(), agentlink.PairRequest{Token: tok, Name: "PC"}); err != nil {
		t.Fatalf("appairage avec le jeton de la page : %v", err)
	}
}

func mustURL(s string) *url.URL { u, _ := url.Parse(s); return u }
