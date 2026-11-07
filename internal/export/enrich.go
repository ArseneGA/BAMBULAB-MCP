package export

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/insights"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// ---- source paramétrique ----

// templateDoc est le JSON de gabarit d'une pièce générée par la bibliothèque (create_from_template).
type templateDoc struct {
	Format          string          `json:"format"`
	Template        string          `json:"template"`
	TemplateVersion string          `json:"template_version"`
	Variant         string          `json:"variant,omitempty"`
	Parameters      json.RawMessage `json:"parameters" jsonschema:"paramètres résolus (après calculs et calibration)"`
	Requested       json.RawMessage `json:"requested,omitempty"`
	Calculations    json.RawMessage `json:"calculations,omitempty"`
	Calibration     json.RawMessage `json:"calibration,omitempty"`
	GeneratedAt     time.Time       `json:"generated_at"`
	Regeneration    string          `json:"regeneration"`
}

func (s *Service) loadTemplate(ctx context.Context, pt *part) (*templateDoc, error) {
	q := `SELECT template,COALESCE(variant_id,''),template_version,params,requested,calcs,COALESCE(calibration,''),created_at FROM part_template_models WHERE `
	var row *sql.Row
	if pt.Version != nil {
		row = s.db().QueryRowContext(ctx, q+`version_id=?`, pt.Version.ID)
	} else {
		row = s.db().QueryRowContext(ctx, q+`model_id=? ORDER BY created_at DESC LIMIT 1`, pt.Model.ID)
	}
	var d templateDoc
	var params, req, calcs, cal, at string
	if err := row.Scan(&d.Template, &d.Variant, &d.TemplateVersion, &params, &req, &calcs, &cal, &at); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	d.Format = "atelier-bambu-parametric/1"
	d.Parameters = redact(params)
	d.Requested = redact(req)
	d.Calculations = redact(calcs)
	d.Calibration = redact(cal)
	d.GeneratedAt, _ = store.ParseTime(at)
	d.Regeneration = "Dans Atelier Bambu : create_from_template avec ce gabarit (template), cette version et ces paramètres (parameters)."
	return &d, nil
}

// redact retire les clés secrètes d'un JSON et renvoie nil si ce n'est pas du JSON valide ou s'il est vide.
func redact(s string) json.RawMessage {
	s = strings.TrimSpace(s)
	if s == "" || !json.Valid([]byte(s)) {
		return nil
	}
	return contracts.RedactJSON(json.RawMessage(s))
}

// ---- profil de découpe ----

// profileRef : préréglages OFFICIELS de Bambu Studio auxquels les surcharges s'appliquent.
type profileRef struct {
	Machine   string   `json:"machine,omitempty"`
	Process   string   `json:"process,omitempty"`
	Filaments []string `json:"filaments,omitempty"`
	NozzleMM  float64  `json:"nozzle_mm,omitempty"`
}

type orientDoc struct {
	Label    string     `json:"label,omitempty"`
	Up       [3]float64 `json:"up"`
	HeightMM float64    `json:"height_mm,omitempty"`
}

// profileDoc est le profil de découpe effectif d'une pièce : uniquement les clés surchargées et la référence au
// préréglage officiel. Jamais de G-code (clés *gcode* retirées).
type profileDoc struct {
	Format       string            `json:"format"`
	Part         string            `json:"part"`
	Source       string            `json:"source" jsonschema:"variante appliquée | impression découpée"`
	Revision     string            `json:"revision,omitempty"`
	Lineage      []string          `json:"lineage,omitempty"`
	Reference    profileRef        `json:"reference"`
	Overrides    []slicer.Override `json:"overrides"`
	Orientation  *orientDoc        `json:"orientation,omitempty"`
	Note         string            `json:"note,omitempty"`
	Dropped      int               `json:"dropped_gcode_keys,omitempty"`
	Instructions string            `json:"instructions"`
}

