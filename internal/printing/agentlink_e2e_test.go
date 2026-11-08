package printing_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentv2"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer/sim"
)

// TestAgentLinkEndToEndThroughBambud : la chaîne COMPLÈTE de bambud (outils MCP, confirmation humaine par navigateur,
// registre d'agents réel, garde HTTP réelle) avec une imprimante en link=agent : bambud ↔ agent local ↔ simulateur.
// Le serveur n'a jamais le code d'accès ni le certificat de l'imprimante.
func TestAgentLinkEndToEndThroughBambud(t *testing.T) {
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	r.sim = startSim(t)
	cam, err := r.sim.StartCamera(sim.CameraConfig{FrameEvery: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cam.Close() })
	r.seedPrinterModel()
	r.c.Printing.SetValidator(fakeValidator("0.4"))
	ap := r.addPrinter(nil)
	r.printerID = str(ap, "printer", "id")
	if ap["printer"].(map[string]any)["link"] != nil {
		t.Fatalf("une imprimante neuve est en transport direct (champ link absent) : %v", ap["printer"])
	}

	// ---- appairage réel : jeton à usage unique du registre existant, protocole v2 ----
	ctx := context.Background()
	tok, _, err := agentlink.CreatePairing(ctx, r.c.Store, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	anon, _ := agentv2.NewClient(r.web.URL, "", "")
	_, serverKeyID, _ := r.c.AgentV2.ServerKey()
	cfg, err := agentv2.PairAgent(ctx, anon, r.web.URL, "", tok, "pi-atelier", "pi", r.printerID, "test", serverKeyID)
	if err != nil {
		t.Fatalf("appairage v2 : %v", err)
	}
	if _, err := anon.Pair(ctx, agentv2.PairRequest{Token: tok, Name: "second"}); err == nil {
		t.Fatal("jeton d'appairage réutilisable")
	}
	pub, _ := cfg.ServerPublicKey()
	cl, _ := agentv2.NewClient(r.web.URL, cfg.Secret, "")
	host, mport := splitAddr(r.sim.MQTTAddr())
	_, fport := splitAddr(r.sim.FTPAddr())
	quietLog := slog.New(slog.NewTextHandler(io.Discard, nil))
	newDevice, snapshot, err := agentv2.BambuDevice{PrinterID: r.printerID, Host: host, Serial: r.sim.Serial(), MQTTPort: mport, FTPPort: fport, CameraPort: cam.Port(),
		AccessCode: simAccessCode, PinSHA256: r.sim.Fingerprint(), Logger: quietLog,
		Mod: func(c *printer.BambuConfig) { c.AllowStartFromFinish = true; c.ReconnectDelay = 100 * time.Millisecond }}.Factory()
	if err != nil {
		t.Fatal(err)
	}
	runner, err := agentv2.NewRunner(agentv2.RunnerConfig{Client: cl, AgentID: cfg.AgentID, PrinterID: r.printerID, ServerKey: pub, NewDevice: newDevice,
		Snapshot: snapshot, StateDir: t.TempDir(), TelemetryEvery: 100 * time.Millisecond, PollWait: 1, Log: quietLog})
	if err != nil {
		t.Fatal(err)
	}
	rctx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = runner.Run(rctx) }()
	t.Cleanup(func() { stop(); <-done; _ = runner.Close() })

	// ---- bascule de l'imprimante en link=agent (agent inconnu refusé) ----
	if st, _, _ := r.httpJSON("PUT", "/api/printers/"+r.printerID+"/link", `{"link":"agent","agent_id":"agt_inconnu"}`); st != 400 {
		t.Fatalf("agent inconnu accepté : %d", st)
	}
	if st, _, _ := r.httpJSON("PUT", "/api/printers/"+r.printerID+"/link", `{"link":"nimporte"}`); st != 400 {
		t.Fatalf("link invalide accepté : %d", st)
	}
	st, view, _ := r.httpJSON("PUT", "/api/printers/"+r.printerID+"/link", fmt.Sprintf(`{"link":"agent","agent_id":%q}`, cfg.AgentID))
	if st != 200 || view["link"] != "agent" || view["agent_id"] != cfg.AgentID || view["access_code_set"] != false {
		t.Fatalf("bascule : %d %v", st, view)
	}

	// ---- état relayé : aucun code d'accès, aucune empreinte épinglée côté serveur ----
	ps := r.waitPrinterState(r.printerID, "idle")
	if ps["connected"] != true || ps["stale"] != false {
		t.Fatalf("état relayé : %v", ps)
	}
	if r.c.Printing.Secrets().HasAccessCode(r.printerID) {
		t.Fatal("le serveur ne doit jamais avoir le code d'accès en mode agent")
	}

	// ---- caméra relayée par l'agent (instantané) ----
	snap, _, isErr := r.raw("get_camera_snapshot", map[string]any{"printer_id": r.printerID})
	if isErr {
		t.Fatalf("instantané via l'agent : %v", snap)
	}

	// ---- dépôt par l'agent, confirmation humaine, lancement signé ----
	art := r.fakeArtifact()
	jobID, up := r.uploadAndAsk(art, "cle-depot-agent-e2e")
	remote := str(up, "job", "remote", "name")
	if remote == "" || !exists(r, remote) {
		t.Fatalf("fichier absent de l'imprimante : %q", remote)
	}
	if n := len(r.sim.StartCommands()); n != 0 {
		t.Fatalf("%d commande(s) de lancement après un simple dépôt", n)
	}
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": jobID})
	if n := len(r.sim.StartCommands()); n != 0 {
		t.Fatalf("lancement avant l'approbation humaine : %d", n)
	}
	if status, body := r.humanConfirm(str(cf, "confirm_url"), true); status != 200 || !strings.Contains(body, "Impression lancée") {
		t.Fatalf("approbation : %d %s", status, body)
	}
	if n := len(r.sim.StartCommands()); n != 1 {
		t.Fatalf("l'approbation humaine devait lancer UNE impression : %d", n)
	}
	if got := r.waitState(jobID, 30*time.Second, "COMPLETED", "FAILED"); got != "COMPLETED" {
		t.Fatalf("fin d'impression : %s", got)
	}
	if n := len(r.sim.StartCommands()); n != 1 {
		t.Fatalf("%d commande(s) de lancement au total", n)
	}

	// ---- audit : UNE autorisation signée, résultat « started » ; rien en clair sur le serveur ----
	var n int
	var result string
	if err := r.c.Store.DB().QueryRow(`SELECT COUNT(*), COALESCE(MAX(result),'') FROM agent_v2_authz WHERE printer_id=?`, r.printerID).Scan(&n, &result); err != nil || n != 1 || result != "started" {
		t.Fatalf("journal des autorisations : %d %q %v", n, result, err)
	}
	_ = filepath.Walk(r.dataDir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && !strings.HasSuffix(p, ".db") && !strings.HasSuffix(p, "-wal") && !strings.HasSuffix(p, "-shm") {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), simAccessCode) {
				t.Errorf("code d'accès en clair sur le serveur : %s", p)
			}
		}
		return nil
	})
	r.assertNoLeak(simAccessCode)

	// ---- retour en direct : le champ disparaît (document identique à celui d'une imprimante jamais bascule) ----
	if st, v, _ := r.httpJSON("PUT", "/api/printers/"+r.printerID+"/link", `{"link":"direct"}`); st != 200 || v["link"] != nil || v["agent_id"] != nil {
		t.Fatalf("retour en direct : %d %v", st, v)
	}
}

