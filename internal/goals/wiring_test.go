package goals

import (
	"errors"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

func TestEngineeringRefusedForTPUShownOnCandidate(t *testing.T) {
	goal, err := BuildGoal(mustRules(t), strengthSpeed("xy", true))
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{}
	crit := goal.Criteria[0]
	// le matériau de la poutre est surchargé par la famille TPU : calcul refusé, erreur portée par le candidat
	crit.Params["engineering"].(map[string]any)["material"] = "TPU"
	e := s.engineeringFor(crit, CandidateView{Candidate: Candidate{ID: "c"}}, goal, "TPU")
	if e.Error == "" || e.SafetyFactor != 0 || e.Label != LabelEngineering {
		t.Fatalf("calcul refusé attendu pour le TPU : %+v", e)
	}
	cc := CompareCandidate{ID: "c", State: "succeeded", Engineering: []EngEntry{e}}
	if _, ok := metricValue(cc, "safety_factor"); ok {
		t.Fatal("pas de valeur de facteur de sécurité pour un calcul refusé")
	}
}

func TestSlicerMissingIsExplicit(t *testing.T) {
	e := newEnv(t, nil, nil, true)
	e.svc.cfg.SlicerFactory = func() (*slicer.Slicer, error) { return nil, errors.New("Bambu Studio introuvable") }
	mdl, _ := e.addModel("pièce", mesh.Cube(20))
	g, err := e.svc.SetGoals(e.ctx, GoalInput{OriginalText: "rapide", Criteria: []CriterionInput{{Type: "speed"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.CreateExperiment(e.ctx, CreateExperimentInput{ModelID: mdl, GoalID: g.ID, Base: BaseSpec{Machine: p1sMachine}})
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "slicer_missing") {
		t.Fatalf("slicer_missing attendu : %v", err)
	}
}
