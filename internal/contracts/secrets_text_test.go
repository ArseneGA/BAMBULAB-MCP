package contracts

import (
	"strings"
	"testing"
)

func TestRedactText(t *testing.T) {
	tok := "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcde" // 43 car. comme un jeton d'envoi
	cases := []struct{ in, gone string }{
		{"PUT http://192.168.1.2:7878/upload/" + tok + " a échoué", tok},
		{"GET /files?token=abc123def&x=1", "abc123def"},
		{"url signée ?sig=deadBEEF99&exp=3", "deadBEEF99"},
		{`{"access_code":"87654321","ok":true}`, "87654321"},
		{"access_code: 87654321 refusé", "87654321"},
		{"Authorization: Bearer eyJhbGciOi.abc.def", "eyJhbGciOi"},
		{"mqtt://bblp:s3cr3tpw@192.168.1.5/", "s3cr3tpw"},
		{"mot de passe : hunter22", "hunter22"},
		{"password=hunter22", "hunter22"},
		{"le code de lancement 123456 a été refusé", "123456"},
		{"launch code is 4321", "4321"},
		{"code: 998877", "998877"},
		{"clé sk-abcdefghijklmnopqrstu1234", "sk-abcdef"},
		{"jeton brut " + tok, tok},
		{"secret_token=zzz999", "zzz999"},
	}
	for _, c := range cases {
		got := RedactText(c.in)
		if strings.Contains(got, c.gone) || !strings.Contains(got, Masked) {
			t.Errorf("%q -> %q : %q subsiste ou rien de masqué", c.in, got, c.gone)
		}
	}
	keep := []string{
		"", "get_print_status renvoie erreur 404 sur pj_3f9a1c2b4d5e6f708192a3b4",
		"sha256 " + strings.Repeat("ab12", 16), "fichier support_etagere_guitare_murale_version_finale.3mf",
		"progression 42 % couche 12/80, buse 220 °C", "code HTTP: 502", "le jeton est expiré", "température 0,2 mm",
	}
	for _, k := range keep {
		if got := RedactText(k); got != k {
			t.Errorf("texte propre modifié : %q -> %q", k, got)
		}
	}
}
