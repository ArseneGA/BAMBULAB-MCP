package goals

import (
	"errors"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

func TestListExperimentsMemoryForAndFilamentPresets(t *testing.T) {
	st := installProfiles(t)
	e := newEnv(t, st, nil, false)
	mdl, _ := e.addModel("support", mesh.TShape(30, 40, 60, 8, 20))

	if l, err := e.svc.ListExperiments(e.ctx, mdl, 10); err != nil || l == nil || len(l) != 0 {
		t.Fatalf("liste vide attendue : %v %v", l, err)
	}
	x, _ := experimentNoSlicing(t, e, mdl, strengthSpeed("xy", false))
	l, err := e.svc.ListExperiments(e.ctx, mdl, 10)
	if err != nil || len(l) != 1 || l[0].ID != x.ID || l[0].Candidates != len(x.Candidates) || l[0].MaterialFamily != "PLA" {
		t.Fatalf("liste : %v %+v", err, l)
	}
	if l[0].State != "running" {
		t.Fatalf("des découpes en attente : running attendu, %q", l[0].State)
	}
	for _, c := range x.Candidates {
		e.svc.setCandidate(e.ctx, c.ID, "succeeded", "", &CandMetrics{Values: map[string]Metric{}, Effective: map[string]any{}}, "")
	}
	if l, _ = e.svc.ListExperiments(e.ctx, mdl, 10); l[0].State != "complete" || l[0].Succeeded != len(x.Candidates) {
		t.Fatalf("complete attendu : %+v", l[0])
	}
	e.svc.setCandidate(e.ctx, x.Candidates[1].ID, "failed", "boom", nil, "")
	if l, _ = e.svc.ListExperiments(e.ctx, mdl, 10); l[0].State != "partial" || l[0].Failed != 1 {
		t.Fatalf("partial attendu : %+v", l[0])
	}
	if other, _ := e.svc.ListExperiments(e.ctx, "mdl_autre", 10); len(other) != 0 {
		t.Fatalf("filtre par modèle : %+v", other)
	}

	// Mémoire depuis l'expérience : mêmes conditions que l'outil MCP, aucun essai enregistré.
	m, err := e.svc.MemoryFor(e.ctx, MemoryRequest{ExperimentID: x.ID}, 20)
	if err != nil || m.Query.MaterialFamily != "PLA" || m.Query.Machine != p1sMachine || m.Query.Filament != p1sFilament || m.Hits == nil || len(m.Hits) != 0 {
		t.Fatalf("mémoire : %v %+v", err, m)
	}
	cand := x.Candidates[0]
	e.svc.setCandidate(e.ctx, cand.ID, "succeeded", "", &CandMetrics{Values: map[string]Metric{}, Effective: map[string]any{}}, "")
	if _, err := e.svc.RecordResult(e.ctx, ResultInput{ExperimentID: x.ID, CandidateID: cand.ID, Outcome: "success", Observations: "propre"}); err != nil {
		t.Fatal(err)
	}
	if m, _ = e.svc.MemoryFor(e.ctx, MemoryRequest{ExperimentID: x.ID}, 20); len(m.Hits) != 1 || m.Hits[0].Strength != 3 {
		t.Fatalf("l'essai enregistré doit être retrouvé : %+v", m.Hits)
	}
	if _, err := e.svc.MemoryFor(e.ctx, MemoryRequest{}, 5); !errors.Is(err, ErrInvalid) {
		t.Fatalf("sans conditions : %v", err)
	}
	m, err = e.svc.MemoryFor(e.ctx, MemoryRequest{MaterialFamily: "PLA", Machine: p1sMachine, SizeMM: []float64{10, 30, 20}}, 5)
	if err != nil || m.Query.DimsSortedMM != [3]float64{30, 20, 10} {
		t.Fatalf("dimensions triées : %v %+v", err, m)
	}

	// Presets filament compatibles avec la machine (profils réels de l'installation).
	fil, err := e.svc.FilamentPresets(p1sMachine)
	found := false
	for _, f := range fil {
		found = found || f == p1sFilament
	}
	if err != nil || !found {
		t.Fatalf("filaments : %v (%d)", err, len(fil))
	}
	if _, err := e.svc.FilamentPresets("Machine imaginaire"); err == nil || !strings.Contains(err.Error(), "inconnu") {
		t.Fatalf("machine inconnue : %v", err)
	}
}
