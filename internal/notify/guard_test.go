package notify

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func reflectField(v any, name string) (reflect.StructField, bool) {
	return reflect.TypeOf(v).FieldByName(name)
}

func strict(t *testing.T) *Service {
	t.Helper()
	t.Setenv("BAMBU_NOTIFY_ALLOW_PRIVATE", "")
	return newSvc(t, func(d *Deps) { d.AllowPrivate = false })
}

func emailInput(host string, port int, user string) ChannelInput {
	return ChannelInput{Name: "mail", Kind: KindEmail, Config: ChannelConfig{SMTPHost: host, SMTPPort: port, SMTPUser: user, From: "a@example.org", To: []string{"b@example.org"}}}
}

// Le modèle ne peut ni créer un canal, ni changer sa destination : PatchChannel n'agit que sur enabled et events.
func TestModelPatchCannotRedirectAChannel(t *testing.T) {
	s := newSvc(t, nil)
	ctx := context.Background()
	if _, err := s.UpsertChannel(ctx, emailInput("smtp.example.org", 587, "robot")); err != nil {
		t.Fatal(err)
	}
	off := false
	v, err := s.PatchChannel(ctx, ChannelPatch{Name: "mail", Enabled: &off, Events: []string{EvPrintFailed}})
	if err != nil {
		t.Fatal(err)
	}
	if v.Enabled || len(v.Events) != 1 || v.Config.SMTPHost != "smtp.example.org" || v.Config.SMTPPort != 587 || v.Config.SMTPUser != "robot" || v.Config.To[0] != "b@example.org" {
		t.Fatalf("patch : %+v", v)
	}
	if _, err := s.PatchChannel(ctx, ChannelPatch{Name: "inconnu"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("création par le modèle : %v", err)
	}
	if _, err := s.PatchChannel(ctx, ChannelPatch{Name: "mail", Events: []string{"nimporte"}}); err == nil {
		t.Fatal("évènement inconnu accepté")
	}
	// la structure de la requête du modèle ne porte aucun champ de destination
	for _, f := range []string{"SMTPHost", "SMTPPort", "SMTPUser", "To", "Config", "Kind"} {
		if _, ok := reflectField(ChannelPatch{}, f); ok {
			t.Errorf("ChannelPatch porte le champ %s", f)
		}
	}
}

// Le mot de passe SMTP est lié à (hôte, port, utilisateur) : tout changement l'invalide, y compris en base contournée.
func TestSMTPPasswordIsBoundToHostPortUser(t *testing.T) {
	for _, change := range []struct {
		name string
		in   ChannelInput
	}{
		{"hôte", emailInput("smtp.attaquant.example", 587, "robot")},
		{"port", emailInput("smtp.example.org", 465, "robot")},
		{"utilisateur", emailInput("smtp.example.org", 587, "autre")},
	} {
		s := newSvc(t, nil)
		ctx := context.Background()
		ch, err := s.UpsertChannel(ctx, emailInput("smtp.example.org", 587, "robot"))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetSecret(ctx, ch.ID, "password", "mdp-smtp"); err != nil {
			t.Fatal(err)
		}
		if v, _ := s.GetChannel(ctx, ch.ID); !v.Secrets[0].Set {
			t.Fatal("mot de passe non enregistré")
		}
		if _, err := s.UpsertChannel(ctx, change.in); err != nil {
			t.Fatal(err)
		}
		v, _ := s.GetChannel(ctx, ch.ID)
		if v.Secrets[0].Set {
			t.Errorf("changement de %s : le mot de passe a survécu", change.name)
		}
		if s.d.Vault.HasAccessCode(vaultKey(ch.ID, "password")) {
			t.Errorf("changement de %s : le mot de passe est resté dans le coffre", change.name)
		}
	}
	// contournement : la configuration est modifiée directement en base, la liaison doit quand même refuser
	s := newSvc(t, nil)
	ctx := context.Background()
	ch, _ := s.UpsertChannel(ctx, emailInput("smtp.example.org", 587, "robot"))
	_ = s.SetSecret(ctx, ch.ID, "password", "mdp-smtp")
	if _, err := s.d.Store.DB().ExecContext(ctx, `UPDATE notify_channels SET config=? WHERE id=?`, `{"smtp_host":"evil.example","smtp_port":587,"smtp_user":"robot","from":"a@example.org","to":["b@example.org"]}`, ch.ID); err != nil {
		t.Fatal(err)
	}
	v, _ := s.GetChannel(ctx, ch.ID)
	if v.Secrets[0].Set {
		t.Fatal("mot de passe servi à un hôte modifié hors de la page humaine")
	}
	// un nouveau mot de passe saisi pour la nouvelle destination fonctionne
	if err := s.SetSecret(ctx, ch.ID, "password", "autre"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.GetChannel(ctx, ch.ID); !v.Secrets[0].Set {
		t.Fatal("mot de passe resaisi non pris en compte")
	}
}

func TestPrivateDestinationsAreRefusedByDefault(t *testing.T) {
	s := strict(t)
	ctx := context.Background()
	for _, host := range []string{"127.0.0.1", "localhost", "10.0.0.5", "192.168.1.9", "169.254.169.254", "[::1]", "::1", "srv.internal", "intranet", "100.64.0.1"} {
		if _, err := s.UpsertChannel(ctx, emailInput(host, 587, "")); err == nil {
			t.Errorf("hôte SMTP %q accepté", host)
		}
	}
	for _, port := range []int{22, 80, 3306, 6379, 8080} {
		if _, err := s.UpsertChannel(ctx, emailInput("smtp.example.org", port, "")); err == nil {
			t.Errorf("port SMTP %d accepté", port)
		}
	}
	for _, port := range []int{25, 465, 587, 2525} {
		in := emailInput("smtp.example.org", port, "")
		in.Config.SMTPTLS = "starttls"
		if _, err := s.UpsertChannel(ctx, in); err != nil {
			t.Errorf("port SMTP %d refusé : %v", port, err)
		}
	}
	ch, _ := s.UpsertChannel(ctx, ChannelInput{Name: "n", Kind: KindNtfy})
	for _, u := range []string{"http://127.0.0.1:8080/t", "http://localhost/t", "https://192.168.0.2/t", "http://169.254.169.254/latest", "http://[::1]/t", "http://10.1.2.3/t", "http://nas.local/t"} {
		if err := s.SetSecret(ctx, ch.ID, "url", u); err == nil {
			t.Errorf("URL %q acceptée", u)
		}
	}
	if err := s.SetSecret(ctx, ch.ID, "url", "https://ntfy.example.org/mon-sujet"); err != nil {
		t.Errorf("URL publique refusée : %v", err)
	}
	// option explicite : ntfy et SMTP locaux permis
	t.Setenv("BAMBU_NOTIFY_ALLOW_PRIVATE", "1")
	s2 := newSvc(t, func(d *Deps) { d.AllowPrivate = false })
	if _, err := s2.UpsertChannel(ctx, emailInput("127.0.0.1", 1025, "")); err != nil {
		t.Errorf("BAMBU_NOTIFY_ALLOW_PRIVATE=1 sans effet : %v", err)
	}
}

// La connexion elle-même est gardée : un nom public qui se résout vers une adresse privée est refusé au dial.
func TestDialRefusesNonPublicResolvedAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	s := strict(t)
	_, err := s.dialer(0).DialContext(context.Background(), "tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err == nil || !errors.Is(err, errPrivateDest) {
		t.Fatalf("connexion à la boucle locale : %v", err)
	}
	// et le client HTTP par défaut du service passe par ce dialer
	resp, err := s.d.HTTP.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("le client HTTP du service a joint la boucle locale")
	}
}

