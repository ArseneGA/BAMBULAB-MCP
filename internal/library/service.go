package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/calibration"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/engineering"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

const actor = "library"

// Catalog : partie du catalogue utilisée (satisfaite par *catalog.Service ; substituable en test).
type Catalog interface {
	RunWorker(ctx context.Context, command string, req map[string]any) (json.RawMessage, error)
	RegisterGenerated(ctx context.Context, actor string, m contracts.Model, parentVersion, label, note string, files []catalog.GeneratedFile) (contracts.Model, contracts.ModelVersion, []contracts.SourceFile, error)
}

// ModelReader (facultatif, satisfait par *catalog.Service) : lecture d'une fiche de modèle.
type ModelReader interface {
	GetModelDetails(ctx context.Context, id string) (*catalog.ModelDetails, error)
}

// Calibrations fournit la calibration applicable (satisfaite par *calibration.Service).
type Calibrations interface {
	Resolve(ctx context.Context, k calibration.Key) (*calibration.Resolution, error)
}

// Deps : dépendances du service.
type Deps struct {
	Store        *store.Store
	Blobs        *store.BlobStore
	Catalog      Catalog
	Calibrations Calibrations // facultatif
	WorkRoot     string
	Now          func() time.Time
}

// Service : bibliothèque de gabarits.
type Service struct {
	d Deps

	loadMu   sync.Mutex // un seul chargement des gabarits à la fois (hors de mu)
	mu       sync.Mutex
	builtins map[string]TemplateInfo
	order    []string
	thumbs   map[string]string // cache des vignettes par défaut (id + hash de schéma)
}

// New construit le service.
func New(d Deps) (*Service, error) {
	if d.Store == nil || d.Blobs == nil {
		return nil, errors.New("bibliothèque : Store et Blobs requis")
	}
	if d.WorkRoot == "" {
		d.WorkRoot = filepath.Join(os.TempDir(), "bambu-library")
	}
	return &Service{d: d, thumbs: map[string]string{}}, nil
}

func (s *Service) now() time.Time {
	if s.d.Now != nil {
		return s.d.Now().UTC()
	}
	return s.d.Store.Now()
}

func (s *Service) worker(ctx context.Context, cmd string, req map[string]any) (json.RawMessage, error) {
	if s.d.Catalog == nil {
		return nil, fmt.Errorf("%w : catalogue absent", ErrUnavailable)
	}
	raw, err := s.d.Catalog.RunWorker(ctx, cmd, req)
	if err != nil {
		var we *catalog.WorkerError
		if errors.As(err, &we) {
			if we.Code == "invalid_params" {
				return nil, invalidf("%s", we.Message)
			}
			return nil, fmt.Errorf("%w : %s : %s", ErrUnavailable, we.Code, we.Message)
		}
		return nil, fmt.Errorf("%w : worker géométrie : %v", ErrUnavailable, err)
	}
	return raw, nil
}

func thumbURL(id string) string { return "/api/library/templates/" + id + "/thumbnail.svg" }

