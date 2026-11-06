// Package humanauth enregistre les navigateurs de l'humain (docs/SECURITY.md).
//
// Modèle de menace : le modèle (Claude) n'a que les outils MCP et ne doit JAMAIS pouvoir produire
// l'accord de lancement ni d'arrêt d'une impression. Ces accords ne se donnent que sur des pages
// HTML servies par bambud, derrière un cookie de session. Ce cookie n'est délivré qu'à un navigateur
// qui saisit un code à usage unique (8 caractères, 10 min) obtenu par la commande `bambud enroll`,
// affichée sur le terminal de l'humain : le code ne transite JAMAIS par MCP ni par l'API JSON.
//
// Choix du canal CLI -> service : la commande écrit le code (haché) directement dans la base SQLite
// partagée. Pas de socket, pas de port, pas de fichier de commande à protéger en plus : la base est
// déjà protégée par les droits du compte, et le service relit la table à chaque saisie. Un processus
// qui a les droits de l'utilisateur sur ce dossier est hors périmètre (il pourrait aussi bien lire le
// coffre des codes d'accès) ; on rend l'abus visible (journal d'audit) et pas silencieux.
package humanauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// CookieName est le nom du cookie de session.
const CookieName = "bambu_session"

const (
	// CodeLen est la longueur du code d'enregistrement.
	CodeLen = 8
	// DefaultCodeTTL : validité d'un code.
	DefaultCodeTTL = 10 * time.Minute
	// DefaultSessionTTL : validité d'une session de navigateur.
	DefaultSessionTTL = 30 * 24 * time.Hour

	codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // sans 0/O/1/I
	maxFailures  = 6
	lockFor      = 5 * time.Minute
)

// Erreurs.
var (
	ErrBadCode     = errors.New("code d'enregistrement inconnu, expiré ou déjà utilisé")
	ErrLocked      = errors.New("trop d'essais : attendre quelques minutes")
	ErrNoSession   = errors.New("navigateur non enregistré")
	ErrSessionGone = errors.New("session expirée ou révoquée")
)

// Session est un navigateur enregistré (sans jeton).
type Session struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Revoked    bool      `json:"revoked"`
}

// Options du service.
type Options struct {
	CodeTTL    time.Duration
	SessionTTL time.Duration
	Now        func() time.Time
	// Secure dit si le cookie doit porter l'attribut Secure pour cette requête (https direct ou URL
	// publique en https). Par défaut : TLS direct seulement.
	Secure func(*http.Request) bool
}

// Service gère les codes et les sessions.
type Service struct {
	st   *store.Store
	o    Options
	mu   sync.Mutex
	fail []time.Time // échecs récents (limiteur global)
	lock time.Time   // verrouillé jusqu'à
}

// New construit le service au-dessus d'une base déjà migrée.
func New(st *store.Store, o Options) *Service {
	if o.CodeTTL <= 0 {
		o.CodeTTL = DefaultCodeTTL
	}
	if o.SessionTTL <= 0 {
		o.SessionTTL = DefaultSessionTTL
	}
	if o.Now == nil {
		o.Now = func() time.Time { return st.Now() }
	}
	return &Service{st: st, o: o}
}

func (s *Service) now() time.Time { return s.o.Now().UTC() }

func hashOf(v string) string {
	h := sha256.Sum256([]byte(v))
	return hex.EncodeToString(h[:])
}

// NormalizeCode met le code saisi en forme canonique (majuscules, sans tirets ni espaces).
func NormalizeCode(c string) string {
	c = strings.ToUpper(strings.TrimSpace(c))
	return strings.NewReplacer("-", "", " ", "").Replace(c)
}

