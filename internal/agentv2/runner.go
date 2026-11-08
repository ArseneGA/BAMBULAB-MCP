package agentv2

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

// Device est l'imprimante vue par l'agent : l'adaptateur Bambu LAN (*printer.Bambu) en production, ou un double en
// test. Le code d'accès LAN n'existe que dans l'adaptateur de l'agent.
type Device interface {
	Refresh(ctx context.Context, maxAge time.Duration) (printer.Status, error)
	Upload(ctx context.Context, r io.Reader, name string, size int64, sha string) (printer.RemoteRef, error)
	RemoteSHA256(ctx context.Context, name string) (string, int64, error)
	Start(ctx context.Context, req printer.StartRequest) error
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error
	Stop(ctx context.Context) error
	Reconcile(ctx context.Context, exp printer.StartExpectation) (printer.Reconciliation, error)
	Close() error
}

// RunnerConfig configure le côté agent.
type RunnerConfig struct {
	Client    *Client
	AgentID   string
	PrinterID string
	// ServerKey : clé publique du serveur, épinglée à l'appairage. Seule une autorisation signée par elle lance.
	ServerKey ed25519.PublicKey
	// NewDevice construit l'imprimante (déjà connectée). Il reçoit le vérificateur à injecter dans l'adaptateur :
	// celui-ci n'accepte une preuve que pendant le lancement d'une autorisation vérifiée.
	NewDevice func(verify printer.ProofVerifier) (Device, error)
	// Snapshot lit une image caméra (nil = caméra non disponible).
	Snapshot func(ctx context.Context) ([]byte, error)
	// StateDir : registre des dépôts, journal des nonces, fichiers en transit.
	StateDir string
	// ReadOnly : kill-switch local (voir ReadOnlyFlag). nil = jamais en lecture seule.
	ReadOnly func() bool
	// Nonces : par défaut un journal fichier dans StateDir (survit au redémarrage).
	Nonces NonceStore
	Ledger *Ledger
	Now    func() time.Time
	// SkipRemoteHash désactive la relecture FTPS du fichier avant lancement (déconseillé : la taille seule est alors vérifiée).
	SkipRemoteHash bool
	// TelemetryEvery : période de la télémétrie (défaut 5 s). PollWait : long-poll en secondes (défaut 25).
	TelemetryEvery time.Duration
	PollWait       int
	Version        string
	Log            *slog.Logger
}

// Runner exécute les ordres du serveur pour UNE imprimante.
type Runner struct {
	cfg RunnerConfig
	dev Device
	led *Ledger
	nc  NonceStore
	log *slog.Logger

	physMu sync.Mutex // une seule opération physique (dépôt, lancement) à la fois

	verMu     sync.Mutex
	verifyTok string // jeton de la preuve interne du lancement en cours

	wg sync.WaitGroup
}

