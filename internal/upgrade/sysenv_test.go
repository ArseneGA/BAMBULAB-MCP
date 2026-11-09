package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// recRunner enregistre les commandes et simule leurs effets sur le système de fichiers (dossiers, binaires construits).
type recRunner struct {
	cmds    []Cmd
	failOn  string // fragment de commande qui échoue
	outputs map[string]string
}

func (r *recRunner) Run(_ context.Context, c Cmd) (string, error) {
	r.cmds = append(r.cmds, c)
	line := c.String()
	if r.failOn != "" && strings.Contains(line, r.failOn) {
		return "sortie de l'échec", fmt.Errorf("%s : exit status 1", line)
	}
	for k, v := range r.outputs {
		if strings.Contains(line, k) {
			return v, nil
		}
	}
	switch {
	case c.Name == "install" && len(c.Args) > 0 && c.Args[len(c.Args)-2] == "-o":
		_ = os.MkdirAll(c.Args[len(c.Args)-1], 0o755)
	case c.Name == "mkdir":
		_ = os.MkdirAll(c.Args[len(c.Args)-1], 0o755)
	case c.Name == "mktemp":
		return filepath.Join(os.TempDir(), "tmp-AAAAAA"), nil
	case c.Name == "rm":
		for _, a := range c.Args[1:] {
			_ = os.RemoveAll(a)
		}
	case c.Name == "go" && len(c.Args) > 0 && c.Args[0] == "build":
		out := c.Args[len(c.Args)-2]
		_ = os.MkdirAll(filepath.Dir(out), 0o755)
		_ = os.WriteFile(out, []byte("#!bin "+filepath.Base(out)), 0o755)
	case strings.HasSuffix(c.Name, "bambud") && len(c.Args) == 1 && c.Args[0] == "schema-version":
		return "280", nil
	case c.Name == "git" && containsArg(c.Args, "describe"):
		return "v1-3-gabcdef0", nil
	case c.Name == "git" && containsArg(c.Args, "rev-parse"):
		return "treeabc123", nil
	}
	return "", nil
}

func containsArg(args []string, a string) bool {
	for _, x := range args {
		if x == a {
			return true
		}
	}
	return false
}

func (r *recRunner) lines() []string {
	var out []string
	for _, c := range r.cmds {
		out = append(out, c.String())
	}
	return out
}

