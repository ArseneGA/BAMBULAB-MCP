package printing_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer/sim"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

const simAccessCode = "Zx9-ACCESS-CODE-77"

type rig struct {
	t         *testing.T
	c         *core.Core
	web       *httptest.Server
	cs        *mcp.ClientSession
	sim       *sim.Sim
	dataDir   string
	printerID string
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func startSim(t *testing.T, faults ...string) *sim.Sim {
	t.Helper()
	s, err := sim.New(sim.Config{ControlPort: -1, AccessCode: simAccessCode, Faults: faults,
		Tick: 20 * time.Millisecond, HeartbeatInterval: 150 * time.Millisecond, PrepareDuration: 150 * time.Millisecond, PrintDuration: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// fakeWorker remplace le worker Python : génère un STL de boîte depuis des paramètres.
func fakeWorker(_ context.Context, cmd string, req map[string]any) (json.RawMessage, error) {
	if cmd != "parametric" {
		return nil, fmt.Errorf("commande %s", cmd)
	}
	out := req["out_dir"].(string)
	name := req["name"].(string)
	p, _ := req["params"].(map[string]any)
	x, _ := p["inner_x"].(float64)
	if x <= 0 {
		return nil, &catalog.WorkerError{Code: "invalid_params", Message: "inner_x requis"}
	}
	b, err := stlBox(x, x, x)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(out, name+".stl"), b, 0o644); err != nil {
		return nil, err
	}
	return json.RawMessage(fmt.Sprintf(`{"template":%q,"params":{"inner_x":%v},"assumptions":["boîtier fictif de test"],"warnings":[],"bbox_mm":{"x":%v},"level":"calculé","files":{"stl":%q,"step":""}}`,
		req["template"], x, x, name+".stl")), nil
}

func newRig(t *testing.T, studio string) *rig {
	t.Helper()
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	c, err := core.Open(ctx, core.Config{DataDir: dir, Logger: quiet(), StudioPath: studio, Catalog: catalog.Config{Worker: fakeWorker},
		PollInterval: 20 * time.Millisecond, HeartbeatInterval: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	web := httptest.NewServer(httpapi.NewHandler(c))
	c.Catalog.SetBaseURL(web.URL)
	ct, st := mcp.NewInMemoryTransports()
	ss, err := mcpserver.New(c).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cs.Close()
		ss.Close()
		web.Close()
		cancel()
		<-done
		c.Close()
	})
	return &rig{t: t, c: c, web: web, cs: cs, dataDir: dir}
}

// call appelle un outil et exige le succès ; renvoie le contenu structuré.
func (r *rig) call(name string, args map[string]any) map[string]any {
	r.t.Helper()
	m, txt, isErr := r.raw(name, args)
	if isErr {
		r.t.Fatalf("%s en erreur : %s", name, txt)
	}
	return m
}

// callErr appelle un outil et exige une erreur ; renvoie son texte.
func (r *rig) callErr(name string, args map[string]any) string {
	r.t.Helper()
	_, txt, isErr := r.raw(name, args)
	if !isErr {
		r.t.Fatalf("%s aurait dû échouer", name)
	}
	return txt
}

func (r *rig) raw(name string, args map[string]any) (map[string]any, string, bool) {
	r.t.Helper()
	res, err := r.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, err.Error(), true
	}
	var txt strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			txt.WriteString(tc.Text + "\n")
		}
	}
	var m map[string]any
	if res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(b, &m)
	}
	return m, txt.String(), res.IsError
}

func str(m map[string]any, path ...string) string {
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[p]
	}
	s, _ := cur.(string)
	return s
}

func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("délai dépassé : %s", what)
}

func (r *rig) printState(id string) string {
	m := r.call("get_print_status", map[string]any{"print_job_id": id})
	return str(m, "job", "state")
}

