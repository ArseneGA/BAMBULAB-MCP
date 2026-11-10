package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestProjectTrackingMigrationOnOlderDatabase : une base créée AVANT la migration 0300 (G9) s'ouvre ; l'existant est
// intact, les nouvelles colonnes de print_logs prennent leurs valeurs par défaut, les tables de projets sont vides.
func TestProjectTrackingMigrationOnOlderDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	if _, err := raw.Exec(`PRAGMA foreign_keys(0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE schema_version (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	applied := 0
	for _, m := range ms {
		if m.version >= 300 {
			continue
		}
		if _, err := raw.Exec(m.sql); err != nil {
			t.Fatalf("migration %s : %v", m.name, err)
		}
		if _, err := raw.Exec(`INSERT INTO schema_version(version,name,applied_at) VALUES(?,?,?)`, m.version, m.name, "2026-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
		applied++
	}
	if applied < 10 {
		t.Fatalf("seulement %d migrations d'avant appliquées", applied)
	}
	if _, err := raw.Exec(`INSERT INTO print_logs(id,print_job_id,printer_id,model_id,material_family,outcome,finished_at,doc,created_at,updated_at)
		VALUES('log_old','pj_old','pr1','mod_old','PLA','success','2026-10-01T10:00:00Z','{"id":"log_old"}','2026-10-01T10:00:00Z','2026-10-01T10:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("ouverture d'une base d'avant : %v", err)
	}
	defer s.Close()
	if v, _ := s.SchemaVersion(ctx); v != LatestSchemaVersion() || v < 300 {
		t.Fatalf("version %d, binaire %d", v, LatestSchemaVersion())
	}
	var verdict, reason, vat, vsrc, origin string
	var parts, reached int
	var outcome string
	if err := s.DB().QueryRow(`SELECT outcome, verdict, fail_reason, verdict_at, verdict_source, origin, part_count, reached_100 FROM print_logs WHERE id='log_old'`).
		Scan(&outcome, &verdict, &reason, &vat, &vsrc, &origin, &parts, &reached); err != nil {
		t.Fatal(err)
	}
	if outcome != "success" || verdict != "" || reason != "" || vat != "" || vsrc != "" || origin != "printalk" || parts != 1 || reached != 0 {
		t.Fatalf("valeurs par défaut : %q %q %q %q %q %q %d %d", outcome, verdict, reason, vat, vsrc, origin, parts, reached)
	}
	for _, tb := range []string{"projects", "project_parts", "entity_notes", "project_variants", "project_variant_parts"} {
		var n int
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM ` + tb).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s : %d lignes, %v", tb, n, err)
		}
	}
}