// loadBuiltins interroge le worker (une fois, puis cache).
//
// Le chargement se fait HORS du verrou s.mu (le worker peut être long, et les autres appels — vignettes en cache, listes —
// ne doivent pas l'attendre) ; loadMu fait office de singleflight : un seul appel au worker, les autres attendent son résultat.
func (s *Service) loadBuiltins(ctx context.Context) (map[string]TemplateInfo, []string, error) {
	cached := func() (map[string]TemplateInfo, []string, bool) {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.builtins, s.order, s.builtins != nil
	}
	if m, o, ok := cached(); ok {
		return m, o, nil
	}
	s.loadMu.Lock()
	defer s.loadMu.Unlock()
	if m, o, ok := cached(); ok { // un autre appel vient de charger
		return m, o, nil
	}
	raw, err := s.worker(ctx, "library_list", map[string]any{})
	if err != nil {
		return nil, nil, err
	}
	var res struct {
		Templates []TemplateInfo `json:"templates"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || len(res.Templates) == 0 {
		return nil, nil, errors.New("réponse du worker sans gabarit")
	}
	m := map[string]TemplateInfo{}
	var order []string
	for _, t := range res.Templates {
		t.Kind = "builtin"
		t.ThumbnailURL = thumbURL(t.ID)
		m[t.ID] = t
		order = append(order, t.ID)
	}
	s.mu.Lock()
	s.builtins, s.order = m, order
	s.mu.Unlock()
	return m, order, nil
}

// ---- variantes ----

type variantRow struct {
	ID, Base, BaseVersion, Title, Description string
	Params                                    map[string]any
	SourceModel                               string
}

func (s *Service) loadVariants(ctx context.Context) ([]variantRow, error) {
	rows, err := s.d.Store.DB().QueryContext(ctx, `SELECT id,base_template,template_version,title,description,params,COALESCE(source_model_id,'') FROM part_template_variants ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []variantRow
	for rows.Next() {
		var v variantRow
		var pj string
		if err := rows.Scan(&v.ID, &v.Base, &v.BaseVersion, &v.Title, &v.Description, &pj, &v.SourceModel); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(pj), &v.Params)
		out = append(out, v)
	}
	return out, rows.Err()
}

func variantInfo(v variantRow, base TemplateInfo) TemplateInfo {
	t := base
	t.ID, t.Kind, t.Title, t.BaseTemplate = v.ID, "variant", v.Title, base.ID
	t.Fixed = v.Params
	t.Summary = "Variante de « " + base.Title + " »"
	if v.Description != "" {
		t.Summary = v.Description
		t.Description = v.Description + " — variante de « " + base.Title + " » : " + base.Description
	}
	t.ThumbnailURL = thumbURL(v.ID)
	ps := make([]ParamSpec, len(base.Params))
	copy(ps, base.Params)
	for i := range ps {
		if val, ok := v.Params[ps[i].Name]; ok {
			ps[i].Default = val
		}
	}
	t.Params = ps
	return t
}

// List renvoie les gabarits intégrés puis les variantes ; category filtre (vide = tous).
func (s *Service) List(ctx context.Context, category string) (*ListOutput, error) {
	b, order, err := s.loadBuiltins(ctx)
	if err != nil {
		return nil, err
	}
	vars, err := s.loadVariants(ctx)
	if err != nil {
		return nil, err
	}
	out := &ListOutput{Templates: []TemplateInfo{}}
	cats := map[string]bool{}
	add := func(t TemplateInfo) {
		cats[t.Category] = true
		if category == "" || strings.EqualFold(t.Category, category) {
			out.Templates = append(out.Templates, t)
		}
	}
	for _, id := range order {
		add(b[id])
	}
	for _, v := range vars {
		if base, ok := b[v.Base]; ok {
			add(variantInfo(v, base))
		}
	}
	for c := range cats {
		out.Categories = append(out.Categories, c)
	}
	sort.Strings(out.Categories)
	out.Note = fmt.Sprintf("%d gabarit(s) et variante(s). Les paramètres « auto » sont calculés par le moteur d'ingénierie (calibré si une calibration existe pour l'imprimante, la buse et le matériau).", len(out.Templates))
	return out, nil
}

