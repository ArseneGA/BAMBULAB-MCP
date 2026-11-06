// Package jobs implémente la file de travaux persistante (SQLite) : idempotence,
// concurrence bornée par classe, heartbeat, journaux bornés, annulation,
// reprise au démarrage et registre de handlers par type de travail.
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Erreurs.
var (
	ErrNotFound     = errors.New("travail introuvable")
	ErrNotRunning   = errors.New("le travail n'est plus en cours d'exécution")
	ErrKindMismatch = errors.New("clé d'idempotence déjà utilisée par un autre type de travail")
)

// QueueConfig règle la file.
type QueueConfig struct {
	// Limits : travaux simultanés par classe. Défaut heavy=1, light=4.
	Limits map[contracts.JobClass]int
	// MaxLogLines : journaux conservés (les plus récents). Défaut 200.
	MaxLogLines int
	// MaxLogLineBytes : taille max d'une ligne. Défaut 2000.
	MaxLogLineBytes int
	// MaxAttempts : au-delà, un travail interrompu n'est plus relancé. Défaut 3.
	MaxAttempts int
}

func (c *QueueConfig) defaults() {
	if c.Limits == nil {
		c.Limits = map[contracts.JobClass]int{}
	}
	if c.Limits[contracts.ClassHeavy] <= 0 {
		c.Limits[contracts.ClassHeavy] = 1
	}
	if c.Limits[contracts.ClassLight] <= 0 {
		c.Limits[contracts.ClassLight] = 4
	}
	if c.MaxLogLines <= 0 {
		c.MaxLogLines = 200
	}
	if c.MaxLogLineBytes <= 0 {
		c.MaxLogLineBytes = 2000
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 3
	}
}

// Queue est la file persistante.
type Queue struct {
	s    *store.Store
	cfg  QueueConfig
	wake chan struct{}
}

// NewQueue crée une file sur le store.
func NewQueue(s *store.Store, cfg QueueConfig) *Queue {
	cfg.defaults()
	return &Queue{s: s, cfg: cfg, wake: make(chan struct{}, 1)}
}

// Wake renvoie un canal signalé à chaque enqueue (pour réveiller le runner).
func (q *Queue) Wake() <-chan struct{} { return q.wake }

// Limit renvoie la concurrence autorisée pour une classe.
func (q *Queue) Limit(c contracts.JobClass) int { return q.cfg.Limits[c] }

// EnqueueParams décrit un nouveau travail.
type EnqueueParams struct {
	Kind  string
	Class contracts.JobClass // défaut : light
	// Params : entrée JSON du handler (optionnelle).
	Params json.RawMessage
	// IdempotencyKey : si fournie et déjà connue, le travail existant est renvoyé.
	IdempotencyKey string
}

const jobColumns = `id,kind,class,state,progress,message,logs,params,result,error,idempotency_key,attempts,cancel_requested,created_at,started_at,heartbeat_at,finished_at`

type scanner interface{ Scan(dest ...any) error }

func scanJob(r scanner) (contracts.Job, error) {
	var j contracts.Job
	var class, state, logs, ca string
	var params, result, errJSON, idem, sa, ha, fa sql.NullString
	var cancel int
	if err := r.Scan(&j.ID, &j.Kind, &class, &state, &j.Progress, &j.Message, &logs, &params, &result, &errJSON,
		&idem, &j.Attempts, &cancel, &ca, &sa, &ha, &fa); err != nil {
		return j, err
	}
	j.Class, j.State = contracts.JobClass(class), contracts.JobState(state)
	_ = json.Unmarshal([]byte(logs), &j.Logs)
	if params.Valid {
		j.Params = json.RawMessage(params.String)
	}
	if result.Valid {
		j.Result = json.RawMessage(result.String)
	}
	if errJSON.Valid {
		j.Error = new(contracts.JobError)
		_ = json.Unmarshal([]byte(errJSON.String), j.Error)
	}
	j.IdempotencyKey = idem.String
	j.CancelRequested = cancel != 0
	j.CreatedAt, _ = store.ParseTime(ca)
	tp := func(n sql.NullString) *time.Time {
		if !n.Valid {
			return nil
		}
		t, _ := store.ParseTime(n.String)
		return &t
	}
	j.StartedAt, j.HeartbeatAt, j.FinishedAt = tp(sa), tp(ha), tp(fa)
	return j, nil
}

func (q *Queue) getTx(ctx context.Context, tx *sql.Tx, id string) (contracts.Job, error) {
	j, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return j, fmt.Errorf("%s : %w", id, ErrNotFound)
	}
	return j, err
}