func newSys(t *testing.T, run Runner) *SysEnv {
	t.Helper()
	root := t.TempDir()
	cfg := Config{Repo: "https://github.com/ArseneGA/BAMBULAB-MCP", Branch: "main", Root: filepath.Join(root, "opt"), ConfDir: filepath.Join(root, "etc"),
		CacheDir: filepath.Join(root, "cache"), DataDir: filepath.Join(root, "data"), BackupDir: filepath.Join(root, "backups"),
		AppUser: "atelier", BuildUser: "atelier-update", Service: "atelier.service", KeepReleases: 3, KeepBackups: 2,
		SmokeMode: SmokeRequired, HealthWait: time.Second, BuildPath: "/usr/local/go/bin"}
	for _, d := range []string{cfg.Root, cfg.CacheDir, cfg.BackupDir, cfg.ConfDir, cfg.DataDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e := NewSysEnv(cfg, "/opt/atelier/updater/bambud")
	e.Run = run
	e.Out = os.Stderr
	e.Sleep = func(context.Context, time.Duration) error { return nil }
	return e
}

// ---- Construction ------------------------------------------------------------------------------------------------

func TestBuildCommandsOrderAndHygiene(t *testing.T) {
	rr := &recRunner{}
	e := newSys(t, rr)
	rel, err := e.Build(context.Background(), shaNew)
	if err != nil {
		t.Fatal(err)
	}
	if rel.SHA != shaNew || rel.Version != "v1-3-gabcdef0" || rel.Schema != 280 || rel.GeometryTree != "treeabc123" || rel.Dir != filepath.Join(e.Cfg.ReleasesDir(), shaNew) {
		t.Fatalf("release : %+v", rel)
	}
	lines := slashAll(rr.lines())
	joined := strings.Join(lines, "\n")
	// Ordre : web, binaires, tests, validateur, publication.
	order := []string{"npm ci --no-audit --no-fund", "npm run build", "go build", "go test -short ./... -count=1 -timeout 30m", "node validate.mjs", "schema-version", "pkill -KILL -u atelier-update", "chown -R root:root", "chmod -R go-w"}
	pos := -1
	for _, o := range order {
		i := strings.Index(joined[pos+1:], o)
		if i < 0 {
			t.Fatalf("%q absent ou dans le désordre :\n%s", o, joined)
		}
		pos += i + 1
	}
	// Les trois binaires.
	for _, b := range []string{"./cmd/bambud", "./cmd/bambu-mcp", "./cmd/bambu-agent"} {
		if !strings.Contains(joined, b) {
			t.Fatalf("binaire %s non construit", b)
		}
	}
	// Version compilée et reproductibilité.
	if !strings.Contains(joined, "-trimpath") || !strings.Contains(joined, "-X github.com/ArseneGA/BAMBULAB-MCP/internal/core.Version=v1-3-gabcdef0") {
		t.Fatalf("ldflags :\n%s", joined)
	}
	// Hygiène : le code téléchargé ne tourne JAMAIS en root ni avec des secrets ; les tests sans BAMBU_STUDIO_BIN et avec TMPDIR.
	for _, c := range rr.cmds {
		switch c.Name {
		case "npm", "go", "node", "tar", "git":
			if c.User != "atelier-update" {
				t.Errorf("%s doit tourner sous atelier-update : %v", c.Name, c)
			}
		}
		for _, kv := range c.Env {
			if strings.HasPrefix(kv, "BAMBU_") || strings.Contains(kv, "TOKEN") {
				t.Errorf("variable sensible transmise à %s : %s", c.Name, strings.Split(kv, "=")[0])
			}
		}
		if c.Name == "go" && c.Args[0] == "test" {
			if !hasEnvPrefix(c.Env, "TMPDIR=") || hasEnvPrefix(c.Env, "BAMBU_STUDIO_BIN") {
				t.Errorf("go test : TMPDIR requis, BAMBU_STUDIO_BIN interdit : %v", c.Env)
			}
		}
		if c.Name == "node" && !hasEnvPrefix(c.Env, "BAMBUD_BIN=") {
			t.Errorf("le validateur doit recevoir BAMBUD_BIN : %v", c.Env)
		}
	}
	// Publication : la version est prête, relisible, et le dossier partiel a disparu.
	if _, ok := e.Prepared(shaNew); !ok {
		t.Fatal("version non marquée prête")
	}
	if _, err := os.Stat(filepath.Join(e.Cfg.ReleasesDir(), ".partial-"+shaNew)); err == nil {
		t.Fatal("dossier partiel resté")
	}
	b, _ := os.ReadFile(filepath.Join(rel.Dir, "REVISION"))
	var back Release
	if json.Unmarshal(b, &back) != nil || back.SHA != shaNew {
		t.Fatalf("REVISION : %s", b)
	}
	for _, bin := range releaseBinaries {
		if _, err := os.Stat(filepath.Join(rel.Dir, "bin", bin)); err != nil {
			t.Errorf("binaire %s absent : %v", bin, err)
		}
	}
}

func hasEnvPrefix(env []string, p string) bool {
	for _, kv := range env {
		if strings.HasPrefix(kv, p) {
			return true
		}
	}
	return false
}

// Chaque étape qui échoue interrompt la construction, nettoie, ne publie rien : le service n'est jamais concerné.
func TestBuildFailureAtEachStepPublishesNothing(t *testing.T) {
	for _, step := range []string{"npm ci --no-audit --no-fund", "npm run build", "go build", "go test", "node validate.mjs", "schema-version", "archive --format"} {
		rr := &recRunner{failOn: step}
		e := newSys(t, rr)
		_, err := e.Build(context.Background(), shaNew)
		if err == nil {
			t.Errorf("%s : l'échec n'est pas remonté", step)
			continue
		}
		if _, ok := e.Prepared(shaNew); ok {
			t.Errorf("%s : version publiée malgré l'échec", step)
		}
		if _, statErr := os.Stat(filepath.Join(e.Cfg.ReleasesDir(), shaNew)); statErr == nil {
			t.Errorf("%s : dossier final présent", step)
		}
		if _, statErr := os.Stat(filepath.Join(e.Cfg.ReleasesDir(), ".partial-"+shaNew)); statErr == nil {
			t.Errorf("%s : dossier partiel non nettoyé", step)
		}
		for _, l := range rr.lines() {
			if strings.Contains(l, "systemctl") || strings.Contains(l, "ln ") {
				t.Errorf("%s : la construction ne doit jamais toucher au service : %s", step, l)
			}
		}
	}
}

func TestBuildRejectsMalformedSHA(t *testing.T) {
	e := newSys(t, &recRunner{})
	for _, bad := range []string{"", "main", "--upload-pack=x", shaNew[:39], "ABCDEFABCDEFABCDEFABCDEFABCDEFABCDEFABCD"} {
		if _, err := e.Build(context.Background(), bad); err == nil {
			t.Errorf("%q accepté", bad)
		}
	}
}

// ---- Git réel (dépôt local file://) ---------------------------------------------------------------------------------

type plainRunner struct{}

// plainRunner exécute les commandes avec l'environnement du processus (tests locaux sans runuser).
func (plainRunner) Run(ctx context.Context, c Cmd) (string, error) {
	c.Env, c.User = nil, ""
	return ExecRunner{}.Run(ctx, c)
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v : %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestFetchResolveAndAncestryWithRealGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git absent")
	}
	// Le test clone en file:// : si l'environnement (construction de l'outil : GIT_ALLOW_PROTOCOL=https) restreint les protocoles,
	// l'autoriser ici, quel que soit l'environnement hérité.
	t.Setenv("GIT_ALLOW_PROTOCOL", "file:https")
	origin := filepath.Join(t.TempDir(), "origin")
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, origin, "init", "-q", "-b", "main")
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(origin, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", "1")
	gitIn(t, origin, "add", ".")
	gitIn(t, origin, "commit", "-q", "-m", "un")
	c1 := gitIn(t, origin, "rev-parse", "HEAD")

	e := newSys(t, plainRunner{})
	e.Cfg.Repo = "file://" + filepath.ToSlash(origin)
	if !strings.HasPrefix(e.Cfg.Repo, "file://") {
		t.Fatal("dépôt de test")
	}
	if strings.HasPrefix(filepath.ToSlash(origin), "/") == false { // Windows : file:///C:/...
		e.Cfg.Repo = "file:///" + filepath.ToSlash(origin)
	}
	ctx := context.Background()
	head, err := e.Fetch(ctx)
	if err != nil || head != c1 {
		t.Fatalf("premier fetch : %q %v (attendu %s)", head, err, c1)
	}
	// Rien de neuf : même tête.
	if again, err := e.Fetch(ctx); err != nil || again != c1 {
		t.Fatalf("second fetch : %q %v", again, err)
	}
	write("a.txt", "2")
	gitIn(t, origin, "commit", "-q", "-am", "deux")
	c2 := gitIn(t, origin, "rev-parse", "HEAD")
	if head, err = e.Fetch(ctx); err != nil || head != c2 {
		t.Fatalf("fetch après un nouveau commit : %q %v", head, err)
	}
	if ok, err := e.IsAncestor(ctx, c1, c2); err != nil || !ok {
		t.Fatalf("c1 est ancêtre de c2 : %v %v", ok, err)
	}
	if ok, err := e.IsAncestor(ctx, c2, c1); err != nil || ok {
		t.Fatalf("c2 n'est pas ancêtre de c1 : %v %v", ok, err)
	}
	if sha, err := e.ResolveCommit(ctx, c1[:8]); err != nil || sha != c1 {
		t.Fatalf("préfixe : %q %v", sha, err)
	}
	if _, err := e.ResolveCommit(ctx, "0000000000000000000000000000000000000000"); err == nil {
		t.Fatal("commit inexistant accepté")
	}
	if _, err := e.ResolveCommit(ctx, "--output=/tmp/x"); err == nil {
		t.Fatal("révision commençant par un tiret acceptée")
	}

	// Historique réécrit (force-push) : l'ancienne tête n'est plus un ancêtre de la nouvelle.
	gitIn(t, origin, "reset", "-q", "--hard", c1)
	write("a.txt", "3")
	gitIn(t, origin, "commit", "-q", "-am", "trois")
	c3 := gitIn(t, origin, "rev-parse", "HEAD")
	if head, err = e.Fetch(ctx); err != nil || head != c3 {
		t.Fatalf("fetch après réécriture : %q %v", head, err)
	}
	if ok, err := e.IsAncestor(ctx, c2, c3); err != nil || ok {
		t.Fatalf("après réécriture, c2 ne doit plus être un ancêtre : %v %v", ok, err)
	}
}

