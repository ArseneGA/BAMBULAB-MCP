package mcpserver_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

func uiOf(t *testing.T, m mcp.Meta) map[string]any {
	t.Helper()
	ui, _ := m["ui"].(map[string]any)
	return ui
}

// create_upload_link : l'URL, la commande curl exacte, le suivi par get_upload_status, la carte de la pièce ensuite.
func TestCreateUploadLinkTool(t *testing.T) {
	c, _ := startCatalogCore(t)
	cs := connectClient(t, mcpserver.New(c))
	c.Catalog.SetUploadLinkBase("https://atelier.exemple.ts.net/", true)
	data := meshData(t, "cube10_binary.stl")
	h := sha256.Sum256(data)
	sha := hex.EncodeToString(h[:])

	out := structured(t, call(t, cs, "create_upload_link", map[string]any{"filename": "mon support.stl", "size": len(data), "sha256": sha, "collection": "Envois"}))
	url, _ := out["url"].(string)
	curl, _ := out["curl"].(string)
	if !strings.HasPrefix(url, "https://atelier.exemple.ts.net/upload/") || len(url) != len("https://atelier.exemple.ts.net/upload/")+43 {
		t.Fatalf("url : %q", url)
	}
	for _, want := range []string{"curl -sS -X PUT -T ", "'CHEMIN/mon support.stl'", "'" + url + "'", "application/octet-stream"} {
		if !strings.Contains(curl, want) {
			t.Errorf("commande curl sans %q : %s", want, curl)
		}
	}
	if out["method"] != "PUT" || out["single_use"] != true || out["internet_reachable"] != true || out["expires_in_seconds"].(float64) != 600 || out["max_bytes"].(float64) != float64(len(data)) {
		t.Fatalf("sortie : %v", out)
	}
	if types, _ := out["allowed_types"].([]any); len(types) != 5 {
		t.Fatalf("types : %v", types)
	}
	id := out["link_id"].(string)

	// en attente
	st := structured(t, call(t, cs, "get_upload_status", map[string]any{"upload_id": id}))
	if st["status"] != "in_progress" || st["percent"].(float64) != 0 {
		t.Fatalf("en attente : %v", st)
	}

	// le PUT arrive (ici directement dans le service : la route HTTP est testée dans httpapi)
	token := strings.TrimPrefix(url, "https://atelier.exemple.ts.net/upload/")
	if _, err := c.Catalog.UploadLinkReceive(t.Context(), token, strings.NewReader(string(data)), int64(len(data))); err != nil {
		t.Fatal(err)
	}
	res := call(t, cs, "get_upload_status", map[string]any{"upload_id": id})
	done := structured(t, res)
	f, _ := done["file"].(map[string]any)
	if done["status"] != "complete" || f["status"] != "imported" || f["sha256"] != sha {
		t.Fatalf("terminé : %v", done)
	}
	txt := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(txt, "get_model_details(model_id=\""+f["model_id"].(string)+"\")") {
		t.Fatalf("le résultat doit proposer la carte de la pièce : %s", txt)
	}
	if uiOf(t, res.Meta)["resourceUri"] != mcpserver.ModelViewURI {
		t.Fatalf("le résultat doit porter le lien de la vue du modèle : %v", res.Meta)
	}

	// refus : type non importable (.glb), nom avec chemin, lien annulé
	for _, bad := range []string{"x.glb", "x.gcode", "../x.stl"} {
		if e := toolErr(t, cs, "create_upload_link", map[string]any{"filename": bad}); e == "" {
			t.Errorf("%s accepté", bad)
		}
	}
	if e := toolErr(t, cs, "create_upload_link", map[string]any{"filename": "x.glb"}); !strings.Contains(e, ".glb") {
		t.Errorf("message .glb : %s", e)
	}
	o2 := structured(t, call(t, cs, "create_upload_link", map[string]any{"filename": "b.stl"}))
	call(t, cs, "cancel_upload", map[string]any{"upload_id": o2["link_id"]})
	tok2 := strings.TrimPrefix(o2["url"].(string), "https://atelier.exemple.ts.net/upload/")
	if _, err := c.Catalog.UploadLinkReceive(t.Context(), tok2, strings.NewReader(string(data)), int64(len(data))); err == nil {
		t.Error("un lien annulé ne doit plus servir")
	}

	// adresse privée : le modèle est prévenu
	c.Catalog.SetUploadLinkBase("http://bambu.lan:7878", false)
	o3 := structured(t, call(t, cs, "create_upload_link", map[string]any{"filename": "c.stl"}))
	if o3["internet_reachable"] != false || !strings.Contains(o3["note"].(string), "réseau privé") {
		t.Fatalf("adresse privée : %v", o3)
	}
}

