package printing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/localtime"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

// Actions à effet physique ou destructif validées par l'HUMAIN : reprise d'une impression en pause, chargement du
// filament de la bobine externe, nettoyage de fichiers de la carte SD. MÊME mécanisme que l'arrêt : un outil MCP CRÉE une
// demande et renvoie une URL /action/{id} ; rien n'est fait tant que l'humain n'a pas validé sur cette page (navigateur
// enregistré, passkey / code de session) ou saisi son code de lancement dans la vue de l'application. Une demande = une
// seule commande : elle est consommée AVANT l'envoi, jamais renvoyée automatiquement, y compris en cas de doute.

// Types d'actions.
const (
	ActionResume  = "resume"        // reprendre une impression en pause
	ActionLoad    = "load_filament" // charger la bobine externe
	ActionCleanup = "cleanup_files" // supprimer des fichiers obsolètes de la carte SD
)

const actionTTL = 10 * time.Minute

type actionReq struct {
	kind      string
	jobID     string
	printerID string
	names     []string // cleanup : liste EXACTE des fichiers à supprimer
	tempC     int      // load_filament : température de buse
	expires   time.Time
}

type goneAction struct {
	reason string
	at     time.Time
}

// ActionOutput est ce que le modèle reçoit en créant une demande : jamais de moyen de l'exécuter.
type ActionOutput struct {
	ActionID       string    `json:"action_id"`
	Kind           string    `json:"kind"`
	ActionURL      string    `json:"action_url" jsonschema:"page que l'HUMAIN ouvre (navigateur enregistré) pour valider ; rien n'est fait tant qu'il n'a pas validé"`
	ExpiresAt      time.Time `json:"expires_at"`
	ExpiresAtLocal string    `json:"expires_at_local"`
	PrintJobID     string    `json:"print_job_id,omitempty"`
	PrinterID      string    `json:"printer_id"`
	Question       string    `json:"question"`
	Details        []string  `json:"details,omitempty" jsonschema:"ce que l'humain va valider (erreurs décodées, fichiers exacts)"`
	Note           string    `json:"note"`
}

