package httpapi_test

import (
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
)

const webToken = "jeton-api-de-test-0123456789"

// call envoie une requête arbitraire (en-têtes libres, hôte libre) et rend statut + corps.
func call(t *testing.T, cl *http.Client, method, u, host string, hdr map[string]string, body string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, u, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if cl == nil {
		cl = http.DefaultClient
	}
	res, err := cl.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

func enrolled(t *testing.T, c *core.Core, srv string) *browser {
	t.Helper()
	b := newBrowser(t, false)
	if res, _ := b.post(srv+"/enroll", url.Values{"code": {newCode(t, c)}}, nil); res.StatusCode != 200 {
		t.Fatalf("enroll : %d", res.StatusCode)
	}
	return b
}

// La SPA et ses assets sont servis sans jeton ni cookie (aucune donnée) ; /api/* et /mcp restent fermés.
func TestSPAServedWithoutAuthWhenTokenSet(t *testing.T) {
	_, srv := humanEnv(t, httpapi.Options{APIToken: webToken})
	for _, p := range []string{"/", "/bibliotheque", "/modele/mdl_x", "/assets/absent.js", "/favicon.ico"} {
		if res, _ := call(t, nil, "GET", srv.URL+p, "", nil, ""); res.StatusCode == 401 {
			t.Errorf("GET %s : 401, la SPA doit être publique", p)
		}
		if res, _ := call(t, nil, "HEAD", srv.URL+p, "", nil, ""); res.StatusCode == 401 {
			t.Errorf("HEAD %s : 401", p)
		}
	}
	for _, p := range []string{"/api/models", "/api/capabilities", "/api/", "/mcp", "/api/../api/models", "/health/x"} {
		if res, _ := call(t, nil, "GET", srv.URL+p, "", nil, ""); res.StatusCode == 200 {
			t.Errorf("GET %s sans authentification : 200", p)
		}
	}
	// Une écriture n'est jamais « statique ».
	if res, _ := call(t, nil, "POST", srv.URL+"/", "", nil, ""); res.StatusCode == 200 {
		t.Errorf("POST / : %d", res.StatusCode)
	}
	// Le refus est exploitable par la SPA.
	res, body := call(t, nil, "GET", srv.URL+"/api/models", "", nil, "")
	if res.StatusCode != 401 || !strings.Contains(body, `"code":"unauthorized"`) || !strings.Contains(body, "/enroll") {
		t.Errorf("refus : %d %s", res.StatusCode, body)
	}
}

// Le navigateur enregistré (cookie) vaut authentification pour /api/* ; jamais pour /mcp ; le Bearer reste valable.
func TestBrowserSessionAuthenticatesAPI(t *testing.T) {
	c, srv := humanEnv(t, httpapi.Options{APIToken: webToken})
	b := enrolled(t, c, srv.URL)
	if res, _ := b.get(srv.URL+"/api/capabilities", nil); res.StatusCode != 200 {
		t.Errorf("GET API avec cookie : %d", res.StatusCode)
	}
	if res, _ := b.get(srv.URL+"/mcp", nil); res.StatusCode != 401 {
		t.Errorf("/mcp avec cookie : %d", res.StatusCode)
	}
	if res, _ := call(t, nil, "GET", srv.URL+"/api/capabilities", "", map[string]string{"Authorization": "Bearer " + webToken}, ""); res.StatusCode != 200 {
		t.Errorf("Bearer : %d", res.StatusCode)
	}
	if res, _ := call(t, nil, "GET", srv.URL+"/api/capabilities", "", map[string]string{"Cookie": "bambu_session=forge"}, ""); res.StatusCode != 401 {
		t.Errorf("cookie forgé : %d", res.StatusCode)
	}
}

// Écritures par cookie : en-tête X-Atelier-Request ET même origine. Le Bearer n'a pas besoin de CSRF.
func TestCookieWritesNeedCSRFHeaderAndSameOrigin(t *testing.T) {
	c, srv := humanEnv(t, httpapi.Options{APIToken: webToken})
	b := enrolled(t, c, srv.URL)
	const path = "/api/jobs/job_x/cancel" // 404 = la requête a atteint le handler
	cases := []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"cookie seul", nil, 403},
		{"en-tête sans Sec-Fetch ni Origin", map[string]string{"X-Atelier-Request": "1"}, 403},
		{"même origine sans en-tête", map[string]string{"Sec-Fetch-Site": "same-origin"}, 403},
		{"en-tête + same-origin", map[string]string{"X-Atelier-Request": "1", "Sec-Fetch-Site": "same-origin"}, 404},
		{"en-tête + same-site", map[string]string{"X-Atelier-Request": "1", "Sec-Fetch-Site": "same-site"}, 403},
		{"en-tête + cross-site", map[string]string{"X-Atelier-Request": "1", "Sec-Fetch-Site": "cross-site"}, 403},
		{"mauvaise valeur d'en-tête", map[string]string{"X-Atelier-Request": "0", "Sec-Fetch-Site": "same-origin"}, 403},
		{"http LAN : Origin = Host", map[string]string{"X-Atelier-Request": "1", "Origin": srv.URL}, 404},
		{"Origin étrangère", map[string]string{"X-Atelier-Request": "1", "Origin": "http://evil.example"}, 403},
	}
	for _, k := range cases {
		hdr := map[string]string{"Sec-Fetch-Site": "", "Sec-Fetch-Mode": ""}
		for h, v := range k.hdr {
			hdr[h] = v
		}
		req, _ := http.NewRequest("POST", srv.URL+path, strings.NewReader("{}"))
		for h, v := range hdr {
			if v != "" {
				req.Header.Set(h, v)
			}
		}
		res, err := b.cl.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != k.want {
			t.Errorf("%s : %d, attendu %d", k.name, res.StatusCode, k.want)
		}
	}
	// Bearer : aucun en-tête de navigateur nécessaire.
	if res, _ := call(t, nil, "POST", srv.URL+path, "", map[string]string{"Authorization": "Bearer " + webToken}, "{}"); res.StatusCode != 404 {
		t.Errorf("POST avec Bearer : %d", res.StatusCode)
	}
	// Les pages d'approbation restent inchangées : le cookie + en-tête de la SPA ne suffit pas (navigation exigée).
	if res, _ := b.post(srv.URL+"/stop/stp_x", url.Values{"nonce": {"x"}}, map[string]string{"Sec-Fetch-Mode": "cors", "X-Atelier-Request": "1"}); res.StatusCode != 403 {
		t.Errorf("approbation par appel de la SPA : %d", res.StatusCode)
	}
	// Et aucune route d'approbation n'existe sous /api (pas d'API JSON pour confirmer ou arrêter).
	for _, p := range []string{"/api/confirm", "/api/stop", "/api/print/approve"} {
		if res, _ := call(t, b.cl, "POST", srv.URL+p, "", map[string]string{"X-Atelier-Request": "1", "Sec-Fetch-Site": "same-origin"}, "{}"); res.StatusCode == 200 || res.StatusCode == 202 {
			t.Errorf("POST %s : %d", p, res.StatusCode)
		}
	}
}

