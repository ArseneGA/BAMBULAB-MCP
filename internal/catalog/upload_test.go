package catalog

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newH : harnais dont les sessions d'envoi sont fermées avant le nettoyage des dossiers (Windows).
func newH(t *testing.T) *harness {
	h := newHarness(t, nil)
	t.Cleanup(h.svc.CloseUploads)
	return h
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// sendAll envoie data par morceaux de n octets et termine l'envoi.
func (h *harness) sendAll(name string, data []byte, n int, meta UploadMeta) (*UploadResult, error) {
	h.t.Helper()
	ctx := context.Background()
	st, err := h.svc.UploadBegin(ctx, name, int64(len(data)), sum(data), meta)
	if err != nil {
		return nil, err
	}
	for off := 0; off < len(data); off += n {
		end := min(off+n, len(data))
		if _, err := h.svc.UploadWrite(ctx, st.UploadID, int64(off), data[off:end]); err != nil {
			return nil, err
		}
	}
	return h.svc.UploadFinish(ctx, st.UploadID)
}

func zipOf(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for n, c := range files {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(c)
	}
	zw.Close()
	return b.Bytes()
}

func blobCount(t *testing.T, h *harness) int {
	t.Helper()
	n := 0
	filepath.Walk(filepath.Join(h.data, "blobs"), func(p string, i os.FileInfo, err error) error {
		if err == nil && i.Mode().IsRegular() && !strings.Contains(p, string(filepath.Separator)+"tmp"+string(filepath.Separator)) {
			n++
		}
		return nil
	})
	return n
}

func TestUploadChunkedImportsAndVersions(t *testing.T) {
	h := newH(t)
	stl := cubeSTL(t, 10)
	r, err := h.svc.UploadBegin(context.Background(), "cube.stl", int64(len(stl)), sum(stl), UploadMeta{Collection: "Envois/Test", Tags: []string{"Claude"}, License: "CC-BY", Source: "généré"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := h.svc.UploadWrite(ctx, r.UploadID, 0, stl[:100]); err != nil {
		t.Fatal(err)
	}
	// Reprise : morceau déjà reçu rejoué (réponse perdue), puis chevauchement.
	if st, err := h.svc.UploadWrite(ctx, r.UploadID, 0, stl[:100]); err != nil || st.NextOffset != 100 {
		t.Fatalf("rejeu : %+v %v", st, err)
	}
	if st, err := h.svc.UploadWrite(ctx, r.UploadID, 50, stl[50:300]); err != nil || st.NextOffset != 300 {
		t.Fatalf("chevauchement : %+v %v", st, err)
	}
	// Trou : refusé avec le bon offset.
	_, err = h.svc.UploadWrite(ctx, r.UploadID, 400, stl[400:])
	var mm *UploadChunkMismatchError
	if !errors.As(err, &mm) || mm.Status.NextOffset != 300 || !errors.Is(err, ErrConflict) {
		t.Fatalf("trou : %v", err)
	}
	// Fin prématurée.
	if _, err := h.svc.UploadFinish(ctx, r.UploadID); !errors.As(err, &mm) {
		t.Fatalf("fin incomplète : %v", err)
	}
	if _, err := h.svc.UploadWrite(ctx, r.UploadID, 300, stl[300:]); err != nil {
		t.Fatal(err)
	}
	res, err := h.svc.UploadFinish(ctx, r.UploadID)
	if err != nil {
		t.Fatal(err)
	}
	if res.File.Status != StatusImported || res.File.SHA256 != sum(stl) {
		t.Fatalf("résultat : %+v", res.File)
	}
	h.idle()
	d := h.details(res.File.ModelID)
	if d.Model.License != "CC-BY" || len(d.Model.Tags) != 1 || d.Model.Provenance.Source != "généré" || d.Model.CollectionID == nil {
		t.Fatalf("fiche : %+v", d.Model)
	}
	if d.Preview.State != "ready" {
		t.Fatalf("aperçu : %+v", d.Preview)
	}
	// Session fermée.
	if _, err := h.svc.UploadStatus(ctx, r.UploadID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session encore là : %v", err)
	}
	// Même nom, même collection, autre contenu : nouvelle version.
	r2, err := h.sendAll("cube.stl", cubeSTL(t, 20), 1000, UploadMeta{Collection: "Envois/Test"})
	if err != nil || r2.File.Status != StatusNewVersion || r2.File.ModelID != res.File.ModelID {
		t.Fatalf("version : %+v %v", r2, err)
	}
	// Même contenu : inchangé. Même contenu, autre nom : doublon.
	r3, _ := h.sendAll("cube.stl", cubeSTL(t, 20), 1000, UploadMeta{Collection: "Envois/Test"})
	if r3.File.Status != StatusUnchanged {
		t.Fatalf("inchangé : %+v", r3.File)
	}
	r4, err := h.sendAll("autre.stl", cubeSTL(t, 20), 1000, UploadMeta{})
	if err != nil || r4.File.Status != StatusDuplicate {
		t.Fatalf("doublon : %+v %v", r4, err)
	}
	if h.modelCount() != 1 {
		t.Fatalf("modèles : %d", h.modelCount())
	}
	// Le dossier d'envois ne garde rien.
	left, _ := os.ReadDir(filepath.Join(h.data, "uploads"))
	if len(left) != 0 {
		t.Fatalf("résidus : %v", left)
	}
}

func TestUploadWrongSHARejected(t *testing.T) {
	h := newH(t)
	stl := cubeSTL(t, 10)
	bad := sum([]byte("autre"))
	st, err := h.svc.UploadBegin(context.Background(), "x.stl", int64(len(stl)), bad, UploadMeta{})
	if err != nil {
		t.Fatal(err)
	}
	h.svc.UploadWrite(context.Background(), st.UploadID, 0, stl)
	_, err = h.svc.UploadFinish(context.Background(), st.UploadID)
	if err == nil || !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "sha256 incorrect") {
		t.Fatalf("attendu sha256 incorrect : %v", err)
	}
	if h.modelCount() != 0 || blobCount(t, h) != 0 {
		t.Fatal("rien ne doit être stocké")
	}
	if _, err := h.svc.UploadStatus(context.Background(), st.UploadID); !errors.Is(err, ErrNotFound) {
		t.Fatal("la session doit être fermée")
	}
	if _, err := h.svc.UploadBegin(context.Background(), "x.stl", 10, "zz", UploadMeta{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("sha mal formé : %v", err)
	}
}

func TestUploadSizeLimits(t *testing.T) {
	h := newH(t)
	h.svc.SetUploadOptions(UploadOptions{MaxBytes: 1 << 20})
	ctx := context.Background()
	if _, err := h.svc.UploadBegin(ctx, "big.stl", 2<<20, "", UploadMeta{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("trop gros : %v", err)
	}
	if _, err := h.svc.UploadBegin(ctx, "zero.stl", 0, "", UploadMeta{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("taille nulle : %v", err)
	}
	st, err := h.svc.UploadBegin(ctx, "ok.stl", 100, "", UploadMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.UploadWrite(ctx, st.UploadID, 0, make([]byte, 101)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("dépassement annoncé : %v", err)
	}
	if l := h.svc.UploadLimits(); l.MaxUploadBytes != 1<<20 || l.MaxByFormatMB["stl"] != 1 || len(l.ImportRoots) != 1 || l.MaxChunkChars != MaxChunkChars {
		t.Fatalf("limites : %+v", l)
	}
}

func TestUploadSessionLimitAndTTL(t *testing.T) {
	h := newH(t)
	h.svc.SetUploadOptions(UploadOptions{TTL: 80 * time.Millisecond, MaxSessions: 2})
	ctx := context.Background()
	a, _ := h.svc.UploadBegin(ctx, "a.stl", 10, "", UploadMeta{})
	h.svc.UploadBegin(ctx, "b.stl", 10, "", UploadMeta{})
	if _, err := h.svc.UploadBegin(ctx, "c.stl", 10, "", UploadMeta{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("trop de sessions : %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	if _, err := h.svc.UploadStatus(ctx, a.UploadID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session non purgée : %v", err)
	}
	if left, _ := os.ReadDir(filepath.Join(h.data, "uploads")); len(left) != 0 {
		t.Fatalf("fichiers non purgés : %v", left)
	}
	if err := h.svc.UploadCancel("upl_inconnue"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestUploadNamesNeverPaths(t *testing.T) {
	h := newH(t)
	for _, n := range []string{"../evil.stl", "a/b.stl", `..\evil.stl`, `C:\x\y.stl`, "/etc/passwd.stl", "a\x00b.stl", "", ".stl", "x.exe", "x.stl.", "noext", "con:.stl", strings.Repeat("a", 300) + ".stl"} {
		if _, err := h.svc.UploadBegin(context.Background(), n, 100, "", UploadMeta{}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q devrait être refusé : %v", n, err)
		}
	}
	// Nom valide : le fichier de session n'a jamais le nom du client.
	st, err := h.svc.UploadBegin(context.Background(), "ok.stl", 100, "", UploadMeta{})
	if err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(filepath.Join(h.data, "uploads"))
	if len(ents) != 1 || strings.Contains(ents[0].Name(), "ok") || !strings.HasPrefix(ents[0].Name(), st.UploadID) {
		t.Fatalf("fichier de session : %v", ents)
	}
	// Collection avec traversée : refusée.
	if _, err := h.sendAll("c.stl", cubeSTL(t, 5), 1000, UploadMeta{Collection: "../x"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("collection traversante : %v", err)
	}
}

func TestUploadFormatSignatures(t *testing.T) {
	h := newH(t)
	good3mf := testdata(t, "multi_cm.3mf")
	evil := zipOf(t, map[string][]byte{"[Content_Types].xml": []byte("x"), "3D/3dmodel.model": []byte("x"), "../../evil.txt": []byte("x")})
	noModel := zipOf(t, map[string][]byte{"hello.txt": []byte("x")})
	cases := []struct {
		name string
		data []byte
		ok   bool
	}{
		{"a.stl", cubeSTL(t, 5), true},
		{"b.stl", testdata(t, "cube10_ascii.stl"), true},
		{"c.obj", testdata(t, "cube10_quads.obj"), true},
		{"d.3mf", good3mf, true},
		{"e.step", []byte("ISO-10303-21;\nHEADER;\nENDSEC;\nDATA;\nENDSEC;\nEND-ISO-10303-21;\n"), true},
		{"f.stl", []byte("ceci n'est pas un STL du tout, juste du texte"), false},
		{"g.stl", append([]byte("solid x\x00"), make([]byte, 200)...), false},
		{"h.obj", append([]byte{0x00, 0x01, 0x02}, make([]byte, 90)...), false},
		{"i.obj", []byte("rien de tel\nque des sommets\n"), false},
		{"j.3mf", []byte("pas un zip"), false},
		{"k.3mf", noModel, false},
		{"l.3mf", evil, false},
		{"m.step", []byte("<html>pas du step</html>"), false},
		{"n.stl", testdata(t, "cube10_binary.stl")[:200], false}, // binaire tronqué
	}
	for _, c := range cases {
		res, err := h.sendAll(c.name, c.data, 4096, UploadMeta{})
		if c.ok && err != nil {
			t.Errorf("%s : %v", c.name, err)
		}
		if !c.ok && (err == nil || !errors.Is(err, ErrInvalid)) {
			t.Errorf("%s devrait être refusé : %+v %v", c.name, res, err)
		}
	}
	// Les refus n'ont rien stocké : 5 fichiers valides seulement.
	if h.modelCount() != 5 {
		t.Fatalf("modèles : %d", h.modelCount())
	}
}

// ---- import par lien ----

func TestCheckURL(t *testing.T) {
	o := URLImportOptions{}
	o.defaults()
	ok := []string{"https://makerworld.com/fr/models/123", "https://public-cdn.bblmw.com/a/b.3mf?x=1", "https://www.printables.com/model/1-x", "https://files.printables.com/media/a.stl"}
	for _, u := range ok {
		if _, err := checkURL(u, o); err != nil {
			t.Errorf("%s : %v", u, err)
		}
	}
	bad := []string{
		"http://makerworld.com/x", "ftp://makerworld.com/x", "file:///etc/passwd", "https://127.0.0.1/x", "https://[::1]/x", "https://10.0.0.5/x",
		"https://169.254.169.254/latest/meta-data", "https://evilmakerworld.com/x", "https://makerworld.com.evil.org/x", "https://example.org/x.stl",
		"https://user:pw@makerworld.com/x", "https://makerworld.com:8443/x", "https://localhost/x", "//makerworld.com/x", "", "https://",
		"https://2130706433/x", "https://0x7f.1/x",
	}
	for _, u := range bad {
		if _, err := checkURL(u, o); err == nil {
			t.Errorf("%q devrait être refusé", u)
		}
	}
	if _, err := checkURL("https://example.org/x", o); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("domaine : %v", err)
	}
}

func TestPublicIP(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "0.0.0.0", "100.64.0.1", "::1", "fe80::1", "fc00::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "224.0.0.1", "198.18.0.1", "2002:7f00:1::1", "64:ff9b::7f00:1"} {
		if PublicIP(net.ParseIP(s)) {
			t.Errorf("%s ne doit pas être public", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !PublicIP(net.ParseIP(s)) {
			t.Errorf("%s doit être public", s)
		}
	}
}

func TestAllowListEnv(t *testing.T) {
	t.Setenv("BAMBU_URL_IMPORT_ALLOW", "Example.com, https://cdn.thing.org/x ; 10.0.0.1, .foo.io, nodot")
	o := urlOptionsFromEnv()
	if strings.Join(o.Allow, ",") != "example.com,cdn.thing.org,foo.io" {
		t.Fatalf("liste : %v", o.Allow)
	}
	t.Setenv("BAMBU_URL_IMPORT", "off")
	if !urlOptionsFromEnv().Disabled {
		t.Fatal("désactivation")
	}
	t.Setenv("BAMBU_URL_IMPORT_ALLOW", "")
	t.Setenv("BAMBU_URL_IMPORT", "")
	if got := urlOptionsFromEnv().Allow; len(got) != 3 || got[0] != "makerworld.com" {
		t.Fatalf("défaut : %v", got)
	}
}

// urlHarness : service dont l'import par lien parle à un serveur TLS local (tous les noms se résolvent vers lui).
func urlHarness(t *testing.T, handler http.Handler, tweak func(*URLImportOptions)) (*harness, string) {
	t.Helper()
	srv := httptest.NewUnstartedServer(handler)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	h := newH(t)
	o := URLImportOptions{allowPrivate: true, tlsConfig: &tls.Config{InsecureSkipVerify: true},
		resolver: func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("127.0.0.1")}, nil }}
	if tweak != nil {
		tweak(&o)
	}
	h.svc.SetUploadOptions(UploadOptions{URL: o})
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	return h, port
}

func TestImportFromURLDirectFile(t *testing.T) {
	stl := cubeSTL(t, 10)
	h, port := urlHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="../../joli cube.stl"`)
		w.Write(stl)
	}), nil)
	res, err := h.svc.ImportFromURL(context.Background(), URLImportInput{URL: "https://files.makerworld.com:" + port + "/dl?id=1", UploadMeta: UploadMeta{Tags: []string{"makerworld"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 1 || res.Files[0].Status != StatusImported || res.Files[0].SHA256 != sum(stl) || res.Source != "MakerWorld" {
		t.Fatalf("résultat : %+v", res)
	}
	h.idle()
	d := h.details(res.Files[0].ModelID)
	if d.Model.Provenance.URL == "" || !strings.Contains(d.Model.Provenance.URL, "makerworld.com") || d.Model.Provenance.Source != "MakerWorld" {
		t.Fatalf("provenance : %+v", d.Model.Provenance)
	}
	if d.Files[0].Name != "joli cube.stl" {
		t.Fatalf("nom : %q", d.Files[0].Name)
	}
}

func TestImportFromURLPageWithLicense(t *testing.T) {
	stl := cubeSTL(t, 10)
	var base string
	h, port := urlHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/model/1":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<html><head><meta property="og:title" content="Super support | Printables"><meta name="author" content="Alice">
<script type="application/ld+json">{"license":"CC BY-NC 4.0"}</script></head><body><a href="https://%s/files/support.stl?sig=a&amp;b=1">dl</a></body></html>`, r.Host)
		case "/files/support.stl":
			w.Write(stl)
		default:
			http.NotFound(w, r)
		}
	}), nil)
	_ = base
	res, err := h.svc.ImportFromURL(context.Background(), URLImportInput{URL: "https://www.printables.com:" + port + "/model/1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.License != "CC BY-NC 4.0" || res.Author != "Alice" || res.Source != "Printables" || len(res.Files) != 1 {
		t.Fatalf("résultat : %+v", res)
	}
	d := h.details(res.Files[0].ModelID)
	if d.Model.License != "CC BY-NC 4.0" || d.Model.Name != "Super support" || d.Model.Provenance.Author != "Alice" {
		t.Fatalf("fiche : %+v", d.Model)
	}
}

func TestImportFromURLManualDownload(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"401": func(w http.ResponseWriter, r *http.Request) { http.Error(w, "login", http.StatusUnauthorized) },
		"403": func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", http.StatusForbidden) },
		"login": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/login" {
				w.Header().Set("Content-Type", "text/html")
				w.Write([]byte("<html>Sign in</html>"))
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
		},
		"nolink": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/login" {
				return
			}
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html><body>Connectez-vous pour télécharger</body></html>"))
		},
	} {
		hh, port := urlHarness(t, h, nil)
		_, err := hh.svc.ImportFromURL(context.Background(), URLImportInput{URL: "https://makerworld.com:" + port + "/fr/models/9"})
		var me *ManualDownloadError
		if name == "login" {
			// /login renvoie une page vide en text/html (par défaut Go) : marche à suivre attendue.
			if !errors.As(err, &me) {
				t.Errorf("%s : %v", name, err)
			}
			continue
		}
		if !errors.As(err, &me) || !strings.Contains(err.Error(), "upload_file") || !errors.Is(err, ErrInvalid) {
			t.Errorf("%s : marche à suivre attendue, reçu %v", name, err)
		}
	}
}

