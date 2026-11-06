package printing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

func awaitingJob(t *testing.T, s *Service, state contracts.PrintState) Job {
	t.Helper()
	j, err := s.createJob(context.Background(), Job{State: state, PrinterID: "prn_t", ArtifactSHA256: strings.Repeat("c", 64), ArtifactSize: 10, Name: "x.gcode.3mf", Plate: 1,
		Assignments: []FilamentAssignment{{1, "ext", "PLA"}}, Remote: &printer.RemoteRef{Name: "x-cccccccccccc.gcode.3mf", Size: 10}})
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestConfirmationTokenNeverPersistedNorReturned(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	ctx := context.Background()
	j := awaitingJob(t, s, contracts.PrintAwaitingConfirmation)
	v, err := s.RequestConfirmation(ctx, RequestConfirmationInput{PrintJobID: j.ID})
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	token := s.approv[v.ConfirmationID].token
	s.mu.Unlock()
	if len(token) < 40 {
		t.Fatalf("jeton trop court : %d", len(token))
	}
	// ni dans la vue renvoyée au modèle, ni dans la base (documents, audit), ni dans aucun fichier
	if strings.Contains(strings.ToLower(jsonOf(t, v)), strings.ToLower(token)) {
		t.Fatal("jeton dans la vue")
	}
	filepath.Walk(s.d.DataDir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), token) {
				t.Errorf("jeton en clair dans %s", p)
			}
		}
		return nil
	})
	// Un booléen « confirmé » fourni par l'appelant n'existe nulle part : sans approbation humaine, rien ne passe.
	if _, err := s.approvedFor(v.ConfirmationID, j.ID); !errors.Is(err, ErrNeedsApproval) {
		t.Fatalf("approbation attendue : %v", err)
	}
	// Approuver sans l'attestation « plateau vide » : refus.
	if _, err := s.Approve(ctx, v.ConfirmationID, false, "web"); !errors.Is(err, ErrNeedsApproval) {
		t.Fatalf("sans plateau vide : %v", err)
	}
	if _, err := s.Approve(ctx, v.ConfirmationID, true, "web"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.approvedFor(v.ConfirmationID, "pj_autre"); err == nil {
		t.Fatal("approbation valable pour une autre impression")
	}
	if _, err := s.approvedFor(v.ConfirmationID, j.ID); err != nil {
		t.Fatal(err)
	}
}

func TestConfirmationInvalidatedWhenBindingChanges(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	ctx := context.Background()
	for name, mutate := range map[string]func(*Job){
		"plateau":     func(j *Job) { j.Plate = 2 },
		"matière":     func(j *Job) { j.Assignments = []FilamentAssignment{{1, "ext", "PETG"}} },
		"emplacement": func(j *Job) { j.Assignments = []FilamentAssignment{{1, "ams1:1", "PLA"}} },
		"empreinte":   func(j *Job) { j.ArtifactSHA256 = strings.Repeat("d", 64) },
		"imprimante":  func(j *Job) { j.PrinterID = "prn_autre" },
	} {
		j := awaitingJob(t, s, contracts.PrintAwaitingConfirmation)
		v, err := s.RequestConfirmation(ctx, RequestConfirmationInput{PrintJobID: j.ID})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.update(ctx, j.ID, func(x *Job) error { mutate(x); return nil }); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Approve(ctx, v.ConfirmationID, true, "web"); !errors.Is(err, contracts.ErrConfirmationMismatch) {
			t.Errorf("%s modifié : approbation attendue refusée (mismatch), reçu %v", name, err)
		}
		// Et ConsumeConfirmation refuse la liaison courante même avec le bon jeton.
		s.mu.Lock()
		tok := s.approv[v.ConfirmationID].token
		s.mu.Unlock()
		cur, _ := s.GetJob(ctx, j.ID)
		if err := s.d.Store.ConsumeConfirmation(ctx, v.ConfirmationID, tok, newBinding(cur), j.ID); !errors.Is(err, contracts.ErrConfirmationMismatch) {
			t.Errorf("%s modifié : ConsumeConfirmation devrait refuser, reçu %v", name, err)
		}
	}
}

