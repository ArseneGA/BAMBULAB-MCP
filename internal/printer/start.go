package printer

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// remoteStem renvoie le nom sans extension .gcode.3mf / .3mf : c'est le
// subtask_name envoyé à l'imprimante et celui qu'on retrouve dans le rapport.
func remoteStem(name string) string {
	n := strings.TrimSuffix(name, ".3mf")
	return strings.TrimSuffix(n, ".gcode")
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// checkProofBinding vérifie, hors toute I/O, que la preuve est présente,
// non expirée et liée à cette imprimante, ce fichier, ce plateau, ces matières.
func (b *Bambu) checkProofBinding(req StartRequest) error {
	if b.cfg.Verifier == nil {
		return fmt.Errorf("%w: no verifier configured", ErrNoProof)
	}
	p := req.Proof
	if p.Token == "" || p.SHA256 == "" || p.PrinterID == "" {
		return ErrNoProof
	}
	if !p.ExpiresAt.After(b.cfg.Now()) {
		return ErrProofExpired
	}
	if p.PrinterID != b.cfg.PrinterID {
		return fmt.Errorf("%w: printer", ErrProofMismatch)
	}
	if !strings.EqualFold(p.SHA256, req.Remote.SHA256) {
		return fmt.Errorf("%w: sha256", ErrProofMismatch)
	}
	if p.Plate != req.Plate {
		return fmt.Errorf("%w: plate", ErrProofMismatch)
	}
	if !equalInts(p.AMSMapping, req.AMSMapping) {
		return fmt.Errorf("%w: material mapping", ErrProofMismatch)
	}
	if !req.PlateClear {
		return ErrPlateNotClear
	}
	if req.Remote.Name == "" || req.Plate < 1 {
		return errors.New("printer: remote name and plate (>=1) are required")
	}
	for _, v := range req.AMSMapping {
		if v < -1 || v > 253 {
			return fmt.Errorf("printer: ams_mapping value %d out of range", v)
		}
	}
	return nil
}

func (b *Bambu) projectFile(seq string, req StartRequest) map[string]any {
	o := req.Options
	if o == (StartOptions{}) {
		o = DefaultStartOptions()
	}
	if o.BedType == "" {
		o.BedType = "auto"
	}
	mapping := []int{}
	if req.UseAMS {
		mapping = append(mapping, req.AMSMapping...)
	}
	url := strings.ReplaceAll(b.cfg.StartURLTemplate, "{name}", req.Remote.Name)
	return map[string]any{"print": map[string]any{
		"sequence_id":    seq,
		"command":        "project_file",
		"param":          fmt.Sprintf("Metadata/plate_%d.gcode", req.Plate),
		"project_id":     "0",
		"profile_id":     "0",
		"task_id":        "0",
		"subtask_id":     "0",
		"subtask_name":   remoteStem(req.Remote.Name),
		"file":           "",
		"url":            url,
		"md5":            "",
		"timelapse":      o.Timelapse,
		"bed_type":       o.BedType,
		"bed_leveling":   o.BedLeveling,
		"bed_levelling":  o.BedLeveling, // orthographe de la note de recherche ; les deux sont envoyées (hypothèse J7)
		"flow_cali":      o.FlowCali,
		"vibration_cali": o.VibrationCali,
		"layer_inspect":  o.LayerInspect,
		"use_ams":        req.UseAMS,
		"ams_mapping":    mapping,
	}}
}

// Start lance une impression. Il ne renvoie nil qu'après avoir observé
// PREPARE/RUNNING. Toute incertitude après publication donne *StartUnknownError.
func (b *Bambu) Start(ctx context.Context, req StartRequest) error {
	// 1. Preuve (liaison locale) : aucun effet de bord, aucune I/O.
	if err := b.checkProofBinding(req); err != nil {
		return err
	}
	// 2. Réservation : un seul Start à la fois.
	if !b.startMu.TryLock() {
		return ErrBusy
	}
	defer b.startMu.Unlock()

	// 3. Conditions imprimante : connectée, fraîche, idle, sans erreur.
	c := b.current()
	if c == nil {
		return ErrNotConnected
	}
	st, err := b.ensureFresh(ctx, c, b.cfg.MaxStartStatusAge)
	if err != nil {
		return err
	}
	okState := st.State == StateIdle || (b.cfg.AllowStartFromFinish && st.State == StateFinished)
	if !okState {
		return fmt.Errorf("%w: state is %s", ErrNotReady, st.State)
	}
	if st.HasError() {
		return fmt.Errorf("%w: print_error=%d hms=%d", ErrPrinterError, st.PrintError, len(st.HMS))
	}
	// 4. Le fichier est bien sur l'imprimante, à la bonne taille (aucun effet physique).
	if err := b.checkRemote(ctx, req.Remote); err != nil {
		return err
	}
	// 5. Vérification du jeton (peut le consommer) en dernier avant l'envoi.
	if err := b.cfg.Verifier(ctx, req.Proof); err != nil {
		return fmt.Errorf("%w: %v", ErrNoProof, err)
	}
	// 6. Envoi. À partir d'ici, toute incertitude => StartUnknown.
	seq := b.nextSeq()
	respCh := b.registerWaiter(seq)
	defer b.dropWaiter(seq)
	stage := StageTransport
	if err := b.publishAcked(ctx, c, b.projectFile(seq, req)); err != nil {
		return &StartUnknownError{Stage: StageTransport, Cause: err}
	}
	stage = StageAcked
	b.cfg.Logger.Info("project_file published", "printer", b.cfg.PrinterID, "seq", seq, "file", req.Remote.Name)

	// 7. Observation : acceptation (réponse) puis état.
	err = b.waitEval(ctx, c, respCh, b.cfg.StartObserveTimeout, func(resp map[string]any, s Status) (bool, error) {
		if resp != nil {
			if r := str(resp["result"]); r != "" && !strings.EqualFold(r, "success") {
				return false, fmt.Errorf("%w: %s %s", ErrStartRejected, r, str(resp["reason"]))
			}
			stage = StageAccepted
		}
		if !s.Connected {
			return false, nil
		}
		switch s.State {
		case StatePreparing, StatePrinting, StateFinished:
			if s.State == StateFinished && !b.cfg.AllowStartFromFinish && stage != StageAccepted {
				return false, nil
			}
			return true, nil
		case StateFailed:
			if stage == StageAccepted {
				return false, fmt.Errorf("%w: printer failed after accept: %s (print_error=%d)", ErrStartRejected, s.FailReason, s.PrintError)
			}
		}
		return false, nil
	})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrStartRejected):
		return err
	default:
		return &StartUnknownError{Stage: stage, Cause: err}
	}
}