// ActionView : ce que montre la page /action/{id}.
type ActionView struct {
	ActionID       string    `json:"action_id"`
	Kind           string    `json:"kind"`
	Heading        string    `json:"heading"`
	Button         string    `json:"button"`
	PrinterName    string    `json:"printer_name"`
	File           string    `json:"file,omitempty"`
	Lines          []string  `json:"lines"`
	Files          []string  `json:"files,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
	ExpiresAtLocal string    `json:"expires_at_local"`
}

// ActionResult : issue de l'exécution humaine.
type ActionResult struct {
	Kind    string   `json:"kind"`
	Done    bool     `json:"done"`
	Message string   `json:"message"`
	Deleted []string `json:"deleted,omitempty"`
	Failed  []string `json:"failed,omitempty"`
}

func newActionID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "act_" + hex.EncodeToString(b)
}

func (s *Service) putAction(r actionReq) (string, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.actions == nil {
		s.actions, s.actionGone = map[string]*actionReq{}, map[string]goneAction{}
	}
	now := s.now()
	for k, v := range s.actions { // purge
		if !now.Before(v.expires) {
			delete(s.actions, k)
			s.actionGone[k] = goneAction{reason: "expired", at: now}
		}
	}
	r.expires = now.Add(actionTTL)
	id := newActionID()
	s.actions[id] = &r
	return id, r.expires
}

func (s *Service) lookupAction(id string) (*actionReq, string) {
	id = trimLinkPunct(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.actions[id]
	if !ok {
		if g, gone := s.actionGone[id]; gone {
			if g.reason == "used" {
				return nil, "cette demande a déjà été traitée : l'action a été exécutée (ou tentée) une fois ; une demande ne vaut qu'une fois"
			}
			return nil, "cette demande a expiré (elle vaut 10 minutes) : redemande-la à Claude"
		}
		return nil, "demande inconnue de ce serveur (identifiant mal recopié, ou service redémarré depuis la demande)"
	}
	if !s.now().Before(r.expires) {
		delete(s.actions, id)
		s.actionGone[id] = goneAction{reason: "expired", at: s.now()}
		return nil, "cette demande a expiré (elle vaut 10 minutes) : redemande-la à Claude"
	}
	cp := *r
	return &cp, ""
}

func (s *Service) actionOut(id string, exp time.Time, r actionReq, q, note string, details []string) *ActionOutput {
	return &ActionOutput{ActionID: id, Kind: r.kind, ActionURL: s.BaseURL() + "/action/" + id, ExpiresAt: exp, ExpiresAtLocal: localtime.Full(exp),
		PrintJobID: r.jobID, PrinterID: r.printerID, Question: q, Note: note, Details: details}
}

// RequestResume crée la demande de reprise d'une impression en pause (état PRINTING côté serveur, PAUSE côté imprimante).
func (s *Service) RequestResume(ctx context.Context, jobID string) (*ActionOutput, error) {
	j, err := s.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if j.State != contracts.PrintPrinting {
		return nil, fmt.Errorf("%w : l'impression %s est en %s ; on ne reprend qu'une impression en cours (PRINTING) que l'imprimante a mise en pause", ErrState, j.ID, j.State)
	}
	ad, _, err := s.adapter(ctx, j.PrinterID)
	if err != nil {
		return nil, err
	}
	st, err := ad.Status(ctx)
	if err != nil || !st.Connected {
		return nil, fmt.Errorf("%w : imprimante injoignable, impossible de proposer la reprise", ErrUnavailable)
	}
	if st.State != printer.StatePaused {
		return nil, fmt.Errorf("%w : l'imprimante est en état %s, pas en pause : rien à reprendre", ErrState, st.State)
	}
	var det []string
	for _, e := range decodeErrors(st) {
		det = append(det, e.SummaryLine()+" → "+e.Action)
	}
	r := actionReq{kind: ActionResume, jobID: j.ID, printerID: j.PrinterID}
	id, exp := s.putAction(r)
	s.journal(ctx, j.ID, "resume_requested", "reprise demandée (resume_print) : en attente de la validation de l'humain")
	_ = s.d.Store.Audit(ctx, actor, "print.resume_requested", "print_job", j.ID, map[string]any{"action": id})
	q := fmt.Sprintf("Veux-tu reprendre l'impression de %s ? L'imprimante est en pause.", j.Name)
	return s.actionOut(id, exp, r, q, "Rien n'est repris. Demande à l'utilisateur de valider (carte dans la conversation, ou action_url dans son navigateur enregistré) ; tu ne peux pas le faire à sa place. Vérifie d'abord, avec lui, que la cause de la pause est réglée (filament chargé, plateau libre…).", det), nil
}

// RequestLoadFilament crée la demande de chargement de la bobine externe (commande de chargement de la bobine hors AMS).
// jobID facultatif (journalisé sur l'impression en pause) ; tempC facultatif (défaut : consigne de buse en cours, sinon 220).
func (s *Service) RequestLoadFilament(ctx context.Context, printerID, jobID string, tempC int) (*ActionOutput, error) {
	pid, err := s.ResolvePrinterID(ctx, printerID)
	if err != nil {
		return nil, err
	}
	ad, _, err := s.adapter(ctx, pid)
	if err != nil {
		return nil, err
	}
	if _, ok := ad.(printer.FilamentLoader); !ok {
		return nil, s.unsupported(ctx, pid, "le chargement de la bobine externe")
	}
	st, err := ad.Status(ctx)
	if err != nil || !st.Connected {
		return nil, fmt.Errorf("%w : imprimante injoignable", ErrUnavailable)
	}
	if st.State == printer.StatePrinting || st.State == printer.StatePreparing {
		return nil, fmt.Errorf("%w : l'imprimante est en état %s : on ne charge pas de filament pendant l'impression (attendre la pause)", ErrState, st.State)
	}
	if jobID != "" {
		j, err := s.GetJob(ctx, jobID)
		if err != nil {
			return nil, err
		}
		if j.PrinterID != pid {
			return nil, invalidf("l'impression %s n'est pas sur cette imprimante", jobID)
		}
	}
	if tempC == 0 {
		tempC = int(st.Temps.NozzleTarget)
		if tempC < 150 {
			tempC = 220
		}
	}
	if tempC < 150 || tempC > 320 {
		return nil, invalidf("température de chargement entre 150 et 320 °C")
	}
	var det []string
	for _, e := range decodeErrors(st) {
		det = append(det, e.SummaryLine()+" → "+e.Action)
	}
	r := actionReq{kind: ActionLoad, jobID: jobID, printerID: pid, tempC: tempC}
	id, exp := s.putAction(r)
	if jobID != "" {
		s.journal(ctx, jobID, "load_requested", fmt.Sprintf("chargement de la bobine externe demandé (%d °C) : en attente de la validation de l'humain", tempC))
	}
	_ = s.d.Store.Audit(ctx, actor, "printer.load_requested", "printer", pid, map[string]any{"action": id, "temp": tempC})
	q := fmt.Sprintf("Veux-tu que l'imprimante charge le filament de la bobine externe (buse chauffée à %d °C) ? Le filament doit être engagé à la main dans l'extrudeur.", tempC)
	return s.actionOut(id, exp, r, q, "Rien n'est chargé. Demande à l'utilisateur de valider (carte ou action_url dans son navigateur enregistré).", det), nil
}

// GetAction relit une demande valide (sans la consommer).
func (s *Service) GetAction(ctx context.Context, id string) (*ActionView, error) {
	r, why := s.lookupAction(id)
	if why != "" {
		return nil, fmt.Errorf("%w : %s", ErrNotFound, why)
	}
	id = trimLinkPunct(id)
	rec, _ := s.GetPrinterRecord(ctx, r.printerID)
	v := &ActionView{ActionID: id, Kind: r.kind, PrinterName: rec.Name, ExpiresAt: r.expires, ExpiresAtLocal: localtime.Full(r.expires)}
	var j Job
	if r.jobID != "" {
		j, _ = s.GetJob(ctx, r.jobID)
		v.File = j.Name
	}
	switch r.kind {
	case ActionResume:
		v.Heading, v.Button = "Reprendre cette impression ?", "Reprendre l'impression"
		if j.State != contracts.PrintPrinting {
			return nil, fmt.Errorf("%w : l'impression n'est plus en cours (%s)", ErrState, j.State)
		}
		v.Lines = []string{"L'imprimante est en pause. Une seule commande de reprise sera envoyée, jamais renvoyée automatiquement."}
		if j.Print != nil && j.Print.Attention != "" {
			v.Lines = append(v.Lines, j.Print.Attention)
		}
		v.Lines = append(v.Lines, "Vérifie que la cause de la pause est réglée (filament chargé, plateau libre, rien ne gêne la tête).")
	case ActionLoad:
		v.Heading, v.Button = "Charger la bobine externe ?", "Charger le filament"
		v.Lines = []string{fmt.Sprintf("La buse va chauffer à %d °C puis l'imprimante charge le filament de la bobine externe (hors AMS).", r.tempC),
			"Engage le filament dans l'extrudeur à la main si l'écran le demande. Une seule commande, jamais renvoyée automatiquement."}
	case ActionCleanup:
		v.Heading, v.Button = "Supprimer ces fichiers de la carte SD ?", "Supprimer ces fichiers"
		v.Files = r.names
		v.Lines = []string{fmt.Sprintf("%d fichier(s) déposé(s) par bambud, jugés obsolètes, seront SUPPRIMÉS de l'imprimante. C'est définitif pour la carte ; les impressions et leurs profils restent dans le catalogue.", len(r.names))}
	}
	return v, nil
}

// ExecuteAction exécute la demande APRÈS validation humaine (page /action/{id} d'un navigateur enregistré, ou code de
// lancement). Jamais appelé par un outil MCP ouvert au modèle. La demande est consommée avant l'envoi.
func (s *Service) ExecuteAction(ctx context.Context, id, via string) (*ActionResult, error) {
	id = trimLinkPunct(id)
	if _, err := s.GetAction(ctx, id); err != nil {
		return nil, err
	}
	s.mu.Lock()
	r := s.actions[id]
	if r == nil { // course : une autre validation l'a prise
		s.mu.Unlock()
		return nil, fmt.Errorf("%w : cette demande a déjà été traitée", ErrNotFound)
	}
	req := *r
	delete(s.actions, id)
	s.actionGone[id] = goneAction{reason: "used", at: s.now()}
	s.mu.Unlock()
	who := "human:" + via
	switch req.kind {
	case ActionResume:
		return s.doResume(ctx, req, who)
	case ActionLoad:
		return s.doLoad(ctx, req, who)
	case ActionCleanup:
		return s.doCleanup(ctx, req, who)
	}
	return nil, invalidf("type d'action inconnu")
}

func (s *Service) doResume(ctx context.Context, req actionReq, who string) (*ActionResult, error) {
	j, err := s.GetJob(ctx, req.jobID)
	if err != nil {
		return nil, err
	}
	ad, _, err := s.adapter(ctx, req.printerID)
	if err != nil {
		s.journal(ctx, j.ID, "resume_failed", "reprise validée mais imprimante injoignable : "+err.Error())
		return nil, err
	}
	s.journal(ctx, j.ID, "resume_approved", "reprise validée par l'humain ("+who+")")
	_ = s.d.Store.Audit(ctx, who, "print.resume_approved", "print_job", j.ID, nil)
	s.journal(ctx, j.ID, "resume_sent", "commande de reprise envoyée (une seule, jamais renvoyée automatiquement)")
	rctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	serr := ad.Resume(rctx)
	switch {
	case serr == nil:
		s.journal(ctx, j.ID, "resume_applied", "l'imprimante a repris (état RUNNING / PREPARE observé)")
		_, _ = s.update(ctx, j.ID, func(x *Job) error {
			if x.Print != nil {
				cp := copyProgress(x.Print)
				cp.Attention, cp.Blocked, cp.Sig, cp.SigAt = "", "", "", time.Time{}
				x.Print = &cp
			}
			return nil
		})
		return &ActionResult{Kind: ActionResume, Done: true, Message: "Reprise acceptée : l'imprimante a repris. Surveille la progression (get_print_status) : un redémarrage bancal serait signalé « bloquée : … »."}, nil
	case errors.Is(serr, printer.ErrStateNotObserved):
		s.journal(ctx, j.ID, "resume_unconfirmed", "reprise acquittée mais l'état attendu n'a pas été observé : ne PAS renvoyer, regarder l'imprimante")
		return &ActionResult{Kind: ActionResume, Done: false, Message: "La commande de reprise a été acquittée mais l'imprimante n'a pas repris dans le délai. Rien n'est renvoyé : regarde l'imprimante (erreur non acquittée à l'écran ?)."}, nil
	}
	s.journal(ctx, j.ID, "resume_failed", "reprise refusée ou impossible : "+serr.Error())
	return nil, fmt.Errorf("reprise en échec : %w", serr)
}

func (s *Service) doLoad(ctx context.Context, req actionReq, who string) (*ActionResult, error) {
	ad, _, err := s.adapter(ctx, req.printerID)
	if err != nil {
		return nil, err
	}
	ld, ok := ad.(printer.FilamentLoader)
	if !ok {
		return nil, s.unsupported(ctx, req.printerID, "le chargement de la bobine externe")
	}
	_ = s.d.Store.Audit(ctx, who, "printer.load_approved", "printer", req.printerID, map[string]any{"temp": req.tempC})
	if req.jobID != "" {
		s.journal(ctx, req.jobID, "load_approved", fmt.Sprintf("chargement de la bobine externe validé par l'humain (%s), %d °C ; commande envoyée (une seule)", who, req.tempC))
	}
	lctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	serr := ld.LoadExternalFilament(lctx, req.tempC)
	switch {
	case serr == nil:
		if req.jobID != "" {
			s.journal(ctx, req.jobID, "load_acked", "l'imprimante a accepté la commande de chargement ; reprendre ensuite avec resume_print")
		}
		return &ActionResult{Kind: ActionLoad, Done: true, Message: "Commande de chargement acceptée par l'imprimante. Suis le chargement à l'écran ; reprends ensuite l'impression avec resume_print (validation humaine)."}, nil
	case errors.Is(serr, printer.ErrStateNotObserved):
		if req.jobID != "" {
			s.journal(ctx, req.jobID, "load_unconfirmed", "chargement acquitté sans réponse de l'imprimante : ne pas renvoyer")
		}
		return &ActionResult{Kind: ActionLoad, Message: "La commande de chargement est partie mais l'imprimante n'a pas répondu. Rien n'est renvoyé : regarde l'écran de l'imprimante."}, nil
	}
	if req.jobID != "" {
		s.journal(ctx, req.jobID, "load_failed", "chargement refusé ou impossible : "+serr.Error())
	}
	return nil, fmt.Errorf("chargement en échec : %w", serr)
}

// ActionWithCode exécute la demande si le code de lancement personnel de l'humain est correct (vue de l'application).
func (s *Service) ActionWithCode(ctx context.Context, id, code string) (*ActionResult, error) {
	if s.PasskeyRequired(ctx) { // « exiger une passkey » : le code est refusé avant tout essai (aucune tentative brûlée)
		return nil, humanauth.ErrPasskeyRequired
	}
	id = trimLinkPunct(id)
	v, err := s.GetAction(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.launchCodes().Verify(ctx, code, map[string]any{"action": id, "kind": v.Kind}); err != nil {
		return nil, err
	}
	_ = s.d.Store.Audit(ctx, "human:app-code", "print.action_code_accepted", "printer", "", map[string]any{"action": id, "kind": v.Kind})
	return s.ExecuteAction(ctx, id, "app-code")
}

// ---- page /action/{id} ----

func init() {
	template.Must(pageTpl.Parse(`{{define "action"}}{{template "head" .}}<h1>{{.Heading}}</h1>
{{if .Closed}}<p class="warn">{{.Closed}}</p>{{else}}
<dl><dt>Imprimante</dt><dd>{{.V.PrinterName}}</dd>{{if .V.File}}<dt>Fichier</dt><dd><code>{{.V.File}}</code></dd>{{end}}
<dt>Valable jusqu'à</dt><dd>{{lt .V.ExpiresAt}}<br><small>fuseau : {{.TZ}}</small></dd></dl>
{{range .V.Lines}}<p class="warn">{{.}}</p>{{end}}
{{if .V.Files}}<p>Fichiers concernés :</p><ul>{{range .V.Files}}<li><code>{{.}}</code></li>{{end}}</ul>{{end}}
<form method="post"{{if .PasskeyOptions}} data-pk-mode="get" data-pk-options="{{.PasskeyOptions}}"{{end}}><input type="hidden" name="nonce" value="{{.Nonce}}">
{{if not .PasskeyRequired}}<button type="submit">{{.V.Button}}</button>{{end}}
{{if .PasskeyOptions}}<button type="button" data-pk-go hidden>Valider avec ma passkey</button>{{end}}
{{if .PasskeyRequired}}<p class="warn">Une passkey est exigée (réglage de <code>/setup/passkeys</code>) : le bouton simple et le code de lancement sont indisponibles.{{if not .PasskeyOptions}} {{if .PasskeyErr}}Passkey indisponible ici : {{.PasskeyErr}}{{else}}Aucune passkey n'est disponible : enregistre-en une sur <code>/setup/passkeys</code> ou lance <code>bambud passkey --require=off</code>.{{end}}{{end}}</p>{{end}}
{{if and .PasskeyErr (not .PasskeyRequired)}}<p class="dim"><small>Passkey indisponible ici : {{.PasskeyErr}}</small></p>{{end}}
<p id="pk-msg" role="status" hidden></p></form>{{if .PasskeyOptions}}<script src="/setup/passkeys/passkey.js" defer></script>{{end}}{{end}}{{template "foot"}}{{end}}`))
}

func (s *Service) actionGet(w http.ResponseWriter, r *http.Request) {
	id := trimLinkPunct(r.PathValue("id"))
	v, err := s.GetAction(r.Context(), id)
	if err != nil {
		msg := err.Error()
		if i := strings.Index(msg, " : "); i >= 0 {
			msg = msg[i+3:]
		}
		s.render(w, http.StatusGone, "action", map[string]any{"Title": "Action", "Heading": "Action sur l'imprimante", "Closed": strings.ToUpper(msg[:1]) + msg[1:] + "."})
		return
	}
	d := map[string]any{"Title": v.Heading, "Heading": v.Heading, "V": v, "Nonce": s.issueNonce("action:" + id), "TZ": localtime.Name()}
	d["PasskeyRequired"] = s.PasskeyRequired(r.Context())
	d["PasskeyOptions"], d["PasskeyErr"] = s.passkeyBegin(r.Context(), r, humanauth.ApprovalSubject{Kind: "action", ID: id, Binding: v.Kind})
	s.render(w, http.StatusOK, "action", d)
}

func (s *Service) actionPost(w http.ResponseWriter, r *http.Request) {
	id := trimLinkPunct(r.PathValue("id"))
	r.Body = http.MaxBytesReader(w, r.Body, humanauth.PasskeyMaxBody+4096)
	if err := r.ParseForm(); err != nil {
		s.render(w, http.StatusBadRequest, "done", map[string]any{"Title": "Requête invalide", "Msg": "Formulaire illisible."})
		return
	}
	if !s.useNonce(r.PostFormValue("nonce"), "action:"+id) {
		s.render(w, http.StatusForbidden, "done", map[string]any{"Title": "Formulaire périmé", "Msg": "Recharge la page puis recommence."})
		return
	}
	var (
		res *ActionResult
		err error
	)
	switch {
	case r.PostFormValue("method") == "passkey":
		a := r.PostFormValue("assertion")
		if len(a) > humanauth.PasskeyMaxBody {
			a = ""
		}
		res, err = s.ActionWithPasskey(r.Context(), id, []byte(a), r)
		if err != nil && isPasskeyError(err) {
			s.render(w, http.StatusForbidden, "done", map[string]any{"Title": "Action refusée", "Msg": err.Error() + ". Recharge la page puis recommence."})
			return
		}
	case s.PasskeyRequired(r.Context()):
		s.render(w, http.StatusForbidden, "done", map[string]any{"Title": "Action refusée", "Msg": humanauth.ErrPasskeyRequired.Error() + "."})
		return
	default:
		res, err = s.ExecuteAction(r.Context(), id, "web")
	}
	if err != nil {
		s.log.Warn("action refusée", "action", id, "err", err)
		s.render(w, http.StatusConflict, "done", map[string]any{"Title": "Action non effectuée", "Msg": "L'action n'a pas pu être effectuée : " + err.Error()})
		return
	}
	title := "Action effectuée"
	if !res.Done {
		title = "Action envoyée, effet non confirmé"
	}
	msg := res.Message
	if len(res.Deleted) > 0 {
		msg += " Supprimés : " + strings.Join(res.Deleted, ", ") + "."
	}
	if len(res.Failed) > 0 {
		msg += " Échecs : " + strings.Join(res.Failed, " ; ") + "."
	}
	s.render(w, http.StatusOK, "done", map[string]any{"Title": title, "Msg": msg})
}

// ActionWithPasskey exécute la demande si l'assertion de la passkey de l'humain est valide (même mécanisme que /stop :
// l'approbation est liée à cette demande précise, à ce navigateur enregistré, et ne vaut qu'une fois).
func (s *Service) ActionWithPasskey(ctx context.Context, id string, assertion []byte, r *http.Request) (*ActionResult, error) {
	id = trimLinkPunct(id)
	v, err := s.GetAction(ctx, id)
	if err != nil {
		return nil, err
	}
	info, err := s.passkeys().FinishApproval(ctx, r, humanauth.ApprovalSubject{Kind: "action", ID: id, Binding: v.Kind}, assertion)
	if err != nil {
		return nil, err
	}
	_ = s.d.Store.Audit(ctx, "human:passkey", "print.action_passkey_accepted", "printer", "", map[string]any{"action": id, "kind": v.Kind, "passkey": info.ID})
	return s.ExecuteAction(ctx, id, "passkey")
}
