package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/notify"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/upgrade"
)

func TestSchemaVersionCommand(t *testing.T) {
	var out bytes.Buffer
	if rc := schemaVersionMain(&out); rc != 0 {
		t.Fatal(rc)
	}
	n, err := strconv.Atoi(strings.TrimSpace(out.String()))
	if err != nil || n != store.LatestSchemaVersion() || n < 270 {
		t.Fatalf("%q %v", out.String(), err)
	}
}

func TestUpgradeProbeCommand(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "bambu.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutDoc(context.Background(), "t", "print_jobs", "pj1", printing.Job{ID: "pj1", State: "PRINTING"}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	var out, errb bytes.Buffer
	if rc := upgradeProbeMain([]string{"--data-dir", dir}, &out, &errb); rc != 0 {
		t.Fatalf("rc=%d %s", rc, errb.String())
	}
	var p upgrade.Probe
	if err := json.Unmarshal(out.Bytes(), &p); err != nil || !p.Busy || p.SchemaVersion != store.LatestSchemaVersion() {
		t.Fatalf("%s %v", out.String(), err)
	}
	if rc := upgradeProbeMain([]string{"--data-dir", t.TempDir()}, &out, &errb); rc == 0 {
		t.Fatal("dossier sans base : erreur attendue")
	}
}

