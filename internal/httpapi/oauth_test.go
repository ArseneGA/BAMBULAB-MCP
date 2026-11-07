package httpapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/oauth"
)

const (
	oauthStaticToken = "static-token-0123456789"
	claudeCallback   = "https://claude.ai/api/mcp/auth_callback"
	verifier         = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ0123456789-._~"
)

func challengeOf(v string) string {
	s := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(s[:])
}

type oauthEnv struct {
	t     *testing.T
	c     *core.Core
	srv   string
	human *browser // navigateur enregistré (ne suit pas les redirections)
	plain *http.Client
}

func newOAuthEnv(t *testing.T) *oauthEnv {
	c, srv := humanEnv(t, httpapi.Options{APIToken: oauthStaticToken})
	return &oauthEnv{t: t, c: c, srv: srv.URL, human: enrolled(t, c, srv.URL),
		plain: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (e *oauthEnv) do(method, path string, hdr map[string]string, body string) (*http.Response, string) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv+path, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := e.plain.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

func (e *oauthEnv) register(redirect string) string {
	e.t.Helper()
	res, body := e.do("POST", "/oauth/register", map[string]string{"Content-Type": "application/json"},
		`{"client_name":"Claude","redirect_uris":["`+redirect+`"],"token_endpoint_auth_method":"none","grant_types":["authorization_code","refresh_token"]}`)
	if res.StatusCode != 201 {
		e.t.Fatalf("register : %d %s", res.StatusCode, body)
	}
	var out struct {
		ClientID string `json:"client_id"`
	}
	_ = json.Unmarshal([]byte(body), &out)
	return out.ClientID
}

func (e *oauthEnv) authURL(cid, redirect string, extra url.Values) string {
	q := url.Values{"response_type": {"code"}, "client_id": {cid}, "redirect_uri": {redirect}, "state": {"st123"},
		"code_challenge": {challengeOf(verifier)}, "code_challenge_method": {"S256"}, "scope": {"mcp"}, "resource": {e.srv + "/mcp"}}
	for k, v := range extra {
		if len(v) == 1 && v[0] == "" {
			q.Del(k)
		} else {
			q[k] = v
		}
	}
	return "/oauth/authorize?" + q.Encode()
}

var consentRe = regexp.MustCompile(`/oauth/consent/([A-Za-z0-9_-]+)`)
var nonceRe = regexp.MustCompile(`name="nonce" value="([^"]+)"`)

// authorize mène la demande jusqu'au consentement de l'humain et renvoie l'URL de retour (Location).
func (e *oauthEnv) authorize(cid, redirect string, extra url.Values, decision string) string {
	e.t.Helper()
	// 1. Navigation venue de Claude (autre site) : pas de cookie SameSite=Strict, page intermédiaire.
	res, body := e.do("GET", e.authURL(cid, redirect, extra), map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate"}, "")
	if res.StatusCode == 303 {
		return res.Header.Get("Location")
	}
	m := consentRe.FindStringSubmatch(body)
	if res.StatusCode != 200 || m == nil {
		e.t.Fatalf("authorize : %d %s", res.StatusCode, body)
	}
	// 2. Sans cookie, la page de consentement renvoie vers /enroll.
	if r2, _ := e.do("GET", "/oauth/consent/"+m[1], nil, ""); r2.StatusCode != 303 || !strings.HasPrefix(r2.Header.Get("Location"), "/enroll") {
		e.t.Fatalf("consentement sans cookie : %d %s", r2.StatusCode, r2.Header.Get("Location"))
	}
	// 3. Navigateur enregistré.
	r3, b3 := e.human.get(e.srv+"/oauth/consent/"+m[1], map[string]string{"Sec-Fetch-Site": "same-origin"})
	n := nonceRe.FindStringSubmatch(b3)
	if r3.StatusCode != 200 || n == nil || !strings.Contains(b3, "jamais") {
		e.t.Fatalf("consentement : %d %s", r3.StatusCode, b3)
	}
	r4, b4 := e.human.post(e.srv+"/oauth/consent/"+m[1], url.Values{"nonce": {n[1]}, "decision": {decision}}, nil)
	if r4.StatusCode != 303 {
		e.t.Fatalf("décision : %d %s", r4.StatusCode, b4)
	}
	if csp := r4.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "form-action 'self' ") {
		e.t.Errorf("CSP de retour : %q", csp)
	}
	return r4.Header.Get("Location")
}

func (e *oauthEnv) token(f url.Values) (int, map[string]any) {
	e.t.Helper()
	res, body := e.do("POST", "/oauth/token", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, f.Encode())
	var m map[string]any
	_ = json.Unmarshal([]byte(body), &m)
	if res.Header.Get("Cache-Control") != "no-store" {
		e.t.Errorf("Cache-Control : %q", res.Header.Get("Cache-Control"))
	}
	return res.StatusCode, m
}

func codeOf(t *testing.T, loc string) (code, state, iss string) {
	t.Helper()
	u, err := url.Parse(loc)
	if err != nil || u.Query().Get("code") == "" {
		t.Fatalf("pas de code dans %q", loc)
	}
	return u.Query().Get("code"), u.Query().Get("state"), u.Query().Get("iss")
}

func (e *oauthEnv) mcpStatus(token string) int {
	e.t.Helper()
	h := map[string]string{"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
	if token != "" {
		h["Authorization"] = "Bearer " + token
	}
	res, _ := e.do("POST", "/mcp", h, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
	return res.StatusCode
}

func (e *oauthEnv) fullGrant() (cid string, tok map[string]any) {
	cid = e.register(claudeCallback)
	loc := e.authorize(cid, claudeCallback, nil, "allow")
	code, state, iss := codeOf(e.t, loc)
	if state != "st123" || iss != e.srv || !strings.HasPrefix(loc, claudeCallback) {
		e.t.Fatalf("retour : %s", loc)
	}
	st, tok := e.token(url.Values{"grant_type": {"authorization_code"}, "client_id": {cid}, "code": {code}, "redirect_uri": {claudeCallback},
		"code_verifier": {verifier}, "resource": {e.srv + "/mcp"}})
	if st != 200 {
		e.t.Fatalf("token : %d %v", st, tok)
	}
	return cid, tok
}

func TestOAuthDiscoveryAndChallenge(t *testing.T) {
	e := newOAuthEnv(t)
	res, _ := e.do("POST", "/mcp", map[string]string{"Content-Type": "application/json"}, `{}`)
	wa := res.Header.Get("WWW-Authenticate")
	if res.StatusCode != 401 || !strings.Contains(wa, `resource_metadata="`+e.srv+`/.well-known/oauth-protected-resource"`) || !strings.Contains(wa, `scope="mcp"`) {
		t.Fatalf("401 /mcp : %d %q", res.StatusCode, wa)
	}
	// Les autres routes protégées gardent le défi d'origine.
	if res, _ := e.do("GET", "/api/models", nil, ""); res.StatusCode != 401 || strings.Contains(res.Header.Get("WWW-Authenticate"), "resource_metadata") {
		t.Errorf("/api/models : %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		res, body := e.do("GET", p, nil, "")
		var m map[string]any
		_ = json.Unmarshal([]byte(body), &m)
		as, _ := m["authorization_servers"].([]any)
		if res.StatusCode != 200 || m["resource"] != e.srv+"/mcp" || len(as) != 1 || as[0] != e.srv {
			t.Errorf("%s : %d %s", p, res.StatusCode, body)
		}
	}
	res, body := e.do("GET", "/.well-known/oauth-authorization-server", nil, "")
	var m map[string]any
	_ = json.Unmarshal([]byte(body), &m)
	cm, _ := m["code_challenge_methods_supported"].([]any)
	if res.StatusCode != 200 || m["issuer"] != e.srv || m["registration_endpoint"] != e.srv+"/oauth/register" || m["token_endpoint"] != e.srv+"/oauth/token" ||
		len(cm) != 1 || cm[0] != "S256" || m["authorization_response_iss_parameter_supported"] != true {
		t.Fatalf("métadonnées AS : %d %s", res.StatusCode, body)
	}
	// Une métadonnée inconnue n'est pas le repli HTML de la SPA.
	if res, body := e.do("GET", "/.well-known/openid-configuration", nil, ""); res.StatusCode != 404 || !strings.HasPrefix(body, "{") {
		t.Errorf("openid-configuration : %d %.40s", res.StatusCode, body)
	}
}

func TestOAuthFullFlowRefreshAndRevoke(t *testing.T) {
	e := newOAuthEnv(t)
	if e.mcpStatus("") != 401 || e.mcpStatus("bat_inconnu") != 401 {
		t.Fatal("/mcp doit refuser sans jeton valide")
	}
	if e.mcpStatus(oauthStaticToken) != 200 {
		t.Fatal("le Bearer statique doit rester valide")
	}
	cid, tok := e.fullGrant()
	access, refresh := tok["access_token"].(string), tok["refresh_token"].(string)
	if tok["token_type"] != "Bearer" || tok["expires_in"].(float64) != 3600 || tok["scope"] != "mcp" || access == "" || refresh == "" {
		t.Fatalf("réponse : %v", tok)
	}
	if e.mcpStatus(access) != 200 {
		t.Fatal("jeton OAuth refusé sur /mcp")
	}
	// Portée minimale : le jeton OAuth ne vaut rien hors /mcp (API, pages de confirmation).
	for _, p := range []string{"/api/models", "/api/capabilities"} {
		if res, _ := e.do("GET", p, map[string]string{"Authorization": "Bearer " + access}, ""); res.StatusCode != 401 {
			t.Errorf("%s avec jeton OAuth : %d", p, res.StatusCode)
		}
	}
	// Jamais stocké en clair.
	rows, err := e.c.Store.DB().Query(`SELECT token_hash FROM oauth_access_tokens UNION ALL SELECT refresh_hash FROM oauth_grants`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var h string
		_ = rows.Scan(&h)
		if h == access || h == refresh || strings.Contains(h, "bat_") || strings.Contains(h, "brt_") {
			t.Errorf("jeton en clair en base : %s", h)
		}
	}
	rows.Close()

	// Rafraîchissement : rotation, l'ancien jeton d'accès cesse de valoir.
	st, t2 := e.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {cid}, "refresh_token": {refresh}})
	if st != 200 || t2["refresh_token"] == refresh || t2["access_token"] == access {
		t.Fatalf("refresh : %d %v", st, t2)
	}
	if e.mcpStatus(access) != 401 || e.mcpStatus(t2["access_token"].(string)) != 200 {
		t.Fatal("après rafraîchissement : ancien jeton valide ou nouveau refusé")
	}
	// Mauvais client / portée excessive.
	if st, m := e.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {"bc_autre"}, "refresh_token": {t2["refresh_token"].(string)}}); st != 400 || m["error"] != "invalid_grant" {
		t.Errorf("refresh autre client : %d %v", st, m)
	}
	if st, m := e.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {cid}, "refresh_token": {t2["refresh_token"].(string)}, "scope": {"mcp admin"}}); st != 400 || m["error"] != "invalid_scope" {
		t.Errorf("refresh portée : %d %v", st, m)
	}
	// Rejeu de l'ancien jeton de rafraîchissement : l'autorisation entière est révoquée.
	if st, m := e.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {cid}, "refresh_token": {refresh}}); st != 400 || m["error"] != "invalid_grant" {
		t.Errorf("rejeu refresh : %d %v", st, m)
	}
	if e.mcpStatus(t2["access_token"].(string)) != 401 {
		t.Error("le rejeu d'un refresh doit révoquer l'autorisation")
	}
	if st, _ := e.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {cid}, "refresh_token": {t2["refresh_token"].(string)}}); st != 400 {
		t.Error("refresh d'une autorisation révoquée accepté")
	}
}

