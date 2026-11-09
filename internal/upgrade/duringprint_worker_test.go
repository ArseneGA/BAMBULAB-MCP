package upgrade

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ---- Mise à jour pendant une impression (ATELIER_UPDATE_ALLOW_DURING_PRINT) ----------------------------------------------

func TestApplyBusyPolicy(t *testing.T) {
	cases := []struct {
		name      string
		allow     bool
		in        BusyState
		wantBusy  bool
		blocking  []string
		tolerated int
	}{
		{"impression en cours tolérée", true, BusyState{Busy: true, Reasons: []string{"print_running: 1 impression(s), dont pj_1 (PRINTING)"}}, false, nil, 1},
		{"imprimante au travail tolérée", true, BusyState{Busy: true, Reasons: []string{"printer_active: imprimante pr_1 : printing"}}, false, nil, 1},
		{"dépôt FTPS bloque", true, BusyState{Busy: true, Reasons: []string{"print_running: 1", "deposit_in_progress: 1 impression(s), dont pj_2 (UPLOADING)"}}, true, []string{"deposit_in_progress: 1 impression(s), dont pj_2 (UPLOADING)"}, 1},
		{"confirmation en attente bloque", true, BusyState{Busy: true, Reasons: []string{"print_running: 1", "confirmation_pending: 1 confirmation(s)"}}, true, []string{"confirmation_pending: 1 confirmation(s)"}, 1},
		{"arrêt en attente bloque", true, BusyState{Busy: true, Reasons: []string{"stop_pending: 1"}}, true, []string{"stop_pending: 1"}, 0},
		{"action en attente bloque", true, BusyState{Busy: true, Reasons: []string{"action_pending: 1"}}, true, []string{"action_pending: 1"}, 0},
		{"lancement en cours d'accusé bloque", true, BusyState{Busy: true, Reasons: []string{"print_active: 1 impression(s), dont pj_3 (START_REQUESTED)"}}, true, []string{"print_active: 1 impression(s), dont pj_3 (START_REQUESTED)"}, 0},
		{"travail lourd (découpe) bloque", true, BusyState{Busy: true, Reasons: []string{"job_running: 1 travail(aux) en cours, dont 1 lourd(s) (slice)"}}, true, []string{"job_running: 1 travail(aux) en cours, dont 1 lourd(s) (slice)"}, 0},
		{"raison inconnue bloque", true, BusyState{Busy: true, Reasons: []string{"nouveau_code: x"}}, true, []string{"nouveau_code: x"}, 0},
		{"option désactivée : l'impression bloque", false, BusyState{Busy: true, Reasons: []string{"print_running: 1"}}, true, []string{"print_running: 1"}, 0},
		{"occupé sans raison lisible : on croit le service", true, BusyState{Busy: true}, true, nil, 0},
		{"libre", true, BusyState{}, false, nil, 0},
	}
	for _, c := range cases {
		got := ApplyBusyPolicy(c.in, c.allow)
		if got.Busy != c.wantBusy || len(got.Tolerated) != c.tolerated || !reflect.DeepEqual(got.Reasons, c.blocking) {
			t.Errorf("%s : %+v (attendu busy=%v, bloquantes=%v, %d tolérée(s))", c.name, got, c.wantBusy, c.blocking, c.tolerated)
		}
	}
	// Occupation inconnue : toujours occupée, quelle que soit la politique.
	if got := ApplyBusyPolicy(BusyState{Unknown: true, Reasons: []string{"service injoignable"}}, true); !got.Unknown || len(got.Tolerated) != 0 {
		t.Fatalf("inconnu : %+v", got)
	}
	// La décision : seule la raison bloquante retarde l'installation.
	d := Decide(Inputs{Target: shaNew, Current: shaOld, CI: CIResult{State: CIGreen, Detail: "ok"}, Busy: ApplyBusyPolicy(BusyState{Busy: true, Reasons: []string{"print_running: 1"}}, true)})
	if d.Action != ActInstall {
		t.Fatalf("une impression en cours ne doit plus retarder la mise à jour : %+v", d)
	}
	d = Decide(Inputs{Target: shaNew, Current: shaOld, CI: CIResult{State: CIGreen, Detail: "ok"}, Busy: ApplyBusyPolicy(BusyState{Busy: true, Reasons: []string{"print_running: 1", "deposit_in_progress: 1"}}, true)})
	if d.Action != ActBusy || !strings.Contains(d.Reason, "deposit_in_progress") || strings.Contains(d.Reason, "print_running") {
		t.Fatalf("un dépôt en cours doit retarder la mise à jour : %+v", d)
	}
}

