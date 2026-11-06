package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "bambu.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}

func TestMigrationsAndReopen(t *testing.T) {
	ctx := context.Background()
	s, dir := openTemp(t)
	v, err := s.SchemaVersion(ctx)
	if err != nil || v != 1 {
		t.Fatalf("version %d err %v", v, err)
	}
	var mode string
	if err := s.DB().QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode=%q err=%v", mode, err)
	}
	s.Close()
	// Réouverture : migrations idempotentes.
	s2, err := Open(filepath.Join(dir, "bambu.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	var n int
	_ = s2.DB().QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&n)
	if n != 1 {
		t.Fatalf("migration réappliquée : %d lignes", n)
	}
}

func TestCatalogAuditAndImmutability(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	col, err := s.CreateCollection(ctx, "test", contracts.Collection{Name: "Atelier"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.CreateModel(ctx, "test", contracts.Model{Name: "Pince", CollectionID: &col.ID, Tags: []string{"pince"}})
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("ab", 32)
	if _, err := s.AddSourceFile(ctx, "test", contracts.SourceFile{ModelID: m.ID, Name: "a.stl", Format: contracts.FormatSTL, SizeBytes: 3, SHA256: sha, Availability: contracts.AvailableLinux}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddSourceFile(ctx, "test", contracts.SourceFile{ModelID: m.ID, Name: "b.stl", Format: "exe", SHA256: sha, Availability: contracts.AvailableLinux}); err == nil {
		t.Fatal("format invalide accepté")
	}
	dups, err := s.FindExactDuplicates(ctx, sha)
	if err != nil || len(dups) != 1 {
		t.Fatalf("dups %v %v", dups, err)
	}
	v1, err := s.CreateVersion(ctx, "test", contracts.ModelVersion{ModelID: m.ID, Label: "v1", SourceFileIDs: []string{dups[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE model_versions SET note='x' WHERE id=?`, v1.ID); err == nil {
		t.Fatal("une version doit être immuable (UPDATE accepté)")
	}
	if _, err := s.DB().Exec(`DELETE FROM model_versions WHERE id=?`, v1.ID); err == nil {
		t.Fatal("une version doit être immuable (DELETE accepté)")
	}
	if err := s.TrashModel(ctx, "test", m.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetModel(ctx, m.ID)
	if got.DeletedAt == nil {
		t.Fatal("corbeille non appliquée")
	}
	if err := s.RestoreModel(ctx, "test", m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetModel(ctx, "inconnu"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("attendu ErrNotFound, obtenu %v", err)
	}
	log, err := s.ListAudit(ctx, "", "", 50)
	if err != nil || len(log) < 6 {
		t.Fatalf("audit : %d entrées, %v", len(log), err)
	}
	one, _ := s.ListAudit(ctx, "model", m.ID, 10)
	if len(one) != 3 { // create, trash, restore
		t.Fatalf("audit modèle : %d", len(one))
	}
}

func TestMutationRollbackLeavesNoAudit(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	// source_files avec modèle inexistant : clé étrangère -> échec -> pas d'audit.
	_, err := s.AddSourceFile(ctx, "t", contracts.SourceFile{ModelID: "nope", Name: "a", Format: contracts.FormatSTL, SHA256: strings.Repeat("a", 64), Availability: contracts.AvailableLinux})
	if err == nil {
		t.Fatal("clé étrangère non appliquée")
	}
	log, _ := s.ListAudit(ctx, "", "", 10)
	if len(log) != 0 {
		t.Fatalf("audit orphelin : %v", log)
	}
}

func TestDocs(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	p := contracts.SeedPrinterModels()[0]
	if err := s.PutDoc(ctx, "t", "printer_models", p.ID, p); err != nil {
		t.Fatal(err)
	}
	var back contracts.PrinterModel
	if err := s.GetDoc(ctx, "printer_models", p.ID, &back); err != nil || back.Name != p.Name || back.MaxBedTempC != nil {
		t.Fatalf("%+v %v", back, err)
	}
	if err := s.PutDoc(ctx, "t", "users; DROP TABLE x", "1", p); err == nil {
		t.Fatal("table non listée acceptée")
	}
}

func TestBlobs(t *testing.T) {
	_, dir := openTemp(t)
	b, err := NewBlobStore(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("solid cube\nendsolid")
	sum := sha256.Sum256(data)
	want := hex.EncodeToString(sum[:])
	sha, size, err := b.Put(bytes.NewReader(data), want)
	if err != nil || sha != want || size != int64(len(data)) {
		t.Fatalf("put %s %d %v", sha, size, err)
	}
	p, _ := b.Path(sha)
	if !strings.Contains(filepath.ToSlash(p), "/"+sha[:2]+"/"+sha[2:4]+"/"+sha) {
		t.Fatalf("chemin inattendu %s", p)
	}
	if err := b.Verify(sha); err != nil {
		t.Fatal(err)
	}
	// Écriture répétée : idempotente.
	if _, _, err := b.Put(bytes.NewReader(data), ""); err != nil {
		t.Fatal(err)
	}
	// Empreinte attendue fausse : rien n'est écrit.
	_, _, err = b.Put(bytes.NewReader([]byte("autre")), want)
	if !errors.Is(err, ErrBlobHashMismatch) {
		t.Fatalf("attendu mismatch, obtenu %v", err)
	}
	// Aucun résidu temporaire.
	if ents, _ := os.ReadDir(filepath.Join(dir, "blobs", "tmp")); len(ents) != 0 {
		t.Fatalf("fichiers temporaires restants : %d", len(ents))
	}
	// Corruption détectée.
	_ = os.Chmod(p, 0o644)
	if err := os.WriteFile(p, []byte("corrompu"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := b.Verify(sha); !errors.Is(err, ErrBlobCorrupt) {
		t.Fatalf("corruption non détectée : %v", err)
	}
	// Traversée de chemin refusée.
	for _, bad := range []string{"../../etc/passwd", strings.Repeat("A", 64), "abc"} {
		if _, err := b.Path(bad); err == nil {
			t.Errorf("empreinte %q acceptée", bad)
		}
	}
	if _, err := b.Open(strings.Repeat("0", 64)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("attendu ErrNotFound, obtenu %v", err)
	}
}
