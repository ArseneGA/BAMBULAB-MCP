package printing

import (
	"math"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

// NaN et ±Inf ne sont jamais des réglages valides (ils donneraient « NaN% » ou « +Inf » dans le 3MF).
func TestNumRejectsNaNAndInf(t *testing.T) {
	for _, v := range []any{math.NaN(), math.Inf(1), math.Inf(-1), "NaN", "nan", "Inf", "+Inf", "-inf", "Infinity", "NaN%", "1e999"} {
		if f, ok := num(v); ok {
			t.Errorf("num(%v) = %v accepté", v, f)
		}
	}
	for _, v := range []any{0.0, 15, "15%", "0.12", 1e3} {
		if _, ok := num(v); !ok {
			t.Errorf("num(%v) refusé", v)
		}
	}
	for _, k := range []string{"sparse_infill_density", "wall_loops", "layer_height", "brim_width"} {
		for _, bad := range []any{math.NaN(), math.Inf(1), "NaN", "Inf"} {
			if _, err := normalizeSettings(map[string]any{k: bad}, ObjectSettingKeys); err == nil {
				t.Errorf("normalizeSettings %s=%v accepté", k, bad)
			}
		}
	}
}

// Après Close, plus aucun observateur de fin d'impression n'est lancé (pas de wg.Add après wg.Wait).
func TestJobEndHooksStopAfterClose(t *testing.T) {
	s, cleanup := newBareService(t)
	defer cleanup()
	var ran atomic.Int64
	s.AddJobEndHook(func(Job) { ran.Add(1) })
	s.fireJobEnd(Job{State: contracts.PrintCompleted})
	deadline := make(chan struct{})
	go func() {
		for ran.Load() == 0 {
		}
		close(deadline)
	}()
	<-deadline
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.fireJobEnd(Job{State: contracts.PrintFailed}) }()
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	before := ran.Load()
	s.fireJobEnd(Job{State: contracts.PrintCompleted})
	s.wg.Wait() // ne doit ni bloquer ni paniquer
	if ran.Load() != before {
		t.Fatal("un observateur a été lancé après Close")
	}
}