const profileHowTo = "Dans Bambu Studio, choisir les préréglages officiels de « reference » (machine, process, filaments) puis modifier uniquement les paramètres listés dans « overrides » (cible : machine, process ou filament). Aucune autre valeur n'est exportée."

// cleanOverrides retire toute surcharge de G-code (jamais exporté) et renvoie le nombre retiré.
func cleanOverrides(in []slicer.Override) (out []slicer.Override, dropped int) {
	out = []slicer.Override{}
	for _, o := range in {
		if strings.Contains(strings.ToLower(o.Key), "gcode") {
			dropped++
			continue
		}
		out = append(out, o)
	}
	return out, dropped
}

func (s *Service) loadProfile(ctx context.Context, pt *part) (*profileDoc, error) {
	if s.d.Goals == nil {
		return nil, nil
	}
	rev, err := s.d.Goals.AppliedFor(ctx, pt.Model.ID)
	if err != nil {
		return nil, err
	}
	d := &profileDoc{Format: "atelier-bambu-profile/1", Part: pt.Model.Name, Instructions: profileHowTo}
	switch {
	case rev != nil:
		d.Source, d.Revision = "variante appliquée", rev.ID
		d.Reference = profileRef{Machine: rev.Machine, Process: rev.Process, Filaments: rev.Filaments}
		d.Overrides, d.Dropped = cleanOverrides(rev.Overrides)
		d.Note = rev.Note
		if rev.Orientation != nil {
			d.Orientation = &orientDoc{Label: rev.Orientation.Label, Up: rev.Orientation.Up, HeightMM: rev.Orientation.HeightMM}
		}
		cur := rev
		for i := 0; i < 20 && cur.ParentID != ""; i++ {
			d.Lineage = append(d.Lineage, cur.ParentID)
			if cur, err = s.d.Goals.GetProfileRevision(ctx, cur.ParentID); err != nil {
				break
			}
		}
	case pt.Print != nil && pt.Print.Slice != nil:
		sl := pt.Print.Slice
		d.Source, d.Revision = "impression découpée", pt.Print.ProfileRevision
		d.Reference = profileRef{Machine: sl.Machine, Process: sl.Process, Filaments: sl.Filaments, NozzleMM: sl.Nozzle}
		d.Overrides, d.Dropped = cleanOverrides(sl.Overrides)
		if sl.Orientation != nil {
			d.Orientation = &orientDoc{Label: sl.Orientation.Label, Up: sl.Orientation.Up, HeightMM: sl.Orientation.HeightMM}
		}
	default:
		return nil, nil
	}
	if d.Reference.NozzleMM == 0 && pt.Print != nil && pt.Print.Slice != nil {
		d.Reference.NozzleMM = pt.Print.Slice.Nozzle
	}
	return d, nil
}

// ---- impression découpée (G-code .3mf validé, estimations) ----

type orientInfo struct {
	Label    string     `json:"label"`
	Up       [3]float64 `json:"up"`
	HeightMM float64    `json:"height_mm"`
}

// sliceInfo : paramètres figés d'une découpe (champ slice d'une impression).
type sliceInfo struct {
	Machine     string            `json:"machine"`
	Nozzle      float64           `json:"nozzle_mm"`
	Process     string            `json:"process"`
	Filaments   []string          `json:"filaments"`
	Overrides   []slicer.Override `json:"overrides"`
	Orientation *orientInfo       `json:"orientation"`
}

// printInfo : ce que l'on lit d'une impression (table print_jobs) ; rien d'imprimante (ni identifiant, ni adresse).
type printInfo struct {
	JobID           string
	State           string
	VersionID       string
	ProfileRevision string
	Name            string
	ArtifactSHA     string
	ArtifactSize    int64
	Plate           int
	Validated       bool
	Seconds         float64
	Grams           float64
	Filaments       []slicer.FilamentUse
	Layers          int
	Slice           *sliceInfo
}

