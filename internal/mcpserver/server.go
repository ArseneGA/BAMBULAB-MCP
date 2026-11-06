// Package mcpserver expose le cœur via le SDK MCP officiel (Go) : outils,
// ressources MCP Apps. Les logs ne vont JAMAIS sur stdout (transport stdio).
package mcpserver

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
)

//go:embed ui/atelier.html
var atelierHTML string

// AtelierURI est la ressource UI de démonstration.
const AtelierURI = "ui://bambu/atelier.html"

const actor = "mcp"

// New construit le serveur MCP au-dessus du cœur.
func New(c *core.Core) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "bambu-mcp", Title: "Atelier Bambu Lab", Version: core.Version},
		&mcp.ServerOptions{
			Logger: c.Log,
			Instructions: "Atelier 3D pour imprimantes Bambu Lab. Les travaux longs renvoient un job_id : " +
				"suivre avec get_job_status. Aucune impression ne démarre sans confirmation explicite.",
		})
	h := &handlers{c: c}
	roAnn := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}

	addTool(s, &mcp.Tool{
		Name: "inspect_environment", Title: "Inspecter l'environnement",
		Description: "Versions, exécutables détectés (Bambu Studio, Python), droits d'écriture et éléments manquants. Aucune donnée sensible.",
		Annotations: roAnn,
	}, h.inspectEnvironment)

	addTool(s, &mcp.Tool{
		Name: "get_capabilities", Title: "Capacités du service",
		Description: "Fonctions réellement disponibles et leurs limites (une fonction non implémentée est annoncée indisponible).",
		Annotations: roAnn,
	}, h.getCapabilities)

	addTool(s, &mcp.Tool{
		Name: "get_job_status", Title: "État d'un travail",
		Description: "État, progression, journaux bornés (les plus récents), résultat ou erreur d'un travail.",
		Annotations: roAnn,
	}, h.getJobStatus)

	f := false
	addTool(s, &mcp.Tool{
		Name: "cancel_job", Title: "Annuler un travail",
		Description: "Annule un travail selon sa phase : en file = immédiat ; en cours = demande relayée au travail (état final ensuite) ; terminé = sans effet.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &f, IdempotentHint: true},
	}, h.cancelJob)

	AddAppResource(s, AppResource{
		URI: AtelierURI, Name: "atelier", Title: "Atelier (démonstration)",
		Description: "Vue de démonstration MCP Apps du jalon 1.", HTML: atelierHTML,
		// CSP vide : aucune connexion externe, tout passe par les outils.
	})
	addTool(s, &mcp.Tool{
		Name: "show_atelier", Title: "Ouvrir l'atelier",
		Description: "Affiche la vue Atelier (démonstration) dans l'interface de Claude quand l'hôte gère MCP Apps ; renvoie aussi un résumé texte.",
		Annotations: roAnn,
		Meta:        AppToolMeta(AtelierURI),
	}, h.showAtelier)
	return s
}

// addTool pose les schémas depuis contracts (source unique, voir contracts/schema.go).
func addTool[In, Out any](s *mcp.Server, t *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) {
	t.InputSchema = contracts.MustSchemaFor[In]()
	t.OutputSchema = contracts.MustSchemaFor[Out]()
	mcp.AddTool(s, t, h)
}

type handlers struct{ c *core.Core }

// ---- entrées/sorties ----

// Empty est l'entrée des outils sans paramètre.
type Empty struct{}

