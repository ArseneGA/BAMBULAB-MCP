package mcpserver_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

func TestExportProjectTools(t *testing.T) {
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))

	sha, _, err := c.Blobs.Put(bytes.NewReader([]byte("solid t\nendsolid t\n")), "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, q := range []string{
		`INSERT INTO collections(id,name,created_at) VALUES('col_m','Support','` + now + `')`,
		`INSERT INTO models(id,collection_id,name,tags,created_at,updated_at,description,license) VALUES('mod_m','col_m','Pièce','[]','` + now + `','` + now + `','','')`,
		`INSERT INTO source_files(id,model_id,name,format,size_bytes,sha256,availability,created_at,rel_path,unit) VALUES('fil_m','mod_m','p.stl','stl',19,'` + sha + `','linux','` + now + `','','mm')`,
		`INSERT INTO model_versions(id,model_id,label,note,source_file_ids,created_at) VALUES('ver_m','mod_m','v1','','["fil_m"]','` + now + `')`,
	} {
		if _, err := c.Store.DB().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	lt, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, tl := range lt.Tools {
		have[tl.Name] = true
	}
	if !have["export_project"] || !have["get_project_export"] {
		t.Fatalf("outils d'export absents : %v", have)
	}

	res := call(t, cs, "export_project", map[string]any{"collection_id": "Support", "options": map[string]any{
		"license": "CC BY 4.0", "assembly_notes": "Rien à assembler.", "hardware": "aucune", "quantities": map[string]int{"mod_m": 3}}})
	out := structured(t, res)
	id, _ := out["export_id"].(string)
	if id == "" || out["job_id"] == "" || out["state"] != "ready" {
		t.Fatalf("export_project : %v", out)
	}
	dl, _ := out["download"].(map[string]any)
	if dl == nil || !strings.Contains(dl["path"].(string), "/api/exports/"+id+".zip?exp=") {
		t.Fatalf("lien de téléchargement absent : %v", out)
	}
	if steps, _ := out["next_steps"].([]any); len(steps) < 5 {
		t.Fatalf("étapes de publication manuelles absentes : %v", out["next_steps"])
	}
	txt := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(txt, "rien n'est publié") || !strings.Contains(txt, "MakerWorld") {
		t.Fatalf("texte : %s", txt)
	}
	if strings.Contains(txt, c.Cfg.DataDir) {
		t.Fatalf("chemin du serveur dans la réponse : %s", txt)
	}

	// relecture : nouveau lien, liste
	g := structured(t, call(t, cs, "get_project_export", map[string]any{"export_id": id}))
	if g["download"] == nil {
		t.Fatalf("get_project_export : %v", g)
	}
	l := structured(t, call(t, cs, "get_project_export", map[string]any{}))
	raw, _ := json.Marshal(l)
	if !strings.Contains(string(raw), id) {
		t.Fatalf("liste : %s", raw)
	}
	// entrée invalide : erreur d'outil claire
	r, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "export_project", Arguments: map[string]any{"model_ids": []string{"mod_nope"}}})
	if err == nil && !r.IsError {
		t.Fatal("modèle inconnu accepté")
	}
}
