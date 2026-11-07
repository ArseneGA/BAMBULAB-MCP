package insights

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/goals"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Defect est un défaut cochable sur une fiche.
type Defect struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Defects est le catalogue des défauts reconnus.
var Defects = []Defect{
	{"warping", "Gauchissement : coins ou bords qui se soulèvent"},
	{"stringing", "Fils et toiles entre les parties"},
	{"layer_lines", "Couches trop visibles"},
	{"delamination", "Couches qui se décollent ou se fendent"},
	{"adhesion", "Mauvaise adhérence au plateau (première couche)"},
	{"dimensions", "Dimensions hors tolérance"},
	{"elephant_foot", "Patte d'éléphant (base élargie)"},
	{"under_extrusion", "Sous-extrusion : trous, couches maigres"},
	{"over_extrusion", "Sur-extrusion : excès de matière, surfaces bombées"},
	{"blobs", "Boutons, points et coutures marqués"},
	{"supports", "Supports difficiles à retirer ou surface abîmée"},
	{"overhang", "Surplombs affaissés ou rugueux"},
	{"layer_shift", "Décalage de couches"},
	{"surface", "Surface rugueuse ou irrégulière"},
	{"other", "Autre défaut (préciser dans le commentaire)"},
}

func validDefect(id string) bool {
	for _, d := range Defects {
		if d.ID == id {
			return true
		}
	}
	return false
}

// Quantities regroupe durée et masse.
type Quantities struct {
	Seconds     float64   `json:"seconds,omitempty"`
	Grams       float64   `json:"grams,omitempty"`
	Basis       string    `json:"basis,omitempty"`
	BySlotGrams []float64 `json:"by_slot_grams,omitempty"`
}

// EffectiveSettings sont les réglages effectifs relevés sur l'impression.
type EffectiveSettings struct {
	Machine     string            `json:"machine,omitempty"`
	Process     string            `json:"process,omitempty"`
	Filaments   []string          `json:"filaments,omitempty"`
	NozzleMM    float64           `json:"nozzle_mm,omitempty"`
	Overrides   []slicer.Override `json:"overrides,omitempty"`
	Orientation string            `json:"orientation,omitempty"`
	BedType     string            `json:"bed_type,omitempty"`
	Plate       int               `json:"plate,omitempty"`
	Layers      int               `json:"layers,omitempty"`
	Supports    bool              `json:"supports,omitempty"`
	Deviations  []goals.Deviation `json:"deviations,omitempty" jsonschema:"écarts entre réglages prévus et réellement utilisés (record_print_result)"`
}

