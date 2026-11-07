package notify

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
)

func (srv *smtpServer) serve(c net.Conn, cfg *tls.Config, offerStartTLS, canUpgrade bool) {
	defer func() { c.Close() }()
	r := bufio.NewReader(c)
	w := func(s string) { _, _ = io.WriteString(c, s+"\r\n") }
	w("220 localhost ESMTP")
	tlsOn := !canUpgrade
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			if offerStartTLS && !tlsOn {
				w("250-localhost\r\n250 STARTTLS")
			} else {
				w("250-localhost\r\n250 AUTH PLAIN")
			}
		case cmd == "STARTTLS":
			w("220 go ahead")
			tc := tls.Server(c, cfg)
			if tc.Handshake() != nil {
				return
			}
			c, r, tlsOn = tc, bufio.NewReader(tc), true
		case strings.HasPrefix(cmd, "AUTH PLAIN"):
			srv.mu.Lock()
			srv.sawPlain = srv.sawPlain || !tlsOn
			if parts := strings.Fields(strings.TrimSpace(line)); len(parts) == 3 {
				raw, _ := base64.StdEncoding.DecodeString(parts[2])
				f := strings.Split(string(raw), "\x00")
				if len(f) == 3 {
					srv.user, srv.pass = f[1], f[2]
				}
			}
			srv.mu.Unlock()
			w("235 ok")
		case strings.HasPrefix(cmd, "MAIL FROM"), strings.HasPrefix(cmd, "RCPT TO"):
			w("250 ok")
		case cmd == "DATA":
			w("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			srv.mu.Lock()
			srv.data = b.String()
			srv.mu.Unlock()
			w("250 queued")
		case cmd == "QUIT":
			w("221 bye")
			return
		default:
			w("250 ok")
		}
	}
}

func (srv *smtpServer) snapshot() (data, user, pass string, plain bool) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	return srv.data, srv.user, srv.pass, srv.sawPlain
}

func writeFile(p, s string) error { return os.WriteFile(p, []byte(s), 0o600) }

func emailChannel(t *testing.T, s *Service, addr, mode string) *ChannelView {
	t.Helper()
	host, portS, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portS)
	ch, err := s.UpsertChannel(context.Background(), ChannelInput{Name: "mail", Kind: KindEmail,
		Config: ChannelConfig{SMTPHost: host, SMTPPort: port, SMTPTLS: mode, SMTPUser: "robot", From: "Atelier <atelier@example.org>", To: []string{"moi@example.org"}}})
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

func TestEmailOverImplicitTLSAndStartTLSWithPhoto(t *testing.T) {
	cert, pool := testCert(t)
	for _, mode := range []string{"implicit", "starttls"} {
		srv := startSMTP(t, cert, mode == "implicit", true)
		s := newSvc(t, func(d *Deps) { d.SMTPTLS = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12} })
		ch := emailChannel(t, s, srv.addr, mode)
		if !ch.Ready {
			t.Fatalf("e-mail sans mot de passe : prêt attendu (authentification facultative) : %+v", ch)
		}
		if err := s.SetSecret(context.Background(), ch.ID, "password", "mot-de-passe-smtp"); err != nil {
			t.Fatal(err)
		}
		res := s.Emit(context.Background(), Event{Kind: EvPrintCompleted, Title: "Impression terminée : pièce", Message: "Terminée.", Link: "http://127.0.0.1:7878/impression", Photo: jpg})
		if len(res) != 1 || !res[0].OK {
			t.Fatalf("%s : %+v", mode, res)
		}
		data, user, pass, plain := srv.snapshot()
		if user != "robot" || pass != "mot-de-passe-smtp" || plain {
			t.Fatalf("%s : authentification %q %q en clair=%v", mode, user, pass, plain)
		}
		for _, want := range []string{"Subject: =?UTF-8?q?", "image/jpeg", "filename=\"photo.jpg\"", "multipart/mixed", "To: moi@example.org"} {
			if !strings.Contains(data, want) {
				t.Errorf("%s : %q absent du message :\n%s", mode, want, data)
			}
		}
		if strings.Contains(data, "mot-de-passe-smtp") {
			t.Fatal("mot de passe dans le message")
		}
	}
}