func TestHeadHealthOpenWithToken(t *testing.T) {
	_, srv := humanEnv(t, httpapi.Options{APIToken: webToken})
	for _, m := range []string{"GET", "HEAD"} {
		if res, _ := call(t, nil, m, srv.URL+"/health", "", nil, ""); res.StatusCode != 200 {
			t.Errorf("%s /health : %d", m, res.StatusCode)
		}
	}
	if res, _ := call(t, nil, "POST", srv.URL+"/health", "", nil, ""); res.StatusCode == 200 {
		t.Error("POST /health ouvert")
	}
}

// L'URL d'aperçu signée reste lisible sans Bearer (c'est une capacité) ; le handler la vérifie.
func TestSignedPreviewNotBlockedByToken(t *testing.T) {
	_, srv := humanEnv(t, httpapi.Options{APIToken: webToken})
	if res, _ := call(t, nil, "GET", srv.URL+"/api/assets/abc.glb", "", nil, ""); res.StatusCode != 401 {
		t.Errorf("sans signature : %d, 401 attendu", res.StatusCode)
	}
	if res, _ := call(t, nil, "GET", srv.URL+"/api/assets/abc.glb?exp=1&sig=zz", "", nil, ""); res.StatusCode != 403 {
		t.Errorf("signature fausse : %d, 403 attendu (vérifié par le handler, pas par le jeton)", res.StatusCode)
	}
}

