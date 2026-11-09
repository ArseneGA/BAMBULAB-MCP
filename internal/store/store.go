// Package store fournit la persistance : SQLite pur Go (modernc.org/sqlite,
// sans cgo), migrations versionnées embarquées, journal d'audit et blobs
// adressés par contenu.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pilote "sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrNotFound est renvoyée quand une entité n'existe pas.
var ErrNotFound = errors.New("introuvable")

// Store encapsule la base SQLite.
type Store struct {
	db    *sql.DB
	now   func() time.Time
	blobs *BlobStore
}

// AttachBlobs relie le magasin de blobs : AddSourceFile vérifie alors que le
// blob d'un fichier « linux » existe avec la taille déclarée.
func (s *Store) AttachBlobs(b *BlobStore) { s.blobs = b }

// Options d'ouverture.
type Options struct {
	// BusyTimeout par défaut 5 s.
	BusyTimeout time.Duration
	// Now injectable pour les tests.
	Now func() time.Time
	// NoMigrate ouvre la base SANS appliquer de migration (outils d'exploitation qui ne doivent jamais changer le
	// schéma : `bambud upgrade-notify` s'exécute avec un binaire dont le schéma peut différer de celui de la base).
	NoMigrate bool
}

// Open ouvre (et crée) la base au chemin donné puis applique les migrations.
// Une seule connexion est utilisée : SQLite n'a qu'un écrivain, cela évite les
// SQLITE_BUSY internes et suffit largement pour un service de contrôle léger.
func Open(path string, opts Options) (*Store, error) {
	if opts.BusyTimeout <= 0 {
		opts.BusyTimeout = 5 * time.Second
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("création du dossier de la base : %w", err)
	}
	q := url.Values{}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", opts.BusyTimeout.Milliseconds()))
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(FULL)")
	q.Add("_pragma", "recursive_triggers(1)")
	// BEGIN IMMEDIATE pour toute transaction : verrou d'écriture pris d'emblée.
	q.Set("_txlock", "immediate")
	dsn := path + "?" + q.Encode()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ouverture de la base : %w", err)
	}
	s := &Store{db: db, now: opts.Now}
	if opts.NoMigrate {
		return s, nil
	}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close ferme la base.
func (s *Store) Close() error { return s.db.Close() }

// DB expose le *sql.DB (utilisé par la file de travaux). Attention : une seule
// connexion ; ne jamais appeler DB() pendant qu'une transaction est ouverte.
func (s *Store) DB() *sql.DB { return s.db }

// Now renvoie l'heure courante du store (injectable).
func (s *Store) Now() time.Time { return s.now().UTC() }

// FormatTime et ParseTime fixent le format de stockage des horodatages.
func FormatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func ParseTime(s string) (time.Time, error) { return time.Parse(time.RFC3339Nano, s) }

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, err
	}
	var ms []migration
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			return nil, fmt.Errorf("migration mal nommée : %s", name)
		}
		v, err := strconv.Atoi(prefix)
		if err != nil {
			return nil, fmt.Errorf("migration mal nommée : %s", name)
		}
		b, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		ms = append(ms, migration{v, name, string(b)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	for i := 1; i < len(ms); i++ {
		if ms[i].version == ms[i-1].version {
			return nil, fmt.Errorf("version de migration en double : %d", ms[i].version)
		}
	}
	return ms, nil
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	var current int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_version`).Scan(&current); err != nil {
		return err
	}
	ms, err := loadMigrations()
	if err != nil {
		return err
	}
	if len(ms) > 0 && current > ms[len(ms)-1].version {
		return fmt.Errorf("la base est en version %d, plus récente que ce binaire (%d) : mise à jour du service nécessaire", current, ms[len(ms)-1].version)
	}
	for _, m := range ms {
		if m.version <= current {
			continue
		}
		err := s.Tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, m.sql); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO schema_version(version,name,applied_at) VALUES(?,?,?)`,
				m.version, m.name, FormatTime(s.now()))
			return err
		})
		if err != nil {
			return fmt.Errorf("migration %s : %w", m.name, err)
		}
	}
	return nil
}

// LatestSchemaVersion renvoie la version de schéma la plus récente embarquée dans ce binaire.
func LatestSchemaVersion() int {
	ms, err := loadMigrations()
	if err != nil || len(ms) == 0 {
		return 0
	}
	return ms[len(ms)-1].version
}

// SchemaVersion renvoie la version courante du schéma.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_version`).Scan(&v)
	return v, err
}

// Tx exécute fn dans une transaction (commit si nil, rollback sinon).
func (s *Store) Tx(ctx context.Context, fn func(*sql.Tx) error) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
