package core

import (
	"os"
	"os/exec"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

// findExe cherche un programme : d'abord la variable d'environnement dédiée
// (chemin explicite), puis le PATH. Rien n'est exécuté.
func findExe(name, purpose, envVar string, candidates ...string) contracts.Executable {
	e := contracts.Executable{Name: name, Purpose: purpose}
	if p := os.Getenv(envVar); p != "" {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			e.Found, e.Path = true, p
			return e
		}
	}
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			e.Found, e.Path = true, p
			return e
		}
	}
	return e
}