type jobRow struct {
	ID              string `json:"id"`
	State           string `json:"state"`
	ModelID         string `json:"model_id"`
	VersionID       string `json:"version_id"`
	ProfileRevision string `json:"profile_revision"`
	Name            string `json:"name"`
	ArtifactSHA256  string `json:"artifact_sha256"`
	ArtifactSize    int64  `json:"artifact_size"`
	Plate           int    `json:"plate"`
	Summary         *struct {
		Plates []slicer.PlateResult `json:"plates"`
	} `json:"summary"`
	Validation *struct {
		OK bool `json:"ok"`
	} `json:"validation"`
	Slice *sliceInfo `json:"slice"`
}

// loadPrint choisit l'impression découpée la plus récente de la pièce : de préférence VALIDÉE, pour la version
// exportée, dont l'artefact est encore dans le stockage.
func (s *Service) loadPrint(ctx context.Context, pt *part) (*printInfo, error) {
	rows, err := s.db().QueryContext(ctx,
		`SELECT doc FROM print_jobs WHERE json_extract(doc,'$.model_id')=? AND COALESCE(json_extract(doc,'$.artifact_sha256'),'')<>'' ORDER BY updated_at DESC LIMIT 30`, pt.Model.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var best *printInfo
	for rows.Next() {
		var doc string
		if err := rows.Scan(&doc); err != nil {
			return nil, err
		}
		var j jobRow
		if json.Unmarshal([]byte(doc), &j) != nil || !s.d.Blobs.Has(j.ArtifactSHA256) {
			continue
		}
		if pt.Version != nil && j.VersionID != "" && j.VersionID != pt.Version.ID {
			continue // découpée pour une autre version de la pièce
		}
		pi := &printInfo{JobID: j.ID, State: j.State, VersionID: j.VersionID, ProfileRevision: j.ProfileRevision, Name: j.Name,
			ArtifactSHA: j.ArtifactSHA256, ArtifactSize: j.ArtifactSize, Plate: j.Plate, Validated: j.Validation != nil && j.Validation.OK}
		pi.Slice = j.Slice
		if j.Summary != nil {
			idx := j.Plate
			for _, p := range j.Summary.Plates {
				if idx == 0 || p.Index == idx {
					pi.Seconds, pi.Grams, pi.Filaments, pi.Layers = p.PredictedSeconds, p.WeightGrams, p.Filaments, p.Layers
					break
				}
			}
		}
		if pi.Validated {
			return pi, nil
		}
		if best == nil {
			best = pi
		}
	}
	return best, rows.Err()
}

// ---- photos du journal ----

type photoItem struct {
	LogID string
	SHA   string
	Path  string
	Size  int64
}

func (s *Service) loadPhotos(ctx context.Context, pt *part) ([]photoItem, error) {
	if s.d.Insights == nil {
		return nil, nil
	}
	out, err := s.d.Insights.ListLogs(ctx, insights.ListQuery{ModelID: pt.Model.ID, Outcome: "success", Limit: 50})
	if err != nil {
		return nil, err
	}
	var items []photoItem
	seen := map[string]bool{}
	add := func(logID, sha string) {
		if len(items) >= MaxPhotosPerPart || seen[sha] || !s.d.Blobs.Has(sha) {
			return
		}
		sz, err := s.d.Blobs.Size(sha)
		if err != nil {
			return
		}
		f, ct, err := s.d.Insights.OpenPhoto(ctx, logID, sha)
		if err != nil {
			return
		}
		f.Close()
		seen[sha] = true
		ext := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp"}[ct]
		if ext == "" {
			return
		}
		items = append(items, photoItem{LogID: logID, SHA: sha, Size: sz, Path: fmt.Sprintf("images/%s/photo-%02d%s", pt.Slug, len(items)+1, ext)})
	}
	for _, e := range out.Entries {
		for _, sha := range e.Photos {
			add(e.ID, sha)
		}
		for _, cp := range e.CameraPhotos {
			if cp.Kind == "final" { // jamais les photos d'alerte (échecs)
				add(e.ID, cp.SHA256)
			}
		}
	}
	return items, nil
}
