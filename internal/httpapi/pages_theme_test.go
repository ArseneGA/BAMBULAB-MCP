package httpapi_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth/pagetest"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/oauth"
)

// CSP des pages humaines d'AVANT l'habillage Printalk : l'habillage ne les élargit pas.
const (
	cspHumanBase   = "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"
	cspPublicPages = "default-src 'none'; style-src 'unsafe-inline'; img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
)

var navSame = map[string]string{"Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "navigate"}

// Pages OAuth : passerelle, consentement, retour à Claude, déjà autorisé, demande refusée ; /agents/pair ; HTTPS requis.
func TestOAuthAndSetupPagesUseCommonTheme(t *testing.T) {
	e := newOAuthEnv(t)
	cid := e.register(claudeCallback)

	// 1. passerelle « Autoriser une application ? » (navigation cross-site, sans cookie de session SameSite)
	res, body := e.human.get(e.srv+e.authURL(cid, claudeCallback, nil), map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate"})
	if res.StatusCode != 200 || !strings.Contains(body, "Autoriser une application ?") {
		t.Fatalf("passerelle : %d %s", res.StatusCode, body)
	}
	pagetest.Check(t, "/oauth/authorize", body)
	pagetest.CheckCSP(t, "/oauth/authorize", res.Header, cspHumanBase)
	pagetest.DumpBody(t, "oauth-authorize", body)
	m := consentRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("lien de consentement absent")
	}

	// 2. consentement : tous les éléments de sécurité restent là, CSP form-action vers l'origine de retour inchangée
	res, body = e.human.get(e.srv+"/oauth/consent/"+m[1], map[string]string{"Sec-Fetch-Site": "same-origin"})
	if res.StatusCode != 200 {
		t.Fatalf("consentement : %d %s", res.StatusCode, body)
	}
	pagetest.Check(t, "/oauth/consent", body)
	pagetest.CheckCSP(t, "/oauth/consent", res.Header,
		"default-src 'none'; style-src 'unsafe-inline'; form-action 'self' "+oauth.Origin(claudeCallback)+"; frame-ancestors 'none'; base-uri 'none'")
	pagetest.DumpBody(t, "oauth-consent", body)
	for _, f := range []string{"Nom déclaré par le client (non vérifié)", "Retour vers", "Demande faite depuis l&#39;adresse", "Heure de la demande", "Ressource", "Droit",
		"<strong>jamais</strong> de lancer ou d&#39;arrêter une impression", `name="decision" value="allow"`, `name="decision" value="deny"`, `name="nonce"`} {
		if !strings.Contains(body, f) && !strings.Contains(body, strings.ReplaceAll(f, "&#39;", "'")) {
			t.Errorf("consentement : %q absent", f)
		}
	}
	if !strings.Contains(body, `value="deny" class="danger"`) {
		t.Error("« Refuser » doit être en style danger")
	}
	n := nonceRe.FindStringSubmatch(body)
	if n == nil {
		t.Fatal("nonce absent")
	}

	// 3. page « Retour à Claude » (meta refresh inclus) puis 4. « Autorisation déjà donnée »
	res, back := e.human.post(e.srv+"/oauth/consent/"+m[1], url.Values{"nonce": {n[1]}, "decision": {"allow"}}, nil)
	if res.StatusCode != 200 || !strings.Contains(back, `http-equiv="refresh"`) {
		t.Fatalf("retour : %d %s", res.StatusCode, back)
	}
	pagetest.Check(t, "retour à Claude", back)
	pagetest.CheckCSP(t, "retour à Claude", res.Header, cspHumanBase) // inchangée : seule la page de consentement porte l'origine de retour
	pagetest.DumpBody(t, "oauth-retour", back)
	res, again := e.human.post(e.srv+"/oauth/consent/"+m[1], url.Values{"nonce": {n[1]}, "decision": {"allow"}}, nil)
	if !strings.Contains(again, "Autorisation déjà donnée") {
		t.Fatalf("rejeu : %d %s", res.StatusCode, again)
	}
	pagetest.Check(t, "autorisation déjà donnée", again)

	// 5. « Demande refusée » : consentement inconnu
	res, body = e.human.get(e.srv+"/oauth/consent/inconnu", map[string]string{"Sec-Fetch-Site": "same-origin"})
	if res.StatusCode < 400 || !strings.Contains(body, "pt-card") {
		t.Fatalf("consentement inconnu : %d %s", res.StatusCode, body)
	}
	pagetest.Check(t, "demande refusée", body)
	pagetest.CheckCSP(t, "demande refusée", res.Header, cspHumanBase)
	pagetest.DumpBody(t, "oauth-demande-refusee", body)

	// 6. /agents/pair (formulaire puis jeton)
	res, body = e.human.get(e.srv+"/agents/pair", navSame)
	if res.StatusCode != 200 {
		t.Fatalf("/agents/pair : %d", res.StatusCode)
	}
	pagetest.Check(t, "/agents/pair", body)
	pagetest.CheckCSP(t, "/agents/pair", res.Header, cspHumanBase)
	res, body = e.human.post(e.srv+"/agents/pair", url.Values{}, nil)
	if res.StatusCode != 200 || !strings.Contains(body, "Jeton d&#39;appairage") && !strings.Contains(body, "Jeton d'appairage") {
		t.Fatalf("/agents/pair POST : %d %s", res.StatusCode, body)
	}
	pagetest.Check(t, "/agents/pair (jeton)", body)

	// 7. /enroll sur l'écouteur principal
	res, body = e.human.get(e.srv+"/enroll", navSame)
	pagetest.Check(t, "/enroll (enregistré)", body)
	pagetest.CheckCSP(t, "/enroll", res.Header, cspHumanBase)
}

func TestHTTPSRequiredPageUsesCommonTheme(t *testing.T) {
	_, srv := humanEnv(t, httpapi.Options{APIToken: "0123456789abcdef-secret", PublicURLs: []string{"http://192.168.0.8:7878", "https://bambu.ts.net"}})
	req, _ := http.NewRequest("GET", srv.URL+"/confirm/abc", nil)
	req.Host = "192.168.0.8:7878"
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusForbidden || !strings.Contains(string(b), "exige HTTPS") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	pagetest.Check(t, "HTTPS requis", string(b))
	pagetest.CheckCSP(t, "HTTPS requis", res.Header, cspHumanBase)
	pagetest.DumpBody(t, "https-requis", string(b))
}

// Pages publiques /about et /privacy, et page d'explication du consentement sur l'écouteur public.
func TestPublicPagesUseCommonTheme(t *testing.T) {
	e := newPubEnv(t, httpapi.Options{ContactEmail: "moi@example.org"})
	for name, p := range map[string]string{"about": "/about", "privacy": "/privacy"} {
		res, body := e.do(e.pub.URL, "GET", p, nil, "")
		if res.StatusCode != 200 {
			t.Fatalf("%s : %d", p, res.StatusCode)
		}
		pagetest.Check(t, p, body)
		pagetest.CheckCSP(t, p, res.Header, cspPublicPages)
		pagetest.DumpBody(t, "public-"+name, body)
		if !strings.Contains(body, `<link rel="icon" href="/icon.svg"`) || !strings.Contains(body, `name="robots" content="noindex"`) {
			t.Errorf("%s : icône ou noindex perdus", p)
		}
	}
	// Une page d'erreur humaine de l'écouteur public : consentement d'un navigateur non enregistré (403).
	res, body := e.do(e.pub.URL, "GET", "/oauth/consent/abc123", nil, "")
	if res.StatusCode != 403 || !strings.Contains(body, "Navigateur non enregistré") {
		t.Fatalf("consentement public sans session : %d %s", res.StatusCode, body)
	}
	pagetest.Check(t, "403 écouteur public", body)
	pagetest.CheckCSP(t, "403 écouteur public", res.Header, cspHumanBase)
	pagetest.DumpBody(t, "erreur-403-public", body)
}
