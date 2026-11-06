package agentlink_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink/agentlinktest"
)

func stagingPart(e *agentlinktest.Env, agentID, sha string) string {
	return filepath.Join(e.Core.Cfg.DataDir, "agent-staging", agentID, sha+".part")
}

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Un .part complet (interruption entre le dernier octet et la finalisation) est finalisé dès la reprise.
func TestUploadFinalizesACompletePartFile(t *testing.T) {
	e := agentlinktest.New(t, agentlink.Config{})
	cl, pr := e.Pair("PC")
	hello(t, cl)
	id := newTaskWithOutputRight(t, e, cl)
	data := agentlinktest.FakeGcode3MF(5000, 7)
	sha := sum(data)
	part := stagingPart(e, pr.AgentID, sha)
	if err := os.MkdirAll(filepath.Dir(part), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(part, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cl.Upload(bg, id, writeTemp(t, "o.gcode.3mf", data), sha, int64(len(data)), agentlink.TargetArtifact, "o.gcode.3mf", nil); err != nil {
		t.Fatal(err)
	}
	if !e.Core.Blobs.Has(sha) || e.Core.Blobs.Verify(sha) != nil {
		t.Fatal(".part complet non finalisé")
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatal(".part non supprimé après finalisation")
	}
	if err := cl.Result(bg, id, agentlink.TaskResult{State: agentlink.StateSucceeded, Outputs: []agentlink.Artifact{{SHA256: sha, Size: int64(len(data)), Name: "o.gcode.3mf"}}}); err != nil {
		t.Fatalf("rapport avec la sortie finalisée : %v", err)
	}
}

// Le client émet le PUT final « bytes */total » quand tout est parti sans que le serveur ait finalisé.
func TestClientSendsFinalPUTWhenEverythingIsSent(t *testing.T) {
	data := []byte("0123456789")
	sha := sum(data)
	var mu sync.Mutex
	var puts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/uploads/begin"):
			fmt.Fprintf(w, `{"offset":%d,"size":%d,"complete":false}`, len(data), len(data)) // .part complet côté serveur, non finalisé
		case r.Method == "PUT":
			mu.Lock()
			puts = append(puts, r.Header.Get("Content-Range"))
			mu.Unlock()
			fmt.Fprintf(w, `{"offset":%d,"size":%d,"complete":true}`, len(data), len(data))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cl := agentlink.NewClient(srv.URL, "x.y")
	if err := cl.Upload(bg, "atk_1", writeTemp(t, "f", data), sha, int64(len(data)), agentlink.TargetArtifact, "f.3mf", nil); err != nil {
		t.Fatal(err)
	}
	if len(puts) != 1 || puts[0] != fmt.Sprintf("bytes */%d", len(data)) {
		t.Fatalf("PUT final attendu, reçu %v", puts)
	}
	// Serveur qui ne finalise jamais : erreur, pas de boucle infinie.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"offset":%d,"size":%d,"complete":false}`, len(data), len(data))
	}))
	defer srv2.Close()
	done := make(chan error, 1)
	go func() {
		done <- agentlink.NewClient(srv2.URL, "x.y").Upload(bg, "atk_1", writeTemp(t, "f", data), sha, int64(len(data)), agentlink.TargetArtifact, "f.3mf", nil)
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("un serveur qui ne finalise pas doit produire une erreur")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("boucle infinie")
	}
}

