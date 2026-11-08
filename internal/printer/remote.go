package printer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"
)

// CheckProofBinding vérifie, hors toute I/O, qu'une preuve de confirmation est présente, non expirée et liée à
// cette imprimante, ce fichier, ce plateau et ces matières, et que le plateau est attesté vide. C'est la
// vérification que fait l'adaptateur direct avant tout lancement ; le transport « via agent » la refait côté
// serveur avant de demander une autorisation signée.
func CheckProofBinding(printerID string, verifier ProofVerifier, now time.Time, req StartRequest) error {
	if verifier == nil {
		return fmt.Errorf("%w: no verifier configured", ErrNoProof)
	}
	p := req.Proof
	if p.Token == "" || p.SHA256 == "" || p.PrinterID == "" {
		return ErrNoProof
	}
	if !p.ExpiresAt.After(now) {
		return ErrProofExpired
	}
	if p.PrinterID != printerID {
		return fmt.Errorf("%w: printer", ErrProofMismatch)
	}
	if !strings.EqualFold(p.SHA256, req.Remote.SHA256) {
		return fmt.Errorf("%w: sha256", ErrProofMismatch)
	}
	if p.Plate != req.Plate {
		return fmt.Errorf("%w: plate", ErrProofMismatch)
	}
	if !equalInts(p.AMSMapping, req.AMSMapping) {
		return fmt.Errorf("%w: material mapping", ErrProofMismatch)
	}
	if !req.PlateClear {
		return ErrPlateNotClear
	}
	if req.Remote.Name == "" || req.Plate < 1 {
		return fmt.Errorf("printer: remote name and plate (>=1) are required")
	}
	for _, v := range req.AMSMapping {
		if v < -1 || v > 253 {
			return fmt.Errorf("printer: ams_mapping value %d out of range", v)
		}
	}
	return nil
}

// RemoteSHA256 relit le fichier déposé sur l'imprimante (FTPS) et renvoie son empreinte SHA-256 et sa taille.
// Aucun effet de bord. Sert à l'agent local, qui revérifie l'empreinte juste avant un lancement.
func (b *Bambu) RemoteSHA256(ctx context.Context, name string) (string, int64, error) {
	c, err := b.dialFTP(ctx)
	if err != nil {
		return "", 0, err
	}
	defer c.Quit()
	r, err := c.Retr(name)
	if err != nil {
		return "", 0, fmt.Errorf("printer: remote file %q not readable: %w", name, err)
	}
	defer r.Close()
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return "", n, fmt.Errorf("printer: reading remote file %q: %w", name, err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// Refresh garantit un rapport d'état de moins de maxAge (pushall si besoin) et le renvoie ; sans session, renvoie
// l'état connu et ErrNotConnected.
func (b *Bambu) Refresh(ctx context.Context, maxAge time.Duration) (Status, error) {
	c := b.current()
	if c == nil {
		return b.snapshot(), ErrNotConnected
	}
	return b.ensureFresh(ctx, c, maxAge)
}
