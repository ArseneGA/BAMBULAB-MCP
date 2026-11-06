package contracts

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestHappyPathStates(t *testing.T) {
	steps := []struct {
		ev   PrintEvent
		want PrintState
	}{
		{EvPrepare, PrintPrepared},
		{EvStartSlice, PrintSlicing},
		{EvSliceOK, PrintValidated},
		{EvStartUpload, PrintUploading},
		{EvUploadOK, PrintUploaded},
		{EvRequestConfirmation, PrintAwaitingConfirmation},
		{EvConfirm, PrintStartRequested},
		{EvObservedPrinting, PrintPrinting},
		{EvPrintDone, PrintCompleted},
	}
	s := PrintDraft
	for _, st := range steps {
		next, err := Transition(s, st.ev)
		if err != nil || next != st.want {
			t.Fatalf("%s --%s--> attendu %s, obtenu %s (%v)", s, st.ev, st.want, next, err)
		}
		s = next
	}
}

func TestIllegalTransitions(t *testing.T) {
	cases := []struct {
		s PrintState
		e PrintEvent
	}{
		{PrintDraft, EvConfirm},              // pas de départ sans parcours complet
		{PrintPrepared, EvConfirm},           //
		{PrintUploaded, EvConfirm},           // confirmation requise d'abord
		{PrintValidated, EvObservedPrinting}, //
		{PrintCompleted, EvCancel},           // terminal
		{PrintFailed, EvPrepare},             // terminal
		{PrintCancelled, EvStartSlice},       // terminal
		{PrintStartUnknown, EvCancel},        // jamais annulable à l'aveugle
		{PrintStartUnknown, EvConfirm},       // pas de relance sans réconciliation
		{PrintStartRequested, EvConfirm},     // pas de double départ
		{PrintPrinting, EvConfirm},
		{PrintState("INVENTE"), EvPrepare},
	}
	for _, c := range cases {
		next, err := Transition(c.s, c.e)
		if !errors.Is(err, ErrIllegalTransition) {
			t.Errorf("%s --%s--> devrait être refusée (obtenu %s, %v)", c.s, c.e, next, err)
		}
		if next != c.s {
			t.Errorf("l'état ne doit pas changer sur refus : %s -> %s", c.s, next)
		}
	}
}

func TestExhaustiveTableSanity(t *testing.T) {
	// Aucun état terminal n'a de sortie ; toute cible existe comme état connu.
	known := map[PrintState]bool{}
	for k, v := range printTransitions {
		known[k.s], known[v] = true, true
	}
	for _, s := range []PrintState{PrintCompleted, PrintFailed, PrintCancelled} {
		if len(AllowedEvents(s)) != 0 {
			t.Errorf("%s est terminal mais a des sorties", s)
		}
	}
	// START_UNKNOWN : seule la réconciliation sort.
	got := map[PrintEvent]bool{}
	for _, e := range AllowedEvents(PrintStartUnknown) {
		got[e] = true
	}
	if !got[EvReconcilePrinting] || !got[EvReconcileNotStarted] || got[EvConfirm] || got[EvCancel] {
		t.Errorf("sorties de START_UNKNOWN incorrectes : %v", got)
	}
	// reconcile_not_started repasse par UPLOADED -> nouvelle confirmation obligatoire.
	n, _ := Transition(PrintStartUnknown, EvReconcileNotStarted)
	if n != PrintUploaded {
		t.Errorf("attendu UPLOADED, obtenu %s", n)
	}
}

func TestPrintJobApply(t *testing.T) {
	p := &PrintJob{State: PrintDraft}
	now := time.Now()
	if err := p.Apply(EvPrepare, now); err != nil || p.State != PrintPrepared || !p.UpdatedAt.Equal(now) {
		t.Fatalf("apply: %v %+v", err, p)
	}
	if err := p.Apply(EvConfirm, now); err == nil {
		t.Fatal("transition illégale acceptée")
	}
	if p.State != PrintPrepared {
		t.Fatal("état modifié malgré le refus")
	}
}

func binding() ConfirmationBinding {
	return ConfirmationBinding{
		PrinterID: "p1s-1", ArtifactSHA256: "aa", Plate: 1,
		Materials: []SlotAssignment{{Slot: "ams1:1", MaterialID: "mat1"}, {Slot: "ams1:2", MaterialID: "mat2"}},
	}
}