func TestImportFromURLRedirectOffListRefused(t *testing.T) {
	hits := 0
	h, port := urlHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, "https://evil.example.org/x.stl", http.StatusFound)
	}), nil)
	_, err := h.svc.ImportFromURL(context.Background(), URLImportInput{URL: "https://makerworld.com:" + port + "/a"})
	if err == nil || !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("redirection hors liste : %v", err)
	}
	// Redirection vers HTTP ou une IP : refusée aussi.
	for _, loc := range []string{"http://makerworld.com/x.stl", "https://127.0.0.1/x.stl", "https://169.254.169.254/x"} {
		h2, p2 := urlHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, loc, http.StatusFound) }), nil)
		if _, err := h2.svc.ImportFromURL(context.Background(), URLImportInput{URL: "https://makerworld.com:" + p2 + "/a"}); err == nil || !errors.Is(err, ErrNotAllowed) {
			t.Errorf("%s : %v", loc, err)
		}
	}
	// Boucle de redirections.
	h3, p3 := urlHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://makerworld.com:"+r.Host[strings.LastIndex(r.Host, ":")+1:]+"/loop", http.StatusFound)
	}), nil)
	if _, err := h3.svc.ImportFromURL(context.Background(), URLImportInput{URL: "https://makerworld.com:" + p3 + "/loop"}); err == nil {
		t.Fatal("boucle attendue en erreur")
	}
}

