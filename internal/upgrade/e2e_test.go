package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Test d'intégration du moteur COMPLET (Upgrader + SysEnv réel) avec de vrais liens symboliques et de vraies écritures de
// fichiers, sur un « serveur » simulé : un faux systemd, un faux /health qui reflète la version liée par `current`, une
// fausse base dont le schéma change quand la nouvelle version démarre, un faux GitHub. À jouer sous Linux (liens symboliques).

type fakeServer struct {
	mu         sync.Mutex
	t          *testing.T
	e          *SysEnv
	log        []string
	up         bool
	version    string
	schema     int
	dbSchema   int
	failBoot   bool // la nouvelle version démarre mais /health répond 503
	migrates   bool // la nouvelle version migre la base au démarrage
	restoredTo int
	ci         string // état du check-run : success | failure | in_progress
	busy       bool
	healthURL  string
}

func (s *fakeServer) logf(f string, a ...any) { s.log = append(s.log, fmt.Sprintf(f, a...)) }

func (s *fakeServer) currentRevision() Release {
	r, err := readRevision(filepath.Join(s.e.Cfg.Root, "current"))
	if err != nil {
		s.t.Fatalf("current illisible : %v", err)
	}
	return r
}

func (s *fakeServer) boot() {
	r := s.currentRevision()
	s.up, s.version, s.schema = true, r.Version, s.dbSchema
	if r.Schema > s.dbSchema { // la version qui démarre migre la base
		if s.migrates {
			s.dbSchema = r.Schema
		}
	}
	s.schema = s.dbSchema
	if r.Schema < s.dbSchema { // un binaire plus ancien refuse une base plus récente
		s.up = false
	}
}

func (s *fakeServer) run(_ context.Context, c Cmd) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	line := strings.ReplaceAll(c.String(), "\\", "/")
	switch {
	case c.Name == "systemctl" && c.Args[0] == "restart":
		s.logf("systemctl restart")
		s.boot()
		return "", nil
	case c.Name == "systemctl" && c.Args[0] == "start":
		s.logf("systemctl start")
		s.boot()
		return "", nil
	case c.Name == "systemctl" && c.Args[0] == "stop":
		s.logf("systemctl stop")
		s.up = false
		return "", nil
	case c.Name == "systemctl" && c.Args[0] == "is-active":
		if s.up {
			return "", nil
		}
		return "", fmt.Errorf("inactive")
	case strings.Contains(line, "upgrade-probe"):
		return fmt.Sprintf(`{"schema_version":%d,"busy":false,"reasons":[]}`, s.dbSchema), nil
	case len(c.Args) > 0 && c.Args[0] == "backup":
		out := c.Args[len(c.Args)-1]
		_ = os.WriteFile(out, []byte("archive"), 0o600)
		_ = os.WriteFile(out+".sha256", []byte("x"), 0o600)
		s.logf("backup (schéma %d) avec %s", s.dbSchema, filepath.Base(filepath.Dir(filepath.Dir(c.Name))))
		return "", nil
	case len(c.Args) > 0 && c.Args[0] == "verify" && containsArg(c.Args, "--archive"):
		s.logf("verify archive")
		return "", nil
	case len(c.Args) > 0 && c.Args[0] == "restore":
		s.restoredTo = 270
		s.dbSchema = 270
		s.logf("restore avec %s", filepath.Base(filepath.Dir(filepath.Dir(c.Name))))
		return "", nil
	case len(c.Args) > 0 && c.Args[0] == "verify":
		s.logf("verify data-dir")
		return "", nil
	case strings.Contains(line, "upgrade-notify"):
		kind := ""
		for i, a := range c.Args {
			if a == "--kind" {
				kind = c.Args[i+1]
			}
		}
		s.logf("notify %s", kind)
		return "", nil
	}
	rr := &recRunner{outputs: map[string]string{
		"refs/remotes/origin/main^{commit}": shaNew,
		"describe --always --tags":          "v-new",
	}}
	return rr.Run(context.Background(), c)
}