// TestDirectLinkIsTheUntouchedDefault : sans réglage, une imprimante reste en transport direct, aucune route agent
// n'est sollicitée, aucune clé n'est créée et le serveur sait toujours s'en passer.
func TestDirectLinkIsTheUntouchedDefault(t *testing.T) {
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	r.sim = startSim(t)
	r.seedPrinterModel()
	r.c.Printing.SetValidator(fakeValidator("0.4"))
	ap := r.addPrinter(nil)
	r.printerID = str(ap, "printer", "id")
	r.humanSetsAccessCode(str(ap, "setup_url"))
	r.waitPrinterState(r.printerID, "idle")
	rec, err := r.c.Printing.GetPrinterRecord(context.Background(), r.printerID)
	if err != nil || rec.Link != "" || rec.AgentID != "" {
		t.Fatalf("enregistrement : %+v %v", rec, err)
	}
	if _, err := os.Stat(filepath.Join(r.dataDir, "secrets", "agent-v2-ed25519.key")); err == nil {
		t.Fatal("une clé de signature a été créée alors qu'aucun agent n'est utilisé")
	}
	if _, err := os.Stat(filepath.Join(r.dataDir, "agent-v2")); err == nil {
		t.Fatal("un dossier agent-v2 a été créé alors qu'aucun agent n'est utilisé")
	}
	jobID, _ := r.uploadAndAsk(r.fakeArtifact(), "cle-depot-direct-defaut")
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": jobID})
	if st, body := r.humanConfirm(str(cf, "confirm_url"), true); st != 200 || !strings.Contains(body, "Impression lancée") {
		t.Fatalf("approbation : %d %s", st, body)
	}
	if got := r.waitState(jobID, 30*time.Second, "COMPLETED", "FAILED"); got != "COMPLETED" {
		t.Fatalf("fin d'impression : %s", got)
	}
	if n := len(r.sim.StartCommands()); n != 1 {
		t.Fatalf("commandes : %d", n)
	}
	var n int
	if err := r.c.Store.DB().QueryRow(`SELECT COUNT(*) FROM agent_v2_authz`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("autorisations émises en mode direct : %d %v", n, err)
	}
}