func TestOAuthRevokeByCLI(t *testing.T) {
	e := newOAuthEnv(t)
	_, tok := e.fullGrant()
	access := tok["access_token"].(string)
	svc := oauth.New(e.c.Store, oauth.Options{}) // comme `bambud oauth` : même base, autre instance
	gs, err := svc.List(context.Background())
	if err != nil || len(gs) != 1 || gs[0].Revoked || gs[0].ClientName != "Claude" {
		t.Fatalf("liste : %v %+v", err, gs)
	}
	if e.mcpStatus(access) != 200 {
		t.Fatal("jeton valide attendu avant révocation")
	}
	if err := svc.Revoke(context.Background(), gs[0].ID[:4]); err != nil {
		t.Fatal(err)
	}
	if e.mcpStatus(access) != 401 {
		t.Fatal("jeton révoqué encore accepté")
	}
	if e.mcpStatus(oauthStaticToken) != 200 {
		t.Fatal("la révocation OAuth ne doit pas toucher le Bearer statique")
	}
	if st, m := e.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {gs[0].ClientID}, "refresh_token": {tok["refresh_token"].(string)}}); st != 400 || m["error"] != "invalid_grant" {
		t.Errorf("refresh après révocation : %d %v", st, m)
	}
	if n, err := svc.RevokeAll(context.Background()); err != nil || n != 0 {
		t.Errorf("RevokeAll : %d %v", n, err)
	}
}

