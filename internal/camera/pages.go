package camera

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Page humaine /setup/camera/{id} : épinglage du certificat présenté par la CAMÉRA (port 6000). Par défaut la caméra réutilise
// l'empreinte déjà épinglée pour MQTT (/setup/printer/{id}) ; si la vraie P1S présente un autre certificat sur le port 6000,
// l'humain compare et épingle ici l'empreinte de la caméra. Navigateur enregistré + nonce lié à l'empreinte affichée.

type nonceEntry struct {
	printerID, fingerprint string
	expires                time.Time
}

var pageTpl = template.Must(template.New("c").Parse(humanauth.Layout("Caméra : certificat", "", "") + `{{template "head" .}}
<h1>Caméra de l'imprimante : certificat</h1>
{{if .Msg}}<p class="ok">{{.Msg}}</p>{{end}}{{if .Err}}<p class="warn">{{.Err}}</p>{{end}}
<dl><dt>Imprimante</dt><dd>{{.Name}} (<code>{{.Serial}}</code>)</dd>
<dt>Empreinte épinglée pour MQTT</dt><dd>{{if .Printer}}<code>{{.Printer}}</code>{{else}}aucune — valider d'abord /setup/printer{{end}}</dd>
<dt>Empreinte épinglée pour la caméra</dt><dd>{{if .Cam}}<code>{{.Cam}}</code>{{else}}la même que MQTT{{end}}</dd>
{{if .Fingerprint}}<dt>Empreinte présentée par la caméra (port {{.Port}})</dt><dd><code>{{.Fingerprint}}</code></dd>{{end}}</dl>
{{if .Same}}<p class="ok">La caméra présente le même certificat que le broker MQTT : rien à faire.</p>{{end}}
{{if .CanPin}}<p class="warn">La caméra présente un certificat <strong>différent</strong> de celui de MQTT. Ne l'épingle que si tu sais que c'est bien ton imprimante (même réseau, tu viens de l'allumer).</p>
<form method="post"><input type="hidden" name="nonce" value="{{.Nonce}}"><p><button type="submit">Épingler cette empreinte pour la caméra</button></p></form>{{end}}
{{template "foot"}}`))

func (s *Service) newNonce(printerID, fp string) string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	n := base64.RawURLEncoding.EncodeToString(b)
	s.mu.Lock()
	for k, v := range s.nonce {
		if s.now().After(v.expires) {
			delete(s.nonce, k)
		}
	}
	s.nonce[n] = nonceEntry{printerID: printerID, fingerprint: fp, expires: s.now().Add(15 * time.Minute)}
	s.mu.Unlock()
	return n
}

// RegisterPages monte GET/POST /setup/camera/{id} derrière require (humanauth.Require).
func (s *Service) RegisterPages(mux *http.ServeMux, require func(http.Handler) http.Handler) {
	if require == nil {
		require = func(h http.Handler) http.Handler { return h }
	}
	mux.Handle("GET /setup/camera/{id}", require(http.HandlerFunc(s.setupGet)))
	mux.Handle("POST /setup/camera/{id}", require(http.HandlerFunc(s.setupPost)))
}

func (s *Service) setupView(ctx context.Context, id string) (map[string]any, string, int) {
	t, err := s.c.Backend.Target(ctx, id)
	if err != nil {
		return map[string]any{"Err": "Imprimante introuvable."}, "", http.StatusNotFound
	}
	d := map[string]any{"Name": t.Name, "Serial": t.Serial, "Printer": t.PinnedSHA256, "Cam": s.cameraPin(ctx, t), "Port": s.port()}
	if t.PinnedSHA256 == s.cameraPin(ctx, t) {
		d["Cam"] = ""
	}
	if _, err := Supported(t); err != nil {
		d["Err"] = err.Error()
		return d, "", 0
	}
	fctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	ci, err := printer.FetchCertInfo(fctx, net.JoinHostPort(t.Host, strconv.Itoa(s.port())))
	if err != nil {
		d["Err"] = "Le certificat de la caméra n'a pas pu être lu (" + err.Error() + ") : imprimante éteinte, hors réseau, ou LAN liveview désactivé."
		return d, "", 0
	}
	d["Fingerprint"] = ci.SHA256
	d["Same"] = strings.EqualFold(ci.SHA256, t.PinnedSHA256)
	d["CanPin"] = !strings.EqualFold(ci.SHA256, s.cameraPin(ctx, t))
	if d["Same"] == true {
		d["CanPin"] = false
	}
	return d, ci.SHA256, 0
}

func (s *Service) renderSetup(w http.ResponseWriter, status int, d map[string]any) {
	humanauth.PageHeaders(w)
	w.WriteHeader(status)
	_ = pageTpl.Execute(w, d)
}

func (s *Service) setupGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, fp, st := s.setupView(r.Context(), id)
	if st != 0 {
		s.renderSetup(w, st, d)
		return
	}
	if d["CanPin"] == true {
		d["Nonce"] = s.newNonce(id, fp)
	}
	s.renderSetup(w, http.StatusOK, d)
}

func (s *Service) setupPost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		s.renderSetup(w, http.StatusBadRequest, map[string]any{"Err": "Formulaire illisible."})
		return
	}
	n := r.PostFormValue("nonce")
	s.mu.Lock()
	e, ok := s.nonce[n]
	delete(s.nonce, n)
	s.mu.Unlock()
	if !ok || e.printerID != id || s.now().After(e.expires) {
		s.renderSetup(w, http.StatusForbidden, map[string]any{"Err": "Formulaire périmé : recharge la page puis recommence."})
		return
	}
	// L'empreinte épinglée est EXACTEMENT celle qui a été affichée à l'humain avec ce nonce.
	doc, _ := json.Marshal(map[string]string{"sha256": e.fingerprint})
	if _, err := s.c.Store.DB().ExecContext(r.Context(), `INSERT INTO camera_settings(key,doc,updated_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET doc=excluded.doc, updated_at=excluded.updated_at`, "pin:"+id, string(doc), store.FormatTime(s.now())); err != nil {
		s.renderSetup(w, http.StatusInternalServerError, map[string]any{"Err": "Enregistrement impossible."})
		return
	}
	_ = s.c.Store.Audit(r.Context(), "human:web", "camera.cert_pinned", "printer", id, map[string]any{"sha256": e.fingerprint})
	d, _, _ := s.setupView(r.Context(), id)
	d["Msg"] = "Empreinte de la caméra épinglée."
	s.renderSetup(w, http.StatusOK, d)
}
