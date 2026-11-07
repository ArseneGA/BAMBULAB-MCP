package humanauth

import (
	"errors"
	"html/template"
	"net/http"
	"strings"
)

// Page humaine /setup/launch-code : définir, changer, révoquer le code de lancement et lever un verrouillage.
// Elle est derrière Require (navigateur enregistré + Sec-Fetch + nonce à usage unique). Le code saisi n'est jamais
// renvoyé dans la page, jamais journalisé, jamais placé dans une URL (POST uniquement).

var launchTpl = template.Must(template.New("l").Parse(`<!doctype html><html lang="fr"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer">
<title>Code de lancement</title><style>` + Style + `</style></head><body><main>
<h1>Code de lancement personnel</h1>
{{if .Msg}}<p class="ok" role="status">{{.Msg}}</p>{{end}}{{if .Err}}<p class="warn" role="alert">{{.Err}}</p>{{end}}
<p>Ce code (6 à 8 chiffres) te permet de lancer ou d'arrêter une impression directement dans la vue de Claude, sans ouvrir le navigateur. Le lien de confirmation du navigateur reste toujours disponible.</p>
<p class="warn"><strong>Ne tape jamais ce code dans la conversation avec Claude</strong> et ne le donne à personne : il ne se saisit que dans le champ prévu de la vue de confirmation. Il est stocké haché (argon2id), jamais affiché, jamais journalisé.</p>
<dl><dt>Code défini</dt><dd>{{if .Set}}oui{{else}}non (la vue affiche alors le lien du navigateur){{end}}</dd>
{{if .Set}}<dt>État</dt><dd>{{if .Locked}}<strong>VERROUILLÉ</strong> après {{.MaxFailures}} échecs{{else}}actif, {{.Left}} essai(s) avant verrouillage{{end}}</dd>{{end}}</dl>
{{if .Locked}}<form method="post"><input type="hidden" name="nonce" value="{{.Nonce}}"><input type="hidden" name="action" value="unlock">
<p><button type="submit">Déverrouiller (garder le même code)</button></p></form>{{end}}
<h2>{{if .Set}}Changer le code{{else}}Définir un code{{end}}</h2>
<form method="post" autocomplete="off"><input type="hidden" name="nonce" value="{{.Nonce}}"><input type="hidden" name="action" value="set">
<p><label>Nouveau code (6 à 8 chiffres)<br><input type="password" name="code" required minlength="6" maxlength="8" pattern="[0-9]{6,8}" inputmode="numeric" autocomplete="off" spellcheck="false"></label></p>
<p><label>Confirmer le code<br><input type="password" name="code2" required minlength="6" maxlength="8" pattern="[0-9]{6,8}" inputmode="numeric" autocomplete="off" spellcheck="false"></label></p>
<p><button type="submit">Enregistrer le code</button></p></form>
{{if .Set}}<h2>Révoquer</h2><form method="post"><input type="hidden" name="nonce" value="{{.Nonce}}"><input type="hidden" name="action" value="revoke">
<p>Sans code, la vue de Claude n'affiche que le lien du navigateur.</p><p><button type="submit">Révoquer le code</button></p></form>{{end}}
<p class="dim">En cas d'oubli : <code>bambud launch-code --reset</code> sur le poste du service.</p>
</main></body></html>`))

// RegisterLaunchCodePages monte GET/POST /setup/launch-code derrière Require.
func (s *Service) RegisterLaunchCodePages(mux *http.ServeMux) {
	mux.Handle("GET /setup/launch-code", s.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.renderLaunchPage(w, r, http.StatusOK, "", "")
	})))
	mux.Handle("POST /setup/launch-code", s.Require(http.HandlerFunc(s.launchPost)))
}

func (s *Service) renderLaunchPage(w http.ResponseWriter, r *http.Request, status int, msg, errMsg string) {
	lc := LaunchCodesFor(s.st)
	st, err := lc.Status(r.Context())
	if err != nil && errMsg == "" {
		errMsg = "État du code illisible."
	}
	PageHeaders(w)
	w.WriteHeader(status)
	_ = launchTpl.Execute(w, map[string]any{"Msg": msg, "Err": errMsg, "Set": st.Set, "Locked": st.Locked, "Left": st.AttemptsLeft,
		"MaxFailures": LaunchMaxFailures, "Nonce": lc.issueNonce()})
}

func (s *Service) launchPost(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		s.renderLaunchPage(w, r, http.StatusBadRequest, "", "Formulaire illisible.")
		return
	}
	lc := LaunchCodesFor(s.st)
	if !lc.useNonce(r.PostFormValue("nonce")) {
		s.renderLaunchPage(w, r, http.StatusForbidden, "", "Formulaire périmé : recommence.")
		return
	}
	actor := "human:web"
	if sess, err := s.SessionFromRequest(r); err == nil {
		actor = "human:web:" + sess.ID
	}
	switch strings.TrimSpace(r.PostFormValue("action")) {
	case "set":
		code, code2 := r.PostFormValue("code"), r.PostFormValue("code2")
		if code != code2 {
			s.renderLaunchPage(w, r, http.StatusBadRequest, "", "Les deux saisies ne sont pas identiques.")
			return
		}
		if err := lc.Set(r.Context(), code, actor); err != nil {
			if errors.Is(err, ErrLaunchCodeFormat) || errors.Is(err, ErrLaunchCodeWeak) {
				s.renderLaunchPage(w, r, http.StatusBadRequest, "", err.Error()+".")
				return
			}
			s.renderLaunchPage(w, r, http.StatusInternalServerError, "", "Le code n'a pas pu être enregistré.")
			return
		}
		s.renderLaunchPage(w, r, http.StatusOK, "Code enregistré (et verrou levé). Ne le tape jamais dans la conversation.", "")
	case "revoke":
		if err := lc.Revoke(r.Context(), actor); err != nil {
			s.renderLaunchPage(w, r, http.StatusInternalServerError, "", "Le code n'a pas pu être révoqué.")
			return
		}
		s.renderLaunchPage(w, r, http.StatusOK, "Code révoqué : la vue de Claude n'affiche plus que le lien du navigateur.", "")
	case "unlock":
		if err := lc.Unlock(r.Context(), actor); err != nil {
			s.renderLaunchPage(w, r, http.StatusInternalServerError, "", "Le déverrouillage a échoué.")
			return
		}
		s.renderLaunchPage(w, r, http.StatusOK, "Verrou levé : le code actuel est de nouveau utilisable.", "")
	default:
		s.renderLaunchPage(w, r, http.StatusBadRequest, "", "Action inconnue.")
	}
}
