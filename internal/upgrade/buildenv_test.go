package upgrade

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Les tests lancés pendant la construction ne reçoivent PAS GIT_ALLOW_PROTOCOL (ils clonent en file://) ; les commandes git de
// l'outil, si.
func TestBuildEnvLeavesGitProtocolToGitCommandsOnly(t *testing.T) {
	e := newSys(t, &recRunner{})
	for _, kv := range e.buildEnv("TMPDIR=/x", "CGO_ENABLED=0") {
		if strings.HasPrefix(kv, "GIT_ALLOW_PROTOCOL=") {
			t.Fatalf("buildEnv ne doit pas restreindre les protocoles git : %s", kv)
		}
	}
	if !hasEnvPrefix(e.gitEnv(), "GIT_ALLOW_PROTOCOL=https") {
		t.Fatalf("gitEnv doit n'autoriser que https : %v", e.gitEnv())
	}
	e.Cfg.Repo = "file:///tmp/x"
	if !hasEnvPrefix(e.gitEnv(), "GIT_ALLOW_PROTOCOL=file:https") {
		t.Fatalf("gitEnv, dépôt local : %v", e.gitEnv())
	}
	// Dans une vraie construction : go test sans, git avec.
	rr := &recRunner{}
	e = newSys(t, rr)
	if _, err := e.Build(context.Background(), shaNew); err != nil {
		t.Fatal(err)
	}
	for _, c := range rr.cmds {
		switch {
		case c.Name == "go" && c.Args[0] == "test":
			if hasEnvPrefix(c.Env, "GIT_ALLOW_PROTOCOL") {
				t.Errorf("go test ne doit pas recevoir GIT_ALLOW_PROTOCOL : %v", c.Env)
			}
		case c.Name == "git":
			if !hasEnvPrefix(c.Env, "GIT_ALLOW_PROTOCOL=https") {
				t.Errorf("git doit recevoir GIT_ALLOW_PROTOCOL=https : %v", c.Env)
			}
		}
	}
}

// Rejoue le test git réel dans l'environnement EXACT d'une construction (environnement vidé de buildEnv + TMPDIR, CGO_ENABLED=0,
// plus GIT_ALLOW_PROTOCOL=https comme l'avait la version fautive) : c'est ce qui a cassé en production (git clone file://
// refusé). Le test réel doit en sortir vert grâce à son t.Setenv.
func TestGitTestPassesUnderBuildEnv(t *testing.T) {
	if os.Getenv("ATELIER_NESTED_BUILD_ENV") != "" {
		t.Skip("déjà dans l'environnement de construction")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go absent")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git absent")
	}
	e := newSys(t, &recRunner{})
	env := e.buildEnv("TMPDIR="+t.TempDir(), "CGO_ENABLED=0", "ATELIER_NESTED_BUILD_ENV=1", "GIT_ALLOW_PROTOCOL=https")
	// Variables strictement nécessaires pour lancer go et git sur ce poste (Windows en exige davantage que Linux).
	for _, k := range []string{"SystemRoot", "USERPROFILE", "LOCALAPPDATA", "APPDATA", "GOCACHE", "GOMODCACHE", "GOPATH", "GOFLAGS", "GOROOT", "TEMP", "TMP", "ProgramFiles", "COMSPEC", "GOTOOLCHAIN"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	env = append(env, "HOME="+os.Getenv("HOME"), "PATH="+os.Getenv("PATH"))
	cmd := exec.Command(goBin, "test", "-short", "-count=1", "-timeout", "5m", "-run", "TestFetchResolveAndAncestryWithRealGit", ".")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("le test git échoue dans l'environnement de construction : %v\n%s", err, out)
	}
}

func TestFailureDigestKeepsTestAndPackageLines(t *testing.T) {
	out := "ok  \tgithub.com/x/a\t0.1s\n--- FAIL: TestAlpha (0.00s)\n    a_test.go:12: boom\n--- FAIL: TestAlpha/sous (0.00s)\nFAIL\nFAIL\tgithub.com/x/b\t0.3s\n" +
		strings.Repeat("bruit\n", 500) + "FAIL\n"
	d := FailureDigest(out)
	for _, want := range []string{"--- FAIL: TestAlpha", "TestAlpha/sous", "FAIL github.com/x/b"} {
		if !strings.Contains(d, want) {
			t.Errorf("%q absent de %q", want, d)
		}
	}
	if FailureDigest("rien d'utile") != "" {
		t.Error("digest non vide sans échec")
	}
	var many strings.Builder
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&many, "--- FAIL: TestN%d (0.0s)\nFAIL\tpkg%d\n", i, i)
	}
	if got := FailureDigest(many.String()); len(got) > 1200 || !strings.Contains(got, "autres") {
		t.Errorf("digest non borné (%d) : %s", len(got), got)
	}
}

type outRunner struct{ recRunner }

func (r *outRunner) Run(ctx context.Context, c Cmd) (string, error) {
	if c.Name == "go" && len(c.Args) > 0 && c.Args[0] == "test" {
		r.cmds = append(r.cmds, c)
		return strings.Repeat("log\n", 3000) + "--- FAIL: TestRareux (0.01s)\n" + strings.Repeat("bruit\n", 3000) + "FAIL\tgithub.com/x/paquet\t1.2s\nFAIL\n",
			fmt.Errorf("%s : exit status 1", c)
	}
	return r.recRunner.Run(ctx, c)
}

func TestBuildTestFailureMessageNamesFailingTest(t *testing.T) {
	e := newSys(t, &outRunner{})
	_, err := e.Build(context.Background(), shaNew)
	if err == nil {
		t.Fatal("échec attendu")
	}
	msg := oneLine(err)
	for _, want := range []string{"--- FAIL: TestRareux", "FAIL github.com/x/paquet"} {
		if !strings.Contains(msg, want) {
			t.Errorf("%q absent du message : %s", want, msg)
		}
	}
}

func TestCheckBuilderAccess(t *testing.T) {
	e := newSys(t, &recRunner{})
	if err := os.MkdirAll(e.Cfg.ReleasesDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CheckBuilderAccess(context.Background(), &recRunner{}, e.Cfg); err != nil {
		t.Fatalf("accès normal refusé : %v", err)
	}
	err := CheckBuilderAccess(context.Background(), &recRunner{failOn: "test -x " + e.Cfg.Root}, e.Cfg)
	if !errors.Is(err, ErrBuilderAccess) || !strings.Contains(err.Error(), "setfacl -m u:atelier-update:--x") {
		t.Fatalf("erreur attendue avec le correctif : %v", err)
	}
	cfg := e.Cfg
	cfg.Root = filepath.Join(t.TempDir(), "absent")
	if err := CheckBuilderAccess(context.Background(), &recRunner{failOn: "test -x"}, cfg); err != nil {
		t.Fatalf("dossier absent : %v", err)
	}
}