// NewRunner valide la configuration et construit l'imprimante.
func NewRunner(cfg RunnerConfig) (*Runner, error) {
	if cfg.Client == nil || cfg.AgentID == "" || cfg.PrinterID == "" || len(cfg.ServerKey) != ed25519.PublicKeySize || cfg.NewDevice == nil {
		return nil, errors.New("agentv2 : Client, AgentID, PrinterID, ServerKey et NewDevice sont requis")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.ReadOnly == nil {
		cfg.ReadOnly = func() bool { return false }
	}
	if cfg.TelemetryEvery <= 0 {
		cfg.TelemetryEvery = 5 * time.Second
	}
	if cfg.PollWait <= 0 {
		cfg.PollWait = 25
	}
	if cfg.StateDir == "" {
		d, err := os.MkdirTemp("", "bambu-agent-state-")
		if err != nil {
			return nil, err
		}
		cfg.StateDir = d
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return nil, err
	}
	r := &Runner{cfg: cfg, log: cfg.Log}
	var err error
	if r.led = cfg.Ledger; r.led == nil {
		if r.led, err = OpenLedger(filepath.Join(cfg.StateDir, "deposits.json")); err != nil {
			return nil, err
		}
	}
	if r.nc = cfg.Nonces; r.nc == nil {
		if r.nc, err = OpenFileNonceStore(filepath.Join(cfg.StateDir, "nonces.log"), cfg.Now); err != nil {
			return nil, err
		}
	}
	if r.dev, err = cfg.NewDevice(r.verify); err != nil {
		return nil, err
	}
	return r, nil
}

// verify est le ProofVerifier de l'adaptateur local : il n'accepte que la preuve construite par doStart.
func (r *Runner) verify(_ context.Context, p printer.ConfirmationProof) error {
	r.verMu.Lock()
	defer r.verMu.Unlock()
	if r.verifyTok == "" || p.Token != r.verifyTok {
		return errors.New("aucune autorisation de lancement vérifiée en cours")
	}
	return nil
}

func (r *Runner) setVerify(tok string) {
	r.verMu.Lock()
	r.verifyTok = tok
	r.verMu.Unlock()
}

// Close ferme l'imprimante.
func (r *Runner) Close() error {
	r.wg.Wait()
	return r.dev.Close()
}

// ReadOnly dit si le kill-switch local est actif.
func (r *Runner) ReadOnly() bool { return r.cfg.ReadOnly() }

// ---- boucle ----

// Run publie la télémétrie et traite les ordres jusqu'à l'annulation du contexte. Toutes les connexions sont
// sortantes ; une coupure réseau est réessayée avec attente croissante.
func (r *Runner) Run(ctx context.Context) error {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.telemetryLoop(ctx)
	}()
	backoff := time.Second
	for ctx.Err() == nil {
		o, err := r.cfg.Client.NextOrder(ctx, r.cfg.PrinterID, r.cfg.PollWait)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			r.log.Warn("agent : long-poll en échec", "err", err, "retry", backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		if o == nil {
			continue
		}
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			res := r.Execute(ctx, *o)
			r.postResult(ctx, o.ID, res)
		}()
	}
	r.wg.Wait()
	return ctx.Err()
}

func (r *Runner) postResult(ctx context.Context, id string, res OrderResult) {
	for try := 0; try < 5; try++ {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		err := r.cfg.Client.PostResult(cctx, id, res)
		cancel()
		if err == nil {
			return
		}
		var he *HTTPError
		if errors.As(err, &he) && he.Status/100 == 4 {
			r.log.Warn("agent : résultat refusé par le serveur", "order", id, "err", err)
			return
		}
		time.Sleep(time.Duration(try+1) * time.Second)
	}
	r.log.Error("agent : résultat non remis au serveur", "order", id, "state", res.State)
}

