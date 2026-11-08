package humanauth

import (
	"encoding/json"
	"errors"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// PageHeaders pose les en-têtes communs des pages humaines (anti-clickjacking, pas de cache, CSP stricte :
// aucune image, aucun script).
func PageHeaders(w http.ResponseWriter) {
	pageHeaders(w, "")
}

// PageHeadersWithImages est PageHeaders pour une page qui affiche des images de SA PROPRE origine (photo du plateau
// de /confirm) : la CSP ajoute `img-src 'self'`. Sans cette directive, `default-src 'none'` bloque toute <img> (icône
// brisée alors que l'image est servie en 200). Jamais de `data:` ni d'origine tierce.
func PageHeadersWithImages(w http.ResponseWriter) {
	pageHeaders(w, "img-src 'self'; ")
}

func pageHeaders(w http.ResponseWriter, extra string) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "default-src 'none'; "+extra+"style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
}

// Style est la feuille de style commune des pages humaines.
const Style = `:root{color-scheme:light dark;--bg:#f6f5f2;--fg:#1d2024;--mut:#5b6068;--line:#cfcbc2;--acc:#1f4f8f;--warn:#9a3412}
@media (prefers-color-scheme:dark){:root{--bg:#15171a;--fg:#e8e9eb;--mut:#a0a5ad;--line:#33373d;--acc:#7fb0ee;--warn:#f0a070}}
body{background:var(--bg);color:var(--fg);font:16px/1.5 system-ui,sans-serif;margin:0;padding:24px 16px}
main{max-width:640px;margin:0 auto}h1{font-size:1.35rem;margin:0 0 .75rem}
dl{display:grid;grid-template-columns:max-content 1fr;gap:.35rem 1rem;border-top:1px solid var(--line);border-bottom:1px solid var(--line);padding:.9rem 0;margin:1rem 0}
dt{color:var(--mut)}dd{margin:0;overflow-wrap:anywhere}code{font:13px ui-monospace,monospace}
.warn{border-left:4px solid var(--warn);padding:.5rem .8rem;margin:1rem 0}
label.chk{display:flex;gap:.6rem;align-items:flex-start;margin:1rem 0;font-weight:600}
input[type=password],input[type=text]{width:100%;box-sizing:border-box;padding:.55rem;font:inherit;border:1px solid var(--line);background:transparent;color:inherit}
button{font:inherit;font-weight:600;padding:.6rem 1.2rem;border:2px solid var(--acc);background:var(--acc);color:var(--bg);cursor:pointer}
button:focus-visible,input:focus-visible{outline:3px solid var(--warn);outline-offset:2px}
.ok{border-left:4px solid var(--acc);padding:.5rem .8rem}`

var tpl = template.Must(template.New("h").Parse(`{{define "head"}}<!doctype html><html lang="fr"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer">
<title>{{.Title}}</title><style>` + Style + `</style></head><body><main>{{end}}
{{define "foot"}}</main></body></html>{{end}}
{{define "enroll"}}{{template "head" .}}<h1>Enregistrer ce navigateur</h1>
{{if .Msg}}<p class="warn">{{.Msg}}</p>{{end}}
<p>Les autorisations de lancer ou d'arrêter une impression ne se donnent que depuis un navigateur enregistré. Sur le poste qui fait tourner bambud, ouvre un terminal et lance <code>bambud enroll</code> : le code à 8 caractères s'affiche dans ce terminal (il n'est jamais transmis à Claude). Saisis-le ici.</p>
<form method="post" action="/enroll" autocomplete="off"><input type="hidden" name="next" value="{{.Next}}">
<p><label>Code d'enregistrement<br><input type="text" name="code" required minlength="8" maxlength="12" autocomplete="off" autocapitalize="characters" spellcheck="false"></label></p>
<p><label>Nom de ce navigateur (facultatif)<br><input type="text" name="label" maxlength="80" autocomplete="off"></label></p>
<p><button type="submit">Enregistrer</button></p></form>{{template "foot"}}{{end}}
{{define "done"}}{{template "head" .}}<h1>{{.Title}}</h1><p class="ok">{{.Msg}}</p>{{if .Form}}<form method="post" action="/enroll/logout"><button type="submit">Retirer ce navigateur</button></form>{{end}}{{template "foot"}}{{end}}`))

// SafeNext n'accepte qu'un chemin local (jamais une URL absolue ni un « // »).
func SafeNext(n string) string {
	if n == "" || !strings.HasPrefix(n, "/") || strings.HasPrefix(n, "//") || strings.Contains(n, "\\") || len(n) > 300 {
		return ""
	}
	u, err := url.Parse(n)
	if err != nil || u.Host != "" || u.Scheme != "" {
		return ""
	}
	return n
}

// Render écrit une page du modèle commun.
func Render(w http.ResponseWriter, status int, name string, data map[string]any) {
	PageHeaders(w)
	w.WriteHeader(status)
	_ = tpl.ExecuteTemplate(w, name, data)
}