// GetCapabilitiesInput : session Studio facultative (réservée aux jalons suivants).
type GetCapabilitiesInput struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"session Bambu Studio éventuelle ; ignorée tant qu'aucun agent Windows n'est connecté"`
}

// JobIDInput identifie un travail.
type JobIDInput struct {
	JobID string `json:"job_id" jsonschema:"identifiant du travail, ex. job_ab12..."`
}

// GetJobStatusInput ajoute la taille de la fenêtre de journaux.
type GetJobStatusInput struct {
	JobID   string `json:"job_id" jsonschema:"identifiant du travail, ex. job_ab12..."`
	LogTail int    `json:"log_tail,omitempty" jsonschema:"nombre de lignes de journal à renvoyer (défaut 50, max 200)"`
}

// CancelJobOutput décrit l'issue d'une demande d'annulation.
type CancelJobOutput struct {
	JobID   string             `json:"job_id"`
	Outcome string             `json:"outcome" jsonschema:"cancelled | cancel_requested | already_finished"`
	State   contracts.JobState `json:"state"`
	Note    string             `json:"note"`
}

// AtelierSummary est le contenu structuré de show_atelier.
type AtelierSummary struct {
	Title        string `json:"title"`
	ServiceReady bool   `json:"service_ready"`
	ActiveJobs   int    `json:"active_jobs"`
	Demo         bool   `json:"demo" jsonschema:"toujours vrai au jalon 1 : aucune donnée réelle"`
}

// ---- outils ----

func (h *handlers) inspectEnvironment(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, contracts.EnvironmentReport, error) {
	rep, err := h.c.Environment(ctx)
	if err != nil {
		return nil, rep, err
	}
	txt := fmt.Sprintf("Service %s, %s/%s, schéma v%d. Manquants : %d.", rep.ServiceVersion, rep.OS, rep.Arch, rep.SchemaVersion, len(rep.Missing))
	return textResult(txt), rep, nil
}

func (h *handlers) getCapabilities(_ context.Context, _ *mcp.CallToolRequest, _ GetCapabilitiesInput) (*mcp.CallToolResult, contracts.Capabilities, error) {
	cp := h.c.Capabilities()
	n := 0
	for _, f := range cp.Features {
		if f.Available {
			n++
		}
	}
	return textResult(fmt.Sprintf("Mode %s, %d/%d fonctions disponibles.", cp.Mode, n, len(cp.Features))), cp, nil
}

func (h *handlers) getJobStatus(ctx context.Context, _ *mcp.CallToolRequest, in GetJobStatusInput) (*mcp.CallToolResult, contracts.Job, error) {
	j, err := h.c.Queue.Get(ctx, in.JobID)
	if err != nil {
		return nil, j, err
	}
	tail := in.LogTail
	if tail <= 0 {
		tail = 50
	}
	if tail > 200 {
		tail = 200
	}
	if len(j.Logs) > tail {
		j.Logs = j.Logs[len(j.Logs)-tail:]
	}
	return textResult(fmt.Sprintf("Travail %s (%s) : %s, %.0f %%.", j.ID, j.Kind, j.State, j.Progress*100)), j, nil
}

func (h *handlers) cancelJob(ctx context.Context, _ *mcp.CallToolRequest, in JobIDInput) (*mcp.CallToolResult, CancelJobOutput, error) {
	out := CancelJobOutput{JobID: in.JobID}
	oc, err := h.c.Queue.RequestCancel(ctx, in.JobID)
	if err != nil {
		if errors.Is(err, jobs.ErrNotFound) {
			return nil, out, fmt.Errorf("travail %q introuvable", in.JobID)
		}
		return nil, out, err
	}
	j, err := h.c.Queue.Get(ctx, in.JobID)
	if err != nil {
		return nil, out, err
	}
	out.Outcome, out.State = string(oc), j.State
	switch oc {
	case jobs.CancelledNow:
		out.Note = "Le travail attendait en file : annulé immédiatement."
	case jobs.CancelRequested:
		out.Note = "Annulation demandée : le travail s'arrête à son prochain point de contrôle. Suivre avec get_job_status ; l'état final est « cancelled » (ou « succeeded » s'il était déjà fini)."
	default:
		out.Note = "Le travail était déjà terminé : rien n'a été modifié."
	}
	h.c.Store.Audit(ctx, actor, "job.cancel_tool", "job", in.JobID, map[string]any{"outcome": out.Outcome}) //nolint:errcheck // l'audit détaillé est déjà écrit par la file
	return textResult(out.Note), out, nil
}

func (h *handlers) showAtelier(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, AtelierSummary, error) {
	active := 0
	for _, st := range []contracts.JobState{contracts.JobQueued, contracts.JobRunning} {
		js, err := h.c.Queue.List(ctx, st, 500)
		if err != nil {
			return nil, AtelierSummary{}, err
		}
		active += len(js)
	}
	sum := AtelierSummary{Title: "Atelier 3D (démonstration)", ServiceReady: true, ActiveJobs: active, Demo: true}
	return textResult(fmt.Sprintf("Atelier (démonstration) : service prêt, %d travail(aux) actif(s). Vue disponible dans les hôtes MCP Apps.", active)), sum, nil
}

func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}
