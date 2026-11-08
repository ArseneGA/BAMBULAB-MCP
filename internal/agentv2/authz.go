package agentv2

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Durées de l'autorisation de lancement.
const (
	// AuthTTL : validité d'une autorisation signée.
	AuthTTL = 2 * time.Minute
	// MaxAuthWindow : fenêtre (expires - issued) maximale acceptée par l'agent ; une autorisation qui prétend
	// valoir plus longtemps est refusée, même signée.
	MaxAuthWindow = AuthTTL + 30*time.Second
	// ClockSkew : tolérance d'horloge entre serveur et agent (émission « dans le futur »).
	ClockSkew = 30 * time.Second
)

// authDomain sépare ce que signe le serveur de toute autre signature Ed25519.
const authDomain = "bambu-atelier/agent-v2/start/1\n"

// StartOptionsJSON : options de démarrage signées (ce que l'humain a vu et approuvé).
type StartOptionsJSON struct {
	Timelapse     bool   `json:"timelapse"`
	BedLeveling   bool   `json:"bed_leveling"`
	FlowCali      bool   `json:"flow_cali"`
	VibrationCali bool   `json:"vibration_cali"`
	LayerInspect  bool   `json:"layer_inspect"`
	BedType       string `json:"bed_type"`
}

// AuthPayload est ce que le serveur signe : un lancement et UN SEUL, lié à une imprimante, un fichier (empreinte),
// un plateau, une affectation de matières et des options, pour un agent précis, pendant deux minutes.
type AuthPayload struct {
	V          int              `json:"v"`
	ID         string           `json:"id"` // nonce à usage unique (128 bits)
	KeyID      string           `json:"key_id"`
	AgentID    string           `json:"agent_id"`
	PrinterID  string           `json:"printer_id"`
	SHA256     string           `json:"sha256"`
	Plate      int              `json:"plate"`
	UseAMS     bool             `json:"use_ams"`
	AMSMapping []int            `json:"ams_mapping"`
	Options    StartOptionsJSON `json:"options"`
	PlateClear bool             `json:"plate_clear"`
	IssuedAt   time.Time        `json:"issued_at"`
	ExpiresAt  time.Time        `json:"expires_at"`
}

// SignedAuth : la charge utile (octets JSON exacts, base64) et sa signature Ed25519 (base64). L'agent vérifie la
// signature sur les octets reçus, jamais sur une ré-encodation.
type SignedAuth struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

// KeyID est l'identifiant court d'une clé publique (8 premiers octets de son SHA-256, en hexadécimal).
func KeyID(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return hex.EncodeToString(h[:8])
}

// Signer détient la clé privée du serveur.
type Signer struct {
	priv ed25519.PrivateKey
}

// NewSigner génère une clé neuve.
func NewSigner() (*Signer, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Signer{priv: priv}, nil
}

// LoadOrCreateSigner lit la clé (graine base64) dans path, ou la crée (droits 0600). Elle ne quitte jamais le
// serveur ; seule sa partie publique est donnée aux agents à l'appairage.
func LoadOrCreateSigner(path string) (*Signer, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		seed, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
		if derr != nil || len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("agentv2 : clé de signature illisible dans %s", path)
		}
		return &Signer{priv: ed25519.NewKeyFromSeed(seed)}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	s, err := NewSigner()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(base64.StdEncoding.EncodeToString(s.priv.Seed())+"\n"), 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, err
	}
	return s, nil
}

// Public renvoie la clé publique.
func (s *Signer) Public() ed25519.PublicKey { return s.priv.Public().(ed25519.PublicKey) }

// KeyID de la clé de ce signataire.
func (s *Signer) KeyID() string { return KeyID(s.Public()) }

// NewNonce renvoie un nonce aléatoire de 128 bits (hexadécimal).
func NewNonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand indisponible : " + err.Error())
	}
	return hex.EncodeToString(b)
}

// Sign signe la charge utile (V, KeyID et ID sont complétés s'ils sont vides).
func (s *Signer) Sign(p AuthPayload) (SignedAuth, error) {
	if p.V == 0 {
		p.V = 1
	}
	if p.KeyID == "" {
		p.KeyID = s.KeyID()
	}
	if p.ID == "" {
		p.ID = NewNonce()
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return SignedAuth{}, err
	}
	sig := ed25519.Sign(s.priv, append([]byte(authDomain), raw...))
	return SignedAuth{Payload: base64.StdEncoding.EncodeToString(raw), Signature: base64.StdEncoding.EncodeToString(sig)}, nil
}

// AuthError est un refus de vérification avec son code.
type AuthError struct {
	Code    string
	Message string
}

func (e *AuthError) Error() string { return e.Code + " : " + e.Message }

