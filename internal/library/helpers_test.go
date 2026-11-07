package library

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/calibration"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/engineering"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// fakeSpecs : schémas minimaux reproduisant les règles « auto » des vrais gabarits.
const fakeSpecs = `{"templates":[
 {"id":"washer","version":"1.0.0","schema_hash":"aaa","title":"Rondelle","category":"rondelle-entretoise","summary":"s","description":"d","tags":[],
  "params":[{"name":"outer_diameter","type":"number","default":18,"min":3,"max":200,"unit":"mm","description":"x"},
            {"name":"inner_diameter","type":"number","default":6.6,"min":1,"max":150,"unit":"mm","description":"x","auto":"hole:vertical"},
            {"name":"thickness","type":"number","default":2,"min":0.4,"max":20,"unit":"mm","description":"x"}]},
 {"id":"hinge_pin","version":"1.0.0","schema_hash":"bbb","title":"Charnière à axe","category":"mécanisme","summary":"s","description":"d","tags":[],
  "params":[{"name":"length","type":"number","default":40,"min":10,"max":300,"unit":"mm","description":"x"},
            {"name":"hole_clearance","type":"number","default":0.3,"min":0.05,"max":2,"unit":"mm","description":"x","auto":"fit:glissant"},
            {"name":"axial_gap","type":"number","default":0.4,"min":0.1,"max":2,"unit":"mm","description":"x"}]},
 {"id":"hinge_print_in_place","version":"1.0.0","schema_hash":"ccc","title":"PIP","category":"mécanisme","summary":"s","description":"d","tags":[],
  "params":[{"name":"clearance","type":"number","default":0.3,"min":0.1,"max":1.5,"unit":"mm","description":"x","auto":"gap_pip"}]},
 {"id":"enclosure","version":"1.0.0","schema_hash":"ddd","title":"Boîtier","category":"boîtier","summary":"s","description":"d","tags":[],
  "params":[{"name":"lid_mode","type":"enum","default":"screw","options":["screw","clip"],"description":"x"},
            {"name":"screw_size","type":"enum","default":"M3","options":["M2","M3","M4"],"description":"x"},
            {"name":"lid_clearance","type":"number","default":0.25,"min":0.05,"max":1.5,"unit":"mm","description":"x","auto":"fitside:libre"},
            {"name":"insert_hole_diameter","type":"number","default":4,"min":1.5,"max":12,"unit":"mm","description":"x","auto":"insert:diameter"},
            {"name":"insert_depth","type":"number","default":7.2,"min":2,"max":20,"unit":"mm","description":"x","auto":"insert:depth"},
            {"name":"boss_diameter","type":"number","default":7.2,"min":3,"max":20,"unit":"mm","description":"x","auto":"insert:boss"}]},
 {"id":"snap_clip","version":"1.0.0","schema_hash":"eee","title":"Clip","category":"mécanisme","summary":"s","description":"d","tags":[],
  "params":[{"name":"beam_length","type":"number","default":18,"min":4,"max":100,"unit":"mm","description":"x"},
            {"name":"beam_thickness","type":"number","default":1.6,"min":0.8,"max":8,"unit":"mm","description":"x"},
            {"name":"beam_width","type":"number","default":8,"min":2,"max":40,"unit":"mm","description":"x"},
            {"name":"hook_depth","type":"number","default":1.2,"min":0.3,"max":6,"unit":"mm","description":"x"},
            {"name":"insertion_angle","type":"number","default":30,"min":10,"max":60,"unit":"deg","description":"x"}]}
]}`

type workerCall struct {
	Cmd string
	Req map[string]any
}

// fakeCatalog : worker simulé + enregistrement réel dans la base (pas de FK vers le catalogue SQL).
type fakeCatalog struct {
	t     *testing.T
	st    *store.Store
	mu    sync.Mutex
	calls []workerCall
	// buildResult complète le résultat de library_build ; nil = résultat standard.
	buildResult func(req map[string]any) map[string]any
	models      map[string]*contracts.Model
	versions    map[string][]contracts.ModelVersion
	failBuild   error
}

func newFakeCatalog(t *testing.T, st *store.Store) *fakeCatalog {
	return &fakeCatalog{t: t, st: st, models: map[string]*contracts.Model{}, versions: map[string][]contracts.ModelVersion{}}
}

func (f *fakeCatalog) last(cmd string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].Cmd == cmd {
			return f.calls[i].Req
		}
	}
	return nil
}

func (f *fakeCatalog) count(cmd string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.Cmd == cmd {
			n++
		}
	}
	return n
}

