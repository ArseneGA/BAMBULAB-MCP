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

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

func formOf(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	f, _ := structured(t, res)["form"].(map[string]any)
	if f == nil {
		t.Fatalf("pas de carte-formulaire dans %v", res.StructuredContent)
	}
	return f
}

func fieldNames(f map[string]any) []string {
	var out []string
	for _, x := range f["fields"].([]any) {
		out = append(out, x.(map[string]any)["name"].(string))
	}
	return out
}

func textOfResult(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// TestFormToolsReturnACardWhenCalledWithoutFields : chaque outil à carte, appelé sans ses champs, renvoie une
// carte pré-remplie (jamais une erreur), le lien vers la vue, et un texte COMPLET pour les clients sans cartes.
func TestFormToolsReturnACardWhenCalledWithoutFields(t *testing.T) {
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))
	cases := []struct {
		tool, formID, submit string
		args                 map[string]any
		wantField            string
	}{
		{"add_printer", "add_printer", "submit_add_printer", map[string]any{"name": "Atelier"}, "serial"},
		{"set_cost_settings", "set_cost_settings", "submit_cost_settings", map[string]any{}, "electricity_per_kwh"},
		{"configure_notifications", "configure_notifications", "submit_notifications", map[string]any{}, ""},
		{"update_spool", "update_spool", "submit_spool", map[string]any{"material": ""}, "remaining_g"},
		{"set_manufacturing_goals", "set_manufacturing_goals", "submit_goals", map[string]any{"original_text": "un support solide"}, "strength"},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			res := call(t, cs, tc.tool, tc.args)
			f := formOf(t, res)
			if f["form_id"] != tc.formID || f["submit_tool"] != tc.submit || f["tool"] != tc.tool {
				t.Fatalf("carte inattendue : %v", f)
			}
			if tc.wantField != "" {
				found := false
				for _, n := range fieldNames(f) {
					found = found || n == tc.wantField
				}
				if !found {
					t.Fatalf("champ %q absent : %v", tc.wantField, fieldNames(f))
				}
			}
			for _, n := range fieldNames(f) {
				if mcpserver.IsSecretFieldName(n) {
					t.Fatalf("champ secret dans la carte : %s", n)
				}
			}
			if ui, _ := res.Meta["ui"].(map[string]any); ui["resourceUri"] != mcpserver.FormViewURI {
				t.Fatalf("lien vers la vue absent du résultat : %v", res.Meta)
			}
			if txt := textOfResult(res); !strings.Contains(txt, "Champs :") || !strings.Contains(txt, tc.tool) && tc.tool != "configure_notifications" {
				t.Fatalf("texte de repli incomplet : %q", txt)
			}
		})
	}
}

func TestAddPrinterCardPrefillsAndNeverAsksForTheAccessCode(t *testing.T) {
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))
	res := call(t, cs, "add_printer", map[string]any{"name": "Atelier", "host": "192.168.1.42"})
	f := formOf(t, res)
	vals := f["values"].(map[string]any)
	if vals["name"] != "Atelier" || vals["host"] != "192.168.1.42" || vals["nozzle_mm"] != 0.4 {
		t.Fatalf("pré-remplissage : %v", vals)
	}
	missing, _ := json.Marshal(f["missing"])
	if !strings.Contains(string(missing), "serial") || !strings.Contains(string(missing), "model_id") || strings.Contains(string(missing), "host") {
		t.Fatalf("champs manquants : %s", missing)
	}
	want := []string{"name", "model_id", "host", "serial", "nozzle_mm", "ams_units"}
	got := fieldNames(f)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("champs de la modale du site : %v", got)
	}
	raw, _ := json.Marshal(f)
	if !strings.Contains(strings.ToLower(string(raw)), "jamais") || strings.Contains(strings.ToLower(string(raw)), `"kind":"password"`) {
		t.Fatalf("la carte doit rappeler que le code d'accès ne se saisit pas ici : %s", raw)
	}
}