// Get renvoie un travail.
func (q *Queue) Get(ctx context.Context, id string) (contracts.Job, error) {
	j, err := scanJob(q.s.DB().QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return j, fmt.Errorf("%s : %w", id, ErrNotFound)
	}
	return j, err
}

// List renvoie les travaux récents (filtrables par état), plus récents d'abord.
func (q *Queue) List(ctx context.Context, state contracts.JobState, limit int) ([]contracts.Job, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	query := `SELECT ` + jobColumns + ` FROM jobs`
	var args []any
	if state != "" {
		query += ` WHERE state=?`
		args = append(args, string(state))
	}
	query += ` ORDER BY created_at DESC, rowid DESC LIMIT ?`
	args = append(args, limit)
	rows, err := q.s.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contracts.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// Enqueue ajoute un travail. created=false si la clé d'idempotence existait déjà
// (le travail existant est alors renvoyé, quel que soit son état).
func (q *Queue) Enqueue(ctx context.Context, p EnqueueParams) (job contracts.Job, created bool, err error) {
	if strings.TrimSpace(p.Kind) == "" {
		return job, false, errors.New("type de travail requis")
	}
	if p.Class == "" {
		p.Class = contracts.ClassLight
	}
	if p.Class != contracts.ClassHeavy && p.Class != contracts.ClassLight {
		return job, false, fmt.Errorf("classe inconnue %q", p.Class)
	}
	if len(p.Params) > 0 && !json.Valid(p.Params) {
		return job, false, errors.New("params n'est pas un JSON valide")
	}
	err = q.s.Tx(ctx, func(tx *sql.Tx) error {
		if p.IdempotencyKey != "" {
			existing, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE idempotency_key=?`, p.IdempotencyKey))
			if err == nil {
				if existing.Kind != p.Kind {
					return fmt.Errorf("%w (%s ≠ %s)", ErrKindMismatch, existing.Kind, p.Kind)
				}
				job, created = existing, false
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		id := contracts.NewID("job")
		var params, idem any
		if len(p.Params) > 0 {
			params = string(p.Params)
		}
		if p.IdempotencyKey != "" {
			idem = p.IdempotencyKey
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO jobs(id,kind,class,state,params,idempotency_key,created_at) VALUES(?,?,?,'queued',?,?,?)`,
			id, p.Kind, string(p.Class), params, idem, store.FormatTime(q.s.Now())); err != nil {
			return err
		}
		if err := q.s.AuditTx(ctx, tx, "system", "job.enqueue", "job", id, map[string]any{"kind": p.Kind, "class": p.Class}); err != nil {
			return err
		}
		j, err := q.getTx(ctx, tx, id)
		job, created = j, true
		return err
	})
	if err == nil && created {
		select {
		case q.wake <- struct{}{}:
		default:
		}
	}
	return job, created, err
}

// Claim réserve le plus ancien travail exécutable en respectant la concurrence
// par classe. Renvoie (nil, nil) si rien n'est disponible.
func (q *Queue) Claim(ctx context.Context, workerID string) (*contracts.Job, error) {
	var out *contracts.Job
	err := q.s.Tx(ctx, func(tx *sql.Tx) error {
		running := map[contracts.JobClass]int{}
		rows, err := tx.QueryContext(ctx, `SELECT class, COUNT(*) FROM jobs WHERE state='running' GROUP BY class`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c string
			var n int
			if err := rows.Scan(&c, &n); err != nil {
				rows.Close()
				return err
			}
			running[contracts.JobClass(c)] = n
		}
		rows.Close()
		var allowed []any
		ph := []string{}
		for _, c := range []contracts.JobClass{contracts.ClassHeavy, contracts.ClassLight} {
			if running[c] < q.cfg.Limits[c] {
				allowed = append(allowed, string(c))
				ph = append(ph, "?")
			}
		}
		if len(allowed) == 0 {
			return nil
		}
		var id string
		err = tx.QueryRowContext(ctx,
			`SELECT id FROM jobs WHERE state='queued' AND class IN (`+strings.Join(ph, ",")+`) ORDER BY created_at, rowid LIMIT 1`,
			allowed...).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		now := store.FormatTime(q.s.Now())
		if _, err := tx.ExecContext(ctx,
			`UPDATE jobs SET state='running', attempts=attempts+1, worker_id=?, started_at=?, heartbeat_at=?, finished_at=NULL WHERE id=? AND state='queued'`,
			workerID, now, now, id); err != nil {
			return err
		}
		j, err := q.getTx(ctx, tx, id)
		out = &j
		return err
	})
	return out, err
}

// Heartbeat prouve que le travail vit ; renvoie true si l'annulation a été demandée.
func (q *Queue) Heartbeat(ctx context.Context, id string) (cancelRequested bool, err error) {
	err = q.s.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE jobs SET heartbeat_at=? WHERE id=? AND state='running'`, store.FormatTime(q.s.Now()), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%s : %w", id, ErrNotRunning)
		}
		var c int
		if err := tx.QueryRowContext(ctx, `SELECT cancel_requested FROM jobs WHERE id=?`, id).Scan(&c); err != nil {
			return err
		}
		cancelRequested = c != 0
		return nil
	})
	return
}

// SetProgress met à jour l'avancement (borné à [0,1]) et le message courant.
func (q *Queue) SetProgress(ctx context.Context, id string, progress float64, message string) error {
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}
	return q.mustUpdate(ctx, id, `UPDATE jobs SET progress=?, message=? WHERE id=? AND state='running'`, progress, truncate(message, q.cfg.MaxLogLineBytes))
}

func (q *Queue) mustUpdate(ctx context.Context, id, query string, args ...any) error {
	args = append(args, id) // l'id est le dernier paramètre de chaque requête appelante
	return q.s.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%s : %w", id, ErrNotRunning)
		}
		return nil
	})
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for !utf8.ValidString(s) && len(s) > 0 {
		s = s[:len(s)-1]
	}
	return s + "…"
}

// AppendLog ajoute une ligne de journal ; seules les MaxLogLines plus récentes sont gardées.
func (q *Queue) AppendLog(ctx context.Context, id, line string) error {
	return q.s.Tx(ctx, func(tx *sql.Tx) error {
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT logs FROM jobs WHERE id=?`, id).Scan(&raw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%s : %w", id, ErrNotFound)
			}
			return err
		}
		var logs []string
		_ = json.Unmarshal([]byte(raw), &logs)
		logs = append(logs, truncate(line, q.cfg.MaxLogLineBytes))
		if over := len(logs) - q.cfg.MaxLogLines; over > 0 {
			logs = logs[over:]
		}
		b, _ := json.Marshal(logs)
		_, err := tx.ExecContext(ctx, `UPDATE jobs SET logs=? WHERE id=?`, string(b), id)
		return err
	})
}