// LogEntry est la fiche de journal d'une impression terminée, échouée ou annulée.
type LogEntry struct {
	ID             string            `json:"id"`
	PrintJobID     string            `json:"print_job_id"`
	Name           string            `json:"name,omitempty"`
	PrinterID      string            `json:"printer_id,omitempty"`
	PrinterModel   string            `json:"printer_model,omitempty"`
	ModelID        string            `json:"model_id,omitempty"`
	ModelName      string            `json:"model_name,omitempty"`
	CollectionID   string            `json:"collection_id,omitempty"`
	CollectionName string            `json:"collection_name,omitempty"`
	State          string            `json:"state" jsonschema:"état final de l'impression : COMPLETED | FAILED | CANCELLED"`
	Outcome        string            `json:"outcome" jsonschema:"success | partial | failed | cancelled (modifiable par vous)"`
	MaterialFamily string            `json:"material_family,omitempty"`
	StartedAt      time.Time         `json:"started_at,omitzero"`
	FinishedAt     time.Time         `json:"finished_at"`
	Settings       EffectiveSettings `json:"settings"`
	Estimated      Quantities        `json:"estimated"`
	Actual         Quantities        `json:"actual"`
	DurationDelta  *float64          `json:"duration_delta_pct,omitempty" jsonschema:"écart de durée réelle par rapport à l'estimation, en %"`
	MassDelta      *float64          `json:"mass_delta_pct,omitempty" jsonschema:"écart de masse réelle par rapport à l'estimation, en % (seulement si la masse a été pesée)"`
	PlannedCost    *CostEstimate     `json:"planned_cost,omitempty"`
	ActualCost     *CostEstimate     `json:"actual_cost,omitempty"`
	Photos         []string          `json:"photos,omitempty" jsonschema:"références de blobs (sha256) des photos de la pièce"`
	Rating         int               `json:"rating,omitempty" jsonschema:"note de 1 à 5 (0 : non noté)"`
	Comment        string            `json:"comment,omitempty"`
	Defects        []string          `json:"defects,omitempty"`
	ExperimentID   string            `json:"experiment_id,omitempty"`
	CandidateID    string            `json:"candidate_id,omitempty"`
	ResultIDs      []string          `json:"result_ids,omitempty" jsonschema:"retours d'expérience (record_print_result) rattachés : ils alimentent get_print_memory"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

func outcomeOf(state string) string {
	switch state {
	case "COMPLETED":
		return "success"
	case "CANCELLED":
		return "cancelled"
	default:
		return "failed"
	}
}

func pctDelta(actual, est float64) *float64 {
	if est <= 0 || actual <= 0 {
		return nil
	}
	v := round2((actual - est) / est * 100)
	return &v
}

// buildEntry construit la fiche initiale d'une impression terminée.
func (s *Service) buildEntry(ctx context.Context, j *jobDoc, set Settings) *LogEntry {
	now := s.now()
	e := &LogEntry{ID: contracts.NewID("log"), PrintJobID: j.ID, Name: j.Name, PrinterID: j.PrinterID, ModelID: j.ModelID,
		State: j.State, Outcome: outcomeOf(j.State), CreatedAt: now, UpdatedAt: now}
	hint := s.printerModelHint(ctx, j)
	if k := modelKey(hint); k != "" {
		e.PrinterModel = loadPower().Models[k].Name
	} else {
		e.PrinterModel = hint
	}
	start, end, ok := j.observed()
	if ok {
		e.StartedAt = start
	}
	e.FinishedAt = end
	if e.FinishedAt.IsZero() {
		e.FinishedAt = j.UpdatedAt
	}
	if e.FinishedAt.IsZero() {
		e.FinishedAt = now
	}
	if j.ModelID != "" {
		var mn, cid, cn sql.NullString
		if s.cfg.Store.DB().QueryRowContext(ctx, `SELECT m.name, m.collection_id, c.name FROM models m LEFT JOIN collections c ON c.id=m.collection_id WHERE m.id=?`, j.ModelID).Scan(&mn, &cid, &cn) == nil {
			e.ModelName, e.CollectionID, e.CollectionName = mn.String, cid.String, cn.String
		}
	}
	if j.Slice != nil {
		e.Settings.Machine, e.Settings.Process, e.Settings.Filaments = j.Slice.Machine, j.Slice.Process, j.Slice.Filaments
		e.Settings.NozzleMM, e.Settings.Overrides = j.Slice.Nozzle, j.Slice.Overrides
		if j.Slice.Orientation != nil {
			e.Settings.Orientation = j.Slice.Orientation.Label
		}
	}
	if j.Summary != nil {
		if e.Settings.Machine == "" {
			e.Settings.Machine = j.Summary.MachinePreset
		}
		if e.Settings.Process == "" {
			e.Settings.Process = j.Summary.Process
		}
		if len(e.Settings.Filaments) == 0 {
			e.Settings.Filaments = j.Summary.Filaments
		}
		e.Settings.BedType = j.Summary.BedType
	}
	e.Settings.Plate = j.Plate
	if pl := j.plate(); pl != nil {
		e.Settings.Layers, e.Settings.Supports = pl.Layers, pl.SupportUsed
		e.Estimated.Seconds, e.Estimated.Grams = pl.PredictedSeconds, round2(pl.WeightGrams)
		e.Estimated.Basis = "annoncé par le slicer dans l'archive découpée"
		for _, f := range pl.Filaments {
			e.Estimated.BySlotGrams = append(e.Estimated.BySlotGrams, round2(f.Grams))
		}
	}
	if cp, err := s.planFromJob(ctx, j, 0); err == nil {
		pc := compute(cp, set)
		e.PlannedCost = &pc
		e.MaterialFamily = dominantFamily(cp)
		e.Estimated.Grams = pc.Grams
		e.ActualCost = s.actualCost(ctx, j, cp, nil, nil, set)
		if e.ActualCost != nil {
			e.Actual.Grams = e.ActualCost.Grams
			if j.State == "COMPLETED" {
				e.Actual.Basis = "estimée : égale à la masse prévue par le slicer (non pesée)"
			} else {
				e.Actual.Basis = "estimée au prorata de l'avancement (non pesée)"
			}
		}
	}
	if ok {
		e.Actual.Seconds = end.Sub(start).Seconds()
		e.DurationDelta = pctDelta(e.Actual.Seconds, e.Estimated.Seconds)
	}
	if e.MaterialFamily == "" && len(e.Settings.Filaments) > 0 {
		e.MaterialFamily = MaterialFamily(e.Settings.Filaments[0])
	}
	return e
}

func dominantFamily(cp costPlan) string {
	best, fam := -1.0, ""
	for _, f := range cp.Filaments {
		if f.Grams > best && f.Family != "" {
			best, fam = f.Grams, f.Family
		}
	}
	return fam
}

func (s *Service) insertEntry(ctx context.Context, e *LogEntry) (bool, error) {
	doc, _ := json.Marshal(e)
	inserted := false
	err := s.cfg.Store.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO print_logs(id,print_job_id,printer_id,model_id,material_family,outcome,finished_at,doc,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(print_job_id) DO NOTHING`,
			e.ID, e.PrintJobID, e.PrinterID, e.ModelID, e.MaterialFamily, e.Outcome, store.FormatTime(e.FinishedAt), string(doc), store.FormatTime(e.CreatedAt), store.FormatTime(e.UpdatedAt))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			inserted = true
			return s.cfg.Store.AuditTx(ctx, tx, actor, "insights.log.create", "print_log", e.ID, map[string]any{"print_job": e.PrintJobID, "outcome": e.Outcome})
		}
		return nil
	})
	return inserted, err
}

