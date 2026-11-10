package store

import (
	"context"
	"path/filepath"
	"testing"
)

// Ouverture d'une base d'avant la migration 0310 : les colonnes de D8 sont ajoutées, les anciennes photos gardent leurs
// valeurs (status 'ok', percent NULL) et rien n'est perdu.
func TestMigration0310OpensAnOlderDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Retour à l'état d'avant 0310 (uniquement dans ce test : une migration ne supprime jamais rien).
	for _, q := range []string{
		`DROP INDEX idx_camera_photos_progress`,
		`ALTER TABLE camera_photos DROP COLUMN percent`,
		`ALTER TABLE camera_photos DROP COLUMN status`,
		`ALTER TABLE camera_photos DROP COLUMN reason`,
		`DELETE FROM schema_version WHERE version>=310`,
		`INSERT INTO camera_photos(id,printer_id,print_job_id,kind,sha256,size,taken_at) VALUES('ph_old','pr_1','pj_1','final','abc',3,'2026-01-01T00:00:00Z')`,
	} {
		if _, err := s.DB().ExecContext(ctx, q); err != nil {
			t.Fatalf("%s : %v", q, err)
		}
	}
	s.Close()
	s2, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("réouverture d'une base d'avant 0310 : %v", err)
	}
	defer s2.Close()
	var status, reason string
	var pct *int
	if err := s2.DB().QueryRowContext(ctx, `SELECT status,reason,percent FROM camera_photos WHERE id='ph_old'`).Scan(&status, &reason, &pct); err != nil {
		t.Fatal(err)
	}
	if status != "ok" || reason != "" || pct != nil {
		t.Fatalf("ancienne photo altérée : %q %q %v", status, reason, pct)
	}
	if v, _ := s2.SchemaVersion(ctx); v < 310 {
		t.Fatalf("version %d", v)
	}
}
