package mcpserver_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Outils G9 de cette vague (suivi de projet PJ2, verdict PJ11).
var g9Tools = []string{"create_project", "update_project", "add_project_part", "remove_project_part", "get_project", "list_projects",
	"set_print_verdict", "submit_print_verdict", "get_pending_verdicts", "submit_project_status", "submit_project_part"}

func seedModel(t *testing.T, c *core.Core, id, name string) {
	t.Helper()
	ts := store.FormatTime(time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC))
	if _, err := c.Store.DB().Exec(`INSERT INTO models(id,name,created_at,updated_at) VALUES(?,?,?,?)`, id, name, ts, ts); err != nil {
		t.Fatal(err)
	}
}

// seedJob enregistre une impression de 2 h pour 100 g de PLA.
func seedJob(t *testing.T, c *core.Core, id, model, state string, percent, day int) {
	t.Helper()
	start := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC).AddDate(0, 0, day)
	end := start.Add(2 * time.Hour)
	events := []map[string]any{{"at": start, "event": "go", "from": "START_REQUESTED", "to": "PRINTING"}}
	if state != "PRINTING" {
		events = append(events, map[string]any{"at": end, "event": "fin", "from": "PRINTING", "to": state})
	}
	doc := map[string]any{
		"id": id, "state": state, "printer_id": "pr1", "model_id": model, "name": id + ".gcode.3mf", "plate": 1,
		"assignments": []map[string]any{{"filament_slot": 1, "tray": "ams1:1", "material_id": "Bambu PLA Basic noir"}},
		"summary": map[string]any{
			"plates":        []slicer.PlateResult{{Index: 1, PredictedSeconds: 7200, WeightGrams: 100, Layers: 100, Filaments: []slicer.FilamentUse{{Slot: 1, Type: "PLA", Grams: 100}}}},
			"printer_model": "Bambu Lab P1S", "machine_preset": "Bambu Lab P1S 0.4 nozzle", "process": "0.20mm Standard @BBL X1C",
			"filament_presets": []string{"Bambu PLA Basic @BBL P1S 0.4 nozzle"},
		},
		"print": map[string]any{"percent": percent}, "events": events,
		"slice":      map[string]any{"machine": "Bambu Lab P1S 0.4 nozzle", "process": "0.20mm Standard @BBL X1C", "filaments": []string{"Bambu PLA Basic @BBL P1S 0.4 nozzle"}, "nozzle_mm": 0.4},
		"created_at": start, "updated_at": end,
	}
	if err := c.Store.PutDoc(context.Background(), "test", "print_jobs", id, doc); err != nil {
		t.Fatal(err)
	}
}

func listToolsByName(t *testing.T, cs *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	lt, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]*mcp.Tool{}
	for _, tl := range lt.Tools {
		m[tl.Name] = tl
	}
	return m
}

// Conformité de tools/list pour les outils G9 : Title, annotations, outputSchema d'objet, cartes, visibilité app, noms sûrs.
func TestG9ToolsConformAndSafe(t *testing.T) {
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))
	by := listToolsByName(t, cs)
	forbiddenName := regexp.MustCompile(`approve|start|launch|stop|resume|cancel`)
	for _, n := range g9Tools {
		tl := by[n]
		if tl == nil {
			t.Fatalf("outil %s absent", n)
		}
		if forbiddenName.MatchString(strings.ToLower(n)) {
			t.Errorf("%s : nom interdit", n)
		}
		if tl.Title == "" || tl.Description == "" || tl.Annotations == nil || tl.OutputSchema == nil {
			t.Errorf("%s : Title, description, annotations et outputSchema requis", n)
		}
		if ty, ok := schemaTypeOf(tl.OutputSchema); !ok || ty != "object" {
			t.Errorf("%s : outputSchema.type = %v", n, ty)
		}
		ro := n == "get_project" || n == "list_projects" || n == "get_pending_verdicts"
		if tl.Annotations != nil && tl.Annotations.ReadOnlyHint != ro {
			t.Errorf("%s : ReadOnlyHint = %v, attendu %v", n, tl.Annotations.ReadOnlyHint, ro)
		}
		meta, _ := json.Marshal(tl.Meta)
		if strings.HasPrefix(n, "submit_") != strings.Contains(string(meta), `"visibility":["app"]`) {
			t.Errorf("%s : visibilité app seulement pour les submit_* : %s", n, meta)
		}
		if raw, _ := json.Marshal(tl.InputSchema); regexp.MustCompile(`"(approved|approve|nonce|token|confirm_token|human|plate_clear|confirmation_id)"`).Match(raw) {
			t.Errorf("%s : champ d'approbation dans le schéma : %s", n, raw)
		}
	}
	for n, uri := range map[string]string{"get_project": mcpserver.ProjectViewURI, "list_projects": mcpserver.ProjectViewURI, "get_pending_verdicts": mcpserver.VerdictViewURI, "set_print_verdict": mcpserver.VerdictViewURI} {
		if ui := metaUI(t, by[n].Meta); ui["resourceUri"] != uri {
			t.Errorf("%s : carte %v, attendu %s", n, ui["resourceUri"], uri)
		}
	}
	rl, err := cs.ListResources(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range rl.Resources {
		got[r.URI] = true
	}
	if !got[mcpserver.ProjectViewURI] || !got[mcpserver.VerdictViewURI] {
		t.Errorf("ressources ui:// manquantes : %v", got)
	}
}