func TestEmailRefusesPlainTextAndUntrustedCertificates(t *testing.T) {
	cert, pool := testCert(t)
	srv := startSMTP(t, cert, false, false) // serveur sans STARTTLS : le clair est refusé
	s := newSvc(t, func(d *Deps) { d.SMTPTLS = &tls.Config{RootCAs: pool}; d.Retries = 1 })
	emailChannel(t, s, srv.addr, "starttls")
	res := s.Emit(context.Background(), Event{Kind: EvTest, Title: "t", Message: "m"})
	if res[0].OK || !strings.Contains(res[0].Error, "STARTTLS") {
		t.Fatalf("clair accepté : %+v", res)
	}
	if data, _, _, _ := srv.snapshot(); data != "" {
		t.Fatal("un message est parti en clair")
	}
	srv2 := startSMTP(t, cert, true, true) // certificat hors du pool : refus
	s2 := newSvc(t, func(d *Deps) { d.Retries = 1 })
	emailChannel(t, s2, srv2.addr, "implicit")
	if res = s2.Emit(context.Background(), Event{Kind: EvTest, Title: "t", Message: "m"}); res[0].OK {
		t.Fatalf("certificat non fiable accepté : %+v", res)
	}
}

func TestSetupPageStoresSecretsAndNeverEchoesThem(t *testing.T) {
	s := newSvc(t, nil)
	ctx := context.Background()
	ch, _ := s.UpsertChannel(ctx, ChannelInput{Name: "hook", Kind: KindWebhook})
	mux := http.NewServeMux()
	s.RegisterPages(mux, nil)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	get := func() string {
		res, err := http.Get(srv.URL + "/setup/notifications")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		page := string(b)
		i := strings.Index(page, `name="nonce" value="`)
		if i < 0 {
			t.Fatalf("pas de nonce : %s", page)
		}
		rest := page[i+len(`name="nonce" value="`):]
		return rest[:strings.Index(rest, `"`)]
	}
	post := func(n string, extra url.Values) (int, string) {
		f := url.Values{"nonce": {n}, "channel": {ch.ID}}
		for k, v := range extra {
			f[k] = v
		}
		res, err := http.PostForm(srv.URL+"/setup/notifications", f)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	nonce := get()
	st, body := post(nonce, url.Values{"secret_url": {"https://hooks.example.org/in/abc123"}, "secret_hmac_key": {"cle-secrete-xyz"}})
	if st != 200 || strings.Contains(body, "abc123") || strings.Contains(body, "cle-secrete-xyz") || !strings.Contains(body, "mis à jour") {
		t.Fatalf("saisie : %d %s", st, body)
	}
	if v, _ := s.GetChannel(ctx, ch.ID); !v.Ready {
		t.Fatalf("canal non prêt après saisie : %+v", v)
	}
	if st, _ := post(nonce, nil); st != http.StatusForbidden { // nonce à usage unique
		t.Fatalf("nonce rejoué : %d", st)
	}
	if st, _ := post("n-importe-quoi", nil); st != http.StatusForbidden {
		t.Fatalf("faux nonce : %d", st)
	}
	if st, _ := post(get(), url.Values{"clear_hmac_key": {"yes"}}); st != 200 {
		t.Fatal("effacement")
	}
	if v, _ := s.GetChannel(ctx, ch.ID); v.Ready {
		t.Fatal("clé effacée : le canal ne doit plus être prêt")
	}
	if st, _ := post(get(), url.Values{"secret_url": {"ftp://x"}}); st != http.StatusBadRequest {
		t.Fatalf("URL invalide : %d", st)
	}
}
