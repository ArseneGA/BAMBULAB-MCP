package catalog

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// loadOrCreateKey lit (ou crée, 0600) la clé de signature des URL d'aperçu.
func loadOrCreateKey(path string) ([]byte, error) {
	if b, err := os.ReadFile(path); err == nil && len(b) >= 32 {
		return b[:32], nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, fmt.Errorf("clé de signature : %w", err)
	}
	return key, nil
}

func (s *Service) sign(sha string, exp int64) string {
	m := hmac.New(sha256.New, s.key)
	fmt.Fprintf(m, "glb:%s:%d", sha, exp)
	return hex.EncodeToString(m.Sum(nil))
}

// SignedAssetURL renvoie l'URL HTTP locale signée d'un GLB dérivé et son expiration.
func (s *Service) SignedAssetURL(sha string) (string, time.Time) {
	exp := s.d.Store.Now().Add(s.cfg.AssetTTL)
	return fmt.Sprintf("%s/api/assets/%s.glb?exp=%d&sig=%s", s.BaseURL(), sha, exp.Unix(), s.sign(sha, exp.Unix())), exp
}

// VerifyAssetSignature contrôle expiration et signature (temps constant).
func (s *Service) VerifyAssetSignature(sha, expStr, sig string) bool {
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || s.d.Store.Now().Unix() > exp {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(s.sign(sha, exp)))
}

// ErrAssetUnknown : le blob demandé n'est pas un aperçu GLB enregistré.
var ErrAssetUnknown = errors.New("aperçu inconnu")

// OpenPreviewAsset ouvre un GLB dérivé. Seuls les aperçus enregistrés sont
// servis : jamais un original ni un blob arbitraire.
func (s *Service) OpenPreviewAsset(ctx context.Context, sha string) (*os.File, error) {
	var n int
	err := s.d.Store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM derived_assets WHERE blob_sha=? AND kind=? AND state='ok'`, sha, KindPreview).Scan(&n)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrAssetUnknown
	}
	f, err := s.d.Blobs.Open(sha)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrAssetUnknown
	}
	return f, err
}
