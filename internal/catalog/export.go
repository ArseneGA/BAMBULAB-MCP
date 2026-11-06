package catalog

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// MaxExportModels borne l'export groupé (une archive se construit en flux, mais la durée d'une requête reste limitée).
const MaxExportModels = 200

// ---- téléchargement d'un original ----

// OriginalFile est un fichier source prêt à être servi. Le contenu vient TOUJOURS du stockage par
// empreintes (blobs) : aucun chemin du disque de l'utilisateur ne peut être lu par cette voie.
type OriginalFile struct {
	File contracts.SourceFile
	Body *os.File
}

// OpenOriginal ouvre l'original d'un fichier source du catalogue. Refuse un fichier seulement présent sur
// Windows (non copié) ou dont le modèle est à la corbeille.
func (s *Service) OpenOriginal(ctx context.Context, fileID string) (*OriginalFile, error) {
	f, err := getFile(ctx, s.db(), fileID)
	if err != nil {
		return nil, err
	}
	if err := s.exportable(ctx, f); err != nil {
		return nil, err
	}
	body, err := s.d.Blobs.Open(f.SHA256)
	if err != nil {
		return nil, fmt.Errorf("original de %s absent du stockage : %w", f.Name, ErrNotFound)
	}
	return &OriginalFile{File: f, Body: body}, nil
}

func (s *Service) exportable(ctx context.Context, f contracts.SourceFile) error {
	if f.Availability != contracts.AvailableLinux {
		return fmt.Errorf("%w : %s n'est pas copié sur le serveur (disponible seulement sur Windows)", ErrInvalid, f.Name)
	}
	m, err := getModel(ctx, s.db(), f.ModelID)
	if err != nil {
		return err
	}
	if m.DeletedAt != nil {
		return fmt.Errorf("%w : le modèle %s est à la corbeille", ErrInvalid, m.ID)
	}
	return nil
}

// ---- export groupé ----

// ExportEntry décrit un fichier de l'archive.
type ExportEntry struct {
	ModelID string `json:"model_id"`
	FileID  string `json:"file_id"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size_bytes"`
}

// ExportSkip : fichier laissé de côté, avec la raison.
type ExportSkip struct {
	ModelID string `json:"model_id"`
	FileID  string `json:"file_id,omitempty"`
	Name    string `json:"name,omitempty"`
	Reason  string `json:"reason"`
}

// ExportManifest est écrit dans l'archive (manifest.json).
type ExportManifest struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Note        string        `json:"note"`
	Files       []ExportEntry `json:"files"`
	Skipped     []ExportSkip  `json:"skipped"`
}

type exportItem struct {
	entry ExportEntry
	file  contracts.SourceFile
}