// Describe renvoie la fiche complète d'un gabarit ou d'une variante.
func (s *Service) Describe(ctx context.Context, id string) (*TemplateInfo, error) {
	t, _, _, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// find retourne la fiche demandée, le gabarit intégré de base et les paramètres figés de la variante.
func (s *Service) find(ctx context.Context, id string) (TemplateInfo, TemplateInfo, map[string]any, error) {
	b, order, err := s.loadBuiltins(ctx)
	if err != nil {
		return TemplateInfo{}, TemplateInfo{}, nil, err
	}
	id = strings.TrimSpace(id)
	if t, ok := b[id]; ok {
		return t, t, nil, nil
	}
	vars, err := s.loadVariants(ctx)
	if err != nil {
		return TemplateInfo{}, TemplateInfo{}, nil, err
	}
	for _, v := range vars {
		if v.ID == id {
			base, ok := b[v.Base]
			if !ok {
				return TemplateInfo{}, TemplateInfo{}, nil, invalidf("variante %q : gabarit de base %q disparu", id, v.Base)
			}
			return variantInfo(v, base), base, v.Params, nil
		}
	}
	return TemplateInfo{}, TemplateInfo{}, nil, invalidf("gabarit ou variante %q inconnu (disponibles : %s)", id, strings.Join(order, ", "))
}

// ---- création ----

var nameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func copyMap(m map[string]any) map[string]any {
	o := make(map[string]any, len(m))
	for k, v := range m {
		o[k] = v
	}
	return o
}

func (s *Service) calibrationFor(ctx context.Context, in CreateInput, material string, nozzle float64) (*engineering.Calibration, CalibrationUsed, error) {
	used := CalibrationUsed{Provenance: "valeurs génériques (indicatives)", Note: "Aucune calibration appliquée : jeux et compensations génériques, à valider par un essai d'impression. Voir generate_calibration_set."}
	if s.d.Calibrations == nil {
		return nil, used, nil
	}
	k := calibration.Key{Printer: in.Printer, NozzleMM: nozzle, Material: material, Filament: in.Filament}
	res, err := s.d.Calibrations.Resolve(ctx, k)
	if err != nil {
		if errors.Is(err, calibration.ErrInvalid) {
			return nil, used, invalidf("%v", err)
		}
		return nil, used, err
	}
	if res.Calibration == nil {
		// repli sur la famille du matériau (« PETG-HF » -> « PETG ») quand la calibration a été saisie pour la famille
		if m, e := engineering.GetMaterial(material); e == nil && m.Family != "" && !strings.EqualFold(m.Family, material) {
			k.Material = m.Family
			if res, err = s.d.Calibrations.Resolve(ctx, k); err != nil {
				return nil, used, err
			}
		}
	}
	if res.Calibration == nil {
		used.Note = res.Note + " Voir generate_calibration_set."
		return nil, used, nil
	}
	return res.Calibration, CalibrationUsed{Applied: true, Provenance: res.Calibration.Provenance, Matched: res.Matched, Note: res.Note}, nil
}

// Create génère un modèle depuis un gabarit (ou une variante), applique les calculs d'ingénierie et l'enregistre au catalogue.
func (s *Service) Create(ctx context.Context, in CreateInput) (*CreateOutput, error) {
	if s.d.Catalog == nil {
		return nil, fmt.Errorf("%w : catalogue absent", ErrUnavailable)
	}
	if strings.TrimSpace(in.Template) == "" {
		return nil, invalidf("template requis")
	}
	info, base, fixed, err := s.find(ctx, in.Template)
	if err != nil {
		return nil, err
	}
	name := in.Name
	if name == "" {
		name = fmt.Sprintf("%s_%s", info.ID, s.now().Format("20060102_150405"))
	}
	if !nameRe.MatchString(name) {
		return nil, invalidf("nom invalide : 1 à 64 caractères parmi lettres, chiffres, point, tiret et souligné")
	}
	nozzle := in.NozzleMM
	if nozzle == 0 {
		nozzle = 0.4
	}
	material := strings.TrimSpace(in.Material)
	if material == "" {
		material = "PLA"
	}
	known := map[string]bool{}
	for _, p := range base.Params {
		known[p.Name] = true
	}
	var unknown []string
	for k := range in.Params {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, invalidf("paramètre(s) inconnu(s) pour %s : %s", base.ID, strings.Join(unknown, ", "))
	}
	if in.ModelID != "" && in.BaseVersionID == "" {
		return nil, invalidf("base_version_id requis avec model_id (nouvelle version d'un modèle existant)")
	}
	requested := copyMap(fixed)
	for k, v := range in.Params {
		requested[k] = v
	}

	cal, used, err := s.calibrationFor(ctx, in, material, nozzle)
	if err != nil {
		return nil, err
	}
	rs := &resolver{cal: cal, nozzle: nozzle, compensate: in.CompensateHoles == nil || *in.CompensateHoles}
	var warns []string
	if m, e := engineering.GetMaterial(material); e == nil {
		rs.material = m.ID
	} else {
		warns = append(warns, fmt.Sprintf("Matériau %q inconnu du moteur d'ingénierie : retrait et déformation admissible non pris en compte.", material))
	}
	params, autos, calcs, w, err := rs.apply(base, requested)
	if err != nil {
		return nil, err
	}
	warns = append(warns, w...)

	if in.ModelID != "" {
		if err := s.checkBase(ctx, in.ModelID, in.BaseVersionID); err != nil {
			return nil, err
		}
	}

	out := filepath.Join(s.d.WorkRoot, "build-"+contracts.NewID("w"))
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, err
	}
	defer os.RemoveAll(out)
	raw, err := s.worker(ctx, "library_build", map[string]any{"template": base.ID, "params": params, "out_dir": out, "name": name,
		"nozzle_diameter": nozzle, "thumbnail": true})
	if err != nil {
		return nil, err
	}
	var res struct {
		Template        string         `json:"template"`
		TemplateVersion string         `json:"template_version"`
		Params          map[string]any `json:"params"`
		Parts           []struct {
			Name   string  `json:"name"`
			Volume float64 `json:"volume_mm3"`
			Solids int     `json:"solids"`
			Tess   struct {
				Triangles  int  `json:"triangles"`
				Watertight bool `json:"watertight"`
			} `json:"tessellation"`
		} `json:"parts"`
		Assumptions []string          `json:"assumptions"`
		Warnings    []string          `json:"warnings"`
		BBox        map[string]any    `json:"bbox_mm"`
		Derived     map[string]any    `json:"derived"`
		Level       string            `json:"level"`
		Files       map[string]string `json:"files"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.Files["stl"] == "" {
		return nil, errors.New("réponse du worker sans fichier STL")
	}
	post, pw := rs.postCalcs(base, res.Params)
	calcs = append(calcs, post...)
	warns = append(warns, pw...)
	warns = append(warns, res.Warnings...)

	var gen []catalog.GeneratedFile
	for _, f := range []struct {
		key, ext string
		fmt      contracts.FileFormat
	}{{"stl", ".stl", contracts.FormatSTL}, {"step", ".step", contracts.FormatSTEP}} {
		g, err := s.putFile(out, res.Files[f.key], name+f.ext, f.fmt)
		if err != nil {
			return nil, err
		}
		if g != nil {
			gen = append(gen, *g)
		}
	}
	thumbSHA := ""
	if tp := res.Files["thumbnail"]; tp != "" {
		if g, err := s.putFile(out, tp, name+".svg", contracts.FormatSTL); err == nil && g != nil {
			thumbSHA = g.SHA256
		}
	}
	pj, _ := json.Marshal(res.Params)
	m := contracts.Model{Name: name, Tags: append([]string{"parametrique", "bibliotheque", base.ID}, in.Tags...),
		Description: fmt.Sprintf("%s — généré depuis le gabarit « %s » v%s.", base.Title, base.ID, res.TemplateVersion),
		Provenance:  &contracts.Provenance{Source: "generation parametrique", Note: "gabarit " + base.ID + " ; paramètres " + string(pj) + " ; " + used.Provenance, ImportedAt: s.now()}}
	parent, label := "", "v1"
	if in.ModelID != "" {
		m.ID, parent, label = in.ModelID, in.BaseVersionID, "tpl-"+base.ID+"-"+s.now().Format("20060102T150405")
	}
	m, ver, files, err := s.d.Catalog.RegisterGenerated(ctx, actor, m, parent, label, "création depuis le gabarit "+base.ID+" v"+res.TemplateVersion, gen)
	if err != nil {
		return nil, err
	}
	reqJSON, _ := json.Marshal(requested)
	calcJSON, _ := json.Marshal(append([]Calc{}, calcs...))
	calJSON := ""
	if used.Applied {
		b, _ := json.Marshal(used)
		calJSON = string(b)
	}
	variantID := ""
	if info.Kind == "variant" {
		variantID = info.ID
	}
	_, err = s.d.Store.DB().ExecContext(ctx, `INSERT INTO part_template_models(version_id,model_id,template,variant_id,template_version,params,requested,calcs,calibration,thumbnail_sha,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		ver.ID, m.ID, base.ID, nullStr(variantID), res.TemplateVersion, string(pj), string(reqJSON), string(calcJSON), nullStr(calJSON), nullStr(thumbSHA), store.FormatTime(s.now()))
	if err != nil {
		return nil, err
	}
	o := &CreateOutput{ModelID: m.ID, VersionID: ver.ID, Files: files, Template: base.ID, TemplateVersion: res.TemplateVersion, Variant: variantID,
		Params: res.Params, Requested: requested, Auto: nzA(autos), Calcs: nzC(calcs), Calibration: used,
		Assumptions: nz(res.Assumptions), Warnings: nz(warns), BBoxMM: res.BBox, Derived: res.Derived, Level: res.Level}
	for _, p := range res.Parts {
		o.Parts = append(o.Parts, PartInfo{Name: p.Name, VolumeMM3: p.Volume, Solids: p.Solids, Watertight: p.Tess.Watertight, Triangles: p.Tess.Triangles})
	}
	if thumbSHA != "" {
		o.ThumbnailURL = "/api/library/models/" + m.ID + "/thumbnail.svg"
	}
	o.Note = "Modèle source paramétrique conservé (STEP + STL). Les cotes sont calculées par le worker ; jeux et compensations viennent du moteur d'ingénierie (" + used.Provenance + ") : ce n'est pas une validation mécanique."
	return o, nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nz(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nzA(a []AutoParam) []AutoParam {
	if a == nil {
		return []AutoParam{}
	}
	return a
}

func nzC(c []Calc) []Calc {
	if c == nil {
		return []Calc{}
	}
	return c
}

func (s *Service) putFile(work, path, name string, f contracts.FileFormat) (*catalog.GeneratedFile, error) {
	if path == "" {
		return nil, nil
	}
	p := path
	if !filepath.IsAbs(p) {
		p = filepath.Join(work, p)
	}
	// formes canoniques (liens symboliques, noms courts 8.3 de Windows) avant de confiner
	cw, werr := filepath.EvalSymlinks(work)
	cp, perr := filepath.EvalSymlinks(p)
	if werr != nil || perr != nil {
		return nil, fmt.Errorf("fichier du worker introuvable : %s", p)
	}
	if rel, err := filepath.Rel(cw, cp); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("fichier du worker hors du dossier de travail : %s", p)
	}
	p = cp
	fh, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	sha, size, err := s.d.Blobs.Put(fh, "")
	if err != nil {
		return nil, err
	}
	return &catalog.GeneratedFile{Name: name, Format: f, SHA256: sha, Size: size, Unit: "mm"}, nil
}

// checkBase refuse une version de départ périmée.
func (s *Service) checkBase(ctx context.Context, modelID, baseVersion string) error {
	r, ok := s.d.Catalog.(ModelReader)
	if !ok {
		return nil
	}
	d, err := r.GetModelDetails(ctx, modelID)
	if err != nil {
		return err
	}
	var latest *contracts.ModelVersion
	found := false
	for i := range d.Versions {
		v := d.Versions[i]
		if v.ID == baseVersion {
			found = true
		}
		if latest == nil || v.CreatedAt.After(latest.CreatedAt) {
			latest = &d.Versions[i]
		}
	}
	if !found {
		return invalidf("base_version_id %q n'appartient pas au modèle %s", baseVersion, modelID)
	}
	if latest != nil && latest.ID != baseVersion {
		return fmt.Errorf("%w : une version plus récente existe (%s) ; repartir de celle-ci", ErrConflict, latest.ID)
	}
	return nil
}

// ---- variantes : save_as_template ----

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	r := strings.NewReplacer("é", "e", "è", "e", "ê", "e", "ë", "e", "à", "a", "â", "a", "ä", "a", "î", "i", "ï", "i", "ô", "o", "ö", "o", "ù", "u", "û", "u", "ü", "u", "ç", "c", "œ", "oe")
	s = slugRe.ReplaceAllString(r.Replace(strings.ToLower(strings.TrimSpace(s))), "-")
	s = strings.Trim(s, "-")
	if len(s) > 48 {
		s = strings.Trim(s[:48], "-")
	}
	return s
}

// modelParams retrouve gabarit et paramètres d'une version de modèle (table de la bibliothèque, sinon note de provenance).
func (s *Service) modelParams(ctx context.Context, modelID, versionID string) (tpl string, resolved, requested map[string]any, ver string, err error) {
	q := `SELECT template,params,requested,version_id FROM part_template_models WHERE model_id=?`
	args := []any{modelID}
	if versionID != "" {
		q += ` AND version_id=?`
		args = append(args, versionID)
	}
	q += ` ORDER BY created_at DESC, rowid DESC LIMIT 1`
	var pj, rj string
	err = s.d.Store.DB().QueryRowContext(ctx, q, args...).Scan(&tpl, &pj, &rj, &ver)
	if err == nil {
		_ = json.Unmarshal([]byte(pj), &resolved)
		_ = json.Unmarshal([]byte(rj), &requested)
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return
	}
	// repli : modèle créé par create_parametric_model (note « gabarit X ; paramètres {json} »)
	if r, ok := s.d.Catalog.(ModelReader); ok {
		d, derr := r.GetModelDetails(ctx, modelID)
		if derr != nil {
			err = derr
			return
		}
		if d.Model.Provenance != nil {
			note := d.Model.Provenance.Note
			if i := strings.Index(note, "gabarit "); i >= 0 {
				rest := note[i+len("gabarit "):]
				if j := strings.Index(rest, " ; paramètres "); j > 0 {
					tpl = rest[:j]
					js := rest[j+len(" ; paramètres "):]
					if k := strings.Index(js, " ; "); k >= 0 {
						js = js[:k]
					}
					if json.Unmarshal([]byte(js), &resolved) == nil {
						requested = resolved
						if len(d.Versions) > 0 {
							ver = d.Versions[len(d.Versions)-1].ID
						}
						return tpl, resolved, requested, ver, nil
					}
				}
			}
		}
	}
	return "", nil, nil, "", invalidf("le modèle %s n'a pas été généré par un gabarit (aucun paramètre enregistré) : créer d'abord le modèle avec create_from_template", modelID)
}

// SaveAs enregistre une variante nommée réutilisable à partir des paramètres d'un modèle existant.
func (s *Service) SaveAs(ctx context.Context, in SaveInput) (*SaveOutput, error) {
	if strings.TrimSpace(in.ModelID) == "" || strings.TrimSpace(in.Name) == "" {
		return nil, invalidf("model_id et name requis")
	}
	id := slug(in.Name)
	if id == "" {
		return nil, invalidf("name doit contenir au moins une lettre ou un chiffre")
	}
	b, _, err := s.loadBuiltins(ctx)
	if err != nil {
		return nil, err
	}
	if _, clash := b[id]; clash {
		return nil, fmt.Errorf("%w : %q est un gabarit intégré ; choisir un autre nom", ErrConflict, id)
	}
	tpl, resolved, requested, _, err := s.modelParams(ctx, in.ModelID, in.VersionID)
	if err != nil {
		return nil, err
	}
	base, ok := b[tpl]
	if !ok {
		return nil, invalidf("gabarit %q du modèle absent de la bibliothèque", tpl)
	}
	params := requested
	if in.Frozen {
		params = resolved
	}
	// ne garde que les paramètres connus du gabarit
	known := map[string]bool{}
	for _, p := range base.Params {
		known[p.Name] = true
	}
	clean := map[string]any{}
	for k, v := range params {
		if known[k] {
			clean[k] = v
		}
	}
	pj, _ := json.Marshal(clean)
	q := `INSERT INTO part_template_variants(id,base_template,template_version,title,description,params,source_model_id,created_at) VALUES(?,?,?,?,?,?,?,?)`
	if in.Replace {
		q += ` ON CONFLICT(id) DO UPDATE SET base_template=excluded.base_template, template_version=excluded.template_version, title=excluded.title, description=excluded.description, params=excluded.params, source_model_id=excluded.source_model_id`
	}
	if _, err := s.d.Store.DB().ExecContext(ctx, q, id, base.ID, base.Version, strings.TrimSpace(in.Name), in.Description, string(pj), in.ModelID, store.FormatTime(s.now())); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "constraint") {
			return nil, fmt.Errorf("%w : la variante %q existe déjà (replace=true pour la remplacer)", ErrConflict, id)
		}
		return nil, err
	}
	row := variantRow{ID: id, Base: base.ID, BaseVersion: base.Version, Title: strings.TrimSpace(in.Name), Description: in.Description, Params: clean, SourceModel: in.ModelID}
	note := fmt.Sprintf("Variante « %s » (%s) enregistrée à partir de %s : utilisable comme `template` dans create_from_template.", row.Title, id, in.ModelID)
	if !in.Frozen {
		note += " Seuls les paramètres demandés sont figés : jeux et compensations restent recalculés avec la calibration du moment."
	}
	return &SaveOutput{Variant: variantInfo(row, base), Note: note}, nil
}

