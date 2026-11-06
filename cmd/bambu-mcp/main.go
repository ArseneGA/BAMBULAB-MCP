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
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

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
	tr := &mcp.IOTransport{Reader: in, Writer: out}

	var err error
	if *embedded {
		err = runEmbedded(ctx, log, *dataDir, tr)
	} else {
		err = runBridge(ctx, log, *url, *apiToken, *attempts, tr)
	}
	if err != nil && ctx.Err() == nil {
		log.Error("arrêt sur erreur", "err", err)
		return 1
	}
	return 0
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
	c, err := core.Open(ctx, core.Config{DataDir: dataDir, Logger: log})
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
