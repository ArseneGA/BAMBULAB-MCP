// Package backup sauvegarde, restaure et vérifie le dossier de données de bambud
// (jalon 9).
//
// Une sauvegarde est une archive tar (gzip) contenant : une copie COHÉRENTE de la
// base (VACUUM INTO sur une connexion distincte : le service n'est pas arrêté et
// ses écritures continuent), les blobs (immuables, adressés par contenu), les
// secrets chiffrés (coffre des codes d'accès + clés) et, EN DERNIER, un manifeste
// JSON qui liste chaque fichier avec sa taille et son SHA-256. Rien n'est lu dans
// l'archive avant d'avoir été vérifié contre ce manifeste.
//
// Ordre d'écriture : la base d'abord, les blobs ensuite. Un blob ajouté entre les
// deux est un orphelin inoffensif ; l'ordre inverse pourrait référencer un blob
// absent de l'archive.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pilote "sqlite"
)

// FormatVersion du manifeste.
const FormatVersion = 1

// Chemins fixes dans l'archive.
const (
	pathDB       = "db/bambu.db"
	pathManifest = "manifest.json"
	pathMaster   = "secrets/master.key"
	pathSecrets  = "secrets/printers.secrets"
	pathAssetKey = "assets.key"
	blobsPrefix  = "blobs/"
)

// FileEntry décrit un fichier de l'archive.
type FileEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Manifest est le dernier élément de l'archive.
type Manifest struct {
	Format          int         `json:"format"`
	CreatedAt       string      `json:"created_at"`
	BambudVersion   string      `json:"bambud_version,omitempty"`
	SchemaVersion   int         `json:"schema_version"`
	IncludesSecrets bool        `json:"includes_secrets"`
	BlobCount       int         `json:"blob_count"`
	Files           []FileEntry `json:"files"`
	Warnings        []string    `json:"warnings,omitempty"`
}

// Fichiers de secrets sauvegardés (relatifs au dossier de données).
var secretFiles = []struct{ arc, rel string }{
	{pathMaster, "secrets/master.key"},
	{pathSecrets, "secrets/printers.secrets"},
	{pathAssetKey, "assets.key"},
}

// CreateOptions règle Create.
type CreateOptions struct {
	// ExcludeSecrets omet le coffre et les clés (l'archive ne permet alors pas de retrouver les codes d'accès).
	ExcludeSecrets bool
	// Version de bambud, consignée dans le manifeste.
	Version string
	// Now injectable.
	Now func() time.Time
	// BusyTimeout : attente maximale d'un verrou SQLite (défaut 30 s).
	BusyTimeout time.Duration
}

func validSHA(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// archiveKind déduit la compression de l'extension.
func archiveKind(p string) (gz bool, err error) {
	l := strings.ToLower(p)
	switch {
	case strings.HasSuffix(l, ".tar.gz"), strings.HasSuffix(l, ".tgz"):
		return true, nil
	case strings.HasSuffix(l, ".tar"):
		return false, nil
	case strings.HasSuffix(l, ".zst"), strings.HasSuffix(l, ".tar.zst"):
		return false, errors.New("le format .tar.zst n'est pas pris en charge (aucune dépendance zstd embarquée) : utiliser .tar.gz")
	}
	return false, fmt.Errorf("extension d'archive %q non reconnue : utiliser .tar.gz (ou .tar)", filepath.Base(p))
}

type archiveWriter struct {
	tw    *tar.Writer
	files []FileEntry
	now   time.Time
}

// addBytes ajoute un petit contenu déjà en mémoire.
func (w *archiveWriter) addBytes(name string, b []byte, mode int64) error {
	h := sha256.Sum256(b)
	if err := w.tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(b)), ModTime: w.now, Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
		return err
	}
	if _, err := w.tw.Write(b); err != nil {
		return err
	}
	w.files = append(w.files, FileEntry{Path: name, Size: int64(len(b)), SHA256: hex.EncodeToString(h[:])})
	return nil
}

