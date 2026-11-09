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

// PageHeadersWith est PageHeaders pour les pages qui montrent une image de leur origine (images) et/ou chargent le
// script statique des passkeys (script) : la CSP ajoute alors `img-src 'self'` et `script-src 'self'` AU MINIMUM, jamais
// de script en ligne, jamais d'origine tierce. Sans l'une ni l'autre, c'est exactement PageHeaders.
func PageHeadersWith(w http.ResponseWriter, images, script bool) {
	extra := ""
	if images {
		extra += "img-src 'self'; "
	}
	if script {
		extra += "script-src 'self'; "
	}
	pageHeaders(w, extra)
}

// PageHeadersFormAction est PageHeaders pour une page dont le formulaire aboutit, par redirection, vers une origine
// externe précise (consentement OAuth : retour vers la redirect_uri validée). Chrome applique le `form-action` du
// DOCUMENT QUI SOUMET aux redirections de la soumission : sans cette origine, le 303 vers le client est bloqué.
// origin = schéma://hôte[:port] exact (jamais de joker) ; vide ou douteux : `'self'` seul.
func PageHeadersFormAction(w http.ResponseWriter, origin string) {
	pageHeadersFA(w, "", origin)
}

func pageHeaders(w http.ResponseWriter, extra string) { pageHeadersFA(w, extra, "") }

func pageHeadersFA(w http.ResponseWriter, extra, origin string) {
	fa := "'self'"
	if origin != "" && !strings.ContainsAny(origin, " ;,'\"*\\\r\n\t") {
		fa += " " + origin
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "default-src 'none'; "+extra+"style-src 'unsafe-inline'; form-action "+fa+"; frame-ancestors 'none'; base-uri 'none'")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
}

var tpl = template.Must(template.New("h").Parse(Layout("", "", "") + `{{define "enroll"}}{{template "head" .}}<h1>Enregistrer ce navigateur</h1>
{{if .Msg}}<p class="warn">{{.Msg}}</p>{{end}}
<p>Les autorisations de lancer ou d'arrêter une impression ne se donnent que depuis un navigateur enregistré. Sur le poste qui fait tourner bambud, ouvre un terminal et lance <code>bambud enroll</code> : le code à 8 caractères s'affiche dans ce terminal (il n'est jamais transmis à Claude). Saisis-le ici.</p>
<form method="post" action="/enroll" autocomplete="off"><input type="hidden" name="next" value="{{.Next}}">
<p><label>Code d'enregistrement<br><input type="text" name="code" required minlength="8" maxlength="12" autocomplete="off" autocapitalize="characters" spellcheck="false"></label></p>
<p><label>Nom de ce navigateur (facultatif)<br><input type="text" name="label" maxlength="80" autocomplete="off"></label></p>
<p><button type="submit">Enregistrer</button></p></form>{{template "foot"}}{{end}}
{{define "done"}}{{template "head" .}}<h1>{{.Title}}</h1><p class="ok">{{.Msg}}</p>{{if .Form}}<form method="post" action="/enroll/logout"><button type="submit" class="danger">Retirer ce navigateur</button></form>{{end}}{{template "foot"}}{{end}}`))

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
