package mcpserver_test

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

// bambud redémarre (nouvelle instance, anciennes sessions perdues) : le pont se
// reconnecte tout seul et rejoue l'appel en lecture seule.
func TestProxyReconnectsAfterBambudRestart(t *testing.T) {
	ctx := context.Background()
	var url atomic.Value
	first := httptest.NewServer(httpapi.NewHandler(startCore(t)))
	url.Store(first.URL)

	p, err := mcpserver.NewProxyWithOptions(ctx, mcpserver.ProxyOptions{
		Dial:           func() mcp.Transport { return &mcp.StreamableClientTransport{Endpoint: url.Load().(string) + "/mcp"} },
		InitialBackoff: 10 * time.Millisecond, MaxBackoff: 40 * time.Millisecond, ReconnectAttempts: 20,
	}, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	cs := connectClient(t, p.Server)
	if caps := structured(t, call(t, cs, "get_capabilities", map[string]any{})); caps["service"] != "bambud" {
		t.Fatalf("%v", caps)
	}

	// Redémarrage : l'ancien serveur disparaît, le nouveau n'est joignable qu'après un délai (backoff).
	first.CloseClientConnections()
	first.Close()
	second := httptest.NewServer(httpapi.NewHandler(startCore(t)))
	t.Cleanup(func() { p.Close(); second.CloseClientConnections(); second.Close() })
	go func() {
		time.Sleep(150 * time.Millisecond)
		url.Store(second.URL)
	}()
	start := time.Now()
	caps := structured(t, call(t, cs, "get_capabilities", map[string]any{}))
	if caps["service"] != "bambud" {
		t.Fatalf("après reconnexion : %v", caps)
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Fatal("le backoff aurait dû attendre le retour du serveur")
	}
	// La session suivante fonctionne sans nouvelle reconnexion.
	structured(t, call(t, cs, "get_capabilities", map[string]any{}))
}

// Sans bambud joignable après toutes les tentatives : erreur claire, pas de blocage.
func TestProxyInitialConnectBackoffGivesUp(t *testing.T) {
	start := time.Now()
	_, err := mcpserver.NewProxyWithOptions(context.Background(), mcpserver.ProxyOptions{
		Dial:            func() mcp.Transport { return &mcp.StreamableClientTransport{Endpoint: "http://127.0.0.1:1/mcp"} },
		InitialBackoff:  20 * time.Millisecond,
		MaxBackoff:      40 * time.Millisecond,
		ConnectAttempts: 3,
	}, quietLog())
	if err == nil {
		t.Fatal("connexion impossible acceptée")
	}
	if d := time.Since(start); d < 50*time.Millisecond || d > 5*time.Second {
		t.Fatalf("durée %s : le backoff n'a pas été appliqué", d)
	}
}
