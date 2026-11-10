package insights

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// ClearedVerdict : réponse de ClearVerdict.
type ClearedVerdict struct {
	PrintJobID string `json:"print_job_id"`
	Verdict    string `json:"verdict"`
	Changed    bool   `json:"changed" jsonschema:"false : l'impression n'avait pas de verdict, rien n'a changé"`
	Note       string `json:"note"`
}

// ClearVerdict retire le verdict d'une impression (elle redevient « non jugée »). Sert au bouton « Annuler » de l'interface
// (8 s après un tap sur l'Accueil, G10 UX6). Écrit une entrée d'audit ; la note éventuelle du verdict est conservée ; aucun effet
// sur l'imprimante, la file ni le compte « imprimée » (D7 : 100 % atteint, verdict ou pas). Idempotent.
func (s *Service) ClearVerdict(ctx context.Context, printJobID string) (*ClearedVerdict, error) {
	printJobID = strings.TrimSpace(printJobID)
	if printJobID == "" {
		return nil, invalid("print_job_id requis")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.syncLocked(ctx); err != nil {
		return nil, err
	}
	var logID, prev string
	err := s.cfg.Store.DB().QueryRowContext(ctx, `SELECT id, verdict FROM print_logs WHERE print_job_id=?`, printJobID).Scan(&logID, &prev)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, invalid("impression %q inconnue : aucun verdict à retirer", printJobID)
	}
	if err != nil {
		return nil, err
	}
	out := &ClearedVerdict{PrintJobID: printJobID}
	if prev == "" {
		out.Note = "Cette impression n'avait pas de verdict : rien n'a changé."
		return out, nil
	}
	now := s.now()
	err = s.cfg.Store.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE print_logs SET verdict='', fail_reason='', verdict_at='', verdict_source='', updated_at=? WHERE id=?`, store.FormatTime(now), logID); err != nil {
			return err
		}
		return s.cfg.Store.AuditTx(ctx, tx, actor, "insights.verdict.cleared", "print_log", logID, map[string]any{"print_job": printJobID, "previous": prev})
	})
	if err != nil {
		return nil, err
	}
	out.Changed = true
	out.Note = "Verdict retiré : l'impression est de nouveau non jugée. Aucun effet sur l'imprimante ni sur la file."
	return out, nil
}
