package printing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/localtime"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

// StartInput est l'entrée de start_print.
type StartInput struct {
	PrintJobID     string `json:"print_job_id"`
	ConfirmationID string `json:"confirmation_id" jsonschema:"identifiant renvoyé par request_print_confirmation ; l'humain doit avoir confirmé sur la page locale"`
	IdempotencyKey string `json:"idempotency_key" jsonschema:"obligatoire : une requête dupliquée ne relance jamais l'impression"`
}

// StartOutput décrit l'issue du lancement.
type StartOutput struct {
	PrintJobID string               `json:"print_job_id"`
	State      contracts.PrintState `json:"state"`
	QueueJobID string               `json:"queue_job_id"`
	Outcome    string               `json:"outcome" jsonschema:"printing | start_requested | start_unknown | rejected | not_sent | aborted_before_send | replayed"`
	Replayed   bool                 `json:"replayed"`
	Note       string               `json:"note"`
	Job        Job                  `json:"job"`
}

var reservingStates = map[contracts.PrintState]bool{
	contracts.PrintStartRequested: true, contracts.PrintPrinting: true, contracts.PrintStartUnknown: true,
}

// StartPrint lance l'impression APRÈS accord humain. Le lancement passe par un travail de file
// de type physique (start_print) : clé d'idempotence obligatoire, jamais rejoué.
func (s *Service) StartPrint(ctx context.Context, in StartInput) (*StartOutput, error) {
	key := strings.TrimSpace(in.IdempotencyKey)
	if len(key) < 8 || len(key) > 128 {
		return nil, invalidf("idempotency_key obligatoire (8 à 128 caractères)")
	}
	if in.PrintJobID == "" || in.ConfirmationID == "" {
		return nil, invalidf("print_job_id et confirmation_id requis")
	}
	j, err := s.GetJob(ctx, in.PrintJobID)
	if err != nil {
		return nil, err
	}
	// Clé composée : une même chaîne fournie pour une autre impression ou un autre type d'opération ne collisionne jamais.
	ikey := idemKey(j.ID, "start", key)
	// Rejeu de la même requête : on rend l'état, on ne relance JAMAIS. Mêmes clé ET paramètres, sinon refus.
	if j.StartJobID != "" {
		// Démarrage déjà fait par l'approbation de l'humain (autostart.go) : on le dit, sans rien renvoyer.
		if q, err := s.d.Queue.Get(ctx, j.StartJobID); err == nil && q.State != contracts.JobFailed && q.IdempotencyKey == idemKey(j.ID, "start", autoStartKey(in.ConfirmationID)) && sameStartParams(q.Params, j.ID, in.ConfirmationID) {
			q, _ = s.waitQueue(ctx, q.ID, 200*time.Millisecond)
			cur, _ := s.GetJob(ctx, j.ID)
			outcome := "start_requested"
			var r struct{ Outcome string }
			if json.Unmarshal(q.Result, &r) == nil && r.Outcome != "" {
				outcome = r.Outcome
			} else if q.State == contracts.JobFailed {
				outcome = "rejected"
			}
			return &StartOutput{PrintJobID: cur.ID, State: cur.State, QueueJobID: q.ID, Outcome: outcome, Replayed: true, Job: cur,
				Note: "Déjà lancé par l'approbation de l'utilisateur : aucune nouvelle commande n'a été envoyée. Suivre avec get_print_status."}, nil
		}
		if q, err := s.d.Queue.Get(ctx, j.StartJobID); err == nil && q.IdempotencyKey == ikey {
			if !sameStartParams(q.Params, j.ID, in.ConfirmationID) {
				return nil, fmt.Errorf("%w : start_print avec cette clé a déjà été demandé pour une autre confirmation", ErrReplayParams)
			}
			q, _ = s.waitQueue(ctx, q.ID, 200*time.Millisecond)
			cur, _ := s.GetJob(ctx, j.ID)
			return &StartOutput{PrintJobID: cur.ID, State: cur.State, QueueJobID: q.ID, Outcome: "replayed", Replayed: true, Job: cur,
				Note: "Clé d'idempotence déjà utilisée : aucune nouvelle commande n'a été envoyée."}, nil
		}
	}
	if j.State != contracts.PrintAwaitingConfirmation {
		return nil, fmt.Errorf("%w : l'impression %s est en %s ; le lancement exige AWAITING_PRINT_CONFIRMATION avec une confirmation valide (une ancienne confirmation ne vaut pas pour une nouvelle impression)", ErrState, j.ID, j.State)
	}
	if j.ConfirmationID != in.ConfirmationID {
		return nil, fmt.Errorf("%w : cette confirmation n'est pas celle de l'impression %s (confirmation courante : %s)", ErrNotConfirmed, j.ID, j.ConfirmationID)
	}
	if _, _, err := s.confirmable(ctx, in.ConfirmationID); err != nil {
		return nil, fmt.Errorf("%w : %v", ErrNotConfirmed, err)
	}
	if _, err := s.approvedFor(in.ConfirmationID, j.ID); err != nil {
		return nil, err
	}
	if err := s.checkReservation(ctx, j); err != nil {
		return nil, err
	}
	// Contrôle d'état FRAIS avant toute chose (idle, connecté, sans erreur) ; Bambu.Start le refait.
	ad, _, err := s.adapter(ctx, j.PrinterID)
	if err != nil {
		return nil, err
	}
	if _, lerr := s.launchableNow(ctx, ad, true); lerr != nil {
		return nil, fmt.Errorf("%w : l'imprimante n'est pas prête (%v) — rien n'a été envoyé, la confirmation reste valable", printer.ErrNotReady, lerr)
	}
	params, _ := json.Marshal(map[string]string{"print_job_id": j.ID, "confirmation_id": in.ConfirmationID})
	qj, created, err := s.d.Queue.Enqueue(ctx, jobs.EnqueueParams{Kind: JobStart, Class: contracts.ClassLight, Params: params, IdempotencyKey: ikey})
	if err != nil {
		return nil, err
	}
	if !created && !sameStartParams(qj.Params, j.ID, in.ConfirmationID) {
		return nil, fmt.Errorf("%w : start_print avec cette clé a déjà été demandé pour une autre confirmation", ErrReplayParams)
	}
	if created {
		if _, err := s.update(ctx, j.ID, func(x *Job) error { x.StartJobID = qj.ID; return nil }); err != nil {
			return nil, err
		}
	}
	q, done := s.waitQueue(ctx, qj.ID, s.syncWait())
	cur, _ := s.GetJob(ctx, j.ID)
	out := &StartOutput{PrintJobID: cur.ID, State: cur.State, QueueJobID: qj.ID, Replayed: !created, Job: cur}
	switch {
	case !done:
		out.Outcome, out.Note = "start_requested", "Lancement en cours (l'imprimante peut mettre plusieurs dizaines de secondes à accuser réception d'un gros fichier) : suivre avec get_print_status. Ne JAMAIS relancer : si l'issue reste incertaine (START_UNKNOWN), le service la réconcilie seul, en lecture, dès que l'imprimante montre ce fichier en cours."
	case q.State == contracts.JobFailed && q.Error != nil && q.Error.Code == "not_sent":
		out.Outcome, out.Note = "not_sent", "Rien n'a été envoyé à l'imprimante : "+q.Error.Message+" L'accord de l'utilisateur est invalidé : il doit confirmer à nouveau sur la page."
	case q.State == contracts.JobFailed && q.Error != nil && q.Error.Code == "aborted_before_send":
		out.Outcome, out.Note = "aborted_before_send", "Rien n'a été envoyé à l'imprimante (état START_ABORTED_BEFORE_SEND, terminal) : la confirmation avait été consommée mais le lancement n'a pas pu être enregistré. Reprendre avec une nouvelle impression et une nouvelle confirmation. "+q.Error.Message
	case q.State == contracts.JobFailed:
		out.Outcome, out.Note = "rejected", "Lancement refusé : "+q.Error.Message
	default:
		var r struct{ Outcome, Note string }
		_ = json.Unmarshal(q.Result, &r)
		out.Outcome, out.Note = r.Outcome, r.Note
	}
	return out, nil
}

