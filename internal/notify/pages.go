package notify

import (
	"crypto/rand"
	"encoding/base64"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
)

// Page humaine /setup/notifications : saisie des secrets des canaux (URL ntfy, URL et clé HMAC du webhook, mot de passe
// SMTP). Navigateur enregistré + nonce à usage unique, comme /setup/printer/{id}. Les valeurs saisies vont au coffre
// chiffré et ne sont jamais réaffichées ni renvoyées à Claude.

var pageTpl = template.Must(template.New("n").Parse(`<!doctype html><html lang="fr"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer">
<title>Notifications : secrets</title><style>` + humanauth.Style + `</style></head><body><main>
<h1>Notifications : secrets des canaux</h1>
{{if .Msg}}<p class="ok">{{.Msg}}</p>{{end}}{{if .Err}}<p class="warn">{{.Err}}</p>{{end}}
<p>Les canaux (nom, type, évènements, destinataires) se règlent avec Claude (<code>configure_notifications</code>). Les <strong>secrets</strong> se saisissent ici seulement :
ils vont dans le coffre chiffré local, ne sont jamais réaffichés, ni renvoyés à Claude, ni écrits dans les journaux.
Une variable d'environnement <code>BAMBU_NOTIFY_&lt;CANAL&gt;_&lt;CHAMP&gt;</code> (ou <code>…_FILE</code> désignant un fichier) l'emporte sur le coffre.</p>
{{if not .Channels}}<p class="warn">Aucun canal défini. Demande à Claude de créer un canal (configure_notifications), puis recharge cette page.</p>{{end}}
{{range .Channels}}<form method="post" autocomplete="off"><input type="hidden" name="nonce" value="{{$.Nonce}}"><input type="hidden" name="channel" value="{{.ID}}">
<h2>{{.Name}} <small>({{.Kind}}{{if not .Enabled}}, désactivé{{end}})</small></h2>
<p>{{if .Ready}}<span class="ok">Prêt.</span>{{else}}<span class="warn">Incomplet : {{range .Missing}}{{.}} ; {{end}}</span>{{end}} Évènements : {{if .Events}}{{range .Events}}{{.}} {{end}}{{else}}tous{{end}}</p>
{{range .Secrets}}<p><label>{{.Label}}{{if .Required}} *{{end}} — {{if .Set}}<em>enregistré ({{.Source}})</em>{{else}}<em>non défini</em>{{end}}<br>
<input type="password" name="secret_{{.Name}}" maxlength="2048" autocomplete="off" placeholder="{{if .Set}}laisser vide pour conserver{{else}}valeur secrète{{end}}"></label>
{{if .Set}}<label class="chk"><input type="checkbox" name="clear_{{.Name}}" value="yes"> effacer du coffre</label>{{end}}</p>{{end}}
<p><button type="submit">Enregistrer</button></p></form>{{end}}
</main></body></html>`))

func (s *Service) newNonce() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	n := base64.RawURLEncoding.EncodeToString(b)
	s.mu.Lock()
	for k, t := range s.pages {
		if s.now().After(t) {
			delete(s.pages, k)
		}
	}
	s.pages[n] = s.now().Add(15 * time.Minute)
	s.mu.Unlock()
	return n
}

func (s *Service) useNonce(n string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.pages[n]
	delete(s.pages, n)
	return ok && s.now().Before(t)
}

// RegisterPages monte GET/POST /setup/notifications derrière require (humanauth.Require).
func (s *Service) RegisterPages(mux *http.ServeMux, require func(http.Handler) http.Handler) {
	if require == nil {
		require = func(h http.Handler) http.Handler { return h }
	}
	mux.Handle("GET /setup/notifications", require(http.HandlerFunc(s.pageGet)))
	mux.Handle("POST /setup/notifications", require(http.HandlerFunc(s.pagePost)))
}

func (s *Service) render(w http.ResponseWriter, r *http.Request, status int, msg, errMsg string) {
	chs, _ := s.ListChannels(r.Context())
	humanauth.PageHeaders(w)
	w.WriteHeader(status)
	_ = pageTpl.Execute(w, map[string]any{"Channels": chs, "Nonce": s.newNonce(), "Msg": msg, "Err": errMsg})
}

func (s *Service) pageGet(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "", "")
}

func (s *Service) pagePost(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	if err := r.ParseForm(); err != nil {
		s.render(w, r, http.StatusBadRequest, "", "Formulaire illisible.")
		return
	}
	if !s.useNonce(r.PostFormValue("nonce")) {
		s.render(w, r, http.StatusForbidden, "", "Formulaire périmé : recharge la page puis recommence.")
		return
	}
	ch, err := s.GetChannel(r.Context(), r.PostFormValue("channel"))
	if err != nil {
		s.render(w, r, http.StatusNotFound, "", "Canal introuvable.")
		return
	}
	n := 0
	for _, f := range secretFields[ch.Kind] {
		if strings.EqualFold(r.PostFormValue("clear_"+f.Name), "yes") {
			_ = s.ClearSecret(r.Context(), ch.ID, f.Name)
			n++
			continue
		}
		if v := r.PostFormValue("secret_" + f.Name); strings.TrimSpace(v) != "" {
			if err := s.SetSecret(r.Context(), ch.ID, f.Name, v); err != nil {
				s.render(w, r, http.StatusBadRequest, "", "Valeur refusée pour « "+f.Name+" » : "+strings.TrimPrefix(err.Error(), ErrInvalid.Error()+" : "))
				return
			}
			n++
		}
	}
	msg := "Aucune modification."
	if n > 0 {
		msg = "Secrets du canal « " + ch.Name + " » mis à jour (coffre chiffré). Demande à Claude un test_notification."
	}
	s.render(w, r, http.StatusOK, msg, "")
}
