package export

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/goals"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/insights"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Deps : composants nécessaires. Goals et Insights sont facultatifs (sans eux : pas de profils ni d'estimations).
type Deps struct {
	Store    *store.Store
	Blobs    *store.BlobStore
	Queue    *jobs.Queue
	Registry *jobs.Registry
	Catalog  *catalog.Service
	Goals    *goals.Service
	Insights *insights.Service
	Log      *slog.Logger
	DataDir  string
	// Version du service, écrite dans le manifeste.
	Version string
	// Retention : durée de conservation d'une archive prête (défaut DefaultRetention).
	Retention time.Duration
	// LinkTTL : durée de validité d'un lien de téléchargement signé (défaut DefaultLinkTTL ; bornée à
	// [MinLinkTTL, MaxLinkTTL] par New).
	LinkTTL time.Duration
}

// ParseLinkTTL lit une durée Go (« 90m », « 4h ») pour BAMBU_EXPORT_LINK_TTL. Vide : le défaut. Hors bornes : refusée
// (jamais corrigée en silence).
func ParseLinkTTL(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return DefaultLinkTTL, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("durée du lien d'export %q invalide : attendu une durée comme 90m ou 4h", s)
	}
	if d < MinLinkTTL || d > MaxLinkTTL {
		return 0, fmt.Errorf("durée du lien d'export %s hors bornes : entre %s et %s", d, MinLinkTTL, MaxLinkTTL)
	}
	return d, nil
}

// Service produit et sert les archives d'export.
type Service struct {
	d   Deps
	dir string // <données>/exports
}

// New crée le service, enregistre le travail (rejouable : l'archive est reconstruite) et purge les restes d'un
// arrêt brutal (fichiers .part).
func New(d Deps) (*Service, error) {
	if d.Store == nil || d.Blobs == nil || d.Catalog == nil {
		return nil, errors.New("export : base, blobs et catalogue requis")
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Retention <= 0 {
		d.Retention = DefaultRetention
	}
	if d.LinkTTL <= 0 {
		d.LinkTTL = DefaultLinkTTL
	}
	d.LinkTTL = min(max(d.LinkTTL, MinLinkTTL), MaxLinkTTL)
	s := &Service{d: d, dir: filepath.Join(d.DataDir, "exports")}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return nil, fmt.Errorf("dossier des exports : %w", err)
	}
	if ents, err := os.ReadDir(s.dir); err == nil {
		for _, e := range ents {
			if strings.HasSuffix(e.Name(), ".part") {
				_ = os.Remove(filepath.Join(s.dir, e.Name()))
			}
		}
	}
	if d.Registry != nil {
		d.Registry.Register(JobKind, true, s.runJob)
	}
	return s, nil
}

func (s *Service) db() *sql.DB { return s.d.Store.DB() }

func (s *Service) now() time.Time { return s.d.Store.Now() }

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w : %s", catalog.ErrInvalid, fmt.Sprintf(format, args...))
}

// normalize contrôle et complète les options (bornes, valeurs par défaut).
func normalize(in Input) (Input, error) {
	o := &in.Options
	in.CollectionID = strings.TrimSpace(in.CollectionID)
	if in.CollectionID == "" && len(in.ModelIDs) == 0 {
		return in, invalidf("collection_id ou model_ids requis")
	}
	if in.CollectionID != "" && len(in.ModelIDs) > 0 {
		return in, invalidf("collection_id et model_ids s'excluent : choisir l'un ou l'autre")
	}
	lim := func(name, v string, max int) error {
		if utf8.RuneCountInString(v) > max {
			return invalidf("%s : %d caractères au plus", name, max)
		}
		return nil
	}
	for _, c := range []struct {
		n string
		v *string
		m int
	}{{"title", &o.Title, 120}, {"description", &o.Description, 5000}, {"license", &o.License, 200},
		{"assembly_notes", &o.AssemblyNotes, 20000}, {"hardware", &o.Hardware, 10000}} {
		*c.v = strings.TrimSpace(*c.v)
		if err := lim(c.n, *c.v, c.m); err != nil {
			return in, err
		}
	}
	if o.License == "" {
		o.License = DefaultLicense
	}
	for id, q := range o.Quantities {
		if q < 1 || q > 999 {
			return in, invalidf("quantities[%s] : entre 1 et 999", id)
		}
	}
	switch {
	case o.MaxBytes < 0:
		return in, invalidf("max_bytes : valeur positive attendue")
	case o.MaxBytes == 0:
		o.MaxBytes = DefaultMaxBytes
	case o.MaxBytes > HardMaxBytes:
		return in, invalidf("max_bytes : %d Gio au plus", HardMaxBytes>>30)
	}
	return in, nil
}