// TestSubmitToolsAreAppOnlyAndSecretFree : outils de soumission réservés à l'application, sans champ secret ni champ
// d'approbation ; un champ secret forgé est refusé.
func TestSubmitToolsAreAppOnlyAndSecretFree(t *testing.T) {
	ctx := context.Background()
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))
	lt, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]*mcp.Tool{}
	for _, tl := range lt.Tools {
		by[tl.Name] = tl
	}
	forbidden := regexp.MustCompile(`(?i)(print_job|confirmation|stop_id|plate_clear|idempotency|approv|launch|cancel|resume|upload|file_id|tray|slot|printer_id)`)
	seen := 0
	for _, n := range mcpserver.SubmitToolNames {
		tl := by[n]
		if tl == nil {
			t.Fatalf("outil %s absent de tools/list", n)
		}
		seen++
		ui, _ := tl.Meta["ui"].(map[string]any)
		vis, _ := json.Marshal(ui["visibility"])
		if string(vis) != `["app"]` {
			t.Fatalf("%s doit être réservé à l'application : %s", n, vis)
		}
		raw, _ := json.Marshal(tl.InputSchema)
		var sc struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		_ = json.Unmarshal(raw, &sc)
		for p := range sc.Properties {
			if mcpserver.IsSecretFieldName(p) || forbidden.MatchString(p) {
				t.Errorf("%s : champ interdit %q dans le schéma d'entrée", n, p)
			}
		}
	}
	if seen != len(mcpserver.SubmitToolNames) {
		t.Fatalf("outils de soumission : %d vus", seen)
	}
	// Un secret forgé (champ hors schéma) est refusé et rien n'est enregistré.
	for _, bad := range []map[string]any{{"access_code": "12345678"}, {"password": "x"}, {"launch_code": "123456"}} {
		args := map[string]any{"name": "Atelier", "model_id": "p1s", "host": "192.168.1.42", "serial": "01P00A000000"}
		for k, v := range bad {
			args[k] = v
		}
		if res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "submit_add_printer", Arguments: args}); err == nil && !res.IsError {
			t.Fatalf("champ secret accepté : %v", bad)
		}
	}
	if ps, _ := c.Printing.ListPrinters(ctx); len(ps) != 0 {
		t.Fatalf("une imprimante a été enregistrée malgré le champ secret : %v", ps)
	}
}

// TestFormSubmitToolsDoNothingPhysical : le code des cartes n'appelle ni lancement, ni arrêt, ni reprise, ni approbation,
// ni envoi à l'imprimante, ni coffre de secrets ; seuls des services d'enregistrement.
func TestFormSubmitToolsDoNothingPhysical(t *testing.T) {
	for _, f := range []string{"forms.go", "form_tools.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		code := string(src)
		for _, bad := range []string{"StartPrint", "CancelPrint", "ResumePrint", "PausePrint", "RequestStop", "Approve", "ConsumeToken", "UploadPrint", "UploadArtifact", "LoadFilament",
			"SetAccessCode", "AccessCode(", "LaunchCode", "ExecuteHeadlessSlice", "ConfirmPrint", "PatchChannel(ctx, notify.ChannelInput", "UpsertChannel", "SaveChannel"} {
			if strings.Contains(code, bad) {
				t.Errorf("%s contient %q : une carte-formulaire ne doit rien faire de physique ni toucher un secret", f, bad)
			}
		}
	}
}

