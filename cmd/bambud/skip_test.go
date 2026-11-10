package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// fakeSkipService imite les trois routes locales avec les VRAIS types du service (les noms de champs JSON sont ceux que
// la sous-commande doit lire) ; il enregistre ce que la sous-commande lui envoie.
type fakeSkipService struct {
	mu       sync.Mutex
	calls    []string
	bodies   map[string]map[string]any
	auth     []string
	execStat string
}

func newFakeSkip(t *testing.T, execStatus string) (*fakeSkipService, *httptest.Server) {
	f := &fakeSkipService{bodies: map[string]map[string]any{}, execStat: execStatus}
	objs := []printing.SkipObject{{ID: 130, Name: "Cube 1"}, {ID: 174, Name: "Pied #3"}, {ID: 200, Name: "Cube 3"}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/system/skip-objects/{op}", func(w http.ResponseWriter, r *http.Request) {
		op := r.PathValue("op")
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.calls = append(f.calls, op)
		f.bodies[op] = in
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch op {
		case "list":
			_ = json.NewEncoder(w).Encode(printing.SkipListOutput{PrintJobID: "prt_1", File: "plaque.gcode.3mf", JobState: "PRINTING", PrinterState: "printing", Objects: objs, CanSkip: true})
		case "prepare":
			ids, _ := in["ids"].([]any)
			if len(ids) == 1 && ids[0] == float64(999) {
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "identifiant 999 absent du fichier de cette impression"})
				return
			}
			_ = json.NewEncoder(w).Encode(printing.SkipPrepareOutput{PrintJobID: "prt_1", File: "plaque.gcode.3mf", Targets: []printing.SkipObject{objs[1]},
				Remaining: []printing.SkipObject{objs[0], objs[2]}, ConfirmToken: "skp_abc", ExpiresAt: time.Now().Add(time.Minute)})
		case "execute":
			msg := "Vérifié : l'imprimante liste ces objets comme ignorés (s_obj)."
			sk := []int{174}
			if f.execStat != printing.SkipVerified {
				msg, sk = "Envoyée, non confirmée : s_obj n'a pas changé. La commande n'est PAS renvoyée.", nil
			}
			_ = json.NewEncoder(w).Encode(printing.SkipResultOutput{Status: f.execStat, PrintJobID: "prt_1", Skipped: sk, Message: msg})
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, srv
}

func skipRun(t *testing.T, srv *httptest.Server, stdin string, tty bool, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	addr := strings.TrimPrefix(srv.URL, "http://")
	code := runSkipObjects(append([]string{"--addr", addr}, args...), skipEnv{in: strings.NewReader(stdin), isTTY: func() bool { return tty }, out: &out, errw: &errb, client: srv.Client()})
	return code, out.String(), errb.String()
}

func TestSkipObjectsCLIQuestionAndAccept(t *testing.T) {
	f, srv := newFakeSkip(t, printing.SkipVerified)
	code, out, errs := skipRun(t, srv, "o\n", true, "--job", "prt_1", "--ids", "174", "--api-token", "jeton-de-test")
	if code != 0 {
		t.Fatalf("code %d\n%s\n%s", code, out, errs)
	}
	want := "Ignorer 1 objet (« Pied #3 », id 174) ? Cette action est irréversible [o/N] "
	if !strings.Contains(out, want) {
		t.Fatalf("question attendue %q dans :\n%s", want, out)
	}
	if !strings.Contains(out, "Statut : verified") || !strings.Contains(out, "Continueront d'être imprimés : « Cube 1 » (id 130), « Cube 3 » (id 200).") {
		t.Fatalf("sortie :\n%s", out)
	}
	if strings.Join(f.calls, ",") != "prepare,execute" {
		t.Fatalf("appels : %v", f.calls)
	}
	if f.bodies["execute"]["confirm_token"] != "skp_abc" || f.bodies["execute"]["wait_s"] != float64(20) {
		t.Fatalf("exécution : %v", f.bodies["execute"])
	}
	if ids, _ := f.bodies["prepare"]["ids"].([]any); len(ids) != 1 || ids[0] != float64(174) {
		t.Fatalf("prepare : %v", f.bodies["prepare"])
	}
	for _, a := range f.auth {
		if a != "Bearer jeton-de-test" {
			t.Fatalf("en-tête d'autorisation : %q", a)
		}
	}
	if strings.Contains(out+errs, "jeton-de-test") {
		t.Fatal("le jeton d'API ne doit apparaître dans aucune sortie")
	}
}

func TestSkipObjectsCLIPluralQuestion(t *testing.T) {
	got := skipQuestion([]skipObjJSON{{ID: 130, Name: "A"}, {ID: 174, Name: "B"}})
	if got != "Ignorer 2 objets (« A », id 130 ; « B », id 174) ? Cette action est irréversible [o/N] " {
		t.Fatalf("%q", got)
	}
}

// Toute réponse autre que « o » / « oui » (y compris vide, fin de fichier) : rien n'est envoyé.
func TestSkipObjectsCLIDeclineSendsNothing(t *testing.T) {
	for _, ans := range []string{"\n", "n\n", "N\n", "non\n", "y\n", "yes\n", "", "ok\n"} {
		f, srv := newFakeSkip(t, printing.SkipVerified)
		code, out, _ := skipRun(t, srv, ans, true, "--job", "prt_1", "--ids", "174")
		if code != 1 || !strings.Contains(out, "rien n'a été envoyé") {
			t.Fatalf("réponse %q : code %d\n%s", ans, code, out)
		}
		if strings.Join(f.calls, ",") != "prepare" {
			t.Fatalf("réponse %q : appels %v", ans, f.calls)
		}
	}
}

