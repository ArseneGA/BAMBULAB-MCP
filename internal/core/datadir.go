package core

import (
	"os"
	"path/filepath"
	"runtime"
)

// DefaultDataDir renvoie le dossier de données par défaut, TOUJOURS absolu (le
// répertoire courant d'un processus lancé par Claude Desktop est imprévisible) :
// %LOCALAPPDATA%\BambuAtelier sous Windows, $XDG_DATA_HOME/bambu-atelier sinon
// (repli ~/.local/share/bambu-atelier).
func DefaultDataDir() string {
	home, _ := os.UserHomeDir()
	return defaultDataDir(runtime.GOOS, os.Getenv, home)
}

func defaultDataDir(goos string, getenv func(string) string, home string) string {
	if goos == "windows" {
		if d := getenv("LOCALAPPDATA"); d != "" && filepath.IsAbs(d) {
			return filepath.Join(d, "BambuAtelier")
		}
		return filepath.Join(home, "AppData", "Local", "BambuAtelier")
	}
	base := getenv("XDG_DATA_HOME")
	if base == "" || !filepath.IsAbs(base) { // la spec XDG ignore les chemins relatifs
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "bambu-atelier")
}