// upload_file : un morceau intermédiaire n'ouvre aucune carte ; la fin propose la carte de la pièce ; drop_file_chunk
// (outil de la carte de dépôt) a le même effet mais n'est pas visible du modèle.
func TestUploadCompletionOffersModelCardAndDropTools(t *testing.T) {
	c, _ := startCatalogCore(t)
	cs := connectClient(t, mcpserver.New(c))
	data := meshData(t, "cylinder_binary.stl")
	h := sha256.Sum256(data)
	sha := hex.EncodeToString(h[:])
	enc := func(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
	const piece = 300

	r1 := call(t, cs, "upload_file", map[string]any{"filename": "cyl.stl", "total_size": len(data), "sha256": sha, "data_base64": enc(data[:piece])})
	if r1.Meta["ui"] != nil || strings.Contains(r1.Content[0].(*mcp.TextContent).Text, "get_model_details") {
		t.Fatalf("un morceau intermédiaire ne doit pas proposer de carte : %v", r1.Meta)
	}
	id := structured(t, r1)["upload_id"].(string)
	var last *mcp.CallToolResult
	for off := piece; off < len(data); off += piece {
		last = call(t, cs, "upload_file", map[string]any{"upload_id": id, "offset": off, "data_base64": enc(data[off:min(off+piece, len(data))])})
	}
	f := structured(t, last)["file"].(map[string]any)
	if !strings.Contains(last.Content[0].(*mcp.TextContent).Text, "get_model_details(model_id=\""+f["model_id"].(string)+"\")") || uiOf(t, last.Meta)["resourceUri"] != mcpserver.ModelViewURI {
		t.Fatalf("la fin d'un envoi doit proposer la carte de la pièce : %v", last.Meta)
	}

	// drop_file_chunk : mêmes paramètres, un seul appel
	cube := meshData(t, "cube10_binary.stl")
	ch := sha256.Sum256(cube)
	drop := call(t, cs, "drop_file_chunk", map[string]any{"filename": "cube.stl", "total_size": len(cube), "sha256": hex.EncodeToString(ch[:]), "data_base64": enc(cube)})
	if s := structured(t, drop); s["status"] != "complete" {
		t.Fatalf("drop_file_chunk : %v", s)
	}

	lt, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]*mcp.Tool{}
	for _, tl := range lt.Tools {
		by[tl.Name] = tl
	}
	if vis, _ := uiOf(t, by["drop_file_chunk"].Meta)["visibility"].([]any); len(vis) != 1 || vis[0] != "app" {
		t.Fatalf("drop_file_chunk doit être réservé à la vue : %v", by["drop_file_chunk"].Meta)
	}
	if uiOf(t, by["upload_file"].Meta) != nil {
		t.Fatalf("upload_file ne doit pas être lié à une vue (une carte par morceau) : %v", by["upload_file"].Meta)
	}
	sd := by["show_file_drop"]
	if sd == nil || uiOf(t, sd.Meta)["resourceUri"] != mcpserver.DropViewURI {
		t.Fatalf("show_file_drop : %+v", sd)
	}
	for _, want := range []string{"75000", "UN SEUL appel", "EMPREINTE FINALE", "get_upload_status", "create_upload_link", "get_model_details"} {
		if !strings.Contains(by["upload_file"].Description, want) {
			t.Errorf("upload_file : description sans %q", want)
		}
	}
	for _, want := range []string{"curl", "UNE fois", "10 minutes", "sha256", ".glb"} {
		if !strings.Contains(by["create_upload_link"].Description, want) {
			t.Errorf("create_upload_link : description sans %q", want)
		}
	}
	for _, uri := range []string{mcpserver.DropViewURI, mcpserver.GcodeViewURI} {
		rr, err := cs.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: uri})
		if err != nil || len(rr.Contents) != 1 || rr.Contents[0].MIMEType != "text/html;profile=mcp-app" {
			t.Fatalf("ressource %s : %v", uri, err)
		}
	}

	// show_file_drop : texte + structuré pour la carte
	sf := call(t, cs, "show_file_drop", map[string]any{"collection": "Envois"})
	ss := structured(t, sf)
	if ss["kind"] != "file_drop" || ss["chunk_bytes"].(float64) != mcpserver.DropChunkBytes || ss["meta"].(map[string]any)["collection"] != "Envois" {
		t.Fatalf("show_file_drop : %v", ss)
	}
	if uiOf(t, sf.Meta)["resourceUri"] != mcpserver.DropViewURI {
		t.Fatalf("show_file_drop : résultat sans lien de vue : %v", sf.Meta)
	}
}

// import_model : la fiche de la pièce (viewer 3D) est proposée au lieu de rendus fabriqués.
func TestImportModelOffersModelCard(t *testing.T) {
	c, root := startCatalogCore(t)
	cs := connectClient(t, mcpserver.New(c))
	p := filepath.Join(root, "cube.stl")
	writeData(t, p, meshData(t, "cube10_binary.stl"))
	res := call(t, cs, "import_model", map[string]any{"path": p, "wait_seconds": 30})
	st := structured(t, res)
	f, _ := st["file"].(map[string]any)
	if f == nil || f["model_id"] == nil {
		t.Fatalf("import : %v", st)
	}
	if !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "get_model_details(model_id=\""+f["model_id"].(string)+"\")") || uiOf(t, res.Meta)["resourceUri"] != mcpserver.ModelViewURI {
		t.Fatalf("import_model doit proposer la carte de la pièce : %v %v", res.Meta, res.Content[0])
	}
}
