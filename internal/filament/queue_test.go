package filament_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func (r *rig) queueList(finished bool) []map[string]any {
	r.t.Helper()
	var out []map[string]any
	for _, e := range r.call("list_print_queue", map[string]any{"include_finished": finished})["queue"].([]any) {
		out = append(out, e.(map[string]any))
	}
	return out
}

func jobOf(r *rig, queueID string) string {
	return entryFor(r.queueList(false), queueID)["print_job_id"].(string)
}

func entryFor(q []map[string]any, id string) map[string]any {
	for _, e := range q {
		if e["id"] == id {
			return e
		}
	}
	return nil
}

// TestQueueNeverStartsAutomatically : la file prépare (envoi du fichier, confirmation, notification) mais ne
// lance jamais : chaque démarrage est un clic humain, y compris la suivante après la fin d'une impression.
func TestQueueNeverStartsAutomatically(t *testing.T) {
	r := newRig(t)
	r.setup(0)
	r.c.Printing.SetValidator(fakeValidator(fil{"PLA", "#FFFFFF", 5}))
	asg := []any{assign(1, "ams1:1", "PLA blanc")}

	var jobs, qids []string
	for i := 0; i < 3; i++ {
		j := r.validated()
		e := r.call("queue_print", map[string]any{"print_job_id": j, "assignments": asg, "note": fmt.Sprintf("pièce %d", i+1)})["entry"].(map[string]any)
		jobs, qids = append(jobs, j), append(qids, e["id"].(string))
	}
	if r.callErr("queue_print", map[string]any{"print_job_id": jobs[0]}) == "" {
		t.Fatal("la même impression ne peut être en file qu'une fois")
	}
	// La tête est préparée : fichier envoyé, confirmation demandée, notification publiée.
	waitFor(t, 30*time.Second, "tête de file prête", func() bool {
		return entryFor(r.queueList(false), qids[0])["state"] == "ready"
	})
	q := r.queueList(false)
	head := entryFor(q, qids[0])
	if head["confirm_url"] == nil || !strings.Contains(head["confirm_url"].(string), "/confirm/") || head["position"].(float64) != 1 {
		t.Fatalf("tête : %v", head)
	}
	for _, id := range qids[1:] {
		if st := entryFor(q, id)["state"]; st != "waiting" {
			t.Fatalf("les suivantes attendent : %v", st)
		}
	}
	if r.jobState(jobs[1]) != "VALIDATED" {
		t.Fatalf("la suivante ne doit pas être envoyée avant son tour : %s", r.jobState(jobs[1]))
	}
	waitFor(t, 5*time.Second, "événement « suivante prête »", func() bool { return len(r.eventsOf("queue_next_ready")) == 1 })
	ev := r.eventsOf("queue_next_ready")[0]
	if ev.PrintJobID != jobs[0] || ev.ConfirmURL != head["confirm_url"] || !strings.Contains(ev.Message, "plateau") && !strings.Contains(ev.Message, "Vider le plateau") {
		t.Fatalf("événement : %+v", ev)
	}
	if n := len(r.sim.StartCommands()); n != 0 || r.sim.GcodeState() != "IDLE" {
		t.Fatalf("la file a lancé une impression sans accord : %d commande(s), état %s", n, r.sim.GcodeState())
	}
	// Laisser tourner la préparation plusieurs fois : toujours rien.
	for i := 0; i < 3; i++ {
		if err := r.c.Filament.PrepareQueue(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.sim.StartCommands()) != 0 {
		t.Fatal("préparation répétée : commande de lancement reçue")
	}
	// L'humain lance la première (un clic) ; à sa fin, la suivante est préparée et annoncée, JAMAIS lancée.
	r.humanPrint(jobs[0], "cle-start-file-1")
	if txt := r.callErr("remove_from_queue", map[string]any{"queue_id": qids[0]}); !strings.Contains(txt, "lancée") {
		t.Fatalf("retrait d'une impression lancée : %s", txt)
	}
	r.waitJob(jobs[0], "COMPLETED")
	waitFor(t, 40*time.Second, "suivante prête après la fin", func() bool {
		q := r.queueList(false)
		return entryFor(q, qids[1]) != nil && entryFor(q, qids[1])["state"] == "ready" && len(r.eventsOf("queue_next_ready")) == 2
	})
	ev2 := r.eventsOf("queue_next_ready")[1]
	if ev2.PrintJobID != jobs[1] || ev2.ConfirmURL == "" {
		t.Fatalf("événement suivant : %+v", ev2)
	}
	time.Sleep(1500 * time.Millisecond) // laisse à la file toute latitude pour (ne pas) démarrer
	if n := len(r.sim.StartCommands()); n != 1 {
		t.Fatalf("une seule commande de lancement (celle de l'humain) attendue, reçu %d", n)
	}
	if st := r.sim.GcodeState(); st == "PREPARE" || st == "RUNNING" {
		t.Fatalf("la suivante a démarré seule : %s", st)
	}
	if r.jobState(jobs[1]) != "AWAITING_PRINT_CONFIRMATION" {
		t.Fatalf("la suivante attend l'humain : %s", r.jobState(jobs[1]))
	}
	// Le premier élément est clos (terminé) ; l'historique le montre.
	if e := entryFor(r.queueList(true), qids[0]); e == nil || e["state"] != "done" {
		t.Fatalf("historique : %v", e)
	}
	// Un lien de confirmation expiré se renouvelle sans rien approuver.
	renewed := r.httpPost(t, "/api/print-queue/"+qids[1]+"/confirmation", nil)
	if !strings.Contains(fmt.Sprint(renewed), "/confirm/") {
		t.Fatalf("renouvellement : %v", renewed)
	}
	if len(r.sim.StartCommands()) != 1 {
		t.Fatal("renouveler une confirmation ne lance rien")
	}

	// La deuxième est lancée par l'humain à son tour ; la troisième reste en attente sans jamais démarrer seule.
	r.humanPrint(jobs[1], "cle-start-file-2")
	r.waitJob(jobs[1], "COMPLETED")
	waitFor(t, 40*time.Second, "troisième prête", func() bool { return entryFor(r.queueList(false), qids[2])["state"] == "ready" })
	time.Sleep(1200 * time.Millisecond)
	if n := len(r.sim.StartCommands()); n != 2 {
		t.Fatalf("2 lancements humains attendus, reçu %d", n)
	}
	if r.jobState(jobs[2]) != "AWAITING_PRINT_CONFIRMATION" {
		t.Fatalf("troisième : %s", r.jobState(jobs[2]))
	}
	// Nombre de commandes de lancement = nombre d'accords humains, jamais plus.
	for _, rec := range r.sim.StartCommands() {
		if rec.Payload["command"] != "project_file" {
			t.Fatalf("commande inattendue : %v", rec.Payload)
		}
	}
}

// TestQueueOrderingRemoveAndAssignments : ordre, retrait, affectation manquante, découpe en cours.
func TestQueueOrderingRemoveAndAssignments(t *testing.T) {
	r := newRig(t)
	r.setup(0)
	r.c.Printing.SetValidator(fakeValidator(fil{"PLA", "#FFFFFF", 5}))
	asg := []any{assign(1, "ams1:1", "PLA blanc")}

	// Sans affectation : la tête attend, rien n'est envoyé.
	j0 := r.validated()
	q0 := r.call("queue_print", map[string]any{"print_job_id": j0})["entry"].(map[string]any)["id"].(string)
	time.Sleep(300 * time.Millisecond)
	if e := entryFor(r.queueList(false), q0); e["state"] != "needs_assignments" || !strings.Contains(e["message"].(string), "plan_colors") {
		t.Fatalf("affectation manquante : %v", e)
	}
	if r.jobState(j0) != "VALIDATED" {
		t.Fatal("envoi sans affectation")
	}
	// Retirer ne supprime ni n'arrête rien.
	r.call("remove_from_queue", map[string]any{"queue_id": q0})
	if len(r.queueList(false)) != 0 || r.jobState(j0) != "VALIDATED" {
		t.Fatal("retrait")
	}
	if r.callErr("remove_from_queue", map[string]any{"queue_id": q0}) == "" {
		t.Fatal("retrait d'un élément déjà retiré")
	}
	if r.callErr("queue_print", map[string]any{"print_job_id": "pj_inconnu"}) == "" {
		t.Fatal("impression inconnue")
	}

	// Ordre : trois éléments, le dernier passe en tête.
	var qs []string
	for i := 0; i < 3; i++ {
		j := r.validated()
		qs = append(qs, r.call("queue_print", map[string]any{"print_job_id": j, "assignments": asg})["entry"].(map[string]any)["id"].(string))
	}
	o := r.call("reorder_queue", map[string]any{"queue_ids": []any{qs[2], qs[0]}})["queue"].([]any)
	var order []string
	for _, e := range o {
		order = append(order, e.(map[string]any)["id"].(string))
	}
	if strings.Join(order, ",") != strings.Join([]string{qs[2], qs[0], qs[1]}, ",") {
		t.Fatalf("ordre : %v", order)
	}
	if r.callErr("reorder_queue", map[string]any{"queue_ids": []any{"qe_inconnu"}}) == "" {
		t.Fatal("identifiant inconnu")
	}
	if r.callErr("reorder_queue", map[string]any{"queue_ids": []any{qs[0], qs[0]}}) == "" {
		t.Fatal("doublon")
	}
	// La nouvelle tête (qs[2]) est préparée en premier.
	waitFor(t, 30*time.Second, "nouvelle tête prête", func() bool { return entryFor(r.queueList(false), qs[2])["state"] == "ready" })
	// qs[0] avait déjà été préparée (fichier envoyé) avant le changement d'ordre ; qs[1] n'a pas encore son tour.
	if st := entryFor(r.queueList(false), qs[1])["state"]; st != "waiting" {
		t.Fatalf("la dernière attend : %v", entryFor(r.queueList(false), qs[1]))
	}
	if r.jobState(jobOf(r, qs[1])) != "VALIDATED" {
		t.Fatal("la dernière ne doit pas être envoyée avant son tour")
	}
	if len(r.sim.StartCommands()) != 0 {
		t.Fatal("aucun lancement attendu")
	}
}

// TestQueueNeverStartsOrApproves : le paquet ne contient aucun appel de lancement, d'approbation ou d'arrêt.
func TestQueueNeverStartsOrApproves(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatal("sources introuvables")
	}
	forbidden := []string{"StartPrint(", "Approve(", "ExecuteStop(", "CancelPrint(", "plate_clear", "PlateClear", "approved", "ConsumeConfirmation", "useNonce", "Start(ctx"}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range forbidden {
			for i, line := range strings.Split(string(b), "\n") {
				if s := strings.TrimSpace(line); strings.HasPrefix(s, "//") || strings.HasPrefix(s, "*") {
					continue
				}
				if strings.Contains(line, w) {
					t.Fatalf("%s:%d : %q est interdit dans internal/filament (la file ne lance et n'approuve jamais)", f, i+1, w)
				}
			}
		}
	}
}
