package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
)

// catalogConfig construit la configuration du catalogue depuis les options.
func catalogConfig(roots, uvPath, geomDir string) catalog.Config {
	cfg := catalog.Config{UVPath: uvPath, WorkerDir: geomDir}
	for _, r := range filepath.SplitList(roots) {
		if r = strings.TrimSpace(r); r != "" {
			cfg.ImportRoots = append(cfg.ImportRoots, r)
		}
	}
	return cfg
}

// seedDemoMain : `bambud seed-demo` génère ~13 modèles de démonstration (maillages Go
// et, si disponible, gabarits paramétriques CadQuery) et les importe dans la base.
func seedDemoMain(args []string) int {
	return runSeedDemo(args, os.Stdout, os.Stderr)
}

func runSeedDemo(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bambud seed-demo", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataDir := fs.String("data-dir", envOr("BAMBU_DATA_DIR", "./data"), "dossier de données (env BAMBU_DATA_DIR)")
	dir := fs.String("dir", "", "dossier de génération des fichiers (défaut <data-dir>/demo-src)")
	noCAD := fs.Bool("no-cad", false, "ne pas essayer les gabarits paramétriques du worker Python")
	uvPath := fs.String("uv-path", os.Getenv("BAMBU_UV_PATH"), "exécutable uv (env BAMBU_UV_PATH)")
	geomDir := fs.String("geometry-dir", os.Getenv("BAMBU_GEOMETRY_DIR"), "projet du worker géométrie (env BAMBU_GEOMETRY_DIR)")
	wait := fs.Duration("wait", 2*time.Minute, "attente maximale des analyses et aperçus")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c, err := core.Open(ctx, core.Config{DataDir: *dataDir, Logger: log, Catalog: catalogConfig("", *uvPath, *geomDir)})
	if err != nil {
		fmt.Fprintln(stderr, "erreur :", err)
		return 1
	}
	defer c.Close()
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = c.Run(runCtx) }()
	rep, err := c.Catalog.SeedDemo(ctx, catalog.SeedOptions{Dir: *dir, WithCAD: !*noCAD, WaitIdle: *wait})
	cancel()
	<-done
	if err != nil {
		fmt.Fprintln(stderr, "erreur :", err)
		return 1
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(rep)
	return 0
}
