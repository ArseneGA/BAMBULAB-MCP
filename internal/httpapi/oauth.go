package httpapi

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/oauth"
)

// Serveur OAuth 2.1 intégré (connecteur distant de Claude). Voir internal/oauth et docs/OAUTH.md.
//
// Routes : /.well-known/oauth-protected-resource[/mcp] (RFC 9728), /.well-known/oauth-authorization-server
// (RFC 8414), POST /oauth/register (RFC 7591), GET /oauth/authorize, GET|POST /oauth/consent/{id} (page humaine :
// cookie d'un navigateur enregistré), POST /oauth/token. Le jeton d'accès ne vaut que sur /mcp.

// isOAuthOpen : routes OAuth sans Bearer d'API (le client n'en a pas encore). Les métadonnées GET passent déjà
// comme fichiers publics ; restent les POST d'enregistrement et d'échange de jetons. /oauth/consent/ est humain.
func isOAuthOpen(r *http.Request) bool {
	switch r.URL.Path {
	case "/oauth/register", "/oauth/token":
		return r.Method == http.MethodPost
	case "/oauth/authorize":
		return r.Method == http.MethodGet || r.Method == http.MethodHead
	}
	return false
}

// baseURL : URL publique sous laquelle ce Host est atteint (l'hôte a déjà passé la garde Host).
func (o Options) baseURL(r *http.Request) string {
	host := hostOnly(r.Host)
	for _, p := range o.publicURLs() {
		if u, err := url.Parse(p); err == nil && strings.EqualFold(u.Hostname(), host) {
			return strings.TrimRight(p, "/")
		}
	}
	scheme := "http"
	if o.requestHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func bearerOf(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

var oauthTpl = template.Must(template.New("oauth").Parse(`{{define "head"}}<!doctype html><html lang="fr"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>{{.Title}}</title>
<style>` + humanauth.Style + `</style></head><body><main><h1>{{.Title}}</h1>{{end}}
{{define "foot"}}</main></body></html>{{end}}
{{define "gate"}}{{template "head" .}}
<p><strong>{{.Client}}</strong> demande l'accès à ton atelier Bambu Lab (outils MCP de bambud).</p>
<p>Pour des raisons de sécurité, l'autorisation se donne depuis un <strong>navigateur enregistré</strong> (<code>bambud enroll</code>).</p>
<p><a href="/oauth/consent/{{.ID}}"><button type="button">Continuer</button></a></p>{{template "foot"}}{{end}}
{{define "consent"}}{{template "head" .}}
<p><strong>{{.Client}}</strong> demande à se connecter à ton atelier Bambu Lab.</p>
<dl><dt>Application</dt><dd>{{.Client}}</dd><dt>Retour vers</dt><dd><code>{{.Host}}</code></dd><dt>Ressource</dt><dd><code>{{.Resource}}</code></dd><dt>Droit</dt><dd>outils MCP de l'atelier (fichiers, devis, aperçus, état des imprimantes)</dd></dl>
{{if .Local}}<p class="warn">Cette application revient sur ton propre ordinateur (boucle locale) : tout programme local pourrait se faire passer pour elle. N'autorise que si tu viens de la lancer.</p>{{end}}
<p class="ok">Cette autorisation ne permet <strong>jamais</strong> de lancer ou d'arrêter une impression : ces gestes restent sur les pages /confirm et /stop, dans un navigateur enregistré. Tu peux la révoquer à tout moment : <code>bambud oauth --revoke</code>.</p>
<form method="post" action="/oauth/consent/{{.ID}}"><input type="hidden" name="nonce" value="{{.Nonce}}">
<p><button type="submit" name="decision" value="allow">Autoriser</button> <button type="submit" name="decision" value="deny" style="background:transparent;color:inherit">Refuser</button></p></form>{{template "foot"}}{{end}}
{{define "msg"}}{{template "head" .}}<p class="warn">{{.Msg}}</p>{{template "foot"}}{{end}}`))

func oauthPage(w http.ResponseWriter, status int, name string, data map[string]any) {
	humanauth.PageHeaders(w)
	w.WriteHeader(status)
	_ = oauthTpl.ExecuteTemplate(w, name, data)
}

func oauthJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, status, v) // Cache-Control: no-store
}

func oauthErr(w http.ResponseWriter, e *oauth.Error) {
	oauthJSON(w, e.Status, map[string]string{"error": e.Code, "error_description": e.Desc})
}

