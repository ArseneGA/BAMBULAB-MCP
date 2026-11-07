package httpapi_test

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/humanauth"
)

// Un lien de consentement transmis à un navigateur enregistré qui n'a pas ouvert la demande est refusé ;
// la page montre le nom comme « déclaré par le client », l'hôte de retour, l'adresse et l'heure.
func TestOAuthConsentPhishingIsRefused(t *testing.T) {
	e := newOAuthEnv(t)
	cid := e.register(claudeCallback)
	hdr := map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate"}
	// l'attaquant ouvre /oauth/authorize dans SON navigateur (client sans cookie enregistré)
	attacker := newBrowser(t, false)
	res, body := attacker.get(e.srv+e.authURL(cid, claudeCallback, nil), hdr)
	m := consentRe.FindStringSubmatch(body)
	if res.StatusCode != 200 || m == nil {
		t.Fatalf("authorize : %d", res.StatusCode)
	}
	for _, want := range []string{"nom déclaré par le client", "claude.ai", "127.0.0.1"} {
		if !strings.Contains(body, want) {
			t.Errorf("page « Continuer » : %q absent", want)
		}
	}
	// la victime (navigateur enregistré) reçoit le lien : refus, pas de formulaire
	r2, b2 := e.human.get(e.srv+"/oauth/consent/"+m[1], map[string]string{"Sec-Fetch-Site": "same-origin"})
	if r2.StatusCode != 403 || nonceRe.MatchString(b2) {
		t.Fatalf("consentement via un autre navigateur : %d", r2.StatusCode)
	}
	// le navigateur initiateur, une fois enregistré, voit la page avec les éléments de reconnaissance
	e.human.get(e.srv+e.authURL(cid, claudeCallback, nil), hdr)
	_, b3 := e.human.get(e.srv+"/oauth/consent/"+consentRe.FindStringSubmatch(func() string {
		_, b := e.human.get(e.srv+e.authURL(cid, claudeCallback, nil), hdr)
		return b
	}())[1], map[string]string{"Sec-Fetch-Site": "same-origin"})
	for _, want := range []string{"Nom déclaré par le client (non vérifié)", "claude.ai", "127.0.0.1", "Heure de la demande"} {
		if !strings.Contains(b3, want) {
			t.Errorf("page de consentement : %q absent", want)
		}
	}
	if strings.Contains(b3, "<dt>Application</dt>") {
		t.Error("le nom du client est encore présenté comme fiable")
	}
}

// Révoquer un navigateur enregistré révoque les autorisations OAuth qu'il a accordées.
func TestRevokingABrowserRevokesItsOAuthGrants(t *testing.T) {
	e := newOAuthEnv(t)
	_, tok := e.fullGrant()
	access := tok["access_token"].(string)
	if st := e.mcpStatus(access); st != 200 {
		t.Fatalf("jeton valide : %d", st)
	}
	h := humanauth.New(e.c.Store, humanauth.Options{})
	sessions, err := h.List(context.Background())
	if err != nil || len(sessions) == 0 {
		t.Fatalf("sessions : %v", err)
	}
	if _, err := h.Revoke(context.Background(), sessions[0].ID); err != nil {
		t.Fatal(err)
	}
	if st := e.mcpStatus(access); st != 401 {
		t.Fatalf("jeton après révocation du navigateur : %d (401 attendu)", st)
	}
	if st, _ := e.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {"x"}, "refresh_token": {tok["refresh_token"].(string)}}); st == 200 {
		t.Fatal("refresh accepté après révocation du navigateur")
	}
	// révoquer tous les navigateurs révoque toutes les autorisations OAuth
	e2 := newOAuthEnv(t)
	_, tok2 := e2.fullGrant()
	h2 := humanauth.New(e2.c.Store, humanauth.Options{})
	if n, err := h2.RevokeAll(context.Background()); err != nil || n == 0 {
		t.Fatalf("RevokeAll : %d %v", n, err)
	}
	if st := e2.mcpStatus(tok2["access_token"].(string)); st != 401 {
		t.Fatalf("après RevokeAll : %d", st)
	}
}
