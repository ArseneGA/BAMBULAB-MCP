package upgrade

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func run(name, status, conclusion string) checkRun {
	return checkRun{Name: name, Status: status, Conclusion: conclusion}
}

func TestEvaluate(t *testing.T) {
	allOK := []checkRun{run("go", "completed", "success"), run("web", "completed", "success"), run("docker", "completed", "skipped"), run("py", "completed", "neutral")}
	cases := []struct {
		name     string
		runs     []checkRun
		state    string
		total    int
		required []string
		want     CIState
		in       string
	}{
		{"tout vert", allOK, "", 0, nil, CIGreen, "verte"},
		{"un échec", append([]checkRun{run("go", "completed", "failure")}, allOK...), "", 0, nil, CIRed, "go (failure)"},
		{"annulé = rouge", []checkRun{run("go", "completed", "cancelled")}, "", 0, nil, CIRed, "cancelled"},
		{"timed_out = rouge", []checkRun{run("go", "completed", "timed_out")}, "", 0, nil, CIRed, ""},
		{"en cours", append([]checkRun{run("go", "in_progress", "")}, allOK...), "", 0, nil, CIPending, "go"},
		{"en file", []checkRun{run("go", "queued", "")}, "", 0, nil, CIPending, ""},
		{"rouge prime sur en cours", []checkRun{run("go", "in_progress", ""), run("web", "completed", "failure")}, "", 0, nil, CIRed, ""},
		{"aucun contrôle encore", nil, "pending", 0, nil, CIPending, "aucun contrôle"},
		{"statut combiné en échec", allOK, "failure", 1, nil, CIRed, "statuts"},
		{"statut combiné en cours", allOK, "pending", 2, nil, CIPending, "statuts"},
		{"statut combiné réussi", allOK, "success", 1, nil, CIGreen, ""},
		{"requis présents", allOK, "", 0, []string{"go", "web"}, CIGreen, ""},
		{"requis absent : on attend", allOK[:1], "", 0, []string{"go", "web"}, CIPending, "web"},
		{"requis en échec", []checkRun{run("go", "completed", "failure"), run("web", "completed", "success")}, "", 0, []string{"go"}, CIRed, ""},
		{"non requis ignoré", []checkRun{run("go", "completed", "success"), run("docker", "completed", "failure")}, "", 0, []string{"go"}, CIGreen, ""},
	}
	for _, c := range cases {
		got := Evaluate(c.runs, c.state, c.total, c.required)
		if got.State != c.want || !strings.Contains(got.Detail, c.in) {
			t.Errorf("%s : %s (%s), attendu %s contenant %q", c.name, got.State, got.Detail, c.want, c.in)
		}
	}
}

type ghServer struct {
	mu       sync.Mutex
	runs     []checkRun
	state    string
	total    int
	status   int // HTTP forcé (0 : normal)
	remain   string
	auth     []string // en-têtes Authorization reçus
	requests []string
}

func (g *ghServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.requests = append(g.requests, r.URL.RequestURI())
		g.auth = append(g.auth, r.Header.Get("Authorization"))
		if g.status != 0 {
			if g.remain != "" {
				w.Header().Set("X-RateLimit-Remaining", g.remain)
			}
			w.WriteHeader(g.status)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			page := r.URL.Query().Get("page")
			lo, hi := 0, len(g.runs)
			if page == "2" {
				lo = 100
			} else if len(g.runs) > 100 {
				hi = 100
			}
			if lo > len(g.runs) {
				lo = len(g.runs)
			}
			fmt.Fprintf(w, `{"total_count":%d,"check_runs":[`, len(g.runs))
			for i, c := range g.runs[lo:hi] {
				if i > 0 {
					fmt.Fprint(w, ",")
				}
				fmt.Fprintf(w, `{"name":%q,"status":%q,"conclusion":%q}`, c.Name, c.Status, c.Conclusion)
			}
			fmt.Fprint(w, `]}`)
		case strings.HasSuffix(r.URL.Path, "/status"):
			fmt.Fprintf(w, `{"state":%q,"total_count":%d}`, g.state, g.total)
		default:
			w.WriteHeader(404)
		}
	})
}

