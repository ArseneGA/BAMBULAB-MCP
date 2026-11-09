package upgrade

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func TestLoadConfigDefaultsAndFiles(t *testing.T) {
	conf := t.TempDir()
	noEnv := func(string) string { return "" }

	// Installation sans aucun fichier : valeurs par défaut sûres.
	c, err := LoadConfig(conf, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if c.Branch != "main" || c.Repo != DefaultRepo || c.Disabled || c.Pin != "" || c.SmokeMode != SmokeRequired || c.KeepReleases != 3 ||
		c.BaseURL != "http://127.0.0.1:7878" || c.Window.Set() || c.BackupDir != "/var/backups/atelier" || c.DataDir != DefaultDataDir {
		t.Fatalf("défauts : %+v", c)
	}

	writeFile(t, filepath.Join(conf, "atelier.env"), "# commentaire\nBAMBU_ADDR=\"0.0.0.0:7979\"\nBAMBU_DATA_DIR=\"/srv/atelier\"\nBAMBU_API_TOKEN=\"abcdefghijklmnop\"\nBAMBU_UV_PATH=\"/opt/atelier/bin/uv\"\nBAMBU_GEOMETRY_DIR=/opt/atelier/cad/geometry\nBAMBU_PUBLIC_URL=\"https://atelier.ts.net\"\n", 0o600)
	writeFile(t, filepath.Join(conf, "backup.env"), "BACKUP_DIR=\"/mnt/disque/sauvegardes\"\nBACKUP_KEEP=14\n", 0o640)
	writeFile(t, filepath.Join(conf, ConfFileName), "ATELIER_UPDATE_WINDOW=07:00-23:00\nATELIER_UPDATE_KEEP_RELEASES=5\nATELIER_UPDATE_SMOKE=advisory # commentaire\nATELIER_UPDATE_REQUIRED_CHECKS=\"Go (vet + tests); Web (build + vitest)\"\n", 0o644)
	c, err = LoadConfig(conf, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseURL != "http://127.0.0.1:7979" || c.DataDir != "/srv/atelier" || c.BackupDir != "/mnt/disque/sauvegardes" || c.APIToken != "abcdefghijklmnop" ||
		!c.UsesCAD || c.CadDir != "/opt/atelier/cad/geometry" || c.PublicURL != "https://atelier.ts.net" || c.KeepReleases != 5 || c.SmokeMode != SmokeAdvisory ||
		!c.Window.Set() || len(c.RequiredChecks) != 2 || c.RequiredChecks[1] != "Web (build + vitest)" {
		t.Fatalf("fichiers : %+v", c)
	}

	// Interrupteur par fichier, épingle par fichier, puis variables d'environnement prioritaires.
	writeFile(t, filepath.Join(conf, DisabledFileName), "", 0o644)
	writeFile(t, filepath.Join(conf, PinFileName), "# version figée\nabc1234\n", 0o644)
	c, err = LoadConfig(conf, noEnv)
	if err != nil || !c.Disabled || c.Pin != "abc1234" {
		t.Fatalf("interrupteur / épingle : %+v %v", c, err)
	}
	os.Remove(filepath.Join(conf, DisabledFileName))
	env := map[string]string{"ATELIER_UPDATE_DISABLED": "1", "ATELIER_UPDATE_PIN": "fedcba9", "ATELIER_UPDATE_BRANCH": "release", "ATELIER_UPDATE_HEALTH_WAIT": "90s"}
	c, err = LoadConfig(conf, func(k string) string { return env[k] })
	if err != nil || !c.Disabled || c.Pin != "fedcba9" || c.Branch != "release" || c.HealthWait != 90*time.Second {
		t.Fatalf("variables : %+v %v", c, err)
	}
}

func TestLoadConfigRejectsBadValues(t *testing.T) {
	for _, bad := range []string{"ATELIER_UPDATE_WINDOW=nuit", "ATELIER_UPDATE_KEEP_RELEASES=1", "ATELIER_UPDATE_SMOKE=peut-etre", "ATELIER_UPDATE_HEALTH_WAIT=vite",
		"ATELIER_UPDATE_BUILD_USER=atelier", "ATELIER_UPDATE_ROOT=relatif"} {
		conf := t.TempDir()
		writeFile(t, filepath.Join(conf, ConfFileName), bad+"\n", 0o644)
		if _, err := LoadConfig(conf, func(string) string { return "" }); err == nil {
			t.Errorf("%q accepté", bad)
		}
	}
}

func TestTokenFileMustBePrivate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "github-token")
	writeFile(t, p, "# jeton en lecture seule\nghp_abcdef123456\n", 0o600)
	tok, err := ReadTokenFile(p)
	if err != nil || tok != "ghp_abcdef123456" {
		t.Fatalf("%q %v", tok, err)
	}
	writeFile(t, p, "GITHUB_TOKEN=\"ghp_zzz\"\n", 0o600)
	if tok, _ = ReadTokenFile(p); tok != "ghp_zzz" {
		t.Fatalf("forme KEY=VALUE : %q", tok)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(p, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadTokenFile(p); err == nil || !strings.Contains(err.Error(), "chmod 600") {
			t.Fatalf("fichier lisible par tous accepté : %v", err)
		}
		// Un jeton refusé fait échouer la configuration (jamais utilisé en silence) : le message ne le contient pas.
		conf := t.TempDir()
		writeFile(t, filepath.Join(conf, "github-token"), "ghp_secret\n", 0o644)
		_, err := LoadConfig(conf, func(string) string { return "" })
		if err == nil || strings.Contains(err.Error(), "ghp_secret") {
			t.Fatalf("%v", err)
		}
	}
	writeFile(t, p, "deux mots\n", 0o600)
	if _, err := ReadTokenFile(p); err == nil {
		t.Fatal("contenu à plusieurs mots accepté")
	}
	if _, err := ReadTokenFile(filepath.Join(dir, "absent")); !os.IsNotExist(err) {
		t.Fatalf("absent : %v", err)
	}
	// Absent : la configuration reste valable (jeton facultatif).
	if c, err := LoadConfig(t.TempDir(), func(string) string { return "" }); err != nil || c.GitHubToken != "" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestReadEnvFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.env")
	writeFile(t, p, "A=1\nB=\"deux mots\"\nexport C='3'\n# D=4\n\nE=5 # fin\nmauvaise ligne\n", 0o644)
	m, err := ReadEnvFile(p)
	if err != nil || m["A"] != "1" || m["B"] != "deux mots" || m["C"] != "3" || m["E"] != "5" || len(m) != 4 {
		t.Fatalf("%v %v", m, err)
	}
	if m, err := ReadEnvFile(filepath.Join(t.TempDir(), "absent")); err != nil || len(m) != 0 {
		t.Fatalf("%v %v", m, err)
	}
}
