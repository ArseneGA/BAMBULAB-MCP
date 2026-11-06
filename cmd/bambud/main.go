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
	"strings"
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

// envBool : "1", "true", "yes" ou "on" (insensible à la casse).
func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// resolveStudioPath retient le chemin de Bambu Studio explicitement demandé (--studio-bin, sinon
// BAMBU_STUDIO_BIN). Un chemin donné mais inexistant (ou dossier) est une erreur de démarrage : jamais de
// repli silencieux sur une autre installation. Vide : détection automatique.
func resolveStudioPath(flagValue, envValue string) (string, error) {
	src, p := "--studio-bin", strings.TrimSpace(flagValue)
	if p == "" {
		src, p = "BAMBU_STUDIO_BIN", strings.TrimSpace(envValue)
	}
	if p == "" {
		return "", nil
	}
	if fi, err := os.Stat(p); err != nil || fi.IsDir() {
		return "", fmt.Errorf("%s=%q : Bambu Studio introuvable (fichier exécutable attendu)", src, p)
	}
	return p, nil
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(key)); err == nil && d > 0 {
		return d
	}
	return def
}

func main() {
	fs := flag.NewFlagSet("bambud", flag.ExitOnError)
	addr := fs.String("addr", envOr("BAMBU_ADDR", "127.0.0.1:7878"), "adresse d'écoute (env BAMBU_ADDR)")
	dataDir := fs.String("data-dir", envOr("BAMBU_DATA_DIR", "./data"), "dossier de données (env BAMBU_DATA_DIR)")
	heavy := fs.Int("heavy-workers", envInt("BAMBU_HEAVY_WORKERS", 1), "travaux lourds simultanés")
	light := fs.Int("light-workers", envInt("BAMBU_LIGHT_WORKERS", 4), "travaux légers simultanés")
	apiToken := fs.String("api-token", os.Getenv("BAMBU_API_TOKEN"), "jeton d'API (Bearer) ; obligatoire hors loopback (env BAMBU_API_TOKEN)")
	allowHosts := fs.String("allowed-hosts", os.Getenv("BAMBU_ALLOWED_HOSTS"), "noms d'hôte HTTP acceptés en plus du loopback, séparés par des virgules")
	drain := fs.Duration("drain-timeout", envDuration("BAMBU_DRAIN_TIMEOUT", 30*time.Second), "attente max. des travaux en cours à l'arrêt (env BAMBU_DRAIN_TIMEOUT)")
	level := fs.String("log-level", envOr("BAMBU_LOG_LEVEL", "info"), "debug|info|warn|error")
	roots := fs.String("import-root", os.Getenv("BAMBU_IMPORT_ROOTS"), "racines d'import autorisées, séparées par '"+string(os.PathListSeparator)+"' (env BAMBU_IMPORT_ROOTS) ; vide = tout import refusé")
	uvPath := fs.String("uv-path", os.Getenv("BAMBU_UV_PATH"), "exécutable uv du worker CAO (env BAMBU_UV_PATH ; défaut : PATH)")
	geomDir := fs.String("geometry-dir", os.Getenv("BAMBU_GEOMETRY_DIR"), "projet Python du worker géométrie (env BAMBU_GEOMETRY_DIR ; défaut workers/geometry)")
	publicURL := fs.String("public-url", os.Getenv("BAMBU_PUBLIC_URL"), "URL(s) sous lesquelles l'humain atteint le service, séparées par des virgules ; la première sert aux liens /confirm, /stop, /setup ; l'attribut Secure du cookie suit celle dont l'hôte correspond à la requête (env BAMBU_PUBLIC_URL)")
	requireToken := fs.Bool("require-token", envBool("BAMBU_REQUIRE_TOKEN"), "exige un jeton d'API même en écoute loopback (derrière un proxy : toujours) (env BAMBU_REQUIRE_TOKEN)")
	trustedProxies := fs.String("trusted-proxies", os.Getenv("BAMBU_TRUSTED_PROXIES"), "adresses/réseaux CIDR dont X-Forwarded-For/-Proto sont crus, séparés par des virgules ; défaut : loopback ; \"none\" : aucun (env BAMBU_TRUSTED_PROXIES)")
	anyHost := fs.Bool("allow-any-printer-host", os.Getenv("BAMBU_ALLOW_ANY_PRINTER_HOST") == "1", "lève la restriction des hôtes d'imprimante aux adresses privées / lien local / .local")
	studioBin := fs.String("studio-bin", "", "exécutable ou AppImage de Bambu Studio (défaut : env BAMBU_STUDIO_BIN, puis détection) ; un chemin donné mais introuvable est une erreur de démarrage")
	studioWrap := fs.String("studio-wrapper", os.Getenv("BAMBU_STUDIO_WRAPPER"), "commande préfixe du slicer, ex. \"xvfb-run -a\" (jamais activée implicitement) ; \"none\" ou vide : aucune (env BAMBU_STUDIO_WRAPPER)")
	version := fs.Bool("version", false, "affiche la version")
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "seed-demo":
			os.Exit(seedDemoMain(os.Args[2:]))
		case "enroll":
			os.Exit(enrollMain(os.Args[2:], os.Stdout, os.Stderr))
		case "backup":
			os.Exit(backupMain(os.Args[2:], os.Stdout, os.Stderr))
		case "restore":
			os.Exit(restoreMain(os.Args[2:], os.Stdout, os.Stderr))
		case "verify":
			os.Exit(verifyMain(os.Args[2:], os.Stdout, os.Stderr))
		case "agent":
			os.Exit(agentMain(os.Args[2:], os.Stdout, os.Stderr))
		}
	}
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
	var hosts []string
	for _, h := range strings.Split(*allowHosts, ",") {
		if h = strings.TrimSpace(h); h != "" {
			hosts = append(hosts, h)
		}
	}
	if err := validatePublicURL(*publicURL); err != nil {
		log.Error("configuration invalide", "err", err)
		os.Exit(2)
	}
	proxies, err := httpapi.ParseTrustedProxies(*trustedProxies)
	if err != nil {
		log.Error("configuration invalide", "err", err)
		os.Exit(2)
	}
	studioPath, err := resolveStudioPath(*studioBin, os.Getenv("BAMBU_STUDIO_BIN"))
	if err != nil {
		log.Error("configuration invalide", "err", err)
		os.Exit(2)
	}
	urls := splitPublicURLs(*publicURL)
	first := ""
	if len(urls) > 0 {
		first = urls[0]
	}
	sc := serveConfig{HTTP: httpapi.Options{APIToken: *apiToken, AllowedHosts: hosts, PublicURL: first, PublicURLs: urls, TrustedProxies: proxies},
		RequireToken: *requireToken, DrainTimeout: *drain}
	if err := run(ctx, log, *addr, core.Config{DataDir: *dataDir, HeavyWorkers: *heavy, LightWorkers: *light, Logger: log,
		Catalog: catalogConfig(*roots, *uvPath, *geomDir), AllowAnyPrinterHost: *anyHost,
		StudioPath: studioPath, StudioWrapper: parseWrapper(*studioWrap)}, sc, nil); err != nil {
		log.Error("arrêt sur erreur", "err", err)
		os.Exit(1)
	}
}

