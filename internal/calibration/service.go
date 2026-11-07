package calibration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/engineering"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/goals"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

const actor = "calibration"

// ErrUnavailable : worker CAO absent ou en échec.
var ErrUnavailable = errors.New("indisponible")

// Catalog est la partie du catalogue utilisée (satisfaite par *catalog.Service ; substituable en test).
type Catalog interface {
	RunWorker(ctx context.Context, command string, req map[string]any) (json.RawMessage, error)
	RegisterGenerated(ctx context.Context, actor string, m contracts.Model, parentVersion, label, note string, files []catalog.GeneratedFile) (contracts.Model, contracts.ModelVersion, []contracts.SourceFile, error)
}

// ProfileMaker crée une révision de profil de découpe (satisfaite par *goals.Service).
type ProfileMaker interface {
	ModifyProfile(ctx context.Context, in goals.ModifyInput) (*goals.ModifyOutput, error)
}

// Deps : dépendances du service.
type Deps struct {
	Store    *store.Store
	Blobs    *store.BlobStore
	Catalog  Catalog
	Profiles ProfileMaker // facultatif : profils découpables des séries de températures
	WorkRoot string       // dossier de travail du worker (confiné)
	Now      func() time.Time
}

// Service gère génération, mesures et résolution des calibrations.
type Service struct{ d Deps }

// New construit le service.
func New(d Deps) (*Service, error) {
	if d.Store == nil || d.Blobs == nil {
		return nil, errors.New("calibration : Store et Blobs requis")
	}
	if d.WorkRoot == "" {
		d.WorkRoot = filepath.Join(os.TempDir(), "bambu-calibration")
	}
	return &Service{d: d}, nil
}

func (s *Service) now() time.Time {
	if s.d.Now != nil {
		return s.d.Now().UTC()
	}
	return s.d.Store.Now()
}

// Key identifie une calibration : (imprimante, buse, matériau, bobine). Printer et Filament vides = « toutes ».
type Key struct {
	Printer  string  `json:"printer,omitempty" jsonschema:"identifiant ou nom de l'imprimante (texte libre, ex. prn_ab12 ou « P1S salon ») ; vide = toutes"`
	NozzleMM float64 `json:"nozzle_mm" jsonschema:"diamètre de buse en mm (ex. 0.4)"`
	Material string  `json:"material" jsonschema:"matériau / famille (PLA, PETG, ABS...) ; la calibration ne vaut que pour ce matériau"`
	Filament string  `json:"filament,omitempty" jsonschema:"bobine précise (marque + couleur, ex. « Bambu PLA Basic noir ») ; vide = toutes les bobines du matériau"`
}

var spaces = regexp.MustCompile(`\s+`)

// Normalize met la clé sous forme canonique et la valide.
func (k Key) Normalize() (Key, error) {
	k.Printer = strings.ToLower(strings.TrimSpace(spaces.ReplaceAllString(k.Printer, " ")))
	k.Material = strings.ToUpper(strings.TrimSpace(k.Material))
	k.Filament = strings.ToLower(strings.TrimSpace(spaces.ReplaceAllString(k.Filament, " ")))
	if !finite(k.NozzleMM) || k.NozzleMM < 0.1 || k.NozzleMM > 1.2 {
		return k, invalidf("nozzle_mm %v hors de [0,1 ; 1,2] mm", k.NozzleMM)
	}
	k.NozzleMM = round(k.NozzleMM, 2)
	if k.Material == "" {
		return k, invalidf("material requis : un jeu ou un retrait mesuré ne vaut que pour un matériau donné")
	}
	for _, v := range []string{k.Printer, k.Material, k.Filament} {
		if len(v) > 120 {
			return k, invalidf("printer, material et filament : 120 caractères maximum")
		}
	}
	return k, nil
}

func (k Key) label() string {
	parts := []string{}
	if k.Printer != "" {
		parts = append(parts, k.Printer)
	}
	parts = append(parts, fmt.Sprintf("buse %g", k.NozzleMM), k.Material)
	if k.Filament != "" {
		parts = append(parts, k.Filament)
	}
	return strings.Join(parts, ", ")
}

// ---- génération ----

