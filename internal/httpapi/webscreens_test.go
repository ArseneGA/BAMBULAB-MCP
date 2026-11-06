package httpapi_test

import "testing"

// Routes utilisées par les écrans Impression (annulation, réconciliation) et Windows : formes et refus sans état préalable.
func TestPrintAndAgentRoutesUsedByTheWebApp(t *testing.T) {
	e := setup(t)
	var er map[string]any
	if code := e.do("POST", "/api/print-jobs/pj_inconnu/reconcile", nil, &er); code != 404 || er["code"] != "not_found" {
		t.Fatalf("reconcile inconnu : %d %v", code, er)
	}
	if code := e.do("POST", "/api/print-jobs/pj_inconnu/cancel", nil, &er); code != 404 {
		t.Fatalf("cancel inconnu : %d %v", code, er)
	}
	var ag struct {
		Agents []any `json:"agents"`
	}
	if code := e.do("GET", "/api/agents", nil, &ag); code != 200 || ag.Agents == nil || len(ag.Agents) != 0 {
		t.Fatalf("agents : %d %+v", code, ag)
	}
	var tk struct {
		Tasks []any `json:"tasks"`
	}
	if code := e.do("GET", "/api/agent/tasks?limit=20", nil, &tk); code != 200 || tk.Tasks == nil {
		t.Fatalf("tâches : %d %+v", code, tk)
	}
	if code := e.do("GET", "/api/agents/ag_inconnu/inventory", nil, &er); code >= 500 {
		t.Fatalf("inventaire d'un agent inconnu : %d %v", code, er)
	}
	if code := e.do("POST", "/api/agents/ag_inconnu/inventory/select", map[string]any{"root_label": "dossiers"}, &er); code < 400 || code >= 500 {
		t.Fatalf("sélection pour un agent inconnu : %d %v", code, er)
	}
}