// Notification de mise à jour : même système que les alertes d'impression (canal webhook signé), évènement system_update,
// et la base est ouverte SANS migration.
func TestUpgradeNotifyUsesExistingChannelsWithoutMigrating(t *testing.T) {
	t.Setenv("BAMBU_NOTIFY_ALLOW_PRIVATE", "1") // le webhook de test est en loopback
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
	}))
	defer srv.Close()

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "bambu.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	vault, err := printing.OpenFileSecretStore(filepath.Join(dir, "secrets"), nil)
	if err != nil {
		t.Fatal(err)
	}
	nv, err := notify.New(notify.Deps{Store: st, Vault: vault, BaseURL: func() string { return "http://127.0.0.1:7878" }})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := nv.UpsertChannel(ctx, notify.ChannelInput{Name: "maj", Kind: notify.KindWebhook, Events: []string{notify.EvSystemUpdate}}); err != nil {
		t.Fatal(err)
	}
	if _, err := nv.UpsertChannel(ctx, notify.ChannelInput{Name: "autre", Kind: notify.KindWebhook, Events: []string{notify.EvPrintFailed}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(notify.EnvName("maj", "url"), srv.URL+"/maj")
	t.Setenv(notify.EnvName("maj", "hmac_key"), "cle-de-test-hmac")
	t.Setenv(notify.EnvName("autre", "url"), srv.URL+"/autre")
	t.Setenv(notify.EnvName("autre", "hmac_key"), "cle-de-test-hmac")
	// Simule un binaire PLUS ANCIEN que la base ou une base en retard : la dernière migration est « oubliée ». Une
	// ouverture avec migration échouerait (table déjà existante) ; sans migration, la notification doit partir.
	if _, err := st.DB().Exec(`DELETE FROM schema_version WHERE version=(SELECT MAX(version) FROM schema_version)`); err != nil {
		t.Fatal(err)
	}
	st.Close()

	var out, errb bytes.Buffer
	rc := upgradeNotifyMain([]string{"--data-dir", dir, "--kind", "rolled_back", "--title", "Mise à jour échouée", "--message", "retour à l'ancienne version"}, &out, &errb)
	if rc != 0 || !strings.Contains(out.String(), "1 canal(aux) sur 1") {
		t.Fatalf("rc=%d out=%q err=%q", rc, out.String(), errb.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 || !strings.Contains(bodies[0], notify.EvSystemUpdate) || !strings.Contains(bodies[0], "Mise à jour échouée") {
		t.Fatalf("corps reçus : %v", bodies)
	}
	if strings.Contains(bodies[0], "cle-de-test-hmac") {
		t.Fatal("le secret HMAC ne doit pas figurer dans la notification")
	}
	// Aucune migration n'a été appliquée par la commande.
	st2, err := store.Open(filepath.Join(dir, "bambu.db"), store.Options{NoMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if v, _ := st2.SchemaVersion(ctx); v >= store.LatestSchemaVersion() {
		t.Fatalf("la base a été migrée par upgrade-notify : %d", v)
	}
	if rc := upgradeNotifyMain([]string{"--data-dir", dir}, &out, &errb); rc != 2 {
		t.Fatalf("--title requis : rc=%d", rc)
	}
}

// Scénario « migration non réversible » au niveau des données, avec les vraies commandes backup / restore / verify :
// une base migrée par la nouvelle version est REFUSÉE par l'ancien binaire ; la restauration de la sauvegarde d'avant la
// mise à jour (faite par l'ancien binaire) lui rend une base qu'il sait ouvrir, et les écritures faites entre-temps sont
// conservées à part (.pre-restore-*).
func TestMigrationRollbackRestoresBackupAndOldBinaryCanOpen(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	st, err := store.Open(filepath.Join(data, "bambu.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutDoc(ctx, "t", "print_jobs", "avant", printing.Job{ID: "avant", State: "COMPLETED"}); err != nil {
		t.Fatal(err)
	}
	oldSchema := store.LatestSchemaVersion()
	st.Close()

	// 1. Sauvegarde d'avant mise à jour + vérification (avec .sha256), comme le fait l'outil de mise à jour.
	archive := filepath.Join(t.TempDir(), "preupdate-test.tar.gz")
	var out, errb bytes.Buffer
	if rc := backupMain([]string{"--data-dir", data, "--out", archive}, &out, &errb); rc != 0 {
		t.Fatalf("backup : %s", errb.String())
	}
	if _, err := os.Stat(archive + ".sha256"); err != nil {
		t.Fatal("empreinte .sha256 absente")
	}
	if rc := verifyMain([]string{"--archive", archive}, &out, &errb); rc != 0 {
		t.Fatalf("verify : %s", out.String()+errb.String())
	}

	// 2. La NOUVELLE version migre la base (schéma + une écriture), puis échoue.
	st, err = store.Open(filepath.Join(data, "bambu.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`INSERT INTO schema_version(version,name,applied_at) VALUES(?,?,?)`, oldSchema+10, "9999_futur", "2026-11-09T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := st.PutDoc(ctx, "t", "print_jobs", "apres", printing.Job{ID: "apres", State: "COMPLETED"}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// La sonde voit la migration (c'est elle qui décide de la restauration).
	p, err := upgrade.ProbeDB(ctx, data)
	if err != nil || p.SchemaVersion != oldSchema+10 {
		t.Fatalf("sonde : %+v %v", p, err)
	}
	// L'ANCIEN binaire refuse la base migrée : sans restauration, la marche arrière est impossible.
	if _, err := store.Open(filepath.Join(data, "bambu.db"), store.Options{}); err == nil || !strings.Contains(err.Error(), "plus récente") {
		t.Fatalf("l'ancien binaire aurait dû refuser la base migrée : %v", err)
	}

	// 3. Retour arrière : restauration forcée par l'ancien binaire, service arrêté.
	out.Reset()
	errb.Reset()
	if rc := restoreMain([]string{"--from", archive, "--data-dir", data, "--force"}, &out, &errb); rc != 0 {
		t.Fatalf("restore : %s", errb.String())
	}
	if rc := verifyMain([]string{"--data-dir", data, "--quick"}, &out, &errb); rc != 0 {
		t.Fatalf("verify après restauration : %s", out.String()+errb.String())
	}
	p, err = upgrade.ProbeDB(ctx, data)
	if err != nil || p.SchemaVersion != oldSchema {
		t.Fatalf("schéma après restauration : %+v %v", p, err)
	}
	st, err = store.Open(filepath.Join(data, "bambu.db"), store.Options{})
	if err != nil {
		t.Fatalf("l'ancien binaire doit rouvrir la base restaurée : %v", err)
	}
	var j printing.Job
	if err := st.GetDoc(ctx, "print_jobs", "avant", &j); err != nil {
		t.Fatalf("donnée d'avant la mise à jour perdue : %v", err)
	}
	if err := st.GetDoc(ctx, "print_jobs", "apres", &j); err == nil {
		t.Fatal("la base restaurée ne doit pas contenir l'écriture faite par la version échouée")
	}
	st.Close()
	// Les données de la version échouée ne sont pas détruites : conservées à part.
	aside, _ := filepath.Glob(filepath.Join(data, ".pre-restore-*", "bambu.db"))
	if len(aside) != 1 {
		t.Fatalf(".pre-restore-* attendu : %v", aside)
	}
}

func TestUpgradePinValidationAndStatus(t *testing.T) {
	conf := t.TempDir()
	t.Setenv("ATELIER_CONF_DIR", conf)
	var out, errb bytes.Buffer
	for _, bad := range []string{"main", "abc", "zzzzzzz", "--x=1", "../etc", strings.Repeat("a", 41)} {
		out.Reset()
		errb.Reset()
		if rc := upgradeMain([]string{"--conf-dir", conf, "--pin", bad}, &out, &errb); rc == 0 {
			t.Errorf("épingle %q acceptée", bad)
		}
	}
	if rc := upgradeMain([]string{"--conf-dir", conf, "--pin", "abcdef1"}, &out, &errb); rc != 0 {
		t.Fatalf("%s", errb.String())
	}
	if b, _ := os.ReadFile(filepath.Join(conf, upgrade.PinFileName)); strings.TrimSpace(string(b)) != "abcdef1" {
		t.Fatalf("fichier d'épingle : %q", b)
	}
	out.Reset()
	if rc := upgradeMain([]string{"--conf-dir", conf, "--status"}, &out, &errb); rc != 0 || !strings.Contains(out.String(), "Version épinglée : abcdef1") || !strings.Contains(out.String(), "ACTIVÉ") {
		t.Fatalf("status : %s", out.String())
	}
	if err := os.WriteFile(filepath.Join(conf, upgrade.DisabledFileName), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	upgradeMain([]string{"--conf-dir", conf, "--status"}, &out, &errb)
	if !strings.Contains(out.String(), "DÉSACTIVÉ") {
		t.Fatalf("status désactivé : %s", out.String())
	}
	if rc := upgradeMain([]string{"--conf-dir", conf, "--unpin"}, &out, &errb); rc != 0 {
		t.Fatal(errb.String())
	}
	if _, err := os.Stat(filepath.Join(conf, upgrade.PinFileName)); err == nil {
		t.Fatal("épingle non retirée")
	}
}
