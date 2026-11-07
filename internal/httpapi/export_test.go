package httpapi_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/export"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
)

const exportToken = "0123456789abcdef0123456789"

func exportEnv(t *testing.T) (*core.Core, *httptest.Server, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c, err := core.Open(ctx, core.Config{DataDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		PollInterval: 10 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond, Catalog: catalog.Config{}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	srv := httptest.NewServer(httpapi.NewHandlerWithOptions(c, httpapi.Options{APIToken: exportToken}))
	c.Catalog.SetBaseURL(srv.URL)
	t.Cleanup(func() { srv.Close(); cancel(); <-done; c.Close() })

	// un modèle, un fichier, une collection (insérés directement)
	content := []byte("solid t\nendsolid t\n")
	sha, _, err := c.Blobs.Put(bytes.NewReader(content), "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, q := range []string{
		`INSERT INTO collections(id,name,created_at) VALUES('col_x','Projet web','` + now + `')`,
		`INSERT INTO models(id,collection_id,name,tags,created_at,updated_at,description,license) VALUES('mod_x','col_x','Pièce web','[]','` + now + `','` + now + `','','')`,
		`INSERT INTO source_files(id,model_id,name,format,size_bytes,sha256,availability,created_at,rel_path,unit) VALUES('fil_x','mod_x','p.stl','stl',` + "19" + `,'` + sha + `','linux','` + now + `','','mm')`,
		`INSERT INTO model_versions(id,model_id,label,note,source_file_ids,created_at) VALUES('ver_x','mod_x','v1','','["fil_x"]','` + now + `')`,
	} {
		if _, err := c.Store.DB().Exec(q); err != nil {
			t.Fatalf("%s : %v", q, err)
		}
	}
	return c, srv, "col_x"
}

func authed(method, u string, body any) (*http.Response, []byte) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, u, rd)
	req.Header.Set("Authorization", "Bearer "+exportToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestExportRoutesAndSignedDownload(t *testing.T) {
	c, srv, col := exportEnv(t)

	// sans jeton : refusé
	req, _ := http.NewRequest("POST", srv.URL+"/api/exports", strings.NewReader(`{"collection_id":"`+col+`"}`))
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST /api/exports sans jeton : %v %v", resp, err)
	}
	// entrée invalide : 400
	if resp, b := authed("POST", srv.URL+"/api/exports", map[string]any{}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("entrée vide : %d %s", resp.StatusCode, b)
	}
	resp, b := authed("POST", srv.URL+"/api/exports", map[string]any{"collection_id": col, "options": map[string]any{"title": "Mon projet", "license": "CC BY 4.0"}})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("lancement : %d %s", resp.StatusCode, b)
	}
	var st export.Started
	if err := json.Unmarshal(b, &st); err != nil || st.ExportID == "" || st.JobID == "" || len(st.Plan.Parts) != 1 {
		t.Fatalf("réponse : %s", b)
	}
	// attendre
	var status struct {
		Export export.Record `json:"export"`
	}
	for i := 0; i < 400; i++ {
		_, b = authed("GET", srv.URL+"/api/exports/"+st.ExportID, nil)
		_ = json.Unmarshal(b, &status)
		if status.Export.State != export.StateQueued {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if status.Export.State != export.StateReady {
		t.Fatalf("export non prêt : %s", b)
	}
	// l'état ne contient rien de sensible
	if strings.Contains(string(b), c.Cfg.DataDir) {
		t.Fatalf("chemin du serveur dans l'état : %s", b)
	}

	// lien signé
	resp, b = authed("POST", srv.URL+"/api/exports/"+st.ExportID+"/link", nil)
	var link export.Link
	if err := json.Unmarshal(b, &link); err != nil || resp.StatusCode != 200 || !strings.HasPrefix(link.URL, srv.URL+"/api/exports/"+st.ExportID+".zip?exp=") {
		t.Fatalf("lien : %d %s", resp.StatusCode, b)
	}
	// téléchargement SANS jeton (capacité signée)
	get := func(raw string) (*http.Response, []byte) {
		resp, err := http.Get(raw)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, b
	}
	resp, zb := get(link.URL)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/zip" || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment;") ||
		resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("téléchargement : %d %v", resp.StatusCode, resp.Header)
	}
	zr, err := zip.NewReader(bytes.NewReader(zb), int64(len(zb)))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range zr.File {
		got[f.Name] = true
	}
	if !got["README.md"] || !got["manifest.json"] || !got["files/Pièce web/p.stl"] {
		t.Fatalf("contenu de l'archive : %v", got)
	}
	// Range (reprise de téléchargement)
	rq, _ := http.NewRequest("GET", link.URL, nil)
	rq.Header.Set("Range", "bytes=0-3")
	if r2, err := http.DefaultClient.Do(rq); err != nil || r2.StatusCode != http.StatusPartialContent {
		t.Fatalf("Range : %v %v", r2, err)
	}

	// signature altérée, expiration modifiée, autre export, lien expiré : tous refusés (403), sans jeton
	u, _ := url.Parse(link.URL)
	q := u.Query()
	mk := func(id string, mod func(url.Values)) string {
		v := url.Values{"exp": q["exp"], "sig": q["sig"]}
		mod(v)
		return srv.URL + "/api/exports/" + id + ".zip?" + v.Encode()
	}
	for name, raw := range map[string]string{
		"signature altérée":  mk(st.ExportID, func(v url.Values) { v.Set("sig", strings.Repeat("0", 64)) }),
		"expiration changée": mk(st.ExportID, func(v url.Values) { v.Set("exp", "9999999999") }),
		"autre export":       mk("exp_autre", func(url.Values) {}),
		"sans signature":     srv.URL + "/api/exports/" + st.ExportID + ".zip",
	} {
		if r, _ := get(raw); r.StatusCode != http.StatusForbidden && r.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s : %d au lieu de 403/401", name, r.StatusCode)
		}
	}
	expired, err := c.Export.LinkTTL(context.Background(), st.ExportID, -time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if r, b := get(expired.URL); r.StatusCode != http.StatusForbidden || !strings.Contains(string(b), "expiré") {
		t.Fatalf("lien expiré : %d %s", r.StatusCode, b)
	}
	// une URL d'aperçu GLB signée n'ouvre pas une archive
	glb, _ := c.Catalog.SignedAssetURL(st.ExportID)
	gu, _ := url.Parse(glb)
	if r, _ := get(srv.URL + "/api/exports/" + st.ExportID + ".zip?" + gu.RawQuery); r.StatusCode != http.StatusForbidden {
		t.Fatalf("signature d'aperçu acceptée : %d", r.StatusCode)
	}
	// liste
	if resp, b := authed("GET", srv.URL+"/api/exports", nil); resp.StatusCode != 200 || !strings.Contains(string(b), st.ExportID) {
		t.Fatalf("liste : %d %s", resp.StatusCode, b)
	}
	// lien d'un export inconnu : 404
	if resp, _ := authed("POST", srv.URL+"/api/exports/exp_nope/link", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("export inconnu : %d", resp.StatusCode)
	}
}
