package main

import (
	"bytes"
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

func TestPublicBaseNeverUnspecified(t *testing.T) {
	for in, want := range map[string]string{
		"0.0.0.0:7878": "http://127.0.0.1:7878", "[::]:7878": "http://127.0.0.1:7878", ":7878": "http://127.0.0.1:7878",
		"127.0.0.1:9": "http://127.0.0.1:9", "192.168.1.5:7878": "http://192.168.1.5:7878",
	} {
		if got := publicBase("", in); got != want {
			t.Errorf("publicBase(%q) = %q, attendu %q", in, got, want)
		}
	}
	if got := publicBase("https://bambu.lan:7878/", "[::]:7878"); got != "https://bambu.lan:7878" {
		t.Errorf("URL publique : %q", got)
	}
	for _, bad := range []string{"ftp://x", "http://", "http://0.0.0.0:7878", "http://[::]:7878", "https://x/chemin", "pas une url"} {
		if validatePublicURL(bad) == nil {
			t.Errorf("--public-url %q accepté", bad)
		}
	}
	if validatePublicURL("") != nil || validatePublicURL("https://bambu.lan:7878") != nil {
		t.Error("URL valide refusée")
	}
}

func TestEnrollCommandPrintsCodeAndManagesSessions(t *testing.T) {
	dir := t.TempDir()
	// Le service a déjà créé la base : on la prépare comme bambud le ferait.
	st, err := store.Open(filepath.Join(dir, "bambu.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	var out, errb bytes.Buffer
	if rc := enrollMain([]string{"--data-dir", dir, "--addr", "[::]:7878"}, &out, &errb); rc != 0 {
		t.Fatalf("enroll : %d %s", rc, errb.String())
	}
	m := regexp.MustCompile(`Code d'enregistrement : ([A-Z2-9]{4}-[A-Z2-9]{4})`).FindStringSubmatch(out.String())
	if m == nil || !strings.Contains(out.String(), "http://127.0.0.1:7878/enroll") || strings.Contains(out.String(), "[::]") {
		t.Fatalf("sortie : %s", out.String())
	}
	// Le code créé par la CLI est utilisable par le service (même base).
	st, _ = store.Open(filepath.Join(dir, "bambu.db"), store.Options{})
	defer st.Close()
	svc := humanauth.New(st, humanauth.Options{})
	if _, sess, err := svc.Enroll(context.Background(), m[1], "test"); err != nil {
		t.Fatalf("enroll avec le code de la CLI : %v", err)
	} else {
		out.Reset()
		if rc := enrollMain([]string{"--data-dir", dir, "--list"}, &out, &errb); rc != 0 || !strings.Contains(out.String(), sess.ID) || !strings.Contains(out.String(), "actif") {
			t.Fatalf("--list : %d %s", rc, out.String())
		}
		out.Reset()
		if rc := enrollMain([]string{"--data-dir", dir, "--revoke", sess.ID}, &out, &errb); rc != 0 {
			t.Fatalf("--revoke : %d %s", rc, errb.String())
		}
		out.Reset()
		enrollMain([]string{"--data-dir", dir, "--list"}, &out, &errb)
		if !strings.Contains(out.String(), "révoqué") {
			t.Fatalf("après révocation : %s", out.String())
		}
		if rc := enrollMain([]string{"--data-dir", dir, "--revoke", "ses_inconnue"}, &out, &errb); rc == 0 {
			t.Fatal("révocation d'une session inconnue acceptée")
		}
	}
	if rc := enrollMain([]string{"--data-dir", filepath.Join(dir, "absent")}, &out, &errb); rc == 0 {
		t.Fatal("dossier de données absent accepté")
	}
}

func TestAgentPairCommandCreatesAPairingToken(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "bambu.db"), store.Options{})
	st.Close()
	var out, errb bytes.Buffer
	if rc := agentMain([]string{"pair", "--data-dir", dir, "--public-url", "https://bambu.lan:7878"}, &out, &errb); rc != 0 {
		t.Fatalf("agent pair : %d %s", rc, errb.String())
	}
	if !regexp.MustCompile(`pair-[0-9a-f]{32}`).MatchString(out.String()) || !strings.Contains(out.String(), "--server https://bambu.lan:7878") {
		t.Fatalf("sortie : %s", out.String())
	}
	if rc := agentMain([]string{"autre"}, &out, &errb); rc == 0 {
		t.Fatal("sous-commande inconnue acceptée")
	}
}
