package goals

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

const (
	p1sMachine  = "Bambu Lab P1S 0.4 nozzle"
	p1sProcess  = "0.20mm Standard @BBL X1C"
	p1sFilament = "Bambu PLA Basic @BBL P1S 0.4 nozzle"
)

// installProfiles charge les profils de l'installation de Bambu Studio, sinon ignore le test.
func installProfiles(t *testing.T) *profiles.Store {
	t.Helper()
	profOnce.Do(func() {
		bin, err := slicer.Locate("")
		if err != nil {
			profErr = fmt.Errorf("Bambu Studio absent : %v", err)
			return
		}
		d := bin.ProfilesDir()
		if d == "" {
			profErr = errors.New("profils Bambu Studio introuvables")
			return
		}
		profStore, profErr = profiles.Load(d)
	})
	if profErr != nil {
		t.Skip(profErr)
	}
	return profStore
}

var (
	profOnce  sync.Once
	profStore *profiles.Store
	profErr   error
)

func realSlicer(t *testing.T) *slicer.Slicer {
	t.Helper()
	if testing.Short() {
		t.Skip("-short")
	}
	installProfiles(t)
	wr := os.Getenv("BAMBU_TEST_WORKROOT")
	if wr == "" {
		wr = t.TempDir()
	}
	s, err := slicer.New(slicer.Config{Profiles: installProfiles(t), WorkRoot: wr, Timeout: 5 * time.Minute})
	if err != nil {
		t.Skipf("Bambu Studio inutilisable : %v", err)
	}
	return s
}

type env struct {
	t      *testing.T
	core   *core.Core
	svc    *Service
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// newEnv ouvre un cœur temporaire. Avec sl non nul, les travaux s'exécutent.
func newEnv(t *testing.T, prof *profiles.Store, sl *slicer.Slicer, run bool) *env {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c, err := core.Open(ctx, core.Config{DataDir: t.TempDir(), PollInterval: 30 * time.Millisecond, HeartbeatInterval: 200 * time.Millisecond})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cfg := Config{Store: c.Store, Blobs: c.Blobs, Profiles: prof, Slicer: sl, WorkRoot: t.TempDir()}
	if run {
		cfg.Queue, cfg.Registry = c.Queue, c.Registry
	}
	svc, err := New(cfg)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	e := &env{t: t, core: c, svc: svc, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	if run {
		go func() { _ = c.Run(ctx); close(e.done) }()
	} else {
		close(e.done)
	}
	t.Cleanup(func() {
		cancel()
		<-e.done
		c.Close()
	})
	return e
}

// addModel crée un modèle avec une version contenant le maillage en STL.
func (e *env) addModel(name string, m *mesh.Mesh) (modelID, versionID string) {
	e.t.Helper()
	stl, err := mesh.STLBytes(m)
	if err != nil {
		e.t.Fatal(err)
	}
	return e.addFile(name, "stl", stl, "")
}

func (e *env) addFile(name, format string, data []byte, modelID string) (string, string) {
	e.t.Helper()
	st := e.core.Store
	if modelID == "" {
		mdl, err := st.CreateModel(e.ctx, "test", contracts.Model{Name: name})
		if err != nil {
			e.t.Fatal(err)
		}
		modelID = mdl.ID
	}
	sha, size, err := e.core.Blobs.Put(bytes.NewReader(data), "")
	if err != nil {
		e.t.Fatal(err)
	}
	f, err := st.AddSourceFile(e.ctx, "test", contracts.SourceFile{ModelID: modelID, Name: name + "." + format, Format: contracts.FileFormat(format),
		SizeBytes: size, SHA256: sha, Availability: contracts.AvailableLinux})
	if err != nil {
		e.t.Fatal(err)
	}
	v, err := st.CreateVersion(e.ctx, "test", contracts.ModelVersion{ModelID: modelID, Label: "v " + name, SourceFileIDs: []string{f.ID}})
	if err != nil {
		e.t.Fatal(err)
	}
	return modelID, v.ID
}

// waitDone attend que toutes les découpes de l'expérience soient terminées.
func (e *env) waitDone(expID string) *Experiment {
	e.t.Helper()
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		x, err := e.svc.GetExperiment(e.ctx, expID)
		if err != nil {
			e.t.Fatal(err)
		}
		done := true
		for _, c := range x.Candidates {
			if c.State != "succeeded" && c.State != "failed" && c.State != "cancelled" {
				done = false
			}
		}
		if done {
			return x
		}
		time.Sleep(100 * time.Millisecond)
	}
	e.t.Fatal("délai dépassé en attendant les découpes")
	return nil
}

func ptrF(f float64) *float64 { return &f }

// strengthSpeed est l'objectif « solide et rapide » (priorités égales).
func strengthSpeed(dir string, withBeam bool) GoalInput {
	p := map[string]any{"load_direction": dir}
	if withBeam {
		p["engineering"] = map[string]any{
			"support": "cantilever", "load_type": "point", "load": 20.0, "length_mm": 60.0,
			"section": map[string]any{"shape": "rect", "b": 20.0, "h": 6.0}, "material": "PLA",
		}
	}
	return GoalInput{
		OriginalText: "Un support solide qui encaisse un effort, mais imprimé vite",
		Material:     "PLA",
		Criteria: []CriterionInput{
			{Type: "strength", Text: "solide", Priority: 2, Params: p},
			{Type: "speed", Text: "vite", Priority: 2, Threshold: &Threshold{Op: "<=", Value: 20, Unit: "min"}},
		},
	}
}
