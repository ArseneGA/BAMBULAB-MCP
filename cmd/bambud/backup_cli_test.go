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

// Le wrapper xvfb n'est plus activé implicitement : seul un réglage explicite compte.
func TestStudioWrapperExplicitOnly(t *testing.T) {
	if got := parseWrapper(""); got != nil {
		t.Errorf("sans réglage : aucun préfixe attendu, obtenu %v", got)
	}
	if got := parseWrapper("xvfb-run -a -s '-screen'"); len(got) != 4 {
		t.Errorf("parseWrapper : %v", got)
	}
	for _, v := range []string{"none", "NONE", "-", " "} {
		if parseWrapper(v) != nil {
			t.Errorf("%q devrait désactiver le préfixe", v)
		}
	}
}

func TestResolveStudioPath(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "bambu-studio")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if p, err := resolveStudioPath("", ""); err != nil || p != "" {
		t.Errorf("rien de demandé : %q %v", p, err)
	}
	if p, err := resolveStudioPath("", exe); err != nil || p != exe {
		t.Errorf("BAMBU_STUDIO_BIN valide : %q %v", p, err)
	}
	if p, err := resolveStudioPath(exe, "/absent"); err != nil || p != exe {
		t.Errorf("--studio-bin l'emporte : %q %v", p, err)
	}
	for name, args := range map[string][2]string{
		"env introuvable":  {"", filepath.Join(dir, "absent")},
		"env dossier":      {"", dir},
		"flag introuvable": {filepath.Join(dir, "absent"), exe},
	} {
		_, err := resolveStudioPath(args[0], args[1])
		if err == nil || !strings.Contains(err.Error(), "introuvable") {
			t.Errorf("%s : erreur claire attendue, obtenu %v", name, err)
		}
	}
}