// NewEnrollCode crée un code à usage unique ; seul son hachage est stocké. Appelé par la CLI.
func (s *Service) NewEnrollCode(ctx context.Context) (code string, expires time.Time, err error) {
	b := make([]byte, CodeLen)
	if _, err = rand.Read(b); err != nil {
		return "", time.Time{}, err
	}
	var sb strings.Builder
	for _, x := range b {
		sb.WriteByte(codeAlphabet[int(x)&31])
	}
	code = sb.String()
	now := s.now()
	expires = now.Add(s.o.CodeTTL)
	_, err = s.st.DB().ExecContext(ctx, `INSERT INTO human_enroll_codes(code_hash,created_at,expires_at) VALUES(?,?,?)`,
		hashOf(code), store.FormatTime(now), store.FormatTime(expires))
	if err == nil {
		_ = s.st.Audit(ctx, "human:cli", "human.enroll_code_created", "human_session", "", map[string]any{"expires": expires})
	}
	return code, expires, err
}

// FormatCode affiche un code par groupes de 4.
func FormatCode(c string) string {
	if len(c) == CodeLen {
		return c[:4] + "-" + c[4:]
	}
	return c
}

func (s *Service) locked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now().Before(s.lock)
}

func (s *Service) noteFailure() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	keep := s.fail[:0]
	for _, t := range s.fail {
		if now.Sub(t) < lockFor {
			keep = append(keep, t)
		}
	}
	s.fail = append(keep, now)
	if len(s.fail) >= maxFailures {
		s.lock = now.Add(lockFor)
		s.fail = nil
	}
}

