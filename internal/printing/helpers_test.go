package printing

import (
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// newBareService : service minimal (base temporaire, coffre en mémoire, aucun adaptateur réel).
func newBareService(t *testing.T) (*Service, func()) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	bl, err := store.NewBlobStore(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	st.AttachBlobs(bl)
	q := jobs.NewQueue(st, jobs.QueueConfig{})
	s, err := New(Deps{Store: st, Blobs: bl, Queue: q, Registry: jobs.NewRegistry(), Secrets: NewMemorySecretStore(), DataDir: dir,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return s, func() { s.Close(); st.Close() }
}

var _ = contracts.PrintDraft

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// loadFixtureProfiles : jeu de profils de test de internal/profiles (fournisseur fictif TST).
func loadFixtureProfiles(t *testing.T) *profiles.Store {
	t.Helper()
	ps, err := profiles.Load(filepath.Join("..", "profiles", "testdata", "vendor"))
	if err != nil {
		t.Fatal(err)
	}
	return ps
}
