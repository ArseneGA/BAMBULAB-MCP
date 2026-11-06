package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Attention : le store n'a qu'une connexion SQLite. Ne jamais ouvrir une
// requête pendant qu'un *sql.Rows est encore ouvert : chaque fonction ci-dessous
// lit tout puis ferme avant de rendre la main.

type dbx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Service) db() *sql.DB { return s.d.Store.DB() }

func fmtT(t time.Time) string { return store.FormatTime(t) }

func parseT(v string) time.Time { t, _ := store.ParseTime(v); return t }

func nullS(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func ptrS(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

// ---- modèles ----

const modelCols = `id,collection_id,name,tags,dimensions,created_at,updated_at,deleted_at,description,license,provenance`

type rowScanner interface{ Scan(dest ...any) error }

func scanModel(r rowScanner) (contracts.Model, error) {
	var m contracts.Model
	var coll, dims, del, prov sql.NullString
	var tags, ca, ua string
	if err := r.Scan(&m.ID, &coll, &m.Name, &tags, &dims, &ca, &ua, &del, &m.Description, &m.License, &prov); err != nil {
		return m, err
	}
	m.CollectionID = ptrS(coll)
	m.Tags = []string{}
	_ = json.Unmarshal([]byte(tags), &m.Tags)
	if dims.Valid {
		m.Dimensions = new(contracts.Dimensions)
		_ = json.Unmarshal([]byte(dims.String), m.Dimensions)
	}
	m.CreatedAt, m.UpdatedAt = parseT(ca), parseT(ua)
	if del.Valid {
		t := parseT(del.String)
		m.DeletedAt = &t
	}
	if prov.Valid {
		m.Provenance = new(contracts.Provenance)
		_ = json.Unmarshal([]byte(prov.String), m.Provenance)
	}
	return m, nil
}

func getModel(ctx context.Context, q dbx, id string) (contracts.Model, error) {
	m, err := scanModel(q.QueryRowContext(ctx, `SELECT `+modelCols+` FROM models WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return m, fmt.Errorf("modèle %s : %w", id, ErrNotFound)
	}
	return m, err
}

func queryModels(ctx context.Context, q dbx, where string, args ...any) ([]contracts.Model, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+modelCols+` FROM models `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contracts.Model
	for rows.Next() {
		m, err := scanModel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func insertModel(ctx context.Context, q dbx, m contracts.Model) error {
	tags, _ := json.Marshal(normTags(m.Tags))
	var dims, prov any
	if m.Dimensions != nil {
		b, _ := json.Marshal(m.Dimensions)
		dims = string(b)
	}
	if m.Provenance != nil {
		b, _ := json.Marshal(m.Provenance)
		prov = string(b)
	}
	_, err := q.ExecContext(ctx, `INSERT INTO models(`+modelCols+`) VALUES(?,?,?,?,?,?,?,NULL,?,?,?)`,
		m.ID, nullS(m.CollectionID), m.Name, string(tags), dims, fmtT(m.CreatedAt), fmtT(m.UpdatedAt), m.Description, m.License, prov)
	return err
}

func touchModel(ctx context.Context, q dbx, id string, now time.Time) error {
	_, err := q.ExecContext(ctx, `UPDATE models SET updated_at=? WHERE id=?`, fmtT(now), id)
	return err
}

// normTags : minuscules, espaces compactés, uniques, triés.
func normTags(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, t := range in {
		t = strings.Join(strings.Fields(strings.ToLower(t)), " ")
		if t == "" || seen[t] {
			continue
		}
		if len(t) > 40 {
			t = t[:40]
		}
		seen[t] = true
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// ---- fichiers sources ----

const fileCols = `id,model_id,name,format,size_bytes,sha256,availability,origin_path,created_at,rel_path,origin_mtime_ns,unit`

func scanFile(r rowScanner) (contracts.SourceFile, error) {
	var f contracts.SourceFile
	var format, avail, at string
	var origin sql.NullString
	var mt sql.NullInt64
	if err := r.Scan(&f.ID, &f.ModelID, &f.Name, &format, &f.SizeBytes, &f.SHA256, &avail, &origin, &at, &f.RelPath, &mt, &f.Unit); err != nil {
		return f, err
	}
	f.Format, f.Availability, f.OriginPath = contracts.FileFormat(format), contracts.Availability(avail), ptrS(origin)
	f.CreatedAt = parseT(at)
	if mt.Valid {
		t := time.Unix(0, mt.Int64).UTC()
		f.OriginMTime = &t
	}
	return f, nil
}

func queryFiles(ctx context.Context, q dbx, where string, args ...any) ([]contracts.SourceFile, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+fileCols+` FROM source_files `+where+` ORDER BY created_at, rowid`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contracts.SourceFile
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func getFile(ctx context.Context, q dbx, id string) (contracts.SourceFile, error) {
	f, err := scanFile(q.QueryRowContext(ctx, `SELECT `+fileCols+` FROM source_files WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return f, fmt.Errorf("fichier %s : %w", id, ErrNotFound)
	}
	return f, err
}

func insertFile(ctx context.Context, q dbx, f contracts.SourceFile) error {
	var mt any
	if f.OriginMTime != nil {
		mt = f.OriginMTime.UnixNano()
	}
	_, err := q.ExecContext(ctx, `INSERT INTO source_files(`+fileCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		f.ID, f.ModelID, f.Name, string(f.Format), f.SizeBytes, f.SHA256, string(f.Availability), nullS(f.OriginPath),
		fmtT(f.CreatedAt), f.RelPath, mt, f.Unit)
	return err
}

// latestFile renvoie le dernier fichier créé (la liste vient de queryFiles : ordre de création).
func latestFile(files []contracts.SourceFile) (contracts.SourceFile, bool) {
	if len(files) == 0 {
		return contracts.SourceFile{}, false
	}
	return files[len(files)-1], true
}

// pickCurrent renvoie le fichier courant d'un modèle : celui de sa dernière version
// (versions triées de la plus récente à la plus ancienne), à défaut le dernier créé.
func pickCurrent(files []contracts.SourceFile, vs []contracts.ModelVersion) (contracts.SourceFile, bool) {
	if len(vs) > 0 {
		for _, id := range vs[0].SourceFileIDs {
			for _, f := range files {
				if f.ID == id {
					return f, true
				}
			}
		}
	}
	return latestFile(files)
}

// currentFile : fichier courant d'un modèle.
func currentFile(ctx context.Context, q dbx, modelID string) (contracts.SourceFile, bool, error) {
	files, err := queryFiles(ctx, q, `WHERE model_id=?`, modelID)
	if err != nil {
		return contracts.SourceFile{}, false, err
	}
	vs, err := queryVersions(ctx, q, modelID)
	if err != nil {
		return contracts.SourceFile{}, false, err
	}
	f, ok := pickCurrent(files, vs)
	return f, ok, nil
}

// ---- versions ----

func queryVersions(ctx context.Context, q dbx, modelID string) ([]contracts.ModelVersion, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id,model_id,parent_id,label,note,source_file_ids,created_at FROM model_versions WHERE model_id=? ORDER BY created_at DESC, rowid DESC`, modelID)
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
		v.ParentID = ptrS(p)
		v.SourceFileIDs = []string{}
		_ = json.Unmarshal([]byte(ids), &v.SourceFileIDs)
		v.CreatedAt = parseT(at)
		out = append(out, v)
	}
	return out, rows.Err()
}

func insertVersion(ctx context.Context, q dbx, v contracts.ModelVersion) error {
	ids, _ := json.Marshal(v.SourceFileIDs)
	_, err := q.ExecContext(ctx,
		`INSERT INTO model_versions(id,model_id,parent_id,label,note,source_file_ids,created_at) VALUES(?,?,?,?,?,?,?)`,
		v.ID, v.ModelID, nullS(v.ParentID), v.Label, v.Note, string(ids), fmtT(v.CreatedAt))
	return err
}

// ---- collections ----

func allCollections(ctx context.Context, q dbx) (map[string]contracts.Collection, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,name,parent_id,created_at FROM collections`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]contracts.Collection{}
	for rows.Next() {
		var c contracts.Collection
		var p sql.NullString
		var at string
		if err := rows.Scan(&c.ID, &c.Name, &p, &at); err != nil {
			return nil, err
		}
		c.ParentID, c.CreatedAt = ptrS(p), parseT(at)
		out[c.ID] = c
	}
	return out, rows.Err()
}

// collectionPath : "Parent/Enfant".
func collectionPath(cols map[string]contracts.Collection, id *string) string {
	if id == nil {
		return ""
	}
	var parts []string
	cur := *id
	for i := 0; i < 32; i++ {
		c, ok := cols[cur]
		if !ok {
			break
		}
		parts = append([]string{c.Name}, parts...)
		if c.ParentID == nil {
			break
		}
		cur = *c.ParentID
	}
	return strings.Join(parts, "/")
}

// ensureCollection renvoie la collection (parent, nom) en la créant si besoin.
func (s *Service) ensureCollection(ctx context.Context, tx dbx, actor string, auditTx func(op, id string, detail any) error, parent *string, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", invalidf("nom de collection vide")
	}
	var id string
	var err error
	if parent == nil {
		err = tx.QueryRowContext(ctx, `SELECT id FROM collections WHERE parent_id IS NULL AND name=? COLLATE NOCASE LIMIT 1`, name).Scan(&id)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT id FROM collections WHERE parent_id=? AND name=? COLLATE NOCASE LIMIT 1`, *parent, name).Scan(&id)
	}
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	id = contracts.NewID("col")
	if _, err := tx.ExecContext(ctx, `INSERT INTO collections(id,name,parent_id,created_at) VALUES(?,?,?,?)`,
		id, name, nullS(parent), fmtT(s.d.Store.Now())); err != nil {
		return "", err
	}
	if auditTx != nil {
		if err := auditTx("collection.create", id, map[string]any{"name": name, "actor": actor}); err != nil {
			return "", err
		}
	}
	return id, nil
}

// ---- produits dérivés ----

// Derived est un produit dérivé enregistré.
type Derived struct {
	SourceSHA string          `json:"source_sha256"`
	Kind      string          `json:"kind"`
	State     string          `json:"state"`
	BlobSHA   string          `json:"blob_sha256,omitempty"`
	Doc       json.RawMessage `json:"doc,omitempty"`
	Note      string          `json:"note,omitempty"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func getDerived(ctx context.Context, q dbx, sha, kind string) (Derived, bool, error) {
	var d Derived
	var blob, doc sql.NullString
	var at string
	err := q.QueryRowContext(ctx, `SELECT source_sha,kind,state,blob_sha,doc,note,updated_at FROM derived_assets WHERE source_sha=? AND kind=?`, sha, kind).
		Scan(&d.SourceSHA, &d.Kind, &d.State, &blob, &doc, &d.Note, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return d, false, nil
	}
	if err != nil {
		return d, false, err
	}
	d.BlobSHA = blob.String
	if doc.Valid {
		d.Doc = json.RawMessage(doc.String)
	}
	d.UpdatedAt = parseT(at)
	return d, true, nil
}

func (s *Service) setDerived(ctx context.Context, sha, kind, state, blobSHA string, doc any, note string) error {
	var docS, blob any
	if doc != nil {
		b, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		docS = string(b)
	}
	if blobSHA != "" {
		blob = blobSHA
	}
	_, err := s.db().ExecContext(ctx, `INSERT INTO derived_assets(source_sha,kind,state,blob_sha,doc,note,updated_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(source_sha,kind) DO UPDATE SET state=excluded.state, blob_sha=excluded.blob_sha, doc=excluded.doc, note=excluded.note, updated_at=excluded.updated_at`,
		sha, kind, state, blob, docS, note, fmtT(s.d.Store.Now()))
	return err
}

// markPending crée une ligne « pending » seulement si rien n'existe encore pour (sha, kind).
func (s *Service) markPending(ctx context.Context, sha, kind string) (created bool, err error) {
	res, err := s.db().ExecContext(ctx, `INSERT OR IGNORE INTO derived_assets(source_sha,kind,state,note,updated_at) VALUES(?,?,?,?,?)`,
		sha, kind, StatePending, "", fmtT(s.d.Store.Now()))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ---- index d'import ----

type indexEntry struct {
	Path         string
	Size         int64
	MTime        int64
	SHA          string
	SourceFileID string
	Copied       bool
}

func indexGet(ctx context.Context, q dbx, path string) (*indexEntry, error) {
	var e indexEntry
	var copied int
	err := q.QueryRowContext(ctx, `SELECT origin_path,size_bytes,mtime_ns,sha256,source_file_id,copied FROM import_index WHERE origin_path=?`, path).
		Scan(&e.Path, &e.Size, &e.MTime, &e.SHA, &e.SourceFileID, &copied)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	e.Copied = copied != 0
	return &e, err
}

func (s *Service) indexPut(ctx context.Context, q dbx, e indexEntry) error {
	c := 0
	if e.Copied {
		c = 1
	}
	_, err := q.ExecContext(ctx, `INSERT INTO import_index(origin_path,size_bytes,mtime_ns,sha256,source_file_id,copied,seen_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(origin_path) DO UPDATE SET size_bytes=excluded.size_bytes, mtime_ns=excluded.mtime_ns, sha256=excluded.sha256,
		source_file_id=excluded.source_file_id, copied=excluded.copied, seen_at=excluded.seen_at`,
		e.Path, e.Size, e.MTime, e.SHA, e.SourceFileID, c, fmtT(s.d.Store.Now()))
	return err
}

// audit enveloppe store.AuditTx pour une transaction.
func (s *Service) auditor(ctx context.Context, tx *sql.Tx, actor, entityType string) func(op, id string, detail any) error {
	return func(op, id string, detail any) error {
		return s.d.Store.AuditTx(ctx, tx, actor, op, entityType, id, detail)
	}
}
