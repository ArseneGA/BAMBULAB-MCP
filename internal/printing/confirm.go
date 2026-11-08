package printing

// Mécanisme de confirmation choisi (dossier §10 : « un simple booléen inventé par
// un agent n'est pas une preuve suffisante ») :
//
//  1. request_print_confirmation crée une PrintConfirmation liée (imprimante,
//     empreinte, plateau, matières) avec un jeton de 256 bits. Le jeton reste dans
//     la MÉMOIRE du service : il n'est jamais renvoyé au modèle, ni stocké en clair.
//  2. Seul un humain le « libère » : en ouvrant la page /confirm/{id} depuis un
//     NAVIGATEUR ENREGISTRÉ (cookie de session obtenu par `bambud enroll`, voir
//     internal/humanauth) : il coche « plateau vide » puis clique « Lancer ». AUCUN outil MCP
//     ni route JSON ne peut approuver (docs/SECURITY.md).
//  3. start_print n'a qu'un identifiant de confirmation : il refuse tant que
//     l'humain n'a pas approuvé, puis le jeton est consommé atomiquement par
//     store.ConsumeConfirmation, à l'instant exact de l'envoi de la commande.
//
// Limite assumée : un processus local qui aurait les droits de l'utilisateur (shell) pourrait
// lire la base et se faire enregistrer ; c'est hors périmètre (le modèle n'a que MCP) mais
// visible (journal d'audit) et plus difficile (code à usage unique affiché sur le terminal).
// Ce n'est pas une preuve cryptographique de présence humaine.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/localtime"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

type approval struct {
	jobID      string
	token      string
	expires    time.Time
	plateClear bool
	approved   bool
	approvedAt time.Time
	via        string
}

// pageNonce : nonce de formulaire à usage unique ; extra porte une donnée liée côté serveur
// (ex. l'empreinte de certificat affichée à l'humain sur la page de saisie du code).
type pageNonce struct {
	confID  string
	expires time.Time
	extra   string
}

// stopReq : demande d'arrêt d'une impression en cours. Elle ne vaut RIEN sans validation humaine
// sur /stop/{id} (navigateur enregistré) ; son identifiant n'est qu'un localisateur.
type stopReq struct {
	jobID   string
	expires time.Time
}

// consumedEntry : lancement (travail de file) qui a consommé une confirmation.
type consumedEntry struct {
	queueJob string
	at       time.Time
}

const (
	nonceTTL    = 15 * time.Minute
	stopTTL     = 10 * time.Minute
	consumedTTL = time.Hour
	// DefaultApprovalTTL : un accord humain n'est utilisable que 2 minutes après le clic.
	DefaultApprovalTTL = 2 * time.Minute
)

// ConfirmationView est ce que reçoit le modèle : jamais le jeton.
//
// Le contenu structuré reste COURT (binding et safety sont déjà des sous-objets, pas de consignes longues) : certains hôtes
// l'affichent tel quel en tableau clé/valeur quand ils ne montrent pas la vue. Les consignes au modèle sont dans le texte
// du résultat de l'outil, pas ici.
type ConfirmationView struct {
	ConfirmationID string               `json:"confirmation_id"`
	PrintJobID     string               `json:"print_job_id"`
	ExpiresAt      time.Time            `json:"expires_at" jsonschema:"RFC 3339"`
	ExpiresAtLocal string               `json:"expires_at_local" jsonschema:"même instant, lisible, dans le fuseau de l'utilisateur (ex. 21:03 (08/11/2026, Europe/Paris))"`
	ConfirmURL     string               `json:"confirm_url" jsonschema:"page locale que l'HUMAIN ouvre pour confirmer ; le jeton n'est pas dans ce lien"`
	Approved       bool                 `json:"approved" jsonschema:"vrai seulement après le clic de l'humain"`
	Binding        PromptDetails        `json:"binding" jsonschema:"ce que l'humain approuve : fichier, imprimante, empreinte, plateau, matières, mise à niveau du plateau"`
	Safety         *SafetyView          `json:"safety,omitempty" jsonschema:"températures et origine du G-code lues dans l'archive (anomalies à signaler à l'utilisateur)"`
	State          contracts.PrintState `json:"state"`
}

