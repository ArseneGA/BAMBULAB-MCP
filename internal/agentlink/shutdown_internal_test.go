package agentlink

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Après Shutdown : le contexte des imports est annulé et plus aucun import ne démarre (rien n'écrit dans la
// base pendant ou après sa fermeture) ; Shutdown est idempotent.
func TestShutdownCancelsAndRefusesBackgroundImports(t *testing.T) {
	c, err := core.Open(context.Background(), core.Config{DataDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r, err := New(Deps{Store: c.Store, Blobs: c.Blobs, Catalog: c.Catalog, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DataDir: c.Cfg.DataDir}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if r.bgCtx.Err() != nil {
		t.Fatal("contexte d'arrêt déjà annulé")
	}
	// Un fichier reçu en attente d'import.
	agent := "agt_t"
	dir := filepath.Join(r.inbox, agent, "Modeles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.stl"), []byte("solid a\nfacet\nendsolid a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := store.FormatTime(r.now())
	if _, err := c.Store.DB().Exec(`INSERT INTO agents(id,name,secret_hash,created_at) VALUES(?,?,?,?)`, agent, "PC", "h", now); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Store.DB().Exec(`INSERT INTO agent_inventory(agent_id,root_label,rel_path,size_bytes,mtime_ns,sha256,state,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		agent, "Modeles", "a.stl", 24, 1, "00", "uploaded", now); err != nil {
		t.Fatal(err)
	}
	r.Shutdown()
	r.Shutdown() // idempotent
	if r.bgCtx.Err() == nil {
		t.Fatal("le contexte des imports doit être annulé par Shutdown")
	}
	r.onTaskFinished(TaskInfo{Kind: KindUploadFiles, AgentID: agent}, TaskResult{})
	r.WaitImports()
	var state string
	if err := c.Store.DB().QueryRow(`SELECT state FROM agent_inventory WHERE agent_id=?`, agent).Scan(&state); err != nil || state != "uploaded" {
		t.Fatalf("un import a démarré après Shutdown : état %q (%v)", state, err)
	}
}