// ---- vignettes ----

// Thumbnail renvoie la vignette SVG d'un gabarit (paramètres par défaut, ou ceux fournis) ; les vignettes par défaut sont mises en cache.
func (s *Service) Thumbnail(ctx context.Context, id string, params map[string]any) ([]byte, error) {
	info, base, fixed, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	use := copyMap(fixed)
	for k, v := range params {
		use[k] = v
	}
	cacheable := len(params) == 0
	ck := info.ID + "@" + info.SchemaHash
	if cacheable {
		s.mu.Lock()
		if v, ok := s.thumbs[ck]; ok {
			s.mu.Unlock()
			return []byte(v), nil
		}
		s.mu.Unlock()
	}
	raw, err := s.worker(ctx, "library_thumbnail", map[string]any{"template": base.ID, "params": use})
	if err != nil {
		return nil, err
	}
	var res struct {
		SVG string `json:"svg"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.SVG == "" {
		return nil, errors.New("réponse du worker sans vignette")
	}
	if cacheable {
		s.mu.Lock()
		s.thumbs[ck] = res.SVG
		s.mu.Unlock()
	}
	return []byte(res.SVG), nil
}

// ModelThumbnail renvoie la vignette enregistrée lors de la génération d'un modèle (dernière version générée).
func (s *Service) ModelThumbnail(ctx context.Context, modelID string) ([]byte, error) {
	var sha sql.NullString
	err := s.d.Store.DB().QueryRowContext(ctx, `SELECT thumbnail_sha FROM part_template_models WHERE model_id=? AND thumbnail_sha IS NOT NULL ORDER BY created_at DESC, rowid DESC LIMIT 1`, modelID).Scan(&sha)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !sha.Valid) {
		return nil, fmt.Errorf("vignette du modèle %s : %w", modelID, store.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	f, err := s.d.Blobs.Open(sha.String)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var b strings.Builder
	buf := make([]byte, 32<<10)
	for {
		n, rerr := f.Read(buf)
		b.Write(buf[:n])
		if rerr != nil {
			break
		}
	}
	return []byte(b.String()), nil
}