func (s *Service) saveEntry(ctx context.Context, e *LogEntry, audit string, detail any) error {
	e.UpdatedAt = s.now()
	doc, _ := json.Marshal(e)
	return s.cfg.Store.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE print_logs SET printer_id=?, model_id=?, material_family=?, outcome=?, finished_at=?, doc=?, updated_at=? WHERE id=?`,
			e.PrinterID, e.ModelID, e.MaterialFamily, e.Outcome, store.FormatTime(e.FinishedAt), string(doc), store.FormatTime(e.UpdatedAt), e.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("fiche %q : %w", e.ID, store.ErrNotFound)
		}
		return s.cfg.Store.AuditTx(ctx, tx, actor, audit, "print_log", e.ID, detail)
	})
}

// SyncResult dit ce que la synchronisation a fait.
type SyncResult struct {
	Created int `json:"created"`
	Merged  int `json:"merged_results"`
}

// SyncJournal crée la fiche de chaque impression terminée qui n'en a pas encore, puis rattache les retours
// d'expérience (record_print_result avec print_job_id). Idempotent.
func (s *Service) SyncJournal(ctx context.Context) (SyncResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.syncLocked(ctx)
}

func (s *Service) syncLocked(ctx context.Context) (SyncResult, error) {
	var res SyncResult
	set, err := s.GetSettings(ctx)
	if err != nil {
		return res, err
	}
	jobs, err := s.listJobs(ctx)
	if err != nil {
		return res, err
	}
	have := map[string]bool{}
	rows, err := s.cfg.Store.DB().QueryContext(ctx, `SELECT print_job_id FROM print_logs`)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			have[id] = true
		}
	}
	rows.Close()
	for _, j := range jobs {
		if !isTerminal(j.State) || have[j.ID] {
			continue
		}
		ok, err := s.insertEntry(ctx, s.buildEntry(ctx, j, set))
		if err != nil {
			return res, err
		}
		if ok {
			res.Created++
		}
	}
	n, err := s.mergeResultsLocked(ctx)
	res.Merged = n
	return res, err
}

// mergeResultsLocked rattache aux fiches les retours d'expérience portant leur print_job_id.
func (s *Service) mergeResultsLocked(ctx context.Context) (int, error) {
	rows, err := s.cfg.Store.DB().QueryContext(ctx,
		`SELECT id, doc FROM print_results WHERE json_extract(doc,'$.print_job_id') IS NOT NULL AND json_extract(doc,'$.print_job_id') != '' ORDER BY created_at LIMIT 5000`)
	if err != nil {
		return 0, err
	}
	type pr struct {
		id string
		r  goals.PrintResult
	}
	var list []pr
	for rows.Next() {
		var id, doc string
		if rows.Scan(&id, &doc) != nil {
			continue
		}
		var r goals.PrintResult
		if json.Unmarshal([]byte(doc), &r) == nil {
			list = append(list, pr{id, r})
		}
	}
	rows.Close()
	merged := 0
	for _, p := range list {
		e, err := s.entryByJob(ctx, p.r.PrintJobID)
		if err != nil {
			continue // impression sans fiche (non terminée) : rattaché plus tard
		}
		if hasStr(e.ResultIDs, p.id) {
			continue
		}
		applyResult(e, p.id, p.r)
		if err := s.saveEntry(ctx, e, "insights.log.merge_result", map[string]any{"result": p.id}); err != nil {
			return merged, err
		}
		merged++
	}
	return merged, nil
}

func applyResult(e *LogEntry, id string, r goals.PrintResult) {
	e.ResultIDs = append(e.ResultIDs, id)
	e.ExperimentID, e.CandidateID = r.ExperimentID, r.CandidateID
	switch r.Outcome {
	case "success", "partial", "failed":
		e.Outcome = r.Outcome
	}
	if r.Rating > 0 && e.Rating == 0 {
		e.Rating = r.Rating
	}
	for _, d := range r.Defects {
		d = strings.TrimSpace(d)
		if !validDefect(d) {
			e.Comment = strings.TrimSpace(e.Comment + "\nDéfaut déclaré : " + d)
			d = "other"
		}
		if !hasStr(e.Defects, d) {
			e.Defects = append(e.Defects, d)
		}
	}
	if o := strings.TrimSpace(r.Observations); o != "" && !strings.Contains(e.Comment, o) {
		e.Comment = strings.TrimSpace(e.Comment + "\n" + o)
	}
	for _, p := range r.Photos {
		if !hasStr(e.Photos, p) {
			e.Photos = append(e.Photos, p)
		}
	}
	e.Settings.Deviations = append(e.Settings.Deviations, r.Deviations...)
	if r.ActualMassG > 0 {
		e.Actual.Grams, e.Actual.Basis = r.ActualMassG, "pesée déclarée"
		e.MassDelta = pctDelta(r.ActualMassG, e.Estimated.Grams)
	}
	if r.ActualMinutes > 0 {
		e.Actual.Seconds = r.ActualMinutes * 60
		e.DurationDelta = pctDelta(e.Actual.Seconds, e.Estimated.Seconds)
	}
}

func (s *Service) entryByJob(ctx context.Context, jobID string) (*LogEntry, error) {
	return s.oneEntry(ctx, `SELECT doc FROM print_logs WHERE print_job_id=?`, jobID)
}

func (s *Service) oneEntry(ctx context.Context, q string, arg any) (*LogEntry, error) {
	var raw string
	err := s.cfg.Store.DB().QueryRowContext(ctx, q, arg).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("fiche de journal : %w", store.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	var e LogEntry
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// ListQuery filtre le journal.
type ListQuery struct {
	PrinterID      string `json:"printer_id,omitempty"`
	MaterialFamily string `json:"material_family,omitempty" jsonschema:"famille de matière (PLA, PETG...)"`
	Outcome        string `json:"outcome,omitempty" jsonschema:"success | partial | failed | cancelled"`
	ModelID        string `json:"model_id,omitempty"`
	Limit          int    `json:"limit,omitempty" jsonschema:"défaut 30, maximum 200"`
	Offset         int    `json:"offset,omitempty"`
}

// ListOutput liste des fiches, les plus récentes d'abord.
type ListOutput struct {
	Entries []LogEntry `json:"entries"`
	Total   int        `json:"total"`
	Created int        `json:"created_from_finished_prints" jsonschema:"fiches créées à l'instant pour des impressions terminées qui n'en avaient pas"`
	Defects []Defect   `json:"defect_catalog"`
}

// ListLogs synchronise puis liste le journal.
func (s *Service) ListLogs(ctx context.Context, q ListQuery) (*ListOutput, error) {
	sr, err := s.SyncJournal(ctx)
	if err != nil {
		return nil, err
	}
	if q.Outcome != "" && q.Outcome != "success" && q.Outcome != "partial" && q.Outcome != "failed" && q.Outcome != "cancelled" {
		return nil, invalid("outcome %q inconnu (success, partial, failed, cancelled)", q.Outcome)
	}
	if q.Limit <= 0 {
		q.Limit = 30
	}
	if q.Limit > 200 {
		q.Limit = 200
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	where, args := []string{"1=1"}, []any{}
	add := func(col, v string) {
		if v != "" {
			where = append(where, col+"=?")
			args = append(args, v)
		}
	}
	add("printer_id", q.PrinterID)
	add("material_family", strings.ToUpper(q.MaterialFamily))
	add("outcome", q.Outcome)
	add("model_id", q.ModelID)
	w := strings.Join(where, " AND ")
	out := &ListOutput{Created: sr.Created, Defects: Defects, Entries: []LogEntry{}}
	if err := s.cfg.Store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM print_logs WHERE `+w, args...).Scan(&out.Total); err != nil {
		return nil, err
	}
	rows, err := s.cfg.Store.DB().QueryContext(ctx, `SELECT doc FROM print_logs WHERE `+w+` ORDER BY finished_at DESC, id LIMIT ? OFFSET ?`, append(args, q.Limit, q.Offset)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var e LogEntry
		if rows.Scan(&raw) == nil && json.Unmarshal([]byte(raw), &e) == nil {
			out.Entries = append(out.Entries, e)
		}
	}
	return out, rows.Err()
}

