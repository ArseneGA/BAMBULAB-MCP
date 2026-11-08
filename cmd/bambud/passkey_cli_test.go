package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth/passkeytest"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

func TestPasskeyCommandListRequireRevoke(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "bambu.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pk := humanauth.PasskeysFor(st)
	run := func(args ...string) (int, string) {
		var out, errb bytes.Buffer
		rc := passkeyMain(append([]string{"--data-dir", dir}, args...), &out, &errb)
		return rc, out.String() + errb.String()
	}
	if rc, out := run(); rc != 0 || !strings.Contains(out, "facultative") || !strings.Contains(out, "0 active") || !strings.Contains(out, "/setup/passkeys") {
		t.Fatalf("sans passkey : %d %s", rc, out)
	}
	// Exiger sans passkey : refusé (sinon plus aucune approbation possible).
	if rc, out := run("--require=on"); rc == 0 || !strings.Contains(out, "aucune passkey") {
		t.Fatalf("exiger sans passkey : %d %s", rc, out)
	}
	// Une passkey enregistrée (par la cérémonie, comme la page).
	req := httptest.NewRequest("GET", "http://localhost:7878/x", nil)
	req.AddCookie(&http.Cookie{Name: humanauth.CookieName, Value: "tok"})
	opts, err := pk.BeginRegistration(ctx, req, humanauth.BrowserKey(req))
	if err != nil {
		t.Fatal(err)
	}
	a := passkeytest.Must(passkeytest.ES256)
	cred, _ := a.Create(opts, "http://localhost:7878")
	info, err := pk.FinishRegistration(ctx, req, humanauth.BrowserKey(req), "ma clé", "human:test", cred)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	if rc, out := run(); rc != 0 || !strings.Contains(out, info.ID) || !strings.Contains(out, "1 active") {
		t.Fatalf("liste : %d %s", rc, out)
	}
	if rc, out := run("--require=on"); rc != 0 || !strings.Contains(out, "exigée") {
		t.Fatalf("exiger : %d %s", rc, out)
	}
	if rc, out := run("--revoke", info.ID); rc == 0 || !strings.Contains(out, "dernière passkey") {
		t.Fatalf("révoquer la dernière passkey exigée : %d %s", rc, out)
	}
	if rc, out := run("--require=off"); rc != 0 || !strings.Contains(out, "facultative") {
		t.Fatalf("secours --require=off : %d %s", rc, out)
	}
	if rc, out := run("--revoke", info.ID); rc != 0 || !strings.Contains(out, "révoquée") || !strings.Contains(out, "0 active") {
		t.Fatalf("révocation : %d %s", rc, out)
	}
	if rc, _ := run("--require=maybe"); rc != 2 {
		t.Fatalf("valeur invalide : %d", rc)
	}
}
