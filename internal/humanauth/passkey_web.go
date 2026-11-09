package humanauth

import (
	_ "embed"
	"errors"
	"html/template"
	"net/http"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/localtime"
)

// PasskeyScriptPath : chemin du script statique des cérémonies WebAuthn (même origine, CSP script-src 'self').
const PasskeyScriptPath = "/setup/passkeys/passkey.js"

//go:embed passkey.js
var passkeyJS []byte

// Page humaine /setup/passkeys : enregistrer, lister, révoquer des passkeys, et régler « exiger une passkey ».
// Derrière Require (navigateur enregistré + Sec-Fetch de navigation + nonce à usage unique lié à CE navigateur). La
// cérémonie WebAuthn est jouée par le script statique ; le résultat revient par un formulaire classique (navigation).

var passkeyTpl = template.Must(template.New("pk").Funcs(template.FuncMap{"lt": localtime.Page}).Parse(Layout("Passkeys", "", "") + `{{template "head" .}}
<h1>Passkeys</h1>
<p id="pk-msg" role="status" {{if or .Msg .Err}}class="{{if .Err}}warn{{else}}ok{{end}}"{{else}}hidden{{end}}>{{if .Err}}{{.Err}}{{else}}{{.Msg}}{{end}}</p>
<p>Une passkey permet d'approuver un lancement ou un arrêt avec ton empreinte, ton visage, le code de ton appareil ou une clé de sécurité, <strong>sans code à taper</strong>. Elle est liée à ce site : la clé privée ne quitte jamais ton appareil, bambud ne garde que la clé publique. <strong>Elle est facultative</strong> : le bouton, le code de lancement et le lien du navigateur continuent de marcher tant que tu n'exiges pas la passkey.</p>
{{if .EnvErr}}<p class="warn" role="alert">{{.EnvErr}}</p>{{else}}<dl><dt>Site (RP ID)</dt><dd><code>{{.Env.RPID}}</code></dd><dt>Origine</dt><dd><code>{{.Env.Origin}}</code></dd></dl>{{end}}
<h2>Passkeys enregistrées</h2>
{{if .Keys}}<div class="tw"><table><thead><tr><th>Nom</th><th>Créée</th><th>Dernier usage</th><th></th></tr></thead><tbody>
{{range .Keys}}<tr{{if .Revoked}} class="revoked"{{end}}><td>{{.Label}}{{if .Synced}} <small>(synchronisée)</small>{{end}}<br><small><code>{{.ID}}</code> · <code>{{.RPID}}</code></small></td><td>{{lt .CreatedAt}}</td><td>{{if .LastUsedAt.IsZero}}jamais{{else}}{{lt .LastUsedAt}}{{end}}</td>
<td>{{if .Revoked}}révoquée{{else}}<form method="post"><input type="hidden" name="nonce" value="{{$.Nonce}}"><input type="hidden" name="action" value="revoke"><input type="hidden" name="id" value="{{.ID}}"><button type="submit" class="danger">Révoquer</button></form>{{end}}</td></tr>{{end}}</tbody></table></div>
{{else}}<p>Aucune passkey enregistrée.</p>{{end}}
{{if .Options}}<h2>Enregistrer une passkey</h2>
<form method="post" autocomplete="off" data-pk-mode="create" data-pk-options="{{.Options}}"><input type="hidden" name="nonce" value="{{.Nonce}}"><input type="hidden" name="action" value="register">
<p><label>Nom de cette passkey (ex. « MacBook », « clé jaune »)<br><input type="text" name="label" maxlength="60" autocomplete="off" placeholder="Passkey"></label></p>
<p><button type="button" data-pk-go hidden>Enregistrer une passkey</button></p>
<noscript><p class="warn">JavaScript est nécessaire pour enregistrer une passkey.</p></noscript></form>{{end}}
<h2>Exiger une passkey</h2>
<p>Par défaut (<strong>désactivé</strong>), tu peux approuver avec le bouton, le code de lancement ou une passkey. Si tu <strong>exiges</strong> la passkey, le bouton simple et le code de lancement deviennent indisponibles : seule une passkey approuve un lancement ou un arrêt.</p>
<dl><dt>État</dt><dd>{{if .Required}}<strong>passkey exigée</strong>{{else}}passkey facultative (défaut){{end}}</dd></dl>
{{if .Required}}<form method="post"><input type="hidden" name="nonce" value="{{.Nonce}}"><input type="hidden" name="action" value="require_off"><p><button type="submit">Ne plus exiger la passkey</button></p></form>
{{else}}{{if .Keys}}<form method="post"><input type="hidden" name="nonce" value="{{.Nonce}}"><input type="hidden" name="action" value="require_on"><p><button type="submit">Exiger une passkey</button></p></form>
{{else}}<p><small>Enregistre d'abord une passkey pour pouvoir l'exiger.</small></p>{{end}}{{end}}
<p><small>Hors secours : <code>bambud passkey --require=off</code> sur le poste du service lève l'exigence, <code>bambud passkey --list</code> / <code>--revoke ID</code> gère les passkeys.</small></p>
<script src="` + PasskeyScriptPath + `" defer></script>
{{template "foot"}}`))

