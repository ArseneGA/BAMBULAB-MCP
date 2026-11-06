package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// BlobStore stocke des fichiers adressés par contenu :
// <root>/ab/cd/<sha256>. Les originaux sont immuables : écriture atomique
// (fichier temporaire + rename), jamais de remplacement d'un blob existant.
type BlobStore struct {
	root string
}

// ErrBlobCorrupt : l'empreinte du contenu ne correspond pas à son adresse.
var ErrBlobCorrupt = errors.New("blob corrompu : empreinte différente")

// ErrBlobHashMismatch : le contenu fourni n'a pas l'empreinte attendue.
var ErrBlobHashMismatch = errors.New("empreinte du contenu différente de l'empreinte attendue")

// NewBlobStore crée le dossier racine si nécessaire.
func NewBlobStore(root string) (*BlobStore, error) {
	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o755); err != nil {
		return nil, err
	}
	return &BlobStore{root: root}, nil
}

func validSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Path renvoie le chemin d'un blob (validation stricte : pas de traversée possible).
func (b *BlobStore) Path(sha string) (string, error) {
	if !validSHA256(sha) {
		return "", fmt.Errorf("empreinte invalide %q", sha)
	}
	return filepath.Join(b.root, sha[0:2], sha[2:4], sha), nil
}

// Has indique si le blob existe.
func (b *BlobStore) Has(sha string) bool {
	p, err := b.Path(sha)
	if err != nil {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// Put lit r jusqu'au bout, l'écrit atomiquement et renvoie son empreinte et sa taille.
// Si wantSHA est non vide, le contenu est rejeté (rien n'est écrit) en cas de différence.
func (b *BlobStore) Put(r io.Reader, wantSHA string) (sha string, size int64, err error) {
	if wantSHA != "" && !validSHA256(wantSHA) {
		return "", 0, fmt.Errorf("empreinte attendue invalide %q", wantSHA)
	}
	tmp, err := os.CreateTemp(filepath.Join(b.root, "tmp"), "blob-*")
	if err != nil {
		return "", 0, err
	}
	tmpName := tmp.Name()
	cleanup := func() { tmp.Close(); os.Remove(tmpName) }
	h := sha256.New()
	size, err = io.Copy(io.MultiWriter(tmp, h), r)
	if err != nil {
		cleanup()
		return "", 0, err
	}
	sha = hex.EncodeToString(h.Sum(nil))
	if wantSHA != "" && sha != wantSHA {
		cleanup()
		return "", 0, fmt.Errorf("%w : attendu %s, obtenu %s", ErrBlobHashMismatch, wantSHA, sha)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return "", 0, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", 0, err
	}
	dst, _ := b.Path(sha)
	if b.Has(sha) { // contenu déjà présent : on ne touche pas à l'original
		os.Remove(tmpName)
		return sha, size, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		os.Remove(tmpName)
		return "", 0, err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		os.Remove(tmpName)
		return "", 0, err
	}
	_ = os.Chmod(dst, 0o444) // meilleur effort : lecture seule
	return sha, size, nil
}

// Open ouvre un blob en lecture.
func (b *BlobStore) Open(sha string) (*os.File, error) {
	p, err := b.Path(sha)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("blob %s : %w", sha, ErrNotFound)
	}
	return f, err
}

// Verify relit le blob et vérifie son empreinte.
func (b *BlobStore) Verify(sha string) error {
	f, err := b.Open(sha)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sha {
		return fmt.Errorf("%w : adresse %s, contenu %s", ErrBlobCorrupt, sha, got)
	}
	return nil
}
