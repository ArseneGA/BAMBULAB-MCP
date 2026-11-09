package mcpserver

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type lockedBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *lockedBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *lockedBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// Les outils de vue appelés en boucle : rien au niveau INFO à chaque appel, une ligne agrégée par minute et par outil.
func TestMCPLogPolledAppToolsAreThrottled(t *testing.T) {
	buf := &lockedBuf{}
	l := slog.New(slog.NewTextHandler(buf, nil)) // niveau INFO
	m := &mcpLogger{log: l, agg: map[string]*polledAgg{}}
	clock := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return clock }

	srv := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	srv.AddReceivingMiddleware(m.middleware)
	type in struct {
		Secret string `json:"secret,omitempty"`
	}
	for _, name := range []string{"get_live_frame", "show_other"} {
		mcp.AddTool(srv, &mcp.Tool{Name: name}, func(_ context.Context, _ *mcp.CallToolRequest, _ in) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "PAYLOAD-SECRET"}}}, nil, nil
		})
	}
	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	call := func(name string) {
		t.Helper()
		if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{"secret": "ARG-SECRET"}}); err != nil {
			t.Fatal(err)
		}
	}
	count := func(name string) int { return strings.Count(buf.String(), "tool="+name) }

	for i := 0; i < 40; i++ { // 40 appels dans la même minute
		call("get_live_frame")
		clock = clock.Add(500 * time.Millisecond)
	}
	if n := count("get_live_frame"); n != 1 {
		t.Fatalf("attendu 1 ligne INFO agrégée (première occurrence), obtenu %d :\n%s", n, buf.String())
	}
	clock = clock.Add(2 * time.Minute)
	call("get_live_frame")
	out := buf.String()
	if n := count("get_live_frame"); n != 2 {
		t.Fatalf("attendu 2 lignes après la minute écoulée, obtenu %d :\n%s", n, out)
	}
	if !strings.Contains(out, "calls=40") {
		t.Errorf("le compteur agrégé devrait valoir 40 :\n%s", out)
	}
	// Un outil ordinaire reste journalisé à chaque appel.
	call("show_other")
	call("show_other")
	if n := count("show_other"); n != 2 {
		t.Errorf("show_other : %d lignes, attendu 2", n)
	}
	if strings.Contains(out, "SECRET") || strings.Contains(buf.String(), "SECRET") {
		t.Fatalf("secret dans le journal :\n%s", buf.String())
	}
}