// RequestConfirmationInput est l'entrée de request_print_confirmation.
type RequestConfirmationInput struct {
	PrintJobID string `json:"print_job_id"`
	// BedLeveling : mise à niveau automatique du plateau au démarrage. Défaut : oui. L'humain la voit sur /confirm et
	// dans la vue, et elle fait partie de la liaison qu'il approuve ; la changer exige une nouvelle confirmation.
	BedLeveling *bool `json:"bed_leveling,omitempty" jsonschema:"mise à niveau du plateau au démarrage (défaut true) ; affichée à l'humain et incluse dans ce qu'il approuve"`
	// FlowCali, VibrationCali, Timelapse : autres options de démarrage. Défauts = ce que le service envoyait déjà
	// (calibration du débit : oui ; vibrations : oui ; timelapse : non). Affichées à l'humain et incluses dans ce qu'il approuve.
	FlowCali      *bool `json:"flow_cali,omitempty" jsonschema:"calibration du débit au démarrage (défaut true) ; affichée à l'humain et incluse dans ce qu'il approuve"`
	VibrationCali *bool `json:"vibration_cali,omitempty" jsonschema:"calibration des vibrations au démarrage (défaut true ; imprimantes qui la gèrent) ; affichée à l'humain et incluse dans ce qu'il approuve"`
	Timelapse     *bool `json:"timelapse,omitempty" jsonschema:"enregistrer un timelapse (défaut false) ; affiché à l'humain et inclus dans ce qu'il approuve"`
}

func newBinding(j Job) contracts.ConfirmationBinding {
	b := contracts.ConfirmationBinding{PrinterID: j.PrinterID, ArtifactSHA256: j.ArtifactSHA256, Plate: j.Plate, NoBedLeveling: j.NoBedLeveling,
		NoFlowCali: j.NoFlowCali, NoVibrationCali: j.NoVibrationCali, Timelapse: j.Timelapse}
	for _, a := range j.Assignments {
		b.Materials = append(b.Materials, contracts.SlotAssignment{Slot: bindingSlot(a), MaterialID: a.MaterialID})
	}
	return b
}

