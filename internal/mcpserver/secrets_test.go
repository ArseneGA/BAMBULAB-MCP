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
