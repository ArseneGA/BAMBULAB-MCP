package humanauth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth/passkeytest"
)

const pkOrigin = "http://localhost:7878"

func pkReq(host, cookie string) *http.Request {
	r := httptest.NewRequest("GET", "http://"+host+"/x", nil)
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: humanauth.CookieName, Value: cookie})
	}
	return r
}

func pkSetup(t *testing.T) (*humanauth.Passkeys, *clock) {
	t.Helper()
	st, ck := launchStore(t)
	pk := humanauth.PasskeysFor(st)
	pk.Configure(humanauth.PasskeyConfig{})
	return pk, ck
}

func pkRegister(t *testing.T, pk *humanauth.Passkeys, a *passkeytest.Authenticator) humanauth.PasskeyInfo {
	t.Helper()
	r := pkReq("localhost:7878", "tok-A")
	opts, err := pk.BeginRegistration(context.Background(), r, humanauth.BrowserKey(r))
	if err != nil {
		t.Fatal(err)
	}
	cred, err := a.Create(opts, pkOrigin)
	if err != nil {
		t.Fatal(err)
	}
	info, err := pk.FinishRegistration(context.Background(), r, humanauth.BrowserKey(r), "ma clé", "human:test", cred)
	if err != nil {
		t.Fatalf("enregistrement : %v", err)
	}
	return info
}

func subj(kind, id, binding string) humanauth.ApprovalSubject {
	return humanauth.ApprovalSubject{Kind: kind, ID: id, Binding: binding}
}

// Vecteurs générés (clé de test ES256 puis EdDSA) : enregistrement, puis approbation vérifiée de bout en bout.
func TestPasskeyRegisterAndApprove(t *testing.T) {
	for name, alg := range map[string]int{"ES256": passkeytest.ES256, "EdDSA": passkeytest.EdDSA} {
		t.Run(name, func(t *testing.T) {
			pk, _ := pkSetup(t)
			ctx := context.Background()
			a := passkeytest.Must(alg)
			info := pkRegister(t, pk, a)
			if info.ID == "" || info.RPID != "localhost" {
				t.Fatalf("info : %+v", info)
			}
			if n, _ := pk.Count(ctx); n != 1 {
				t.Fatalf("compte : %d", n)
			}
			r := pkReq("localhost:7878", "tok-A")
			opts, err := pk.BeginApproval(ctx, r, subj("print", "cf_1", "bind-1"))
			if err != nil {
				t.Fatal(err)
			}
			as, err := a.Get(opts, pkOrigin)
			if err != nil {
				t.Fatal(err)
			}
			got, err := pk.FinishApproval(ctx, r, subj("print", "cf_1", "bind-1"), as)
			if err != nil {
				t.Fatalf("approbation : %v", err)
			}
			if got.ID != info.ID || got.SignCount != 1 {
				t.Fatalf("passkey utilisée : %+v", got)
			}
			// L'état durable est à jour (compteur, dernier usage).
			list, _ := pk.List(ctx)
			if len(list) != 1 || list[0].SignCount != 1 || list[0].LastUsedAt.IsZero() {
				t.Fatalf("liste : %+v", list)
			}
		})
	}
}