// ---- Bascule du lien, versions, purge -------------------------------------------------------------------------------

func mkRelease(t *testing.T, e *SysEnv, name string, built time.Time, sha string) Release {
	t.Helper()
	dir := filepath.Join(e.Cfg.ReleasesDir(), name)
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "bambud"), []byte("bin "+name), 0o755); err != nil {
		t.Fatal(err)
	}
	r := Release{SHA: sha, Version: "v-" + name, Schema: 270, BuiltAt: built}
	b, _ := json.Marshal(r)
	if err := os.WriteFile(filepath.Join(dir, "REVISION"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if sha != "" {
		_ = os.WriteFile(filepath.Join(dir, ".ready"), nil, 0o644)
	}
	r.Dir = dir
	return r
}

func symlinkOrSkip(t *testing.T, e *SysEnv, to string) {
	t.Helper()
	if err := atomicSymlink(e.Cfg.CurrentLink(), to); err != nil {
		t.Skipf("liens symboliques indisponibles ici (à jouer sur Linux) : %v", err)
	}
}

func TestSwitchIsAtomicAndKeepsPrevious(t *testing.T) {
	e := newSys(t, &recRunner{})
	a := mkRelease(t, e, "legacy-1", time.Now().Add(-48*time.Hour), "")
	b := mkRelease(t, e, shaNew, time.Now(), shaNew)
	symlinkOrSkip(t, e, "releases/legacy-1")
	ctx := context.Background()

	cur, err := e.Current(ctx)
	if err != nil || cur.Version != "v-legacy-1" || cur.Dir != a.Dir {
		t.Fatalf("Current : %+v %v", cur, err)
	}
	if err := e.Switch(ctx, b, true); err != nil {
		t.Fatal(err)
	}
	if cur, _ = e.Current(ctx); cur.SHA != shaNew {
		t.Fatalf("après bascule : %+v", cur)
	}
	if prev, err := os.Readlink(e.Cfg.PreviousLink()); err != nil || prev != "releases/legacy-1" {
		t.Fatalf("previous : %q %v", prev, err)
	}
	// Retour arrière : current revient, previous n'est pas écrasé.
	if err := e.Switch(ctx, a, false); err != nil {
		t.Fatal(err)
	}
	if cur, _ = e.Current(ctx); cur.Version != "v-legacy-1" {
		t.Fatalf("après retour : %+v", cur)
	}
	if prev, _ := os.Readlink(e.Cfg.PreviousLink()); prev != "releases/legacy-1" {
		t.Fatalf("previous modifié par le retour : %q", prev)
	}
	// Jamais de lien vers une version sans binaire.
	if err := e.Switch(ctx, Release{Dir: filepath.Join(e.Cfg.ReleasesDir(), "vide")}, true); err == nil {
		t.Fatal("bascule vers une version sans binaire acceptée")
	}
	if cur, _ = e.Current(ctx); cur.Version != "v-legacy-1" {
		t.Fatal("le lien a bougé malgré l'erreur")
	}
	// Pas de fichier temporaire oublié.
	ents, _ := os.ReadDir(e.Cfg.Root)
	for _, en := range ents {
		if strings.Contains(en.Name(), ".tmp-") {
			t.Fatalf("lien temporaire resté : %s", en.Name())
		}
	}
}

func TestCurrentWithoutLayoutIsExplicit(t *testing.T) {
	e := newSys(t, &recRunner{})
	if _, err := e.Current(context.Background()); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("%v", err)
	}
}