// PlanExport vérifie la demande AVANT d'écrire le moindre octet : modèles connus et hors corbeille, au moins un
// fichier copié exportable. Le code HTTP peut ainsi refuser proprement ; WriteExport ne fait plus que diffuser.
func (s *Service) PlanExport(ctx context.Context, modelIDs []string) (*ExportPlan, error) {
	if len(modelIDs) == 0 {
		return nil, invalidf("model_ids vide : rien à exporter")
	}
	if len(modelIDs) > MaxExportModels {
		return nil, invalidf("trop de modèles (%d, maximum %d)", len(modelIDs), MaxExportModels)
	}
	plan := &ExportPlan{manifest: ExportManifest{Note: "Originaux copiés tels quels depuis le stockage par empreintes ; sha256 vérifié à l'écriture. Le catalogue ne renomme ni ne déplace jamais les fichiers d'origine.", Files: []ExportEntry{}, Skipped: []ExportSkip{}}}
	seenModel := map[string]bool{}
	used := map[string]int{}
	for _, id := range modelIDs {
		if seenModel[id] {
			continue
		}
		seenModel[id] = true
		m, err := getModel(ctx, s.db(), id)
		if err != nil {
			return nil, err
		}
		if m.DeletedAt != nil {
			return nil, invalidf("le modèle %s est à la corbeille", id)
		}
		files, err := queryFiles(ctx, s.db(), `WHERE model_id=?`, id)
		if err != nil {
			return nil, err
		}
		dir := safeName(m.Name, "modele")
		for _, f := range files {
			if f.Availability != contracts.AvailableLinux {
				plan.manifest.Skipped = append(plan.manifest.Skipped, ExportSkip{ModelID: id, FileID: f.ID, Name: f.Name, Reason: "non copié sur le serveur (windows_only)"})
				continue
			}
			if !s.d.Blobs.Has(f.SHA256) {
				plan.manifest.Skipped = append(plan.manifest.Skipped, ExportSkip{ModelID: id, FileID: f.ID, Name: f.Name, Reason: "contenu absent du stockage"})
				continue
			}
			p := dir + "/" + safeName(f.Name, "fichier")
			if n := used[strings.ToLower(p)]; n > 0 { // deux fichiers du même nom : suffixe, jamais d'écrasement
				ext := ""
				base := p
				if i := strings.LastIndex(p, "."); i > strings.LastIndex(p, "/") {
					base, ext = p[:i], p[i:]
				}
				p = fmt.Sprintf("%s (%d)%s", base, n+1, ext)
			}
			used[strings.ToLower(dir+"/"+safeName(f.Name, "fichier"))]++
			e := ExportEntry{ModelID: id, FileID: f.ID, Path: p, SHA256: f.SHA256, Size: f.SizeBytes}
			plan.items = append(plan.items, exportItem{entry: e, file: f})
			plan.manifest.Files = append(plan.manifest.Files, e)
		}
	}
	if len(plan.items) == 0 {
		return nil, invalidf("aucun fichier exportable : les originaux ne sont pas copiés sur le serveur")
	}
	return plan, nil
}

// ExportPlan est une demande d'export validée.
type ExportPlan struct {
	items    []exportItem
	manifest ExportManifest
}

// Count : nombre de fichiers de l'archive (hors manifeste).
func (p *ExportPlan) Count() int { return len(p.items) }

// Skipped : fichiers laissés de côté.
func (p *ExportPlan) Skipped() []ExportSkip { return p.manifest.Skipped }

// WriteExport écrit l'archive zip en flux. Les originaux sont relus depuis le stockage par empreintes et leur sha256
// est recalculé en route : une divergence interrompt l'archive SANS en écrire la fin, de sorte qu'elle est
// inutilisable plutôt que silencieusement fausse.
func (s *Service) WriteExport(ctx context.Context, w io.Writer, plan *ExportPlan) error {
	zw := zip.NewWriter(w)
	for _, it := range plan.items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.zipOne(zw, it); err != nil {
			return err
		}
	}
	plan.manifest.GeneratedAt = time.Now().UTC()
	mw, err := zw.CreateHeader(&zip.FileHeader{Name: "manifest.json", Method: zip.Deflate, Modified: plan.manifest.GeneratedAt})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(mw)
	enc.SetIndent("", "  ")
	if err := enc.Encode(plan.manifest); err != nil {
		return err
	}
	return zw.Close()
}

func (s *Service) zipOne(zw *zip.Writer, it exportItem) error {
	f, err := s.d.Blobs.Open(it.file.SHA256)
	if err != nil {
		return fmt.Errorf("original %s : %w", it.file.Name, err)
	}
	defer f.Close()
	hw, err := zw.CreateHeader(&zip.FileHeader{Name: it.entry.Path, Method: zip.Deflate, Modified: it.file.CreatedAt})
	if err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(hw, h), f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != it.file.SHA256 {
		return fmt.Errorf("%w : l'empreinte de %s ne correspond plus au catalogue", store.ErrBlobCorrupt, it.file.Name)
	}
	return nil
}

