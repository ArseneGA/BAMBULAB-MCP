package printing

import (
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

// Point 11 : correspondance de fichier exacte (jamais une sous-chaîne).
func TestMatchesIsExactNotSubstring(t *testing.T) {
	remote := "benchy-156fa5bbe39e.gcode.3mf"
	if !matches(printer.Status{SubtaskName: "benchy-156fa5bbe39e"}, remote) || !matches(printer.Status{GcodeFile: "/sdcard/benchy-156fa5bbe39e.gcode.3mf"}, remote) {
		t.Fatal("le fichier de l'impression doit correspondre")
	}
	for _, st := range []printer.Status{{SubtaskName: "benchy-156fa5bbe39e_v2"}, {GcodeFile: "Metadata/mon-benchy-156fa5bbe39e.gcode"}, {SubtaskName: "benchy"}} {
		if matches(st, remote) {
			t.Fatalf("faux positif : %+v", st)
		}
	}
}

// Point 11 : la réconciliation transmet l'instant de START_REQUESTED à l'adaptateur.
func TestStartRequestedAtUsesLastEvent(t *testing.T) {
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	j := Job{Events: []JobEvent{{At: t0.Add(-time.Hour), To: contracts.PrintAwaitingConfirmation}, {At: t0, To: contracts.PrintStartRequested}, {At: t0.Add(time.Minute), To: contracts.PrintStartUnknown}}}
	if got := startRequestedAt(j); !got.Equal(t0) {
		t.Fatalf("startRequestedAt = %v", got)
	}
	if !startRequestedAt(Job{}).IsZero() {
		t.Fatal("sans événement : zéro")
	}
}
