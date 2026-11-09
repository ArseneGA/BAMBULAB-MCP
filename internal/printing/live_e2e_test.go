package printing_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"image/jpeg"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mcpserver"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer/sim"
)

func imageOf(res *mcp.CallToolResult) *mcp.ImageContent {
	for _, c := range res.Content {
		if ic, ok := c.(*mcp.ImageContent); ok {
			return ic
		}
	}
	return nil
}

// Suivi en direct de bout en bout (imprimante et caméra simulées) : outil d'image partagé et borné, repli texte + image de
// get_print_status, liens de vue sur le résultat, outil d'état, résumé final avec photo.
func TestLiveTrackingEndToEnd(t *testing.T) {
	s, err := sim.New(sim.Config{ControlPort: -1, AccessCode: simAccessCode, Tick: 20 * time.Millisecond, HeartbeatInterval: 150 * time.Millisecond,
		PrepareDuration: 150 * time.Millisecond, PrintDuration: 4 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	cam, err := s.StartCamera(sim.CameraConfig{FrameEvery: 30 * time.Millisecond, Width: 640, Height: 360})
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
	ctx := context.Background()

	// ---- image partagée : trois vues ouvertes en même temps = UNE capture (même image, même heure) ----
	var wg sync.WaitGroup
	frames := make([]map[string]any, 3)
	for i := range frames {
		wg.Add(1)
		go func() {
			defer wg.Done()
			frames[i] = r.call("get_live_frame", map[string]any{"printer_id": r.printerID})
		}()
	}
	wg.Wait()
	fresh := 0
	for i, f := range frames {
		if f["cached"] != true {
			fresh++
		}
		if f["taken_at"] != frames[0]["taken_at"] {
			t.Fatalf("vue %d : image d'une autre capture (%v / %v)", i, f["taken_at"], frames[0]["taken_at"])
		}
	}
	if fresh != 1 {
		t.Fatalf("captures réelles pour 3 appels concurrents : %d, attendu 1", fresh)
	}
	data, _ := frames[0]["data"].(string)
	jpg, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(jpg)); err != nil {
		t.Fatalf("image illisible : %v", err)
	}
	if len(data) > 100_000 || len(jpg) > mcpserver.LiveFrameMaxBytes {
		t.Fatalf("image non bornée : %d octets, %d caractères", len(jpg), len(data))
	}
	if r.callErr("get_live_frame", map[string]any{"printer_id": "pr_inconnue"}) == "" {
		t.Fatal("imprimante inconnue : refus attendu")
	}

	// ---- impression lancée par l'humain ----
	art := r.fakeArtifact()
	jobID, _ := r.uploadAndAsk(art, "cle-depot-live-e2e")
	cf := r.call("request_print_confirmation", map[string]any{"print_job_id": jobID})
	if st, b := r.humanConfirm(str(cf, "confirm_url"), true); st != 200 {
		t.Fatalf("approbation : %d %s", st, b)
	}
	if st := r.call("start_print", map[string]any{"print_job_id": jobID, "confirmation_id": str(cf, "confirmation_id"), "idempotency_key": "cle-start-live-e2e"}); str(st, "state") != "PRINTING" {
		t.Fatalf("lancement : %v", st)
	}

	// ---- repli sans vue : le texte reste complet ET une image est jointe (défaut pendant l'impression) ----
	res, err := r.cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_print_status", Arguments: map[string]any{"print_job_id": jobID}})
	if err != nil || res.IsError {
		t.Fatalf("get_print_status : %v %+v", err, res)
	}
	first, ok := res.Content[0].(*mcp.TextContent)
	if !ok || first.Text == "" || !bytes.Contains([]byte(first.Text), []byte("Impression "+jobID+" : PRINTING")) {
		t.Fatalf("le texte de suivi doit rester en tête et complet : %+v", res.Content)
	}
	img := imageOf(res)
	if img == nil || img.MIMEType != "image/jpeg" || len(img.Data) == 0 || len(img.Data) > mcpserver.LiveFrameMaxBytes {
		t.Fatalf("instantané de repli absent ou non borné : %+v", res.Content)
	}
	if _, err := jpeg.Decode(bytes.NewReader(img.Data)); err != nil {
		t.Fatal(err)
	}
	if ui, _ := res.Meta["ui"].(map[string]any); ui["resourceUri"] != mcpserver.LiveViewURI || res.Meta["ui/resourceUri"] != mcpserver.LiveViewURI {
		t.Fatalf("lien de vue absent du RÉSULTAT : %v", res.Meta)
	}
	// with_photo=false : texte seul, identique ; la structure reste celle de toujours
	res2, err := r.cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_print_status", Arguments: map[string]any{"print_job_id": jobID, "with_photo": false}})
	if err != nil || res2.IsError || imageOf(res2) != nil {
		t.Fatalf("with_photo=false : %v %+v", err, res2)
	}
	if len(res2.Content) != 1 || res2.StructuredContent == nil {
		t.Fatalf("contenu : %+v", res2.Content)
	}
	// wait_for_print_change porte aussi le lien de vue
	res3, err := r.cs.CallTool(ctx, &mcp.CallToolParams{Name: "wait_for_print_change", Arguments: map[string]any{"print_job_id": jobID, "timeout_seconds": 1}})
	if err != nil || res3.IsError {
		t.Fatalf("wait_for_print_change : %v", err)
	}
	if ui, _ := res3.Meta["ui"].(map[string]any); ui["resourceUri"] != mcpserver.LiveViewURI {
		t.Fatalf("lien de vue absent de wait_for_print_change : %v", res3.Meta)
	}

	// ---- état de la carte : suivi complet, pas encore terminé ----
	ls := r.call("get_live_state", map[string]any{"print_job_id": jobID})
	if str(ls, "status", "job", "id") != jobID || ls["terminal"] != false || ls["camera_available"] != true || ls["final"] != nil {
		t.Fatalf("get_live_state : %v", ls)
	}

	// ---- fin : résumé final avec photo ----
	r.waitState(jobID, 40*time.Second, "COMPLETED")
	fin := r.call("get_live_state", map[string]any{"print_job_id": jobID, "with_final_photo": true})
	if fin["terminal"] != true {
		t.Fatalf("terminal attendu : %v", fin)
	}
	f, _ := fin["final"].(map[string]any)
	if f == nil || f["outcome"] != "completed" {
		t.Fatalf("résumé final : %v", fin["final"])
	}
	ph, _ := f["photo"].(map[string]any)
	pd, _ := ph["data"].(string)
	pj, err := base64.StdEncoding.DecodeString(pd)
	if err != nil || len(pj) == 0 || len(pj) > mcpserver.LiveFrameMaxBytes {
		t.Fatalf("photo de fin : %v %d", err, len(pj))
	}
	if _, err := jpeg.Decode(bytes.NewReader(pj)); err != nil {
		t.Fatal(err)
	}
}