// RequestConfirmation crée la PrintConfirmation liée (TTL court) pour une impression déposée.
func (s *Service) RequestConfirmation(ctx context.Context, in RequestConfirmationInput) (*ConfirmationView, error) {
	j, err := s.GetJob(ctx, in.PrintJobID)
	if err != nil {
		return nil, err
	}
	if j.State != contracts.PrintUploaded && j.State != contracts.PrintAwaitingConfirmation {
		return nil, fmt.Errorf("%w : l'impression %s est en %s ; la confirmation suit le dépôt (UPLOADED / AWAITING_PRINT_CONFIRMATION)", ErrState, j.ID, j.State)
	}
	if j.Remote == nil || j.Plate < 1 || len(j.Assignments) == 0 {
		return nil, fmt.Errorf("%w : dépôt, plateau et matières requis avant la confirmation", ErrState)
	}
	noBL := in.BedLeveling != nil && !*in.BedLeveling
	noFlow := in.FlowCali != nil && !*in.FlowCali
	noVib := in.VibrationCali != nil && !*in.VibrationCali
	timelapse := in.Timelapse != nil && *in.Timelapse
	if rec, rerr := s.GetPrinterRecord(ctx, j.PrinterID); rerr == nil && in.VibrationCali != nil && *in.VibrationCali != true && !vibrationCaliSupported(rec.ModelID) {
		return nil, invalidf("la calibration des vibrations n'est pas réglable sur le modèle %q", rec.ModelID)
	}
	// AVANT toute confirmation : l'imprimante doit être lançable (sinon l'humain approuverait un lancement voué à l'échec).
	if err := s.ensureLaunchable(ctx, j.PrinterID); err != nil {
		return nil, err
	}
	j.NoBedLeveling, j.NoFlowCali, j.NoVibrationCali, j.Timelapse = noBL, noFlow, noVib, timelapse // liaison de CETTE confirmation ; enregistrée plus bas
	id := contracts.NewID("cf")
	conf, token, err := contracts.NewConfirmation(id, j.ID, newBinding(j), s.d.ConfirmationTTL, s.now())
	if err != nil {
		return nil, err
	}
	if err := s.d.Store.PutDoc(ctx, actor, "print_confirmations", id, conf); err != nil {
		return nil, err
	}
	old := ""
	if j.State == contracts.PrintUploaded {
		if _, err := s.transition(ctx, j.ID, contracts.EvRequestConfirmation, "confirmation demandée", nil); err != nil {
			return nil, err
		}
	}
	j, err = s.update(ctx, j.ID, func(x *Job) error {
		if x.State != contracts.PrintAwaitingConfirmation {
			return fmt.Errorf("%w : %s", ErrState, x.State)
		}
		old, x.ConfirmationID = x.ConfirmationID, id
		x.NoBedLeveling, x.NoFlowCali, x.NoVibrationCali, x.Timelapse = noBL, noFlow, noVib, timelapse
		x.event("confirmation_issued", x.State, x.State, "confirmation "+id+" émise (jeton non transmis au modèle)", s.now())
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if old != "" {
		delete(s.approv, old) // l'ancienne confirmation ne vaut plus rien
	}
	s.approv[id] = &approval{jobID: j.ID, token: token, expires: conf.ExpiresAt}
	s.mu.Unlock()
	_ = s.d.Store.Audit(ctx, actor, "print.confirmation_requested", "print_job", j.ID, map[string]any{"confirmation": id, "expires": conf.ExpiresAt})
	return s.confirmationView(j, conf), nil
}

func (s *Service) confirmationView(j Job, conf contracts.PrintConfirmation) *ConfirmationView {
	_, d := s.promptFor(j)
	s.mu.Lock()
	a := s.approv[conf.ID]
	approved := a != nil && a.approved && s.now().Before(a.approvedAt.Add(s.approvalTTL()))
	if conf.UsedAt != nil { // consommée : l'humain l'avait approuvée et le lancement est parti (autostart.go)
		approved = true
	}
	s.mu.Unlock()
	return &ConfirmationView{ConfirmationID: conf.ID, PrintJobID: j.ID, ExpiresAt: conf.ExpiresAt, ConfirmURL: s.BaseURL() + "/confirm/" + conf.ID,
		Approved: approved, Binding: *d, State: j.State, Safety: safetyOf(j), ExpiresAtLocal: localtime.Full(conf.ExpiresAt)}
}

// GetConfirmation relit une confirmation (sans jeton).
func (s *Service) GetConfirmation(ctx context.Context, confID string) (*ConfirmationView, error) {
	var conf contracts.PrintConfirmation
	if err := s.d.Store.GetDoc(ctx, "print_confirmations", confID, &conf); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("%w : confirmation %q", ErrNotFound, confID)
		}
		return nil, err
	}
	j, err := s.GetJob(ctx, conf.PrintJobID)
	if err != nil {
		return nil, err
	}
	return s.confirmationView(j, conf), nil
}

// confirmable vérifie que la confirmation est encore celle de l'impression, non utilisée, non expirée et que la liaison n'a pas bougé.
func (s *Service) confirmable(ctx context.Context, confID string) (contracts.PrintConfirmation, Job, error) {
	var conf contracts.PrintConfirmation
	if err := s.d.Store.GetDoc(ctx, "print_confirmations", confID, &conf); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return conf, Job{}, fmt.Errorf("%w : confirmation %q", ErrNotFound, confID)
		}
		return conf, Job{}, err
	}
	j, err := s.GetJob(ctx, conf.PrintJobID)
	if err != nil {
		return conf, j, err
	}
	switch {
	case conf.UsedAt != nil:
		return conf, j, contracts.ErrConfirmationUsed
	case !s.now().Before(conf.ExpiresAt):
		return conf, j, contracts.ErrConfirmationExpired
	case j.State != contracts.PrintAwaitingConfirmation || j.ConfirmationID != confID:
		return conf, j, fmt.Errorf("%w : cette confirmation ne correspond plus à l'impression (état %s)", contracts.ErrConfirmationMismatch, j.State)
	case newBinding(j).Fingerprint() != conf.BindingFingerprint:
		return conf, j, contracts.ErrConfirmationMismatch
	}
	return conf, j, nil
}