// Pas de terminal : refus avant tout appel réseau. Pas d'option --yes, sous aucune forme.
func TestSkipObjectsCLIRefusesWithoutTerminalAndWithYes(t *testing.T) {
	f, srv := newFakeSkip(t, printing.SkipVerified)
	code, _, errs := skipRun(t, srv, "o\n", false, "--job", "prt_1", "--ids", "174")
	if code != 2 || !strings.Contains(errs, "pas un terminal") || len(f.calls) != 0 {
		t.Fatalf("sans terminal : %d %q %v", code, errs, f.calls)
	}
	for _, y := range []string{"--yes", "-yes", "-y", "--yes=true", "--assume-yes", "--force"} {
		code, _, errs := skipRun(t, srv, "o\n", true, "--job", "prt_1", "--ids", "174", y)
		if code != 2 || !strings.Contains(errs, "confirmation interactive est obligatoire") || len(f.calls) != 0 {
			t.Fatalf("%s : %d %q %v", y, code, errs, f.calls)
		}
	}
}

func TestSkipObjectsCLIArguments(t *testing.T) {
	f, srv := newFakeSkip(t, printing.SkipVerified)
	for name, args := range map[string][]string{
		"sans job":       {"--ids", "174"},
		"sans ids":       {"--job", "p"},
		"id nul":         {"--job", "p", "--ids", "0"},
		"id négatif":     {"--job", "p", "--ids", "-4"},
		"id texte":       {"--job", "p", "--ids", "abc"},
		"doublon":        {"--job", "p", "--ids", "5,5"},
		"liste creuse":   {"--job", "p", "--ids", "5,,6"},
		"trop d'ids":     {"--job", "p", "--ids", strings.TrimSuffix(strings.Repeat("1,", 65), ",")},
		"list et ids":    {"--job", "p", "--list", "--ids", "5"},
		"wait trop bas":  {"--job", "p", "--ids", "5", "--wait", "100ms"},
		"wait trop haut": {"--job", "p", "--ids", "5", "--wait", "1h"},
		"surplus":        {"--job", "p", "--ids", "5", "extra"},
	} {
		if code, _, _ := skipRun(t, srv, "o\n", true, args...); code != 2 {
			t.Fatalf("%s : code %d", name, code)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("aucun appel attendu : %v", f.calls)
	}
}

// Le service refuse (identifiant absent, déjà ignoré, tous les objets…) : message affiché, jamais d'exécution.
func TestSkipObjectsCLIRefusedByService(t *testing.T) {
	f, srv := newFakeSkip(t, printing.SkipVerified)
	code, _, errs := skipRun(t, srv, "o\n", true, "--job", "prt_1", "--ids", "999")
	if code != 1 || !strings.Contains(errs, "absent du fichier") {
		t.Fatalf("%d %q", code, errs)
	}
	if strings.Join(f.calls, ",") != "prepare" {
		t.Fatalf("appels : %v", f.calls)
	}
}

func TestSkipObjectsCLISentUnconfirmedExitCode(t *testing.T) {
	_, srv := newFakeSkip(t, printing.SkipSentUnconfirmed)
	code, out, _ := skipRun(t, srv, "oui\n", true, "--job", "prt_1", "--ids", "174", "--wait", "5s")
	if code != 3 || !strings.Contains(out, "Statut : sent_unconfirmed") || !strings.Contains(out, "PAS renvoyée") {
		t.Fatalf("%d\n%s", code, out)
	}
}

// --list : aucun envoi, pas besoin de terminal, pas de question.
func TestSkipObjectsCLIListSendsNothing(t *testing.T) {
	f, srv := newFakeSkip(t, printing.SkipVerified)
	code, out, errs := skipRun(t, srv, "", false, "--job", "prt_1", "--list")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	for _, w := range []string{"id 130", "« Cube 1 »", "id 174", "« Pied #3 »", "On peut ignorer un objet maintenant"} {
		if !strings.Contains(out, w) {
			t.Fatalf("%q absent :\n%s", w, out)
		}
	}
	if strings.Join(f.calls, ",") != "list" || strings.Contains(out, "irréversible") {
		t.Fatalf("appels : %v", f.calls)
	}
}

func TestSkipBase(t *testing.T) {
	for in, want := range map[string]string{"127.0.0.1:7878": "http://127.0.0.1:7878", "0.0.0.0:9": "http://127.0.0.1:9", "[::]:9": "http://127.0.0.1:9", ":9": "http://127.0.0.1:9", "localhost:9": "http://127.0.0.1:9"} {
		if got, err := skipBase(in); err != nil || got != want {
			t.Fatalf("%s -> %s %v", in, got, err)
		}
	}
	if _, err := skipBase("pas-une-adresse"); err == nil {
		t.Fatal("adresse invalide acceptée")
	}
}

// Service injoignable : message clair, code 1, rien d'envoyé.
func TestSkipObjectsCLIServiceDown(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	code, _, errs := skipRun(t, srv, "o\n", true, "--job", "p", "--ids", "5")
	if code != 1 || !strings.Contains(errs, "injoignable") {
		t.Fatalf("%d %q", code, errs)
	}
}
