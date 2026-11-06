package agentlink_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink/agentlinktest"
)

var bg = context.Background()

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func code(err error) string {
	var ae *agentlink.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return fmt.Sprint(err)
}

func hello(t *testing.T, cl *agentlink.Client) agentlink.HelloResponse {
	t.Helper()
	r, err := cl.Hello(bg, agentlink.HelloRequest{AgentVersion: "test", Capabilities: agentlinktest.AllCaps()})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPairingOneShotAndSecretNotStored(t *testing.T) {
	e := agentlinktest.New(t, agentlink.Config{})
	tok, _, err := e.Reg.NewPairing(bg)
	if err != nil {
		t.Fatal(err)
	}
	anon := agentlink.NewClient(e.Server.URL, "")
	resp, err := anon.Pair(bg, agentlink.PairRequest{Token: tok, Name: "PC-Atelier", AgentVersion: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.AgentID == "" || !strings.HasPrefix(resp.Secret, resp.AgentID+".") || resp.Protocol != agentlink.ProtocolVersion {
		t.Fatalf("réponse d'appairage : %+v", resp)
	}
	// Le jeton est à usage unique.
	if _, err := anon.Pair(bg, agentlink.PairRequest{Token: tok, Name: "bis"}); code(err) != agentlink.CodeBadPairing {
		t.Fatalf("second usage du jeton : %v", err)
	}
	// Jeton inconnu.
	if _, err := anon.Pair(bg, agentlink.PairRequest{Token: "pair-xxxx", Name: "x"}); code(err) != agentlink.CodeBadPairing {
		t.Fatalf("jeton inconnu : %v", err)
	}
	// Le secret n'est stocké nulle part en clair : ni dans les agents, ni dans les jetons.
	secret := strings.SplitN(resp.Secret, ".", 2)[1]
	for _, q := range []string{`SELECT secret_hash FROM agents`, `SELECT token_hash FROM agent_pairings`} {
		rows, _ := e.Core.Store.DB().Query(q)
		for rows.Next() {
			var s string
			rows.Scan(&s)
			if strings.Contains(s, secret) || strings.Contains(s, tok) {
				t.Fatalf("secret ou jeton en clair dans la base (%s)", q)
			}
		}
		rows.Close()
	}
	// Authentification : bon secret accepté, mauvais refusé.
	cl := agentlink.NewClient(e.Server.URL, resp.Secret)
	hello(t, cl)
	bad := agentlink.NewClient(e.Server.URL, resp.AgentID+".deadbeef")
	if _, err := bad.Heartbeat(bg, agentlink.HeartbeatRequest{}); code(err) != agentlink.CodeUnauthorized {
		t.Fatalf("mauvais secret : %v", err)
	}
	if _, err := agentlink.NewClient(e.Server.URL, "").Heartbeat(bg, agentlink.HeartbeatRequest{}); code(err) != agentlink.CodeUnauthorized {
		t.Fatalf("sans secret : %v", err)
	}
	// Révocation.
	if err := e.Reg.Revoke(bg, resp.AgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.Heartbeat(bg, agentlink.HeartbeatRequest{}); code(err) != agentlink.CodeUnauthorized {
		t.Fatalf("agent révoqué : %v", err)
	}
}

func TestPairingExpiryAndProtocolVersion(t *testing.T) {
	e := agentlinktest.New(t, agentlink.Config{PairingTTL: time.Minute})
	tok, _, _ := e.Reg.NewPairing(bg)
	e.Clock.Advance(2 * time.Minute)
	anon := agentlink.NewClient(e.Server.URL, "")
	if _, err := anon.Pair(bg, agentlink.PairRequest{Token: tok, Name: "x"}); code(err) != agentlink.CodeBadPairing {
		t.Fatalf("jeton expiré : %v", err)
	}
	tok2, _, _ := e.Reg.NewPairing(bg)
	// Version de protocole différente : refusée (appel direct, le client force la version courante).
	body, _ := json.Marshal(agentlink.PairRequest{Token: tok2, Name: "x", Protocol: 99})
	resp, err := http.Post(e.Server.URL+agentlink.BasePath+"/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("protocole 99 : statut %d", resp.StatusCode)
	}
	// Le jeton n'a pas été consommé par l'erreur de version.
	if _, err := anon.Pair(bg, agentlink.PairRequest{Token: tok2, Name: "ok"}); err != nil {
		t.Fatalf("jeton après refus de version : %v", err)
	}
}

func TestAvailabilityOnlineOffline(t *testing.T) {
	e := agentlinktest.New(t, agentlink.Config{OfflineAfter: 30 * time.Second})
	cl, pr := e.Pair("PC")
	hello(t, cl)
	st := e.Reg.Status(bg)
	if len(st) != 1 || !st[0].Online || !st[0].StudioInstalled || st[0].StudioVersion != "02.08.02.61" || st[0].LastSeen == "" {
		t.Fatalf("statut en ligne : %+v", st)
	}
	e.Clock.Advance(31 * time.Second)
	st = e.Reg.Status(bg)
	if st[0].Online || st[0].LastSeen == "" || st[0].ID != pr.AgentID {
		t.Fatalf("statut hors ligne : %+v", st)
	}
	// Un heartbeat suffit à revenir en ligne.
	if _, err := cl.Heartbeat(bg, agentlink.HeartbeatRequest{StudioRunning: true}); err != nil {
		t.Fatal(err)
	}
	if st = e.Reg.Status(bg); !st[0].Online || !st[0].StudioRunning {
		t.Fatalf("après heartbeat : %+v", st)
	}
}

func TestTaskLifecycleAndLongPoll(t *testing.T) {
	e := agentlinktest.New(t, agentlink.Config{})
	cl, _ := e.Pair("PC")
	hello(t, cl)

	// Long-polling sans travail : 204 après l'attente.
	t0 := time.Now()
	got, err := cl.NextTask(bg, 1*time.Second)
	if err != nil || got != nil || time.Since(t0) < 900*time.Millisecond {
		t.Fatalf("long-polling vide : %v %v %v", got, err, time.Since(t0))
	}
	// Un travail créé pendant l'attente réveille le poll immédiatement.
	type res struct {
		t   *agentlink.Task
		err error
	}
	ch := make(chan res, 1)
	go func() { tk, err := cl.NextTask(bg, 10*time.Second); ch <- res{tk, err} }()
	time.Sleep(200 * time.Millisecond)
	ti, err := e.Reg.CreateTask(bg, agentlink.CreateTaskInput{Kind: agentlink.KindGetProjectState})
	if err != nil {
		t.Fatal(err)
	}
	// Le poll en attente peut déjà l'avoir pris : queued ou assigned.
	if (ti.State != agentlink.StateQueued && ti.State != agentlink.StateAssigned) || ti.Executor != agentlink.ExecutorWindowsAgent {
		t.Fatalf("tâche créée : %+v", ti)
	}
	select {
	case r := <-ch:
		if r.err != nil || r.t == nil || r.t.ID != ti.ID || r.t.Kind != agentlink.KindGetProjectState {
			t.Fatalf("poll : %+v %v", r.t, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("le long-polling n'a pas été réveillé")
	}
	if cur, _ := e.Reg.GetTask(bg, ti.ID); cur.State != agentlink.StateAssigned {
		t.Fatalf("état après remise : %s", cur.State)
	}
	if err := cl.StartTask(bg, ti.ID); err != nil {
		t.Fatal(err)
	}
	if err := cl.Progress(bg, ti.ID, agentlink.ProgressReport{Progress: 0.5, Message: "mi-parcours"}); err != nil {
		t.Fatal(err)
	}
	cur, _ := e.Reg.GetTask(bg, ti.ID)
	if cur.State != agentlink.StateRunning || cur.Progress != 0.5 || cur.Message != "mi-parcours" {
		t.Fatalf("en cours : %+v", cur)
	}
	if err := cl.Result(bg, ti.ID, agentlink.TaskResult{State: agentlink.StateSucceeded, Result: json.RawMessage(`{"ok":true}`)}); err != nil {
		t.Fatal(err)
	}
	// Idempotent : un second rapport identique est accepté.
	if err := cl.Result(bg, ti.ID, agentlink.TaskResult{State: agentlink.StateSucceeded}); err != nil {
		t.Fatalf("rapport répété : %v", err)
	}
	cur, _ = e.Reg.GetTask(bg, ti.ID)
	if cur.State != agentlink.StateSucceeded || string(cur.Result) != `{"ok":true}` || cur.FinishedAt == nil {
		t.Fatalf("terminé : %+v", cur)
	}
	// Un autre agent ne voit ni ne touche ce travail.
	cl2, _ := e.Pair("Autre")
	hello(t, cl2)
	if err := cl2.StartTask(bg, ti.ID); code(err) != agentlink.CodeNotFound {
		t.Fatalf("travail d'un autre agent : %v", err)
	}
}

func TestTaskCreationRules(t *testing.T) {
	e := agentlinktest.New(t, agentlink.Config{OfflineAfter: 30 * time.Second})
	// Sans agent : agent_offline.
	if _, err := e.Reg.CreateTask(bg, agentlink.CreateTaskInput{Kind: agentlink.KindGetProjectState}); code(err) != agentlink.CodeAgentOffline {
		t.Fatalf("sans agent : %v", err)
	}
	cl, _ := e.Pair("PC")
	hello(t, cl)
	// Travail physique : jamais confié à un agent.
	if _, err := e.Reg.CreateTask(bg, agentlink.CreateTaskInput{Kind: "start_print"}); code(err) != agentlink.CodePhysicalRefused {
		t.Fatalf("start_print : %v", err)
	}
	if _, err := e.Reg.CreateTask(bg, agentlink.CreateTaskInput{Kind: "inconnu"}); code(err) != agentlink.CodeInvalid {
		t.Fatalf("type inconnu : %v", err)
	}
	if _, err := e.Reg.CreateTask(bg, agentlink.CreateTaskInput{Kind: agentlink.KindSlice, Executor: "linux"}); code(err) != agentlink.CodeInvalid {
		t.Fatalf("exécuteur linux : %v", err)
	}
	// Modification en direct du projet ouvert : non pris en charge, expliqué.
	_, err := e.Reg.CreateTask(bg, agentlink.CreateTaskInput{Kind: agentlink.KindModifyOpenProject})
	var ae *agentlink.Error
	if !errors.As(err, &ae) || ae.Code != agentlink.CodeNotSupported || ae.Status != 501 || !strings.Contains(ae.Message, "copie") {
		t.Fatalf("modify_open_project : %v", err)
	}
	// Entrée absente du magasin : refusée.
	if _, err := e.Reg.CreateTask(bg, agentlink.CreateTaskInput{Kind: agentlink.KindSlice, Inputs: []agentlink.Artifact{{SHA256: strings.Repeat("a", 64)}}}); code(err) != agentlink.CodeInvalid {
		t.Fatalf("entrée absente : %v", err)
	}
	// Agent hors ligne : refus (les opérations Windows attendent son retour).
	e.Clock.Advance(time.Minute)
	if _, err := e.Reg.CreateTask(bg, agentlink.CreateTaskInput{Kind: agentlink.KindGetProjectState}); code(err) != agentlink.CodeAgentOffline {
		t.Fatalf("agent hors ligne : %v", err)
	}
	// Travail en file annulable avant remise.
	hello(t, cl)
	ti, _ := e.Reg.CreateTask(bg, agentlink.CreateTaskInput{Kind: agentlink.KindGetProjectState})
	c, err := e.Reg.CancelTask(bg, ti.ID)
	if err != nil || c.State != agentlink.StateCancelled {
		t.Fatalf("annulation en file : %+v %v", c, err)
	}
	if tk, _ := cl.NextTask(bg, 0); tk != nil {
		t.Fatalf("un travail annulé a été remis : %+v", tk)
	}
}

func TestAgentLostMakesTaskUnknownThenReconciles(t *testing.T) {
	e := agentlinktest.New(t, agentlink.Config{OfflineAfter: 30 * time.Second})
	cl, pr := e.Pair("PC")
	hello(t, cl)

	mk := func() agentlink.TaskInfo {
		ti, err := e.Reg.CreateTask(bg, agentlink.CreateTaskInput{Kind: agentlink.KindGetProjectState})
		if err != nil {
			t.Fatal(err)
		}
		return ti
	}
	// A : en cours quand l'agent disparaît ; B : encore en file (jamais remis).
	a := mk()
	tk, _ := cl.NextTask(bg, time.Second)
	if tk == nil || tk.ID != a.ID {
		t.Fatal("travail A non remis")
	}
	_ = cl.StartTask(bg, a.ID)
	b := mk()

	e.Clock.Advance(31 * time.Second) // plus de signe de vie
	ga, _ := e.Reg.GetTask(bg, a.ID)
	gb, _ := e.Reg.GetTask(bg, b.ID)
	if ga.State != agentlink.StateUnknown || !strings.Contains(ga.Note, "agent perdu") {
		t.Fatalf("A après perte : %+v", ga)
	}
	if gb.State != agentlink.StateQueued {
		t.Fatalf("B n'a jamais démarré, il doit rester en file : %s", gb.State)
	}
	// Aucune relance automatique : A reste inconnu, personne ne le reprend.
	e.Clock.Advance(10 * time.Minute)
	if ga, _ = e.Reg.GetTask(bg, a.ID); ga.State != agentlink.StateUnknown {
		t.Fatalf("A ne doit pas changer seul : %s", ga.State)
	}

	// Retour de l'agent, il détient toujours A en cours : réconcilié en running.
	r, err := cl.Hello(bg, agentlink.HelloRequest{AgentVersion: "t", Capabilities: agentlinktest.AllCaps(), Held: []agentlink.HeldTask{{ID: a.ID, State: "running"}}})
	if err != nil || len(r.Reconciled) != 1 || r.Reconciled[0] != a.ID {
		t.Fatalf("hello de retour : %+v %v", r, err)
	}
	if ga, _ = e.Reg.GetTask(bg, a.ID); ga.State != agentlink.StateRunning {
		t.Fatalf("A réconcilié : %s", ga.State)
	}

	// Seconde perte puis rapport du résultat réel après reconnexion.
	e.Clock.Advance(31 * time.Second)
	if ga, _ = e.Reg.GetTask(bg, a.ID); ga.State != agentlink.StateUnknown {
		t.Fatalf("A de nouveau inconnu : %s", ga.State)
	}
	if err := cl.Result(bg, a.ID, agentlink.TaskResult{State: agentlink.StateSucceeded, Result: json.RawMessage(`{"x":1}`)}); err != nil {
		t.Fatal(err)
	}
	ga, _ = e.Reg.GetTask(bg, a.ID)
	if ga.State != agentlink.StateSucceeded || !strings.Contains(ga.Note, "réconcilié") {
		t.Fatalf("A après rapport tardif : %+v", ga)
	}

	// Cas où l'agent revient SANS détenir le travail : reste inconnu, jamais relancé tout seul ;
	// recréation explicite, l'ancien est remplacé et un rapport tardif est refusé (pas de doublon).
	c := mk()
	_, _ = cl.NextTask(bg, time.Second) // récupère B puis C ... on consomme B
	tkC, _ := cl.NextTask(bg, time.Second)
	if tkC == nil || tkC.ID != c.ID {
		t.Fatalf("C attendu : %+v", tkC)
	}
	_ = cl.StartTask(bg, c.ID)
	e.Clock.Advance(31 * time.Second)
	r, err = cl.Hello(bg, agentlink.HelloRequest{AgentVersion: "t", Capabilities: agentlinktest.AllCaps()})
	if err != nil {
		t.Fatal(err)
	}
	if gc, _ := e.Reg.GetTask(bg, c.ID); gc.State != agentlink.StateUnknown {
		t.Fatalf("C sans détention : %s", gc.State)
	}
	nt, err := e.Reg.Requeue(bg, c.ID)
	if err != nil || nt.Replaces != c.ID || nt.State != agentlink.StateQueued {
		t.Fatalf("recréation : %+v %v", nt, err)
	}
	if gc, _ := e.Reg.GetTask(bg, c.ID); gc.State != agentlink.StateCancelled || !strings.Contains(gc.Note, nt.ID) {
		t.Fatalf("ancien travail remplacé : %+v", gc)
	}
	if err := cl.Result(bg, c.ID, agentlink.TaskResult{State: agentlink.StateSucceeded}); code(err) != agentlink.CodeConflict {
		t.Fatalf("rapport tardif d'un travail remplacé : %v", err)
	}
	// Le serveur demande d'abandonner C si l'agent le détient encore.
	r, _ = cl.Hello(bg, agentlink.HelloRequest{AgentVersion: "t", Capabilities: agentlinktest.AllCaps(), Held: []agentlink.HeldTask{{ID: c.ID, State: "running"}}})
	if len(r.Abandon) != 1 || r.Abandon[0] != c.ID {
		t.Fatalf("abandon : %+v", r)
	}
	_ = pr
}

func TestPhysicalTaskIsNeverRequeued(t *testing.T) {
	e := agentlinktest.New(t, agentlink.Config{})
	cl, pr := e.Pair("PC")
	hello(t, cl)
	ti, _ := e.Reg.CreateTask(bg, agentlink.CreateTaskInput{Kind: agentlink.KindGetProjectState})
	// Simule un travail physique (aucun type physique n'est accepté par l'API) et sa perte.
	if _, err := e.Core.Store.DB().Exec(`UPDATE agent_tasks SET physical=1, kind='start_print', state='unknown' WHERE id=?`, ti.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Reg.Requeue(bg, ti.ID); code(err) != agentlink.CodePhysicalRefused {
		t.Fatalf("requeue d'un travail physique : %v", err)
	}
	if tk, _ := cl.NextTask(bg, 0); tk != nil {
		t.Fatalf("remis à un agent : %+v", tk)
	}
	_ = pr
}

// ---- transferts ----

func newTaskWithOutputRight(t *testing.T, e *agentlinktest.Env, cl *agentlink.Client) string {
	t.Helper()
	ti, err := e.Reg.CreateTask(bg, agentlink.CreateTaskInput{Kind: agentlink.KindSlice})
	if err != nil {
		t.Fatal(err)
	}
	tk, _ := cl.NextTask(bg, time.Second)
	if tk == nil {
		t.Fatal("pas de travail")
	}
	_ = cl.StartTask(bg, ti.ID)
	return ti.ID
}

func TestUploadResumeAndVerification(t *testing.T) {
	e := agentlinktest.New(t, agentlink.Config{MaxChunkBytes: 1 << 20})
	cl, _ := e.Pair("PC")
	hello(t, cl)
	id := newTaskWithOutputRight(t, e, cl)

	data := bytes.Repeat([]byte("0123456789abcdef"), 40_000) // 640 Ko
	sha := sum(data)
	dir := t.TempDir()
	src := filepath.Join(dir, "out.gcode.3mf")
	os.WriteFile(src, data, 0o644)

	// Seule la cible « artifact » peut être déclarée par l'agent ; « inbox » doit être pré-autorisée par le serveur.
	if code(cl.Upload(bg, id, src, sha, int64(len(data)), "inbox", "x", nil)) != agentlink.CodeNotAllowed {
		t.Fatal("un envoi non autorisé (target inbox non pré-déclaré) doit être refusé")
	}

	// Envoi interrompu : premier morceau seulement (envoi manuel).
	partial := func(n int) {
		body, _ := json.Marshal(agentlink.UploadBegin{TaskID: id, SHA256: sha, Size: int64(len(data)), Target: agentlink.TargetArtifact, Name: "out.gcode.3mf"})
		req, _ := http.NewRequest("POST", e.Server.URL+agentlink.BasePath+"/uploads/begin", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+cl.Secret)
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("begin : %v %v", resp, err)
		}
		resp.Body.Close()
		put, _ := http.NewRequest("PUT", e.Server.URL+agentlink.BasePath+"/uploads/"+sha+"?task="+id, bytes.NewReader(data[:n]))
		put.Header.Set("Authorization", "Bearer "+cl.Secret)
		put.Header.Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", n-1, len(data)))
		pr, err := http.DefaultClient.Do(put)
		if err != nil || pr.StatusCode != 200 {
			t.Fatalf("morceau : %v %v", pr, err)
		}
		pr.Body.Close()
	}
	partial(300_000)

	// Reprise : le client demande l'offset au serveur et n'envoie que le reste.
	var sent []int64
	if err := cl.Upload(bg, id, src, sha, int64(len(data)), agentlink.TargetArtifact, "out.gcode.3mf", func(s, total int64) { sent = append(sent, s) }); err != nil {
		t.Fatal(err)
	}
	if len(sent) == 0 || sent[0] <= 300_000 {
		t.Fatalf("la reprise doit repartir de l'octet 300000 : %v", sent)
	}
	if !e.Core.Blobs.Has(sha) {
		t.Fatal("blob absent après envoi")
	}
	if err := e.Core.Blobs.Verify(sha); err != nil {
		t.Fatal(err)
	}
	// Un second Upload du même contenu est immédiat.
	if err := cl.Upload(bg, id, src, sha, int64(len(data)), agentlink.TargetArtifact, "out.gcode.3mf", nil); err != nil {
		t.Fatal(err)
	}
	// Rapport avec sortie vérifiée.
	if err := cl.Result(bg, id, agentlink.TaskResult{State: agentlink.StateSucceeded, Outputs: []agentlink.Artifact{{SHA256: sha, Size: int64(len(data)), Name: "out.gcode.3mf"}}}); err != nil {
		t.Fatal(err)
	}
	ti, _ := e.Reg.GetTask(bg, id)
	if len(ti.Outputs) != 1 || ti.Outputs[0].SHA256 != sha || ti.Outputs[0].Name != "out.gcode.3mf" {
		t.Fatalf("sorties : %+v", ti.Outputs)
	}
}

func TestUploadRejectsWrongHashAndOutOfOrder(t *testing.T) {
	e := agentlinktest.New(t, agentlink.Config{})
	cl, _ := e.Pair("PC")
	hello(t, cl)
	id := newTaskWithOutputRight(t, e, cl)
	good := []byte("contenu annoncé")
	sha := sum(good)
	evil := []byte("contenu ALTÉRÉ!")
	if len(evil) != len(good) { // même taille, empreinte différente
		evil = append(evil[:0], []byte(strings.Repeat("x", len(good)))...)
	}
	src := filepath.Join(t.TempDir(), "f")
	os.WriteFile(src, evil, 0o644)
	err := cl.Upload(bg, id, src, sha, int64(len(good)), agentlink.TargetArtifact, "f.bin", nil)
	if code(err) != agentlink.CodeHashMismatch {
		t.Fatalf("empreinte altérée : %v", err)
	}
	if e.Core.Blobs.Has(sha) || e.Core.Blobs.Has(sum(evil)) {
		t.Fatal("un contenu rejeté a été publié dans le magasin")
	}
	// Décalage : le serveur répond 409 avec l'offset attendu.
	put, _ := http.NewRequest("PUT", e.Server.URL+agentlink.BasePath+"/uploads/"+sha+"?task="+id, bytes.NewReader(good[5:]))
	put.Header.Set("Authorization", "Bearer "+cl.Secret)
	put.Header.Set("Content-Range", fmt.Sprintf("bytes 5-%d/%d", len(good)-1, len(good)))
	resp, _ := http.DefaultClient.Do(put)
	if resp.StatusCode != 409 || resp.Header.Get("Upload-Offset") != "0" {
		t.Fatalf("décalage : %d offset=%q", resp.StatusCode, resp.Header.Get("Upload-Offset"))
	}
	resp.Body.Close()
	// Un rapport qui cite une sortie jamais transférée est refusé.
	err = cl.Result(bg, id, agentlink.TaskResult{State: agentlink.StateSucceeded, Outputs: []agentlink.Artifact{{SHA256: sha, Size: 1}}})
	if code(err) != agentlink.CodeOutputMissing {
		t.Fatalf("sortie non transférée : %v", err)
	}
	// Taille déclarée excessive.
	e2 := agentlinktest.New(t, agentlink.Config{MaxUploadBytes: 10})
	cl2, _ := e2.Pair("PC")
	hello(t, cl2)
	id2 := newTaskWithOutputRight(t, e2, cl2)
	if code(cl2.Upload(bg, id2, src, sha, 1000, agentlink.TargetArtifact, "f", nil)) != agentlink.CodeInvalid {
		t.Fatal("taille au-delà de la limite acceptée")
	}
}

func TestDownloadOnlyTaskInputsWithResumeAndVerify(t *testing.T) {
	e := agentlinktest.New(t, agentlink.Config{})
	cl, _ := e.Pair("PC")
	hello(t, cl)
	data := bytes.Repeat([]byte("modèle3D"), 50_000)
	sha, _, err := e.Core.Blobs.Put(bytes.NewReader(data), "")
	if err != nil {
		t.Fatal(err)
	}
	other, _, _ := e.Core.Blobs.Put(strings.NewReader("secret du serveur"), "")

	dest := filepath.Join(t.TempDir(), "in", "model.stl")
	// Avant qu'un travail ne le désigne, le blob n'est pas téléchargeable.
	if err := cl.Download(bg, sha, dest); code(err) != agentlink.CodeNotAllowed {
		t.Fatalf("téléchargement non autorisé : %v", err)
	}
	if _, err := e.Reg.CreateTask(bg, agentlink.CreateTaskInput{Kind: agentlink.KindSlice, Inputs: []agentlink.Artifact{{SHA256: sha, Name: "model.stl"}}}); err != nil {
		t.Fatal(err)
	}
	if err := cl.Download(bg, other, dest); code(err) != agentlink.CodeNotAllowed {
		t.Fatalf("blob étranger aux entrées : %v", err)
	}
	// Reprise : un .part existant est complété par Range.
	os.MkdirAll(filepath.Dir(dest), 0o755)
	os.WriteFile(dest+".part", data[:100_000], 0o600)
	if err := cl.Download(bg, sha, dest); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, data) {
		t.Fatal("contenu téléchargé différent")
	}
	if _, err := os.Stat(dest + ".part"); err == nil {
		t.Fatal(".part laissé")
	}
	// Un .part corrompu est détecté par l'empreinte.
	os.Remove(dest)
	os.WriteFile(dest+".part", bytes.Repeat([]byte("Z"), 100_000), 0o600)
	if err := cl.Download(bg, sha, dest); err == nil || !strings.Contains(err.Error(), "empreinte") {
		t.Fatalf("reprise sur .part corrompu : %v", err)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Fatal("fichier corrompu publié")
	}
}

func TestCleanRelAndLabels(t *testing.T) {
	for _, bad := range []string{"", "../x.stl", "a/../../x.stl", "/abs/x.stl", `a\b.stl`, "C:/x.stl", "a//b.stl", "a/./b.stl", "nul.stl", "con", "x.stl.", "a/b /c.stl", "a\x00b"} {
		if _, err := agentlink.CleanRel(bad); err == nil {
			t.Errorf("chemin accepté à tort : %q", bad)
		}
	}
	for _, ok := range []string{"x.stl", "dir/sous dossier/é à.3mf"} {
		if _, err := agentlink.CleanRel(ok); err != nil {
			t.Errorf("chemin refusé : %q : %v", ok, err)
		}
	}
	if agentlink.SafeLabel("..\\Mes Modèles/3D") != "Mes Modèles_3D" && agentlink.SafeLabel("Ma bibliothèque") != "Ma bibliothèque" {
		t.Fatal("assainissement des étiquettes")
	}
}

func TestInventoryRejectsHostilePaths(t *testing.T) {
	e := agentlinktest.New(t, agentlink.Config{})
	cl, _ := e.Pair("PC")
	hello(t, cl)
	good := strings.Repeat("a", 64)
	resp, err := cl.Inventory(bg, agentlink.InventoryRequest{RootLabel: "Modèles", Entries: []agentlink.InventoryEntry{
		{RelPath: "ok/cube.stl", Size: 10, SHA256: good, OriginPath: `C:\M\ok\cube.stl`},
		{RelPath: "../evil.stl", Size: 10, SHA256: good},
		{RelPath: "bad.exe", Size: 10, SHA256: good},
		{RelPath: "x.stl", Size: 10, SHA256: "xyz"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Accepted != 1 || resp.Rejected != 3 || resp.ByStatus["indexed"] != 1 {
		t.Fatalf("inventaire : %+v", resp)
	}
	if _, err := cl.Inventory(bg, agentlink.InventoryRequest{RootLabel: "../Hors", Entries: nil}); code(err) != agentlink.CodeInvalid {
		t.Fatalf("étiquette hostile : %v", err)
	}
	_ = io.Discard
}