// registerOAuth monte le serveur d'autorisation et branche la vérification des jetons sur la garde de /mcp.
func registerOAuth(mux *http.ServeMux, c *core.Core, human *humanauth.Service, opts *Options) *oauth.Service {
	svc := oauth.New(c.Store, oauth.Options{ExtraRedirects: opts.OAuthRedirects})
	resourceOf := func(r *http.Request) string { return opts.baseURL(r) + "/mcp" }

	opts.extraBearer = func(r *http.Request) bool {
		t := bearerOf(r)
		return t != "" && svc.Verify(r.Context(), t, resourceOf(r))
	}
	opts.challenge = func(r *http.Request) string {
		return `Bearer realm="bambud", resource_metadata="` + opts.baseURL(r) + `/.well-known/oauth-protected-resource", scope="` + oauth.Scope + `"`
	}

	prm := func(w http.ResponseWriter, r *http.Request) {
		b := opts.baseURL(r)
		oauthJSON(w, http.StatusOK, map[string]any{
			"resource": b + "/mcp", "authorization_servers": []string{b}, "scopes_supported": []string{oauth.Scope},
			"bearer_methods_supported": []string{"header"}, "resource_name": "Atelier Bambu Lab (bambud)",
		})
	}
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", prm)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", prm)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		b := opts.baseURL(r)
		oauthJSON(w, http.StatusOK, map[string]any{
			"issuer": b, "authorization_endpoint": b + "/oauth/authorize", "token_endpoint": b + "/oauth/token",
			"registration_endpoint": b + "/oauth/register", "scopes_supported": []string{oauth.Scope},
			"response_types_supported": []string{"code"}, "response_modes_supported": []string{"query"},
			"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported":               []string{"S256"},
			"token_endpoint_auth_methods_supported":          []string{"none"},
			"authorization_response_iss_parameter_supported": true,
		})
	})
	// Toute autre métadonnée .well-known : 404 JSON (jamais le repli HTML de la SPA, qui tromperait un client).
	mux.HandleFunc("GET /.well-known/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "introuvable"})
	})

	mux.HandleFunc("POST /oauth/register", func(w http.ResponseWriter, r *http.Request) {
		body := http.MaxBytesReader(w, r.Body, 16<<10)
		var raw json.RawMessage
		if err := json.NewDecoder(body).Decode(&raw); err != nil {
			oauthErr(w, &oauth.Error{Status: 400, Code: "invalid_client_metadata", Desc: "JSON attendu (≤ 16 Kio)"})
			return
		}
		out, e := svc.Register(r.Context(), raw)
		if e != nil {
			oauthErr(w, e)
			return
		}
		oauthJSON(w, http.StatusCreated, out)
	})

	mux.HandleFunc("GET /oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		b := opts.baseURL(r)
		res := svc.Authorize(r.Context(), r.URL.Query(), b+"/mcp")
		switch {
		case res.Fatal != "":
			oauthPage(w, http.StatusBadRequest, "msg", map[string]any{"Title": "Demande invalide", "Msg": res.Fatal})
		case res.RedirectError != nil:
			http.Redirect(w, r, oauth.ErrorRedirect(res.RedirectURI, res.State, b, res.RedirectError), http.StatusSeeOther)
		default:
			// Le cookie du navigateur enregistré est SameSite=Strict : il n'accompagne pas cette navigation venue de
			// Claude (autre site). On passe par une page intermédiaire dont le lien est, lui, une navigation interne.
			name := res.Pending.ClientName
			if name == "" {
				name = "Une application"
			}
			oauthPage(w, http.StatusOK, "gate", map[string]any{"Title": "Autoriser une application ?", "Client": name, "ID": res.Pending.ID})
		}
	})

	mux.Handle("GET /oauth/consent/{id}", human.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := svc.PendingByID(r.PathValue("id"))
		if !ok {
			oauthPage(w, http.StatusNotFound, "msg", map[string]any{"Title": "Demande expirée", "Msg": "Cette demande a expiré ou a déjà été traitée. Relance la connexion depuis Claude."})
			return
		}
		name := p.ClientName
		if name == "" {
			name = "Une application"
		}
		host, local := "", false
		if u, err := url.Parse(p.RedirectURI); err == nil {
			host = u.Host
			local = u.Scheme == "http"
		}
		oauthPage(w, http.StatusOK, "consent", map[string]any{"Title": "Autoriser " + name + " ?", "Client": name, "Host": host, "Local": local,
			"Resource": p.Resource, "ID": p.ID, "Nonce": p.Nonce})
	})))
	mux.Handle("POST /oauth/consent/{id}", human.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := r.ParseForm(); err != nil {
			oauthPage(w, http.StatusBadRequest, "msg", map[string]any{"Title": "Requête invalide", "Msg": "Formulaire illisible."})
			return
		}
		sess, err := human.SessionFromRequest(r)
		if err != nil {
			oauthPage(w, http.StatusForbidden, "msg", map[string]any{"Title": "Navigateur non enregistré", "Msg": "Enregistre ce navigateur avant d'autoriser."})
			return
		}
		redir, err := svc.Decide(r.Context(), r.PathValue("id"), r.PostFormValue("nonce"), opts.baseURL(r), sess.ID, r.PostFormValue("decision") == "allow")
		if err != nil {
			oauthPage(w, http.StatusBadRequest, "msg", map[string]any{"Title": "Demande refusée", "Msg": err.Error()})
			return
		}
		// Chrome applique form-action aux redirections : on autorise l'origine de retour (et elle seule).
		if u, e := url.Parse(redir); e == nil {
			humanauth.PageHeaders(w)
			w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self' "+u.Scheme+"://"+u.Host+"; frame-ancestors 'none'; base-uri 'none'")
		}
		http.Redirect(w, r, redir, http.StatusSeeOther)
	})))

	mux.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if ct := strings.ToLower(r.Header.Get("Content-Type")); !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
			oauthErr(w, &oauth.Error{Status: 400, Code: "invalid_request", Desc: "Content-Type application/x-www-form-urlencoded requis"})
			return
		}
		if err := r.ParseForm(); err != nil {
			oauthErr(w, &oauth.Error{Status: 400, Code: "invalid_request", Desc: "formulaire illisible"})
			return
		}
		out, e := svc.Token(context.WithoutCancel(r.Context()), r.PostForm)
		if e != nil {
			oauthErr(w, e)
			return
		}
		oauthJSON(w, http.StatusOK, out)
	})
	return svc
}