// Contrôle du format selon le type de travail ; rien n'est publié dans le magasin en cas de refus.
func TestUploadFormatChecks(t *testing.T) {
	e := agentlinktest.New(t, agentlink.Config{})
	cl, pr := e.Pair("PC")
	hello(t, cl)
	id := newTaskWithOutputRight(t, e, cl) // travail « slice » : sorties .gcode.3mf / .3mf seulement
	upload := func(name string, data []byte) error {
		return cl.Upload(bg, id, writeTemp(t, "f", data), sum(data), int64(len(data)), agentlink.TargetArtifact, name, nil)
	}
	notZip := bytes.Repeat([]byte("PK pas une archive "), 50)
	if err := upload("o.gcode.3mf", notZip); code(err) != agentlink.CodeInvalid {
		t.Fatalf("contenu non ZIP : %v", err)
	}
	if e.Core.Blobs.Has(sum(notZip)) {
		t.Fatal("contenu invalide publié")
	}
	if _, err := os.Stat(stagingPart(e, pr.AgentID, sum(notZip))); !os.IsNotExist(err) {
		t.Fatal(".part du contenu invalide non supprimé")
	}
	// ZIP valide mais sans G-code déclaré .gcode.3mf.
	if err := upload("o.gcode.3mf", agentlinktest.FakeModel3MF()); code(err) != agentlink.CodeInvalid {
		t.Fatalf(".gcode.3mf sans G-code : %v", err)
	}
	// Extensions refusées pour ce type de travail, ou inconnues.
	for _, name := range []string{"o.stl", "o.step", "o.exe", "o.gcode", "o"} {
		if err := upload(name, []byte("solid x\nfacet\nendsolid")); err == nil {
			t.Errorf("%s accepté pour un travail slice", name)
		}
	}
	// Valide : .gcode.3mf avec G-code, et .3mf avec modèle.
	if err := upload("ok.gcode.3mf", agentlinktest.FakeGcode3MF(300, 9)); err != nil {
		t.Fatalf(".gcode.3mf valide : %v", err)
	}
	if err := upload("ok.3mf", agentlinktest.FakeGcode3MF(50, 11)); err != nil {
		t.Fatalf(".3mf valide : %v", err)
	}
}

// Contrôles de contenu par format (import STL/OBJ/3MF/STEP) et tailles maximales par type.
func TestFormatChecksAndSizeLimits(t *testing.T) {
	stlBin := make([]byte, 84+50*2)
	stlBin[80] = 2 // 2 triangles
	cases := []struct {
		name string
		data []byte
		ok   bool
	}{
		{"a.stl", []byte("solid cube\n facet normal 0 0 1\n endfacet\nendsolid cube\n"), true},
		{"a.stl", stlBin, true},
		{"a.stl", append(stlBin, 1), false}, // taille incohérente avec le nombre de triangles
		{"a.stl", []byte("n'importe quoi"), false},
		{"a.obj", []byte("# cube\nv 0 0 0\nv 1 0 0\nf 1 2 2\n"), true},
		{"a.obj", []byte("MZ\x00\x00binaire"), false},
		{"a.obj", []byte("pas d'obj ici\n"), false},
		{"a.step", []byte("ISO-10303-21;\nHEADER;\nENDSEC;\n"), true},
		{"a.stp", []byte("  \nISO-10303-21;\n"), true},
		{"a.step", []byte("HEADER;"), false},
		{"a.3mf", agentlinktest.FakeModel3MF(), true},
		{"a.3mf", []byte("PK\x03\x04vide"), false},
		{"a.gcode.3mf", agentlinktest.FakeGcode3MF(100, 1), true},
	}
	for i, c := range cases {
		err := agentlink.CheckContentForTest(writeTemp(t, "f", c.data), c.name)
		if (err == nil) != c.ok {
			t.Errorf("cas %d %s : ok=%v attendu %v (%v)", i, c.name, err == nil, c.ok, err)
		}
	}
	for ext, max := range map[string]int64{".stl": 512 << 20, ".obj": 512 << 20, ".step": 512 << 20, ".3mf": 1 << 30, ".gcode.3mf": 1 << 30} {
		if got := agentlink.MaxSizeFor(ext, 4<<30); got != max {
			t.Errorf("taille max %s = %d, attendu %d", ext, got, max)
		}
	}
	if agentlink.MaxSizeFor(".stl", 1000) != 1000 || agentlink.MaxSizeFor(".exe", 1<<30) != 0 {
		t.Error("plafond global ou extension inconnue")
	}
	// Taille déclarée trop grande pour le type : refusée avant tout transfert.
	e := agentlinktest.New(t, agentlink.Config{MaxUploadBytes: 100})
	cl, _ := e.Pair("PC")
	hello(t, cl)
	id := newTaskWithOutputRight(t, e, cl)
	data := agentlinktest.FakeGcode3MF(2000, 1)
	if err := cl.Upload(bg, id, writeTemp(t, "f", data), sum(data), int64(len(data)), agentlink.TargetArtifact, "o.gcode.3mf", nil); err == nil {
		t.Fatal("taille supérieure au plafond acceptée")
	}
}