// safeName garde un nom lisible et inoffensif pour une entrée d'archive (jamais de séparateur, de « .. » ni de
// caractère de contrôle).
func safeName(s, fallback string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x20 || r == 0x7f, strings.ContainsRune(`/\:*?"<>|`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(b.String()), "."))
	if r := []rune(out); len(r) > 120 {
		out = string(r[:120])
	}
	if out == "" {
		return fallback
	}
	return out
}

// ---- sous-parties ----

// Part est une sous-partie stable d'un modèle : un objet du fichier (3MF/OBJ/STL) ou un corps connexe de cet objet.
// L'identifiant ne dépend que du contenu du fichier source (même fichier = mêmes identifiants).
type Part struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind" jsonschema:"object | component"`
	ParentID  string  `json:"parent_id,omitempty"`
	Label     string  `json:"label"`
	Triangles int     `json:"triangles"`
	VolumeMM3 float64 `json:"volume_mm3"`
	SizeMM    Size3   `json:"size_mm"`
	// GLBNode : nom du nœud dans l'aperçu GLB quand il existe (objets seulement).
	GLBNode string `json:"glb_node,omitempty"`
}

// PartsOutput : sous-parties d'un modèle, d'après l'analyse stockée.
type PartsOutput struct {
	ModelID string `json:"model_id"`
	FileID  string `json:"file_id"`
	State   string `json:"state"`
	Note    string `json:"note,omitempty"`
	Parts   []Part `json:"parts"`
}

// Size3 : dimensions en mm (clés minuscules, comme le résumé d'analyse).
type Size3 struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

func size3(v mesh.Vec3) Size3 { return Size3{X: v.X, Y: v.Y, Z: v.Z} }

// ModelParts liste les sous-parties du fichier courant d'un modèle depuis l'analyse stockée (aucun calcul ici).
func (s *Service) ModelParts(ctx context.Context, modelID string) (*PartsOutput, error) {
	m, f, err := s.resolveFile(ctx, modelID, "")
	if err != nil {
		return nil, err
	}
	out := &PartsOutput{ModelID: m.ID, FileID: f.ID, Parts: []Part{}}
	if f.Availability != contracts.AvailableLinux {
		out.State, out.Note = "unavailable", "fichier non copié sur le serveur : pas d'analyse, donc pas de sous-parties"
		return out, nil
	}
	d, ok, err := getDerived(ctx, s.db(), f.SHA256, KindAnalysis)
	if err != nil {
		return nil, err
	}
	var doc AnalysisDoc
	if !ok || d.State != StateOK || json.Unmarshal(d.Doc, &doc) != nil || doc.Report == nil {
		out.State, out.Note = "pending", "analyse non disponible (en cours ou en échec) : relancer l'analyse depuis la fiche"
		return out, nil
	}
	out.State = StateOK
	for i, o := range doc.Report.Objects {
		id := o.ID
		if id == "" {
			id = fmt.Sprintf("obj%d", i+1)
		}
		label := o.Name
		if label == "" {
			label = "Objet " + id
		}
		if o.Report == nil {
			continue
		}
		out.Parts = append(out.Parts, Part{ID: id, Kind: "object", Label: label, Triangles: o.Report.Triangles, VolumeMM3: o.Report.Volume,
			SizeMM: size3(o.Report.BBox.Size), GLBNode: o.Name})
		if len(o.Report.ComponentList) > 1 {
			for j, c := range o.Report.ComponentList {
				out.Parts = append(out.Parts, Part{ID: fmt.Sprintf("%s#c%d", id, j+1), Kind: "component", ParentID: id,
					Label: fmt.Sprintf("%s · corps %d", label, j+1), Triangles: c.Triangles, VolumeMM3: c.Volume, SizeMM: size3(c.BBox.Size)})
			}
		}
	}
	if len(out.Parts) == 0 {
		out.Note = "aucun objet dans l'analyse"
	}
	return out, nil
}