// Approve enregistre l'accord HUMAIN (page web d'un navigateur enregistré UNIQUEMENT : aucun outil MCP,
// aucune route JSON ne l'appelle ; test noapprove_test.go). plateClear est
// l'attestation « le plateau est vide » : sans elle, refus.
func (s *Service) Approve(ctx context.Context, confID string, plateClear bool, via string) (*ConfirmationView, error) {
	if !plateClear {
		return nil, fmt.Errorf("%w : l'attestation « plateau vide et dégagé » est obligatoire", ErrNeedsApproval)
	}
	conf, j, err := s.confirmable(ctx, confID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	a := s.approv[confID]
	if a == nil { // service redémarré depuis la demande : le jeton n'existe plus
		s.mu.Unlock()
		return nil, fmt.Errorf("%w : le service a redémarré depuis la demande, refaire request_print_confirmation", contracts.ErrConfirmationExpired)
	}
	a.plateClear, a.approved, a.approvedAt, a.via = true, true, s.now(), via
	s.mu.Unlock()
	_ = s.d.Store.Audit(ctx, "human:"+via, "print.confirmation_approved", "print_job", j.ID, map[string]any{"confirmation": confID, "plate_clear": true})
	return s.confirmationView(j, conf), nil
}

// approved renvoie l'approbation humaine valide d'une confirmation.
func (s *Service) approvedFor(confID, jobID string) (*approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.approv[confID]
	if a == nil || a.jobID != jobID {
		return nil, fmt.Errorf("%w : aucune confirmation en attente pour cette impression", ErrNeedsApproval)
	}
	if !a.approved || !a.plateClear {
		return nil, fmt.Errorf("%w : l'utilisateur n'a pas encore confirmé (ou son accord a été invalidé) sur %s", ErrNeedsApproval, s.BaseURL()+"/confirm/"+confID)
	}
	if !s.now().Before(a.expires) {
		return nil, contracts.ErrConfirmationExpired
	}
	if !s.now().Before(a.approvedAt.Add(s.approvalTTL())) {
		a.approved, a.plateClear = false, false // périmé : l'humain doit recliquer
		return nil, fmt.Errorf("%w : l'accord de l'utilisateur date de plus de %s ; il doit confirmer à nouveau sur %s", ErrNeedsApproval, s.approvalTTL(), s.BaseURL()+"/confirm/"+confID)
	}
	cp := *a
	return &cp, nil
}

func (s *Service) approvalTTL() time.Duration {
	if s.d.ApprovalTTL > 0 {
		return s.d.ApprovalTTL
	}
	return DefaultApprovalTTL
}

// invalidateApproval retire l'accord humain d'une confirmation (après tout lancement « not_sent » :
// le jeton n'est pas brûlé mais l'humain doit recliquer, pour que l'accord ne traîne pas).
func (s *Service) invalidateApproval(confID string) {
	s.mu.Lock()
	if a := s.approv[confID]; a != nil {
		a.approved, a.plateClear = false, false
	}
	s.mu.Unlock()
}

// proofToken encode « identifiant.jeton.travail » dans la preuve de l'adaptateur : le troisième
// élément est le travail de file qui porte le lancement, pour savoir QUEL lancement a consommé la confirmation.
func proofToken(confID, token, queueJob string) string { return confID + "." + token + "." + queueJob }

// verifyProof est le ProofVerifier de l'adaptateur : appelé par printer.Bambu.Start
// APRÈS les contrôles d'état de l'imprimante et JUSTE AVANT la publication de la
// commande. Il consomme atomiquement la confirmation (ConsumeConfirmation) pour CE
// PrintJob et la liaison COURANTE, puis passe l'impression en START_REQUESTED.
func (s *Service) verifyProof(ctx context.Context, p printer.ConfirmationProof) error {
	parts := strings.SplitN(p.Token, ".", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return errors.New("preuve mal formée")
	}
	confID, token, queueJob := parts[0], parts[1], parts[2]
	bg := context.Background()
	var conf contracts.PrintConfirmation
	if err := s.d.Store.GetDoc(bg, "print_confirmations", confID, &conf); err != nil {
		return err
	}
	j, err := s.GetJob(bg, conf.PrintJobID)
	if err != nil {
		return err
	}
	if j.State != contracts.PrintAwaitingConfirmation || j.ConfirmationID != confID {
		return fmt.Errorf("%w : état %s", contracts.ErrConfirmationMismatch, j.State)
	}
	if p.PrinterID != j.PrinterID || !strings.EqualFold(p.SHA256, j.ArtifactSHA256) || p.Plate != j.Plate {
		return contracts.ErrConfirmationMismatch
	}
	if _, err := s.approvedFor(confID, j.ID); err != nil {
		return err
	}
	// Liaison COURANTE recalculée depuis l'impression : tout changement refuse.
	if err := s.d.Store.ConsumeConfirmation(bg, confID, token, newBinding(j), j.ID); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.approv, confID) // usage unique, y compris côté mémoire
	s.consumedBy[confID] = consumedEntry{queueJob: queueJob, at: s.now()}
	s.mu.Unlock()
	if err := s.confirmTransition(bg, j.ID, confID); err != nil {
		// La confirmation est consommée mais l'impression n'a pas pu passer en START_REQUESTED : la
		// commande ne sera PAS publiée (cette fonction renvoie une erreur avant l'envoi). État explicite,
		// terminal et sûr : rien n'a été envoyé ; une nouvelle impression exige une nouvelle confirmation.
		msg := "confirmation " + confID + " consommée mais START_REQUESTED non enregistré : " + err.Error() + " ; rien n'a été envoyé à l'imprimante"
		if _, aerr := s.transition(bg, j.ID, contracts.EvAbortBeforeSend, msg, func(x *Job) error { x.LastError = msg; return nil }); aerr != nil {
			s.log.Warn("START_ABORTED_BEFORE_SEND non enregistré", "print_job", j.ID, "err", aerr)
		}
		return fmt.Errorf("confirmation consommée mais l'impression n'a pas pu passer en START_REQUESTED (aucune commande envoyée) : %w", err)
	}
	return nil
}