// addFile copie un fichier en calculant son empreinte. Si wantSHA est non vide, une empreinte
// différente fait échouer (blob corrompu) ; la taille est figée par le stat initial.
func (w *archiveWriter) addFile(name, src string, mode int64, wantSHA string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("%s n'est pas un fichier ordinaire", src)
	}
	if err := w.tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: st.Size(), ModTime: w.now, Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(w.tw, h), io.LimitReader(f, st.Size()))
	if err != nil {
		return err
	}
	if n != st.Size() {
		return fmt.Errorf("%s : fichier tronqué pendant la copie (%d/%d)", src, n, st.Size())
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if wantSHA != "" && sum != wantSHA {
		return fmt.Errorf("blob corrompu : %s a pour contenu %s", wantSHA, sum)
	}
	w.files = append(w.files, FileEntry{Path: name, Size: st.Size(), SHA256: sum})
	return nil
}

// snapshotDB écrit dans dst une copie cohérente de la base (VACUUM INTO) et la vérifie.
func snapshotDB(ctx context.Context, dbPath, dst string, busy time.Duration) (schema int, err error) {
	if _, err := os.Stat(dbPath); err != nil {
		return 0, fmt.Errorf("base introuvable (%s) : %w", dbPath, err)
	}
	// Connexion distincte de celle du service ; lecture seule (aucune migration, aucune écriture dans la base source).
	dsn := "file:" + filepath.ToSlash(dbPath) + "?mode=ro&_pragma=busy_timeout(" + fmt.Sprint(busy.Milliseconds()) + ")"
	if filepath.VolumeName(dbPath) != "" { // file:C:/... -> file:///C:/...
		dsn = "file:///" + filepath.ToSlash(dbPath) + "?mode=ro&_pragma=busy_timeout(" + fmt.Sprint(busy.Milliseconds()) + ")"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		return 0, fmt.Errorf("copie cohérente de la base : %w", err)
	}
	return checkDBFile(ctx, dst)
}

