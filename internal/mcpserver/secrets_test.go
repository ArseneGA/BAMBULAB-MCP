package mcpserver_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

func TestGetJobStatusRedactsSecrets(t *testing.T) {
	ctx := context.Background()
	c := startCore(t)
	c.Registry.Register("test.leaky", true, func(ctx context.Context, jc *jobs.JobCtx) (json.RawMessage, error) {
		return json.RawMessage(`{"ok":true,"access_code":"12345678","nested":{"secret_token":"tok"}}`), nil
	})
	cs := connectClient(t, mcpserver.New(c))
	j, _, err := c.Queue.Enqueue(ctx, jobs.EnqueueParams{Kind: "test.leaky"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 400; i++ {
		if got, _ := c.Queue.Get(ctx, j.ID); got.State == contracts.JobSucceeded {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	res := call(t, cs, "get_job_status", map[string]any{"job_id": j.ID})
	raw, _ := json.Marshal(res.StructuredContent)
	s := string(raw)
	if strings.Contains(s, "12345678") || strings.Contains(s, "secret_token") || strings.Contains(s, "access_code") {
		t.Fatalf("secret exposé par get_job_status : %s", s)
	}
	if !strings.Contains(s, `"ok":true`) {
		t.Fatalf("résultat légitime perdu : %s", s)
	}
}

// Les outils G9 (suivi de projet, verdict) ne renvoient ni code d'accès, ni jeton, ni chemin du serveur (§ 7 de la spec G9).
func TestG9ToolsLeakNoSecrets(t *testing.T) {
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))
	seedModel(t, c, "mS", "Pièce secrète")
	seedJob(t, c, "jS1", "mS", "COMPLETED", 100, 1)
	p := structured(t, call(t, cs, "create_project", map[string]any{"name": "Projet", "description": "note"}))
	pid, _ := p["id"].(string)
	call(t, cs, "add_project_part", map[string]any{"project_id": pid, "model_id": "mS"})
	call(t, cs, "set_print_verdict", map[string]any{"print_job_id": "jS1", "verdict": "failed", "reason": "stringing", "note": "fils"})
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"get_project", map[string]any{"id": pid}}, {"list_projects", map[string]any{}}, {"get_pending_verdicts", map[string]any{}},
		{"update_project", map[string]any{"id": pid, "status": "printing"}}, {"submit_project_status", map[string]any{"project_id": pid, "status": "design"}},
		{"set_print_verdict", map[string]any{"print_job_id": "jS1", "verdict": "redo"}},
	} {
		raw := resultAll(call(t, cs, tc.name, tc.args))
		if m := secretPatterns.FindString(raw); m != "" {
			t.Errorf("%s : motif sensible %q dans la sortie : %s", tc.name, m, raw)
		}
	}
}
