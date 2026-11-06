package printing

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
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
