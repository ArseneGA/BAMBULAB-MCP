package printing

import (
	"errors"
	"html/template"
	"net/http"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

// Pages HTML serveur (sans JavaScript) : saisie du code d'accès et confirmation de lancement.
// Ces deux pages sont le canal HUMAIN : leur contenu n'est jamais renvoyé au modèle.

var pageTpl = template.Must(template.New("p").Parse(`{{define "head"}}<!doctype html><html lang="fr"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer">
<title>{{.Title}}</title><style>
:root{color-scheme:light dark;--bg:#f6f5f2;--fg:#1d2024;--mut:#5b6068;--line:#cfcbc2;--acc:#1f4f8f;--warn:#9a3412}
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
.ok{border-left:4px solid var(--acc);padding:.5rem .8rem}</style></head><body><main>{{end}}
{{define "foot"}}</main></body></html>{{end}}
{{define "setup"}}{{template "head" .}}<h1>Code d'accès de l'imprimante</h1>
<dl><dt>Imprimante</dt><dd>{{.Name}}</dd><dt>Modèle</dt><dd>{{.Model}}</dd><dt>Adresse</dt><dd><code>{{.Host}}</code></dd><dt>Série</dt><dd><code>{{.Serial}}</code></dd>
<dt>Code enregistré</dt><dd>{{if .Set}}oui (sa valeur n'est jamais affichée){{else}}non{{end}}</dd></dl>
{{if .Done}}<p class="ok">Code d'accès enregistré dans le coffre local chiffré. Il n'est ni écrit en base, ni renvoyé à Claude, ni journalisé.</p>{{else}}
<p>Saisis le code d'accès LAN affiché sur l'écran de l'imprimante (Réglages &gt; Réseau). Il reste sur ce poste et n'est jamais transmis à Claude.</p>
<form method="post" autocomplete="off"><input type="hidden" name="nonce" value="{{.Nonce}}">
<p><label>Code d'accès<br><input type="password" name="access_code" required minlength="4" maxlength="64" autocomplete="off"></label></p>
<p><button type="submit">Enregistrer</button></p></form>{{end}}{{template "foot"}}{{end}}
{{define "confirm"}}{{template "head" .}}<h1>Lancer cette impression ?</h1>
{{if .Closed}}<p class="warn">{{.Closed}}</p>{{else}}
<dl><dt>Fichier</dt><dd><code>{{.V.Binding.File}}</code></dd>
<dt>Imprimante</dt><dd>{{.V.Binding.PrinterName}} ({{.V.Binding.PrinterModel}})</dd>
<dt>Empreinte</dt><dd><code>{{.V.Binding.SHA256}}</code></dd><dt>Plateau</dt><dd>{{.V.Binding.Plate}}</dd>
<dt>Matières</dt><dd>{{range .V.Binding.Materials}}filament {{.FilamentSlot}} &rarr; {{.Tray}} : {{.MaterialID}}<br>{{end}}</dd>
{{if .V.Binding.EstimatedMin}}<dt>Durée estimée</dt><dd>{{printf "%.0f" .V.Binding.EstimatedMin}} min (estimation du slicer){{if .V.Binding.WeightG}}, {{printf "%.1f" .V.Binding.WeightG}} g{{end}}</dd>{{end}}
<dt>Valable jusqu'à</dt><dd>{{.V.ExpiresAt.Format "15:04:05 (02/01/2006)"}}</dd></dl>
<p class="warn">L'imprimante ne peut pas savoir si le plateau est vide. Cette autorisation vaut pour <strong>cette impression seulement</strong> ; changer le fichier, le plateau, l'imprimante ou les matières l'annule.</p>
<form method="post"><input type="hidden" name="nonce" value="{{.Nonce}}">
<label class="chk"><input type="checkbox" name="plate_clear" value="yes" required> Le plateau est vide et dégagé, l'imprimante est surveillée.</label>
<button type="submit">Lancer l'impression</button></form>{{end}}{{template "foot"}}{{end}}
{{define "done"}}{{template "head" .}}<h1>{{.Title}}</h1><p class="ok">{{.Msg}}</p>{{template "foot"}}{{end}}`))

func pageHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Frame-Options", "DENY") // anti-clickjacking de la page de confirmation
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
}

func (s *Service) render(w http.ResponseWriter, status int, name string, data any) {
	pageHeaders(w)
	w.WriteHeader(status)
	if err := pageTpl.ExecuteTemplate(w, name, data); err != nil {
		s.log.Warn("page non rendue", "page", name, "err", err)
	}
}

// RegisterPages monte /setup/printer/{id} et /confirm/{id} sur mux.
func (s *Service) RegisterPages(mux *http.ServeMux) {
	mux.HandleFunc("GET /setup/printer/{id}", s.setupGet)
	mux.HandleFunc("POST /setup/printer/{id}", s.setupPost)
	mux.HandleFunc("GET /confirm/{id}", s.confirmGet)
	mux.HandleFunc("POST /confirm/{id}", s.confirmPost)
}

