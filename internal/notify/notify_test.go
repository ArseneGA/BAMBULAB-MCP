package notify

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

func newSvc(t *testing.T, mut func(*Deps)) *Service {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	d := Deps{Store: st, Vault: printing.NewMemorySecretStore(), Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		BaseURL: func() string { return "http://127.0.0.1:7878" }, RetryDelay: time.Millisecond, Retries: 2, AllowPrivate: true}
	if mut != nil {
		mut(&d)
	}
	s, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type captured struct {
	method string
	hdr    http.Header
	body   []byte
	path   string
}

func recorder(status int) (*httptest.Server, func() []captured) {
	var mu sync.Mutex
	var got []captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, captured{r.Method, r.Header.Clone(), b, r.URL.Path})
		mu.Unlock()
		w.WriteHeader(status)
	}))
	return srv, func() []captured { mu.Lock(); defer mu.Unlock(); return append([]captured(nil), got...) }
}

var jpg = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 1, 2, 3, 0xFF, 0xD9}

func TestNtfyChannelSendsTitleLinkAndPhotoWithoutLeakingTheTopic(t *testing.T) {
	srv, got := recorder(200)
	defer srv.Close()
	s := newSvc(t, nil)
	ctx := context.Background()
	ch, err := s.UpsertChannel(ctx, ChannelInput{Name: "Téléphone ntfy", Kind: KindNtfy})
	if err == nil {
		t.Fatalf("nom avec accent refusé attendu : %+v", ch)
	}
	ch, err = s.UpsertChannel(ctx, ChannelInput{Name: "telephone", Kind: KindNtfy, Events: []string{EvDetectionAlert, EvPrintCompleted}})
	if err != nil {
		t.Fatal(err)
	}
	if ch.Ready {
		t.Fatal("canal sans secret : ne doit pas être prêt")
	}
	topic := srv.URL + "/sujet-ultra-secret-4242"
	if err := s.SetSecret(ctx, ch.ID, "url", topic); err != nil {
		t.Fatal(err)
	}
	res := s.Emit(ctx, Event{Kind: EvDetectionAlert, Title: "Alerte : échec probable de pièce.3mf", Message: "Score 0.90. Rien n'a été arrêté.", Link: "http://127.0.0.1:7878/stop/stp_abc", Photo: jpg, JobID: "pj_1"})
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("envoi : %+v", res)
	}
	c := got()
	if len(c) != 1 || c[0].method != http.MethodPut || c[0].path != "/sujet-ultra-secret-4242" {
		t.Fatalf("requête ntfy : %+v", c)
	}
	if string(c[0].body) != string(jpg) || c[0].hdr.Get("Filename") == "" || c[0].hdr.Get("Click") != "http://127.0.0.1:7878/stop/stp_abc" || c[0].hdr.Get("Priority") != "high" {
		t.Fatalf("en-têtes : %+v", c[0].hdr)
	}
	if !strings.Contains(c[0].hdr.Get("Title"), "=?UTF-8?") { // titre non ASCII : encodé RFC 2047
		t.Fatalf("titre non encodé : %q", c[0].hdr.Get("Title"))
	}
	// évènement non abonné : aucun envoi
	if r := s.Emit(ctx, Event{Kind: EvPrinterOffline, Title: "x", Message: "y"}); len(r) != 0 {
		t.Fatalf("évènement non abonné envoyé : %+v", r)
	}
	// le sujet n'apparaît ni dans la vue, ni dans le journal
	view, _ := json.Marshal(mustList(t, s))
	lg, _ := s.Log(ctx, 10)
	logJSON, _ := json.Marshal(lg)
	for _, b := range [][]byte{view, logJSON} {
		if strings.Contains(string(b), "ultra-secret") || strings.Contains(string(b), srv.URL) {
			t.Fatalf("secret divulgué : %s", b)
		}
	}
	if !mustList(t, s)[0].Ready || mustList(t, s)[0].Secrets[0].Source != "vault" {
		t.Fatalf("état des secrets : %+v", mustList(t, s)[0].Secrets)
	}
}

