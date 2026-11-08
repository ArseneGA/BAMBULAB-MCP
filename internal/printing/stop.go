package printing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

// Arrêt validé par l'humain : journalisé de bout en bout et VÉRIFIÉ.
//
// Défaut de terrain : cancel_print -> stop_url validée -> chauffes coupées, mais gcode_state resté RUNNING ; l'ancien code
// attendait un état IDLE/FAILED/FINISH, concluait « arrêt en échec » après 10 s et ne laissait AUCUNE trace dans
// l'historique (l'impression restait PRINTING). Maintenant : demande, validation humaine, commande envoyée, accusé et
// vérification de l'effet sont des évènements horodatés de l'impression ; si l'état ne change pas, on regarde l'effet réel
// (chauffes coupées, plus aucun mouvement) ; si l'imprimante reste RUNNING, on le dit et on propose de la redémarrer.
// Une seule commande d'arrêt part par validation : jamais de renvoi automatique.

// ExecuteStop arrête l'impression après la validation HUMAINE de la demande (appelé par la page /stop/{id}
// d'un navigateur enregistré, jamais par MCP). La demande n'est consommée qu'une fois l'arrêt constaté.
func (s *Service) ExecuteStop(ctx context.Context, stopID string) (*CancelOutput, error) {
	return s.executeStop(ctx, stopID, "web")
}

const restartAdvice = "Redémarrez l'imprimante (éteignez-la puis rallumez-la, ou acquittez le message sur son écran) avant de relancer : bambud ne peut pas la redémarrer à distance."

func (s *Service) executeStop(ctx context.Context, stopID, via string) (*CancelOutput, error) {
	v, err := s.GetStop(ctx, stopID)
	if err != nil {
		return nil, err
	}
	j, err := s.GetJob(ctx, v.PrintJobID)
	if err != nil {
		return nil, err
	}
	ad, _, err := s.adapter(ctx, j.PrinterID)
	if err != nil {
		s.journal(ctx, j.ID, "stop_failed", "arrêt validé par l'humain ("+via+") mais l'imprimante est injoignable : "+err.Error())
		return nil, err
	}
	s.journal(ctx, j.ID, "stop_approved", "arrêt validé par l'humain ("+via+")")
	_ = s.d.Store.Audit(ctx, "human:"+via, "print.stop_approved", "print_job", j.ID, map[string]any{"stop": v.StopID})
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	s.mu.Lock()
	s.stopping[j.ID] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.stopping, j.ID)
		s.mu.Unlock()
	}()
	s.journal(ctx, j.ID, "stop_sent", "commande d'arrêt envoyée à l'imprimante (une seule, jamais renvoyée automatiquement)")
	serr := ad.Stop(cctx)
	applied, residual := serr == nil, false
	switch {
	case serr == nil:
		s.journal(ctx, j.ID, "stop_acked", "l'imprimante a accepté l'arrêt et son état a changé")
	case errors.Is(serr, printer.ErrStateNotObserved):
		s.journal(ctx, j.ID, "stop_acked", "l'imprimante a acquitté l'arrêt mais son état n'a pas changé dans le délai : vérification de l'effet réel (chauffes, mouvement)")
		applied, residual = s.verifyStopApplied(cctx, ad)
		if applied {
			s.journal(ctx, j.ID, "stop_verified", map[bool]string{
				true:  "effet constaté : chauffes coupées et plus aucun mouvement, mais gcode_state est resté RUNNING (état résiduel de l'imprimante)",
				false: "effet constaté : l'imprimante a changé d'état",
			}[residual])
		}
	default:
		s.journal(ctx, j.ID, "stop_failed", "l'arrêt n'a pas abouti : "+serr.Error())
		return nil, fmt.Errorf("arrêt de l'imprimante en échec : %w", serr)
	}
	if !applied {
		msg := "arrêt acquitté par l'imprimante mais NON appliqué après vérification : elle chauffe ou imprime encore. " + restartAdvice
		s.journal(ctx, j.ID, "stop_not_applied", msg)
		s.emitAlert(j, "stop_not_applied", msg)
		_ = s.d.Store.Audit(ctx, "human:"+via, "print.stop_not_applied", "print_job", j.ID, nil)
		return nil, fmt.Errorf("%s", msg)
	}
	s.mu.Lock()
	delete(s.stops, v.StopID)
	s.stopGone[v.StopID] = goneStop{reason: "used", at: s.now()}
	s.mu.Unlock()
	note := "Commande d'arrêt acceptée par l'imprimante."
	if _, err = s.transition(ctx, j.ID, contracts.EvCancel, "arrêt validé par l'utilisateur ("+via+")", func(x *Job) error {
		x.StoppedByUser = true
		if residual && x.Print != nil {
			cp := copyProgress(x.Print)
			cp.Attention = "arrêt appliqué (chauffes coupées) mais l'imprimante affiche encore RUNNING : " + restartAdvice
			x.Print = &cp
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if residual {
		note = "Arrêt appliqué (chauffes coupées, plus aucun mouvement) mais l'imprimante affiche encore RUNNING. " + restartAdvice
		s.emitAlert(j, "stop_not_applied", note)
	}
	_ = s.d.Store.Audit(ctx, "human:"+via, "print.stopped", "print_job", j.ID, map[string]any{"residual_running": residual})
	cur, _ := s.GetJob(ctx, j.ID)
	return &CancelOutput{PrintJobID: j.ID, Outcome: "stopped", State: cur.State, Note: note, Job: cur}, nil
}

// verifyStopApplied surveille l'imprimante après un arrêt acquitté dont l'état n'a pas changé. applied : état final
// (IDLE / FAILED / FINISH) OU chauffes coupées (consignes buse et plateau à 0) avec couche, % et temps restant immobiles
// depuis un court moment. residual : appliqué sans changement d'état (gcode_state encore RUNNING).
func (s *Service) verifyStopApplied(ctx context.Context, ad printer.Adapter) (applied, residual bool) {
	wc := s.watchCfg()
	hold := min(3*time.Second, wc.StopVerifyWait/4)
	deadline := time.Now().Add(wc.StopVerifyWait)
	var offSince time.Time
	var sig string
	for time.Now().Before(deadline) && ctx.Err() == nil {
		st, err := ad.Status(ctx)
		if err == nil && st.Connected {
			switch st.State {
			case printer.StateIdle, printer.StateFailed, printer.StateFinished:
				return true, false
			}
			cur := fmt.Sprintf("%d/%d/%d", st.Layer, st.Progress, st.RemainingMin)
			if st.Temps.NozzleTarget == 0 && st.Temps.BedTarget == 0 {
				if offSince.IsZero() || cur != sig {
					offSince, sig = time.Now(), cur
				} else if time.Since(offSince) >= hold {
					return true, true
				}
			} else {
				offSince = time.Time{}
			}
		}
		sleepCtx(ctx, 100*time.Millisecond)
	}
	return false, false
}
