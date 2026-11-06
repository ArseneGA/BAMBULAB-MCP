package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
)

// Outils de la session Windows / Bambu Studio (jalon 8). Ils passent par l'agent
// Windows : sans agent en ligne, ils répondent « agent_offline » au lieu d'inventer
// un état. Linux ne lit pas l'état d'une application Windows (dossier §3).

// ProjectStateInput lit le projet ouvert dans Studio.
type ProjectStateInput struct {
	WaitSecs int `json:"wait_seconds,omitempty" jsonschema:"attendre la réponse de l'agent jusqu'à ce délai (défaut 20, max 60)"`
}

// OpenInStudioInput ouvre un fichier du catalogue ou un artefact dans Studio.
type OpenInStudioInput struct {
	SHA256   string `json:"sha256,omitempty" jsonschema:"empreinte d'un artefact du service (blob) à ouvrir"`
	ModelID  string `json:"model_id,omitempty" jsonschema:"ou un modèle du catalogue (son fichier courant, copié sur le service, est ouvert)"`
	FileName string `json:"file_name,omitempty" jsonschema:"nom du fichier à créer côté Windows (défaut : nom du modèle)"`
	WaitSecs int    `json:"wait_seconds,omitempty" jsonschema:"attendre la réponse de l'agent (défaut 20, max 60)"`
}

// ModifyOpenProjectInput : demande de modification en direct (non prise en charge).
type ModifyOpenProjectInput struct {
	Request string `json:"request,omitempty" jsonschema:"modification souhaitée (objets, supports, coutures, paramètres) ; sert seulement à expliquer l'alternative"`
}

// AgentToolOutput est la sortie commune des outils de session.
type AgentToolOutput struct {
	Status   string               `json:"status" jsonschema:"succeeded | failed | pending | agent_offline | not_supported | unknown"`
	TaskID   string               `json:"task_id,omitempty"`
	Result   json.RawMessage      `json:"result,omitempty"`
	Error    *agentlink.TaskError `json:"error,omitempty"`
	Message  string               `json:"message,omitempty"`
	Executor string               `json:"executor,omitempty"`
}

type agentHandlers struct {
	c   *core.Core
	reg *agentlink.Registry
}

func registerAgentTools(s *mcp.Server, c *core.Core) {
	reg, err := agentlink.For(c)
	if err != nil {
		c.Log.Error("outils agent indisponibles", "err", err)
		return
	}
	h := &agentHandlers{c: c, reg: reg}
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	f := false
	addTool(s, &mcp.Tool{
		Name: "get_project_state", Title: "État du projet ouvert dans Studio",
		Description: "Lit, via l'agent Windows, le projet ouvert dans Bambu Studio : processus, titre de fenêtre, fichier .3mf du projet (lu sur une COPIE, jamais modifié), objets, plateaux et paramètres de l'état SAUVEGARDÉ. Les modifications non enregistrées dans Studio ne sont pas visibles. Sans agent en ligne : agent_offline.",
		Annotations: ro,
	}, h.getProjectState)
	addTool(s, &mcp.Tool{
		Name: "open_in_studio", Title: "Ouvrir dans Bambu Studio",
		Description: "Transfère (par empreinte, vérifié) un fichier du service vers le poste Windows et l'ouvre dans Bambu Studio (copie dans un dossier de travail de l'agent ; l'original n'est jamais modifié). Ne démarre aucune impression.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &f},
	}, h.openInStudio)
	addTool(s, &mcp.Tool{
		Name: "modify_open_project", Title: "Modifier le projet ouvert (non pris en charge)",
		Description: "Renvoie toujours not_supported : modifier en direct les objets, supports ou coutures du projet ouvert exige une passerelle C++ dans Studio. Alternative : modifier une copie du projet côté service puis open_in_studio.",
		Annotations: ro,
	}, h.modifyOpenProject)
}

