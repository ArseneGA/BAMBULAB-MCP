package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

// Chaque mutation écrit son entrée d'audit dans la même transaction.

func nullStr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func strPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

// CreateCollection insère une collection.
func (s *Store) CreateCollection(ctx context.Context, actor string, c contracts.Collection) (contracts.Collection, error) {
	if c.ID == "" {
		c.ID = contracts.NewID("col")
	}
	if c.Name == "" {
		return c, errors.New("nom de collection requis")
	}
	c.CreatedAt = s.Now()
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO collections(id,name,parent_id,created_at) VALUES(?,?,?,?)`,
			c.ID, c.Name, nullStr(c.ParentID), FormatTime(c.CreatedAt)); err != nil {
			return err
		}
		return s.AuditTx(ctx, tx, actor, "collection.create", "collection", c.ID, map[string]any{"name": c.Name})
	})
	return c, err
}

// ListCollections renvoie toutes les collections par nom.
func (s *Store) ListCollections(ctx context.Context) ([]contracts.Collection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,parent_id,created_at FROM collections ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contracts.Collection
	for rows.Next() {
		var c contracts.Collection
		var p sql.NullString
		var at string
		if err := rows.Scan(&c.ID, &c.Name, &p, &at); err != nil {
			return nil, err
		}
		c.ParentID = strPtr(p)
		c.CreatedAt, _ = ParseTime(at)
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateModel insère un modèle logique.
func (s *Store) CreateModel(ctx context.Context, actor string, m contracts.Model) (contracts.Model, error) {
	if m.ID == "" {
		m.ID = contracts.NewID("mdl")
	}
	if m.Name == "" {
		return m, errors.New("nom de modèle requis")
	}
	if m.Tags == nil {
		m.Tags = []string{}
	}
	m.CreatedAt = s.Now()
	m.UpdatedAt = m.CreatedAt
	tags, _ := json.Marshal(m.Tags)
	var dims any
	if m.Dimensions != nil {
		b, _ := json.Marshal(m.Dimensions)
		dims = string(b)
	}
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO models(id,collection_id,name,tags,dimensions,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
			m.ID, nullStr(m.CollectionID), m.Name, string(tags), dims, FormatTime(m.CreatedAt), FormatTime(m.UpdatedAt)); err != nil {
			return err
		}
		return s.AuditTx(ctx, tx, actor, "model.create", "model", m.ID, map[string]any{"name": m.Name})
	})
	return m, err
}

// GetModel renvoie un modèle (y compris en corbeille).
func (s *Store) GetModel(ctx context.Context, id string) (contracts.Model, error) {
	var m contracts.Model
	var coll, dims, del sql.NullString
	var tags, ca, ua string
	err := s.db.QueryRowContext(ctx,
		`SELECT id,collection_id,name,tags,dimensions,created_at,updated_at,deleted_at FROM models WHERE id=?`, id).
		Scan(&m.ID, &coll, &m.Name, &tags, &dims, &ca, &ua, &del)
	if errors.Is(err, sql.ErrNoRows) {
		return m, fmt.Errorf("modèle %s : %w", id, ErrNotFound)
	}
	if err != nil {
		return m, err
	}
	m.CollectionID = strPtr(coll)
	_ = json.Unmarshal([]byte(tags), &m.Tags)
	if dims.Valid {
		m.Dimensions = new(contracts.Dimensions)
		_ = json.Unmarshal([]byte(dims.String), m.Dimensions)
	}
	m.CreatedAt, _ = ParseTime(ca)
	m.UpdatedAt, _ = ParseTime(ua)
	if del.Valid {
		t, _ := ParseTime(del.String)
		m.DeletedAt = &t
	}
	return m, nil
}

// TrashModel met un modèle en corbeille (récupérable) ; RestoreModel l'en sort.
func (s *Store) TrashModel(ctx context.Context, actor, id string) error {
	return s.setDeleted(ctx, actor, id, true)
}

// RestoreModel restaure un modèle de la corbeille.
func (s *Store) RestoreModel(ctx context.Context, actor, id string) error {
	return s.setDeleted(ctx, actor, id, false)
}

func (s *Store) setDeleted(ctx context.Context, actor, id string, deleted bool) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var val any
		op := "model.restore"
		if deleted {
			val, op = FormatTime(s.now()), "model.trash"
		}
		res, err := tx.ExecContext(ctx, `UPDATE models SET deleted_at=?, updated_at=? WHERE id=?`, val, FormatTime(s.now()), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("modèle %s : %w", id, ErrNotFound)
		}
		return s.AuditTx(ctx, tx, actor, op, "model", id, nil)
	})
}

// AddSourceFile enregistre un fichier source (le blob doit déjà être stocké si availability=linux).
func (s *Store) AddSourceFile(ctx context.Context, actor string, f contracts.SourceFile) (contracts.SourceFile, error) {
	if f.ID == "" {
		f.ID = contracts.NewID("src")
	}
	if !f.Format.Valid() {
		return f, fmt.Errorf("format inconnu %q", f.Format)
	}
	if !f.Availability.Valid() {
		return f, fmt.Errorf("disponibilité inconnue %q", f.Availability)
	}
	if !validSHA256(f.SHA256) {
		return f, errors.New("empreinte sha256 invalide")
	}
	if f.SizeBytes < 0 {
		return f, errors.New("taille négative")
	}
	if f.Availability == contracts.AvailableLinux && s.blobs != nil {
		size, err := s.blobs.Size(f.SHA256)
		if err != nil {
			return f, fmt.Errorf("blob de %s absent du magasin : %w", f.Name, err)
		}
		if size != f.SizeBytes {
			return f, fmt.Errorf("taille déclarée %d ≠ taille du blob %d pour %s", f.SizeBytes, size, f.Name)
		}
	}
	f.CreatedAt = s.Now()
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO source_files(id,model_id,name,format,size_bytes,sha256,availability,origin_path,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			f.ID, f.ModelID, f.Name, string(f.Format), f.SizeBytes, f.SHA256, string(f.Availability), nullStr(f.OriginPath), FormatTime(f.CreatedAt)); err != nil {
			return err
		}
		return s.AuditTx(ctx, tx, actor, "source_file.add", "source_file", f.ID, map[string]any{"model_id": f.ModelID, "sha256": f.SHA256})
	})
	return f, err
}

// ListSourceFiles renvoie les fichiers d'un modèle.
func (s *Store) ListSourceFiles(ctx context.Context, modelID string) ([]contracts.SourceFile, error) {
	return s.querySourceFiles(ctx, `WHERE model_id=? ORDER BY created_at, id`, modelID)
}

// FindExactDuplicates renvoie les fichiers partageant l'empreinte (doublons exacts).
func (s *Store) FindExactDuplicates(ctx context.Context, sha256 string) ([]contracts.SourceFile, error) {
	return s.querySourceFiles(ctx, `WHERE sha256=? ORDER BY created_at, id`, sha256)
}

func (s *Store) querySourceFiles(ctx context.Context, where string, arg any) ([]contracts.SourceFile, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,model_id,name,format,size_bytes,sha256,availability,origin_path,created_at FROM source_files `+where, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contracts.SourceFile
	for rows.Next() {
		var f contracts.SourceFile
		var format, avail, at string
		var origin sql.NullString
		if err := rows.Scan(&f.ID, &f.ModelID, &f.Name, &format, &f.SizeBytes, &f.SHA256, &avail, &origin, &at); err != nil {
			return nil, err
		}
		f.Format, f.Availability, f.OriginPath = contracts.FileFormat(format), contracts.Availability(avail), strPtr(origin)
		f.CreatedAt, _ = ParseTime(at)
		out = append(out, f)
	}
	return out, rows.Err()
}

