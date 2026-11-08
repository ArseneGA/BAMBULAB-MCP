package printing

import (
	"context"
	"errors"
	"html/template"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/localtime"
)

// Pages HTML serveur (sans JavaScript) : saisie du code d'accès (avec épinglage du certificat),
// confirmation de lancement et validation d'arrêt. Ce sont les canaux HUMAINS : leur contenu n'est
// jamais renvoyé au modèle, et elles exigent un navigateur enregistré (humanauth.Require).

// lt formate une heure dans le fuseau de l'utilisateur (BAMBU_TIMEZONE) : « 21:03:26 (08/11/2026) », jamais en UTC.
var pageTpl = template.Must(template.New("p").Funcs(template.FuncMap{"lt": localtime.Page}).Parse(`{{define "head"}}<!doctype html><html lang="fr"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer">
<title>{{.Title}}</title><style>` + humanauth.Style + `</style></head><body><main>{{end}}
{{define "foot"}}</main></body></html>{{end}}
{{define "setup"}}{{template "head" .}}<h1>Imprimante : certificat et code d'accès</h1>
<dl><dt>Imprimante</dt><dd>{{.Name}}</dd><dt>Modèle</dt><dd>{{.Model}}</dd><dt>Adresse</dt><dd><code>{{.Host}}</code></dd>
<dt>Numéro de série attendu</dt><dd><code>{{.Serial}}</code></dd>
<dt>Code enregistré</dt><dd>{{if .Set}}oui (sa valeur n'est jamais affichée){{else}}non{{end}}</dd>
<dt>Empreinte épinglée</dt><dd>{{if .Pinned}}<code>{{.Pinned}}</code>{{else}}aucune{{end}}</dd></dl>
{{if .Done}}<p class="ok">Empreinte du certificat épinglée et code d'accès enregistré dans le coffre local chiffré. Il n'est ni écrit en base, ni renvoyé à Claude, ni journalisé.</p>{{else}}
{{if .FetchErr}}<p class="warn">Le certificat de l'imprimante n'a pas pu être lu ({{.FetchErr}}). {{if .Pinned}}Tu peux quand même mettre à jour le code ; l'empreinte déjà épinglée est conservée.{{else}}Allume l'imprimante, vérifie qu'elle est sur le réseau local, puis recharge cette page : sans empreinte validée, aucune connexion n'est jamais tentée.{{end}}</p>
{{else}}<dl><dt>Empreinte SHA-256 présentée</dt><dd><code>{{.Fingerprint}}</code></dd>
<dt>Sujet du certificat (CN)</dt><dd><code>{{.CertCN}}</code></dd></dl>
{{if .CNMismatch}}<p class="warn"><strong>Attention : le certificat ne porte pas le numéro de série attendu.</strong> Ce n'est peut-être pas votre imprimante (ou un autre équipement répond à cette adresse). Ne continue que si tu sais pourquoi.</p>{{end}}
{{if .Changed}}<p class="warn"><strong>Le certificat a changé</strong> depuis l'épinglage précédent. Valider ci-dessous remplace l'ancienne empreinte.</p>{{end}}
<p>Compare cette empreinte et ce numéro de série à ceux de ton imprimante (l'écran de l'imprimante affiche son numéro de série ; Réglages &gt; Réseau le code d'accès). En saisissant le code d'accès, tu confirmes que c'est bien ton imprimante et tu épingles cette empreinte.</p>{{end}}
{{if or .Fingerprint .Pinned}}<form method="post" autocomplete="off"><input type="hidden" name="nonce" value="{{.Nonce}}">
<p><label>Code d'accès LAN<br><input type="password" name="access_code" required minlength="4" maxlength="64" autocomplete="off"></label></p>
<p><button type="submit">Épingler le certificat et enregistrer</button></p></form>{{end}}{{end}}{{template "foot"}}{{end}}
{{define "confirm"}}{{template "head" .}}<h1>Lancer cette impression ?</h1>
{{if .Closed}}<p class="warn">{{.Closed}}</p>{{else}}
<dl><dt>Fichier</dt><dd><code>{{.V.Binding.File}}</code></dd>
<dt>Imprimante</dt><dd>{{.V.Binding.PrinterName}} ({{.V.Binding.PrinterModel}})</dd>
<dt>Empreinte</dt><dd><code>{{.V.Binding.SHA256}}</code></dd><dt>Plateau</dt><dd>{{.V.Binding.Plate}}</dd>
<dt>Matières</dt><dd>{{range .V.Binding.Materials}}filament {{.FilamentSlot}} &rarr; {{.Tray}} : {{.MaterialID}}<br>{{end}}</dd>
{{if .V.Binding.EstimatedMin}}<dt>Durée estimée</dt><dd>{{printf "%.0f" .V.Binding.EstimatedMin}} min (estimation du slicer){{if .V.Binding.WeightG}}, {{printf "%.1f" .V.Binding.WeightG}} g{{end}}</dd>{{end}}
{{with .V.Safety}}<dt>Températures<br><small>lues dans le G-code</small></dt><dd>{{if .Analysed}}buse : démarrage jusqu'à {{printf "%.0f" .NozzleStartMaxC}} °C, 1re couche {{printf "%.0f" .NozzleFirstLayerC}} °C, impression {{printf "%.0f" .NozzlePrintMinC}} à {{printf "%.0f" .NozzlePrintMaxC}} °C<br>plateau : 1re couche {{printf "%.0f" .BedFirstLayerC}} °C (de {{printf "%.0f" .BedMinC}} à {{printf "%.0f" .BedMaxC}} °C){{if .ChamberMaxC}}<br>chambre : jusqu'à {{printf "%.0f" .ChamberMaxC}} °C{{end}}{{else}}<span class="bad">non lues : G-code non analysé</span>{{end}}</dd>
{{if .Materials}}<dt>Matières lues dans le G-code</dt><dd>{{range .Materials}}{{.}}<br>{{end}}</dd>{{end}}{{end}}
<dt>Mise à niveau du plateau</dt><dd>{{if .V.Binding.BedLeveling}}oui (automatique au démarrage){{else}}<strong>non</strong> (désactivée pour cette impression){{end}}</dd>
<dt>Calibration du débit</dt><dd>{{if .V.Binding.FlowCali}}oui (automatique au démarrage){{else}}<strong>non</strong> (désactivée pour cette impression){{end}}</dd>
<dt>Calibration des vibrations</dt><dd>{{if .V.Binding.VibrationCali}}oui (automatique au démarrage){{else}}<strong>non</strong> (désactivée pour cette impression){{end}}</dd>
<dt>Timelapse</dt><dd>{{if .V.Binding.Timelapse}}<strong>oui</strong> (enregistré pendant l'impression){{else}}non{{end}}</dd>
<dt>Valable jusqu'à</dt><dd>{{lt .V.ExpiresAt}}<br><small>fuseau : {{.TZ}}</small></dd></dl>
{{with .V.Safety}}<style>.bad{color:#c00000;font-weight:600}@media (prefers-color-scheme:dark){.bad{color:#ff7b7b}}</style>
{{if .GcodeOfficial}}<p class="ok">{{.GcodeProfileLine}}</p>{{else}}<p class="warn bad">{{.GcodeProfileLine}}</p>{{end}}
{{range .Anomalies}}{{if ne . $.V.Safety.GcodeProfileLine}}<p class="warn bad">Anomalie du G-code : {{.}}</p>{{end}}{{end}}{{end}}
{{if .Photo}}<figure>{{if .Photo.URL}}<img src="{{.Photo.URL}}" alt="Photo du plateau" style="max-width:100%;border:1px solid #888;border-radius:6px"><figcaption>Photo du plateau prise à {{lt .Photo.TakenAt}}, <strong>{{.PhotoAge}}</strong>, par la caméra de l'imprimante. Vérifie sur cette image que le plateau est vide et dégagé ; si tu viens de le dégager, reprends la photo.</figcaption>{{else}}<p class="warn">Photo du plateau indisponible ({{.Photo.Err}}). Vérifie le plateau à l'œil, devant l'imprimante.</p>{{end}}</figure>
<form method="post" action="/confirm/{{.ID}}/photo"><input type="hidden" name="nonce" value="{{.PhotoNonce}}"><button type="submit">Reprendre la photo</button></form>{{end}}
{{range .V.Binding.Alerts}}<p class="warn">Alerte matière : {{.}}</p>{{end}}
{{range .V.Binding.Adhesion}}<p class="warn"><strong>Risque de décollement.</strong> {{.}}</p>{{end}}
<p class="warn">L'imprimante ne peut pas savoir si le plateau est vide. Cette autorisation vaut pour <strong>cette impression seulement</strong>, pendant <strong>2 minutes</strong> ; changer le fichier, le plateau, l'imprimante ou les matières l'annule.</p>
<form method="post"><input type="hidden" name="nonce" value="{{.Nonce}}">
<label class="chk"><input type="checkbox" name="plate_clear" value="yes" required> Le plateau est vide et dégagé, l'imprimante est surveillée.</label>
<button type="submit">Lancer l'impression</button></form>{{end}}{{template "foot"}}{{end}}
{{define "stop"}}{{template "head" .}}<h1>Arrêter cette impression ?</h1>
{{if .Closed}}<p class="warn">{{.Closed}}</p>{{else}}
<dl><dt>Fichier</dt><dd><code>{{.V.File}}</code></dd><dt>Imprimante</dt><dd>{{.V.PrinterName}}</dd>
<dt>Avancement</dt><dd>{{.V.Percent}} % (couche {{.V.Layer}} / {{.V.TotalLayers}})</dd>
<dt>Valable jusqu'à</dt><dd>{{lt .V.ExpiresAt}}<br><small>fuseau : {{.TZ}}</small></dd></dl>
<p class="warn">L'arrêt est <strong>définitif</strong> : le travail en cours est perdu. Rien n'est arrêté tant que tu n'as pas cliqué.</p>
<form method="post"><input type="hidden" name="nonce" value="{{.Nonce}}"><button type="submit">Arrêter l'impression</button></form>{{end}}{{template "foot"}}{{end}}
{{define "done"}}{{template "head" .}}<h1>{{.Title}}</h1><p class="ok">{{.Msg}}</p>{{template "foot"}}{{end}}`))

