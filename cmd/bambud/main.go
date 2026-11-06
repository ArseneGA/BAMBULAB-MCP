// bambud est le service unique : API HTTP, MCP Streamable HTTP (/mcp) et worker
// de la file de travaux.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return def
}

func main() {
	fs := flag.NewFlagSet("bambud", flag.ExitOnError)
	addr := fs.String("addr", envOr("BAMBU_ADDR", "127.0.0.1:7878"), "adresse d'écoute (env BAMBU_ADDR)")
	dataDir := fs.String("data-dir", envOr("BAMBU_DATA_DIR", "./data"), "dossier de données (env BAMBU_DATA_DIR)")
	heavy := fs.Int("heavy-workers", envInt("BAMBU_HEAVY_WORKERS", 1), "travaux lourds simultanés")
	light := fs.Int("light-workers", envInt("BAMBU_LIGHT_WORKERS", 4), "travaux légers simultanés")
	level := fs.String("log-level", envOr("BAMBU_LOG_LEVEL", "info"), "debug|info|warn|error")
	version := fs.Bool("version", false, "affiche la version")
	_ = fs.Parse(os.Args[1:])
	if *version {
		fmt.Println("bambud", core.Version)
		return
	}

	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(*level)); err != nil {
		lvl = slog.LevelInfo
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, log, *addr, core.Config{DataDir: *dataDir, HeavyWorkers: *heavy, LightWorkers: *light, Logger: log}, nil); err != nil {
		log.Error("arrêt sur erreur", "err", err)
		os.Exit(1)
	}
}

// run démarre le service et bloque jusqu'à l'annulation de ctx. Si ready n'est
// pas nil, l'adresse réelle d'écoute y est envoyée (utile aux tests, port 0).
func run(ctx context.Context, log *slog.Logger, addr string, cfg core.Config, ready chan<- string) error {
	c, err := core.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer c.Close()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("écoute sur %s : %w", addr, err)
	}
	srv := &http.Server{
		Handler:           httpapi.NewHandler(c),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	workerDone := make(chan struct{})
	go func() { defer close(workerDone); _ = c.Run(ctx) }()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	log.Info("bambud démarré", "addr", ln.Addr().String(), "data_dir", c.Cfg.DataDir, "version", core.Version)
	if ready != nil {
		ready <- ln.Addr().String()
	}

	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}

	// Arrêt propre : plus de nouvelles requêtes, puis drain des travaux en cours.
	log.Info("arrêt demandé : drain des requêtes et des travaux")
	shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Warn("arrêt HTTP incomplet", "err", err)
		srv.Close()
	}
	<-workerDone
	log.Info("arrêt terminé")
	return nil
}
