package printing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

// Attente synchrone maximale d'un outil long avant de rendre job_id + état (le client MCP abandonne à 60 s
// alors que le travail, lui, continue : l'appel ne doit JAMAIS sembler en échec).
const defaultSyncWait = 20 * time.Second

func (s *Service) syncWait() time.Duration {
	if s.d.SyncWait > 0 {
		return s.d.SyncWait
	}
	return defaultSyncWait
}

// progressOfQueue donne l'avancement du travail de file (pourcentage, phase, octets, ETA, position).
func (s *Service) progressOfQueue(ctx context.Context, id string) *jobs.Progress {
	if id == "" {
		return nil
	}
	q, err := s.d.Queue.Get(ctx, id)
	if err != nil {
		return nil
	}
	p := s.d.Queue.ProgressOf(ctx, q)
	return &p
}

// ProgressFor : avancement de la phase en cours d'une impression (découpe, dépôt, impression).
func (s *Service) ProgressFor(ctx context.Context, j Job) *jobs.Progress {
	switch j.State {
	case contracts.PrintPrepared, contracts.PrintSlicing:
		return s.progressOfQueue(ctx, j.SliceJobID)
	case contracts.PrintUploading:
		return s.progressOfQueue(ctx, j.UploadJobID)
	case contracts.PrintPrinting:
		p := jobs.Progress{JobID: j.ID, State: string(j.State), Phase: "impression"}
		if j.Print != nil {
			p.Percent = j.Print.Percent
			p.Message = fmt.Sprintf("couche %d/%d", j.Print.Layer, j.Print.TotalLayers)
			if j.Print.RemainingMin > 0 {
				eta := j.Print.RemainingMin * 60
				p.ETASeconds = &eta
			}
		}
		return &p
	}
	return nil
}

// sliceTimeoutBeyond : au-delà de ce délai sans fin ni battement, une découpe est déclarée perdue.
const sliceMaxAge = 45 * time.Minute

// CheckConsistency réconcilie une impression en PREPARED / SLICING / UPLOADING avec son travail de file :
// travail échoué, annulé, perdu, bloqué (sans battement) -> état final explicite. Retourne l'impression à jour.
// Appelé à chaque lecture (GetPrintStatus, liste) et périodiquement.
func (s *Service) CheckConsistency(ctx context.Context, j Job) Job {
	var qid string
	switch j.State {
	case contracts.PrintPrepared, contracts.PrintSlicing:
		qid = j.SliceJobID
	case contracts.PrintUploading:
		qid = j.UploadJobID
	default:
		return j
	}
	if qid == "" {
		if j.State == contracts.PrintPrepared && s.now().Sub(j.UpdatedAt) > 2*time.Minute {
			return s.closeOrphan(ctx, j, "découpe jamais mise en file (travail introuvable)")
		}
		return j
	}
	q, err := s.d.Queue.Get(ctx, qid)
	if err != nil {
		if errors.Is(err, jobs.ErrNotFound) {
			return s.closeOrphan(ctx, j, "travail de file introuvable (supprimé ou base restaurée)")
		}
		return j
	}
	switch q.State {
	case contracts.JobFailed:
		msg := "travail de file en échec"
		if q.Error != nil {
			msg += " : " + q.Error.Message
		}
		return s.closeOrphan(ctx, j, msg)
	case contracts.JobCancelled:
		return s.closeOrphan(ctx, j, "travail de file annulé (cancel_job) sans mise à jour de l'impression")
	case contracts.JobUnknown:
		return s.closeOrphan(ctx, j, "issue du travail de file inconnue (service interrompu pendant la tâche)")
	case contracts.JobSucceeded:
		// la découpe a fini mais l'impression n'a pas avancé : crash entre les deux écritures
		if j.State == contracts.PrintSlicing || j.State == contracts.PrintPrepared {
			if cur, e := s.GetJob(ctx, j.ID); e == nil && (cur.State == contracts.PrintSlicing || cur.State == contracts.PrintPrepared) {
				return s.closeOrphan(ctx, j, "travail de file terminé sans résultat enregistré pour l'impression")
			}
		}
	case contracts.JobRunning:
		stalled := q.HeartbeatAt != nil && s.now().Sub(*q.HeartbeatAt) > jobs.StallAfter*4
		tooOld := q.StartedAt != nil && s.now().Sub(*q.StartedAt) > sliceMaxAge
		if stalled || tooOld {
			_, _ = s.d.Queue.RequestCancel(ctx, qid)
			why := "sans signe de vie depuis plus de 3 minutes"
			if tooOld && !stalled {
				why = fmt.Sprintf("en cours depuis plus de %d minutes", int(sliceMaxAge.Minutes()))
			}
			return s.closeOrphan(ctx, j, "travail "+why+" : annulé automatiquement")
		}
	}
	return j
}