func TestOAuthPKCEAndCodeRules(t *testing.T) {
	e := newOAuthEnv(t)
	cid := e.register(claudeCallback)
	exchange := func(code, ver string) (int, map[string]any) {
		return e.token(url.Values{"grant_type": {"authorization_code"}, "client_id": {cid}, "code": {code}, "redirect_uri": {claudeCallback}, "code_verifier": {ver}})
	}
	// Mauvais verifier : refusé, et le code est brûlé.
	code, _, _ := codeOf(t, e.authorize(cid, claudeCallback, nil, "allow"))
	if st, m := exchange(code, strings.Repeat("x", 50)); st != 400 || m["error"] != "invalid_grant" {
		t.Fatalf("mauvais verifier : %d %v", st, m)
	}
	if st, _ := exchange(code, verifier); st != 400 {
		t.Fatal("un code dont l'échange a échoué ne doit plus servir")
	}
	// Verifier trop court (RFC 7636 : 43 minimum).
	code, _, _ = codeOf(t, e.authorize(cid, claudeCallback, nil, "allow"))
	if st, _ := exchange(code, "court"); st != 400 {
		t.Fatal("verifier trop court accepté")
	}
	// Rejeu d'un code déjà échangé : refusé, et le jeton obtenu est révoqué.
	code, _, _ = codeOf(t, e.authorize(cid, claudeCallback, nil, "allow"))
	st, tok := exchange(code, verifier)
	if st != 200 {
		t.Fatalf("échange : %d %v", st, tok)
	}
	if e.mcpStatus(tok["access_token"].(string)) != 200 {
		t.Fatal("jeton valide attendu")
	}
	if st, _ := exchange(code, verifier); st != 400 {
		t.Fatal("rejeu du code accepté")
	}
	if e.mcpStatus(tok["access_token"].(string)) != 401 {
		t.Fatal("le rejeu d'un code doit révoquer le jeton émis")
	}
	// redirect_uri différente à l'échange.
	code, _, _ = codeOf(t, e.authorize(cid, claudeCallback, nil, "allow"))
	if st, m := e.token(url.Values{"grant_type": {"authorization_code"}, "client_id": {cid}, "code": {code}, "redirect_uri": {"https://claude.com/api/mcp/auth_callback"}, "code_verifier": {verifier}}); st != 400 || m["error"] != "invalid_grant" {
		t.Errorf("redirect_uri différente : %d %v", st, m)
	}
	// Code d'un autre client.
	other := e.register(claudeCallback)
	code, _, _ = codeOf(t, e.authorize(cid, claudeCallback, nil, "allow"))
	if st, _ := e.token(url.Values{"grant_type": {"authorization_code"}, "client_id": {other}, "code": {code}, "redirect_uri": {claudeCallback}, "code_verifier": {verifier}}); st != 400 {
		t.Error("code d'un autre client accepté")
	}
	// resource différente à l'échange.
	code, _, _ = codeOf(t, e.authorize(cid, claudeCallback, nil, "allow"))
	if st, m := e.token(url.Values{"grant_type": {"authorization_code"}, "client_id": {cid}, "code": {code}, "redirect_uri": {claudeCallback}, "code_verifier": {verifier}, "resource": {"https://autre.example/mcp"}}); st != 400 || m["error"] != "invalid_target" {
		t.Errorf("resource différente : %d %v", st, m)
	}
	// Type de contenu JSON refusé sur /token ; grant inconnu.
	if res, _ := e.do("POST", "/oauth/token", map[string]string{"Content-Type": "application/json"}, `{}`); res.StatusCode != 400 {
		t.Errorf("token en JSON : %d", res.StatusCode)
	}
	if st, m := e.token(url.Values{"grant_type": {"password"}}); st != 400 || m["error"] != "unsupported_grant_type" {
		t.Errorf("grant password : %d %v", st, m)
	}
}