// checkReservation : une seule impression active par imprimante.
func (s *Service) checkReservation(ctx context.Context, j Job) error {
	all, err := s.ListJobs(ctx, "", 500)
	if err != nil {
		return err
	}
	for _, o := range all {
		if o.ID != j.ID && o.PrinterID == j.PrinterID && reservingStates[o.State] {
			return fmt.Errorf("%w : l'impression %s (%s) occupe déjà l'imprimante", ErrPrinterBusy, o.ID, o.State)
		}
	}
	return nil
}

// idemKey compose la clé d'idempotence stockée dans la file : impression + type d'opération + clé fournie.
func idemKey(printJobID, kind, key string) string { return printJobID + ":" + kind + ":" + key }

// sameStartParams dit si les paramètres d'un travail start_print déjà en file sont ceux de la requête.
func sameStartParams(raw json.RawMessage, printJobID, confID string) bool {
	var p struct {
		ID   string `json:"print_job_id"`
		Conf string `json:"confirmation_id"`
	}
	return json.Unmarshal(raw, &p) == nil && p.ID == printJobID && p.Conf == confID
}

func (s *Service) startHandler(ctx context.Context, jc *jobs.JobCtx) (json.RawMessage, error) {
	var p struct {
		ID   string `json:"print_job_id"`
		Conf string `json:"confirmation_id"`
	}
	if err := json.Unmarshal(jc.Job.Params, &p); err != nil {
		return nil, &jobs.HandlerError{Code: "bad_params", Err: err}
	}
	bg := context.Background()
	notSent := func(err error) (json.RawMessage, error) {
		// Rien n'est parti : l'accord humain est invalidé (il doit recliquer), le jeton n'est pas brûlé.
		if !errors.Is(err, printer.ErrBusy) && !errors.Is(err, ErrState) && !errors.Is(err, ErrNeedsApproval) { // un lancement concurrent perdant ne doit pas invalider l'accord du gagnant
			s.invalidateApproval(p.Conf)
		}
		return nil, &jobs.HandlerError{Code: "not_sent", Err: err}
	}
	j, err := s.GetJob(bg, p.ID)
	if err != nil {
		return notSent(err)
	}
	if j.State != contracts.PrintAwaitingConfirmation || j.ConfirmationID != p.Conf {
		return notSent(fmt.Errorf("%w : %s", ErrState, j.State))
	}
	a, err := s.approvedFor(p.Conf, j.ID)
	if err != nil {
		return notSent(err)
	}
	if j.Remote == nil {
		return notSent(errors.New("aucun fichier déposé"))
	}
	ad, _, err := s.adapter(bg, j.PrinterID)
	if err != nil {
		return notSent(err)
	}
	var conf contracts.PrintConfirmation
	if err := s.d.Store.GetDoc(bg, "print_confirmations", p.Conf, &conf); err != nil {
		return notSent(err)
	}
	opts := startOptionsOf(j)
	preState, _ := ad.Status(bg) // état juste avant l'envoi : un départ depuis FINISH/FAILED exige de VOIR l'activité ensuite
	fromTerminal := printer.TerminalState(preState.State)
	req := printer.StartRequest{
		Remote: *j.Remote, Plate: j.Plate, UseAMS: j.UseAMS, AMSMapping: j.AMSMapping, PlateClear: a.plateClear,
		Options: opts,
		Proof: printer.ConfirmationProof{PrinterID: j.PrinterID, SHA256: j.ArtifactSHA256, Plate: j.Plate, AMSMapping: j.AMSMapping,
			Token: proofToken(p.Conf, a.token, jc.Job.ID), ExpiresAt: conf.ExpiresAt},
	}
	jc.Progress(0.3, "commande de lancement")
	serr := ad.Start(ctx, req)
	s.mu.Lock()
	consumed := s.consumedBy[p.Conf].queueJob == jc.Job.ID // CE lancement a consommé la confirmation
	s.mu.Unlock()
	if cur, gerr := s.GetJob(bg, j.ID); gerr == nil && cur.State == contracts.PrintStartAbortedBeforeSend {
		return nil, &jobs.HandlerError{Code: "aborted_before_send", Err: serr}
	}
	switch {
	case serr == nil && fromTerminal && !s.confirmActive(ctx, ad, j.Remote.Name):
		// Départ depuis FINISH : l'état FINISH précédent peut avoir été pris pour un succès ; rien d'actif n'a été vu.
		if _, err := s.transition(bg, j.ID, contracts.EvStartLost, "commande acceptée mais aucune activité observée pour ce fichier (départ depuis FINISH)", func(x *Job) error {
			x.LastError = "lancement indéterminé : ne pas relancer ; reconcile_print"
			return nil
		}); err != nil && !errors.Is(err, ErrState) {
			s.log.Warn("START_UNKNOWN non enregistré", "print_job", j.ID, "err", err)
		}
		out, _ := json.Marshal(map[string]string{"outcome": "start_unknown", "note": "Aucune activité observée après la commande (départ depuis FINISH). NE PAS relancer : appeler reconcile_print."})
		return out, nil
	case serr == nil:
		first := &PrintProgress{PrinterState: "preparing", MatchesFile: true, SeenActive: true, ObservedAt: s.now()}
		if st2, e := ad.Status(bg); e == nil && matches(st2, j.Remote.Name) {
			first.noteStage(st2, s.now()) // première étape vue (historique propre à CETTE impression)
		}
		if _, err := s.transition(bg, j.ID, contracts.EvObservedPrinting, "imprimante en préparation/impression observée", func(x *Job) error {
			x.Print = first
			return nil
		}); err != nil {
			return nil, &jobs.HandlerError{Code: "state", Err: err}
		}
		s.track(j.ID)
		out, _ := json.Marshal(map[string]string{"outcome": "printing", "note": "État PRINTING observé sur l'imprimante (accusé de transport, acceptation et état distingués). Suivi en cours."})
		return out, nil
	case errors.Is(serr, printer.ErrStartRejected):
		s.rejectStart(bg, j.ID, serr)
		return nil, &jobs.HandlerError{Code: "start_rejected", Err: serr}
	case errors.Is(serr, printer.ErrStartUnknown), consumed:
		var su *printer.StartUnknownError
		stage := "inconnue"
		if errors.As(serr, &su) {
			stage = string(su.Stage)
		}
		if _, err := s.transition(bg, j.ID, contracts.EvStartLost, fmt.Sprintf("résultat du lancement indéterminé (étape %s) : %v", stage, serr), func(x *Job) error {
			x.LastError = "lancement indéterminé : ne pas relancer ; reconcile_print"
			return nil
		}); err != nil && !errors.Is(err, ErrState) {
			s.log.Warn("START_UNKNOWN non enregistré", "print_job", j.ID, "err", err)
		}
		out, _ := json.Marshal(map[string]string{"outcome": "start_unknown", "note": "Le lancement a peut-être eu lieu (étape " + stage + "). NE PAS relancer : appeler reconcile_print."})
		return out, nil
	default:
		return notSent(serr) // refus avant la consommation de la confirmation : elle reste valable
	}
}