func (s *Service) render(w http.ResponseWriter, status int, name string, data any) {
	if name == "confirm" { // seule page qui montre une image (photo du plateau, même origine)
		humanauth.PageHeadersWithImages(w)
	} else {
		humanauth.PageHeaders(w)
	}
	w.WriteHeader(status)
	if err := pageTpl.ExecuteTemplate(w, name, data); err != nil {
		s.log.Warn("page non rendue", "page", name, "err", err)
	}
}

// RegisterPages monte /setup/printer/{id}, /confirm/{id} et /stop/{id} sur mux. require (humanauth.Require)
// impose le navigateur enregistré ; le nonce de chaque formulaire est vérifié ici.
func (s *Service) RegisterPages(mux *http.ServeMux, require func(http.Handler) http.Handler) {
	if require == nil {
		require = func(h http.Handler) http.Handler { return h }
	}
	h := func(f http.HandlerFunc) http.Handler { return require(f) }
	mux.Handle("GET /setup/printer/{id}", h(s.setupGet))
	mux.Handle("POST /setup/printer/{id}", h(s.setupPost))
	mux.Handle("GET /confirm/{id}", h(s.confirmGet))
	mux.Handle("POST /confirm/{id}", h(s.confirmPost))
	mux.Handle("POST /confirm/{id}/photo", h(s.photoPost))
	mux.Handle("GET /stop/{id}", h(s.stopGet))
	mux.Handle("POST /stop/{id}", h(s.stopPost))
}

