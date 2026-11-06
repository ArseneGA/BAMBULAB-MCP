package slicer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"time"
)

// Binary est une installation localisée de Bambu Studio.
type Binary struct {
	Path    string   // chemin de l'exécutable (ou de l'AppImage)
	Version string   // ex. "02.08.02.61" ; vide si non détectée
	Args    []string // arguments à passer avant les options (ex. --appimage-extract-and-run)
}

// ProfilesDir déduit le répertoire des profils système BBL de l'installation
// (Windows : <install>\resources\profiles\BBL). Vide si introuvable.
func (b *Binary) ProfilesDir() string {
	base := filepath.Dir(b.Path)
	for _, rel := range []string{
		filepath.Join("resources", "profiles", "BBL"),
		filepath.Join("..", "resources", "profiles", "BBL"),
		filepath.Join("..", "share", "BambuStudio", "profiles", "BBL"),
		filepath.Join("squashfs-root", "resources", "profiles", "BBL"),
	} {
		p := filepath.Join(base, rel)
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return filepath.Clean(p)
		}
	}
	return ""
}

// Locate cherche Bambu Studio. Ordre : chemin configuré, variable
// BAMBU_STUDIO_BIN, emplacements usuels de la plateforme, PATH. La version est
// lue sur l'exécutable (ressource de version Windows) ou, ailleurs, extraite de
// la sortie d'aide.
func Locate(configured string) (*Binary, error) {
	var cands []string
	if configured != "" {
		cands = append(cands, configured)
	}
	if e := os.Getenv("BAMBU_STUDIO_BIN"); e != "" {
		cands = append(cands, e)
	}
	switch runtime.GOOS {
	case "windows":
		for _, env := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)", "LOCALAPPDATA"} {
			if d := os.Getenv(env); d != "" {
				cands = append(cands, filepath.Join(d, "Bambu Studio", "bambu-studio.exe"))
			}
		}
		cands = append(cands, `C:\Program Files\Bambu Studio\bambu-studio.exe`)
	default:
		cands = append(cands, "/opt/bambu-studio/bambu-studio", "/usr/bin/bambu-studio", "/usr/local/bin/bambu-studio")
		if home, err := os.UserHomeDir(); err == nil {
			for _, d := range []string{home, filepath.Join(home, "Applications"), filepath.Join(home, ".local", "bin"), "/opt"} {
				m, _ := filepath.Glob(filepath.Join(d, "[Bb]ambu[Ss]tudio*.AppImage"))
				sort.Sort(sort.Reverse(sort.StringSlice(m)))
				cands = append(cands, m...)
			}
		}
	}
	if p, err := exec.LookPath("bambu-studio"); err == nil {
		cands = append(cands, p)
	}
	for _, c := range cands {
		fi, err := os.Stat(c)
		if err != nil || fi.IsDir() {
			continue
		}
		b := &Binary{Path: c}
		if filepath.Ext(c) == ".AppImage" {
			b.Args = []string{"--appimage-extract-and-run"}
		}
		b.Version = detectVersion(c)
		return b, nil
	}
	return nil, fmt.Errorf("%w (cherché: %v)", ErrBinaryNotFound, cands)
}

var versionRe = regexp.MustCompile(`\b(\d{2}\.\d{2}\.\d{2}\.\d{2})\b`)

// detectVersion essaie la ressource de version (Windows) puis la sortie d'aide.
// Sur le build Windows testé, l'aide est vide (exécutable sans console).
func detectVersion(path string) string {
	if v := fileVersion(path); v != "" {
		return v
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, path, "--help").CombinedOutput()
	if m := versionRe.FindSubmatch(out); m != nil {
		return string(m[1])
	}
	return ""
}
