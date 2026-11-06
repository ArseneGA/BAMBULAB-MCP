package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
)

const actorImport = "import"

// ImportInput décrit un import non destructif : les originaux ne sont jamais
// déplacés ni modifiés ; les fichiers sont copiés dans le magasin de blobs.
type ImportInput struct {
	Paths         []string `json:"paths" jsonschema:"fichiers ou dossiers absolus à importer ; chacun doit se trouver sous une racine d'import autorisée"`
	Collection    string   `json:"collection,omitempty" jsonschema:"collection de destination (nom ou identifiant) ; absente = nom du dossier importé"`
	Copy          *bool    `json:"copy,omitempty" jsonschema:"copier dans le stockage du service (défaut vrai) ; faux = indexer seulement, les fichiers sont alors marqués windows_only (non disponibles hors ligne)"`
	OnDuplicate   string   `json:"on_duplicate,omitempty" jsonschema:"skip (défaut : un contenu déjà présent n'est pas ré-importé) | keep (crée un second modèle partageant le même blob)"`
	Recursive     *bool    `json:"recursive,omitempty" jsonschema:"descendre dans les sous-dossiers (défaut vrai)"`
	KeepStructure *bool    `json:"keep_structure,omitempty" jsonschema:"reproduire les sous-dossiers en collections imbriquées (défaut vrai)"`
	Tags          []string `json:"tags,omitempty" jsonschema:"étiquettes ajoutées aux modèles créés"`
	License       string   `json:"license,omitempty" jsonschema:"licence des modèles créés (vide = inconnue)"`
	Source        string   `json:"source,omitempty" jsonschema:"provenance déclarée (ex. nom du site ou de l'auteur)"`
	Unit          string   `json:"unit,omitempty" jsonschema:"unité des coordonnées si le fichier n'en porte pas : mm | cm | m | inch (vide = inconnue, millimètres supposés et signalés)"`
	Name          string   `json:"name,omitempty" jsonschema:"nom du modèle (seulement si un seul fichier est importé)"`
	WaitSecs      int      `json:"wait_seconds,omitempty" jsonschema:"attendre la fin de l'import jusqu'à ce délai en secondes (défaut 0 : rendre le job_id immédiatement ; max 120)"`
}

func (in ImportInput) copyFiles() bool     { return in.Copy == nil || *in.Copy }
func (in ImportInput) recursive() bool     { return in.Recursive == nil || *in.Recursive }
func (in ImportInput) keepStructure() bool { return in.KeepStructure == nil || *in.KeepStructure }
func (in ImportInput) onDuplicate() string { return in.OnDuplicate }

// Statuts d'un fichier importé.
const (
	StatusImported   = "imported"    // nouveau modèle
	StatusNewVersion = "new_version" // fichier modifié depuis le dernier import : nouvelle version
	StatusDuplicate  = "duplicate"   // contenu déjà présent (doublon exact), non ré-importé
	StatusUnchanged  = "unchanged"   // même chemin, même taille, même mtime (ou même contenu)
	StatusIndexed    = "indexed"     // indexé sans copie : windows_only
	StatusUpgraded   = "upgraded"    // fichier windows_only désormais copié
	StatusRejected   = "rejected"    // format invalide, trop gros...
	StatusError      = "error"
)

