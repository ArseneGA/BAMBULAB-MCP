package insights

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

// Verdict en un geste (G9, PJ11). Le verdict est le JUGEMENT DE L'HUMAIN sur une impression terminée (success, failed,
// redo) : il vit dans des colonnes additives de print_logs, n'écrase jamais Outcome (l'état technique) et n'a AUCUN
// effet sur l'imprimante, la file ni la confirmation de lancement. Aucune IA : le serveur n'invente jamais de verdict.

// Verdicts reconnus.
const (
	VerdictSuccess = "success"
	VerdictFailed  = "failed"
	VerdictRedo    = "redo"
)

// Sources d'un verdict.
const (
	SourceHumanCard = "human_card"
	SourceHumanWeb  = "human_web"
	SourceClaude    = "claude_on_request"
)

// Origines d'une impression.
const (
	OriginPrintalk = "printalk"
	OriginExternal = "external"
)

// FailReasons : vocabulaire fermé des raisons d'échec ; le détail libre va dans la note.
var FailReasons = []Defect{
	{"stringing", "Fils et toiles entre les parties"},
	{"warping", "Gauchissement : coins ou bords qui se soulèvent"},
	{"adhesion", "Mauvaise adhérence au plateau"},
	{"spaghetti", "Spaghetti : l'impression s'est détachée et a filé dans le vide"},
	{"layer_shift", "Décalage de couches"},
	{"under_extrusion", "Sous-extrusion : trous, couches maigres"},
	{"nozzle_clog", "Buse bouchée"},
	{"other", "Autre raison (préciser dans la note)"},
}

// MaxVerdictNote : longueur maximale (en caractères) de la note d'un verdict.
const MaxVerdictNote = 2000

func failReasonIDs() string {
	ids := make([]string, len(FailReasons))
	for i, r := range FailReasons {
		ids[i] = r.ID
	}
	return strings.Join(ids, ", ")
}

func validFailReason(id string) bool {
	for _, r := range FailReasons {
		if r.ID == id {
			return true
		}
	}
	return false
}

// clearTracking retire les champs G9 relus depuis les colonnes : ils ne sont jamais écrits dans le document de la fiche.
func (e *LogEntry) clearTracking() {
	e.Verdict, e.FailReason, e.VerdictAt, e.VerdictSource = "", "", time.Time{}, ""
	e.Origin, e.PartCount, e.Reached100 = "", 0, false
}

// attachTracking relit les colonnes de suivi (verdict, origine, 100 %) d'une fiche ; une base d'avant la migration 0300
// ou une erreur de lecture laissent la fiche telle quelle. « Atteint 100 % » se calcule aussi pour les anciennes fiches
// (état COMPLETED), sans rétro-remplissage.
func (s *Service) attachTracking(ctx context.Context, e *LogEntry) {
	var verdict, reason, at, src, origin string
	var parts, reached int
	err := s.cfg.Store.DB().QueryRowContext(ctx, `SELECT verdict, fail_reason, verdict_at, verdict_source, origin, part_count, reached_100 FROM print_logs WHERE id=?`, e.ID).
		Scan(&verdict, &reason, &at, &src, &origin, &parts, &reached)
	if err != nil {
		return
	}
	e.Verdict, e.FailReason, e.VerdictSource, e.Origin, e.PartCount = verdict, reason, src, origin, parts
	e.VerdictAt = time.Time{}
	if at != "" {
		e.VerdictAt, _ = store.ParseTime(at)
	}
	e.Reached100 = reached == 1 || e.State == "COMPLETED"
}

