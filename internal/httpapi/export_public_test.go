package httpapi_test

import (
	"bytes"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/export"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
)

// FB13 : l'archive d'un export se télécharge depuis l'adresse PUBLIQUE restreinte, mais seulement par URL signée ;
// tout le reste de /api/exports reste 404 sur cet écouteur.
func TestExportDownloadOnPublicListenerSignedOnly(t *testing.T) {
	e := newPubEnv(t, httpapi.Options{})
	sha, _, err := e.c.Blobs.Put(bytes.NewReader([]byte("solid t\nendsolid t\n")), "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, q := range []string{
		`INSERT INTO collections(id,name,created_at) VALUES('col_x','Projet pub','` + now + `')`,
		`INSERT INTO models(id,collection_id,name,tags,created_at,updated_at,description,license) VALUES('mod_x','col_x','Pièce pub','[]','` + now + `','` + now + `','','')`,
		`INSERT INTO source_files(id,model_id,name,format,size_bytes,sha256,availability,created_at,rel_path,unit) VALUES('fil_x','mod_x','p.stl','stl',19,'` + sha + `','linux','` + now + `','','mm')`,
		`INSERT INTO model_versions(id,model_id,label,note,source_file_ids,created_at) VALUES('ver_x','mod_x','v1','','["fil_x"]','` + now + `')`,
	} {
		if _, err := e.c.Store.DB().Exec(q); err != nil {
			t.Fatalf("%s : %v", q, err)
		}
	}
	st, err := e.c.Export.Start(t.Context(), export.Input{CollectionID: "col_x"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 400; i++ {
		if rec, _ := e.c.Export.Get(t.Context(), st.ExportID); rec.State != export.StateQueued {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	// sans adresse publique configurée : le lien est celui du réseau privé, signalé comme tel
	l0, err := e.c.Export.Link(t.Context(), st.ExportID)
	if err != nil {
		t.Fatal(err)
	}
	if l0.Public || !strings.HasPrefix(l0.URL, e.main.URL) {
		t.Fatalf("sans adresse publique : %+v", l0)
	}
	// avec : le lien passe par l'écouteur public, même signature, durée par défaut (> 10 min), adresse privée conservée
	e.c.Catalog.SetUploadLinkBase(e.pub.URL, true)
	l, err := e.c.Export.Link(t.Context(), st.ExportID)
	if err != nil {
		t.Fatal(err)
	}
	if !l.Public || !strings.HasPrefix(l.URL, e.pub.URL+"/api/exports/") || !strings.HasPrefix(l.PrivateURL, e.main.URL) {
		t.Fatalf("lien public : %+v", l)
	}
	if d := time.Until(l.ExpiresAt); d < 100*time.Minute || d > export.DefaultLinkTTL+time.Second {
		t.Fatalf("durée du lien : %v", d)
	}
	pu, _ := url.Parse(l.URL)
	pathQ := pu.Path + "?" + pu.RawQuery

	res, body := e.do(e.pub.URL, "GET", pathQ, nil, "")
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/zip" || !strings.HasPrefix(body, "PK") {
		t.Fatalf("téléchargement public signé : %d %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if res.Header.Get("Cache-Control") != "private, no-store" {
		t.Errorf("Cache-Control : %q", res.Header.Get("Cache-Control"))
	}

	q := pu.Query()
	mk := func(mod func(url.Values)) string {
		v := url.Values{"exp": q["exp"], "sig": q["sig"]}
		mod(v)
		return pu.Path + "?" + v.Encode()
	}
	// URL altérée ou expirée : refus (403) ; sans signature ou forme inattendue : 404 (rien n'est révélé)
	expired, err := e.c.Export.LinkTTL(t.Context(), st.ExportID, -time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	eu, _ := url.Parse(expired.URL)
	for name, tc := range map[string]struct {
		path string
		want int
	}{
		"signature altérée":   {mk(func(v url.Values) { v.Set("sig", strings.Repeat("0", 64)) }), 403},
		"expiration allongée": {mk(func(v url.Values) { v.Set("exp", "9999999999") }), 403},
		"lien expiré":         {eu.Path + "?" + eu.RawQuery, 403},
		"autre export":        {"/api/exports/exp_" + strings.Repeat("0", 24) + ".zip?" + pu.RawQuery, 403},
		"sans signature":      {pu.Path, 404},
		"sans expiration":     {mk(func(v url.Values) { v.Del("exp") }), 404},
		"identifiant forgé":   {"/api/exports/..%2f..%2fetc.zip?" + pu.RawQuery, 404},
		"pas une archive":     {"/api/exports/" + st.ExportID + "?" + pu.RawQuery, 404},
		"liste":               {"/api/exports", 404},
		"état":                {"/api/exports/" + st.ExportID, 404},
	} {
		if r, b := e.do(e.pub.URL, "GET", tc.path, nil, ""); r.StatusCode != tc.want {
			t.Errorf("%s : %d au lieu de %d (%s)", name, r.StatusCode, tc.want, b)
		}
	}
	// aucune écriture ni création de lien sur l'écouteur public, même avec une signature valide
	for _, m := range []string{"POST", "PUT", "DELETE"} {
		if r, _ := e.do(e.pub.URL, m, pathQ, nil, ""); r.StatusCode != 404 {
			t.Errorf("%s sur l'archive publique : %d", m, r.StatusCode)
		}
	}
	if r, _ := e.do(e.pub.URL, "POST", "/api/exports/"+st.ExportID+"/link", nil, ""); r.StatusCode != 404 {
		t.Errorf("création de lien sur l'écouteur public : %d", r.StatusCode)
	}
}
