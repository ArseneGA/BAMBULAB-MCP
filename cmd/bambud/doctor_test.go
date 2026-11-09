package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/httpapi"
)

const doctorToken = "doctor-secret-token-0123456789"

// publicTLSServer : la surface publique de bambud derrière un serveur TLS de test (certificat auto-signé,
// que le client de test accepte). wrap permet de simuler une surface mal réglée.
func publicTLSServer(t *testing.T, wrap func(http.Handler) http.Handler) *httptest.Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c, err := core.Open(ctx, core.Config{DataDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		PollInterval: 10 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	srv := httptest.NewUnstartedServer(nil)
	base := "https://" + srv.Listener.Addr().String()
	h := httpapi.NewHandlers(c, httpapi.Options{APIToken: doctorToken, PublicListenURL: base}).Public
	if wrap != nil {
		h = wrap(h)
	}
	srv.Config.Handler = h
	srv.StartTLS()
	t.Cleanup(func() { srv.Close(); cancel(); <-done; c.Close() })
	return srv
}

func TestDoctorConnectorAllOKOnConformantSurface(t *testing.T) {
	srv := publicTLSServer(t, nil)
	checks, err := runConnectorDoctor(t.Context(), doctorOptions{URL: srv.URL + "/mcp", Token: doctorToken, Client: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range checks {
		// La vue n'est pas toujours embarquée dans un binaire de test (go test sans npm run build) : avertissement admis.
		if c.Status == doctorKO || (c.Status == doctorWarn && !strings.Contains(c.Detail, "vue peut-être non construite")) {
			t.Errorf("%s : %s %s", c.Name, c.Status, c.Detail)
		}
	}
	need := []string{"TLS", "POST /mcp sans jeton", "Enregistrement dynamique (DCR)", "Chemins interdits", "MCP tools/list", "Carte de request_print_confirmation", "Carte de get_print_status", "resources/read ui://bambu/print-live.html"}
	for _, n := range need {
		found := false
		for _, c := range checks {
			if c.Name == n {
				found = true
			}
		}
		if !found {
			t.Errorf("contrôle manquant : %s", n)
		}
	}
}

func TestDoctorConnectorFlagsExposedForbiddenPathAndNeverPrintsToken(t *testing.T) {
	srv := publicTLSServer(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/confirm/") {
				w.WriteHeader(200)
				_, _ = w.Write([]byte("page de confirmation " + doctorToken)) // un secret qui ne doit pas ressortir
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	tokFile := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(tokFile, []byte(doctorToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// doctorMain utilise le client par défaut (certificat auto-signé refusé) : on passe par le cœur avec le client de test,
	// puis par printDoctor, exactement le chemin de doctorMain.
	checks, err := runConnectorDoctor(t.Context(), doctorOptions{URL: srv.URL, Token: doctorToken, Client: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code := printDoctor(&out, checks, false, doctorToken)
	if code == 0 || !strings.Contains(out.String(), "chemin interdit exposé") || !strings.Contains(out.String(), "/confirm/x") {
		t.Fatalf("code %d\n%s", code, out.String())
	}
	if strings.Contains(out.String(), doctorToken) {
		t.Fatal("le jeton apparaît dans la sortie")
	}
	var js bytes.Buffer
	if printDoctor(&js, checks, true, doctorToken) == 0 || !strings.Contains(js.String(), `"ok": false`) || strings.Contains(js.String(), doctorToken) {
		t.Fatalf("sortie JSON : %s", js.String())
	}

	// doctorMain refuse un certificat non approuvé : TLS KO, sortie sans jeton, code non nul.
	var so, se bytes.Buffer
	if rc := doctorMain([]string{"connector", "--url", srv.URL, "--token-file", tokFile}, &so, &se); rc == 0 {
		t.Fatalf("certificat auto-signé accepté : %s", so.String())
	}
	if strings.Contains(so.String()+se.String(), doctorToken) || !strings.Contains(so.String(), "KO") {
		t.Fatalf("sortie : %s | %s", so.String(), se.String())
	}
}

func TestDoctorConnectorMisconfiguredIssuerAndUsage(t *testing.T) {
	// Ressource annoncée différente de l'URL testée (adresse publique mal réglée) : KO explicite.
	srv := publicTLSServer(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-protected-resource" {
				_, _ = w.Write([]byte(`{"resource":"https://autre.example.net/mcp","authorization_servers":["https://autre.example.net"]}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	checks, err := runConnectorDoctor(t.Context(), doctorOptions{URL: srv.URL, Client: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ko := false
	for _, ch := range checks {
		if ch.Status == doctorKO && strings.Contains(ch.Detail, "BAMBU_PUBLIC_LISTEN_URL") {
			ko = true
		}
		if ch.Name == "Contrôles MCP avec jeton" && ch.Status != doctorSkip {
			t.Errorf("sans jeton les contrôles MCP doivent être ignorés : %v", ch)
		}
	}
	if !ko {
		t.Errorf("émetteur mal réglé non détecté : %+v", checks)
	}

	for _, bad := range []string{"http://exemple.org", "ftp://x", "https://x/chemin", "", "https://"} {
		if _, err := normalizeConnectorURL(bad, false); err == nil {
			t.Errorf("%q doit être refusée", bad)
		}
	}
	if _, err := normalizeConnectorURL("http://exemple.org", true); err == nil {
		t.Error("--allow-http ne vaut que pour la boucle locale")
	}
	if u, err := normalizeConnectorURL("http://127.0.0.1:7879/mcp/", true); err != nil || u != "http://127.0.0.1:7879" {
		t.Errorf("boucle locale : %q %v", u, err)
	}
	if u, _ := normalizeConnectorURL("https://a.ts.net/mcp", false); u != "https://a.ts.net" {
		t.Errorf("normalisation : %q", u)
	}
	var so, se bytes.Buffer
	if doctorMain(nil, &so, &se) != 2 || doctorMain([]string{"connector"}, &so, &se) != 2 {
		t.Error("usage : code 2 attendu")
	}
}