func (s *Service) mqttHostPort(rec PrinterRecord) string {
	p := rec.MQTTPort
	if p == 0 {
		p = 8883
	}
	return net.JoinHostPort(rec.Host, strconv.Itoa(p))
}

// setupData prépare la page de saisie : lit le certificat présenté par l'imprimante (aucune donnée envoyée).
func (s *Service) setupData(ctx context.Context, id string) (map[string]any, string, int) {
	rec, err := s.GetPrinterRecord(ctx, id)
	if err != nil {
		return map[string]any{"Title": "Imprimante introuvable", "Msg": "Cette imprimante n'existe pas."}, "", http.StatusNotFound
	}
	m, _ := s.GetPrinterModel(ctx, rec.ModelID)
	d := map[string]any{"Title": "Certificat et code d'accès", "Name": rec.Name, "Model": m.Name, "Host": rec.Host, "Serial": rec.Serial,
		"Set": s.secrets.HasAccessCode(id), "Pinned": rec.PinnedSHA256}
	fctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	ci, ferr := s.d.CertFetch(fctx, s.mqttHostPort(rec))
	if ferr != nil {
		d["FetchErr"] = ferr.Error()
		return d, "", 0
	}
	d["Fingerprint"], d["CertCN"] = ci.SHA256, ci.SubjectCN
	d["CNMismatch"] = !strings.EqualFold(ci.SubjectCN, rec.Serial) && !containsFold(ci.DNSNames, rec.Serial)
	d["Changed"] = rec.PinnedSHA256 != "" && !strings.EqualFold(rec.PinnedSHA256, ci.SHA256)
	return d, ci.SHA256, 0
}

