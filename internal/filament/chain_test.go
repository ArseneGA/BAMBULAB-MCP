package filament_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// humanPrint : confirmation, clic de l'humain, lancement, attente de la fin.
func (r *rig) humanPrint(jobID, key string) {
	r.t.Helper()
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": jobID})
	if st, body := r.humanConfirm(str(cf, "confirm_url")); st != 200 {
		r.t.Fatalf("approbation humaine : %d %s", st, body)
	}
	s := r.call("start_print", map[string]any{"print_job_id": jobID, "confirmation_id": str(cf, "confirmation_id"), "idempotency_key": key})
	if str(s, "state") != "PRINTING" {
		r.t.Fatalf("lancement : %v", s)
	}
}

func (r *rig) jobState(id string) string {
	return str(r.call("get_print_status", map[string]any{"print_job_id": id}), "job", "state")
}

func (r *rig) waitJob(id string, want ...string) {
	r.t.Helper()
	var last string
	waitFor(r.t, 40*time.Second, "état de "+id+" parmi "+strings.Join(want, "/"), func() bool {
		last = r.jobState(id)
		for _, w := range want {
			if last == w {
				return true
			}
		}
		return false
	})
	_ = last
}

func (r *rig) spoolOf(id string) map[string]any {
	r.t.Helper()
	for _, s := range r.call("list_spools", map[string]any{"include_archived": true})["spools"].([]any) {
		if sm := s.(map[string]any); sm["id"] == id {
			return sm
		}
	}
	r.t.Fatalf("bobine %s introuvable", id)
	return nil
}

