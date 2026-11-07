package printing

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfinedPathUsesCanonicalForms(t *testing.T) {
	work := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "ok.stl"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(work, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	// relatif, absolu, et dossier de travail écrit de façon non canonique
	for _, c := range []struct{ work, path string }{
		{work, "ok.stl"}, {work, filepath.Join(work, "ok.stl")}, {filepath.Join(work, "sub", ".."), "ok.stl"}, {work, filepath.Join("sub", "..", "ok.stl")},
	} {
		if _, err := confinedPath(c.work, c.path); err != nil {
			t.Errorf("%+v refusé : %v", c, err)
		}
	}
	for _, p := range []string{filepath.Join("..", filepath.Base(outside), "secret.txt"), filepath.Join(outside, "secret.txt"), "absent.stl", ".."} {
		if _, err := confinedPath(work, p); err == nil {
			t.Errorf("%q accepté", p)
		}
	}
	// un nom qui COMMENCE par deux points n'est pas une sortie
	if err := os.WriteFile(filepath.Join(work, "..ok.stl"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := confinedPath(work, "..ok.stl"); err != nil {
		t.Errorf("..ok.stl refusé : %v", err)
	}
	// lien symbolique vers l'extérieur (ignoré si le système n'en autorise pas)
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(work, "lien.stl")); err == nil {
		if _, err := confinedPath(work, "lien.stl"); err == nil {
			t.Error("lien symbolique sortant accepté")
		}
	}
}