func mustList(t *testing.T, s *Service) []ChannelView {
	t.Helper()
	l, err := s.ListChannels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestWebhookIsSignedWithHMAC(t *testing.T) {
	srv, got := recorder(200)
	defer srv.Close()
	s := newSvc(t, nil)
	ctx := context.Background()
	ch, _ := s.UpsertChannel(ctx, ChannelInput{Name: "hook", Kind: KindWebhook})
	// sans clé HMAC : le webhook n'est pas envoyé non signé
	_ = s.SetSecret(ctx, ch.ID, "url", srv.URL+"/in")
	if r := s.Emit(ctx, Event{Kind: EvPrintFailed, Title: "t", Message: "m"}); len(r) != 1 || r[0].OK {
		t.Fatalf("webhook non prêt : %+v", r)
	}
	if len(got()) != 0 {
		t.Fatal("rien ne doit partir sans clé HMAC")
	}
	_ = s.SetSecret(ctx, ch.ID, "hmac_key", "cle-hmac-de-test")
	res := s.Emit(ctx, Event{Kind: EvPrintFailed, Title: "Échec", Message: "m", Link: "http://x/impression", PrinterID: "pr_1", JobID: "pj_1", PhotoURL: "http://x/api/camera/photos/abc"})
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("%+v", res)
	}
	c := got()[0]
	ts := c.hdr.Get("X-Bambu-Timestamp")
	if ts == "" || c.hdr.Get("X-Bambu-Signature") != "sha256="+Sign("cle-hmac-de-test", ts, c.body) {
		t.Fatalf("signature invalide : %v", c.hdr)
	}
	var p WebhookPayload
	if err := json.Unmarshal(c.body, &p); err != nil || p.Event != EvPrintFailed || p.JobID != "pj_1" || p.PhotoURL == "" {
		t.Fatalf("corps : %s %v", c.body, err)
	}
	if strings.Contains(string(c.body), "cle-hmac") {
		t.Fatal("la clé HMAC ne doit pas figurer dans le corps")
	}
	// un corps altéré ne vérifie plus
	if Sign("cle-hmac-de-test", ts, append(c.body, ' ')) == strings.TrimPrefix(c.hdr.Get("X-Bambu-Signature"), "sha256=") {
		t.Fatal("la signature doit dépendre du corps")
	}
}

func TestSecretsFromEnvAndFileOverrideTheVault(t *testing.T) {
	srv, got := recorder(200)
	defer srv.Close()
	s := newSvc(t, nil)
	ctx := context.Background()
	ch, _ := s.UpsertChannel(ctx, ChannelInput{Name: "env-hook", Kind: KindWebhook})
	t.Setenv(EnvName("env-hook", "url"), srv.URL+"/via-env")
	f := filepath.Join(t.TempDir(), "hmac")
	if err := writeFile(f, "cle-depuis-fichier\n"); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvName("env-hook", "hmac_key")+"_FILE", f)
	v, _ := s.GetChannel(ctx, ch.ID)
	if !v.Ready || v.Secrets[0].Source != "env" || v.Secrets[1].Source != "file" {
		t.Fatalf("sources : %+v", v.Secrets)
	}
	if r := s.Emit(ctx, Event{Kind: EvTest, Title: "t", Message: "m"}); len(r) != 1 || !r[0].OK {
		t.Fatalf("%+v", r)
	}
	c := got()[0]
	if c.path != "/via-env" || c.hdr.Get("X-Bambu-Signature") != "sha256="+Sign("cle-depuis-fichier", c.hdr.Get("X-Bambu-Timestamp"), c.body) {
		t.Fatalf("%+v", c)
	}
}

func TestServerErrorsAreRetriedAndURLsScrubbed(t *testing.T) {
	var n int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		mu.Unlock()
		w.WriteHeader(500)
	}))
	s := newSvc(t, func(d *Deps) { d.Retries = 3 })
	ctx := context.Background()
	ch, _ := s.UpsertChannel(ctx, ChannelInput{Name: "ko", Kind: KindNtfy})
	_ = s.SetSecret(ctx, ch.ID, "url", srv.URL+"/secret-topic")
	res := s.Emit(ctx, Event{Kind: EvTest, Title: "t", Message: "m"})
	if res[0].OK || n != 3 {
		t.Fatalf("essais=%d %+v", n, res)
	}
	srv.Close()
	// serveur arrêté : l'erreur réseau ne doit pas contenir l'URL du sujet
	res = s.Emit(ctx, Event{Kind: EvTest, Title: "t", Message: "m"})
	if res[0].OK || strings.Contains(res[0].Error, "secret-topic") || strings.Contains(res[0].Error, "127.0.0.1:") && strings.Contains(res[0].Error, "http") {
		t.Fatalf("erreur : %q", res[0].Error)
	}
	// 4xx : définitif, pas de nouvel essai
	n = 0
	srv4 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mu.Lock(); n++; mu.Unlock(); w.WriteHeader(403) }))
	defer srv4.Close()
	_ = s.SetSecret(ctx, ch.ID, "url", srv4.URL+"/x")
	if r := s.Emit(ctx, Event{Kind: EvTest, Title: "t", Message: "m"}); r[0].OK || n != 1 {
		t.Fatalf("4xx : essais=%d %+v", n, r)
	}
}

