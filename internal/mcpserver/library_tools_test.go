package mcpserver_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

const mcpSpecs = `{"templates":[{"id":"washer","version":"1.0.0","schema_hash":"h","title":"Rondelle","category":"rondelle-entretoise","summary":"Rondelle plate","description":"d",
 "params":[{"name":"outer_diameter","type":"number","default":18,"min":3,"max":200,"unit":"mm","description":"x"},
           {"name":"inner_diameter","type":"number","default":6.6,"min":1,"max":150,"unit":"mm","description":"x","auto":"hole:vertical"}]}]}`

func mcpLibWorker(_ context.Context, cmd string, req map[string]any) (json.RawMessage, error) {
	switch cmd {
	case "library_list":
		return json.RawMessage(mcpSpecs), nil
	case "library_build":
		out, name := req["out_dir"].(string), req["name"].(string)
		for _, e := range []string{".stl", ".step"} {
			_ = os.WriteFile(filepath.Join(out, name+e), []byte("x"+name+e), 0o644)
		}
		b, _ := json.Marshal(map[string]any{"template": "washer", "template_version": "1.0.0", "params": req["params"], "parts": []any{},
			"assumptions": []string{"h"}, "warnings": []string{}, "level": "calculé",
			"files": map[string]string{"stl": filepath.Join(out, name+".stl"), "step": filepath.Join(out, name+".step")}})
		return b, nil
	case "calibration_set":
		out, prefix := req["out_dir"].(string), req["name_prefix"].(string)
		for _, e := range []string{".stl", ".step"} {
			_ = os.WriteFile(filepath.Join(out, prefix+"_c"+e), []byte("x"), 0o644)
		}
		b, _ := json.Marshal(map[string]any{"pieces": []any{map[string]any{"kind": "shrink_cube", "name": prefix + "_c", "description": "cube", "measure": []string{"cube_mm : X, Y, Z"},
			"files": map[string]string{"stl": filepath.Join(out, prefix+"_c.stl"), "step": filepath.Join(out, prefix+"_c.step")}, "parts": []any{}, "bbox_mm": map[string]any{}, "info": map[string]any{}, "warnings": []string{}}}})
		return b, nil
	}
	return nil, nil
}

func TestLibraryAndCalibrationTools(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	root, _ := filepath.EvalSymlinks(t.TempDir())
	c, err := core.Open(ctx, core.Config{DataDir: t.TempDir(), Logger: quietLog(), PollInterval: 10 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond,
		Catalog: catalog.Config{ImportRoots: []string{root}, UVPath: filepath.Join(root, "absent-uv"), Worker: mcpLibWorker}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done; c.Close() })
	cs := connectClient(t, mcpserver.New(c))

	lt, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range lt.Tools {
		names = append(names, tl.Name)
	}
	for _, w := range []string{"list_part_templates", "describe_part_template", "create_from_template", "save_as_template", "generate_calibration_set", "record_calibration", "get_calibration"} {
		if !slices.Contains(names, w) {
			t.Fatalf("outil manquant %s : %v", w, names)
		}
	}

	l := structured(t, call(t, cs, "list_part_templates", map[string]any{}))
	if ts := l["templates"].([]any); len(ts) != 1 || ts[0].(map[string]any)["id"] != "washer" {
		t.Fatalf("liste : %v", l)
	}
	d := structured(t, call(t, cs, "describe_part_template", map[string]any{"template": "washer"}))
	if d["params"].([]any)[1].(map[string]any)["auto"] != "hole:vertical" {
		t.Fatalf("fiche : %v", d)
	}
	if r := call(t, cs, "describe_part_template", map[string]any{"template": "zzz"}); !r.IsError {
		t.Fatal("gabarit inconnu accepté")
	}

	// 1. sans calibration : valeurs génériques
	a := structured(t, call(t, cs, "create_from_template", map[string]any{"template": "washer", "name": "w1", "printer": "P1S", "params": map[string]any{"inner_diameter": 8}}))
	if a["calibration"].(map[string]any)["applied"] != false || a["params"].(map[string]any)["inner_diameter"].(float64) <= 8 {
		t.Fatalf("création générique : %v", a)
	}
	genericID := a["params"].(map[string]any)["inner_diameter"].(float64)

	// 2. génération + mesures + calibration prioritaire
	g := structured(t, call(t, cs, "generate_calibration_set", map[string]any{"nozzle_mm": 0.4, "material": "PLA", "printer": "P1S"}))
	if len(g["pieces"].([]any)) != 1 || g["set_id"] == nil || g["pieces"].([]any)[0].(map[string]any)["model_id"] == "" {
		t.Fatalf("série : %v", g)
	}
	rec := structured(t, call(t, cs, "record_calibration", map[string]any{"nozzle_mm": 0.4, "material": "PLA", "printer": "P1S", "set_id": g["set_id"],
		"measures": map[string]any{"holes": []any{map[string]any{"nominal": 4, "measured": 3.8}, map[string]any{"nominal": 6, "measured": 5.8}, map[string]any{"nominal": 8, "measured": 7.8}}}}))
	if !strings.Contains(rec["active"].(map[string]any)["provenance"].(string), "calibré le") {
		t.Fatalf("calibration : %v", rec)
	}
	if r := call(t, cs, "record_calibration", map[string]any{"nozzle_mm": 0.4, "material": "PLA", "measures": map[string]any{"fit_slides_gap_mm": 7}}); !r.IsError {
		t.Fatal("mesure implausible acceptée")
	}
	gc := structured(t, call(t, cs, "get_calibration", map[string]any{"nozzle_mm": 0.4, "material": "PLA", "printer": "P1S"}))
	if gc["calibration"] == nil || len(gc["history"].([]any)) != 1 {
		t.Fatalf("get_calibration : %v", gc)
	}
	b := structured(t, call(t, cs, "create_from_template", map[string]any{"template": "washer", "name": "w2", "printer": "P1S", "params": map[string]any{"inner_diameter": 8}}))
	if b["calibration"].(map[string]any)["applied"] != true || b["params"].(map[string]any)["inner_diameter"].(float64) != 8.2 || b["params"].(map[string]any)["inner_diameter"].(float64) == genericID {
		t.Fatalf("calibration prioritaire : %v", b)
	}

	// 3. variante
	v := structured(t, call(t, cs, "save_as_template", map[string]any{"model_id": b["model_id"], "name": "Rondelle P1S"}))
	if v["variant"].(map[string]any)["id"] != "rondelle-p1s" {
		t.Fatalf("variante : %v", v)
	}
	if r := call(t, cs, "save_as_template", map[string]any{"model_id": b["model_id"], "name": "Rondelle P1S"}); !r.IsError {
		t.Fatal("doublon accepté")
	}
}
