package mcpserver_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

// startCatalogCore ouvre un cœur avec une racine d'import et ses workers.
func startCatalogCore(t *testing.T) (*core.Core, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c, err := core.Open(ctx, core.Config{
		DataDir: t.TempDir(), Logger: quietLog(),
		PollInterval: 10 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond,
		Catalog: catalog.Config{ImportRoots: []string{root}, UVPath: filepath.Join(root, "absent-uv")},
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done; c.Close() })
	return c, root
}

func writeData(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func meshData(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "mesh", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func toolErr(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return err.Error() // refus au niveau protocole (validation du schéma)
	}
	if !res.IsError {
		t.Fatalf("%s devait échouer : %+v", name, res.StructuredContent)
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func arr(t *testing.T, m map[string]any, key string) []any {
	t.Helper()
	a, ok := m[key].([]any)
	if !ok {
		t.Fatalf("%s absent ou pas un tableau : %v", key, m[key])
	}
	return a
}

func TestCatalogToolsEndToEnd(t *testing.T) {
	ctx := context.Background()
	c, root := startCatalogCore(t)
	cs := connectClient(t, mcpserver.New(c))

	writeData(t, filepath.Join(root, "lot", "cube.stl"), meshData(t, "cube10_ascii.stl"))
	writeData(t, filepath.Join(root, "lot", "copie.stl"), meshData(t, "cube10_ascii.stl"))
	writeData(t, filepath.Join(root, "lot", "sous", "cyl.stl"), meshData(t, "cylinder_binary.stl"))
	writeData(t, filepath.Join(root, "lot", "sous", "forme.obj"), meshData(t, "cube10_quads.obj"))
	writeData(t, filepath.Join(root, "lot", "multi.3mf"), meshData(t, "multi_cm.3mf"))

	// scan_folder : lecture seule.
	sc := structured(t, call(t, cs, "scan_folder", map[string]any{"path": filepath.Join(root, "lot")}))
	if sc["read_only"] != true || sc["total_files"].(float64) != 5 || sc["new_files"].(float64) != 5 {
		t.Fatalf("scan : %v", sc)
	}
	if got := structured(t, call(t, cs, "search_models", map[string]any{})); got["total"].(float64) != 0 {
		t.Fatalf("le scan a créé des modèles : %v", got)
	}

	// import_files avec attente : rapport complet, doublon détecté, originaux intacts.
	imp := structured(t, call(t, cs, "import_files", map[string]any{"paths": []string{filepath.Join(root, "lot")}, "wait_seconds": 30, "tags": []string{"Lot A"}}))
	rep := imp["report"].(map[string]any)["counts"].(map[string]any)
	if imp["done"] != true || rep["imported"].(float64) != 4 || rep["duplicate"].(float64) != 1 {
		t.Fatalf("import : %v", imp)
	}
	if _, err := os.Stat(filepath.Join(root, "lot", "copie.stl")); err != nil {
		t.Fatal("original supprimé ou déplacé")
	}
	if !c.Catalog.Idle(ctx, 30*time.Second) {
		t.Fatal("travaux dérivés non terminés")
	}

	// get_job_status relit le travail d'import.
	js := structured(t, call(t, cs, "get_job_status", map[string]any{"job_id": imp["job_id"]}))
	if js["state"] != "succeeded" || js["kind"] != "catalog.import" {
		t.Fatalf("job : %v", js)
	}

	// search_models : pagination, filtres.
	p1 := structured(t, call(t, cs, "search_models", map[string]any{"limit": 3, "tags": []string{"lot a"}}))
	if p1["total"].(float64) != 4 || len(arr(t, p1, "models")) != 3 || p1["has_more"] != true || p1["next_offset"].(float64) != 3 {
		t.Fatalf("page 1 : %v", p1)
	}
	p2 := structured(t, call(t, cs, "search_models", map[string]any{"limit": 3, "offset": 3, "collection": "lot"}))
	if len(arr(t, p2, "models")) != 1 || p2["has_more"] == true {
		t.Fatalf("page 2 : %v", p2)
	}
	cubes := structured(t, call(t, cs, "search_models", map[string]any{"text": "copie", "format": "stl"}))
	models := arr(t, cubes, "models")
	if len(models) != 1 {
		t.Fatalf("recherche texte : %v", cubes)
	}
	cube := models[0].(map[string]any)
	id := cube["id"].(string)
	prev := cube["preview"].(map[string]any)
	if prev["state"] != "ready" || !strings.Contains(prev["url"].(string), "/api/assets/") || !strings.Contains(prev["url"].(string), "sig=") {
		t.Fatalf("aperçu : %v", prev)
	}

	// list_collection.
	lc := structured(t, call(t, cs, "list_collection", map[string]any{"collection": "lot"}))
	if len(arr(t, lc, "subcollections")) != 1 || lc["models"].(map[string]any)["total"].(float64) != 2 {
		t.Fatalf("list_collection : %v", lc)
	}

	// get_model_details.
	d := structured(t, call(t, cs, "get_model_details", map[string]any{"model_id": id}))
	if len(arr(t, d, "files")) != 1 || len(arr(t, d, "versions")) != 1 || d["analysis"].(map[string]any)["state"] != "ok" {
		t.Fatalf("détails : %v", d)
	}
	if dm := d["model"].(map[string]any)["dimensions"].(map[string]any); dm["x"].(float64) != 10 {
		t.Fatalf("dimensions : %v", dm)
	}

	// get_preview_asset : référence + URL signée, pas de contenu.
	pa := structured(t, call(t, cs, "get_preview_asset", map[string]any{"model_id": id}))
	url := pa["preview"].(map[string]any)["url"].(string)
	if pa["mime"] != "model/gltf-binary" || !strings.HasPrefix(url, "http://127.0.0.1") || len(url) > 400 {
		t.Fatalf("aperçu : %v", pa)
	}

	// analyze_geometry.
	an := structured(t, call(t, cs, "analyze_geometry", map[string]any{"model_id": id, "machine": "p1s"}))
	if an["state"] != "ok" || an["fabricability"] == nil || an["analysis"] == nil || len(arr(t, an, "caveats")) == 0 {
		t.Fatalf("analyze : %v", an)
	}

	// repair_mesh : un cube sain n'a rien à réparer, aucune version n'est créée.
	rp := structured(t, call(t, cs, "repair_mesh", map[string]any{"model_id": id}))
	if rp["created"] != false || rp["report"] == nil || len(arr(t, rp, "caveats")) == 0 {
		t.Fatalf("repair_mesh : %v", rp)
	}

	// find_exact_duplicates : le doublon n'a pas été ré-importé, donc aucun groupe ; contrôle par chemin.
	dup := structured(t, call(t, cs, "find_exact_duplicates", map[string]any{}))
	if dup["total_groups"].(float64) != 0 {
		t.Fatalf("doublons : %v", dup)
	}
	chk := structured(t, call(t, cs, "find_exact_duplicates", map[string]any{"path": filepath.Join(root, "lot", "cube.stl")}))
	if chk["in_catalog"] != true {
		t.Fatalf("contrôle : %v", chk)
	}

	// tag_models.
	tg := structured(t, call(t, cs, "tag_models", map[string]any{"model_ids": []string{id}, "add": []string{"Favori"}, "remove": []string{"lot a"}}))
	if len(arr(t, tg, "updated")) != 1 {
		t.Fatalf("tag : %v", tg)
	}

	// preview_file_operations puis apply_file_operations : l'aperçu ne change rien.
	pv := structured(t, call(t, cs, "preview_file_operations", map[string]any{"operations": []map[string]any{
		{"op": "rename", "model_ids": []string{id}, "new_name": "Cube de référence"},
		{"op": "delete", "model_ids": []string{id}},
	}}))
	token := pv["token"].(string)
	if len(arr(t, pv, "changes")) != 2 || pv["to_trash"].(float64) != 1 {
		t.Fatalf("aperçu des opérations : %v", pv)
	}
	if det := structured(t, call(t, cs, "get_model_details", map[string]any{"model_id": id})); det["model"].(map[string]any)["name"] != "copie" {
		t.Fatal("l'aperçu a modifié le modèle")
	}
	if msg := toolErr(t, cs, "apply_file_operations", map[string]any{"token": "pas-un-vrai-jeton-valide-123"}); !strings.Contains(msg, "jeton") {
		t.Fatalf("jeton inconnu : %s", msg)
	}
	ap := structured(t, call(t, cs, "apply_file_operations", map[string]any{"token": token}))
	if ap["applied"].(float64) != 2 || len(arr(t, ap, "trashed")) != 1 {
		t.Fatalf("apply : %v", ap)
	}
	if msg := toolErr(t, cs, "apply_file_operations", map[string]any{"token": token}); !strings.Contains(msg, "déjà été utilisé") {
		t.Fatalf("réutilisation du jeton : %s", msg)
	}
	tr := structured(t, call(t, cs, "search_models", map[string]any{"only_trashed": true}))
	if tr["total"].(float64) != 1 {
		t.Fatalf("corbeille : %v", tr)
	}
	rs := structured(t, call(t, cs, "restore_from_trash", map[string]any{"model_ids": []string{id}}))
	if len(arr(t, rs, "restored")) != 1 {
		t.Fatalf("restore : %v", rs)
	}
	if det := structured(t, call(t, cs, "get_model_details", map[string]any{"model_id": id})); det["model"].(map[string]any)["name"] != "Cube de référence" {
		t.Fatal("nom non appliqué")
	}
}

func TestCatalogToolsRefuseOutsideRoots(t *testing.T) {
	c, root := startCatalogCore(t)
	cs := connectClient(t, mcpserver.New(c))
	outside := t.TempDir()
	writeData(t, filepath.Join(outside, "secret.stl"), meshData(t, "cube10_ascii.stl"))
	for tool, args := range map[string]map[string]any{
		"import_files":          {"paths": []string{outside}},
		"import_model":          {"path": filepath.Join(outside, "secret.stl")},
		"scan_folder":           {"path": outside},
		"find_exact_duplicates": {"path": filepath.Join(outside, "secret.stl")},
	} {
		if msg := toolErr(t, cs, tool, args); !strings.Contains(msg, "hors des racines autorisées") {
			t.Fatalf("%s : %s", tool, msg)
		}
	}
	if msg := toolErr(t, cs, "scan_folder", map[string]any{"path": filepath.Join(root, "..", filepath.Base(outside))}); msg == "" {
		t.Fatal("traversée acceptée")
	}
	// Entrées invalides rejetées par le schéma (champ inconnu, requis manquant).
	if msg := toolErr(t, cs, "import_files", map[string]any{"paths": []string{root}, "move": true}); msg == "" {
		t.Fatal("champ inconnu accepté")
	}
	if msg := toolErr(t, cs, "preview_file_operations", map[string]any{}); msg == "" {
		t.Fatal("opérations requises")
	}
	if got := structured(t, call(t, cs, "search_models", map[string]any{})); got["total"].(float64) != 0 {
		t.Fatal("rien ne devait être importé")
	}
}

func TestImportModelAndLinkVersionsTools(t *testing.T) {
	c, root := startCatalogCore(t)
	cs := connectClient(t, mcpserver.New(c))
	a := filepath.Join(root, "a.stl")
	b := filepath.Join(root, "b.stl")
	writeData(t, a, meshData(t, "cube10_binary.stl"))
	writeData(t, b, meshData(t, "cylinder_binary.stl"))

	r1 := structured(t, call(t, cs, "import_model", map[string]any{"path": a, "units": "mm", "name": "Pince", "wait_seconds": 30}))
	r2 := structured(t, call(t, cs, "import_model", map[string]any{"path": b, "name": "Pince révisée", "wait_seconds": 30}))
	f1, f2 := r1["file"].(map[string]any), r2["file"].(map[string]any)
	if r1["done"] != true || f1["status"] != "imported" || f2["status"] != "imported" {
		t.Fatalf("%v %v", r1, r2)
	}
	if msg := toolErr(t, cs, "import_model", map[string]any{"path": root}); !strings.Contains(msg, "fichier") {
		t.Fatalf("dossier refusé par import_model : %s", msg)
	}
	lk := structured(t, call(t, cs, "link_model_versions", map[string]any{
		"target_model_id": f1["model_id"], "version_model_ids": []string{f2["model_id"].(string)}, "note": "révision B"}))
	if len(arr(t, lk, "created")) != 1 || len(arr(t, lk, "trashed")) != 1 {
		t.Fatalf("link : %v", lk)
	}
	d := structured(t, call(t, cs, "get_model_details", map[string]any{"model_id": f1["model_id"]}))
	vs := arr(t, d, "versions")
	if len(vs) != 2 || vs[0].(map[string]any)["label"] != "v2" || vs[0].(map[string]any)["note"] != "révision B" {
		t.Fatalf("versions : %v", vs)
	}
}

func TestCatalogToolSchemas(t *testing.T) {
	c, _ := startCatalogCore(t)
	cs := connectClient(t, mcpserver.New(c))
	lt, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	need := map[string]string{
		"import_files": `"paths"`, "scan_folder": `"path"`, "get_model_details": `"model_id"`, "apply_file_operations": `"token"`,
		"preview_file_operations": `"operations"`, "restore_from_trash": `"model_ids"`, "link_model_versions": `"target_model_id"`,
	}
	got := map[string]bool{}
	for _, tl := range lt.Tools {
		in, _ := json.Marshal(tl.InputSchema)
		out, _ := json.Marshal(tl.OutputSchema)
		if want, ok := need[tl.Name]; ok {
			if !strings.Contains(string(in), `"required":[`) || !strings.Contains(string(in), want) {
				t.Fatalf("%s : schéma d'entrée %s", tl.Name, in)
			}
			got[tl.Name] = true
		}
		if len(out) < 10 {
			t.Fatalf("%s : pas de schéma de sortie", tl.Name)
		}
	}
	if len(got) != len(need) {
		t.Fatalf("outils manquants : %v", got)
	}
}
