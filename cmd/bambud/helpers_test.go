package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
)

func jobsEnqueue(params string) jobs.EnqueueParams {
	return jobs.EnqueueParams{Kind: "system.selftest", Params: json.RawMessage(params)}
}

func newTestServer(t *testing.T, c *core.Core) *httptest.Server {
	t.Helper()
	return httptest.NewServer(httpapi.NewHandler(c))
}