// TestAlertBeforeSendThenConsumption : une bobine qui risque de manquer est signalée AVANT l'envoi ;
// après l'impression, la masse du fichier est décomptée (idempotent) avec le coût de la matière.
func TestAlertBeforeSendThenConsumption(t *testing.T) {
	r := newRig(t)
	r.setup(0)
	r.c.Printing.SetValidator(fakeValidator(fil{"PLA", "#000000", 40}))
	sp := r.createSpool(map[string]any{"material": "PLA", "name": "Noir", "color_hex": "#000000", "net_weight_g": 1000, "remaining_g": 30, "price_per_kg": 20,
		"printer_id": r.printerID, "slot": "ams1:2"})
	asg := []any{assign(1, "ams1:2", "PLA Noir")}

	// Manque : 30 g restants pour 40 g.
	job1 := r.validated()
	chk := r.call("check_filament_for_print", map[string]any{"print_job_id": job1, "assignments": asg})
	slot := chk["slots"].([]any)[0].(map[string]any)
	if chk["verdict"] != "insufficient" || chk["ok"] != false || slot["shortfall_g"].(float64) != 10 || slot["needed_g"].(float64) != 40 {
		t.Fatalf("manque attendu : %v", chk)
	}
	// Alerte dans le rapport de validation, donc avant l'envoi, et dans les détails montrés à l'humain.
	up := r.call("upload_print_artifact", map[string]any{"print_job_id": job1, "printer_id": r.printerID, "idempotency_key": "cle-alerte-manque-1", "plate": 1, "assignments": asg})
	if !strings.Contains(fmt.Sprint(up["job"].(map[string]any)["validation"].(map[string]any)["warnings"]), "filament_short_1") {
		t.Fatalf("alerte absente du rapport : %v", up["job"].(map[string]any)["validation"])
	}
	if al := up["details"].(map[string]any)["alerts"]; al == nil || !strings.Contains(fmt.Sprint(al), "RISQUE DE MANQUE") {
		t.Fatalf("alerte absente des détails : %v", up["details"])
	}
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": job1})
	if _, body, _ := r.humanGet(str(cf, "confirm_url")); !strings.Contains(body, "Alerte matière") {
		t.Fatalf("alerte absente de la page de confirmation")
	}

	// Marginal puis suffisant : la marge est configurable.
	r.call("update_spool", map[string]any{"spool_id": sp["id"], "remaining_g": 47})
	if v := r.call("check_filament_for_print", map[string]any{"print_job_id": job1, "assignments": asg})["verdict"]; v != "marginal" {
		t.Fatalf("marginal attendu (47 g pour 40 g, marge 10 %% + 5 g = 49 g) : %v", v)
	}
	if v := r.call("check_filament_for_print", map[string]any{"print_job_id": job1, "assignments": asg, "margin_percent": 5, "margin_grams": 2})["verdict"]; v != "ok" {
		t.Fatalf("marge réduite : %v", v)
	}
	r.call("set_filament_margin", map[string]any{"margin_percent": 5, "margin_grams": 2})
	if v := r.call("check_filament_for_print", map[string]any{"print_job_id": job1, "assignments": asg})["verdict"]; v != "ok" {
		t.Fatalf("marge enregistrée : %v", v)
	}
	// Bobine de remplacement proposée quand il manque.
	alt := r.createSpool(map[string]any{"material": "PLA", "name": "Noir 2", "color_hex": "#050505", "remaining_g": 800})
	r.call("update_spool", map[string]any{"spool_id": sp["id"], "remaining_g": 10})
	c2 := r.call("check_filament_for_print", map[string]any{"print_job_id": job1, "assignments": asg})
	if alts := c2["slots"].([]any)[0].(map[string]any)["alternatives"]; alts == nil || !strings.Contains(fmt.Sprint(alts), alt["id"].(string)) {
		t.Fatalf("alternative : %v", c2)
	}

	// Impression réelle (simulateur) avec assez de matière : décompte à la fin.
	r.call("update_spool", map[string]any{"spool_id": sp["id"], "remaining_g": 100})
	job2 := r.validated()
	r.call("upload_print_artifact", map[string]any{"print_job_id": job2, "printer_id": r.printerID, "idempotency_key": "cle-impression-2", "plate": 1, "assignments": asg})
	r.humanPrint(job2, "cle-start-consommation")
	r.waitJob(job2, "COMPLETED")
	waitFor(t, 10*time.Second, "décompte de la consommation", func() bool { return r.spoolOf(sp["id"].(string))["remaining_g"].(float64) == 60 })
	got := r.spoolOf(sp["id"].(string))
	if got["remaining_source"] != "human" {
		t.Fatalf("la base saisie reste la saisie : %v", got)
	}
	u, err := r.c.Filament.UsageOf(context.Background(), job2)
	if err != nil || len(u) != 1 || u[0].Grams != 40 || u[0].Cost == nil || *u[0].Cost != 0.8 || u[0].Estimated {
		t.Fatalf("consommation : %v %v", u, err)
	}
	// Idempotent : un second décompte de la même impression ne retire rien de plus.
	j, _ := r.c.Printing.GetJob(context.Background(), job2)
	if err := r.c.Filament.RecordConsumption(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if g := r.spoolOf(sp["id"].(string))["remaining_g"].(float64); g != 60 {
		t.Fatalf("double décompte : %v", g)
	}
	// Prix/kg lisible par le calcul de coût (chantier C).
	if p, ok := r.c.Filament.PriceForSlot(context.Background(), r.printerID, "ams1:2"); !ok || p.PricePerKg != 20 || p.SpoolID != sp["id"] {
		t.Fatalf("prix : %v %v", p, ok)
	}
	// L'AMS simulé a retiré 3 % au bac : le restant saisi n'a pas bougé.
	if r.sim.AMSTrayRemain(1, 2) != 82 {
		t.Fatalf("restant AMS simulé : %d", r.sim.AMSTrayRemain(1, 2))
	}
	if r.spoolOf(sp["id"].(string))["remaining_g"].(float64) != 60 {
		t.Fatal("la lecture de l'AMS a écrasé la saisie")
	}
	if n := len(r.eventsOf("spool_low")); n != 1 { // 60 g restants sur 1000 g : sous les 10 %, l alerte est légitime
		t.Fatalf("alerte « bobine presque vide » attendue à 60 g sur 1000 g : %d", n)
	}
}

// TestPartialConsumptionOnInterruptedPrint : au prorata de l'avancement, signalée comme estimation.
func TestPartialConsumptionOnInterruptedPrint(t *testing.T) {
	r := newRig(t)
	r.setup(0)
	sp := r.createSpool(map[string]any{"material": "PLA", "remaining_g": 200, "price_per_kg": 10, "printer_id": r.printerID, "slot": "ams1:1"})
	job := printing.Job{ID: "pj_test_interrompu", State: contracts.PrintCancelled, PrinterID: r.printerID, Plate: 1,
		Assignments: []printing.FilamentAssignment{{FilamentSlot: 1, Tray: "ams1:1", MaterialID: "PLA"}},
		Summary:     &printing.ArtifactSummary{Plates: []slicer.PlateResult{{Index: 1, Filaments: []slicer.FilamentUse{{Slot: 1, Type: "PLA", Grams: 80}}}}},
		Print:       &printing.PrintProgress{SeenActive: true, Percent: 25}}
	if err := r.c.Filament.RecordConsumption(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if g := r.spoolOf(sp["id"].(string))["remaining_g"].(float64); g != 180 {
		t.Fatalf("25 %% de 80 g = 20 g : %v", g)
	}
	u, _ := r.c.Filament.UsageOf(context.Background(), job.ID)
	if len(u) != 1 || !u[0].Estimated || u[0].Grams != 20 {
		t.Fatalf("usage : %v", u)
	}
	// Aucune activité observée : rien n'est décompté.
	job2 := job
	job2.ID, job2.Print = "pj_test_jamais_demarre", &printing.PrintProgress{SeenActive: false, Percent: 0}
	_ = r.c.Filament.RecordConsumption(context.Background(), job2)
	if u, _ := r.c.Filament.UsageOf(context.Background(), job2.ID); len(u) != 0 {
		t.Fatalf("consommation inventée : %v", u)
	}
	// Seuil bas franchi : événement spool_low.
	job3 := job
	job3.ID, job3.State, job3.Print = "pj_test_gros", contracts.PrintCompleted, nil
	job3.Summary = &printing.ArtifactSummary{Plates: []slicer.PlateResult{{Index: 1, Filaments: []slicer.FilamentUse{{Slot: 1, Type: "PLA", Grams: 150}}}}}
	_ = r.c.Filament.RecordConsumption(context.Background(), job3)
	waitFor(t, 5*time.Second, "événement spool_low", func() bool { return len(r.eventsOf("spool_low")) == 1 })
}

// TestPlanColors : affectation couleur -> emplacement AMS, ams_mapping, pièces sans bobine, épingles.
func TestPlanColors(t *testing.T) {
	r := newRig(t)
	r.setup(0)
	r.c.Printing.SetValidator(fakeValidator(fil{"PLA", "#FFFFFF", 10}, fil{"PLA", "#1060D0", 8}, fil{"PETG", "#D01010", 5}))
	job := r.validated()

	// Couleurs du fichier : blanc -> ams1:1, bleu -> ams1:4, PETG rouge -> ams1:3.
	p := r.call("plan_colors", map[string]any{"print_job_id": job})
	want := map[float64]string{1: "ams1:1", 2: "ams1:4", 3: "ams1:3"}
	for _, s := range p["slots"].([]any) {
		sm := s.(map[string]any)
		if want[sm["filament_slot"].(float64)] != sm["tray"] || sm["quality"] != "exact" {
			t.Fatalf("slot : %v", sm)
		}
	}
	if fmt.Sprint(p["ams_mapping"]) != "[0 3 2 -1 -1]" || p["use_ams"] != true || p["complete"] != true {
		t.Fatalf("plan : %v", p)
	}
	// Les assignments du plan passent tels quels au dépôt (même contrat que upload_print_artifact).
	if len(p["assignments"].([]any)) != 3 {
		t.Fatalf("assignments : %v", p["assignments"])
	}

	// Pièce -> couleur choisie : le filament 1 devient noir, le 2 blanc (échange).
	p = r.call("plan_colors", map[string]any{"print_job_id": job, "colors": []any{
		map[string]any{"filament_slot": 1, "color_hex": "#000000", "part": "socle"},
		map[string]any{"filament_slot": 2, "color_hex": "#FFFFFF", "part": "capot"}}})
	got := map[float64]string{}
	for _, s := range p["slots"].([]any) {
		sm := s.(map[string]any)
		got[sm["filament_slot"].(float64)] = sm["tray"].(string)
		if sm["filament_slot"].(float64) == 1 && sm["parts"] != "socle" {
			t.Fatalf("pièce non reprise : %v", sm)
		}
	}
	if got[1] != "ams1:2" || got[2] != "ams1:1" || fmt.Sprint(p["ams_mapping"]) != "[1 0 2 -1 -1]" {
		t.Fatalf("échange noir/blanc : %v %v", got, p["ams_mapping"])
	}

	// Couleur sans bobine (vert) : filament non affecté, plan incomplet, indication claire.
	p = r.call("plan_colors", map[string]any{"print_job_id": job, "colors": []any{map[string]any{"filament_slot": 2, "color_hex": "#00AA00"}}})
	if p["complete"] != false || fmt.Sprint(p["unmatched_filaments"]) != "[2]" || !strings.Contains(fmt.Sprint(p["slots"]), "charger cette couleur") {
		t.Fatalf("couleur manquante : %v", p)
	}

	// Choix imposé par l'utilisateur : le filament 2 sur ams1:2 (noir) ; les autres s'adaptent autour.
	p = r.call("plan_colors", map[string]any{"print_job_id": job, "fixed": []any{assign(2, "ams1:2", "PLA noir")}})
	pinned := false
	for _, s := range p["slots"].([]any) {
		if sm := s.(map[string]any); sm["filament_slot"].(float64) == 2 {
			pinned = sm["tray"] == "ams1:2" && sm["pinned"] == true
		}
	}
	if !pinned {
		t.Fatalf("choix imposé : %v", p["slots"])
	}
	if r.callErr("plan_colors", map[string]any{"print_job_id": job, "colors": []any{map[string]any{"filament_slot": 1, "color_hex": "vert"}}}) == "" {
		t.Fatal("couleur invalide")
	}

	// Une bobine proposée qui risque de manquer fait échouer le plan (alerte), sans rien envoyer.
	r.createSpool(map[string]any{"material": "PLA", "color_hex": "#FFFFFF", "remaining_g": 3, "printer_id": r.printerID, "slot": "ams1:1"})
	p = r.call("plan_colors", map[string]any{"print_job_id": job})
	if p["complete"] != false || !strings.Contains(fmt.Sprint(p["warnings"]), "risque de manquer") {
		t.Fatalf("manque dans le plan : %v", p)
	}
	if n := len(r.sim.StartCommands()); n != 0 {
		t.Fatalf("plan_colors ne lance rien : %d", n)
	}
}

// TestConfirmationBindsMaterialAssignment : un changement d'affectation après la confirmation l'invalide.
func TestConfirmationBindsMaterialAssignment(t *testing.T) {
	r := newRig(t)
	r.setup(0)
	r.c.Printing.SetValidator(fakeValidator(fil{"PLA", "#FFFFFF", 6}, fil{"PLA", "#1060D0", 4}))
	job := r.validated()
	p := r.call("plan_colors", map[string]any{"print_job_id": job})
	r.call("upload_print_artifact", map[string]any{"print_job_id": job, "printer_id": r.printerID, "idempotency_key": "cle-affectation-1", "plate": 1, "assignments": p["assignments"]})
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": job})
	b1 := fmt.Sprint(cf["binding"].(map[string]any)["materials"])
	if !strings.Contains(b1, "ams1:1") || !strings.Contains(b1, "ams1:4") {
		t.Fatalf("la confirmation doit montrer l'affectation : %s", b1)
	}
	// L'approbation humaine lance l'impression : l'affectation change donc AVANT le clic (couleurs échangées).
	var doc map[string]any
	if err := r.c.Store.GetDoc(context.Background(), "print_jobs", job, &doc); err != nil {
		t.Fatal(err)
	}
	as := doc["assignments"].([]any)
	a0, a1 := as[0].(map[string]any), as[1].(map[string]any)
	a0["tray"], a1["tray"] = a1["tray"], a0["tray"]
	if err := r.c.Store.PutDoc(context.Background(), "test", "print_jobs", job, doc); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := r.humanGet(str(cf, "confirm_url")); st == 200 {
		t.Fatal("l'approbation d'une confirmation dont la liaison a changé doit être refusée")
	}
	txt := r.callErr("start_print", map[string]any{"print_job_id": job, "confirmation_id": str(cf, "confirmation_id"), "idempotency_key": "cle-start-affectation"})
	if txt == "" || len(r.sim.StartCommands()) != 0 {
		t.Fatalf("lancement après changement d'affectation : %q (%d commande(s))", txt, len(r.sim.StartCommands()))
	}
	// Une nouvelle confirmation montre la NOUVELLE affectation (autre liaison).
	cf2 := r.call("request_print_confirmation", map[string]any{"print_job_id": job})
	if b2 := fmt.Sprint(cf2["binding"].(map[string]any)["materials"]); b2 == b1 || str(cf2, "confirmation_id") == str(cf, "confirmation_id") {
		t.Fatalf("liaison inchangée : %s", b2)
	}
}