func (q *Queue) finish(ctx context.Context, id string, state contracts.JobState, result json.RawMessage, jerr *contracts.JobError, setProgress bool) error {
	return q.s.Tx(ctx, func(tx *sql.Tx) error {
		var res, e any
		if len(result) > 0 {
			if !json.Valid(result) {
				return errors.New("result n'est pas un JSON valide")
			}
			res = string(result)
		}
		if jerr != nil {
			b, _ := json.Marshal(jerr)
			e = string(b)
		}
		query := `UPDATE jobs SET state=?, result=?, error=?, finished_at=? WHERE id=? AND state='running'`
		if setProgress {
			query = `UPDATE jobs SET state=?, result=?, error=?, finished_at=?, progress=1 WHERE id=? AND state='running'`
		}
		r, err := tx.ExecContext(ctx, query, string(state), res, e, store.FormatTime(q.s.Now()), id)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return fmt.Errorf("%s : %w", id, ErrNotRunning)
		}
		return q.s.AuditTx(ctx, tx, "system", "job."+string(state), "job", id, nil)
	})
}

// Complete termine un travail en succès.
func (q *Queue) Complete(ctx context.Context, id string, result json.RawMessage) error {
	return q.finish(ctx, id, contracts.JobSucceeded, result, nil, true)
}

// Fail termine un travail en échec.
func (q *Queue) Fail(ctx context.Context, id string, code, message string) error {
	return q.finish(ctx, id, contracts.JobFailed, nil, &contracts.JobError{Code: code, Message: truncate(message, q.cfg.MaxLogLineBytes)}, false)
}

// MarkCancelled termine un travail en cours comme annulé.
func (q *Queue) MarkCancelled(ctx context.Context, id string) error {
	return q.finish(ctx, id, contracts.JobCancelled, nil, nil, false)
}

// Requeue remet en file un travail en cours (arrêt propre d'un travail rejouable).
func (q *Queue) Requeue(ctx context.Context, id string) error {
	return q.mustUpdate(ctx, id, `UPDATE jobs SET state='queued', worker_id=NULL, progress=0, message='' WHERE id=? AND state='running'`)
}

// CancelOutcome décrit le résultat d'une demande d'annulation.
type CancelOutcome string

const (
	// CancelledNow : le travail attendait en file, annulé immédiatement.
	CancelledNow CancelOutcome = "cancelled"
	// CancelRequested : travail en cours ; son contexte sera annulé, l'état final arrivera ensuite.
	CancelRequested CancelOutcome = "cancel_requested"
	// AlreadyFinished : déjà terminé, rien à faire.
	AlreadyFinished CancelOutcome = "already_finished"
)

