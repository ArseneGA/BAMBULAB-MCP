package mcpserver_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

func boxSTL(t *testing.T, x, y, z float64) []byte {
	t.Helper()
	p := func(a, b, c float64) mesh.Vec3 { return mesh.Vec3{X: a, Y: b, Z: c} }
	m := &mesh.Mesh{
		V: []mesh.Vec3{p(0, 0, 0), p(x, 0, 0), p(x, y, 0), p(0, y, 0), p(0, 0, z), p(x, 0, z), p(x, y, z), p(0, y, z)},
		T: []mesh.Tri{{0, 2, 1}, {0, 3, 2}, {4, 5, 6}, {4, 6, 7}, {0, 1, 5}, {0, 5, 4}, {1, 2, 6}, {1, 6, 5}, {2, 3, 7}, {2, 7, 6}, {3, 0, 4}, {3, 4, 7}},
	}
	b, err := mesh.STLBytes(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func importBox(t *testing.T, cs *mcp.ClientSession, root, file string, x, y, z float64) (id, text string) {
	t.Helper()
	path := filepath.Join(root, file)
	writeData(t, path, boxSTL(t, x, y, z))
	res := call(t, cs, "import_model", map[string]any{"path": path, "wait_seconds": 30})
	st := structured(t, res)
	f, _ := st["file"].(map[string]any)
	id, _ = f["model_id"].(string)
	if id == "" {
		t.Fatalf("import sans model_id : %v", st)
	}
	return id, resultText(res)
}

// De bout en bout : proposition au bon moment, plan (image + questions + hypothèses), refus sans entrée, calcul,
// historique rattaché à la version, journal sans secret.
func TestStrengthFlow(t *testing.T) {
	ctx := context.Background()
	c, root := startCatalogCore(t)
	cs := connectClient(t, mcpserver.New(c))

	// 1. proposition : seulement pour une pièce qui semble porteuse, une seule fois par modèle
	id, txt := importBox(t, cs, root, "support_etagere.stl", 60, 10, 8)
	if !strings.Contains(txt, "test de résistance") || !strings.Contains(txt, "assess_strength") || !strings.Contains(txt, "porter une charge") {
		t.Fatalf("import d'un support : proposition absente\n%s", txt)
	}
	if _, txt2 := importBox(t, cs, root, "cube_calibration.stl", 20, 20, 20); strings.Contains(txt2, "test de résistance") {
		t.Errorf("proposition inutile pour un cube de calibration : %s", txt2)
	}
	if again := resultText(call(t, cs, "analyze_geometry", map[string]any{"model_id": id, "machine": "p1s"})); strings.Contains(again, "test de résistance") {
		t.Errorf("proposition répétée pour le même modèle")
	}

	// 2. instructions du serveur : proposer, ne jamais inventer, poser les questions
	if ins := cs.InitializeResult().Instructions; !strings.Contains(ins, "plan_strength_test") || !strings.Contains(ins, "Ne jamais inventer") || !strings.Contains(ins, "assumed") {
		t.Errorf("instructions sans consigne du test de résistance : %s", ins)
	}

	// 3. plan : image, zones numérotées, questions une à une, hypothèses proposées
	plan := call(t, cs, "plan_strength_test", map[string]any{"model_id": id, "usage": "porte-guitare mural"})
	if plan.IsError {
		t.Fatalf("plan : %+v", plan.Content)
	}
	var hasImage bool
	for _, ct := range plan.Content {
		if im, ok := ct.(*mcp.ImageContent); ok && im.MIMEType == "image/png" && len(im.Data) > 1000 {
			hasImage = true
		}
	}
	ptxt := resultText(plan)
	if !hasImage || !strings.Contains(ptxt, "UNE par une") || !strings.Contains(ptxt, "guitare") || !strings.Contains(ptxt, "HYPOTHÈSES PROPOSÉES") {
		t.Fatalf("plan incomplet (image %v)\n%s", hasImage, ptxt)
	}
	ps := structured(t, plan)
	if n := len(arr(t, ps, "zones")); n < 5 {
		t.Errorf("%d zones", n)
	}
	if n := len(arr(t, ps, "questions")); n < 7 {
		t.Errorf("%d questions", n)
	}
	prop := ps["proposed_assumptions"].(map[string]any)
	loads := arr(t, prop, "loads")
	if len(loads) != 1 || loads[0].(map[string]any)["source"] != "assumed" {
		t.Errorf("charge proposée : %v", loads)
	}
	zones := arr(t, ps, "zones")
	var wall, tip float64
	for _, z := range zones {
		zm := z.(map[string]any)
		switch zm["kind"] {
		case "paroi_x-":
			wall = zm["id"].(float64)
		case "paroi_x+":
			tip = zm["id"].(float64)
		}
	}
	if wall == 0 || tip == 0 {
		t.Fatalf("zones paroi absentes : %v", zones)
	}

	// 4. refus sans charge ni appui : la consigne pointe vers plan_strength_test
	if e := toolErr(t, cs, "assess_strength", map[string]any{"model_id": id}); !strings.Contains(e, "plan_strength_test") && !strings.Contains(e, "loads") {
		t.Errorf("refus sans charge : %q", e)
	}
	if e := toolErr(t, cs, "assess_strength", map[string]any{"model_id": id, "loads": []any{}, "supports": []any{}}); !strings.Contains(e, "plan_strength_test") {
		t.Errorf("refus sans charge ni appui : %q", e)
	}
	if e := toolErr(t, cs, "assess_strength", map[string]any{"model_id": id,
		"loads":    []any{map[string]any{"zone": tip, "force_N": 40}},
		"supports": []any{map[string]any{"zone": wall, "type": "fixed", "source": "user"}}}); !strings.Contains(e, "source") {
		t.Errorf("source de charge obligatoire : %q", e)
	}

	// 5. calcul avec une hypothèse annoncée : le verdict le dit
	args := map[string]any{"model_id": id, "material": "PETG", "infill": 30, "walls": 3, "layer_orientation": "z", "duration": "occasional",
		"loads":    []any{map[string]any{"zone": tip, "force_N": 40, "direction": "down", "source": "assumed", "note": "charge supposée"}},
		"supports": []any{map[string]any{"zone": wall, "type": "fixed", "source": "user"}}}
	res := call(t, cs, "assess_strength", args)
	if res.IsError {
		t.Fatalf("assess : %+v", res.Content)
	}
	st := structured(t, res)
	if st["state"] != "done" {
		// calcul encore en cours : attendre le travail
		jid, _ := st["job_id"].(string)
		deadline := time.Now().Add(2 * time.Minute)
		for time.Now().Before(deadline) {
			if j, err := c.Queue.Get(ctx, jid); err == nil && j.State.Terminal() {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		res = call(t, cs, "get_strength_report", map[string]any{"model_id": id, "job_id": jid})
	}
	atxt := resultText(res)
	for _, want := range []string{"VERDICT", "BASÉ SUR DES HYPOTHÈSES", "ne remplace pas un essai réel", "MESURÉ", "CALCULÉ", "ESTIMÉ", "RECOMMANDATIONS", "Déplacement maximal"} {
		if !strings.Contains(atxt, want) {
			t.Errorf("texte du résultat sans %q\n%s", want, atxt)
		}
	}
	var img bool
	for _, ct := range res.Content {
		if im, ok := ct.(*mcp.ImageContent); ok && len(im.Data) > 1000 {
			img = true
		}
	}
	if !img {
		t.Error("carte de contraintes PNG absente")
	}
	if ui, _ := res.Meta["ui"].(map[string]any); ui["resourceUri"] != mcpserver.StrengthViewURI {
		t.Errorf("lien de vue absent du résultat : %v", res.Meta)
	}
	t.Log(atxt)

	// 6. historique rattaché à la version
	rep := call(t, cs, "get_strength_report", map[string]any{"model_id": id})
	rs := structured(t, rep)
	tests := arr(t, rs, "tests")
	if len(tests) != 1 {
		t.Fatalf("%d test(s) enregistré(s)", len(tests))
	}
	t0 := tests[0].(map[string]any)
	if t0["version_id"] == "" || t0["based_on_assumptions"] != true || t0["verdict"] == "" {
		t.Errorf("enregistrement : %v", t0)
	}
	detail := rs["detail"].(map[string]any)
	if in := detail["input"].(map[string]any); len(in["loads"].([]any)) != 1 {
		t.Errorf("entrées non conservées : %v", in)
	}

	// 7. vue et outils déclarés
	rr, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: mcpserver.StrengthViewURI})
	if err != nil || len(rr.Contents) != 1 {
		t.Fatalf("ressource de la vue : %v", err)
	}
	tl, _ := cs.ListTools(ctx, nil)
	got := map[string]*mcp.Tool{}
	for _, tt := range tl.Tools {
		got[tt.Name] = tt
	}
	for _, n := range []string{"plan_strength_test", "assess_strength", "get_strength_report"} {
		if got[n] == nil {
			t.Fatalf("outil %s absent", n)
		}
	}
	if metaUI(t, got["assess_strength"].Meta)["resourceUri"] != mcpserver.StrengthViewURI {
		t.Errorf("assess_strength sans vue : %v", got["assess_strength"].Meta)
	}

	// 8. journal sans secret : paramètres des travaux et audit
	js, _ := c.Queue.List(ctx, "", 50)
	for _, j := range js {
		if j.Kind == "strength.assess" && contracts.ContainsSecret(j.Params) {
			t.Errorf("secret dans les paramètres du travail %s", j.ID)
		}
	}
	rows, err := c.Store.DB().QueryContext(ctx, `SELECT operation, COALESCE(detail,'') FROM audit_log WHERE operation LIKE 'strength.%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var op, d string
		if err := rows.Scan(&op, &d); err != nil {
			t.Fatal(err)
		}
		n++
		raw, _ := json.Marshal(d)
		if contracts.ContainsSecret(raw) || strings.Contains(strings.ToLower(d), "token") || strings.Contains(strings.ToLower(d), "password") {
			t.Errorf("secret dans le journal : %s %s", op, d)
		}
	}
	if n < 2 {
		t.Errorf("journal : %d ligne(s) strength.*", n)
	}
}
