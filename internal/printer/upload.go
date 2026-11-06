package printer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"
)

// Choix de bibliothèque FTPS : github.com/jlaffaye/ftp (DialWithTLS = TLS
// implicite, PROT P automatique, SIZE/STOR/DELE). Le TLS de contrôle et de
// données partage le *tls.Config (cache de sessions + ServerName fixe), ce qui
// permet la reprise de session que le serveur Bambu exige probablement.

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// remoteName dérive un nom distant déterministe : <stem>-<sha12>.<ext>.
// L'empreinte dans le nom rend l'upload idempotent sans relire le fichier distant.
func remoteName(name, sha string) string {
	base := path.Base(strings.ReplaceAll(name, "\\", "/"))
	ext := ".3mf"
	stem := base
	switch {
	case strings.HasSuffix(base, ".gcode.3mf"):
		ext, stem = ".gcode.3mf", strings.TrimSuffix(base, ".gcode.3mf")
	case strings.HasSuffix(base, ".3mf"):
		stem = strings.TrimSuffix(base, ".3mf")
	}
	stem = strings.Trim(unsafeName.ReplaceAllString(stem, "_"), "._-")
	if stem == "" {
		stem = "job"
	}
	return stem + "-" + sha[:12] + ext
}

func (b *Bambu) dialFTP(ctx context.Context) (*ftp.ServerConn, error) {
	code, err := b.cfg.Secrets.AccessCode(ctx)
	if err != nil {
		return nil, fmt.Errorf("printer: access code unavailable: %w", err)
	}
	c, err := ftp.Dial(b.ftpAddr(),
		ftp.DialWithTLS(b.tls),
		ftp.DialWithContext(ctx),
		ftp.DialWithTimeout(15*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("printer: ftps dial: %w", err)
	}
	if err := c.Login("bblp", code); err != nil {
		_ = c.Quit()
		return nil, fmt.Errorf("printer: ftps login: %w", err)
	}
	return c, nil
}

// checkRemote vérifie la présence et la taille du fichier distant.
func (b *Bambu) checkRemote(ctx context.Context, ref RemoteRef) error {
	c, err := b.dialFTP(ctx)
	if err != nil {
		return err
	}
	defer c.Quit()
	n, err := c.FileSize(ref.Name)
	if err != nil {
		return fmt.Errorf("printer: remote file %q not found: %w", ref.Name, err)
	}
	if n != ref.Size {
		return fmt.Errorf("%w: remote size %d != expected %d", ErrUploadMismatch, n, ref.Size)
	}
	return nil
}

// Upload dépose l'artefact par FTPS implicite et vérifie taille et empreinte.
// Idempotent par sha256+nom : si le fichier distant homonyme (le nom porte
// l'empreinte) a la bonne taille, il n'est pas retransféré.
func (b *Bambu) Upload(ctx context.Context, artefact io.Reader, name string, size int64, sha string) (RemoteRef, error) {
	sha = strings.ToLower(sha)
	if len(sha) != 64 {
		return RemoteRef{}, fmt.Errorf("printer: sha256 must be 64 hex chars")
	}
	if _, err := hex.DecodeString(sha); err != nil {
		return RemoteRef{}, fmt.Errorf("printer: sha256 invalid: %w", err)
	}
	if size < 0 {
		return RemoteRef{}, fmt.Errorf("printer: negative size")
	}
	rn := remoteName(name, sha)
	ref := RemoteRef{
		Name: rn, Path: "/" + rn, Size: size, SHA256: sha,
		URL: strings.ReplaceAll(b.cfg.StartURLTemplate, "{name}", rn),
	}
	b.uploadMu.Lock()
	defer b.uploadMu.Unlock()

	c, err := b.dialFTP(ctx)
	if err != nil {
		return RemoteRef{}, err
	}
	defer c.Quit()

	if n, err := c.FileSize(rn); err == nil && n == size {
		ref.Reused = true
		return ref, nil
	}

	h := sha256.New()
	cr := &countReader{r: io.LimitReader(io.TeeReader(artefact, h), size+1)}
	if err := c.Stor(rn, cr); err != nil {
		_ = c.Delete(rn)
		return RemoteRef{}, fmt.Errorf("printer: ftps store: %w", err)
	}
	if cr.n != size || hex.EncodeToString(h.Sum(nil)) != sha {
		_ = c.Delete(rn)
		return RemoteRef{}, fmt.Errorf("%w: read %d bytes (expected %d) or sha256 differs", ErrUploadMismatch, cr.n, size)
	}
	n, err := c.FileSize(rn)
	if err != nil || n != size {
		_ = c.Delete(rn)
		return RemoteRef{}, fmt.Errorf("%w: remote size after upload %d (err %v)", ErrUploadMismatch, n, err)
	}
	return ref, nil
}

type countReader struct {
	r io.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