// TestSubmitUsesTheSameServerValidation : la carte n'ajoute aucune validation plus laxiste que l'outil normal.
func TestSubmitUsesTheSameServerValidation(t *testing.T) {
	ctx := context.Background()
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))
	bad := map[string]any{"name": "X", "model_id": "p1s", "host": "8.8.8.8", "serial": "01P00A000000"}
	errOf := func(tool string) string {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: bad})
		if err != nil {
			return err.Error()
		}
		if !res.IsError {
			t.Fatalf("%s : adresse publique acceptée", tool)
		}
		return textOfResult(res)
	}
	a, b := errOf("add_printer"), errOf("submit_add_printer")
	if a != b || a == "" {
		t.Fatalf("validations différentes :\n normal : %s\n carte  : %s", a, b)
	}
	// Champs obligatoires : refusés par la soumission (la carte les marque obligatoires), mais l'outil normal affiche la carte.
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "submit_add_printer", Arguments: map[string]any{"name": "X"}})
	if err == nil && !res.IsError {
		t.Fatal("soumission incomplète acceptée")
	}
	if ps, _ := c.Printing.ListPrinters(ctx); len(ps) != 0 {
		t.Fatalf("imprimante enregistrée à tort : %v", ps)
	}
	// Prix : valeur négative refusée de la même façon.
	neg := map[string]any{"electricity_per_kwh": -1}
	_, e1 := cs.CallTool(ctx, &mcp.CallToolParams{Name: "set_cost_settings", Arguments: neg})
	r2, e2 := cs.CallTool(ctx, &mcp.CallToolParams{Name: "submit_cost_settings", Arguments: neg})
	if e1 == nil && e2 == nil {
		r1, _ := cs.CallTool(ctx, &mcp.CallToolParams{Name: "set_cost_settings", Arguments: neg})
		if r1.IsError != r2.IsError {
			t.Fatalf("validation des prix différente : normal=%v carte=%v", r1.IsError, r2.IsError)
		}
	}
}

func TestCostSettingsSubmitRoundTrip(t *testing.T) {
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))
	m := structured(t, call(t, cs, "submit_cost_settings", map[string]any{"electricity_per_kwh": 0.31, "filament_per_kg": map[string]any{"PLA": 18.5}, "margin_percent": 12, "margin_grams": 8}))
	if m["electricity_per_kwh"] != 0.31 {
		t.Fatalf("kWh non enregistré : %v", m)
	}
	fr, _ := m["form_result"].(map[string]any)
	if fr == nil || fr["ok"] != true {
		t.Fatalf("résumé absent : %v", m)
	}
	// La carte relit les valeurs enregistrées.
	f := formOf(t, call(t, cs, "set_cost_settings", map[string]any{}))
	vals := f["values"].(map[string]any)
	if vals["electricity_per_kwh"] != 0.31 || vals["filament_per_kg.PLA"] != 18.5 || vals["margin_percent"] != 12.0 {
		t.Fatalf("pré-remplissage : %v", vals)
	}
	if f["send"] != "changed" {
		t.Fatalf("send = %v", f["send"])
	}
	// L'outil normal avec des champs agit toujours directement (comportement inchangé).
	m = structured(t, call(t, cs, "set_cost_settings", map[string]any{"electricity_per_kwh": 0.4}))
	if m["electricity_per_kwh"] != 0.4 || m["form"] != nil {
		t.Fatalf("outil normal : %v", m)
	}
}

func TestSpoolCardCreateAndEdit(t *testing.T) {
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))
	m := structured(t, call(t, cs, "submit_spool", map[string]any{"material": "PETG", "name": "Bleu nuit", "color_hex": "#112244", "remaining_g": 640, "price_per_kg": 24}))
	sp := m["spool"].(map[string]any)
	id := sp["id"].(string)
	if sp["material"] != "PETG" || sp["remaining_g"] != 640.0 || m["form_result"] == nil {
		t.Fatalf("bobine : %v", m)
	}
	f := formOf(t, call(t, cs, "update_spool", map[string]any{"spool_id": id}))
	vals := f["values"].(map[string]any)
	if vals["spool_id"] != id || vals["material"] != "PETG" || vals["color_hex"] != "#112244" || f["send"] != "changed" {
		t.Fatalf("carte de modification : %v", f)
	}
	// Création complète par l'outil normal : comportement inchangé (pas de carte).
	m = structured(t, call(t, cs, "update_spool", map[string]any{"material": "PLA"}))
	if m["form"] != nil || m["spool"] == nil {
		t.Fatalf("outil normal : %v", m)
	}
}