func (f *fakeCatalog) RunWorker(_ context.Context, cmd string, req map[string]any) (json.RawMessage, error) {
	f.mu.Lock()
	cp := map[string]any{}
	for k, v := range req {
		cp[k] = v
	}
	f.calls = append(f.calls, workerCall{cmd, cp})
	f.mu.Unlock()
	switch cmd {
	case "library_list":
		return json.RawMessage(fakeSpecs), nil
	case "library_thumbnail":
		return json.RawMessage(`{"svg":"<svg xmlns=\"http://www.w3.org/2000/svg\"><polygon points=\"0,0 1,0 1,1\"/></svg>"}`), nil
	case "library_build":
		if f.failBuild != nil {
			return nil, f.failBuild
		}
		out := req["out_dir"].(string)
		name := req["name"].(string)
		for _, ext := range []string{".stl", ".step", ".thumb.svg"} {
			if err := os.WriteFile(filepath.Join(out, name+ext), []byte("contenu "+name+ext), 0o644); err != nil {
				f.t.Fatal(err)
			}
		}
		res := map[string]any{
			"template": req["template"], "template_version": "1.0.0", "params": req["params"],
			"parts":       []map[string]any{{"name": "p1", "volume_mm3": 123.4, "solids": 1, "valid": true, "tessellation": map[string]any{"triangles": 12, "watertight": true}}},
			"assumptions": []string{"hyp"}, "warnings": []string{}, "bbox_mm": map[string]any{"size": []float64{1, 2, 3}},
			"derived": map[string]any{}, "level": "calculé",
			"files": map[string]string{"stl": filepath.Join(out, name+".stl"), "step": filepath.Join(out, name+".step"), "thumbnail": filepath.Join(out, name+".thumb.svg")},
		}
		if f.buildResult != nil {
			res = f.buildResult(req)
		}
		b, _ := json.Marshal(res)
		return b, nil
	}
	return nil, fmt.Errorf("commande inattendue %s", cmd)
}

func (f *fakeCatalog) RegisterGenerated(_ context.Context, _ string, m contracts.Model, parent, label, note string, files []catalog.GeneratedFile) (contracts.Model, contracts.ModelVersion, []contracts.SourceFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m.ID == "" {
		m.ID = contracts.NewID("mdl")
		m.CreatedAt = time.Now()
		cp := m
		f.models[m.ID] = &cp
	}
	ver := contracts.ModelVersion{ID: contracts.NewID("ver"), ModelID: m.ID, Label: label, Note: note, CreatedAt: time.Now().Add(time.Duration(len(f.versions[m.ID])) * time.Second)}
	if parent != "" {
		p := parent
		ver.ParentID = &p
	}
	f.versions[m.ID] = append(f.versions[m.ID], ver)
	var sf []contracts.SourceFile
	for _, g := range files {
		sf = append(sf, contracts.SourceFile{ID: contracts.NewID("src"), ModelID: m.ID, Name: g.Name, Format: g.Format, SHA256: g.SHA256, SizeBytes: g.Size})
	}
	return m, ver, sf, nil
}

func (f *fakeCatalog) GetModelDetails(_ context.Context, id string) (*catalog.ModelDetails, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.models[id]
	if !ok {
		return nil, fmt.Errorf("modèle %s : %w", id, store.ErrNotFound)
	}
	return &catalog.ModelDetails{Model: *m, Versions: append([]contracts.ModelVersion{}, f.versions[id]...)}, nil
}

// fakeCal : calibration fixe renvoyée pour toute clé.
type fakeCal struct {
	cal *engineering.Calibration
	got []calibration.Key
}

func (f *fakeCal) Resolve(_ context.Context, k calibration.Key) (*calibration.Resolution, error) {
	nk, err := k.Normalize()
	if err != nil {
		return nil, err
	}
	f.got = append(f.got, nk)
	if f.cal == nil {
		return &calibration.Resolution{Matched: []string{}, Note: "Aucune calibration pour " + nk.Label()}, nil
	}
	return &calibration.Resolution{Calibration: f.cal, Matched: []string{nk.Label()}, Note: "Calibration appliquée : " + f.cal.Provenance}, nil
}

type rig struct {
	svc  *Service
	fc   *fakeCatalog
	cal  *fakeCal
	st   *store.Store
	blob *store.BlobStore
}

func newRig(t *testing.T, cal *engineering.Calibration) *rig {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bl, err := store.NewBlobStore(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	st.AttachBlobs(bl)
	fc := newFakeCatalog(t, st)
	fcal := &fakeCal{cal: cal}
	svc, err := New(Deps{Store: st, Blobs: bl, Catalog: fc, Calibrations: fcal, WorkRoot: filepath.Join(dir, "work")})
	if err != nil {
		t.Fatal(err)
	}
	return &rig{svc: svc, fc: fc, cal: fcal, st: st, blob: bl}
}

func f64(v float64) *float64 { return &v }

func contractsModel(name, note string) contracts.Model {
	return contracts.Model{Name: name, Provenance: &contracts.Provenance{Source: "generation parametrique", Note: note}}
}

func sentParams(r *rig) map[string]any { return r.fc.last("library_build")["params"].(map[string]any) }
