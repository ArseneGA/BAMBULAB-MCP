package mcpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// startCore ouvre un cœur dans un dossier temporaire et lance ses workers.
func startCore(t *testing.T) *core.Core {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c, err := core.Open(ctx, core.Config{
		DataDir: t.TempDir(), Logger: quietLog(),
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

func connectClient(t *testing.T, srv *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func structured(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	if res.IsError {
		t.Fatalf("outil en erreur : %+v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s : %v", name, err)
	}
	return res
}

func TestIntegrationToolsAndResource(t *testing.T) {
	ctx := context.Background()
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))

	lt, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	byName := map[string]*mcp.Tool{}
	for _, tl := range lt.Tools {
		names = append(names, tl.Name)
		byName[tl.Name] = tl
	}
	sort.Strings(names)
	want := []string{"cancel_job", "get_capabilities", "get_job_status", "inspect_environment", "show_atelier"}
	for _, w := range want {
		if !slices.Contains(names, w) {
			t.Fatalf("outil manquant %s : %v", w, names)
		}
	}
	if len(names) < 46 { // socle 5 + catalogue 14 + objectifs 8 + session Windows 3 + impression 16 (minimum ; confirm_print_from_app supprimé)
		t.Fatalf("outils : %v", names)
	}
	// Schéma d'entrée issu de contracts : job_id requis.
	raw, _ := json.Marshal(byName["get_job_status"].InputSchema)
	if !strings.Contains(string(raw), `"required":["job_id"]`) {
		t.Fatalf("schéma get_job_status : %s", raw)
	}
	// Lien outil -> vue.
	meta := byName["show_atelier"].Meta
	ui, _ := meta["ui"].(map[string]any)
	if ui["resourceUri"] != mcpserver.AtelierURI || meta["ui/resourceUri"] != mcpserver.AtelierURI {
		t.Fatalf("_meta de show_atelier : %v", meta)
	}

	// get_capabilities
	caps := structured(t, call(t, cs, "get_capabilities", map[string]any{}))
	if caps["service"] != "bambud" || caps["mode"] != "linux_seul" {
		t.Fatalf("capacités : %v", caps)
	}
	feats, _ := caps["features"].([]any)
	foundSlicingOff := false
	for _, f := range feats {
		m := f.(map[string]any)
		if m["id"] == "slicing" && m["note"] != "" {
			foundSlicingOff = true
		}
	}
	if !foundSlicingOff {
		t.Fatal("la capacité slicing doit être annoncée avec sa note (disponible ou slicer_missing)")
	}

	// inspect_environment
	env := structured(t, call(t, cs, "inspect_environment", map[string]any{}))
	if env["os"] == "" || env["data_dir_writable"] != true {
		t.Fatalf("environnement : %v", env)
	}

	// Ressource ui://
	rr, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: mcpserver.AtelierURI})
	if err != nil {
		t.Fatal(err)
	}
	if len(rr.Contents) != 1 || rr.Contents[0].MIMEType != "text/html;profile=mcp-app" ||
		!strings.Contains(rr.Contents[0].Text, "<html") {
		t.Fatalf("ressource : %+v", rr.Contents)
	}
	if _, ok := rr.Contents[0].Meta["ui"].(map[string]any)["csp"]; !ok {
		t.Fatalf("CSP absente du _meta : %v", rr.Contents[0].Meta)
	}
	lr, _ := cs.ListResources(ctx, nil)
	if len(lr.Resources) != 5 || lr.Resources[0].URI != mcpserver.AtelierURI || lr.Resources[0].MIMEType != mcpserver.AppMIMEType { // atelier + search + model + print-confirm + print-stop
		t.Fatalf("liste des ressources : %+v", lr.Resources)
	}
	if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "ui://inconnu"}); err == nil {
		t.Fatal("ressource inconnue acceptée")
	}

	// show_atelier
	sa := structured(t, call(t, cs, "show_atelier", map[string]any{}))
	if sa["demo"] != true {
		t.Fatalf("show_atelier : %v", sa)
	}

	// Validation des entrées par schéma : job_id manquant.
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_job_status", Arguments: map[string]any{}})
	if err == nil && !res.IsError {
		t.Fatal("entrée invalide acceptée")
	}
	// Travail inconnu : erreur d'outil.
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_job_status", Arguments: map[string]any{"job_id": "job_nope"}})
	if err == nil && !res.IsError {
		t.Fatal("travail inconnu accepté")
	}
}

func TestIntegrationJobLifecycleThroughMCP(t *testing.T) {
	ctx := context.Background()
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))

	// Travail court : suivi jusqu'au succès.
	j, _, err := c.Queue.Enqueue(ctx, jobsParams("system.selftest", `{"steps":3,"step_ms":5}`))
	if err != nil {
		t.Fatal(err)
	}
	var st map[string]any
	for i := 0; i < 400; i++ {
		st = structured(t, call(t, cs, "get_job_status", map[string]any{"job_id": j.ID, "log_tail": 2}))
		if st["state"] == string(contracts.JobSucceeded) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st["state"] != "succeeded" || st["progress"].(float64) != 1 {
		t.Fatalf("travail : %v", st)
	}
	if logs, _ := st["logs"].([]any); len(logs) != 2 {
		t.Fatalf("log_tail non respecté : %v", st["logs"])
	}

	// Travail long : annulation via l'outil.
	long, _, _ := c.Queue.Enqueue(ctx, jobsParams("system.selftest", `{"steps":1000,"step_ms":50}`))
	for i := 0; i < 400; i++ {
		got, _ := c.Queue.Get(ctx, long.ID)
		if got.State == contracts.JobRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	out := structured(t, call(t, cs, "cancel_job", map[string]any{"job_id": long.ID}))
	if out["outcome"] != "cancel_requested" {
		t.Fatalf("annulation : %v", out)
	}
	for i := 0; i < 400; i++ {
		st = structured(t, call(t, cs, "get_job_status", map[string]any{"job_id": long.ID}))
		if st["state"] == "cancelled" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st["state"] != "cancelled" {
		t.Fatalf("état final : %v", st["state"])
	}
	again := structured(t, call(t, cs, "cancel_job", map[string]any{"job_id": long.ID}))
	if again["outcome"] != "already_finished" {
		t.Fatalf("second cancel : %v", again)
	}
}

// TestProxyBridge : le pont relaie outils, ressources et _meta d'un vrai bambud (HTTP).
func TestProxyBridge(t *testing.T) {
	ctx := context.Background()
	c := startCore(t)
	ts := httptest.NewServer(httpapi.NewHandler(c))
	defer ts.Close()

	p, err := mcpserver.NewProxy(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp"}, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	cs := connectClient(t, p.Server)

	lt, err := cs.ListTools(ctx, nil)
	if err != nil || len(lt.Tools) < 46 {
		t.Fatalf("outils relayés : %v %v", lt, err)
	}
	for _, tl := range lt.Tools {
		if tl.Name == "show_atelier" && tl.Meta["ui/resourceUri"] != mcpserver.AtelierURI {
			t.Fatalf("_meta perdu par le pont : %v", tl.Meta)
		}
	}
	caps := structured(t, call(t, cs, "get_capabilities", map[string]any{}))
	if caps["service"] != "bambud" {
		t.Fatalf("%v", caps)
	}
	rr, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: mcpserver.AtelierURI})
	if err != nil || len(rr.Contents) != 1 || rr.Contents[0].MIMEType != mcpserver.AppMIMEType {
		t.Fatalf("ressource relayée : %+v %v", rr, err)
	}
}