// confirmActive attend (au plus 20 s) de voir ce fichier préparer/imprimer sur l'imprimante.
func (s *Service) confirmActive(ctx context.Context, ad printer.Adapter, remote string) bool {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if st, err := ad.Status(ctx); err == nil && matches(st, remote) &&
			(st.State == printer.StatePreparing || st.State == printer.StatePrinting || st.State == printer.StatePaused) {
			return true
		}
		sleepCtx(ctx, 100*time.Millisecond)
	}
	return false
}

func (s *Service) rejectStart(ctx context.Context, id string, cause error) {
	msg := "lancement refusé par l'imprimante : " + cause.Error()
	if _, err := s.transition(ctx, id, contracts.EvStartRejected, msg, func(j *Job) error { j.LastError = msg; return nil }); err != nil {
		s.log.Warn("refus non enregistré", "print_job", id, "err", err)
	}
}

// ---- suivi ----

func matches(st printer.Status, remote string) bool { return printer.MatchesRemote(st, remote) }

// track suit une impression PRINTING jusqu'à COMPLETED/FAILED (abonnement + relevé périodique).
func (s *Service) track(id string) {
	s.mu.Lock()
	if _, ok := s.tracking[id]; ok {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.tracking[id] = cancel
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			cancel()
			s.mu.Lock()
			delete(s.tracking, id)
			s.mu.Unlock()
		}()
		for ctx.Err() == nil {
			j, err := s.GetJob(ctx, id)
			if err != nil || j.State != contracts.PrintPrinting {
				return
			}
			ad, _, err := s.adapter(ctx, j.PrinterID)
			if err != nil {
				sleepCtx(ctx, 2*time.Second)
				continue
			}
			ch, err := ad.Subscribe(ctx)
			if err != nil {
				sleepCtx(ctx, 2*time.Second)
				continue
			}
			tick := time.NewTicker(s.watchCfg().TrackTick)
		inner:
			for {
				select {
				case <-ctx.Done():
					tick.Stop()
					return
				case st, ok := <-ch:
					if !ok {
						break inner
					}
					s.observe(ctx, id, st)
				case <-tick.C:
					if st, err := ad.Status(ctx); err == nil {
						s.observe(ctx, id, st)
					}
				}
				if cur, err := s.GetJob(ctx, id); err != nil || cur.State != contracts.PrintPrinting {
					tick.Stop()
					return
				}
			}
			tick.Stop()
		}
	}()
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// PrintStatus : impression + état de l'imprimante.
type PrintStatus struct {
	Job      Job            `json:"job"`
	Progress *jobs.Progress `json:"progress,omitempty" jsonschema:"avancement de la phase en cours : pourcentage, phase, octets, durée restante, position en file"`
	Printer  *PrinterStatus `json:"printer,omitempty"`
	// Stages : ce que l'imprimante a RÉELLEMENT exécuté pour cette impression (étapes vues, avec l'heure locale).
	Stages *StageSummary `json:"stage_report,omitempty" jsonschema:"étapes réellement exécutées d'après l'étape courante transmise par l'imprimante (mise à niveau, chauffe, nettoyage, calibrations) avec l'heure de première observation"`
	Wait   *WaitInfo     `json:"wait,omitempty" jsonschema:"présent quand wait_seconds ou wait_for_print_change a attendu : ce qui a changé, ou échéance"`
	// Alert : « bloquée : … », « attend une action à l'écran : <texte HMS> » ou « injoignable… » : à signaler tout de suite à l'utilisateur.
	Alert string `json:"alert,omitempty" jsonschema:"situation à signaler : impression bloquée, attente d'une action à l'écran (texte HMS décodé), imprimante injoignable"`
	// Causes : codes d'erreur ACTIFS (print_error, HMS rapportés encore par l'imprimante à la dernière observation), horodatés.
	// CausesHistory : codes vus pendant l'impression mais périmés (l'imprimante ne les rapporte plus), horodatés : ce n'est pas
	// une cause en cours (FB3).
	Causes        []Cause `json:"causes,omitempty" jsonschema:"codes d'erreur ACTIFS maintenant, décodés (texte, gravité, consigne, lien wiki) avec première et dernière observation ; vide si hms_count et print_error sont à 0"`
	CausesHistory []Cause `json:"causes_history,omitempty" jsonschema:"historique : codes d'erreur vus pendant l'impression mais plus rapportés par l'imprimante (périmés), horodatés ; pas une cause en cours"`
	Note          string  `json:"note,omitempty"`
}

