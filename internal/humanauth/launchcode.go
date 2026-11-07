package humanauth

// Code de lancement personnel (docs/SECURITY.md § 12).
//
// Le bouton « Lancer » de la vue MCP App ne peut pas reposer sur la visibilité des outils : le serveur ne
// distingue pas un appel venu de la vue d'un appel fait par le modèle. L'accord repose donc sur un SECRET que
// seul l'humain connaît : un code de 6 à 8 chiffres, défini sur une page humaine (navigateur enregistré) ou
// remis à zéro depuis son terminal, stocké haché (argon2id), jamais renvoyé, jamais journalisé. Trois échecs
// consécutifs verrouillent TOUT (il faut alors la page humaine ou la CLI), et deux essais ne peuvent pas
// se suivre de moins de quelques secondes.

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

const (
	// LaunchCodeMinLen et LaunchCodeMaxLen : bornes du code (chiffres uniquement).
	LaunchCodeMinLen = 6
	LaunchCodeMaxLen = 8
	// LaunchMaxFailures : échecs consécutifs avant verrouillage global.
	LaunchMaxFailures = 3
	// DefaultLaunchAttemptDelay : délai minimal entre deux essais.
	DefaultLaunchAttemptDelay = 3 * time.Second

	argonTime    = 2
	argonMemory  = 32 * 1024 // 32 Mio
	argonThreads = 1
	argonKeyLen  = 32
	launchNonceT = 15 * time.Minute
)

// Erreurs du code de lancement. Aucune ne contient le code saisi.
var (
	ErrLaunchCodeNotSet  = errors.New("aucun code de lancement n'est défini")
	ErrLaunchCodeLocked  = errors.New("approbation par code verrouillée après trop d'échecs : seule la page /setup/launch-code (navigateur enregistré) ou la commande « bambud launch-code --unlock » la déverrouille")
	ErrLaunchCodeTooFast = errors.New("deux essais trop rapprochés : attendre quelques secondes")
	ErrLaunchCodeFormat  = fmt.Errorf("le code de lancement est composé de %d à %d chiffres", LaunchCodeMinLen, LaunchCodeMaxLen)
	ErrLaunchCodeWeak    = errors.New("code trop simple (chiffres identiques ou suite) : en choisir un autre")
	ErrLaunchCodeBad     = errors.New("code incorrect")
)

// BadLaunchCodeError : code incorrect ; Left = essais restants avant verrouillage, Locked = verrouillé par cet échec.
type BadLaunchCodeError struct {
	Left   int
	Locked bool
}

func (e *BadLaunchCodeError) Error() string {
	if e.Locked {
		return ErrLaunchCodeBad.Error() + " : approbation par code VERROUILLÉE (page /setup/launch-code ou bambud launch-code --unlock)"
	}
	return fmt.Sprintf("%s (essais restants avant verrouillage : %d)", ErrLaunchCodeBad, e.Left)
}

// Is permet errors.Is(err, ErrLaunchCodeBad).
func (e *BadLaunchCodeError) Is(target error) bool { return target == ErrLaunchCodeBad }

// LaunchCodeStatus : état public (aucun secret).
type LaunchCodeStatus struct {
	Set          bool      `json:"set"`
	Locked       bool      `json:"locked"`
	AttemptsLeft int       `json:"attempts_left"`
	UpdatedAt    time.Time `json:"updated_at,omitzero"`
}

// LaunchCodes gère le code de lancement d'une base. Une seule instance par base (LaunchCodesFor).
type LaunchCodes struct {
	st       *store.Store
	mu       sync.Mutex
	minDelay time.Duration
	nonces   map[string]time.Time // nonces des formulaires de /setup/launch-code
}

var launchRegistry sync.Map // *store.Store -> *LaunchCodes

