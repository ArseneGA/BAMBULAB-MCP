package catalog

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain : le binaire de test sert aussi de faux `uv` (BAMBU_FAKE_UV=1). Il enregistre ses arguments et
// quelques variables d'environnement dans BAMBU_FAKE_UV_OUT, puis répond selon la commande du worker.
func TestMain(m *testing.M) {
	if os.Getenv("BAMBU_FAKE_UV") == "1" {
		fakeUV()
		return
	}
	os.Exit(m.Run())
}

func fakeUV() {
	in, _ := io.ReadAll(os.Stdin)
	rec := map[string]any{
		"args": os.Args[1:], "stdin": string(in),
		"uv_project_environment": os.Getenv("UV_PROJECT_ENVIRONMENT"),
		"dont_write_bytecode":    os.Getenv("PYTHONDONTWRITEBYTECODE"),
	}
	if out := os.Getenv("BAMBU_FAKE_UV_OUT"); out != "" {
		b, _ := json.Marshal(rec)
		_ = os.WriteFile(out, b, 0o600)
	}
	cmd := os.Args[len(os.Args)-1]
	switch {
	case os.Getenv("BAMBU_FAKE_UV_FAIL") == "1":
		os.Stdout.WriteString(`{"ok":false,"error":{"code":"cad_unavailable","message":"CadQuery/OCP indisponible : No module named cadquery"}}` + "\n")
		os.Exit(1)
	case cmd == "ping":
		os.Stdout.WriteString(`{"ok":true,"result":{"cadquery":"2.5.2","python":"3.12.1"}}` + "\n")
	default:
		os.Stdout.WriteString(`{"ok":true,"result":{}}` + "\n")
	}
	os.Exit(0)
}

// fakeBackend prépare un faux uv (ce binaire de test) et un projet worker.
func fakeBackend(t *testing.T) (uv, project, out string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	project = t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "pyproject.toml"), []byte("[project]\nname='x'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = filepath.Join(t.TempDir(), "rec.json")
	t.Setenv("BAMBU_FAKE_UV", "1")
	t.Setenv("BAMBU_FAKE_UV_OUT", out)
	return exe, project, out
}

func readRec(t *testing.T, out string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("le faux uv n'a pas été lancé : %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func listDir(t *testing.T, dir string) string {
	t.Helper()
	var names []string
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil {
			names = append(names, p)
		}
		return nil
	})
	return strings.Join(names, "\n")
}

// uv run --frozen --no-dev ; venv hors du projet ; rien n'est écrit dans BAMBU_GEOMETRY_DIR.
func TestExecWorkerFrozenNoDevAndNeverWritesProject(t *testing.T) {
	uv, project, out := fakeBackend(t)
	t.Setenv("UV_PROJECT_ENVIRONMENT", "")
	t.Setenv("PYTHONDONTWRITEBYTECODE", "")
	h := newHarness(t, func(c *Config) { c.UVPath, c.WorkerDir = uv, project })
	before := listDir(t, project)
	if _, err := h.svc.execWorker(t.Context(), "parametric", map[string]any{"template": "box"}); err != nil {
		t.Fatal(err)
	}
	rec := readRec(t, out)
	args := strings.Join(toStrings(rec["args"]), " ")
	want := "run --frozen --no-dev --project " + project + " python -m bambu_geometry parametric"
	if args != want {
		t.Errorf("arguments : %q, attendu %q", args, want)
	}
	if got, want := rec["uv_project_environment"], filepath.Join(h.data, "venvs", "geometry"); got != want {
		t.Errorf("UV_PROJECT_ENVIRONMENT par défaut : %v, attendu %v (hors du projet)", got, want)
	}
	if rec["dont_write_bytecode"] != "1" {
		t.Error("PYTHONDONTWRITEBYTECODE devrait valoir 1")
	}
	if after := listDir(t, project); after != before {
		t.Errorf("le dossier du projet a été modifié :\n%s\n--\n%s", before, after)
	}
}

