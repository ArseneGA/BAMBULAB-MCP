package mcpserver_test

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

// Outils de fiches G9 (PJ5 et PJ10) : lecture seule, jamais d'approbation ni de lancement.
var sheetTools = []string{"get_part_sheet", "get_print_sheet", "get_sheet_photo"}

// Conformité de tools/list : Title, annotations de lecture, outputSchema d'objet, cartes liées, visibilité app pour la photo.
func TestSheetToolsConformAndSafe(t *testing.T) {
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))
	by := listToolsByName(t, cs)
	forbiddenName := regexp.MustCompile(`approve|start|launch|stop|resume|cancel`)
	for _, n := range sheetTools {
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
		if tl.Annotations != nil && !tl.Annotations.ReadOnlyHint {
			t.Errorf("%s : lecture seule attendue", n)
		}
		meta, _ := json.Marshal(tl.Meta)
		if (n == "get_sheet_photo") != strings.Contains(string(meta), `"visibility":["app"]`) {
			t.Errorf("%s : visibilité app seulement pour la photo : %s", n, meta)
		}
		if raw, _ := json.Marshal(tl.InputSchema); regexp.MustCompile(`"(approved|approve|nonce|token|confirm_token|human|plate_clear|confirmation_id|code)"`).Match(raw) {
			t.Errorf("%s : champ d'approbation dans le schéma : %s", n, raw)
		}
	}
	for n, uri := range map[string]string{"get_part_sheet": mcpserver.PartViewURI, "get_print_sheet": mcpserver.PrintSheetViewURI} {
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
	if !got[mcpserver.PartViewURI] || !got[mcpserver.PrintSheetViewURI] {
		t.Errorf("ressources ui:// manquantes : %v", got)
	}
}

// De bout en bout : CA5.1 (5 impressions à 100 %, 4 réussies, 1 ratée), CA10.x via les outils ; aucun secret ; rien n'est lancé.
func TestSheetToolsFlow(t *testing.T) {
	ctx := context.Background()
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))
	var outputs []string
	keep := func(res *mcp.CallToolResult) *mcp.CallToolResult {
		outputs = append(outputs, resultAll(res))
		return res
	}
	seedModel(t, c, "mA", "Équerre")
	seedModel(t, c, "mZ", "Neuve")
	for i := 1; i <= 5; i++ {
		seedJob(t, c, "jA"+string(rune('0'+i)), "mA", "COMPLETED", 100, i)
	}
	for i := 1; i <= 4; i++ {
		call(t, cs, "set_print_verdict", map[string]any{"print_job_id": "jA" + string(rune('0'+i)), "verdict": "success"})
	}
	call(t, cs, "set_print_verdict", map[string]any{"print_job_id": "jA5", "verdict": "failed", "reason": "stringing"})

	res := keep(call(t, cs, "get_part_sheet", map[string]any{"model_id": "mA"}))
	sh := structured(t, res)
	counts := sh["counts"].(map[string]any)
	if counts["printed"] != float64(5) || counts["judged_success"] != float64(4) || counts["judged_failed"] != float64(1) {
		t.Fatalf("compteurs : %v", counts)
	}
	if txt := resultText(res); !strings.Contains(txt, "imprimée 5 fois (100 % atteint), 4 jugées réussies, 1 jugée ratée") || !strings.Contains(txt, "stringing") {
		t.Errorf("texte : %s", txt)
	}
	if len(sh["prints"].([]any)) != 5 || len(sh["profiles"].([]any)) != 1 {
		t.Errorf("impressions / profils : %v", sh)
	}
	if lim := structured(t, call(t, cs, "get_part_sheet", map[string]any{"model_id": "mA", "limit": 2, "include": []string{"prints"}})); len(lim["prints"].([]any)) != 2 || lim["prints_truncated"] != true || lim["prints_total"] != float64(5) {
		t.Errorf("limite : %v", lim)
	}
	z := keep(call(t, cs, "get_part_sheet", map[string]any{"model_id": "mZ"}))
	if zs := structured(t, z); zs["counts"].(map[string]any)["never_printed"] != true || !strings.Contains(resultText(z), "jamais imprimée") {
		t.Errorf("jamais imprimée : %v", zs)
	}
	if r, _ := cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_part_sheet", Arguments: map[string]any{"model_id": "mdl_inconnu"}}); !r.IsError {
		t.Error("pièce inconnue : erreur attendue")
	}
	if r, _ := cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_part_sheet", Arguments: map[string]any{"model_id": "mA", "include": []string{"zzz"}}}); !r.IsError || !strings.Contains(resultText(r), "versions") {
		t.Errorf("section inconnue : %v", resultText(r))
	}

	ps := keep(call(t, cs, "get_print_sheet", map[string]any{"print_job_id": "jA5"}))
	p := structured(t, ps)
	v := p["verdict"].(map[string]any)
	if v["value"] != "failed" || v["fail_reason"] != "stringing" || v["level"] != "declared" {
		t.Errorf("verdict : %v", v)
	}
	prof := p["profile"].(map[string]any)
	if prof["frozen_note"] == nil || prof["settings"].(map[string]any)["process"] != "0.20mm Standard @BBL X1C" {
		t.Errorf("profil : %v", prof)
	}
	if p["duration"].(map[string]any)["text"] == nil || p["camera"].(map[string]any)["alerts_status"] == nil || p["approval"].(map[string]any)["status"] != "none" {
		t.Errorf("fiche : %v", p)
	}
	if r, _ := cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_print_sheet", Arguments: map[string]any{"print_job_id": "inconnue"}}); !r.IsError {
		t.Error("impression inconnue : erreur attendue")
	}
	// get_print_log reste inchangé (sortie de journal, sans les blocs de fiche).
	if pl := structured(t, call(t, cs, "get_print_log", map[string]any{"id": "jA5"})); pl["profile"] != nil || pl["verdict"] != "failed" {
		t.Errorf("get_print_log : %v", pl)
	}
	// get_sheet_photo : empreinte invalide ou photo inconnue refusées.
	if r, _ := cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_sheet_photo", Arguments: map[string]any{"sha256": "../../etc/passwd"}}); !r.IsError {
		t.Error("sha256 invalide : erreur attendue")
	}
	if r, _ := cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_sheet_photo", Arguments: map[string]any{"sha256": strings.Repeat("a", 64)}}); !r.IsError {
		t.Error("photo inconnue : erreur attendue")
	}
	for _, o := range outputs {
		if m := secretPatterns.FindString(o); m != "" {
			t.Fatalf("motif sensible %q dans une sortie : %s", m, o)
		}
	}
	// Lecture seule : aucune confirmation créée.
	var n int
	if err := c.Store.DB().QueryRow(`SELECT COUNT(*) FROM print_confirmations`).Scan(&n); err != nil || n != 0 {
		t.Errorf("confirmations : %d %v", n, err)
	}
}
