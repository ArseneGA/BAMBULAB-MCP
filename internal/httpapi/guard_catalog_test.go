package httpapi

import "testing"

// Le garde couvre aussi les routes du catalogue (/api/models, /api/assets, /api/import...).
func TestGuardCoversCatalogRoutes(t *testing.T) {
	const tok = "0123456789abcdef-secret"
	h := NewHandlerWithOptions(newCore(t), Options{APIToken: tok})
	routes := [][2]string{
		{"GET", "/api/models"}, {"GET", "/api/collections"}, {"GET", "/api/assets/" + "a.glb"},
		{"POST", "/api/import"}, {"POST", "/api/scan"}, {"POST", "/api/operations/apply"}, {"GET", "/api/trash"},
		{"GET", "/api/materials"}, {"GET", "/api/materials/PLA"}, {"POST", "/api/models/x/analyze"}, {"GET", "/api/models/x/parts"},
		{"GET", "/api/files/x/download"}, {"POST", "/api/export"}, {"GET", "/api/experiments"}, {"GET", "/api/print-memory"}, {"GET", "/api/profiles/filaments"},
		{"GET", "/api/issues"}, {"POST", "/api/issues"}, {"GET", "/api/issues/export"},
	}
	for _, r := range routes {
		if got := do(h, r[0], r[1], "evil.example.com", map[string]string{"Authorization": "Bearer " + tok}); got != 403 {
			t.Errorf("%s %s mauvais Host : %d", r[0], r[1], got)
		}
		if got := do(h, r[0], r[1], "127.0.0.1:7878", nil); got != 401 {
			t.Errorf("%s %s sans jeton : %d", r[0], r[1], got)
		}
		if got := do(h, r[0], r[1], "127.0.0.1:7878", map[string]string{"Sec-Fetch-Site": "cross-site", "Authorization": "Bearer " + tok}); r[0] == "POST" && got != 403 {
			t.Errorf("%s %s cross-site : %d", r[0], r[1], got)
		}
	}
}
