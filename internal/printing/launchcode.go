package printing

// Approbation par code de lancement personnel (docs/SECURITY.md § 12).
//
// Un modèle PEUT appeler les outils app-only qui s'appuient sur ces fonctions : la sécurité repose sur le CODE
// (secret de l'humain, haché, verrouillé après 3 échecs), jamais sur la visibilité de l'outil. Le code n'est ni
// stocké, ni journalisé, ni renvoyé ici : il passe de l'argument de l'outil à humanauth.LaunchCodes.Verify, c'est tout.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
)

// LaunchCodeAttempts : essais avant verrouillage global.
const LaunchCodeAttempts = humanauth.LaunchMaxFailures

func (s *Service) launchCodes() *humanauth.LaunchCodes { return humanauth.LaunchCodesFor(s.d.Store) }

// LaunchCodeStatus : le code est-il défini, verrouillé ? (aucun secret)
func (s *Service) LaunchCodeStatus(ctx context.Context) (humanauth.LaunchCodeStatus, error) {
	return s.launchCodes().Status(ctx)
}

// ApproveWithCode donne l'accord de lancement d'une confirmation précise si le code de lancement est correct.
// La tentative n'est examinée que sur une confirmation vivante, non consommée, dont la liaison (imprimante,
// empreinte, plateau, matières) n'a pas bougé ; l'accord qui en résulte ne vaut que pour CETTE confirmation et
// 2 minutes (start_print, ConsumeConfirmation : inchangés). plateClear est l'attestation « plateau vide ».
func (s *Service) ApproveWithCode(ctx context.Context, confID, code string, plateClear bool) (*ApprovalResult, error) {
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
	if err := s.launchCodes().Verify(ctx, code, map[string]any{"confirmation": confID, "print_job": j.ID, "binding": conf.BindingFingerprint}); err != nil {
		return nil, err
	}
	_ = s.d.Store.Audit(ctx, "human:app-code", "print.confirmation_code_accepted", "print_job", j.ID, map[string]any{"confirmation": confID})
	return s.ApproveAndStart(ctx, confID, true, "app-code") // le démarrage suit l'accord, sans passer par le modèle
}

// StopWithCode arrête l'impression d'une demande d'arrêt précise si le code de lancement est correct.
func (s *Service) StopWithCode(ctx context.Context, stopID, code string) (*CancelOutput, error) {
	v, err := s.GetStop(ctx, stopID)
	if err != nil {
		return nil, err
	}
	if err := s.launchCodes().Verify(ctx, code, map[string]any{"stop": stopID, "print_job": v.PrintJobID}); err != nil {
		return nil, err
	}
	_ = s.d.Store.Audit(ctx, "human:app-code", "print.stop_code_accepted", "print_job", v.PrintJobID, map[string]any{"stop": stopID})
	return s.ExecuteStop(ctx, stopID)
}

// PlatePhotoForConfirmation prend la photo du plateau pour la vue de confirmation (nil si aucune caméra).
func (s *Service) PlatePhotoForConfirmation(ctx context.Context, confID string) (*PlatePhoto, error) {
	_, j, err := s.confirmable(ctx, confID)
	if err != nil {
		return nil, err
	}
	return s.platePhotoFor(ctx, j.PrinterID), nil
}

// ConfirmationApprovalLeft : durée restante de l'accord humain (0 si non approuvé).
func (s *Service) ConfirmationApprovalLeft(confID string) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.approv[confID]
	if a == nil || !a.approved {
		return 0
	}
	left := a.approvedAt.Add(s.approvalTTL()).Sub(s.now())
	return max(left, 0)
}

// LaunchCodeProblem classe une erreur de vérification en code court pour la vue : bad_code, locked, too_fast,
// not_set, format. Chaîne vide si l'erreur n'est pas liée au code.
func LaunchCodeProblem(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, humanauth.ErrLaunchCodeBad):
		return "bad_code"
	case errors.Is(err, humanauth.ErrLaunchCodeLocked):
		return "locked"
	case errors.Is(err, humanauth.ErrLaunchCodeTooFast):
		return "too_fast"
	case errors.Is(err, humanauth.ErrLaunchCodeNotSet):
		return "not_set"
	case errors.Is(err, humanauth.ErrLaunchCodeFormat):
		return "format"
	}
	return ""
}

// StopIDFromURL extrait l'identifiant d'une demande d'arrêt d'une URL /stop/{id} (ou d'un identifiant nu).
func StopIDFromURL(u string) string {
	u = strings.TrimSpace(u)
	if i := strings.LastIndex(u, "/stop/"); i >= 0 {
		u = u[i+len("/stop/"):]
	}
	return strings.Trim(u, "/ ")
}
