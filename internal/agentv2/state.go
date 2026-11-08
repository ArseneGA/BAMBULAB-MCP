package agentv2

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LedgerEntry : un fichier que CET agent a déposé (et dont il a vérifié l'empreinte avant le dépôt).
type LedgerEntry struct {
	Name string    `json:"name"` // nom sur l'imprimante
	Size int64     `json:"size_bytes"`
	At   time.Time `json:"deposited_at"`
}

// Ledger est le registre des dépôts de l'agent, indexé par empreinte SHA-256. Un lancement n'est possible que pour
// une empreinte inscrite ici : un fichier que l'agent n'a pas lui-même déposé n'est jamais lancé.
type Ledger struct {
	mu   sync.Mutex
	path string
	m    map[string]LedgerEntry
}

// OpenLedger lit (ou crée) le registre. path vide = en mémoire.
func OpenLedger(path string) (*Ledger, error) {
	l := &Ledger{path: path, m: map[string]LedgerEntry{}}
	if path == "" {
		return l, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &l.m); err != nil {
		return nil, fmt.Errorf("%s : %w", path, err)
	}
	return l, nil
}

// Put inscrit un dépôt.
func (l *Ledger) Put(sha string, e LedgerEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.m[strings.ToLower(sha)] = e
	return l.saveLocked()
}

// Get relit une entrée.
func (l *Ledger) Get(sha string) (LedgerEntry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.m[strings.ToLower(sha)]
	return e, ok
}

func (l *Ledger) saveLocked() error {
	if l.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(l.m, "", "  ")
	if err != nil {
		return err
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, l.path)
}

// LinkConfig est la configuration persistée du mode « printer-link » de l'agent. Elle contient le secret d'agent
// (fichier en droits 0600) et la clé publique du serveur épinglée à l'appairage. Elle ne contient JAMAIS le code
// d'accès LAN de l'imprimante : celui-ci est lu dans un fichier à part (--access-code-file) à chaque démarrage.
type LinkConfig struct {
	ServerURL         string `json:"server_url"`
	ServerFingerprint string `json:"server_fingerprint,omitempty"`
	AgentID           string `json:"agent_id"`
	Secret            string `json:"secret"` // « <agent_id>.<secret> »
	ServerKey         string `json:"server_key"`
	ServerKeyID       string `json:"server_key_id"`
	PrinterID         string `json:"printer_id"`
	Name              string `json:"name,omitempty"`
}

// ServerPublicKey décode la clé épinglée.
func (c *LinkConfig) ServerPublicKey() (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(c.ServerKey)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("clé publique du serveur absente ou illisible : réappairer l'agent")
	}
	return ed25519.PublicKey(b), nil
}

// LoadLinkConfig lit la configuration ; un fichier absent donne ErrNotPaired.
func LoadLinkConfig(path string) (*LinkConfig, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotPaired
	}
	if err != nil {
		return nil, err
	}
	var c LinkConfig
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s : %w", path, err)
	}
	return &c, nil
}

// Save écrit la configuration de façon atomique, en droits restreints.
func (c *LinkConfig) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ErrNotPaired : l'agent n'est pas appairé.
var ErrNotPaired = errors.New("agent non appairé : relancer printer-link avec --token (jeton d'appairage de « bambud agent pair »)")

// ReadOnlyFlag renvoie la fonction du kill-switch local : l'agent est en lecture seule si l'option est posée OU si
// le fichier indiqué existe. Le fichier est relu à chaque ordre : on peut basculer sans redémarrer l'agent
// (touch / rm), et aucun ordre du serveur ne peut le lever.
func ReadOnlyFlag(forced bool, file string) func() bool {
	return func() bool {
		if forced {
			return true
		}
		if file == "" {
			return false
		}
		_, err := os.Stat(file)
		return err == nil
	}
}
