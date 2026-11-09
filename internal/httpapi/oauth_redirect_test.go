package httpapi_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
)

// Régression de production : Chrome applique le form-action du document qui soumet aux REDIRECTIONS de la
// soumission. La page GET /oauth/consent/{id} doit donc autoriser l'origine exacte de la redirect_uri de la demande.

func formActionOf(csp string) string {
	for _, d := range strings.Split(csp, ";") {
		d = strings.TrimSpace(d)
		if strings.HasPrefix(d, "form-action") {
			return d
		}
	}
	return ""
}

// cspAllowsFormTarget : le navigateur laisserait-il la soumission aboutir (redirections comprises) vers target ?
func cspAllowsFormTarget(csp, pageOrigin, target string) bool {
	u, err := url.Parse(target)
	if err != nil {
		return false
	}
	to := u.Scheme + "://" + u.Host
	for _, src := range strings.Fields(formActionOf(csp))[1:] {
		if src == "'self'" && to == pageOrigin || src == to {
			return true
		}
	}
	return false
}

func consentPage(e *oauthEnv, cid, redirect string) (id, nonce, csp string) {
	e.t.Helper()
	res, body := e.human.get(e.srv+e.authURL(cid, redirect, nil), map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate"})
	m := consentRe.FindStringSubmatch(body)
	if res.StatusCode != 200 || m == nil {
		e.t.Fatalf("authorize : %d %s", res.StatusCode, body)
	}
	r, b := e.human.get(e.srv+"/oauth/consent/"+m[1], map[string]string{"Sec-Fetch-Site": "same-origin"})
	n := nonceRe.FindStringSubmatch(b)
	if r.StatusCode != 200 || n == nil {
		e.t.Fatalf("consentement : %d %s", r.StatusCode, b)
	}
	return m[1], n[1], r.Header.Get("Content-Security-Policy")
}

func TestConsentPageCSPAllowsOnlyTheRequestRedirectOrigin(t *testing.T) {
	e := newOAuthEnv(t)
	for _, tc := range []struct{ registered, redirect, origin string }{
		{claudeCallback, claudeCallback, "https://claude.ai"},
		{"https://claude.com/api/mcp/auth_callback", "https://claude.com/api/mcp/auth_callback", "https://claude.com"},
		{"http://127.0.0.1/callback", "http://127.0.0.1:53682/callback", "http://127.0.0.1:53682"},
		{"http://localhost/callback", "http://localhost:41999/callback", "http://localhost:41999"},
	} {
		cid := e.register(tc.registered)
		_, _, csp := consentPage(e, cid, tc.redirect)
		want := "default-src 'none'; style-src 'unsafe-inline'; form-action 'self' " + tc.origin + "; frame-ancestors 'none'; base-uri 'none'"
		if csp != want {
			t.Errorf("%s : CSP %q, attendu %q", tc.redirect, csp, want)
		}
		if strings.Contains(csp, "*") {
			t.Errorf("joker dans la CSP : %q", csp)
		}
	}
	// Les autres pages humaines gardent `form-action 'self'` seul.
	r, _ := e.human.get(e.srv+"/enroll", nil)
	if fa := formActionOf(r.Header.Get("Content-Security-Policy")); fa != "form-action 'self'" {
		t.Errorf("CSP de /enroll : %q", fa)
	}
	// La page « Continuer » n'a aucun formulaire : CSP stricte inchangée.
	cid := e.register(claudeCallback)
	res, _ := e.human.get(e.srv+e.authURL(cid, claudeCallback, nil), map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate"})
	if fa := formActionOf(res.Header.Get("Content-Security-Policy")); fa != "form-action 'self'" {
		t.Errorf("CSP de /oauth/authorize : %q", fa)
	}
}

// De bout en bout : on suit ce que ferait le navigateur et on vérifie que la cible de retour est autorisée par la
// CSP de la page qui soumet, pour l'autorisation comme pour le refus.
func TestConsentReturnTargetIsAllowedByTheSubmittingPageCSP(t *testing.T) {
	e := newOAuthEnv(t)
	for _, tc := range []struct{ registered, redirect string }{
		{claudeCallback, claudeCallback},
		{"http://127.0.0.1/callback", "http://127.0.0.1:53682/callback"},
	} {
		for _, decision := range []string{"allow", "deny"} {
			cid := e.register(tc.registered)
			id, nonce, csp := consentPage(e, cid, tc.redirect)
			r, body := e.human.post(e.srv+"/oauth/consent/"+id, url.Values{"nonce": {nonce}, "decision": {decision}}, nil)
			if r.StatusCode != 200 || r.Header.Get("Location") != "" {
				t.Fatalf("la réponse au POST ne doit pas être une redirection : %d %q", r.StatusCode, r.Header.Get("Location"))
			}
			target := backLink(t, body)
			if !strings.HasPrefix(target, tc.redirect+"?") {
				t.Errorf("cible inattendue : %s", target)
			}
			if !cspAllowsFormTarget(csp, e.srv, target) {
				t.Errorf("%s/%s : la CSP %q interdirait %s", tc.redirect, decision, csp, target)
			}
			if strings.Contains(body, "<script") || strings.Contains(r.Header.Get("Content-Security-Policy"), "script-src") {
				t.Errorf("script sur la page de retour")
			}
		}
	}
}

func TestSecondConsentPostShowsAlreadyGivenWithoutNewCode(t *testing.T) {
	e := newOAuthEnv(t)
	cid := e.register(claudeCallback)
	id, nonce, _ := consentPage(e, cid, claudeCallback)
	_, b1 := e.human.post(e.srv+"/oauth/consent/"+id, url.Values{"nonce": {nonce}, "decision": {"allow"}}, nil)
	first := backLink(t, b1)
	code1, _, _ := codeOf(t, first)
	for i := 0; i < 3; i++ {
		r, b := e.human.post(e.srv+"/oauth/consent/"+id, url.Values{"nonce": {nonce}, "decision": {"allow"}}, nil)
		if r.StatusCode != 200 || !strings.Contains(b, "Autorisation déjà donnée") || strings.Contains(b, "expirée") {
			t.Fatalf("rejeu %d : %d %s", i, r.StatusCode, b)
		}
		if strings.Contains(b, "code=") && !strings.Contains(html2(b), code1) {
			t.Errorf("le rejeu expose un autre code : %s", b)
		}
	}
	var n int
	if err := e.c.Store.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM audit_log WHERE operation='oauth.approved'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("oauth.approved : %d (%v), un seul attendu", n, err)
	}
	// Un rejeu ne vaut rien sans le bon nonce ni le cookie du navigateur initiateur.
	if r, _ := e.human.post(e.srv+"/oauth/consent/"+id, url.Values{"nonce": {"faux"}, "decision": {"allow"}}, nil); r.StatusCode != 400 {
		t.Errorf("rejeu avec mauvais nonce : %d", r.StatusCode)
	}
	// Le code reste à usage unique : un seul échange réussit.
	tokf := url.Values{"grant_type": {"authorization_code"}, "client_id": {cid}, "code": {code1}, "redirect_uri": {claudeCallback},
		"code_verifier": {verifier}, "resource": {e.srv + "/mcp"}}
	if st, _ := e.token(tokf); st != 200 {
		t.Fatalf("premier échange : %d", st)
	}
	if st, _ := e.token(tokf); st != 400 {
		t.Errorf("second échange : %d", st)
	}
}

func html2(s string) string { return strings.ReplaceAll(s, "&amp;", "&") }

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func TestOAuthJournalHasNoSecrets(t *testing.T) {
	buf := &syncBuf{}
	ctx, cancel := context.WithCancel(context.Background())
	c, err := core.Open(ctx, core.Config{DataDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
		PollInterval: 10 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	srv := httptest.NewServer(httpapi.NewHandlerWithOptions(c, httpapi.Options{APIToken: oauthStaticToken}))
	c.Catalog.SetBaseURL(srv.URL)
	t.Cleanup(func() { srv.Close(); cancel(); <-done; c.Close() })
	e := &oauthEnv{t: t, c: c, srv: srv.URL, human: enrolled(t, c, srv.URL), plain: plainClient}

	const sentState = "SENTINELSTATE-7f3a91"
	const sentAuth = "SENTINELAUTHZ-55c2"
	cid := e.register(claudeCallback)
	res, body := e.human.get(e.srv+e.authURL(cid, claudeCallback, url.Values{"state": {sentState}}), map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate"})
	m := consentRe.FindStringSubmatch(body)
	if res.StatusCode != 200 || m == nil {
		t.Fatalf("authorize : %d", res.StatusCode)
	}
	_, b3 := e.human.get(e.srv+"/oauth/consent/"+m[1], map[string]string{"Sec-Fetch-Site": "same-origin"})
	n := nonceRe.FindStringSubmatch(b3)[1]
	_, b4 := e.human.post(e.srv+"/oauth/consent/"+m[1], url.Values{"nonce": {n}, "decision": {"allow"}}, nil)
	code, _, _ := codeOf(t, backLink(t, b4))
	e.human.post(e.srv+"/oauth/consent/"+m[1], url.Values{"nonce": {n}, "decision": {"allow"}}, nil)
	_, tok := e.token(url.Values{"grant_type": {"authorization_code"}, "client_id": {cid}, "code": {code}, "redirect_uri": {claudeCallback},
		"code_verifier": {verifier}, "resource": {e.srv + "/mcp"}})
	e.do("POST", "/oauth/token", map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Authorization": "Bearer " + sentAuth},
		url.Values{"grant_type": {"authorization_code"}, "client_id": {cid}, "code": {code}, "redirect_uri": {claudeCallback}, "code_verifier": {verifier}}.Encode())
	e.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {cid}, "refresh_token": {tok["refresh_token"].(string)}})

	logs := buf.String()
	for _, secret := range []string{sentState, sentAuth, code, verifier, tok["access_token"].(string), tok["refresh_token"].(string), challengeOf(verifier), n} {
		if strings.Contains(logs, secret) {
			t.Errorf("secret présent dans le journal : %q", secret)
		}
	}
	for _, want := range []string{
		`msg="OAuth register"`, `msg="OAuth authorize"`, `msg="OAuth consent"`, `msg="OAuth token"`,
		"client_id=" + cid, "redirect_origin=https://claude.ai", "decision=approved", "decision=already",
		"grant_type=authorization_code", "grant_type=refresh_token", "error=invalid_grant", "redirect_uris=1",
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("journal : %q absent\n%s", want, logs)
		}
	}
	for _, l := range strings.Split(logs, "\n") {
		if strings.Contains(l, "OAuth") && strings.Contains(l, "level=INFO") == false {
			t.Errorf("ligne OAuth hors INFO : %s", l)
		}
	}
}
