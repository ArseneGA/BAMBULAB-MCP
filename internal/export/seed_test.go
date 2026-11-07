package export_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/insights"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

func (e *env) printer(host, serial string) string {
	id := contracts.NewID("prn")
	doc := map[string]any{"id": id, "model_id": "p1s", "name": "P1S atelier", "host": host, "serial": serial, "nozzle_mm": 0.4, "created_at": e.next()}
	if err := e.c.Store.PutDoc(context.Background(), "test", "printers", id, doc); err != nil {
		e.t.Fatal(err)
	}
	return id
}

type jobOpt struct {
	State     string
	Validated bool
	Artifact  []byte
	Name      string
	Profile   string
	Overrides []map[string]any
}

// printJob insère une impression découpée pour un modèle/version ; renvoie son id.
func (e *env) printJob(printer, model, version string, o jobOpt) string {
	id := contracts.NewID("pj")
	sha := e.blob(o.Artifact)
	if o.State == "" {
		o.State = "VALIDATED"
	}
	if o.Name == "" {
		o.Name = "piece.gcode.3mf"
	}
	if o.Overrides == nil {
		o.Overrides = []map[string]any{{"key": "sparse_infill_density", "value": "25%", "target": "process"}}
	}
	doc := map[string]any{
		"id": id, "state": o.State, "printer_id": printer, "model_id": model, "version_id": version, "profile_revision": o.Profile, "name": o.Name,
		"artifact_sha256": sha, "artifact_size": len(o.Artifact), "plate": 1,
		"summary": map[string]any{
			"plates": []any{map[string]any{"index": 1, "predicted_seconds": 5400, "weight_grams": 22.5, "layers": 120,
				"filaments": []any{map[string]any{"slot": 1, "type": "PLA", "used_g": 22.5}}}},
			"printer_model": "Bambu Lab P1S", "machine_preset": "Bambu Lab P1S 0.4 nozzle", "process": "0.20mm Standard @BBL X1C", "filament_presets": []string{"Bambu PLA Basic @BBL X1C"},
		},
		"validation": map[string]any{"ok": o.Validated},
		"slice": map[string]any{"machine": "Bambu Lab P1S 0.4 nozzle", "nozzle_mm": 0.4, "process": "0.20mm Standard @BBL X1C",
			"filaments": []string{"Bambu PLA Basic @BBL X1C"}, "overrides": o.Overrides},
		"created_at": e.next(), "updated_at": e.next(),
	}
	if err := e.c.Store.PutDoc(context.Background(), "test", "print_jobs", id, doc); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// template insère la trace d'une pièce générée par un gabarit.
func (e *env) template(model, version, tpl string) {
	e.exec(`INSERT INTO part_template_models(version_id,model_id,template,variant_id,template_version,params,requested,calcs,calibration,thumbnail_sha,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		version, model, tpl, nil, "1.1.0", `{"outer_diameter":18,"inner_diameter":6.6}`, `{"outer_diameter":18}`,
		`[{"name":"jeu","value":0.2,"unit":"mm"}]`, `{"printer_key":"p1s","access_code":"87654321"}`, nil, e.next())
}

// appliedProfile insère une lignée de deux révisions de profil et la marque appliquée au modèle.
func (e *env) appliedProfile(model, version string, extra map[string]any) string {
	parent, rev := contracts.NewID("prof"), contracts.NewID("prof")
	base := map[string]any{"machine": "Bambu Lab P1S 0.4 nozzle", "process": "0.20mm Standard @BBL X1C", "filaments": []string{"Bambu PLA Basic @BBL X1C"}, "created_at": e.next()}
	pd := map[string]any{"id": parent, "overrides": []any{}}
	rd := map[string]any{"id": rev, "parent_id": parent, "note": "plus de parois",
		"overrides": []any{
			map[string]any{"key": "wall_loops", "value": 4, "target": "process"},
			map[string]any{"key": "sparse_infill_density", "value": "30%", "target": "process"},
			map[string]any{"key": "machine_start_gcode", "value": "M104 S250 ; secret", "target": "machine"},
			map[string]any{"key": "notes", "value": `voir D:\perso\reglages.txt`, "target": "process"},
		},
		"orientation": map[string]any{"mode": "auto", "label": "à plat sur la face large", "up": []float64{0, 0, 1}, "rotation": make([]float64, 12), "height_mm": 12}}
	for _, d := range []map[string]any{pd, rd} {
		for k, v := range base {
			d[k] = v
		}
		for k, v := range extra {
			d[k] = v
		}
		raw, _ := json.Marshal(d)
		pid, _ := d["id"].(string)
		var par any
		if p, ok := d["parent_id"].(string); ok {
			par = p
		}
		e.exec(`INSERT INTO slicing_profiles(id,parent_id,doc,created_at) VALUES(?,?,?,?)`, pid, par, string(raw), e.next())
	}
	e.exec(`INSERT INTO applied_variants(id,model_id,model_version_id,experiment_id,candidate_id,profile_id,applied_at) VALUES(?,?,?,?,?,?,?)`,
		contracts.NewID("av"), model, version, "exp1", "cand1", rev, e.next())
	return rev
}

// jpegWithEXIF : petit JPEG portant un segment EXIF (position fictive) à retirer.
func jpegWithEXIF() []byte {
	exif := append([]byte("Exif\x00\x00"), []byte("GPSLATITUDE48.85")...)
	b := []byte{0xFF, 0xD8, 0xFF, 0xE1, byte((len(exif) + 2) >> 8), byte((len(exif) + 2) & 0xFF)}
	b = append(b, exif...)
	b = append(b, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0, 1, 1, 0, 0, 1, 0, 1, 0, 0)
	b = append(b, 0xFF, 0xDA, 0x00, 0x08, 1, 1, 0, 0, 0x3F, 0, 0x11, 0x22, 0x33, 0xFF, 0xD9)
	return b
}

func (e *env) journalPhoto(jobModel string, photo []byte) {
	e.t.Helper()
	ctx := context.Background()
	out, err := e.c.Insights.ListLogs(ctx, insights.ListQuery{ModelID: jobModel, Limit: 5})
	if err != nil || len(out.Entries) == 0 {
		e.t.Fatalf("journal : %v %d", err, len(out.Entries))
	}
	if _, _, err := e.c.Insights.AddPhoto(ctx, out.Entries[0].ID, strings.NewReader(string(photo))); err != nil {
		e.t.Fatal(err)
	}
}

var _ = fmt.Sprint
var _ = store.ErrNotFound
