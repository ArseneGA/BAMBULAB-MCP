package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
)

// Proxy relaie un serveur MCP distant (bambud, MCP Streamable HTTP) vers un
// serveur local exposable sur n'importe quel transport (stdio pour Claude
// Desktop). Les outils et ressources du distant sont énumérés à la connexion
// puis relayés tels quels : schémas, annotations et _meta (donc MCP Apps) inclus.
// Limite connue : la liste est figée à la connexion (pas de relais de
// notifications list_changed) ; relancer le pont après une mise à jour du service.
//
// Reconnexion : si bambud redémarre (session perdue), le pont se reconnecte avec un
// backoff exponentiel. L'appel en échec n'est rejoué automatiquement que si l'outil
// est déclaré en lecture seule ou idempotent ; sinon l'erreur est renvoyée (avec
// l'indication que la connexion est rétablie) pour ne jamais dupliquer un effet.
type Proxy struct {
	Server *mcp.Server

	mu     sync.Mutex
	remote *mcp.ClientSession
	dial   func() mcp.Transport // nil : pas de reconnexion
	opts   ProxyOptions
	log    *slog.Logger
}

// ProxyOptions règle les (re)connexions.
type ProxyOptions struct {
	// Dial fournit un transport NEUF à chaque tentative de connexion. nil = pas de reconnexion.
	Dial func() mcp.Transport
	// InitialBackoff (défaut 250 ms), MaxBackoff (défaut 4 s), doublement entre tentatives.
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	// ConnectAttempts : tentatives par (re)connexion (défaut 1 à l'ouverture, 6 en reconnexion).
	ConnectAttempts   int
	ReconnectAttempts int
}

func (o *ProxyOptions) defaults() {
	if o.InitialBackoff <= 0 {
		o.InitialBackoff = 250 * time.Millisecond
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = 4 * time.Second
	}
	if o.ConnectAttempts <= 0 {
		o.ConnectAttempts = 1
	}
	if o.ReconnectAttempts <= 0 {
		o.ReconnectAttempts = 6
	}
}

// Close ferme la session distante.
func (p *Proxy) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.remote.Close()
}

// NewProxy se connecte via transport (usage unique) au serveur distant et construit
// le miroir local. Sans reconnexion possible : voir NewReconnectingProxy.
func NewProxy(ctx context.Context, transport mcp.Transport, log *slog.Logger) (*Proxy, error) {
	used := false
	return NewReconnectingProxy(ctx, ProxyOptions{}, log, func() mcp.Transport {
		if used {
			return nil
		}
		used = true
		return transport
	}, false)
}

// NewProxyWithOptions est NewProxy avec reconnexion : opts.Dial doit fournir un transport
// neuf à chaque appel.
func NewProxyWithOptions(ctx context.Context, opts ProxyOptions, log *slog.Logger) (*Proxy, error) {
	if opts.Dial == nil {
		return nil, errors.New("ProxyOptions.Dial requis")
	}
	return NewReconnectingProxy(ctx, opts, log, opts.Dial, true)
}

// NewReconnectingProxy construit le proxy ; reconnect=false désactive les reconnexions.
func NewReconnectingProxy(ctx context.Context, opts ProxyOptions, log *slog.Logger, dial func() mcp.Transport, reconnect bool) (*Proxy, error) {
	opts.defaults()
	p := &Proxy{opts: opts, log: log}
	if reconnect {
		p.dial = dial
	}
	cs, err := p.connect(ctx, dial, opts.ConnectAttempts)
	if err != nil {
		return nil, err
	}
	p.remote = cs
	instructions := ""
	if init := cs.InitializeResult(); init != nil {
		instructions = init.Instructions
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "bambu-mcp", Title: "Atelier Bambu Lab (pont)", Version: core.Version},
		&mcp.ServerOptions{Logger: log, Instructions: instructions})
	p.Server = srv

	nt, nr := 0, 0
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			cs.Close()
			return nil, fmt.Errorf("liste des outils : %w", err)
		}
		t := *tool
		retryable := t.Annotations != nil && (t.Annotations.ReadOnlyHint || t.Annotations.IdempotentHint)
		srv.AddTool(&t, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			params := &mcp.CallToolParams{Name: req.Params.Name, Arguments: req.Params.Arguments}
			return withReconnect(ctx, p, retryable, func(s *mcp.ClientSession) (*mcp.CallToolResult, error) {
				return s.CallTool(ctx, params)
			})
		})
		nt++
	}
	for res, err := range cs.Resources(ctx, nil) {
		if err != nil {
			cs.Close()
			return nil, fmt.Errorf("liste des ressources : %w", err)
		}
		r := *res
		srv.AddResource(&r, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			params := &mcp.ReadResourceParams{URI: req.Params.URI}
			return withReconnect(ctx, p, true, func(s *mcp.ClientSession) (*mcp.ReadResourceResult, error) {
				return s.ReadResource(ctx, params)
			})
		})
		nr++
	}
	if log != nil {
		log.Info("pont MCP prêt", "outils", nt, "ressources", nr)
	}
	return p, nil
}