func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

func (s *Service) setupGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, fp, st := s.setupData(r.Context(), id)
	if st != 0 {
		s.render(w, st, "done", d)
		return
	}
	d["Nonce"] = s.issueNonce("setup:"+id, fp) // l'empreinte affichée est liée au nonce côté serveur
	s.render(w, http.StatusOK, "setup", d)
}

func (s *Service) setupPost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		s.render(w, http.StatusBadRequest, "done", map[string]any{"Title": "Requête invalide", "Msg": "Formulaire illisible."})
		return
	}
	pn, ok := s.takeNonce(r.PostFormValue("nonce"), "setup:"+id)
	if !ok {
		s.render(w, http.StatusForbidden, "done", map[string]any{"Title": "Formulaire périmé", "Msg": "Recharge la page puis recommence."})
		return
	}
	// Ce qui est épinglé est EXACTEMENT l'empreinte qui a été affichée à l'humain avec ce nonce.
	if err := s.PinAndSetAccessCode(r.Context(), id, pn.extra, r.PostFormValue("access_code")); err != nil {
		d, fp, st := s.setupData(r.Context(), id)
		if st != 0 {
			s.render(w, st, "done", d)
			return
		}
		d["Nonce"] = s.issueNonce("setup:"+id, fp)
		d["Title"] = "Code refusé"
		s.render(w, http.StatusBadRequest, "setup", d)
		return
	}
	d, _, st := s.setupData(r.Context(), id)
	if st != 0 {
		s.render(w, st, "done", d)
		return
	}
	d["Done"] = true
	d["Set"] = true
	d["Pinned"] = pn.extra
	if pn.extra == "" {
		rec, _ := s.GetPrinterRecord(r.Context(), id)
		d["Pinned"] = rec.PinnedSHA256
	}
	s.render(w, http.StatusOK, "setup", d)
}

func closedMsg(err error) string {
	switch {
	case errors.Is(err, contracts.ErrConfirmationUsed):
		return "Cette confirmation a déjà été utilisée : elle ne vaut qu'une fois."
	case errors.Is(err, contracts.ErrConfirmationExpired):
		return "Cette confirmation a expiré. Demande-en une nouvelle à Claude."
	case errors.Is(err, contracts.ErrConfirmationMismatch):
		msg := err.Error()
		switch {
		case strings.Contains(msg, "COMPLETED"), strings.Contains(msg, "FAILED"), strings.Contains(msg, "CANCELLED"), strings.Contains(msg, "PRINTING"), strings.Contains(msg, "START_"):
			return "Cette impression n'attend plus de confirmation (" + msg[strings.LastIndex(msg, "(")+1:] + " : elle est lancée, terminée ou annulée."
		}
		return "Cette confirmation ne correspond plus à l'impression (fichier, imprimante, plateau ou matières modifiés, ou nouvelle confirmation émise)."
	case errors.Is(err, ErrNotFound):
		return "Lien de confirmation inconnu de ce serveur (identifiant mal recopié, ou service redémarré depuis la demande). Demande-en un nouveau à Claude."
	}
	return "Confirmation indisponible."
}

func (s *Service) confirmGet(w http.ResponseWriter, r *http.Request) {
	id := trimLinkPunct(r.PathValue("id"))
	_, cj, err := s.confirmable(r.Context(), id)
	if err != nil {
		s.render(w, http.StatusGone, "confirm", map[string]any{"Title": "Confirmation", "Closed": closedMsg(err)})
		return
	}
	v, err := s.GetConfirmation(r.Context(), id)
	if err != nil {
		s.render(w, http.StatusNotFound, "confirm", map[string]any{"Title": "Confirmation", "Closed": closedMsg(err)})
		return
	}
	if v.Approved {
		s.render(w, http.StatusOK, "done", map[string]any{"Title": "Déjà confirmée", "Msg": "Tu as déjà autorisé cette impression (l'accord vaut 2 minutes). Le lancement suit ton accord : demande à Claude l'état de l'impression."})
		return
	}
	d := map[string]any{"Title": "Confirmation de lancement", "V": v, "Nonce": s.issueNonce(id), "ID": id, "TZ": localtime.Name()}
	// Chaque ouverture ou rechargement reprend une photo si la dernière date de plus de PhotoMaxAge (~15 s).
	if ph := s.platePhotoFor(r.Context(), cj.PrinterID, false); ph != nil {
		d["Photo"] = ph
		d["PhotoNonce"] = s.issueNonce("photo:" + id)
		if ph.URL != "" {
			d["PhotoAge"] = localtime.Age(ph.TakenAt, s.now())
		}
	}
	s.render(w, http.StatusOK, "confirm", d)
}

