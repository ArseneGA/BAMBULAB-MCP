package mcpserver_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

func TestUploadFileToolChunkedAndResume(t *testing.T) {
	ctx := context.Background()
	c, root := startCatalogCore(t)
	cs := connectClient(t, mcpserver.New(c))
	data := meshData(t, "cylinder_binary.stl")
	h := sha256.Sum256(data)
	sha := hex.EncodeToString(h[:])
	enc := func(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
	const piece = 300

	// Premier morceau : ouvre la session.
	first := structured(t, call(t, cs, "upload_file", map[string]any{
		"filename": "cyl.stl", "total_size": len(data), "sha256": sha, "offset": 0, "data_base64": enc(data[:piece]),
		"collection": "Envois", "tags": []string{"claude"}, "license": "CC0", "source": "généré par Claude"}))
	id, _ := first["upload_id"].(string)
	if id == "" || first["status"] != "in_progress" || first["next_offset"].(float64) != piece {
		t.Fatalf("premier appel : %v", first)
	}
	// Mauvais offset : pas d'erreur, la réponse donne le bon offset.
	bad := structured(t, call(t, cs, "upload_file", map[string]any{"upload_id": id, "offset": 5000, "data_base64": enc(data[piece : piece+10])}))
	if bad["next_offset"].(float64) != piece || !strings.Contains(bad["note"].(string), "offset") {
		t.Fatalf("offset : %v", bad)
	}
	st := structured(t, call(t, cs, "get_upload_status", map[string]any{"upload_id": id}))
	if st["received"].(float64) != piece {
		t.Fatalf("statut : %v", st)
	}
	// Suite et fin.
	var last map[string]any
	for off := piece; off < len(data); off += piece {
		end := min(off+piece, len(data))
		last = structured(t, call(t, cs, "upload_file", map[string]any{"upload_id": id, "offset": off, "data_base64": enc(data[off:end])}))
	}
	if last["status"] != "complete" {
		t.Fatalf("fin : %v", last)
	}
	f := last["file"].(map[string]any)
	if f["status"] != "imported" || f["sha256"] != sha {
		t.Fatalf("fichier : %v", f)
	}
	if !c.Catalog.Idle(ctx, 30*time.Second) {
		t.Fatal("travaux dérivés non terminés")
	}
	d := structured(t, call(t, cs, "get_model_details", map[string]any{"model_id": f["model_id"]}))
	m := d["model"].(map[string]any)
	if m["license"] != "CC0" || m["name"] != "cyl" {
		t.Fatalf("fiche : %v", m)
	}

	// Fichier en un seul appel + capacités.
	cube := meshData(t, "cube10_binary.stl")
	ch := sha256.Sum256(cube)
	one := structured(t, call(t, cs, "upload_file", map[string]any{"filename": "cube.stl", "total_size": len(cube), "sha256": hex.EncodeToString(ch[:]), "data_base64": enc(cube)}))
	if one["status"] != "complete" || one["file"].(map[string]any)["status"] != "imported" {
		t.Fatalf("un seul appel : %v", one)
	}
	caps := structured(t, call(t, cs, "get_capabilities", map[string]any{}))
	up, ok := caps["upload"].(map[string]any)
	if !ok {
		t.Fatalf("capacités sans upload : %v", caps)
	}
	if roots := up["import_roots"].([]any); len(roots) < 1 || roots[0] != root {
		t.Fatalf("dossiers d'import : %v (attendu %s)", up["import_roots"], root)
	}
	if up["max_upload_bytes"].(float64) <= 0 || up["max_chunk_chars"].(float64) != 100000 || up["url_import"].(map[string]any)["enabled"] != true {
		t.Fatalf("limites : %v", up)
	}
}

func TestUploadFileToolRefusals(t *testing.T) {
	c, _ := startCatalogCore(t)
	cs := connectClient(t, mcpserver.New(c))
	data := meshData(t, "cube10_binary.stl")
	h := sha256.Sum256(data)
	sha := hex.EncodeToString(h[:])
	enc := base64.StdEncoding.EncodeToString(data)

	// sha256 faux.
	wrong := strings.Repeat("0", 64)
	if e := toolErr(t, cs, "upload_file", map[string]any{"filename": "a.stl", "total_size": len(data), "sha256": wrong, "data_base64": enc}); !strings.Contains(e, "sha256 incorrect") {
		t.Fatalf("sha : %s", e)
	}
	// Chemin dans le nom, extension inconnue, sha absent, morceau trop grand, base64 invalide.
	for name, args := range map[string]map[string]any{
		"traversée":    {"filename": "../x.stl", "total_size": len(data), "sha256": sha, "data_base64": enc},
		"sous-dossier": {"filename": "dir/x.stl", "total_size": len(data), "sha256": sha, "data_base64": enc},
		"extension":    {"filename": "x.exe", "total_size": len(data), "sha256": sha, "data_base64": enc},
		"sans sha":     {"filename": "x.stl", "total_size": len(data), "data_base64": enc},
		"trop grand":   {"filename": "x.stl", "total_size": 1 << 20, "sha256": sha, "data_base64": strings.Repeat("QUJD", 25_001)},
		"base64":       {"filename": "x.stl", "total_size": len(data), "sha256": sha, "data_base64": "***pas du base64***"},
		"format":       {"filename": "x.3mf", "total_size": len(data), "sha256": sha, "data_base64": enc},
	} {
		e := toolErr(t, cs, "upload_file", args)
		if e == "" {
			t.Errorf("%s : erreur vide", name)
		}
	}
	if got := structured(t, call(t, cs, "search_models", map[string]any{})); got["total"].(float64) != 0 {
		t.Fatalf("un envoi refusé a créé un modèle : %v", got)
	}
	// Session inconnue, annulation.
	if e := toolErr(t, cs, "get_upload_status", map[string]any{"upload_id": "upl_nope"}); !strings.Contains(e, "inconnue") {
		t.Fatal(e)
	}
	first := structured(t, call(t, cs, "upload_file", map[string]any{"filename": "ok.stl", "total_size": len(data), "sha256": sha, "data_base64": enc[:8]}))
	cancel := structured(t, call(t, cs, "cancel_upload", map[string]any{"upload_id": first["upload_id"]}))
	if cancel["canceled"] != true {
		t.Fatal(cancel)
	}
}

func TestImportFromURLToolRefusesOffList(t *testing.T) {
	c, _ := startCatalogCore(t)
	cs := connectClient(t, mcpserver.New(c))
	for _, u := range []string{"http://makerworld.com/x", "https://example.org/x.stl", "https://127.0.0.1/x.stl", "https://169.254.169.254/latest"} {
		if e := toolErr(t, cs, "import_from_url", map[string]any{"url": u}); e == "" {
			t.Errorf("%s accepté", u)
		}
	}
}
