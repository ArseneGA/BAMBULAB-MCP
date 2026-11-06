package catalog

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type harness struct {
	t      *testing.T
	svc    *Service
	st     *store.Store
	q      *jobs.Queue
	reg    *jobs.Registry
	blobs  *store.BlobStore
	data   string
	root   string
	cancel context.CancelFunc
	done   chan struct{}
}

func canon(t *testing.T, p string) string {
	t.Helper()
	c, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// newHarness ouvre base, blobs, file et catalogue (sans cœur) et démarre un runner.
func newHarness(t *testing.T, mod func(*Config)) *harness {
	t.Helper()
	h := open(t, canon(t, t.TempDir()), canon(t, t.TempDir()), mod)
	h.start()
	return h
}

// open ouvre les composants sur des dossiers donnés, sans runner.
func open(t *testing.T, data, root string, mod func(*Config)) *harness {
	t.Helper()
	h := &harness{t: t, data: data, root: root}
	st, err := store.Open(filepath.Join(data, "bambu.db"), store.Options{Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := store.NewBlobStore(filepath.Join(data, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	q := jobs.NewQueue(st, jobs.QueueConfig{})
	reg := jobs.NewRegistry()
	cfg := Config{ImportRoots: []string{root}, UVPath: filepath.Join(root, "absent", "uv.exe")}
	if mod != nil {
		mod(&cfg)
	}
	svc, err := New(Deps{Store: st, Blobs: blobs, Queue: q, Registry: reg, Log: quiet(), DataDir: data}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.svc, h.st, h.q, h.reg, h.blobs = svc, st, q, reg, blobs
	t.Cleanup(h.stop)
	return h
}

func (h *harness) start() {
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.done = make(chan struct{})
	r := jobs.NewRunner(h.q, h.reg, jobs.RunnerConfig{PollInterval: 10 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond, Logger: quiet()})
	go func() { defer close(h.done); _ = r.Run(ctx) }()
}

// stop arrête le runner puis ferme la base (idempotent).
func (h *harness) stop() {
	if h.cancel != nil {
		h.cancel()
		<-h.done
		h.cancel = nil
	}
	if h.st != nil {
		h.st.Close()
		h.st = nil
	}
}

func (h *harness) idle() {
	h.t.Helper()
	if !h.svc.Idle(context.Background(), 30*time.Second) {
		h.t.Fatal("les travaux ne se terminent pas")
	}
}

// testdata lit un fichier de internal/mesh/testdata (vrais STL/OBJ/3MF).
func testdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "mesh", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// put écrit un fichier sous la racine et renvoie son chemin.
func (h *harness) put(rel string, data []byte) string {
	h.t.Helper()
	p := filepath.Join(h.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		h.t.Fatal(err)
	}
	return p
}

func cubeSTL(t *testing.T, s float64) []byte {
	t.Helper()
	b, err := mesh.STLBytes(mesh.Cube(s))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (h *harness) importAll(in ImportInput) *ImportReport {
	h.t.Helper()
	in.WaitSecs = 30
	r, err := h.svc.StartImportWait(context.Background(), in)
	if err != nil {
		h.t.Fatal(err)
	}
	if !r.Done || r.Report == nil {
		h.t.Fatalf("import non terminé : %+v", r)
	}
	return r.Report
}

func (h *harness) modelCount() int {
	h.t.Helper()
	var n int
	if err := h.st.DB().QueryRow(`SELECT COUNT(*) FROM models`).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

func (h *harness) fileCount() int {
	h.t.Helper()
	var n int
	if err := h.st.DB().QueryRow(`SELECT COUNT(*) FROM source_files`).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

func (h *harness) jobCount() int {
	h.t.Helper()
	var n int
	if err := h.st.DB().QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

func (h *harness) details(id string) *ModelDetails {
	h.t.Helper()
	d, err := h.svc.GetModelDetails(context.Background(), id)
	if err != nil {
		h.t.Fatal(err)
	}
	return d
}

var _ = contracts.NewID
