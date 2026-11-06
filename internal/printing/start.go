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
	Outcome    string               `json:"outcome" jsonschema:"printing | start_requested | start_unknown | rejected | not_sent | replayed"`
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
	// Rejeu de la même requête : on rend l'état, on ne relance JAMAIS.
	if j.StartJobID != "" {
		if q, err := s.d.Queue.Get(ctx, j.StartJobID); err == nil && q.IdempotencyKey == key {
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
	sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	st, err := ad.Status(sctx)
	cancel()
	if err != nil || !st.Connected || st.Stale || st.State != printer.StateIdle || st.HasError() {
		why := fmt.Sprintf("état %s, connecté=%v, périmé=%v, erreurs=%v", st.State, st.Connected, st.Stale, st.HasError())
		if err != nil {
			why = err.Error()
		}
		return nil, fmt.Errorf("%w : l'imprimante n'est pas prête (%s) — rien n'a été envoyé, la confirmation reste valable", printer.ErrNotReady, why)
	}
	params, _ := json.Marshal(map[string]string{"print_job_id": j.ID, "confirmation_id": in.ConfirmationID})
	qj, created, err := s.d.Queue.Enqueue(ctx, jobs.EnqueueParams{Kind: JobStart, Class: contracts.ClassLight, Params: params, IdempotencyKey: key})
	if err != nil {
		return nil, err
	}
	if created {
		if _, err := s.update(ctx, j.ID, func(x *Job) error { x.StartJobID = qj.ID; return nil }); err != nil {
			return nil, err
		}
	}
	q, done := s.waitQueue(ctx, qj.ID, 100*time.Second)
	cur, _ := s.GetJob(ctx, j.ID)
	out := &StartOutput{PrintJobID: cur.ID, State: cur.State, QueueJobID: qj.ID, Replayed: !created, Job: cur}
	switch {
	case !done:
		out.Outcome, out.Note = "start_requested", "Lancement en cours : suivre avec get_print_status. Ne pas relancer."
	case q.State == contracts.JobFailed && q.Error != nil && q.Error.Code == "not_sent":
		out.Outcome, out.Note = "not_sent", "Rien n'a été envoyé à l'imprimante : "+q.Error.Message
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
	req := printer.StartRequest{
		Remote: *j.Remote, Plate: j.Plate, UseAMS: j.UseAMS, AMSMapping: j.AMSMapping, PlateClear: a.plateClear,
		Options: printer.DefaultStartOptions(),
		Proof: printer.ConfirmationProof{PrinterID: j.PrinterID, SHA256: j.ArtifactSHA256, Plate: j.Plate, AMSMapping: j.AMSMapping,
			Token: proofToken(p.Conf, a.token), ExpiresAt: conf.ExpiresAt},
	}
	jc.Progress(0.3, "commande de lancement")
	serr := ad.Start(ctx, req)
	cur, _ := s.GetJob(bg, j.ID)
	consumed := cur.State != contracts.PrintAwaitingConfirmation
	switch {
	case serr == nil:
		if _, err := s.transition(bg, j.ID, contracts.EvObservedPrinting, "imprimante en préparation/impression observée", nil); err != nil {
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

func (s *Service) rejectStart(ctx context.Context, id string, cause error) {
	msg := "lancement refusé par l'imprimante : " + cause.Error()
	if _, err := s.transition(ctx, id, contracts.EvStartRejected, msg, func(j *Job) error { j.LastError = msg; return nil }); err != nil {
		s.log.Warn("refus non enregistré", "print_job", id, "err", err)
	}
}

// ---- suivi ----

func stemOf(name string) string {
	n := strings.TrimSuffix(name, ".3mf")
	return strings.TrimSuffix(n, ".gcode")
}

func matches(st printer.Status, remote string) bool {
	stem := stemOf(remote)
	return stem != "" && (st.SubtaskName == stem || strings.Contains(st.GcodeFile, stem))
}

// observe applique un état d'imprimante à une impression PRINTING.
func (s *Service) observe(ctx context.Context, id string, st printer.Status) {
	j, err := s.GetJob(ctx, id)
	if err != nil || j.State != contracts.PrintPrinting || j.Remote == nil {
		return
	}
	m := matches(st, j.Remote.Name)
	pg := &PrintProgress{PrinterState: string(st.State), Percent: st.Progress, Layer: st.Layer, TotalLayers: st.TotalLayers,
		RemainingMin: st.RemainingMin, ObservedAt: s.now(), MatchesFile: m, PrinterReason: st.FailReason}
	if !m {
		if j.Print == nil || j.Print.MatchesFile || j.Print.PrinterState != pg.PrinterState {
			_, _ = s.update(ctx, id, func(x *Job) error { x.Print = pg; return nil })
		}
		return
	}
	switch st.State {
	case printer.StateFinished:
		_, _ = s.transition(ctx, id, contracts.EvPrintDone, "l'imprimante rapporte la fin de l'impression (FINISH)", func(x *Job) error { pg.Percent = 100; x.Print = pg; return nil })
	case printer.StateFailed:
		msg := fmt.Sprintf("l'imprimante rapporte un échec (%s, print_error=%d)", st.FailReason, st.PrintError)
		_, _ = s.transition(ctx, id, contracts.EvFail, msg, func(x *Job) error { x.Print, x.LastError = pg, msg; return nil })
	default:
		if j.Print == nil || j.Print.Percent != pg.Percent || j.Print.PrinterState != pg.PrinterState || j.Print.Layer != pg.Layer {
			_, _ = s.update(ctx, id, func(x *Job) error { x.Print = pg; return nil })
		}
	}
}

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
			tick := time.NewTicker(2 * time.Second)
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
	Job     Job            `json:"job"`
	Printer *PrinterStatus `json:"printer,omitempty"`
	Note    string         `json:"note,omitempty"`
}

// GetPrintStatus relit l'impression et, si elle est active, rafraîchit depuis l'imprimante.
func (s *Service) GetPrintStatus(ctx context.Context, id string) (*PrintStatus, error) {
	j, err := s.GetJob(ctx, id)
	if err != nil {
		return nil, err
	}
	out := &PrintStatus{}
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
			}
		}
		s.track(id)
		j, _ = s.GetJob(ctx, id)
		if j.Print != nil && !j.Print.MatchesFile {
			out.Note = "Le rapport de l'imprimante ne désigne pas ce fichier : état de l'impression non confirmé (imprimante occupée par autre chose ou réinitialisée) ; utiliser reconcile_print si besoin."
		}
	}
	if j.State == contracts.PrintStartUnknown {
		out.Note = "Issue du lancement inconnue : ne pas relancer ; reconcile_print lit l'état réel."
	}
	out.Job = j
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
	r, err := rc.Reconcile(rctx, printer.StartExpectation{Remote: *j.Remote})
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

// ---- annulation ----

// CancelInput est l'entrée de cancel_print.
type CancelInput struct {
	PrintJobID string `json:"print_job_id"`
	// StopConfirmationID : seulement pour arrêter une impression EN COURS. Obtenu dans la
	// réponse d'un premier appel ; l'utilisateur doit avoir confirmé l'arrêt.
	StopConfirmationID string `json:"stop_confirmation_id,omitempty"`
}

// CancelOutput : résultat explicite.
type CancelOutput struct {
	PrintJobID         string               `json:"print_job_id"`
	Outcome            string               `json:"outcome" jsonschema:"cancelled | needs_stop_confirmation | stopped | refused"`
	State              contracts.PrintState `json:"state"`
	StopConfirmationID string               `json:"stop_confirmation_id,omitempty"`
	Question           string               `json:"question,omitempty"`
	Note               string               `json:"note"`
	Job                Job                  `json:"job"`
}

// CancelPrint annule selon la phase. En PRINTING : arrêt de l'imprimante après une
// confirmation séparée (deux appels : identifiant d'arrêt, puis confirmation).
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
	case contracts.PrintCompleted, contracts.PrintFailed, contracts.PrintCancelled:
		return finish("refused", "L'impression est déjà terminée ("+string(j.State)+") : rien n'a été modifié.")
	case contracts.PrintStartRequested, contracts.PrintStartUnknown:
		return finish("refused", "Le lancement est en cours ou son issue est inconnue : annuler maintenant pourrait laisser une impression tourner. Utiliser reconcile_print puis, si elle tourne, cancel_print avec arrêt confirmé.")
	case contracts.PrintPrinting:
		if in.StopConfirmationID == "" || !s.takeStop(in.StopConfirmationID, j.ID) {
			id := "stop_" + randomNonce()[:16]
			s.mu.Lock()
			s.stops[id] = j.ID
			s.mu.Unlock()
			out.StopConfirmationID = id
			out.Question = fmt.Sprintf("Veux-tu vraiment arrêter l'impression de %s en cours sur l'imprimante ? Le travail en cours sera perdu.", j.Name)
			return finish("needs_stop_confirmation", "Arrêt d'une impression en cours : poser la question à l'utilisateur ; s'il confirme, rappeler cancel_print avec stop_confirmation_id.")
		}
		ad, _, err := s.adapter(ctx, j.PrinterID)
		if err != nil {
			return nil, err
		}
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := ad.Stop(cctx); err != nil {
			return nil, fmt.Errorf("arrêt de l'imprimante en échec : %w", err)
		}
		_, err = s.transition(ctx, j.ID, contracts.EvCancel, "arrêt demandé par l'utilisateur sur l'imprimante", nil)
		if err != nil {
			return nil, err
		}
		_ = s.d.Store.Audit(ctx, actor, "print.stopped", "print_job", j.ID, nil)
		return finish("stopped", "Commande d'arrêt acceptée par l'imprimante.")
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

func (s *Service) takeStop(stopID, jobID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stops[stopID] == jobID {
		delete(s.stops, stopID)
		return true
	}
	return false
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
