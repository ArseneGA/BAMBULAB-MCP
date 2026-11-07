package printing_test

import (
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"image/jpeg"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer/sim"
)

// newCamRig : comme newRig, avec le port caméra du simulateur et des intervalles d'instantanés courts.
func newCamRig(t *testing.T, camPort int) *rig {
	t.Helper()
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	c, err := core.Open(ctx, core.Config{DataDir: dir, Logger: quiet(), StudioPath: "Z:/absent/bambu-studio.exe", Catalog: catalog.Config{Worker: fakeWorker},
		PollInterval: 20 * time.Millisecond, HeartbeatInterval: 100 * time.Millisecond, AllowAnyPrinterHost: true, CameraPort: camPort, CameraMinInterval: 0.05})
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
	r := &rig{t: t, c: c, web: web, cs: cs, dataDir: dir}
	r.human = r.enrollBrowser()
	c.Printing.SetReconcileSettle(200 * time.Millisecond)
	return r
}

func TestCameraEndToEnd(t *testing.T) {
	s, err := sim.New(sim.Config{ControlPort: -1, AccessCode: simAccessCode, Tick: 20 * time.Millisecond, HeartbeatInterval: 150 * time.Millisecond,
		PrepareDuration: 150 * time.Millisecond, PrintDuration: 14 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	cam, err := s.StartCamera(sim.CameraConfig{FrameEvery: 40 * time.Millisecond, Width: 640, Height: 360, SpaghettiAt: 40})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cam.Close() })
	r := newCamRig(t, cam.Port())
	r.sim = s
	r.seedPrinterModel()
	r.c.Printing.SetValidator(fakeValidator("0.4"))
	ap := r.addPrinter(nil)
	r.printerID = str(ap, "printer", "id")
	r.humanSetsAccessCode(str(ap, "setup_url"))
	r.waitPrinterState(r.printerID, "idle")

	// ---- outil get_camera_snapshot : une IMAGE que Claude voit ----
	res, err := r.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_camera_snapshot", Arguments: map[string]any{"printer_id": r.printerID}})
	if err != nil || res.IsError {
		t.Fatalf("snapshot : %v %+v", err, res)
	}
	var img *mcp.ImageContent
	for _, c := range res.Content {
		if ic, ok := c.(*mcp.ImageContent); ok {
			img = ic
		}
	}
	if img == nil || img.MIMEType != "image/jpeg" {
		t.Fatalf("contenu image absent : %+v", res.Content)
	}
	if _, err := jpeg.Decode(bytes.NewReader(img.Data)); err != nil {
		t.Fatal(err)
	}
	// route web d'instantané + flux MJPEG relayé (une seule connexion à la caméra)
	resp, err := http.Get(r.web.URL + "/api/printers/" + r.printerID + "/camera/snapshot")
	if err != nil || resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("snapshot web : %v %v", err, resp)
	}
	resp.Body.Close()
	sresp, err := http.Get(r.web.URL + "/api/printers/" + r.printerID + "/camera/stream")
	if err != nil || sresp.StatusCode != 200 {
		b0, _ := io.ReadAll(sresp.Body)
		t.Fatalf("flux : %v %s", err, b0)
	}
	_, params, _ := mime.ParseMediaType(sresp.Header.Get("Content-Type"))
	mr := multipart.NewReader(sresp.Body, params["boundary"])
	for i := 0; i < 3; i++ {
		p, err := mr.NextPart()
		if err != nil {
			t.Fatalf("partie %d : %v", i, err)
		}
		b, _ := io.ReadAll(p)
		if _, err := jpeg.Decode(bytes.NewReader(b)); err != nil {
			t.Fatalf("partie %d illisible : %v", i, err)
		}
	}
	sresp.Body.Close()

	// ---- notifications : canal sans secret par l'outil, secret saisi par l'humain ----
	var mu sync.Mutex
	var got []*http.Request
	var bodies [][]byte
	ntfy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, rq *http.Request) {
		b, _ := io.ReadAll(rq.Body)
		mu.Lock()
		got = append(got, rq)
		bodies = append(bodies, b)
		mu.Unlock()
	}))
	defer ntfy.Close()
	out := r.call("configure_notifications", map[string]any{"channels": []any{map[string]any{"name": "telephone", "kind": "ntfy", "events": []any{"detection_alert", "print_completed"}}}})
	setupURL := str(out, "setup_url")
	if !strings.HasSuffix(setupURL, "/setup/notifications") {
		t.Fatalf("setup_url : %q", setupURL)
	}
	if r.callErr("configure_notifications", map[string]any{"channels": []any{map[string]any{"name": "x", "kind": "ntfy", "url": "http://x"}}}) == "" {
		// un champ inconnu (secret) doit être refusé par le schéma
		t.Fatal("un secret passé à l'outil doit être refusé")
	}
	st, page, nonce := r.humanGet(setupURL)
	chID := regexp.MustCompile(`name="channel" value="([^"]+)"`).FindStringSubmatch(page)
	if st != 200 || nonce == "" || chID == nil {
		t.Fatalf("page secrets : %d", st)
	}
	topic := ntfy.URL + "/sujet-e2e-secret"
	if st, body := r.humanPost(setupURL, url.Values{"nonce": {nonce}, "channel": {chID[1]}, "secret_url": {topic}}); st != 200 || strings.Contains(body, "sujet-e2e-secret") {
		t.Fatalf("saisie du secret : %d", st)
	}
	tn := r.call("test_notification", map[string]any{"channel": "telephone"})
	if !strings.Contains(strings.ToLower(str(tn, "note")), "1/1") {
		t.Fatalf("test_notification : %v", tn)
	}
	if strings.Contains(strings.ToLower(str(r.call("configure_notifications", nil), "setup_url")+jsonOf(r.call("configure_notifications", nil))), "sujet-e2e-secret") {
		t.Fatal("secret renvoyé à Claude")
	}

	// ---- réglages de détection : intervalle court, une seule analyse suffit pour alerter ----
	r.call("configure_failure_detection", map[string]any{"interval_seconds": 0.1, "consecutive_alerts": 1})

	// ---- photo du plateau sur /confirm ----
	art := r.fakeArtifact()
	jobID, _ := r.uploadAndAsk(art, "cle-depot-camera-e2e")
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": jobID})
	confURL := str(cf, "confirm_url")
	st, body, _ := r.humanGet(confURL)
	m := regexp.MustCompile(`<img src="(/api/camera/photos/[0-9a-f]{64})"`).FindStringSubmatch(body)
	if st != 200 || m == nil || !strings.Contains(body, "Photo du plateau prise à") {
		t.Fatalf("photo absente de /confirm : %d %s", st, body)
	}
	ires, err := browserGet(r.human, r.web.URL+m[1])
	if err != nil || ires.StatusCode != 200 || ires.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("image de /confirm : %v %v", err, ires)
	}
	ires.Body.Close()
	// la caméra tombe en panne : la page l'annonce sans photo périmée
	time.Sleep(4 * time.Second) // le flux MJPEG partagé se ferme 3 s après le dernier spectateur
	_ = cam.SetFault(sim.CameraFaultDown, true)
	if _, body, _ := r.humanGet(confURL); !strings.Contains(body, "Photo du plateau indisponible") || strings.Contains(body, "<img") {
		t.Fatalf("panne de caméra sur /confirm : %s", body)
	}
	_ = cam.SetFault(sim.CameraFaultDown, false)
	_, _, _ = r.humanGet(confURL) // photo fraîche = image de référence « plateau vide »

	// ---- lancement par l'humain, puis spaghetti dans l'image ----
	if st, b := r.humanConfirm(confURL, true); st != 200 {
		t.Fatalf("approbation : %d %s", st, b)
	}
	s1 := r.call("start_print", map[string]any{"print_job_id": jobID, "confirmation_id": str(cf, "confirmation_id"), "idempotency_key": "cle-start-camera-e2e"})
	if str(s1, "state") != "PRINTING" {
		t.Fatalf("lancement : %v", s1)
	}
	_ = cam.SetFault(sim.CameraFaultSpaghetti, true)
	waitFor(t, 20*time.Second, "alerte de détection", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, rq := range got {
			if strings.Contains(rq.Header.Get("Tags"), "warning") {
				return true
			}
		}
		return false
	})
	mu.Lock()
	var alert *http.Request
	var alertBody []byte
	for i, rq := range got {
		if strings.Contains(rq.Header.Get("Tags"), "warning") {
			alert, alertBody = rq, bodies[i]
		}
	}
	mu.Unlock()
	if alert.Method != http.MethodPut {
		t.Fatalf("l'alerte doit joindre la photo : %s", alert.Method)
	}
	if _, err := jpeg.Decode(bytes.NewReader(alertBody)); err != nil {
		t.Fatalf("photo d'alerte : %v", err)
	}
	click := alert.Header.Get("Click")
	if !strings.Contains(click, "/stop/stp_") {
		t.Fatalf("lien d'arrêt absent : %q", click)
	}
	// JAMAIS d'arrêt automatique : l'impression tourne toujours, rien n'a été envoyé à l'imprimante
	if g := s.GcodeState(); g != "RUNNING" {
		t.Fatalf("l'imprimante n'est plus en RUNNING : %s", g)
	}
	if state := r.printState(jobID); state != "PRINTING" {
		t.Fatalf("impression : %s", state)
	}
	// le lien mène à la page HUMAINE d'arrêt (formulaire non soumis)
	stopPath := click[strings.Index(click, "/stop/"):]
	if st, body, nn := r.humanGet(r.web.URL + stopPath); st != 200 || nn == "" || !strings.Contains(body, "Arrêter l'impression") {
		t.Fatalf("page d'arrêt : %d", st)
	}
	if s.GcodeState() != "RUNNING" {
		t.Fatal("la simple ouverture de la page d'arrêt ne doit rien arrêter")
	}
	// analyse + alertes consultables
	an := r.call("analyze_print_frame", map[string]any{"printer_id": r.printerID})
	if an["detector_report"] == nil || str(an, "print_job_id") != jobID {
		t.Fatalf("analyze_print_frame : %v", an)
	}
	resp2, _ := http.Get(r.web.URL + "/api/print-jobs/" + jobID + "/analysis")
	ab, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if !strings.Contains(string(ab), `"alerts":[{`) {
		t.Fatalf("alertes : %s", ab)
	}

	// ---- fin d'impression : photo de fin rattachée + notification ----
	r.waitState(jobID, 40*time.Second, "COMPLETED")
	waitFor(t, 15*time.Second, "photo de fin", func() bool {
		resp, err := http.Get(r.web.URL + "/api/print-jobs/" + jobID + "/photos")
		if err != nil {
			return false
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return strings.Contains(string(b), `"kind":"final"`)
	})
	waitFor(t, 15*time.Second, "notification de fin", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, rq := range got {
			if strings.Contains(rq.Header.Get("Tags"), "white_check_mark") {
				return true
			}
		}
		return false
	})
	_ = strconv.Itoa
}

func jsonOf(m map[string]any) string {
	return strings.ReplaceAll(strings.TrimSpace(strings.Join(flat(m), " ")), "\n", " ")
}

func flat(v any) []string {
	switch x := v.(type) {
	case map[string]any:
		var out []string
		for k, e := range x {
			out = append(out, k)
			out = append(out, flat(e)...)
		}
		return out
	case []any:
		var out []string
		for _, e := range x {
			out = append(out, flat(e)...)
		}
		return out
	case string:
		return []string{x}
	}
	return nil
}
