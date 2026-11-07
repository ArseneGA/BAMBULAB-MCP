package httpapi_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
)

func (b *browser) do(method, u string, body io.Reader, csrf bool) (*http.Response, map[string]any) {
	b.t.Helper()
	req, _ := http.NewRequest(method, u, body)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Content-Type", "application/json")
	if csrf {
		req.Header.Set("X-Atelier-Request", "1")
	}
	res, err := b.cl.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return res, m
}

func TestWebUploadChunkedWithBrowserSession(t *testing.T) {
	c, srv := humanEnv(t, httpapi.Options{APIToken: webToken})
	b := enrolled(t, c, srv.URL)
	data, err := os.ReadFile(filepath.Join("..", "mesh", "testdata", "cylinder_binary.stl"))
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(data)
	begin := `{"filename":"cyl.stl","total_size":` + itoa(len(data)) + `,"sha256":"` + hex.EncodeToString(h[:]) + `","collection":"Web","tags":["glisser"]}`

	// Sans cookie : 401. Cookie sans en-tête CSRF : 403.
	if res, _ := call(t, nil, "POST", srv.URL+"/api/uploads", "", map[string]string{"Content-Type": "application/json"}, begin); res.StatusCode != 401 {
		t.Fatalf("sans session : %d", res.StatusCode)
	}
	if res, _ := b.do("POST", srv.URL+"/api/uploads", strings.NewReader(begin), false); res.StatusCode != 403 {
		t.Fatalf("sans CSRF : %d", res.StatusCode)
	}
	res, st := b.do("POST", srv.URL+"/api/uploads", strings.NewReader(begin), true)
	if res.StatusCode != 201 || st["upload_id"] == nil {
		t.Fatalf("begin : %d %v", res.StatusCode, st)
	}
	id := st["upload_id"].(string)
	put := func(off int, part []byte) (*http.Response, map[string]any) {
		return b.do("PUT", srv.URL+"/api/uploads/"+id+"?offset="+itoa(off), strings.NewReader(string(part)), true)
	}
	if res, m := put(0, data[:200]); res.StatusCode != 200 || m["next_offset"].(float64) != 200 {
		t.Fatalf("put : %d %v", res.StatusCode, m)
	}
	if res, m := put(900, data[900:]); res.StatusCode != 409 || m["code"] != "offset_mismatch" || m["status"].(map[string]any)["next_offset"].(float64) != 200 {
		t.Fatalf("trou : %d %v", res.StatusCode, m)
	}
	if res, m := b.do("POST", srv.URL+"/api/uploads/"+id+"/finish", nil, true); res.StatusCode != 409 {
		t.Fatalf("fin incomplète : %d %v", res.StatusCode, m)
	}
	if res, m := put(200, data[200:]); res.StatusCode != 200 || m["complete"] != true {
		t.Fatalf("put 2 : %d %v", res.StatusCode, m)
	}
	res, fin := b.do("POST", srv.URL+"/api/uploads/"+id+"/finish", nil, true)
	f, _ := fin["file"].(map[string]any)
	if res.StatusCode != 200 || f["status"] != "imported" || f["sha256"] != hex.EncodeToString(h[:]) {
		t.Fatalf("finish : %d %v", res.StatusCode, fin)
	}
	// Capacités : taille max et dossiers d'import.
	_, caps := b.do("GET", srv.URL+"/api/capabilities", nil, false)
	up, _ := caps["upload"].(map[string]any)
	if up == nil || up["max_upload_bytes"].(float64) <= 0 || up["import_roots"] == nil {
		t.Fatalf("capacités : %v", caps)
	}
	// Mauvaise empreinte et nom piégé via HTTP.
	bad := `{"filename":"x.stl","total_size":` + itoa(len(data)) + `,"sha256":"` + strings.Repeat("a", 64) + `"}`
	_, st2 := b.do("POST", srv.URL+"/api/uploads", strings.NewReader(bad), true)
	id2 := st2["upload_id"].(string)
	b.do("PUT", srv.URL+"/api/uploads/"+id2+"?offset=0", strings.NewReader(string(data)), true)
	if res, m := b.do("POST", srv.URL+"/api/uploads/"+id2+"/finish", nil, true); res.StatusCode != 400 || !strings.Contains(m["error"].(string), "sha256") {
		t.Fatalf("sha faux : %d %v", res.StatusCode, m)
	}
	if res, _ := b.do("POST", srv.URL+"/api/uploads", strings.NewReader(`{"filename":"../../x.stl","total_size":10}`), true); res.StatusCode != 400 {
		t.Fatalf("traversée : %d", res.StatusCode)
	}
	// Import par lien hors liste : refusé.
	if res, _ := b.do("POST", srv.URL+"/api/import-url", strings.NewReader(`{"url":"https://127.0.0.1/x.stl"}`), true); res.StatusCode != 403 {
		t.Fatalf("SSRF : %d", res.StatusCode)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