func TestDedupAndTestNotification(t *testing.T) {
	srv, got := recorder(200)
	defer srv.Close()
	s := newSvc(t, nil)
	ctx := context.Background()
	ch, _ := s.UpsertChannel(ctx, ChannelInput{Name: "n1", Kind: KindNtfy})
	_ = s.SetSecret(ctx, ch.ID, "url", srv.URL+"/t")
	e := Event{Kind: EvPrinterOffline, Title: "off", Message: "m", DedupKey: "offline:pr_1"}
	s.Emit(ctx, e)
	if r := s.Emit(ctx, e); len(r) != 1 || r[0].Skipped == "" {
		t.Fatalf("doublon : %+v", r)
	}
	if len(got()) != 1 {
		t.Fatalf("%d envois", len(got()))
	}
	rs, err := s.Test(ctx, "n1")
	if err != nil || len(rs) != 1 || !rs[0].OK {
		t.Fatalf("%+v %v", rs, err)
	}
	if _, err := s.Test(ctx, "inconnu"); err == nil {
		t.Fatal("canal inconnu")
	}
	last := got()[len(got())-1]
	if !strings.Contains(string(last.body), "Aucun secret") {
		t.Fatalf("corps du test : %s", last.body)
	}
}

func TestValidationRejectsSecretsInConfigAndBadValues(t *testing.T) {
	s := newSvc(t, nil)
	ctx := context.Background()
	bad := []ChannelInput{
		{Name: "", Kind: KindNtfy},
		{Name: "x", Kind: "sms"},
		{Name: "x", Kind: KindNtfy, Events: []string{"inconnu"}},
		{Name: "x", Kind: KindNtfy, Config: ChannelConfig{SMTPHost: "smtp.example.org"}},
		{Name: "x", Kind: KindEmail, Config: ChannelConfig{SMTPHost: "smtp.example.org", From: "a@b.c"}},                                          // pas de destinataire
		{Name: "x", Kind: KindEmail, Config: ChannelConfig{SMTPHost: "smtp.example.org", From: "pas une adresse", To: []string{"a@b.c"}}},         // expéditeur
		{Name: "x", Kind: KindEmail, Config: ChannelConfig{SMTPHost: "smtp.example.org", From: "a@b.c", To: []string{"a@b.c"}, SMTPTLS: "plain"}}, // clair refusé
		{Name: "x", Kind: KindNtfy, Config: ChannelConfig{Priority: "urgent"}},
	}
	for i, in := range bad {
		if _, err := s.UpsertChannel(ctx, in); err == nil {
			t.Errorf("entrée invalide %d acceptée : %+v", i, in)
		}
	}
	ch, _ := s.UpsertChannel(ctx, ChannelInput{Name: "ok", Kind: KindNtfy})
	for _, v := range []string{"ftp://x/y", "javascript:alert(1)", "http://user:pw@host/x", "pas une url"} {
		if err := s.SetSecret(ctx, ch.ID, "url", v); err == nil {
			t.Errorf("URL %q acceptée", v)
		}
	}
	if err := s.SetSecret(ctx, ch.ID, "hmac_key", "x"); err == nil {
		t.Error("secret inconnu pour ce type accepté")
	}
	if _, err := s.UpsertChannel(ctx, ChannelInput{Name: "ok", Kind: KindWebhook}); err == nil {
		t.Error("changement de type d'un canal existant accepté")
	}
	if err := s.DeleteChannel(ctx, "ok"); err != nil {
		t.Fatal(err)
	}
	if len(mustList(t, s)) != 0 {
		t.Fatal("canal non supprimé")
	}
}

// ---- SMTP : serveur de test minimal, TLS implicite puis STARTTLS ----

func testCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

type smtpServer struct {
	addr       string
	mu         sync.Mutex
	data       string
	user, pass string
	sawPlain   bool
}

// startSMTP : implicit=true -> TLS dès la connexion ; sinon STARTTLS (offerStartTLS=false : serveur sans STARTTLS).
func startSMTP(t *testing.T, cert tls.Certificate, implicit, offerStartTLS bool) *smtpServer {
	t.Helper()
	var ln net.Listener
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}}
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln = raw
	if implicit {
		ln = tls.NewListener(raw, cfg)
	}
	t.Cleanup(func() { ln.Close() })
	srv := &smtpServer{addr: raw.Addr().String()}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go srv.serve(c, cfg, !implicit && offerStartTLS, !implicit)
		}
	}()
	return srv
}
