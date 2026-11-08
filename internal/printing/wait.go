package printing

import (
	"context"
	"fmt"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
)

// Attente (interrogation longue) d'un changement sur une impression : rend dès que l'état de l'impression, celui de
// l'imprimante ou l'étape courante change, ou à l'échéance, avec la progression. Lecture seule : aucune commande
// n'est envoyée à l'imprimante.

const (
	// DefaultWaitSeconds / MaxWaitSeconds : bornes de l'attente d'un outil (le client MCP abandonne vers 60 s).
	DefaultWaitSeconds = 25
	MaxWaitSeconds     = 60
)

// ClampWait normalise une durée d'attente en secondes : 0 -> défaut, plafonnée à MaxWaitSeconds.
func ClampWait(sec int) time.Duration {
	if sec <= 0 {
		sec = DefaultWaitSeconds
	}
	if sec > MaxWaitSeconds {
		sec = MaxWaitSeconds
	}
	return time.Duration(sec) * time.Second
}

// WaitInfo décrit l'attente qui a eu lieu.
type WaitInfo struct {
	Changed       bool   `json:"changed" jsonschema:"vrai si quelque chose a changé pendant l'attente"`
	TimedOut      bool   `json:"timed_out" jsonschema:"vrai si l'échéance est atteinte sans changement : rappeler pour continuer"`
	WaitedSeconds int    `json:"waited_seconds"`
	Reason        string `json:"reason,omitempty" jsonschema:"ce qui a changé : état de l'impression, état de l'imprimante, étape, fin"`
}

// printSig : ce qui, en changeant, réveille l'attente.
type printSig struct{ job, printer, stage, alert string }

func sigOf(ps *PrintStatus) printSig {
	s := printSig{job: string(ps.Job.State), alert: ps.Alert}
	if ps.Printer != nil {
		s.printer = ps.Printer.GcodeState + "/" + ps.Printer.State
		s.stage = ps.Printer.StageName
	}
	return s
}

func terminalPrint(ps *PrintStatus) bool {
	switch ps.Job.State {
	case "COMPLETED", "FAILED", "CANCELLED", "START_ABORTED_BEFORE_SEND":
		return true
	}
	return false
}

// WaitPrintChange relit l'impression jusqu'à un changement ou à l'échéance. sink (facultatif) reçoit l'avancement
// (au plus une fois par seconde) pour les notifications/progress du client.
func (s *Service) WaitPrintChange(ctx context.Context, id string, timeout time.Duration, sink func(jobs.Progress)) (*PrintStatus, error) {
	start := time.Now()
	ps, err := s.GetPrintStatus(ctx, id)
	if err != nil {
		return nil, err
	}
	first := sigOf(ps)
	finish := func(changed bool, reason string, cur *PrintStatus) *PrintStatus {
		cur.Wait = &WaitInfo{Changed: changed, TimedOut: !changed && reason == "", WaitedSeconds: int(time.Since(start).Seconds()), Reason: reason}
		return cur
	}
	if terminalPrint(ps) {
		return finish(false, "impression terminée (état final déjà atteint)", ps), nil
	}
	var lastSink time.Time
	for {
		if sink != nil && ps.Progress != nil && time.Since(lastSink) >= time.Second {
			lastSink = time.Now()
			sink(*ps.Progress)
		}
		if time.Since(start) >= timeout || ctx.Err() != nil {
			return finish(false, "", ps), nil
		}
		sleepCtx(ctx, time.Second)
		if ctx.Err() != nil {
			return finish(false, "", ps), nil
		}
		ps, err = s.GetPrintStatus(ctx, id)
		if err != nil {
			return nil, err
		}
		cur := sigOf(ps)
		switch {
		case cur.job != first.job:
			return finish(true, fmt.Sprintf("état de l'impression : %s -> %s", first.job, cur.job), ps), nil
		case cur.printer != first.printer:
			return finish(true, fmt.Sprintf("état de l'imprimante : %s -> %s", first.printer, cur.printer), ps), nil
		case cur.stage != first.stage:
			return finish(true, fmt.Sprintf("étape de l'imprimante : %q -> %q", first.stage, cur.stage), ps), nil
		}
	}
}
