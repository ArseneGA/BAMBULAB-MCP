package mcpserver_test

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

type syncBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// startCoreLogged : comme startCore, avec un journal capturé (niveau INFO, format texte comme en production).
func startCoreLogged(t *testing.T, buf *syncBuf) *core.Core {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c, err := core.Open(ctx, core.Config{
		DataDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(buf, nil)),
		PollInterval: 20 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done; c.Close() })
	return c
}

// sessionMCP : un outil lié à une vue, la lecture de la vue, les listes ; un argument « secret » factice.
func sessionMCP(t *testing.T, buf *syncBuf) {
	t.Helper()
	ctx := context.Background()
	c := startCoreLogged(t, buf)
	cs := connectClient(t, mcpserver.New(c))
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "search_models", Arguments: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: mcpserver.SearchViewURI}); err != nil {
		t.Fatal(err)
	}
	// Erreur d'outil (travail inconnu) avec une valeur secrète en argument : elle ne doit apparaître nulle part.
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_job_status", Arguments: map[string]any{"job_id": "SECRET-ARG-VALUE-123"}}); err != nil {
		t.Logf("get_job_status : %v", err)
	}
	if _, err := cs.ListResources(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ListResourceTemplates(ctx, nil); err != nil {
		t.Fatal(err)
	}
}

func TestMCPRequestLogging(t *testing.T) {
	t.Setenv("BAMBU_LOG_MCP", "")
	buf := &syncBuf{}
	sessionMCP(t, buf)
	out := buf.String()

	var call, read, list, tmpl, failed string
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(l, "MCP tools/call") && strings.Contains(l, "tool=search_models"):
			call = l
		case strings.Contains(l, "MCP tools/call") && strings.Contains(l, "tool=get_job_status"):
			failed = l
		case strings.Contains(l, "MCP resources/read"):
			read = l
		case strings.Contains(l, "MCP resources/list"):
			list = l
		case strings.Contains(l, "MCP resources/templates/list"):
			tmpl = l
		}
	}
	for name, want := range map[string][]string{
		call:   {"tool=search_models", "session=", "duration_ms=", "ok=true", "meta_ui=true", "bytes=", "blocks=", "block_types="},
		read:   {"uri=" + mcpserver.SearchViewURI, `mime="text/html;profile=mcp-app"`, "bytes=", "ok=true"},
		list:   {"count=", "ok=true"},
		tmpl:   {"count=", "ok=true"},
		failed: {"ok=false", "meta_ui=false"},
	} {
		if name == "" {
			t.Fatalf("ligne attendue absente du journal :\n%s", out)
		}
		for _, w := range want {
			if !strings.Contains(name, w) {
				t.Errorf("ligne %q : champ %q absent", name, w)
			}
		}
	}
	if strings.Contains(out, "SECRET-ARG-VALUE-123") {
		t.Fatalf("la valeur d'argument apparaît dans le journal :\n%s", out)
	}
	t.Logf("exemples :\n%s\n%s\n%s", call, read, list)
}

func TestMCPRequestLoggingOff(t *testing.T) {
	for _, v := range []string{"off", "OFF", "0", "false"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("BAMBU_LOG_MCP", v)
			buf := &syncBuf{}
			sessionMCP(t, buf)
			if strings.Contains(buf.String(), "MCP tools/call") || strings.Contains(buf.String(), "MCP resources/") {
				t.Fatalf("lignes MCP présentes malgré BAMBU_LOG_MCP=%s :\n%s", v, buf.String())
			}
		})
	}
}
