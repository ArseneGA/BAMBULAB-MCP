package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/oauth"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

func TestOAuthCLIListAndRevoke(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "bambu.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	svc := oauth.New(st, oauth.Options{})
	if _, e := svc.Register(context.Background(), []byte(`{"client_name":"Claude","redirect_uris":["https://claude.ai/api/mcp/auth_callback"]}`), ""); e != nil {
		t.Fatal(e)
	}
	st.Close()

	run := func(args ...string) (int, string, string) {
		var out, errw bytes.Buffer
		code := oauthMain(append([]string{"--data-dir", dir}, args...), &out, &errw)
		return code, out.String(), errw.String()
	}
	if code, out, _ := run("--list"); code != 0 || !strings.Contains(out, "Aucune autorisation") {
		t.Errorf("list : %d %q", code, out)
	}
	if code, _, _ := run(); code != 2 {
		t.Errorf("sans option : %d, attendu 2", code)
	}
	if code, _, errw := run("--revoke", "g_inconnu"); code != 1 || !strings.Contains(errw, "aucune autorisation") {
		t.Errorf("revoke inconnu : %d %q", code, errw)
	}
	if code, out, _ := run("--revoke-all"); code != 0 || !strings.Contains(out, "0 autorisation") {
		t.Errorf("revoke-all : %d %q", code, out)
	}
}