// /mcp derrière un proxy loopback : le Host public autorisé n'est plus rejeté par le SDK ; le garde reste maître.
func TestMCPBehindLoopbackProxyWithPublicHost(t *testing.T) {
	const pub = "bambu.example.ts.net"
	_, srv := humanEnv(t, httpapi.Options{APIToken: webToken, AllowedHosts: []string{pub}, PublicURL: "https://" + pub, PublicURLs: []string{"https://" + pub}})
	const init = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	hdr := map[string]string{"Authorization": "Bearer " + webToken, "Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
	res, body := call(t, nil, "POST", srv.URL+"/mcp", pub, hdr, init)
	if res.StatusCode != 200 || strings.Contains(body, "invalid Host") {
		t.Fatalf("/mcp avec Host public autorisé : %d %s", res.StatusCode, body)
	}
	// Un Host non autorisé reste refusé par notre garde.
	if res, _ := call(t, nil, "POST", srv.URL+"/mcp", "evil.example.com", hdr, init); res.StatusCode != 403 {
		t.Fatalf("Host étranger : %d", res.StatusCode)
	}
	// Sans jeton : 401, même avec le bon Host.
	if res, _ := call(t, nil, "POST", srv.URL+"/mcp", pub, map[string]string{"Content-Type": "application/json"}, init); res.StatusCode != 401 {
		t.Fatalf("/mcp sans jeton : %d", res.StatusCode)
	}
}

// Cookie Secure : X-Forwarded-Proto n'est cru que d'un proxy de confiance ; plusieurs URL publiques.
func TestSecureCookieForwardedProtoAndMultiplePublicURLs(t *testing.T) {
	secureFor := func(opts httpapi.Options, host string, hdr map[string]string) bool {
		c, srv := humanEnv(t, opts)
		b := newBrowser(t, false)
		h := map[string]string{}
		for k, v := range hdr {
			h[k] = v
		}
		req, _ := http.NewRequest("POST", srv.URL+"/enroll", strings.NewReader(url.Values{"code": {newCode(t, c)}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Mode", "navigate")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		if host != "" {
			req.Host = host
		}
		for k, v := range h {
			req.Header.Set(k, v)
		}
		res, err := b.cl.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("enroll : %d", res.StatusCode)
		}
		for _, ck := range res.Cookies() {
			if ck.Name == "bambu_session" {
				return ck.Secure
			}
		}
		t.Fatal("pas de cookie")
		return false
	}
	https := map[string]string{"X-Forwarded-Proto": "https"}
	// Proxy loopback (défaut) : X-Forwarded-Proto https => Secure ; http => non.
	if !secureFor(httpapi.Options{}, "", https) {
		t.Error("X-Forwarded-Proto: https d'un proxy loopback ignoré")
	}
	if secureFor(httpapi.Options{}, "", map[string]string{"X-Forwarded-Proto": "http"}) {
		t.Error("X-Forwarded-Proto: http => Secure")
	}
	// Connexion qui ne vient pas d'un proxy de confiance : l'en-tête est forgeable, donc ignoré.
	if secureFor(httpapi.Options{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}, "", https) {
		t.Error("X-Forwarded-Proto cru depuis une adresse non fiable")
	}
	if secureFor(httpapi.Options{TrustedProxies: []netip.Prefix{}}, "", https) {
		t.Error("« none » devrait ignorer X-Forwarded-Proto")
	}
	// Deux URL publiques : le schéma suit l'hôte de la requête (l'accès LAN en http est refusé : voir httpsrequired_test.go).
	two := httpapi.Options{PublicURL: "http://192.168.1.5:7878", PublicURLs: []string{"http://192.168.1.5:7878", "https://bambu.example.ts.net"}}
	if !secureFor(two, "bambu.example.ts.net", nil) {
		t.Error("accès https : cookie non Secure")
	}
}

// Limiteur d'enrôlement par adresse client, avec X-Forwarded-For d'un proxy de confiance.
func TestEnrollLockPerClientBehindTrustedProxy(t *testing.T) {
	c, srv := humanEnv(t, httpapi.Options{})
	attempt := func(xff, code string) int {
		b := newBrowser(t, false)
		res, _ := b.post(srv.URL+"/enroll", url.Values{"code": {code}}, map[string]string{"X-Forwarded-For": xff})
		return res.StatusCode
	}
	for i := 0; i < 6; i++ {
		if st := attempt("198.51.100.7", "ZZZZ-ZZZZ"); st != 403 {
			t.Fatalf("essai %d : %d", i, st)
		}
	}
	good := newCode(t, c)
	if st := attempt("198.51.100.7", good); st != 429 {
		t.Fatalf("l'attaquant devrait être verrouillé : %d", st)
	}
	if st := attempt("203.0.113.9", good); st != 200 {
		t.Fatalf("un autre client ne doit pas être bloqué : %d", st)
	}
	// Un X-Forwarded-For forgé à gauche ne change pas l'identité (entrée la plus à droite non fiable).
	if st := attempt("1.1.1.1, 198.51.100.7", newCode(t, c)); st != 429 {
		t.Fatalf("XFF forgé à gauche : %d", st)
	}
}

// Sans proxy de confiance, X-Forwarded-For est ignoré : tous les visiteurs partagent l'adresse TCP.
func TestEnrollLockIgnoresForwardedForFromUntrusted(t *testing.T) {
	c, srv := humanEnv(t, httpapi.Options{TrustedProxies: []netip.Prefix{}})
	for i := 0; i < 6; i++ {
		b := newBrowser(t, false)
		b.post(srv.URL+"/enroll", url.Values{"code": {"ZZZZ-ZZZZ"}}, map[string]string{"X-Forwarded-For": "10.0.0." + string(rune('1'+i))})
	}
	b := newBrowser(t, false)
	res, _ := b.post(srv.URL+"/enroll", url.Values{"code": {newCode(t, c)}}, map[string]string{"X-Forwarded-For": "203.0.113.200"})
	if res.StatusCode != 429 {
		t.Fatalf("XFF d'un client non fiable pris en compte : %d", res.StatusCode)
	}
}

func TestParseTrustedProxies(t *testing.T) {
	if p, err := httpapi.ParseTrustedProxies(""); err != nil || p != nil {
		t.Errorf("vide : %v %v", p, err)
	}
	if p, err := httpapi.ParseTrustedProxies("none"); err != nil || p == nil || len(p) != 0 {
		t.Errorf("none : %v %v", p, err)
	}
	p, err := httpapi.ParseTrustedProxies("127.0.0.1, 10.0.0.0/8 ,::1")
	if err != nil || len(p) != 3 {
		t.Errorf("liste : %v %v", p, err)
	}
	if _, err := httpapi.ParseTrustedProxies("pas-une-ip"); err == nil {
		t.Error("entrée invalide acceptée")
	}
}