func TestConfirmationOK(t *testing.T) {
	now := time.Now()
	c, tok, err := NewConfirmation("c1", "j1", binding(), time.Minute, now)
	if err != nil || tok == "" {
		t.Fatal(err)
	}
	if c.TokenHash == tok {
		t.Fatal("le jeton ne doit pas être stocké en clair")
	}
	// ordre des matières sans importance
	b := binding()
	b.Materials[0], b.Materials[1] = b.Materials[1], b.Materials[0]
	if err := c.Consume(tok, b, now.Add(time.Second)); err != nil {
		t.Fatalf("consume: %v", err)
	}
	// usage unique
	if err := c.Consume(tok, binding(), now.Add(2*time.Second)); !errors.Is(err, ErrConfirmationUsed) {
		t.Fatalf("attendu usage unique, obtenu %v", err)
	}
}

func TestConfirmationInvalidations(t *testing.T) {
	now := time.Now()
	mutations := map[string]func(*ConfirmationBinding){
		"imprimante": func(b *ConfirmationBinding) { b.PrinterID = "autre" },
		"artefact":   func(b *ConfirmationBinding) { b.ArtifactSHA256 = "bb" },
		"plateau":    func(b *ConfirmationBinding) { b.Plate = 2 },
		"matière":    func(b *ConfirmationBinding) { b.Materials[0].MaterialID = "autre" },
		"slot":       func(b *ConfirmationBinding) { b.Materials[0].Slot = "ext" },
		"matière retirée": func(b *ConfirmationBinding) {
			b.Materials = b.Materials[:1]
		},
	}
	for name, mut := range mutations {
		c, tok, _ := NewConfirmation("c", "j", binding(), time.Minute, now)
		b := binding()
		mut(&b)
		if err := c.Consume(tok, b, now); !errors.Is(err, ErrConfirmationMismatch) {
			t.Errorf("%s : attendu mismatch, obtenu %v", name, err)
		}
		if c.UsedAt != nil {
			t.Errorf("%s : ne doit pas être marquée utilisée après échec", name)
		}
	}
	c, tok, _ := NewConfirmation("c", "j", binding(), time.Minute, now)
	if err := c.Consume("faux", binding(), now); !errors.Is(err, ErrConfirmationToken) {
		t.Errorf("mauvais jeton : %v", err)
	}
	if err := c.Consume(tok, binding(), now.Add(time.Minute)); !errors.Is(err, ErrConfirmationExpired) {
		t.Errorf("expiration : %v", err)
	}
	c2, t2, _ := NewConfirmation("c", "j", binding(), time.Minute, now)
	if t2 == tok {
		t.Error("jetons identiques : aléa défaillant")
	}
	_ = c2
}

func TestSchemas(t *testing.T) {
	type in struct {
		JobID string `json:"job_id" jsonschema:"identifiant du travail"`
		Tail  int    `json:"tail,omitempty"`
	}
	s, err := SchemaFor[in]()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(s)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if m["type"] != "object" {
		t.Fatalf("type: %v", m["type"])
	}
	req, _ := m["required"].([]any)
	if len(req) != 1 || req[0] != "job_id" {
		t.Fatalf("required: %v", req)
	}
	// Types du domaine : doivent tous produire un schéma (RawMessage, time, pointeurs).
	_ = MustSchemaFor[Job]()
	_ = MustSchemaFor[PrinterModel]()
	_ = MustSchemaFor[PrintJob]()
	_ = MustSchemaFor[PrintConfirmation]()
	_ = MustSchemaFor[ManufacturingGoal]()
	_ = MustSchemaFor[Material]()
	_ = MustSchemaFor[ModelVersion]()
	_ = MustSchemaFor[SourceFile]()
}

func TestGoalEvaluable(t *testing.T) {
	f := 80.0
	g := ManufacturingGoal{OriginalText: "rapide et solide", Criteria: []Criterion{
		{Metric: "print_time_s", Op: OpMinimize},
		{Metric: "wall_count", Op: OpAtLeast, Target: &f},
	}}
	if !g.RecomputeEvaluable() {
		t.Fatal("devrait être évaluable")
	}
	g.Criteria = append(g.Criteria, Criterion{Metric: "joli", Op: OpQualitative})
	if g.RecomputeEvaluable() {
		t.Fatal("un critère qualitatif rend l'objectif non évaluable")
	}
	g.Criteria = []Criterion{{Metric: "x", Op: OpAtMost}}
	if g.RecomputeEvaluable() {
		t.Fatal("cible manquante")
	}
	empty := ManufacturingGoal{}
	if empty.RecomputeEvaluable() {
		t.Fatal("sans critère : non évaluable")
	}
}

func TestSeedPrinterUnknownsNil(t *testing.T) {
	p := SeedPrinterModels()[0]
	if p.MaxBedTempC != nil || p.AMSUnitsMax != nil || p.Camera != nil {
		t.Fatal("les inconnus doivent rester nil")
	}
}
