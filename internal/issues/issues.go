// Package issues tient le carnet de bord des dysfonctionnements : l'agent (Claude, ou un humain depuis la page web)
// y consigne ce qu'il constate d'anormal dans Printalk (outil qui refuse à tort, réponse incohérente, fausse alerte,
// donnée périmée, réponse trop lourde), le serveur RANGE, dédoublonne et filtre les secrets, et un export Markdown
// se colle à l'agent qui code les correctifs.
//
// Aucune API d'IA : le serveur ne rédige rien, il stocke ce qu'on lui dit. Aucune fonction du paquet ne lance, n'arrête
// ni ne reprend d'impression ; il n'écrit que dans sa table `issues`.
package issues

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// ErrInvalid : entrée invalide (traduite en 400 / erreur d'outil).
var ErrInvalid = errors.New("entrée invalide")

// ErrNotFound : constat introuvable.
var ErrNotFound = store.ErrNotFound

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w : %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Gravités : P0 bloquant ou sécurité, P1 grave, P2 gênant (défaut), P3 détail.
var Severities = []string{"P0", "P1", "P2", "P3"}

// États : new -> seen -> fixed (avec référence de commit) | dismissed.
const (
	StateNew       = "new"
	StateSeen      = "seen"
	StateFixed     = "fixed"
	StateDismissed = "dismissed"
)

// States liste les états connus.
var States = []string{StateNew, StateSeen, StateFixed, StateDismissed}

// Bornes de taille (en caractères) : un constat est court, pas un journal complet.
const (
	maxTitle = 160
	maxTool  = 80
	maxText  = 2000
	maxID    = 80
	maxList  = 200
)

const actor = "issues"

