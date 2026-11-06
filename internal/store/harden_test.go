package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

func testBinding() contracts.ConfirmationBinding {
	return contracts.ConfirmationBinding{PrinterID: "p1", ArtifactSHA256: "aa", Plate: 1}
}

func putConfirmation(t *testing.T, s *Store, ttl time.Duration) (string, string) {
	t.Helper()
	c, tok, err := contracts.NewConfirmation("conf1", "job1", testBinding(), ttl, s.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutDoc(context.Background(), "t", "print_confirmations", c.ID, c); err != nil {
		t.Fatal(err)
	}
	return c.ID, tok
}

func TestSynchronousFull(t *testing.T) {
	s, _ := openTemp(t)
	var v int
	if err := s.DB().QueryRow(`PRAGMA synchronous`).Scan(&v); err != nil || v != 2 {
		t.Fatalf("synchronous=%d (attendu 2=FULL) err=%v", v, err)
	}
	if err := s.DB().QueryRow(`PRAGMA recursive_triggers`).Scan(&v); err != nil || v != 1 {
		t.Fatalf("recursive_triggers=%d err=%v", v, err)
	}
}

func TestConsumeConfirmationConcurrent(t *testing.T) {
	s, _ := openTemp(t)
	id, tok := putConfirmation(t, s, time.Minute)
	var ok, used int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.ConsumeConfirmation(context.Background(), id, tok, testBinding(), "job1")
			switch {
			case err == nil:
				atomic.AddInt32(&ok, 1)
			case errors.Is(err, contracts.ErrConfirmationUsed):
				atomic.AddInt32(&used, 1)
			default:
				t.Errorf("erreur inattendue : %v", err)
			}
		}()
	}
	wg.Wait()
	if ok != 1 || used != 19 {
		t.Fatalf("succès=%d déjà-utilisée=%d", ok, used)
	}
}

func TestConsumeConfirmationChecks(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	id, tok := putConfirmation(t, s, time.Minute)
	other := testBinding()
	other.Plate = 2
	cases := []struct {
		name  string
		token string
		b     contracts.ConfirmationBinding
		job   string
		want  error
	}{
		{"jeton", "faux", testBinding(), "job1", contracts.ErrConfirmationToken},
		{"liaison", tok, other, "job1", contracts.ErrConfirmationMismatch},
		{"job", tok, testBinding(), "autre", contracts.ErrConfirmationMismatch},
	}
	for _, c := range cases {
		if err := s.ConsumeConfirmation(ctx, id, c.token, c.b, c.job); !errors.Is(err, c.want) {
			t.Errorf("%s : %v", c.name, err)
		}
	}
	// Les échecs ne consomment rien.
	if err := s.ConsumeConfirmation(ctx, id, tok, testBinding(), "job1"); err != nil {
		t.Fatalf("la confirmation aurait dû rester utilisable : %v", err)
	}
	if err := s.ConsumeConfirmation(ctx, "inconnue", tok, testBinding(), "job1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("attendu ErrNotFound : %v", err)
	}
}

func TestConsumeConfirmationExpired(t *testing.T) {
	s, _ := openTemp(t)
	id, tok := putConfirmation(t, s, time.Nanosecond)
	time.Sleep(5 * time.Millisecond)
	if err := s.ConsumeConfirmation(context.Background(), id, tok, testBinding(), "job1"); !errors.Is(err, contracts.ErrConfirmationExpired) {
		t.Fatalf("attendu expirée : %v", err)
	}
}

// PutDoc ne doit pas pouvoir « ressusciter » un jeton consommé.
func TestPutDocCannotResetUsedAt(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	id, tok := putConfirmation(t, s, time.Minute)
	var before contracts.PrintConfirmation
	if err := s.GetDoc(ctx, "print_confirmations", id, &before); err != nil {
		t.Fatal(err)
	}
	if err := s.ConsumeConfirmation(ctx, id, tok, testBinding(), "job1"); err != nil {
		t.Fatal(err)
	}
	// before.UsedAt == nil : l'ancien document, réécrit tel quel.
	if err := s.PutDoc(ctx, "t", "print_confirmations", id, before); err == nil {
		t.Fatal("PutDoc a pu réécrire une confirmation utilisée")
	}
	if err := s.ConsumeConfirmation(ctx, id, tok, testBinding(), "job1"); !errors.Is(err, contracts.ErrConfirmationUsed) {
		t.Fatalf("le jeton est redevenu utilisable : %v", err)
	}
	// Et on ne peut pas marquer used_at via PutDoc.
	now := time.Now()
	before.ID = "conf2"
	before.UsedAt = &now
	if err := s.PutDoc(ctx, "t", "print_confirmations", "conf2", before); err == nil {
		t.Fatal("PutDoc a accepté used_at")
	}
}

