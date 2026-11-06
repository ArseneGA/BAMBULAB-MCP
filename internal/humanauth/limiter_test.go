package humanauth_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
)

func failN(s *humanauth.Service, key string, n int) {
	for i := 0; i < n; i++ {
		_, _, _ = s.EnrollFrom(context.Background(), key, "ZZZZZZZZ", "")
	}
}

// Le verrou est par adresse client : un attaquant verrouillé ne bloque pas un autre navigateur.
func TestLockoutIsPerClient(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	code, _, _ := s.NewEnrollCode(ctx)
	failN(s, "198.51.100.7", 6)
	if _, _, err := s.EnrollFrom(ctx, "198.51.100.7", code, ""); err != humanauth.ErrLocked {
		t.Fatalf("l'attaquant n'est pas verrouillé : %v", err)
	}
	if _, _, err := s.EnrollFrom(ctx, "203.0.113.9", code, "bureau"); err != nil {
		t.Fatalf("un autre client est bloqué par le verrou de l'attaquant : %v", err)
	}
}

// Backoff par adresse : le deuxième verrou dure deux fois plus longtemps (5 min, puis 10 min).
func TestPerClientBackoff(t *testing.T) {
	s, ck := newSvc(t)
	ctx := context.Background()
	failN(s, "198.51.100.7", 6)
	ck.add(5*time.Minute + time.Second)
	failN(s, "198.51.100.7", 6)
	ck.add(5*time.Minute + time.Second)
	if _, _, err := s.EnrollFrom(ctx, "198.51.100.7", "ZZZZZZZZ", ""); err != humanauth.ErrLocked {
		t.Fatalf("le 2e verrou aurait dû durer 10 min : %v", err)
	}
	ck.add(5 * time.Minute)
	if _, _, err := s.EnrollFrom(ctx, "198.51.100.7", "ZZZZZZZZ", ""); err != humanauth.ErrBadCode {
		t.Fatalf("après 10 min le verrou doit être levé : %v", err)
	}
}

// Plafond global : il faut de nombreuses adresses distinctes pour le déclencher (un seul attaquant, même
// acharné, ne bloque pas tout le monde) ; il se lève avec le temps (premier palier : 1 minute).
func TestGlobalCeilingNeedsManyClients(t *testing.T) {
	s, ck := newSvc(t)
	ctx := context.Background()
	code, _, _ := s.NewEnrollCode(ctx)
	failN(s, "198.51.100.7", 500) // verrouillé après 6 : le reste ne compte plus
	if _, _, err := s.EnrollFrom(ctx, "203.0.113.9", code, "x"); err != nil {
		t.Fatalf("un seul attaquant a bloqué tout le monde : %v", err)
	}
	code2, _, _ := s.NewEnrollCode(ctx)
	for k := 0; k < 10; k++ { // une armée d'adresses : 10 x 6 échecs
		failN(s, fmt.Sprintf("192.0.2.%d", k+1), 6)
	}
	if _, _, err := s.EnrollFrom(ctx, "203.0.113.50", code2, "y"); err != humanauth.ErrLocked {
		t.Fatalf("plafond global non déclenché : %v", err)
	}
	ck.add(time.Minute + time.Second)
	if _, _, err := s.EnrollFrom(ctx, "203.0.113.50", code2, "y"); err != nil {
		t.Fatalf("le plafond global doit se lever après 1 min : %v", err)
	}
}
