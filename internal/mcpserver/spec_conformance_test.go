package mcpserver_test

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

var toolNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// schemaType relit le "type" d'un schéma tel qu'il passe sur le fil (JSON), pour
// détecter les tableaux (["object","null"]) ou l'absence que Claude Desktop refuse.
func schemaType(t *testing.T, schema any) (any, bool) {
	t.Helper()
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("schéma non objet : %s", raw)
	}
	v, ok := m["type"]
	return v, ok
}

// TestToolsListConformsToMCPSpec : la spec MCP exige inputSchema.type == "object" et,
// si outputSchema est présent, outputSchema.type == "object" (chaîne exacte). Le SDK
// TypeScript (Claude Desktop) valide strictement et rejette tout tools/list sinon.
func TestToolsListConformsToMCPSpec(t *testing.T) {
	ctx := context.Background()
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))

	lt, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(lt.Tools) == 0 {
		t.Fatal("aucun outil")
	}
	withOut := map[string]bool{}
	for _, tl := range lt.Tools {
		if !toolNameRe.MatchString(tl.Name) {
			t.Errorf("%s : nom invalide (64 car. max, [a-zA-Z0-9_-])", tl.Name)
		}
		if tl.Description == "" {
			t.Errorf("%s : description vide", tl.Name)
		}
		if ty, ok := schemaType(t, tl.InputSchema); !ok || ty != "object" {
			t.Errorf("%s : inputSchema.type = %v, attendu \"object\"", tl.Name, ty)
		}
		if tl.OutputSchema != nil {
			withOut[tl.Name] = true
			if ty, ok := schemaType(t, tl.OutputSchema); !ok || ty != "object" {
				t.Errorf("%s : outputSchema.type = %v, attendu \"object\"", tl.Name, ty)
			}
		}
	}

	// structuredContent doit être un objet quand l'outil a un outputSchema.
	checked := 0
	for _, name := range []string{"get_capabilities", "inspect_environment", "list_printers", "list_filaments", "list_spools", "list_print_queue", "list_printer_models", "list_goal_criteria", "get_cost_settings", "list_projects", "get_pending_verdicts"} {
		if !withOut[name] {
			continue
		}
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
		if err != nil || res.IsError {
			continue
		}
		raw, _ := json.Marshal(res.StructuredContent)
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			t.Errorf("%s : structuredContent n'est pas un objet : %s", name, raw)
		}
		checked++
	}
	if checked < 3 {
		t.Errorf("trop peu d'appels vérifiés : %d", checked)
	}
}
