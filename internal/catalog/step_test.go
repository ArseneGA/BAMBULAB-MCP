package catalog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

const fakeSTEP = "ISO-10303-21;\nHEADER;\nFILE_DESCRIPTION(('test'),'2;1');\nENDSEC;\nDATA;\nENDSEC;\nEND-ISO-10303-21;\n"

// fakeWorker imite le worker Python : écrit un cube de 10 mm (STL + 3MF factice) dans out_dir.
func fakeWorker(t *testing.T, calls *[]string) WorkerFunc {
	return func(ctx context.Context, command string, req map[string]any) (json.RawMessage, error) {
		*calls = append(*calls, command)
		in, _ := req["path"].(string)
		if b, err := os.ReadFile(in); err != nil || len(b) == 0 {
			t.Errorf("le worker doit recevoir une copie lisible du STEP : %v", err)
		}
		out := req["out_dir"].(string)
		stl, _ := mesh.STLBytes(mesh.Cube(10))
		_ = os.WriteFile(filepath.Join(out, "part.stl"), stl, 0o644)
		_ = os.WriteFile(filepath.Join(out, "part.3mf"), []byte("PK\x03\x04fake"), 0o644)
		return json.RawMessage(`{"files":{"stl":"part.stl","3mf":"part.3mf"},"level":"calculé","objects":[{"index":0}]}`), nil
	}
}

func lastJob(t *testing.T, h *harness, kind string) contracts.Job {
	t.Helper()
	var id string
	if err := h.st.DB().QueryRow(`SELECT id FROM jobs WHERE kind=? ORDER BY created_at DESC, rowid DESC LIMIT 1`, kind).Scan(&id); err != nil {
		t.Fatalf("aucun travail %s : %v", kind, err)
	}
	j, err := h.q.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestStepImportWithWorker(t *testing.T) {
	var calls []string
	h := newHarness(t, func(c *Config) { c.Worker = fakeWorker(t, &calls) })
	p := h.put("cao/piece.step", []byte(fakeSTEP))
	rep := h.importAll(ImportInput{Paths: []string{p}})
	if rep.Counts[StatusImported] != 1 {
		t.Fatalf("%+v", rep.Counts)
	}
	h.idle()
	if len(calls) != 1 || calls[0] != "step_import" {
		t.Fatalf("appels du worker : %v", calls)
	}
	j := lastJob(t, h, JobStepImport)
	if j.State != contracts.JobSucceeded {
		t.Fatalf("job STEP : %+v", j)
	}
	d := h.details(rep.Files[0].ModelID)
	if d.CAD == nil || d.CAD.State != StateOK || d.Analysis.State != StateOK || d.Preview.State != PreviewReady {
		t.Fatalf("cad=%+v analyse=%+v aperçu=%+v", d.CAD, d.Analysis, d.Preview)
	}
	if d.Analysis.SizeMM.X != 10 || d.Analysis.Triangles != 12 {
		t.Fatalf("l'analyse porte sur le STL dérivé : %+v", d.Analysis)
	}
	// L'original STEP reste intact ; les dérivés sont des blobs distincts.
	step := d.Files[0]
	if step.Format != contracts.FormatSTEP {
		t.Fatal(step.Format)
	}
	if err := h.blobs.Verify(step.SHA256); err != nil {
		t.Fatal(err)
	}
	stl, ok, _ := getDerived(context.Background(), h.st.DB(), step.SHA256, KindStepSTL)
	tmf, ok2, _ := getDerived(context.Background(), h.st.DB(), step.SHA256, KindStep3MF)
	if !ok || !ok2 || stl.BlobSHA == "" || stl.BlobSHA == step.SHA256 || tmf.BlobSHA == "" || !h.blobs.Has(stl.BlobSHA) {
		t.Fatalf("dérivés : %+v %+v", stl, tmf)
	}
	b, _ := os.ReadFile(p)
	if string(b) != fakeSTEP {
		t.Fatal("original modifié")
	}
	// analyze_geometry sur un STEP : fondé sur le maillage tessellé, signalé.
	a, err := h.svc.AnalyzeGeometry(context.Background(), AnalyzeInput{ModelID: d.Model.ID})
	if err != nil || a.State != StateOK || len(a.Caveats) == 0 {
		t.Fatalf("%+v %v", a, err)
	}
	found := false
	for _, c := range a.Caveats {
		if len(c) > 4 && c[:4] == "STEP" {
			found = true
		}
	}
	if !found {
		t.Fatalf("la tessellation doit être signalée : %v", a.Caveats)
	}
}

func TestStepBackendMissing(t *testing.T) {
	h := newHarness(t, nil) // UVPath inexistant, pas de worker injecté
	if ok, note := h.svc.CADStatus(); ok || note == "" {
		t.Fatalf("CADStatus : %v %q", ok, note)
	}
	rep := h.importAll(ImportInput{Paths: []string{h.put("cao/piece.step", []byte(fakeSTEP))}})
	h.idle()
	j := lastJob(t, h, JobStepImport)
	if j.State != contracts.JobFailed || j.Error == nil || j.Error.Code != "cad_backend_missing" {
		t.Fatalf("job : %+v", j)
	}
	d := h.details(rep.Files[0].ModelID)
	if d.CAD == nil || d.CAD.State != StateBackendMissing || d.Preview.State != PreviewBackendMissing || d.Analysis.State != StateBackendMissing {
		t.Fatalf("cad=%+v aperçu=%+v analyse=%+v", d.CAD, d.Preview, d.Analysis)
	}
	a, err := h.svc.AnalyzeGeometry(context.Background(), AnalyzeInput{ModelID: d.Model.ID})
	if err != nil || a.State != StateBackendMissing {
		t.Fatalf("%+v %v", a, err)
	}
	pa, err := h.svc.GetPreviewAsset(context.Background(), PreviewAssetInput{ModelID: d.Model.ID})
	if err != nil || pa.Preview.State != PreviewBackendMissing || pa.Preview.URL != "" {
		t.Fatalf("%+v %v", pa, err)
	}
	// Le fichier STEP original est quand même conservé et disponible.
	if d.Files[0].Availability != contracts.AvailableLinux || !h.blobs.Has(d.Files[0].SHA256) {
		t.Fatal("l'original STEP doit être conservé même sans backend CAO")
	}
}

func TestStepWorkerErrors(t *testing.T) {
	cases := map[string]struct {
		err       error
		wantState string
		wantCode  string
	}{
		"ocp absent":   {&WorkerError{Code: "step_backend_missing", Message: "OCP non installé"}, StateBackendMissing, "cad_backend_missing"},
		"échec métier": {&WorkerError{Code: "corrupt_file", Message: "STEP illisible"}, StateError, "corrupt_file"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, func(cfg *Config) {
				cfg.Worker = func(context.Context, string, map[string]any) (json.RawMessage, error) { return nil, c.err }
			})
			rep := h.importAll(ImportInput{Paths: []string{h.put("cao/x.stp", []byte(fakeSTEP))}})
			h.idle()
			j := lastJob(t, h, JobStepImport)
			if j.State != contracts.JobFailed || j.Error.Code != c.wantCode {
				t.Fatalf("%+v", j)
			}
			if d := h.details(rep.Files[0].ModelID); d.CAD.State != c.wantState {
				t.Fatalf("%+v", d.CAD)
			}
		})
	}
}

