package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveInFolderStaysInside(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.MkdirAll(filepath.Join(root, "a"), 0o755)
	os.WriteFile(filepath.Join(root, "a", "ok.stl"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(outside, "secret.stl"), []byte("x"), 0o644)
	f := Folder{Label: "L", Path: root}
	if p, err := resolveInFolder(f, "a/ok.stl"); err != nil || filepath.Base(p) != "ok.stl" {
		t.Fatalf("fichier légitime : %q %v", p, err)
	}
	for _, rel := range []string{"../" + filepath.Base(outside) + "/secret.stl", "a/../../x.stl", "/etc/passwd", `C:\Windows\win.ini`, "a/absent.stl"} {
		if _, err := resolveInFolder(f, rel); err == nil {
			t.Errorf("sortie du dossier acceptée : %q", rel)
		}
	}
	// Un lien symbolique vers l'extérieur est refusé (si la plateforme autorise sa création).
	if err := os.Symlink(outside, filepath.Join(root, "lien")); err != nil {
		t.Logf("lien symbolique non créable ici (%v) : cas non testé", err)
		return
	}
	if _, err := resolveInFolder(f, "lien/secret.stl"); err == nil {
		t.Fatal("un lien symbolique sortant a été suivi")
	}
}
