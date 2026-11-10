package printing_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
)

// Modèle de menace : le modèle n'a que les outils MCP ; aucun ne doit pouvoir produire l'accord de
// lancement SANS le code de lancement personnel de l'humain. On énumère TOUS les outils, on vérifie leur schéma
// d'entrée (aucun champ d'approbation, de jeton ou de nonce, hors les deux outils à code, réservés à l'application),
// puis on les appelle tous avec des arguments d'approbation forgés : la confirmation reste « non approuvée » et
// start_print refuse. Ensuite : sans code, aucun outil n'approuve ; trois mauvais codes verrouillent TOUT (même le
// bon code est refusé) ; seul un déverrouillage humain puis le bon code approuvent ; le code n'apparaît dans aucune
// réponse, aucune erreur, aucun journal, aucun audit.
func TestNoMCPToolCanApproveAConfirmation(t *testing.T) {
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	r.sim = startSim(t)
	r.seedPrinterModel()
	r.c.Printing.SetValidator(fakeValidator("0.4"))
	ap := r.addPrinter(nil)
	r.printerID = str(ap, "printer", "id")
	r.humanSetsAccessCode(str(ap, "setup_url"))
	art := r.fakeArtifact()
	job, _ := r.uploadAndAsk(art, "cle-depot-noapprove")
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": job})
	confID := str(cf, "confirmation_id")
	// L'humain a défini son code de lancement (page /setup/launch-code) ; le modèle ne le connaît pas.
	const humanCode = "579204"
	r.humanSetLaunchCode(humanCode)
	humanauth.LaunchCodesFor(r.c.Store).SetMinDelay(0)

	tools, err := r.cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{"plate_clear", "approved", "approve", "nonce", "token", "confirm_token", "human"}
	called := 0
	// N4 : les outils d'accessoires ne portent aucun nom de lancement, d'arrêt, de reprise ni d'approbation.
	accessory := map[string]bool{"set_printer_light": false, "set_printer_fan": false, "set_print_speed_level": false}
	for _, tl := range tools.Tools {
		if _, ok := accessory[tl.Name]; ok {
			accessory[tl.Name] = true
			if forbiddenToolName.MatchString(tl.Name) {
				t.Fatalf("outil d'accessoire au nom interdit : %s", tl.Name)
			}
		}
		low := strings.ToLower(tl.Name)
		codeTool := tl.Name == "approve_print_with_code" || tl.Name == "approve_stop_with_code" || tl.Name == "validate_action_with_code"
		if (strings.Contains(low, "approve") && !codeTool) || strings.Contains(low, "from_app") || low == "confirm_print" {
			t.Fatalf("outil d'approbation interdit : %s", tl.Name)
		}
		schema, _ := json.Marshal(tl.InputSchema)
		if codeTool {
			// Seuls outils d'approbation : réservés à l'application, exigent le code, ne portent aucun jeton ni nonce.
			meta, _ := json.Marshal(tl.Meta)
			if !strings.Contains(string(meta), `"visibility":["app"]`) || !strings.Contains(string(schema), `"code"`) {
				t.Fatalf("%s : visibilité app-only et champ code obligatoires : %s %s", tl.Name, meta, schema)
			}
		}
		for _, f := range forbidden {
			if f == "token" && tl.Name == "apply_file_operations" {
				continue // jeton d'aperçu d'une opération sur FICHIERS (catalogue) : sans rapport avec le lancement d'une impression
			}
			if f == "plate_clear" && tl.Name == "approve_print_with_code" {
				continue // attestation « plateau vide » : exigée EN PLUS du code
			}
			if strings.Contains(string(schema), `"`+f+`"`) {
				t.Fatalf("l'outil %s expose un champ d'approbation %q dans son schéma d'entrée", tl.Name, f)
			}
		}
		if tl.Name == "start_print" || tl.Name == "cancel_print" || tl.Name == "execute_headless_slice" || tl.Name == "create_parametric_model" ||
			tl.Name == "upload_print_artifact" || strings.HasPrefix(tl.Name, "import") {
			continue // appels à effet : couverts par leurs propres tests ; ils n'acceptent de toute façon aucun champ d'approbation
		}
		// Appel avec des arguments d'approbation forgés : l'outil échoue ou ignore ; peu importe, rien ne doit être approuvé.
		_, _, _ = r.raw(tl.Name, map[string]any{"confirmation_id": confID, "print_job_id": job, "plate_clear": true,
			"approved": true, "approve": true, "nonce": "x", "token": "x"})
		called++
	}
	if called < 20 {
		t.Fatalf("seulement %d outils exercés", called)
	}
	for n, ok := range accessory {
		if !ok {
			t.Fatalf("outil d'accessoire %s absent", n)
		}
	}
	// Les outils d'accessoires ont reçu des arguments forgés (ci-dessus) : aucune commande de lancement, d'arrêt, de
	// pause ni de reprise n'a pu partir ; seules des commandes d'accessoires peuvent avoir été publiées.
	for _, c := range r.sim.Commands() {
		if c != "ledctrl" && c != "gcode_line" && c != "print_speed" {
			t.Fatalf("commande inattendue reçue par l'imprimante pendant l'énumération des outils : %q", c)
		}
	}
	v := r.call("get_print_confirmation_view", map[string]any{"confirmation_id": confID})
	if v["approved"] != false {
		t.Fatalf("un outil MCP a approuvé la confirmation : %v", v)
	}
	if r.callErr("start_print", map[string]any{"print_job_id": job, "confirmation_id": confID, "idempotency_key": "cle-start-noapprove"}) == "" || len(r.sim.StartCommands()) != 0 {
		t.Fatal("start_print a abouti sans approbation humaine")
	}

	// ---- le code de lancement : seul moyen d'approuver par un outil ----
	approve := func(code string) map[string]any {
		return r.call("approve_print_with_code", map[string]any{"confirmation_id": confID, "code": code, "plate_clear": true})
	}
	notApproved := func(when string) {
		t.Helper()
		if v := r.call("get_print_confirmation_view", map[string]any{"confirmation_id": confID}); v["approved"] != false {
			t.Fatalf("%s : la confirmation est approuvée : %v", when, v)
		}
		if r.callErr("start_print", map[string]any{"print_job_id": job, "confirmation_id": confID, "idempotency_key": "cle-start-noapprove-2"}) == "" || len(r.sim.StartCommands()) != 0 {
			t.Fatalf("%s : start_print a abouti", when)
		}
	}
	// 1. sans le bon code (absent, mal formé, faux) : aucun outil n'approuve.
	_, _, _ = r.raw("approve_print_with_code", map[string]any{"confirmation_id": confID, "plate_clear": true})
	_, _, _ = r.raw("approve_print_with_code", map[string]any{"confirmation_id": confID, "code": "", "plate_clear": true})
	_, _, _ = r.raw("approve_print_with_code", map[string]any{"confirmation_id": confID, "code": "abcdef", "plate_clear": true})
	_, _, _ = r.raw("approve_print_with_code", map[string]any{"confirmation_id": confID, "code": humanCode, "plate_clear": false})
	_, _, _ = r.raw("approve_stop_with_code", map[string]any{"stop_id": confID, "code": humanCode})
	notApproved("sans code")
	// 2. trois mauvais codes -> verrouillage global.
	for i, bad := range []string{"135791", "246801", "975310"} {
		if m := approve(bad); m["approved"] != false || str(m, "problem") != "bad_code" || (i == 2) != (m["locked"] == true) {
			t.Fatalf("mauvais code %d : %v", i+1, m)
		}
	}
	// 3. verrouillé : même le BON code n'approuve rien.
	if m := approve(humanCode); m["approved"] != false || str(m, "problem") != "locked" {
		t.Fatalf("bon code sous verrou : %v", m)
	}
	notApproved("sous verrou")
	// 4. déverrouillage par la page humaine (aucun outil MCP ne le fait), puis le bon code approuve.
	_, _, nonce := r.humanGet(r.web.URL + "/setup/launch-code")
	if st, body := r.humanPost(r.web.URL+"/setup/launch-code", url.Values{"nonce": {nonce}, "action": {"unlock"}}); st != 200 || !strings.Contains(body, "Verrou levé") {
		t.Fatalf("déverrouillage humain : %d %s", st, body)
	}
	if m := approve(humanCode); m["approved"] != true {
		t.Fatalf("bon code après déverrouillage : %v", m)
	}
	if v := r.call("get_print_confirmation_view", map[string]any{"confirmation_id": confID}); v["approved"] != true {
		t.Fatalf("avec le bon code la confirmation doit être approuvée : %v", v)
	}
	if s := r.call("start_print", map[string]any{"print_job_id": job, "confirmation_id": confID, "idempotency_key": "cle-start-noapprove-3"}); str(s, "outcome") != "printing" || len(r.sim.StartCommands()) != 1 {
		t.Fatalf("lancement après approbation par code : %v", s)
	}
	// 5. le code n'apparaît nulle part (réponses, erreurs, journaux Debug du serveur, audit, fichiers).
	r.assertNoLeak(humanCode, "135791", "246801", "975310")
}
