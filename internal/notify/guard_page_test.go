package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// La page humaine crée les canaux et applique la même garde que l'API.
func TestSetupPageCreatesChannelsButRefusesPrivateSMTP(t *testing.T) {
	s := strict(t)
	mux := http.NewServeMux()
	s.RegisterPages(mux, nil)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	post := func(extra url.Values) int {
		f := url.Values{"nonce": {s.newNonce()}, "action": {"save_channel"}, "enabled": {"yes"}}
		for k, v := range extra {
			f[k] = v
		}
		res, err := http.PostForm(srv.URL+"/setup/notifications", f)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if c := post(url.Values{"name": {"mail"}, "kind": {"email"}, "smtp_host": {"127.0.0.1"}, "smtp_port": {"587"}, "from": {"a@example.org"}, "to": {"b@example.org"}}); c != http.StatusBadRequest {
		t.Fatalf("SMTP local accepté : %d", c)
	}
	if c := post(url.Values{"name": {"mail"}, "kind": {"email"}, "smtp_host": {"smtp.example.org"}, "smtp_port": {"587"}, "from": {"a@example.org"}, "to": {"b@example.org, c@example.org"}}); c != http.StatusOK {
		t.Fatalf("SMTP public refusé : %d", c)
	}
	v, err := s.find(context.Background(), "mail")
	if err != nil || len(v.Config.To) != 2 {
		t.Fatalf("canal : %+v %v", v, err)
	}
}