// FileResult : issue de l'import d'un fichier.
type FileResult struct {
	Path         string `json:"path"`
	RelPath      string `json:"rel_path,omitempty"`
	Status       string `json:"status"`
	Format       string `json:"format,omitempty"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
	ModelID      string `json:"model_id,omitempty"`
	SourceFileID string `json:"source_file_id,omitempty"`
	DuplicateOf  string `json:"duplicate_of_model_id,omitempty"`
	Availability string `json:"availability,omitempty"`
	Note         string `json:"note,omitempty"`
}

// ImportReport : résultat d'un travail d'import.
type ImportReport struct {
	Counts         map[string]int `json:"counts"`
	BytesCopied    int64          `json:"bytes_copied"`
	Total          int            `json:"total"`
	Files          []FileResult   `json:"files"`
	FilesTruncated bool           `json:"files_truncated,omitempty"`
	Skipped        []string       `json:"skipped,omitempty"`
}

const maxReportFiles = 1000

func normalizeUnit(u string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(u)) {
	case "":
		return "", nil
	case "mm", "millimeter", "millimetre":
		return "mm", nil
	case "cm", "centimeter", "centimetre":
		return "cm", nil
	case "m", "meter", "metre":
		return "m", nil
	case "in", "inch", "inches", "pouce", "pouces":
		return "inch", nil
	}
	return "", invalidf("unité inconnue %q (mm | cm | m | inch)", u)
}

// unitScale : facteur vers les millimètres.
func unitScale(u string) float64 {
	switch u {
	case "cm":
		return 10
	case "m":
		return 1000
	case "inch":
		return 25.4
	}
	return 1
}

// StartImport valide les chemins (refus immédiat hors racines) puis met l'import
// en file. Le travail est rejouable : un import interrompu reprend sans doublon
// (import incrémental).
func (s *Service) StartImport(ctx context.Context, in ImportInput) (contracts.Job, error) {
	if len(in.Paths) == 0 {
		return contracts.Job{}, invalidf("au moins un chemin requis")
	}
	if len(in.Paths) > 1000 {
		return contracts.Job{}, invalidf("trop de chemins (max 1000)")
	}
	switch in.OnDuplicate {
	case "", "skip", "keep":
	default:
		return contracts.Job{}, invalidf("on_duplicate : skip | keep")
	}
	u, err := normalizeUnit(in.Unit)
	if err != nil {
		return contracts.Job{}, err
	}
	in.Unit = u
	if in.Name != "" && len(in.Paths) != 1 {
		return contracts.Job{}, invalidf("name n'a de sens que pour un seul chemin")
	}
	resolved := make([]string, 0, len(in.Paths))
	for _, p := range in.Paths {
		abs, _, err := s.authorize(p)
		if err != nil {
			return contracts.Job{}, err
		}
		resolved = append(resolved, abs)
	}
	in.Paths = resolved
	in.Tags = normTags(in.Tags)
	params, _ := json.Marshal(in)
	j, _, err := s.d.Queue.Enqueue(ctx, jobs.EnqueueParams{Kind: JobImport, Class: contracts.ClassLight, Params: params})
	return j, err
}

func (s *Service) runImportJob(ctx context.Context, jc *jobs.JobCtx) (json.RawMessage, error) {
	var in ImportInput
	if err := json.Unmarshal(jc.Job.Params, &in); err != nil {
		return nil, &jobs.HandlerError{Code: "bad_params", Err: err}
	}
	rep, err := s.RunImport(ctx, in, func(done, total int, msg string) {
		if total > 0 {
			jc.Progress(float64(done)/float64(total), msg)
		}
	}, jc.Logf)
	if err != nil {
		code := "import_failed"
		switch {
		case isNotAllowed(err):
			code = "path_not_allowed"
		case ctx.Err() != nil:
			return nil, ctx.Err()
		}
		return nil, &jobs.HandlerError{Code: code, Err: err}
	}
	jc.Logf("import terminé : %v", rep.Counts)
	return json.Marshal(rep)
}

func isNotAllowed(err error) bool { return errors.Is(err, ErrNotAllowed) }

// RunImport exécute l'import (appelé par le travail ; exporté pour les tests et la démo).
func (s *Service) RunImport(ctx context.Context, in ImportInput, progress func(done, total int, msg string), logf func(format string, args ...any)) (*ImportReport, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if progress == nil {
		progress = func(int, int, string) {}
	}
	type item struct {
		c       candidate
		top     string   // nom de collection racine ("" = aucune)
		subdirs []string // sous-dossiers relatifs
	}
	rep := &ImportReport{Counts: map[string]int{}, Files: []FileResult{}}
	var items []item
	for _, p := range in.Paths {
		abs, _, err := s.authorize(p) // revalidation : les racines ont pu changer entre la mise en file et l'exécution
		if err != nil {
			return nil, err
		}
		st, err := os.Stat(abs)
		if err != nil {
			return nil, err
		}
		w, err := s.collect(ctx, abs, in.recursive())
		if err != nil {
			return nil, err
		}
		rep.Skipped = append(rep.Skipped, w.Skipped...)
		top := in.Collection
		if top == "" && st.IsDir() {
			top = filepath.Base(abs)
		}
		for _, c := range w.Files {
			var sub []string
			if st.IsDir() && in.keepStructure() {
				if d := path.Dir(c.Rel); d != "." {
					sub = strings.Split(d, "/")
				}
			}
			items = append(items, item{c: c, top: top, subdirs: sub})
		}
	}
	rep.Total = len(items)
	if in.Name != "" && len(items) != 1 {
		return nil, invalidf("name n'a de sens que pour un seul fichier")
	}
	logf("%d fichier(s) à traiter", len(items))
	for i, it := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fr := s.importOne(ctx, in, it.c, it.top, it.subdirs)
		rep.Counts[fr.Status]++
		if fr.Status == StatusImported || fr.Status == StatusNewVersion || fr.Status == StatusUpgraded {
			rep.BytesCopied += fr.SizeBytes
		}
		if fr.Status == StatusError || fr.Status == StatusRejected {
			logf("%s : %s — %s", fr.Status, fr.Path, fr.Note)
		}
		if len(rep.Files) < maxReportFiles {
			rep.Files = append(rep.Files, fr)
		} else {
			rep.FilesTruncated = true
		}
		progress(i+1, len(items), fmt.Sprintf("%d/%d %s", i+1, len(items), filepath.Base(it.c.Abs)))
		if s.testHook != nil {
			s.testHook(i + 1)
		}
	}
	return rep, nil
}

func magicOK(f *os.File, format contracts.FileFormat) bool {
	head := make([]byte, 4096)
	n, _ := f.Read(head)
	head = head[:n]
	_, _ = f.Seek(0, io.SeekStart)
	switch format {
	case contracts.Format3MF:
		return bytes.HasPrefix(head, []byte("PK\x03\x04")) || bytes.HasPrefix(head, []byte("PK\x05\x06"))
	case contracts.FormatSTEP:
		return bytes.Contains(bytes.ToUpper(head), []byte("ISO-10303-21"))
	}
	return true
}

func hashFile(f *os.File) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	_, _ = f.Seek(0, io.SeekStart)
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *Service) importOne(ctx context.Context, in ImportInput, c candidate, top string, subdirs []string) FileResult {
	fr := FileResult{Path: c.Abs, RelPath: c.Rel, Format: string(c.Format), SizeBytes: c.Size}
	fail := func(status, note string, err error) FileResult {
		fr.Status, fr.Note = status, note
		if err != nil {
			fr.Note = note + " : " + err.Error()
		}
		return fr
	}
	copyFiles := in.copyFiles()
	if c.Size > s.cfg.MaxFileBytes {
		return fail(StatusRejected, fmt.Sprintf("fichier trop gros (%d octets > limite %d)", c.Size, s.cfg.MaxFileBytes), nil)
	}
	mtime := c.MTime.UnixNano()

	// Import incrémental : même chemin, même taille, même mtime = inchangé, sans relire le fichier.
	idx, err := indexGet(ctx, s.db(), c.Abs)
	if err != nil {
		return fail(StatusError, "index d'import", err)
	}
	if idx != nil && idx.Size == c.Size && idx.MTime == mtime && (idx.Copied || !copyFiles) {
		fr.Status, fr.SHA256, fr.SourceFileID = StatusUnchanged, idx.SHA, idx.SourceFileID
		if f, err := getFile(ctx, s.db(), idx.SourceFileID); err == nil {
			fr.ModelID, fr.Availability = f.ModelID, string(f.Availability)
			if m, err := getModel(ctx, s.db(), f.ModelID); err == nil && m.DeletedAt != nil {
				fr.Note = "le modèle est en corbeille (restore_from_trash pour le récupérer)"
			}
			s.ensureDerived(ctx, f)
		}
		return fr
	}

	f, err := os.Open(c.Abs)
	if err != nil {
		return fail(StatusError, "ouverture", err)
	}
	defer f.Close()
	if !magicOK(f, c.Format) {
		return fail(StatusRejected, fmt.Sprintf("le contenu ne correspond pas au format %s", c.Format), nil)
	}
	var sha string
	if copyFiles {
		var size int64
		sha, size, err = s.d.Blobs.Put(f, "")
		if err != nil {
			return fail(StatusError, "copie", err)
		}
		// Vérification d'empreinte après copie : relecture du blob écrit.
		if err := s.d.Blobs.Verify(sha); err != nil {
			return fail(StatusError, "vérification après copie", err)
		}
		fr.SizeBytes = size
	} else {
		if sha, err = hashFile(f); err != nil {
			return fail(StatusError, "empreinte", err)
		}
	}
	// Le fichier ne doit pas avoir changé pendant la lecture.
	if st2, err := os.Stat(c.Abs); err != nil || st2.Size() != fr.SizeBytes || st2.ModTime().UnixNano() != mtime {
		return fail(StatusError, "le fichier a été modifié pendant la lecture ; réessayer", nil)
	}
	fr.SHA256 = sha

	var created []contracts.SourceFile // fichiers à dériver après commit
	err = s.d.Store.Tx(ctx, func(tx *sql.Tx) error {
		created = nil
		au := s.auditor(ctx, tx, actorImport, "source_file")
		idx, err := indexGet(ctx, tx, c.Abs)
		if err != nil {
			return err
		}
		now := s.d.Store.Now()
		avail := contracts.AvailableWindowsOnly
		if copyFiles {
			avail = contracts.AvailableLinux
		}
		fr.Availability = string(avail)
		upgrade := func() (int64, error) {
			if !copyFiles {
				return 0, nil
			}
			res, err := tx.ExecContext(ctx, `UPDATE source_files SET availability='linux' WHERE sha256=? AND availability='windows_only'`, sha)
			if err != nil {
				return 0, err
			}
			n, _ := res.RowsAffected()
			return n, nil
		}
		newFile := func(modelID string) contracts.SourceFile {
			origin := c.Abs
			mt := c.MTime.UTC()
			return contracts.SourceFile{ID: contracts.NewID("src"), ModelID: modelID, Name: filepath.Base(c.Abs), Format: c.Format,
				SizeBytes: c.Size, SHA256: sha, Availability: avail, OriginPath: &origin, CreatedAt: now,
				RelPath: c.Rel, OriginMTime: &mt, Unit: in.Unit}
		}
		entry := indexEntry{Path: c.Abs, Size: c.Size, MTime: mtime, SHA: sha, Copied: copyFiles}

		switch {
		// Même contenu qu'au dernier import (seul le mtime a changé, ou passage windows_only -> copié).
		case idx != nil && idx.SHA == sha:
			n, err := upgrade()
			if err != nil {
				return err
			}
			entry.SourceFileID, entry.Copied = idx.SourceFileID, idx.Copied || copyFiles
			if err := s.indexPut(ctx, tx, entry); err != nil {
				return err
			}
			of, err := getFile(ctx, tx, idx.SourceFileID)
			if err != nil {
				return err
			}
			fr.ModelID, fr.SourceFileID, fr.Status = of.ModelID, of.ID, StatusUnchanged
			if n > 0 {
				fr.Status = StatusUpgraded
				if err := au("source_file.upgrade", of.ID, map[string]any{"sha256": sha}); err != nil {
					return err
				}
			}
			fr.Availability = string(effectiveAvail(of, copyFiles))
			if m, err := getModel(ctx, tx, of.ModelID); err == nil && m.DeletedAt != nil {
				fr.Note = "le modèle est en corbeille (restore_from_trash pour le récupérer)"
			}
			created = append(created, of)
			return nil

		// Contenu modifié depuis le dernier import : nouvelle version du même modèle.
		case idx != nil:
			old, err := getFile(ctx, tx, idx.SourceFileID)
			if err != nil {
				return err
			}
			nf := newFile(old.ModelID)
			if err := insertFile(ctx, tx, nf); err != nil {
				return err
			}
			vs, err := queryVersions(ctx, tx, old.ModelID)
			if err != nil {
				return err
			}
			var parent *string
			if len(vs) > 0 {
				parent = &vs[0].ID
			}
			v := contracts.ModelVersion{ID: contracts.NewID("ver"), ModelID: old.ModelID, ParentID: parent,
				Label: fmt.Sprintf("v%d", len(vs)+1), Note: "fichier modifié depuis le dernier import", SourceFileIDs: []string{nf.ID}, CreatedAt: now}
			if err := insertVersion(ctx, tx, v); err != nil {
				return err
			}
			if err := touchModel(ctx, tx, old.ModelID, now); err != nil {
				return err
			}
			entry.SourceFileID = nf.ID
			if err := s.indexPut(ctx, tx, entry); err != nil {
				return err
			}
			if _, err := upgrade(); err != nil {
				return err
			}
			if err := au("source_file.add", nf.ID, map[string]any{"model_id": old.ModelID, "sha256": sha, "version": v.Label}); err != nil {
				return err
			}
			fr.Status, fr.ModelID, fr.SourceFileID = StatusNewVersion, old.ModelID, nf.ID
			fr.Note = "contenu modifié : version " + v.Label + " (l'ancienne reste disponible)"
			created = append(created, nf)
			return nil
		}

		// Chemin inconnu : doublon exact éventuel (modèles en corbeille ignorés).
		if in.onDuplicate() != "keep" {
			dups, err := queryFiles(ctx, tx, `WHERE sha256=? AND model_id IN (SELECT id FROM models WHERE deleted_at IS NULL)`, sha)
			if err != nil {
				return err
			}
			if len(dups) > 0 {
				d := dups[0]
				n, err := upgrade()
				if err != nil {
					return err
				}
				entry.SourceFileID = d.ID
				if err := s.indexPut(ctx, tx, entry); err != nil {
					return err
				}
				fr.Status, fr.ModelID, fr.SourceFileID, fr.DuplicateOf = StatusDuplicate, d.ModelID, d.ID, d.ModelID
				fr.Availability = string(effectiveAvail(d, copyFiles))
				fr.Note = "doublon exact d'un fichier déjà au catalogue : rien n'a été ajouté"
				if n > 0 {
					fr.Note += " (le fichier existant est maintenant copié sur le service)"
					_ = au("source_file.upgrade", d.ID, map[string]any{"sha256": sha})
				}
				created = append(created, d)
				return nil
			}
		}

		// Nouveau modèle.
		var colID *string
		if top != "" {
			cid, err := s.resolveOrCreateCollection(ctx, tx, in, top)
			if err != nil {
				return err
			}
			for _, sd := range subdirs {
				if cid, err = s.ensureCollection(ctx, tx, actorImport, s.auditor(ctx, tx, actorImport, "collection"), &cid, sd); err != nil {
					return err
				}
			}
			colID = &cid
		}
		name := strings.TrimSuffix(filepath.Base(c.Abs), filepath.Ext(c.Abs))
		if in.Name != "" {
			name = in.Name
		}
		m := contracts.Model{ID: contracts.NewID("mdl"), CollectionID: colID, Name: name, Tags: normTags(in.Tags), CreatedAt: now, UpdatedAt: now,
			License:    strings.TrimSpace(in.License),
			Provenance: &contracts.Provenance{Source: firstNonEmpty(in.Source, "import de fichier"), OriginPath: c.Abs, ImportedAt: now}}
		if err := insertModel(ctx, tx, m); err != nil {
			return err
		}
		nf := newFile(m.ID)
		if err := insertFile(ctx, tx, nf); err != nil {
			return err
		}
		v := contracts.ModelVersion{ID: contracts.NewID("ver"), ModelID: m.ID, Label: "v1", Note: "import initial", SourceFileIDs: []string{nf.ID}, CreatedAt: now}
		if err := insertVersion(ctx, tx, v); err != nil {
			return err
		}
		entry.SourceFileID = nf.ID
		if err := s.indexPut(ctx, tx, entry); err != nil {
			return err
		}
		if _, err := upgrade(); err != nil {
			return err
		}
		if err := s.d.Store.AuditTx(ctx, tx, actorImport, "model.create", "model", m.ID, map[string]any{"name": m.Name, "origin_path": c.Abs}); err != nil {
			return err
		}
		if err := au("source_file.add", nf.ID, map[string]any{"model_id": m.ID, "sha256": sha, "availability": string(avail)}); err != nil {
			return err
		}
		fr.Status, fr.ModelID, fr.SourceFileID = StatusImported, m.ID, nf.ID
		if !copyFiles {
			fr.Status = StatusIndexed
			fr.Note = "indexé sans copie : disponible uniquement sur Windows (windows_only), hors des capacités hors ligne"
		}
		created = append(created, nf)
		return nil
	})
	if err != nil {
		return fail(StatusError, "enregistrement", err)
	}
	for _, nf := range created {
		s.ensureDerived(ctx, nf)
	}
	return fr
}

func effectiveAvail(f contracts.SourceFile, copied bool) contracts.Availability {
	if copied {
		return contracts.AvailableLinux
	}
	return f.Availability
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// resolveOrCreateCollection : identifiant existant, sinon nom (racine) créé au besoin.
func (s *Service) resolveOrCreateCollection(ctx context.Context, tx *sql.Tx, in ImportInput, name string) (string, error) {
	if strings.HasPrefix(name, "col_") {
		var id string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM collections WHERE id=?`, name).Scan(&id); err == nil {
			return id, nil
		}
	}
	return s.ensureCollection(ctx, tx, actorImport, s.auditor(ctx, tx, actorImport, "collection"), nil, name)
}
