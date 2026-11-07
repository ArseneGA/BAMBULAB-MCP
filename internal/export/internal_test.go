package export

import (
	"bytes"
	"strings"
	"testing"
)

func TestStripJPEGMetadata(t *testing.T) {
	exif := append([]byte("Exif\x00\x00"), []byte("GPS48.85N")...)
	in := []byte{0xFF, 0xD8, 0xFF, 0xE1, byte((len(exif) + 2) >> 8), byte(len(exif) + 2)}
	in = append(in, exif...)
	in = append(in, 0xFF, 0xFE, 0x00, 0x05, 'a', 'b', 'c')                // commentaire
	in = append(in, 0xFF, 0xE0, 0x00, 0x04, 'J', 'F')                     // JFIF conservé
	in = append(in, 0xFF, 0xDA, 0x00, 0x04, 0, 0, 0xAA, 0xBB, 0xFF, 0xD9) // image
	out := stripJPEGMetadata(in)
	if bytes.Contains(out, []byte("GPS")) || bytes.Contains(out, []byte("abc")) {
		t.Fatalf("métadonnées conservées : %x", out)
	}
	if !bytes.Contains(out, []byte{0xFF, 0xE0}) || !bytes.HasSuffix(out, []byte{0xAA, 0xBB, 0xFF, 0xD9}) || !bytes.HasPrefix(out, []byte{0xFF, 0xD8}) {
		t.Fatalf("structure de l'image abîmée : %x", out)
	}
	if bad := []byte("pas un jpeg"); !bytes.Equal(stripJPEGMetadata(bad), bad) {
		t.Fatal("un fichier non JPEG doit être rendu tel quel")
	}
	trunc := in[:len(in)-12]
	_ = stripJPEGMetadata(trunc) // ne doit pas paniquer
}

func TestScrubberText(t *testing.T) {
	s := &scrubber{labels: map[string]string{}}
	for _, v := range []string{`C:\Données\bambu`, "192.168.1.50", "01P00A123456789"} {
		for _, x := range variants(v) {
			s.lits = append(s.lits, x)
			s.labels[x] = "x"
		}
	}
	cases := map[string]string{
		`dossier C:\Données\bambu\blobs`:    "dossier " + redactedData + `\blobs`,
		"imprimante 192.168.1.50 ok":        "imprimante " + redactedData + " ok",
		"série 01P00A123456789":             "série " + redactedData,
		`fichier D:\perso\x.txt`:            "fichier " + redactedPath,
		"fichier /home/bob/x.txt ici":       "fichier " + redactedPath + " ici",
		"voir https://makerworld.com/fr/m":  "voir https://makerworld.com/fr/m",
		"réseau 10.0.0.7 et 172.20.3.4":     "réseau " + redactedIP + " et " + redactedIP,
		"version 1.2.3.4 publique 8.8.8.8":  "version 1.2.3.4 publique 8.8.8.8",
		"ratio 1/2 et A:1 et http://x/home": "ratio 1/2 et A:1 et http://x/home",
	}
	for in, want := range cases {
		if got := s.Text(in); got != want {
			t.Errorf("Text(%q) = %q, attendu %q", in, got, want)
		}
	}
	// JSON : le littéral échappé est aussi couvert
	if got := string(s.Bytes([]byte(`{"p":"C:\\Données\\bambu\\x"}`))); strings.Contains(got, "Données") {
		t.Errorf("chemin JSON échappé conservé : %s", got)
	}
}

func TestSafeSegmentAndEntryNames(t *testing.T) {
	cases := map[string]string{
		"../../etc/passwd": "_.._etc_passwd",
		`a\b:c*d?"e<f>g|h`: "a_b_c_d__e_f_g_h",
		"CON":              "_CON",
		"nul.txt":          "_nul.txt",
		"fin.  ":           "fin",
		"":                 "x",
		"...":              "_",
		"normal-éè 12.stl": "normal-éè 12.stl",
	}
	for in, want := range cases {
		got := safeSegment(in, "x")
		if in == "../../etc/passwd" {
			if strings.Contains(got, "/") || strings.Contains(got, "..") {
				t.Errorf("safeSegment(%q) = %q", in, got)
			}
			continue
		}
		if got != want {
			t.Errorf("safeSegment(%q) = %q, attendu %q", in, got, want)
		}
	}
	for _, bad := range []string{"", "/abs", "../x", "a/../b", `a\b`, "a//b", "c:/x", "a/./b", "x/"} {
		if validEntryName(bad) {
			t.Errorf("validEntryName(%q) accepté", bad)
		}
	}
	for _, ok := range []string{"README.md", "files/Pièce/a.stl", "images/p/photo-01.jpg"} {
		if !validEntryName(ok) {
			t.Errorf("validEntryName(%q) refusé", ok)
		}
	}
	used := map[string]int{}
	if a, b := uniqueName(used, "x.gcode.3mf"), uniqueName(used, "X.gcode.3mf"); a != "x.gcode.3mf" || b != "X (2).gcode.3mf" {
		t.Errorf("uniqueName : %q %q", a, b)
	}
}

func TestCleanOverridesDropsGcodeKeys(t *testing.T) {
	out, dropped := cleanOverrides(nil)
	if len(out) != 0 || dropped != 0 || out == nil {
		t.Fatal("liste vide attendue non nulle")
	}
}

func TestNormalize(t *testing.T) {
	if _, err := normalize(Input{}); err == nil {
		t.Error("entrée vide acceptée")
	}
	in, err := normalize(Input{ModelIDs: []string{"a"}})
	if err != nil || in.Options.License != DefaultLicense || in.Options.MaxBytes != DefaultMaxBytes {
		t.Fatalf("valeurs par défaut : %+v %v", in.Options, err)
	}
	if _, err := normalize(Input{ModelIDs: []string{"a"}, Options: Options{AssemblyNotes: strings.Repeat("é", 20001)}}); err == nil {
		t.Error("notice trop longue acceptée")
	}
}
