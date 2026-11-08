package mcpserver_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

// Vues de progression et de trajectoires : liées aux bons outils (définition ET résultat), ressources lisibles,
// outil de la vue réservé à l'application ; les descriptions orientent vers les aperçus qui marchent sans vue.
func TestProgressAndLayersViewLinks(t *testing.T) {
	ctx := context.Background()
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))
	tl, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]*mcp.Tool{}
	for _, tt := range tl.Tools {
		tools[tt.Name] = tt
	}
	for name, uri := range map[string]string{"get_job_status": mcpserver.ProgressViewURI, "get_upload_status": mcpserver.ProgressViewURI} {
		tt := tools[name]
		if tt == nil {
			t.Fatalf("outil %s absent", name)
		}
		if ui := metaUI(t, tt.Meta); ui["resourceUri"] != uri || tt.Meta["ui/resourceUri"] != uri {
			t.Fatalf("%s : lien de vue incorrect : %v", name, tt.Meta)
		}
	}
	// Le résultat porte aussi le lien (get_upload_status d'une session inconnue = erreur ; get_job_status idem) : on
	// vérifie sur un envoi réel plus bas dans TestGetUploadStatusResultMeta ; ici les ressources.
	for _, uri := range []string{mcpserver.ProgressViewURI, mcpserver.LayersViewURI} {
		rr, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil || len(rr.Contents) != 1 || rr.Contents[0].MIMEType != "text/html;profile=mcp-app" || len(rr.Contents[0].Text) < 200 {
			t.Fatalf("ressource %s : %v", uri, err)
		}
	}
	// Outils : show_layers visible, get_layer_paths réservé à l'application, capture_preview documente les nouveaux modes.
	if sl := tools["show_layers"]; sl == nil || metaUI(t, sl.Meta)["resourceUri"] != mcpserver.LayersViewURI {
		t.Fatalf("show_layers : %+v", sl)
	}
	lp := tools["get_layer_paths"]
	if lp == nil {
		t.Fatal("get_layer_paths absent")
	}
	if vis, _ := metaUI(t, lp.Meta)["visibility"].([]any); len(vis) != 1 || vis[0] != "app" {
		t.Fatalf("get_layer_paths doit être réservé à l'application : %v", lp.Meta)
	}
	cp := tools["capture_preview"]
	if cp == nil {
		t.Fatal("capture_preview absent")
	}
	for _, want := range []string{"first_layer", "views=", "every_n", "gif", "EMPRISE", "aucune vue"} {
		if !strings.Contains(cp.Description, want) {
			t.Errorf("capture_preview : description sans %q", want)
		}
	}
	for _, name := range []string{"get_preview_asset", "show_atelier"} {
		if d := tools[name].Description; !strings.Contains(d, "get_model_details") || !strings.Contains(d, "capture_preview") {
			t.Errorf("%s : la description doit orienter vers le viewer et capture_preview : %q", name, d)
		}
	}
	if ps := tools["capture_preview"].InputSchema; ps == nil {
		t.Fatal("schéma de capture_preview absent")
	}
}

// get_model_details et get_preview_asset indiquent comment VOIR le modèle sans réseau privé ni vue.
func TestModelDetailsPointsToViewerAndFourViews(t *testing.T) {
	ctx := context.Background()
	c, root := startCatalogCore(t)
	cs := connectClient(t, mcpserver.New(c))
	writeData(t, filepath.Join(root, "p", "cyl.stl"), meshData(t, "cylinder_binary.stl"))
	call(t, cs, "import_files", map[string]any{"paths": []string{filepath.Join(root, "p")}, "wait_seconds": 30})
	if !c.Catalog.Idle(ctx, 30*time.Second) {
		t.Fatal("travaux dérivés non terminés")
	}
	id := arr(t, structured(t, call(t, cs, "search_models", map[string]any{})), "models")[0].(map[string]any)["id"].(string)

	res := call(t, cs, "get_model_details", map[string]any{"model_id": id})
	txt := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(txt, "viewer 3D") || !strings.Contains(txt, `capture_preview(model_id="`+id+`", views=["all"])`) {
		t.Fatalf("texte de get_model_details : %s", txt)
	}
	if ui, _ := res.Meta["ui"].(map[string]any); ui["resourceUri"] != mcpserver.ModelViewURI {
		t.Fatalf("résultat sans lien de vue : %v", res.Meta)
	}
	pa := call(t, cs, "get_preview_asset", map[string]any{"model_id": id})
	if t2 := pa.Content[0].(*mcp.TextContent).Text; !strings.Contains(t2, "réseau privé") || !strings.Contains(t2, "views") {
		t.Fatalf("texte de get_preview_asset : %s", t2)
	}
}