// checkDBFile ouvre une base en lecture seule, vérifie son intégrité et renvoie sa version de schéma.
func checkDBFile(ctx context.Context, p string) (int, error) {
	db, err := openRO(p)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	if err := integrity(ctx, db); err != nil {
		return 0, err
	}
	var v int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_version`).Scan(&v); err != nil {
		return 0, fmt.Errorf("version du schéma illisible : %w", err)
	}
	return v, nil
}

func openRO(p string) (*sql.DB, error) {
	dsn := "file:" + filepath.ToSlash(p) + "?mode=ro&immutable=0&_pragma=busy_timeout(10000)"
	if filepath.VolumeName(p) != "" {
		dsn = "file:///" + filepath.ToSlash(p) + "?mode=ro&immutable=0&_pragma=busy_timeout(10000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func integrity(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return fmt.Errorf("integrity_check : %w", err)
	}
	defer rows.Close()
	var bad []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return err
		}
		if s != "ok" {
			bad = append(bad, s)
			if len(bad) >= 10 {
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(bad) > 0 {
		return fmt.Errorf("base corrompue : %s", strings.Join(bad, " ; "))
	}
	return nil
}

// listBlobs énumère les blobs du magasin (<root>/ab/cd/<sha>), triés ; ignore tmp/ et les noms hors format.
func listBlobs(root string) (shas []string, skipped []string, err error) {
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			if errors.Is(werr, fs.ErrNotExist) {
				return nil
			}
			return werr
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			if rel == "tmp" {
				return filepath.SkipDir
			}
			return nil
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) != 3 || !validSHA(parts[2]) || parts[0] != parts[2][0:2] || parts[1] != parts[2][2:4] {
			skipped = append(skipped, rel)
			return nil
		}
		shas = append(shas, parts[2])
		return nil
	})
	sort.Strings(shas)
	return
}

func blobArcPath(sha string) string { return blobsPrefix + sha[0:2] + "/" + sha[2:4] + "/" + sha }

// Create écrit une sauvegarde de dataDir dans out. Le service peut tourner pendant ce temps.
func Create(ctx context.Context, dataDir, out string, opts CreateOptions) (*Manifest, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.BusyTimeout <= 0 {
		opts.BusyTimeout = 30 * time.Second
	}
	gz, err := archiveKind(out)
	if err != nil {
		return nil, err
	}
	dataDir, err = filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	out, err = filepath.Abs(out)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(dataDir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("dossier de données %q introuvable", dataDir)
	}
	if rel, err := filepath.Rel(dataDir, out); err == nil && !strings.HasPrefix(rel, "..") {
		return nil, fmt.Errorf("l'archive %q ne doit pas être écrite dans le dossier de données", out)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return nil, err
	}
	tmpDir, err := os.MkdirTemp(filepath.Dir(out), ".backup-work-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	// 1. La base, d'abord.
	snap := filepath.Join(tmpDir, "bambu.db")
	schema, err := snapshotDB(ctx, filepath.Join(dataDir, "bambu.db"), snap, opts.BusyTimeout)
	if err != nil {
		return nil, err
	}

	part := out + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	cleanup := func() { f.Close(); os.Remove(part) }
	var gw *gzip.Writer
	whole := sha256.New() // empreinte de l'archive entière (fichier .sha256)
	var sink io.Writer = io.MultiWriter(f, whole)
	if gz {
		gw = gzip.NewWriter(sink)
		sink = gw
	}
	w := &archiveWriter{tw: tar.NewWriter(sink), now: opts.Now().UTC()}
	m := &Manifest{Format: FormatVersion, CreatedAt: w.now.Format(time.RFC3339), BambudVersion: opts.Version, SchemaVersion: schema, IncludesSecrets: !opts.ExcludeSecrets}

	if err := w.addFile(pathDB, snap, 0o600, ""); err != nil {
		cleanup()
		return nil, err
	}
	// 2. Les blobs.
	shas, skipped, err := listBlobs(filepath.Join(dataDir, "blobs"))
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("parcours des blobs : %w", err)
	}
	for _, s := range skipped {
		m.Warnings = append(m.Warnings, "fichier ignoré dans blobs/ (nom hors format) : "+filepath.ToSlash(s))
	}
	for _, sha := range shas {
		if err := ctx.Err(); err != nil {
			cleanup()
			return nil, err
		}
		src := filepath.Join(dataDir, "blobs", sha[0:2], sha[2:4], sha)
		if err := w.addFile(blobArcPath(sha), src, 0o444, sha); err != nil {
			if errors.Is(err, fs.ErrNotExist) { // supprimé pendant la sauvegarde
				m.Warnings = append(m.Warnings, "blob disparu pendant la sauvegarde : "+sha)
				continue
			}
			cleanup()
			return nil, fmt.Errorf("blob %s : %w", sha, err)
		}
		m.BlobCount++
	}
	// 3. Les secrets chiffrés.
	if !opts.ExcludeSecrets {
		for _, s := range secretFiles {
			b, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(s.rel)))
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				cleanup()
				return nil, fmt.Errorf("%s : %w", s.rel, err)
			}
			if err := w.addBytes(s.arc, b, 0o600); err != nil {
				cleanup()
				return nil, err
			}
		}
	}
	// 4. Le manifeste, en dernier.
	m.Files = w.files
	mb, _ := json.MarshalIndent(m, "", "  ")
	if err := w.tw.WriteHeader(&tar.Header{Name: pathManifest, Mode: 0o600, Size: int64(len(mb)), ModTime: w.now, Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
		cleanup()
		return nil, err
	}
	if _, err := w.tw.Write(mb); err != nil {
		cleanup()
		return nil, err
	}
	if err := w.tw.Close(); err != nil {
		cleanup()
		return nil, err
	}
	if gw != nil {
		if err := gw.Close(); err != nil {
			cleanup()
			return nil, err
		}
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return nil, err
	}
	if err := f.Close(); err != nil {
		os.Remove(part)
		return nil, err
	}
	if err := os.Rename(part, out); err != nil {
		os.Remove(part)
		return nil, err
	}
	side := hex.EncodeToString(whole.Sum(nil)) + "  " + filepath.Base(out) + "\n"
	if err := os.WriteFile(out+".sha256", []byte(side), 0o600); err != nil {
		return m, fmt.Errorf("archive écrite mais empreinte .sha256 non écrite : %w", err)
	}
	return m, nil
}

// VerifyFileSHA vérifie l'empreinte d'une archive contre son fichier .sha256 (s'il existe).
func VerifyFileSHA(archive string) (checked bool, err error) {
	b, err := os.ReadFile(archive + ".sha256")
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	want := strings.Fields(string(b))
	if len(want) == 0 || !validSHA(want[0]) {
		return true, errors.New("fichier .sha256 illisible")
	}
	f, err := os.Open(archive)
	if err != nil {
		return true, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return true, err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want[0] {
		return true, fmt.Errorf("empreinte de l'archive différente de %s.sha256 (attendu %s, obtenu %s)", filepath.Base(archive), want[0], got)
	}
	return true, nil
}

// allowedEntry valide le nom d'un élément de l'archive (liste blanche stricte : aucun chemin libre).
func allowedEntry(name string) bool {
	switch name {
	case pathDB, pathManifest, pathMaster, pathSecrets, pathAssetKey:
		return true
	}
	if strings.HasPrefix(name, blobsPrefix) {
		parts := strings.Split(strings.TrimPrefix(name, blobsPrefix), "/")
		return len(parts) == 3 && validSHA(parts[2]) && parts[0] == parts[2][0:2] && parts[1] == parts[2][2:4]
	}
	return false
}

// extract lit l'archive, vérifie chaque fichier contre le manifeste (lu à la fin) et écrit le contenu dans
// staging. Si keepBlobs est faux, les blobs sont lus et vérifiés mais pas écrits (vérification seule).
func extract(ctx context.Context, archive, staging string, keepBlobs bool) (*Manifest, error) {
	if _, err := archiveKind(archive); err != nil {
		return nil, err
	}
	if _, err := VerifyFileSHA(archive); err != nil {
		return nil, err
	}
	f, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r io.Reader = f
	if gz, _ := archiveKind(archive); gz {
		gr, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("archive illisible (gzip) : %w", err)
		}
		defer gr.Close()
		r = gr
	}
	tr := tar.NewReader(r)
	seen := map[string]FileEntry{}
	var man *Manifest
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("archive corrompue : %w", err)
		}
		if h.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("élément %q refusé : type %c (seuls les fichiers ordinaires sont admis)", h.Name, h.Typeflag)
		}
		if !allowedEntry(h.Name) {
			return nil, fmt.Errorf("élément %q refusé : hors de la liste des chemins admis", h.Name)
		}
		if _, dup := seen[h.Name]; dup || (man != nil) {
			return nil, fmt.Errorf("élément %q en double ou après le manifeste", h.Name)
		}
		if h.Name == pathManifest {
			b, err := io.ReadAll(io.LimitReader(tr, 64<<20))
			if err != nil {
				return nil, fmt.Errorf("manifeste illisible : %w", err)
			}
			man = &Manifest{}
			if err := json.Unmarshal(b, man); err != nil {
				return nil, fmt.Errorf("manifeste invalide : %w", err)
			}
			seen[h.Name] = FileEntry{Path: h.Name}
			continue
		}
		var dst io.Writer = io.Discard
		var out *os.File
		if keepBlobs || !strings.HasPrefix(h.Name, blobsPrefix) {
			p := filepath.Join(staging, filepath.FromSlash(h.Name))
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				return nil, err
			}
			if out, err = os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err != nil {
				return nil, err
			}
			dst = out
		}
		hs := sha256.New()
		n, err := io.Copy(io.MultiWriter(dst, hs), tr)
		if out != nil {
			if cerr := out.Close(); err == nil {
				err = cerr
			}
		}
		if err != nil {
			return nil, fmt.Errorf("lecture de %q : %w", h.Name, err)
		}
		seen[h.Name] = FileEntry{Path: h.Name, Size: n, SHA256: hex.EncodeToString(hs.Sum(nil))}
	}
	if man == nil {
		return nil, errors.New("archive incomplète : manifeste absent (sauvegarde interrompue ou tronquée)")
	}
	if man.Format != FormatVersion {
		return nil, fmt.Errorf("format de manifeste %d non pris en charge", man.Format)
	}
	// Comparaison fichier par fichier, dans les deux sens.
	declared := map[string]FileEntry{}
	for _, e := range man.Files {
		declared[e.Path] = e
	}
	for _, e := range man.Files {
		got, ok := seen[e.Path]
		if !ok {
			return nil, fmt.Errorf("fichier du manifeste absent de l'archive : %s", e.Path)
		}
		if got.Size != e.Size || got.SHA256 != e.SHA256 {
			return nil, fmt.Errorf("empreinte ou taille différente du manifeste : %s", e.Path)
		}
	}
	for name := range seen {
		if name == pathManifest {
			continue
		}
		if _, ok := declared[name]; !ok {
			return nil, fmt.Errorf("fichier absent du manifeste : %s", name)
		}
	}
	if _, ok := declared[pathDB]; !ok {
		return nil, errors.New("archive sans base de données")
	}
	return man, nil
}

// referencedBlobs renvoie les empreintes de blobs référencées par la base (fichiers sources présents sur le serveur,
// dérivés, artefacts de candidats, aperçus).
func referencedBlobs(ctx context.Context, db *sql.DB) (shas map[string]string, warns []string) {
	shas = map[string]string{}
	for _, q := range []struct{ what, sql string }{
		{"fichier source", `SELECT sha256 FROM source_files WHERE availability='linux'`},
		{"dérivé", `SELECT blob_sha FROM derived_assets WHERE blob_sha IS NOT NULL AND blob_sha<>''`},
		{"artefact de candidat", `SELECT artifact_sha FROM experiment_candidates WHERE artifact_sha IS NOT NULL AND artifact_sha<>''`},
		{"aperçu", `SELECT sha256 FROM print_previews`},
		{"photo caméra", `SELECT sha256 FROM camera_photos`},
	} {
		rows, err := db.QueryContext(ctx, q.sql)
		if err != nil {
			warns = append(warns, fmt.Sprintf("références %s non vérifiées : %v", q.what, err))
			continue
		}
		for rows.Next() {
			var s string
			if rows.Scan(&s) == nil && validSHA(s) {
				if _, ok := shas[s]; !ok {
					shas[s] = q.what
				}
			}
		}
		rows.Close()
	}
	return
}

// VerifyReport est le résultat d'une vérification.
type VerifyReport struct {
	Kind          string   `json:"kind"` // "archive" | "data-dir"
	SchemaVersion int      `json:"schema_version"`
	Blobs         int      `json:"blobs"`
	BlobBytes     int64    `json:"blob_bytes"`
	References    int      `json:"references"`
	Problems      []string `json:"problems,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
}