func (s *fakeServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.up || (s.failBoot && s.version != "v-old") {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintf(w, `{"status":"ok","version":%q,"schema_version":%d}`, s.version, s.schema)
	})
	mux.HandleFunc("/api/system/busy", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		fmt.Fprintf(w, `{"busy":%v,"reasons":[{"code":"print_active","detail":"1 impression(s)"}]}`, s.busy)
	})
	mux.HandleFunc("/repos/ArseneGA/BAMBULAB-MCP/commits/", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/status") {
			fmt.Fprint(w, `{"state":"success","total_count":0}`)
			return
		}
		status, concl := "completed", s.ci
		if s.ci == "in_progress" {
			status, concl = "in_progress", ""
		}
		fmt.Fprintf(w, `{"total_count":1,"check_runs":[{"name":"go","status":%q,"conclusion":%q}]}`, status, concl)
	})
	return mux
}

func (s *fakeServer) calls() string { return strings.Join(s.log, " | ") }

func newE2E(t *testing.T) (*fakeServer, *Upgrader, *SysEnv) {
	t.Helper()
	srv := &fakeServer{t: t, up: true, version: "v-old", schema: 270, dbSchema: 270, ci: "success"}
	ts := httptest.NewServer(srv.handler())
	t.Cleanup(ts.Close)
	e := newSys(t, funcRunner(srv.run))
	srv.e = e
	e.Cfg.BaseURL, e.Cfg.GitHubAPI, e.HTTP = ts.URL, ts.URL, ts.Client()
	e.Cfg.StableWait, e.Cfg.HealthWait = 0, 2*time.Second
	e.Out = os.Stderr
	// Installation historique migrée : releases/legacy-1 + current.
	legacy := filepath.Join(e.Cfg.ReleasesDir(), "legacy-1")
	if err := os.MkdirAll(filepath.Join(legacy, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(legacy, "bin", "bambud"), []byte("old"), 0o755)
	b, _ := json.Marshal(Release{SHA: shaOld, Version: "v-old", Schema: 270, BuiltAt: time.Now().Add(-24 * time.Hour)})
	_ = os.WriteFile(filepath.Join(legacy, "REVISION"), b, 0o644)
	if err := atomicSymlink(e.Cfg.CurrentLink(), "releases/legacy-1"); err != nil {
		t.Skipf("liens symboliques indisponibles ici (à jouer sur Linux) : %v", err)
	}
	cfg := e.Cfg
	cfg.Repo = "https://github.com/ArseneGA/BAMBULAB-MCP"
	e.Cfg = cfg
	return srv, &Upgrader{Cfg: cfg, Env: e, StatePath: filepath.Join(t.TempDir(), "state.json")}, e
}

type funcRunner func(context.Context, Cmd) (string, error)

func (f funcRunner) Run(ctx context.Context, c Cmd) (string, error) { return f(ctx, c) }

func linkTarget(t *testing.T, p string) string {
	t.Helper()
	s, err := os.Readlink(p)
	if err != nil {
		t.Fatalf("readlink %s : %v", p, err)
	}
	return s
}

func TestEndToEndSuccess(t *testing.T) {
	srv, u, e := newE2E(t)
	srv.migrates = true
	res := mustRun(t, u, Options{})
	if res.Outcome != OutInstalled {
		t.Fatalf("%+v\n%s", res, srv.calls())
	}
	if got := linkTarget(t, e.Cfg.CurrentLink()); got != "releases/"+shaNew {
		t.Fatalf("current -> %s", got)
	}
	if got := linkTarget(t, e.Cfg.PreviousLink()); got != "releases/legacy-1" {
		t.Fatalf("previous -> %s", got)
	}
	if !srv.up || srv.version != "v-new" || srv.dbSchema != 280 {
		t.Fatalf("service : %+v", srv)
	}
	// La sauvegarde (faite avec l'ANCIEN binaire) précède le redémarrage.
	if i, j := strings.Index(srv.calls(), "backup"), strings.Index(srv.calls(), "systemctl restart"); i < 0 || j < 0 || i > j {
		t.Fatalf("ordre : %s", srv.calls())
	}
	if !strings.Contains(srv.calls(), "backup (schéma 270) avec legacy-1") || !strings.Contains(srv.calls(), "notify installed") {
		t.Fatalf("%s", srv.calls())
	}
	// Le moteur est idempotent : au passage suivant, rien de neuf, aucun redémarrage.
	srv.log = nil
	if res := mustRun(t, u, Options{}); res.Outcome != OutUpToDate || strings.Contains(srv.calls(), "restart") {
		t.Fatalf("%+v %s", res, srv.calls())
	}
}

// Scénario demandé : /health en échec après le redémarrage, schéma inchangé → lien ramené, ancienne version relancée.
func TestEndToEndHealthFailureRollsBackWithoutTouchingData(t *testing.T) {
	srv, u, e := newE2E(t)
	srv.failBoot = true
	res := mustRun(t, u, Options{})
	if res.Outcome != OutRolledBack {
		t.Fatalf("%+v\n%s", res, srv.calls())
	}
	if got := linkTarget(t, e.Cfg.CurrentLink()); got != "releases/legacy-1" {
		t.Fatalf("current -> %s (retour arrière attendu)", got)
	}
	if strings.Contains(srv.calls(), "restore") {
		t.Fatalf("schéma inchangé : pas de restauration : %s", srv.calls())
	}
	if !srv.up || srv.version != "v-old" || srv.dbSchema != 270 {
		t.Fatalf("l'ancienne version doit tourner : %+v", srv)
	}
	if !strings.Contains(srv.calls(), "systemctl stop") || !strings.Contains(srv.calls(), "systemctl start") || !strings.Contains(srv.calls(), "notify rolled_back") {
		t.Fatalf("%s", srv.calls())
	}
	// Pas de boucle : le commit en échec n'est pas retenté.
	srv.log = nil
	if res := mustRun(t, u, Options{}); res.Outcome != OutKnownFail || strings.Contains(srv.calls(), "restart") {
		t.Fatalf("%+v %s", res, srv.calls())
	}
}

// Scénario demandé : la nouvelle version a MIGRÉ la base puis échoue : l'ancienne version refuserait de démarrer sur
// cette base ; la sauvegarde d'avant mise à jour est restaurée (service arrêté) AVANT de relancer l'ancienne version.
func TestEndToEndMigratedThenFailsRestoresBackup(t *testing.T) {
	srv, u, e := newE2E(t)
	srv.migrates, srv.failBoot = true, true
	res := mustRun(t, u, Options{})
	if res.Outcome != OutRestored {
		t.Fatalf("%+v\n%s", res, srv.calls())
	}
	calls := srv.calls()
	order := []string{"backup (schéma 270)", "systemctl restart", "systemctl stop", "restore avec legacy-1", "verify data-dir", "systemctl start", "notify restored"}
	pos := -1
	for _, o := range order {
		i := strings.Index(calls[pos+1:], o)
		if i < 0 {
			t.Fatalf("%q absent ou dans le désordre : %s", o, calls)
		}
		pos += i + 1
	}
	if got := linkTarget(t, e.Cfg.CurrentLink()); got != "releases/legacy-1" {
		t.Fatalf("current -> %s", got)
	}
	if !srv.up || srv.version != "v-old" || srv.dbSchema != 270 {
		t.Fatalf("après restauration l'ancienne version doit tourner sur le schéma 270 : %+v", srv)
	}
	if _, bad := LoadState(u.StatePath).Failed[shaNew]; !bad {
		t.Fatal("commit non marqué en échec")
	}
}

func TestEndToEndGates(t *testing.T) {
	// CI rouge : jamais construit.
	srv, u, _ := newE2E(t)
	srv.ci = "failure"
	if res := mustRun(t, u, Options{}); res.Outcome != OutCIRed || strings.Contains(srv.calls(), "restart") || strings.Contains(srv.calls(), "backup") {
		t.Fatalf("CI rouge : %+v %s", res, srv.calls())
	}
	// CI en cours : attendre.
	srv, u, _ = newE2E(t)
	srv.ci = "in_progress"
	if res := mustRun(t, u, Options{}); res.Outcome != OutWaitCI {
		t.Fatalf("CI en cours : %+v", res)
	}
	// Impression en cours : reporté, aucune sauvegarde, aucun redémarrage, version intacte.
	srv, u, e := newE2E(t)
	srv.busy = true
	res := mustRun(t, u, Options{})
	if res.Outcome != OutBusy || strings.Contains(srv.calls(), "restart") || strings.Contains(srv.calls(), "backup") || linkTarget(t, e.Cfg.CurrentLink()) != "releases/legacy-1" {
		t.Fatalf("occupé : %+v %s", res, srv.calls())
	}
	if _, ok := e.Prepared(shaNew); ok {
		t.Fatal("service occupé dès le départ : rien ne doit être construit")
	}
}
