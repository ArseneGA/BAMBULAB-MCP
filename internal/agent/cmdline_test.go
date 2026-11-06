package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSplitCommandLineAndProjectFromCommandLine(t *testing.T) {
	got := splitCommandLine(`"C:\Program Files\Bambu Studio\bambu-studio.exe" "C:\Mes projets\cube20.gcode.3mf" --x`)
	if len(got) != 3 || got[0] != `C:\Program Files\Bambu Studio\bambu-studio.exe` || got[1] != `C:\Mes projets\cube20.gcode.3mf` || got[2] != "--x" {
		t.Fatalf("découpage : %q", got)
	}
	dir := t.TempDir()
	if runtime.GOOS != "windows" {
		t.Skip("chemins absolus Windows")
	}
	proj := filepath.Join(dir, "cube20.gcode.3mf")
	os.WriteFile(proj, []byte("x"), 0o644)
	cl := `"C:\Program Files\Bambu Studio\bambu-studio.exe" "` + proj + `"`
	if p := ProjectFromCommandLine(cl, "cube20.gcode"); p != proj {
		t.Fatalf("projet depuis la ligne de commande : %q", p)
	}
	// Le titre a changé (autre projet ouvert depuis) : on n'invente rien.
	if p := ProjectFromCommandLine(cl, "autre"); p != "" {
		t.Fatalf("nom différent accepté : %q", p)
	}
	if p := ProjectFromCommandLine(`"C:\x\bambu-studio.exe"`, "cube20.gcode"); p != "" {
		t.Fatalf("sans argument : %q", p)
	}
	if p := ProjectFromCommandLine(cl, ""); p != "" {
		t.Fatal("titre sans projet")
	}
}

func TestProcessCommandLineReadsOwnProcess(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows seulement")
	}
	cl, err := processCommandLine(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	if !strings.Contains(strings.ToLower(cl), strings.ToLower(filepath.Base(exe))) {
		t.Fatalf("ligne de commande lue : %q (exécutable %s)", cl, exe)
	}
}