func authErr(code, format string, args ...any) *AuthError {
	return &AuthError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// VerifyAuth vérifie la signature (clé épinglée), la forme et la fraîcheur d'une autorisation. Il ne touche ni au
// nonce, ni à l'agent, ni à l'imprimante : l'appelant (Runner) contrôle la cible et consomme le nonce.
func VerifyAuth(pub ed25519.PublicKey, a SignedAuth, now time.Time) (AuthPayload, error) {
	var p AuthPayload
	raw, err := base64.StdEncoding.DecodeString(a.Payload)
	if err != nil || len(raw) == 0 {
		return p, authErr(CodeBadSignature, "charge utile illisible")
	}
	sig, err := base64.StdEncoding.DecodeString(a.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return p, authErr(CodeBadSignature, "signature illisible")
	}
	if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, append([]byte(authDomain), raw...), sig) {
		return p, authErr(CodeBadSignature, "signature invalide (ou clé du serveur différente de celle épinglée à l'appairage)")
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, authErr(CodeBadSignature, "charge utile mal formée")
	}
	if p.V != 1 || p.ID == "" || len(p.ID) < 16 {
		return p, authErr(CodeBadSignature, "version ou nonce invalide")
	}
	if p.KeyID != KeyID(pub) {
		return p, authErr(CodeBadSignature, "identifiant de clé incohérent")
	}
	if p.IssuedAt.IsZero() || p.ExpiresAt.IsZero() || !p.ExpiresAt.After(p.IssuedAt) {
		return p, authErr(CodeExpired, "fenêtre de validité absente ou incohérente")
	}
	if p.ExpiresAt.Sub(p.IssuedAt) > MaxAuthWindow {
		return p, authErr(CodeExpired, "fenêtre de validité trop longue (%s > %s)", p.ExpiresAt.Sub(p.IssuedAt), MaxAuthWindow)
	}
	if now.Add(ClockSkew).Before(p.IssuedAt) {
		return p, authErr(CodeNotYetValid, "autorisation émise dans le futur (horloge de l'agent ou du serveur à corriger)")
	}
	if !now.Before(p.ExpiresAt) {
		return p, authErr(CodeExpired, "autorisation périmée depuis %s", now.Sub(p.ExpiresAt).Round(time.Second))
	}
	return p, nil
}

// NonceStore mémorise les nonces consommés. Consume renvoie false si le nonce l'a déjà été ; il doit survivre à un
// redémarrage de l'agent (sinon un rejeu dans la fenêtre de validité serait possible).
type NonceStore interface {
	Consume(id string, expires time.Time) (bool, error)
}

// MemNonceStore : en mémoire (tests).
type MemNonceStore struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

// NewMemNonceStore crée un magasin vide.
func NewMemNonceStore() *MemNonceStore { return &MemNonceStore{seen: map[string]time.Time{}} }

// Consume marque le nonce utilisé.
func (m *MemNonceStore) Consume(id string, expires time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.seen[id]; ok {
		return false, nil
	}
	m.seen[id] = expires
	return true, nil
}

// FileNonceStore : journal append-only « <nonce> <expiration unix> » ; relu au démarrage. Un nonce est écrit (et
// synchronisé) AVANT que la commande ne parte : en cas de plantage, il reste consommé.
type FileNonceStore struct {
	mu   sync.Mutex
	path string
	seen map[string]time.Time
	now  func() time.Time
}

// OpenFileNonceStore ouvre (ou crée) le journal et oublie les nonces expirés depuis plus d'une heure.
func OpenFileNonceStore(path string, now func() time.Time) (*FileNonceStore, error) {
	if now == nil {
		now = time.Now
	}
	s := &FileNonceStore{path: path, seen: map[string]time.Time{}, now: now}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			id, exp, ok := strings.Cut(strings.TrimSpace(sc.Text()), " ")
			if !ok {
				continue
			}
			n, perr := strconv.ParseInt(exp, 10, 64)
			if perr != nil {
				continue
			}
			s.seen[id] = time.Unix(n, 0)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return s, nil
}

// Consume marque le nonce utilisé et l'inscrit dans le journal.
func (s *FileNonceStore) Consume(id string, expires time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[id]; ok {
		return false, nil
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return false, err
	}
	_, werr := fmt.Fprintf(f, "%s %d\n", id, expires.Unix())
	if werr == nil {
		werr = f.Sync()
	}
	cerr := f.Close()
	if werr != nil {
		return false, werr
	}
	if cerr != nil {
		return false, cerr
	}
	s.seen[id] = expires
	s.prune()
	return true, nil
}

// prune retire de la mémoire les nonces expirés depuis plus d'une heure (le journal reste tel quel : petit).
func (s *FileNonceStore) prune() {
	cut := s.now().Add(-time.Hour)
	for id, exp := range s.seen {
		if exp.Before(cut) {
			delete(s.seen, id)
		}
	}
}
