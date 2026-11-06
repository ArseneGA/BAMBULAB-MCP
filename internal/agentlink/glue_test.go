package agentlink_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink/agentlinktest"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
)

// agentlink.For branche Registry.Run sur Core.Run (balayage + purge des .part) et Registry.Shutdown sur
// Core.Close (imports annulés et attendus avant la fermeture de la base).
func TestForStartsRegistryRunInCoreAndShutsDownOnClose(t *testing.T) {
	c, err := core.Open(context.Background(), core.Config{DataDir: t.TempDir(), Logger: agentlinktest.Quiet(),
		PollInterval: 10 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := agentlink.For(c)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := agentlink.For(c); again != reg {
		t.Fatal("For doit renvoyer le même registre")
	}
	// Un .part abandonné depuis 48 h : la purge fait partie de Registry.Run.
	old := filepath.Join(c.Cfg.DataDir, "agent-staging", "agt_x", strings.Repeat("c", 64)+".part")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(old, []byte("x"), 0o600)
	past := time.Now().Add(-48 * time.Hour)
	os.Chtimes(old, past, past)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = c.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(old); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Registry.Run n'a pas été démarré par Core.Run (le .part abandonné est resté)")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Core.Run n'attend/ne rend pas la main après l'annulation")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	reg.Shutdown() // idempotent après Close
}
