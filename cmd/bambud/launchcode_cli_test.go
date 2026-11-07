package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

func TestLaunchCodeCommandStatusUnlockReset(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "bambu.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	lc := humanauth.LaunchCodesFor(st)
	lc.SetMinDelay(0)
	run := func(args ...string) (int, string) {
		var out, errb bytes.Buffer
		rc := launchCodeMain(append([]string{"--data-dir", dir}, args...), &out, &errb)
		return rc, out.String() + errb.String()
	}
	if rc, out := run(); rc != 0 || !strings.Contains(out, "Aucun code") || !strings.Contains(out, "/setup/launch-code") {
		t.Fatalf("sans code : %d %s", rc, out)
	}
	if err := lc.Set(ctx, "482916", "human:test"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_ = lc.Verify(ctx, "111222", nil)
	}
	if rc, out := run(); rc != 0 || !strings.Contains(out, "VERROUILLÉ") || strings.Contains(out, "482916") {
		t.Fatalf("verrouillé : %d %s", rc, out)
	}
	// --unlock : garde le code
	if rc, out := run("--unlock"); rc != 0 || !strings.Contains(out, "levé") {
		t.Fatalf("unlock : %d %s", rc, out)
	}
	if err := lc.Verify(ctx, "482916", nil); err != nil {
		t.Fatalf("le code doit rester valable après --unlock : %v", err)
	}
	// --reset : supprime le code
	if rc, out := run("--reset"); rc != 0 || !strings.Contains(out, "supprimé") {
		t.Fatalf("reset : %d %s", rc, out)
	}
	if s, _ := lc.Status(ctx); s.Set {
		t.Fatal("code encore défini après --reset")
	}
	if rc, _ := run("--reset", "--unlock"); rc != 2 {
		t.Fatalf("options incompatibles : %d", rc)
	}
	st.Close()
}
