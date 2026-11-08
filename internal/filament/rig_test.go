package filament_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/filament"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
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
	printerID string
	human     *http.Client

	evMu   sync.Mutex
	events []filament.Event
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func startSim(t *testing.T, printDur time.Duration) *sim.Sim {
	t.Helper()
	if printDur == 0 {
		printDur = 1500 * time.Millisecond
	}
	s, err := sim.New(sim.Config{ControlPort: -1, AccessCode: simAccessCode, Tick: 20 * time.Millisecond, HeartbeatInterval: 150 * time.Millisecond,
		PrepareDuration: 100 * time.Millisecond, PrintDuration: printDur})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func noWorker(context.Context, string, map[string]any) (json.RawMessage, error) {
	return nil, errors.New("worker CAO absent (test)")
}

// newRig démarre le cœur complet (sans Bambu Studio), le serveur HTTP et un client MCP en mémoire.
func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	c, err := core.Open(ctx, core.Config{DataDir: dir, Logger: quiet(), StudioPath: "Z:/absent/bambu-studio.exe", Catalog: catalog.Config{Worker: noWorker},
		PollInterval: 20 * time.Millisecond, HeartbeatInterval: 100 * time.Millisecond, AllowAnyPrinterHost: true})
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
	r := &rig{t: t, c: c, web: web, cs: cs}
	r.human = r.enrollBrowser()
	c.Printing.SetReconcileSettle(200 * time.Millisecond)
	c.Filament.Subscribe(func(e filament.Event) {
		r.evMu.Lock()
		r.events = append(r.events, e)
		r.evMu.Unlock()
	})
	return r
}

func (r *rig) eventsOf(kind string) []filament.Event {
	r.evMu.Lock()
	defer r.evMu.Unlock()
	var out []filament.Event
	for _, e := range r.events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func (r *rig) enrollBrowser() *http.Client {
	r.t.Helper()
	code, _, err := humanauth.New(r.c.Store, humanauth.Options{}).NewEnrollCode(context.Background())
	if err != nil {
		r.t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	cl := &http.Client{Jar: jar, Timeout: 30 * time.Second}
	if _, err := browserGet(cl, r.web.URL+"/enroll"); err != nil {
		r.t.Fatal(err)
	}
	st, body := browserPost(r.t, cl, r.web.URL+"/enroll", url.Values{"code": {humanauth.FormatCode(code)}, "label": {"test"}})
	if st != 200 || !strings.Contains(body, "enregistré") {
		r.t.Fatalf("enregistrement du navigateur : %d %s", st, body)
	}
	return cl
}

func browserGet(cl *http.Client, u string) (*http.Response, error) {
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	return cl.Do(req)
}

func browserPost(t *testing.T, cl *http.Client, u string, f url.Values) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", u, strings.NewReader(f.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	res, err := cl.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func (r *rig) call(name string, args map[string]any) map[string]any {
	r.t.Helper()
	m, txt, isErr := r.raw(name, args)
	if isErr {
		r.t.Fatalf("%s en erreur : %s", name, txt)
	}
	return m
}

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

func num(m map[string]any, path ...string) float64 {
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return 0
		}
		cur = mm[p]
	}
	f, _ := cur.(float64)
	return f
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

var nonceRe = regexp.MustCompile(`name="nonce" value="([^"]+)"`)