// Issue est un constat du carnet.
type Issue struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Severity    string    `json:"severity" jsonschema:"P0 | P1 | P2 | P3"`
	Tool        string    `json:"tool,omitempty" jsonschema:"outil Printalk concerné"`
	Expected    string    `json:"expected,omitempty"`
	Observed    string    `json:"observed,omitempty"`
	PrintJobID  string    `json:"print_job_id,omitempty"`
	PrinterID   string    `json:"printer_id,omitempty"`
	ModelID     string    `json:"model_id,omitempty"`
	Occurrences int       `json:"occurrences" jsonschema:"nombre de fois où ce même symptôme a été signalé"`
	Regressions int       `json:"regressions,omitempty" jsonschema:"fois où un constat déjà corrigé est revenu"`
	State       string    `json:"state" jsonschema:"new | seen | fixed | dismissed"`
	FixCommit   string    `json:"fix_commit,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ReportInput : ce que l'agent consigne.
type ReportInput struct {
	Title      string `json:"title" jsonschema:"le symptôme en une phrase courte, sans identifiant ni secret (sert au dédoublonnage)"`
	Severity   string `json:"severity,omitempty" jsonschema:"P0 bloquant ou sécurité | P1 grave | P2 gênant (défaut) | P3 détail"`
	Tool       string `json:"tool,omitempty" jsonschema:"nom de l'outil MCP (ou de la page) concerné, ex. get_print_status"`
	Expected   string `json:"expected,omitempty" jsonschema:"ce qui était attendu"`
	Observed   string `json:"observed,omitempty" jsonschema:"ce qui s'est réellement passé (message d'erreur, valeur incohérente...)"`
	PrintJobID string `json:"print_job_id,omitempty" jsonschema:"impression concernée, si elle existe"`
	PrinterID  string `json:"printer_id,omitempty" jsonschema:"imprimante concernée, si elle existe"`
	ModelID    string `json:"model_id,omitempty" jsonschema:"modèle concerné, si il existe"`
}

// ReportOutput : le constat (créé ou déjà connu).
type ReportOutput struct {
	Issue    Issue  `json:"issue"`
	Created  bool   `json:"created" jsonschema:"true : nouvelle ligne ; false : symptôme déjà connu, compteur incrémenté"`
	Reopened bool   `json:"reopened,omitempty" jsonschema:"true : un constat déjà corrigé est revenu (régression)"`
	Redacted bool   `json:"redacted,omitempty" jsonschema:"true : des éléments sensibles ont été masqués avant stockage"`
	Note     string `json:"note"`
}

// ListQuery : filtres de la liste.
type ListQuery struct {
	State    string `json:"state,omitempty" jsonschema:"open (défaut : new + seen) | new | seen | fixed | dismissed | all"`
	Severity string `json:"severity,omitempty" jsonschema:"P0 | P1 | P2 | P3 (une seule gravité)"`
	Tool     string `json:"tool,omitempty" jsonschema:"restreindre à un outil"`
	Limit    int    `json:"limit,omitempty" jsonschema:"nombre maximal de lignes (défaut 50, max 200)"`
	Offset   int    `json:"offset,omitempty"`
}

// ListOutput : les constats, les plus graves puis les plus fréquents d'abord.
type ListOutput struct {
	Issues []Issue        `json:"issues"`
	Total  int            `json:"total" jsonschema:"nombre de constats correspondant aux filtres"`
	Counts map[string]int `json:"counts" jsonschema:"nombre de constats par état (tous filtres levés)"`
	Note   string         `json:"note,omitempty"`
}

// UpdateInput : changement d'état (et de gravité) d'un constat.
type UpdateInput struct {
	ID        string `json:"id" jsonschema:"identifiant du constat (iss_...)"`
	State     string `json:"state,omitempty" jsonschema:"new | seen | fixed | dismissed"`
	FixCommit string `json:"fix_commit,omitempty" jsonschema:"référence du commit du correctif (obligatoire pour fixed)"`
	Severity  string `json:"severity,omitempty" jsonschema:"corriger la gravité : P0 | P1 | P2 | P3"`
}

// Service est le point d'entrée du paquet.
type Service struct {
	st  *store.Store
	now func() time.Time
}

// New construit le service (la base doit être migrée : table issues, migration 0290).
func New(st *store.Store) (*Service, error) {
	if st == nil {
		return nil, errors.New("issues : magasin requis")
	}
	return &Service{st: st, now: st.Now}, nil
}

// ---- normalisation ----

var (
	reID      = regexp.MustCompile(`\b[a-z]{2,6}_[0-9a-f]{6,}\b`)
	reNumber  = regexp.MustCompile(`\d+([.,]\d+)?`)
	reNotWord = regexp.MustCompile(`[^a-z0-9#]+`)
	accents   = strings.NewReplacer("à", "a", "â", "a", "ä", "a", "é", "e", "è", "e", "ê", "e", "ë", "e", "î", "i", "ï", "i",
		"ô", "o", "ö", "o", "ù", "u", "û", "u", "ü", "u", "ç", "c", "œ", "oe", "æ", "ae", "’", "'")
)

// Fingerprint donne la clé de dédoublonnage : l'outil et le titre normalisé (minuscules, sans accents, identifiants
// et nombres remplacés par « # », ponctuation et espaces réduits). « Le job pj_3f9a… reste à 42 % » et « le job
// pj_77b0… reste à 87 % » sont donc le même symptôme.
func Fingerprint(tool, title string) string {
	return normalize(tool) + "|" + normalize(title)
}

func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = reID.ReplaceAllString(s, "#")
	s = accents.Replace(s)
	s = reNumber.ReplaceAllString(s, "#")
	s = reNotWord.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

