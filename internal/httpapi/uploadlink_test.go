package httpapi_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

func stlBytes(t *testing.T, size float64) []byte {
	t.Helper()
	b, err := mesh.STLBytes(mesh.Box(0, 0, 0, size, size, size))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func hexSHA(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// put envoie un corps sur un chemin (curl -T) sans aucun jeton d'API.
func (e *pubEnv) put(srv, path string, body []byte) (*http.Response, string) {
	e.t.Helper()
	return e.do(srv, "PUT", path, map[string]string{"Content-Type": "application/octet-stream"}, string(body))
}

func newLink(t *testing.T, e *pubEnv, name string, size int64, sha string) *catalog.UploadLinkInfo {
	t.Helper()
	l, err := e.c.Catalog.CreateUploadLink(t.Context(), name, size, sha, catalog.UploadMeta{})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// Un lien : le fichier arrive intact (empreinte vérifiée), par l'écouteur PUBLIC comme par le principal, sans jeton d'API ; usage unique.
func TestUploadLinkSingleUseOnPublicAndMain(t *testing.T) {
	e := newPubEnv(t, httpapi.Options{})
	data := stlBytes(t, 10)
	want := hexSHA(data)

	l := newLink(t, e, "cube.stl", int64(len(data)), want)
	res, body := e.put(e.pub.URL, "/upload/"+l.Token, data)
	if res.StatusCode != 200 {
		t.Fatalf("PUT public : %d %s", res.StatusCode, body)
	}
	var out struct {
		File struct {
			Status  string `json:"status"`
			SHA256  string `json:"sha256"`
			ModelID string `json:"model_id"`
		} `json:"file"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || out.File.Status != "imported" || out.File.SHA256 != want || out.File.ModelID == "" {
		t.Fatalf("réponse : %v %s", err, body)
	}
	if !e.c.Blobs.Has(want) {
		t.Fatal("blob absent du stockage")
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control : %q", res.Header.Get("Cache-Control"))
	}
	// usage unique : le même jeton ne sert plus, ni en public ni sur le principal
	for _, srv := range []string{e.pub.URL, e.main.URL} {
		if res, _ := e.put(srv, "/upload/"+l.Token, data); res.StatusCode != 404 {
			t.Errorf("deuxième usage sur %s : %d (attendu 404)", srv, res.StatusCode)
		}
	}
	st, err := e.c.Catalog.UploadLinkStatus(t.Context(), l.LinkID)
	if err != nil || st.State != "complete" || st.Result == nil || st.Result.File.ModelID != out.File.ModelID {
		t.Fatalf("statut du lien : %+v %v", st, err)
	}

	// écouteur principal, POST accepté aussi, sans Bearer
	data2 := stlBytes(t, 12)
	l2 := newLink(t, e, "cube12.stl", 0, "")
	if res, body := e.do(e.main.URL, "POST", "/upload/"+l2.Token, nil, string(data2)); res.StatusCode != 200 {
		t.Fatalf("POST principal : %d %s", res.StatusCode, body)
	}
}

func TestUploadLinkRefusals(t *testing.T) {
	e := newPubEnv(t, httpapi.Options{})
	data := stlBytes(t, 10)

	// mauvaise empreinte : refusé, rien au stockage, le lien est consommé
	l := newLink(t, e, "a.stl", int64(len(data)), strings.Repeat("0", 64))
	if res, body := e.put(e.pub.URL, "/upload/"+l.Token, data); res.StatusCode != 400 || !strings.Contains(body, "sha256") {
		t.Fatalf("empreinte : %d %s", res.StatusCode, body)
	}
	if e.c.Blobs.Has(hexSHA(data)) {
		t.Error("un fichier refusé ne doit pas entrer au stockage")
	}
	if res, _ := e.put(e.pub.URL, "/upload/"+l.Token, data); res.StatusCode != 404 {
		t.Errorf("lien consommé par l'échec : %d", res.StatusCode)
	}

	// Content-Length au-delà de la taille annoncée : 413 sans consommer ; le bon envoi passe ensuite
	l = newLink(t, e, "b.stl", int64(len(data)), "")
	if res, _ := e.put(e.pub.URL, "/upload/"+l.Token, append(append([]byte{}, data...), make([]byte, 100)...)); res.StatusCode != 413 {
		t.Errorf("trop gros : %d (attendu 413)", res.StatusCode)
	}
	if res, body := e.put(e.pub.URL, "/upload/"+l.Token, data); res.StatusCode != 200 {
		t.Errorf("le lien doit rester utilisable après un refus de taille annoncée : %d %s", res.StatusCode, body)
	}

	// taille réelle différente de la taille annoncée (corps plus court)
	l = newLink(t, e, "c.stl", int64(len(data))+500, "")
	if res, body := e.put(e.pub.URL, "/upload/"+l.Token, data); res.StatusCode != 400 || !strings.Contains(body, "annoncée") {
		t.Errorf("taille annoncée : %d %s", res.StatusCode, body)
	}

	// signature fausse : un « .stl » qui n'en est pas un
	l = newLink(t, e, "d.stl", 0, "")
	if res, _ := e.put(e.pub.URL, "/upload/"+l.Token, []byte("ceci n'est pas un maillage")); res.StatusCode != 400 {
		t.Errorf("signature : %d", res.StatusCode)
	}

	// corps vide
	l = newLink(t, e, "e.stl", 0, "")
	if res, _ := e.put(e.pub.URL, "/upload/"+l.Token, nil); res.StatusCode != 400 {
		t.Errorf("corps vide : %d", res.StatusCode)
	}

	// types : seulement ceux du catalogue
	for _, n := range []string{"x.glb", "x.exe", "x.gcode", "x", "../x.stl", "a/b.stl"} {
		if _, err := e.c.Catalog.CreateUploadLink(t.Context(), n, 0, "", catalog.UploadMeta{}); err == nil {
			t.Errorf("%q doit être refusé à la création", n)
		}
	}
	if _, err := e.c.Catalog.CreateUploadLink(t.Context(), "big.stl", 1<<40, "", catalog.UploadMeta{}); err == nil {
		t.Error("taille annoncée excessive acceptée")
	}
	if _, err := e.c.Catalog.CreateUploadLink(t.Context(), "x.stl", 0, "zz", catalog.UploadMeta{}); err == nil {
		t.Error("sha256 invalide accepté")
	}
}

func TestUploadLinkExpires(t *testing.T) {
	e := newPubEnv(t, httpapi.Options{})
	now := time.Now()
	e.c.Catalog.SetUploadLinkClock(func() time.Time { return now })
	l := newLink(t, e, "a.stl", 0, "")
	if d := l.ExpiresAt.Sub(now); d != 10*time.Minute {
		t.Fatalf("durée de vie : %v", d)
	}
	now = now.Add(10*time.Minute + time.Second)
	if res, _ := e.put(e.pub.URL, "/upload/"+l.Token, stlBytes(t, 5)); res.StatusCode != 404 {
		t.Errorf("lien expiré : %d (attendu 404)", res.StatusCode)
	}
	if st, _ := e.c.Catalog.UploadLinkStatus(t.Context(), l.LinkID); st.State != "expired" {
		t.Errorf("état : %q", st.State)
	}
}

// Sur l'écouteur public, /upload/<jeton> en PUT|POST est la SEULE exception ; tout le reste de l'envoi reste 404.
func TestPublicWhitelistAllowsOnlyTheUploadLink(t *testing.T) {
	e := newPubEnv(t, httpapi.Options{})
	tok := strings.Repeat("A", 43)
	for _, c := range []struct{ method, path string }{
		{"GET", "/upload/" + tok}, {"HEAD", "/upload/" + tok}, {"DELETE", "/upload/" + tok}, {"PATCH", "/upload/" + tok}, {"OPTIONS", "/upload/" + tok},
		{"PUT", "/upload/"}, {"PUT", "/upload"}, {"PUT", "/upload/" + tok[:42]}, {"PUT", "/upload/" + tok + "A"}, {"PUT", "/upload/" + tok + "/x"},
		{"PUT", "/upload/" + strings.Repeat(".", 43)}, {"PUT", "/upload/" + strings.Repeat("%2e", 43)}, {"PUT", "/uploads/" + tok}, {"PUT", "/Upload/" + tok},
		{"PUT", "/api/uploads/x?offset=0"}, {"POST", "/api/uploads"}, {"POST", "/api/uploads/x/finish"}, {"POST", "/api/import-url"}, {"DELETE", "/api/uploads/x"},
		{"POST", "/api/print-jobs/upload"}, {"PUT", "/api/models/x"}, {"POST", "/api/previews"},
	} {
		res, body := e.do(e.pub.URL, c.method, c.path, map[string]string{"Authorization": "Bearer " + oauthStaticToken}, "x")
		if res.StatusCode != 404 {
			t.Errorf("%s %s sur le public : %d (attendu 404) %.60s", c.method, c.path, res.StatusCode, body)
		}
	}
	// le principal garde son régime : l'API d'envoi exige le jeton d'API
	if res, _ := e.do(e.main.URL, "PUT", "/api/uploads/x?offset=0", nil, "x"); res.StatusCode != 401 {
		t.Errorf("PUT /api/uploads sans jeton sur le principal : %d", res.StatusCode)
	}
	// jeton de forme valide mais inconnu : 404 sans rien révéler
	res, body := e.put(e.pub.URL, "/upload/"+tok, stlBytes(t, 5))
	if res.StatusCode != 404 || strings.Contains(body, tok) {
		t.Errorf("jeton inconnu : %d %s", res.StatusCode, body)
	}
}

func TestPublicUploadRateLimit(t *testing.T) {
	e := newPubEnv(t, httpapi.Options{PublicRate: httpapi.PublicRateLimits{UploadPerMinute: 3}})
	tok := strings.Repeat("B", 43)
	do := func(ip string) int {
		res, _ := e.do(e.pub.URL, "PUT", "/upload/"+tok, map[string]string{"X-Forwarded-For": ip}, "x")
		return res.StatusCode
	}
	for i := 0; i < 3; i++ {
		if c := do("203.0.113.5"); c == 429 {
			t.Fatalf("appel %d déjà limité", i+1)
		}
	}
	if do("203.0.113.5") != 429 {
		t.Error("au-delà de la limite : 429 attendu")
	}
	if do("198.51.100.1") == 429 {
		t.Error("une autre adresse doit passer")
	}
	if l, err := httpapi.ParsePublicRateLimits("upload=7"); err != nil || l.UploadPerMinute != 7 {
		t.Errorf("clé upload : %v %v", l, err)
	}
}

func TestUploadLinkBodyOverLimitWithoutLength(t *testing.T) {
	e := newPubEnv(t, httpapi.Options{})
	data := stlBytes(t, 10)
	l := newLink(t, e, "a.stl", int64(len(data)), "")
	// corps sans Content-Length (en flux) plus gros que la taille annoncée : 413, lien consommé
	req, _ := http.NewRequest("PUT", e.pub.URL+"/upload/"+l.Token, bytes.NewReader(append(append([]byte{}, data...), make([]byte, 1000)...)))
	req.ContentLength = -1
	res, err := plainClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 413 {
		t.Fatalf("corps en flux trop gros : %d", res.StatusCode)
	}
	if res, _ := e.put(e.pub.URL, "/upload/"+l.Token, data); res.StatusCode != 404 {
		t.Errorf("lien consommé : %d", res.StatusCode)
	}
}
