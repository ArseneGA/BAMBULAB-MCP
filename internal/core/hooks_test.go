package core

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// Tâches de fond et fonctions d'arrêt enregistrées par d'autres paquets (agentlink).
func TestRunAndCloseHooks(t *testing.T) {
	c, err := Open(context.Background(), Config{DataDir: t.TempDir(), Logger: quiet(),
		PollInterval: 10 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	var early, late, closed atomic.Bool
	c.AddRunHook(func(ctx context.Context) { early.Store(true); <-ctx.Done() })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = c.Run(ctx) }()
	wait := func(b *atomic.Bool, what string) {
		for i := 0; i < 200 && !b.Load(); i++ {
			time.Sleep(10 * time.Millisecond)
		}
		if !b.Load() {
			t.Fatal(what)
		}
	}
	wait(&early, "hook enregistré avant Run non démarré")
	c.AddRunHook(func(ctx context.Context) { late.Store(true); <-ctx.Done() }) // enregistré pendant Run : démarre aussitôt
	wait(&late, "hook enregistré pendant Run non démarré")
	c.AddCloseHook(func() {
		if _, err := c.Store.SchemaVersion(context.Background()); err != nil {
			t.Errorf("la base doit encore être ouverte pendant les hooks d'arrêt : %v", err)
		}
		closed.Store(true)
	})
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run doit rendre la main quand ses hooks se terminent")
	}
	if err := c.Close(); err != nil || !closed.Load() {
		t.Fatalf("Close : %v, hook d'arrêt appelé = %v", err, closed.Load())
	}
}