// LaunchCodesFor renvoie l'instance unique liée à cette base (la page humaine et la chaîne d'impression partagent
// ainsi le même verrou en mémoire ; l'état durable, lui, est dans la base).
func LaunchCodesFor(st *store.Store) *LaunchCodes {
	if v, ok := launchRegistry.Load(st); ok {
		return v.(*LaunchCodes)
	}
	v, _ := launchRegistry.LoadOrStore(st, &LaunchCodes{st: st, minDelay: DefaultLaunchAttemptDelay, nonces: map[string]time.Time{}})
	return v.(*LaunchCodes)
}

// SetMinDelay change le délai minimal entre deux essais (tests).
func (l *LaunchCodes) SetMinDelay(d time.Duration) {
	l.mu.Lock()
	l.minDelay = d
	l.mu.Unlock()
}

// ValidateLaunchCode contrôle la forme d'un nouveau code (6 à 8 chiffres, ni identiques ni en suite).
func ValidateLaunchCode(code string) error {
	if !wellFormed(code) {
		return ErrLaunchCodeFormat
	}
	same, up, down := true, true, true
	for i := 1; i < len(code); i++ {
		if code[i] != code[0] {
			same = false
		}
		if code[i] != code[i-1]+1 {
			up = false
		}
		if code[i] != code[i-1]-1 {
			down = false
		}
	}
	if same || up || down {
		return ErrLaunchCodeWeak
	}
	return nil
}

func wellFormed(code string) bool {
	if len(code) < LaunchCodeMinLen || len(code) > LaunchCodeMaxLen {
		return false
	}
	for i := 0; i < len(code); i++ {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}

func hashLaunchCode(code string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	k := argon2.IDKey([]byte(code), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(k)), nil
}

func checkLaunchCode(code, enc string) bool {
	parts := strings.Split(enc, "$")
	if len(parts) != 5 || parts[0] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[2], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || m > 256*1024 || t > 10 || p == 0 {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[3])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[4])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(code), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func (l *LaunchCodes) now() time.Time { return l.st.Now().UTC() }

// Status renvoie l'état public (aucun secret).
func (l *LaunchCodes) Status(ctx context.Context) (LaunchCodeStatus, error) {
	var fails int
	var locked sql.NullString
	var upd string
	err := l.st.DB().QueryRowContext(ctx, `SELECT failures, locked_at, updated_at FROM launch_code WHERE id=1`).Scan(&fails, &locked, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return LaunchCodeStatus{}, nil
	}
	if err != nil {
		return LaunchCodeStatus{}, err
	}
	t, _ := store.ParseTime(upd)
	st := LaunchCodeStatus{Set: true, Locked: locked.Valid, UpdatedAt: t, AttemptsLeft: max(LaunchMaxFailures-fails, 0)}
	if st.Locked {
		st.AttemptsLeft = 0
	}
	return st, nil
}

// Set définit ou remplace le code (déverrouille et remet les échecs à zéro). actor : « human:web » ou « human:cli ».
func (l *LaunchCodes) Set(ctx context.Context, code, actor string) error {
	if err := ValidateLaunchCode(code); err != nil {
		return err
	}
	enc, err := hashLaunchCode(code)
	if err != nil {
		return err
	}
	now := store.FormatTime(l.now())
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.st.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, `INSERT INTO launch_code(id,code_hash,created_at,updated_at,failures,locked_at,last_attempt_at) VALUES(1,?,?,?,0,NULL,NULL)
			ON CONFLICT(id) DO UPDATE SET code_hash=excluded.code_hash, updated_at=excluded.updated_at, failures=0, locked_at=NULL, last_attempt_at=NULL`, enc, now, now); e != nil {
			return e
		}
		return l.st.AuditTx(ctx, tx, actor, "human.launch_code_set", "launch_code", "1", nil)
	})
}

