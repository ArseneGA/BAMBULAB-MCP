package mcpserver

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
)

func TestFilamentListTextListsPresets(t *testing.T) {
	l := &printing.FilamentList{Total: 30, Families: []printing.FilamentFamily{{Family: "PETG"}}}
	for i := 0; i < 30; i++ {
		l.Families[0].Filaments = append(l.Families[0].Filaments, profiles.FilamentEntry{Name: fmt.Sprintf("Bambu PETG %d @BBL X1C", i), Line: fmt.Sprintf("Bambu PETG %d", i)})
	}
	txt := filamentListText(l, "pr_1")
	if !strings.Contains(txt, "Bambu PETG 0 @BBL X1C") || strings.Count(txt, "\n- ") != maxSearchTextRows || !strings.Contains(txt, "10 autre(s)") {
		t.Fatalf("texte de list_filaments : %s", txt)
	}
}

func TestHistoryTextListsJobs(t *testing.T) {
	h := &printing.History{View: "all", Counts: map[string]int{"finished": 2}}
	h.Jobs = []printing.Job{{ID: "pj_1", Name: "porte-guitare.gcode.3mf", State: "FAILED", PrinterID: "pr_1", UpdatedAt: time.Now()}, {ID: "pj_2", Name: "cube", State: "SUCCEEDED", PrinterID: "pr_1", UpdatedAt: time.Now()}}
	txt := historyText(h)
	if !strings.Contains(txt, "pj_1") || !strings.Contains(txt, "porte-guitare") || !strings.Contains(txt, "pj_2") {
		t.Fatalf("texte de list_print_history : %s", txt)
	}
}