// clean filtre les secrets d'un texte libre, retire les caractères de contrôle et borne la taille.
func clean(s string, max int, redacted *bool) string {
	s = strings.TrimSpace(s)
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	r := contracts.RedactText(s)
	if r != s && redacted != nil {
		*redacted = true
	}
	if utf8.RuneCountInString(r) > max {
		r = string([]rune(r)[:max-1]) + "…"
	}
	return r
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

var reCommit = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/@#-]{1,99}$`)

func validSeverity(s string) bool {
	for _, v := range Severities {
		if s == v {
			return true
		}
	}
	return false
}

func validState(s string) bool {
	for _, v := range States {
		if s == v {
			return true
		}
	}
	return false
}

// severityRank : P0 < P1 < P2 < P3 (le plus petit est le plus grave).
func severityRank(s string) int {
	for i, v := range Severities {
		if s == v {
			return i
		}
	}
	return len(Severities)
}

// ---- écriture ----

// Report consigne un constat. Même outil + même titre normalisé : le compteur de la ligne existante est incrémenté
// (la gravité la plus haute est conservée, les identifiants manquants sont complétés) ; un constat déjà « fixed » qui
// revient repasse à « new » avec une régression comptée ; un constat « dismissed » reste écarté (le compteur monte).
func (s *Service) Report(ctx context.Context, in ReportInput) (*ReportOutput, error) {
	var redacted bool
	title := oneLine(clean(in.Title, maxTitle, &redacted))
	if title == "" {
		return nil, invalid("title requis : le symptôme en une phrase")
	}
	sev := strings.ToUpper(strings.TrimSpace(in.Severity))
	if sev == "" {
		sev = "P2"
	}
	if !validSeverity(sev) {
		return nil, invalid("severity %q inconnue (P0, P1, P2 ou P3)", in.Severity)
	}
	tool := oneLine(clean(in.Tool, maxTool, &redacted))
	expected := clean(in.Expected, maxText, &redacted)
	observed := clean(in.Observed, maxText, &redacted)
	jobID := oneLine(clean(in.PrintJobID, maxID, &redacted))
	printerID := oneLine(clean(in.PrinterID, maxID, &redacted))
	modelID := oneLine(clean(in.ModelID, maxID, &redacted))
	fp := Fingerprint(tool, title)
	now := s.now()
	ts := store.FormatTime(now)

	var out ReportOutput
	err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		cur, err := scanOne(tx.QueryRowContext(ctx, selectIssue+` WHERE fingerprint=?`, fp))
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if errors.Is(err, ErrNotFound) {
			id := contracts.NewID("iss")
			if _, err := tx.ExecContext(ctx, `INSERT INTO issues(id,fingerprint,title,severity,tool,expected,observed,print_job_id,printer_id,model_id,occurrences,regressions,state,fix_commit,created_at,last_seen_at,updated_at)
				VALUES(?,?,?,?,?,?,?,?,?,?,1,0,'new','',?,?,?)`, id, fp, title, sev, tool, expected, observed, jobID, printerID, modelID, ts, ts, ts); err != nil {
				return err
			}
			out.Created = true
			cur = &Issue{ID: id}
			if err := s.st.AuditTx(ctx, tx, actor, "issues.report", "issue", id, map[string]any{"severity": sev, "tool": tool}); err != nil {
				return err
			}
		} else {
			state, regress := cur.State, cur.Regressions
			if cur.State == StateFixed {
				state, regress = StateNew, regress+1 // le correctif n'a pas tenu
				out.Reopened = true
			}
			if severityRank(sev) < severityRank(cur.Severity) {
				cur.Severity = sev
			}
			pick := func(old, neu string) string {
				if old == "" {
					return neu
				}
				return old
			}
			if _, err := tx.ExecContext(ctx, `UPDATE issues SET severity=?, expected=?, observed=?, print_job_id=?, printer_id=?, model_id=?,
				occurrences=occurrences+1, regressions=?, state=?, last_seen_at=?, updated_at=? WHERE id=?`,
				cur.Severity, pick(cur.Expected, expected), pick(cur.Observed, observed), pick(cur.PrintJobID, jobID), pick(cur.PrinterID, printerID),
				pick(cur.ModelID, modelID), regress, state, ts, ts, cur.ID); err != nil {
				return err
			}
			if err := s.st.AuditTx(ctx, tx, actor, "issues.repeat", "issue", cur.ID, map[string]any{"occurrences": cur.Occurrences + 1, "reopened": out.Reopened}); err != nil {
				return err
			}
		}
		got, err := scanOne(tx.QueryRowContext(ctx, selectIssue+` WHERE id=?`, cur.ID))
		if err != nil {
			return err
		}
		out.Issue = *got
		return nil
	})
	if err != nil {
		return nil, err
	}
	out.Redacted = redacted
	switch {
	case out.Created:
		out.Note = "Constat consigné (" + out.Issue.ID + ")."
	case out.Reopened:
		out.Note = fmt.Sprintf("Ce symptôme avait été corrigé (commit %s) et revient : constat rouvert, %d occurrence(s).", out.Issue.FixCommit, out.Issue.Occurrences)
	default:
		out.Note = fmt.Sprintf("Symptôme déjà connu (%s) : %d occurrence(s), aucune ligne ajoutée.", out.Issue.ID, out.Issue.Occurrences)
	}
	if redacted {
		out.Note += " Des éléments sensibles ont été masqués avant stockage."
	}
	return &out, nil
}

