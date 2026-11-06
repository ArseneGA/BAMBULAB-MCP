package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink"
)

func getJSON(t *testing.T, url string, out any) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func postJSON(t *testing.T, url string, in, out any) int {
	t.Helper()
	b, _ := json.Marshal(in)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func TestAgentAvailabilityInCapabilities(t *testing.T) {
	e := setup(t)
	bg := context.Background()
	feature := func(caps map[string]any, id string) map[string]any {
		for _, f := range caps["features"].([]any) {
			if m := f.(map[string]any); m["id"] == id {
				return m
			}
		}
		t.Fatalf("fonction %s absente", id)
		return nil
	}
	var caps map[string]any
	getJSON(t, e.srv.URL+"/api/capabilities", &caps)
	if caps["mode"] != "linux_seul" || feature(caps, "windows_agent")["available"] != false || caps["agents"] != nil {
		t.Fatalf("sans agent : %v", caps)
	}

	// Appairage : jeton créé par la CLI humaine (`bambud agent pair`), échangé une fois.
	var pr struct {
		Token string
	}
	tok, _, err := agentlink.CreatePairing(bg, e.c.Store, agentlink.DefaultPairingTTL)
	if err != nil || !strings.HasPrefix(tok, "pair-") {
		t.Fatalf("pairing : %v %q", err, tok)
	}
	pr.Token = tok
	cl := agentlink.NewClient(e.srv.URL, "")
	resp, err := cl.Pair(bg, agentlink.PairRequest{Token: pr.Token, Name: "PC-Bureau"})
	if err != nil {
		t.Fatal(err)
	}
	cl.Secret = resp.Secret
	if _, err := cl.Hello(bg, agentlink.HelloRequest{Capabilities: agentlink.AgentCaps{OS: "windows", Tasks: []string{agentlink.KindGetProjectState, agentlink.KindOpenInStudio},
		Studio: agentlink.StudioCaps{Installed: true, Version: "02.08.02.61", Running: true}, SharedFolders: []agentlink.FolderCap{{Label: "Modèles"}}}}); err != nil {
		t.Fatal(err)
	}

	caps = map[string]any{}
	getJSON(t, e.srv.URL+"/api/capabilities", &caps)
	agents, _ := caps["agents"].([]any)
	if caps["mode"] != "linux_renfort_windows" || feature(caps, "windows_agent")["available"] != true || len(agents) != 1 {
		t.Fatalf("avec agent : %v", caps)
	}
	a := agents[0].(map[string]any)
	if a["online"] != true || a["name"] != "PC-Bureau" || a["studio_version"] != "02.08.02.61" || a["last_seen"] == "" {
		t.Fatalf("agent : %v", a)
	}
	if feature(caps, "studio_session")["available"] != true || feature(caps, "windows_slicing")["available"] != false {
		t.Fatalf("fonctions : %v", caps["features"])
	}

	// Le jeton est à usage unique.
	if _, err := agentlink.NewClient(e.srv.URL, "").Pair(bg, agentlink.PairRequest{Token: pr.Token, Name: "bis"}); err == nil {
		t.Fatal("jeton réutilisé")
	}
	// Routes agent sans secret : 401. Travail confié : suivi par l'API.
	if st := getJSON(t, e.srv.URL+agentlink.BasePath+"/tasks/next", nil); st != 401 {
		t.Fatalf("sans secret : %d", st)
	}
	var ti agentlink.TaskInfo
	if st := postJSON(t, e.srv.URL+"/api/agent/tasks", map[string]any{"kind": "get_project_state"}, &ti); st != 202 || ti.Executor != "windows-agent" {
		t.Fatalf("création : %d %+v", st, ti)
	}
	tk, _ := cl.NextTask(bg, time.Second)
	if tk == nil || tk.ID != ti.ID {
		t.Fatalf("remise : %+v", tk)
	}
	_ = cl.StartTask(bg, ti.ID)
	_ = cl.Result(bg, ti.ID, agentlink.TaskResult{State: "succeeded", Result: json.RawMessage(`{"open":false}`)})
	var got agentlink.TaskInfo
	getJSON(t, e.srv.URL+"/api/agent/tasks/"+ti.ID, &got)
	if got.State != "succeeded" || string(got.Result) != `{"open":false}` {
		t.Fatalf("suivi : %+v", got)
	}
	// Refus clairs.
	var er map[string]map[string]string
	if st := postJSON(t, e.srv.URL+"/api/agent/tasks", map[string]any{"kind": "modify_open_project"}, &er); st != 501 || er["error"]["code"] != "not_supported" {
		t.Fatalf("modify_open_project : %d %v", st, er)
	}
	if st := postJSON(t, e.srv.URL+"/api/agent/tasks", map[string]any{"kind": "start_print"}, &er); st != 409 || er["error"]["code"] != "physical_task_refused" {
		t.Fatalf("start_print : %d %v", st, er)
	}
	// Révocation : l'agent disparaît des capacités.
	req, _ := http.NewRequest("DELETE", e.srv.URL+"/api/agents/"+resp.AgentID, nil)
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 200 {
		t.Fatalf("révocation : %v %v", r, err)
	}
	caps = map[string]any{}
	getJSON(t, e.srv.URL+"/api/capabilities", &caps)
	if caps["mode"] != "linux_seul" || caps["agents"] != nil {
		t.Fatalf("après révocation : %v", caps)
	}
}