// errDrainTimeout : des travaux n'ont pas terminé dans le délai de drain ; la base
// n'est volontairement PAS fermée (un worker pourrait encore y écrire).
var errDrainTimeout = errors.New("drain des travaux non terminé dans le délai imparti")

// serveConfig regroupe les réglages de run/serve.
type serveConfig struct {
	HTTP         httpapi.Options
	DrainTimeout time.Duration // attente max. des travaux après l'arrêt (défaut 30 s)
	RequireToken bool          // --require-token : jeton obligatoire même en écoute loopback
}

// run démarre le service et bloque jusqu'à l'annulation de ctx. Si ready n'est
// pas nil, l'adresse réelle d'écoute y est envoyée (utile aux tests, port 0).
func run(ctx context.Context, log *slog.Logger, addr string, cfg core.Config, sc serveConfig, ready chan<- string) error {
	if err := httpapi.ValidateServe(addr, sc.HTTP.APIToken, httpapi.Exposure{
		PublicURLs: sc.HTTP.PublicURLs, AllowedHosts: sc.HTTP.AllowedHosts, RequireToken: sc.RequireToken}); err != nil {
		return err
	}
	c, err := core.Open(ctx, cfg)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		c.Close() // aucun worker démarré : fermeture sûre
		return fmt.Errorf("écoute sur %s : %w", addr, err)
	}
	err = serve(ctx, log, c, ln, sc, ready)
	if errors.Is(err, errDrainTimeout) {
		log.Error("base laissée ouverte : des travaux tournent encore", "err", err)
		return err
	}
	if cerr := c.Close(); cerr != nil && err == nil {
		err = cerr
	}
	return err
}

// serve fait tourner HTTP et les workers sur ln jusqu'à l'annulation de ctx ou
// l'échec du serveur HTTP. Dans TOUS les cas : le contexte des workers est annulé,
// les travaux sont drainés (au plus DrainTimeout), et seulement ensuite l'appelant
// peut fermer la base (si errDrainTimeout, il ne doit pas le faire).
func serve(ctx context.Context, log *slog.Logger, c *core.Core, ln net.Listener, sc serveConfig, ready chan<- string) error {
	if sc.DrainTimeout <= 0 {
		sc.DrainTimeout = 30 * time.Second
	}
	srv := &http.Server{
		Handler:           httpapi.NewHandlerWithOptions(c, sc.HTTP),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	workerCtx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers() // garanti sur tous les chemins de sortie
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); _ = c.Run(workerCtx) }()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	c.Catalog.SetBaseURL(publicBase(sc.HTTP.PublicURL, ln.Addr().String())) // jamais « http://[::]:port »
	log.Info("bambud démarré", "addr", ln.Addr().String(), "data_dir", c.Cfg.DataDir, "version", core.Version)
	if ready != nil {
		ready <- ln.Addr().String()
	}

	var result error
	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			result = err
		}
	case <-ctx.Done():
	}

	// Arrêt : plus de nouvelles requêtes, workers signalés, puis drain borné.
	log.Info("arrêt demandé : drain des requêtes et des travaux")
	shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Warn("arrêt HTTP incomplet", "err", err)
		srv.Close()
	}
	cancelWorkers()
	select {
	case <-workerDone:
		log.Info("arrêt terminé")
		return result
	case <-time.After(sc.DrainTimeout):
		return errors.Join(result, fmt.Errorf("%w (%s)", errDrainTimeout, sc.DrainTimeout))
	}
}