// GetPrintStatus relit l'impression et, si elle est active, rafraîchit depuis l'imprimante.
func (s *Service) GetPrintStatus(ctx context.Context, id string) (*PrintStatus, error) {
	j, err := s.GetJob(ctx, id)
	if err != nil {
		return nil, err
	}
	out := &PrintStatus{}
	var curSt *printer.Status
	j = s.CheckConsistency(ctx, j)
	switch j.State {
	case contracts.PrintPrinting, contracts.PrintStartRequested, contracts.PrintStartUnknown, contracts.PrintUploaded, contracts.PrintAwaitingConfirmation:
		if ps, err := s.GetPrinterStatus(ctx, j.PrinterID); err == nil {
			out.Printer = ps
		}
	}
	if j.State == contracts.PrintPrinting {
		if ad, _, err := s.adapter(ctx, j.PrinterID); err == nil {
			if st, err := ad.Status(ctx); err == nil {
				s.observe(ctx, id, st)
				curSt = &st
			}
		}
		s.track(id)
		j, _ = s.GetJob(ctx, id)
		if j.Print != nil && !j.Print.MatchesFile {
			out.Note = "Le rapport de l'imprimante ne désigne pas ce fichier : état de l'impression non confirmé (imprimante occupée par autre chose ou réinitialisée) ; utiliser reconcile_print si besoin."
		}
	}
	if j.State == contracts.PrintStartUnknown {
		if cur, ok := s.passiveReconcile(ctx, j); ok {
			j = cur
			out.Note = "Réconciliation automatique (lecture seule) : l'imprimante montre ce fichier en cours, l'impression est suivie. Aucune commande n'a été renvoyée."
		} else {
			out.Note = "Issue du lancement inconnue : ne pas relancer ; le service réconcilie seul dès que l'imprimante montre ce fichier en cours ; reconcile_print lit l'état réel."
		}
	}
	out.Job = j
	if j.Print != nil {
		out.Alert = alertSentence(j.Print)
		out.Causes, out.CausesHistory = j.Print.causes()
		if len(j.Print.ErrorLog) == 0 { // impression enregistrée avant l'historique horodaté : codes vus, sans heure
			for _, e := range j.Print.ErrorDetails {
				out.CausesHistory = append(out.CausesHistory, Cause{ErrorInfo: e})
			}
		}
	}
	out.Progress = s.ProgressFor(ctx, j)
	out.Stages = stageSummary(j, curSt)
	return out, nil
}