func TestOAuthAuthorizeValidation(t *testing.T) {
	e := newOAuthEnv(t)
	cid := e.register(claudeCallback)
	hdr := map[string]string{"Sec-Fetch-Site": "cross-site"}
	// Redirection non enregistrée / client inconnu : jamais de redirection (page d'erreur).
	for name, u := range map[string]string{
		"redirect autre": e.authURL(cid, "https://evil.example/cb", nil),
		"client inconnu": e.authURL("bc_inconnu", claudeCallback, nil),
		"sans redirect":  e.authURL(cid, "", nil),
	} {
		if res, _ := e.do("GET", u, hdr, ""); res.StatusCode != 400 || res.Header.Get("Location") != "" {
			t.Errorf("%s : %d Location=%q", name, res.StatusCode, res.Header.Get("Location"))
		}
	}
	// Erreurs renvoyées au client (redirect_uri fiable) avec state et iss.
	for name, extra := range map[string]url.Values{
		"sans PKCE":       {"code_challenge": {""}, "code_challenge_method": {""}},
		"PKCE plain":      {"code_challenge_method": {"plain"}},
		"mauvais type":    {"response_type": {"token"}},
		"autre resource":  {"resource": {"https://autre.example/mcp"}},
		"portée inconnue": {"scope": {"mcp admin"}},
	} {
		res, _ := e.do("GET", e.authURL(cid, claudeCallback, extra), hdr, "")
		loc, _ := url.Parse(res.Header.Get("Location"))
		if res.StatusCode != 303 || loc == nil || loc.Query().Get("error") == "" || loc.Query().Get("state") != "st123" || loc.Query().Get("iss") != e.srv || loc.Query().Get("code") != "" {
			t.Errorf("%s : %d %q", name, res.StatusCode, res.Header.Get("Location"))
		}
	}
	// Refus de l'humain.
	loc := e.authorize(cid, claudeCallback, nil, "deny")
	u, _ := url.Parse(loc)
	if u.Query().Get("error") != "access_denied" || u.Query().Get("code") != "" {
		t.Errorf("refus : %s", loc)
	}
	// Demande déjà traitée : le consentement ne se rejoue pas.
	res, body := e.do("GET", e.authURL(cid, claudeCallback, nil), hdr, "")
	m := consentRe.FindStringSubmatch(body)
	if res.StatusCode != 200 || m == nil {
		t.Fatal("page intermédiaire attendue")
	}
	_, b3 := e.human.get(e.srv+"/oauth/consent/"+m[1], map[string]string{"Sec-Fetch-Site": "same-origin"})
	n := nonceRe.FindStringSubmatch(b3)[1]
	// POST sans en-têtes Sec-Fetch (cross-site / non navigateur) ou avec mauvais nonce : refusé.
	if r, _ := e.human.post(e.srv+"/oauth/consent/"+m[1], url.Values{"nonce": {n}, "decision": {"allow"}}, map[string]string{"Sec-Fetch-Site": "cross-site"}); r.StatusCode != 403 {
		t.Errorf("POST cross-site : %d", r.StatusCode)
	}
	if r, _ := e.human.post(e.srv+"/oauth/consent/"+m[1], url.Values{"nonce": {"faux"}, "decision": {"allow"}}, nil); r.StatusCode != 400 {
		t.Errorf("mauvais nonce : %d", r.StatusCode)
	}
	// Sans cookie : le POST est refusé même avec le bon nonce.
	if r, _ := e.do("POST", "/oauth/consent/"+m[1], map[string]string{"Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "navigate", "Content-Type": "application/x-www-form-urlencoded"}, url.Values{"nonce": {n}, "decision": {"allow"}}.Encode()); r.StatusCode != 403 {
		t.Errorf("POST sans cookie : %d", r.StatusCode)
	}
	if r, _ := e.human.post(e.srv+"/oauth/consent/"+m[1], url.Values{"nonce": {n}, "decision": {"allow"}}, nil); r.StatusCode != 303 {
		t.Errorf("POST valide : %d", r.StatusCode)
	}
	if r, _ := e.human.post(e.srv+"/oauth/consent/"+m[1], url.Values{"nonce": {n}, "decision": {"allow"}}, nil); r.StatusCode != 400 {
		t.Errorf("rejeu du consentement : %d", r.StatusCode)
	}
}

func TestOAuthRegistrationRules(t *testing.T) {
	e := newOAuthEnv(t)
	post := func(body string) int {
		res, _ := e.do("POST", "/oauth/register", map[string]string{"Content-Type": "application/json"}, body)
		return res.StatusCode
	}
	for name, body := range map[string]string{
		"redirect étrangère":  `{"redirect_uris":["https://evil.example/cb"]}`,
		"http non loopback":   `{"redirect_uris":["http://claude.ai/api/mcp/auth_callback"]}`,
		"sans redirect":       `{"client_name":"x"}`,
		"client confidentiel": `{"redirect_uris":["` + claudeCallback + `"],"token_endpoint_auth_method":"client_secret_basic"}`,
		"grant implicite":     `{"redirect_uris":["` + claudeCallback + `"],"grant_types":["implicit"]}`,
		"fragment":            `{"redirect_uris":["` + claudeCallback + `#x"]}`,
		"JSON invalide":       `{`,
	} {
		if st := post(body); st != 400 {
			t.Errorf("%s : %d, attendu 400", name, st)
		}
	}
	for _, ok := range []string{claudeCallback, "https://claude.com/api/mcp/auth_callback", "http://localhost:3118/callback", "http://127.0.0.1:5555/callback", "http://[::1]:5555/callback"} {
		if st := post(`{"client_name":"c","redirect_uris":["` + ok + `"]}`); st != 201 {
			t.Errorf("%s : %d, attendu 201", ok, st)
		}
	}
	// Boucle locale (Claude Code) : le port est libre à l'autorisation (RFC 8252 §7.3).
	cid := e.register("http://localhost:3118/callback")
	loc := e.authorize(cid, "http://localhost:40001/callback", nil, "allow")
	if !strings.HasPrefix(loc, "http://localhost:40001/callback?") {
		t.Errorf("boucle locale : %s", loc)
	}
	// Mais pas un autre chemin ni un autre hôte.
	for _, bad := range []string{"http://localhost:40001/autre", "http://127.0.0.1:3118/callback"} {
		if res, _ := e.do("GET", e.authURL(cid, bad, nil), nil, ""); res.StatusCode != 400 {
			t.Errorf("%s : %d", bad, res.StatusCode)
		}
	}
	// Nom de client assaini (pas de caractères de contrôle).
	res, body := e.do("POST", "/oauth/register", map[string]string{"Content-Type": "application/json"}, `{"client_name":"A\u0000B\nC","redirect_uris":["`+claudeCallback+`"]}`)
	if res.StatusCode != 201 || strings.Contains(body, `\u0000`) {
		t.Errorf("nom : %d %s", res.StatusCode, body)
	}
}

// Un jeton émis pour une ressource (hôte) ne vaut pas pour une autre (RFC 8707).
func TestOAuthTokenBoundToResource(t *testing.T) {
	e := newOAuthEnv(t)
	_, tok := e.fullGrant()
	req, _ := http.NewRequest("POST", e.srv+"/mcp", strings.NewReader(`{}`))
	req.Host = "localhost" + e.srv[strings.LastIndex(e.srv, ":"):]
	req.Header.Set("Authorization", "Bearer "+tok["access_token"].(string))
	res, err := e.plain.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Errorf("jeton émis pour 127.0.0.1 accepté pour localhost : %d", res.StatusCode)
	}
}