func TestPutDocIfRev(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	doc := map[string]any{"a": 1}
	rev, err := s.PutDocIfRev(ctx, "t", "printers", "x", doc, 0)
	if err != nil || rev != 1 {
		t.Fatalf("création : rev=%d err=%v", rev, err)
	}
	if _, err := s.PutDocIfRev(ctx, "t", "printers", "x", doc, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("recréation : %v", err)
	}
	var out map[string]any
	r, err := s.GetDocRev(ctx, "printers", "x", &out)
	if err != nil || r != 1 {
		t.Fatalf("GetDocRev %d %v", r, err)
	}
	if rev, err = s.PutDocIfRev(ctx, "t", "printers", "x", map[string]any{"a": 2}, 1); err != nil || rev != 2 {
		t.Fatalf("mise à jour : %d %v", rev, err)
	}
	if _, err := s.PutDocIfRev(ctx, "t", "printers", "x", map[string]any{"a": 3}, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("version périmée acceptée : %v", err)
	}
	if err := s.PutDoc(ctx, "t", "printers", "x", map[string]any{"a": 4}); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.GetDocRev(ctx, "printers", "x", &out); r != 3 {
		t.Fatalf("PutDoc doit incrémenter rev : %d", r)
	}
}

func catalogWithBlobs(t *testing.T) (*Store, *BlobStore) {
	t.Helper()
	s, dir := openTemp(t)
	b, err := NewBlobStore(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	s.AttachBlobs(b)
	return s, b
}

func TestAddSourceFileChecksBlob(t *testing.T) {
	ctx := context.Background()
	s, b := catalogWithBlobs(t)
	m, _ := s.CreateModel(ctx, "t", contracts.Model{Name: "M"})
	data := []byte("solid x\nendsolid x\n")
	sha, size, err := b.Put(bytes.NewReader(data), "")
	if err != nil {
		t.Fatal(err)
	}
	mk := func(sha string, size int64) contracts.SourceFile {
		return contracts.SourceFile{ModelID: m.ID, Name: "a.stl", Format: contracts.FormatSTL, SizeBytes: size, SHA256: sha, Availability: contracts.AvailableLinux}
	}
	if _, err := s.AddSourceFile(ctx, "t", mk(strings.Repeat("c", 64), size)); !errors.Is(err, ErrNotFound) {
		t.Errorf("blob absent accepté : %v", err)
	}
	if _, err := s.AddSourceFile(ctx, "t", mk(sha, size+1)); err == nil {
		t.Error("taille erronée acceptée")
	}
	if _, err := s.AddSourceFile(ctx, "t", mk(sha, size)); err != nil {
		t.Errorf("fichier valide refusé : %v", err)
	}
	// Un fichier « windows_only » n'a pas de blob côté Linux.
	w := mk(strings.Repeat("d", 64), 5)
	w.Availability = contracts.AvailableWindowsOnly
	if _, err := s.AddSourceFile(ctx, "t", w); err != nil {
		t.Errorf("windows_only refusé : %v", err)
	}
}

func TestCreateVersionChecksOwnership(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	m1, _ := s.CreateModel(ctx, "t", contracts.Model{Name: "M1"})
	m2, _ := s.CreateModel(ctx, "t", contracts.Model{Name: "M2"})
	sha := strings.Repeat("ab", 32)
	f1, _ := s.AddSourceFile(ctx, "t", contracts.SourceFile{ModelID: m1.ID, Name: "a.stl", Format: contracts.FormatSTL, SHA256: sha, Availability: contracts.AvailableLinux})
	v1, err := s.CreateVersion(ctx, "t", contracts.ModelVersion{ModelID: m1.ID, Label: "v1", SourceFileIDs: []string{f1.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateVersion(ctx, "t", contracts.ModelVersion{ModelID: "inconnu", Label: "x"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("modèle inexistant : %v", err)
	}
	if _, err := s.CreateVersion(ctx, "t", contracts.ModelVersion{ModelID: m2.ID, Label: "x", SourceFileIDs: []string{f1.ID}}); err == nil {
		t.Error("fichier d'un autre modèle accepté")
	}
	if _, err := s.CreateVersion(ctx, "t", contracts.ModelVersion{ModelID: m1.ID, Label: "x", SourceFileIDs: []string{"src_inconnu"}}); !errors.Is(err, ErrNotFound) {
		t.Errorf("fichier inconnu : %v", err)
	}
	if _, err := s.CreateVersion(ctx, "t", contracts.ModelVersion{ModelID: m1.ID, Label: "x", SourceFileIDs: []string{f1.ID, f1.ID}}); err == nil {
		t.Error("doublon accepté")
	}
	if _, err := s.CreateVersion(ctx, "t", contracts.ModelVersion{ModelID: m2.ID, Label: "x", ParentID: &v1.ID}); err == nil {
		t.Error("parent d'un autre modèle accepté")
	}
	bad := "ver_inconnue"
	if _, err := s.CreateVersion(ctx, "t", contracts.ModelVersion{ModelID: m1.ID, Label: "x", ParentID: &bad}); !errors.Is(err, ErrNotFound) {
		t.Errorf("parent inconnu : %v", err)
	}
	if _, err := s.CreateVersion(ctx, "t", contracts.ModelVersion{ModelID: m1.ID, Label: "v2", ParentID: &v1.ID, SourceFileIDs: []string{f1.ID}}); err != nil {
		t.Errorf("version valide refusée : %v", err)
	}
}

func TestSourceFilesAndVersionsImmutable(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)
	m, _ := s.CreateModel(ctx, "t", contracts.Model{Name: "M"})
	f, _ := s.AddSourceFile(ctx, "t", contracts.SourceFile{ModelID: m.ID, Name: "a.stl", Format: contracts.FormatSTL, SHA256: strings.Repeat("ab", 32), Availability: contracts.AvailableLinux})
	v, _ := s.CreateVersion(ctx, "t", contracts.ModelVersion{ModelID: m.ID, Label: "v1"})
	for _, q := range []string{
		`UPDATE source_files SET sha256='cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc' WHERE id='` + f.ID + `'`,
		`DELETE FROM source_files WHERE id='` + f.ID + `'`,
		// INSERT OR REPLACE supprime l'ancienne ligne : sans recursive_triggers, le déclencheur de suppression ne se déclenche pas.
		`INSERT OR REPLACE INTO source_files(id,model_id,name,format,size_bytes,sha256,availability,created_at) VALUES('` + f.ID + `','` + m.ID + `','y','stl',0,'` + strings.Repeat("ab", 32) + `','linux','2020-01-01T00:00:00Z')`,
		`INSERT OR REPLACE INTO model_versions(id,model_id,label,created_at) VALUES('` + v.ID + `','` + m.ID + `','hack','2020-01-01T00:00:00Z')`,
	} {
		if _, err := s.DB().Exec(q); err == nil {
			t.Errorf("mutation acceptée : %s", q)
		}
	}
}

func TestBlobConcurrentPutSameContent(t *testing.T) {
	_, dir := openTemp(t)
	b, err := NewBlobStore(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("contenu identique "), 50000)
	var wg sync.WaitGroup
	shas := make([]string, 20)
	for i := range shas {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sha, _, err := b.Put(bytes.NewReader(data), "")
			if err != nil {
				t.Errorf("Put concurrent #%d : %v", i, err)
			}
			shas[i] = sha
		}(i)
	}
	wg.Wait()
	if err := b.Verify(shas[0]); err != nil {
		t.Fatal(err)
	}
	for _, sh := range shas {
		if sh != shas[0] {
			t.Fatal("empreintes divergentes")
		}
	}
	// Réécriture d'un blob existant en lecture seule : succès sans toucher à l'original.
	if _, _, err := b.Put(bytes.NewReader(data), shas[0]); err != nil {
		t.Fatalf("réécriture : %v", err)
	}
}

func TestBlobMaxSize(t *testing.T) {
	_, dir := openTemp(t)
	b, _ := NewBlobStore(filepath.Join(dir, "blobs"))
	b.SetMaxBytes(100)
	if _, _, err := b.Put(bytes.NewReader(make([]byte, 101)), ""); !errors.Is(err, ErrBlobTooLarge) {
		t.Fatalf("attendu ErrBlobTooLarge : %v", err)
	}
	if _, _, err := b.Put(bytes.NewReader(make([]byte, 100)), ""); err != nil {
		t.Fatalf("100 octets refusés : %v", err)
	}
	if ents, _ := os.ReadDir(filepath.Join(dir, "blobs", "tmp")); len(ents) != 0 {
		t.Fatalf("résidus temporaires : %d", len(ents))
	}
}
