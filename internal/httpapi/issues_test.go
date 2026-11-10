package httpapi_test

import (
	"strings"
	"testing"
)

func TestIssuesEndpoints(t *testing.T) {
	srv, c := insightsServer(t)
	base := srv.URL + "/api/issues"

	code, l := do(t, "GET", base, "")
	if code != 200 || l["total"] != float64(0) || len(l["issues"].([]any)) != 0 {
		t.Fatalf("liste vide : %d %v", code, l)
	}
	code, o := do(t, "POST", base, `{"title":"Fausse alerte de bobine","tool":"list_spools","severity":"P1","observed":"code de lancement 123456 refusé"}`)
	iss, _ := o["issue"].(map[string]any)
	if code != 201 || o["created"] != true || o["redacted"] != true || iss["state"] != "new" {
		t.Fatalf("POST : %d %v", code, o)
	}
	id := iss["id"].(string)
	if strings.Contains(iss["observed"].(string), "123456") {
		t.Errorf("secret renvoyé : %v", iss)
	}
	// doublon : 200, compteur
	code, o = do(t, "POST", base, `{"title":"fausse alerte de bobine","tool":"list_spools"}`)
	if code != 200 || o["created"] != false || o["issue"].(map[string]any)["occurrences"] != float64(2) {
		t.Fatalf("doublon : %d %v", code, o)
	}
	for _, bad := range []string{`{"title":""}`, `{"title":"x","severity":"P9"}`, `{"title":"x","inconnu":1}`} {
		if code, _ := do(t, "POST", base, bad); code != 400 {
			t.Errorf("%s : %d", bad, code)
		}
	}

	code, g := do(t, "GET", base+"/"+id, "")
	if code != 200 || g["id"] != id {
		t.Fatalf("GET un : %d %v", code, g)
	}
	if code, _ := do(t, "GET", base+"/iss_none", ""); code != 404 {
		t.Errorf("inconnu : %d", code)
	}
	if code, _ := do(t, "PATCH", base+"/"+id, `{"state":"fixed"}`); code != 400 {
		t.Errorf("fixed sans commit : %d", code)
	}
	if code, _ := do(t, "PATCH", base+"/"+id, `{"id":"autre","state":"seen"}`); code != 400 {
		t.Errorf("id incohérent : %d", code)
	}
	code, p := do(t, "PATCH", base+"/"+id, `{"state":"fixed","fix_commit":"abc1234"}`)
	if code != 200 || p["state"] != "fixed" || p["fix_commit"] != "abc1234" {
		t.Fatalf("PATCH : %d %v", code, p)
	}
	if code, _ := do(t, "PATCH", base+"/iss_none", `{"state":"seen"}`); code != 404 {
		t.Errorf("PATCH inconnu : %d", code)
	}
	if code, l := do(t, "GET", base+"?state=fixed&severity=P1", ""); code != 200 || l["total"] != float64(1) {
		t.Errorf("filtres : %d %v", code, l)
	}
	if code, _ := do(t, "GET", base+"?state=nope", ""); code != 400 {
		t.Errorf("état inconnu : %d", code)
	}

	st, hdr, body := raw(t, "GET", base+"/export?state=all", "", nil)
	if st != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/markdown") || !strings.Contains(string(body), "# Dysfonctionnements de Printalk") ||
		!strings.Contains(string(body), "commit abc1234") || strings.Contains(string(body), "123456") {
		t.Errorf("export : %d %s\n%s", st, hdr.Get("Content-Type"), body)
	}
	if n, _ := c.Issues.Get(t.Context(), id); n == nil || n.Occurrences != 2 {
		t.Errorf("service : %+v", n)
	}
}
