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
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/localtime"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
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
	oauthRedirects := fs.String("oauth-redirects", os.Getenv("BAMBU_OAUTH_REDIRECTS"), "URI de redirection OAuth exactes supplémentaires, séparées par des virgules (en plus des callbacks de Claude et de la boucle locale ; env BAMBU_OAUTH_REDIRECTS)")
	publicListen := fs.String("public-listen", os.Getenv("BAMBU_PUBLIC_LISTEN"), "second écouteur : surface publique restreinte (/mcp, OAuth, pages /about /privacy ; tout le reste 404) à publier sur Internet, ex. 127.0.0.1:7879 ; vide = aucun (env BAMBU_PUBLIC_LISTEN)")
	publicListenURL := fs.String("public-listen-url", os.Getenv("BAMBU_PUBLIC_LISTEN_URL"), "URL https de la surface publique vue d'Internet (ex. https://atelier.exemple.ts.net) : émetteur OAuth et ressource /mcp de cet écouteur ; --public-url reste l'adresse privée des liens /confirm et /stop (env BAMBU_PUBLIC_LISTEN_URL)")
	publicRate := fs.String("public-rate-limit", os.Getenv("BAMBU_PUBLIC_RATE_LIMIT"), "limites par adresse de la surface publique, ex. register=10,token=60,mcp=60 (register par heure ; token et mcp par minute ; /mcp sans jeton valide) (env BAMBU_PUBLIC_RATE_LIMIT)")
	version := fs.Bool("version", false, "affiche la version")
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "seed-demo":
			os.Exit(seedDemoMain(os.Args[2:]))
		case "oauth":
			os.Exit(oauthMain(os.Args[2:], os.Stdout, os.Stderr))
		case "enroll":
			os.Exit(enrollMain(os.Args[2:], os.Stdout, os.Stderr))
		case "launch-code":
			os.Exit(launchCodeMain(os.Args[2:], os.Stdout, os.Stderr))
		case "passkey":
			os.Exit(passkeyMain(os.Args[2:], os.Stdout, os.Stderr))
		case "backup":
			os.Exit(backupMain(os.Args[2:], os.Stdout, os.Stderr))
		case "restore":
			os.Exit(restoreMain(os.Args[2:], os.Stdout, os.Stderr))
		case "verify":
			os.Exit(verifyMain(os.Args[2:], os.Stdout, os.Stderr))
		case "upgrade":
			os.Exit(upgradeMain(os.Args[2:], os.Stdout, os.Stderr))
		case "upgrade-probe":
			os.Exit(upgradeProbeMain(os.Args[2:], os.Stdout, os.Stderr))
		case "upgrade-notify":
			os.Exit(upgradeNotifyMain(os.Args[2:], os.Stdout, os.Stderr))
		case "schema-version":
			os.Exit(schemaVersionMain(os.Stdout))
		case "agent":
			os.Exit(agentMain(os.Args[2:], os.Stdout, os.Stderr))
		case "doctor":
			os.Exit(doctorMain(os.Args[2:], os.Stdout, os.Stderr))
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
	rate, err := httpapi.ParsePublicRateLimits(*publicRate)
	if err != nil {
		log.Error("configuration invalide", "err", fmt.Errorf("--public-rate-limit : %w", err))
		os.Exit(2)
	}
	listenURL, err := validatePublicListenURL(*publicListenURL, *publicListen)
	if err != nil {
		log.Error("configuration invalide", "err", err)
		os.Exit(2)
	}
	sc := serveConfig{HTTP: httpapi.Options{APIToken: *apiToken, AllowedHosts: hosts, PublicURL: first, PublicURLs: urls, TrustedProxies: proxies, OAuthRedirects: splitPublicURLs(*oauthRedirects),
		PublicListenURL: listenURL, ContactEmail: strings.TrimSpace(os.Getenv("BAMBU_CONTACT_EMAIL")), PublicRate: rate},
		RequireToken: *requireToken, DrainTimeout: *drain, PublicListen: strings.TrimSpace(*publicListen)}
	if sc.PublicListen != "" {
		for _, w := range publicListenWarnings(sc.PublicListen, listenURL, first) {
			log.Warn("surface publique", "detail", w)
		}
	}
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
	PublicListen string        // --public-listen : adresse du second écouteur (surface publique restreinte) ; vide = aucun
	publicLn     net.Listener  // écouteur public déjà ouvert (run) ; sinon serve ouvre PublicListen
}