func (s *Service) defaultConfirmTransition(ctx context.Context, jobID, confID string) error {
	_, err := s.transition(ctx, jobID, contracts.EvConfirm, "confirmation "+confID+" consommée ; commande de lancement envoyée à l'imprimante", func(x *Job) error {
		x.LastError = ""
		return nil
	})
	return err
}

func randomNonce() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// issueNonce délivre un nonce à usage unique pour un formulaire (extra : donnée liée côté serveur).
func (s *Service) issueNonce(key string, extra ...string) string {
	n := randomNonce()
	s.mu.Lock()
	s.purgeLocked()
	pn := pageNonce{confID: key, expires: s.now().Add(nonceTTL)}
	if len(extra) > 0 {
		pn.extra = extra[0]
	}
	s.pages[n] = pn
	s.mu.Unlock()
	return n
}

// useNonce consomme le nonce ; faux s'il est inconnu, périmé ou d'une autre clé.
func (s *Service) useNonce(n, key string) bool {
	_, ok := s.takeNonce(n, key)
	return ok
}

// takeNonce consomme le nonce et rend sa donnée liée.
func (s *Service) takeNonce(n, key string) (pageNonce, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.pages[n]
	if !ok {
		return pageNonce{}, false
	}
	delete(s.pages, n)
	return v, v.confID == key && s.now().Before(v.expires)
}

// purgeLocked supprime ce qui a expiré (nonces, demandes d'arrêt, approbations, traces de consommation).
func (s *Service) purgeLocked() {
	now := s.now()
	for k, v := range s.pages {
		if !now.Before(v.expires) {
			delete(s.pages, k)
		}
	}
	for k, v := range s.stops {
		if !now.Before(v.expires) {
			delete(s.stops, k)
			s.stopGone[k] = goneStop{reason: "expired", at: now}
		}
	}
	for k, v := range s.stopGone {
		if now.Sub(v.at) > 24*time.Hour {
			delete(s.stopGone, k)
		}
	}
	for k, v := range s.approv {
		if !now.Before(v.expires) {
			delete(s.approv, k)
		}
	}
	for k, v := range s.consumedBy {
		if now.Sub(v.at) > consumedTTL {
			delete(s.consumedBy, k)
		}
	}
}

// purge est appelé périodiquement (maintenance) et à chaque émission.
func (s *Service) purge() {
	s.mu.Lock()
	s.purgeLocked()
	s.mu.Unlock()
}
