package mcpserver_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

func TestSessionToolsWithoutAndWithAgent(t *testing.T) {
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))

	// Sans agent : réponse honnête, rien d'inventé.
	out := structured(t, call(t, cs, "get_project_state", map[string]any{}))
	if out["status"] != "agent_offline" {
		t.Fatalf("sans agent : %v", out)
	}
	sha, _, err := c.Blobs.Put(strings.NewReader("3mf-bidon"), "")
	if err != nil {
		t.Fatal(err)
	}
	if res, _ := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "open_in_studio", Arguments: map[string]any{"sha256": "00"}}); !res.IsError {
		t.Fatal("empreinte invalide acceptée")
	}
	out = structured(t, call(t, cs, "open_in_studio", map[string]any{"sha256": sha}))
	if out["status"] != "agent_offline" {
		t.Fatalf("open_in_studio sans agent : %v", out)
	}
	// modify_open_project : toujours non pris en charge, avec l'alternative.
	out = structured(t, call(t, cs, "modify_open_project", map[string]any{"request": "ajouter des supports"}))
	if out["status"] != "not_supported" || out["message"] == "" {
		t.Fatalf("modify_open_project : %v", out)
	}

	// get_capabilities annonce les agents.
	caps := structured(t, call(t, cs, "get_capabilities", map[string]any{}))
	if caps["mode"] != "linux_seul" {
		t.Fatalf("mode : %v", caps["mode"])
	}

	// Avec un agent simulé qui répond.
	reg, err := agentlink.For(c)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(reg.Handler())
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tok, _, _ := reg.NewPairing(ctx)
	pr, err := agentlink.NewClient(srv.URL, "").Pair(ctx, agentlink.PairRequest{Token: tok, Name: "PC"})
	if err != nil {
		t.Fatal(err)
	}
	cl := agentlink.NewClient(srv.URL, pr.Secret)
	if _, err := cl.Hello(ctx, agentlink.HelloRequest{Capabilities: agentlink.AgentCaps{Studio: agentlink.StudioCaps{Installed: true, Version: "02.08.02.61"},
		Tasks: []string{agentlink.KindGetProjectState}}}); err != nil {
		t.Fatal(err)
	}
	go func() {
		for ctx.Err() == nil {
			tk, err := cl.NextTask(ctx, time.Second)
			if err != nil || tk == nil {
				continue
			}
			_ = cl.StartTask(ctx, tk.ID)
			_ = cl.Result(ctx, tk.ID, agentlink.TaskResult{State: agentlink.StateSucceeded, Result: json.RawMessage(`{"studio":{"running":true},"project":{"objects":[{"name":"cube"}]}}`)})
		}
	}()
	out = structured(t, call(t, cs, "get_project_state", map[string]any{"wait_seconds": 10}))
	if out["status"] != "succeeded" || out["executor"] != "windows-agent" || out["task_id"] == "" || out["result"] == nil {
		t.Fatalf("avec agent : %v", out)
	}
	caps = structured(t, call(t, cs, "get_capabilities", map[string]any{}))
	agents, _ := caps["agents"].([]any)
	if caps["mode"] != "linux_renfort_windows" || len(agents) != 1 || agents[0].(map[string]any)["online"] != true {
		t.Fatalf("capacités avec agent : %v", caps)
	}
	// open_in_studio sur un agent qui ne l'annonce pas : agent_offline (capacité absente), pas de travail créé.
	out = structured(t, call(t, cs, "open_in_studio", map[string]any{"sha256": sha}))
	if out["status"] != "agent_offline" {
		t.Fatalf("open_in_studio : %v", out)
	}
}
