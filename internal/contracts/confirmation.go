package contracts

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Erreurs de confirmation (distinguables par errors.Is).
var (
	ErrConfirmationUsed     = errors.New("confirmation déjà utilisée")
	ErrConfirmationExpired  = errors.New("confirmation expirée")
	ErrConfirmationMismatch = errors.New("la confirmation ne correspond plus à l'impression demandée")
	ErrConfirmationToken    = errors.New("jeton de confirmation invalide")
	ErrConfirmationInvalid  = errors.New("confirmation invalide")
)

// MaxConfirmationTTL : durée de vie maximale d'une confirmation.
const MaxConfirmationTTL = 24 * time.Hour

// Validate vérifie la liaison : imprimante et empreinte renseignées, plateau >= 0,
// emplacements non vides et sans doublon (deux matières sur un même emplacement
// rendraient l'empreinte ambiguë).
func (b ConfirmationBinding) Validate() error {
	if b.PrinterID == "" || b.ArtifactSHA256 == "" {
		return fmt.Errorf("%w : imprimante et empreinte de l'artefact requises", ErrConfirmationInvalid)
	}
	if b.Plate < 0 {
		return fmt.Errorf("%w : plateau négatif", ErrConfirmationInvalid)
	}
	seen := map[string]bool{}
	for _, m := range b.Materials {
		if m.Slot == "" || m.MaterialID == "" {
			return fmt.Errorf("%w : emplacement et matière requis", ErrConfirmationInvalid)
		}
		if seen[m.Slot] {
			return fmt.Errorf("%w : emplacement %q dupliqué", ErrConfirmationInvalid, m.Slot)
		}
		seen[m.Slot] = true
	}
	return nil
}

// ConfirmationBinding est ce que l'utilisateur a approuvé : imprimante, empreinte
// de l'artefact, plateau et affectation matière (dossier §10). Tout changement
// invalide la confirmation.
type ConfirmationBinding struct {
	PrinterID      string           `json:"printer_id"`
	ArtifactSHA256 string           `json:"artifact_sha256"`
	Plate          int              `json:"plate"`
	Materials      []SlotAssignment `json:"materials"`
	// NoBedLeveling : le lancement approuvé n'effectue PAS la mise à niveau du plateau. Absent (faux) par défaut :
	// l'empreinte des liaisons « mise à niveau activée » reste inchangée (omitempty).
	NoBedLeveling bool `json:"no_bed_leveling,omitempty"`
	// Options de démarrage approuvées. Elles sont exprimées par l'ÉCART au comportement historique (calibration du
	// débit et des vibrations activées, pas de timelapse) : absentes (faux) par défaut, donc l'empreinte des liaisons
	// « par défaut » est inchangée. Les valeurs approuvées sont celles de la commande envoyée.
	NoFlowCali      bool `json:"no_flow_cali,omitempty"`
	NoVibrationCali bool `json:"no_vibration_cali,omitempty"`
	Timelapse       bool `json:"timelapse,omitempty"`
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
	if ttl <= 0 || ttl > MaxConfirmationTTL {
		return PrintConfirmation{}, "", fmt.Errorf("%w : durée de vie %s hors de ]0 ; %s]", ErrConfirmationInvalid, ttl, MaxConfirmationTTL)
	}
	if err := binding.Validate(); err != nil {
		return PrintConfirmation{}, "", err
	}
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
	if err := current.Validate(); err != nil {
		return fmt.Errorf("%w : %v", ErrConfirmationMismatch, err)
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
