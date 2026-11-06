package mcpserver_test

import (
	"encoding/json"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
)

func jobsParams(kind, params string) jobs.EnqueueParams {
	return jobs.EnqueueParams{Kind: kind, Params: json.RawMessage(params)}
}
