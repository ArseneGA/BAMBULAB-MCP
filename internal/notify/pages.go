package notify

import (
	"crypto/rand"
	"encoding/base64"
	"html/template"
	"net/http"
	"strconv"
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
<p>La <strong>destination</strong> d'un canal (hôte, port et utilisateur SMTP, destinataires, URL) et ses <strong>secrets</strong> se saisissent ici seulement : Claude
(<code>configure_notifications</code>) peut seulement activer ou désactiver un canal existant et choisir ses évènements. Changer l'hôte, le port ou l'utilisateur SMTP efface le mot de passe enregistré.
Les adresses locales, privées ou de lien local sont refusées (sauf <code>BAMBU_NOTIFY_ALLOW_PRIVATE=1</code> côté serveur). Les secrets saisis ici :
ils vont dans le coffre chiffré local, ne sont jamais réaffichés, ni renvoyés à Claude, ni écrits dans les journaux.
Une variable d'environnement <code>BAMBU_NOTIFY_&lt;CANAL&gt;_&lt;CHAMP&gt;</code> (ou <code>…_FILE</code> désignant un fichier) l'emporte sur le coffre.</p>
{{if not .Channels}}<p class="warn">Aucun canal défini. Demande à Claude de créer un canal (configure_notifications), puis recharge cette page.</p>{{end}}
{{range .Channels}}<form method="post" autocomplete="off"><input type="hidden" name="nonce" value="{{$.Nonce}}"><input type="hidden" name="channel" value="{{.ID}}">
<h2>{{.Name}} <small>({{.Kind}}{{if not .Enabled}}, désactivé{{end}})</small></h2>
<p>{{if .Ready}}<span class="ok">Prêt.</span>{{else}}<span class="warn">Incomplet : {{range .Missing}}{{.}} ; {{end}}</span>{{end}} Évènements : {{if .Events}}{{range .Events}}{{.}} {{end}}{{else}}tous{{end}}</p>
{{range .Secrets}}<p><label>{{.Label}}{{if .Required}} *{{end}} — {{if .Set}}<em>enregistré ({{.Source}})</em>{{else}}<em>non défini</em>{{end}}<br>
<input type="password" name="secret_{{.Name}}" maxlength="2048" autocomplete="off" placeholder="{{if .Set}}laisser vide pour conserver{{else}}valeur secrète{{end}}"></label>
{{if .Set}}<label class="chk"><input type="checkbox" name="clear_{{.Name}}" value="yes"> effacer du coffre</label>{{end}}</p>{{end}}
<p><button type="submit" name="action" value="secrets">Enregistrer</button></p>
{{if eq .Kind "email"}}<p class="meta">SMTP : {{.Config.SMTPHost}}:{{.Config.SMTPPort}} ({{.Config.SMTPTLS}}), utilisateur « {{.Config.SMTPUser}} », de {{.Config.From}} vers {{range .Config.To}}{{.}} {{end}}</p>{{end}}
<p><button type="submit" name="action" value="delete_channel" class="danger">Supprimer ce canal</button></p></form>{{end}}
<hr><form method="post" autocomplete="off"><input type="hidden" name="nonce" value="{{.Nonce}}"><input type="hidden" name="action" value="save_channel">
<h2>Créer ou modifier un canal</h2>
<p><label>Nom (un nom existant met le canal à jour)<br><input name="name" maxlength="40" required></label></p>
<p><label>Type<br><select name="kind"><option value="ntfy">ntfy</option><option value="webhook">webhook</option><option value="email">email</option></select></label>
<label class="chk"><input type="checkbox" name="enabled" value="yes" checked> actif</label></p>
<p>Évènements (aucun coché = tous) :{{range .Events}} <label class="chk"><input type="checkbox" name="events" value="{{.}}"> {{.}}</label>{{end}}</p>
<p><label>ntfy : priorité (min, low, default, high, max)<br><input name="ntfy_priority" maxlength="10"></label></p>
<p><strong>E-mail</strong> :</p>
<p><label>Hôte SMTP<br><input name="smtp_host" maxlength="253"></label> <label>Port<br><input name="smtp_port" maxlength="5" size="6"></label>
<label>TLS<br><select name="smtp_tls"><option value="">auto</option><option value="starttls">starttls</option><option value="implicit">implicit</option></select></label></p>
<p><label>Utilisateur SMTP<br><input name="smtp_user" maxlength="200"></label></p>
<p><label>Expéditeur<br><input name="from" maxlength="200"></label></p>
<p><label>Destinataires (séparés par une virgule)<br><input name="to" maxlength="1000"></label></p>
<p><button type="submit">Enregistrer le canal</button></p></form>
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
	_ = pageTpl.Execute(w, map[string]any{"Channels": chs, "Nonce": s.newNonce(), "Msg": msg, "Err": errMsg, "Events": AllEvents})
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
	if r.PostFormValue("action") == "save_channel" {
		s.pageSaveChannel(w, r)
		return
	}
	ch, err := s.GetChannel(r.Context(), r.PostFormValue("channel"))
	if err != nil {
		s.render(w, r, http.StatusNotFound, "", "Canal introuvable.")
		return
	}
	if r.PostFormValue("action") == "delete_channel" {
		if err := s.DeleteChannel(r.Context(), ch.ID); err != nil {
			s.render(w, r, http.StatusInternalServerError, "", "Suppression impossible.")
			return
		}
		s.render(w, r, http.StatusOK, "Canal « "+ch.Name+" » supprimé.", "")
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

func (s *Service) pageSaveChannel(w http.ResponseWriter, r *http.Request) {
	port, _ := strconv.Atoi(strings.TrimSpace(r.PostFormValue("smtp_port")))
	var to []string
	for _, a := range strings.FieldsFunc(r.PostFormValue("to"), func(c rune) bool { return c == ',' || c == ';' || c == '\n' }) {
		if a = strings.TrimSpace(a); a != "" {
			to = append(to, a)
		}
	}
	enabled := strings.EqualFold(r.PostFormValue("enabled"), "yes")
	in := ChannelInput{Name: r.PostFormValue("name"), Kind: r.PostFormValue("kind"), Enabled: &enabled, Events: r.PostForm["events"],
		Config: ChannelConfig{SMTPHost: r.PostFormValue("smtp_host"), SMTPPort: port, SMTPUser: strings.TrimSpace(r.PostFormValue("smtp_user")), SMTPTLS: r.PostFormValue("smtp_tls"),
			From: strings.TrimSpace(r.PostFormValue("from")), To: to, Priority: strings.TrimSpace(r.PostFormValue("ntfy_priority"))}}
	if in.Kind != KindEmail { // les champs SMTP ne concernent que le courriel
		in.Config = ChannelConfig{Priority: in.Config.Priority}
	}
	if in.Kind != KindNtfy {
		in.Config.Priority = ""
	}
	ch, err := s.UpsertChannel(r.Context(), in)
	if err != nil {
		s.render(w, r, http.StatusBadRequest, "", "Canal refusé : "+strings.TrimPrefix(err.Error(), ErrInvalid.Error()+" : "))
		return
	}
	s.render(w, r, http.StatusOK, "Canal « "+ch.Name+" » enregistré. Saisis ses secrets ci-dessous si besoin.", "")
}