// ---- réconciliation ----

// ReconcileOutput est le verdict de reconcile_print.
type ReconcileOutput struct {
	PrintJobID string               `json:"print_job_id"`
	Outcome    string               `json:"outcome" jsonschema:"started | not_started | unknown"`
	Reason     string               `json:"reason"`
	State      contracts.PrintState `json:"state"`
	Note       string               `json:"note"`
	Job        Job                  `json:"job"`
}

// Reconcile lit l'état réel après un START_UNKNOWN. N'envoie JAMAIS de commande de lancement.
func (s *Service) Reconcile(ctx context.Context, id string) (*ReconcileOutput, error) {
	j, err := s.GetJob(ctx, id)
	if err != nil {
		return nil, err
	}
	if j.State != contracts.PrintStartUnknown {
		return nil, fmt.Errorf("%w : la réconciliation s'applique à START_UNKNOWN (état actuel %s)", ErrState, j.State)
	}
	ad, _, err := s.adapter(ctx, j.PrinterID)
	if err != nil {
		return nil, err
	}
	rc, ok := ad.(printer.Reconciler)
	if !ok {
		return nil, fmt.Errorf("%w : cet adaptateur ne sait pas réconcilier", ErrUnavailable)
	}
	rctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	r, err := rc.Reconcile(rctx, printer.StartExpectation{Remote: *j.Remote, Since: startRequestedAt(j)})
	if err != nil {
		return nil, err
	}
	out := &ReconcileOutput{PrintJobID: j.ID, Outcome: string(r.Outcome), Reason: r.Reason}
	switch r.Outcome {
	case printer.ReconcileStarted:
		j, err = s.transition(ctx, id, contracts.EvReconcilePrinting, "réconciliation : "+r.Reason, func(x *Job) error { x.LastError = ""; return nil })
		if err == nil {
			s.observe(ctx, id, r.Status)
			s.track(id)
			out.Note = "L'impression tourne bien : suivi repris."
		}
	case printer.ReconcileNotStarted:
		j, err = s.transition(ctx, id, contracts.EvReconcileNotStarted, "réconciliation : "+r.Reason, func(x *Job) error { x.ConfirmationID = ""; return nil })
		out.Note = "Rien n'a démarré. Le fichier est toujours sur l'imprimante (UPLOADED) ; un nouveau lancement exige une NOUVELLE confirmation humaine (request_print_confirmation)."
	default:
		out.Note = "Impossible de conclure (" + r.Reason + ") : l'état reste START_UNKNOWN. Vérifier l'imprimante, puis réessayer reconcile_print. Aucun renvoi automatique."
	}
	if err != nil {
		return nil, err
	}
	cur, _ := s.GetJob(ctx, id)
	out.State, out.Job = cur.State, cur
	return out, nil
}