// GetLog renvoie une fiche par son identifiant ou par celui de l'impression.
func (s *Service) GetLog(ctx context.Context, id string) (*LogEntry, error) {
	if _, err := s.SyncJournal(ctx); err != nil {
		return nil, err
	}
	return s.findEntry(ctx, id)
}

func (s *Service) findEntry(ctx context.Context, id string) (*LogEntry, error) {
	if id == "" {
		return nil, invalid("identifiant de fiche ou d'impression requis")
	}
	e, err := s.oneEntry(ctx, `SELECT doc FROM print_logs WHERE id=?`, id)
	if errors.Is(err, store.ErrNotFound) {
		e, err = s.oneEntry(ctx, `SELECT doc FROM print_logs WHERE print_job_id=?`, id)
	}
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("fiche %q (ni fiche ni impression terminée de ce nom) : %w", id, store.ErrNotFound)
	}
	return e, err
}

// UpdateInput complète une fiche. Les champs omis ne changent pas.
type UpdateInput struct {
	ID            string   `json:"id" jsonschema:"identifiant de la fiche (log_...) ou de l'impression (print job)"`
	Outcome       string   `json:"outcome,omitempty" jsonschema:"success | partial | failed | cancelled"`
	Rating        *int     `json:"rating,omitempty" jsonschema:"note de 1 à 5"`
	Comment       *string  `json:"comment,omitempty"`
	Defects       []string `json:"defects,omitempty" jsonschema:"remplace la liste des défauts (liste vide = aucun défaut) : warping, stringing, layer_lines, delamination, adhesion, dimensions, elephant_foot, under_extrusion, over_extrusion, blobs, supports, overhang, layer_shift, surface, other"`
	ActualGrams   *float64 `json:"actual_mass_g,omitempty" jsonschema:"masse de filament réellement consommée (pesée), en grammes"`
	ActualMinutes *float64 `json:"actual_duration_minutes,omitempty" jsonschema:"durée réelle d'impression en minutes (sinon la durée observée)"`
	PhotoBlobs    []string `json:"photo_blobs,omitempty" jsonschema:"références de blobs (sha256) d'images déjà stockées, ajoutées à la fiche"`
	ExperimentID  string   `json:"experiment_id,omitempty" jsonschema:"avec candidate_id : verse le retour dans la mémoire des essais (get_print_memory)"`
	CandidateID   string   `json:"candidate_id,omitempty"`
}

