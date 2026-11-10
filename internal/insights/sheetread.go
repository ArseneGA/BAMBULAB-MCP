package insights

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// MaxLogsForModel borne la lecture du journal d'une pièce (fiche pièce, PJ5).
const MaxLogsForModel = 2000

// LogsForModel renvoie les fiches de journal d'une pièce, les plus récentes d'abord, avec les colonnes de suivi G9
// (verdict, raison, origine, nombre d'exemplaires, 100 % atteint) relues en UNE requête. Le journal est d'abord
// synchronisé. Aucune photo de caméra n'est rattachée (la fiche les lit à part). Un modèle sans impression : liste vide.
func (s *Service) LogsForModel(ctx context.Context, modelID string) ([]LogEntry, error) {
	if _, err := s.SyncJournal(ctx); err != nil {
		return nil, err
	}
	rows, err := s.cfg.Store.DB().QueryContext(ctx, `SELECT doc, verdict, fail_reason, verdict_at, verdict_source, origin, part_count, reached_100
		FROM print_logs WHERE model_id=? ORDER BY finished_at DESC, id LIMIT ?`, modelID, MaxLogsForModel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogEntry{}
	for rows.Next() {
		var doc, verdict, reason, at, src, origin string
		var parts, reached int
		if err := rows.Scan(&doc, &verdict, &reason, &at, &src, &origin, &parts, &reached); err != nil {
			return nil, err
		}
		var e LogEntry
		if json.Unmarshal([]byte(doc), &e) != nil {
			continue
		}
		e.Verdict, e.FailReason, e.VerdictSource, e.Origin, e.PartCount = verdict, reason, src, origin, parts
		e.VerdictAt = time.Time{}
		if at != "" {
			e.VerdictAt, _ = store.ParseTime(at)
		}
		e.Reached100 = reached == 1 || e.State == "COMPLETED"
		out = append(out, e)
	}
	return out, rows.Err()
}

// IsPrinted : la fiche compte « imprimée » (D7 : 100 % atteint, verdict ou pas).
func (e LogEntry) IsPrinted() bool { return e.Reached100 }

// IsExternal : impression déclarée hors Printalk.
func (e LogEntry) IsExternal() bool { return e.Origin == OriginExternal }
