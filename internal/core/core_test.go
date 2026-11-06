package core

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestEnvironmentAndCapabilities(t *testing.T) {
	ctx := context.Background()
	c, err := Open(ctx, Config{DataDir: t.TempDir(), Logger: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	env, err := c.Environment(ctx)
	if err != nil || !env.DataDirWritable || env.SchemaVersion < 1 || env.Missing == nil {
		t.Fatalf("%+v %v", env, err)
	}
	cp := c.Capabilities()
	if cp.Concurrency["heavy"] != 1 || cp.Concurrency["light"] != 4 {
		t.Fatalf("concurrence : %v", cp.Concurrency)
	}
	for _, f := range cp.Features {
		if f.ID == "slicing" && f.Available {
			t.Fatal("slicing annoncé disponible alors qu'il n'existe pas")
		}
	}
	if len(cp.JobKinds) == 0 || cp.JobKinds[0] != "system.selftest" {
		t.Fatalf("kinds : %v", cp.JobKinds)
	}
}

func TestOpenAppliesRecovery(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	c, err := Open(ctx, Config{DataDir: dir, Logger: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	a, _, _ := c.Queue.Enqueue(ctx, jobs.EnqueueParams{Kind: "system.selftest"})
	b, _, _ := c.Queue.Enqueue(ctx, jobs.EnqueueParams{Kind: "start_print", IdempotencyKey: "print-core-1"})
	c.Queue.Claim(ctx, "w")
	c.Queue.Claim(ctx, "w")
	c.Close() // arrêt brutal simulé : deux travaux restent 'running'

	c2, err := Open(ctx, Config{DataDir: dir, Logger: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if len(c2.Recovery.Requeued) != 1 || c2.Recovery.Requeued[0] != a.ID || len(c2.Recovery.Unknown) != 1 || c2.Recovery.Unknown[0] != b.ID {
		t.Fatalf("reprise : %+v", c2.Recovery)
	}
	if j, _ := c2.Queue.Get(ctx, b.ID); j.State != contracts.JobUnknown {
		t.Fatalf("start_print : %s", j.State)
	}
}
