package mcpserver_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
)

// De bout en bout par MCP : instruction du serveur, signalement, dédoublonnage, filtre des secrets, liste, changement d'état,
// export Markdown, lien de carte ; aucun outil du carnet ne touche aux impressions.
func TestIssueToolsFlow(t *testing.T) {
	ctx := context.Background()
	c := startCore(t)
	cs := connectClient(t, mcpserver.New(c))

	ins := cs.InitializeResult().Instructions
	for _, want := range []string{"report_issue", "SPONTANÉMENT", "outil qui refuse à tort", "fausse alerte", "donnée périmée", "réponse trop lourde"} {
		if !strings.Contains(ins, want) {
			t.Errorf("instructions du serveur sans %q", want)
		}
	}

	lt, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]bool{}
	for _, tl := range lt.Tools {
		byName[tl.Name] = true
		if tl.Name == "list_issues" || tl.Name == "export_issues" {
			if tl.Annotations == nil || !tl.Annotations.ReadOnlyHint {
				t.Errorf("%s doit être en lecture seule", tl.Name)
			}
		}
		if tl.Name == "list_issues" {
			if ui := metaUI(t, tl.Meta); ui["resourceUri"] != mcpserver.IssuesViewURI {
				t.Errorf("list_issues sans carte : %v", tl.Meta)
			}
		}
	}
	for _, n := range []string{"report_issue", "list_issues", "update_issue", "export_issues"} {
		if !byName[n] {
			t.Fatalf("outil %s manquant", n)
		}
	}

	r1 := call(t, cs, "report_issue", map[string]any{
		"title": "get_print_status garde 42 % alors que l'impression est finie", "severity": "P1", "tool": "get_print_status",
		"expected": "100 %", "observed": "42 % ; code de lancement 123456 ; access_code=87654321", "print_job_id": "pj_aaaaaaaa1111", "printer_id": "pr_1",
	})
	s1 := structured(t, r1)
	iss := s1["issue"].(map[string]any)
	id, _ := iss["id"].(string)
	if s1["created"] != true || !strings.HasPrefix(id, "iss_") || s1["redacted"] != true || iss["state"] != "new" || iss["occurrences"] != float64(1) {
		t.Fatalf("report_issue : %v", s1)
	}
	if raw, _ := json.Marshal(s1); strings.Contains(string(raw), "123456") || strings.Contains(string(raw), "87654321") || strings.Contains(resultText(r1), "123456") {
		t.Errorf("secret renvoyé : %s", raw)
	}

	// même symptôme (autre nombre, autre identifiant) : compteur, pas de nouvelle ligne
	s2 := structured(t, call(t, cs, "report_issue", map[string]any{"title": "get_print_status garde 87 % alors que l'impression est finie", "tool": "get_print_status", "severity": "P0"}))
	if s2["created"] != false || s2["issue"].(map[string]any)["id"] != id || s2["issue"].(map[string]any)["occurrences"] != float64(2) || s2["issue"].(map[string]any)["severity"] != "P0" {
		t.Fatalf("dédoublonnage : %v", s2)
	}
	if res := call(t, cs, "report_issue", map[string]any{"title": ""}); !res.IsError {
		t.Error("titre vide accepté")
	}

	call(t, cs, "report_issue", map[string]any{"title": "Réponse de list_print_log trop lourde", "tool": "list_print_log", "severity": "P3"})

	l := structured(t, call(t, cs, "list_issues", map[string]any{}))
	if l["total"] != float64(2) || l["issues"].([]any)[0].(map[string]any)["id"] != id {
		t.Fatalf("list_issues : %v", l)
	}
	if l := structured(t, call(t, cs, "list_issues", map[string]any{"severity": "P3"})); l["total"] != float64(1) {
		t.Errorf("filtre de gravité : %v", l)
	}
	if res := call(t, cs, "list_issues", map[string]any{"state": "n'importe quoi"}); !res.IsError {
		t.Error("état inconnu accepté")
	}

	if res := call(t, cs, "update_issue", map[string]any{"id": id, "state": "fixed"}); !res.IsError {
		t.Error("fixed sans commit accepté")
	}
	u := structured(t, call(t, cs, "update_issue", map[string]any{"id": id, "state": "fixed", "fix_commit": "3f9a1c2"}))
	if u["state"] != "fixed" || u["fix_commit"] != "3f9a1c2" {
		t.Fatalf("update_issue : %v", u)
	}
	if l := structured(t, call(t, cs, "list_issues", map[string]any{})); l["total"] != float64(1) {
		t.Errorf("le constat corrigé reste dans les ouverts : %v", l)
	}

	exp := call(t, cs, "export_issues", map[string]any{"state": "all"})
	es := structured(t, exp)
	md, _ := es["markdown"].(string)
	if es["count"] != float64(2) || md != resultText(exp) || !strings.Contains(md, "# Dysfonctionnements de Printalk") || !strings.Contains(md, "commit 3f9a1c2") || !strings.Contains(md, "get_print_status") {
		t.Errorf("export : %v\n%s", es, md)
	}
	for _, bad := range []string{"123456", "87654321"} {
		if strings.Contains(md, bad) {
			t.Errorf("secret %s dans l'export", bad)
		}
	}
}
