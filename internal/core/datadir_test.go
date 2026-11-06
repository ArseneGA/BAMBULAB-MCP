package core

import (
	"path/filepath"
	"testing"
)

func TestDefaultDataDir(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	abs := func(p string) string { a, _ := filepath.Abs(p); return a }
	home := abs("/home/u")
	cases := []struct {
		goos string
		env  map[string]string
		want string
	}{
		{"windows", map[string]string{"LOCALAPPDATA": abs("/local")}, filepath.Join(abs("/local"), "BambuAtelier")},
		{"windows", nil, filepath.Join(home, "AppData", "Local", "BambuAtelier")},
		{"linux", map[string]string{"XDG_DATA_HOME": abs("/xdg")}, filepath.Join(abs("/xdg"), "bambu-atelier")},
		{"linux", nil, filepath.Join(home, ".local", "share", "bambu-atelier")},
		{"linux", map[string]string{"XDG_DATA_HOME": "relatif/refuse"}, filepath.Join(home, ".local", "share", "bambu-atelier")},
	}
	for _, c := range cases {
		got := defaultDataDir(c.goos, env(c.env), home)
		if got != c.want || !filepath.IsAbs(got) {
			t.Errorf("%s %v : %q, attendu %q", c.goos, c.env, got, c.want)
		}
	}
	if d := DefaultDataDir(); !filepath.IsAbs(d) {
		t.Errorf("DefaultDataDir non absolu : %q", d)
	}
}