func (r *rig) waitState(id string, d time.Duration, want ...string) string {
	r.t.Helper()
	var last string
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		last = r.printState(id)
		for _, w := range want {
			if last == w {
				return last
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	st := r.call("get_print_status", map[string]any{"print_job_id": id})
	r.t.Fatalf("impression %s : état %s au lieu de %v ; dernière erreur %q ; imprimante %v ; progression %v", id, last, want, str(st, "job", "last_error"), st["printer"], st["job"].(map[string]any)["print"])
	return last
}

var nonceRe = regexp.MustCompile(`name="nonce" value="([^"]+)"`)

// humanGet ouvre une page comme un navigateur et en extrait le nonce du formulaire.
func (r *rig) humanGet(pageURL string) (status int, body, nonce string) {
	r.t.Helper()
	res, err := http.Get(pageURL)
	if err != nil {
		r.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if m := nonceRe.FindSubmatch(b); m != nil {
		nonce = string(m[1])
	}
	return res.StatusCode, string(b), nonce
}

func (r *rig) humanPost(pageURL string, form url.Values) (int, string) {
	r.t.Helper()
	res, err := http.PostForm(pageURL, form)
	if err != nil {
		r.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// humanConfirm : l'humain ouvre la page, coche (ou non) « plateau vide » et clique Lancer.
func (r *rig) humanConfirm(confirmURL string, plateClear bool) (int, string) {
	r.t.Helper()
	st, body, nonce := r.humanGet(confirmURL)
	if st != 200 || nonce == "" {
		r.t.Fatalf("page de confirmation : %d %s", st, body)
	}
	f := url.Values{"nonce": {nonce}}
	if plateClear {
		f.Set("plate_clear", "yes")
	}
	return r.humanPost(confirmURL, f)
}

func (r *rig) seedPrinterModel() {
	r.t.Helper()
	yes := true
	err := r.c.Printing.PutPrinterModel(context.Background(), contracts.PrinterModel{ID: "p1s", Name: "Bambu Lab P1S", Vendor: "Bambu Lab",
		BuildVolume: &contracts.Dimensions{X: 256, Y: 256, Z: 250}, NozzleDiametersMM: []float64{0.4, 0.6}, Plates: []string{"Textured PEI Plate", "Cool Plate"},
		AMSSupported: &yes, Source: "test"})
	if err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) addPrinter(extra map[string]any) map[string]any { return r.addPrinterFor(r.sim, extra) }

func (r *rig) addPrinterFor(s *sim.Sim, extra map[string]any) map[string]any {
	r.t.Helper()
	args := map[string]any{"name": "Atelier", "model_id": "p1s", "host": "127.0.0.1", "serial": s.Serial(), "nozzle_mm": 0.4, "ams_units": 1,
		"pinned_sha256": s.Fingerprint(), "allow_start_from_finish": true}
	_, mp := splitAddr(s.MQTTAddr())
	_, fp := splitAddr(s.FTPAddr())
	args["mqtt_port"], args["ftp_port"] = mp, fp
	for k, v := range extra {
		args[k] = v
	}
	return r.call("add_printer", args)
}

func splitAddr(a string) (string, int) {
	i := strings.LastIndex(a, ":")
	var p int
	fmt.Sscan(a[i+1:], &p)
	return a[:i], p
}

// humanSetsAccessCode : l'humain saisit le code sur la page /setup/printer/{id}.
func (r *rig) humanSetsAccessCode(setupURL string) {
	r.t.Helper()
	st, _, nonce := r.humanGet(setupURL)
	if st != 200 || nonce == "" {
		r.t.Fatalf("page de saisie : %d", st)
	}
	if st, body := r.humanPost(setupURL, url.Values{"nonce": {nonce}, "access_code": {simAccessCode}}); st != 200 || strings.Contains(body, simAccessCode) {
		r.t.Fatalf("saisie du code : %d (le code ne doit jamais être réaffiché)", st)
	}
}

// fakeArtifact dépose dans le stockage un faux .gcode.3mf (octets quelconques) : seul le validateur substitué le lit.
func (r *rig) fakeArtifact() string {
	r.t.Helper()
	pad := make([]byte, 64)
	_, _ = rand.Read(pad)
	data := append([]byte("FAKE-GCODE-3MF-FOR-SIMULATOR-"), []byte(hex.EncodeToString(pad))...)
	sha, _, err := r.c.Blobs.Put(bytes.NewReader(data), "")
	if err != nil {
		r.t.Fatal(err)
	}
	return sha
}

// fakeValidator : rapport d'archive plausible pour un P1S 0,4 mm, un filament PLA.
func fakeValidator(nozzle string) printingValidator {
	return func(path string, exp slicer.Expect) (*slicer.ArchiveInfo, *slicer.Validation, error) {
		v := &slicer.Validation{OK: true, Checks: []slicer.Check{{Name: "fake_archive", OK: true, Level: "error", Detail: "validateur de test (artefact factice)"}}}
		info := &slicer.ArchiveInfo{Entries: 1, StudioVersion: "02.00.00.00", PrinterModel: "Bambu Lab P1S", PrinterProfile: "Bambu Lab P1S 0.4 nozzle",
			ProcessProfile: "0.20mm Standard", FilamentIDs: []string{"Bambu PLA Basic"}, Nozzles: []string{nozzle}, BedType: "Textured PEI Plate",
			Plates: []slicer.PlateResult{{Index: 1, Layers: 100, PredictedSeconds: 600, WeightGrams: 4.2, MaxZ: 20, Filaments: []slicer.FilamentUse{{Slot: 1, Type: "PLA", Grams: 4.2}}}}}
		return info, v, nil
	}
}

type catalogFile = catalog.GeneratedFile

func newModel(name string) contracts.Model { return contracts.Model{Name: name} }

type printingValidator = func(path string, exp slicer.Expect) (*slicer.ArchiveInfo, *slicer.Validation, error)

var ext1 = []map[string]any{{"filament_slot": 1, "tray": "ext", "material_id": "PLA Basic noir"}}
var ams1 = []map[string]any{{"filament_slot": 1, "tray": "ams1:2", "material_id": "PLA Basic noir"}}

func anyAssign(a []map[string]any) []any {
	out := make([]any, len(a))
	for i, v := range a {
		out[i] = v
	}
	return out
}
