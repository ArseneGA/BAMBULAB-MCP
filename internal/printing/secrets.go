// Package printing assemble la chaîne d'impression de bout en bout : registre
// d'imprimantes, découpe headless, validation de l'artefact, dépôt, confirmation
// humaine, lancement, suivi, annulation et réconciliation (dossier §6 et §10).
//
// Règle d'or : un lancement exige une PrintConfirmation consommée atomiquement
// (store.ConsumeConfirmation) et liée à CE PrintJob ; le jeton secret n'est
// jamais rendu au modèle (voir confirm.go).
package printing

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// ErrNoAccessCode : aucun code d'accès enregistré pour cette imprimante.
var ErrNoAccessCode = errors.New("code d'accès de l'imprimante non enregistré")

// SecretStore conserve les codes d'accès LAN HORS de la base. Les valeurs ne
// sont jamais renvoyées par l'API/MCP ni journalisées.
type SecretStore interface {
	SetAccessCode(ctx context.Context, printerID, code string) error
	AccessCode(ctx context.Context, printerID string) (string, error)
	HasAccessCode(printerID string) bool
	DeleteAccessCode(printerID string) error
	// Protection décrit la protection de la clé : "dpapi", "file-0600", "memory".
	Protection() string
}

// FileSecretStore chiffre les codes (AES-256-GCM, l'identifiant d'imprimante
// sert de donnée authentifiée) dans <dir>/printers.secrets. La clé maître vit
// dans <dir>/master.key : enveloppée par DPAPI sous Windows (liée à
// l'utilisateur), sinon fichier en mode 0600 avec avertissement au démarrage.
type FileSecretStore struct {
	dir  string
	mu   sync.Mutex
	aead cipher.AEAD
	prot string
}

type secretFile struct {
	V       int               `json:"v"`
	Entries map[string]string `json:"entries"`
}

// OpenFileSecretStore ouvre (ou crée) le coffre dans dir.
func OpenFileSecretStore(dir string, log *slog.Logger) (*FileSecretStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	key, prot, err := loadOrCreateMasterKey(filepath.Join(dir, "master.key"))
	if err != nil {
		return nil, fmt.Errorf("clé maître du coffre : %w", err)
	}
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, err
	}
	if prot == "file-0600" && log != nil {
		log.Warn("coffre des codes d'accès : la clé maître est un simple fichier (droits 0600) ; protéger le dossier de données (disque chiffré, accès limité)", "dir", dir)
	}
	return &FileSecretStore{dir: dir, aead: aead, prot: prot}, nil
}

func (s *FileSecretStore) Protection() string { return s.prot }

func (s *FileSecretStore) path() string { return filepath.Join(s.dir, "printers.secrets") }

func (s *FileSecretStore) read() (secretFile, error) {
	f := secretFile{V: 1, Entries: map[string]string{}}
	b, err := os.ReadFile(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("coffre illisible : %w", err)
	}
	if f.Entries == nil {
		f.Entries = map[string]string{}
	}
	return f, nil
}

func (s *FileSecretStore) write(f secretFile) error {
	b, _ := json.Marshal(f)
	tmp, err := os.CreateTemp(s.dir, ".sec-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_ = tmp.Chmod(0o600)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, s.path()); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

func (s *FileSecretStore) SetAccessCode(_ context.Context, printerID, code string) error {
	if printerID == "" || code == "" {
		return errors.New("imprimante et code requis")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		return err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	ct := s.aead.Seal(nonce, nonce, []byte(code), []byte(printerID))
	f.Entries[printerID] = base64.StdEncoding.EncodeToString(ct)
	return s.write(f)
}

func (s *FileSecretStore) AccessCode(_ context.Context, printerID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		return "", err
	}
	enc, ok := f.Entries[printerID]
	if !ok {
		return "", ErrNoAccessCode
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(raw) < s.aead.NonceSize() {
		return "", errors.New("entrée du coffre corrompue")
	}
	n := s.aead.NonceSize()
	pt, err := s.aead.Open(nil, raw[:n], raw[n:], []byte(printerID))
	if err != nil {
		return "", errors.New("entrée du coffre illisible (clé maître changée ?)")
	}
	return string(pt), nil
}

func (s *FileSecretStore) HasAccessCode(printerID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		return false
	}
	_, ok := f.Entries[printerID]
	return ok
}

func (s *FileSecretStore) DeleteAccessCode(printerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		return err
	}
	delete(f.Entries, printerID)
	return s.write(f)
}

// MemorySecretStore : coffre volatil (tests).
type MemorySecretStore struct {
	mu sync.Mutex
	m  map[string]string
}

func NewMemorySecretStore() *MemorySecretStore { return &MemorySecretStore{m: map[string]string{}} }

func (s *MemorySecretStore) Protection() string { return "memory" }
func (s *MemorySecretStore) SetAccessCode(_ context.Context, id, code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[id] = code
	return nil
}
func (s *MemorySecretStore) AccessCode(_ context.Context, id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.m[id]; ok {
		return c, nil
	}
	return "", ErrNoAccessCode
}
func (s *MemorySecretStore) HasAccessCode(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.m[id]
	return ok
}
func (s *MemorySecretStore) DeleteAccessCode(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
	return nil
}

// accessCodeProvider adapte un SecretStore à printer.SecretProvider. Sa mise en
// forme texte est masquée : jamais de fuite par fmt/log.
type accessCodeProvider struct {
	store SecretStore
	id    string
}

func (p accessCodeProvider) AccessCode(ctx context.Context) (string, error) {
	return p.store.AccessCode(ctx, p.id)
}
func (p accessCodeProvider) String() string   { return "AccessCode(***)" }
func (p accessCodeProvider) GoString() string { return "AccessCode(***)" }

func randomKey() ([]byte, error) {
	k := make([]byte, 32)
	_, err := rand.Read(k)
	return k, err
}

func loadOrCreateMasterKey(path string) ([]byte, string, error) {
	if raw, err := os.ReadFile(path); err == nil {
		return unwrapKey(raw)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	key, err := randomKey()
	if err != nil {
		return nil, "", err
	}
	wrapped, prot, err := wrapKey(key)
	if err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(path, wrapped, 0o600); err != nil {
		return nil, "", err
	}
	return key, prot, nil
}