func TestExecWorkerRespectsProvidedUVProjectEnvironment(t *testing.T) {
	uv, project, out := fakeBackend(t)
	venv := filepath.Join(t.TempDir(), "mon-venv")
	t.Setenv("UV_PROJECT_ENVIRONMENT", venv)
	h := newHarness(t, func(c *Config) { c.UVPath, c.WorkerDir = uv, project })
	if _, err := h.svc.execWorker(t.Context(), "step_import", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if got := readRec(t, out)["uv_project_environment"]; got != venv {
		t.Errorf("UV_PROJECT_ENVIRONMENT fourni non respecté : %v", got)
	}
}

func TestExecWorkerKeepsExistingProjectVenv(t *testing.T) {
	uv, project, out := fakeBackend(t)
	t.Setenv("UV_PROJECT_ENVIRONMENT", "")
	if err := os.Mkdir(filepath.Join(project, ".venv"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, func(c *Config) { c.UVPath, c.WorkerDir = uv, project })
	if _, err := h.svc.execWorker(t.Context(), "step_import", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if got := readRec(t, out)["uv_project_environment"]; got != "" {
		t.Errorf("un .venv existant devait être gardé tel quel : %v", got)
	}
}

// Un dossier de projet en lecture seule (installation /opt) ne gêne pas : rien n'y est écrit.
func TestExecWorkerReadOnlyProjectDir(t *testing.T) {
	if os.PathSeparator == '\\' || os.Geteuid() == 0 {
		t.Skip("droits de lecture seule non vérifiables ici (Windows ou root)")
	}
	uv, project, _ := fakeBackend(t)
	t.Setenv("UV_PROJECT_ENVIRONMENT", "")
	if err := os.Chmod(project, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(project, 0o755) })
	h := newHarness(t, func(c *Config) { c.UVPath, c.WorkerDir = uv, project })
	if _, err := h.svc.execWorker(t.Context(), "parametric", map[string]any{}); err != nil {
		t.Fatalf("projet en lecture seule : %v", err)
	}
}

// CAD réellement vérifié : ping du worker (import de cadquery), résultat mis en cache.
func TestCADHealthPingAndCache(t *testing.T) {
	uv, project, out := fakeBackend(t)
	h := newHarness(t, func(c *Config) { c.UVPath, c.WorkerDir = uv, project })
	if ok, note := h.svc.CADHealth(); ok || !strings.Contains(note, "en cours") {
		t.Fatalf("avant le ping : %v %q", ok, note)
	}
	waitCAD(t, h.svc, true)
	ok, note := h.svc.CADHealth()
	if !ok || !strings.Contains(note, "CadQuery 2.5.2") {
		t.Fatalf("après le ping : %v %q", ok, note)
	}
	if args := toStrings(readRec(t, out)["args"]); args[len(args)-1] != "ping" {
		t.Errorf("commande lancée : %v", args)
	}
	// Mis en cache : on supprime l'enregistrement, un nouvel appel ne relance pas uv.
	_ = os.Remove(out)
	h.svc.CADHealth()
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(out); err == nil {
		t.Error("ping relancé alors que le résultat est en cache")
	}
}

func TestCADHealthReportsMissingCadQuery(t *testing.T) {
	uv, project, _ := fakeBackend(t)
	t.Setenv("BAMBU_FAKE_UV_FAIL", "1")
	h := newHarness(t, func(c *Config) { c.UVPath, c.WorkerDir = uv, project })
	h.svc.CADHealth()
	waitCAD(t, h.svc, false)
	ok, note := h.svc.CADHealth()
	if ok || !strings.Contains(note, "CadQuery indisponible") || !strings.Contains(note, "cadquery") {
		t.Fatalf("ping en échec : %v %q", ok, note)
	}
	// uv présent mais CadQuery absent : CADStatus (présence seule) reste vrai, CADHealth (réel) dit non.
	if ok, _ := h.svc.CADStatus(); !ok {
		t.Error("CADStatus doit rester « uv + projet présents »")
	}
}

func waitCAD(t *testing.T, s *Service, wantKnownOK bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		s.cad.mu.Lock()
		known := s.cad.known
		s.cad.mu.Unlock()
		if known {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("le ping n'a pas répondu (attendu ok=%v)", wantKnownOK)
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

// Racines d'import : jamais le dossier de données, ni un parent, ni un enfant.
func TestImportRootCannotContainOrLiveInDataDir(t *testing.T) {
	data := canon(t, t.TempDir())
	inside := filepath.Join(data, "secrets")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(data)
	for name, root := range map[string]string{"égale": data, "enfant (secrets)": inside, "parent": parent} {
		if err := checkRootAgainstDataDir(root, data); err == nil {
			t.Errorf("racine %s (%s) acceptée", name, root)
		}
	}
	other := canon(t, t.TempDir())
	if err := checkRootAgainstDataDir(other, data); err != nil {
		t.Errorf("racine distincte refusée : %v", err)
	}
}