// OK indique l'absence de problème.
func (r *VerifyReport) OK() bool { return len(r.Problems) == 0 }

// VerifyArchive relit toute l'archive : empreinte globale (.sha256), manifeste, empreinte de chaque fichier,
// intégrité de la base et blobs référencés présents.
func VerifyArchive(ctx context.Context, archive string) (*VerifyReport, *Manifest, error) {
	tmp, err := os.MkdirTemp(filepath.Dir(archive), ".verify-")
	if err != nil {
		tmp, err = os.MkdirTemp("", "bambu-verify-")
		if err != nil {
			return nil, nil, err
		}
	}
	defer os.RemoveAll(tmp)
	m, err := extract(ctx, archive, tmp, false)
	if err != nil {
		return nil, nil, err
	}
	rep := &VerifyReport{Kind: "archive", SchemaVersion: m.SchemaVersion, Warnings: m.Warnings}
	have := map[string]bool{}
	for _, e := range m.Files {
		if strings.HasPrefix(e.Path, blobsPrefix) {
			have[path.Base(e.Path)] = true
			rep.Blobs++
			rep.BlobBytes += e.Size
		}
	}
	dbp := filepath.Join(tmp, filepath.FromSlash(pathDB))
	db, err := openRO(dbp)
	if err != nil {
		return nil, m, err
	}
	defer db.Close()
	if err := integrity(ctx, db); err != nil {
		rep.Problems = append(rep.Problems, err.Error())
		return rep, m, nil
	}
	refs, warns := referencedBlobs(ctx, db)
	rep.Warnings = append(rep.Warnings, warns...)
	rep.References = len(refs)
	for sha, what := range refs {
		if !have[sha] {
			rep.Problems = append(rep.Problems, fmt.Sprintf("blob référencé (%s) absent de l'archive : %s", what, sha))
		}
	}
	sort.Strings(rep.Problems)
	return rep, m, nil
}