// RegisterPasskeyPages monte GET/POST /setup/passkeys (derrière Require) et le script statique.
func (s *Service) RegisterPasskeyPages(mux *http.ServeMux) {
	mux.HandleFunc("GET "+PasskeyScriptPath, func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "text/javascript; charset=utf-8")
		h.Set("Cache-Control", "no-cache")
		h.Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(passkeyJS)
	})
	mux.Handle("GET /setup/passkeys", s.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.renderPasskeyPage(w, r, http.StatusOK, "", "")
	})))
	mux.Handle("POST /setup/passkeys", s.Require(http.HandlerFunc(s.passkeyPost)))
}

func (s *Service) renderPasskeyPage(w http.ResponseWriter, r *http.Request, status int, msg, errMsg string) {
	pk := PasskeysFor(s.st)
	ctx := r.Context()
	browser := BrowserKey(r)
	d := map[string]any{"Msg": msg, "Err": errMsg, "Nonce": pk.IssueNonce(browser), "Required": pk.Required(ctx)}
	keys, err := pk.List(ctx)
	if err != nil && errMsg == "" {
		d["Err"] = "Passkeys illisibles."
	}
	d["Keys"] = keys
	if env, eerr := pk.Env(r); eerr != nil {
		d["EnvErr"] = eerr.Error()
	} else {
		d["Env"] = env
		if opts, oerr := pk.BeginRegistration(ctx, r, browser); oerr == nil {
			d["Options"] = string(opts)
		} else if errors.Is(oerr, ErrPasskeyLimit) && d["Err"] == "" {
			d["Err"] = oerr.Error() + "."
		}
	}
	PageHeadersWith(w, false, true)
	w.WriteHeader(status)
	_ = passkeyTpl.Execute(w, d)
}

func (s *Service) passkeyPost(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, PasskeyMaxBody+4096)
	if err := r.ParseForm(); err != nil {
		s.renderPasskeyPage(w, r, http.StatusBadRequest, "", "Formulaire illisible (trop gros ?).")
		return
	}
	pk := PasskeysFor(s.st)
	browser := BrowserKey(r)
	if !pk.UseNonce(r.PostFormValue("nonce"), browser) {
		s.renderPasskeyPage(w, r, http.StatusForbidden, "", "Formulaire périmé : recommence.")
		return
	}
	actor := "human:web"
	if sess, err := s.SessionFromRequest(r); err == nil {
		actor = "human:web:" + sess.ID
	}
	ctx := r.Context()
	switch strings.TrimSpace(r.PostFormValue("action")) {
	case "register":
		cred := r.PostFormValue("credential")
		if cred == "" || len(cred) > PasskeyMaxBody {
			s.renderPasskeyPage(w, r, http.StatusBadRequest, "", "Aucune passkey reçue : réessaie (JavaScript requis).")
			return
		}
		info, err := pk.FinishRegistration(ctx, r, browser, r.PostFormValue("label"), actor, []byte(cred))
		if err != nil {
			s.renderPasskeyPage(w, r, http.StatusBadRequest, "", "Passkey non enregistrée : "+err.Error()+".")
			return
		}
		s.renderPasskeyPage(w, r, http.StatusOK, "Passkey « "+info.Label+" » enregistrée. Tu peux maintenant approuver un lancement ou un arrêt avec elle.", "")
	case "revoke":
		id, err := pk.Revoke(ctx, r.PostFormValue("id"), actor)
		if err != nil {
			s.renderPasskeyPage(w, r, http.StatusBadRequest, "", "Révocation refusée : "+err.Error()+".")
			return
		}
		s.renderPasskeyPage(w, r, http.StatusOK, "Passkey "+id+" révoquée.", "")
	case "require_on":
		if err := pk.SetRequired(ctx, true, actor); err != nil {
			s.renderPasskeyPage(w, r, http.StatusBadRequest, "", "Impossible d'exiger la passkey : "+err.Error()+".")
			return
		}
		s.renderPasskeyPage(w, r, http.StatusOK, "Passkey exigée : le bouton simple et le code de lancement sont désormais indisponibles.", "")
	case "require_off":
		if err := pk.SetRequired(ctx, false, actor); err != nil {
			s.renderPasskeyPage(w, r, http.StatusInternalServerError, "", "Réglage non enregistré.")
			return
		}
		s.renderPasskeyPage(w, r, http.StatusOK, "Passkey facultative de nouveau : le bouton et le code de lancement refonctionnent.", "")
	default:
		s.renderPasskeyPage(w, r, http.StatusBadRequest, "", "Action inconnue.")
	}
}
