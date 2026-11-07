package mcpserver_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

func TestInsightToolsRegisteredAndWorking(t *testing.T) {
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]*mcp.Tool{}
	for _, tl := range tools.Tools {
		have[tl.Name] = tl
	}
	for _, n := range []string{"estimate_cost", "get_cost_settings", "set_cost_settings", "list_print_log", "get_print_log", "update_print_log",
		"suggest_print_adjustments", "get_usage_dashboard", "get_project_costs", "post_processing_advice"} {
		if have[n] == nil {
			t.Errorf("outil %s absent", n)
		}
	}
	// aucune de ces voies ne peut lancer ni arrêter une impression ni approuver quoi que ce soit
	for n, tl := range have {
		if !strings.Contains("estimate_cost get_cost_settings set_cost_settings list_print_log get_print_log update_print_log suggest_print_adjustments get_usage_dashboard get_project_costs post_processing_advice", n) {
			continue
		}
		raw, _ := tl.InputSchema.(map[string]any)
		if props, ok := raw["properties"].(map[string]any); ok {
			for k := range props {
				if strings.Contains(k, "approv") || strings.Contains(k, "confirm") || k == "start" || k == "token" {
					t.Errorf("%s : champ suspect %q", n, k)
				}
			}
		}
	}

	res := call(t, cs, "estimate_cost", map[string]any{"printer_model": "p1s", "duration_minutes": 120, "filaments": []any{map[string]any{"material": "PLA", "grams": 100}}})
	m := structured(t, res)
	est := m["estimate"].(map[string]any)
	if est["total"].(float64) <= 0 || !strings.HasPrefix(est["label"].(string), "ESTIMATION") {
		t.Errorf("estimation : %v", est)
	}
	if txt := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(txt, "ESTIMATION") || !strings.Contains(txt, "Hypothèses") {
		t.Errorf("texte : %s", txt)
	}

	st := structured(t, call(t, cs, "set_cost_settings", map[string]any{"electricity_per_kwh": 0.2, "filament_per_kg": map[string]any{"PLA": 19}}))
	if st["electricity_per_kwh"] != 0.2 || st["using_defaults"] != false {
		t.Errorf("réglages : %v", st)
	}
	if g := structured(t, call(t, cs, "get_cost_settings", map[string]any{})); g["electricity_per_kwh"] != 0.2 {
		t.Errorf("relecture : %v", g)
	}

	pp := structured(t, call(t, cs, "post_processing_advice", map[string]any{"material": "PETG", "finish": "transparente"}))
	if pp["family"] != "PETG" || len(pp["honesty"].([]any)) == 0 {
		t.Errorf("post-traitement : %v", pp)
	}
	bad, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "post_processing_advice", Arguments: map[string]any{"material": "bois"}})
	if err == nil && !bad.IsError {
		t.Error("matière inconnue : erreur attendue")
	}

	// journal sur une impression terminée + tableau de bord avec graphe PNG
	start := time.Now().UTC().Add(-3 * time.Hour)
	doc := map[string]any{"id": "jobM", "state": "COMPLETED", "printer_id": "pr1", "plate": 1,
		"summary": map[string]any{"plates": []map[string]any{{"index": 1, "predicted_seconds": 3600, "weight_grams": 30, "layers": 10,
			"filaments": []map[string]any{{"slot": 1, "type": "PETG", "used_g": 30}}}}, "printer_model": "Bambu Lab P1S"},
		"events":     []map[string]any{{"at": start, "event": "go", "from": "START_REQUESTED", "to": "PRINTING"}, {"at": start.Add(time.Hour), "event": "end", "from": "PRINTING", "to": "COMPLETED"}},
		"created_at": start, "updated_at": start.Add(time.Hour)}
	if err := c.Store.PutDoc(context.Background(), "test", "print_jobs", "jobM", doc); err != nil {
		t.Fatal(err)
	}
	l := structured(t, call(t, cs, "list_print_log", map[string]any{}))
	if l["total"] != float64(1) {
		t.Fatalf("journal : %v", l)
	}
	u := structured(t, call(t, cs, "update_print_log", map[string]any{"id": "jobM", "rating": 2, "defects": []any{"stringing"}, "comment": "fils partout"}))
	if u["rating"] != float64(2) || u["material_family"] != "PETG" {
		t.Errorf("mise à jour : %v", u)
	}
	adj := structured(t, call(t, cs, "suggest_print_adjustments", map[string]any{"log_id": "jobM"}))
	if adj["automatic"] != false || len(adj["proposals"].([]any)) != 1 {
		t.Errorf("ajustements : %v", adj)
	}
	dres := call(t, cs, "get_usage_dashboard", map[string]any{})
	if d := structured(t, dres); d["totals"].(map[string]any)["prints"] != float64(1) {
		t.Errorf("tableau de bord : %v", d)
	}
	gotImg := false
	for _, ct := range dres.Content {
		if ic, ok := ct.(*mcp.ImageContent); ok && ic.MIMEType == "image/png" && len(ic.Data) > 1000 {
			gotImg = true
		}
	}
	if !gotImg {
		t.Error("graphe PNG attendu")
	}
	nres := call(t, cs, "get_usage_dashboard", map[string]any{"no_chart": true})
	for _, ct := range nres.Content {
		if _, ok := ct.(*mcp.ImageContent); ok {
			t.Error("no_chart : pas d'image")
		}
	}
	pc := structured(t, call(t, cs, "get_project_costs", map[string]any{}))
	if len(pc["projects"].([]any)) != 1 {
		t.Errorf("projets : %v", pc)
	}
}
