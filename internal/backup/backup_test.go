package backup_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/backup"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func shaOf(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// putPreview dépose un blob et le référence (print_previews) comme le ferait capture_preview.
func putPreview(t *testing.T, c *core.Core, content string) string {
	t.Helper()
	sha, _, err := c.Blobs.Put(strings.NewReader(content), "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Store.DB().Exec(`INSERT OR IGNORE INTO print_previews(sha256, created_at) VALUES(?,?)`, sha, store.FormatTime(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	return sha
}

func blobPath(dir, sha string) string { return filepath.Join(dir, "blobs", sha[0:2], sha[2:4], sha) }

// TestBackupWhileJobRunningThenRestoreInFreshDir : sauvegarde d'un service VIVANT (un travail tourne, des blobs
// arrivent pendant la copie), restauration dans un dossier neuf, puis le service y redémarre avec les mêmes
// données, le même travail connu et le même coffre de codes d'accès.
func TestBackupWhileJobRunningThenRestoreInFreshDir(t *testing.T) {
	ctx := context.Background()
	src := t.TempDir()
	c, err := core.Open(ctx, core.Config{DataDir: src, Logger: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	var shas []string
	for i := 0; i < 40; i++ {
		shas = append(shas, putPreview(t, c, fmt.Sprintf("contenu du blob numéro %d %s", i, strings.Repeat("x", i*100))))
	}
	if err := c.Printing.Secrets().SetAccessCode(ctx, "p1s-test", "12345678"); err != nil {
		t.Fatal(err)
	}
	// Un travail long tourne pendant la sauvegarde.
	wctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _ = c.Run(wctx) }()
	job, _, err := c.Queue.Enqueue(ctx, jobs.EnqueueParams{Kind: "system.selftest", Params: json.RawMessage(`{"steps":50,"step_ms":100}`)})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		j, _ := c.Queue.Get(ctx, job.ID)
		if j.State == contracts.JobRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("le travail n'a pas démarré : %s", j.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Des écritures continuent pendant la copie.
	stopWriters := make(chan struct{})
	var ww sync.WaitGroup
	ww.Add(1)
	go func() {
		defer ww.Done()
		for i := 0; ; i++ {
			select {
			case <-stopWriters:
				return
			default:
			}
			sha, _, err := c.Blobs.Put(strings.NewReader(fmt.Sprintf("blob concurrent %d", i)), "")
			if err == nil {
				_, _ = c.Store.DB().Exec(`INSERT OR IGNORE INTO print_previews(sha256, created_at) VALUES(?,?)`, sha, store.FormatTime(time.Now()))
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	out := filepath.Join(t.TempDir(), "atelier.tar.gz")
	m, err := backup.Create(ctx, src, out, backup.CreateOptions{Version: "test"})
	if j, _ := c.Queue.Get(ctx, job.ID); j.State != contracts.JobRunning {
		t.Logf("remarque : le travail était %s à la fin de la sauvegarde", j.State)
	}
	close(stopWriters)
	ww.Wait()
	if err != nil {
		t.Fatalf("sauvegarde pendant un travail : %v", err)
	}
	if m.BlobCount < 40 || !m.IncludesSecrets || m.SchemaVersion < 1 {
		t.Fatalf("manifeste : %+v", m)
	}
	if _, err := os.Stat(out + ".sha256"); err != nil {
		t.Fatal("fichier .sha256 absent")
	}
	if checked, err := backup.VerifyFileSHA(out); !checked || err != nil {
		t.Fatalf("empreinte globale : %v %v", checked, err)
	}
	rep, _, err := backup.VerifyArchive(ctx, out)
	if err != nil || !rep.OK() {
		t.Fatalf("archive : %v %+v", err, rep)
	}
	cancel()
	wg.Wait()
	c.Close()

	// Restauration dans un dossier neuf.
	dst := filepath.Join(t.TempDir(), "neuf")
	if _, err := backup.Restore(ctx, out, dst, backup.RestoreOptions{MaxSchema: store.LatestSchemaVersion()}); err != nil {
		t.Fatalf("restauration : %v", err)
	}
	for _, sha := range shas {
		if err := hashEq(blobPath(dst, sha), sha); err != nil {
			t.Fatalf("blob %s restauré : %v", sha, err)
		}
	}
	vr, err := backup.VerifyDataDir(ctx, dst, false)
	if err != nil || !vr.OK() {
		t.Fatalf("verify après restauration : %v %+v", err, vr)
	}
	c2, err := core.Open(ctx, core.Config{DataDir: dst, Logger: quiet()})
	if err != nil {
		t.Fatalf("réouverture du service sur les données restaurées : %v", err)
	}
	defer c2.Close()
	if _, err := c2.Queue.Get(ctx, job.ID); err != nil {
		t.Fatalf("le travail est inconnu après restauration : %v", err)
	}
	code, err := c2.Printing.Secrets().AccessCode(ctx, "p1s-test")
	if err != nil || code != "12345678" {
		t.Fatalf("coffre restauré illisible : %q %v", code, err)
	}
}

func hashEq(p, want string) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	if shaOf(b) != want {
		return fmt.Errorf("contenu différent")
	}
	return nil
}

// seeded crée un dossier de données avec quelques blobs référencés, base fermée (pas de service).
func seeded(t *testing.T) (dir string, shas []string) {
	t.Helper()
	dir = t.TempDir()
	c, err := core.Open(context.Background(), core.Config{DataDir: dir, Logger: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		shas = append(shas, putPreview(t, c, fmt.Sprintf("blob %d", i)))
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	return dir, shas
}

func TestVerifyDataDirDetectsCorruptAndMissingBlobs(t *testing.T) {
	ctx := context.Background()
	dir, shas := seeded(t)
	rep, err := backup.VerifyDataDir(ctx, dir, false)
	if err != nil || !rep.OK() || rep.Blobs != 5 || rep.References != 5 {
		t.Fatalf("état sain : %v %+v", err, rep)
	}
	// Corruption d'un blob (même taille) et disparition d'un autre.
	p := blobPath(dir, shas[0])
	_ = os.Chmod(p, 0o644)
	if err := os.WriteFile(p, []byte("blob X"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(blobPath(dir, shas[1]), 0o644)
	if err := os.Remove(blobPath(dir, shas[1])); err != nil {
		t.Fatal(err)
	}
	rep, err = backup.VerifyDataDir(ctx, dir, false)
	if err != nil || rep.OK() {
		t.Fatalf("anomalies non détectées : %v %+v", err, rep)
	}
	joined := strings.Join(rep.Problems, "\n")
	if !strings.Contains(joined, shas[0]) || !strings.Contains(joined, shas[1]) {
		t.Fatalf("problèmes : %s", joined)
	}
	// Le mode rapide ne relit pas le contenu : la corruption de même taille passe, la disparition non.
	q, _ := backup.VerifyDataDir(ctx, dir, true)
	if strings.Contains(strings.Join(q.Problems, "\n"), shas[0]) || !strings.Contains(strings.Join(q.Problems, "\n"), shas[1]) {
		t.Fatalf("mode rapide : %v", q.Problems)
	}
	// La sauvegarde d'un blob corrompu échoue plutôt que d'archiver un contenu faux.
	if _, err := backup.Create(ctx, dir, filepath.Join(t.TempDir(), "x.tar.gz"), backup.CreateOptions{}); err == nil || !strings.Contains(err.Error(), "corrompu") {
		t.Fatalf("sauvegarde d'un blob corrompu : %v", err)
	}
}

func TestManifestHashesAndTamperedTarDetected(t *testing.T) {
	ctx := context.Background()
	dir, _ := seeded(t)
	out := filepath.Join(t.TempDir(), "a.tar") // .tar non compressé : on peut altérer un octet sans casser gzip
	m, err := backup.Create(ctx, dir, out, backup.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range m.Files {
		if len(f.SHA256) != 64 || f.Size < 0 {
			t.Fatalf("entrée de manifeste : %+v", f)
		}
	}
	// Réécriture de l'archive avec un blob modifié mais le manifeste d'origine (et sans .sha256).
	altered := filepath.Join(t.TempDir(), "b.tar")
	rewriteTar(t, out, altered, func(h *tar.Header, b []byte) []byte {
		if strings.HasPrefix(h.Name, "blobs/") {
			return append([]byte(nil), []byte("altéré")...)
		}
		return b
	}, true)
	if _, _, err := backup.VerifyArchive(ctx, altered); err == nil || !strings.Contains(err.Error(), "manifeste") && !strings.Contains(err.Error(), "empreinte") {
		t.Fatalf("altération non détectée : %v", err)
	}
	// Restauration refusée, et rien n'est laissé dans le dossier cible.
	dst := t.TempDir()
	if _, err := backup.Restore(ctx, altered, dst, backup.RestoreOptions{}); err == nil {
		t.Fatal("restauration d'une archive altérée acceptée")
	}
	if ents, _ := os.ReadDir(dst); len(ents) != 0 {
		t.Fatalf("dossier cible pollué : %v", ents)
	}
}

// rewriteTar recopie src vers dst en appliquant f aux contenus ; en-tête.Size est recalculée.
func rewriteTar(t *testing.T, src, dst string, f func(*tar.Header, []byte) []byte, keepManifest bool) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	o, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	tr, tw := tar.NewReader(in), tar.NewWriter(o)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		if h.Name != "manifest.json" || !keepManifest {
			b = f(h, b)
		}
		h.Size = int64(len(b))
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		tw.Write(b)
	}
	tw.Close()
}

func TestCorruptedAndTruncatedArchives(t *testing.T) {
	ctx := context.Background()
	dir, _ := seeded(t)
	out := filepath.Join(t.TempDir(), "a.tar.gz")
	if _, err := backup.Create(ctx, dir, out, backup.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(out)

	// 1. Octet altéré au milieu : l'empreinte .sha256 le détecte avant toute lecture.
	bad := filepath.Join(t.TempDir(), "bad.tar.gz")
	cp := append([]byte(nil), raw...)
	cp[len(cp)/2] ^= 0xFF
	os.WriteFile(bad, cp, 0o600)
	os.WriteFile(bad+".sha256", mustRead(t, out+".sha256"), 0o600) // sidecar d'origine
	if _, _, err := backup.VerifyArchive(ctx, bad); err == nil {
		t.Fatal("archive altérée acceptée (avec .sha256)")
	}
	// 2. Même altération sans .sha256 : gzip / tar / manifeste doivent la refuser.
	os.Remove(bad + ".sha256")
	if _, _, err := backup.VerifyArchive(ctx, bad); err == nil {
		t.Fatal("archive altérée acceptée (sans .sha256)")
	}
	// 3. Archive tronquée (sauvegarde interrompue) : pas de manifeste.
	trunc := filepath.Join(t.TempDir(), "trunc.tar.gz")
	os.WriteFile(trunc, raw[:len(raw)*2/3], 0o600)
	if _, _, err := backup.VerifyArchive(ctx, trunc); err == nil {
		t.Fatal("archive tronquée acceptée")
	}
	// 4. Restauration d'une archive corrompue : refusée, dossier cible intact.
	dst := t.TempDir()
	if _, err := backup.Restore(ctx, bad, dst, backup.RestoreOptions{}); err == nil {
		t.Fatal("restauration d'une archive corrompue acceptée")
	}
	if ents, _ := os.ReadDir(dst); len(ents) != 0 {
		t.Fatalf("dossier cible pollué : %v", ents)
	}
	// 5. Ni zstd ni extension inconnue.
	if _, err := backup.Create(ctx, dir, filepath.Join(t.TempDir(), "a.tar.zst"), backup.CreateOptions{}); err == nil || !strings.Contains(err.Error(), "zstd") {
		t.Fatalf("zstd : %v", err)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHostileArchiveEntriesRefused(t *testing.T) {
	ctx := context.Background()
	for name, hdr := range map[string]tar.Header{
		"traversée": {Name: "../evil", Typeflag: tar.TypeReg, Mode: 0o644},
		"absolu":    {Name: "/etc/passwd", Typeflag: tar.TypeReg, Mode: 0o644},
		"lien":      {Name: "blobs/ab/cd/x", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		"inconnu":   {Name: "work/x", Typeflag: tar.TypeReg, Mode: 0o644},
	} {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gw)
		hdr.Size = 1
		if hdr.Typeflag == tar.TypeSymlink {
			hdr.Size = 0
		}
		tw.WriteHeader(&hdr)
		if hdr.Size > 0 {
			tw.Write([]byte("x"))
		}
		tw.Close()
		gw.Close()
		p := filepath.Join(t.TempDir(), "h.tar.gz")
		os.WriteFile(p, buf.Bytes(), 0o600)
		dst := t.TempDir()
		if _, err := backup.Restore(ctx, p, dst, backup.RestoreOptions{}); err == nil {
			t.Fatalf("%s : élément hostile accepté", name)
		}
		if ents, _ := os.ReadDir(dst); len(ents) != 0 {
			t.Fatalf("%s : dossier pollué %v", name, ents)
		}
	}
}

func TestRestoreRefusesNonEmptyDirUnlessForce(t *testing.T) {
	ctx := context.Background()
	dir, shas := seeded(t)
	out := filepath.Join(t.TempDir(), "a.tar.gz")
	if _, err := backup.Create(ctx, dir, out, backup.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	// Le dossier source (non vide) est refusé sans --force.
	if _, err := backup.Restore(ctx, out, dir, backup.RestoreOptions{}); err == nil || !strings.Contains(err.Error(), "pas vide") {
		t.Fatalf("dossier non vide : %v", err)
	}
	if err := hashEq(blobPath(dir, shas[0]), shas[0]); err != nil {
		t.Fatalf("le refus a touché aux données : %v", err)
	}
	// Avec --force, l'existant est conservé à l'écart (et non supprimé).
	marker := filepath.Join(dir, "blobs", "marqueur.txt")
	os.WriteFile(marker, []byte("m"), 0o644)
	if _, err := backup.Restore(ctx, out, dir, backup.RestoreOptions{Force: true, Now: func() time.Time { return time.Unix(1700000000, 0) }}); err != nil {
		t.Fatalf("restauration forcée : %v", err)
	}
	aside, _ := filepath.Glob(filepath.Join(dir, ".pre-restore-*", "blobs", "marqueur.txt"))
	if len(aside) != 1 {
		t.Fatalf("ancien contenu non conservé : %v", aside)
	}
	if rep, err := backup.VerifyDataDir(ctx, dir, false); err != nil || !rep.OK() {
		t.Fatalf("après --force : %v %+v", err, rep)
	}
}

func TestExcludeSecrets(t *testing.T) {
	ctx := context.Background()
	dir, _ := seeded(t)
	out := filepath.Join(t.TempDir(), "s.tar.gz")
	m, err := backup.Create(ctx, dir, out, backup.CreateOptions{ExcludeSecrets: true})
	if err != nil || m.IncludesSecrets {
		t.Fatalf("%v %+v", err, m)
	}
	for _, f := range m.Files {
		if strings.HasPrefix(f.Path, "secrets/") || f.Path == "assets.key" {
			t.Fatalf("secret inclus malgré --exclude-secrets : %s", f.Path)
		}
	}
	// L'archive ne doit pas être écrite dans le dossier de données.
	if _, err := backup.Create(ctx, dir, filepath.Join(dir, "x.tar.gz"), backup.CreateOptions{}); err == nil {
		t.Fatal("archive dans le dossier de données acceptée")
	}
}