// GenerateInput décrit la série de pièces à produire.
type GenerateInput struct {
	Key
	Kinds           []string        `json:"kinds,omitempty" jsonschema:"fit_comb | hole_shaft | shrink_cube | bridge_overhang | temperature (défaut : toutes)"`
	ClearanceGaps   []float64       `json:"clearance_gaps,omitempty" jsonschema:"jeux du peigne en mm (défaut 0.1 à 0.6 par 0.1)"`
	HoleDiameters   []float64       `json:"hole_diameters,omitempty" jsonschema:"diamètres nominaux des trous/arbres en mm (défaut 3,4,5,6,8,10)"`
	BridgeSpans     []float64       `json:"bridge_spans,omitempty" jsonschema:"portées de ponts en mm (défaut 10 à 50)"`
	OverhangAngles  []float64       `json:"overhang_angles,omitempty" jsonschema:"angles de surplomb en degrés depuis la verticale (défaut 30 à 70)"`
	Temperatures    []float64       `json:"temperatures,omitempty" jsonschema:"températures de buse à tester (défaut : 5 valeurs autour de la plage du matériau)"`
	TemperatureMode string          `json:"temperature_mode,omitempty" jsonschema:"series (une petite pièce par température, défaut) | tower (une tour avec étages ; nécessite des changements de température G-code)"`
	Base            *goals.BaseSpec `json:"base,omitempty" jsonschema:"profils de départ (machine/process/filament) : si fourni, une révision de profil découpable est créée par pièce de température"`
	Tags            []string        `json:"tags,omitempty"`
}

// Piece : une pièce de calibration enregistrée au catalogue.
type Piece struct {
	Kind            string            `json:"kind"`
	Name            string            `json:"name"`
	ModelID         string            `json:"model_id"`
	VersionID       string            `json:"version_id"`
	Description     string            `json:"description"`
	Measure         []string          `json:"measure" jsonschema:"ce qu'il faut mesurer / observer, avec la clé de record_calibration correspondante"`
	BBoxMM          map[string]any    `json:"bbox_mm,omitempty"`
	Info            map[string]any    `json:"info,omitempty"`
	Parts           []map[string]any  `json:"parts,omitempty"`
	TemperatureC    *float64          `json:"nozzle_temperature_c,omitempty"`
	Segments        []map[string]any  `json:"segments,omitempty" jsonschema:"tour de température : étages (hauteurs et température)"`
	LayerChanges    []map[string]any  `json:"layer_changes,omitempty"`
	GcodeHint       []string          `json:"gcode_hint,omitempty"`
	Overrides       []slicer.Override `json:"slicing_overrides,omitempty" jsonschema:"surcharges de profil à appliquer pour cette pièce (modify_slicing_profile)"`
	ProfileRevision string            `json:"profile_revision,omitempty" jsonschema:"profil découpable déjà créé (si base fourni)"`
	Warnings        []string          `json:"warnings,omitempty"`
}

// GenerateOutput : série générée.
type GenerateOutput struct {
	SetID            string         `json:"set_id"`
	Key              Key            `json:"key"`
	Pieces           []Piece        `json:"pieces"`
	MeasuresTemplate map[string]any `json:"measures_template" jsonschema:"squelette des mesures à renvoyer à record_calibration"`
	Instructions     []string       `json:"instructions"`
	Note             string         `json:"note"`
}

// DefaultTemperatures propose 5 températures autour de la plage usuelle de la famille de matériau.
func DefaultTemperatures(material string) []float64 {
	fam := strings.ToUpper(material)
	if m, err := engineering.GetMaterial(material); err == nil && m != nil {
		fam = strings.ToUpper(m.Family)
	}
	lo := 190.0
	switch {
	case strings.HasPrefix(fam, "PETG"):
		lo = 220
	case strings.HasPrefix(fam, "ABS"), strings.HasPrefix(fam, "ASA"):
		lo = 230
	case strings.HasPrefix(fam, "TPU"):
		lo = 205
	case strings.HasPrefix(fam, "PC"):
		lo = 260
	case strings.HasPrefix(fam, "PA"):
		lo = 250
	}
	return []float64{lo, lo + 10, lo + 20, lo + 30, lo + 40}
}

var safeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Generate produit les pièces (worker CAO), les enregistre au catalogue et mémorise la série.
func (s *Service) Generate(ctx context.Context, in GenerateInput) (*GenerateOutput, error) {
	if s.d.Catalog == nil {
		return nil, fmt.Errorf("%w : catalogue absent", ErrUnavailable)
	}
	key, err := in.Key.Normalize()
	if err != nil {
		return nil, err
	}
	temps := in.Temperatures
	if len(temps) == 0 {
		temps = DefaultTemperatures(key.Material)
	}
	mode := in.TemperatureMode
	if mode == "" {
		mode = "series"
	}
	setID := contracts.NewID("cal")
	work := filepath.Join(s.d.WorkRoot, setID)
	if err := os.MkdirAll(work, 0o755); err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	prefix := "calib-" + safeName.ReplaceAllString(fmt.Sprintf("%g-%s", key.NozzleMM, key.Material), "_")
	req := map[string]any{"out_dir": work, "nozzle_diameter": key.NozzleMM, "name_prefix": prefix, "temperatures": temps, "temperature_mode": mode}
	if len(in.Kinds) > 0 {
		req["kinds"] = in.Kinds
	}
	for k, v := range map[string][]float64{"clearance_gaps": in.ClearanceGaps, "hole_diameters": in.HoleDiameters, "bridge_spans": in.BridgeSpans, "overhang_angles": in.OverhangAngles} {
		if len(v) > 0 {
			req[k] = v
		}
	}
	raw, err := s.d.Catalog.RunWorker(ctx, "calibration_set", req)
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
	var res struct {
		Pieces []struct {
			Kind        string            `json:"kind"`
			Name        string            `json:"name"`
			Description string            `json:"description"`
			Measure     []string          `json:"measure"`
			Parts       []map[string]any  `json:"parts"`
			BBox        map[string]any    `json:"bbox_mm"`
			Info        map[string]any    `json:"info"`
			Warnings    []string          `json:"warnings"`
			Segments    []map[string]any  `json:"segments"`
			LayerChange []map[string]any  `json:"layer_changes"`
			Gcode       []string          `json:"gcode_hint"`
			Overrides   []slicer.Override `json:"slicing_overrides"`
			TempC       *float64          `json:"nozzle_temperature_c"`
		} `json:"pieces"`
	}
	// Files utilise des clés minuscules côté worker : décodage dédié
	if err := json.Unmarshal(raw, &res); err != nil || len(res.Pieces) == 0 {
		return nil, errors.New("réponse du worker sans pièce de calibration")
	}
	var rawPieces struct {
		Pieces []struct {
			Files map[string]string `json:"files"`
		} `json:"pieces"`
	}
	_ = json.Unmarshal(raw, &rawPieces)

	out := &GenerateOutput{SetID: setID, Key: key}
	for i, p := range res.Pieces {
		files := rawPieces.Pieces[i].Files
		var gen []catalog.GeneratedFile
		for _, f := range []struct {
			path, ext string
			fmt       contracts.FileFormat
		}{{files["stl"], ".stl", contracts.FormatSTL}, {files["step"], ".step", contracts.FormatSTEP}} {
			g, err := s.storeFile(work, f.path, p.Name+f.ext, f.fmt)
			if err != nil {
				return nil, err
			}
			if g != nil {
				gen = append(gen, *g)
			}
		}
		m := contracts.Model{Name: p.Name, Tags: append([]string{"calibration", p.Kind}, in.Tags...),
			Description: p.Description,
			Provenance:  &contracts.Provenance{Source: "calibration", Note: "série " + setID + " pour " + key.label(), ImportedAt: s.now()}}
		m, ver, _, err := s.d.Catalog.RegisterGenerated(ctx, actor, m, "", "v1", "pièce de calibration ("+p.Kind+")", gen)
		if err != nil {
			return nil, err
		}
		pc := Piece{Kind: p.Kind, Name: p.Name, ModelID: m.ID, VersionID: ver.ID, Description: p.Description, Measure: p.Measure,
			BBoxMM: p.BBox, Info: p.Info, Parts: p.Parts, TemperatureC: p.TempC, Segments: p.Segments, LayerChanges: p.LayerChange,
			GcodeHint: p.Gcode, Overrides: p.Overrides, Warnings: p.Warnings}
		if in.Base != nil && s.d.Profiles != nil && len(p.Overrides) > 0 {
			mo, perr := s.d.Profiles.ModifyProfile(ctx, goals.ModifyInput{Base: in.Base, Modifications: p.Overrides,
				Note: fmt.Sprintf("calibration %s : %s", setID, p.Name)})
			if perr != nil {
				pc.Warnings = append(pc.Warnings, "profil découpable non créé : "+perr.Error())
			} else {
				pc.ProfileRevision = mo.Revision
			}
		}
		out.Pieces = append(out.Pieces, pc)
	}
	out.MeasuresTemplate = measuresTemplate(out.Pieces)
	out.Instructions = []string{
		"Imprimer les pièces avec EXACTEMENT les réglages, la buse et la bobine à calibrer (" + key.label() + "), plateau propre, pièces refroidies avant mesure.",
		"Mesurer au pied à coulisse (deux mesures croisées par cote, moyenne) ou à la main pour les jeux ; noter les valeurs en mm.",
		"Renvoyer les mesures avec record_calibration (même imprimante/buse/matériau/bobine) : les corrections sont ensuite appliquées automatiquement aux gabarits et aux calculs d'ingénierie.",
	}
	out.Note = "Les corrections ne valent que pour ce couple imprimante + buse + matériau (+ bobine) ; changer de bobine ou de buse impose de recalibrer. Les valeurs génériques restent utilisées tant qu'aucune mesure n'est enregistrée."
	doc, _ := json.Marshal(out)
	_, err = s.d.Store.DB().ExecContext(ctx, `INSERT INTO calibration_sets(id,printer_key,nozzle_mm,material,filament,doc,created_at) VALUES(?,?,?,?,?,?,?)`,
		setID, key.Printer, key.NozzleMM, key.Material, key.Filament, string(doc), store.FormatTime(s.now()))
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) storeFile(work, path, name string, f contracts.FileFormat) (*catalog.GeneratedFile, error) {
	if path == "" {
		return nil, nil
	}
	p := path
	if !filepath.IsAbs(p) {
		p = filepath.Join(work, p)
	}
	if rel, err := filepath.Rel(work, p); err != nil || strings.HasPrefix(rel, "..") {
		return nil, fmt.Errorf("fichier du worker hors du dossier de travail : %s", p)
	}
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

func measuresTemplate(ps []Piece) map[string]any {
	t := map[string]any{}
	has := map[string]bool{}
	for _, p := range ps {
		has[p.Kind] = true
	}
	if has["fit_comb"] {
		t["fit_slides_gap_mm"] = "plus petit jeu où la languette coulisse librement (mm)"
		t["fit_snug_gap_mm"] = "(facultatif) plus petit jeu où elle entre avec un léger serrage (mm)"
	}
	if has["hole_shaft"] {
		t["holes"] = []map[string]any{{"nominal": 5, "measured": "mm", "axis": "vertical|horizontal"}}
		t["shafts"] = []map[string]any{{"nominal": 5, "measured": "mm"}}
	}
	if has["shrink_cube"] {
		t["cube_mm"] = []string{"X", "Y", "Z"}
	}
	if has["temperature"] {
		t["best_temp_c"] = "température de la pièce / de l'étage le plus propre"
	}
	if has["bridge_overhang"] {
		t["bridge_max_span_mm"] = "plus grande portée sans affaissement"
		t["overhang_max_deg"] = "plus grand angle propre depuis la verticale"
	}
	return t
}

// ---- mesures ----

// RecordInput : mesures saisies par l'utilisateur.
type RecordInput struct {
	Key
	SetID    string   `json:"set_id,omitempty" jsonschema:"série générée par generate_calibration_set (facultatif)"`
	Measures Measures `json:"measures"`
	Notes    string   `json:"notes,omitempty"`
}

// RecordOutput : corrections calculées et calibration active résultante.
type RecordOutput struct {
	RecordID    string                  `json:"record_id"`
	Key         Key                     `json:"key"`
	Results     engineering.Calibration `json:"results" jsonschema:"corrections déduites de ces mesures"`
	Active      engineering.Calibration `json:"active" jsonschema:"calibration active après fusion avec les mesures antérieures de la même clé"`
	Explanation []string                `json:"explanation"`
	Effects     []string                `json:"effects" jsonschema:"ce qui change concrètement dans les gabarits et calculs d'ingénierie"`
	Note        string                  `json:"note"`
}

// Record valide les mesures, calcule les corrections, les journalise (immuable) et met à jour la calibration active.
func (s *Service) Record(ctx context.Context, in RecordInput) (*RecordOutput, error) {
	key, err := in.Key.Normalize()
	if err != nil {
		return nil, err
	}
	cal, notes, err := Compute(in.Measures)
	if err != nil {
		return nil, err
	}
	now := s.now()
	cal.CalibratedAt = now.Format("2006-01-02")
	cal.Provenance = fmt.Sprintf("calibré le %s (%s)", cal.CalibratedAt, key.label())
	if in.SetID != "" {
		var x int
		if err := s.d.Store.DB().QueryRowContext(ctx, `SELECT 1 FROM calibration_sets WHERE id=?`, in.SetID).Scan(&x); errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("série %q : %w", in.SetID, store.ErrNotFound)
		} else if err != nil {
			return nil, err
		}
	}
	recID := contracts.NewID("calrec")
	mj, _ := json.Marshal(in.Measures)
	rj, _ := json.Marshal(cal)
	var active *engineering.Calibration
	err = s.d.Store.Tx(ctx, func(tx *sql.Tx) error {
		var setID any
		if in.SetID != "" {
			setID = in.SetID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO calibration_records(id,set_id,printer_key,nozzle_mm,material,filament,measures,results,notes,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			recID, setID, key.Printer, key.NozzleMM, key.Material, key.Filament, string(mj), string(rj), in.Notes, store.FormatTime(now)); err != nil {
			return err
		}
		cur, err := loadProfile(ctx, tx, key)
		if err != nil {
			return err
		}
		active = &cal
		if cur != nil {
			active = Merge(cur, &cal)
		}
		aj, _ := json.Marshal(active)
		if _, err := tx.ExecContext(ctx, `INSERT INTO calibration_profiles(printer_key,nozzle_mm,material,filament,doc,record_id,updated_at) VALUES(?,?,?,?,?,?,?)
			ON CONFLICT(printer_key,nozzle_mm,material,filament) DO UPDATE SET doc=excluded.doc, record_id=excluded.record_id, updated_at=excluded.updated_at`,
			key.Printer, key.NozzleMM, key.Material, key.Filament, string(aj), recID, store.FormatTime(now)); err != nil {
			return err
		}
		return s.d.Store.AuditTx(ctx, tx, actor, "calibration.record", "calibration_record", recID, map[string]any{"key": key.label()})
	})
	if err != nil {
		return nil, err
	}
	return &RecordOutput{RecordID: recID, Key: key, Results: cal, Active: *active, Explanation: notes, Effects: effects(&cal),
		Note: "Mesures conservées (journal immuable). Ces corrections priment désormais sur les valeurs génériques pour " + key.label() +
			", avec la provenance « " + cal.Provenance + " »."}, nil
}

func effects(c *engineering.Calibration) []string {
	var e []string
	if c.SlidesGapMM != nil || c.SnugGapMM != nil {
		e = append(e, "Jeux d'ajustement (libre/glissant/serré) des gabarits et de fit_clearance : valeurs mesurées au lieu des valeurs génériques.")
	}
	if c.HoleVertical != nil || c.HoleHorizontal != nil {
		e = append(e, "Diamètres de trous des gabarits (vis, alésages, perçages) compensés avec la droite mesurée.")
	}
	if c.Shaft != nil {
		e = append(e, "Diamètres d'arbres/pions corrigés d'après les arbres mesurés.")
	}
	if c.ShrinkXY != nil || c.ShrinkZ != nil {
		e = append(e, "Facteurs d'échelle de retrait disponibles (à appliquer avec modify_geometry scale sur les cotes extérieures critiques).")
	}
	if c.BestTempC != nil {
		e = append(e, "Température de buse recommandée mémorisée pour ce matériau.")
	}
	if c.BridgeMaxSpanMM != nil || c.OverhangMaxDeg != nil {
		e = append(e, "Limites de ponts / surplombs mémorisées (avertissements de conception).")
	}
	return e
}

// ---- résolution ----

func loadProfile(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, k Key) (*engineering.Calibration, error) {
	var doc string
	err := q.QueryRowContext(ctx, `SELECT doc FROM calibration_profiles WHERE printer_key=? AND ABS(nozzle_mm-?)<0.001 AND material=? AND filament=?`,
		k.Printer, k.NozzleMM, k.Material, k.Filament).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c engineering.Calibration
	if err := json.Unmarshal([]byte(doc), &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Resolution : calibration effective pour une clé, du plus général au plus spécifique (le plus spécifique l'emporte champ par champ).
type Resolution struct {
	Calibration *engineering.Calibration `json:"calibration,omitempty"`
	Matched     []string                 `json:"matched" jsonschema:"clés utilisées, de la plus générale à la plus spécifique"`
	Note        string                   `json:"note"`
}

// Resolve cherche la calibration applicable. Sans correspondance : Calibration nil (valeurs génériques).
func (s *Service) Resolve(ctx context.Context, k Key) (*Resolution, error) {
	key, err := k.Normalize()
	if err != nil {
		return nil, err
	}
	chain := []Key{{NozzleMM: key.NozzleMM, Material: key.Material}}
	if key.Filament != "" {
		chain = append(chain, Key{NozzleMM: key.NozzleMM, Material: key.Material, Filament: key.Filament})
	}
	if key.Printer != "" {
		chain = append(chain, Key{Printer: key.Printer, NozzleMM: key.NozzleMM, Material: key.Material})
		if key.Filament != "" {
			chain = append(chain, key)
		}
	}
	var merged *engineering.Calibration
	res := &Resolution{Matched: []string{}}
	var provs []string
	for _, c := range chain {
		p, err := loadProfile(ctx, s.d.Store.DB(), c)
		if err != nil {
			return nil, err
		}
		if p == nil {
			continue
		}
		res.Matched = append(res.Matched, c.label())
		provs = append(provs, p.Provenance)
		if merged == nil {
			merged = p
		} else {
			merged = Merge(merged, p)
		}
	}
	if merged == nil {
		res.Note = "Aucune calibration pour " + key.label() + " : valeurs génériques (indicatives) utilisées."
		return res, nil
	}
	merged.Provenance = strings.Join(dedupe(provs), " ; ")
	res.Calibration = merged
	res.Note = "Calibration appliquée : " + merged.Provenance
	return res, nil
}

// ForEngineering renvoie la calibration (nil si aucune) — signature commode pour la bibliothèque de gabarits.
func (s *Service) ForEngineering(ctx context.Context, k Key) (*engineering.Calibration, error) {
	r, err := s.Resolve(ctx, k)
	if err != nil {
		return nil, err
	}
	return r.Calibration, nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// ---- historique ----

// HistoryItem : une calibration enregistrée.
type HistoryItem struct {
	RecordID  string                  `json:"record_id"`
	Key       Key                     `json:"key"`
	Results   engineering.Calibration `json:"results"`
	Notes     string                  `json:"notes,omitempty"`
	CreatedAt time.Time               `json:"created_at"`
}

// History liste les mesures enregistrées, les plus récentes d'abord (filtres facultatifs sur la clé).
func (s *Service) History(ctx context.Context, material string, nozzle float64, limit int) ([]HistoryItem, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT id,printer_key,nozzle_mm,material,filament,results,notes,created_at FROM calibration_records WHERE 1=1`
	var args []any
	if material != "" {
		q += ` AND material=?`
		args = append(args, strings.ToUpper(strings.TrimSpace(material)))
	}
	if nozzle > 0 {
		q += ` AND ABS(nozzle_mm-?)<0.001`
		args = append(args, math.Round(nozzle*100)/100)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.d.Store.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryItem{}
	for rows.Next() {
		var it HistoryItem
		var res, ts string
		if err := rows.Scan(&it.RecordID, &it.Key.Printer, &it.Key.NozzleMM, &it.Key.Material, &it.Key.Filament, &res, &it.Notes, &ts); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(res), &it.Results)
		it.CreatedAt, _ = store.ParseTime(ts)
		out = append(out, it)
	}
	return out, rows.Err()
}
