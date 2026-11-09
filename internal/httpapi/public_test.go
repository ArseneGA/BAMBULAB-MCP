package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

// pubEnv : l'écouteur principal ET l'écouteur public, sur le même service (comme bambud --public-listen).
type pubEnv struct {
	t     *testing.T
	c     *core.Core
	main  *httptest.Server
	pub   *httptest.Server
	human *browser // navigateur enregistré SUR L'ÉCOUTEUR PRINCIPAL (cookie partagé : même hôte, port ignoré)
}

func newPubEnv(t *testing.T, opts httpapi.Options) *pubEnv {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c, err := core.Open(ctx, core.Config{DataDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		PollInterval: 10 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	if opts.APIToken == "" {
		opts.APIToken = oauthStaticToken
	}
	h := httpapi.NewHandlers(c, opts)
	main, pub := httptest.NewServer(h.Main), httptest.NewServer(h.Public)
	c.Catalog.SetBaseURL(main.URL)
	t.Cleanup(func() { main.Close(); pub.Close(); cancel(); <-done; c.Close() })
	return &pubEnv{t: t, c: c, main: main, pub: pub, human: enrolled(t, c, main.URL)}
}

var plainClient = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func (e *pubEnv) do(srv, method, path string, hdr map[string]string, body string) (*http.Response, string) {
	e.t.Helper()
	req, _ := http.NewRequest(method, srv+path, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := plainClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

// Chaque chemin hors liste blanche répond 404 sur l'écouteur public (jamais 401/403/303), et reste inchangé sur le principal.
func TestPublicSurfaceWhitelistIsExact(t *testing.T) {
	e := newPubEnv(t, httpapi.Options{})
	forbidden := []struct{ method, path string }{
		{"GET", "/confirm/abc"}, {"POST", "/confirm/abc"}, {"GET", "/stop/abc"}, {"GET", "/action/abc"}, {"GET", "/setup/launch-code"},
		{"GET", "/setup/passkeys"}, {"GET", "/setup/printer/p1"}, {"GET", "/enroll"}, {"POST", "/enroll"}, {"GET", "/agents/pair"},
		{"GET", "/api/capabilities"}, {"GET", "/api/jobs/x"}, {"GET", "/api/session"}, {"GET", "/api/system/busy"}, {"POST", "/api/jobs/x/cancel"},
		{"POST", "/agent/v1/pair"}, {"GET", "/agent/v1/tasks/next"}, {"GET", "/agent/v2/x"}, {"POST", "/agent/v2/x"},
		{"GET", "/"}, {"GET", "/assets/x.js"}, {"GET", "/bibliotheque"}, {"GET", "/favicon.ico"}, {"GET", "/support"}, {"GET", "/mcp/"}, {"GET", "/mcp/x"},
		{"GET", "/oauth/consent/"}, {"GET", "/oauth/consent/a/b"}, {"GET", "/oauth/consent/a%2Fb"}, {"GET", "/oauth/revoke"}, {"POST", "/oauth/authorize"},
		{"GET", "/oauth/token"}, {"GET", "/oauth/register"}, {"POST", "/health"}, {"GET", "/.well-known/openid-configuration"}, {"GET", "/.well-known/"},
		{"GET", "/../confirm/x"}, {"GET", "/mcp/../confirm/x"}, {"GET", "/ABOUT"}, {"OPTIONS", "/mcp"}, {"PUT", "/mcp"},
	}
	for _, c := range forbidden {
		res, body := e.do(e.pub.URL, c.method, c.path, nil, "")
		if res.StatusCode != 404 {
			t.Errorf("%s %s sur l'écouteur public : %d (attendu 404) %.60s", c.method, c.path, res.StatusCode, body)
		}
		if res.Header.Get("Location") != "" || res.Header.Get("Set-Cookie") != "" {
			t.Errorf("%s %s : en-têtes révélateurs %v", c.method, c.path, res.Header)
		}
	}
	// Avec un Bearer valide, toujours 404 (le jeton n'ouvre rien hors liste).
	for _, p := range []string{"/api/capabilities", "/api/system/busy", "/confirm/abc"} {
		if res, _ := e.do(e.pub.URL, "GET", p, map[string]string{"Authorization": "Bearer " + oauthStaticToken}, ""); res.StatusCode != 404 {
			t.Errorf("GET %s avec Bearer sur le public : %d", p, res.StatusCode)
		}
	}

	// Écouteur principal inchangé.
	if res, body := e.do(e.main.URL, "GET", "/health", nil, ""); res.StatusCode != 200 || !strings.Contains(body, `"version"`) || !strings.Contains(body, "mcp_app_views") {
		t.Errorf("/health principal : %d %s", res.StatusCode, body)
	}
	if res, _ := e.do(e.main.URL, "GET", "/api/capabilities", nil, ""); res.StatusCode != 401 {
		t.Errorf("/api/capabilities principal : %d", res.StatusCode)
	}
	if res, _ := e.do(e.main.URL, "GET", "/enroll", nil, ""); res.StatusCode != 200 {
		t.Errorf("/enroll principal : %d", res.StatusCode)
	}
	if res, _ := e.do(e.main.URL, "GET", "/confirm/abc", nil, ""); res.StatusCode != 303 {
		t.Errorf("/confirm principal sans cookie : %d (redirection vers /enroll attendue)", res.StatusCode)
	}
	if res, _ := e.do(e.main.URL, "GET", "/", nil, ""); res.StatusCode == 404 || res.StatusCode == 401 {
		t.Errorf("SPA principale : %d", res.StatusCode)
	}
}

func TestPublicHealthIsReducedAndPagesAreSelfContained(t *testing.T) {
	e := newPubEnv(t, httpapi.Options{})
	res, body := e.do(e.pub.URL, "GET", "/health", nil, "")
	if res.StatusCode != 200 || strings.TrimSpace(body) != `{"status":"ok"}` {
		t.Fatalf("/health public : %d %q", res.StatusCode, body)
	}
	for _, p := range []string{"/about", "/privacy"} {
		res, body := e.do(e.pub.URL, "GET", p, nil, "")
		if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/html; charset=utf-8" || !strings.Contains(body, "Atelier Bambu Lab") {
			t.Fatalf("%s : %d %s", p, res.StatusCode, res.Header.Get("Content-Type"))
		}
		low := strings.ToLower(body)
		if strings.Contains(low, "http://") || strings.Contains(low, "https://") || strings.Contains(low, "<script") {
			t.Errorf("%s référence un hôte externe ou un script", p)
		}
	}
	if _, body := e.do(e.pub.URL, "GET", "/privacy", nil, ""); !strings.Contains(body, "Contact non configuré") || strings.Contains(body, "mailto:") {
		t.Error("/privacy sans contact configuré doit le dire")
	}
	e2 := newPubEnv(t, httpapi.Options{ContactEmail: "moi@example.org"})
	if _, body := e2.do(e2.pub.URL, "GET", "/privacy", nil, ""); !strings.Contains(body, "mailto:moi@example.org") {
		t.Error("contact configuré absent")
	}
	res, body = e.do(e.pub.URL, "GET", "/icon.svg", nil, "")
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/svg+xml" || res.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(body, "<svg") {
		t.Errorf("/icon.svg : %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if _, body := e.do(e.pub.URL, "GET", "/robots.txt", nil, ""); !strings.Contains(body, "Disallow: /") {
		t.Error("robots.txt")
	}
}

func TestPublicHostGuardStillApplies(t *testing.T) {
	e := newPubEnv(t, httpapi.Options{PublicListenURL: "https://atelier.example.ts.net"})
	req, _ := http.NewRequest("GET", e.pub.URL+"/about", nil)
	req.Host = "evil.example.com"
	res, err := plainClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Errorf("Host étranger : %d", res.StatusCode)
	}
	req, _ = http.NewRequest("GET", e.pub.URL+"/about", nil)
	req.Host = "atelier.example.ts.net"
	res, err = plainClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Errorf("Host public : %d", res.StatusCode)
	}
}

func TestPublicDiscoveryUsesPublicListenURL(t *testing.T) {
	e := newPubEnv(t, httpapi.Options{PublicListenURL: "https://atelier.example.ts.net", PublicURL: "https://atelier.example.ts.net:8443"})
	get := func(srv, path string) map[string]any {
		req, _ := http.NewRequest("GET", srv+path, nil)
		req.Host = "atelier.example.ts.net"
		res, err := plainClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return m
	}
	if m := get(e.pub.URL, "/.well-known/oauth-protected-resource"); m["resource"] != "https://atelier.example.ts.net/mcp" {
		t.Errorf("ressource publique : %v", m)
	}
	if m := get(e.pub.URL, "/.well-known/oauth-authorization-server"); m["issuer"] != "https://atelier.example.ts.net" || m["token_endpoint"] != "https://atelier.example.ts.net/oauth/token" {
		t.Errorf("émetteur public : %v", m)
	}
	// Les liens pour l'humain restent sur l'adresse privée : base du catalogue (confirm_url, stop_url, action_url).
	if got := e.c.Catalog.BaseURL(); got != e.main.URL {
		t.Errorf("base des liens humains : %q", got)
	}
}

func TestPublicRateLimits(t *testing.T) {
	e := newPubEnv(t, httpapi.Options{PublicRate: httpapi.PublicRateLimits{RegisterPerHour: 3, TokenPerMinute: 5, MCPPerMinute: 4}})
	form := "grant_type=authorization_code&code=x"
	post := func(ip string) *http.Response {
		hdr := map[string]string{"Content-Type": "application/x-www-form-urlencoded", "X-Forwarded-For": ip}
		res, _ := e.do(e.pub.URL, "POST", "/oauth/token", hdr, form)
		return res
	}
	for i := 1; i <= 5; i++ {
		if res := post("203.0.113.7"); res.StatusCode == 429 {
			t.Fatalf("appel %d déjà limité", i)
		}
	}
	res := post("203.0.113.7")
	if res.StatusCode != 429 || res.Header.Get("Retry-After") == "" {
		t.Fatalf("au-delà de la limite : %d Retry-After=%q", res.StatusCode, res.Header.Get("Retry-After"))
	}
	if post("198.51.100.9").StatusCode == 429 {
		t.Error("une autre adresse doit passer")
	}

	// /mcp sans jeton : limité ; avec jeton valide : jamais compté.
	mcpAnon := func() int {
		res, _ := e.do(e.pub.URL, "POST", "/mcp", map[string]string{"Content-Type": "application/json", "X-Forwarded-For": "203.0.113.50"}, `{}`)
		return res.StatusCode
	}
	for i := 0; i < 4; i++ {
		if mcpAnon() != 401 {
			t.Fatal("/mcp anonyme doit répondre 401")
		}
	}
	if mcpAnon() != 429 {
		t.Error("/mcp anonyme au-delà de la limite")
	}
	for i := 0; i < 10; i++ {
		hdr := map[string]string{"Content-Type": "application/json", "Accept": "application/json, text/event-stream", "Authorization": "Bearer " + oauthStaticToken, "X-Forwarded-For": "203.0.113.50"}
		res, _ := e.do(e.pub.URL, "POST", "/mcp", hdr, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
		if res.StatusCode == 429 {
			t.Fatal("un client authentifié ne doit pas être limité")
		}
	}

	// Enregistrements : plafond horaire.
	reg := func() int {
		res, _ := e.do(e.pub.URL, "POST", "/oauth/register", map[string]string{"Content-Type": "application/json", "X-Forwarded-For": "203.0.113.99"},
			`{"client_name":"c","redirect_uris":["`+claudeCallback+`"],"token_endpoint_auth_method":"none"}`)
		return res.StatusCode
	}
	for i := 0; i < 3; i++ {
		if reg() != 201 {
			t.Fatal("enregistrement attendu")
		}
	}
	if reg() != 429 {
		t.Error("4e enregistrement de l'heure doit être limité")
	}
}

func TestParsePublicRateLimits(t *testing.T) {
	l, err := httpapi.ParsePublicRateLimits("")
	if err != nil || l != httpapi.DefaultPublicRateLimits() {
		t.Fatalf("défauts : %v %v", l, err)
	}
	l, err = httpapi.ParsePublicRateLimits("register=5, mcp=7")
	if err != nil || l.RegisterPerHour != 5 || l.MCPPerMinute != 7 || l.TokenPerMinute != 60 {
		t.Fatalf("%v %v", l, err)
	}
	for _, bad := range []string{"register", "register=0", "token=-1", "mcp=999999", "x=1", "token=abc"} {
		if _, err := httpapi.ParsePublicRateLimits(bad); err == nil {
			t.Errorf("%q doit être refusé", bad)
		}
	}
}

func TestPublicTokenRequired(t *testing.T) {
	if err := httpapi.ValidateServe("127.0.0.1:7878", "", httpapi.Exposure{PublicListen: "127.0.0.1:7879"}); err == nil {
		t.Error("--public-listen sans jeton doit être refusé")
	}
	if err := httpapi.ValidateServe("127.0.0.1:7878", "0123456789abcdef", httpapi.Exposure{PublicListen: "127.0.0.1:7879"}); err != nil {
		t.Errorf("avec jeton : %v", err)
	}
	if err := httpapi.ValidateServe("127.0.0.1:7878", "0123456789abcdef", httpapi.Exposure{PublicListen: "127.0.0.1:7878"}); err == nil {
		t.Error("même adresse que --addr")
	}
	if err := httpapi.ValidateServe("127.0.0.1:7878", "", httpapi.Exposure{}); err != nil {
		t.Errorf("défaut inchangé : %v", err)
	}
}

var pubConsentRe = regexp.MustCompile(`/oauth/consent/([A-Za-z0-9_-]+)`)

// Flux OAuth complet sur l'écouteur public : le navigateur est enregistré via l'écouteur PRINCIPAL (même hôte) ;
// le cookie est accepté par l'écouteur public. Puis /mcp initialize, tools/list, resources/read d'une vue.
func TestPublicFullOAuthFlowAndMCP(t *testing.T) {
	e := newPubEnv(t, httpapi.Options{})
	oe := &oauthEnv{t: t, c: e.c, srv: e.pub.URL, human: e.human, plain: plainClient}

	// Découverte
	if res, _ := oe.do("POST", "/mcp", map[string]string{"Content-Type": "application/json"}, `{}`); res.StatusCode != 401 ||
		!strings.Contains(res.Header.Get("WWW-Authenticate"), `resource_metadata="`+e.pub.URL+`/.well-known/oauth-protected-resource"`) {
		t.Fatalf("401 public : %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
	// DCR, autorisation, consentement (cookie posé par l'écouteur principal), jeton.
	cid := oe.register(claudeCallback)
	res, body := e.human.get(e.pub.URL+oe.authURL(cid, claudeCallback, nil), map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate"})
	cm := pubConsentRe.FindStringSubmatch(body)
	if res.StatusCode != 200 || cm == nil {
		t.Fatalf("authorize : %d %s", res.StatusCode, body)
	}
	r3, b3 := e.human.get(e.pub.URL+"/oauth/consent/"+cm[1], map[string]string{"Sec-Fetch-Site": "same-origin"})
	n := nonceRe.FindStringSubmatch(b3)
	if r3.StatusCode != 200 || n == nil {
		t.Fatalf("consentement sur l'écouteur public avec le cookie du principal : %d %s", r3.StatusCode, b3)
	}
	r4, b4 := e.human.post(e.pub.URL+"/oauth/consent/"+cm[1], url.Values{"nonce": {n[1]}, "decision": {"allow"}}, nil)
	if r4.StatusCode != 200 {
		t.Fatalf("décision : %d %s", r4.StatusCode, b4)
	}
	back := backLink(t, b4)
	code, state, iss := codeOf(t, back)
	if state != "st123" || iss != e.pub.URL {
		t.Fatalf("retour : %s", back)
	}
	st, tok := oe.token(url.Values{"grant_type": {"authorization_code"}, "client_id": {cid}, "code": {code}, "redirect_uri": {claudeCallback},
		"code_verifier": {verifier}, "resource": {e.pub.URL + "/mcp"}})
	if st != 200 {
		t.Fatalf("token : %d %v", st, tok)
	}
	access := tok["access_token"].(string)

	// Un navigateur NON enregistré reçoit une explication (403), pas une redirection vers /enroll (404 ici).
	anon := newBrowser(t, false)
	res, body = anon.get(e.pub.URL+oe.authURL(cid, claudeCallback, nil), map[string]string{"Sec-Fetch-Site": "cross-site"})
	m := pubConsentRe.FindStringSubmatch(body)
	if res.StatusCode != 200 || m == nil {
		t.Fatalf("authorize anonyme : %d", res.StatusCode)
	}
	res, body = anon.get(e.pub.URL+"/oauth/consent/"+m[1], map[string]string{"Sec-Fetch-Site": "same-origin"})
	if res.StatusCode != 403 || res.Header.Get("Location") != "" || !strings.Contains(body, "enregistr") {
		t.Errorf("consentement sans navigateur enregistré : %d %s", res.StatusCode, res.Header.Get("Location"))
	}

	// Client MCP du SDK, par l'écouteur public, avec le jeton OAuth.
	cs := mcpConnect(t, e.pub.URL, access)
	tools, err := cs.ListTools(t.Context(), nil)
	if err != nil || len(tools.Tools) < 20 {
		t.Fatalf("tools/list : %v %v", len(tools.Tools), err)
	}
	var uri string
	for _, tl := range tools.Tools {
		if tl.Name == "get_print_status" {
			if ui, ok := tl.Meta["ui"].(map[string]any); ok {
				uri, _ = ui["resourceUri"].(string)
			}
		}
	}
	if uri != mcpserver.LiveViewURI {
		t.Errorf("get_print_status : resourceUri %q", uri)
	}
	rr, err := cs.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: mcpserver.LiveViewURI})
	if err != nil || len(rr.Contents) == 0 || rr.Contents[0].Text == "" || rr.Contents[0].MIMEType != mcpserver.AppMIMEType {
		t.Fatalf("resources/read : %v %+v", err, rr)
	}

	// Le jeton OAuth ne vaut rien hors /mcp : ni sur l'API de l'écouteur principal, ni (404) sur le public.
	for _, srv := range []string{e.main.URL, e.pub.URL} {
		res, _ := e.do(srv, "GET", "/api/capabilities", map[string]string{"Authorization": "Bearer " + access}, "")
		want := 401
		if srv == e.pub.URL {
			want = 404
		}
		if res.StatusCode != want {
			t.Errorf("/api/capabilities avec jeton OAuth sur %s : %d (attendu %d)", srv, res.StatusCode, want)
		}
	}
	// Un jeton émis pour l'écouteur public ne vaut pas sur le principal (ressource différente).
	if res, _ := e.do(e.main.URL, "POST", "/mcp", map[string]string{"Content-Type": "application/json", "Accept": "application/json, text/event-stream", "Authorization": "Bearer " + access},
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`); res.StatusCode != 401 {
		t.Errorf("jeton public sur /mcp principal : %d", res.StatusCode)
	}
}

type tokRT struct{ tok string }

func (r tokRT) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+r.tok)
	return http.DefaultTransport.RoundTrip(req)
}

func mcpConnect(t *testing.T, base, token string) *mcp.ClientSession {
	t.Helper()
	cl := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := cl.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: base + "/mcp", HTTPClient: &http.Client{Transport: tokRT{token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// Aucun outil MCP appelé par l'écouteur public ne produit un lancement : la liste des outils n'a pas d'outil
// d'approbation par jeton ; le contrôle exhaustif reste TestNoMCPToolCanApproveAConfirmation (internal/printing),
// qui appelle les mêmes outils (le serveur MCP est le même objet pour les deux écouteurs).
func TestPublicToolsListHasNoApprovalTool(t *testing.T) {
	e := newPubEnv(t, httpapi.Options{})
	cs := mcpConnect(t, e.pub.URL, oauthStaticToken)
	tools, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		low := strings.ToLower(tl.Name)
		if low == "confirm_print" || strings.Contains(low, "from_app") || (strings.Contains(low, "approve") && !strings.HasSuffix(low, "_with_code")) {
			t.Errorf("outil d'approbation interdit : %s", tl.Name)
		}
	}
}