// Update change l'état d'un constat (et la référence du commit pour « fixed »), ou sa gravité.
func (s *Service) Update(ctx context.Context, in UpdateInput) (*Issue, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return nil, invalid("id requis")
	}
	state := strings.ToLower(strings.TrimSpace(in.State))
	sev := strings.ToUpper(strings.TrimSpace(in.Severity))
	if state == "" && sev == "" {
		return nil, invalid("rien à changer : state ou severity")
	}
	if state != "" && !validState(state) {
		return nil, invalid("state %q inconnu (new, seen, fixed ou dismissed)", in.State)
	}
	if sev != "" && !validSeverity(sev) {
		return nil, invalid("severity %q inconnue (P0, P1, P2 ou P3)", in.Severity)
	}
	commit := strings.TrimSpace(in.FixCommit)
	if commit != "" {
		if c := contracts.RedactText(commit); c != commit || !reCommit.MatchString(commit) {
			return nil, invalid("fix_commit : référence de commit attendue (empreinte ou nom de branche, sans espace ni secret)")
		}
	}
	if state == StateFixed && commit == "" {
		return nil, invalid("fix_commit requis pour passer à fixed (la référence du commit qui corrige)")
	}
	if commit != "" && state != "" && state != StateFixed {
		return nil, invalid("fix_commit n'a de sens que pour l'état fixed")
	}
	var out *Issue
	err := s.st.Tx(ctx, func(tx *sql.Tx) error {
		cur, err := scanOne(tx.QueryRowContext(ctx, selectIssue+` WHERE id=?`, id))
		if err != nil {
			return err
		}
		if state == "" && commit != "" { // référence seule : seulement sur un constat déjà corrigé
			if cur.State != StateFixed {
				return invalid("fix_commit n'a de sens que pour l'état fixed")
			}
			state = StateFixed
		}
		newState, newCommit, newSev := cur.State, cur.FixCommit, cur.Severity
		if state != "" {
			newState = state
			switch state {
			case StateFixed:
				newCommit = commit
			default:
				newCommit = ""
			}
		}
		if sev != "" {
			newSev = sev
		}
		if _, err := tx.ExecContext(ctx, `UPDATE issues SET state=?, fix_commit=?, severity=?, updated_at=? WHERE id=?`,
			newState, newCommit, newSev, store.FormatTime(s.now()), id); err != nil {
			return err
		}
		if err := s.st.AuditTx(ctx, tx, actor, "issues.update", "issue", id, map[string]any{"from": cur.State, "to": newState, "severity": newSev}); err != nil {
			return err
		}
		out, err = scanOne(tx.QueryRowContext(ctx, selectIssue+` WHERE id=?`, id))
		return err
	})
	return out, err
}

// ---- lecture ----

const selectIssue = `SELECT id,title,severity,tool,expected,observed,print_job_id,printer_id,model_id,occurrences,regressions,state,fix_commit,created_at,last_seen_at,updated_at FROM issues`

type rowScanner interface{ Scan(dest ...any) error }

func scanRow(r rowScanner) (*Issue, error) {
	var i Issue
	var c, l, u string
	if err := r.Scan(&i.ID, &i.Title, &i.Severity, &i.Tool, &i.Expected, &i.Observed, &i.PrintJobID, &i.PrinterID, &i.ModelID,
		&i.Occurrences, &i.Regressions, &i.State, &i.FixCommit, &c, &l, &u); err != nil {
		return nil, err
	}
	i.CreatedAt, _ = store.ParseTime(c)
	i.LastSeenAt, _ = store.ParseTime(l)
	i.UpdatedAt, _ = store.ParseTime(u)
	return &i, nil
}