// RequestCancel annule selon la phase : file -> immédiat ; en cours -> demande
// relayée par heartbeat au contexte du handler ; terminé -> sans effet.
func (q *Queue) RequestCancel(ctx context.Context, id string) (CancelOutcome, error) {
	var outcome CancelOutcome
	err := q.s.Tx(ctx, func(tx *sql.Tx) error {
		j, err := q.getTx(ctx, tx, id)
		if err != nil {
			return err
		}
		switch j.State {
		case contracts.JobQueued:
			if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state='cancelled', cancel_requested=1, finished_at=? WHERE id=? AND state='queued'`,
				store.FormatTime(q.s.Now()), id); err != nil {
				return err
			}
			outcome = CancelledNow
		case contracts.JobRunning:
			if _, err := tx.ExecContext(ctx, `UPDATE jobs SET cancel_requested=1 WHERE id=?`, id); err != nil {
				return err
			}
			outcome = CancelRequested
		default:
			outcome = AlreadyFinished
			return nil
		}
		return q.s.AuditTx(ctx, tx, "system", "job.cancel", "job", id, map[string]any{"outcome": outcome})
	})
	return outcome, err
}

// RecoveryReport résume la reprise au démarrage.
type RecoveryReport struct {
	Requeued  []string `json:"requeued"`
	Unknown   []string `json:"unknown"`
	Cancelled []string `json:"cancelled"`
	Failed    []string `json:"failed"`
}

// Recover traite les travaux restés « running » après un arrêt brutal :
//   - annulation déjà demandée -> cancelled ;
//   - kind rejouable et tentatives restantes -> requeue ;
//   - kind rejouable mais tentatives épuisées -> failed ;
//   - sinon (non rejouable, ex. start_print) -> unknown, JAMAIS relancé.
//
// À appeler une seule fois, avant de démarrer les workers.
func (q *Queue) Recover(ctx context.Context, replayable func(kind string) bool) (RecoveryReport, error) {
	rep := RecoveryReport{}
	err := q.s.Tx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE state='running' ORDER BY created_at, rowid`)
		if err != nil {
			return err
		}
		var orphans []contracts.Job
		for rows.Next() {
			j, err := scanJob(rows)
			if err != nil {
				rows.Close()
				return err
			}
			orphans = append(orphans, j)
		}
		rows.Close()
		now := store.FormatTime(q.s.Now())
		for _, j := range orphans {
			var query string
			var args []any
			var note string
			switch {
			case j.CancelRequested:
				query, note = `UPDATE jobs SET state='cancelled', finished_at=? WHERE id=?`, "annulé : redémarrage après demande d'annulation"
				args = []any{now, j.ID}
				rep.Cancelled = append(rep.Cancelled, j.ID)
			case replayable(j.Kind) && j.Attempts < q.cfg.MaxAttempts:
				query, note = `UPDATE jobs SET state='queued', worker_id=NULL, progress=0, message='' WHERE id=?`, "remis en file après redémarrage"
				args = []any{j.ID}
				rep.Requeued = append(rep.Requeued, j.ID)
			case replayable(j.Kind):
				e, _ := json.Marshal(contracts.JobError{Code: "attempts_exhausted", Message: "tentatives épuisées après interruptions"})
				query, note = `UPDATE jobs SET state='failed', error=?, finished_at=? WHERE id=?`, "échec : tentatives épuisées"
				args = []any{string(e), now, j.ID}
				rep.Failed = append(rep.Failed, j.ID)
			default:
				e, _ := json.Marshal(contracts.JobError{Code: "interrupted_unknown", Message: "interrompu par un arrêt du service ; type non rejouable, l'issue réelle est inconnue : vérifier l'état réel avant toute action"})
				query, note = `UPDATE jobs SET state='unknown', error=?, finished_at=? WHERE id=?`, "issue inconnue : interrompu, non rejouable"
				args = []any{string(e), now, j.ID}
				rep.Unknown = append(rep.Unknown, j.ID)
			}
			if _, err := tx.ExecContext(ctx, query, args...); err != nil {
				return err
			}
			logs := append(j.Logs, "reprise : "+note)
			if over := len(logs) - q.cfg.MaxLogLines; over > 0 {
				logs = logs[over:]
			}
			lb, _ := json.Marshal(logs)
			if _, err := tx.ExecContext(ctx, `UPDATE jobs SET logs=? WHERE id=?`, string(lb), j.ID); err != nil {
				return err
			}
			if err := q.s.AuditTx(ctx, tx, "system", "job.recover", "job", j.ID, map[string]any{"kind": j.Kind, "note": note}); err != nil {
				return err
			}
		}
		return nil
	})
	return rep, err
}
