package upgrade

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pilote "sqlite" (déjà lié par internal/store)
)

// Probe est le résultat de `bambud upgrade-probe` : lecture seule de la base, indépendante du schéma du binaire.
//   - SchemaVersion : version du schéma de la base (avant/après une mise à jour, pour décider d'une restauration) ;
//   - Busy : occupation déduite de la BASE seulement (impressions actives, travaux en cours). Elle sert quand le service
//     n'est pas joignable ou qu'il est trop ancien pour répondre à /api/system/busy ; elle ne voit pas les demandes
//     humaines en mémoire (confirmation, arrêt), que seul le service connaît.
type Probe struct {
	SchemaVersion int      `json:"schema_version"`
	Busy          bool     `json:"busy"`
	Reasons       []string `json:"reasons"`
}

// États d'impression (documents JSON de print_jobs) qui rendent un redémarrage risqué. Doit rester synchronisé avec
// printing.activeJobStates (vérifié par un test).
var ProbeBusyPrintStates = []string{"PRINTING", "START_REQUESTED", "START_UNKNOWN", "UPLOADING", "SLICING"}

// probeCode : code de la raison d'occupation, le même que celui du service (printing.Busy*).
func probeCode(state string) string {
	switch state {
	case "PRINTING":
		return "print_running"
	case "UPLOADING", "SLICING":
		return "deposit_in_progress"
	}
	return "print_active" // START_REQUESTED, START_UNKNOWN
}

// ProbeDB lit la base en lecture seule. Le fichier -wal/-shm existants sont honorés (service en marche).
func ProbeDB(ctx context.Context, dataDir string) (Probe, error) {
	var p Probe
	path := filepath.Join(dataDir, "bambu.db")
	q := url.Values{}
	q.Add("mode", "ro")
	q.Add("_pragma", "busy_timeout(5000)")
	dsn := "file:" + filepath.ToSlash(path) + "?" + q.Encode()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return p, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_version`).Scan(&p.SchemaVersion); err != nil {
		return p, fmt.Errorf("lecture de schema_version : %w", err)
	}
	p.Reasons = []string{}

	holders := strings.TrimSuffix(strings.Repeat("?,", len(ProbeBusyPrintStates)), ",")
	args := make([]any, len(ProbeBusyPrintStates))
	for i, s := range ProbeBusyPrintStates {
		args[i] = s
	}
	rows, err := db.QueryContext(ctx, `SELECT json_extract(doc,'$.state'), COUNT(*) FROM print_jobs WHERE json_extract(doc,'$.state') IN (`+holders+`) GROUP BY 1`, args...)
	if err != nil {
		return p, fmt.Errorf("lecture de print_jobs : %w", err)
	}
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			rows.Close()
			return p, err
		}
		p.Reasons = append(p.Reasons, fmt.Sprintf("%s: %d impression(s) %s", probeCode(state), n, state))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return p, err
	}
	rows.Close()
	var running int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE state='running'`).Scan(&running); err != nil {
		return p, fmt.Errorf("lecture de jobs : %w", err)
	}
	if running > 0 {
		p.Reasons = append(p.Reasons, fmt.Sprintf("job_running: %d travail(aux) en cours", running))
	}
	p.Busy = len(p.Reasons) > 0
	return p, nil
}

// ParseProbe lit la sortie JSON de `bambud upgrade-probe`.
func ParseProbe(out string) (Probe, error) {
	var p Probe
	i, j := strings.Index(out, "{"), strings.LastIndex(out, "}")
	if i < 0 || j < i {
		return p, fmt.Errorf("sortie de upgrade-probe illisible : %q", truncate(out, 120))
	}
	if err := json.Unmarshal([]byte(out[i:j+1]), &p); err != nil {
		return p, fmt.Errorf("sortie de upgrade-probe illisible : %w", err)
	}
	return p, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