func (r *Runner) telemetryLoop(ctx context.Context) {
	t := time.NewTicker(r.cfg.TelemetryEvery)
	defer t.Stop()
	for {
		r.pushTelemetry(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (r *Runner) pushTelemetry(ctx context.Context) {
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	st, _ := r.dev.Refresh(rctx, 20*time.Second)
	cancel()
	tel := Telemetry{PrinterID: r.cfg.PrinterID, Status: st, ReadOnly: r.ReadOnly(), AgentVer: r.cfg.Version,
		CameraOK: r.cfg.Snapshot != nil, RemoteHash: !r.cfg.SkipRemoteHash}
	if !st.UpdatedAt.IsZero() {
		if age := r.cfg.Now().Sub(st.UpdatedAt); age > 0 {
			tel.AgeMS = age.Milliseconds()
		}
	}
	cctx, cancel2 := context.WithTimeout(ctx, 15*time.Second)
	defer cancel2()
	if err := r.cfg.Client.PostTelemetry(cctx, tel); err != nil && ctx.Err() == nil {
		r.log.Debug("agent : télémétrie non remise", "err", err)
	}
}

// ---- ordres ----

func refused(code, format string, args ...any) OrderResult {
	return OrderResult{State: ResultRefused, Code: code, Message: fmt.Sprintf(format, args...)}
}

func failed(code string, err error) OrderResult {
	return OrderResult{State: ResultFailed, Code: code, Message: err.Error()}
}

// Execute exécute UN ordre et renvoie son résultat. Il ne fait confiance à rien d'autre qu'à l'ordre et à la clé
// épinglée : un lancement exige une autorisation signée valide, fraîche, à nonce neuf, pour CETTE imprimante et un
// fichier que cet agent a déposé et dont il revérifie l'empreinte.
func (r *Runner) Execute(ctx context.Context, o Order) OrderResult {
	if o.PrinterID != r.cfg.PrinterID {
		return refused(CodeWrongTarget, "ordre adressé à l'imprimante %q, cet agent sert %q", o.PrinterID, r.cfg.PrinterID)
	}
	switch o.Kind {
	case OrderStart:
		return r.doStart(ctx, o)
	case OrderPutFile:
		return r.doPutFile(ctx, o)
	case OrderControl:
		return r.doControl(ctx, o)
	case OrderSnapshot:
		return r.doSnapshot(ctx)
	case OrderReconcile:
		return r.doReconcile(ctx, o)
	case OrderStatus:
		r.pushTelemetry(ctx)
		return OrderResult{State: ResultOK}
	}
	return refused(CodeUnsupported, "ordre %q inconnu", o.Kind)
}

func (r *Runner) doControl(ctx context.Context, o Order) OrderResult {
	var err error
	switch o.Cmd {
	case CmdPause:
		err = r.dev.Pause(ctx)
	case CmdStop:
		err = r.dev.Stop(ctx) // geste de sécurité : permis même en lecture seule
	case CmdResume:
		if r.ReadOnly() {
			return refused(CodeReadOnly, "agent en lecture seule : reprise refusée")
		}
		err = r.dev.Resume(ctx)
	default:
		return refused(CodeBadRequest, "commande %q inconnue", o.Cmd)
	}
	switch {
	case err == nil:
		return OrderResult{State: ResultOK}
	case errors.Is(err, printer.ErrBadState), errors.Is(err, printer.ErrNotConnected):
		return OrderResult{State: ResultFailed, Code: CodeNotReady, Message: err.Error()}
	}
	return failed(CodePrinterFailed, err)
}

func (r *Runner) doSnapshot(ctx context.Context) OrderResult {
	if r.cfg.Snapshot == nil {
		return OrderResult{State: ResultFailed, Code: CodeUnsupported, Message: "caméra non disponible sur cet agent"}
	}
	b, err := r.cfg.Snapshot(ctx)
	if err != nil {
		return failed(CodePrinterFailed, err)
	}
	return OrderResult{State: ResultOK, JPEGBase64: base64.StdEncoding.EncodeToString(b)}
}

func (r *Runner) doReconcile(ctx context.Context, o Order) OrderResult {
	if o.Reconcile == nil || o.Reconcile.Name == "" {
		return refused(CodeBadRequest, "reconcile sans fichier")
	}
	rc, err := r.dev.Reconcile(ctx, printer.StartExpectation{Remote: printer.RemoteRef{Name: o.Reconcile.Name, SHA256: o.Reconcile.SHA256}, Since: o.Reconcile.Since})
	if err != nil {
		return failed(CodePrinterFailed, err)
	}
	st := rc.Status
	return OrderResult{State: ResultOK, Outcome: string(rc.Outcome), Reason: rc.Reason, Status: &st}
}

func (r *Runner) doPutFile(ctx context.Context, o Order) OrderResult {
	if r.ReadOnly() {
		return refused(CodeReadOnly, "agent en lecture seule : dépôt de fichier refusé")
	}
	f := o.File
	if f == nil || f.ID == "" || f.Name == "" || f.Size < 0 || len(f.SHA256) != 64 {
		return refused(CodeBadRequest, "dépôt mal formé")
	}
	if _, err := hex.DecodeString(f.SHA256); err != nil {
		return refused(CodeBadRequest, "empreinte invalide")
	}
	if !r.physMu.TryLock() {
		return refused(CodeBusy, "une autre opération physique est en cours")
	}
	defer r.physMu.Unlock()

	dir := filepath.Join(r.cfg.StateDir, "stage")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return failed(CodePrinterFailed, err)
	}
	tmp, err := os.CreateTemp(dir, "dl-*.part")
	if err != nil {
		return failed(CodePrinterFailed, err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := r.cfg.Client.Download(ctx, f.ID, f.Size, tmp); err != nil {
		return failed(CodePrinterFailed, fmt.Errorf("téléchargement du fichier : %w", err))
	}
	// Empreinte vérifiée AVANT le dépôt : on ne dépose que ce que le serveur a annoncé.
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return failed(CodePrinterFailed, err)
	}
	h := sha256.New()
	n, err := io.Copy(h, tmp)
	if err != nil {
		return failed(CodePrinterFailed, err)
	}
	if n != f.Size || hex.EncodeToString(h.Sum(nil)) != strings.ToLower(f.SHA256) {
		return OrderResult{State: ResultFailed, Code: CodeHashMismatch, Message: "le fichier reçu n'a pas l'empreinte annoncée : dépôt abandonné"}
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return failed(CodePrinterFailed, err)
	}
	ref, err := r.dev.Upload(ctx, tmp, f.Name, f.Size, strings.ToLower(f.SHA256))
	if err != nil {
		return failed(CodePrinterFailed, err)
	}
	if err := r.led.Put(f.SHA256, LedgerEntry{Name: ref.Name, Size: ref.Size, At: r.cfg.Now().UTC()}); err != nil {
		return failed(CodePrinterFailed, fmt.Errorf("registre des dépôts : %w", err))
	}
	return OrderResult{State: ResultOK, Remote: &ref}
}

// doStart : la seule porte vers une commande de lancement.
func (r *Runner) doStart(ctx context.Context, o Order) OrderResult {
	// 1. Kill-switch local : prioritaire sur tout, sans même regarder l'autorisation.
	if r.ReadOnly() {
		return refused(CodeReadOnly, "agent en lecture seule (kill-switch local) : aucun lancement")
	}
	if o.Auth == nil {
		return refused(CodeBadSignature, "ordre de lancement sans autorisation signée")
	}
	// 2. Signature (clé épinglée), forme, fraîcheur.
	p, err := VerifyAuth(r.cfg.ServerKey, *o.Auth, r.cfg.Now())
	if err != nil {
		var ae *AuthError
		if errors.As(err, &ae) {
			return refused(ae.Code, "%s", ae.Message)
		}
		return refused(CodeBadSignature, "%v", err)
	}
	// 3. Cible : cet agent, cette imprimante, ce que l'ordre annonce.
	if p.AgentID != r.cfg.AgentID || p.PrinterID != r.cfg.PrinterID || o.PrinterID != p.PrinterID {
		return refused(CodeWrongTarget, "autorisation émise pour un autre agent ou une autre imprimante")
	}
	if !p.PlateClear {
		return refused(CodeBadRequest, "autorisation sans attestation « plateau vide »")
	}
	sha := strings.ToLower(p.SHA256)
	if len(sha) != 64 || p.Plate < 1 {
		return refused(CodeBadRequest, "autorisation incomplète (empreinte ou plateau)")
	}
	for _, v := range p.AMSMapping {
		if v < -1 || v > 253 {
			return refused(CodeBadRequest, "ams_mapping hors plage")
		}
	}
	// 4. Fichier : déposé par cet agent.
	ent, ok := r.led.Get(sha)
	if !ok {
		return refused(CodeFileUnknown, "ce fichier (empreinte %s…) n'a pas été déposé par cet agent", sha[:12])
	}
	// 5. Une seule opération physique à la fois (sans brûler le nonce si on est occupé).
	if !r.physMu.TryLock() {
		return refused(CodeBusy, "une autre opération physique est en cours")
	}
	defer r.physMu.Unlock()
	// 6. Empreinte du fichier présent SUR L'IMPRIMANTE (pas seulement celle du dépôt).
	if r.cfg.SkipRemoteHash {
		// repli : au moins la taille (Bambu.Start la contrôle aussi)
	} else {
		got, size, herr := r.dev.RemoteSHA256(ctx, ent.Name)
		if herr != nil {
			return refused(CodeHashMismatch, "fichier introuvable ou illisible sur l'imprimante : %v", herr)
		}
		if got != sha || size != ent.Size {
			return refused(CodeHashMismatch, "le fichier présent sur l'imprimante n'a pas l'empreinte autorisée (attendu %s…, trouvé %s…)", sha[:12], short(got))
		}
	}
	// 7. Fraîcheur revérifiée (la relecture a pu durer) puis le nonce est brûlé, AVANT toute commande.
	if _, err := VerifyAuth(r.cfg.ServerKey, *o.Auth, r.cfg.Now()); err != nil {
		var ae *AuthError
		if errors.As(err, &ae) {
			return refused(ae.Code, "%s", ae.Message)
		}
		return refused(CodeExpired, "%v", err)
	}
	if r.ReadOnly() { // le kill-switch peut avoir été posé pendant la relecture
		return refused(CodeReadOnly, "agent en lecture seule (kill-switch local) : aucun lancement")
	}
	fresh, err := r.nc.Consume(p.ID, p.ExpiresAt)
	if err != nil {
		return refused(CodePrinterFailed, "journal des nonces illisible : %v (aucun lancement sans lui)", err)
	}
	if !fresh {
		return refused(CodeReplay, "autorisation déjà utilisée (rejeu refusé)")
	}
	// 8. UNE commande. L'adaptateur local refait ses garde-fous (état frais, pas d'erreur, fichier présent) et
	// n'envoie jamais deux fois. Aucune nouvelle tentative ici : une issue indéterminée reste indéterminée.
	r.setVerify(p.ID)
	defer r.setVerify("")
	req := printer.StartRequest{
		Remote:     printer.RemoteRef{Name: ent.Name, Path: "/" + ent.Name, Size: ent.Size, SHA256: sha},
		Plate:      p.Plate,
		UseAMS:     p.UseAMS,
		AMSMapping: p.AMSMapping,
		PlateClear: p.PlateClear,
		Options: printer.StartOptions{Timelapse: p.Options.Timelapse, BedLeveling: p.Options.BedLeveling, FlowCali: p.Options.FlowCali,
			VibrationCali: p.Options.VibrationCali, LayerInspect: p.Options.LayerInspect, BedType: p.Options.BedType},
		Proof: printer.ConfirmationProof{PrinterID: p.PrinterID, SHA256: sha, Plate: p.Plate, AMSMapping: p.AMSMapping, Token: p.ID, ExpiresAt: p.ExpiresAt},
	}
	serr := r.dev.Start(ctx, req)
	var su *printer.StartUnknownError
	switch {
	case serr == nil:
		return OrderResult{State: ResultStarted}
	case errors.As(serr, &su):
		return OrderResult{State: ResultUnknown, Stage: string(su.Stage), Message: serr.Error()}
	case errors.Is(serr, printer.ErrStartRejected):
		return OrderResult{State: ResultRejected, Code: CodePrinterFailed, Message: serr.Error()}
	case errors.Is(serr, printer.ErrBusy):
		return refused(CodeBusy, "%v", serr)
	}
	// Refus local avant toute publication (état non lançable, imprimante hors ligne...) : rien n'est parti.
	return OrderResult{State: ResultRefused, Code: CodeNotReady, Message: serr.Error()}
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