// startRequestedAt : instant du dernier passage en START_REQUESTED (repère de la réconciliation).
func startRequestedAt(j Job) time.Time {
	for i := len(j.Events) - 1; i >= 0; i-- {
		if j.Events[i].To == contracts.PrintStartRequested {
			return j.Events[i].At
		}
	}
	return time.Time{}
}

// ---- annulation ----

// CancelInput est l'entrée de cancel_print. Il n'y a PAS d'identifiant d'arrêt à fournir : arrêter une
// impression en cours exige la validation d'un humain sur la page /stop/{id}.
type CancelInput struct {
	PrintJobID string `json:"print_job_id"`
}

// CancelOutput : résultat explicite.
type CancelOutput struct {
	PrintJobID         string               `json:"print_job_id"`
	Outcome            string               `json:"outcome" jsonschema:"cancelled | stop_requested | stopped | refused"`
	State              contracts.PrintState `json:"state"`
	StopURL            string               `json:"stop_url,omitempty" jsonschema:"page que l'HUMAIN ouvre (navigateur enregistré) pour arrêter l'impression ; rien n'est arrêté tant qu'il n'a pas validé"`
	StopExpiresAt      time.Time            `json:"stop_expires_at,omitzero"`
	StopExpiresAtLocal string               `json:"stop_expires_at_local,omitempty" jsonschema:"même instant, lisible, dans le fuseau de l utilisateur"`
	Question           string               `json:"question,omitempty"`
	Note               string               `json:"note"`
	Job                Job                  `json:"job"`
}