// Start valide la demande (modèles, versions, fichiers copiés, taille) AVANT de créer quoi que ce soit, puis met
// l'export en file. Le travail construit l'archive en flux.
func (s *Service) Start(ctx context.Context, in Input) (*Started, error) {
	in, err := normalize(in)
	if err != nil {
		return nil, err
	}
	pl, err := s.resolve(ctx, in)
	if err != nil {
		return nil, err
	}
	s.expireOld(ctx)
	id := contracts.NewID("exp")
	now := s.now()
	doc, _ := json.Marshal(Summary{Parts: len(pl.parts), Licence: in.Options.License})
	if _, err := s.db().ExecContext(ctx, `INSERT INTO project_exports(id,job_id,state,title,file_name,size_bytes,sha256,doc,error,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		id, "", StateQueued, pl.title, archiveName(pl.title, now), 0, "", string(doc), "", store.FormatTime(now), store.FormatTime(now.Add(s.d.Retention))); err != nil {
		return nil, err
	}
	params, _ := json.Marshal(jobParams{ExportID: id, Input: in})
	job, _, err := s.d.Queue.Enqueue(ctx, jobs.EnqueueParams{Kind: JobKind, Class: contracts.ClassLight, Params: params})
	if err != nil {
		_, _ = s.db().ExecContext(ctx, `DELETE FROM project_exports WHERE id=?`, id)
		return nil, err
	}
	_, _ = s.db().ExecContext(ctx, `UPDATE project_exports SET job_id=? WHERE id=?`, job.ID, id)
	return &Started{ExportID: id, JobID: job.ID, Plan: pl.summary()}, nil
}

type jobParams struct {
	ExportID string `json:"export_id"`
	Input    Input  `json:"input"`
}

func (s *Service) runJob(ctx context.Context, jc *jobs.JobCtx) (json.RawMessage, error) {
	var p jobParams
	if err := json.Unmarshal(jc.Job.Params, &p); err != nil || p.ExportID == "" {
		return nil, &jobs.HandlerError{Code: "bad_params", Err: errors.New("paramètres d'export illisibles")}
	}
	rec, err := s.Get(ctx, p.ExportID)
	if err != nil {
		return nil, &jobs.HandlerError{Code: "not_found", Err: err}
	}
	sum, size, sha, err := s.build(ctx, jc, p.ExportID, rec.FileName, p.Input)
	if err != nil {
		msg := err.Error()
		_, _ = s.db().ExecContext(context.Background(), `UPDATE project_exports SET state=?, error=? WHERE id=?`, StateFailed, msg, p.ExportID)
		code := "export_failed"
		if errors.Is(err, catalog.ErrInvalid) {
			code = "invalid"
		}
		return nil, &jobs.HandlerError{Code: code, Err: err}
	}
	doc, _ := json.Marshal(sum)
	now := s.now()
	if _, err := s.db().ExecContext(ctx, `UPDATE project_exports SET state=?, size_bytes=?, sha256=?, doc=?, error='', expires_at=? WHERE id=?`,
		StateReady, size, sha, string(doc), store.FormatTime(now.Add(s.d.Retention)), p.ExportID); err != nil {
		return nil, err
	}
	res, _ := json.Marshal(map[string]any{"export_id": p.ExportID, "size_bytes": size, "sha256": sha, "entries": sum.Files, "warnings": len(sum.Warnings)})
	return res, nil
}

// ---- lecture ----

const recCols = `id,job_id,state,title,file_name,size_bytes,sha256,doc,error,created_at,expires_at`

func scanRecord(r interface{ Scan(...any) error }) (Record, error) {
	var rec Record
	var doc, cat, exp string
	if err := r.Scan(&rec.ID, &rec.JobID, &rec.State, &rec.Title, &rec.FileName, &rec.SizeBytes, &rec.SHA256, &doc, &rec.Error, &cat, &exp); err != nil {
		return rec, err
	}
	rec.CreatedAt, _ = store.ParseTime(cat)
	rec.ExpiresAt, _ = store.ParseTime(exp)
	var sum Summary
	if json.Unmarshal([]byte(doc), &sum) == nil {
		rec.Summary = &sum
	}
	if rec.State == StateReady && !rec.ExpiresAt.After(time.Now()) {
		rec.State = StateExpired
	}
	return rec, nil
}

// Get relit un export.
func (s *Service) Get(ctx context.Context, id string) (Record, error) {
	rec, err := scanRecord(s.db().QueryRowContext(ctx, `SELECT `+recCols+` FROM project_exports WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return rec, fmt.Errorf("export %q : %w", id, catalog.ErrNotFound)
	}
	return rec, err
}

