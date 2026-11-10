package httpapi

import "testing"

// Le garde (hôte, jeton, session) couvre aussi les routes des fiches pièce et impression.
func TestGuardCoversSheetRoutes(t *testing.T) {
	const tok = "0123456789abcdef-secret"
	h := NewHandlerWithOptions(newCore(t), Options{APIToken: tok})
	for _, p := range []string{"/api/models/x/sheet", "/api/prints/x/sheet"} {
		if got := do(h, "GET", p, "evil.example.com", map[string]string{"Authorization": "Bearer " + tok}); got != 403 {
			t.Errorf("GET %s mauvais Host : %d", p, got)
		}
		if got := do(h, "GET", p, "127.0.0.1:7878", nil); got != 401 {
			t.Errorf("GET %s sans jeton : %d", p, got)
		}
	}
}