func TestLoadConfigAllowDuringPrint(t *testing.T) {
	conf := t.TempDir()
	none := func(string) string { return "" }
	c, err := LoadConfig(conf, none)
	if err != nil || !c.AllowDuringPrint {
		t.Fatalf("défaut : la mise à jour pendant une impression est autorisée : %+v %v", c.AllowDuringPrint, err)
	}
	for _, v := range []string{"0", "false", "off", "non"} {
		c, err = LoadConfig(conf, func(k string) string {
			if k == "ATELIER_UPDATE_ALLOW_DURING_PRINT" {
				return v
			}
			return ""
		})
		if err != nil || c.AllowDuringPrint {
			t.Fatalf("%q doit désactiver : %v %v", v, c.AllowDuringPrint, err)
		}
	}
	writeFile(t, filepath.Join(conf, ConfFileName), "ATELIER_UPDATE_ALLOW_DURING_PRINT=\"0\"\n", 0o644)
	if c, _ = LoadConfig(conf, none); c.AllowDuringPrint {
		t.Fatal("auto-update.env doit pouvoir désactiver")
	}
}

// SysEnv.Busy applique la politique à ce que répond le service, et la base seule (repli) emploie les mêmes codes.
func TestSysEnvBusyAllowsPrintButNotDeposit(t *testing.T) {
	reply := `{"busy":true,"reasons":[{"code":"print_running","detail":"1 impression(s), dont pj_1 (PRINTING)"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, reply) }))
	defer srv.Close()
	e := newSys(t, &recRunner{})
	e.Cfg.BaseURL, e.HTTP = srv.URL, srv.Client()
	ctx := context.Background()

	e.Cfg.AllowDuringPrint = true
	if st := e.Busy(ctx); st.Busy || len(st.Tolerated) != 1 || !strings.Contains(st.Tolerated[0], "pj_1") {
		t.Fatalf("impression en cours : %+v", st)
	}
	e.Cfg.AllowDuringPrint = false
	if st := e.Busy(ctx); !st.Busy {
		t.Fatalf("option désactivée : %+v", st)
	}
	e.Cfg.AllowDuringPrint = true
	reply = `{"busy":true,"reasons":[{"code":"print_running","detail":"1"},{"code":"deposit_in_progress","detail":"1 impression(s), dont pj_2 (UPLOADING)"},{"code":"confirmation_pending","detail":"1"}]}`
	st := e.Busy(ctx)
	if !st.Busy || len(st.Reasons) != 2 || len(st.Tolerated) != 1 {
		t.Fatalf("dépôt et confirmation : %+v", st)
	}
	// Repli sur la base (service ancien ou arrêté) : codes de la sonde.
	for state, code := range map[string]string{"PRINTING": "print_running", "START_REQUESTED": "print_active", "START_UNKNOWN": "print_active", "UPLOADING": "deposit_in_progress", "SLICING": "deposit_in_progress"} {
		if got := probeCode(state); got != code {
			t.Errorf("probeCode(%s) = %s, attendu %s", state, got, code)
		}
	}
}

// ---- Runner : dossier de départ sous un autre utilisateur -------------------------------------------------------------------

func TestEffectiveDirStartsFromRootUnderOtherUser(t *testing.T) {
	if got := effectiveDir(Cmd{Name: "go", User: "atelier-update"}, true); got != "/" {
		t.Fatalf("sous runuser sans dossier : %q, attendu /", got)
	}
	if got := effectiveDir(Cmd{Name: "go", User: "atelier-update", Dir: "/var/lib/atelier-update"}, true); got != "/var/lib/atelier-update" {
		t.Fatalf("dossier explicite : %q", got)
	}
	if got := effectiveDir(Cmd{Name: "systemctl"}, true); got != "" {
		t.Fatalf("root : dossier inchangé : %q", got)
	}
	if got := effectiveDir(Cmd{Name: "go", User: "x"}, false); got != "" {
		t.Fatalf("sans changement d'utilisateur : %q", got)
	}
}

// ---- Worker CAO ---------------------------------------------------------------------------------------------------------

func TestSplitSystemdWordsAndShow(t *testing.T) {
	got := splitSystemdWords(`UV_PROJECT_ENVIRONMENT=/var/lib/atelier/venvs/geometry UV_FROZEN=1 "NOTE=a b" 'X=y z' PLAIN=ok`)
	want := []string{"UV_PROJECT_ENVIRONMENT=/var/lib/atelier/venvs/geometry", "UV_FROZEN=1", "NOTE=a b", "X=y z", "PLAIN=ok"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
	env, files := ParseSystemctlShow("Environment=UV_PROJECT_ENVIRONMENT=/var/lib/atelier/venvs/geometry UV_FROZEN=1\nEnvironmentFiles=/etc/atelier/atelier.env (ignore_errors=no)\nEnvironmentFiles=-/etc/atelier/extra.env (ignore_errors=yes)\n")
	if env["UV_PROJECT_ENVIRONMENT"] != "/var/lib/atelier/venvs/geometry" || env["UV_FROZEN"] != "1" {
		t.Fatalf("env : %v", env)
	}
	if !reflect.DeepEqual(files, []string{"/etc/atelier/atelier.env", "/etc/atelier/extra.env"}) {
		t.Fatalf("fichiers : %v", files)
	}
}

// Empreinte de référence calculée avec workers/geometry/src/bambu_geometry/fingerprint.py sur le même arbre :
// a.py = « a = 1\n », sub/b.py = « b = 2\n » ; __pycache__/z.py et note.txt sont ignorés.
func TestWorkerFingerprintMatchesPython(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"a.py": "a = 1\n", "sub/b.py": "b = 2\n", "__pycache__/z.py": "x", "note.txt": "ignored"} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := WorkerFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := "c4ac246b9048196757cf87303c08949bddc9e15e35a0bbdc7f592b86cf5bf562"; got != want {
		t.Fatalf("empreinte %s, attendue %s (Python)", got, want)
	}
	if _, err := WorkerFingerprint(t.TempDir()); err == nil {
		t.Fatal("dossier sans .py : erreur attendue")
	}
}

// workerRunner : enregistre les commandes, copie les arbres (cp -a), simule systemctl show, uv sync et le ping du worker.
type workerRunner struct {
	cmds    []Cmd
	show    string // sortie de systemctl show
	ping    string // sortie du ping
	syncErr error
}

func (r *workerRunner) Run(_ context.Context, c Cmd) (string, error) {
	r.cmds = append(r.cmds, c)
	switch {
	case c.Name == "systemctl" && len(c.Args) > 0 && c.Args[0] == "show":
		return r.show, nil
	case c.Name == "chown":
		return "", nil
	case c.Name == "cp":
		return "", copyTree(c.Args[len(c.Args)-2], c.Args[len(c.Args)-1])
	case strings.HasSuffix(c.Name, "/uv") && len(c.Args) > 0 && c.Args[0] == "sync":
		return "", r.syncErr
	case strings.HasSuffix(c.Name, "/uv") && len(c.Args) > 0 && c.Args[0] == "run":
		return "INFO command=ping\n" + r.ping + "\n", nil
	}
	return "", nil
}

func (r *workerRunner) find(prefix string) *Cmd {
	for i := range r.cmds {
		if strings.HasPrefix(r.cmds[i].Name+" "+strings.Join(r.cmds[i].Args, " "), prefix) {
			return &r.cmds[i]
		}
	}
	return nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

func envOf(c *Cmd, key string) string {
	for _, kv := range c.Env {
		if strings.HasPrefix(kv, key+"=") {
			return strings.TrimPrefix(kv, key+"=")
		}
	}
	return ""
}

// En production, le venv est hors du dossier du worker (drop-in systemd) : la mise à jour lit l'environnement du SERVICE, lance
// `uv sync --frozen --no-dev --extra cad --reinstall-package bambu-geometry` avec UV_PROJECT_ENVIRONMENT / UV_FROZEN, et refait la
// réinstallation depuis l'ancien code au retour arrière.
func TestSyncGeometryUsesServiceEnvAndReinstallsPackage(t *testing.T) {
	rr := &workerRunner{show: "Environment=UV_PROJECT_ENVIRONMENT=/var/lib/atelier/venvs/geometry UV_FROZEN=1\nEnvironmentFiles=/etc/atelier/atelier.env (ignore_errors=no)\n"}
	e := newSys(t, rr)
	cad := filepath.Join(e.Cfg.Root, "cad", "geometry")
	e.Cfg.CadDir = cad
	e.Cfg.UsesCAD = true
	e.Cfg.AppEnv = map[string]string{"BAMBU_UV_PATH": "/opt/atelier/bin/uv", "UV_CACHE_DIR": "/var/lib/atelier/home/.cache/uv"}
	if err := os.MkdirAll(cad, 0o755); err != nil { // pas de .venv : l'environnement est ailleurs
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(cad, "worker.py"), []byte("ancien"), 0o644)
	_ = os.WriteFile(e.geomMarker(), []byte("tree-old\n"), 0o644)
	rel := Release{SHA: shaNew, Dir: filepath.Join(e.Cfg.ReleasesDir(), "n"), GeometryTree: "tree-new"}
	_ = os.MkdirAll(filepath.Join(rel.Dir, "geometry"), 0o755)
	_ = os.WriteFile(filepath.Join(rel.Dir, "geometry", "worker.py"), []byte("nouveau"), 0o644)

	ctx := context.Background()
	if err := e.SyncGeometry(ctx, rel); err != nil {
		t.Fatal(err)
	}
	sync := rr.find("/opt/atelier/bin/uv sync")
	if sync == nil {
		t.Fatalf("uv sync non lancé : %v", rr.cmds)
	}
	if got := strings.Join(sync.Args, " "); got != "sync --frozen --no-dev --extra cad --reinstall-package bambu-geometry" {
		t.Fatalf("arguments : %s", got)
	}
	if sync.User != "atelier" || sync.Dir != cad {
		t.Fatalf("utilisateur / dossier : %s %s", sync.User, sync.Dir)
	}
	if envOf(sync, "UV_PROJECT_ENVIRONMENT") != "/var/lib/atelier/venvs/geometry" || envOf(sync, "UV_FROZEN") != "1" || envOf(sync, "UV_CACHE_DIR") == "" {
		t.Fatalf("environnement du service non repris : %v", sync.Env)
	}
	// Retour arrière : code d'avant, puis réinstallation du paquet depuis ce code.
	rr.cmds = nil
	if err := e.RevertGeometry(ctx); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(cad, "worker.py")); string(b) != "ancien" {
		t.Fatalf("code non restauré : %s", b)
	}
	if rr.find("/opt/atelier/bin/uv sync --frozen --no-dev --extra cad --reinstall-package bambu-geometry") == nil {
		t.Fatalf("réinstallation de l'ancien paquet absente : %v", rr.cmds)
	}
}

// Sans UV_PROJECT_ENVIRONMENT ni .venv : même règle que le service (<données>/venvs/geometry).
func TestWorkerEnvDefaultsLikeTheService(t *testing.T) {
	rr := &workerRunner{show: ""}
	e := newSys(t, rr)
	cad := filepath.Join(e.Cfg.Root, "cad", "geometry")
	_ = os.MkdirAll(cad, 0o755)
	env := strings.Join(e.workerEnv(context.Background(), cad), "\n")
	if !strings.Contains(env, "UV_PROJECT_ENVIRONMENT="+filepath.Join(e.Cfg.DataDir, "venvs", "geometry")) {
		t.Fatalf("défaut : %s", env)
	}
	_ = os.MkdirAll(filepath.Join(cad, ".venv"), 0o755)
	if env = strings.Join(e.workerEnv(context.Background(), cad), "\n"); strings.Contains(env, "UV_PROJECT_ENVIRONMENT") {
		t.Fatalf(".venv présent : aucun défaut : %s", env)
	}
}

// Le test de fumée vérifie la VERSION du worker, pas seulement qu'il répond.
func TestCheckWorkerVersion(t *testing.T) {
	rr := &workerRunner{show: "Environment=UV_PROJECT_ENVIRONMENT=/var/lib/atelier/venvs/geometry UV_FROZEN=1\n"}
	e := newSys(t, rr)
	e.Cfg.CadDir = filepath.Join(e.Cfg.Root, "cad", "geometry")
	e.Cfg.UsesCAD = true
	e.Cfg.AppEnv = map[string]string{"BAMBU_UV_PATH": "/opt/atelier/bin/uv"}
	pkg := filepath.Join(e.Cfg.CurrentLink(), "geometry", "src", "bambu_geometry") // « current » est un dossier ici : pas besoin de lien
	_ = os.MkdirAll(pkg, 0o755)
	_ = os.WriteFile(filepath.Join(pkg, "__init__.py"), []byte("__version__ = \"0.1.0\"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(pkg, "cad.py"), []byte("# nouveau code\n"), 0o644)
	want, err := WorkerFingerprint(pkg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	rr.ping = fmt.Sprintf(`{"ok": true, "result": {"cadquery": "2.4", "python": "3.12", "worker": {"version": "0.1.0", "code_sha256": %q, "path": "/var/lib/atelier/venvs/geometry/lib/site-packages/bambu_geometry"}}}`, want)
	if err := e.checkWorkerVersion(ctx); err != nil {
		t.Fatalf("worker conforme : %v", err)
	}
	run := rr.find("/opt/atelier/bin/uv run --frozen --no-dev --extra cad --project " + e.Cfg.CadDir + " python -m bambu_geometry ping")
	if run == nil || run.User != "atelier" || run.Stdin == "" || envOf(run, "UV_PROJECT_ENVIRONMENT") != "/var/lib/atelier/venvs/geometry" {
		t.Fatalf("le ping doit utiliser l'environnement du service, sous son utilisateur : %+v", run)
	}

	// Paquet non éditable resté sur l'ancien code : le ping répond, mais avec une autre empreinte -> échec.
	rr.ping = `{"ok": true, "result": {"worker": {"version": "0.1.0", "code_sha256": "deadbeefdeadbeef", "path": "/venv/bambu_geometry"}}}`
	if err := e.checkWorkerVersion(ctx); err == nil || !strings.Contains(err.Error(), "autre code") {
		t.Fatalf("paquet périmé non détecté : %v", err)
	}
	// Ancien worker sans empreinte (ping « simple ») : échec aussi.
	rr.ping = `{"ok": true, "result": {"cadquery": "2.4", "python": "3.12"}}`
	if err := e.checkWorkerVersion(ctx); err == nil || !strings.Contains(err.Error(), "empreinte") {
		t.Fatalf("worker sans empreinte : %v", err)
	}
	// Réponse en erreur ou illisible.
	rr.ping = `{"ok": false, "error": {"code": "cad_unavailable", "message": "OCP absent"}}`
	if err := e.checkWorkerVersion(ctx); err == nil || !strings.Contains(err.Error(), "cad_unavailable") {
		t.Fatalf("erreur du worker : %v", err)
	}
	rr.ping = `pas du json`
	if err := e.checkWorkerVersion(ctx); err == nil {
		t.Fatal("réponse illisible acceptée")
	}
	// Pas de worker configuré : rien à vérifier.
	e.Cfg.AppEnv = map[string]string{}
	if err := e.checkWorkerVersion(ctx); err != nil {
		t.Fatalf("sans worker : %v", err)
	}
}
