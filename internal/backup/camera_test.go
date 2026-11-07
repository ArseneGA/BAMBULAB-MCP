package backup_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/backup"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Les photos de la caméra (camera_photos) sont sauvegardées puis restaurées, et relisibles par le service.
func TestCameraPhotosSurviveBackupRestore(t *testing.T) {
	ctx := context.Background()
	src := t.TempDir()
	c, err := core.Open(ctx, core.Config{DataDir: src, Logger: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	jpg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 'p', 'h', 'o', 't', 'o', 0xFF, 0xD9}
	sha, _, err := c.Blobs.Put(bytes.NewReader(jpg), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Store.DB().Exec(`INSERT INTO camera_photos(id,printer_id,print_job_id,kind,sha256,size,taken_at) VALUES('ph_1','pr_1','pj_1','final',?,?,?)`,
		sha, len(jpg), store.FormatTime(time.Now())); err != nil {
		t.Fatal(err)
	}
	c.Close()
	out := filepath.Join(t.TempDir(), "a.tar.gz")
	m, err := backup.Create(ctx, src, out, backup.CreateOptions{Version: "test"})
	if err != nil || m.BlobCount < 1 {
		t.Fatalf("sauvegarde : %v %+v", err, m)
	}
	dst := filepath.Join(t.TempDir(), "neuf")
	if _, err := backup.Restore(ctx, out, dst, backup.RestoreOptions{MaxSchema: store.LatestSchemaVersion()}); err != nil {
		t.Fatal(err)
	}
	c2, err := core.Open(ctx, core.Config{DataDir: dst, Logger: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	got, err := c2.Camera.OpenPhoto(ctx, sha)
	if err != nil || !bytes.Equal(got, jpg) {
		t.Fatalf("photo non relisible après restauration : %v", err)
	}
}