const maxPhotos = 8

// UpdateLog modifie la fiche et, si elle est liée à une expérience, alimente la mémoire existante.
func (s *Service) UpdateLog(ctx context.Context, in UpdateInput) (*LogEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.syncLocked(ctx); err != nil {
		return nil, err
	}
	e, err := s.findEntry(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	set, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	changed := []string{}
	if in.Outcome != "" {
		switch in.Outcome {
		case "success", "partial", "failed", "cancelled":
		default:
			return nil, invalid("outcome %q inconnu (success, partial, failed, cancelled)", in.Outcome)
		}
		e.Outcome = in.Outcome
		changed = append(changed, "outcome")
	}
	if in.Rating != nil {
		if *in.Rating < 1 || *in.Rating > 5 {
			return nil, invalid("rating : entre 1 et 5")
		}
		e.Rating = *in.Rating
		changed = append(changed, "rating")
	}
	if in.Comment != nil {
		if len([]rune(*in.Comment)) > 4000 {
			return nil, invalid("comment : 4000 caractères au plus")
		}
		e.Comment = strings.TrimSpace(*in.Comment)
		changed = append(changed, "comment")
	}
	if in.Defects != nil {
		seen := map[string]bool{}
		ds := []string{}
		for _, d := range in.Defects {
			d = strings.ToLower(strings.TrimSpace(d))
			if !validDefect(d) {
				ids := make([]string, len(Defects))
				for i, x := range Defects {
					ids[i] = x.ID
				}
				return nil, invalid("défaut %q inconnu (%s)", d, strings.Join(ids, ", "))
			}
			if !seen[d] {
				seen[d] = true
				ds = append(ds, d)
			}
		}
		e.Defects = ds
		changed = append(changed, "defects")
	}
	for _, p := range in.PhotoBlobs {
		if err := s.checkImageBlob(p); err != nil {
			return nil, err
		}
		if !hasStr(e.Photos, p) {
			if len(e.Photos) >= maxPhotos {
				return nil, invalid("%d photos au plus par fiche", maxPhotos)
			}
			e.Photos = append(e.Photos, p)
			changed = append(changed, "photo")
		}
	}
	if in.ActualGrams != nil || in.ActualMinutes != nil {
		if in.ActualGrams != nil && (*in.ActualGrams < 0 || *in.ActualGrams > 100000) {
			return nil, invalid("actual_mass_g : valeur positive plausible attendue")
		}
		if in.ActualMinutes != nil && (*in.ActualMinutes < 0 || *in.ActualMinutes > 60*24*30) {
			return nil, invalid("actual_duration_minutes : valeur positive plausible attendue")
		}
		if in.ActualGrams != nil && *in.ActualGrams > 0 {
			e.Actual.Grams, e.Actual.Basis = *in.ActualGrams, "pesée déclarée"
			e.MassDelta = pctDelta(*in.ActualGrams, e.Estimated.Grams)
		}
		if in.ActualMinutes != nil && *in.ActualMinutes > 0 {
			e.Actual.Seconds = *in.ActualMinutes * 60
			e.DurationDelta = pctDelta(e.Actual.Seconds, e.Estimated.Seconds)
		}
		s.recost(ctx, e, set)
		changed = append(changed, "actuals")
	}
	if (in.ExperimentID != "") != (in.CandidateID != "") {
		return nil, invalid("experiment_id et candidate_id vont ensemble")
	}
	if in.ExperimentID != "" {
		if err := s.pushToMemory(ctx, e, in.ExperimentID, in.CandidateID); err != nil {
			return nil, err
		}
		changed = append(changed, "memory")
	}
	if len(changed) == 0 {
		return e, nil
	}
	if err := s.saveEntry(ctx, e, "insights.log.update", map[string]any{"fields": changed}); err != nil {
		return nil, err
	}
	return e, nil
}

// recost recalcule le coût réel d'une fiche avec les masses et la durée de la fiche.
func (s *Service) recost(ctx context.Context, e *LogEntry, set Settings) {
	j, err := s.loadJob(ctx, e.PrintJobID)
	if err != nil {
		return
	}
	cp, err := s.planFromJob(ctx, j, 0)
	if err != nil {
		return
	}
	pc := compute(cp, set)
	e.PlannedCost = &pc
	var g, sec *float64
	if e.Actual.Grams > 0 && strings.HasPrefix(e.Actual.Basis, "pesée") {
		v := e.Actual.Grams
		g = &v
	}
	if e.Actual.Seconds > 0 {
		v := e.Actual.Seconds
		sec = &v
	}
	if ac := s.actualCost(ctx, j, cp, g, sec, set); ac != nil {
		e.ActualCost = ac
		if g == nil {
			e.Actual.Grams = ac.Grams
		}
	}
}

func (s *Service) recomputeHistory(ctx context.Context, set Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.cfg.Store.DB().QueryContext(ctx, `SELECT doc FROM print_logs`)
	if err != nil {
		return err
	}
	var all []LogEntry
	for rows.Next() {
		var raw string
		var e LogEntry
		if rows.Scan(&raw) == nil && json.Unmarshal([]byte(raw), &e) == nil {
			all = append(all, e)
		}
	}
	rows.Close()
	for i := range all {
		s.recost(ctx, &all[i], set)
		if err := s.saveEntry(ctx, &all[i], "insights.log.recost", nil); err != nil {
			return err
		}
	}
	return nil
}

// pushToMemory verse le retour de la fiche dans la mémoire des essais (goals.RecordResult).
func (s *Service) pushToMemory(ctx context.Context, e *LogEntry, expID, candID string) error {
	if s.cfg.Goals == nil {
		return invalid("moteur d'objectifs indisponible : impossible d'alimenter la mémoire des essais")
	}
	if len(e.ResultIDs) > 0 {
		return invalid("cette fiche alimente déjà la mémoire (résultat %s)", e.ResultIDs[0])
	}
	if e.Outcome == "cancelled" {
		return invalid("une impression annulée n'alimente pas la mémoire : choisir success, partial ou failed pour l'issue")
	}
	obs := strings.TrimSpace(e.Comment)
	if len(e.Defects) > 0 {
		obs = strings.TrimSpace(obs + "\nDéfauts : " + strings.Join(e.Defects, ", "))
	}
	if e.Rating > 0 {
		obs = strings.TrimSpace(obs + fmt.Sprintf("\nNote : %d/5", e.Rating))
	}
	if obs == "" {
		obs = "Fiche de journal " + e.ID
	}
	in := goals.ResultInput{ExperimentID: expID, CandidateID: candID, Outcome: e.Outcome, Observations: obs, Photos: e.Photos,
		PrintJobID: e.PrintJobID, Rating: e.Rating, Defects: e.Defects}
	if strings.HasPrefix(e.Actual.Basis, "pesée") {
		in.ActualMassG = e.Actual.Grams
	}
	if e.Actual.Seconds > 0 {
		in.ActualMinutes = e.Actual.Seconds / 60
	}
	r, err := s.cfg.Goals.RecordResult(ctx, in)
	if err != nil {
		return err
	}
	e.ResultIDs = append(e.ResultIDs, r.ID)
	e.ExperimentID, e.CandidateID = expID, candID
	e.Settings.Deviations = append(e.Settings.Deviations, r.Deviations...)
	return nil
}

// ---- photos ----

// MaxPhotoBytes : taille maximale d'une photo de fiche.
const MaxPhotoBytes = 12 << 20

// imageType reconnaît JPEG, PNG et WebP par leur signature ; "" sinon (SVG et autres refusés).
func imageType(head []byte) string {
	switch {
	case len(head) >= 3 && head[0] == 0xFF && head[1] == 0xD8 && head[2] == 0xFF:
		return "image/jpeg"
	case len(head) >= 8 && bytes.Equal(head[:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png"
	case len(head) >= 12 && bytes.Equal(head[:4], []byte("RIFF")) && bytes.Equal(head[8:12], []byte("WEBP")):
		return "image/webp"
	}
	return ""
}

func (s *Service) checkImageBlob(sha string) error {
	if s.cfg.Blobs == nil || !s.cfg.Blobs.Has(sha) {
		return invalid("photo %q : blob introuvable (déposer d'abord l'image, ou utiliser l'ajout de photo de la fiche)", sha)
	}
	f, err := s.cfg.Blobs.Open(sha)
	if err != nil {
		return invalid("photo %q : blob illisible", sha)
	}
	defer f.Close()
	head := make([]byte, 16)
	n, _ := io.ReadFull(f, head)
	if imageType(head[:n]) == "" {
		return invalid("photo %q : ce blob n'est pas une image JPEG, PNG ou WebP", sha)
	}
	return nil
}

// AddPhoto range une photo (JPEG, PNG ou WebP) dans le stockage de blobs et la rattache à la fiche.
func (s *Service) AddPhoto(ctx context.Context, id string, r io.Reader) (*LogEntry, string, error) {
	if s.cfg.Blobs == nil {
		return nil, "", errors.New("stockage de blobs indisponible")
	}
	data, err := io.ReadAll(io.LimitReader(r, MaxPhotoBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > MaxPhotoBytes {
		return nil, "", invalid("photo trop volumineuse (%d Mio au plus)", MaxPhotoBytes>>20)
	}
	if imageType(data) == "" {
		return nil, "", invalid("la photo doit être une image JPEG, PNG ou WebP")
	}
	sha, _, err := s.cfg.Blobs.Put(bytes.NewReader(data), "")
	if err != nil {
		return nil, "", err
	}
	e, err := s.UpdateLog(ctx, UpdateInput{ID: id, PhotoBlobs: []string{sha}})
	if err != nil {
		return nil, "", err
	}
	return e, sha, nil
}

// OpenPhoto ouvre une photo de la fiche (refus si le blob n'est pas une photo de cette fiche).
func (s *Service) OpenPhoto(ctx context.Context, id, sha string) (*os.File, string, error) {
	e, err := s.findEntry(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if !hasStr(e.Photos, sha) || s.cfg.Blobs == nil {
		return nil, "", fmt.Errorf("photo %q de la fiche %s : %w", sha, id, store.ErrNotFound)
	}
	f, err := s.cfg.Blobs.Open(sha)
	if err != nil {
		return nil, "", err
	}
	head := make([]byte, 16)
	n, _ := io.ReadFull(f, head)
	ct := imageType(head[:n])
	if ct == "" {
		f.Close()
		return nil, "", fmt.Errorf("photo %q : %w", sha, store.ErrNotFound)
	}
	_, _ = f.Seek(0, io.SeekStart)
	return f, ct, nil
}

// sortedKeys est un petit utilitaire de tri.
func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