func TestPruneKeepsCurrentPreviousAndNewest(t *testing.T) {
	e := newSys(t, &recRunner{})
	e.Cfg.KeepReleases = 2
	now := time.Now()
	var rels []Release
	for i := 0; i < 6; i++ {
		rels = append(rels, mkRelease(t, e, fmt.Sprintf("r%d", i), now.Add(time.Duration(i)*time.Hour), fmt.Sprintf("%040d", i)))
	}
	if err := os.WriteFile(filepath.Join(rels[0].Dir, ".keep"), nil, 0o644); err != nil { // épinglée à la main
		t.Fatal(err)
	}
	// Un dossier partiel d'une construction en cours n'est jamais touché.
	if err := os.MkdirAll(filepath.Join(e.Cfg.ReleasesDir(), ".partial-x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := e.pruneReleases(rels[5], rels[1]); err != nil {
		t.Fatal(err)
	}
	for i, want := range []bool{true /* .keep */, true /* previous */, false, false, true, true} {
		_, err := os.Stat(rels[i].Dir)
		if (err == nil) != want {
			t.Errorf("r%d : présent=%v, attendu %v", i, err == nil, want)
		}
	}
	if _, err := os.Stat(filepath.Join(e.Cfg.ReleasesDir(), ".partial-x")); err != nil {
		t.Fatal("dossier partiel supprimé")
	}
}

func TestPruneBackupsKeepsNewestPreupdateOnly(t *testing.T) {
	e := newSys(t, &recRunner{})
	for _, n := range []string{"preupdate-20260101T000000Z-aaaaaaa.tar.gz", "preupdate-20260102T000000Z-bbbbbbb.tar.gz", "preupdate-20260103T000000Z-ccccccc.tar.gz", "atelier-20260101T030000Z.tar.gz"} {
		p := filepath.Join(e.Cfg.BackupDir, n)
		_ = os.WriteFile(p, []byte("x"), 0o600)
		_ = os.WriteFile(p+".sha256", []byte("x"), 0o600)
	}
	if err := e.pruneBackups(); err != nil {
		t.Fatal(err)
	}
	left, _ := filepath.Glob(filepath.Join(e.Cfg.BackupDir, "*"))
	if len(left) != 6 { // 2 preupdate + leurs .sha256, + la sauvegarde quotidienne (jamais touchée) — son .sha256 compte
		names := []string{}
		for _, l := range left {
			names = append(names, filepath.Base(l))
		}
		t.Fatalf("restant : %v", names)
	}
	if _, err := os.Stat(filepath.Join(e.Cfg.BackupDir, "atelier-20260101T030000Z.tar.gz")); err != nil {
		t.Fatal("la sauvegarde quotidienne ne doit pas être supprimée par la purge des sauvegardes d'avant mise à jour")
	}
	if _, err := os.Stat(filepath.Join(e.Cfg.BackupDir, "preupdate-20260101T000000Z-aaaaaaa.tar.gz")); err == nil {
		t.Fatal("la plus ancienne sauvegarde d'avant mise à jour aurait dû être supprimée")
	}
}

func TestRefreshUpdaterCopiesNewBinary(t *testing.T) {
	e := newSys(t, &recRunner{})
	r := mkRelease(t, e, "new", time.Now(), shaNew)
	if err := e.refreshUpdater(r); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(e.Cfg.UpdaterBin())
	if err != nil || string(got) != "bin new" {
		t.Fatalf("%q %v", got, err)
	}
}

// ---- Sauvegarde / restauration -------------------------------------------------------------------------------------

func TestBackupUsesRunningBinaryAndChecksSidecar(t *testing.T) {
	rr := &recRunner{}
	e := newSys(t, rr)
	cur := Release{Dir: "/opt/atelier/releases/old"}
	// Le runner simulé n'écrit pas l'archive : on la crée à la demande pour simuler bambud backup.
	out, err := func() (string, error) {
		rr2 := &hookRunner{recRunner: rr, onRun: func(c Cmd) {
			if len(c.Args) > 0 && c.Args[0] == "backup" {
				p := c.Args[len(c.Args)-1]
				_ = os.WriteFile(p, []byte("x"), 0o600)
				_ = os.WriteFile(p+".sha256", []byte("x"), 0o600)
			}
		}}
		e.Run = rr2
		return e.Backup(context.Background(), cur, Release{SHA: shaNew})
	}()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(out), "preupdate-") || !strings.HasSuffix(out, "-22222222.tar.gz") || filepath.Dir(out) != e.Cfg.BackupDir {
		t.Fatalf("nom : %s", out)
	}
	lines := slashAll(rr.lines())
	if len(lines) != 2 || !strings.Contains(lines[0], "[atelier] /opt/atelier/releases/old/bin/bambud backup --data-dir") ||
		!strings.Contains(lines[1], "verify --archive "+filepath.ToSlash(out)) {
		t.Fatalf("commandes : %v", lines)
	}
	_ = os.Remove(out + ".sha256")
	// Sans empreinte .sha256 : échec.
	e.Run = &recRunner{}
	if _, err := e.Backup(context.Background(), cur, Release{SHA: shaNew}); err == nil {
		t.Fatal("sauvegarde sans .sha256 acceptée")
	}
	// bambud backup en échec : échec.
	e.Run = &recRunner{failOn: "backup"}
	if _, err := e.Backup(context.Background(), cur, Release{SHA: shaNew}); err == nil {
		t.Fatal("échec de bambud backup non remonté")
	}
}