// Plusieurs envois simultanés du même fichier : un seul écrivain à la fois, résultat intègre.
func TestConcurrentUploadsOfTheSameFileStayIntact(t *testing.T) {
	e := agentlinktest.New(t, agentlink.Config{MaxChunkBytes: 64 << 10})
	cl, _ := e.Pair("PC")
	hello(t, cl)
	id := newTaskWithOutputRight(t, e, cl)
	data := agentlinktest.FakeGcode3MF(400_000, 4)
	sha := sum(data)
	src := writeTemp(t, "o.gcode.3mf", data)
	cl.ChunkSize = 32 << 10
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- cl.Upload(bg, id, src, sha, int64(len(data)), agentlink.TargetArtifact, "o.gcode.3mf", nil)
		}()
	}
	wg.Wait()
	close(errs)
	ok := 0
	for err := range errs {
		if err == nil {
			ok++
		}
	}
	if ok == 0 {
		t.Fatal("aucun envoi n'a abouti")
	}
	if !e.Core.Blobs.Has(sha) || e.Core.Blobs.Verify(sha) != nil {
		t.Fatal("blob absent ou corrompu après des envois concurrents")
	}
}

// Purge des .part : abandonnés (âge) et ceux d'un travail qui se termine sans eux.
func TestStagingPurge(t *testing.T) {
	e := agentlinktest.New(t, agentlink.Config{})
	cl, pr := e.Pair("PC")
	hello(t, cl)
	old := stagingPart(e, pr.AgentID, strings.Repeat("a", 64))
	fresh := stagingPart(e, pr.AgentID, strings.Repeat("b", 64))
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(old, []byte("x"), 0o600)
	os.WriteFile(fresh, []byte("y"), 0o600)
	past := time.Now().Add(-48 * time.Hour)
	os.Chtimes(old, past, past)
	if n := e.Reg.PurgeStaging(24 * time.Hour); n != 1 {
		t.Fatalf("%d fichier(s) purgé(s)", n)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("ancien .part conservé")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("jeune .part supprimé")
	}
	// Un envoi partiel puis l'échec du travail : le .part est supprimé.
	id := newTaskWithOutputRight(t, e, cl)
	data := agentlinktest.FakeGcode3MF(50_000, 5)
	sha := sum(data)
	begin := agentlink.UploadBegin{TaskID: id, SHA256: sha, Size: int64(len(data)), Target: agentlink.TargetArtifact, Name: "o.gcode.3mf"}
	if _, err := e.Reg.BeginUpload(bg, mustAgent(t, e, pr.AgentID), begin); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Reg.PutChunk(bg, mustAgent(t, e, pr.AgentID), id, sha, 0, int64(len(data)), bytes.NewReader(data[:1000])); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stagingPart(e, pr.AgentID, sha)); err != nil {
		t.Fatalf("pas de .part partiel : %v", err)
	}
	if err := cl.Result(bg, id, agentlink.TaskResult{State: agentlink.StateFailed, Error: &agentlink.TaskError{Code: "x", Message: "échec"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stagingPart(e, pr.AgentID, sha)); !os.IsNotExist(err) {
		t.Fatal(".part d'un travail terminé non supprimé")
	}
}

func mustAgent(t *testing.T, e *agentlinktest.Env, id string) agentlink.Agent {
	t.Helper()
	a, err := e.Reg.GetAgent(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// Complete / CancelTask / Requeue : gardes d'état.
func TestStateGuards(t *testing.T) {
	e := agentlinktest.New(t, agentlink.Config{})
	cl, pr := e.Pair("PC")
	hello(t, cl)
	ag := mustAgent(t, e, pr.AgentID)
	ti, err := e.Reg.CreateTask(bg, agentlink.CreateTaskInput{Kind: agentlink.KindSlice})
	if err != nil {
		t.Fatal(err)
	}
	// Un travail encore « queued » (jamais remis à l'agent) ne peut pas recevoir de rapport.
	if _, err := e.Reg.Complete(bg, ag, ti.ID, agentlink.TaskResult{State: agentlink.StateSucceeded}); code(err) != agentlink.CodeConflict {
		t.Fatalf("rapport sur un travail non remis : %v", code(err))
	}
	// Annulation d'un travail en file : immédiate, idempotente.
	c1, err := e.Reg.CancelTask(bg, ti.ID)
	if err != nil || c1.State != agentlink.StateCancelled {
		t.Fatalf("annulation : %+v %v", c1, err)
	}
	if c2, err := e.Reg.CancelTask(bg, ti.ID); err != nil || c2.State != agentlink.StateCancelled {
		t.Fatalf("seconde annulation : %+v %v", c2, err)
	}
	// Un rapport tardif sur un travail annulé est refusé (pas de publication en double).
	if _, err := e.Reg.Complete(bg, ag, ti.ID, agentlink.TaskResult{State: agentlink.StateSucceeded}); code(err) != agentlink.CodeConflict {
		t.Fatalf("rapport tardif : %v", code(err))
	}
	// Travail remis puis terminé : ni annulable ni recréable.
	t2, _ := e.Reg.CreateTask(bg, agentlink.CreateTaskInput{Kind: agentlink.KindSlice})
	tk, _ := cl.NextTask(bg, time.Second)
	if tk == nil || tk.ID != t2.ID {
		t.Fatalf("remise : %+v", tk)
	}
	_ = cl.StartTask(bg, t2.ID)
	if err := cl.Result(bg, t2.ID, agentlink.TaskResult{State: agentlink.StateSucceeded}); err != nil {
		t.Fatal(err)
	}
	if c, err := e.Reg.CancelTask(bg, t2.ID); err != nil || c.State != agentlink.StateSucceeded {
		t.Fatalf("annulation d'un travail terminé : %+v %v", c, err)
	}
	if _, err := e.Reg.Requeue(bg, t2.ID); code(err) != agentlink.CodeConflict {
		t.Fatalf("recréation d'un travail réussi : %v", code(err))
	}
	// Recréation d'un travail annulé : l'ancien reste annulé, un nouveau est créé.
	nt, err := e.Reg.Requeue(bg, ti.ID)
	if err != nil || nt.ID == ti.ID || nt.State != agentlink.StateQueued {
		t.Fatalf("recréation : %+v %v", nt, err)
	}
}

// Épinglage TLS côté agent et avertissement http hors loopback.
func TestClientTLSPinningAndInsecureWarning(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"server_time":"2026-01-01T00:00:00Z"}`)
	}))
	defer srv.Close()
	sum256 := sha256.Sum256(srv.Certificate().Raw)
	good := hex.EncodeToString(sum256[:])
	ok, err := agentlink.NewClientWithOptions(srv.URL, "a.b", agentlink.ClientOptions{ServerFingerprint: strings.ToUpper(good)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ok.Heartbeat(bg, agentlink.HeartbeatRequest{}); err != nil {
		t.Fatalf("empreinte correcte : %v", err)
	}
	bad, _ := agentlink.NewClientWithOptions(srv.URL, "a.b", agentlink.ClientOptions{ServerFingerprint: strings.Repeat("0", 64)})
	if _, err := bad.Heartbeat(bg, agentlink.HeartbeatRequest{}); err == nil || !strings.Contains(err.Error(), "ne correspond pas") {
		t.Fatalf("mauvaise empreinte : %v", err)
	}
	// Sans empreinte, le certificat auto-signé est refusé (vérification classique).
	if _, err := agentlink.NewClient(srv.URL, "a.b").Heartbeat(bg, agentlink.HeartbeatRequest{}); err == nil {
		t.Fatal("certificat auto-signé accepté sans épinglage")
	}
	if _, err := agentlink.NewClientWithOptions(srv.URL, "", agentlink.ClientOptions{ServerFingerprint: "pas-hex"}); err == nil {
		t.Fatal("empreinte invalide acceptée")
	}
	for url, want := range map[string]bool{
		"http://127.0.0.1:7878": false, "http://localhost:7878": false, "http://[::1]:7878": false,
		"http://192.168.1.20:7878": true, "http://bambu.lan:7878": true, "https://bambu.lan:7878": false,
	} {
		if got := agentlink.InsecureTransportWarning(url) != ""; got != want {
			t.Errorf("avertissement pour %s = %v, attendu %v", url, got, want)
		}
	}
}

// Le client a des délais (pas de requête qui pend indéfiniment).
func TestClientTimeouts(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block }))
	defer srv.Close()
	defer close(block)
	cl := agentlink.NewClient(srv.URL, "a.b")
	cl.JSONTimeout = 300 * time.Millisecond
	t0 := time.Now()
	if _, err := cl.Heartbeat(bg, agentlink.HeartbeatRequest{}); err == nil || time.Since(t0) > 5*time.Second {
		t.Fatalf("heartbeat sans délai : %v après %v", err, time.Since(t0))
	}
	tr, ok := cl.HTTP.Transport.(*http.Transport)
	if !ok || tr.TLSHandshakeTimeout == 0 || tr.ResponseHeaderTimeout == 0 || tr.DialContext == nil {
		t.Fatalf("transport sans délais : %+v", cl.HTTP.Transport)
	}
}
