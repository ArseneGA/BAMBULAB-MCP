package printing

import (
	"context"
	"fmt"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

// lastJobOn : la dernière impression (par date de mise à jour) liée à cette imprimante.
func (s *Service) lastJobOn(ctx context.Context, printerID string) *Job {
	all, err := s.ListJobs(ctx, "", 300)
	if err != nil {
		return nil
	}
	var best *Job
	for i := range all {
		j := all[i]
		if j.PrinterID != printerID || j.State == contracts.PrintDraft || j.State == contracts.PrintPrepared || j.State == contracts.PrintSlicing || j.State == contracts.PrintValidated {
			continue
		}
		if best == nil || j.UpdatedAt.After(best.UpdatedAt) {
			b := j
			best = &b
		}
	}
	return best
}

// describeStatus remplit l'affichage lisible de l'état : un arrêt demandé par l'humain sur /stop s'affiche
// « arrêtée (annulée par vous) » au lieu du « failed » brut de Bambu, et le « failed » est expliqué.
func (s *Service) describeStatus(ctx context.Context, o *PrinterStatus, st printer.Status) {
	if !st.Connected {
		o.Display = "Injoignable"
		return
	}
	conn := "Joignable"
	if st.Stale {
		conn = "Joignable (info périmée)"
	}
	last := s.lastJobOn(ctx, o.PrinterID)
	if last != nil {
		o.LastJob = &LastJobInfo{ID: last.ID, Name: last.Name, State: last.State, StoppedByUser: last.StoppedByUser, At: last.UpdatedAt}
	}
	state := strings.ToLower(st.GcodeState)
	if state == "" {
		state = string(st.State)
	}
	o.Display = conn + " · " + state
	switch st.State {
	case printer.StateFailed:
		cause := "cause non précisée par l'imprimante"
		switch {
		case st.PrintError != 0 || len(st.HMS) > 0:
			cause = fmt.Sprintf("erreur signalée (print_error=%d, %d alerte(s) HMS : %s)", st.PrintError, len(st.HMS), strings.Join(o.HMSCodes, ", "))
		case st.FailReason != "":
			cause = "raison : " + st.FailReason
		}
		o.Explanation = "gcode_state FAILED : dans le protocole Bambu, FAILED signifie que la dernière impression s'est terminée SANS aller au bout — soit annulée (par l'écran, Bambu Studio ou le service), soit en erreur. Ici : "
		if last != nil && last.StoppedByUser && last.State == contracts.PrintCancelled {
			o.Display = conn + " · arrêtée (annulée par vous)"
			o.Explanation += "l'arrêt a été demandé et validé par vous sur la page /stop (" + last.Name + "). Ce n'est pas une panne."
		} else {
			o.Display = conn + " · échec ou annulation (failed)"
			o.Explanation += cause + "."
		}
	case printer.StateFinished:
		o.Display = conn + " · terminée"
		o.Explanation = "gcode_state FINISH : la dernière impression est allée au bout. L'imprimante reste dans cet état jusqu'à la suivante ; le plateau est peut-être encore plein."
	case printer.StateIdle:
		o.Display = conn + " · au repos"
	case printer.StatePrinting:
		o.Display = conn + " · impression en cours"
	case printer.StatePreparing:
		o.Display = conn + " · préparation"
	}
}
