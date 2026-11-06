// Package agentlinktest fournit un serveur d'agents en processus pour les tests
// (cœur réel, base temporaire, horloge contrôlable). Réservé aux tests.
package agentlinktest

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
)

// Clock est une horloge manuelle.
type Clock struct {
	mu sync.Mutex
	t  time.Time
}

// Now implémente Deps.Now.
func (c *Clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }

// Advance avance l'horloge.
func (c *Clock) Advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

// Env regroupe le serveur de test.
type Env struct {
	T      *testing.T
	Core   *core.Core
	Reg    *agentlink.Registry
	Server *httptest.Server
	Clock  *Clock
}

// Quiet est un journal muet.
func Quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// New ouvre un cœur temporaire et monte les routes agent sur un serveur httptest.
func New(t *testing.T, cfg agentlink.Config) *Env {
	t.Helper()
	c, err := core.Open(context.Background(), core.Config{DataDir: t.TempDir(), Logger: Quiet()})
	if err != nil {
		t.Fatal(err)
	}
	clk := &Clock{t: time.Now().UTC()}
	reg, err := agentlink.New(agentlink.Deps{Store: c.Store, Blobs: c.Blobs, Catalog: c.Catalog, Log: Quiet(), DataDir: c.Cfg.DataDir, Now: clk.Now}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(reg.Handler())
	e := &Env{T: t, Core: c, Reg: reg, Server: srv, Clock: clk}
	t.Cleanup(func() { srv.Close(); reg.WaitImports(); c.Close() })
	return e
}

// Pair appaire un agent et renvoie son client authentifié.
func (e *Env) Pair(name string) (*agentlink.Client, agentlink.PairResponse) {
	e.T.Helper()
	tok, _, err := e.Reg.NewPairing(context.Background())
	if err != nil {
		e.T.Fatal(err)
	}
	resp, err := agentlink.NewClient(e.Server.URL, "").Pair(context.Background(), agentlink.PairRequest{Token: tok, Name: name, Host: "PC-TEST", AgentVersion: "test"})
	if err != nil {
		e.T.Fatal(err)
	}
	return agentlink.NewClient(e.Server.URL, resp.Secret), resp
}

// AllCaps annonce toutes les capacités.
func AllCaps() agentlink.AgentCaps {
	return agentlink.AgentCaps{OS: "windows", Arch: "amd64", AgentVersion: "test",
		Studio:      agentlink.StudioCaps{Installed: true, Version: "02.08.02.61"},
		LocalSlicer: true, Tasks: []string{agentlink.KindInventory, agentlink.KindUploadFiles, agentlink.KindSlice, agentlink.KindGetProjectState, agentlink.KindOpenInStudio}}
}