// Aucune erreur renvoyée au modèle ne contient adresse, port ni nom d'hôte.
func TestNetworkErrorsNeverRevealAddressOrPort(t *testing.T) {
	s := newSvc(t, nil)
	ctx := context.Background()
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	_, port, _ := net.SplitHostPort(addr)
	l.Close() // plus personne n'écoute
	pn, _ := strconv.Atoi(port)
	ch, _ := s.UpsertChannel(ctx, emailInput("127.0.0.1", pn, "u"))
	res := s.Emit(ctx, Event{Kind: EvTest, Title: "t", Message: "m"})
	if len(res) != 1 || res[0].OK {
		t.Fatalf("%+v", res)
	}
	nt, _ := s.UpsertChannel(ctx, ChannelInput{Name: "n", Kind: KindNtfy})
	_ = s.SetSecret(ctx, nt.ID, "url", "http://"+addr+"/sujet")
	res = append(res, s.Emit(ctx, Event{Kind: EvTest, Title: "t", Message: "m"})...)
	lg, _ := s.Log(ctx, 20)
	var all []string
	for _, r := range res {
		all = append(all, r.Error)
	}
	for _, e := range lg {
		all = append(all, e.Error)
	}
	_ = ch
	for _, e := range all {
		if strings.Contains(e, "127.0.0.1") || strings.Contains(e, port) || strings.Contains(e, "sujet") || strings.Contains(e, "dial") {
			t.Errorf("erreur trop bavarde : %q", e)
		}
	}
	if len(all) < 3 {
		t.Fatalf("erreurs attendues : %v", all)
	}
	// la phrase générique d'un rejet de destination ne donne pas l'adresse non plus
	if m := scrubErr(&net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.ParseIP("10.1.2.3"), Port: 6379}, Err: errors.New("connect: connection refused")}); strings.Contains(m, "10.1.2.3") || strings.Contains(m, "6379") {
		t.Errorf("scrubErr : %q", m)
	}
	if m := scrubErr(errors.New("lookup 10.9.8.7:2525 failed at [fe80::1]:25")); strings.Contains(m, "10.9.8.7") || strings.Contains(m, "fe80") {
		t.Errorf("scrubErr (adresse en texte) : %q", m)
	}
}
