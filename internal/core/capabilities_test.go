package core

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func quietCfg(t *testing.T) Config {
	return Config{DataDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// fakeStudio crée une « installation » : un fichier exécutable factice et un mini jeu de profils BBL.
func fakeStudio(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	exe := filepath.Join(root, "bambu-studio")
	if err := os.WriteFile(exe, []byte("pas un vrai exécutable"), 0o755); err != nil {
		t.Fatal(err)
	}
	bbl := filepath.Join(root, "resources", "profiles", "BBL")
	files := map[string]string{
		"machine/model.json": `{"name":"Bambu Lab P1S","type":"machine_model","nozzle_diameter":"0.4;0.6"}`,
		"machine/p1s.json":   `{"name":"Bambu Lab P1S 0.4 nozzle","type":"machine","instantiation":"true","printer_model":"Bambu Lab P1S"}`,
		"process/p.json":     `{"name":"0.20mm Standard @BBL P1S","type":"process","instantiation":"true"}`,
		"filament/pla.json":  `{"name":"Bambu PLA Basic @BBL P1S","type":"filament","instantiation":"true"}`,
		"filament/petg.json": `{"name":"Bambu PETG Basic @BBL P1S","type":"filament","instantiation":"true"}`,
		"filament/base.json": `{"name":"fdm_filament_common","type":"filament","instantiation":"false"}`,
	}
	for rel, body := range files {
		p := filepath.Join(bbl, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return exe
}

func TestCapabilitiesReportsStudioAndProfileCounts(t *testing.T) {
	cfg := quietCfg(t)
	cfg.StudioPath = fakeStudio(t)
	c, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	first := c.Capabilities().Studio
	if first == nil || !first.Available || first.Path != cfg.StudioPath || first.ProfilesDir == "" {
		t.Fatalf("Studio : %+v", first)
	}
	var got *struct{ m, ma, p, f int }
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline) && got == nil; time.Sleep(20 * time.Millisecond) {
		if s := c.Capabilities().Studio; s != nil && s.Profiles != nil {
			got = &struct{ m, ma, p, f int }{s.Profiles.Models, s.Profiles.Machines, s.Profiles.Processes, s.Profiles.Filaments}
		}
	}
	if got == nil {
		t.Fatalf("profils jamais comptés : %+v", c.Capabilities().Studio)
	}
	if got.m != 1 || got.ma != 1 || got.p != 1 || got.f != 2 {
		t.Errorf("comptes (modèles, machines, process, filaments) = %+v, attendu 1/1/1/2 (la base non instanciable ne compte pas)", *got)
	}
}

// BAMBU_STUDIO_BIN posée mais invalide : erreur explicite, aucun repli silencieux.
func TestInvalidStudioEnvIsAnErrorNotAFallback(t *testing.T) {
	t.Setenv("BAMBU_STUDIO_BIN", filepath.Join(t.TempDir(), "absent", "bambu-studio"))
	c, err := Open(context.Background(), quietCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.StudioErr == nil || !strings.Contains(c.StudioErr.Error(), "introuvable") {
		t.Fatalf("StudioErr = %v", c.StudioErr)
	}
	if s := c.Capabilities().Studio; s == nil || s.Available || !strings.Contains(s.Note, "introuvable") {
		t.Fatalf("Studio : %+v", s)
	}
}

// inspect_environment lit ce que le service utilise (BAMBU_STUDIO_BIN, BAMBU_UV_PATH…).
func TestEnvironmentUsesRealConfiguration(t *testing.T) {
	cfg := quietCfg(t)
	cfg.StudioPath = fakeStudio(t)
	c, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	rep, err := c.Environment(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var studio, uv bool
	for _, e := range rep.Executables {
		switch e.Name {
		case "bambu-studio":
			studio = e.Found && e.Path == cfg.StudioPath
		case "uv":
			uv = true
		}
	}
	if !studio || !uv {
		t.Fatalf("rapport : %+v", rep.Executables)
	}
}
