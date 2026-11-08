package printing_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/localtime"
)

// rigPrepared : service + simulateur + imprimante enregistrée, un artefact déposé (UPLOADED).
func rigPrepared(t *testing.T, key string) (*rig, string) {
	t.Helper()
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	r.sim = startSim(t)
	r.seedPrinterModel()
	r.c.Printing.SetValidator(fakeValidator("0.4"))
	ap := r.addPrinter(nil)
	r.printerID = str(ap, "printer", "id")
	r.humanSetsAccessCode(str(ap, "setup_url"))
	job, _ := r.uploadAndAsk(r.fakeArtifact(), key)
	return r, job
}

// La mise à niveau du plateau est activée par défaut : la commande project_file envoyée à l'imprimante porte les DEUX
// orthographes (bed_leveling et bed_levelling) à true, et la page /confirm l'affiche à l'humain.
func TestStartSendsBedLevelingByDefault(t *testing.T) {
	r, job := rigPrepared(t, "cle-depot-nivellement-defaut")
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": job})
	if b := cf["binding"].(map[string]any); b["bed_leveling"] != true {
		t.Fatalf("liaison : %v", b)
	}
	st, body, _ := r.humanGet(str(cf, "confirm_url"))
	if st != 200 || !strings.Contains(body, "Mise à niveau du plateau") || !strings.Contains(body, "oui (automatique au démarrage)") {
		t.Fatalf("la page doit afficher la mise à niveau : %d %s", st, body)
	}
	if st, b := r.humanConfirm(str(cf, "confirm_url"), true); st != 200 {
		t.Fatalf("approbation : %d %s", st, b)
	}
	cmds := r.sim.StartCommands()
	if len(cmds) != 1 {
		t.Fatalf("%d commande(s)", len(cmds))
	}
	p := cmds[0].Payload
	if p["bed_leveling"] != true || p["bed_levelling"] != true {
		t.Fatalf("mise à niveau absente de la commande : %v", p)
	}
	if p["flow_cali"] != true || p["vibration_cali"] != true || p["timelapse"] != false {
		t.Fatalf("les autres options doivent rester celles par défaut : %v", p)
	}
}

// bed_leveling:false fait partie de la liaison approuvée : affichée « non », envoyée telle quelle (les deux clés).
func TestBedLevelingOffIsBoundAndShown(t *testing.T) {
	r, job := rigPrepared(t, "cle-depot-nivellement-off")
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": job, "bed_leveling": false})
	if b := cf["binding"].(map[string]any); b["bed_leveling"] != false {
		t.Fatalf("liaison : %v", b)
	}
	_, body, _ := r.humanGet(str(cf, "confirm_url"))
	if !strings.Contains(body, "<strong>non</strong> (désactivée pour cette impression)") {
		t.Fatalf("la page doit dire que la mise à niveau est désactivée : %s", body)
	}
	if st, b := r.humanConfirm(str(cf, "confirm_url"), true); st != 200 {
		t.Fatalf("approbation : %d %s", st, b)
	}
	p := r.sim.StartCommands()[0].Payload
	if p["bed_leveling"] != false || p["bed_levelling"] != false {
		t.Fatalf("la commande doit porter ce que l'humain a vu : %v", p)
	}
}

// Les heures des pages humaines et des réponses MCP sont dans le fuseau configuré, pas en UTC.
func TestConfirmPageAndToolUseConfiguredTimezone(t *testing.T) {
	t.Setenv("BAMBU_TIMEZONE", "Pacific/Auckland") // UTC+13 en novembre : jamais confondu avec l'UTC
	localtime.Reload()
	t.Cleanup(func() { t.Setenv("BAMBU_TIMEZONE", ""); localtime.Reload() })
	r, job := rigPrepared(t, "cle-depot-fuseau")
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": job})
	exp, err := time.Parse(time.RFC3339, str(cf, "expires_at"))
	if err != nil {
		t.Fatal(err)
	}
	if want := localtime.Full(exp); str(cf, "expires_at_local") != want || !strings.Contains(want, "Pacific/Auckland") {
		t.Fatalf("expires_at_local = %q, attendu %q", str(cf, "expires_at_local"), want)
	}
	_, body, _ := r.humanGet(str(cf, "confirm_url"))
	if want := localtime.Page(exp); !strings.Contains(body, want) {
		t.Fatalf("la page doit afficher %q (fuseau local) : %s", want, body)
	}
	if utc := exp.UTC().Format("15:04:05 (02/01/2006)"); strings.Contains(body, utc) {
		t.Fatalf("la page affiche encore l'heure UTC %q", utc)
	}
}