func scanOne(r *sql.Row) (*Issue, error) {
	i, err := scanRow(r)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return i, err
}

// Get lit un constat.
func (s *Service) Get(ctx context.Context, id string) (*Issue, error) {
	return scanOne(s.st.DB().QueryRowContext(ctx, selectIssue+` WHERE id=?`, strings.TrimSpace(id)))
}

// orderBy : les plus graves d'abord, puis les plus fréquents, puis les plus récents.
const orderBy = ` ORDER BY severity ASC, occurrences DESC, last_seen_at DESC`

func (q *ListQuery) where() (string, []any, error) {
	var conds []string
	var args []any
	switch st := strings.ToLower(strings.TrimSpace(q.State)); st {
	case "", "open":
		conds = append(conds, `state IN ('new','seen')`)
	case "all":
	default:
		if !validState(st) {
			return "", nil, invalid("state %q inconnu (open, new, seen, fixed, dismissed ou all)", q.State)
		}
		conds = append(conds, `state=?`)
		args = append(args, st)
	}
	if sv := strings.ToUpper(strings.TrimSpace(q.Severity)); sv != "" {
		if !validSeverity(sv) {
			return "", nil, invalid("severity %q inconnue (P0, P1, P2 ou P3)", q.Severity)
		}
		conds = append(conds, `severity=?`)
		args = append(args, sv)
	}
	if t := strings.TrimSpace(q.Tool); t != "" {
		conds = append(conds, `tool=?`)
		args = append(args, t)
	}
	if len(conds) == 0 {
		return "", args, nil
	}
	return ` WHERE ` + strings.Join(conds, ` AND `), args, nil
}

// List renvoie les constats filtrés (par défaut : ouverts, c'est-à-dire new + seen).
func (s *Service) List(ctx context.Context, q ListQuery) (*ListOutput, error) {
	where, args, err := q.where()
	if err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > maxList {
		limit = maxList
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	out := &ListOutput{Issues: []Issue{}, Counts: map[string]int{}}
	db := s.st.DB()
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM issues`+where, args...).Scan(&out.Total); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, selectIssue+where+orderBy+` LIMIT ? OFFSET ?`, append(append([]any{}, args...), limit, q.Offset)...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		i, err := scanRow(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out.Issues = append(out.Issues, *i)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	crow, err := db.QueryContext(ctx, `SELECT state, COUNT(*) FROM issues GROUP BY state`)
	if err != nil {
		return nil, err
	}
	defer crow.Close()
	for _, st := range States {
		out.Counts[st] = 0
	}
	for crow.Next() {
		var st string
		var n int
		if err := crow.Scan(&st, &n); err != nil {
			return nil, err
		}
		out.Counts[st] = n
	}
	return out, crow.Err()
}

// OpenCounts renvoie le nombre de constats ouverts (new + seen) et, parmi eux, de P0/P1 (pour un indicateur).
func (s *Service) OpenCounts(ctx context.Context) (open, urgent int, err error) {
	err = s.st.DB().QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN severity IN ('P0','P1') THEN 1 ELSE 0 END),0) FROM issues WHERE state IN ('new','seen')`).Scan(&open, &urgent)
	return
}

// ---- export Markdown ----

// ExportMarkdown rend le carnet (constats ouverts par défaut, filtres de ListQuery) sous forme de Markdown à coller à
// l'agent qui code les correctifs. Les textes ont été filtrés des secrets à l'écriture.
func (s *Service) ExportMarkdown(ctx context.Context, q ListQuery) (string, int, error) {
	q.Limit, q.Offset = maxList, 0
	l, err := s.List(ctx, q)
	if err != nil {
		return "", 0, err
	}
	return RenderMarkdown(l.Issues, q, l.Total, s.now()), len(l.Issues), nil
}