func TestCIChecker(t *testing.T) {
	g := &ghServer{runs: []checkRun{run("go", "completed", "success")}, state: "pending"}
	srv := httptest.NewServer(g.handler())
	defer srv.Close()
	c := &CIChecker{BaseURL: srv.URL, Owner: "ArseneGA", Repo: "BAMBULAB-MCP"}
	ctx := context.Background()

	if r := c.Status(ctx, shaNew); r.State != CIGreen {
		t.Fatalf("vert : %+v", r)
	}
	if len(g.requests) != 2 || !strings.HasPrefix(g.requests[0], "/repos/ArseneGA/BAMBULAB-MCP/commits/"+shaNew+"/check-runs") {
		t.Fatalf("requêtes : %v", g.requests)
	}
	if g.auth[0] != "" {
		t.Fatal("sans jeton configuré, aucun en-tête Authorization")
	}

	g.runs = append(g.runs, run("web", "completed", "failure"))
	if r := c.Status(ctx, shaNew); r.State != CIRed {
		t.Fatalf("rouge : %+v", r)
	}
	g.runs = []checkRun{run("go", "in_progress", "")}
	if r := c.Status(ctx, shaNew); r.State != CIPending {
		t.Fatalf("en cours : %+v", r)
	}

	// Pagination : plus de 100 contrôles.
	g.runs = nil
	for i := 0; i < 130; i++ {
		g.runs = append(g.runs, run(fmt.Sprintf("c%03d", i), "completed", "success"))
	}
	g.runs[120] = run("c120", "completed", "failure") // sur la page 2
	if r := c.Status(ctx, shaNew); r.State != CIRed || !strings.Contains(r.Detail, "c120") {
		t.Fatalf("pagination : %+v", r)
	}

	// Jeton : envoyé seulement s'il est configuré, jamais renvoyé dans un message d'erreur.
	c.Token = "ghp_SECRETSECRET"
	g.runs = []checkRun{run("go", "completed", "success")}
	c.Status(ctx, shaNew)
	if g.auth[len(g.auth)-1] != "Bearer ghp_SECRETSECRET" {
		t.Fatalf("Authorization : %q", g.auth[len(g.auth)-1])
	}

	// Limite de débit, 404, serveur en panne : « inconnu » (on attend), jamais « vert ».
	g.status, g.remain = 403, "0"
	if r := c.Status(ctx, shaNew); r.State != CIUnknown || !strings.Contains(r.Detail, "limite de débit") {
		t.Fatalf("limite de débit : %+v", r)
	}
	g.status, g.remain = 429, ""
	if r := c.Status(ctx, shaNew); r.State != CIUnknown {
		t.Fatalf("429 : %+v", r)
	}
	g.status = 404
	if r := c.Status(ctx, shaNew); r.State != CIUnknown {
		t.Fatalf("404 : %+v", r)
	}
	g.status = 500
	if r := c.Status(ctx, shaNew); r.State != CIUnknown || strings.Contains(r.Detail, "ghp_") {
		t.Fatalf("500 : %+v", r)
	}
	srv.Close()
	if r := c.Status(ctx, shaNew); r.State != CIUnknown || strings.Contains(r.Detail, "ghp_SECRET") {
		t.Fatalf("serveur arrêté : %+v", r)
	}
}

func TestParseRepoURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/ArseneGA/BAMBULAB-MCP":     "ArseneGA/BAMBULAB-MCP",
		"https://github.com/ArseneGA/BAMBULAB-MCP.git": "ArseneGA/BAMBULAB-MCP",
		"https://github.com/ArseneGA/BAMBULAB-MCP/":    "ArseneGA/BAMBULAB-MCP",
	} {
		o, n, err := ParseRepoURL(in)
		if err != nil || o+"/"+n != want {
			t.Errorf("%s : %s/%s %v", in, o, n, err)
		}
	}
	for _, bad := range []string{"", "github.com", "https://github.com/seul"} {
		if _, _, err := ParseRepoURL(bad); err == nil {
			t.Errorf("%q accepté", bad)
		}
	}
}