// SetVerdictInput : le verdict d'une impression terminée.
type SetVerdictInput struct {
	PrintJobID string `json:"print_job_id" jsonschema:"impression terminée à juger (identifiant de l'impression)"`
	Verdict    string `json:"verdict" jsonschema:"success (réussie) | failed (ratée) | redo (à refaire)"`
	Reason     string `json:"reason,omitempty" jsonschema:"raison de l'échec (failed ou redo) : stringing, warping, adhesion, spaghetti, layer_shift, under_extrusion, nozzle_clog, other"`
	Note       string `json:"note,omitempty" jsonschema:"détail libre (2000 caractères au plus) ; ne jamais y mettre de secret"`
	// Source : human_card (carte), human_web (page), claude_on_request (Claude, à la demande de l'utilisateur) ; défaut claude_on_request.
	Source string `json:"-"`
}

// VerdictOutput : le verdict enregistré.
type VerdictOutput struct {
	PrintJobID    string    `json:"print_job_id"`
	LogID         string    `json:"log_id"`
	Name          string    `json:"name,omitempty"`
	Verdict       string    `json:"verdict" jsonschema:"success | failed | redo"`
	FailReason    string    `json:"fail_reason,omitempty"`
	VerdictAt     time.Time `json:"verdict_at"`
	VerdictSource string    `json:"verdict_source"`
	Previous      string    `json:"previous_verdict,omitempty" jsonschema:"verdict avant cet appel (vide : pas encore jugée)"`
	Changed       bool      `json:"changed" jsonschema:"false : même verdict et même raison déjà enregistrés, rien n'a changé (appel idempotent)"`
	Warnings      []string  `json:"warnings,omitempty"`
	Note          string    `json:"note" jsonschema:"phrase de synthèse"`
	// FailReasons : vocabulaire fermé, pour la carte (jamais vide ; sert aussi à reconnaître la forme du résultat).
	FailReasons []Defect `json:"fail_reasons"`
}

