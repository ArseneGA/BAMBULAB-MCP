package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// Attente bloquante (interrogation longue) et notifications de progression.
//
// Quand le client fournit un progressToken (_meta.progressToken de tools/call, spec MCP), les outils qui attendent
// émettent des notifications/progress : progress = pourcentage (0..100, strictement croissant), total = 100,
// message = la ligne de barre lisible (« Dépôt sur la P1S : ███████░░░ 72 % (21,6 / 30 Mo, ~1 min) »). Sans token,
// rien n'est émis : le résultat de l'outil porte la même information.

// progressNotifier renvoie la fonction qui émet un notifications/progress, ou nil si le client n'a pas fourni de
// progressToken (ou si la session est absente).
func progressNotifier(ctx context.Context, req *mcp.CallToolRequest) func(jobs.Progress) {
	if req == nil || req.Params == nil || req.Session == nil {
		return nil
	}
	tok := req.Params.GetProgressToken()
	if tok == nil {
		return nil
	}
	var mu sync.Mutex
	last := -1.0
	return func(p jobs.Progress) {
		mu.Lock()
		v := float64(p.Percent)
		if v <= last { // la spec exige un progrès strictement croissant
			v = last + 0.01
		}
		last = v
		mu.Unlock()
		msg := p.Text
		if msg == "" {
			msg = p.Message
		}
		_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: tok, Progress: v, Total: 100, Message: msg})
	}
}

