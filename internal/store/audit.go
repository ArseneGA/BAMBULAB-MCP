package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// AuditEntry est une ligne du journal d'audit.
type AuditEntry struct {
	Seq        int64           `json:"seq"`
	At         time.Time       `json:"at"`
	Actor      string          `json:"actor"`
	Operation  string          `json:"operation"`
	EntityType string          `json:"entity_type"`
	EntityID   string          `json:"entity_id"`
	Detail     json.RawMessage `json:"detail,omitempty"`
}

// AuditTx ajoute une entrée dans la transaction en cours (atomique avec la mutation).
func (s *Store) AuditTx(ctx context.Context, tx *sql.Tx, actor, operation, entityType, entityID string, detail any) error {
	var d any
	if detail != nil {
		raw, err := json.Marshal(detail)
		if err != nil {
			return err
		}
		d = string(raw)
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO audit_log(at,actor,operation,entity_type,entity_id,detail) VALUES(?,?,?,?,?,?)`,
		FormatTime(s.now()), actor, operation, entityType, entityID, d)
	return err
}

// Audit ajoute une entrée hors de toute autre mutation.
func (s *Store) Audit(ctx context.Context, actor, operation, entityType, entityID string, detail any) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		return s.AuditTx(ctx, tx, actor, operation, entityType, entityID, detail)
	})
}

// ListAudit renvoie les entrées d'une entité (ou toutes si entityType vide), les plus récentes d'abord.
func (s *Store) ListAudit(ctx context.Context, entityType, entityID string, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	q := `SELECT seq,at,actor,operation,entity_type,entity_id,detail FROM audit_log`
	var args []any
	if entityType != "" {
		q += ` WHERE entity_type=? AND entity_id=?`
		args = append(args, entityType, entityID)
	}
	q += ` ORDER BY seq DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var at string
		var d sql.NullString
		if err := rows.Scan(&e.Seq, &at, &e.Actor, &e.Operation, &e.EntityType, &e.EntityID, &d); err != nil {
			return nil, err
		}
		e.At, _ = ParseTime(at)
		if d.Valid {
			e.Detail = json.RawMessage(d.String)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
