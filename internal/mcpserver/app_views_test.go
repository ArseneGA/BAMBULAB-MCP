package mcpserver_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

func metaUI(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	ui, ok := m["ui"].(map[string]any)
	if !ok {
		t.Fatalf("_meta.ui absent : %v", m)
	}
	return ui
}

// La ressource ui:// est servie avec le MIME MCP Apps et une CSP sans domaine externe.
func TestAppViewResources(t *testing.T) {
	ctx := context.Background()
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))

	lr, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]*mcp.Resource{}
	for _, r := range lr.Resources {
		found[r.URI] = r
	}
	for _, uri := range []string{mcpserver.SearchViewURI, mcpserver.ModelViewURI, mcpserver.PrintConfirmViewURI} {
		r := found[uri]
		if r == nil {
			t.Fatalf("ressource %s non déclarée : %v", uri, lr.Resources)
		}
		if r.MIMEType != "text/html;profile=mcp-app" {
			t.Fatalf("MIME de la liste : %q", r.MIMEType)
		}
		rr, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil {
			t.Fatal(err)
		}
		if len(rr.Contents) != 1 {
			t.Fatalf("contenus : %d", len(rr.Contents))
		}
		ct := rr.Contents[0]
		if ct.MIMEType != "text/html;profile=mcp-app" || ct.URI != uri {
			t.Fatalf("contenu : %q %q", ct.MIMEType, ct.URI)
		}
		if !strings.Contains(strings.ToLower(ct.Text), "<html") || len(ct.Text) < 200 {
			t.Fatalf("HTML vide ou invalide (%d octets)", len(ct.Text))
		}
		ui := metaUI(t, ct.Meta)
		csp, ok := ui["csp"].(map[string]any)
		if !ok {
			t.Fatalf("csp absente : %v", ui)
		}
		// Aucune origine externe : tout passe par callServerTool, Three.js est embarqué.
		for _, k := range []string{"connectDomains", "resourceDomains", "frameDomains", "baseUriDomains"} {
			if v, present := csp[k]; present && len(v.([]any)) > 0 {
				t.Fatalf("csp.%s doit rester vide : %v", k, v)
			}
		}
		// Un bundle construit ne référence aucune ressource distante.
		if mcpserver.ViewsBuilt() {
			for _, bad := range []string{`src="http`, `href="http`, `url(http`, `@import`} {
				if strings.Contains(ct.Text, bad) {
					t.Fatalf("le bundle charge une ressource externe (%s)", bad)
				}
			}
		}
	}
	if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "ui://bambu/inconnue.html"}); err == nil {
		t.Fatal("une ressource inconnue doit échouer")
	}
}

// Les outils sont liés à leurs vues ; get_mesh_chunk est réservé à l'application.
func TestAppToolLinks(t *testing.T) {
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
	for name, uri := range map[string]string{"search_models": mcpserver.SearchViewURI, "get_model_details": mcpserver.ModelViewURI, "request_print_confirmation": mcpserver.PrintConfirmViewURI} {
		tt := tools[name]
		if tt == nil {
			t.Fatalf("outil %s absent", name)
		}
		ui := metaUI(t, tt.Meta)
		if ui["resourceUri"] != uri || tt.Meta["ui/resourceUri"] != uri {
			t.Fatalf("%s : lien de vue incorrect : %v", name, tt.Meta)
		}
	}
	mc := tools["get_mesh_chunk"]
	if mc == nil {
		t.Fatal("get_mesh_chunk absent")
	}
	vis, _ := metaUI(t, mc.Meta)["visibility"].([]any)
	if len(vis) != 1 || vis[0] != "app" {
		t.Fatalf("visibility attendue [app] : %v", mc.Meta)
	}
	if mc.Annotations == nil || !mc.Annotations.ReadOnlyHint {
		t.Fatal("get_mesh_chunk doit être en lecture seule")
	}
}

// Le GLB d'aperçu est livré par tranches de moins de 100 000 caractères, recomposables à l'identique.
func TestGetMeshChunk(t *testing.T) {
	ctx := context.Background()
	c, root := startCatalogCore(t)
	cs := connectClient(t, mcpserver.New(c))

	writeData(t, filepath.Join(root, "p", "cyl.stl"), meshData(t, "cylinder_binary.stl"))
	call(t, cs, "import_files", map[string]any{"paths": []string{filepath.Join(root, "p")}, "wait_seconds": 30})
	if !c.Catalog.Idle(ctx, 30*time.Second) {
		t.Fatal("travaux dérivés non terminés")
	}
	list := structured(t, call(t, cs, "search_models", map[string]any{}))
	id := arr(t, list, "models")[0].(map[string]any)["id"].(string)

	first := structured(t, call(t, cs, "get_mesh_chunk", map[string]any{"model_id": id}))
	n := int(first["chunk_count"].(float64))
	size := int64(first["size_bytes"].(float64))
	if n < 1 || size < 100 || first["encoding"] != "base64" || first["mime"] != "model/gltf-binary" {
		t.Fatalf("première tranche : %v", first)
	}
	var glb []byte
	for i := 0; i < n; i++ {
		ch := first
		if i > 0 {
			ch = structured(t, call(t, cs, "get_mesh_chunk", map[string]any{"model_id": id, "index": i}))
		}
		data := ch["data"].(string)
		if len(data) > 100_000 {
			t.Fatalf("tranche %d : %d caractères (> 100000)", i, len(data))
		}
		if ch["sha256"] != first["sha256"] {
			t.Fatal("empreinte différente entre tranches")
		}
		b, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			t.Fatal(err)
		}
		glb = append(glb, b...)
	}
	if int64(len(glb)) != size {
		t.Fatalf("taille recomposée %d, attendue %d", len(glb), size)
	}
	if !bytes.HasPrefix(glb, []byte("glTF")) || int64(binary.LittleEndian.Uint32(glb[8:12])) != size {
		t.Fatal("GLB invalide après recomposition")
	}

	// Erreurs : index hors limites, modèle inconnu.
	if msg := toolErr(t, cs, "get_mesh_chunk", map[string]any{"model_id": id, "index": n}); !strings.Contains(msg, "hors limites") {
		t.Fatalf("message : %s", msg)
	}
	if msg := toolErr(t, cs, "get_mesh_chunk", map[string]any{"model_id": "mdl_inconnu"}); msg == "" {
		t.Fatal("modèle inconnu : erreur attendue")
	}
}