// List renvoie les exports récents, du plus récent au plus ancien.
func (s *Service) List(ctx context.Context, limit int) ([]Record, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := s.db().QueryContext(ctx, `SELECT `+recCols+` FROM project_exports ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *Service) path(id string) string { return filepath.Join(s.dir, id+".zip") }

// Link produit une URL de téléchargement signée (LinkTTL). Refuse un export non prêt ou expiré.
func (s *Service) Link(ctx context.Context, id string) (*Link, error) {
	return s.LinkTTL(ctx, id, s.d.LinkTTL)
}

// LinkTTL est Link avec une durée choisie (tests : durée négative = lien déjà expiré). Une durée positive est
// plafonnée à MaxLinkTTL ET à la conservation de l'archive : un lien ne survit jamais au fichier qu'il ouvre.
//
// Adresse (FB13) : quand l'écouteur public restreint existe, l'URL principale est celle de cet écouteur (joignable
// hors du réseau privé), avec la MÊME signature HMAC ; l'URL du réseau privé reste donnée dans PrivateURL.
func (s *Service) LinkTTL(ctx context.Context, id string, ttl time.Duration) (*Link, error) {
	rec, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := readyErr(rec); err != nil {
		return nil, err
	}
	if ttl > MaxLinkTTL {
		ttl = MaxLinkTTL
	}
	if left := rec.ExpiresAt.Sub(s.now()); ttl > left {
		if left <= 0 {
			return nil, fmt.Errorf("%w : l'export %s a expiré ; relancer export_project", catalog.ErrNotFound, rec.ID)
		}
		ttl = left
	}
	path, priv, exp := s.d.Catalog.SignedScopedURL(Scope, Segment, id, ".zip", ttl)
	l := &Link{URL: priv, Path: path, ExpiresAt: exp, FileName: rec.FileName, SizeBytes: rec.SizeBytes}
	if base, public := s.d.Catalog.UploadLinkBase(); public {
		l.URL, l.PrivateURL, l.Public = base+path, priv, true
	}
	return l, nil
}

func readyErr(rec Record) error {
	switch rec.State {
	case StateReady:
		return nil
	case StateExpired:
		return fmt.Errorf("%w : l'export %s a expiré (conservé %s) ; relancer export_project", catalog.ErrNotFound, rec.ID, DefaultRetention)
	case StateFailed:
		return invalidf("l'export %s a échoué : %s", rec.ID, rec.Error)
	default:
		return invalidf("l'export %s n'est pas terminé (état %s) : suivre le travail avec get_job_status", rec.ID, rec.State)
	}
}

// VerifyLink contrôle la signature et l'expiration d'une URL de téléchargement.
func (s *Service) VerifyLink(id, exp, sig string) bool {
	return s.d.Catalog.VerifyScoped(Scope, id, exp, sig)
}

// Open ouvre l'archive d'un export prêt.
func (s *Service) Open(ctx context.Context, id string) (*os.File, Record, error) {
	rec, err := s.Get(ctx, id)
	if err != nil {
		return nil, rec, err
	}
	if err := readyErr(rec); err != nil {
		return nil, rec, err
	}
	f, err := os.Open(s.path(id))
	if err != nil {
		return nil, rec, fmt.Errorf("archive de l'export %s absente : %w", id, catalog.ErrNotFound)
	}
	return f, rec, nil
}

// ---- conservation ----

// expireOld supprime les archives expirées ou en surnombre et marque leur ligne « expired ».
func (s *Service) expireOld(ctx context.Context) {
	now := store.FormatTime(s.now())
	rows, err := s.db().QueryContext(ctx, `SELECT id FROM project_exports WHERE state=? AND (expires_at<=? OR id NOT IN (SELECT id FROM project_exports WHERE state=? ORDER BY created_at DESC, id DESC LIMIT ?))`,
		StateReady, now, StateReady, MaxKept)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		_ = os.Remove(s.path(id))
		_, _ = s.db().ExecContext(ctx, `UPDATE project_exports SET state=? WHERE id=?`, StateExpired, id)
	}
	// lignes anciennes (30 jours après expiration) : oubliées
	cut := store.FormatTime(s.now().Add(-30 * 24 * time.Hour))
	_, _ = s.db().ExecContext(ctx, `DELETE FROM project_exports WHERE state IN (?,?) AND expires_at<?`, StateExpired, StateFailed, cut)
}

// Run purge périodiquement les archives expirées (tâche de fond, arrêtée avec ctx).
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(15 * time.Minute)
	defer t.Stop()
	for {
		s.expireOld(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