// Reconcile interroge l'imprimante après un StartUnknown et dit si
// l'impression attendue a démarré. Il n'envoie JAMAIS de commande de lancement.
func (b *Bambu) Reconcile(ctx context.Context, exp StartExpectation) (Reconciliation, error) {
	// Attend (borné par ctx) que la session soit rétablie.
	var c *conn
	for {
		ch := b.changedCh()
		if c = b.current(); c != nil {
			break
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return Reconciliation{Outcome: ReconcileUnknown, Status: b.snapshot(), Reason: "printer unreachable"}, nil
		}
	}
	st, err := b.ensureFresh(ctx, c, b.cfg.MaxStartStatusAge)
	if err != nil {
		return Reconciliation{Outcome: ReconcileUnknown, Status: st, Reason: err.Error()}, nil
	}
	stem := remoteStem(exp.Remote.Name)
	match := stem != "" && (st.SubtaskName == stem || strings.Contains(st.GcodeFile, stem))
	r := Reconciliation{Status: st}
	switch st.State {
	case StatePreparing, StatePrinting, StatePaused, StateFinished, StateFailed:
		if match && (st.State == StateFinished || st.State == StateFailed) {
			// Un FINISH/FAILED du même fichier peut venir d'un tirage antérieur.
			r.Outcome, r.Reason = ReconcileUnknown, "same file reported "+string(st.State)+"; cannot tell whether this run or an earlier one"
		} else if match {
			r.Outcome, r.Reason = ReconcileStarted, "printer reports the expected job in state "+string(st.State)
		} else if st.State == StateFinished || st.State == StateFailed {
			r.Outcome, r.Reason = ReconcileNotStarted, "printer reports a different (previous) job"
		} else {
			r.Outcome, r.Reason = ReconcileUnknown, "printer is busy with a different job"
		}
	case StateIdle:
		r.Outcome, r.Reason = ReconcileNotStarted, "printer idle, no trace of the expected job"
	default:
		r.Outcome, r.Reason = ReconcileUnknown, "state "+string(st.State)
	}
	return r, nil
}
