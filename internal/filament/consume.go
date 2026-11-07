package filament

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// Usage est la consommation d'une impression sur une bobine.
type Usage struct {
	PrintJobID   string    `json:"print_job_id"`
	SpoolID      string    `json:"spool_id"`
	FilamentSlot int       `json:"filament_slot"`
	Grams        float64   `json:"grams"`
	Estimated    bool      `json:"estimated" jsonschema:"vrai si l'impression a été interrompue (masse au prorata de l'avancement)"`
	PricePerKg   *float64  `json:"price_per_kg,omitempty" jsonschema:"prix/kg de la bobine au moment du décompte"`
	Cost         *float64  `json:"cost,omitempty" jsonschema:"coût de la matière consommée (grams × prix/kg)"`
	Note         string    `json:"note,omitempty"`
	RecordedAt   time.Time `json:"recorded_at"`
}

// plateUse renvoie la plaque d'une impression (masse par filament, purge comprise : « used_g » de Studio).
func plateUse(j printing.Job) *slicer.PlateResult {
	if j.Summary == nil {
		return nil
	}
	for i := range j.Summary.Plates {
		if j.Summary.Plates[i].Index == j.Plate {
			return &j.Summary.Plates[i]
		}
	}
	if j.Plate == 0 && len(j.Summary.Plates) == 1 {
		return &j.Summary.Plates[0]
	}
	return nil
}

func gramsFor(p *slicer.PlateResult, slot int) (float64, bool) {
	if p == nil {
		return 0, false
	}
	for _, f := range p.Filaments {
		if f.Slot == slot {
			return f.Grams, true
		}
	}
	return 0, false
}

// onJobEnd décompte la consommation à la fin d'une impression puis prépare la suite de la file.
// COMPLETED : masse entière du fichier. FAILED / CANCELLED après un début d'impression : au prorata de
// l'avancement (estimation signalée). Idempotent par (impression, bobine).
func (s *Service) onJobEnd(j printing.Job) {
	ctx := context.Background()
	if err := s.RecordConsumption(ctx, j); err != nil {
		s.log.Warn("décompte de consommation", "print_job", j.ID, "err", err)
	}
	if err := s.PrepareQueue(ctx); err != nil {
		s.log.Warn("préparation de la file après impression", "print_job", j.ID, "err", err)
	}
}

