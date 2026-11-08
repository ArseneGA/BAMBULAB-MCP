package humanauth_test

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newSvc(t *testing.T) (*humanauth.Service, *clock) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "b.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ck := &clock{t: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)}
	return humanauth.New(st, humanauth.Options{Now: ck.now}), ck
}

func TestCodeFormatAndSingleUse(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	code, exp, err := s.NewEnrollCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[A-HJ-NP-Z2-9]{8}$`).MatchString(code) {
		t.Fatalf("code %q : 8 caractères sans ambiguïté attendus", code)
	}
	if exp.Sub(time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)) != 10*time.Minute {
		t.Fatalf("durée de validité : %v", exp)
	}
	if got := humanauth.FormatCode(code); len(got) != 9 || got[4] != '-' || humanauth.NormalizeCode(strings.ToLower(got)) != code {
		t.Fatalf("format %q", got)
	}
	tok, sess, err := s.Enroll(ctx, strings.ToLower(humanauth.FormatCode(code)), "Firefox")
	if err != nil || tok == "" || sess.ID == "" {
		t.Fatalf("enroll : %v", err)
	}
	if _, _, err := s.Enroll(ctx, code, ""); err != humanauth.ErrBadCode {
		t.Fatalf("réutilisation du code : %v", err)
	}
	got, err := s.Lookup(ctx, tok)
	if err != nil || got.ID != sess.ID || got.Label != "Firefox" {
		t.Fatalf("lookup : %+v %v", got, err)
	}
	if _, err := s.Lookup(ctx, tok+"x"); err != humanauth.ErrNoSession {
		t.Fatalf("jeton forgé : %v", err)
	}
}

func TestCodeExpiresAfterTenMinutes(t *testing.T) {
	s, ck := newSvc(t)
	code, _, _ := s.NewEnrollCode(context.Background())
	ck.add(10*time.Minute + time.Second)
	if _, _, err := s.Enroll(context.Background(), code, ""); err != humanauth.ErrBadCode {
		t.Fatalf("code expiré accepté : %v", err)
	}
}

func TestLockoutAfterRepeatedFailures(t *testing.T) {
	s, ck := newSvc(t)
	ctx := context.Background()
	code, _, _ := s.NewEnrollCode(ctx)
	for i := 0; i < 6; i++ {
		if _, _, err := s.Enroll(ctx, "ZZZZZZZ"+string(rune('2'+i)), ""); err != humanauth.ErrBadCode {
			t.Fatalf("essai %d : %v", i, err)
		}
	}
	// Verrouillé : même le bon code est refusé pendant 5 minutes.
	if _, _, err := s.Enroll(ctx, code, ""); err != humanauth.ErrLocked {
		t.Fatalf("pas de verrouillage : %v", err)
	}
	ck.add(5*time.Minute + time.Second)
	if _, _, err := s.Enroll(ctx, code, ""); err != nil {
		t.Fatalf("après le délai : %v", err)
	}
}

// Un préfixe commun à plusieurs sessions actives est refusé avec une erreur claire, et rien n'est révoqué.
func TestRevokeAmbiguousPrefix(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	var toks []string
	for _, n := range []string{"un", "deux"} {
		c, _, _ := s.NewEnrollCode(ctx)
		tok, _, err := s.Enroll(ctx, c, n)
		if err != nil {
			t.Fatal(err)
		}
		toks = append(toks, tok)
	}
	n, err := s.Revoke(ctx, "ses_") // « ses_ » préfixe tous les identifiants : ambigu par construction
	if err == nil || n != 0 || !strings.Contains(err.Error(), "ambigu") || !strings.Contains(err.Error(), "2 sessions") {
		t.Fatalf("préfixe ambigu : %d %v", n, err)
	}
	for _, tok := range toks {
		if _, err := s.Lookup(ctx, tok); err != nil {
			t.Fatalf("aucune session ne doit être révoquée par un préfixe ambigu : %v", err)
		}
	}
}

func TestSessionExpiryRevokeAndList(t *testing.T) {
	s, ck := newSvc(t)
	ctx := context.Background()
	c1, _, _ := s.NewEnrollCode(ctx)
	t1, s1, _ := s.Enroll(ctx, c1, "un")
	c2, _, _ := s.NewEnrollCode(ctx)
	t2, s2, _ := s.Enroll(ctx, c2, "deux")
	if n, err := s.Revoke(ctx, s1.ID); err != nil || n != 1 {
		t.Fatalf("révocation par préfixe : %d %v", n, err)
	}
	if _, err := s.Lookup(ctx, t1); err != humanauth.ErrSessionGone {
		t.Fatalf("session révoquée : %v", err)
	}
	if _, err := s.Lookup(ctx, t2); err != nil {
		t.Fatalf("l'autre session doit rester valide : %v", err)
	}
	c3, _, _ := s.NewEnrollCode(ctx)
	if _, _, err := s.Enroll(ctx, c3, "trois"); err != nil {
		t.Fatal(err)
	}
	ss, _ := s.List(ctx)
	if len(ss) != 3 {
		t.Fatalf("liste : %+v", ss)
	}
	ck.add(31 * 24 * time.Hour)
	if _, err := s.Lookup(ctx, t2); err != humanauth.ErrSessionGone {
		t.Fatalf("session expirée : %v (%s)", err, s2.ID)
	}
}