func TestNewConfirmationSupersedesTheOld(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	ctx := context.Background()
	j := awaitingJob(t, s, contracts.PrintAwaitingConfirmation)
	a, _ := s.RequestConfirmation(ctx, RequestConfirmationInput{PrintJobID: j.ID})
	b, _ := s.RequestConfirmation(ctx, RequestConfirmationInput{PrintJobID: j.ID})
	if _, err := s.Approve(ctx, a.ConfirmationID, true, "web"); err == nil {
		t.Fatal("l'ancienne confirmation ne doit plus pouvoir être approuvée")
	}
	if _, err := s.Approve(ctx, b.ConfirmationID, true, "web"); err != nil {
		t.Fatal(err)
	}
	// Expiration : horloge avancée au-delà du TTL.
	s.d.Now = func() time.Time { return time.Now().Add(time.Hour) }
	if _, err := s.Approve(ctx, b.ConfirmationID, true, "web"); !errors.Is(err, contracts.ErrConfirmationExpired) {
		t.Fatalf("expiration : %v", err)
	}
}

func TestRecoverTurnsInterruptedStartIntoUnknown(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	ctx := context.Background()
	j := awaitingJob(t, s, contracts.PrintStartRequested)
	s.Recover(ctx)
	got, _ := s.GetJob(ctx, j.ID)
	if got.State != contracts.PrintStartUnknown || !strings.Contains(got.LastError, "reconcile_print") {
		t.Fatalf("après redémarrage : %s (%s)", got.State, got.LastError)
	}
	// Une impression START_UNKNOWN ne se relance pas et ne s'annule pas à l'aveugle.
	out, err := s.CancelPrint(ctx, CancelInput{PrintJobID: j.ID})
	if err != nil || out.Outcome != "refused" {
		t.Fatalf("annulation : %v %v", out, err)
	}
	if _, err := s.StartPrint(ctx, StartInput{PrintJobID: j.ID, ConfirmationID: "cf_x", IdempotencyKey: "cle-de-test-123"}); !errors.Is(err, ErrState) {
		t.Fatalf("StartPrint sur START_UNKNOWN : %v", err)
	}
}

func TestStateMachineGuardsOperations(t *testing.T) {
	s, done := newBareService(t)
	defer done()
	ctx := context.Background()
	j := awaitingJob(t, s, contracts.PrintPrepared)
	if _, err := s.RequestConfirmation(ctx, RequestConfirmationInput{PrintJobID: j.ID}); !errors.Is(err, ErrState) {
		t.Fatalf("confirmation avant dépôt : %v", err)
	}
	if _, err := s.UploadArtifact(ctx, UploadInput{PrintJobID: j.ID, PrinterID: "prn_t", IdempotencyKey: "cle-upload-1234"}); err == nil {
		t.Fatal("dépôt avant validation")
	}
	if _, err := s.Reconcile(ctx, j.ID); !errors.Is(err, ErrState) {
		t.Fatalf("réconciliation hors START_UNKNOWN : %v", err)
	}
	// transition illégale refusée par la machine d'états du contrat
	if _, err := s.transition(ctx, j.ID, contracts.EvConfirm, "", nil); !errors.Is(err, ErrState) {
		t.Fatalf("transition illégale : %v", err)
	}
	// annulation d'une impression PREPARED
	out, err := s.CancelPrint(ctx, CancelInput{PrintJobID: j.ID})
	if err != nil || out.State != contracts.PrintCancelled {
		t.Fatalf("annulation : %v %v", out, err)
	}
	if out, _ := s.CancelPrint(ctx, CancelInput{PrintJobID: j.ID}); out.Outcome != "refused" {
		t.Fatal("double annulation")
	}
}

func TestSeedPrinterModelsFromProfiles(t *testing.T) {
	ps := loadFixtureProfiles(t)
	ms, err := SeedPrinterModels(ps)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) < 1 {
		t.Fatal("aucun modèle semé depuis les profils de test")
	}
	for _, m := range ms {
		if m.Vendor != "Bambu Lab" || m.ID == "" || strings.Contains(m.ID, " ") {
			t.Errorf("modèle : %+v", m)
		}
		// valeurs non fournies par les profils : inconnues (nil), jamais inventées
		if m.Enclosed != nil || m.AMSSupported != nil || m.MaxNozzleTempC != nil || m.Camera != nil {
			t.Errorf("%s : champs inventés : %+v", m.ID, m)
		}
		if m.Source == "" {
			t.Errorf("%s : source non renseignée", m.ID)
		}
	}
	if modelSlug("Bambu Lab A1 mini") != "a1_mini" || modelSlug("Bambu Lab P1S") != "p1s" || shortName("Bambu Lab P1S") != "P1S" {
		t.Fatal("identifiants de modèle")
	}
}