// run démarre le service et bloque jusqu'à l'annulation de ctx. Si ready n'est
// pas nil, l'adresse réelle d'écoute y est envoyée (utile aux tests, port 0).
func run(ctx context.Context, log *slog.Logger, addr string, cfg core.Config, sc serveConfig, ready chan<- string) error {
	if err := httpapi.ValidateServe(addr, sc.HTTP.APIToken, httpapi.Exposure{
		PublicURLs: sc.HTTP.PublicURLs, AllowedHosts: sc.HTTP.AllowedHosts, RequireToken: sc.RequireToken, PublicListen: sc.PublicListen}); err != nil {
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
	if sc.PublicListen != "" {
		pln, perr := net.Listen("tcp", sc.PublicListen)
		if perr != nil {
			ln.Close()
			c.Close() // aucun worker démarré : fermeture sûre
			return fmt.Errorf("écoute publique sur %s : %w", sc.PublicListen, perr)
		}
		sc.publicLn = pln
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
	handlers := httpapi.NewHandlers(c, sc.HTTP)
	srv := &http.Server{
		Handler:           handlers.Main,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	var pubSrv *http.Server
	if sc.publicLn != nil {
		// Surface exposée à Internet : en-têtes et inactivité bornés (le flux SSE de /mcp n'est pas coupé : pas de WriteTimeout).
		pubSrv = &http.Server{
			Handler:           handlers.Public,
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       2 * time.Minute,
			MaxHeaderBytes:    32 << 10,
			ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		}
	}

	workerCtx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers() // garanti sur tous les chemins de sortie
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); _ = c.Run(workerCtx) }()

	serveErr := make(chan error, 2)
	go func() { serveErr <- srv.Serve(ln) }()
	if pubSrv != nil {
		go func() { serveErr <- pubSrv.Serve(sc.publicLn) }()
		log.Info("surface publique restreinte active", "addr", sc.publicLn.Addr().String(), "public_url", sc.HTTP.PublicListenURL)
	}
	c.Catalog.SetBaseURL(publicBase(sc.HTTP.PublicURL, ln.Addr().String())) // jamais « http://[::]:port »
	log.Info("bambud démarré", "addr", ln.Addr().String(), "data_dir", c.Cfg.DataDir, "version", core.Version, "timezone", localtime.Name())
	if w := localtime.Warning(); w != "" {
		log.Warn("fuseau horaire", "detail", w)
	}
	if !mcpserver.ViewsBuilt() {
		log.Warn("vue MCP App non embarquée : Claude affichera « Vue non construite » au lieu du viewer 3D ; construire avec scripts/build.sh release (ou npm run build dans web/) avant go build")
	}
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
	if pubSrv != nil {
		if err := pubSrv.Shutdown(shutCtx); err != nil {
			log.Warn("arrêt HTTP public incomplet", "err", err)
			pubSrv.Close()
		}
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

// validatePublicListenURL valide --public-listen-url : https (ou http loopback), hôte, sans chemin ; exige
// --public-listen. Renvoie l'URL sans « / » final.
func validatePublicListenURL(raw, listen string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "", nil
	}
	if strings.TrimSpace(listen) == "" {
		return "", errors.New("--public-listen-url exige --public-listen")
	}
	if err := validateOnePublicURL(raw); err != nil {
		return "", fmt.Errorf("--public-listen-url : %w", err)
	}
	u, _ := url.Parse(raw)
	if u.Scheme != "https" && !httpapi.IsLoopbackAddr(net.JoinHostPort(u.Hostname(), "0")) {
		return "", fmt.Errorf("--public-listen-url %q : une adresse publique doit être en https", raw)
	}
	return raw, nil
}

// publicListenWarnings : réglages qui marchent mais trompent.
func publicListenWarnings(listen, listenURL, privateURL string) []string {
	var w []string
	if !httpapi.IsLoopbackAddr(listen) {
		w = append(w, "--public-listen n'est pas en loopback : l'écouteur est joignable sans TLS depuis le réseau ; préférer 127.0.0.1 derrière Funnel ou un reverse proxy")
	}
	if listenURL == "" {
		w = append(w, "--public-listen-url absent : l'émetteur OAuth est déduit de chaque requête ; le renseigner (https://nom-public)")
	}
	if listenURL != "" && strings.EqualFold(listenURL, privateURL) {
		w = append(w, "--public-url (liens /confirm, /stop) est identique à --public-listen-url : ces liens pointeraient vers la surface publique, qui répond 404 ; donner à --public-url l'adresse privée (ex. https://nom:8443)")
	}
	return w
}
