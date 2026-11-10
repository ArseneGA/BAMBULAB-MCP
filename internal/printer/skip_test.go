package printer_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer/sim"
)

func skipPayload(seq string, ids any) map[string]any {
	return map[string]any{"print": map[string]any{"sequence_id": seq, "command": "skip_objects", "obj_list": ids}}
}

// CA0.1 : le validateur refuse liste vide, entier <= 0, doublon, > 64 éléments, clé en trop ; seule la forme exacte passe.
func TestValidateSkipObjectsPayload(t *testing.T) {
	ok := func(name string, p map[string]any) {
		t.Helper()
		if err := printer.ValidateSkipObjectsPayload(p); err != nil {
			t.Fatalf("%s : refusé à tort : %v", name, err)
		}
	}
	bad := func(name string, p map[string]any) {
		t.Helper()
		err := printer.ValidateSkipObjectsPayload(p)
		if err == nil {
			t.Fatalf("%s : accepté à tort", name)
		}
		if !errors.Is(err, printer.ErrSkipNotAllowed) {
			t.Fatalf("%s : erreur %v", name, err)
		}
	}
	ok("forme exacte []int", skipPayload("1001", []int{130, 174}))
	ok("forme exacte []any (JSON)", skipPayload("1001", []any{float64(130), float64(174)}))
	big := make([]int, 64)
	for i := range big {
		big[i] = i + 1
	}
	ok("64 éléments", skipPayload("7", big))

	bad("liste vide", skipPayload("1", []int{}))
	bad("liste absente", skipPayload("1", nil))
	bad("zéro", skipPayload("1", []int{0}))
	bad("négatif", skipPayload("1", []int{5, -3}))
	bad("doublon", skipPayload("1", []int{5, 5}))
	bad("fraction", skipPayload("1", []any{1.5}))
	bad("chaîne", skipPayload("1", []any{"130"}))
	bad("65 éléments", skipPayload("1", append(big, 65)))
	bad("sequence_id non numérique", skipPayload("abc", []int{1}))
	bad("sequence_id absent", map[string]any{"print": map[string]any{"command": "skip_objects", "obj_list": []int{1}}})
	bad("autre commande", map[string]any{"print": map[string]any{"sequence_id": "1", "command": "stop", "obj_list": []int{1}}})
	bad("clé en trop (param)", map[string]any{"print": map[string]any{"sequence_id": "1", "command": "skip_objects", "obj_list": []int{1}, "param": ""}})
	bad("clé en trop (gcode)", map[string]any{"print": map[string]any{"sequence_id": "1", "command": "skip_objects", "obj_list": []int{1}, "gcode": "G28"}})
	bad("deux objets racine", map[string]any{"print": map[string]any{"sequence_id": "1", "command": "skip_objects", "obj_list": []int{1}}, "system": map[string]any{}})
	bad("racine system", map[string]any{"system": map[string]any{"sequence_id": "1", "command": "skip_objects", "obj_list": []int{1}}})
	bad("objet vide", map[string]any{})
}

// La liste blanche des accessoires continue de REFUSER skip_objects : il n'y entre pas.
func TestAccessoryWhitelistStillRefusesSkipObjects(t *testing.T) {
	err := printer.ValidateAccessoryPayload(skipPayload("1001", []int{130}))
	if err == nil || !errors.Is(err, printer.ErrAccessoryNotAllowed) {
		t.Fatalf("la liste blanche des accessoires doit refuser skip_objects : %v", err)
	}
}

func printingEnv(t *testing.T, faults ...string) *env {
	t.Helper()
	e := newEnv(t, sim.Config{PrepareDuration: 100 * time.Millisecond, PrintDuration: 60 * time.Second, Faults: faults}, nil)
	e.sim.SetObjects([]sim.Object{{ID: 130, Name: "A", Grams: 10}, {ID: 174, Name: "B", Grams: 10}, {ID: 200, Name: "C", Grams: 10}})
	ctx := ctx5(t)
	ref := e.upload(newArtefact(32 * 1024))
	if err := e.ad.Start(ctx, e.request(ref, "tok-skip-1")); err != nil {
		t.Fatal(err)
	}
	waitState(t, e.ad, printer.StatePrinting, 10*time.Second)
	return e
}