// RenderMarkdown met en forme des constats (fonction pure, testée).
func RenderMarkdown(list []Issue, q ListQuery, total int, now time.Time) string {
	var b strings.Builder
	state := strings.ToLower(strings.TrimSpace(q.State))
	if state == "" {
		state = "open"
	}
	fmt.Fprintf(&b, "# Dysfonctionnements de Printalk à corriger\n\n")
	fmt.Fprintf(&b, "Export du %s : %d constat(s) (état : %s", now.Local().Format("02/01/2006 15:04"), len(list), state)
	if q.Severity != "" {
		fmt.Fprintf(&b, ", gravité %s", strings.ToUpper(q.Severity))
	}
	if q.Tool != "" {
		fmt.Fprintf(&b, ", outil %s", q.Tool)
	}
	b.WriteString(").\n\n")
	b.WriteString("Consigne à l'agent : pour chaque constat, reproduis le symptôme, corrige la cause (pas le symptôme), ajoute un test qui échouait avant le correctif, puis marque le constat corrigé avec `update_issue` (state=fixed, fix_commit=<commit>). Gravités : P0 bloquant ou sécurité, P1 grave, P2 gênant, P3 détail. Les secrets ont été retirés des textes.\n\n")
	if len(list) == 0 {
		b.WriteString("_Aucun constat ne correspond._\n")
		return b.String()
	}
	if total > len(list) {
		fmt.Fprintf(&b, "_%d constat(s) de plus ne sont pas listés (limite de l'export)._\n\n", total-len(list))
	}
	for _, sev := range Severities {
		first := true
		for _, i := range list {
			if i.Severity != sev {
				continue
			}
			if first {
				fmt.Fprintf(&b, "## %s\n\n", sev)
				first = false
			}
			fmt.Fprintf(&b, "### %s `%s`\n\n", i.Title, i.ID)
			if i.Tool != "" {
				fmt.Fprintf(&b, "- Outil : `%s`\n", i.Tool)
			}
			fmt.Fprintf(&b, "- État : %s", StateLabel(i.State))
			if i.FixCommit != "" {
				fmt.Fprintf(&b, " (commit %s)", i.FixCommit)
			}
			b.WriteString("\n")
			fmt.Fprintf(&b, "- Occurrences : %d (dernière : %s)", i.Occurrences, i.LastSeenAt.Local().Format("02/01/2006 15:04"))
			if i.Regressions > 0 {
				fmt.Fprintf(&b, ", revenu %d fois après correction", i.Regressions)
			}
			b.WriteString("\n")
			if i.PrintJobID != "" {
				fmt.Fprintf(&b, "- Impression : `%s`\n", i.PrintJobID)
			}
			if i.PrinterID != "" {
				fmt.Fprintf(&b, "- Imprimante : `%s`\n", i.PrinterID)
			}
			if i.ModelID != "" {
				fmt.Fprintf(&b, "- Modèle : `%s`\n", i.ModelID)
			}
			if i.Expected != "" {
				fmt.Fprintf(&b, "- Attendu : %s\n", quoteInline(i.Expected))
			}
			if i.Observed != "" {
				fmt.Fprintf(&b, "- Observé : %s\n", quoteInline(i.Observed))
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// quoteInline garde un texte multiligne dans une puce Markdown.
func quoteInline(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n  ")
}

// StateLabel : libellé français d'un état.
func StateLabel(s string) string {
	switch s {
	case StateNew:
		return "nouveau"
	case StateSeen:
		return "vu"
	case StateFixed:
		return "corrigé"
	case StateDismissed:
		return "écarté"
	}
	return s
}

// InstructionText : consigne ajoutée aux instructions du serveur MCP (internal/mcpserver/server.go).
const InstructionText = "Dysfonctionnements : dès que tu constates un comportement anormal de Printalk (outil qui refuse à tort, réponse incohérente avec l'état réel, fausse alerte, donnée périmée, réponse trop lourde ou illisible), " +
	"appelle report_issue SPONTANÉMENT, sans attendre qu'on te le demande, puis continue d'aider l'utilisateur : titre court et sans identifiant, gravité P0 à P3, outil concerné, attendu, observé, identifiants d'impression ou d'imprimante. " +
	"Le serveur dédoublonne (même symptôme : le compteur monte) et masque les secrets ; n'y mets de toute façon jamais de code de lancement, de jeton ni de mot de passe. " +
	"Rien n'est corrigé automatiquement : l'utilisateur colle l'export (export_issues) à l'agent qui code."