func (h *agentHandlers) run(ctx context.Context, kind string, params any, inputs []agentlink.Artifact, wait int) (*mcp.CallToolResult, AgentToolOutput, error) {
	raw, _ := json.Marshal(params)
	t, err := h.reg.CreateTask(ctx, agentlink.CreateTaskInput{Kind: kind, Params: raw, Inputs: inputs})
	if err != nil {
		var ae *agentlink.Error
		if errors.As(err, &ae) {
			st := "failed"
			switch ae.Code {
			case agentlink.CodeAgentOffline:
				st = "agent_offline"
			case agentlink.CodeNotSupported:
				st = "not_supported"
			}
			return textResult(ae.Message), AgentToolOutput{Status: st, Message: ae.Message, Executor: agentlink.ExecutorWindowsAgent}, nil
		}
		return nil, AgentToolOutput{}, err
	}
	if wait <= 0 {
		wait = 20
	}
	if wait > 60 {
		wait = 60
	}
	deadline := time.Now().Add(time.Duration(wait) * time.Second)
	for {
		cur, err := h.reg.GetTask(ctx, t.ID)
		if err != nil {
			return nil, AgentToolOutput{}, err
		}
		if agentlink.IsTerminal(cur.State) || cur.State == agentlink.StateUnknown || time.Now().After(deadline) {
			out := AgentToolOutput{Status: cur.State, TaskID: cur.ID, Result: cur.Result, Error: cur.Error, Message: cur.Note, Executor: cur.Executor}
			if !agentlink.IsTerminal(cur.State) && cur.State != agentlink.StateUnknown {
				out.Status, out.Message = "pending", "l'agent n'a pas encore répondu ; suivre le travail "+cur.ID+" (GET /api/agent/tasks/"+cur.ID+")"
			}
			return textResult(fmt.Sprintf("%s : %s", kind, out.Status)), out, nil
		}
		select {
		case <-ctx.Done():
			return nil, AgentToolOutput{}, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}

func (h *agentHandlers) getProjectState(ctx context.Context, _ *mcp.CallToolRequest, in ProjectStateInput) (*mcp.CallToolResult, AgentToolOutput, error) {
	return h.run(ctx, agentlink.KindGetProjectState, map[string]any{}, nil, in.WaitSecs)
}

func (h *agentHandlers) openInStudio(ctx context.Context, _ *mcp.CallToolRequest, in OpenInStudioInput) (*mcp.CallToolResult, AgentToolOutput, error) {
	sha, name := in.SHA256, in.FileName
	if sha == "" && in.ModelID != "" && h.c.Catalog != nil {
		d, err := h.c.Catalog.GetModelDetails(ctx, in.ModelID)
		if err != nil {
			return nil, AgentToolOutput{}, err
		}
		for _, f := range d.Files {
			if f.ID == d.CurrentFileID && f.Availability == "linux" {
				sha = f.SHA256
				if name == "" {
					name = f.Name
				}
			}
		}
		if sha == "" {
			msg := "le fichier courant du modèle n'est pas copié sur le service (windows_only) : le copier d'abord"
			return textResult(msg), AgentToolOutput{Status: "failed", Message: msg}, nil
		}
	}
	if !agentlink.ValidSHA(sha) {
		return nil, AgentToolOutput{}, errors.New("sha256 (64 caractères hexadécimaux minuscules) ou model_id requis")
	}
	if name == "" {
		name = sha[:12] + ".3mf"
	}
	return h.run(ctx, agentlink.KindOpenInStudio, map[string]any{"sha256": sha, "file_name": name},
		[]agentlink.Artifact{{SHA256: sha, Name: name}}, in.WaitSecs)
}

func (h *agentHandlers) modifyOpenProject(ctx context.Context, _ *mcp.CallToolRequest, in ModifyOpenProjectInput) (*mcp.CallToolResult, AgentToolOutput, error) {
	_, err := h.reg.CreateTask(ctx, agentlink.CreateTaskInput{Kind: agentlink.KindModifyOpenProject})
	msg := "non pris en charge"
	var ae *agentlink.Error
	if errors.As(err, &ae) {
		msg = ae.Message
	}
	return textResult(msg), AgentToolOutput{Status: "not_supported", Message: msg}, nil
}
