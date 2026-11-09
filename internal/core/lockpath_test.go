package core

import (
	"errors"
	"path/filepath"
	"testing"
)

// Une seule mise à jour à la fois : le second verrou échoue tant que le premier est tenu.
func TestLockPathIsExclusive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "atelier-update.lock")
	release, err := LockPath(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LockPath(p); !errors.Is(err, ErrLocked) {
		t.Fatalf("second verrou : %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	again, err := LockPath(p)
	if err != nil {
		t.Fatalf("après libération : %v", err)
	}
	again()
}
