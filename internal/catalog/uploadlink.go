package catalog

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

// Liens d'envoi à usage unique : le modèle de langage (ou l'utilisateur) obtient une URL, y envoie le fichier ENTIER en
// un seul PUT (curl -T) depuis son environnement de code, et le fichier arrive dans la bibliothèque exactement comme
// un upload_file / import_model (original non modifié, signature contrôlée, empreinte sha256 recalculée).
//
// Sécurité : le jeton est tiré au hasard (256 bits), n'est jamais journalisé ni conservé en clair (seul son sha256 sert
// de clé), n'ouvre QU'UN envoi (consommé dès le premier PUT valide), expire au bout de 10 minutes, borne la taille
// (taille annoncée, sinon limite du format) et n'autorise que les formats du catalogue ; il ne donne aucun autre accès.
// Les liens vivent en mémoire : un redémarrage les invalide.

const (
	// UploadLinkTTL : durée de vie d'un lien.
	UploadLinkTTL = 10 * time.Minute
	// maxUploadLinks : liens simultanés (non expirés).
	maxUploadLinks = 16
	// UploadLinkTokenLen : longueur du jeton dans l'URL (32 octets en base64 URL sans remplissage).
	UploadLinkTokenLen = 43
	linkIDPrefix       = "lnk_"
)

// ErrUploadTooLarge : le corps dépasse la taille permise.
var ErrUploadTooLarge = errors.New("fichier trop gros")

// UploadLinkInfo : ce que l'outil donne au modèle.
type UploadLinkInfo struct {
	LinkID    string    `json:"link_id" jsonschema:"identifiant du lien (get_upload_status(link_id) donne le résultat)"`
	Token     string    `json:"-"`
	Filename  string    `json:"filename"`
	Format    string    `json:"format"`
	MaxBytes  int64     `json:"max_bytes" jsonschema:"taille maximale acceptée (octets)"`
	ExpiresAt time.Time `json:"expires_at"`
}

// UploadLinkStatus : état d'un lien.
type UploadLinkStatus struct {
	LinkID    string        `json:"link_id"`
	Filename  string        `json:"filename"`
	State     string        `json:"state" jsonschema:"pending (en attente du PUT) | receiving | complete | failed | expired"`
	ExpiresAt time.Time     `json:"expires_at"`
	Received  int64         `json:"received"`
	Total     int64         `json:"total_size,omitempty"`
	Result    *UploadResult `json:"result,omitempty"`
	Error     string        `json:"error,omitempty"`
}

type uploadLink struct {
	id       string
	name     string
	format   contracts.FileFormat
	maxBytes int64
	size     int64 // annoncée (0 = inconnue)
	sha      string
	meta     UploadMeta
	expires  time.Time
	state    string
	received int64
	result   *UploadResult
	err      string
}

type linkState struct {
	mu     sync.Mutex
	byHash map[string]*uploadLink // sha256 du jeton -> lien
	byID   map[string]*uploadLink
	now    func() time.Time
	base   string
	public bool
}

var linkStates sync.Map // *Service -> *linkState

func (s *Service) links() *linkState {
	v, _ := linkStates.LoadOrStore(s, &linkState{byHash: map[string]*uploadLink{}, byID: map[string]*uploadLink{}, now: time.Now})
	return v.(*linkState)
}

// SetUploadLinkBase fixe l'URL de base des liens d'envoi ; public = joignable depuis Internet (écouteur public).
func (s *Service) SetUploadLinkBase(base string, public bool) {
	ls := s.links()
	ls.mu.Lock()
	ls.base, ls.public = strings.TrimRight(base, "/"), public
	ls.mu.Unlock()
}

// UploadLinkBase renvoie l'URL de base des liens (repli : BaseURL) et si elle est publique.
func (s *Service) UploadLinkBase() (string, bool) {
	ls := s.links()
	ls.mu.Lock()
	b, p := ls.base, ls.public
	ls.mu.Unlock()
	if b == "" {
		return s.BaseURL(), false
	}
	return b, p
}

