// Package core assemble le cœur local du service : base, blobs, file de travaux,
// handlers intégrés. Il est partagé par bambud, le mode --embedded du pont stdio
// et les tests.
package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Version du service (surchargeable : -ldflags "-X .../internal/core.Version=1.2.3").
var Version = "0.1.0-j1"

// Config du cœur.
type Config struct {
	DataDir      string
	HeavyWorkers int // défaut 1
	LightWorkers int // défaut 4
	Logger       *slog.Logger
	// Intervalles du runner (tests).
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
}

// Core regroupe les composants.
type Core struct {
	Cfg      Config
	Store    *store.Store
	Blobs    *store.BlobStore
	Queue    *jobs.Queue
	Registry *jobs.Registry
	Runner   *jobs.Runner
	Log      *slog.Logger
	Recovery jobs.RecoveryReport

	lock *instanceLock
}

// Open prépare le dossier de données, migre la base, enregistre les handlers
// intégrés et applique la reprise des travaux interrompus. Les workers ne
// démarrent qu'avec Run.
func Open(ctx context.Context, cfg Config) (*Core, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("dossier de données requis")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	abs, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	cfg.DataDir = abs
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("dossier de données : %w", err)
	}
	// Verrou d'instance AVANT migrations et reprise : un seul service par dossier.
	lock, err := acquireInstanceLock(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "bambu.db"), store.Options{})
	if err != nil {
		lock.release()
		return nil, err
	}
	blobs, err := store.NewBlobStore(filepath.Join(cfg.DataDir, "blobs"))
	if err != nil {
		st.Close()
		lock.release()
		return nil, err
	}
	st.AttachBlobs(blobs)
	q := jobs.NewQueue(st, jobs.QueueConfig{Limits: map[contracts.JobClass]int{
		contracts.ClassHeavy: cfg.HeavyWorkers, contracts.ClassLight: cfg.LightWorkers,
	}})
	reg := jobs.NewRegistry()
	registerBuiltins(reg)
	c := &Core{Cfg: cfg, Store: st, Blobs: blobs, Queue: q, Registry: reg, Log: cfg.Logger, lock: lock}
	c.Runner = jobs.NewRunner(q, reg, jobs.RunnerConfig{
		PollInterval: cfg.PollInterval, HeartbeatInterval: cfg.HeartbeatInterval, Logger: cfg.Logger,
	})
	rep, err := q.Recover(ctx, reg.Replayable)
	if err != nil {
		st.Close()
		lock.release()
		return nil, fmt.Errorf("reprise des travaux : %w", err)
	}
	c.Recovery = rep
	if n := len(rep.Requeued) + len(rep.Unknown) + len(rep.Cancelled) + len(rep.Failed); n > 0 {
		cfg.Logger.Warn("reprise après interruption", "remis_en_file", len(rep.Requeued), "inconnus", len(rep.Unknown),
			"annules", len(rep.Cancelled), "echecs", len(rep.Failed))
	}
	return c, nil
}

// Run démarre les workers et bloque jusqu'à l'annulation de ctx (puis drain).
func (c *Core) Run(ctx context.Context) error { return c.Runner.Run(ctx) }

// Close ferme la base puis libère le verrou d'instance. À appeler après la fin de Run.
func (c *Core) Close() error {
	err := c.Store.Close()
	if lerr := c.lock.release(); err == nil {
		err = lerr
	}
	return err
}

// registerBuiltins : handlers fournis d'office.
func registerBuiltins(reg *jobs.Registry) {
	// system.selftest : travail de démonstration/diagnostic, rejouable sans effet.
	reg.Register("system.selftest", true, func(ctx context.Context, jc *jobs.JobCtx) (json.RawMessage, error) {
		var p struct {
			Steps  int `json:"steps"`
			StepMS int `json:"step_ms"`
		}
		if len(jc.Job.Params) > 0 {
			if err := json.Unmarshal(jc.Job.Params, &p); err != nil {
				return nil, &jobs.HandlerError{Code: "bad_params", Err: err}
			}
		}
		if p.Steps <= 0 {
			p.Steps = 3
		}
		if p.Steps > 1000 {
			p.Steps = 1000
		}
		for i := 1; i <= p.Steps; i++ {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(p.StepMS) * time.Millisecond):
			}
			jc.Progress(float64(i)/float64(p.Steps), fmt.Sprintf("étape %d/%d", i, p.Steps))
			jc.Logf("étape %d terminée", i)
		}
		return json.RawMessage(fmt.Sprintf(`{"steps":%d}`, p.Steps)), nil
	})
}

// Capabilities décrit ce que le service sait réellement faire à cet instant.
func (c *Core) Capabilities() contracts.Capabilities {
	kinds := c.Registry.Kinds()
	sort.Strings(kinds)
	return contracts.Capabilities{
		Service: "bambud",
		Version: Version,
		Mode:    "linux_seul",
		Features: []contracts.Feature{
			{ID: "catalog_store", Available: true, Note: "stockage, versions immuables, audit ; outils MCP de catalogue à venir"},
			{ID: "blob_store", Available: true, Note: "adressage par contenu, écriture atomique"},
			{ID: "job_queue", Available: true, Note: "persistante, reprise au démarrage, annulation"},
			{ID: "mcp_streamable_http", Available: true},
			{ID: "mcp_stdio_bridge", Available: true},
			{ID: "mcp_apps_ui", Available: true, Note: "page de démonstration uniquement (jalon 1)"},
			{ID: "mesh_import", Available: false, Note: "non implémenté dans ce jalon"},
			{ID: "slicing", Available: false, Note: "non implémenté dans ce jalon"},
			{ID: "printer_lan", Available: false, Note: "non implémenté dans ce jalon"},
			{ID: "cad_worker", Available: false, Note: "non implémenté dans ce jalon"},
			{ID: "windows_agent", Available: false, Note: "aucun agent connecté"},
		},
		JobKinds: kinds,
		Concurrency: map[string]int{
			string(contracts.ClassHeavy): c.Queue.Limit(contracts.ClassHeavy),
			string(contracts.ClassLight): c.Queue.Limit(contracts.ClassLight),
		},
	}
}

func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".w-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	return os.Remove(name) == nil
}

// Environment inspecte la machine : versions, exécutables, éléments manquants.
func (c *Core) Environment(ctx context.Context) (contracts.EnvironmentReport, error) {
	v, err := c.Store.SchemaVersion(ctx)
	if err != nil {
		return contracts.EnvironmentReport{}, err
	}
	rep := contracts.EnvironmentReport{
		ServiceVersion: Version, GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH,
		NumCPU: runtime.NumCPU(), DataDir: c.Cfg.DataDir, DataDirWritable: writable(c.Cfg.DataDir),
		SchemaVersion: v, Missing: []string{},
	}
	rep.Executables = []contracts.Executable{
		findExe("bambu-studio", "slicing headless (CLI Bambu Studio)", "BAMBU_STUDIO_PATH", "bambu-studio", "bambu-studio.AppImage", "BambuStudio", "bambu-studio-cli"),
		findExe("python", "worker CAO (STEP, paramétrique)", "BAMBU_PYTHON", "python3", "python"),
	}
	if !rep.DataDirWritable {
		rep.Missing = append(rep.Missing, "droit d'écriture sur le dossier de données")
	}
	for _, e := range rep.Executables {
		if !e.Found {
			rep.Missing = append(rep.Missing, e.Name+" : "+e.Purpose)
		}
	}
	return rep, nil
}
