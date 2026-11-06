package store

import (
	"context"
	"errors"
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