// WaitForJobInput est l'entrée de wait_for_job.
type WaitForJobInput struct {
	JobID          string `json:"job_id" jsonschema:"identifiant du travail, ex. job_ab12..."`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"attente maximale en secondes (1 à 60, défaut 25)"`
}

// WaitForJobOutput : l'état du travail à la fin de l'attente.
type WaitForJobOutput struct {
	JobStatusOut
	Wait printing.WaitInfo `json:"wait" jsonschema:"changed : l'état a changé ; timed_out : échéance atteinte (le travail continue, rappeler wait_for_job)"`
}

const jobPollEvery = 400 * time.Millisecond

func (h *handlers) waitForJob(ctx context.Context, req *mcp.CallToolRequest, in WaitForJobInput) (*mcp.CallToolResult, WaitForJobOutput, error) {
	timeout := printing.ClampWait(in.TimeoutSeconds)
	start := time.Now()
	cur, err := h.c.Queue.Get(ctx, in.JobID)
	if err != nil {
		return nil, WaitForJobOutput{}, err
	}
	first := cur.State
	sink := progressNotifier(ctx, req)
	var lastSink time.Time
	reason := ""
	for {
		if cur.State.Terminal() {
			if first.Terminal() {
				reason = "travail déjà terminé"
			} else {
				reason = fmt.Sprintf("état : %s -> %s", first, cur.State)
			}
			break
		}
		if cur.State != first {
			reason = fmt.Sprintf("état : %s -> %s", first, cur.State)
			break
		}
		if sink != nil && time.Since(lastSink) >= time.Second {
			lastSink = time.Now()
			sink(h.c.Queue.ProgressOf(ctx, cur))
		}
		if time.Since(start) >= timeout || ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(jobPollEvery):
		}
		if next, gerr := h.c.Queue.Get(ctx, in.JobID); gerr == nil {
			cur = next
		}
	}
	st, txt := h.jobStatusOf(ctx, cur, 20)
	if sink != nil {
		sink(st.Detail) // dernière notification : l'état final
	}
	changed := reason != "" && reason != "travail déjà terminé"
	info := printing.WaitInfo{Changed: changed, TimedOut: reason == "", WaitedSeconds: int(time.Since(start).Seconds()), Reason: reason}
	switch {
	case info.TimedOut:
		txt += " Échéance de l'attente atteinte sans changement : le travail continue, rappeler wait_for_job."
	case changed:
		txt += " (" + reason + ")"
	}
	return textResult(txt), WaitForJobOutput{JobStatusOut: st, Wait: info}, nil
}

// PrintStatusInput : get_print_status avec attente facultative.
type PrintStatusInput struct {
	PrintJobID  string `json:"print_job_id"`
	WaitSeconds int    `json:"wait_seconds,omitempty" jsonschema:"si > 0 (max 60) : attend jusqu'à ce que l'état de l'impression, de l'imprimante ou l'étape change, puis rend l'état avec la progression"`
	WithPhoto   *bool  `json:"with_photo,omitempty" jsonschema:"joindre un instantané de la caméra (image) au résultat texte ; défaut : vrai si l'impression est en cours, faux sinon. Faux pour un suivi texte seul"`
	Detail      bool   `json:"detail,omitempty" jsonschema:"vrai : réponse COMPLÈTE (rapport de validation détaillé, résumé de découpe, réglages figés, tout l'historique). Défaut faux : réponse compacte (progression, étape, causes actives, alertes, verdict de validation en bref)"`
}

// WaitPrintInput : entrée de wait_for_print_change.
type WaitPrintInput struct {
	PrintJobID     string `json:"print_job_id"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"attente maximale en secondes (1 à 60, défaut 25)"`
}

// printStatusText : texte lisible d'un suivi d'impression (état, progression, étapes vues, erreurs décodées).
func printStatusText(st *printing.PrintStatus) string {
	txt := fmt.Sprintf("Impression %s : %s.", st.Job.ID, st.Job.State)
	if st.Progress != nil && st.Progress.Text != "" {
		txt += " " + st.Progress.Text + "."
	}
	if st.Job.Print != nil {
		txt += fmt.Sprintf(" Imprimante : %s, %d %%, couche %d/%d.", st.Job.Print.PrinterState, st.Job.Print.Percent, st.Job.Print.Layer, st.Job.Print.TotalLayers)
	}
	if line := st.Stages.StageLine(); line != "" {
		txt += " " + line
		if st.Stages.Current != "" {
			txt += " Étape en cours : " + st.Stages.Current + "."
		}
		if st.Stages.Note != "" {
			txt += " " + st.Stages.Note
		}
	}
	if st.Alert != "" {
		txt += " À SIGNALER À L'UTILISATEUR : " + st.Alert + "."
	}
	if p := st.Printer; p != nil && len(p.ErrorDetails) > 0 {
		var parts []string
		for _, d := range p.ErrorDetails {
			parts = append(parts, d.Short+" : "+d.Text)
		}
		txt += " Erreurs de l'imprimante : " + strings.Join(parts, " ; ") + "."
	}
	if st.Job.LastError != "" {
		txt += " Dernière erreur : " + st.Job.LastError
	}
	if st.Job.Next != "" {
		txt += " Suite : " + st.Job.Next + "."
	}
	if st.Wait != nil {
		switch {
		case st.Wait.Changed:
			txt += " (changement : " + st.Wait.Reason + ")"
		case st.Wait.TimedOut:
			txt += " Échéance de l'attente sans changement : rappeler wait_for_print_change."
		case st.Wait.Reason != "":
			txt += " (" + st.Wait.Reason + ")"
		}
	}
	return txt
}

// AddWaitTools enregistre wait_for_job et wait_for_print_change (lecture seule : aucune commande n'est envoyée).
func AddWaitTools(s *mcp.Server, c *core.Core) {
	h := &handlers{c: c}
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	addTool(s, &mcp.Tool{
		Name: "wait_for_job", Title: "Attendre un travail",
		Description: "Interrogation longue : attend jusqu'à timeout_seconds (1 à 60, défaut 25) que l'état du travail change (en file -> en cours -> terminé), puis rend l'état avec la progression RÉELLE " +
			"(percent, phase_code / phase_label, done_bytes / total_bytes, eta_seconds, bar, text). Si le client fournit un progressToken, des notifications/progress sont émises pendant l'attente. " +
			"À l'échéance le travail CONTINUE (wait.timed_out = true) : rappeler wait_for_job. Évite de boucler sur get_job_status.",
		Annotations: ro,
	}, h.waitForJob)
	if c.Printing != nil {
		p := c.Printing
		addTool(s, &mcp.Tool{
			Name: "wait_for_print_change", Title: "Attendre un changement d'impression",
			Description: "Interrogation longue sur une impression : attend jusqu'à timeout_seconds (1 à 60, défaut 25) que l'état de l'impression, celui de l'imprimante ou l'étape courante (mise à niveau, chauffe, calibration…) change, " +
				"puis rend le suivi complet (progression, étapes vues, erreurs décodées). Lecture seule. À l'échéance, rappeler.",
			Annotations: ro,
		}, func(ctx context.Context, req *mcp.CallToolRequest, in WaitPrintInput) (*mcp.CallToolResult, printing.PrintStatus, error) {
			st, err := p.WaitPrintChange(ctx, in.PrintJobID, printing.ClampWait(in.TimeoutSeconds), progressNotifier(ctx, req))
			if err != nil {
				return nil, printing.PrintStatus{}, err
			}
			return textResult(printStatusText(st)), *st, nil
		})
	}
}