// CancelPrint annule selon la phase. En PRINTING : CRÉE une demande d'arrêt et renvoie l'URL /stop/{id} ;
// l'arrêt n'a lieu qu'après validation humaine sur cette page (le modèle ne peut pas le provoquer seul).
func (s *Service) CancelPrint(ctx context.Context, in CancelInput) (*CancelOutput, error) {
	j, err := s.GetJob(ctx, in.PrintJobID)
	if err != nil {
		return nil, err
	}
	out := &CancelOutput{PrintJobID: j.ID}
	finish := func(o, note string) (*CancelOutput, error) {
		cur, _ := s.GetJob(ctx, j.ID)
		out.Outcome, out.Note, out.State, out.Job = o, note, cur.State, cur
		return out, nil
	}
	switch j.State {
	case contracts.PrintCompleted, contracts.PrintFailed, contracts.PrintCancelled, contracts.PrintStartAbortedBeforeSend:
		return finish("refused", "L'impression est déjà terminée ("+string(j.State)+") : rien n'a été modifié.")
	case contracts.PrintStartRequested, contracts.PrintStartUnknown:
		return finish("refused", "Le lancement est en cours ou son issue est inconnue : annuler maintenant pourrait laisser une impression tourner. Utiliser reconcile_print puis, si elle tourne, cancel_print (l'arrêt est alors validé par l'humain).")
	case contracts.PrintPrinting:
		id, exp := s.requestStop(j.ID)
		out.StopURL, out.StopExpiresAt = s.BaseURL()+"/stop/"+id, exp
		out.StopExpiresAtLocal = localtime.Full(exp)
		out.Question = fmt.Sprintf("Veux-tu vraiment arrêter l'impression de %s en cours sur l'imprimante ? Le travail en cours sera perdu.", j.Name)
		_ = s.d.Store.Audit(ctx, actor, "print.stop_requested", "print_job", j.ID, map[string]any{"expires": exp})
		s.journal(ctx, j.ID, "stop_requested", "arrêt demandé (cancel_print) : en attente de la validation de l'humain")
		return finish("stop_requested", "Rien n'est arrêté. Demande à l'utilisateur d'ouvrir stop_url dans son navigateur enregistré et de valider l'arrêt ; tu ne peux pas le faire à sa place.")
	}
	// Phases avant le lancement : annulation purement logique (et de la découpe en cours).
	if j.State == contracts.PrintSlicing && j.SliceJobID != "" {
		_, _ = s.d.Queue.RequestCancel(ctx, j.SliceJobID)
	}
	if _, err := s.transition(ctx, j.ID, contracts.EvCancel, "annulation avant lancement", nil); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if j.ConfirmationID != "" {
		delete(s.approv, j.ConfirmationID)
	}
	s.mu.Unlock()
	return finish("cancelled", "Impression annulée avant tout lancement ; le fichier déposé éventuel reste sur la carte de l'imprimante mais ne sera pas lancé.")
}

