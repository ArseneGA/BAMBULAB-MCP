package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

// ErrConflict : la version (rev) du document a changé depuis sa lecture.
var ErrConflict = errors.New("conflit de version : le document a été modifié")

const confirmationsTable = "print_confirmations"

// PutDoc insère ou remplace un document JSON (écrasement inconditionnel : la
// version rev est incrémentée). Pour un écrasement conditionnel, voir PutDocIfRev.
// Les confirmations d'impression ne peuvent pas être marquées « utilisées » par
// ce chemin (ConsumeConfirmation seul le fait) et une confirmation déjà
// utilisée est immuable (déclencheur SQL) : used_at ne peut jamais revenir à nil.
func (s *Store) PutDoc(ctx context.Context, actor, table, id string, v any) error {
	if !docTables[table] {
		return fmt.Errorf("table de documents inconnue %q", table)
	}
	raw, err := marshalDoc(table, v)
	if err != nil {
		return err
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO `+table+`(id,doc,updated_at,rev) VALUES(?,?,?,1)
			 ON CONFLICT(id) DO UPDATE SET doc=excluded.doc, updated_at=excluded.updated_at, rev=`+table+`.rev+1`,
			id, raw, FormatTime(s.now())); err != nil {
			return err
		}
		return s.AuditTx(ctx, tx, actor, table+".put", table, id, nil)
	})
}

// PutDocIfRev écrit le document seulement si sa version courante vaut
// expectedRev (0 = le document ne doit pas exister). Renvoie la nouvelle version
// ou ErrConflict.
func (s *Store) PutDocIfRev(ctx context.Context, actor, table, id string, v any, expectedRev int64) (int64, error) {
	if !docTables[table] {
		return 0, fmt.Errorf("table de documents inconnue %q", table)
	}
	raw, err := marshalDoc(table, v)
	if err != nil {
		return 0, err
	}
	newRev := expectedRev + 1
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		var res sql.Result
		var err error
		if expectedRev == 0 {
			res, err = tx.ExecContext(ctx,
				`INSERT INTO `+table+`(id,doc,updated_at,rev) VALUES(?,?,?,1) ON CONFLICT(id) DO NOTHING`,
				id, raw, FormatTime(s.now()))
		} else {
			res, err = tx.ExecContext(ctx,
				`UPDATE `+table+` SET doc=?, updated_at=?, rev=rev+1 WHERE id=? AND rev=?`,
				raw, FormatTime(s.now()), id, expectedRev)
		}
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("%s %s : %w", table, id, ErrConflict)
		}
		return s.AuditTx(ctx, tx, actor, table+".put", table, id, map[string]any{"rev": newRev})
	})
	if err != nil {
		return 0, err
	}
	return newRev, nil
}

func marshalDoc(table string, v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	if table == confirmationsTable {
		var probe struct {
			UsedAt *string `json:"used_at"`
		}
		if err := json.Unmarshal(raw, &probe); err == nil && probe.UsedAt != nil {
			return "", errors.New("une confirmation ne peut être marquée utilisée que par ConsumeConfirmation")
		}
	}
	return string(raw), nil
}

// GetDoc décode un document dans out.
func (s *Store) GetDoc(ctx context.Context, table, id string, out any) error {
	_, err := s.GetDocRev(ctx, table, id, out)
	return err
}

// GetDocRev décode un document et renvoie sa version (pour PutDocIfRev).
func (s *Store) GetDocRev(ctx context.Context, table, id string, out any) (int64, error) {
	if !docTables[table] {
		return 0, fmt.Errorf("table de documents inconnue %q", table)
	}
	var doc string
	var rev int64
	err := s.db.QueryRowContext(ctx, `SELECT doc, rev FROM `+table+` WHERE id=?`, id).Scan(&doc, &rev)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%s %s : %w", table, id, ErrNotFound)
	}
	if err != nil {
		return 0, err
	}
	return rev, json.Unmarshal([]byte(doc), out)
}

// ConsumeConfirmation consomme atomiquement (usage unique) une confirmation
// d'impression : transaction BEGIN IMMEDIATE, lecture, vérifications (jeton,
// expiration, liaison, print_job_id), puis UPDATE ... WHERE used_at IS NULL avec
// contrôle RowsAffected==1. Même sous forte concurrence, un seul appel réussit.
func (s *Store) ConsumeConfirmation(ctx context.Context, id, token string, binding contracts.ConfirmationBinding, printJobID string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var doc string
		var usedAt sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT doc, used_at FROM `+confirmationsTable+` WHERE id=?`, id).Scan(&doc, &usedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("confirmation %s : %w", id, ErrNotFound)
		}
		if err != nil {
			return err
		}
		var c contracts.PrintConfirmation
		if err := json.Unmarshal([]byte(doc), &c); err != nil {
			return err
		}
		if usedAt.Valid {
			return contracts.ErrConfirmationUsed
		}
		if c.PrintJobID != printJobID {
			return contracts.ErrConfirmationMismatch
		}
		now := s.Now()
		if err := c.Consume(token, binding, now); err != nil {
			return err
		}
		used := FormatTime(now)
		c.UsedAt = &now
		raw, err := json.Marshal(c)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE `+confirmationsTable+` SET used_at=?, doc=?, updated_at=?, rev=rev+1 WHERE id=? AND used_at IS NULL`,
			used, string(raw), used, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return contracts.ErrConfirmationUsed
		}
		return s.AuditTx(ctx, tx, "confirmation", "print_confirmation.consume", confirmationsTable, id, map[string]any{"print_job_id": printJobID})
	})
}