// RecordConsumption retire de chaque bobine chargée la masse utilisée par l'impression j.
func (s *Service) RecordConsumption(ctx context.Context, j printing.Job) error {
	factor, estimated := 1.0, false
	switch j.State {
	case contracts.PrintCompleted:
	case contracts.PrintFailed, contracts.PrintCancelled:
		if j.Print == nil || !j.Print.SeenActive || j.Print.Percent <= 0 {
			return nil // rien n'a été imprimé de façon attribuable
		}
		factor, estimated = float64(j.Print.Percent)/100, true
	default:
		return nil
	}
	p := plateUse(j)
	if p == nil || len(j.Assignments) == 0 {
		return nil
	}
	for _, a := range j.Assignments {
		g, ok := gramsFor(p, a.FilamentSlot)
		if !ok || g <= 0 {
			continue
		}
		g *= factor
		sp, err := s.SpoolAt(ctx, j.PrinterID, a.Tray)
		if err != nil {
			return err
		}
		if sp == nil {
			s.log.Info("consommation non décomptée : aucune bobine inscrite", "print_job", j.ID, "slot", a.Tray, "grams", g)
			continue
		}
		if err := s.consume(ctx, j.ID, a.FilamentSlot, sp.ID, g, estimated); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) consume(ctx context.Context, jobID string, filSlot int, spoolID string, grams float64, estimated bool) error {
	var became *Spool
	err := s.d.Store.Tx(ctx, func(tx *sql.Tx) error {
		sp, rev, err := s.loadSpool(ctx, tx, spoolID)
		if err != nil {
			return err
		}
		var price, cost any
		if sp.PricePerKg != nil {
			price, cost = *sp.PricePerKg, grams/1000**sp.PricePerKg
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO spool_usage(print_job_id,spool_id,filament_slot,grams,estimated,price_per_kg,cost,note,recorded_at)
			VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, jobID, spoolID, filSlot, grams, b2i(estimated), price, cost, noteFor(estimated), formatTime(s.now()))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return nil // déjà décompté
		}
		before := sp.Status
		sp.RemainingG = max(0, sp.RemainingG-grams)
		if sp.RemainingSource == "default" || sp.RemainingSource == "ams" || sp.RemainingSource == "" {
			sp.RemainingSource = "consumption"
		}
		sp.UpdatedAt = s.now()
		sp.compute()
		r, err := tx.ExecContext(ctx, `UPDATE spools SET doc=?, updated_at=?, rev=rev+1 WHERE id=? AND rev=?`, mustJSON(sp), formatTime(sp.UpdatedAt), spoolID, rev)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n != 1 {
			return fmt.Errorf("%w : bobine %s modifiée en parallèle", ErrConflict, spoolID)
		}
		if before == "ok" && (sp.Status == "low" || sp.Status == "empty") {
			became = sp
		}
		return s.d.Store.AuditTx(ctx, tx, actor, "spool.consume", "spool", spoolID, map[string]any{"print_job": jobID, "grams": grams, "estimated": estimated})
	})
	if err != nil {
		return err
	}
	if became != nil {
		s.publish(Event{Kind: "spool_low", SpoolID: became.ID, PrintJobID: jobID, PrinterID: became.PrinterID,
			Message: fmt.Sprintf("Bobine %s : il reste %s (%s).", became.Label(), fmtG(became.RemainingG), became.Status)})
	}
	return nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func noteFor(estimated bool) string {
	if estimated {
		return "impression interrompue : masse au prorata de l'avancement"
	}
	return ""
}

// UsageOf liste la consommation enregistrée pour une impression.
func (s *Service) UsageOf(ctx context.Context, printJobID string) ([]Usage, error) {
	rows, err := s.d.Store.DB().QueryContext(ctx, `SELECT print_job_id,spool_id,filament_slot,grams,estimated,price_per_kg,cost,note,recorded_at FROM spool_usage WHERE print_job_id=? ORDER BY filament_slot, spool_id`, printJobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Usage{}
	for rows.Next() {
		var u Usage
		var est int
		var price, cost sql.NullFloat64
		var at string
		if err := rows.Scan(&u.PrintJobID, &u.SpoolID, &u.FilamentSlot, &u.Grams, &est, &price, &cost, &u.Note, &at); err != nil {
			return nil, err
		}
		u.Estimated = est == 1
		if price.Valid {
			u.PricePerKg = &price.Float64
		}
		if cost.Valid {
			u.Cost = &cost.Float64
		}
		u.RecordedAt, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, u)
	}
	return out, rows.Err()
}

// SlotPrice est le prix au kilo de la bobine chargée à un emplacement : point de lecture du calcul de coût (chantier C).
type SlotPrice struct {
	SpoolID    string  `json:"spool_id"`
	Label      string  `json:"label"`
	Material   string  `json:"material"`
	ColorHex   string  `json:"color_hex,omitempty"`
	PricePerKg float64 `json:"price_per_kg"`
}

// PriceForSlot renvoie le prix/kg de la bobine chargée dans (imprimante, emplacement) ; ok=false si aucune bobine
// n'y est inscrite ou si son prix est inconnu.
func (s *Service) PriceForSlot(ctx context.Context, printerID, slot string) (SlotPrice, bool) {
	sp, err := s.SpoolAt(ctx, printerID, slot)
	if err != nil || sp == nil {
		return SlotPrice{}, false
	}
	return priceOf(sp)
}

// PriceForSpool renvoie le prix/kg d'une bobine.
func (s *Service) PriceForSpool(ctx context.Context, spoolID string) (SlotPrice, bool) {
	sp, err := s.GetSpool(ctx, spoolID)
	if err != nil {
		return SlotPrice{}, false
	}
	return priceOf(sp)
}

func priceOf(sp *Spool) (SlotPrice, bool) {
	if sp.PricePerKg == nil {
		return SlotPrice{}, false
	}
	return SlotPrice{SpoolID: sp.ID, Label: sp.Label(), Material: sp.Material, ColorHex: sp.ColorHex, PricePerKg: *sp.PricePerKg}, true
}
