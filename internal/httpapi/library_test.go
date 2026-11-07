package httpapi_test

import (
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

const libSpecs = `{"templates":[{"id":"washer","version":"1.0.0","schema_hash":"h","title":"Rondelle","category":"rondelle-entretoise","summary":"s","description":"d",
 "params":[{"name":"outer_diameter","type":"number","default":18,"min":3,"max":200,"unit":"mm","description":"x"},
           {"name":"inner_diameter","type":"number","default":6.6,"min":1,"max":150,"unit":"mm","description":"x","auto":"hole:vertical"}]}]}`

// libWorker : worker géométrie simulé (liste, construction, vignette, calibration).
func libWorker(_ context.Context, cmd string, req map[string]any) (json.RawMessage, error) {
	write := func(out, name string, exts ...string) {
		for _, e := range exts {
			_ = os.WriteFile(filepath.Join(out, name+e), []byte("x"+name+e), 0o644)
		}
	}
	switch cmd {
	case "library_list":
		return json.RawMessage(libSpecs), nil
	case "library_thumbnail":
		return json.RawMessage(`{"svg":"<svg xmlns=\"http://www.w3.org/2000/svg\"></svg>"}`), nil
	case "library_build":
		out, name := req["out_dir"].(string), req["name"].(string)
		write(out, name, ".stl", ".step", ".thumb.svg")
		b, _ := json.Marshal(map[string]any{"template": "washer", "template_version": "1.0.0", "params": req["params"],
			"parts": []any{}, "assumptions": []string{"h"}, "warnings": []string{}, "level": "calculé",
			"files": map[string]string{"stl": filepath.Join(out, name+".stl"), "step": filepath.Join(out, name+".step"), "thumbnail": filepath.Join(out, name+".thumb.svg")}})
		return b, nil
	case "calibration_set":
		out, prefix := req["out_dir"].(string), req["name_prefix"].(string)
		write(out, prefix+"_cube", ".stl", ".step")
		b, _ := json.Marshal(map[string]any{"pieces": []any{map[string]any{"kind": "shrink_cube", "name": prefix + "_cube", "description": "cube", "measure": []string{"cube_mm"},
			"files": map[string]string{"stl": filepath.Join(out, prefix+"_cube.stl"), "step": filepath.Join(out, prefix+"_cube.step")}, "parts": []any{}, "bbox_mm": map[string]any{}, "info": map[string]any{}, "warnings": []string{}}}})
		return b, nil
	}
	return nil, nil
}

func libEnv(t *testing.T) *env {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c, err := core.Open(ctx, core.Config{DataDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		PollInterval: 10 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond,
		Catalog: catalog.Config{ImportRoots: []string{root}, UVPath: filepath.Join(root, "absent-uv"), Worker: libWorker}})
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

func TestLibraryRoutes(t *testing.T) {
	e := libEnv(t)
	var list struct {
		Templates []map[string]any `json:"templates"`
	}
	if code := e.do("GET", "/api/library/templates", nil, &list); code != 200 || len(list.Templates) != 1 || list.Templates[0]["id"] != "washer" {
		t.Fatalf("liste : %d %v", code, list)
	}
	var one map[string]any
	if code := e.do("GET", "/api/library/templates/washer", nil, &one); code != 200 || one["version"] != "1.0.0" {
		t.Fatalf("fiche : %d %v", code, one)
	}
	var bad map[string]any
	if code := e.do("GET", "/api/library/templates/nope", nil, &bad); code != 400 || bad["code"] != "invalid" {
		t.Fatalf("inconnu : %d %v", code, bad)
	}
	// vignette : SVG avec CSP restrictive
	resp, err := http.Get(e.srv.URL + "/api/library/templates/washer/thumbnail.svg")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/svg+xml; charset=utf-8" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'none'") || !strings.HasPrefix(string(body), "<svg") {
		t.Fatalf("vignette : %d %v %q", resp.StatusCode, resp.Header, body)
	}
	// création, vignette du modèle, variante
	var created map[string]any
	if code := e.do("POST", "/api/library/create", map[string]any{"template": "washer", "name": "rond", "params": map[string]any{"inner_diameter": 8}}, &created); code != 201 {
		t.Fatalf("create : %d %v", code, created)
	}
	if created["params"].(map[string]any)["inner_diameter"].(float64) <= 8 || created["calibration"].(map[string]any)["applied"] != false {
		t.Fatalf("compensation attendue : %v", created)
	}
	mid := created["model_id"].(string)
	r2, _ := http.Get(e.srv.URL + "/api/library/models/" + mid + "/thumbnail.svg")
	r2.Body.Close()
	if r2.StatusCode != 200 {
		t.Fatalf("vignette modèle : %d", r2.StatusCode)
	}
	if r3, _ := http.Get(e.srv.URL + "/api/library/models/mdl_inconnu/thumbnail.svg"); r3.StatusCode != 404 {
		t.Fatalf("vignette inconnue : %d", r3.StatusCode)
	}
	var variant map[string]any
	if code := e.do("POST", "/api/library/variants", map[string]any{"model_id": mid, "name": "Ma rondelle"}, &variant); code != 201 || variant["variant"].(map[string]any)["id"] != "ma-rondelle" {
		t.Fatalf("variante : %d %v", code, variant)
	}
	if code := e.do("POST", "/api/library/variants", map[string]any{"model_id": mid, "name": "Ma rondelle"}, &variant); code != 409 {
		t.Fatalf("doublon : %d", code)
	}
	if code := e.do("POST", "/api/library/create", map[string]any{"template": "washer", "inconnu": 1}, &bad); code != 400 {
		t.Fatalf("champ inconnu : %d", code)
	}
}

func TestCalibrationRoutes(t *testing.T) {
	e := libEnv(t)
	var gen map[string]any
	if code := e.do("POST", "/api/calibration/sets", map[string]any{"nozzle_mm": 0.4, "material": "PLA", "printer": "P1S"}, &gen); code != 201 || len(gen["pieces"].([]any)) != 1 {
		t.Fatalf("set : %d %v", code, gen)
	}
	rec := map[string]any{"nozzle_mm": 0.4, "material": "PLA", "printer": "P1S", "set_id": gen["set_id"], "measures": map[string]any{"fit_slides_gap_mm": 0.3}}
	var out map[string]any
	if code := e.do("POST", "/api/calibration/records", rec, &out); code != 201 || out["record_id"] == nil {
		t.Fatalf("record : %d %v", code, out)
	}
	rec["measures"] = map[string]any{"fit_slides_gap_mm": 9}
	if code := e.do("POST", "/api/calibration/records", rec, &out); code != 400 {
		t.Fatalf("mesure implausible : %d", code)
	}
	var got map[string]any
	if code := e.do("GET", "/api/calibration?printer=p1s&nozzle=0.4&material=PLA", nil, &got); code != 200 {
		t.Fatalf("get : %d", code)
	}
	cal := got["resolution"].(map[string]any)["calibration"].(map[string]any)
	if cal["slides_gap_mm"].(float64) != 0.3 || len(got["history"].([]any)) != 1 {
		t.Fatalf("%v", got)
	}
	// la calibration est utilisée par la bibliothèque
	var created map[string]any
	e.do("POST", "/api/library/create", map[string]any{"template": "washer", "printer": "P1S", "params": map[string]any{"inner_diameter": 8}}, &created)
	if created["calibration"].(map[string]any)["applied"] != true {
		t.Fatalf("calibration non utilisée : %v", created["calibration"])
	}
	if code := e.do("GET", "/api/calibration?nozzle=9&material=PLA", nil, &got); code != 400 {
		t.Fatalf("buse invalide : %d", code)
	}
}