// Revoke supprime le code : la vue MCP App retombe sur le lien navigateur.
func (l *LaunchCodes) Revoke(ctx context.Context, actor string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.st.Tx(ctx, func(tx *sql.Tx) error {
		res, e := tx.ExecContext(ctx, `DELETE FROM launch_code WHERE id=1`)
		if e != nil {
			return e
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		return l.st.AuditTx(ctx, tx, actor, "human.launch_code_revoked", "launch_code", "1", nil)
	})
}

// Unlock remet les échecs à zéro et lève le verrou (le code reste le même).
func (l *LaunchCodes) Unlock(ctx context.Context, actor string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.st.Tx(ctx, func(tx *sql.Tx) error {
		res, e := tx.ExecContext(ctx, `UPDATE launch_code SET failures=0, locked_at=NULL, last_attempt_at=NULL WHERE id=1`)
		if e != nil {
			return e
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		return l.st.AuditTx(ctx, tx, actor, "human.launch_code_unlocked", "launch_code", "1", nil)
	})
}

// Verify contrôle un code saisi. Un essai mal formé (pas 6 à 8 chiffres) est refusé SANS compter : il ne permet
// aucune devinette. Tout essai bien formé est compté AVANT la comparaison (aucune course entre appels
// parallèles), puis remis à zéro en cas de réussite. detail (identifiants de confirmation, jamais le code) est
// versé au journal d'audit en cas d'échec.
func (l *LaunchCodes) Verify(ctx context.Context, code string, detail map[string]any) error {
	if !wellFormed(code) {
		return ErrLaunchCodeFormat
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	var enc string
	var res error
	var failsNow int
	err := l.st.Tx(ctx, func(tx *sql.Tx) error {
		var fails int
		var locked, last sql.NullString
		e := tx.QueryRowContext(ctx, `SELECT code_hash, failures, locked_at, last_attempt_at FROM launch_code WHERE id=1`).Scan(&enc, &fails, &locked, &last)
		if errors.Is(e, sql.ErrNoRows) {
			res = ErrLaunchCodeNotSet
			return nil
		}
		if e != nil {
			return e
		}
		if locked.Valid || fails >= LaunchMaxFailures {
			res = ErrLaunchCodeLocked
			return nil
		}
		if last.Valid {
			if t, perr := store.ParseTime(last.String); perr == nil && now.Sub(t) < l.minDelay {
				res = ErrLaunchCodeTooFast
				return nil
			}
		}
		failsNow = fails + 1
		var lock any
		if failsNow >= LaunchMaxFailures {
			lock = store.FormatTime(now)
		}
		_, e = tx.ExecContext(ctx, `UPDATE launch_code SET failures=?, locked_at=?, last_attempt_at=? WHERE id=1`, failsNow, lock, store.FormatTime(now))
		return e
	})
	if err != nil {
		return err
	}
	if res != nil {
		return res
	}
	if checkLaunchCode(code, enc) {
		return l.st.Tx(ctx, func(tx *sql.Tx) error {
			_, e := tx.ExecContext(ctx, `UPDATE launch_code SET failures=0, locked_at=NULL WHERE id=1`)
			return e
		})
	}
	d := map[string]any{"failures": failsNow, "locked": failsNow >= LaunchMaxFailures}
	for k, v := range detail {
		d[k] = v
	}
	_ = l.st.Audit(ctx, "mcp:app", "print.launch_code_failed", "launch_code", "1", d)
	return &BadLaunchCodeError{Left: LaunchMaxFailures - failsNow, Locked: failsNow >= LaunchMaxFailures}
}

// ---- nonces de la page humaine ----

func (l *LaunchCodes) issueNonce() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	n := base64.RawURLEncoding.EncodeToString(b)
	now := l.now()
	l.mu.Lock()
	for k, t := range l.nonces {
		if now.After(t) {
			delete(l.nonces, k)
		}
	}
	l.nonces[n] = now.Add(launchNonceT)
	l.mu.Unlock()
	return n
}

func (l *LaunchCodes) useNonce(n string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	t, ok := l.nonces[n]
	delete(l.nonces, n)
	return ok && l.now().Before(t)
}