// Enroll échange un code valide contre un jeton de session (renvoyé une seule fois, jamais stocké en clair).
func (s *Service) Enroll(ctx context.Context, code, label string) (token string, sess Session, err error) {
	if s.locked() {
		return "", Session{}, ErrLocked
	}
	code = NormalizeCode(code)
	if len(code) != CodeLen {
		s.noteFailure()
		return "", Session{}, ErrBadCode
	}
	if len(label) > 80 {
		label = label[:80]
	}
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", Session{}, err
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	idb := make([]byte, 6)
	_, _ = rand.Read(idb)
	now := s.now()
	sess = Session{ID: "ses_" + hex.EncodeToString(idb), Label: label, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(s.o.SessionTTL)}
	err = s.st.Tx(ctx, func(tx *sql.Tx) error {
		var exp string
		var used sql.NullString
		e := tx.QueryRowContext(ctx, `SELECT expires_at, used_at FROM human_enroll_codes WHERE code_hash=?`, hashOf(code)).Scan(&exp, &used)
		if errors.Is(e, sql.ErrNoRows) {
			return ErrBadCode
		}
		if e != nil {
			return e
		}
		t, _ := store.ParseTime(exp)
		if used.Valid || !now.Before(t) {
			return ErrBadCode
		}
		res, e := tx.ExecContext(ctx, `UPDATE human_enroll_codes SET used_at=? WHERE code_hash=? AND used_at IS NULL`, store.FormatTime(now), hashOf(code))
		if e != nil {
			return e
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrBadCode
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO human_sessions(id,token_hash,label,created_at,last_seen_at,expires_at) VALUES(?,?,?,?,?,?)`,
			sess.ID, hashOf(token), label, store.FormatTime(now), store.FormatTime(now), store.FormatTime(sess.ExpiresAt)); e != nil {
			return e
		}
		return s.st.AuditTx(ctx, tx, "human:web", "human.session_created", "human_session", sess.ID, map[string]any{"label": label})
	})
	if err != nil {
		if errors.Is(err, ErrBadCode) {
			s.noteFailure()
		}
		return "", Session{}, err
	}
	return token, sess, nil
}

// Lookup valide un jeton de cookie.
func (s *Service) Lookup(ctx context.Context, token string) (Session, error) {
	if token == "" || len(token) > 200 {
		return Session{}, ErrNoSession
	}
	var ss Session
	var created, seen, exp string
	var rev sql.NullString
	err := s.st.DB().QueryRowContext(ctx, `SELECT id,label,created_at,last_seen_at,expires_at,revoked_at FROM human_sessions WHERE token_hash=?`, hashOf(token)).
		Scan(&ss.ID, &ss.Label, &created, &seen, &exp, &rev)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNoSession
	}
	if err != nil {
		return Session{}, err
	}
	ss.CreatedAt, _ = store.ParseTime(created)
	ss.LastSeenAt, _ = store.ParseTime(seen)
	ss.ExpiresAt, _ = store.ParseTime(exp)
	ss.Revoked = rev.Valid
	if ss.Revoked || !s.now().Before(ss.ExpiresAt) {
		return Session{}, ErrSessionGone
	}
	if s.now().Sub(ss.LastSeenAt) > time.Minute {
		_, _ = s.st.DB().ExecContext(ctx, `UPDATE human_sessions SET last_seen_at=? WHERE id=?`, store.FormatTime(s.now()), ss.ID)
	}
	return ss, nil
}

// SessionFromRequest lit le cookie de la requête.
func (s *Service) SessionFromRequest(r *http.Request) (Session, error) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return Session{}, ErrNoSession
	}
	return s.Lookup(r.Context(), c.Value)
}

// List renvoie les sessions (actives d'abord, récentes d'abord).
func (s *Service) List(ctx context.Context) ([]Session, error) {
	rows, err := s.st.DB().QueryContext(ctx, `SELECT id,label,created_at,last_seen_at,expires_at,revoked_at FROM human_sessions ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var ss Session
		var created, seen, exp string
		var rev sql.NullString
		if err := rows.Scan(&ss.ID, &ss.Label, &created, &seen, &exp, &rev); err != nil {
			return nil, err
		}
		ss.CreatedAt, _ = store.ParseTime(created)
		ss.LastSeenAt, _ = store.ParseTime(seen)
		ss.ExpiresAt, _ = store.ParseTime(exp)
		ss.Revoked = rev.Valid
		out = append(out, ss)
	}
	return out, rows.Err()
}

// Revoke révoque une session par identifiant (ou préfixe non ambigu). Renvoie le nombre révoqué.
func (s *Service) Revoke(ctx context.Context, id string) (int, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return 0, errors.New("identifiant de session requis")
	}
	rows, err := s.st.DB().QueryContext(ctx, `SELECT id FROM human_sessions WHERE revoked_at IS NULL AND id LIKE ? ESCAPE '\'`, strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(id)+"%")
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var x string
		if rows.Scan(&x) == nil {
			ids = append(ids, x)
		}
	}
	rows.Close()
	if len(ids) == 0 {
		return 0, fmt.Errorf("aucune session active ne commence par %q", id)
	}
	if len(ids) > 1 {
		return 0, fmt.Errorf("%q est ambigu (%d sessions) : donner l'identifiant complet", id, len(ids))
	}
	_, err = s.st.DB().ExecContext(ctx, `UPDATE human_sessions SET revoked_at=? WHERE id=? AND revoked_at IS NULL`, store.FormatTime(s.now()), ids[0])
	if err == nil {
		_ = s.st.Audit(ctx, "human:cli", "human.session_revoked", "human_session", ids[0], nil)
	}
	return 1, err
}

// RevokeAll révoque toutes les sessions actives.
func (s *Service) RevokeAll(ctx context.Context) (int64, error) {
	res, err := s.st.DB().ExecContext(ctx, `UPDATE human_sessions SET revoked_at=? WHERE revoked_at IS NULL`, store.FormatTime(s.now()))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		_ = s.st.Audit(ctx, "human:cli", "human.sessions_revoked_all", "human_session", "", map[string]any{"count": n})
	}
	return n, nil
}

func (s *Service) setCookie(w http.ResponseWriter, r *http.Request, token string, sess Session) {
	secure := r.TLS != nil
	if s.o.Secure != nil && s.o.Secure(r) {
		secure = true
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: token, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode,
		Expires: sess.ExpiresAt, MaxAge: int(sess.ExpiresAt.Sub(s.now()).Seconds())})
}
