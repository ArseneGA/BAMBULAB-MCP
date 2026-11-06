package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
)

func TestJobEndpointRedactsSecrets(t *testing.T) {
	ctx := context.Background()
	c := newCore(t)
	j, _, err := c.Queue.Enqueue(ctx, jobs.EnqueueParams{Kind: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Queue.Claim(ctx, "w"); err != nil {
		t.Fatal(err)
	}
	if err := c.Queue.Complete(ctx, j.ID, json.RawMessage(`{"ok":1,"password":"hunter2"}`)); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+j.ID, nil)
	req.Host = "127.0.0.1:7878"
	rec := httptest.NewRecorder()
	NewHandler(c).ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != 200 || strings.Contains(body, "hunter2") || strings.Contains(body, "password") || !strings.Contains(body, `"ok":1`) {
		t.Fatalf("%d %s", rec.Code, body)
	}
}
