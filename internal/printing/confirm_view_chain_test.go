package printing_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Chaîne complète de la confirmation DANS Claude (vue MCP App) : définition de l'outil dans tools/list, ressource ui://
// listée et lisible, résultat de l'outil (texte pour le modèle + _meta), état sans code défini, puis durée de l'accord.
func TestConfirmationViewChain(t *testing.T) {
	r := launchRig(t)
	ctx := context.Background()
	const viewURI = "ui://bambu/print-confirm.html"

	// 1. tools/list : la DÉFINITION de l'outil porte le lien de vue (clé moderne et clé historique).
	tl, err := r.cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]*mcp.Tool{}
	for _, tt := range tl.Tools {
		tools[tt.Name] = tt
	}
	for _, name := range []string{"request_print_confirmation"} {
		tt := tools[name]
		if tt == nil {
			t.Fatalf("%s absent de tools/list", name)
		}
		ui, _ := tt.Meta["ui"].(map[string]any)
		if ui["resourceUri"] != viewURI || tt.Meta["ui/resourceUri"] != viewURI {
			t.Fatalf("%s : _meta de la définition : %v", name, tt.Meta)
		}
		if vis, has := ui["visibility"]; has {
			t.Fatalf("%s doit rester visible du modèle : %v", name, vis)
		}
	}
	if ui, _ := tools["cancel_print"].Meta["ui"].(map[string]any); ui["resourceUri"] != "ui://bambu/print-stop.html" {
		t.Fatalf("cancel_print : %v", tools["cancel_print"].Meta)
	}
	for _, name := range []string{"approve_print_with_code", "approve_stop_with_code", "get_launch_view_state", "get_stop_view_state"} {
		ui, _ := tools[name].Meta["ui"].(map[string]any)
		if vis, _ := ui["visibility"].([]any); len(vis) != 1 || vis[0] != "app" {
			t.Fatalf("%s doit être app-only : %v", name, tools[name].Meta)
		}
	}

	// 2. resources/list + resources/read : la ressource est lisible avec le MIME MCP Apps.
	lr, err := r.cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var listed bool
	for _, res := range lr.Resources {
		if res.URI == viewURI {
			listed = res.MIMEType == "text/html;profile=mcp-app"
		}
	}
	if !listed {
		t.Fatalf("ressource %s absente de resources/list ou mauvais MIME", viewURI)
	}
	rr, err := r.cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: viewURI})
	if err != nil || len(rr.Contents) != 1 || rr.Contents[0].MIMEType != "text/html;profile=mcp-app" || !strings.Contains(strings.ToLower(rr.Contents[0].Text), "<html") {
		t.Fatalf("resources/read : %v %+v", err, rr)
	}
	// La photo du plateau arrive en data: par get_launch_view_state (aucun domaine réseau à autoriser) : la CSP de la
	// ressource reste vide ; la CSP par défaut de la spec MCP Apps autorise img-src data:.
	csp, _ := rr.Contents[0].Meta["ui"].(map[string]any)["csp"].(map[string]any)
	if len(csp) != 0 {
		t.Fatalf("CSP inattendue : %v", csp)
	}

	// 3. résultat de l'outil : _meta de vue + texte qui dit au modèle que la confirmation s'affiche dans la conversation.
	job, _ := r.uploadAndAsk(r.fakeArtifact(), "cle-depot-chaine-vue")
	res, err := r.cs.CallTool(ctx, &mcp.CallToolParams{Name: "request_print_confirmation", Arguments: map[string]any{"print_job_id": job}})
	if err != nil || res.IsError {
		t.Fatalf("request_print_confirmation : %v %+v", err, res)
	}
	if ui, _ := res.Meta["ui"].(map[string]any); ui["resourceUri"] != viewURI || res.Meta["ui/resourceUri"] != viewURI {
		t.Fatalf("_meta du résultat : %v", res.Meta)
	}
	var txt string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			txt += tc.Text
		}
	}
	for _, want := range []string{"Ce client n'a pas annoncé", "JAMAIS son code", "/setup/launch-code", "LANCE L'IMPRESSION LUI-MÊME", "n'appelle PAS start_print"} {
		if !strings.Contains(txt, want) {
			t.Errorf("le texte du résultat ne contient pas %q : %s", want, txt)
		}
	}
	if strings.Contains(txt, "doit ouvrir") {
		t.Errorf("le texte renvoie encore l'utilisateur vers le navigateur : %s", txt)
	}
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": job})
	if cf["binding"] == nil || cf["safety"] == nil || str(cf, "confirm_url") == "" {
		t.Fatalf("structuredContent incomplet pour la vue : %v", cf)
	}
	confID := str(cf, "confirmation_id")

	// 4. sans code défini : l'état dit où le définir (la vue affiche alors le lien de secours).
	vs := r.call("get_launch_view_state", map[string]any{"confirmation_id": confID})
	if vs["code_set"] != false || !strings.HasSuffix(str(vs, "setup_url"), "/setup/launch-code") || !strings.HasPrefix(str(vs, "setup_url"), "http") {
		t.Fatalf("état sans code : %v", vs)
	}

	// 5. durée : la confirmation vit 10 min depuis la demande ; l'accord de 2 min court depuis l'approbation (code ou page).
	exp, err := time.Parse(time.RFC3339Nano, str(cf, "expires_at"))
	if err != nil || time.Until(exp) < 9*time.Minute || time.Until(exp) > 10*time.Minute+5*time.Second {
		t.Fatalf("expires_at = %v (%v) : attendu ~10 min", exp, err)
	}
	r.humanSetLaunchCode(goodCode)
	if vs := r.call("get_launch_view_state", map[string]any{"confirmation_id": confID}); vs["approved"] != false || vs["seconds_left"] != nil {
		t.Fatalf("avant approbation, aucun compte à rebours : %v", vs)
	}
	m := r.call("approve_print_with_code", approveArgs(cf, goodCode, true))
	if m["approved"] != true || m["started"] != true || str(m, "outcome") != "printing" || len(r.sim.StartCommands()) != 1 {
		t.Fatalf("le code doit approuver ET lancer l'impression, une seule fois : %v", m)
	}
	// start_print du modèle ensuite : constat sans renvoi.
	if s := r.call("start_print", map[string]any{"print_job_id": job, "confirmation_id": confID, "idempotency_key": "cle-modele-apres"}); s["replayed"] != true || len(r.sim.StartCommands()) != 1 {
		t.Fatalf("start_print après le lancement automatique : %v", s)
	}
	if vs := r.call("get_launch_view_state", map[string]any{"confirmation_id": confID}); vs["approved"] != true {
		t.Fatalf("état après approbation : %v", vs)
	}
}
