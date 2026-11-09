package upgrade

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Le script de fumée vient du dépôt : il ne s'exécute JAMAIS en root, et le jeton d'API ne passe ni par argv ni par un
// fichier lisible par d'autres.
func TestSmokeRunsAsServiceUserWithPrivateTokenFile(t *testing.T) {
	var seenToken, seenMode string
	var smokeCmd Cmd
	var order []string
	rr := funcRunner(func(_ context.Context, c Cmd) (string, error) {
		order = append(order, c.Name)
		if c.Name == "bash" {
			smokeCmd = c
			for i, a := range c.Args {
				if a == "--token-file" {
					b, err := os.ReadFile(c.Args[i+1])
					if err != nil {
						t.Errorf("fichier de jeton illisible pendant le test : %v", err)
					}
					seenToken = string(b)
					if fi, err := os.Stat(c.Args[i+1]); err == nil {
						seenMode = fi.Mode().Perm().String()
					}
				}
			}
		}
		return "", nil
	})
	e := newSys(t, rr)
	e.Cfg.APIToken = "jeton-secret-0123456789"
	scriptDir := filepath.Join(e.Cfg.CurrentLink(), "scripts") // « current » est un dossier ordinaire dans ce test
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "smoke-linux.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := e.Smoke(context.Background()); err != nil {
		t.Fatal(err)
	}
	if smokeCmd.User != e.Cfg.AppUser || smokeCmd.User == "" {
		t.Fatalf("le test de fumée doit tourner sous l'utilisateur du service, pas en root : %+v", smokeCmd)
	}
	for _, a := range smokeCmd.Args {
		if strings.Contains(a, "jeton-secret") {
			t.Fatalf("jeton dans les arguments : %v", smokeCmd.Args)
		}
	}
	if !strings.Contains(smokeCmd.String(), "--no-cad") {
		t.Fatalf("CAO non configurée : --no-cad attendu : %v", smokeCmd.Args)
	}
	if !strings.Contains(seenToken, "BAMBU_API_TOKEN=jeton-secret-0123456789") {
		t.Fatalf("contenu du fichier de jeton : %q", seenToken)
	}
	if runtimeEnforcesModes() && seenMode != "-rw-------" {
		t.Fatalf("droits du fichier de jeton : %s", seenMode)
	}
	if len(order) < 2 || order[0] != "chown" {
		t.Fatalf("le fichier doit être donné à l'utilisateur du service avant le test : %v", order)
	}
	if left, _ := filepath.Glob(filepath.Join(e.Cfg.DataDir, ".update-smoke-token")); len(left) != 0 {
		t.Fatal("fichier de jeton non supprimé")
	}
	// Sans jeton configuré : pas de fichier du tout.
	e.Cfg.APIToken, e.Cfg.UsesCAD = "", true
	order, smokeCmd = nil, Cmd{}
	if err := e.Smoke(context.Background()); err != nil || strings.Contains(smokeCmd.String(), "--token-file") || strings.Contains(smokeCmd.String(), "--no-cad") {
		t.Fatalf("%v %v", err, smokeCmd.Args)
	}
}

// L'outil de mise à jour tourne en root : il n'est remplacé par du code téléchargé que sur demande explicite.
func TestFinishDoesNotReplaceRootUpdaterByDefault(t *testing.T) {
	e := newSys(t, &recRunner{})
	if err := os.MkdirAll(filepath.Dir(e.Cfg.UpdaterBin()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.Cfg.UpdaterBin(), []byte("outil audité"), 0o755); err != nil {
		t.Fatal(err)
	}
	rel := mkRelease(t, e, "nouvelle", time.Now(), shaNew)
	prev := mkRelease(t, e, "ancienne", time.Now().Add(-time.Hour), shaOld)
	e.Finish(context.Background(), rel, prev)
	if b, _ := os.ReadFile(e.Cfg.UpdaterBin()); string(b) != "outil audité" {
		t.Fatalf("outil root remplacé sans demande : %q", b)
	}
	e.Cfg.RefreshUpdater = true
	e.Finish(context.Background(), rel, prev)
	if b, _ := os.ReadFile(e.Cfg.UpdaterBin()); string(b) != "bin nouvelle" {
		t.Fatalf("rafraîchissement demandé non fait : %q", b)
	}
}