func schemaTypeOf(schema any) (any, bool) {
	raw, _ := json.Marshal(schema)
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil, false
	}
	v, ok := m["type"]
	return v, ok
}

var secretPatterns = regexp.MustCompile(`bblp|C:\\|/home/|/opt/|Bearer|access_code`)

func resultAll(res *mcp.CallToolResult) string {
	raw, _ := json.Marshal(res.StructuredContent)
	return string(raw) + " " + resultText(res)
}

// De bout en bout : projet, pièces, impressions, verdict ; CA2.1, CA2.2, CA2.3, CA2.4, CA11.1 à CA11.4 ; aucun secret.
func TestProjectAndVerdictFlow(t *testing.T) {
	ctx := context.Background()
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))
	var outputs []string
	keep := func(res *mcp.CallToolResult) *mcp.CallToolResult {
		outputs = append(outputs, resultAll(res))
		return res
	}

	// base sans projet : liste vide explicite, outils existants inchangés
	empty := structured(t, keep(call(t, cs, "list_projects", map[string]any{})))
	if empty["total"] != float64(0) || !strings.Contains(empty["note"].(string), "create_project") {
		t.Fatalf("liste vide : %v", empty)
	}
	seedModel(t, c, "mA", "Équerre")
	seedModel(t, c, "mB", "Support")
	seedModel(t, c, "mC", "Capot")
	for i, j := range []struct{ id, model string }{{"jA1", "mA"}, {"jA2", "mA"}, {"jB1", "mB"}, {"jC1", "mC"}} {
		seedJob(t, c, j.id, j.model, "COMPLETED", 100, i+1)
	}
	seedJob(t, c, "jC2", "mC", "CANCELLED", 60, 6)
	seedJob(t, c, "jRun", "mA", "PRINTING", 40, 9)
	costsBefore, _ := json.Marshal(structured(t, call(t, cs, "get_project_costs", map[string]any{})))

	p := structured(t, keep(call(t, cs, "create_project", map[string]any{"name": "Étagère", "description": "salon"})))
	pid, _ := p["id"].(string)
	if !strings.HasPrefix(pid, "prj_") || p["status"] != "idea" {
		t.Fatalf("create_project : %v", p)
	}
	sub := structured(t, keep(call(t, cs, "create_project", map[string]any{"name": "Pieds", "parent_id": pid})))
	subID, _ := sub["id"].(string)
	call(t, cs, "add_project_part", map[string]any{"project_id": pid, "model_id": "mA", "quantity": 2})
	call(t, cs, "add_project_part", map[string]any{"project_id": pid, "model_id": "mB", "quantity": 1})
	call(t, cs, "add_project_part", map[string]any{"project_id": subID, "model_id": "mC", "quantity": 3, "role": "pied"})
	// erreurs claires
	if r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "update_project", Arguments: map[string]any{"id": pid, "parent_id": subID}}); err != nil || !r.IsError || !strings.Contains(resultText(r), "cycle") {
		t.Errorf("cycle : %v %v", err, r)
	}
	if r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "add_project_part", Arguments: map[string]any{"project_id": pid, "model_id": "m_inconnu"}}); err != nil || !r.IsError {
		t.Errorf("pièce inconnue : %v", r)
	}

	g := keep(call(t, cs, "get_project", map[string]any{"id": pid}))
	d := structured(t, g)
	prog := d["progress"].(map[string]any)
	// A : mA x2 (2 imprimées), mB x1 (1), sous-projet : mC x3 (1 imprimée)
	if prog["parts_done"] != float64(2) || prog["parts_total"] != float64(3) || prog["units_printed"] != float64(4) || prog["units_total"] != float64(6) {
		t.Fatalf("avancement : %v", prog)
	}
	if txt := resultText(g); !strings.Contains(txt, "Pieds") || !strings.Contains(txt, "4/6 unités") {
		t.Errorf("texte : %s", txt)
	}
	if d["suggested_status"] != "printing" || d["project"].(map[string]any)["status"] != "idea" { // CA2.4
		t.Errorf("suggestion %v, statut %v", d["suggested_status"], d["project"])
	}

	// CA11.4 : en attente, du plus récent au plus ancien ; l'annulée n'y figure pas
	pend := structured(t, keep(call(t, cs, "get_pending_verdicts", map[string]any{})))
	pl := pend["pending"].([]any)
	if pend["total"] != float64(4) || len(pl) != 4 || pl[0].(map[string]any)["print_job_id"] != "jC1" {
		t.Fatalf("en attente : %v", pend)
	}
	if len(pend["fail_reasons"].([]any)) == 0 {
		t.Error("vocabulaire des raisons absent")
	}
	// CA11.1 : verdict, puis modification ; la fiche pièce du projet reflète le compteur
	v := structured(t, keep(call(t, cs, "set_print_verdict", map[string]any{"print_job_id": "jA1", "verdict": "success", "note": "propre"})))
	if v["verdict"] != "success" || v["changed"] != true || v["verdict_source"] != "claude_on_request" || v["verdict_at"] == nil {
		t.Fatalf("set_print_verdict : %v", v)
	}
	if again := structured(t, call(t, cs, "set_print_verdict", map[string]any{"print_job_id": "jA1", "verdict": "success"})); again["changed"] != false {
		t.Errorf("idempotence : %v", again)
	}
	v2 := structured(t, keep(call(t, cs, "set_print_verdict", map[string]any{"print_job_id": "jA1", "verdict": "failed"})))
	if v2["verdict"] != "failed" || v2["fail_reason"] != "other" || v2["previous_verdict"] != "success" || len(v2["warnings"].([]any)) == 0 { // CA11.2
		t.Fatalf("échec sans raison : %v", v2)
	}
	if n := auditCount(t, c, "insights.verdict.set"); n != 2 {
		t.Errorf("entrées d'audit : %d", n)
	}
	if r, _ := cs.CallTool(ctx, &mcp.CallToolParams{Name: "set_print_verdict", Arguments: map[string]any{"print_job_id": "jA1", "verdict": "failed", "reason": "magie"}}); !r.IsError || !strings.Contains(resultText(r), "stringing") {
		t.Errorf("raison hors vocabulaire : %v", resultText(r))
	}
	if r, _ := cs.CallTool(ctx, &mcp.CallToolParams{Name: "set_print_verdict", Arguments: map[string]any{"print_job_id": "jRun", "verdict": "success"}}); !r.IsError || !strings.Contains(resultText(r), "pas terminée") { // CA11.3
		t.Errorf("non terminée : %v", resultText(r))
	}
	if r, _ := cs.CallTool(ctx, &mcp.CallToolParams{Name: "set_print_verdict", Arguments: map[string]any{"print_job_id": "jInconnue", "verdict": "success"}}); !r.IsError {
		t.Error("inconnue : erreur attendue")
	}
	// depuis la carte : source human_card
	cv := structured(t, keep(call(t, cs, "submit_print_verdict", map[string]any{"print_job_id": "jB1", "verdict": "redo", "reason": "warping"})))
	if cv["verdict_source"] != "human_card" || cv["verdict"] != "redo" {
		t.Errorf("submit_print_verdict : %v", cv)
	}
	pend = structured(t, call(t, cs, "get_pending_verdicts", map[string]any{"limit": 1}))
	if pend["total"] != float64(2) || len(pend["pending"].([]any)) != 1 {
		t.Errorf("après verdicts : %v", pend)
	}
	// le verdict ne change pas « imprimée » (D7), seulement le jugement
	d = structured(t, keep(call(t, cs, "get_project", map[string]any{"id": pid})))
	parts := d["parts"].([]any)
	var pa map[string]any
	for _, x := range parts {
		if x.(map[string]any)["model_id"] == "mA" {
			pa = x.(map[string]any)
		}
	}
	if pa["printed"] != float64(2) || pa["judged_failed"] != float64(1) || pa["unjudged"] != float64(1) || pa["judged_success"] != float64(0) {
		t.Errorf("compteur de la pièce : %v", pa)
	}
	// statut : seul update_project / submit_project_status le change (CA2.4), archivage masque (CA2.6)
	u := structured(t, call(t, cs, "update_project", map[string]any{"id": pid, "status": "printing"}))
	if u["status"] != "printing" {
		t.Errorf("update_project : %v", u)
	}
	sd := structured(t, keep(call(t, cs, "submit_project_status", map[string]any{"project_id": subID, "status": "design"})))
	if sd["project"].(map[string]any)["status"] != "design" {
		t.Errorf("submit_project_status : %v", sd)
	}
	sp := structured(t, call(t, cs, "submit_project_part", map[string]any{"project_id": subID, "model_id": "mC", "quantity": 4}))
	if sp["progress"].(map[string]any)["units_total"] != float64(4) {
		t.Errorf("submit_project_part : %v", sp)
	}
	call(t, cs, "update_project", map[string]any{"id": pid, "archived": true})
	l := structured(t, keep(call(t, cs, "list_projects", map[string]any{})))
	if l["total"] != float64(1) || l["projects"].([]any)[0].(map[string]any)["project"].(map[string]any)["id"] != subID {
		t.Errorf("liste après archivage : %v", l)
	}
	if l := structured(t, call(t, cs, "list_projects", map[string]any{"include_archived": true, "status": "printing"})); l["total"] != float64(1) {
		t.Errorf("filtre de statut : %v", l)
	}
	call(t, cs, "remove_project_part", map[string]any{"project_id": pid, "model_id": "mB"})

	// CA2.3 : get_project_costs rend exactement la même sortie qu'avant
	costsAfter, _ := json.Marshal(structured(t, call(t, cs, "get_project_costs", map[string]any{})))
	if string(costsBefore) != string(costsAfter) {
		t.Errorf("get_project_costs a changé :\n%s\n%s", costsBefore, costsAfter)
	}
	// aucun secret ni chemin dans les sorties G9
	for _, o := range outputs {
		if m := secretPatterns.FindString(o); m != "" {
			t.Fatalf("motif sensible %q dans une sortie : %s", m, o)
		}
	}
	// aucun outil n'a envoyé de commande à une imprimante : aucune confirmation créée
	var n int
	if err := c.Store.DB().QueryRow(`SELECT COUNT(*) FROM print_confirmations`).Scan(&n); err != nil || n != 0 {
		t.Errorf("confirmations : %d %v", n, err)
	}
}

func auditCount(t *testing.T, c *core.Core, op string) int {
	t.Helper()
	var n int
	if err := c.Store.DB().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE operation=?`, op).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Aucun appel de lancement, d'arrêt, de reprise, d'approbation ou d'annulation dans le code G9 de mcpserver.
func TestG9SourcesNeverStartStopResumeOrApprove(t *testing.T) {
	re := regexp.MustCompile(`\.(Approve|Start|Stop|Resume|Cancel|Launch)[A-Za-z]*\(`)
	for _, f := range []string{"project_tools.go", "project_views.go", "sheet_tools.go"} {
		b, err := os.ReadFile(filepath.Join(".", f))
		if err != nil {
			t.Fatal(err)
		}
		if m := re.FindString(string(b)); m != "" {
			t.Errorf("%s : appel interdit %q", f, m)
		}
	}
}