// CreateVersion crée une version immuable (jamais modifiée ensuite : déclencheurs SQL).
func (s *Store) CreateVersion(ctx context.Context, actor string, v contracts.ModelVersion) (contracts.ModelVersion, error) {
	if v.ID == "" {
		v.ID = contracts.NewID("ver")
	}
	if v.SourceFileIDs == nil {
		v.SourceFileIDs = []string{}
	}
	v.CreatedAt = s.Now()
	ids, _ := json.Marshal(v.SourceFileIDs)
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM models WHERE id=?`, v.ModelID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return fmt.Errorf("modèle %s : %w", v.ModelID, ErrNotFound)
		}
		seen := map[string]bool{}
		for _, sid := range v.SourceFileIDs {
			if seen[sid] {
				return fmt.Errorf("fichier source %s en double dans la version", sid)
			}
			seen[sid] = true
			var owner string
			err := tx.QueryRowContext(ctx, `SELECT model_id FROM source_files WHERE id=?`, sid).Scan(&owner)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("fichier source %s : %w", sid, ErrNotFound)
			}
			if err != nil {
				return err
			}
			if owner != v.ModelID {
				return fmt.Errorf("fichier source %s appartient au modèle %s, pas à %s", sid, owner, v.ModelID)
			}
		}
		if v.ParentID != nil {
			var pm string
			err := tx.QueryRowContext(ctx, `SELECT model_id FROM model_versions WHERE id=?`, *v.ParentID).Scan(&pm)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("version parente %s : %w", *v.ParentID, ErrNotFound)
			}
			if err != nil {
				return err
			}
			if pm != v.ModelID {
				return fmt.Errorf("la version parente %s appartient au modèle %s, pas à %s", *v.ParentID, pm, v.ModelID)
			}
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO model_versions(id,model_id,parent_id,label,note,source_file_ids,created_at) VALUES(?,?,?,?,?,?,?)`,
			v.ID, v.ModelID, nullStr(v.ParentID), v.Label, v.Note, string(ids), FormatTime(v.CreatedAt)); err != nil {
			return err
		}
		return s.AuditTx(ctx, tx, actor, "version.create", "model_version", v.ID, map[string]any{"model_id": v.ModelID, "label": v.Label})
	})
	return v, err
}

// ListVersions renvoie les versions d'un modèle, la plus récente d'abord.
func (s *Store) ListVersions(ctx context.Context, modelID string) ([]contracts.ModelVersion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,model_id,parent_id,label,note,source_file_ids,created_at FROM model_versions WHERE model_id=? ORDER BY created_at DESC, id`, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contracts.ModelVersion
	for rows.Next() {
		var v contracts.ModelVersion
		var p sql.NullString
		var ids, at string
		if err := rows.Scan(&v.ID, &v.ModelID, &p, &v.Label, &v.Note, &ids, &at); err != nil {
			return nil, err
		}
		v.ParentID = strPtr(p)
		_ = json.Unmarshal([]byte(ids), &v.SourceFileIDs)
		v.CreatedAt, _ = ParseTime(at)
		out = append(out, v)
	}
	return out, rows.Err()
}

// docTables : liste blanche des tables à documents JSON.
var docTables = map[string]bool{
	"materials": true, "printer_models": true, "printers": true,
	"print_jobs": true, "print_confirmations": true, "manufacturing_goals": true,
}