// VerifyDataDir contrôle un dossier de données vivant (lecture seule) : intégrité de la base, clés étrangères,
// empreinte de chaque blob (sauf quick), blobs référencés présents.
func VerifyDataDir(ctx context.Context, dataDir string, quick bool) (*VerifyReport, error) {
	rep := &VerifyReport{Kind: "data-dir"}
	dbp := filepath.Join(dataDir, "bambu.db")
	if _, err := os.Stat(dbp); err != nil {
		return nil, fmt.Errorf("base introuvable (%s) : %w", dbp, err)
	}
	db, err := openRO(dbp)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := integrity(ctx, db); err != nil {
		rep.Problems = append(rep.Problems, err.Error())
		return rep, nil
	}
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_version`).Scan(&rep.SchemaVersion); err != nil {
		rep.Problems = append(rep.Problems, "version du schéma illisible : "+err.Error())
	}
	if rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`); err == nil {
		n := 0
		for rows.Next() {
			n++
		}
		rows.Close()
		if n > 0 {
			rep.Problems = append(rep.Problems, fmt.Sprintf("%d violation(s) de clé étrangère", n))
		}
	}
	root := filepath.Join(dataDir, "blobs")
	shas, skipped, err := listBlobs(root)
	if err != nil {
		return nil, fmt.Errorf("parcours des blobs : %w", err)
	}
	for _, s := range skipped {
		rep.Warnings = append(rep.Warnings, "fichier hors format dans blobs/ : "+filepath.ToSlash(s))
	}
	have := map[string]bool{}
	for _, sha := range shas {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p := filepath.Join(root, sha[0:2], sha[2:4], sha)
		st, err := os.Stat(p)
		if err != nil {
			rep.Problems = append(rep.Problems, fmt.Sprintf("blob illisible %s : %v", sha, err))
			continue
		}
		rep.Blobs++
		rep.BlobBytes += st.Size()
		have[sha] = true
		if quick {
			continue
		}
		if err := hashMatches(p, sha); err != nil {
			rep.Problems = append(rep.Problems, fmt.Sprintf("blob %s : %v", sha, err))
			delete(have, sha)
		}
	}
	refs, warns := referencedBlobs(ctx, db)
	rep.Warnings = append(rep.Warnings, warns...)
	rep.References = len(refs)
	for sha, what := range refs {
		if !have[sha] {
			rep.Problems = append(rep.Problems, fmt.Sprintf("blob référencé (%s) absent ou corrompu : %s", what, sha))
		}
	}
	sort.Strings(rep.Problems)
	return rep, nil
}

