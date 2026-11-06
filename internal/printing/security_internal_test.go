package printing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// fakeAdapter imite l'ordre de printer.Bambu.Start : refus d'état éventuel, PUIS vérification de la preuve
// (qui consomme la confirmation), PUIS « publication » (compteur sent).
type fakeAdapter struct {
	verifier printer.ProofVerifier
	startErr error
	sent     atomic.Int32
	mu       sync.Mutex
	state    printer.State
}

func (f *fakeAdapter) getState() printer.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state == "" {
		return printer.StateIdle
	}
	return f.state
}
func (f *fakeAdapter) Status(context.Context) (printer.Status, error) {
	return printer.Status{State: f.getState(), Connected: true, UpdatedAt: time.Now(), FullReportSeen: true, SubtaskName: "x-cccccccccccc"}, nil
}
func (f *fakeAdapter) Upload(context.Context, io.Reader, string, int64, string) (printer.RemoteRef, error) {
	return printer.RemoteRef{}, errors.New("non utilisé")
}
func (f *fakeAdapter) Start(ctx context.Context, req printer.StartRequest) error {
	if f.startErr != nil {
		return f.startErr
	}
	if err := f.verifier(ctx, req.Proof); err != nil {
		return fmt.Errorf("%w: %v", printer.ErrNoProof, err)
	}
	f.sent.Add(1)
	f.mu.Lock()
	f.state = printer.StatePrinting
	f.mu.Unlock()
	return nil
}
func (f *fakeAdapter) Pause(context.Context) error  { return nil }
func (f *fakeAdapter) Resume(context.Context) error { return nil }
func (f *fakeAdapter) Stop(context.Context) error {
	f.mu.Lock()
	f.state = printer.StateIdle
	f.mu.Unlock()
	return nil
}
func (f *fakeAdapter) Subscribe(ctx context.Context) (<-chan printer.Status, error) {
	ch := make(chan printer.Status)
	go func() { <-ctx.Done(); close(ch) }()
	return ch, nil
}