func TestImportFromURLPrivateResolutionRefused(t *testing.T) {
	// Pas de allowPrivate : un nom autorisé qui se résout vers une adresse privée est refusé AVANT toute connexion.
	for _, ip := range []string{"127.0.0.1", "10.0.0.8", "169.254.169.254", "::1", "192.168.0.1"} {
		h := newH(t)
		h.svc.SetUploadOptions(UploadOptions{URL: URLImportOptions{resolver: func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP(ip)}, nil }}})
		_, err := h.svc.ImportFromURL(context.Background(), URLImportInput{URL: "https://makerworld.com/x.stl"})
		if err == nil || !errors.Is(err, ErrNotAllowed) {
			t.Errorf("%s : %v", ip, err)
		}
	}
	// Une seule adresse privée parmi plusieurs suffit à refuser (rebinding).
	h := newH(t)
	h.svc.SetUploadOptions(UploadOptions{URL: URLImportOptions{resolver: func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("10.0.0.1")}, nil
	}}})
	if _, err := h.svc.ImportFromURL(context.Background(), URLImportInput{URL: "https://makerworld.com/x.stl"}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("mixte : %v", err)
	}
}

func TestImportFromURLDisabledAndOffList(t *testing.T) {
	h := newH(t)
	h.svc.SetUploadOptions(UploadOptions{URL: URLImportOptions{Disabled: true}})
	if _, err := h.svc.ImportFromURL(context.Background(), URLImportInput{URL: "https://makerworld.com/x"}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("désactivé : %v", err)
	}
	if h.svc.URLImportInfo().Enabled {
		t.Fatal("info")
	}
	h2 := newH(t)
	if _, err := h2.svc.ImportFromURL(context.Background(), URLImportInput{URL: "https://example.org/x.stl"}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("hors liste : %v", err)
	}
	if _, err := h2.svc.ImportFromURL(context.Background(), URLImportInput{URL: "http://makerworld.com/x.stl"}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("http : %v", err)
	}
}

