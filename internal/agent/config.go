// Package agent est le cœur de l'agent Windows (bambu-agent) : configuration,
// secret protégé par DPAPI, boucle de travaux (long-polling), inventaire et envoi
// contrôlé de fichiers, slicing local de renfort, détection et ouverture de
// Bambu Studio, lecture du projet ouvert. Les parties propres à Windows sont
// derrière des suffixes _windows.go ; le reste se compile et se teste partout.
package agent

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink"
)

// Folder est un dossier autorisé à l'inventaire et à l'envoi (lecture seule).
type Folder struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

// Config est la configuration persistée dans %LOCALAPPDATA%\BambuAtelier\agent.json.
// Le secret d'agent n'y figure jamais en clair : seulement le blob DPAPI (base64).
type Config struct {
	ServerURL string `json:"server_url"`
	// ServerFingerprint : empreinte SHA-256 du certificat TLS du serveur (--server-fingerprint) ; épinglage
	// côté agent (https avec certificat auto-signé). Vide : vérification TLS classique pour https.
	ServerFingerprint string `json:"server_fingerprint,omitempty"`
	Name              string `json:"name,omitempty"`
	AgentID           string `json:"agent_id,omitempty"`
	// SecretProtected : "<agent_id>.<secret>" chiffré par DPAPI, en base64.
	SecretProtected string `json:"secret_dpapi,omitempty"`
	// DPAPIScope : "user" (défaut) ou "machine" (utile si l'agent tourne en service sous un autre compte).
	DPAPIScope string `json:"dpapi_scope,omitempty"`
	// Folders : dossiers autorisés (seuls ceux-ci sont lus ou envoyés).
	Folders []Folder `json:"folders,omitempty"`
	// StudioPath : chemin de bambu-studio.exe (vide = détection).
	StudioPath string `json:"studio_path,omitempty"`
	// WorkDir : copies de travail (entrées reçues, slicing, fichiers ouverts dans Studio).
	WorkDir string `json:"work_dir,omitempty"`
	// StateDir : journal des travaux (reprise après redémarrage).
	StateDir string `json:"state_dir,omitempty"`
	// PollWaitSeconds : durée du long-polling (défaut 25).
	PollWaitSeconds int `json:"poll_wait_seconds,omitempty"`

	path string
}

// ErrNotPaired : l'agent n'a pas de secret enregistré.
var ErrNotPaired = errors.New("agent non appairé : bambu-agent pair --server URL --token JETON")

// DefaultDir renvoie %LOCALAPPDATA%\BambuAtelier (ou équivalent ailleurs).
func DefaultDir() string {
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		return filepath.Join(d, "BambuAtelier")
	}
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "BambuAtelier")
	}
	return "BambuAtelier"
}

// DefaultConfigPath : %LOCALAPPDATA%\BambuAtelier\agent.json.
func DefaultConfigPath() string { return filepath.Join(DefaultDir(), "agent.json") }

// LoadConfig lit la configuration ; un fichier absent donne une configuration vide.
func LoadConfig(path string) (*Config, error) {
	c := &Config{path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("%s : %w", path, err)
	}
	c.path = path
	return c, nil
}

// Path renvoie le chemin du fichier.
func (c *Config) Path() string { return c.path }

// Save écrit la configuration de façon atomique (droits restreints).
func (c *Config) Save() error {
	if c.path == "" {
		return errors.New("chemin de configuration inconnu")
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

func (c *Config) workDir() string {
	if c.WorkDir != "" {
		return c.WorkDir
	}
	return filepath.Join(filepath.Dir(c.path), "work")
}

func (c *Config) stateDir() string {
	if c.StateDir != "" {
		return c.StateDir
	}
	return filepath.Join(filepath.Dir(c.path), "state")
}

// SetSecret protège et enregistre le secret d'agent (jamais en clair sur disque).
func (c *Config) SetSecret(p Protector, agentID, secret string) error {
	blob, err := p.Protect([]byte(secret), c.DPAPIScope == "machine")
	if err != nil {
		return fmt.Errorf("protection du secret : %w", err)
	}
	c.AgentID, c.SecretProtected = agentID, base64.StdEncoding.EncodeToString(blob)
	return nil
}

// Secret déchiffre le secret d'agent.
func (c *Config) Secret(p Protector) (string, error) {
	if c.SecretProtected == "" {
		return "", ErrNotPaired
	}
	blob, err := base64.StdEncoding.DecodeString(c.SecretProtected)
	if err != nil {
		return "", err
	}
	b, err := p.Unprotect(blob, c.DPAPIScope == "machine")
	if err != nil {
		return "", fmt.Errorf("lecture du secret (DPAPI) : %w — réappairer l'agent si le profil Windows a changé", err)
	}
	return string(b), nil
}

// Folder retrouve un dossier autorisé par son étiquette.
func (c *Config) Folder(label string) (Folder, bool) {
	for _, f := range c.Folders {
		if f.Label == label {
			return f, true
		}
	}
	return Folder{}, false
}

// Validate vérifie les dossiers autorisés (étiquettes sûres et uniques, chemins absolus existants).
func (c *Config) Validate() error {
	seen := map[string]bool{}
	for _, f := range c.Folders {
		if f.Label == "" || agentlink.SafeLabel(f.Label) != f.Label {
			return fmt.Errorf("étiquette de dossier invalide %q (lettres, chiffres, . _ - et espace)", f.Label)
		}
		if seen[strings.ToLower(f.Label)] {
			return fmt.Errorf("étiquette en double %q", f.Label)
		}
		seen[strings.ToLower(f.Label)] = true
		if !filepath.IsAbs(f.Path) {
			return fmt.Errorf("dossier %q : chemin absolu requis", f.Label)
		}
		st, err := os.Stat(f.Path)
		if err != nil || !st.IsDir() {
			return fmt.Errorf("dossier %q : %s n'est pas un dossier accessible", f.Label, f.Path)
		}
	}
	return nil
}

// Protector chiffre un secret pour le stockage local (DPAPI sous Windows).
type Protector interface {
	Protect(plain []byte, machineScope bool) ([]byte, error)
	Unprotect(blob []byte, machineScope bool) ([]byte, error)
}

// ErrProtectUnsupported : pas de protecteur sur cette plateforme.
var ErrProtectUnsupported = errors.New("protection du secret non disponible sur cette plateforme (DPAPI requis : Windows)")