// SetVerdict enregistre (ou modifie) le verdict d'une impression terminée. Idempotent : le même verdict avec la même
// raison ne change rien et n'ajoute aucune entrée d'audit ; un changement écrit une entrée d'audit.
func (s *Service) SetVerdict(ctx context.Context, in SetVerdictInput) (*VerdictOutput, error) {
	in.PrintJobID = strings.TrimSpace(in.PrintJobID)
	in.Verdict = strings.ToLower(strings.TrimSpace(in.Verdict))
	in.Reason = strings.ToLower(strings.TrimSpace(in.Reason))
	in.Note = strings.TrimSpace(in.Note)
	if in.PrintJobID == "" {
		return nil, invalid("print_job_id requis")
	}
	switch in.Verdict {
	case VerdictSuccess, VerdictFailed, VerdictRedo:
	default:
		return nil, invalid("verdict %q inconnu (success, failed, redo)", in.Verdict)
	}
	if in.Reason != "" && !validFailReason(in.Reason) {
		return nil, invalid("raison %q inconnue (%s)", in.Reason, failReasonIDs())
	}
	if utf8.RuneCountInString(in.Note) > MaxVerdictNote {
		return nil, invalid("note : %d caractères au plus", MaxVerdictNote)
	}
	switch in.Source {
	case "":
		in.Source = SourceClaude
	case SourceHumanCard, SourceHumanWeb, SourceClaude:
	default:
		return nil, invalid("source %q inconnue", in.Source)
	}
	var warns []string
	reason := in.Reason
	switch {
	case in.Verdict == VerdictSuccess && reason != "":
		warns = append(warns, "Une impression réussie n'a pas de raison d'échec : la raison est ignorée.")
		reason = ""
	case in.Verdict == VerdictFailed && reason == "":
		reason = "other"
		warns = append(warns, "Raison d'échec non précisée : enregistrée « other » ; la préciser avec reason ("+failReasonIDs()+") et note.")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.syncLocked(ctx); err != nil {
		return nil, err
	}
	var logID, prevVerdict, prevReason, doc string
	err := s.cfg.Store.DB().QueryRowContext(ctx, `SELECT id, verdict, fail_reason, doc FROM print_logs WHERE print_job_id=?`, in.PrintJobID).Scan(&logID, &prevVerdict, &prevReason, &doc)
	if errors.Is(err, sql.ErrNoRows) {
		j, jerr := s.loadJob(ctx, in.PrintJobID)
		if jerr != nil {
			return nil, invalid("impression %q inconnue : aucun verdict possible", in.PrintJobID)
		}
		return nil, invalid("l'impression %q n'est pas terminée (état %s) : le verdict se donne une fois l'impression finie", in.PrintJobID, j.State)
	}
	if err != nil {
		return nil, err
	}
	var e LogEntry
	_ = json.Unmarshal([]byte(doc), &e)
	out := &VerdictOutput{PrintJobID: in.PrintJobID, LogID: logID, Name: e.Name, Verdict: in.Verdict, FailReason: reason,
		VerdictSource: in.Source, Previous: prevVerdict, Warnings: warns, FailReasons: FailReasons}
	now := s.now()
	changed := prevVerdict != in.Verdict || prevReason != reason
	if !changed && in.Note == "" {
		var at, src string
		_ = s.cfg.Store.DB().QueryRowContext(ctx, `SELECT verdict_at, verdict_source FROM print_logs WHERE id=?`, logID).Scan(&at, &src)
		out.VerdictAt, _ = store.ParseTime(at)
		out.VerdictSource = src
		out.Note = "Verdict déjà enregistré à l'identique (" + verdictLabel(in.Verdict) + ") : rien n'a changé."
		return out, nil
	}
	out.Changed = changed
	out.VerdictAt = now
	err = s.cfg.Store.Tx(ctx, func(tx *sql.Tx) error {
		if changed {
			if _, err := tx.ExecContext(ctx, `UPDATE print_logs SET verdict=?, fail_reason=?, verdict_at=?, verdict_source=?, updated_at=? WHERE id=?`,
				in.Verdict, reason, store.FormatTime(now), in.Source, store.FormatTime(now), logID); err != nil {
				return err
			}
			detail := map[string]any{"print_job": in.PrintJobID, "verdict": in.Verdict, "previous": prevVerdict, "source": in.Source, "note_set": in.Note != ""}
			if reason != "" {
				detail["reason"] = reason
			}
			if err := s.cfg.Store.AuditTx(ctx, tx, actor, "insights.verdict.set", "print_log", logID, detail); err != nil {
				return err
			}
		}
		if in.Note != "" {
			return s.putVerdictNoteTx(ctx, tx, in.PrintJobID, in.Note, now, logID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if changed {
		out.Note = "Verdict enregistré : " + verdictLabel(in.Verdict)
		if reason != "" {
			out.Note += " (" + reason + ")"
		}
		out.Note += ". Il n'a aucun effet sur l'imprimante ni sur la file."
	} else {
		var at, src string
		_ = s.cfg.Store.DB().QueryRowContext(ctx, `SELECT verdict_at, verdict_source FROM print_logs WHERE id=?`, logID).Scan(&at, &src)
		out.VerdictAt, _ = store.ParseTime(at)
		out.VerdictSource = src
		out.Note = "Verdict inchangé (" + verdictLabel(in.Verdict) + ") ; note enregistrée."
	}
	return out, nil
}

// putVerdictNoteTx range la note d'un verdict dans entity_notes (étiquette « verdict », une seule note par impression).
// La note n'est jamais copiée dans l'audit ni dans les journaux applicatifs.
func (s *Service) putVerdictNoteTx(ctx context.Context, tx *sql.Tx, printJobID, text string, now time.Time, logID string) error {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM entity_notes WHERE kind='print' AND entity_id=? AND tags='["verdict"]' ORDER BY created_at LIMIT 1`, printJobID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `INSERT INTO entity_notes(id,kind,entity_id,text,tags,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
			contracts.NewID("note"), "print", printJobID, text, `["verdict"]`, store.FormatTime(now), store.FormatTime(now))
		return err
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE entity_notes SET text=?, updated_at=? WHERE id=?`, text, store.FormatTime(now), id)
	return err
}

// VerdictNote relit la note d'un verdict ("" sans note).
func (s *Service) VerdictNote(ctx context.Context, printJobID string) string {
	var t string
	_ = s.cfg.Store.DB().QueryRowContext(ctx, `SELECT text FROM entity_notes WHERE kind='print' AND entity_id=? AND tags='["verdict"]' ORDER BY created_at LIMIT 1`, printJobID).Scan(&t)
	return t
}

func verdictLabel(v string) string {
	switch v {
	case VerdictSuccess:
		return "réussie"
	case VerdictFailed:
		return "ratée"
	case VerdictRedo:
		return "à refaire"
	}
	return "non jugée"
}

// VerdictLabel : libellé français d'un verdict (« non jugée » quand vide).
func VerdictLabel(v string) string { return verdictLabel(v) }

// PendingVerdict : une impression terminée qui attend le jugement de l'humain.
type PendingVerdict struct {
	PrintJobID     string    `json:"print_job_id"`
	LogID          string    `json:"log_id"`
	Name           string    `json:"name,omitempty"`
	ModelID        string    `json:"model_id,omitempty"`
	ModelName      string    `json:"model_name,omitempty"`
	PrinterID      string    `json:"printer_id,omitempty"`
	State          string    `json:"state" jsonschema:"COMPLETED | FAILED"`
	Reached100     bool      `json:"reached_100"`
	MaterialFamily string    `json:"material_family,omitempty"`
	FinishedAt     time.Time `json:"finished_at"`
}

// PendingOutput : impressions terminées sans verdict, les plus récentes d'abord.
type PendingOutput struct {
	Pending     []PendingVerdict `json:"pending"`
	Total       int              `json:"total" jsonschema:"nombre total d'impressions terminées sans verdict"`
	FailReasons []Defect         `json:"fail_reasons"`
	Note        string           `json:"note"`
}

// PendingVerdicts liste les impressions terminées (COMPLETED ou FAILED) sans verdict, par date de fin décroissante.
// Une impression annulée volontairement n'attend pas de verdict (elle peut en recevoir un avec SetVerdict).
func (s *Service) PendingVerdicts(ctx context.Context, limit int) (*PendingOutput, error) {
	if _, err := s.SyncJournal(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	out := &PendingOutput{Pending: []PendingVerdict{}, FailReasons: FailReasons}
	const where = `verdict='' AND json_extract(doc,'$.state') IN ('COMPLETED','FAILED')`
	if err := s.cfg.Store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM print_logs WHERE `+where).Scan(&out.Total); err != nil {
		return nil, err
	}
	rows, err := s.cfg.Store.DB().QueryContext(ctx, `SELECT id, print_job_id, model_id, printer_id, material_family, finished_at, reached_100, doc FROM print_logs WHERE `+where+` ORDER BY finished_at DESC, id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p PendingVerdict
		var fin, doc string
		var reached int
		if err := rows.Scan(&p.LogID, &p.PrintJobID, &p.ModelID, &p.PrinterID, &p.MaterialFamily, &fin, &reached, &doc); err != nil {
			rows.Close()
			return nil, err
		}
		p.FinishedAt, _ = store.ParseTime(fin)
		var e LogEntry
		if json.Unmarshal([]byte(doc), &e) == nil {
			p.Name, p.ModelName, p.State = e.Name, e.ModelName, e.State
		}
		p.Reached100 = reached == 1 || p.State == "COMPLETED"
		out.Pending = append(out.Pending, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	switch {
	case out.Total == 0:
		out.Note = "Aucune impression terminée n'attend de verdict."
	case out.Total > len(out.Pending):
		out.Note = fmt.Sprintf("%d impression(s) terminée(s) sans verdict (%d affichées).", out.Total, len(out.Pending))
	default:
		out.Note = fmt.Sprintf("%d impression(s) terminée(s) sans verdict.", out.Total)
	}
	return out, nil
}
