package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
)

func TestBackupRestoreVerifyCommands(t *testing.T) {
	ctx := context.Background()
	src := t.TempDir()
	c, err := core.Open(ctx, core.Config{DataDir: src, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Blobs.Put(strings.NewReader("un fichier"), ""); err != nil {
		t.Fatal(err)
	}
	// Le service tourne (verrou pris) : la sauvegarde fonctionne quand même, la restauration SUR lui est refusée.
	arch := filepath.Join(t.TempDir(), "s.tar.gz")
	var out, errb bytes.Buffer
	if rc := backupMain([]string{"--data-dir", src, "--out", arch}, &out, &errb); rc != 0 {
		t.Fatalf("backup : %d %s", rc, errb.String())
	}
	if !strings.Contains(out.String(), "1 blob(s)") {
		t.Fatalf("sortie : %s", out.String())
	}
	out.Reset()
	errb.Reset()
	if rc := restoreMain([]string{"--from", arch, "--data-dir", src, "--force"}, &out, &errb); rc == 0 || !strings.Contains(errb.String(), "déjà utilisé") {
		t.Fatalf("restauration sur un service en marche : rc=%d %s", rc, errb.String())
	}
	c.Close()

	// Dossier de sortie = dossier existant : nom daté.
	dir := t.TempDir()
	out.Reset()
	if rc := backupMain([]string{"--data-dir", src, "--out", dir, "--exclude-secrets"}, &out, &errb); rc != 0 {
		t.Fatalf("backup vers un dossier : %d %s", rc, errb.String())
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "atelier-*.tar.gz")); len(m) != 1 {
		t.Fatalf("archive datée absente : %v", m)
	}

	dst := filepath.Join(t.TempDir(), "neuf")
	out.Reset()
	errb.Reset()
	if rc := restoreMain([]string{"--from", arch, "--data-dir", dst}, &out, &errb); rc != 0 {
		t.Fatalf("restore : %d %s", rc, errb.String())
	}
	// Second restore dans le même dossier (maintenant non vide) : refus sans --force.
	if rc := restoreMain([]string{"--from", arch, "--data-dir", dst}, &out, &errb); rc == 0 {
		t.Fatal("dossier non vide accepté")
	}
	out.Reset()
	if rc := verifyMain([]string{"--data-dir", dst}, &out, &errb); rc != 0 || !strings.Contains(out.String(), "OK") {
		t.Fatalf("verify : %d %s %s", rc, out.String(), errb.String())
	}
	out.Reset()
	if rc := verifyMain([]string{"--archive", arch}, &out, &errb); rc != 0 {
		t.Fatalf("verify archive : %d %s", rc, out.String())
	}
	// Corruption de l'archive : code de sortie non nul.
	b, _ := os.ReadFile(arch)
	b[len(b)/2] ^= 0xFF
	os.WriteFile(arch, b, 0o600)
	errb.Reset()
	if rc := verifyMain([]string{"--archive", arch}, &out, &errb); rc == 0 {
		t.Fatal("archive corrompue non signalée")
	}
}

func TestStudioWrapperDefaults(t *testing.T) {
	found := func(string) (string, error) { return "/usr/bin/xvfb-run", nil }
	none := func(string) (string, error) { return "", os.ErrNotExist }
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if got := defaultStudioWrapper("linux", env(nil), found); len(got) != 2 || got[0] != "xvfb-run" || got[1] != "-a" {
		t.Errorf("service sans écran : %v", got)
	}
	if defaultStudioWrapper("linux", env(map[string]string{"DISPLAY": ":0"}), found) != nil {
		t.Error("avec écran : pas de wrapper")
	}
	if defaultStudioWrapper("linux", env(nil), none) != nil || defaultStudioWrapper("windows", env(nil), found) != nil {
		t.Error("sans xvfb-run ou hors Linux : pas de wrapper")
	}
	if got := parseWrapper("xvfb-run -a -s '-screen'"); len(got) != 4 {
		t.Errorf("parseWrapper : %v", got)
	}
	if parseWrapper("none") != nil || parseWrapper(" ") != nil {
		t.Error("none / vide")
	}
}
