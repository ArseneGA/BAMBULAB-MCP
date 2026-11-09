package mcpserver_test

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

// La carte de suivi est liée à get_print_status ET à wait_for_print_change (définition, clé plate comprise) ; ses deux outils
// d'appui sont réservés à l'application ; la ressource est servie avec le MIME MCP Apps et une CSP vide.
func TestLiveViewLinks(t *testing.T) {
	ctx := context.Background()
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))
	tl, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]*mcp.Tool{}
	for _, tt := range tl.Tools {
		tools[tt.Name] = tt
	}
	for _, name := range []string{"get_print_status", "wait_for_print_change", "get_printer_status"} {
		tt := tools[name]
		if tt == nil {
			t.Fatalf("outil %s absent", name)
		}
		if ui := metaUI(t, tt.Meta); ui["resourceUri"] != mcpserver.LiveViewURI || tt.Meta["ui/resourceUri"] != mcpserver.LiveViewURI {
			t.Fatalf("%s : lien de vue incorrect : %v", name, tt.Meta)
		}
		if vis, _ := metaUI(t, tt.Meta)["visibility"].([]any); len(vis) != 0 {
			t.Fatalf("%s doit rester visible du modèle : %v", name, tt.Meta)
		}
	}
	for _, name := range []string{"get_live_frame", "get_live_state"} {
		tt := tools[name]
		if tt == nil {
			t.Fatalf("outil %s absent", name)
		}
		if vis, _ := metaUI(t, tt.Meta)["visibility"].([]any); len(vis) != 1 || vis[0] != "app" {
			t.Fatalf("%s : visibility attendue [app] : %v", name, tt.Meta)
		}
		if a := tt.Annotations; a == nil || (!a.ReadOnlyHint && (a.DestructiveHint == nil || *a.DestructiveHint)) {
			t.Fatalf("%s ne doit pas être annoncé destructif : %+v", name, tt.Annotations)
		}
	}
	// Aucun outil renommé : les noms historiques sont toujours là, avec leur titre.
	for _, name := range []string{"cancel_print", "resume_print", "get_camera_snapshot", "analyze_print_frame"} {
		if tools[name] == nil {
			t.Fatalf("outil %s disparu", name)
		}
	}

	rr, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: mcpserver.LiveViewURI})
	if err != nil || len(rr.Contents) != 1 {
		t.Fatalf("ressource : %v", err)
	}
	ct := rr.Contents[0]
	if ct.MIMEType != "text/html;profile=mcp-app" {
		t.Fatalf("MIME : %q", ct.MIMEType)
	}
	if csp, _ := metaUI(t, ct.Meta)["csp"].(map[string]any); len(csp) != 0 {
		t.Fatalf("la CSP de la vue de suivi doit rester vide : %v", csp)
	}
}

// Aucune imprimante : l'outil d'image est refusé avec une consigne, jamais une image vide.
func TestGetLiveFrameRefusesWithoutPrinter(t *testing.T) {
	ctx := context.Background()
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_live_frame", Arguments: map[string]any{}})
	if err == nil && !res.IsError {
		t.Fatal("get_live_frame sans imprimante doit être refusé")
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	} else {
		for _, ct := range res.Content {
			if tc, ok := ct.(*mcp.TextContent); ok {
				msg += tc.Text
			}
		}
	}
	if !strings.Contains(msg, "printer_id requis") {
		t.Fatalf("message : %q", msg)
	}
	res2, err2 := cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_live_frame", Arguments: map[string]any{"printer_id": "pr_inconnue"}})
	if err2 == nil && !res2.IsError {
		t.Fatal("imprimante inconnue : refus attendu")
	}
}
