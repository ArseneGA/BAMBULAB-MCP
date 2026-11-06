package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestSeedDemoCommand(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	if code := runSeedDemo([]string{"--data-dir", dir, "--no-cad", "--wait", "30s"}, &out, &errb); code != 0 {
		t.Fatalf("code %d : %s", code, errb.String())
	}
	var rep map[string]any
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("%v : %s", err, out.String())
	}
	if rep["models_in_catalog"].(float64) != 10 || rep["derived_done"] != true {
		t.Fatalf("%v", rep)
	}
	if !strings.HasPrefix(rep["dir"].(string), dir) && !strings.Contains(rep["dir"].(string), filepath.Base(dir)) {
		t.Fatalf("dossier de génération hors du dossier de données : %v", rep["dir"])
	}
	// Deuxième exécution : idempotente.
	out.Reset()
	if code := runSeedDemo([]string{"--data-dir", dir, "--no-cad"}, &out, &errb); code != 0 {
		t.Fatalf("2e exécution : %s", errb.String())
	}
	_ = json.Unmarshal(out.Bytes(), &rep)
	if rep["models_in_catalog"].(float64) != 10 || rep["generated_files"].(float64) != 0 {
		t.Fatalf("pas idempotent : %v", rep)
	}
}

func TestCatalogConfigParsesRoots(t *testing.T) {
	sep := string(filepath.ListSeparator)
	cfg := catalogConfig(" /a "+sep+sep+"/b", "uv.exe", "w")
	if len(cfg.ImportRoots) != 2 || cfg.ImportRoots[0] != "/a" || cfg.UVPath != "uv.exe" || cfg.WorkerDir != "w" {
		t.Fatalf("%+v", cfg)
	}
}