func (s *Service) setupData(r *http.Request, id string) (map[string]any, int) {
	rec, err := s.GetPrinterRecord(r.Context(), id)
	if err != nil {
		return map[string]any{"Title": "Imprimante introuvable", "Msg": "Cette imprimante n'existe pas."}, http.StatusNotFound
	}
	m, _ := s.GetPrinterModel(r.Context(), rec.ModelID)
	return map[string]any{"Title": "Code d'accès", "Name": rec.Name, "Model": m.Name, "Host": rec.Host, "Serial": rec.Serial, "Set": s.secrets.HasAccessCode(id)}, 0
}

func (s *Service) setupGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, st := s.setupData(r, id)
	if st != 0 {
		s.render(w, st, "done", d)
		return
	}
	d["Nonce"] = s.issueNonce("setup:" + id)
	s.render(w, http.StatusOK, "setup", d)
}

func (s *Service) setupPost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		s.render(w, http.StatusBadRequest, "done", map[string]any{"Title": "Requête invalide", "Msg": "Formulaire illisible."})
		return
	}
	d, st := s.setupData(r, id)
	if st != 0 {
		s.render(w, st, "done", d)
		return
	}
	if !s.useNonce(r.PostFormValue("nonce"), "setup:"+id) {
		s.render(w, http.StatusForbidden, "done", map[string]any{"Title": "Formulaire périmé", "Msg": "Recharge la page puis recommence."})
		return
	}
	if err := s.SetAccessCode(r.Context(), id, r.PostFormValue("access_code")); err != nil {
		d["Nonce"] = s.issueNonce("setup:" + id)
		d["Title"] = "Code refusé"
		s.render(w, http.StatusBadRequest, "setup", d)
		return
	}
	d["Set"], d["Done"] = true, true
	s.render(w, http.StatusOK, "setup", d)
}

func closedMsg(err error) string {
	switch {
	case errors.Is(err, contracts.ErrConfirmationUsed):
		return "Cette confirmation a déjà été utilisée : elle ne vaut qu'une fois."
	case errors.Is(err, contracts.ErrConfirmationExpired):
		return "Cette confirmation a expiré. Demande-en une nouvelle à Claude."
	case errors.Is(err, contracts.ErrConfirmationMismatch):
		return "Cette confirmation ne correspond plus à l'impression (fichier, imprimante, plateau ou matières modifiés, ou nouvelle confirmation émise)."
	case errors.Is(err, ErrNotFound):
		return "Confirmation introuvable."
	}
	return "Confirmation indisponible."
}

func (s *Service) confirmGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, _, err := s.confirmable(r.Context(), id); err != nil {
		s.render(w, http.StatusGone, "confirm", map[string]any{"Title": "Confirmation", "Closed": closedMsg(err)})
		return
	}
	v, err := s.GetConfirmation(r.Context(), id)
	if err != nil {
		s.render(w, http.StatusNotFound, "confirm", map[string]any{"Title": "Confirmation", "Closed": closedMsg(err)})
		return
	}
	if v.Approved {
		s.render(w, http.StatusOK, "done", map[string]any{"Title": "Déjà confirmée", "Msg": "Tu as déjà autorisé cette impression. Retourne dans Claude : il peut lancer."})
		return
	}
	s.render(w, http.StatusOK, "confirm", map[string]any{"Title": "Confirmation de lancement", "V": v, "Nonce": s.issueNonce(id)})
}

func (s *Service) confirmPost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		s.render(w, http.StatusBadRequest, "done", map[string]any{"Title": "Requête invalide", "Msg": "Formulaire illisible."})
		return
	}
	if !s.useNonce(r.PostFormValue("nonce"), id) {
		s.render(w, http.StatusForbidden, "done", map[string]any{"Title": "Formulaire périmé", "Msg": "Recharge la page de confirmation puis recommence."})
		return
	}
	plate := strings.EqualFold(r.PostFormValue("plate_clear"), "yes")
	if _, err := s.Approve(r.Context(), id, plate, "web"); err != nil {
		st := http.StatusGone
		msg := closedMsg(err)
		if errors.Is(err, ErrNeedsApproval) {
			st, msg = http.StatusBadRequest, "Coche l'attestation « plateau vide » pour autoriser."
		}
		s.render(w, st, "done", map[string]any{"Title": "Non confirmée", "Msg": msg})
		return
	}
	s.render(w, http.StatusOK, "done", map[string]any{"Title": "Lancement autorisé", "Msg": "Autorisation enregistrée pour cette impression uniquement. Retourne dans Claude et dis-lui que c'est fait : il lancera l'impression."})
}