func TestPasskeyReplayIsRefused(t *testing.T) {
	pk, _ := pkSetup(t)
	ctx := context.Background()
	a := passkeytest.Must(passkeytest.ES256)
	pkRegister(t, pk, a)
	r := pkReq("localhost:7878", "tok-A")
	opts, _ := pk.BeginApproval(ctx, r, subj("print", "cf_1", "b"))
	as, _ := a.Get(opts, pkOrigin)
	if _, err := pk.FinishApproval(ctx, r, subj("print", "cf_1", "b"), as); err != nil {
		t.Fatal(err)
	}
	// Même assertion rejouée : le challenge est consommé.
	if _, err := pk.FinishApproval(ctx, r, subj("print", "cf_1", "b"), as); !errors.Is(err, humanauth.ErrPasskeyChallenge) {
		t.Fatalf("rejeu : %v", err)
	}
	// Rejeu en parallèle d'une même assertion : au plus un succès.
	opts2, _ := pk.BeginApproval(ctx, r, subj("print", "cf_2", "b"))
	as2, _ := a.Get(opts2, pkOrigin)
	var ok atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := pk.FinishApproval(ctx, r, subj("print", "cf_2", "b"), as2); err == nil {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 {
		t.Fatalf("%d succès pour une seule assertion", ok.Load())
	}
}

func TestPasskeyWrongOriginIsRefused(t *testing.T) {
	pk, _ := pkSetup(t)
	ctx := context.Background()
	a := passkeytest.Must(passkeytest.ES256)
	pkRegister(t, pk, a)
	r := pkReq("localhost:7878", "tok-A")
	opts, _ := pk.BeginApproval(ctx, r, subj("print", "cf_1", "b"))
	as, _ := a.Get(opts, "https://evil.example") // le « navigateur » est sur un autre site
	if _, err := pk.FinishApproval(ctx, r, subj("print", "cf_1", "b"), as); !errors.Is(err, humanauth.ErrPasskeyInvalid) {
		t.Fatalf("mauvaise origine : %v", err)
	}
	// Même origine textuelle mais mauvais port : refusé aussi.
	opts, _ = pk.BeginApproval(ctx, r, subj("print", "cf_1", "b"))
	as, _ = a.Get(opts, "http://localhost:9999")
	if _, err := pk.FinishApproval(ctx, r, subj("print", "cf_1", "b"), as); !errors.Is(err, humanauth.ErrPasskeyInvalid) {
		t.Fatalf("mauvais port : %v", err)
	}
}

func TestPasskeyWrongRPIDIsRefused(t *testing.T) {
	pk, _ := pkSetup(t)
	ctx := context.Background()
	a := passkeytest.Must(passkeytest.ES256)
	pkRegister(t, pk, a)
	r := pkReq("localhost:7878", "tok-A")
	opts, _ := pk.BeginApproval(ctx, r, subj("print", "cf_1", "b"))
	chal, _ := passkeytest.Challenge(opts)
	as, _ := a.GetChallenge("autre.example", chal, pkOrigin) // authData signé pour un autre RP ID
	if _, err := pk.FinishApproval(ctx, r, subj("print", "cf_1", "b"), as); !errors.Is(err, humanauth.ErrPasskeyInvalid) {
		t.Fatalf("mauvais RP ID : %v", err)
	}
}

func TestPasskeyChallengeOfAnotherConfirmationIsRefused(t *testing.T) {
	pk, _ := pkSetup(t)
	ctx := context.Background()
	a := passkeytest.Must(passkeytest.ES256)
	pkRegister(t, pk, a)
	r := pkReq("localhost:7878", "tok-A")
	optsA, _ := pk.BeginApproval(ctx, r, subj("print", "cf_A", "bind-A"))
	optsB, _ := pk.BeginApproval(ctx, r, subj("print", "cf_B", "bind-B"))
	// Signature faite pour B, présentée pour A.
	asB, _ := a.Get(optsB, pkOrigin)
	if _, err := pk.FinishApproval(ctx, r, subj("print", "cf_A", "bind-A"), asB); !errors.Is(err, humanauth.ErrPasskeyChallenge) {
		t.Fatalf("challenge de B présenté pour A : %v", err)
	}
	// Signature faite pour A, présentée pour B.
	asA, _ := a.Get(optsA, pkOrigin)
	if _, err := pk.FinishApproval(ctx, r, subj("print", "cf_B", "bind-B"), asA); !errors.Is(err, humanauth.ErrPasskeyChallenge) {
		t.Fatalf("challenge de A présenté pour B : %v", err)
	}
	// Une demande d'arrêt n'est pas une confirmation de lancement.
	opts3, _ := pk.BeginApproval(ctx, r, subj("print", "x1", "job-1"))
	as3, _ := a.Get(opts3, pkOrigin)
	if _, err := pk.FinishApproval(ctx, r, subj("stop", "x1", "job-1"), as3); !errors.Is(err, humanauth.ErrPasskeyChallenge) {
		t.Fatalf("challenge de lancement présenté pour un arrêt : %v", err)
	}
}

func TestPasskeyBindingChangeIsRefused(t *testing.T) {
	pk, _ := pkSetup(t)
	ctx := context.Background()
	a := passkeytest.Must(passkeytest.ES256)
	pkRegister(t, pk, a)
	r := pkReq("localhost:7878", "tok-A")
	opts, _ := pk.BeginApproval(ctx, r, subj("print", "cf_1", "empreinte-avant"))
	as, _ := a.Get(opts, pkOrigin)
	// Entre l'ouverture de la page et la signature, la liaison (fichier, plaque, matières, options) a changé.
	if _, err := pk.FinishApproval(ctx, r, subj("print", "cf_1", "empreinte-apres"), as); !errors.Is(err, humanauth.ErrPasskeyChallenge) {
		t.Fatalf("liaison changée : %v", err)
	}
}

func TestPasskeyChallengeIsBoundToTheBrowserAndExpires(t *testing.T) {
	pk, ck := pkSetup(t)
	ctx := context.Background()
	a := passkeytest.Must(passkeytest.ES256)
	pkRegister(t, pk, a)
	rA, rB := pkReq("localhost:7878", "tok-A"), pkReq("localhost:7878", "tok-B")
	opts, _ := pk.BeginApproval(ctx, rA, subj("print", "cf_1", "b"))
	as, _ := a.Get(opts, pkOrigin)
	if _, err := pk.FinishApproval(ctx, rB, subj("print", "cf_1", "b"), as); !errors.Is(err, humanauth.ErrPasskeyChallenge) {
		t.Fatalf("autre navigateur : %v", err)
	}
	opts, _ = pk.BeginApproval(ctx, rA, subj("print", "cf_1", "b"))
	as, _ = a.Get(opts, pkOrigin)
	ck.add(humanauth.PasskeyChallengeTTL + time.Second)
	if _, err := pk.FinishApproval(ctx, rA, subj("print", "cf_1", "b"), as); !errors.Is(err, humanauth.ErrPasskeyChallenge) {
		t.Fatalf("challenge périmé : %v", err)
	}
}

func TestPasskeyCounterRegressionIsRefused(t *testing.T) {
	pk, _ := pkSetup(t)
	ctx := context.Background()
	a := passkeytest.Must(passkeytest.ES256)
	pkRegister(t, pk, a)
	r := pkReq("localhost:7878", "tok-A")
	for i := 1; i <= 3; i++ { // compteur 1, 2, 3
		opts, _ := pk.BeginApproval(ctx, r, subj("print", "cf_1", "b"))
		as, _ := a.Get(opts, pkOrigin)
		if _, err := pk.FinishApproval(ctx, r, subj("print", "cf_1", "b"), as); err != nil {
			t.Fatalf("approbation %d : %v", i, err)
		}
	}
	// Un clone repart d'un compteur plus ancien : 2 <= 3.
	a.Counter = 1
	opts, _ := pk.BeginApproval(ctx, r, subj("print", "cf_1", "b"))
	as, _ := a.Get(opts, pkOrigin)
	if _, err := pk.FinishApproval(ctx, r, subj("print", "cf_1", "b"), as); !errors.Is(err, humanauth.ErrPasskeyCounter) {
		t.Fatalf("compteur régressif : %v", err)
	}
	// Compteur identique : refusé aussi.
	a.Counter = 2
	opts, _ = pk.BeginApproval(ctx, r, subj("print", "cf_1", "b"))
	as, _ = a.Get(opts, pkOrigin)
	if _, err := pk.FinishApproval(ctx, r, subj("print", "cf_1", "b"), as); !errors.Is(err, humanauth.ErrPasskeyCounter) {
		t.Fatalf("compteur identique : %v", err)
	}
	// Le compteur en base n'a pas bougé et la clé légitime reprend.
	a.Counter = 3
	opts, _ = pk.BeginApproval(ctx, r, subj("print", "cf_1", "b"))
	as, _ = a.Get(opts, pkOrigin)
	if _, err := pk.FinishApproval(ctx, r, subj("print", "cf_1", "b"), as); err != nil {
		t.Fatalf("clé légitime : %v", err)
	}
}

// Les passkeys synchronisées (iCloud, Google) envoient toujours 0 : acceptées (0 puis 0), jamais un rejeu (challenge).
func TestPasskeyZeroCounterAuthenticatorIsAccepted(t *testing.T) {
	pk, _ := pkSetup(t)
	ctx := context.Background()
	a := passkeytest.Must(passkeytest.ES256)
	pkRegister(t, pk, a)
	r := pkReq("localhost:7878", "tok-A")
	for range 2 {
		opts, _ := pk.BeginApproval(ctx, r, subj("print", "cf_1", "b"))
		chal, _ := passkeytest.Challenge(opts)
		a.Counter = ^uint32(0) // Get incrémente : 0 après débordement
		as, _ := a.GetChallenge("localhost", chal, pkOrigin)
		if _, err := pk.FinishApproval(ctx, r, subj("print", "cf_1", "b"), as); err != nil {
			t.Fatalf("compteur à zéro : %v", err)
		}
	}
}

func TestPasskeyUserVerificationIsRequired(t *testing.T) {
	pk, _ := pkSetup(t)
	ctx := context.Background()
	a := passkeytest.Must(passkeytest.ES256)
	pkRegister(t, pk, a)
	r := pkReq("localhost:7878", "tok-A")
	a.NoUserVerification = true
	opts, _ := pk.BeginApproval(ctx, r, subj("print", "cf_1", "b"))
	as, _ := a.Get(opts, pkOrigin)
	if _, err := pk.FinishApproval(ctx, r, subj("print", "cf_1", "b"), as); !errors.Is(err, humanauth.ErrPasskeyInvalid) {
		t.Fatalf("sans vérification de l'utilisateur : %v", err)
	}
	// À l'enregistrement aussi.
	r2 := pkReq("localhost:7878", "tok-A")
	b := passkeytest.Must(passkeytest.EdDSA)
	b.NoUserVerification = true
	ro, _ := pk.BeginRegistration(ctx, r2, humanauth.BrowserKey(r2))
	cred, _ := b.Create(ro, pkOrigin)
	if _, err := pk.FinishRegistration(ctx, r2, humanauth.BrowserKey(r2), "x", "t", cred); !errors.Is(err, humanauth.ErrPasskeyInvalid) {
		t.Fatalf("enregistrement sans vérification : %v", err)
	}
}

func TestPasskeyForgedSignatureAndUnknownKey(t *testing.T) {
	pk, _ := pkSetup(t)
	ctx := context.Background()
	a := passkeytest.Must(passkeytest.ES256)
	pkRegister(t, pk, a)
	r := pkReq("localhost:7878", "tok-A")
	// Une autre clé, avec le même identifiant de credential (pas la clé enregistrée) : signature invalide.
	imposter := passkeytest.Must(passkeytest.ES256)
	imposter.CredID = a.CredID
	opts, _ := pk.BeginApproval(ctx, r, subj("print", "cf_1", "b"))
	as, _ := imposter.Get(opts, pkOrigin)
	if _, err := pk.FinishApproval(ctx, r, subj("print", "cf_1", "b"), as); !errors.Is(err, humanauth.ErrPasskeyInvalid) {
		t.Fatalf("signature d'une autre clé : %v", err)
	}
	// Une clé jamais enregistrée.
	stranger := passkeytest.Must(passkeytest.EdDSA)
	opts, _ = pk.BeginApproval(ctx, r, subj("print", "cf_1", "b"))
	as, _ = stranger.Get(opts, pkOrigin)
	if _, err := pk.FinishApproval(ctx, r, subj("print", "cf_1", "b"), as); !errors.Is(err, humanauth.ErrPasskeyInvalid) {
		t.Fatalf("clé inconnue : %v", err)
	}
	// Données absentes ou corrompues.
	for _, bad := range []string{"", "{}", "pas du json", `{"id":"x"}`} {
		if _, err := pk.FinishApproval(ctx, r, subj("print", "cf_1", "b"), []byte(bad)); err == nil {
			t.Fatalf("%q accepté", bad)
		}
	}
}

func TestPasskeyRevokedKeyIsRefusedAndRequireSetting(t *testing.T) {
	pk, _ := pkSetup(t)
	ctx := context.Background()
	a := passkeytest.Must(passkeytest.ES256)
	info := pkRegister(t, pk, a)
	if pk.Required(ctx) {
		t.Fatal("défaut : la passkey doit rester facultative")
	}
	if err := pk.SetRequired(ctx, true, "t"); err != nil || !pk.Required(ctx) {
		t.Fatalf("exiger : %v", err)
	}
	if _, err := pk.Revoke(ctx, info.ID, "t"); !errors.Is(err, humanauth.ErrPasskeyLastKept) {
		t.Fatalf("révoquer la dernière passkey exigée : %v", err)
	}
	if err := pk.SetRequired(ctx, false, "t"); err != nil || pk.Required(ctx) {
		t.Fatalf("ne plus exiger : %v", err)
	}
	r := pkReq("localhost:7878", "tok-A")
	opts, _ := pk.BeginApproval(ctx, r, subj("print", "cf_1", "b"))
	as, _ := a.Get(opts, pkOrigin)
	if _, err := pk.Revoke(ctx, info.ID[:5], "t"); err != nil {
		t.Fatalf("révocation par préfixe : %v", err)
	}
	if _, err := pk.FinishApproval(ctx, r, subj("print", "cf_1", "b"), as); !errors.Is(err, humanauth.ErrPasskeyInvalid) {
		t.Fatalf("passkey révoquée : %v", err)
	}
	if _, err := pk.BeginApproval(ctx, r, subj("print", "cf_1", "b")); !errors.Is(err, humanauth.ErrPasskeyNone) {
		t.Fatalf("plus aucune passkey : %v", err)
	}
	// Exiger sans passkey est refusé (sinon plus aucune approbation possible).
	if err := pk.SetRequired(ctx, true, "t"); !errors.Is(err, humanauth.ErrPasskeyNone) {
		t.Fatalf("exiger sans passkey : %v", err)
	}
}

func TestPasskeyRegistrationNeedsTheSameBrowserAndIsSingleUse(t *testing.T) {
	pk, _ := pkSetup(t)
	ctx := context.Background()
	a := passkeytest.Must(passkeytest.ES256)
	rA, rB := pkReq("localhost:7878", "tok-A"), pkReq("localhost:7878", "tok-B")
	opts, _ := pk.BeginRegistration(ctx, rA, humanauth.BrowserKey(rA))
	cred, _ := a.Create(opts, pkOrigin)
	if _, err := pk.FinishRegistration(ctx, rB, humanauth.BrowserKey(rB), "x", "t", cred); !errors.Is(err, humanauth.ErrPasskeyChallenge) {
		t.Fatalf("autre navigateur : %v", err)
	}
	// Le challenge est consommé par la tentative : même le bon navigateur doit recommencer.
	if _, err := pk.FinishRegistration(ctx, rA, humanauth.BrowserKey(rA), "x", "t", cred); !errors.Is(err, humanauth.ErrPasskeyChallenge) {
		t.Fatalf("rejeu : %v", err)
	}
	// Mauvaise origine à l'enregistrement.
	opts, _ = pk.BeginRegistration(ctx, rA, humanauth.BrowserKey(rA))
	cred, _ = a.Create(opts, "https://evil.example")
	if _, err := pk.FinishRegistration(ctx, rA, humanauth.BrowserKey(rA), "x", "t", cred); !errors.Is(err, humanauth.ErrPasskeyInvalid) {
		t.Fatalf("mauvaise origine : %v", err)
	}
	if n, _ := pk.Count(ctx); n != 0 {
		t.Fatalf("aucune passkey ne devait être enregistrée : %d", n)
	}
	// Plusieurs passkeys : une même clé ne s'enregistre pas deux fois, la limite est tenue.
	pkRegister(t, pk, a)
	opts, _ = pk.BeginRegistration(ctx, rA, humanauth.BrowserKey(rA))
	if !strings.Contains(string(opts), "excludeCredentials") {
		t.Fatalf("les passkeys existantes doivent être exclues : %s", opts)
	}
	for i := 1; i < humanauth.MaxPasskeys; i++ {
		pkRegister(t, pk, passkeytest.Must(passkeytest.ES256))
	}
	if _, err := pk.BeginRegistration(ctx, rA, humanauth.BrowserKey(rA)); !errors.Is(err, humanauth.ErrPasskeyLimit) {
		t.Fatalf("limite : %v", err)
	}
}

func TestPasskeyEnvironmentRules(t *testing.T) {
	cases := []struct {
		name   string
		urls   []string
		host   string
		https  bool
		wantID string
		wantOr string
		err    error
	}{
		{"localhost sans URL publique", nil, "localhost:7878", false, "localhost", "http://localhost:7878", nil},
		{"LAN en http refusé", []string{"http://bambu.lan:7878"}, "bambu.lan:7878", false, "", "", humanauth.ErrPasskeyInsecure},
		{"adresse IP refusée (https)", nil, "192.168.1.20:7878", true, "", "", humanauth.ErrPasskeyIPHost},
		{"loopback IP refusée", nil, "127.0.0.1:7878", false, "", "", humanauth.ErrPasskeyIPHost},
		{"http sur un nom non localhost", nil, "atelier.example:80", false, "", "", humanauth.ErrPasskeyInsecure},
		{"https tailscale", []string{"https://atelier.tail1234.ts.net", "http://bambu.lan:7878"}, "atelier.tail1234.ts.net", true, "atelier.tail1234.ts.net", "https://atelier.tail1234.ts.net", nil},
		{"LAN http listé à côté d'un https : refusé", []string{"https://atelier.tail1234.ts.net", "http://bambu.lan:7878"}, "bambu.lan:7878", false, "", "", humanauth.ErrPasskeyInsecure},
		{"localhost avec RP https configuré : à refuser (autre site)", []string{"https://atelier.tail1234.ts.net"}, "localhost:7878", false, "", "", humanauth.ErrPasskeyOrigin},
		{"https non déclaré", []string{"https://atelier.tail1234.ts.net"}, "autre.example", true, "", "", humanauth.ErrPasskeyOrigin},
		{"deux origines https sous le même RP ID", []string{"https://atelier.example.com", "https://hp.atelier.example.com:8443"}, "hp.atelier.example.com:8443", true, "atelier.example.com", "https://hp.atelier.example.com:8443", nil},
		{"https sans URL publique (proxy)", nil, "bambu.example.org", true, "bambu.example.org", "https://bambu.example.org", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, _ := launchStore(t)
			pk := humanauth.PasskeysFor(st)
			pk.Configure(humanauth.PasskeyConfig{PublicURLs: c.urls, Secure: func(*http.Request) bool { return c.https }})
			env, err := pk.Env(pkReq(c.host, "tok"))
			if c.err != nil {
				if !errors.Is(err, c.err) {
					t.Fatalf("erreur = %v, attendu %v", err, c.err)
				}
				return
			}
			if err != nil || env.RPID != c.wantID || env.Origin != c.wantOr {
				t.Fatalf("env = %+v, %v", env, err)
			}
		})
	}
}

// Une passkey enregistrée sous un RP ID n'est pas proposée sous un autre.
func TestPasskeyIsBoundToItsRPID(t *testing.T) {
	st, _ := launchStore(t)
	pk := humanauth.PasskeysFor(st)
	ctx := context.Background()
	pk.Configure(humanauth.PasskeyConfig{})
	a := passkeytest.Must(passkeytest.ES256)
	pkRegister(t, pk, a) // RP ID localhost
	pk.Configure(humanauth.PasskeyConfig{PublicURLs: []string{"https://atelier.example.com"}, Secure: func(*http.Request) bool { return true }})
	r := pkReq("atelier.example.com", "tok-A")
	if _, err := pk.BeginApproval(ctx, r, subj("print", "cf_1", "b")); !errors.Is(err, humanauth.ErrPasskeyNone) {
		t.Fatalf("la passkey de localhost ne doit pas être proposée sur atelier.example.com : %v", err)
	}
}
