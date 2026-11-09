package core

import (
	"context"
	"fmt"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// BusyJobRunning : un travail (découpe, analyse, CAO, import...) est en cours d'exécution.
const BusyJobRunning = "job_running"

// BusyReport dit si redémarrer le service maintenant risque de perdre ou d'interrompre quelque chose.
// Il sert à la mise à jour automatique (internal/upgrade) et à GET /api/system/busy ; lecture seule.
type BusyReport struct {
	Busy    bool                  `json:"busy"`
	Reasons []printing.BusyReason `json:"reasons"`
}

// Busy calcule l'occupation : impression active (ou incertaine), imprimante au travail, confirmation / arrêt / action
// en attente de l'humain, dépôt ou découpe en cours, travail en cours d'exécution.
func (c *Core) Busy(ctx context.Context) BusyReport {
	rep := BusyReport{Reasons: []printing.BusyReason{}}
	if c.Printing != nil {
		rep.Reasons = append(rep.Reasons, c.Printing.BusyReasons(ctx)...)
	}
	if c.Queue != nil {
		jobs, err := c.Queue.List(ctx, contracts.JobRunning, 100)
		switch {
		case err != nil:
			rep.Reasons = append(rep.Reasons, printing.BusyReason{Code: BusyJobRunning, Detail: "travaux illisibles : " + err.Error()})
		case len(jobs) > 0:
			heavy := 0
			for _, j := range jobs {
				if j.Class == contracts.ClassHeavy {
					heavy++
				}
			}
			rep.Reasons = append(rep.Reasons, printing.BusyReason{Code: BusyJobRunning,
				Detail: fmt.Sprintf("%d travail(aux) en cours, dont %d lourd(s) (%s)", len(jobs), heavy, jobs[0].Kind)})
		}
	}
	rep.Busy = len(rep.Reasons) > 0
	return rep
}
