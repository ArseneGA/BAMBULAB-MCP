package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
)

type env struct {
	t    *testing.T
	c    *core.Core
	srv  *httptest.Server
	root string
}

func setup(t *testing.T) *env {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c, err := core.Open(ctx, core.Config{
		DataDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		PollInterval: 10 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond,
		Catalog: catalog.Config{ImportRoots: []string{root}, UVPath: filepath.Join(root, "absent-uv")},
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	srv := httptest.NewServer(httpapi.NewHandler(c))
	c.Catalog.SetBaseURL(srv.URL)
	t.Cleanup(func() { srv.Close(); cancel(); <-done; c.Close() })
	return &env{t: t, c: c, srv: srv, root: root}
}

func (e *env) do(method, path string, body any, out any) int {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		raw, _ := io.ReadAll(resp.Body)
		if err := json.Unmarshal(raw, out); err != nil {
			e.t.Fatalf("%s %s : %v : %s", method, path, err, raw)
		}
	}
	return resp.StatusCode
}

func data(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "mesh", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (e *env) write(rel string, b []byte) {
	e.t.Helper()
	p := filepath.Join(e.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func TestCatalogAPI(t *testing.T) {
	e := setup(t)
	e.write("lib/a.stl", data(t, "cube10_ascii.stl"))
	e.write("lib/sous/b.stl", data(t, "cylinder_binary.stl"))
	e.write("lib/sous/c.obj", data(t, "cube10_quads.obj"))

	// scan : lecture seule.
	var sc map[string]any
	if code := e.do("POST", "/api/scan", map[string]any{"path": filepath.Join(e.root, "lib")}, &sc); code != 200 || sc["total_files"].(float64) != 3 || sc["read_only"] != true {
		t.Fatalf("scan : %d %v", code, sc)
	}
	// import avec attente.
	var imp map[string]any
	if code := e.do("POST", "/api/import", map[string]any{"paths": []string{filepath.Join(e.root, "lib")}, "wait_seconds": 30}, &imp); code != 200 || imp["done"] != true {
		t.Fatalf("import : %d %v", code, imp)
	}
	// import asynchrone : 202 + job.
	var async map[string]any
	if code := e.do("POST", "/api/import", map[string]any{"paths": []string{filepath.Join(e.root, "lib")}}, &async); code != 202 || async["job_id"] == nil {
		t.Fatalf("import async : %d %v", code, async)
	}
	var job map[string]any
	for i := 0; i < 200; i++ {
		e.do("GET", "/api/jobs/"+async["job_id"].(string), nil, &job)
		if job["state"] == "succeeded" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if job["state"] != "succeeded" {
		t.Fatalf("job : %v", job)
	}
	if !e.c.Catalog.Idle(context.Background(), 30*time.Second) {
		t.Fatal("travaux dérivés non terminés")
	}

	// /api/models : pagination et filtres.
	var page map[string]any
	if code := e.do("GET", "/api/models?limit=2", nil, &page); code != 200 || page["total"].(float64) != 3 || len(page["models"].([]any)) != 2 || page["has_more"] != true {
		t.Fatalf("page : %d %v", code, page)
	}
	var f map[string]any
	e.do("GET", "/api/models?format=obj&q=c", nil, &f)
	if f["total"].(float64) != 1 {
		t.Fatalf("filtre : %v", f)
	}
	if code := e.do("GET", "/api/models?format=zzz", nil, &f); code != 400 || f["code"] != "invalid" {
		t.Fatalf("filtre invalide : %d %v", code, f)
	}
	var cols map[string]any
	if code := e.do("GET", "/api/collections", nil, &cols); code != 200 || len(cols["collections"].([]any)) != 2 {
		t.Fatalf("collections : %d %v", code, cols)
	}
	var lc map[string]any
	if code := e.do("GET", "/api/collections/lib?recursive=1", nil, &lc); code != 200 || lc["models"].(map[string]any)["total"].(float64) != 3 {
		t.Fatalf("collection : %d %v", code, lc)
	}
	if code := e.do("GET", "/api/collections/inconnue", nil, &lc); code != 404 {
		t.Fatalf("collection inconnue : %d", code)
	}

	// /api/models/{id} + aperçu GLB (streaming, ETag, signature).
	first := page["models"].([]any)[0].(map[string]any)
	var det map[string]any
	if code := e.do("GET", "/api/models/"+first["id"].(string), nil, &det); code != 200 || det["preview"].(map[string]any)["state"] != "ready" {
		t.Fatalf("détails : %d %v", code, det)
	}
	if code := e.do("GET", "/api/models/mdl_inconnu", nil, &f); code != 404 {
		t.Fatalf("modèle inconnu : %d", code)
	}
	url := det["preview"].(map[string]any)["url"].(string)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	glb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	etag := resp.Header.Get("ETag")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "model/gltf-binary" || etag == "" || !bytes.HasPrefix(glb, []byte("glTF")) {
		t.Fatalf("GLB : %d %q %q", resp.StatusCode, resp.Header.Get("Content-Type"), etag)
	}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("If-None-Match", etag)
	r304, _ := http.DefaultClient.Do(req)
	r304.Body.Close()
	if r304.StatusCode != http.StatusNotModified {
		t.Fatalf("ETag : %d", r304.StatusCode)
	}
	req, _ = http.NewRequest("GET", url, nil)
	req.Header.Set("Range", "bytes=0-3")
	rr, _ := http.DefaultClient.Do(req)
	part, _ := io.ReadAll(rr.Body)
	rr.Body.Close()
	if rr.StatusCode != http.StatusPartialContent || string(part) != "glTF" {
		t.Fatalf("Range : %d %q", rr.StatusCode, part)
	}
	// Signature absente / falsifiée / expirée : refus. Original jamais servi.
	sha := det["preview"].(map[string]any)["sha256"].(string)
	for _, bad := range []string{"/api/assets/" + sha + ".glb", "/api/assets/" + sha + ".glb?exp=1&sig=00", strings.Replace(url, "sig=", "sig=0", 1)} {
		var out map[string]any
		path := strings.TrimPrefix(bad, e.srv.URL)
		if code := e.do("GET", path, nil, &out); code != 403 {
			t.Fatalf("%s : %d", path, code)
		}
	}
	origSHA := det["files"].([]any)[0].(map[string]any)["sha256"].(string)
	signed, _ := e.c.Catalog.SignedAssetURL(origSHA) // signature valide mais blob non enregistré comme aperçu
	var out map[string]any
	if code := e.do("GET", strings.TrimPrefix(signed, e.srv.URL), nil, &out); code != 404 {
		t.Fatalf("un original ne doit jamais être servi : %d", code)
	}
	if code := e.do("GET", "/api/assets/"+sha+".bin", nil, &out); code != 404 {
		t.Fatalf("extension : %d", code)
	}

	// opérations : aperçu puis application.
	id := first["id"].(string)
	var pv map[string]any
	if code := e.do("POST", "/api/operations/preview", map[string]any{"operations": []map[string]any{
		{"op": "rename", "model_ids": []string{id}, "new_name": "Renommé"}, {"op": "delete", "model_ids": []string{id}}}}, &pv); code != 200 || pv["token"] == nil {
		t.Fatalf("preview : %d %v", code, pv)
	}
	if code := e.do("POST", "/api/operations/apply", map[string]any{"token": "x"}, &out); code != 400 {
		t.Fatalf("apply sans vrai jeton : %d", code)
	}
	var ap map[string]any
	if code := e.do("POST", "/api/operations/apply", map[string]any{"token": pv["token"]}, &ap); code != 200 || ap["applied"].(float64) != 2 {
		t.Fatalf("apply : %d %v", code, ap)
	}
	if code := e.do("POST", "/api/operations/apply", map[string]any{"token": pv["token"]}, &out); code != 409 {
		t.Fatalf("second apply : %d", code)
	}
	// corbeille.
	var tr map[string]any
	if code := e.do("GET", "/api/trash", nil, &tr); code != 200 || tr["total"].(float64) != 1 {
		t.Fatalf("trash : %d %v", code, tr)
	}
	var rs map[string]any
	if code := e.do("POST", "/api/trash/restore", map[string]any{"model_ids": []string{id}}, &rs); code != 200 || len(rs["restored"].([]any)) != 1 {
		t.Fatalf("restore : %d %v", code, rs)
	}
	e.do("GET", "/api/trash", nil, &tr)
	if tr["total"].(float64) != 0 {
		t.Fatalf("corbeille non vidée : %v", tr)
	}
	// étiquettes + doublons.
	var tg map[string]any
	if code := e.do("POST", "/api/tags", map[string]any{"model_ids": []string{id}, "add": []string{"x"}}, &tg); code != 200 || len(tg["updated"].([]any)) != 1 {
		t.Fatalf("tags : %d %v", code, tg)
	}
	var dups map[string]any
	if code := e.do("GET", "/api/duplicates", nil, &dups); code != 200 || dups["total_groups"].(float64) != 0 {
		t.Fatalf("doublons : %d %v", code, dups)
	}
}

func TestCatalogAPIRefusals(t *testing.T) {
	e := setup(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "x.stl"), data(t, "cube10_ascii.stl"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if code := e.do("POST", "/api/import", map[string]any{"paths": []string{outside}}, &out); code != 403 || out["code"] != "not_allowed" {
		t.Fatalf("import hors racine : %d %v", code, out)
	}
	if code := e.do("POST", "/api/scan", map[string]any{"path": outside}, &out); code != 403 {
		t.Fatalf("scan hors racine : %d", code)
	}
	if code := e.do("POST", "/api/scan", map[string]any{"path": filepath.Join(e.root, "n-existe-pas")}, &out); code != 404 {
		t.Fatalf("scan inexistant : %d", code)
	}
	if code := e.do("POST", "/api/import", map[string]any{"paths": []string{e.root}, "deplacer": true}, &out); code != 400 {
		t.Fatalf("champ inconnu : %d", code)
	}
	if code := e.do("POST", "/api/import", map[string]any{"paths": []string{}}, &out); code != 400 {
		t.Fatalf("sans chemin : %d", code)
	}
	var page map[string]any
	e.do("GET", "/api/models", nil, &page)
	if page["total"].(float64) != 0 {
		t.Fatal("rien ne devait être importé")
	}
	var cp map[string]any
	e.do("GET", "/api/capabilities", nil, &cp)
	feats := map[string]bool{}
	for _, f := range cp["features"].([]any) {
		m := f.(map[string]any)
		feats[m["id"].(string)] = m["available"].(bool)
	}
	if !feats["mesh_import"] || !feats["catalog_import"] || feats["cad_worker"] {
		t.Fatalf("capacités : %v", feats)
	}
}
