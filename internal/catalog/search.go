package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

// États d'un aperçu GLB vu du catalogue.
const (
	PreviewReady          = "ready"
	PreviewPending        = "pending"
	PreviewNone           = "none"
	PreviewWindowsOnly    = "windows_only"
	PreviewBackendMissing = "backend_missing"
	PreviewError          = "error"
)

// PreviewRef référence l'aperçu GLB d'un fichier (jamais le GLB lui-même).
type PreviewRef struct {
	State  string `json:"state" jsonschema:"ready | pending | none | windows_only | backend_missing | error"`
	SHA256 string `json:"sha256,omitempty" jsonschema:"empreinte du GLB dérivé"`
	URL    string `json:"url,omitempty" jsonschema:"URL HTTP locale signée du GLB (expire) ; absente si l'aperçu n'est pas prêt"`
	Note   string `json:"note,omitempty"`
}

// ModelSummary : ligne de liste / carte de bibliothèque.
type ModelSummary struct {
	ID             string                `json:"id"`
	Name           string                `json:"name"`
	CollectionID   *string               `json:"collection_id,omitempty"`
	CollectionPath string                `json:"collection_path,omitempty"`
	Tags           []string              `json:"tags"`
	Dimensions     *contracts.Dimensions `json:"dimensions,omitempty" jsonschema:"mm, d'après l'analyse ; absentes tant que l'analyse n'est pas faite"`
	Formats        []string              `json:"formats"`
	FileCount      int                   `json:"file_count"`
	SizeBytes      int64                 `json:"size_bytes"`
	Availability   string                `json:"availability" jsonschema:"linux | windows_only (fichier courant)"`
	Versions       int                   `json:"versions"`
	License        string                `json:"license,omitempty"`
	Preview        PreviewRef            `json:"preview"`
	Trashed        bool                  `json:"trashed,omitempty"`
	CreatedAt      time.Time             `json:"created_at"`
	UpdatedAt      time.Time             `json:"updated_at"`
}

// SearchQuery : recherche paginée.
type SearchQuery struct {
	Text           string   `json:"text,omitempty" jsonschema:"mots cherchés (tous requis) dans le nom, les étiquettes, la description et les noms de fichiers ; insensible à la casse (ASCII)"`
	Tags           []string `json:"tags,omitempty" jsonschema:"étiquettes exigées (toutes)"`
	Collection     string   `json:"collection,omitempty" jsonschema:"identifiant, nom ou chemin « A/B » d'une collection ; ses sous-collections sont incluses"`
	Format         string   `json:"format,omitempty" jsonschema:"stl | obj | step | 3mf"`
	Availability   string   `json:"availability,omitempty" jsonschema:"linux | windows_only"`
	IncludeTrashed bool     `json:"include_trashed,omitempty"`
	OnlyTrashed    bool     `json:"only_trashed,omitempty" jsonschema:"ne renvoyer que la corbeille"`
	Sort           string   `json:"sort,omitempty" jsonschema:"name (défaut) | created | updated"`
	Descending     bool     `json:"descending,omitempty"`
	Limit          int      `json:"limit,omitempty" jsonschema:"taille de page (défaut 25, max 200)"`
	Offset         int      `json:"offset,omitempty" jsonschema:"décalage de la page"`

	directOnly bool // collection exacte, sans descendants
}

// SearchResult : une page de résultats.
type SearchResult struct {
	Models     []ModelSummary `json:"models"`
	Total      int            `json:"total"`
	Limit      int            `json:"limit"`
	Offset     int            `json:"offset"`
	HasMore    bool           `json:"has_more"`
	NextOffset *int           `json:"next_offset,omitempty"`
}

func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(strings.ToLower(s)) + "%"
}