// photoPost : bouton « Reprendre la photo ». Même protection que le reste de /confirm (navigateur enregistré, garde
// cross-origin / Sec-Fetch de l'API, nonce à usage unique lié à cette confirmation) ; il ne fait QUE reprendre une
// photo (aucun effet sur l'approbation ni sur l'imprimante), puis recharge la page.
func (s *Service) photoPost(w http.ResponseWriter, r *http.Request) {
	id := trimLinkPunct(r.PathValue("id"))
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		s.render(w, http.StatusBadRequest, "done", map[string]any{"Title": "Requête invalide", "Msg": "Formulaire illisible."})
		return
	}
	if !s.useNonce(r.PostFormValue("nonce"), "photo:"+id) {
		s.render(w, http.StatusForbidden, "done", map[string]any{"Title": "Formulaire périmé", "Msg": "Recharge la page de confirmation puis recommence."})
		return
	}
	if err := s.RefreshPlatePhoto(r.Context(), id); err != nil {
		s.render(w, http.StatusGone, "confirm", map[string]any{"Title": "Confirmation", "Closed": closedMsg(err)})
		return
	}
	http.Redirect(w, r, "/confirm/"+id, http.StatusSeeOther)
}

func (s *Service) confirmPost(w http.ResponseWriter, r *http.Request) {
	id := trimLinkPunct(r.PathValue("id"))
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
	res, err := s.ApproveAndStart(r.Context(), id, plate, "web") // l'approbation humaine lance l'impression (autostart.go)
	if err != nil {
		st := http.StatusGone
		msg := closedMsg(err)
		if errors.Is(err, ErrNeedsApproval) {
			st, msg = http.StatusBadRequest, "Coche l'attestation « plateau vide » pour autoriser."
		}
		s.render(w, st, "done", map[string]any{"Title": "Non confirmée", "Msg": msg})
		return
	}
	title := "Lancement autorisé"
	switch {
	case res.Started() && res.Start.Outcome == "printing":
		title = "Impression lancée"
	case res.Started():
		title = "Lancement envoyé"
	case res.Start != nil:
		title = "Lancement non effectué"
	default:
		title = "Autorisé, pas démarré"
	}
	s.render(w, http.StatusOK, "done", map[string]any{"Title": title, "Msg": res.Message() + " Tu n'as rien à dire à Claude."})
}

func (s *Service) stopGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	v, err := s.GetStop(r.Context(), id)
	if err != nil {
		msg := err.Error()
		if i := strings.Index(msg, " : "); i >= 0 {
			msg = msg[i+3:]
		}
		s.render(w, http.StatusGone, "stop", map[string]any{"Title": "Arrêt", "Closed": strings.ToUpper(msg[:1]) + msg[1:] + "."})
		return
	}
	s.render(w, http.StatusOK, "stop", map[string]any{"Title": "Arrêt d'impression", "V": v, "Nonce": s.issueNonce("stop:" + id), "TZ": localtime.Name()})
}

func (s *Service) stopPost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		s.render(w, http.StatusBadRequest, "done", map[string]any{"Title": "Requête invalide", "Msg": "Formulaire illisible."})
		return
	}
	if !s.useNonce(r.PostFormValue("nonce"), "stop:"+id) {
		s.render(w, http.StatusForbidden, "done", map[string]any{"Title": "Formulaire périmé", "Msg": "Recharge la page d'arrêt puis recommence."})
		return
	}
	if _, err := s.ExecuteStop(r.Context(), id); err != nil {
		s.log.Warn("arrêt refusé", "stop", id, "err", err)
		s.render(w, http.StatusConflict, "done", map[string]any{"Title": "Arrêt non effectué", "Msg": "L'arrêt n'a pas pu être effectué : " + err.Error()})
		return
	}
	s.render(w, http.StatusOK, "done", map[string]any{"Title": "Impression arrêtée", "Msg": "La commande d'arrêt a été acceptée par l'imprimante."})
}