type hookRunner struct {
	*recRunner
	onRun func(Cmd)
}

func (h *hookRunner) Run(ctx context.Context, c Cmd) (string, error) {
	h.onRun(c)
	return h.recRunner.Run(ctx, c)
}

func TestRestoreUsesOldBinaryWithForceThenVerifies(t *testing.T) {
	rr := &recRunner{}
	e := newSys(t, rr)
	if err := e.Restore(context.Background(), Release{Dir: "/opt/atelier/releases/old"}, "/var/backups/atelier/preupdate-x.tar.gz"); err != nil {
		t.Fatal(err)
	}
	lines := slashAll(rr.lines())
	if len(lines) != 2 || !strings.Contains(lines[0], "/opt/atelier/releases/old/bin/bambud restore --from /var/backups/atelier/preupdate-x.tar.gz --data-dir") ||
		!strings.HasSuffix(lines[0], "--force") || !strings.Contains(lines[1], "verify --data-dir") {
		t.Fatalf("%v", lines)
	}
	e.Run = &recRunner{failOn: "verify --data-dir"}
	if err := e.Restore(context.Background(), Release{Dir: "/x"}, "/a"); err == nil {
		t.Fatal("contrôle après restauration en échec non remonté")
	}
}

// ---- /health -----------------------------------------------------------------------------------------------------