// resolveCollections renvoie l'identifiant de la collection désignée et, sauf directOnly, de ses descendants.
func resolveCollections(cols map[string]contracts.Collection, spec string, directOnly bool) ([]string, error) {
	spec = strings.TrimSpace(spec)
	var roots []string
	if _, ok := cols[spec]; ok {
		roots = []string{spec}
	} else {
		for id, c := range cols {
			p := collectionPath(cols, &id)
			if strings.EqualFold(p, spec) || (!strings.Contains(spec, "/") && strings.EqualFold(c.Name, spec)) {
				roots = append(roots, id)
			}
		}
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("collection %q : %w", spec, ErrNotFound)
	}
	if directOnly {
		return roots, nil
	}
	set := map[string]bool{}
	var add func(id string)
	add = func(id string) {
		if set[id] {
			return
		}
		set[id] = true
		for cid, c := range cols {
			if c.ParentID != nil && *c.ParentID == id {
				add(cid)
			}
		}
	}
	for _, r := range roots {
		add(r)
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// Search liste les modèles (pagination, filtres).
func (s *Service) Search(ctx context.Context, q SearchQuery) (*SearchResult, error) {
	if q.Limit <= 0 {
		q.Limit = 25
	}
	if q.Limit > 200 {
		q.Limit = 200
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	var conds []string
	var args []any
	switch {
	case q.OnlyTrashed:
		conds = append(conds, "m.deleted_at IS NOT NULL")
	case !q.IncludeTrashed:
		conds = append(conds, "m.deleted_at IS NULL")
	}
	for _, term := range strings.Fields(q.Text) {
		l := likeEscape(term)
		conds = append(conds, `(LOWER(m.name) LIKE ? ESCAPE '\' OR LOWER(m.tags) LIKE ? ESCAPE '\' OR LOWER(m.description) LIKE ? ESCAPE '\'
			OR EXISTS (SELECT 1 FROM source_files f WHERE f.model_id=m.id AND LOWER(f.name) LIKE ? ESCAPE '\'))`)
		args = append(args, l, l, l, l)
	}
	for _, t := range normTags(q.Tags) {
		conds = append(conds, `EXISTS (SELECT 1 FROM json_each(m.tags) j WHERE j.value = ?)`)
		args = append(args, t)
	}
	cols, err := allCollections(ctx, s.db())
	if err != nil {
		return nil, err
	}
	if q.Collection != "" {
		ids, err := resolveCollections(cols, q.Collection, q.directOnly)
		if err != nil {
			return nil, err
		}
		ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		conds = append(conds, "m.collection_id IN ("+ph+")")
		for _, id := range ids {
			args = append(args, id)
		}
	}
	if q.Format != "" {
		if !contracts.FileFormat(q.Format).Valid() {
			return nil, invalidf("format inconnu %q", q.Format)
		}
		conds = append(conds, "EXISTS (SELECT 1 FROM source_files f WHERE f.model_id=m.id AND f.format=?)")
		args = append(args, q.Format)
	}
	if q.Availability != "" {
		if !contracts.Availability(q.Availability).Valid() {
			return nil, invalidf("disponibilité inconnue %q", q.Availability)
		}
		conds = append(conds, "EXISTS (SELECT 1 FROM source_files f WHERE f.model_id=m.id AND f.availability=?)")
		args = append(args, q.Availability)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	dir := "ASC"
	if q.Descending {
		dir = "DESC"
	}
	var order string
	switch q.Sort {
	case "", "name":
		order = "m.name COLLATE NOCASE " + dir + ", m.id"
	case "created":
		order = "m.created_at " + dir + ", m.id"
	case "updated":
		order = "m.updated_at " + dir + ", m.id"
	default:
		return nil, invalidf("tri inconnu %q (name | created | updated)", q.Sort)
	}
	var total int
	if err := s.db().QueryRowContext(ctx, "SELECT COUNT(*) FROM models m"+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	models, err := queryModelsAliased(ctx, s, where+" ORDER BY "+order+" LIMIT ? OFFSET ?", append(append([]any(nil), args...), q.Limit, q.Offset)...)
	if err != nil {
		return nil, err
	}
	res := &SearchResult{Models: make([]ModelSummary, 0, len(models)), Total: total, Limit: q.Limit, Offset: q.Offset}
	for _, m := range models {
		sum, err := s.summarize(ctx, m, cols)
		if err != nil {
			return nil, err
		}
		res.Models = append(res.Models, sum)
	}
	if q.Offset+len(models) < total {
		n := q.Offset + len(models)
		res.HasMore, res.NextOffset = true, &n
	}
	return res, nil
}

func queryModelsAliased(ctx context.Context, s *Service, tail string, args ...any) ([]contracts.Model, error) {
	cols := strings.ReplaceAll(modelCols, ",", ",m.")
	rows, err := s.db().QueryContext(ctx, `SELECT m.`+cols+` FROM models m`+tail, args...)
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

func (s *Service) summarize(ctx context.Context, m contracts.Model, cols map[string]contracts.Collection) (ModelSummary, error) {
	files, err := queryFiles(ctx, s.db(), `WHERE model_id=?`, m.ID)
	if err != nil {
		return ModelSummary{}, err
	}
	vs, err := queryVersions(ctx, s.db(), m.ID)
	if err != nil {
		return ModelSummary{}, err
	}
	nv := len(vs)
	sum := ModelSummary{ID: m.ID, Name: m.Name, CollectionID: m.CollectionID, CollectionPath: collectionPath(cols, m.CollectionID),
		Tags: m.Tags, Dimensions: m.Dimensions, Formats: []string{}, FileCount: len(files), Versions: nv, License: m.License,
		Trashed: m.DeletedAt != nil, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, Availability: string(contracts.AvailableLinux)}
	if sum.Tags == nil {
		sum.Tags = []string{}
	}
	seen := map[string]bool{}
	for _, f := range files {
		sum.SizeBytes += f.SizeBytes
		if !seen[string(f.Format)] {
			seen[string(f.Format)] = true
			sum.Formats = append(sum.Formats, string(f.Format))
		}
	}
	sort.Strings(sum.Formats)
	if lf, ok := pickCurrent(files, vs); ok {
		sum.Availability = string(lf.Availability)
		sum.Preview, err = s.previewRef(ctx, lf)
		if err != nil {
			return sum, err
		}
	} else {
		sum.Preview = PreviewRef{State: PreviewNone}
	}
	return sum, nil
}

// previewRef calcule l'état de l'aperçu d'un fichier (avec URL signée si prêt).
func (s *Service) previewRef(ctx context.Context, f contracts.SourceFile) (PreviewRef, error) {
	if f.Availability != contracts.AvailableLinux {
		return PreviewRef{State: PreviewWindowsOnly, Note: "fichier indexé mais non copié : pas d'aperçu tant qu'il n'est pas importé"}, nil
	}
	d, ok, err := getDerived(ctx, s.db(), f.SHA256, KindPreview)
	if err != nil {
		return PreviewRef{}, err
	}
	if ok {
		switch d.State {
		case StateOK:
			url, _ := s.SignedAssetURL(d.BlobSHA)
			return PreviewRef{State: PreviewReady, SHA256: d.BlobSHA, URL: url}, nil
		case StatePending:
			return PreviewRef{State: PreviewPending, Note: "aperçu en cours de calcul"}, nil
		case StateBackendMissing:
			return PreviewRef{State: PreviewBackendMissing, Note: d.Note}, nil
		default:
			return PreviewRef{State: PreviewError, Note: d.Note}, nil
		}
	}
	if f.Format == contracts.FormatSTEP {
		if sd, ok, _ := getDerived(ctx, s.db(), f.SHA256, KindStepSTL); ok && sd.State == StateBackendMissing {
			return PreviewRef{State: PreviewBackendMissing, Note: sd.Note}, nil
		}
	}
	return PreviewRef{State: PreviewNone, Note: "aperçu non demandé"}, nil
}

// ---- collections ----

// CollectionInfo : collection avec ses effectifs.
type CollectionInfo struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	ParentID   *string   `json:"parent_id,omitempty"`
	Models     int       `json:"models" jsonschema:"modèles directement dans la collection (hors corbeille)"`
	TotalModel int       `json:"total_models" jsonschema:"modèles de la collection et de ses sous-collections"`
	CreatedAt  time.Time `json:"created_at"`
}

// ListCollections renvoie toutes les collections avec leurs effectifs.
func (s *Service) ListCollections(ctx context.Context) ([]CollectionInfo, error) {
	cols, err := allCollections(ctx, s.db())
	if err != nil {
		return nil, err
	}
	rows, err := s.db().QueryContext(ctx, `SELECT collection_id, COUNT(*) FROM models WHERE deleted_at IS NULL AND collection_id IS NOT NULL GROUP BY collection_id`)
	if err != nil {
		return nil, err
	}
	direct := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			rows.Close()
			return nil, err
		}
		direct[id] = n
	}
	rows.Close()
	total := map[string]int{}
	for id, n := range direct {
		cur := id
		for i := 0; i < 32; i++ {
			total[cur] += n
			c, ok := cols[cur]
			if !ok || c.ParentID == nil {
				break
			}
			cur = *c.ParentID
		}
	}
	out := make([]CollectionInfo, 0, len(cols))
	for id, c := range cols {
		out = append(out, CollectionInfo{ID: id, Name: c.Name, Path: collectionPath(cols, &id), ParentID: c.ParentID,
			Models: direct[id], TotalModel: total[id], CreatedAt: c.CreatedAt})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path) })
	return out, nil
}

// ListCollectionInput : contenu d'une collection.
type ListCollectionInput struct {
	Collection string `json:"collection" jsonschema:"identifiant, nom ou chemin « A/B » de la collection"`
	Limit      int    `json:"limit,omitempty" jsonschema:"taille de page (défaut 25, max 200)"`
	Offset     int    `json:"offset,omitempty"`
	Recursive  bool   `json:"recursive,omitempty" jsonschema:"inclure les modèles des sous-collections (défaut faux)"`
}

// CollectionListing : collection, sous-collections et page de modèles.
type CollectionListing struct {
	Collection     CollectionInfo   `json:"collection"`
	Subcollections []CollectionInfo `json:"subcollections"`
	Models         *SearchResult    `json:"models"`
}

// ListCollection renvoie le contenu d'une collection.
func (s *Service) ListCollection(ctx context.Context, in ListCollectionInput) (*CollectionListing, error) {
	if strings.TrimSpace(in.Collection) == "" {
		return nil, invalidf("collection requise")
	}
	all, err := s.ListCollections(ctx)
	if err != nil {
		return nil, err
	}
	cols, err := allCollections(ctx, s.db())
	if err != nil {
		return nil, err
	}
	ids, err := resolveCollections(cols, in.Collection, true)
	if err != nil {
		return nil, err
	}
	if len(ids) > 1 {
		return nil, invalidf("« %s » désigne %d collections : utiliser l'identifiant ou le chemin complet", in.Collection, len(ids))
	}
	out := &CollectionListing{Subcollections: []CollectionInfo{}}
	for _, ci := range all {
		if ci.ID == ids[0] {
			out.Collection = ci
		}
		if ci.ParentID != nil && *ci.ParentID == ids[0] {
			out.Subcollections = append(out.Subcollections, ci)
		}
	}
	out.Models, err = s.Search(ctx, SearchQuery{Collection: ids[0], Limit: in.Limit, Offset: in.Offset, directOnly: !in.Recursive})
	return out, err
}

// ---- fiche modèle ----

// AnalysisSummary : résumé de l'analyse stockée (le rapport complet vient d'analyze_geometry).
type AnalysisSummary struct {
	State               string                `json:"state" jsonschema:"ok | pending | error | backend_missing | none"`
	Note                string                `json:"note,omitempty"`
	Format              string                `json:"format,omitempty"`
	Unit                string                `json:"unit,omitempty"`
	UnitAssumed         bool                  `json:"unit_assumed,omitempty"`
	UnitNote            string                `json:"unit_note,omitempty"`
	Objects             int                   `json:"objects,omitempty"`
	Triangles           int                   `json:"triangles,omitempty"`
	SizeMM              *contracts.Dimensions `json:"size_mm,omitempty"`
	VolumeMM3           float64               `json:"volume_mm3,omitempty"`
	AreaMM2             float64               `json:"area_mm2,omitempty"`
	Watertight          bool                  `json:"watertight,omitempty"`
	OpenEdges           int                   `json:"open_edges,omitempty"`
	NonManifoldEdges    int                   `json:"non_manifold_edges,omitempty"`
	DegenerateTriangles int                   `json:"degenerate_triangles,omitempty"`
	Components          int                   `json:"components,omitempty"`
	Warnings            []string              `json:"warnings,omitempty"`
	NotInterpreted      []string              `json:"not_interpreted,omitempty" jsonschema:"fichiers du conteneur que l'analyse ne lit pas"`
	Level               string                `json:"level,omitempty"`
}

// AuditLine : ligne du journal d'une entité.
type AuditLine struct {
	At        time.Time       `json:"at"`
	Actor     string          `json:"actor"`
	Operation string          `json:"operation"`
	Detail    json.RawMessage `json:"detail,omitempty"`
}

// ModelDetails : fiche complète d'un modèle.
type ModelDetails struct {
	Model          contracts.Model          `json:"model"`
	CollectionPath string                   `json:"collection_path,omitempty"`
	Files          []contracts.SourceFile   `json:"files"`
	Versions       []contracts.ModelVersion `json:"versions"`
	CurrentFileID  string                   `json:"current_file_id,omitempty"`
	Preview        PreviewRef               `json:"preview"`
	Analysis       AnalysisSummary          `json:"analysis"`
	CAD            *CADInfo                 `json:"cad,omitempty"`
	DuplicateOf    []string                 `json:"same_content_models,omitempty" jsonschema:"autres modèles du catalogue ayant exactement le même contenu (empreinte)"`
	Audit          []AuditLine              `json:"audit"`
}

// CADInfo : état de l'import STEP.
type CADInfo struct {
	State string `json:"state"`
	Note  string `json:"note,omitempty"`
}

// GetModelDetails renvoie la fiche d'un modèle (corbeille comprise).
func (s *Service) GetModelDetails(ctx context.Context, id string) (*ModelDetails, error) {
	m, err := getModel(ctx, s.db(), id)
	if err != nil {
		return nil, err
	}
	cols, err := allCollections(ctx, s.db())
	if err != nil {
		return nil, err
	}
	files, err := queryFiles(ctx, s.db(), `WHERE model_id=?`, id)
	if err != nil {
		return nil, err
	}
	vs, err := queryVersions(ctx, s.db(), id)
	if err != nil {
		return nil, err
	}
	d := &ModelDetails{Model: m, CollectionPath: collectionPath(cols, m.CollectionID), Files: files, Versions: vs,
		Analysis: AnalysisSummary{State: "none"}, Audit: []AuditLine{}}
	if d.Files == nil {
		d.Files = []contracts.SourceFile{}
	}
	if d.Versions == nil {
		d.Versions = []contracts.ModelVersion{}
	}
	if lf, ok := pickCurrent(files, vs); ok {
		d.CurrentFileID = lf.ID
		if d.Preview, err = s.previewRef(ctx, lf); err != nil {
			return nil, err
		}
		if d.Analysis, err = s.analysisSummary(ctx, lf); err != nil {
			return nil, err
		}
		if lf.Format == contracts.FormatSTEP {
			if sd, ok, _ := getDerived(ctx, s.db(), lf.SHA256, KindStepSTL); ok {
				d.CAD = &CADInfo{State: sd.State, Note: sd.Note}
			} else {
				d.CAD = &CADInfo{State: "none"}
			}
		}
		same, err := queryFiles(ctx, s.db(), `WHERE sha256=? AND model_id<>? AND model_id IN (SELECT id FROM models WHERE deleted_at IS NULL)`, lf.SHA256, id)
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, f := range same {
			if !seen[f.ModelID] {
				seen[f.ModelID] = true
				d.DuplicateOf = append(d.DuplicateOf, f.ModelID)
			}
		}
	} else {
		d.Preview = PreviewRef{State: PreviewNone}
	}
	au, err := s.d.Store.ListAudit(ctx, "model", id, 15)
	if err != nil {
		return nil, err
	}
	for _, a := range au {
		d.Audit = append(d.Audit, AuditLine{At: a.At, Actor: a.Actor, Operation: a.Operation, Detail: a.Detail})
	}
	return d, nil
}

func (s *Service) analysisSummary(ctx context.Context, f contracts.SourceFile) (AnalysisSummary, error) {
	if f.Availability != contracts.AvailableLinux {
		return AnalysisSummary{State: "none", Note: "fichier non copié (windows_only) : analyse impossible"}, nil
	}
	d, ok, err := getDerived(ctx, s.db(), f.SHA256, KindAnalysis)
	if err != nil {
		return AnalysisSummary{}, err
	}
	if !ok {
		return AnalysisSummary{State: "none"}, nil
	}
	if d.State != StateOK {
		return AnalysisSummary{State: d.State, Note: d.Note}, nil
	}
	var doc AnalysisDoc
	if err := json.Unmarshal(d.Doc, &doc); err != nil || doc.Report == nil || doc.Report.Combined == nil {
		return AnalysisSummary{State: StateError, Note: "analyse illisible"}, nil
	}
	r, c := doc.Report, doc.Report.Combined
	return AnalysisSummary{State: StateOK, Format: r.Format, Unit: r.Unit, UnitAssumed: r.UnitAssumed, UnitNote: r.UnitNote,
		Objects: len(r.Objects), Triangles: c.Triangles,
		SizeMM:    &contracts.Dimensions{X: c.BBox.Size.X, Y: c.BBox.Size.Y, Z: c.BBox.Size.Z},
		VolumeMM3: c.Volume, AreaMM2: c.Area, Watertight: c.Watertight, OpenEdges: c.OpenEdges,
		NonManifoldEdges: c.NonManifoldEdges, DegenerateTriangles: c.DegenerateTriangles, Components: c.Components,
		Warnings: append(append([]string(nil), r.Warnings...), c.Warnings...), NotInterpreted: r.Extras, Level: doc.Level}, nil
}