func waitSkipped(t *testing.T, e *env, want []int, d time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		st, _ := e.ad.Status(context.Background())
		if len(st.SkippedObjects) == len(want) {
			same := true
			for i := range want {
				same = same && st.SkippedObjects[i] == want[i]
			}
			if same {
				return true
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}

// Simulateur : la commande exacte part UNE fois, s_obj est lu dans Status, l'objet ignoré cesse de consommer.
func TestSimSkipObjectsVerified(t *testing.T) {
	e := printingEnv(t)
	ctx := ctx5(t)
	if st, _ := e.ad.Status(ctx); len(st.SkippedObjects) != 0 {
		t.Fatalf("s_obj au départ : %v", st.SkippedObjects)
	}
	time.Sleep(300 * time.Millisecond)
	if err := e.ad.SkipObjects(ctx, []int{174}); err != nil {
		t.Fatal(err)
	}
	if !waitSkipped(t, e, []int{174}, 5*time.Second) {
		st, _ := e.ad.Status(ctx)
		t.Fatalf("s_obj n'a pas reçu 174 : %v", st.SkippedObjects)
	}
	if e.ad.LastSkipReply() != "success" {
		t.Fatalf("réponse : %q", e.ad.LastSkipReply())
	}
	if n := e.sim.CommandCount("skip_objects"); n != 1 {
		t.Fatalf("skip_objects reçu %d fois", n)
	}
	// forme exacte du message reçu par l'imprimante
	raw := e.sim.SkipPayloads()
	if len(raw) != 1 {
		t.Fatalf("charges reçues : %v", raw)
	}
	var m map[string]map[string]any
	if err := json.Unmarshal([]byte(raw[0]), &m); err != nil || len(m) != 1 {
		t.Fatalf("%s : %v", raw[0], err)
	}
	p := m["print"]
	if len(p) != 3 || p["command"] != "skip_objects" || p["sequence_id"] == nil {
		t.Fatalf("forme : %s", raw[0])
	}
	if l, _ := p["obj_list"].([]any); len(l) != 1 || l[0] != float64(174) {
		t.Fatalf("obj_list : %s", raw[0])
	}
	// l'objet ignoré cesse de consommer ; les autres continuent
	c1 := e.sim.ObjectConsumed(174)
	c2 := e.sim.ObjectConsumed(130)
	time.Sleep(600 * time.Millisecond)
	if e.sim.ObjectConsumed(174) != c1 {
		t.Fatal("l'objet ignoré consomme encore")
	}
	_ = c2
	// aucune pause / reprise / arrêt / G-code
	for _, c := range e.sim.Commands() {
		if c != "project_file" && c != "skip_objects" {
			t.Fatalf("commande inattendue : %q (toutes : %v)", c, e.sim.Commands())
		}
	}
}

// Hors impression : refus côté client, rien n'est envoyé.
func TestSimSkipObjectsRefusedWhenIdle(t *testing.T) {
	e := newEnv(t, sim.Config{}, nil)
	waitState(t, e.ad, printer.StateIdle, 10*time.Second)
	err := e.ad.SkipObjects(ctx5(t), []int{1})
	if !errors.Is(err, printer.ErrBadState) {
		t.Fatalf("au repos : %v", err)
	}
	if e.sim.CommandCount("skip_objects") != 0 {
		t.Fatal("rien ne devait partir")
	}
	// identifiants invalides : refusés par le validateur, rien ne part
	e2 := printingEnv(t)
	for _, ids := range [][]int{nil, {0}, {3, 3}} {
		if err := e2.ad.SkipObjects(ctx5(t), ids); !errors.Is(err, printer.ErrSkipNotAllowed) {
			t.Fatalf("%v : %v", ids, err)
		}
	}
	if e2.sim.CommandCount("skip_objects") != 0 {
		t.Fatal("rien ne devait partir")
	}
}

// Le simulateur qui ignore la commande : un seul envoi, s_obj inchangé, aucune nouvelle tentative.
func TestSimSkipObjectsIgnoredByPrinter(t *testing.T) {
	e := printingEnv(t, sim.FaultIgnoreSkip)
	if err := e.ad.SkipObjects(ctx5(t), []int{130}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)
	if st, _ := e.ad.Status(ctx5(t)); len(st.SkippedObjects) != 0 {
		t.Fatalf("s_obj : %v", st.SkippedObjects)
	}
	if n := e.sim.CommandCount("skip_objects"); n != 1 {
		t.Fatalf("reçu %d fois (jamais renvoyé)", n)
	}
}

// Identifiant inconnu du simulateur : réponse fail, relevée par LastSkipReply.
func TestSimSkipObjectsRefusedByPrinter(t *testing.T) {
	e := printingEnv(t)
	if err := e.ad.SkipObjects(ctx5(t), []int{999}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for e.ad.LastSkipReply() == "" && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if r := e.ad.LastSkipReply(); r == "" || r == "success" {
		t.Fatalf("réponse attendue « fail » : %q", r)
	}
	if st, _ := e.ad.Status(ctx5(t)); len(st.SkippedObjects) != 0 {
		t.Fatalf("s_obj : %v", st.SkippedObjects)
	}
}
