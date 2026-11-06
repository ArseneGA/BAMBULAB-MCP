package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveTokenFromFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "api-token.txt")
	os.WriteFile(f, []byte("\xef\xbb\xbf  jeton-secret-123  \r\nautre ligne ignorée\n"), 0o600) // BOM + CRLF (PowerShell 5.1)
	if got, err := resolveToken("", f); err != nil || got != "jeton-secret-123" {
		t.Fatalf("lecture du fichier : %q %v", got, err)
	}
	if got, _ := resolveToken("direct", f); got != "direct" {
		t.Fatalf("le jeton direct doit l'emporter : %q", got)
	}
	if got, err := resolveToken("", ""); got != "" || err != nil {
		t.Fatalf("sans jeton : %q %v", got, err)
	}
	if _, err := resolveToken("", filepath.Join(dir, "absent")); err == nil {
		t.Fatal("fichier absent accepté")
	}
	empty := filepath.Join(dir, "vide")
	os.WriteFile(empty, []byte(" \n"), 0o600)
	if _, err := resolveToken("", empty); err == nil {
		t.Fatal("fichier vide accepté")
	}
}

// Le jeton lu dans le fichier est bien envoyé en Bearer, et n'apparaît jamais sur stderr.
func TestTokenFileIsSentAsBearerAndNotLogged(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		http.Error(w, "refus", http.StatusUnauthorized)
	}))
	defer srv.Close()
	f := filepath.Join(t.TempDir(), "tok")
	os.WriteFile(f, []byte("tok-xyz-987\n"), 0o600)
	var stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rc := run(ctx, []string{"--url", srv.URL + "/mcp", "--api-token-file", f, "--connect-attempts", "1"}, io.NopCloser(strings.NewReader("")), nopWriteCloser{io.Discard}, &stderr)
	if rc == 0 {
		t.Fatal("la connexion refusée devrait échouer")
	}
	if got != "Bearer tok-xyz-987" {
		t.Fatalf("en-tête reçu : %q", got)
	}
	if strings.Contains(stderr.String(), "tok-xyz-987") {
		t.Fatalf("le jeton apparaît dans les logs : %s", stderr.String())
	}
	// Fichier de jeton absent : arrêt net, code 2.
	if rc := run(ctx, []string{"--api-token-file", filepath.Join(t.TempDir(), "absent")}, io.NopCloser(strings.NewReader("")), nopWriteCloser{io.Discard}, &stderr); rc != 2 {
		t.Fatalf("fichier absent : code %d", rc)
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
