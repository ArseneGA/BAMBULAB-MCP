package printing

// Approbation par passkey (WebAuthn) : docs/SECURITY.md § 13.
//
// Même chemin que l'existant : après vérification de l'assertion, ApproveAndStart (consommation atomique de la
// confirmation, une seule commande, jamais de renvoi) ; l'arrêt passe par ExecuteStop. La passkey ne remplace rien :
// elle s'ajoute au bouton et au code de lancement, sauf si l'humain a activé « exiger une passkey ».
//
// Seule une page humaine (navigateur enregistré, nonce, Sec-Fetch) appelle ces fonctions : aucun outil MCP ni route
// JSON n'y mène (test noapprove_test.go, TestNoMCPToolCanApproveAConfirmation).

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
)

func (s *Service) passkeys() *humanauth.Passkeys { return humanauth.PasskeysFor(s.d.Store) }

// PasskeyRequired dit si « exiger une passkey » est actif.
func (s *Service) PasskeyRequired(ctx context.Context) bool { return s.passkeys().Required(ctx) }

// ApproveWithPasskey vérifie l'assertion d'une passkey pour CETTE confirmation (challenge, empreinte de liaison,
// navigateur, origine, signature, compteur) puis approuve et lance comme les autres canaux humains. plateClear reste
// l'attestation « plateau vide », obligatoire en plus de la passkey.
func (s *Service) ApproveWithPasskey(ctx context.Context, confID string, plateClear bool, assertion []byte, r *http.Request) (*ApprovalResult, error) {
	if !plateClear {
		return nil, fmt.Errorf("%w : l'attestation « plateau vide et dégagé » est obligatoire", ErrNeedsApproval)
	}
	conf, j, err := s.confirmable(ctx, confID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	pending := s.approv[confID] != nil
	s.mu.Unlock()
	if !pending {
		return nil, fmt.Errorf("%w : le service a redémarré depuis la demande, refaire request_print_confirmation", contracts.ErrConfirmationExpired)
	}
	subj := humanauth.ApprovalSubject{Kind: "print", ID: confID, Binding: conf.BindingFingerprint}
	info, err := s.passkeys().FinishApproval(ctx, r, subj, assertion)
	if err != nil {
		return nil, err
	}
	_ = s.d.Store.Audit(ctx, "human:passkey", "print.confirmation_passkey_accepted", "print_job", j.ID, map[string]any{"confirmation": confID, "passkey": info.ID})
	return s.ApproveAndStart(ctx, confID, true, "passkey")
}

// StopWithPasskey arrête l'impression visée par une demande d'arrêt précise si l'assertion de la passkey est valide.
func (s *Service) StopWithPasskey(ctx context.Context, stopID string, assertion []byte, r *http.Request) (*CancelOutput, error) {
	v, err := s.GetStop(ctx, stopID)
	if err != nil {
		return nil, err
	}
	subj := humanauth.ApprovalSubject{Kind: "stop", ID: stopID, Binding: v.PrintJobID}
	info, err := s.passkeys().FinishApproval(ctx, r, subj, assertion)
	if err != nil {
		return nil, err
	}
	_ = s.d.Store.Audit(ctx, "human:passkey", "print.stop_passkey_accepted", "print_job", v.PrintJobID, map[string]any{"stop": stopID, "passkey": info.ID})
	return s.ExecuteStop(ctx, stopID)
}

// passkeyBegin prépare les options de la page d'approbation. Renvoie (options JSON, message d'indisponibilité) : les
// options sont vides s'il n'y a aucune passkey (rien à dire) ou si l'environnement ne s'y prête pas (message).
func (s *Service) passkeyBegin(ctx context.Context, r *http.Request, subj humanauth.ApprovalSubject) (string, string) {
	pk := s.passkeys()
	n, err := pk.Count(ctx)
	if err != nil || n == 0 {
		return "", ""
	}
	opts, err := pk.BeginApproval(ctx, r, subj)
	if err != nil {
		return "", err.Error()
	}
	return string(opts), ""
}

// isPasskeyError : erreurs de passkey dont le texte est sûr à montrer à l'humain.
func isPasskeyError(err error) bool {
	for _, e := range []error{humanauth.ErrPasskeyRequired, humanauth.ErrPasskeyChallenge, humanauth.ErrPasskeyInvalid,
		humanauth.ErrPasskeyCounter, humanauth.ErrPasskeyNone, humanauth.ErrPasskeyInsecure, humanauth.ErrPasskeyIPHost, humanauth.ErrPasskeyOrigin} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}
