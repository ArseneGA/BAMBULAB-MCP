package mcpserver_test

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/goals"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

func TestGoalToolsRegisteredAndWorking(t *testing.T) {
	c := startCore(t)
	svc, err := goals.New(goals.Config{Store: c.Store, Blobs: c.Blobs})
	if err != nil {
		t.Fatal(err)
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	mcpserver.AddGoalTools(srv, svc) // tous les schémas d'entrée/sortie doivent se générer
	cs := connectClient(t, srv)

	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, tl := range tools.Tools {
		have[tl.Name] = true
	}
	for _, n := range []string{"list_goal_criteria", "set_manufacturing_goals", "modify_slicing_profile", "create_experiment",
		"compare_variants", "apply_variant", "record_print_result", "get_print_memory"} {
		if !have[n] {
			t.Errorf("outil %s absent", n)
		}
	}

	cat := structured(t, call(t, cs, "list_goal_criteria", map[string]any{}))
	if list, _ := cat["criteria"].([]any); len(list) < 10 {
		t.Fatalf("catalogue : %v", cat)
	}

	res := call(t, cs, "set_manufacturing_goals", map[string]any{
		"original_text": "solide mais rapide, et ça doit sentir la lavande",
		"material":      "PLA",
		"criteria": []any{
			map[string]any{"type": "strength", "priority": 2},
			map[string]any{"type": "speed", "priority": 2, "threshold": map[string]any{"op": "<=", "value": 30, "unit": "min"}},
			map[string]any{"type": "odeur_lavande", "text": "doit sentir la lavande"},
		},
	})
	g := structured(t, res)
	amb, _ := g["ambiguities"].([]any)
	ne, _ := g["non_evaluable"].([]any)
	conf, _ := g["conflicts"].([]any)
	if len(amb) < 2 || len(ne) < 2 || len(conf) != 1 {
		t.Fatalf("ambiguïtés (direction d'effort, priorités), non évaluables (résistance, custom) et conflit attendus : %v", g)
	}
	if g["original_text"] != "solide mais rapide, et ça doit sentir la lavande" {
		t.Fatal("texte original non conservé")
	}

	// erreurs de validation renvoyées comme erreurs d'outil
	bad, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "set_manufacturing_goals", Arguments: map[string]any{
		"original_text": "x", "criteria": []any{map[string]any{"type": "speed", "priority": 9}}}})
	if err == nil && !bad.IsError {
		t.Fatal("priorité hors bornes : erreur attendue")
	}
	// apply_variant sur une expérience inconnue
	r2, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "apply_variant", Arguments: map[string]any{
		"experiment_id": "exp_x", "candidate_id": "c", "expected_revision": "ver_x"}})
	if err == nil && (!r2.IsError || !strings.Contains(goalToolText(r2), "introuvable")) {
		t.Fatalf("expérience inconnue : %+v", r2)
	}
	// mémoire : conditions obligatoires
	r3, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_print_memory", Arguments: map[string]any{}})
	if err == nil && !r3.IsError {
		t.Fatal("get_print_memory sans conditions : erreur attendue")
	}
	m := structured(t, call(t, cs, "get_print_memory", map[string]any{"material_family": "PLA", "machine": "Bambu Lab P1S 0.4 nozzle", "size_mm": []any{10, 20, 30}}))
	if hits, _ := m["hits"].([]any); len(hits) != 0 {
		t.Fatalf("aucun essai enregistré : %v", m)
	}
}

func goalToolText(r *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
