package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

const (
	cb       = "https://claude.ai/api/mcp/auth_callback"
	verifier = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ0123456789-._~"
)

type clock struct{ t atomic.Int64 }

func (c *clock) now() time.Time      { return time.Unix(c.t.Load(), 0).UTC() }
func (c *clock) add(d time.Duration) { c.t.Add(int64(d.Seconds())) }

func newSvc(t *testing.T, o Options) (*Service, *clock) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ck := &clock{}
	ck.t.Store(time.Now().Unix())
	o.Now = ck.now
	return New(st, o), ck
}

func register(t *testing.T, s *Service, ip string) string {
	t.Helper()
	out, e := s.Register(context.Background(), []byte(`{"client_name":"Claude (faux)","redirect_uris":["`+cb+`"]}`), ip)
	if e != nil {
		t.Fatalf("register : %v", e)
	}
	return out["client_id"].(string)
}

func authQuery(cid string) url.Values {
	sum := sha256.Sum256([]byte(verifier))
	return url.Values{"response_type": {"code"}, "client_id": {cid}, "redirect_uri": {cb}, "state": {"s"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}}
}

// Le consentement n'est valable que dans le navigateur qui a ouvert /oauth/authorize (cookie de liaison).
func TestConsentIsBoundToTheInitiatingBrowser(t *testing.T) {
	s, _ := newSvc(t, Options{})
	ctx := context.Background()
	cid := register(t, s, "203.0.113.5")
	res := s.Authorize(ctx, authQuery(cid), "https://h/mcp", "203.0.113.5")
	if res.Pending == nil || res.Bind == "" {
		t.Fatalf("authorize : %+v", res)
	}
	p := res.Pending
	if p.RemoteIP != "203.0.113.5" || p.RequestedAt.IsZero() {
		t.Fatalf("IP et heure de la demande absentes : %+v", p)
	}
	if p.Bound("") || p.Bound("autre-navigateur") {
		t.Fatal("liaison acceptée sans le bon jeton")
	}
	// un tiers qui connaît l'URL et le nonce mais pas le cookie de liaison est refusé, et la demande reste utilisable
	if _, err := s.Decide(ctx, p.ID, p.Nonce, "https://h", "hs_victime", "pas-le-bon", true); err == nil || !strings.Contains(err.Error(), "autre navigateur") {
		t.Fatalf("consentement d'un autre navigateur : %v", err)
	}
	if _, err := s.Decide(ctx, p.ID, p.Nonce, "https://h", "hs_1", "", true); err == nil {
		t.Fatal("consentement sans cookie de liaison accepté")
	}
	loc, err := s.Decide(ctx, p.ID, p.Nonce, "https://h", "hs_1", res.Bind, true)
	if err != nil || !strings.Contains(loc, "code=") {
		t.Fatalf("consentement légitime : %q %v", loc, err)
	}
}

// Limites de débit par adresse et plafond de clients ; une autre adresse n'est pas affectée.
func TestRegisterAndAuthorizeAreRateLimitedPerIP(t *testing.T) {
	s, ck := newSvc(t, Options{RegisterBurst: 3, RegisterEvery: time.Hour, AuthorizeBurst: 2, AuthorizeEvery: time.Hour, MaxClientsPerIP: 100})
	ctx := context.Background()
	body := []byte(`{"client_name":"x","redirect_uris":["` + cb + `"]}`)
	for i := 0; i < 3; i++ {
		if _, e := s.Register(ctx, body, "198.51.100.1"); e != nil {
			t.Fatalf("enregistrement %d : %v", i, e)
		}
	}
	if _, e := s.Register(ctx, body, "198.51.100.1"); e == nil || e.Status != 429 {
		t.Fatalf("4e enregistrement : %v", e)
	}
	if _, e := s.Register(ctx, body, "198.51.100.2"); e != nil {
		t.Fatalf("autre adresse : %v", e)
	}
	cid := register(t, s, "198.51.100.3")
	for i := 0; i < 2; i++ {
		if r := s.Authorize(ctx, authQuery(cid), "https://h/mcp", "198.51.100.3"); r.Pending == nil {
			t.Fatalf("authorize %d : %+v", i, r)
		}
	}
	if r := s.Authorize(ctx, authQuery(cid), "https://h/mcp", "198.51.100.3"); !r.Limited {
		t.Fatalf("3e authorize : %+v", r)
	}
	ck.add(2 * time.Hour) // le débit se reconstitue
	if r := s.Authorize(ctx, authQuery(cid), "https://h/mcp", "198.51.100.3"); r.Pending == nil {
		t.Fatalf("après attente : %+v", r)
	}
}

func TestClientsWithoutGrantExpireAfter15MinutesAndAreCappedPerIP(t *testing.T) {
	s, ck := newSvc(t, Options{RegisterBurst: 100, RegisterEvery: time.Millisecond, MaxClientsPerIP: 4})
	ctx := context.Background()
	ids := []string{}
	for i := 0; i < 4; i++ {
		ids = append(ids, register(t, s, "192.0.2.9"))
	}
	if _, e := s.Register(ctx, []byte(`{"redirect_uris":["`+cb+`"]}`), "192.0.2.9"); e == nil || e.Status != 429 {
		t.Fatalf("plafond par adresse : %v", e)
	}
	ck.add(16 * time.Minute)
	register(t, s, "192.0.2.10") // l'enregistrement purge les clients sans autorisation de plus de 15 min
	for _, id := range ids {
		if _, err := s.client(ctx, id); err == nil {
			t.Fatalf("client %s sans autorisation conservé après 15 min", id)
		}
	}
	// et l'adresse peut de nouveau en enregistrer
	register(t, s, "192.0.2.9")
}