// fetchOK : une soumission de formulaire légitime vient d'une navigation de la même origine.
// Un en-tête absent est refusé (navigateur trop ancien ou client non navigateur).
func fetchOK(r *http.Request) bool {
	return r.Header.Get("Sec-Fetch-Site") == "same-origin" && r.Header.Get("Sec-Fetch-Mode") == "navigate"
}

// Require protège un handler de page humaine : il faut un navigateur enregistré (cookie de session).
// GET/HEAD sans session -> redirection vers /enroll. POST : session ET Sec-Fetch-Site same-origin ET
// Sec-Fetch-Mode navigate, sinon 403. Le nonce du formulaire est vérifié par la page elle-même.
func (s *Service) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := s.SessionFromRequest(r)
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			if err != nil {
				http.Redirect(w, r, "/enroll?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
				return
			}
		case http.MethodPost:
			if err != nil {
				Render(w, http.StatusForbidden, "done", map[string]any{"Title": "Navigateur non enregistré", "Msg": "Enregistre ce navigateur (bambud enroll puis /enroll) avant de donner une autorisation."})
				return
			}
			if !fetchOK(r) {
				Render(w, http.StatusForbidden, "done", map[string]any{"Title": "Requête refusée", "Msg": "La soumission doit venir d'une navigation de cette page (en-têtes Sec-Fetch)."})
				return
			}
		default:
			http.Error(w, "méthode non autorisée", http.StatusMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RegisterPages monte /enroll (public mais limité), /enroll/logout et GET /api/session.
func (s *Service) RegisterPages(mux *http.ServeMux) {
	mux.HandleFunc("GET /enroll", func(w http.ResponseWriter, r *http.Request) {
		if _, err := s.SessionFromRequest(r); err == nil {
			Render(w, http.StatusOK, "done", map[string]any{"Title": "Navigateur déjà enregistré", "Msg": "Ce navigateur peut donner les autorisations de lancement et d'arrêt.", "Form": true})
			return
		}
		Render(w, http.StatusOK, "enroll", map[string]any{"Title": "Enregistrer ce navigateur", "Next": SafeNext(r.URL.Query().Get("next"))})
	})
	mux.HandleFunc("POST /enroll", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if !fetchOK(r) {
			Render(w, http.StatusForbidden, "done", map[string]any{"Title": "Requête refusée", "Msg": "La soumission doit venir d'une navigation de la page /enroll."})
			return
		}
		if err := r.ParseForm(); err != nil {
			Render(w, http.StatusBadRequest, "done", map[string]any{"Title": "Requête invalide", "Msg": "Formulaire illisible."})
			return
		}
		label := strings.TrimSpace(r.PostFormValue("label"))
		if label == "" {
			label = shortUA(r.UserAgent())
		}
		token, sess, err := s.EnrollFrom(r.Context(), s.clientKey(r), r.PostFormValue("code"), label)
		if err != nil {
			st, msg := http.StatusForbidden, ErrBadCode.Error()
			switch {
			case errors.Is(err, ErrLocked):
				st, msg = http.StatusTooManyRequests, ErrLocked.Error()
			case errors.Is(err, ErrBadCode):
			default:
				st, msg = http.StatusInternalServerError, "Erreur interne."
			}
			Render(w, st, "enroll", map[string]any{"Title": "Enregistrer ce navigateur", "Msg": msg, "Next": SafeNext(r.PostFormValue("next"))})
			return
		}
		s.setCookie(w, r, token, sess)
		if n := SafeNext(r.PostFormValue("next")); n != "" {
			http.Redirect(w, r, n, http.StatusSeeOther)
			return
		}
		Render(w, http.StatusOK, "done", map[string]any{"Title": "Navigateur enregistré", "Msg": "Ce navigateur peut désormais confirmer le lancement ou l'arrêt d'une impression. Révocation : bambud enroll --revoke " + sess.ID + ".", "Form": true})
	})
	mux.Handle("POST /enroll/logout", s.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sess, err := s.SessionFromRequest(r); err == nil {
			_, _ = s.Revoke(r.Context(), sess.ID)
		}
		http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		Render(w, http.StatusOK, "done", map[string]any{"Title": "Navigateur retiré", "Msg": "Ce navigateur n'est plus enregistré."})
	})))
	mux.HandleFunc("GET /api/session", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		out := map[string]any{"enrolled": false, "enroll_url": "/enroll", "how": "bambud enroll (terminal de l'humain) puis /enroll"}
		if sess, err := s.SessionFromRequest(r); err == nil {
			out["enrolled"] = true
			out["session"] = map[string]any{"id": sess.ID, "label": sess.Label, "expires_at": sess.ExpiresAt}
		}
		_ = json.NewEncoder(w).Encode(out)
	})
}

func shortUA(ua string) string {
	ua = strings.TrimSpace(ua)
	if len(ua) > 60 {
		ua = ua[:60]
	}
	return ua
}

// clientKey identifie l'appelant pour le limiteur d'essais.
func (s *Service) clientKey(r *http.Request) string {
	if s.o.ClientKey != nil {
		return s.o.ClientKey(r)
	}
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}
