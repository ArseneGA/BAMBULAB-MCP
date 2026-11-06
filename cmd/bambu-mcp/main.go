// bambu-mcp est le pont MCP stdio pour Claude Desktop : MCP sur stdin/stdout,
// relayé vers bambud (MCP Streamable HTTP), ou --embedded pour ouvrir le cœur
// local directement. RIEN n'est écrit sur stdout hors protocole : les logs vont
// sur stderr.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run est le vrai main, testable : stdin/stdout sont le transport MCP, errw reçoit
// tous les logs et messages. Renvoie le code de sortie.
func run(ctx context.Context, args []string, in io.ReadCloser, out io.WriteCloser, errw io.Writer) int {
	fs := flag.NewFlagSet("bambu-mcp", flag.ContinueOnError)
	fs.SetOutput(errw) // jamais stdout
	url := fs.String("url", envOr("BAMBU_URL", "http://127.0.0.1:7878/mcp"), "point d'entrée MCP de bambud (env BAMBU_URL)")
	embedded := fs.Bool("embedded", false, "ouvre le cœur local au lieu de relayer vers bambud")
	dataDir := fs.String("data-dir", envOr("BAMBU_DATA_DIR", core.DefaultDataDir()), "dossier de données absolu (mode --embedded ; env BAMBU_DATA_DIR ; défaut %LOCALAPPDATA%\\BambuAtelier ou $XDG_DATA_HOME/bambu-atelier)")
	apiToken := fs.String("api-token", os.Getenv("BAMBU_API_TOKEN"), "jeton d'API de bambud (Bearer ; env BAMBU_API_TOKEN)")
	tokenFile := fs.String("api-token-file", os.Getenv("BAMBU_API_TOKEN_FILE"), "fichier contenant le jeton d'API (évite de l'écrire dans la configuration de Claude Desktop ; env BAMBU_API_TOKEN_FILE)")
	attempts := fs.Int("connect-attempts", 4, "tentatives de connexion initiale à bambud (backoff exponentiel)")
	level := fs.String("log-level", envOr("BAMBU_LOG_LEVEL", "info"), "debug|info|warn|error")
	version := fs.Bool("version", false, "affiche la version (sur stderr)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *version {
		fmt.Fprintln(errw, "bambu-mcp", core.Version)
		return 0
	}
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(*level)); err != nil {
		lvl = slog.LevelInfo
	}
	log := slog.New(slog.NewTextHandler(errw, &slog.HandlerOptions{Level: lvl}))
	token, err := resolveToken(*apiToken, *tokenFile)
	if err != nil {
		log.Error("configuration invalide", "err", err)
		return 2
	}
	tr := &mcp.IOTransport{Reader: in, Writer: out}

	if *embedded {
		err = runEmbedded(ctx, log, *dataDir, tr)
	} else {
		err = runBridge(ctx, log, *url, token, *attempts, tr)
	}
	if err != nil && ctx.Err() == nil {
		log.Error("arrêt sur erreur", "err", err)
		return 1
	}
	return 0
}

// resolveToken renvoie le jeton d'API : celui de --api-token / BAMBU_API_TOKEN, sinon le contenu du fichier
// --api-token-file / BAMBU_API_TOKEN_FILE (première ligne, espaces retirés). Le fichier permet de ne pas écrire le
// jeton en clair dans claude_desktop_config.json ; son contenu n'est jamais journalisé.
func resolveToken(token, file string) (string, error) {
	if token != "" || file == "" {
		return token, nil
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("fichier de jeton illisible : %w", err)
	}
	line, _, _ := strings.Cut(strings.TrimPrefix(string(b), "\xef\xbb\xbf"), "\n") // BOM éventuel (PowerShell 5.1)
	line = strings.TrimSpace(line)
	if line == "" {
		return "", fmt.Errorf("fichier de jeton %q vide", file)
	}
	return line, nil
}

// bearerTransport ajoute le jeton d'API à chaque requête vers bambud.
type bearerTransport struct {
	token string
	next  http.RoundTripper
}

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

func runBridge(ctx context.Context, log *slog.Logger, url, token string, attempts int, tr mcp.Transport) error {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("URL invalide %q", url)
	}
	dial := func() mcp.Transport {
		t := &mcp.StreamableClientTransport{Endpoint: url}
		if token != "" {
			t.HTTPClient = &http.Client{Transport: bearerTransport{token: token, next: http.DefaultTransport}}
		}
		return t
	}
	p, err := mcpserver.NewProxyWithOptions(ctx, mcpserver.ProxyOptions{Dial: dial, ConnectAttempts: attempts}, log)
	if err != nil {
		return fmt.Errorf("%w (bambud est-il démarré sur %s ? sinon utiliser --embedded)", err, url)
	}
	defer p.Close()
	return p.Server.Run(ctx, tr)
}

func runEmbedded(ctx context.Context, log *slog.Logger, dataDir string, tr mcp.Transport) error {
	cat := catalog.Config{UVPath: os.Getenv("BAMBU_UV_PATH"), WorkerDir: os.Getenv("BAMBU_GEOMETRY_DIR")}
	for _, r := range filepath.SplitList(os.Getenv("BAMBU_IMPORT_ROOTS")) {
		if r = strings.TrimSpace(r); r != "" {
			cat.ImportRoots = append(cat.ImportRoots, r)
		}
	}
	c, err := core.Open(ctx, core.Config{DataDir: dataDir, Logger: log, Catalog: cat})
	if err != nil {
		return err
	}
	defer c.Close()
	workerDone := make(chan struct{})
	wctx, cancel := context.WithCancel(ctx)
	go func() { defer close(workerDone); _ = c.Run(wctx) }()
	err = mcpserver.New(c).Run(ctx, tr)
	cancel()
	<-workerDone // drain
	return err
}