func TestGoalsCardSubmit(t *testing.T) {
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))
	f := formOf(t, call(t, cs, "set_manufacturing_goals", map[string]any{"original_text": "support d'écran", "material": "PETG"}))
	if f["values"].(map[string]any)["material"] != "PETG" {
		t.Fatalf("pré-remplissage : %v", f["values"])
	}
	for _, want := range []string{"strength", "surface", "speed", "translucent", "economy"} {
		ok := false
		for _, n := range fieldNames(f) {
			ok = ok || n == want
		}
		if !ok {
			t.Fatalf("objectif %s absent", want)
		}
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "submit_goals", Arguments: map[string]any{"original_text": "support", "material": "PETG"}})
	if err == nil && !res.IsError {
		t.Fatal("soumission sans objectif acceptée")
	}
	m := structured(t, call(t, cs, "submit_goals", map[string]any{"original_text": "support d'écran solide et rapide", "material": "PETG", "strength": 1, "speed": 3}))
	if crit, _ := m["criteria"].([]any); len(crit) != 2 || m["form_result"] == nil {
		t.Fatalf("objectif : %v", m)
	}
}

func TestNotificationsCardHasNoSecretAndLinksToTheHumanPage(t *testing.T) {
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))
	m := structured(t, call(t, cs, "configure_notifications", map[string]any{}))
	f := m["form"].(map[string]any)
	links, _ := json.Marshal(f["links"])
	if !strings.Contains(string(links), "/setup/notifications") {
		t.Fatalf("lien vers la page humaine absent : %s", links)
	}
	raw, _ := json.Marshal(f)
	for _, bad := range []string{"smtp_password", "webhook_url", "ntfy_url", "\"password\""} {
		if strings.Contains(string(raw), bad) {
			t.Fatalf("la carte contient %s", bad)
		}
	}
	// La soumission sans canal passe et ne crée aucun canal.
	res := call(t, cs, "submit_notifications", map[string]any{"channels": []any{map[string]any{"name": "inconnu", "enabled": true}}})
	if !res.IsError {
		t.Fatal("un canal inexistant ne doit pas être créé par la carte")
	}
}

func TestTemplateCardFromDescribe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	root, _ := filepath.EvalSymlinks(t.TempDir())
	c, err := core.Open(ctx, core.Config{DataDir: t.TempDir(), Logger: quietLog(), PollInterval: 10 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond,
		Catalog: catalog.Config{ImportRoots: []string{root}, UVPath: filepath.Join(root, "absent-uv"), Worker: mcpLibWorker}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done; c.Close() })
	cs := connectClient(t, mcpserver.New(c))

	f := formOf(t, call(t, cs, "create_from_template", map[string]any{"template": "washer"}))
	if f["submit_tool"] != "submit_part_template" || f["preview_tool"] != "preview_part_template" || f["values"].(map[string]any)["template"] != "washer" {
		t.Fatalf("carte gabarit : %v", f)
	}
	desc := structured(t, call(t, cs, "describe_part_template", map[string]any{"template": "washer"}))
	params, _ := desc["params"].([]any)
	n := 0
	for _, name := range fieldNames(f) {
		if strings.HasPrefix(name, "params.") {
			n++
		}
	}
	if n != len(params) || n == 0 {
		t.Fatalf("%d champs de paramètres pour %d paramètres du gabarit", n, len(params))
	}
	if f["values"].(map[string]any)["params.inner_diameter"] != 6.6 {
		t.Fatalf("valeur par défaut du gabarit non reprise : %v", f["values"])
	}
	// La soumission passe par la même création que l'outil historique.
	m := structured(t, call(t, cs, "submit_part_template", map[string]any{"template": "washer", "name": "w1", "params": map[string]any{"inner_diameter": 8}}))
	if m["model_id"] == nil || m["form_result"] == nil {
		t.Fatalf("création par la carte : %v", m)
	}
	// no_form : création immédiate avec les défauts (comportement historique).
	if m2 := structured(t, call(t, cs, "create_from_template", map[string]any{"template": "washer", "no_form": true, "name": "w2"})); m2["model_id"] == nil || m2["form"] != nil {
		t.Fatalf("no_form : %v", m2)
	}
	// Gabarit inconnu : même erreur que l'outil historique (pas de carte vide).
	if r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "create_from_template", Arguments: map[string]any{"template": "nexiste-pas"}}); err == nil && !r.IsError {
		t.Fatal("gabarit inconnu accepté")
	}
}