// SetUploadLinkClock remplace l'horloge des liens (tests).
func (s *Service) SetUploadLinkClock(now func() time.Time) {
	ls := s.links()
	ls.mu.Lock()
	ls.now = now
	ls.mu.Unlock()
}

func tokenKey(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// ValidUploadToken : forme exacte d'un jeton (43 caractères base64 URL). Sert aussi au filtre de la surface publique.
func ValidUploadToken(t string) bool {
	if len(t) != UploadLinkTokenLen {
		return false
	}
	for _, c := range t {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// purgeLocked oublie les liens expirés depuis plus d'une heure (le statut reste lisible un moment).
func (ls *linkState) purgeLocked(now time.Time) {
	for h, l := range ls.byHash {
		if now.After(l.expires.Add(time.Hour)) {
			delete(ls.byHash, h)
			delete(ls.byID, l.id)
		}
	}
}

// CreateUploadLink crée un lien d'envoi à usage unique. size et sha (facultatifs) sont vérifiés à la réception.
func (s *Service) CreateUploadLink(_ context.Context, name string, size int64, sha string, meta UploadMeta) (*UploadLinkInfo, error) {
	n, format, err := ValidUploadName(name)
	if err != nil {
		if strings.EqualFold(filepath.Ext(name), ".glb") {
			return nil, invalidf("le format .glb n'est pas importable dans la bibliothèque : exporter le modèle en .stl, .obj ou .3mf")
		}
		return nil, err
	}
	mx := s.maxFor(format)
	if size < 0 {
		return nil, invalidf("size : octets, positif")
	}
	if size > mx {
		return nil, invalidf("fichier trop gros pour le format %s : %d octets > limite %d (%d Mo)", format, size, mx, mx>>20)
	}
	sha = strings.ToLower(strings.TrimSpace(sha))
	if sha != "" && !validHex64(sha) {
		return nil, invalidf("sha256 invalide : 64 caractères hexadécimaux attendus")
	}
	if meta.OnDuplicate != "" && meta.OnDuplicate != "skip" && meta.OnDuplicate != "keep" {
		return nil, invalidf("on_duplicate : skip | keep")
	}
	u, err := normalizeUnit(meta.Unit)
	if err != nil {
		return nil, err
	}
	meta.Unit = u
	max := mx
	if size > 0 {
		max = size
	}
	rb := make([]byte, 32)
	if _, err := rand.Read(rb); err != nil {
		return nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(rb)
	ls := s.links()
	ls.mu.Lock()
	defer ls.mu.Unlock()
	now := ls.now()
	ls.purgeLocked(now)
	live := 0
	for _, l := range ls.byID {
		if l.state == "pending" && now.Before(l.expires) {
			live++
		}
	}
	if live >= maxUploadLinks {
		return nil, fmt.Errorf("%w : trop de liens d'envoi en attente (max %d) ; en utiliser un ou attendre leur expiration (%d min)", ErrConflict, maxUploadLinks, int(UploadLinkTTL.Minutes()))
	}
	ib := make([]byte, 9)
	_, _ = rand.Read(ib)
	l := &uploadLink{id: linkIDPrefix + hex.EncodeToString(ib), name: n, format: format, maxBytes: max, size: size, sha: sha, meta: meta, expires: now.Add(UploadLinkTTL), state: "pending"}
	ls.byHash[tokenKey(token)] = l
	ls.byID[l.id] = l
	return &UploadLinkInfo{LinkID: l.id, Token: token, Filename: n, Format: string(format), MaxBytes: max, ExpiresAt: l.expires}, nil
}

// IsUploadLinkID dit si un identifiant désigne un lien d'envoi (et non une session par morceaux).
func IsUploadLinkID(id string) bool { return strings.HasPrefix(id, linkIDPrefix) }

// UploadLinkStatus renvoie l'état d'un lien (par son identifiant).
func (s *Service) UploadLinkStatus(_ context.Context, id string) (UploadLinkStatus, error) {
	ls := s.links()
	ls.mu.Lock()
	defer ls.mu.Unlock()
	l := ls.byID[id]
	if l == nil {
		return UploadLinkStatus{}, fmt.Errorf("%w : lien d'envoi %q inconnu ou purgé", ErrNotFound, id)
	}
	st := l.state
	if st == "pending" && !ls.now().Before(l.expires) {
		st = "expired"
	}
	return UploadLinkStatus{LinkID: l.id, Filename: l.name, State: st, ExpiresAt: l.expires, Received: l.received, Total: l.size, Result: l.result, Error: l.err}, nil
}

// CancelUploadLink invalide un lien encore en attente.
func (s *Service) CancelUploadLink(id string) error {
	ls := s.links()
	ls.mu.Lock()
	defer ls.mu.Unlock()
	l := ls.byID[id]
	if l == nil {
		return fmt.Errorf("%w : lien d'envoi %q inconnu ou purgé", ErrNotFound, id)
	}
	if l.state == "pending" {
		l.state, l.err = "expired", "annulé"
		l.expires = ls.now()
	}
	return nil
}

var errLinkGone = fmt.Errorf("%w : lien d'envoi inconnu, expiré ou déjà utilisé", ErrNotFound)

// UploadLinkReceive consomme le jeton (usage unique) et reçoit le fichier entier depuis body. contentLength < 0 =
// inconnue. Les refus qui n'ont pas lu le corps (jeton inconnu, expiré, Content-Length excessif) ne consomment rien.
func (s *Service) UploadLinkReceive(ctx context.Context, token string, body io.Reader, contentLength int64) (*UploadResult, error) {
	if !ValidUploadToken(token) {
		return nil, errLinkGone
	}
	ls := s.links()
	ls.mu.Lock()
	l := ls.byHash[tokenKey(token)]
	now := ls.now()
	if l == nil || l.state != "pending" || !now.Before(l.expires) {
		ls.mu.Unlock()
		return nil, errLinkGone
	}
	if contentLength > l.maxBytes {
		max := l.maxBytes
		ls.mu.Unlock()
		return nil, fmt.Errorf("%w : %d octets annoncés, maximum %d pour ce lien", ErrUploadTooLarge, contentLength, max)
	}
	l.state = "receiving" // usage unique : le jeton ne sert plus, quoi qu'il arrive ensuite
	name, format, max, size, sha, meta := l.name, l.format, l.maxBytes, l.size, l.sha, l.meta
	ls.mu.Unlock()

	fail := func(err error) (*UploadResult, error) {
		ls.mu.Lock()
		l.state, l.err = "failed", err.Error()
		ls.mu.Unlock()
		return nil, err
	}
	st := s.up()
	_ = os.MkdirAll(st.dir, 0o700)
	tmp, err := os.CreateTemp(st.dir, "link-*.part")
	if err != nil {
		return fail(fmt.Errorf("réception : %w", err))
	}
	defer os.Remove(tmp.Name())
	n, cerr := io.Copy(tmp, io.LimitReader(&progressReader{r: body, add: func(k int64) { ls.mu.Lock(); l.received += k; ls.mu.Unlock() }}, max+1))
	if err := tmp.Close(); err != nil && cerr == nil {
		cerr = err
	}
	if cerr != nil {
		return fail(fmt.Errorf("%w : réception interrompue : %v ; demander un nouveau lien", ErrInvalid, cerr))
	}
	if n > max {
		return fail(fmt.Errorf("%w : le corps dépasse %d octets", ErrUploadTooLarge, max))
	}
	if n == 0 {
		return fail(invalidf("corps vide : envoyer le fichier avec curl -T fichier URL"))
	}
	if size > 0 && n != size {
		return fail(invalidf("taille reçue %d différente de la taille annoncée %d : fichier tronqué ou modifié ; demander un nouveau lien", n, size))
	}
	res, err := s.finalize(ctx, tmp.Name(), name, format, n, sha, meta)
	if err != nil {
		return fail(err)
	}
	ls.mu.Lock()
	l.state, l.result = "complete", res
	ls.mu.Unlock()
	return res, nil
}

type progressReader struct {
	r   io.Reader
	add func(int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.add(int64(n))
	}
	return n, err
}
