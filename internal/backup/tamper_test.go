package backup_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/backup"
)

// regz réécrit une archive .tar.gz en appliquant mod sur la liste des entrées (ordre conservé).
func regz(t *testing.T, raw []byte, mod func(names []string, data map[string][]byte) ([]string, map[string][]byte)) []byte {
	t.Helper()
	gr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gr)
	var names []string
	data := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		names = append(names, h.Name)
		data[h.Name] = b
	}
	names, data = mod(names, data)
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, n := range names {
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: 0o600, Size: int64(len(data[n])), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write(data[n])
	}
	tw.Close()
	gw.Close()
	return buf.Bytes()
}

func makeArchive(t *testing.T) []byte {
	t.Helper()
	dir, _ := seeded(t)
	out := filepath.Join(t.TempDir(), "a.tar.gz")
	if _, err := backup.Create(context.Background(), dir, out, backup.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	return mustRead(t, out)
}

// TestAlteredArchiveRefusedWithoutSidecar : toute altération, sans fichier .sha256, est refusée par la
// vérification ET par la restauration (qui ne doit rien appliquer).
func TestAlteredArchiveRefusedWithoutSidecar(t *testing.T) {
	ctx := context.Background()
	raw := makeArchive(t)
	good := filepath.Join(t.TempDir(), "good.tar.gz")
	os.WriteFile(good, raw, 0o600)
	if rep, _, err := backup.VerifyArchive(ctx, good); err != nil || !rep.OK() {
		t.Fatalf("archive saine refusée : %v %+v", err, rep)
	}

	flip := func(i int) []byte { c := append([]byte(nil), raw...); c[i] ^= 0xFF; return c }
	cases := map[string][]byte{
		"en-tête gzip (magique)": flip(0),
		"en-tête gzip (MTIME)":   flip(5),
		"début":                  flip(len(raw) / 10),
		"quart":                  flip(len(raw) / 4),
		"milieu":                 flip(len(raw) / 2),
		"trois quarts":           flip(len(raw) * 3 / 4),
		"avant la fin":           flip(len(raw) - 20),
		"CRC final":              flip(len(raw) - 8),
		"taille finale (ISIZE)":  flip(len(raw) - 2),
		"octets ajoutés":         append(append([]byte(nil), raw...), 'x', 'y', 'z'),
	}
	cases["fichier ajouté"] = regz(t, raw, func(n []string, d map[string][]byte) ([]string, map[string][]byte) {
		extra := "blobs/00/00/" + strings.Repeat("0", 64)
		d[extra] = []byte("intrus")
		return append(append([]string(nil), n[:len(n)-1]...), extra, n[len(n)-1]), d
	})
	cases["fichier manquant"] = regz(t, raw, func(n []string, d map[string][]byte) ([]string, map[string][]byte) {
		for i, name := range n {
			if name != "db/bambu.db" && name != "manifest.json" {
				return append(append([]string(nil), n[:i]...), n[i+1:]...), d
			}
		}
		t.Fatal("aucun fichier à retirer")
		return n, d
	})
	cases["contenu modifié, taille égale"] = regz(t, raw, func(n []string, d map[string][]byte) ([]string, map[string][]byte) {
		b := append([]byte(nil), d["db/bambu.db"]...)
		b[len(b)/2] ^= 0xFF
		d["db/bambu.db"] = b
		return n, d
	})

	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if sameDecoded(raw, content) {
				t.Skip("octet sans effet sur le contenu décompressé (métadonnée gzip ou en-tête de bloc deflate) : pas une altération")
			}
			p := filepath.Join(t.TempDir(), "bad.tar.gz")
			os.WriteFile(p, content, 0o600)
			if rep, _, err := backup.VerifyArchive(ctx, p); err == nil && (rep == nil || rep.OK()) {
				t.Fatal("archive altérée acceptée par VerifyArchive")
			}
			dst := t.TempDir()
			if _, err := backup.Restore(ctx, p, dst, backup.RestoreOptions{}); err == nil {
				t.Fatal("archive altérée restaurée")
			}
			if ents, _ := os.ReadDir(dst); len(ents) != 0 {
				t.Fatalf("dossier cible pollué : %v", ents)
			}
		})
	}
}

// TestFlipAcrossArchiveNeverAccepted balaie des positions réparties sur toute l'archive.
func TestFlipAcrossArchiveNeverAccepted(t *testing.T) {
	ctx := context.Background()
	raw := makeArchive(t)
	p := filepath.Join(t.TempDir(), "bad.tar.gz")
	step := len(raw)/64 + 1
	for i := 0; i < len(raw); i += step {
		c := append([]byte(nil), raw...)
		c[i] ^= 0xFF
		if sameDecoded(raw, c) {
			continue // contenu décompressé identique : pas une altération
		}
		os.WriteFile(p, c, 0o600)
		if rep, _, err := backup.VerifyArchive(ctx, p); err == nil && (rep == nil || rep.OK()) {
			t.Fatalf("octet %d/%d inversé : archive acceptée", i, len(raw))
		}
	}
}

// sameDecoded indique si deux archives .tar.gz se décompressent en octets identiques (CRC compris). Inverser
// certains octets du flux deflate (en-têtes de blocs) ne change pas le contenu : ce n'est pas une altération
// détectable ni dangereuse, puisque tout le contenu est ensuite vérifié contre le manifeste.
func sameDecoded(a, b []byte) bool {
	dec := func(x []byte) ([]byte, error) {
		gr, err := gzip.NewReader(bytes.NewReader(x))
		if err != nil {
			return nil, err
		}
		return io.ReadAll(gr)
	}
	da, ea := dec(a)
	db, eb := dec(b)
	return ea == nil && eb == nil && bytes.Equal(da, db)
}