func TestHealthChecksStatusVersionSchemaAndStability(t *testing.T) {
	var hits atomic.Int32
	var failFirst int32 = 2
	version, schema, status := "v-new", 280, "ok"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) <= failFirst {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintf(w, `{"status":%q,"version":%q,"schema_version":%d}`, status, version, schema)
	}))
	defer srv.Close()
	rr := &recRunner{}
	e := newSys(t, rr)
	e.Cfg.BaseURL = srv.URL
	e.HTTP = srv.Client()
	ctx := context.Background()
	// Horloge qui avance à chaque lecture : l'échéance est atteignable sans attendre.
	base := time.Now()
	var ticks int
	e.clock = func() time.Time { ticks++; return base.Add(time.Duration(ticks) * 10 * time.Second) }
	e.Cfg.HealthWait = 5 * time.Minute
	e.Cfg.StableWait = time.Second

	if err := e.Health(ctx, "v-new", 280); err != nil {
		t.Fatalf("service qui finit par répondre : %v", err)
	}
	if hits.Load() < 4 {
		t.Fatalf("attente et vérification de stabilité attendues : %d requêtes", hits.Load())
	}
	found := false
	for _, l := range rr.lines() {
		if strings.Contains(l, "systemctl is-active --quiet atelier.service") {
			found = true
		}
	}
	if !found {
		t.Fatal("le service doit être vérifié actif après le délai de stabilité")
	}

	failFirst = 0
	e.Cfg.HealthWait = 25 * time.Second
	for name, c := range map[string]struct {
		v string
		s int
	}{"version": {"v-autre", 280}, "schéma": {"v-new", 270}} {
		if err := e.Health(ctx, c.v, c.s); err == nil {
			t.Errorf("%s inattendu(e) accepté(e)", name)
		}
	}
	status = "degraded"
	if err := e.Health(ctx, "v-new", 280); err == nil {
		t.Error("status degraded accepté")
	}
	status = "ok"
	// Version attendue vide (installation d'avant l'automatique) : seuls status et schéma comptent.
	if err := e.Health(ctx, "", 280); err != nil {
		t.Errorf("version non exigée : %v", err)
	}
	// Service retombé après son démarrage : échec même si /health a répondu une fois.
	e.Run = &recRunner{failOn: "is-active"}
	if err := e.Health(ctx, "v-new", 280); err == nil || !strings.Contains(err.Error(), "retombé") {
		t.Errorf("service retombé non détecté : %v", err)
	}
}

