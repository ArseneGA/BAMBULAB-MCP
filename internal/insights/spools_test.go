package insights

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/filament"
)

type fakeSpools struct{}

func (fakeSpools) PriceForSlot(_ context.Context, printer, slot string) (filament.SlotPrice, bool) {
	if printer == "pr1" && slot == "ams1:1" {
		return filament.SlotPrice{SpoolID: "sp1", Label: "PLA noir", PricePerKg: 40}, true
	}
	return filament.SlotPrice{}, false
}

func (fakeSpools) UsageOf(_ context.Context, job string) ([]filament.Usage, error) {
	return []filament.Usage{{PrintJobID: job, FilamentSlot: 1, Grams: 120}}, nil
}

func TestSpoolPriceAndUsageAndCostFunc(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.SetSpools(fakeSpools{})
	st := t0
	e.seedJob(jobSpec{ID: "j1", State: "COMPLETED", Printer: "pr1", Seconds: 3600, Grams: 100, Start: st, End: st.Add(time.Hour), Percent: 100})
	o, err := e.svc.EstimateCost(ctx, EstimateInput{PrintJobID: "j1"})
	if err != nil {
		t.Fatal(err)
	}
	f := o.Estimate.Filament[0]
	if f.PricePerKg != 40 || !strings.Contains(f.PriceSource, "bobine de l'inventaire") || f.Amount != 4 {
		t.Errorf("prix de la bobine : %+v", f)
	}
	if o.Actual == nil || o.Actual.Filament[0].Grams != 120 || o.Actual.Filament[0].Amount != 4.8 {
		t.Errorf("réel : %+v", o.Actual)
	}
	c, ok := e.svc.MaterialCost(ctx, "PETG", 100)
	if !ok || c != 2.2 {
		t.Errorf("MaterialCost : %v %v", c, ok)
	}
}
