package insights

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/goals"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

var t0 = time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)

type fakeGoals struct {
	got []goals.ResultInput
	err error
}

func (f *fakeGoals) RecordResult(_ context.Context, in goals.ResultInput) (*goals.PrintResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.got = append(f.got, in)
	return &goals.PrintResult{ID: "res_fake", ExperimentID: in.ExperimentID, CandidateID: in.CandidateID}, nil
}

type env struct {
	t   *testing.T
	st  *store.Store
	bl  *store.BlobStore
	svc *Service
	fg  *fakeGoals
	now time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "bambu.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bl, err := store.NewBlobStore(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, st: st, bl: bl, fg: &fakeGoals{}, now: t0.Add(30 * 24 * time.Hour)}
	e.svc, err = New(Config{Store: st, Blobs: bl, Goals: e.fg, Now: func() time.Time { return e.now }})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.st.DB().Exec(q, args...); err != nil {
		e.t.Fatalf("%s : %v", q, err)
	}
}

func (e *env) seedPrinter(id, modelID, name string) {
	e.t.Helper()
	if err := e.st.PutDoc(context.Background(), "test", "printers", id, map[string]any{"id": id, "model_id": modelID, "name": name}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) seedModel(modelID, name, colID, colName string) {
	e.t.Helper()
	ts := store.FormatTime(t0)
	if colID != "" {
		e.exec(`INSERT OR IGNORE INTO collections(id,name,created_at) VALUES(?,?,?)`, colID, colName, ts)
	}
	var cid any
	if colID != "" {
		cid = colID
	}
	e.exec(`INSERT INTO models(id,collection_id,name,created_at,updated_at) VALUES(?,?,?,?,?)`, modelID, cid, name, ts, ts)
}

// jobSpec décrit une impression de test.
type jobSpec struct {
	ID        string
	State     string
	Printer   string
	Model     string
	Seconds   float64
	Grams     float64
	Type      string
	Start     time.Time
	End       time.Time
	Percent   int
	Preset    string
	Overrides []slicer.Override
}

func (e *env) seedJob(js jobSpec) {
	e.t.Helper()
	if js.Type == "" {
		js.Type = "PLA"
	}
	if js.Preset == "" {
		js.Preset = "Bambu " + js.Type + " Basic @BBL P1S 0.4 nozzle"
	}
	events := []map[string]any{}
	if !js.Start.IsZero() {
		events = append(events, map[string]any{"at": js.Start, "event": "started", "from": "START_REQUESTED", "to": "PRINTING"})
	}
	if !js.End.IsZero() {
		events = append(events, map[string]any{"at": js.End, "event": "finished", "from": "PRINTING", "to": js.State})
	}
	doc := map[string]any{
		"id": js.ID, "state": js.State, "printer_id": js.Printer, "model_id": js.Model, "name": js.ID + ".gcode.3mf", "plate": 1,
		"assignments": []map[string]any{{"filament_slot": 1, "tray": "ams1:1", "material_id": "Bambu " + js.Type + " Basic noir"}},
		"summary": map[string]any{
			"plates": []slicer.PlateResult{{Index: 1, PredictedSeconds: js.Seconds, WeightGrams: js.Grams, Layers: 100,
				Filaments: []slicer.FilamentUse{{Slot: 1, Type: js.Type, Grams: js.Grams}}}},
			"printer_model": "Bambu Lab P1S", "machine_preset": "Bambu Lab P1S 0.4 nozzle", "process": "0.20mm Standard @BBL X1C",
			"filament_presets": []string{js.Preset},
		},
		"print":      map[string]any{"percent": js.Percent},
		"events":     events,
		"slice":      map[string]any{"machine": "Bambu Lab P1S 0.4 nozzle", "process": "0.20mm Standard @BBL X1C", "filaments": []string{js.Preset}, "nozzle_mm": 0.4, "overrides": js.Overrides},
		"created_at": t0, "updated_at": js.End,
	}
	if err := e.st.PutDoc(context.Background(), "test", "print_jobs", js.ID, doc); err != nil {
		e.t.Fatal(err)
	}
}

// done enregistre une impression terminée de 2 h pour 100 g de PLA, le jour donné.
func (e *env) done(id string, day int, state string) {
	start := t0.AddDate(0, 0, day)
	e.seedJob(jobSpec{ID: id, State: state, Printer: "pr1", Model: "mod1", Seconds: 7200, Grams: 100, Start: start, End: start.Add(2 * time.Hour), Percent: 100})
}

var png1x1 = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0}