// ---- Occupation côté mise à jour -------------------------------------------------------------------------------

func TestBusyViaServiceThenDatabaseFallback(t *testing.T) {
	mode := "busy"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/system/busy" || r.Header.Get("Authorization") != "Bearer tok-secret-123456" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch mode {
		case "busy":
			fmt.Fprint(w, `{"busy":true,"reasons":[{"code":"print_active","detail":"1 impression(s)"}]}`)
		case "free":
			fmt.Fprint(w, `{"busy":false,"reasons":[]}`)
		case "old": // service d'avant cette fonctionnalité
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	rr := &recRunner{outputs: map[string]string{"upgrade-probe": `{"schema_version":270,"busy":true,"reasons":["print_active: 1 impression(s) PRINTING"]}`}}
	e := newSys(t, rr)
	e.Cfg.BaseURL, e.Cfg.APIToken, e.HTTP = srv.URL, "tok-secret-123456", srv.Client()
	ctx := context.Background()

	if st := e.Busy(ctx); !st.Busy || st.Unknown || !strings.Contains(st.Reasons[0], "print_active") {
		t.Fatalf("service occupé : %+v", st)
	}
	mode = "free"
	if st := e.Busy(ctx); st.Busy || st.Unknown {
		t.Fatalf("service libre : %+v", st)
	}
	if len(rr.cmds) != 0 {
		t.Fatal("quand le service répond, la base n'est pas lue")
	}
	// Ancien service (404) : repli sur la base, qui voit l'impression.
	mode = "old"
	if st := e.Busy(ctx); !st.Busy || st.Unknown {
		t.Fatalf("repli base : %+v", st)
	}
	if rr.cmds[0].User != "atelier" || !strings.Contains(rr.cmds[0].String(), "upgrade-probe --data-dir") {
		t.Fatalf("la sonde doit tourner sous l'utilisateur du service : %v", rr.cmds[0])
	}
	// Service arrêté ET base illisible : inconnu = occupé.
	srv.Close()
	e.Run = &recRunner{failOn: "upgrade-probe"}
	if st := e.Busy(ctx); !st.Unknown {
		t.Fatalf("tout est illisible : %+v", st)
	}
	// Le jeton n'apparaît jamais dans les raisons.
	for _, r := range e.Busy(ctx).Reasons {
		if strings.Contains(r, "tok-secret") {
			t.Fatalf("jeton divulgué : %s", r)
		}
	}
}

