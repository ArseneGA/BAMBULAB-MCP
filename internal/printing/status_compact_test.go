package printing

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

func TestAsCompactDropsHeavyFieldsKeepsSafety(t *testing.T) {
	var checks []slicer.Check
	for i := 0; i < 60; i++ {
		checks = append(checks, slicer.Check{Name: fmt.Sprintf("controle_%d", i), OK: true, Level: "error", Detail: "détail d'un contrôle de validation assez long pour peser"})
	}
	var ev []JobEvent
	for i := 0; i < 12; i++ {
		ev = append(ev, JobEvent{Event: fmt.Sprintf("e%d", i)})
	}
	st := &PrintStatus{Job: Job{ID: "pj_1", State: "PRINTING", PrinterID: "p1", ArtifactSHA256: "abc123", ConfirmationID: "cf_1", Plate: 1, NoFlowCali: true,
		Validation: &ValidationReport{OK: true, At: time.Now(), Archive: checks, Compat: checks, Warnings: []string{"filament_marginal_1"}},
		Summary:    &ArtifactSummary{Plates: []slicer.PlateResult{{Index: 1}}}, Slice: &SliceSpec{Process: "x"}, Events: ev,
		Print: &PrintProgress{Percent: 40, Layer: 10, TotalLayers: 25, Attention: "attend une action", Stages: []StageSeen{{}}, ErrorLog: []Cause{{}}}},
		Alert: "bloquée : test", Causes: []Cause{{}}}
	full, _ := json.Marshal(st)
	c := st.AsCompact()
	cb, _ := json.Marshal(c)
	if len(cb)*3 > len(full) {
		t.Fatalf("compact %d o contre complet %d o", len(cb), len(full))
	}
	// L'original n'est pas modifié (la carte et l'API web gardent le suivi complet).
	if st.Job.Validation == nil || st.Job.Summary == nil || len(st.Job.Events) != 12 || len(st.Job.Print.Stages) != 1 || st.Compact {
		t.Fatalf("l'original a été modifié")
	}
	j := c.Job
	if !c.Compact || j.Validation != nil || j.Summary != nil || j.Slice != nil || len(j.Events) != 12 { // le journal d'événements (arrêts, lancements) reste entier
		t.Fatalf("allègement : %+v", j)
	}
	if c.ValidationBrief == nil || !c.ValidationBrief.OK || c.ValidationBrief.Checks != 120 || len(c.ValidationBrief.Warnings) != 1 {
		t.Fatalf("verdict en bref : %+v", c.ValidationBrief)
	}
	// Sécurité, progression, alertes conservées.
	if j.ID != "pj_1" || j.State != "PRINTING" || j.ArtifactSHA256 != "abc123" || j.ConfirmationID != "cf_1" || !j.NoFlowCali || j.Plate != 1 ||
		j.Print.Percent != 40 || j.Print.Attention == "" || c.Alert == "" || len(c.Causes) != 1 {
		t.Fatalf("champs essentiels perdus : %+v", c)
	}
}
