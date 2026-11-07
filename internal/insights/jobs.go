package insights

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// jobDoc est la lecture, par champs JSON, d'un document print_jobs (écrit par internal/printing).
// Les champs inconnus sont ignorés : le paquet ne dépend pas des types de la chaîne d'impression.
type jobDoc struct {
	ID          string `json:"id"`
	State       string `json:"state"`
	PrinterID   string `json:"printer_id"`
	ModelID     string `json:"model_id"`
	Name        string `json:"name"`
	Plate       int    `json:"plate"`
	Assignments []struct {
		FilamentSlot int    `json:"filament_slot"`
		Tray         string `json:"tray"`
		MaterialID   string `json:"material_id"`
	} `json:"assignments"`
	Summary *struct {
		Plates        []slicer.PlateResult `json:"plates"`
		PrinterModel  string               `json:"printer_model"`
		MachinePreset string               `json:"machine_preset"`
		Process       string               `json:"process"`
		Filaments     []string             `json:"filament_presets"`
		Nozzles       []string             `json:"nozzles"`
		BedType       string               `json:"bed_type"`
	} `json:"summary"`
	Print *struct {
		Percent     int       `json:"percent"`
		Layer       int       `json:"layer"`
		TotalLayers int       `json:"total_layers"`
		ObservedAt  time.Time `json:"observed_at"`
	} `json:"print"`
	Events []struct {
		At    time.Time `json:"at"`
		Event string    `json:"event"`
		From  string    `json:"from"`
		To    string    `json:"to"`
		Note  string    `json:"note"`
	} `json:"events"`
	Slice *struct {
		Machine     string            `json:"machine"`
		Printer     string            `json:"printer_model"`
		Nozzle      float64           `json:"nozzle_mm"`
		Process     string            `json:"process"`
		Filaments   []string          `json:"filaments"`
		Overrides   []slicer.Override `json:"overrides"`
		Orientation *struct {
			Label string `json:"label"`
		} `json:"orientation"`
	} `json:"slice"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func isTerminal(state string) bool {
	return state == "COMPLETED" || state == "FAILED" || state == "CANCELLED"
}

func (s *Service) loadJob(ctx context.Context, id string) (*jobDoc, error) {
	var raw string
	err := s.cfg.Store.DB().QueryRowContext(ctx, `SELECT doc FROM print_jobs WHERE id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("impression %q : %w", id, store.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	var j jobDoc
	if err := json.Unmarshal([]byte(raw), &j); err != nil {
		return nil, fmt.Errorf("impression %q : document illisible : %w", id, err)
	}
	return &j, nil
}

func (s *Service) listJobs(ctx context.Context) ([]*jobDoc, error) {
	rows, err := s.cfg.Store.DB().QueryContext(ctx, `SELECT doc FROM print_jobs ORDER BY updated_at`)
	if err != nil {
		return nil, err
	}
	var raws []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			rows.Close()
			return nil, err
		}
		raws = append(raws, r)
	}
	rows.Close()
	var out []*jobDoc
	for _, r := range raws {
		var j jobDoc
		if json.Unmarshal([]byte(r), &j) == nil && j.ID != "" {
			out = append(out, &j)
		}
	}
	return out, nil
}

// plate renvoie la plaque de l'impression (celle demandée, sinon la première).
func (j *jobDoc) plate() *slicer.PlateResult {
	if j.Summary == nil || len(j.Summary.Plates) == 0 {
		return nil
	}
	for i := range j.Summary.Plates {
		if j.Plate > 0 && j.Summary.Plates[i].Index == j.Plate {
			return &j.Summary.Plates[i]
		}
	}
	return &j.Summary.Plates[0]
}

// observed renvoie le début (premier état PRINTING, sinon START_REQUESTED) et la fin (dernier état terminal).
func (j *jobDoc) observed() (start, end time.Time, ok bool) {
	for _, e := range j.Events {
		if e.To == "PRINTING" && start.IsZero() {
			start = e.At
		}
	}
	if start.IsZero() {
		for _, e := range j.Events {
			if e.To == "START_REQUESTED" {
				start = e.At
			}
		}
	}
	for _, e := range j.Events {
		if isTerminal(e.To) {
			end = e.At
		}
	}
	if end.IsZero() && isTerminal(j.State) {
		end = j.UpdatedAt
	}
	if start.IsZero() || end.IsZero() || !end.After(start) {
		return start, end, false
	}
	return start, end, true
}

// printerModelHint donne un libellé de modèle d'imprimante exploitable par modelKey.
func (s *Service) printerModelHint(ctx context.Context, j *jobDoc) string {
	var hint string
	if j.PrinterID != "" {
		var raw string
		if s.cfg.Store.DB().QueryRowContext(ctx, `SELECT doc FROM printers WHERE id=?`, j.PrinterID).Scan(&raw) == nil {
			var p struct {
				ModelID string `json:"model_id"`
				Name    string `json:"name"`
			}
			if json.Unmarshal([]byte(raw), &p) == nil {
				hint = p.ModelID
			}
		}
	}
	if modelKey(hint) != "" {
		return hint
	}
	if j.Summary != nil {
		if modelKey(j.Summary.PrinterModel) != "" {
			return j.Summary.PrinterModel
		}
		if modelKey(j.Summary.MachinePreset) != "" {
			return j.Summary.MachinePreset
		}
	}
	if j.Slice != nil {
		if modelKey(j.Slice.Printer) != "" {
			return j.Slice.Printer
		}
		if modelKey(j.Slice.Machine) != "" {
			return j.Slice.Machine
		}
	}
	return hint
}
