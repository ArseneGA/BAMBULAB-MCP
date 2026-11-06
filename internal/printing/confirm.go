package printing

// Mécanisme de confirmation choisi (dossier §10 : « un simple booléen inventé par
// un agent n'est pas une preuve suffisante ») :
//
//  1. request_print_confirmation crée une PrintConfirmation liée (imprimante,
//     empreinte, plateau, matières) avec un jeton de 256 bits. Le jeton reste dans
//     la MÉMOIRE du service : il n'est jamais renvoyé au modèle, ni stocké en clair.
//  2. Seul un humain le « libère » : soit en ouvrant la page locale
//     /confirm/{id} (il coche « plateau vide » puis clique « Lancer »), soit via
//     l'outil réservé à l'application MCP (visibility ["app"]) appelé par la vue.
//  3. start_print n'a qu'un identifiant de confirmation : il refuse tant que
//     l'humain n'a pas approuvé, puis le jeton est consommé atomiquement par
//     store.ConsumeConfirmation, à l'instant exact de l'envoi de la commande.
//
// Limite assumée : la page web est protégée par le garde HTTP (hôte loopback ou
// liste blanche, anti-CSRF navigateur, jeton d'API hors loopback) et par un nonce
// à usage unique, mais un agent qui disposerait lui-même d'un client HTTP local
// pourrait la valider ; l'outil d'application dépend de la mise en œuvre de
// `visibility:["app"]` par l'hôte. Ce n'est donc pas une preuve cryptographique
// de présence humaine : c'est une barrière qui sort la décision du canal que
// contrôle le modèle.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
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

type pageNonce struct {
	confID  string
	expires time.Time
}

// ConfirmationView est ce que reçoit le modèle : jamais le jeton.
type ConfirmationView struct {
	ConfirmationID string               `json:"confirmation_id"`
	PrintJobID     string               `json:"print_job_id"`
	ExpiresAt      time.Time            `json:"expires_at"`
	ConfirmURL     string               `json:"confirm_url" jsonschema:"page locale que l'HUMAIN ouvre pour confirmer ; le jeton n'est pas dans ce lien"`
	Approved       bool                 `json:"approved" jsonschema:"vrai seulement après le clic de l'humain"`
	Binding        PromptDetails        `json:"binding" jsonschema:"ce que l'humain approuve : fichier, imprimante, empreinte, plateau, matières"`
	Instructions   string               `json:"instructions"`
	State          contracts.PrintState `json:"state"`
}

// RequestConfirmationInput est l'entrée de request_print_confirmation.
type RequestConfirmationInput struct {
	PrintJobID string `json:"print_job_id"`
}

func newBinding(j Job) contracts.ConfirmationBinding {
	b := contracts.ConfirmationBinding{PrinterID: j.PrinterID, ArtifactSHA256: j.ArtifactSHA256, Plate: j.Plate}
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
	approved := a != nil && a.approved
	s.mu.Unlock()
	return &ConfirmationView{ConfirmationID: conf.ID, PrintJobID: j.ID, ExpiresAt: conf.ExpiresAt, ConfirmURL: s.BaseURL() + "/confirm/" + conf.ID,
		Approved: approved, Binding: *d, State: j.State,
		Instructions: "Demande à l'utilisateur d'ouvrir confirm_url dans son navigateur, de vérifier le résumé, de cocher « plateau vide » et de cliquer sur Lancer. " +
			"Tu ne peux pas confirmer à sa place. Ensuite appelle start_print avec confirmation_id et une idempotency_key."}
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

// Approve enregistre l'accord HUMAIN (page web ou application MCP). plateClear est
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
		return nil, fmt.Errorf("%w : l'utilisateur n'a pas encore confirmé sur %s", ErrNeedsApproval, s.BaseURL()+"/confirm/"+confID)
	}
	if !s.now().Before(a.expires) {
		return nil, contracts.ErrConfirmationExpired
	}
	cp := *a
	return &cp, nil
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
	s.consumedBy[confID] = queueJob
	s.mu.Unlock()
	if _, err := s.transition(bg, j.ID, contracts.EvConfirm, "confirmation "+confID+" consommée ; commande de lancement envoyée à l'imprimante", func(x *Job) error {
		x.LastError = ""
		return nil
	}); err != nil {
		return fmt.Errorf("confirmation consommée mais l'impression n'a pas pu passer en START_REQUESTED : %w", err)
	}
	return nil
}

func randomNonce() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// issueNonce délivre un nonce à usage unique pour le formulaire de confirmation.
func (s *Service) issueNonce(confID string) string {
	n := randomNonce()
	s.mu.Lock()
	for k, v := range s.pages { // purge
		if !s.now().Before(v.expires) {
			delete(s.pages, k)
		}
	}
	s.pages[n] = pageNonce{confID: confID, expires: s.now().Add(15 * time.Minute)}
	s.mu.Unlock()
	return n
}

// useNonce consomme le nonce ; faux s'il est inconnu, périmé ou d'une autre confirmation.
func (s *Service) useNonce(n, confID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.pages[n]
	if !ok {
		return false
	}
	delete(s.pages, n)
	return v.confID == confID && s.now().Before(v.expires)
}