func hashMatches(p, want string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("corrompu (contenu %s)", got)
	}
	return nil
}

// RestoreOptions règle Restore.
type RestoreOptions struct {
	// Force autorise un dossier non vide : son contenu connu est déplacé dans .pre-restore-<date>/ (jamais supprimé).
	Force bool
	// MaxSchema : version de schéma maximale que le binaire sait ouvrir (0 : pas de contrôle).
	MaxSchema int
	Now       func() time.Time
}

// restoredItems : éléments du dossier de données gérés par la restauration (déplacés avec --force).
var restoredItems = []string{"bambu.db", "bambu.db-wal", "bambu.db-shm", "blobs", "secrets", "assets.key"}

// ignoredForEmpty : fichiers qui ne rendent pas un dossier « non vide ».
func ignoredForEmpty(name string) bool { return name == ".bambud.lock" }

// Restore restaure l'archive dans dataDir. L'archive est entièrement vérifiée dans un dossier de travail avant
// toute modification de dataDir.
func Restore(ctx context.Context, archive, dataDir string, opts RestoreOptions) (*Manifest, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	dataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(dataDir)
	if err != nil {
		return nil, err
	}
	var present []string
	for _, e := range ents {
		if !ignoredForEmpty(e.Name()) {
			present = append(present, e.Name())
		}
	}
	if len(present) > 0 && !opts.Force {
		return nil, fmt.Errorf("le dossier %q n'est pas vide (%d élément(s)) : choisir un dossier neuf ou ajouter --force (le contenu actuel est alors conservé dans .pre-restore-<date>/)", dataDir, len(present))
	}
	staging, err := os.MkdirTemp(dataDir, ".restore-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	m, err := extract(ctx, archive, staging, true)
	if err != nil {
		return nil, err
	}
	schema, err := checkDBFile(ctx, filepath.Join(staging, filepath.FromSlash(pathDB)))
	if err != nil {
		return nil, fmt.Errorf("base de l'archive : %w", err)
	}
	if opts.MaxSchema > 0 && schema > opts.MaxSchema {
		return nil, fmt.Errorf("la base archivée est en version %d, plus récente que ce binaire (%d) : mettre bambud à jour avant de restaurer", schema, opts.MaxSchema)
	}
	// Mise à l'écart de l'existant (--force) : tout ce que la restauration remplacerait, déplacé et non supprimé.
	if len(present) > 0 {
		aside := filepath.Join(dataDir, ".pre-restore-"+opts.Now().UTC().Format("20060102T150405Z"))
		if err := os.MkdirAll(aside, 0o700); err != nil {
			return nil, err
		}
		for _, name := range restoredItems {
			src := filepath.Join(dataDir, name)
			if _, err := os.Lstat(src); err != nil {
				continue
			}
			if err := os.Rename(src, filepath.Join(aside, name)); err != nil {
				return nil, fmt.Errorf("mise à l'écart de %s : %w", name, err)
			}
		}
	}
	// Mise en place : base, blobs, secrets.
	if err := os.Rename(filepath.Join(staging, "db", "bambu.db"), filepath.Join(dataDir, "bambu.db")); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(staging, "blobs")); err == nil {
		if err := os.Rename(filepath.Join(staging, "blobs"), filepath.Join(dataDir, "blobs")); err != nil {
			return nil, err
		}
	}
	for _, e := range restoredBlobFiles(filepath.Join(dataDir, "blobs")) {
		_ = os.Chmod(e, 0o444)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "blobs", "tmp"), 0o755); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(staging, "secrets")); err == nil {
		if err := os.Rename(filepath.Join(staging, "secrets"), filepath.Join(dataDir, "secrets")); err != nil {
			return nil, err
		}
		_ = os.Chmod(filepath.Join(dataDir, "secrets"), 0o700)
	}
	if _, err := os.Stat(filepath.Join(staging, "assets.key")); err == nil {
		if err := os.Rename(filepath.Join(staging, "assets.key"), filepath.Join(dataDir, "assets.key")); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func restoredBlobFiles(root string) (files []string) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	return
}
