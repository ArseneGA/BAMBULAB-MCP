package agentv2

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

// HubLink est le transport « via agent » d'une imprimante : il implémente printer.Link en relayant chaque opération
// vers l'agent appairé (ordres en long-poll). Il ne connaît jamais le code d'accès LAN.
type HubLink struct {
	h         *Hub
	printerID string
	agentID   string
	verifier  printer.ProofVerifier
}

var _ printer.Link = (*HubLink)(nil)

// Link construit le transport de l'imprimante printerID servie par l'agent agentID. verifier est celui du service
// d'impression : il consomme la confirmation humaine à usage unique juste avant la signature du lancement.
func (h *Hub) Link(printerID, agentID string, verifier printer.ProofVerifier) *HubLink {
	return &HubLink{h: h, printerID: printerID, agentID: agentID, verifier: verifier}
}

// Kind : agent.
func (l *HubLink) Kind() printer.LinkKind { return printer.LinkAgent }

// Status renvoie la dernière télémétrie de l'agent (horodatée sur l'horloge du serveur). Hors ligne : état offline.
func (l *HubLink) Status(context.Context) (printer.Status, error) {
	return l.h.statusOf(l.agentID, l.printerID), nil
}

// Subscribe livre les états au fil de la télémétrie.
func (l *HubLink) Subscribe(ctx context.Context) (<-chan printer.Status, error) {
	k := teleKey(l.agentID, l.printerID)
	s := &sub{ch: make(chan printer.Status, 16)}
	l.h.mu.Lock()
	if l.h.subs[k] == nil {
		l.h.subs[k] = map[*sub]struct{}{}
	}
	l.h.subs[k][s] = struct{}{}
	l.h.mu.Unlock()
	s.ch <- l.h.statusOf(l.agentID, l.printerID)
	out := make(chan printer.Status, 16)
	go func() {
		defer func() {
			l.h.mu.Lock()
			delete(l.h.subs[k], s)
			l.h.mu.Unlock()
			close(out)
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case v := <-s.ch:
				select {
				case out <- v:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

func (l *HubLink) online() error {
	if !l.h.Online(l.agentID) {
		return fmt.Errorf("%w: l'agent local de cette imprimante est hors ligne", printer.ErrNotConnected)
	}
	return nil
}

// roundTrip envoie un ordre non physique et attend son résultat.
func (l *HubLink) roundTrip(ctx context.Context, o Order, wait time.Duration) (OrderResult, error) {
	if err := l.online(); err != nil {
		return OrderResult{}, err
	}
	o.PrinterID = l.printerID
	p := l.h.enqueue(l.agentID, o)
	res, ok := l.h.await(ctx, p, wait)
	if !ok {
		if !l.h.withdraw(l.agentID, p.id) { // jamais remis : retiré ; sinon l'agent l'a et son résultat sera ignoré
			l.h.forget(p.id)
		}
		if ctx.Err() != nil {
			return OrderResult{}, ctx.Err()
		}
		return OrderResult{}, fmt.Errorf("%w: l'agent n'a pas répondu en %s", printer.ErrNotConnected, wait)
	}
	l.h.forget(p.id)
	return res, nil
}

func resultErr(res OrderResult) error {
	if res.State == ResultOK {
		return nil
	}
	msg := res.Message
	if msg == "" {
		msg = res.Code
	}
	switch res.Code {
	case CodeReadOnly:
		return fmt.Errorf("%w: agent en lecture seule (kill-switch local) : %s", printer.ErrNotReady, msg)
	case CodeNotReady:
		return fmt.Errorf("%w: %s", printer.ErrNotReady, msg)
	case CodeBusy:
		return fmt.Errorf("%w: %s", printer.ErrBusy, msg)
	}
	return fmt.Errorf("printer: agent: %s (%s)", msg, res.Code)
}

// Upload dépose le fichier : le serveur le met en transit (empreinte et taille vérifiées), l'agent le tire par
// morceaux (Range), revérifie l'empreinte puis le dépose en FTPS sur l'imprimante.
func (l *HubLink) Upload(ctx context.Context, artefact io.Reader, name string, size int64, sha string) (printer.RemoteRef, error) {
	sha = strings.ToLower(sha)
	if len(sha) != 64 {
		return printer.RemoteRef{}, fmt.Errorf("printer: sha256 must be 64 hex chars")
	}
	if size < 0 {
		return printer.RemoteRef{}, fmt.Errorf("printer: negative size")
	}
	if err := l.online(); err != nil {
		return printer.RemoteRef{}, err
	}
	fid, _, err := l.h.stage(l.agentID, artefact, size, sha)
	if err != nil {
		return printer.RemoteRef{}, err
	}
	defer l.h.unstage(fid)
	wait := 5*time.Minute + time.Duration(size/(256<<10))*time.Second // ~256 Kio/s au pire (liaison domestique + FTPS)
	res, err := l.roundTrip(ctx, Order{Kind: OrderPutFile, File: &FileSpec{ID: fid, Name: name, Size: size, SHA256: sha}}, wait)
	if err != nil {
		return printer.RemoteRef{}, err
	}
	if err := resultErr(res); err != nil {
		return printer.RemoteRef{}, err
	}
	if res.Remote == nil || res.Remote.SHA256 != sha {
		return printer.RemoteRef{}, fmt.Errorf("%w: l'agent n'a pas confirmé le dépôt de ce fichier", printer.ErrUploadMismatch)
	}
	return *res.Remote, nil
}

func (l *HubLink) startWait(size int64) time.Duration {
	if l.h.cfg.StartWait > 0 {
		return l.h.cfg.StartWait
	}
	w := 3*time.Minute + time.Duration(size/(1<<20))*time.Second
	if w > 20*time.Minute {
		w = 20 * time.Minute
	}
	return w
}

// Start lance une impression, une seule fois.
//
// Le serveur vérifie la preuve de confirmation, contrôle l'état connu, consomme la confirmation, puis signe une
// autorisation (Ed25519) liée à imprimante + empreinte du fichier + plateau + matières + options, valable deux
// minutes, à nonce unique. Il remet UN ordre « start » ; l'agent revérifie tout et envoie la commande une fois.
// Jamais de renvoi : si l'issue est indéterminée, *printer.StartUnknownError (règle START_UNKNOWN) et seule une
// réconciliation par lecture d'état peut conclure.
func (l *HubLink) Start(ctx context.Context, req printer.StartRequest) error {
	now := l.h.now()
	if err := printer.CheckProofBinding(l.printerID, l.verifier, now, req); err != nil {
		return err
	}
	if err := l.online(); err != nil {
		return err
	}
	if l.h.AgentReadOnly(l.agentID, l.printerID) {
		return fmt.Errorf("%w: l'agent est en lecture seule (kill-switch local) : aucun lancement possible", printer.ErrNotReady)
	}
	// Comme l'adaptateur direct : l'état doit être frais et lançable AVANT de consommer la confirmation.
	if err := printer.CheckLaunchable(l.h.statusOf(l.agentID, l.printerID), 30*time.Second, now, printer.FullStartPolicy); err != nil {
		return err
	}
	if err := l.verifier(ctx, req.Proof); err != nil {
		return fmt.Errorf("%w: %v", printer.ErrNoProof, err)
	}
	// À partir d'ici la confirmation est consommée.
	signer, err := l.h.Signer()
	if err != nil {
		return fmt.Errorf("%w: clé de signature indisponible : %v", printer.ErrStartRejected, err)
	}
	issued := l.h.cfg.AuthClock().UTC()
	o := req.Options
	if o == (printer.StartOptions{}) {
		o = printer.DefaultStartOptions()
	}
	if o.BedType == "" {
		o.BedType = "auto"
	}
	p := AuthPayload{V: 1, ID: NewNonce(), KeyID: signer.KeyID(), AgentID: l.agentID, PrinterID: l.printerID, SHA256: strings.ToLower(req.Remote.SHA256),
		Plate: req.Plate, UseAMS: req.UseAMS, AMSMapping: append([]int(nil), req.AMSMapping...), PlateClear: req.PlateClear,
		Options:  StartOptionsJSON{Timelapse: o.Timelapse, BedLeveling: o.BedLeveling, FlowCali: o.FlowCali, VibrationCali: o.VibrationCali, LayerInspect: o.LayerInspect, BedType: o.BedType},
		IssuedAt: issued, ExpiresAt: issued.Add(AuthTTL)}
	signed, err := signer.Sign(p)
	if err != nil {
		return fmt.Errorf("%w: signature impossible : %v", printer.ErrStartRejected, err)
	}
	l.h.auditIssued(p)

	pd := l.h.enqueue(l.agentID, Order{Kind: OrderStart, PrinterID: l.printerID, Auth: &signed})
	wait := l.startWait(req.Remote.Size)
	res, ok := l.h.await(ctx, pd, wait)
	if !ok {
		if l.h.withdraw(l.agentID, pd.id) { // jamais remis à l'agent : rien n'a pu partir
			l.h.auditResult(p.ID, "withdrawn", "")
			return fmt.Errorf("%w: l'agent n'a pas pris l'ordre de lancement, rien n'a été envoyé à l'imprimante", printer.ErrStartRejected)
		}
		l.h.forget(pd.id)
		l.h.auditResult(p.ID, ResultUnknown, "no_result")
		cause := errors.New("aucun résultat de l'agent (délai ou connexion perdue) : la commande a peut-être été envoyée")
		if ctx.Err() != nil {
			cause = ctx.Err()
		}
		return &printer.StartUnknownError{Stage: printer.StageTransport, Cause: cause}
	}
	l.h.forget(pd.id)
	l.h.auditResult(p.ID, res.State, res.Code)
	switch res.State {
	case ResultStarted:
		return nil
	case ResultRejected:
		return fmt.Errorf("%w: %s", printer.ErrStartRejected, res.Message)
	case ResultRefused:
		// Refus AVANT toute commande (autorisation, lecture seule, empreinte...) : rien n'a été envoyé.
		return fmt.Errorf("%w: refus de l'agent (%s) : %s — aucune commande n'a été envoyée", printer.ErrStartRejected, res.Code, res.Message)
	case ResultUnknown:
		return &printer.StartUnknownError{Stage: printer.StartStage(res.Stage), Cause: errors.New(res.Message)}
	}
	return &printer.StartUnknownError{Stage: printer.StageTransport, Cause: fmt.Errorf("résultat inattendu de l'agent : %s %s", res.State, res.Message)}
}

func (l *HubLink) control(ctx context.Context, cmd string) error {
	res, err := l.roundTrip(ctx, Order{Kind: OrderControl, Cmd: cmd}, 45*time.Second)
	if err != nil {
		return err
	}
	return resultErr(res)
}

// Pause met l'impression en pause.
func (l *HubLink) Pause(ctx context.Context) error { return l.control(ctx, CmdPause) }

// Resume reprend l'impression.
func (l *HubLink) Resume(ctx context.Context) error { return l.control(ctx, CmdResume) }

// Stop arrête l'impression.
func (l *HubLink) Stop(ctx context.Context) error { return l.control(ctx, CmdStop) }

// Snapshot demande une image JPEG de la caméra à l'agent.
func (l *HubLink) Snapshot(ctx context.Context) ([]byte, error) {
	res, err := l.roundTrip(ctx, Order{Kind: OrderSnapshot}, 30*time.Second)
	if err != nil {
		return nil, err
	}
	if err := resultErr(res); err != nil {
		return nil, err
	}
	b, err := base64.StdEncoding.DecodeString(res.JPEGBase64)
	if err != nil || len(b) < 4 {
		return nil, printer.ErrCameraFrame
	}
	return b, nil
}

// Reconcile demande à l'agent de relire l'état pour dire si l'impression attendue a démarré. N'envoie jamais de
// commande de lancement.
func (l *HubLink) Reconcile(ctx context.Context, exp printer.StartExpectation) (printer.Reconciliation, error) {
	if !l.h.Online(l.agentID) {
		return printer.Reconciliation{Outcome: printer.ReconcileUnknown, Status: l.h.statusOf(l.agentID, l.printerID), Reason: "l'agent est hors ligne"}, nil
	}
	res, err := l.roundTrip(ctx, Order{Kind: OrderReconcile, Reconcile: &ReconcileSpec{Name: exp.Remote.Name, SHA256: exp.Remote.SHA256, Since: exp.Since}}, 90*time.Second)
	if err != nil {
		return printer.Reconciliation{Outcome: printer.ReconcileUnknown, Status: l.h.statusOf(l.agentID, l.printerID), Reason: err.Error()}, nil
	}
	if res.State != ResultOK {
		return printer.Reconciliation{Outcome: printer.ReconcileUnknown, Status: l.h.statusOf(l.agentID, l.printerID), Reason: res.Message}, nil
	}
	out := printer.Reconciliation{Outcome: printer.ReconcileOutcome(res.Outcome), Reason: res.Reason, Status: l.h.statusOf(l.agentID, l.printerID)}
	if res.Status != nil {
		out.Status = *res.Status
	}
	switch out.Outcome {
	case printer.ReconcileStarted, printer.ReconcileNotStarted, printer.ReconcileUnknown:
	default:
		out.Outcome = printer.ReconcileUnknown
	}
	return out, nil
}