// connect tente de se connecter jusqu'à attempts fois, avec backoff exponentiel.
func (p *Proxy) connect(ctx context.Context, dial func() mcp.Transport, attempts int) (*mcp.ClientSession, error) {
	delay := p.opts.InitialBackoff
	var last error
	for i := 1; i <= attempts; i++ {
		tr := dial()
		if tr == nil {
			return nil, errors.New("connexion à bambud : aucun transport disponible")
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "bambu-mcp-bridge", Version: core.Version}, nil)
		cs, err := client.Connect(ctx, tr, nil)
		if err == nil {
			return cs, nil
		}
		last = err
		if i == attempts {
			break
		}
		if p.log != nil {
			p.log.Warn("connexion à bambud impossible, nouvelle tentative", "tentative", i, "dans", delay.String(), "err", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		if delay *= 2; delay > p.opts.MaxBackoff {
			delay = p.opts.MaxBackoff
		}
	}
	return nil, fmt.Errorf("connexion à bambud : %w", last)
}

// isConnectionError : l'erreur vient du transport (connexion coupée, session perdue après
// un redémarrage de bambud, requête rejetée par le transport) et non d'une vraie réponse
// d'erreur JSON-RPC du serveur.
func isConnectionError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, mcp.ErrConnectionClosed) || errors.Is(err, mcp.ErrSessionMissing) {
		return true
	}
	var rpc *jsonrpc.Error
	if errors.As(err, &rpc) {
		// Codes du transport JSON-RPC du SDK : client/serveur en fermeture, inconnu, rejet.
		switch rpc.Code {
		case -32001, -32003, -32004, -32005:
			return true
		}
		return false
	}
	return true
}

func (p *Proxy) session() *mcp.ClientSession {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.remote
}

// reconnect remplace la session périmée stale (si personne ne l'a déjà fait).
func (p *Proxy) reconnect(ctx context.Context, stale *mcp.ClientSession) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.remote != stale {
		return nil // déjà reconnecté par un autre appel
	}
	if p.log != nil {
		p.log.Warn("connexion à bambud perdue : reconnexion")
	}
	_ = stale.Close()
	cs, err := p.connect(ctx, p.dial, p.opts.ReconnectAttempts)
	if err != nil {
		return err
	}
	p.remote = cs
	if p.log != nil {
		p.log.Info("reconnecté à bambud")
	}
	return nil
}

// withReconnect exécute call sur la session courante ; sur erreur de transport, reconnecte
// et rejoue l'appel seulement s'il est sans danger (retryable).
func withReconnect[T any](ctx context.Context, p *Proxy, retryable bool, call func(*mcp.ClientSession) (T, error)) (T, error) {
	s := p.session()
	out, err := call(s)
	if err == nil || p.dial == nil || !isConnectionError(err) {
		return out, err
	}
	if rerr := p.reconnect(ctx, s); rerr != nil {
		var zero T
		return zero, fmt.Errorf("%w ; reconnexion à bambud impossible : %v", err, rerr)
	}
	if !retryable {
		var zero T
		return zero, fmt.Errorf("%w ; connexion rétablie, mais l'appel n'est pas rejoué automatiquement (effet possible) : vérifier l'état avant de relancer", err)
	}
	return call(p.session())
}
