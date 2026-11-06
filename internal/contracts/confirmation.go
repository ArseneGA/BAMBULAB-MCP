package contracts

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

// Erreurs de confirmation (distinguables par errors.Is).
var (
	ErrConfirmationUsed     = errors.New("confirmation déjà utilisée")
	ErrConfirmationExpired  = errors.New("confirmation expirée")
	ErrConfirmationMismatch = errors.New("la confirmation ne correspond plus à l'impression demandée")
	ErrConfirmationToken    = errors.New("jeton de confirmation invalide")
)

// ConfirmationBinding est ce que l'utilisateur a approuvé : imprimante, empreinte
// de l'artefact, plateau et affectation matière (dossier §10). Tout changement
// invalide la confirmation.
type ConfirmationBinding struct {
	PrinterID      string           `json:"printer_id"`
	ArtifactSHA256 string           `json:"artifact_sha256"`
	Plate          int              `json:"plate"`
	Materials      []SlotAssignment `json:"materials"`
}

// Fingerprint est une empreinte canonique (matières triées par emplacement).
func (b ConfirmationBinding) Fingerprint() string {
	m := append([]SlotAssignment(nil), b.Materials...)
	sort.Slice(m, func(i, j int) bool { return m[i].Slot < m[j].Slot })
	b.Materials = m
	if b.Materials == nil {
		b.Materials = []SlotAssignment{}
	}
	raw, _ := json.Marshal(b) // champs ordonnés par la struct : canonique
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// PrintConfirmation est une autorisation à usage unique. Seule l'empreinte du
// jeton est conservée ; le jeton en clair n'est remis qu'une fois, à l'émission.
type PrintConfirmation struct {
	ID                 string              `json:"id"`
	PrintJobID         string              `json:"print_job_id"`
	Binding            ConfirmationBinding `json:"binding"`
	BindingFingerprint string              `json:"binding_fingerprint"`
	TokenHash          string              `json:"token_hash"`
	IssuedAt           time.Time           `json:"issued_at"`
	ExpiresAt          time.Time           `json:"expires_at"`
	UsedAt             *time.Time          `json:"used_at,omitempty"`
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewConfirmation émet une confirmation et le jeton aléatoire (256 bits) à remettre
// à l'utilisateur. Le jeton n'est pas récupérable ensuite.
func NewConfirmation(id, printJobID string, binding ConfirmationBinding, ttl time.Duration, now time.Time) (PrintConfirmation, string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return PrintConfirmation{}, "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	return PrintConfirmation{
		ID:                 id,
		PrintJobID:         printJobID,
		Binding:            binding,
		BindingFingerprint: binding.Fingerprint(),
		TokenHash:          hashToken(token),
		IssuedAt:           now,
		ExpiresAt:          now.Add(ttl),
	}, token, nil
}

// Consume vérifie le jeton, l'expiration et que la liaison courante est
// identique à celle approuvée, puis marque la confirmation utilisée (usage
// unique). En cas d'échec, rien n'est marqué ; la persistance doit rendre
// l'opération atomique (lecture + écriture dans une même transaction).
func (c *PrintConfirmation) Consume(token string, current ConfirmationBinding, now time.Time) error {
	if c.UsedAt != nil {
		return ErrConfirmationUsed
	}
	if !now.Before(c.ExpiresAt) {
		return ErrConfirmationExpired
	}
	if subtle.ConstantTimeCompare([]byte(hashToken(token)), []byte(c.TokenHash)) != 1 {
		return ErrConfirmationToken
	}
	if subtle.ConstantTimeCompare([]byte(current.Fingerprint()), []byte(c.BindingFingerprint)) != 1 {
		return ErrConfirmationMismatch
	}
	t := now
	c.UsedAt = &t
	return nil
}