// closeOrphan amène une impression orpheline à un état final explicite (échec si la machine d'états le permet,
// sinon annulation) avec la raison.
func (s *Service) closeOrphan(ctx context.Context, j Job, reason string) Job {
	msg := "orpheline : " + reason
	set := func(x *Job) error { x.LastError = msg; return nil }
	var err error
	if _, err = s.transition(ctx, j.ID, contracts.EvFail, msg, set); err != nil {
		_, err = s.transition(ctx, j.ID, contracts.EvCancel, msg, set)
	}
	if err != nil {
		s.log.Warn("impression orpheline non fermée", "print_job", j.ID, "err", err)
		return j
	}
	cur, e := s.GetJob(ctx, j.ID)
	if e != nil {
		return j
	}
	return cur
}

// passiveReconcile : START_UNKNOWN -> PRINTING sans jamais envoyer de commande. Se fonde sur le DERNIER rapport
// de l'imprimante (lecture du cache MQTT) : s'il est récent, postérieur à la demande de lancement et qu'il
// désigne EXACTEMENT ce fichier en préparation / impression / pause, l'impression a bien démarré. Toute autre
// situation (idle, autre fichier, rapport périmé) laisse l'état inchangé : seul reconcile_print peut conclure
// « non démarré ».
func (s *Service) passiveReconcile(ctx context.Context, j Job) (Job, bool) {
	if j.State != contracts.PrintStartUnknown || j.Remote == nil {
		return j, false
	}
	ad, _, err := s.adapter(ctx, j.PrinterID)
	if err != nil {
		return j, false
	}
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	st, err := ad.Status(cctx)
	if err != nil || !st.Connected || st.Stale {
		return j, false
	}
	active := st.State == printer.StatePreparing || st.State == printer.StatePrinting || st.State == printer.StatePaused
	if !active || !matches(st, j.Remote.Name) {
		return j, false
	}
	if since := startRequestedAt(j); !since.IsZero() && st.UpdatedAt.Before(since) {
		return j, false
	}
	cur, err := s.transition(ctx, j.ID, contracts.EvReconcilePrinting, "réconciliation automatique en lecture : l'imprimante montre ce fichier en cours ("+string(st.State)+")", func(x *Job) error {
		x.LastError = ""
		return nil
	})
	if err != nil {
		return j, false
	}
	s.observe(ctx, j.ID, st)
	s.track(j.ID)
	_ = s.d.Store.Audit(ctx, actor, "print.passive_reconcile", "print_job", j.ID, map[string]any{"printer_state": string(st.State)})
	return cur, true
}

// SweepOrphans passe en revue les impressions en découpe/dépôt (maintenance périodique).
func (s *Service) SweepOrphans(ctx context.Context) int {
	n := 0
	for _, st := range []contracts.PrintState{contracts.PrintPrepared, contracts.PrintSlicing, contracts.PrintUploading, contracts.PrintStartUnknown} {
		js, err := s.ListJobs(ctx, st, 200)
		if err != nil {
			continue
		}
		for _, j := range js {
			if st == contracts.PrintStartUnknown {
				if cur, ok := s.passiveReconcile(ctx, j); ok && cur.State != j.State {
					n++
				}
				continue
			}
			if cur := s.CheckConsistency(ctx, j); cur.State != j.State {
				n++
			}
		}
	}
	return n
}
