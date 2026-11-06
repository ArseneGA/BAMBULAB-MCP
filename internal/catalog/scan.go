package catalog

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

// maxScanEntries borne le nombre d'entrées parcourues (dossier §14.2 : limites).
const maxScanEntries = 500_000

// formatOf déduit le format d'un fichier de son extension.
func formatOf(name string) (contracts.FileFormat, bool) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".stl":
		return contracts.FormatSTL, true
	case ".obj":
		return contracts.FormatOBJ, true
	case ".3mf":
		return contracts.Format3MF, true
	case ".step", ".stp":
		return contracts.FormatSTEP, true
	}
	return "", false
}

// candidate est un fichier supporté trouvé lors d'un parcours.
type candidate struct {
	Abs    string // chemin canonique
	Rel    string // relatif au dossier demandé (séparateur /)
	Format contracts.FileFormat
	Size   int64
	MTime  time.Time
}

type walkResult struct {
	Files       []candidate
	Unsupported map[string]int // extension -> nombre
	Skipped     []string       // liens hors racine, illisibles...
	Truncated   bool
}

// collect parcourt un fichier ou un dossier (lecture seule, sans suivre les
// liens symboliques de dossiers ; un lien de fichier n'est retenu que s'il
// reste sous une racine autorisée).
func (s *Service) collect(ctx context.Context, abs string, recursive bool) (*walkResult, error) {
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	res := &walkResult{Unsupported: map[string]int{}}
	add := func(p, rel string, info fs.FileInfo) {
		fm, ok := formatOf(p)
		if !ok {
			ext := strings.ToLower(filepath.Ext(p))
			if ext == "" {
				ext = "(sans extension)"
			}
			res.Unsupported[ext]++
			return
		}
		res.Files = append(res.Files, candidate{Abs: p, Rel: filepath.ToSlash(rel), Format: fm, Size: info.Size(), MTime: info.ModTime()})
	}
	if !st.IsDir() {
		add(abs, filepath.Base(abs), st)
		return res, nil
	}
	entries := 0
	err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, werr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if werr != nil {
			res.Skipped = append(res.Skipped, p+" : "+werr.Error())
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entries++; entries > maxScanEntries {
			res.Truncated = true
			return fs.SkipAll
		}
		if d.IsDir() {
			if p != abs && (!recursive || d.Name() == ".git" || d.Name() == "node_modules") {
				return fs.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(abs, p)
		file := p
		if d.Type()&fs.ModeSymlink != 0 {
			target, _, aerr := s.authorize(p)
			if aerr != nil {
				res.Skipped = append(res.Skipped, p+" : lien symbolique hors des racines autorisées")
				return nil
			}
			file = target
		}
		info, ierr := os.Stat(file)
		if ierr != nil || !info.Mode().IsRegular() {
			return nil
		}
		add(file, rel, info)
		return nil
	})
	if err != nil && !errors.Is(err, fs.SkipAll) {
		return nil, err
	}
	sort.Slice(res.Files, func(i, j int) bool { return res.Files[i].Rel < res.Files[j].Rel })
	return res, nil
}

// ---- scan_folder ----

// ScanInput : inventaire en lecture seule.
type ScanInput struct {
	Path      string `json:"path" jsonschema:"dossier (ou fichier) absolu à inventorier ; doit se trouver sous une racine d'import autorisée"`
	Recursive *bool  `json:"recursive,omitempty" jsonschema:"descendre dans les sous-dossiers (défaut vrai)"`
	MaxListed int    `json:"max_listed,omitempty" jsonschema:"nombre maximal de fichiers détaillés dans la réponse (défaut 100, max 1000)"`
}

// ScanFile est un fichier inventorié.
type ScanFile struct {
	Path      string `json:"path"`
	RelPath   string `json:"rel_path"`
	Format    string `json:"format"`
	SizeBytes int64  `json:"size_bytes"`
	ModTime   string `json:"mtime"`
	// State : new (jamais importé), changed (importé puis modifié), imported (déjà importé, inchangé).
	State string `json:"state" jsonschema:"new | changed | imported"`
}

// FormatStat : décompte par format.
type FormatStat struct {
	Count int   `json:"count"`
	Bytes int64 `json:"bytes"`
}

// ScanReport : résultat du scan. Rien n'est copié, déplacé ni modifié.
type ScanReport struct {
	Path                  string                `json:"path"`
	Root                  string                `json:"root"`
	Recursive             bool                  `json:"recursive"`
	ReadOnly              bool                  `json:"read_only" jsonschema:"toujours vrai : aucun fichier n'a été copié ni modifié"`
	TotalFiles            int                   `json:"total_files" jsonschema:"fichiers 3D supportés trouvés"`
	TotalBytes            int64                 `json:"total_bytes"`
	ByFormat              map[string]FormatStat `json:"by_format"`
	UnsupportedFiles      int                   `json:"unsupported_files"`
	UnsupportedExtensions map[string]int        `json:"unsupported_extensions,omitempty"`
	NewFiles              int                   `json:"new_files"`
	ChangedFiles          int                   `json:"changed_files"`
	AlreadyImported       int                   `json:"already_imported"`
	BytesToCopy           int64                 `json:"bytes_to_copy" jsonschema:"octets à copier pour importer les fichiers nouveaux ou modifiés (avant dédoublonnage par empreinte)"`
	FreeDiskBytes         *int64                `json:"free_disk_bytes,omitempty" jsonschema:"espace libre sur le volume de stockage du service ; absent si inconnu"`
	EnoughSpace           *bool                 `json:"enough_space,omitempty"`
	Largest               []ScanFile            `json:"largest,omitempty"`
	Files                 []ScanFile            `json:"files"`
	FilesTruncated        bool                  `json:"files_truncated,omitempty"`
	WalkTruncated         bool                  `json:"walk_truncated,omitempty"`
	Skipped               []string              `json:"skipped,omitempty"`
	Notes                 []string              `json:"notes,omitempty"`
}

// ScanFolder inventorie un dossier sans rien copier (dossier §15.4, premier démarrage).
func (s *Service) ScanFolder(ctx context.Context, in ScanInput) (*ScanReport, error) {
	abs, root, err := s.authorize(in.Path)
	if err != nil {
		return nil, err
	}
	recursive := in.Recursive == nil || *in.Recursive
	maxListed := in.MaxListed
	if maxListed <= 0 {
		maxListed = 100
	}
	if maxListed > 1000 {
		maxListed = 1000
	}
	w, err := s.collect(ctx, abs, recursive)
	if err != nil {
		return nil, err
	}
	rep := &ScanReport{Path: abs, Root: root, Recursive: recursive, ReadOnly: true,
		ByFormat: map[string]FormatStat{}, Files: []ScanFile{}, Skipped: w.Skipped, WalkTruncated: w.Truncated}
	if len(w.Unsupported) > 0 {
		rep.UnsupportedExtensions = w.Unsupported
		for _, n := range w.Unsupported {
			rep.UnsupportedFiles += n
		}
	}
	all := make([]ScanFile, 0, len(w.Files))
	for _, c := range w.Files {
		sf := ScanFile{Path: c.Abs, RelPath: c.Rel, Format: string(c.Format), SizeBytes: c.Size,
			ModTime: c.MTime.UTC().Format(time.RFC3339), State: "new"}
		idx, err := indexGet(ctx, s.db(), c.Abs)
		if err != nil {
			return nil, err
		}
		switch {
		case idx == nil:
			rep.NewFiles++
			rep.BytesToCopy += c.Size
		case idx.Size == c.Size && idx.MTime == c.MTime.UnixNano():
			sf.State = "imported"
			rep.AlreadyImported++
		default:
			sf.State = "changed"
			rep.ChangedFiles++
			rep.BytesToCopy += c.Size
		}
		fst := rep.ByFormat[sf.Format]
		fst.Count++
		fst.Bytes += c.Size
		rep.ByFormat[sf.Format] = fst
		rep.TotalFiles++
		rep.TotalBytes += c.Size
		all = append(all, sf)
	}
	big := append([]ScanFile(nil), all...)
	sort.Slice(big, func(i, j int) bool { return big[i].SizeBytes > big[j].SizeBytes })
	if len(big) > 5 {
		big = big[:5]
	}
	rep.Largest = big
	if len(all) > maxListed {
		rep.Files, rep.FilesTruncated = all[:maxListed], true
	} else {
		rep.Files = all
	}
	if free, ok := freeDiskBytes(s.d.DataDir); ok {
		rep.FreeDiskBytes = &free
		enough := free > rep.BytesToCopy+rep.BytesToCopy/10+(64<<20)
		rep.EnoughSpace = &enough
		if !enough {
			rep.Notes = append(rep.Notes, "espace libre insuffisant (marge de 10 % + 64 Mio incluse) pour copier tous les fichiers")
		}
	} else {
		rep.Notes = append(rep.Notes, "espace libre du volume de stockage inconnu sur cette plateforme")
	}
	rep.Notes = append(rep.Notes, "inventaire en lecture seule : aucun fichier copié, déplacé ni modifié ; les doublons exacts ne sont détectés qu'à l'import (empreinte sha256)")
	return rep, nil
}