// requestStop crée (ou réutilise, tant qu'elle est valide) la demande d'arrêt d'une impression.
func (s *Service) requestStop(jobID string) (string, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeLocked()
	for id, r := range s.stops {
		if r.jobID == jobID {
			return id, r.expires
		}
	}
	id := newStopID()
	exp := s.now().Add(stopTTL)
	s.stops[id] = stopReq{jobID: jobID, expires: exp}
	return id, exp
}

// StopView : ce que la page /stop/{id} montre à l'humain.
type StopView struct {
	StopID         string    `json:"stop_id"`
	PrintJobID     string    `json:"print_job_id"`
	File           string    `json:"file"`
	PrinterName    string    `json:"printer_name"`
	Percent        int       `json:"percent"`
	Layer          int       `json:"layer"`
	TotalLayers    int       `json:"total_layers"`
	ExpiresAt      time.Time `json:"expires_at"`
	ExpiresAtLocal string    `json:"expires_at_local" jsonschema:"même instant, lisible, dans le fuseau de l utilisateur"`
}

// GetStop relit une demande d'arrêt valide (sans la consommer).
func (s *Service) GetStop(ctx context.Context, stopID string) (*StopView, error) {
	stopID, r, why := s.lookupStop(stopID)
	if why != "" {
		return nil, fmt.Errorf("%w : %s", ErrNotFound, why)
	}
	j, err := s.GetJob(ctx, r.jobID)
	if err != nil {
		return nil, err
	}
	if j.State != contracts.PrintPrinting {
		return nil, fmt.Errorf("%w : %s", ErrState, stopGoneMessage("finished", j.State))
	}
	rec, _ := s.GetPrinterRecord(ctx, j.PrinterID)
	v := &StopView{StopID: stopID, PrintJobID: j.ID, File: j.Name, PrinterName: rec.Name, ExpiresAt: r.expires, ExpiresAtLocal: localtime.Full(r.expires)}
	if j.Print != nil {
		v.Percent, v.Layer, v.TotalLayers = j.Print.Percent, j.Print.Layer, j.Print.TotalLayers
	}
	return v, nil
}

// Recover traite l'impression laissée par un arrêt du service : un lancement en cours
// devient START_UNKNOWN (jamais rejoué) et le suivi des impressions actives reprend.
func (s *Service) Recover(ctx context.Context) {
	all, err := s.ListJobs(ctx, "", 1000)
	if err != nil {
		return
	}
	for _, j := range all {
		switch j.State {
		case contracts.PrintStartRequested:
			_, _ = s.transition(ctx, j.ID, contracts.EvStartLost, "service interrompu pendant le lancement", func(x *Job) error {
				x.LastError = "lancement interrompu par un arrêt du service : reconcile_print"
				return nil
			})
		case contracts.PrintPrinting:
			s.track(j.ID)
		}
	}
}