// ---- Worker géométrie -----------------------------------------------------------------------------------------------

func TestGeometrySyncKeepsVenvAndRevertRestoresOldCode(t *testing.T) {
	e := newSys(t, plainRunner2{})
	if _, err := exec.LookPath("cp"); err != nil {
		t.Skip("cp absent")
	}
	cad := filepath.Join(e.Cfg.Root, "cad", "geometry")
	e.Cfg.CadDir = cad
	if err := os.MkdirAll(filepath.Join(cad, ".venv"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(cad, ".venv", "marker"), []byte("venv"), 0o644)
	_ = os.WriteFile(filepath.Join(cad, "worker.py"), []byte("ancien"), 0o644)
	_ = os.WriteFile(e.geomMarker(), []byte("tree-old\n"), 0o644)

	rel := Release{SHA: shaNew, Dir: filepath.Join(e.Cfg.ReleasesDir(), "n"), GeometryTree: "tree-new"}
	if err := os.MkdirAll(filepath.Join(rel.Dir, "geometry"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(rel.Dir, "geometry", "worker.py"), []byte("nouveau"), 0o644)

	ctx := context.Background()
	// Même arbre : rien ne bouge.
	same := rel
	same.GeometryTree = "tree-old"
	if err := e.SyncGeometry(ctx, same); err != nil || e.geomChanged {
		t.Fatalf("arbre identique : %v changed=%v", err, e.geomChanged)
	}
	if err := e.SyncGeometry(ctx, rel); err != nil || !e.geomChanged {
		t.Fatalf("sync : %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(cad, "worker.py")); string(b) != "nouveau" {
		t.Fatalf("code non remplacé : %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(cad, ".venv", "marker")); string(b) != "venv" {
		t.Fatal("l'environnement .venv doit être conservé")
	}
	if b, _ := os.ReadFile(e.geomMarker()); strings.TrimSpace(string(b)) != "tree-new" {
		t.Fatalf("marqueur : %q", b)
	}
	if err := e.RevertGeometry(ctx); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(cad, "worker.py")); string(b) != "ancien" {
		t.Fatalf("retour du code : %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(cad, ".venv", "marker")); string(b) != "venv" {
		t.Fatal(".venv perdu au retour arrière")
	}
	if b, _ := os.ReadFile(e.geomMarker()); strings.TrimSpace(string(b)) != "tree-old" {
		t.Fatalf("marqueur après retour : %q", b)
	}
	// Sans changement préalable, le retour est un no-op.
	if err := e.RevertGeometry(ctx); err != nil {
		t.Fatal(err)
	}
}

// plainRunner2 : exécute cp réellement, ignore chown (pas de root ici).
type plainRunner2 struct{}

func (plainRunner2) Run(ctx context.Context, c Cmd) (string, error) {
	if c.Name == "chown" {
		return "", nil
	}
	return plainRunner{}.Run(ctx, c)
}

// ---- Rapprochement de la sonde de base et du service -----------------------------------------------------------------

func TestSmokeSkippedWhenScriptMissing(t *testing.T) {
	e := newSys(t, &recRunner{})
	if err := e.Smoke(context.Background()); err != nil {
		t.Fatalf("script absent : le test de fumée est ignoré, pas en échec : %v", err)
	}
}
