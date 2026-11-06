package catalog

import (
	"strings"
	"testing"
)

func TestSafeNameNeverEscapesTheArchive(t *testing.T) {
	cases := map[string]string{
		"../../etc/passwd":      "_.._etc_passwd",
		`..\..\win.ini`:         "_.._win.ini",
		"/abs/olu":              "_abs_olu",
		"   ":                   "x",
		"":                      "x",
		"pièce 1 (v2).stl":      "pièce 1 (v2).stl",
		"a\x00b\nc":             "a_b_c",
		"...cachee":             "cachee",
		"nom:avec*caractères?!": "nom_avec_caractères_!",
	}
	for in, want := range cases {
		got := safeName(in, "x")
		if got != want {
			t.Errorf("safeName(%q) = %q, attendu %q", in, got, want)
		}
		if strings.ContainsAny(got, `/\`) || strings.HasPrefix(got, "..") {
			t.Errorf("safeName(%q) = %q s'échappe de l'archive", in, got)
		}
	}
	if got := safeName(strings.Repeat("é", 300), "x"); len([]rune(got)) != 120 {
		t.Errorf("longueur : %d", len([]rune(got)))
	}
}