func TestStepWorkerPathEscapeRefused(t *testing.T) {
	h := newHarness(t, func(cfg *Config) {
		cfg.Worker = func(_ context.Context, _ string, req map[string]any) (json.RawMessage, error) {
			return json.RawMessage(`{"files":{"stl":"../../../../etc/passwd"}}`), nil
		}
	})
	h.importAll(ImportInput{Paths: []string{h.put("cao/y.step", []byte(fakeSTEP))}})
	h.idle()
	if j := lastJob(t, h, JobStepImport); j.State != contracts.JobFailed || j.Error.Code != "cad_output" {
		t.Fatalf("%+v", j)
	}
}

// TestRealWorker n'est exécuté qu'avec BAMBU_TEST_REAL_WORKER=1 (uv + CadQuery installés).
func TestRealWorkerParametric(t *testing.T) {
	if os.Getenv("BAMBU_TEST_REAL_WORKER") == "" {
		t.Skip("worker Python réel : définir BAMBU_TEST_REAL_WORKER=1")
	}
	h := newHarness(t, func(c *Config) { c.UVPath, c.WorkerDir = "", filepath.Join("..", "..", "workers", "geometry") })
	if ok, note := h.svc.CADStatus(); !ok {
		t.Skip(note)
	}
	dir := t.TempDir()
	raw, err := h.svc.worker()(context.Background(), "parametric", map[string]any{"template": "spacer", "params": map[string]any{"outer_diameter": 20.0, "inner_diameter": 8.0, "height": 10.0}, "out_dir": dir, "name": "spacer"})
	if err != nil {
		t.Skipf("worker indisponible : %v", err)
	}
	if err := moveWorkerOutputs(raw, dir, filepath.Join(dir, "dst"), "spacer"); err != nil {
		t.Fatal(err)
	}
}
