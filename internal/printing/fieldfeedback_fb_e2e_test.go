package printing_test

import (
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer/sim"
)

// FB3 de bout en bout (outil get_print_status) : une erreur HMS vue pendant l'impression est une cause ACTIVE tant que
// l'imprimante la rapporte ; une fois acquittée (hms_count 0, print_error 0) elle passe dans causes_history, horodatée, et
// ne figure plus dans causes.
func TestStaleErrorCodesMoveToHistoryWhenPrinterReportsNone(t *testing.T) {
	r, job := recRig(t, "cle-depot-causes-perimees", 100*time.Millisecond)
	r.launch(job)
	waitFor(t, 10*time.Second, "impression en cours", func() bool {
		return str(r.call("get_printer_status", map[string]any{"printer_id": r.printerID}), "state") == "printing"
	})
	r.sim.PauseWithHMS([]sim.HMSEntry{hmsExt1}, printErrExt)
	st := r.waitAlert(job, "attend une action", 25*time.Second)
	causes, _ := st["causes"].([]any)
	if len(causes) < 1 {
		t.Fatalf("code actif attendu dans causes : %v", st)
	}
	for _, c := range causes {
		m := c.(map[string]any)
		if m["active"] != true || m["first_seen_at"] == nil || m["last_seen_at"] == nil {
			t.Fatalf("cause active horodatée attendue : %v", m)
		}
	}
	if h, _ := st["causes_history"].([]any); len(h) != 0 {
		t.Fatalf("rien en historique tant que l'erreur est active : %v", h)
	}

	r.sim.ClearErrors() // acquittée à l'écran : l'imprimante ne rapporte plus rien
	waitFor(t, 20*time.Second, "causes périmées déplacées en historique", func() bool {
		s := r.status(job)
		c, _ := s["causes"].([]any)
		h, _ := s["causes_history"].([]any)
		return len(c) == 0 && len(h) >= 1
	})
	s := r.status(job)
	ps, _ := s["printer"].(map[string]any)
	if ps["hms_count"] != float64(0) || ps["print_error"] != float64(0) {
		t.Fatalf("l'imprimante ne doit plus rapporter d'erreur : %v", ps)
	}
	for _, h := range s["causes_history"].([]any) {
		m := h.(map[string]any)
		if m["active"] == true || m["first_seen_at"] == nil || m["last_seen_at"] == nil || m["code"] == "" {
			t.Fatalf("code périmé horodaté et inactif attendu : %v", m)
		}
	}
}