func (r *rig) humanGet(pageURL string) (int, string, string) {
	r.t.Helper()
	res, err := browserGet(r.human, pageURL)
	if err != nil {
		r.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	nonce := ""
	if all := nonceRe.FindAllSubmatch(b, -1); len(all) > 0 { // le formulaire principal est le dernier (le bouton « Reprendre la photo » le précède)
		m := all[len(all)-1]
		nonce = string(m[1])
	}
	return res.StatusCode, string(b), nonce
}

// humanConfirm : l'humain ouvre la page, coche « plateau vide » et clique Lancer.
func (r *rig) humanConfirm(confirmURL string) (int, string) {
	r.t.Helper()
	st, body, nonce := r.humanGet(confirmURL)
	if st != 200 || nonce == "" {
		r.t.Fatalf("page de confirmation : %d %s", st, body)
	}
	return browserPost(r.t, r.human, confirmURL, url.Values{"nonce": {nonce}, "plate_clear": {"yes"}})
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

func splitAddr(a string) (string, int) {
	i := strings.LastIndex(a, ":")
	var p int
	fmt.Sscan(a[i+1:], &p)
	return a[:i], p
}

// setup : modèle, simulateur, imprimante ajoutée avec son code saisi par l'humain.
func (r *rig) setup(printDur time.Duration) {
	r.t.Helper()
	r.sim = startSim(r.t, printDur)
	r.seedPrinterModel()
	_, mp := splitAddr(r.sim.MQTTAddr())
	_, fp := splitAddr(r.sim.FTPAddr())
	out := r.call("add_printer", map[string]any{"name": "Atelier", "model_id": "p1s", "host": "127.0.0.1", "serial": r.sim.Serial(), "nozzle_mm": 0.4, "ams_units": 1,
		"mqtt_port": mp, "ftp_port": fp})
	r.printerID = str(out, "printer", "id")
	if err := r.c.Printing.SetAllowStartFromFinish(context.Background(), r.printerID, true); err != nil {
		r.t.Fatal(err)
	}
	setupURL := str(out, "setup_url")
	st, page, nonce := r.humanGet(setupURL)
	if st != 200 || nonce == "" || !strings.Contains(page, "Empreinte SHA-256") {
		r.t.Fatalf("page de saisie : %d", st)
	}
	if st, _ := browserPost(r.t, r.human, setupURL, url.Values{"nonce": {nonce}, "access_code": {simAccessCode}}); st != 200 {
		r.t.Fatalf("saisie du code : %d", st)
	}
	waitFor(r.t, 20*time.Second, "premier rapport avec AMS", func() bool {
		v, err := r.c.Filament.GetAMS(context.Background(), r.printerID)
		return err == nil && v.Present
	})
}

// fakeArtifact dépose un faux .gcode.3mf distinct (le validateur substitué ne lit pas l'archive).
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

// fakeValidator : plaque 1 avec les filaments donnés (type, couleur, grammes).
type fil struct {
	typ, color string
	grams      float64
}

func fakeValidator(fs ...fil) func(string, slicer.Expect) (*slicer.ArchiveInfo, *slicer.Validation, error) {
	return func(path string, exp slicer.Expect) (*slicer.ArchiveInfo, *slicer.Validation, error) {
		v := &slicer.Validation{OK: true, Checks: []slicer.Check{{Name: "fake_archive", OK: true, Level: "error", Detail: "validateur de test"}}}
		var uses []slicer.FilamentUse
		var w float64
		var ids []string
		for i, f := range fs {
			uses = append(uses, slicer.FilamentUse{Slot: i + 1, Type: f.typ, Color: f.color, Grams: f.grams})
			w += f.grams
			ids = append(ids, "Bambu "+f.typ+" Basic")
		}
		info := &slicer.ArchiveInfo{Entries: 1, StudioVersion: "02.00.00.00", PrinterModel: "Bambu Lab P1S", PrinterProfile: "Bambu Lab P1S 0.4 nozzle",
			ProcessProfile: "0.20mm Standard", FilamentIDs: ids, Nozzles: []string{"0.4"}, BedType: "Textured PEI Plate",
			Plates: []slicer.PlateResult{{Index: 1, Layers: 100, PredictedSeconds: 600, WeightGrams: w, MaxZ: 20, Filaments: uses}}}
		return info, v, nil
	}
}

func anySlice[T any](in []T) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

func assign(slot int, tray, material string) map[string]any {
	return map[string]any{"filament_slot": slot, "tray": tray, "material_id": material}
}

// validated dépose un faux artefact et renvoie l'impression VALIDATED.
func (r *rig) validated() string {
	r.t.Helper()
	art := r.fakeArtifact()
	v := r.call("validate_print_artifact", map[string]any{"artifact_id": art, "printer_id": r.printerID})
	if str(v, "state") != "VALIDATED" {
		r.t.Fatalf("validation : %v", v)
	}
	return str(v, "print_job_id")
}

// httpPost appelle l'API JSON (écriture) comme le ferait la SPA et renvoie le corps décodé.
func (r *rig) httpPost(t *testing.T, path string, body map[string]any) map[string]any {
	t.Helper()
	var rd io.Reader = http.NoBody
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest("POST", r.web.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	if res.StatusCode >= 300 {
		t.Fatalf("POST %s : %d %v", path, res.StatusCode, m)
	}
	return m
}

func (r *rig) createSpool(args map[string]any) map[string]any {
	r.t.Helper()
	return r.call("update_spool", args)["spool"].(map[string]any)
}