// newRunService : service complet avec file de travaux en marche et adaptateur factice.
func newRunService(t *testing.T) (*Service, *fakeAdapter) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	bl, _ := store.NewBlobStore(filepath.Join(dir, "blobs"))
	st.AttachBlobs(bl)
	q := jobs.NewQueue(st, jobs.QueueConfig{})
	reg := jobs.NewRegistry()
	fa := &fakeAdapter{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s, err := New(Deps{Store: st, Blobs: bl, Queue: q, Registry: reg, Secrets: NewMemorySecretStore(), DataDir: dir, Log: log,
		Adapters: func(_ context.Context, _ PrinterRecord, _ printer.SecretProvider, v printer.ProofVerifier) (printer.Adapter, error) {
			fa.verifier = v
			return fa, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	rn := jobs.NewRunner(q, reg, jobs.RunnerConfig{PollInterval: 10 * time.Millisecond, HeartbeatInterval: 50 * time.Millisecond, Logger: log})
	done := make(chan struct{})
	go func() { defer close(done); _ = rn.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done; s.Close(); st.Close() })
	rec := PrinterRecord{ID: "prn_t", ModelID: "p1s", Name: "Atelier", Host: "192.168.1.20", Serial: "01P00A000000001", PinnedSHA256: strings.Repeat("a", 64), CreatedAt: time.Now()}
	if err := st.PutDoc(ctx, actor, "printers", rec.ID, rec); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.SetAccessCode(ctx, rec.ID, "code-de-test"); err != nil {
		t.Fatal(err)
	}
	return s, fa
}

func confirmed(t *testing.T, s *Service) (Job, string) {
	t.Helper()
	j := awaitingJob(t, s, contracts.PrintAwaitingConfirmation)
	v, err := s.RequestConfirmation(context.Background(), RequestConfirmationInput{PrintJobID: j.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(context.Background(), v.ConfirmationID, true, "web"); err != nil {
		t.Fatal(err)
	}
	return j, v.ConfirmationID
}

// Point 7 : l'accord humain ne vaut que 2 minutes après le clic.
func TestApprovalExpiresTwoMinutesAfterTheClick(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	ctx := context.Background()
	base := time.Now()
	clock := base
	s.d.Now = func() time.Time { return clock }
	j := awaitingJob(t, s, contracts.PrintAwaitingConfirmation)
	v, _ := s.RequestConfirmation(ctx, RequestConfirmationInput{PrintJobID: j.ID})
	clock = base.Add(5 * time.Minute) // l'humain clique 5 min après la demande (confirmation valable 10 min)
	if _, err := s.Approve(ctx, v.ConfirmationID, true, "web"); err != nil {
		t.Fatal(err)
	}
	clock = base.Add(5*time.Minute + 119*time.Second)
	if _, err := s.approvedFor(v.ConfirmationID, j.ID); err != nil {
		t.Fatalf("à 119 s : %v", err)
	}
	clock = base.Add(5*time.Minute + 121*time.Second)
	if _, err := s.approvedFor(v.ConfirmationID, j.ID); !errors.Is(err, ErrNeedsApproval) {
		t.Fatalf("à 121 s l'accord doit être périmé : %v", err)
	}
	// Une fois périmé, il le reste (l'humain doit recliquer) et la vue ne le montre plus approuvé.
	clock = base.Add(5*time.Minute + 10*time.Second)
	if _, err := s.approvedFor(v.ConfirmationID, j.ID); !errors.Is(err, ErrNeedsApproval) {
		t.Fatalf("accord périmé ressuscité : %v", err)
	}
	if view, _ := s.GetConfirmation(ctx, v.ConfirmationID); view.Approved {
		t.Fatal("la vue montre encore l'accord")
	}
	// Reclic : valable de nouveau 2 minutes.
	if _, err := s.Approve(ctx, v.ConfirmationID, true, "web"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.approvedFor(v.ConfirmationID, j.ID); err != nil {
		t.Fatal(err)
	}
}

// Point 7 : après tout not_sent l'accord est invalidé (le jeton n'est pas brûlé, l'humain doit recliquer).
func TestApprovalInvalidatedAfterNotSent(t *testing.T) {
	s, fa := newRunService(t)
	ctx := context.Background()
	j, conf := confirmed(t, s)
	fa.startErr = fmt.Errorf("%w: état test", printer.ErrNotReady)
	out, err := s.StartPrint(ctx, StartInput{PrintJobID: j.ID, ConfirmationID: conf, IdempotencyKey: "cle-start-notsent-1"})
	if err != nil || out.Outcome != "not_sent" {
		t.Fatalf("not_sent attendu : %+v %v", out, err)
	}
	if fa.sent.Load() != 0 {
		t.Fatal("commande envoyée")
	}
	if _, err := s.approvedFor(conf, j.ID); !errors.Is(err, ErrNeedsApproval) {
		t.Fatalf("l'accord doit être invalidé après not_sent : %v", err)
	}
	var c contracts.PrintConfirmation
	if err := s.d.Store.GetDoc(ctx, "print_confirmations", conf, &c); err != nil || c.UsedAt != nil {
		t.Fatalf("le jeton ne doit pas être brûlé par un not_sent : %+v %v", c.UsedAt, err)
	}
	// Sans nouveau clic : refus ; avec un nouveau clic et l'imprimante prête : lancement.
	if _, err := s.StartPrint(ctx, StartInput{PrintJobID: j.ID, ConfirmationID: conf, IdempotencyKey: "cle-start-notsent-2"}); !errors.Is(err, ErrNeedsApproval) {
		t.Fatalf("relance sans nouvel accord : %v", err)
	}
	fa.startErr = nil
	if _, err := s.Approve(ctx, conf, true, "web"); err != nil {
		t.Fatal(err)
	}
	out, err = s.StartPrint(ctx, StartInput{PrintJobID: j.ID, ConfirmationID: conf, IdempotencyKey: "cle-start-notsent-3"})
	if err != nil || out.Outcome != "printing" || fa.sent.Load() != 1 {
		t.Fatalf("lancement après nouvel accord : %+v %v (envoyés %d)", out, err, fa.sent.Load())
	}
}

// Point 11 : confirmation consommée mais transition en échec -> état explicite, terminal, sûr ; rien n'est envoyé.
func TestConsumeOKThenTransitionFailsAbortsBeforeSend(t *testing.T) {
	s, fa := newRunService(t)
	ctx := context.Background()
	j, conf := confirmed(t, s)
	s.confirmTransition = func(context.Context, string, string) error { return errors.New("disque plein (simulé)") }
	out, err := s.StartPrint(ctx, StartInput{PrintJobID: j.ID, ConfirmationID: conf, IdempotencyKey: "cle-start-abort-1"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome != "aborted_before_send" || out.State != contracts.PrintStartAbortedBeforeSend {
		t.Fatalf("issue : %s / %s", out.Outcome, out.State)
	}
	if fa.sent.Load() != 0 {
		t.Fatal("une commande a été envoyée malgré l'échec d'enregistrement")
	}
	var c contracts.PrintConfirmation
	if err := s.d.Store.GetDoc(ctx, "print_confirmations", conf, &c); err != nil || c.UsedAt == nil {
		t.Fatalf("la confirmation était consommée : %+v %v", c.UsedAt, err)
	}
	cur, _ := s.GetJob(ctx, j.ID)
	if !strings.Contains(cur.LastError, "rien n'a été envoyé") || !cur.State.Terminal() {
		t.Fatalf("état explicite attendu : %s %q", cur.State, cur.LastError)
	}
	// Terminal : ni relance, ni nouvelle confirmation, ni annulation « utile ».
	if _, err := s.StartPrint(ctx, StartInput{PrintJobID: j.ID, ConfirmationID: conf, IdempotencyKey: "cle-start-abort-2"}); !errors.Is(err, ErrState) {
		t.Fatalf("relance depuis l'état terminal : %v", err)
	}
	if _, err := s.RequestConfirmation(ctx, RequestConfirmationInput{PrintJobID: j.ID}); !errors.Is(err, ErrState) {
		t.Fatalf("nouvelle confirmation : %v", err)
	}
	if o, _ := s.CancelPrint(ctx, CancelInput{PrintJobID: j.ID}); o.Outcome != "refused" {
		t.Fatalf("annulation : %+v", o)
	}
}

// Point 6 : clés composées et rejeu avec d'autres paramètres refusé.
func TestStartIdempotencyKeyIsComposedAndParamsChecked(t *testing.T) {
	s, fa := newRunService(t)
	ctx := context.Background()
	j, conf := confirmed(t, s)
	const key = "cle-partagee-par-erreur"
	out, err := s.StartPrint(ctx, StartInput{PrintJobID: j.ID, ConfirmationID: conf, IdempotencyKey: key})
	if err != nil || out.Outcome != "printing" {
		t.Fatalf("lancement : %+v %v", out, err)
	}
	q, err := s.d.Queue.Get(ctx, out.QueueJobID)
	if err != nil || q.IdempotencyKey != j.ID+":start:"+key {
		t.Fatalf("clé stockée %q : attendue %q (%v)", q.IdempotencyKey, j.ID+":start:"+key, err)
	}
	// Même clé, mêmes paramètres : rejeu, aucune nouvelle commande.
	again, err := s.StartPrint(ctx, StartInput{PrintJobID: j.ID, ConfirmationID: conf, IdempotencyKey: key})
	if err != nil || !again.Replayed || fa.sent.Load() != 1 {
		t.Fatalf("rejeu : %+v %v (envoyés %d)", again, err, fa.sent.Load())
	}
	// Même clé, autre confirmation : refusé.
	if _, err := s.StartPrint(ctx, StartInput{PrintJobID: j.ID, ConfirmationID: "cf_autre", IdempotencyKey: key}); !errors.Is(err, ErrReplayParams) {
		t.Fatalf("rejeu avec d'autres paramètres : %v", err)
	}
	// La même chaîne sur une autre impression ne collisionne pas.
	j2, conf2 := confirmed(t, s)
	if _, err := s.update(ctx, j2.ID, func(x *Job) error { x.PrinterID = "prn_t2"; return nil }); err != nil {
		t.Fatal(err)
	}
	_ = conf2
	if k := idemKey(j.ID, "start", key); k == idemKey(j2.ID, "start", key) || k == idemKey(j.ID, "upload", key) {
		t.Fatal("clés non distinctes")
	}
}

// Point 3 : maps stops / pages / consumedBy avec TTL et purge.
func TestStopsPagesAndConsumedAreExpiredAndPurged(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	base := time.Now()
	clock := base
	s.d.Now = func() time.Time { return clock }
	id1, _ := s.requestStop("pj_1")
	if id2, _ := s.requestStop("pj_1"); id2 != id1 {
		t.Fatal("une demande valide doit être réutilisée")
	}
	n := s.issueNonce("confirm:x")
	s.mu.Lock()
	s.consumedBy["cf_old"] = consumedEntry{queueJob: "job_1", at: base}
	s.mu.Unlock()
	if len(s.stops) != 1 || len(s.pages) != 1 || len(s.consumedBy) != 1 {
		t.Fatalf("préparation : %d %d %d", len(s.stops), len(s.pages), len(s.consumedBy))
	}
	clock = base.Add(stopTTL + time.Second)
	if _, err := s.GetStop(context.Background(), id1); err == nil {
		t.Fatal("demande d'arrêt expirée encore lisible")
	}
	s.purge()
	s.mu.Lock()
	ns, np := len(s.stops), len(s.pages)
	nc := len(s.consumedBy)
	s.mu.Unlock()
	if ns != 0 {
		t.Fatalf("stops non purgé : %d", ns)
	}
	if np != 1 { // le nonce vit 15 min
		t.Fatalf("pages : %d", np)
	}
	if nc != 1 { // la trace de consommation vit 1 h
		t.Fatalf("consumedBy : %d", nc)
	}
	clock = base.Add(consumedTTL + time.Second)
	s.purge()
	if s.useNonce(n, "confirm:x") {
		t.Fatal("nonce périmé accepté")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pages) != 0 || len(s.consumedBy) != 0 {
		t.Fatalf("purge incomplète : %d %d", len(s.pages), len(s.consumedBy))
	}
}

// Point 3 : cancel_print en PRINTING ne renvoie aucun identifiant exécutable ; l'arrêt vient d'ExecuteStop (page humaine).
func TestCancelPrintingOnlyCreatesAStopRequest(t *testing.T) {
	s, fa := newRunService(t)
	ctx := context.Background()
	j, conf := confirmed(t, s)
	if out, err := s.StartPrint(ctx, StartInput{PrintJobID: j.ID, ConfirmationID: conf, IdempotencyKey: "cle-start-stop-1"}); err != nil || out.Outcome != "printing" {
		t.Fatalf("lancement : %+v %v", out, err)
	}
	o, err := s.CancelPrint(ctx, CancelInput{PrintJobID: j.ID})
	if err != nil || o.Outcome != "stop_requested" || !strings.Contains(o.StopURL, "/stop/stp_") {
		t.Fatalf("demande d'arrêt : %+v %v", o, err)
	}
	if fa.getState() != printer.StatePrinting {
		t.Fatal("cancel_print a arrêté l'imprimante sans l'humain")
	}
	if strings.Contains(jsonOf(t, o), "stop_confirmation_id") {
		t.Fatalf("la sortie ne doit exposer aucun identifiant d'arrêt exécutable : %s", jsonOf(t, o))
	}
	stopID := o.StopURL[strings.LastIndex(o.StopURL, "/")+1:]
	if _, err := s.ExecuteStop(ctx, "stp_forge"); err == nil {
		t.Fatal("arrêt avec un identifiant forgé")
	}
	if _, err := s.ExecuteStop(ctx, stopID); err != nil {
		t.Fatal(err)
	}
	if fa.getState() != printer.StateIdle {
		t.Fatal("l'imprimante n'a pas reçu l'arrêt")
	}
	if cur, _ := s.GetJob(ctx, j.ID); cur.State != contracts.PrintCancelled {
		t.Fatalf("état : %s", cur.State)
	}
	if _, err := s.ExecuteStop(ctx, stopID); err == nil {
		t.Fatal("demande d'arrêt rejouée")
	}
}

// Point 11 : EnsureModels ne fige pas un échec (contexte annulé, Studio absent) : chaque appel réessaie.
func TestEnsureModelsRetriesAfterFailure(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	calls := 0
	s.d.Slicer = func() (*slicer.Slicer, error) { calls++; return nil, errors.New("Bambu Studio absent (simulé)") }
	for i := 1; i <= 3; i++ {
		if _, err := s.EnsureModels(context.Background()); err == nil {
			t.Fatal("slicer absent : erreur attendue")
		}
		if calls != i {
			t.Fatalf("appel %d : le slicer a été interrogé %d fois (un sync.Once aurait figé l'échec)", i, calls)
		}
	}
	if s.modelsDone {
		t.Fatal("échec mémorisé comme un succès")
	}
	if _, err := s.GetPrinterModel(context.Background(), "p1s"); err == nil || !strings.Contains(err.Error(), "indisponibles") {
		t.Fatalf("la raison de l'absence doit être rapportée : %v", err)
	}
}