func TestImportFromURLSizeAndTimeout(t *testing.T) {
	h, port := urlHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, 3<<20))
	}), nil)
	h.svc.SetUploadOptions(UploadOptions{MaxBytes: 1 << 20, URL: URLImportOptions{allowPrivate: true, tlsConfig: &tls.Config{InsecureSkipVerify: true},
		resolver: func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("127.0.0.1")}, nil }}})
	if _, err := h.svc.ImportFromURL(context.Background(), URLImportInput{URL: "https://makerworld.com:" + port + "/big.stl"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("trop gros : %v", err)
	}
	hs, ps := urlHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush()
		time.Sleep(2 * time.Second)
	}), func(o *URLImportOptions) { o.Timeout = 300 * time.Millisecond })
	start := time.Now()
	if _, err := hs.svc.ImportFromURL(context.Background(), URLImportInput{URL: "https://makerworld.com:" + ps + "/slow.stl"}); err == nil || time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("délai : %v (%v)", err, time.Since(start))
	}
}

func TestImportFromURLZip(t *testing.T) {
	cube := cubeSTL(t, 10)
	z := zipOf(t, map[string][]byte{"files/cube.stl": cube, "../../etc/pwn.stl": cube, "readme.txt": []byte("x"), "p.3mf": testdata(t, "multi_cm.3mf"), "bad.stl": []byte("nope")})
	h, port := urlHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(z)
	}), nil)
	res, err := h.svc.ImportFromURL(context.Background(), URLImportInput{URL: "https://makerworld.com:" + port + "/all.zip"})
	if err != nil {
		t.Fatal(err)
	}
	ok, rej := 0, 0
	for _, f := range res.Files {
		switch f.Status {
		case StatusImported, StatusDuplicate:
			ok++
		case StatusRejected:
			rej++
		}
	}
	if ok != 3 || rej != 1 { // cube.stl, pwn.stl (doublon, nom aplati), p.3mf ; bad.stl refusé
		t.Fatalf("zip : %+v", res.Files)
	}
	// Zip sans fichier 3D.
	hz, pz := urlHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(zipOf(t, map[string][]byte{"a.txt": []byte("x")}))
	}), nil)
	if _, err := hz.svc.ImportFromURL(context.Background(), URLImportInput{URL: "https://makerworld.com:" + pz + "/a.zip"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zip vide : %v", err)
	}
}

func TestImportFromURLNon3DContentRejected(t *testing.T) {
	h, port := urlHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte("MZ\x90\x00 un exécutable"))
	}), nil)
	for _, p := range []string{"/virus.exe", "/x", "/fake.stl"} {
		if _, err := h.svc.ImportFromURL(context.Background(), URLImportInput{URL: "https://makerworld.com:" + port + p}); err == nil {
			t.Errorf("%s doit être refusé", p)
		}
	}
	if h.modelCount() != 0 || blobCount(t, h) != 0 {
		t.Fatal("rien ne doit être stocké")
	}
}
