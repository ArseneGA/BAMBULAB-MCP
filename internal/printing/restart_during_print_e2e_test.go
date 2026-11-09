package printing_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/upgrade"
)

// Mise à jour automatique pendant une impression : redémarrer bambud n'interrompt pas l'impression (le fichier est sur la carte
// SD de l'imprimante). Avec le simulateur : impression en cours -> arrêt puis réouverture du service sur le même dossier de
// données -> l'impression est toujours suivie en PRINTING (reconnexion MQTT, progression qui avance) et AUCUNE commande n'a été
// envoyée à l'imprimante par le redémarrage.
func TestRestartDuringPrintKeepsTrackingWithoutCommands(t *testing.T) {
	r, job := recRig(t, "cle-depot-restart-print", 150*time.Millisecond)
	r.launch(job)
	ctx := context.Background()

	// Avant : la politique de mise à jour laisse passer l'impression en cours (ATELIER_UPDATE_ALLOW_DURING_PRINT par défaut)...
	rep := r.c.Busy(ctx)
	var reasons []string
	for _, b := range rep.Reasons {
		reasons = append(reasons, b.Code+": "+b.Detail)
	}
	if !rep.Busy {
		t.Fatal("une impression en cours est signalée par le service (raison print_running)")
	}
	if st := upgrade.ApplyBusyPolicy(upgrade.BusyState{Busy: rep.Busy, Reasons: reasons}, true); st.Busy {
		t.Fatalf("une impression en cours ne doit pas bloquer la mise à jour : %+v", st)
	}
	if st := upgrade.ApplyBusyPolicy(upgrade.BusyState{Busy: rep.Busy, Reasons: reasons}, false); !st.Busy {
		t.Fatalf("avec l'option désactivée, l'impression bloque : %+v", st)
	}

	// ... et la progression est suivie sur l'imprimante.
	before, err := r.c.Printing.GetJob(ctx, job)
	if err != nil || before.Print == nil {
		t.Fatalf("suivi avant redémarrage : %v %+v", err, before)
	}
	cmdsBefore := r.sim.Commands()
	if len(cmdsBefore) != 1 || cmdsBefore[0] != "project_file" || len(r.sim.StartCommands()) != 1 {
		t.Fatalf("avant : un seul project_file attendu : %v", cmdsBefore)
	}
	dataDir, printerID := r.dataDir, r.printerID
	seenAt := before.Print.ObservedAt

	// ---- redémarrage du service (même dossier de données, même imprimante) ----
	r.stop()
	r2 := openRig(t, "Z:/absent/bambu-studio.exe", dataDir, false)
	r2.sim, r2.printerID = r.sim, printerID
	r2.c.Printing.SetWatchConfig(printing.WatchConfig{StallAfter: time.Second, IdleGrace: 300 * time.Millisecond, MismatchGrace: 500 * time.Millisecond,
		InconsistAfter: 500 * time.Millisecond, GoneAfter: time.Second, TrackTick: 100 * time.Millisecond, StopVerifyWait: 6 * time.Second, CommandWait: time.Second})

	// L'impression est toujours PRINTING (jamais START_UNKNOWN) et son suivi reprend SANS que personne le demande : l'observation
	// la plus récente avance (lecture du document seule, aucun appel d'outil).
	waitFor(t, 25*time.Second, "suivi repris après redémarrage", func() bool {
		j, err := r2.c.Printing.GetJob(ctx, job)
		if err != nil {
			return false
		}
		if j.State != "PRINTING" {
			t.Fatalf("après redémarrage l'impression doit rester PRINTING : %s (%s)", j.State, j.LastError)
		}
		return j.Print != nil && j.Print.ObservedAt.After(seenAt) && j.Print.MatchesFile
	})

	// La carte de suivi (get_print_status) voit l'imprimante joignable en train d'imprimer ce fichier.
	st := r2.status(job)
	if str(st, "job", "state") != "PRINTING" || str(st, "printer", "state") != "printing" || st["printer"].(map[string]any)["reachable"] != true {
		t.Fatalf("get_print_status après redémarrage : %v", st)
	}
	if got := str(r2.call("get_printer_status", map[string]any{"printer_id": printerID}), "state"); got != "printing" {
		t.Fatalf("get_printer_status : %s", got)
	}

	// AUCUNE commande envoyée à l'imprimante par le redémarrage : ni nouveau lancement, ni arrêt, ni pause, ni reprise.
	if cmds := r2.sim.Commands(); !reflect.DeepEqual(cmds, cmdsBefore) {
		t.Fatalf("le redémarrage a envoyé des commandes à l'imprimante : %v (avant : %v)", cmdsBefore, cmds)
	}
	if n := len(r2.sim.StartCommands()); n != 1 {
		t.Fatalf("%d lancements, 1 attendu", n)
	}
	if got := r2.sim.GcodeState(); got != "RUNNING" {
		t.Fatalf("l'imprimante doit toujours imprimer : %s", got)
	}
}
