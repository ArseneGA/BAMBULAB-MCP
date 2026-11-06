package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

func sumHex(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func TestImportRealFormats(t *testing.T) {
	h := newHarness(t, nil)
	files := map[string][]byte{
		"pieces/cube_ascii.stl":       testdata(t, "cube10_ascii.stl"),
		"pieces/sous/cube_quads.obj":  testdata(t, "cube10_quads.obj"),
		"pieces/sous/multi_cm.3mf":    testdata(t, "multi_cm.3mf"),
		"pieces/cylindre_binaire.stl": testdata(t, "cylinder_binary.stl"),
		"pieces/notes.txt":            []byte("ignoré"),
	}
	before := map[string]string{}
	for rel, b := range files {
		h.put(rel, b)
		before[rel] = sumHex(b)
	}
	rep := h.importAll(ImportInput{Paths: []string{filepath.Join(h.root, "pieces")}})
	if rep.Counts[StatusImported] != 4 || rep.Total != 4 {
		t.Fatalf("rapport : %+v", rep.Counts)
	}
	h.idle()

	// Originaux intacts (jamais déplacés ni modifiés).
	for rel, want := range before {
		b, err := os.ReadFile(filepath.Join(h.root, filepath.FromSlash(rel)))
		if err != nil || sumHex(b) != want {
			t.Fatalf("original %s altéré ou déplacé : %v", rel, err)
		}
	}
	// Collections : dossier importé + sous-dossier imbriqué.
	cols, err := h.svc.ListCollections(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]int{}
	for _, c := range cols {
		paths[c.Path] = c.Models
	}
	if paths["pieces"] != 2 || paths["pieces/sous"] != 2 {
		t.Fatalf("collections : %v", paths)
	}
	res, err := h.svc.Search(context.Background(), SearchQuery{})
	if err != nil || res.Total != 4 {
		t.Fatalf("search : %+v %v", res, err)
	}
	for _, m := range res.Models {
		d := h.details(m.ID)
		f := d.Files[0]
		if f.Availability != contracts.AvailableLinux || len(d.Versions) != 1 || d.Versions[0].Label != "v1" {
			t.Fatalf("%s : %+v", m.Name, d)
		}
		if err := h.blobs.Verify(f.SHA256); err != nil {
			t.Fatalf("blob %s : %v", m.Name, err)
		}
		if f.OriginPath == nil || d.Model.Provenance == nil || d.Model.Provenance.OriginPath == "" {
			t.Fatalf("provenance manquante : %+v", d.Model)
		}
		if d.Analysis.State != StateOK || d.Analysis.Triangles == 0 || d.Analysis.SizeMM == nil {
			t.Fatalf("%s analyse : %+v", m.Name, d.Analysis)
		}
		if d.Preview.State != PreviewReady || d.Preview.URL == "" || d.Preview.SHA256 == f.SHA256 {
			t.Fatalf("%s aperçu : %+v", m.Name, d.Preview)
		}
		// Le GLB est un blob dérivé distinct de l'original, valide.
		glb, err := os.ReadFile(mustPath(t, h, d.Preview.SHA256))
		if err != nil || !bytes.HasPrefix(glb, []byte("glTF")) {
			t.Fatalf("GLB invalide : %v", err)
		}
		if m.Name == "cube_ascii" {
			dm := d.Analysis.SizeMM
			if dm.X != 10 || dm.Y != 10 || dm.Z != 10 || !d.Analysis.Watertight {
				t.Fatalf("cube : %+v", d.Analysis)
			}
			if d.Model.Dimensions == nil || d.Model.Dimensions.X != 10 {
				t.Fatalf("dimensions non mémorisées : %+v", d.Model.Dimensions)
			}
		}
		if m.Name == "multi_cm" && d.Analysis.Objects < 2 {
			t.Fatalf("3MF multi-objets : %+v", d.Analysis)
		}
	}
}

func mustPath(t *testing.T, h *harness, sha string) string {
	t.Helper()
	p, err := h.blobs.Path(sha)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExactDuplicates(t *testing.T) {
	h := newHarness(t, nil)
	cube := cubeSTL(t, 10)
	h.put("a/cube.stl", cube)
	h.put("b/cube_copie.stl", cube)
	h.put("c/autre.stl", cubeSTL(t, 12))
	rep := h.importAll(ImportInput{Paths: []string{h.root}})
	if rep.Counts[StatusImported] != 2 || rep.Counts[StatusDuplicate] != 1 {
		t.Fatalf("par défaut un doublon exact n'est pas ré-importé : %+v", rep.Counts)
	}
	for _, f := range rep.Files {
		if f.Status == StatusDuplicate && f.DuplicateOf == "" {
			t.Fatal("duplicate_of manquant")
		}
	}
	if h.modelCount() != 2 || h.fileCount() != 2 {
		t.Fatalf("modèles=%d fichiers=%d", h.modelCount(), h.fileCount())
	}
	// Mode keep : deux modèles partagent le même blob (métadonnées différentes possibles).
	h.put("d/cube_garde.stl", cube)
	rep = h.importAll(ImportInput{Paths: []string{filepath.Join(h.root, "d")}, OnDuplicate: "keep", License: "CC-BY-4.0"})
	if rep.Counts[StatusImported] != 1 {
		t.Fatalf("keep : %+v", rep.Counts)
	}
	dups, err := h.svc.FindExactDuplicates(context.Background(), DuplicatesInput{})
	if err != nil || dups.TotalGroups != 1 || dups.Groups[0].Copies != 2 || dups.Groups[0].LogicalWastedBytes != dups.Groups[0].SizeBytes {
		t.Fatalf("doublons : %+v %v", dups, err)
	}
	// Contrôle ciblé par chemin : rien n'est copié.
	chk, err := h.svc.FindExactDuplicates(context.Background(), DuplicatesInput{Path: h.put("e/nouveau.stl", cube)})
	if err != nil || chk.InCatalog == nil || !*chk.InCatalog || chk.Groups[0].Copies != 2 {
		t.Fatalf("contrôle ciblé : %+v %v", chk, err)
	}
	chk, _ = h.svc.FindExactDuplicates(context.Background(), DuplicatesInput{Path: h.put("e/inconnu.stl", cubeSTL(t, 99))})
	if chk.InCatalog == nil || *chk.InCatalog {
		t.Fatalf("contenu inconnu : %+v", chk)
	}
	if h.modelCount() != 3 {
		t.Fatalf("le contrôle a créé des modèles : %d", h.modelCount())
	}
}

func TestIncrementalReimport(t *testing.T) {
	h := newHarness(t, nil)
	p := h.put("lot/piece.stl", cubeSTL(t, 10))
	h.put("lot/autre.stl", cubeSTL(t, 15))
	in := ImportInput{Paths: []string{filepath.Join(h.root, "lot")}}
	if rep := h.importAll(in); rep.Counts[StatusImported] != 2 {
		t.Fatalf("%+v", rep.Counts)
	}
	h.idle()
	jobs1, models1 := h.jobCount(), h.modelCount()

	// Ré-import sans changement : aucun doublon, aucun nouveau travail dérivé.
	rep := h.importAll(in)
	if rep.Counts[StatusUnchanged] != 2 || len(rep.Counts) != 1 {
		t.Fatalf("ré-import : %+v", rep.Counts)
	}
	h.idle()
	if h.modelCount() != models1 || h.fileCount() != 2 || h.jobCount() != jobs1+1 { // +1 : le travail d'import lui-même
		t.Fatalf("modèles=%d fichiers=%d jobs=%d (avant %d)", h.modelCount(), h.fileCount(), h.jobCount(), jobs1)
	}

	// mtime modifié, contenu identique : toujours « inchangé », index rafraîchi.
	later := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	if rep := h.importAll(in); rep.Counts[StatusUnchanged] != 2 {
		t.Fatalf("mtime seul : %+v", rep.Counts)
	}
	if h.fileCount() != 2 {
		t.Fatal("un simple changement de date a créé un fichier")
	}

	// Contenu modifié : nouvelle version du même modèle, l'ancienne reste disponible.
	h.put("lot/piece.stl", cubeSTL(t, 20))
	rep = h.importAll(in)
	if rep.Counts[StatusNewVersion] != 1 || rep.Counts[StatusUnchanged] != 1 {
		t.Fatalf("modifié : %+v", rep.Counts)
	}
	h.idle()
	var modelID string
	for _, f := range rep.Files {
		if f.Status == StatusNewVersion {
			modelID = f.ModelID
		}
	}
	d := h.details(modelID)
	if len(d.Versions) != 2 || len(d.Files) != 2 || h.modelCount() != models1 {
		t.Fatalf("versions=%d fichiers=%d", len(d.Versions), len(d.Files))
	}
	if d.Versions[0].Label != "v2" || d.Versions[0].ParentID == nil || *d.Versions[0].ParentID != d.Versions[1].ID {
		t.Fatalf("chaînage des versions : %+v", d.Versions)
	}
	if d.Analysis.SizeMM == nil || d.Analysis.SizeMM.X != 20 || d.CurrentFileID != d.Versions[0].SourceFileIDs[0] {
		t.Fatalf("la fiche doit refléter la version courante : %+v", d.Analysis)
	}
	// L'ancien contenu reste lisible dans les blobs.
	if err := h.blobs.Verify(d.Files[0].SHA256); err != nil {
		t.Fatal(err)
	}
}

func TestPathsOutsideRootsRefused(t *testing.T) {
	h := newHarness(t, nil)
	other := canon(t, t.TempDir())
	if err := os.WriteFile(filepath.Join(other, "x.stl"), cubeSTL(t, 5), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, p := range []string{
		other,
		filepath.Join(other, "x.stl"),
		filepath.Join(h.root, "..", filepath.Base(other), "x.stl"), // traversée
	} {
		if _, err := h.svc.StartImport(ctx, ImportInput{Paths: []string{p}}); !errors.Is(err, ErrNotAllowed) && !errors.Is(err, ErrNotFound) {
			t.Fatalf("import de %q devait être refusé : %v", p, err)
		}
		if _, err := h.svc.ScanFolder(ctx, ScanInput{Path: p}); err == nil {
			t.Fatalf("scan de %q devait être refusé", p)
		}
	}
	if _, err := h.svc.StartImport(ctx, ImportInput{Paths: []string{other}}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("hors racine : %v", err)
	}
	if _, err := h.svc.StartImport(ctx, ImportInput{Paths: []string{"relatif/x.stl"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("chemin relatif : %v", err)
	}
	if _, err := h.svc.FindExactDuplicates(ctx, DuplicatesInput{Path: filepath.Join(other, "x.stl")}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("contrôle de doublon hors racine : %v", err)
	}
	if h.modelCount() != 0 || h.jobCount() != 0 {
		t.Fatal("un chemin refusé a laissé des traces")
	}
	// Lien symbolique qui s'échappe de la racine : refusé (si la plateforme autorise le lien).
	link := filepath.Join(h.root, "evasion")
	if err := os.Symlink(other, link); err == nil {
		if _, err := h.svc.StartImport(ctx, ImportInput{Paths: []string{link}}); !errors.Is(err, ErrNotAllowed) {
			t.Fatalf("lien symbolique sortant : %v", err)
		}
		h.put("ok/a.stl", cubeSTL(t, 3))
		rep := h.importAll(ImportInput{Paths: []string{h.root}})
		if rep.Counts[StatusImported] != 1 {
			t.Fatalf("seul le fichier sous la racine doit être importé : %+v", rep.Counts)
		}
	} else {
		t.Logf("lien symbolique non créable sur ce poste : %v", err)
	}
	// Sans aucune racine configurée : tout est refusé.
	h2 := newHarness(t, func(c *Config) { c.ImportRoots = nil })
	if _, err := h2.svc.StartImport(ctx, ImportInput{Paths: []string{h2.root}}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("sans racine : %v", err)
	}
}

func TestScanFolderReadOnly(t *testing.T) {
	h := newHarness(t, nil)
	h.put("lot/a.stl", cubeSTL(t, 10))
	h.put("lot/sous/b.stl", cubeSTL(t, 11))
	h.put("lot/sous/c.step", []byte("ISO-10303-21;\nEND-ISO-10303-21;\n"))
	h.put("lot/readme.md", []byte("x"))
	h.put("lot/photo.jpg", []byte("x"))
	blobsBefore := countFiles(t, filepath.Join(h.data, "blobs"))
	rep, err := h.svc.ScanFolder(context.Background(), ScanInput{Path: filepath.Join(h.root, "lot")})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.ReadOnly || rep.TotalFiles != 3 || rep.NewFiles != 3 || rep.UnsupportedFiles != 2 || rep.ByFormat["stl"].Count != 2 || rep.ByFormat["step"].Count != 1 {
		t.Fatalf("%+v", rep)
	}
	if rep.BytesToCopy != rep.TotalBytes || rep.TotalBytes == 0 {
		t.Fatalf("estimation d'espace : %+v", rep)
	}
	if rep.FreeDiskBytes != nil && (rep.EnoughSpace == nil || !*rep.EnoughSpace) {
		t.Fatalf("espace libre : %+v", rep)
	}
	if h.modelCount() != 0 || h.jobCount() != 0 || countFiles(t, filepath.Join(h.data, "blobs")) != blobsBefore {
		t.Fatal("le scan a modifié l'état (copie, modèle ou travail)")
	}
	nr, _ := h.svc.ScanFolder(context.Background(), ScanInput{Path: filepath.Join(h.root, "lot"), Recursive: ptr(false)})
	if nr.TotalFiles != 1 {
		t.Fatalf("non récursif : %d", nr.TotalFiles)
	}
	// Après import : tout est « déjà importé », rien à copier.
	h.importAll(ImportInput{Paths: []string{filepath.Join(h.root, "lot")}})
	rep, _ = h.svc.ScanFolder(context.Background(), ScanInput{Path: filepath.Join(h.root, "lot")})
	if rep.AlreadyImported != 3 || rep.BytesToCopy != 0 {
		t.Fatalf("après import : %+v", rep)
	}
}

func ptr[T any](v T) *T { return &v }

func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

func TestWindowsOnlyThenCopy(t *testing.T) {
	h := newHarness(t, nil)
	p := h.put("win/piece.stl", cubeSTL(t, 10))
	rep := h.importAll(ImportInput{Paths: []string{p}, Copy: ptr(false)})
	if rep.Counts[StatusIndexed] != 1 {
		t.Fatalf("%+v", rep.Counts)
	}
	fr := rep.Files[0]
	if fr.Availability != string(contracts.AvailableWindowsOnly) {
		t.Fatalf("%+v", fr)
	}
	h.idle()
	if h.blobs.Has(fr.SHA256) {
		t.Fatal("indexation sans copie : le blob ne doit pas exister")
	}
	d := h.details(fr.ModelID)
	if d.Files[0].Availability != contracts.AvailableWindowsOnly || d.Preview.State != PreviewWindowsOnly || d.Analysis.State != "none" {
		t.Fatalf("%+v %+v", d.Preview, d.Analysis)
	}
	res, _ := h.svc.Search(context.Background(), SearchQuery{Availability: "windows_only"})
	if res.Total != 1 {
		t.Fatalf("filtre disponibilité : %d", res.Total)
	}
	// Ré-import sans copie : inchangé.
	if r := h.importAll(ImportInput{Paths: []string{p}, Copy: ptr(false)}); r.Counts[StatusUnchanged] != 1 {
		t.Fatalf("%+v", r.Counts)
	}
	// Copie ensuite : le même modèle passe en « linux » (pas de doublon), analyse + aperçu suivent.
	r := h.importAll(ImportInput{Paths: []string{p}})
	if r.Counts[StatusUpgraded] != 1 || h.modelCount() != 1 {
		t.Fatalf("%+v modèles=%d", r.Counts, h.modelCount())
	}
	h.idle()
	d = h.details(fr.ModelID)
	if d.Files[0].Availability != contracts.AvailableLinux || !h.blobs.Has(fr.SHA256) || d.Preview.State != PreviewReady {
		t.Fatalf("%+v %+v", d.Files[0], d.Preview)
	}
}

func TestOperationsRequirePreview(t *testing.T) {
	h := newHarness(t, nil)
	h.put("o/a.stl", cubeSTL(t, 10))
	h.put("o/b.stl", cubeSTL(t, 11))
	rep := h.importAll(ImportInput{Paths: []string{filepath.Join(h.root, "o")}})
	ids := map[string]string{}
	for _, f := range rep.Files {
		ids[strings.TrimSuffix(filepath.Base(f.Path), ".stl")] = f.ModelID
	}
	ctx := context.Background()

	// Sans aperçu : refusé.
	if _, err := h.svc.ApplyOperations(ctx, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("sans jeton : %v", err)
	}
	if _, err := h.svc.ApplyOperations(ctx, strings.Repeat("ab", 32)); !errors.Is(err, ErrConflict) {
		t.Fatalf("jeton inconnu : %v", err)
	}
	// Opérations invalides refusées dès l'aperçu.
	for _, op := range []Operation{
		{Op: "rename", ModelIDs: []string{ids["a"]}},
		{Op: "rename", ModelIDs: []string{ids["a"], ids["b"]}, NewName: "x"},
		{Op: "move", ModelIDs: []string{ids["a"]}},
		{Op: "rename_file", FileID: h.details(ids["a"]).Files[0].ID, NewName: "a.obj"},
		{Op: "detruire", ModelIDs: []string{ids["a"]}},
		{Op: "delete", ModelIDs: []string{"mdl_inconnu"}},
	} {
		if _, err := h.svc.PreviewOperations(ctx, []Operation{op}); err == nil {
			t.Fatalf("opération invalide acceptée : %+v", op)
		}
	}

	ops := []Operation{
		{Op: "rename", ModelIDs: []string{ids["a"]}, NewName: "Pièce A"},
		{Op: "move", ModelIDs: []string{ids["a"], ids["b"]}, Collection: "Archive/2026"},
		{Op: "add_tags", ModelIDs: []string{ids["b"]}, Tags: []string{"Test", "lot"}},
		{Op: "delete", ModelIDs: []string{ids["b"]}},
	}
	pv, err := h.svc.PreviewOperations(ctx, ops)
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Changes) != 5 || pv.ToTrash != 1 || pv.Models != 2 || len(pv.CreatesCollections) != 1 || pv.Token == "" {
		t.Fatalf("aperçu : %+v", pv)
	}
	// L'aperçu n'a rien changé.
	if d := h.details(ids["a"]); d.Model.Name != "a" || d.CollectionPath != "o" {
		t.Fatalf("l'aperçu a modifié le catalogue : %+v", d.Model)
	}
	if cols, _ := h.svc.ListCollections(ctx); len(cols) != 1 {
		t.Fatalf("collections créées par l'aperçu : %v", cols)
	}
	res, err := h.svc.ApplyOperations(ctx, pv.Token)
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 5 || len(res.Trashed) != 1 || res.Trashed[0] != ids["b"] {
		t.Fatalf("%+v", res)
	}
	da := h.details(ids["a"])
	if da.Model.Name != "Pièce A" || da.CollectionPath != "Archive/2026" {
		t.Fatalf("%+v", da.Model)
	}
	// Jeton à usage unique.
	if _, err := h.svc.ApplyOperations(ctx, pv.Token); !errors.Is(err, ErrConflict) {
		t.Fatalf("second usage : %v", err)
	}
	// Journal d'audit : chaque changement est tracé.
	ops2 := map[string]bool{}
	au, _ := h.st.ListAudit(ctx, "model", ids["a"], 50)
	for _, a := range au {
		ops2[a.Operation] = true
	}
	if !ops2["ops.rename"] || !ops2["ops.move"] {
		t.Fatalf("audit : %v", ops2)
	}
	// Corbeille : modèle absent de la recherche, présent dans la corbeille, restaurable.
	if r, _ := h.svc.Search(ctx, SearchQuery{}); r.Total != 1 {
		t.Fatalf("hors corbeille : %d", r.Total)
	}
	tr, _ := h.svc.Search(ctx, SearchQuery{OnlyTrashed: true})
	if tr.Total != 1 || tr.Models[0].ID != ids["b"] || !tr.Models[0].Trashed {
		t.Fatalf("corbeille : %+v", tr)
	}
	rr, err := h.svc.RestoreFromTrash(ctx, []string{ids["b"], ids["a"]})
	if err != nil || len(rr.Restored) != 1 || len(rr.Skipped) != 1 {
		t.Fatalf("%+v %v", rr, err)
	}
	if r, _ := h.svc.Search(ctx, SearchQuery{}); r.Total != 2 {
		t.Fatalf("après restauration : %d", r.Total)
	}
	// Un fichier en corbeille reste lisible : le blob n'a jamais été supprimé.
	if err := h.blobs.Verify(h.details(ids["b"]).Files[0].SHA256); err != nil {
		t.Fatal(err)
	}

	// État modifié entre aperçu et application : conflit, rien d'appliqué.
	pv2, err := h.svc.PreviewOperations(ctx, []Operation{{Op: "rename", ModelIDs: []string{ids["a"]}, NewName: "Autre nom"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.TagModels(ctx, TagInput{ModelIDs: []string{ids["a"]}, Add: []string{"nouveau"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ApplyOperations(ctx, pv2.Token); !errors.Is(err, ErrConflict) {
		t.Fatalf("état modifié depuis l'aperçu : %v", err)
	}
	if h.details(ids["a"]).Model.Name != "Pièce A" {
		t.Fatal("application partielle malgré le conflit")
	}
	// Aperçu expiré.
	h.svc.cfg.PreviewTTL = time.Nanosecond
	pv3, _ := h.svc.PreviewOperations(ctx, []Operation{{Op: "set_license", ModelIDs: []string{ids["a"]}, License: "MIT"}})
	time.Sleep(5 * time.Millisecond)
	if _, err := h.svc.ApplyOperations(ctx, pv3.Token); !errors.Is(err, ErrConflict) {
		t.Fatalf("aperçu expiré : %v", err)
	}
}

func TestRenameFileKeepsBlob(t *testing.T) {
	h := newHarness(t, nil)
	p := h.put("r/piece.stl", cubeSTL(t, 10))
	rep := h.importAll(ImportInput{Paths: []string{p}})
	d := h.details(rep.Files[0].ModelID)
	f := d.Files[0]
	pv, err := h.svc.PreviewOperations(context.Background(), []Operation{{Op: "rename_file", FileID: f.ID, NewName: "piece_v2.stl"}})
	if err != nil || len(pv.Warnings) == 0 {
		t.Fatalf("%+v %v", pv, err)
	}
	if _, err := h.svc.ApplyOperations(context.Background(), pv.Token); err != nil {
		t.Fatal(err)
	}
	d = h.details(f.ModelID)
	if d.Files[0].Name != "piece_v2.stl" || d.Files[0].SHA256 != f.SHA256 {
		t.Fatalf("%+v", d.Files[0])
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("l'original sur disque ne doit pas être renommé")
	}
}

func TestSearchFiltersAndPagination(t *testing.T) {
	h := newHarness(t, nil)
	for i, n := range []string{"alpha", "bravo", "charlie", "delta", "echo"} {
		h.put("lib/"+n+".stl", cubeSTL(t, float64(5+i)))
	}
	h.put("lib/sous/foxtrot.obj", testdata(t, "cube10_quads.obj"))
	h.put("lib/sous/golf.3mf", testdata(t, "multi_cm.3mf"))
	rep := h.importAll(ImportInput{Paths: []string{filepath.Join(h.root, "lib")}, Tags: []string{"Lot-1"}})
	if rep.Counts[StatusImported] != 7 {
		t.Fatalf("%+v", rep.Counts)
	}
	h.idle()
	ctx := context.Background()
	r, err := h.svc.Search(ctx, SearchQuery{Limit: 3})
	if err != nil || r.Total != 7 || len(r.Models) != 3 || !r.HasMore || *r.NextOffset != 3 || r.Models[0].Name != "alpha" {
		t.Fatalf("%+v %v", r, err)
	}
	seen := map[string]bool{}
	for off := 0; ; {
		p, err := h.svc.Search(ctx, SearchQuery{Limit: 3, Offset: off})
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range p.Models {
			if seen[m.ID] {
				t.Fatal("modèle en double entre pages")
			}
			seen[m.ID] = true
		}
		if !p.HasMore {
			break
		}
		off = *p.NextOffset
	}
	if len(seen) != 7 {
		t.Fatalf("pagination : %d", len(seen))
	}
	count := func(q SearchQuery) int {
		x, err := h.svc.Search(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return x.Total
	}
	if count(SearchQuery{Format: "obj"}) != 1 || count(SearchQuery{Format: "3mf"}) != 1 || count(SearchQuery{Format: "stl"}) != 5 {
		t.Fatal("filtre format")
	}
	if count(SearchQuery{Text: "ALP"}) != 1 || count(SearchQuery{Text: "xyz"}) != 0 || count(SearchQuery{Text: "lie ar"}) != 1 || count(SearchQuery{Text: "lie zz"}) != 0 {
		t.Fatal("recherche texte")
	}
	if count(SearchQuery{Tags: []string{"lot-1"}}) != 7 || count(SearchQuery{Tags: []string{"lot-1", "absent"}}) != 0 {
		t.Fatal("filtre étiquettes")
	}
	if count(SearchQuery{Collection: "lib/sous"}) != 2 || count(SearchQuery{Collection: "lib"}) != 7 || count(SearchQuery{Text: "%"}) != 0 {
		t.Fatal("filtre collection / échappement LIKE")
	}
	if _, err := h.svc.Search(ctx, SearchQuery{Collection: "inconnue"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("collection inconnue : %v", err)
	}
	if _, err := h.svc.Search(ctx, SearchQuery{Format: "xyz"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("format invalide accepté")
	}
	lc, err := h.svc.ListCollection(ctx, ListCollectionInput{Collection: "lib"})
	if err != nil || lc.Models.Total != 5 || len(lc.Subcollections) != 1 || lc.Subcollections[0].TotalModel != 2 {
		t.Fatalf("list_collection : %+v %v", lc, err)
	}
	lc, _ = h.svc.ListCollection(ctx, ListCollectionInput{Collection: "lib", Recursive: true})
	if lc.Models.Total != 7 {
		t.Fatalf("récursif : %d", lc.Models.Total)
	}
}

func TestLinkVersions(t *testing.T) {
	h := newHarness(t, nil)
	h.put("v/pince_v1.stl", cubeSTL(t, 10))
	h.put("v/pince_v2.stl", cubeSTL(t, 12))
	h.put("v/pince_v3.stl", cubeSTL(t, 14))
	rep := h.importAll(ImportInput{Paths: []string{filepath.Join(h.root, "v")}})
	id := map[string]string{}
	for _, f := range rep.Files {
		id[strings.TrimSuffix(filepath.Base(f.Path), ".stl")] = f.ModelID
	}
	h.idle()
	ctx := context.Background()
	if _, err := h.svc.LinkVersions(ctx, LinkInput{TargetModelID: id["pince_v1"], VersionModelIDs: []string{id["pince_v1"]}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("auto-lien : %v", err)
	}
	res, err := h.svc.LinkVersions(ctx, LinkInput{TargetModelID: id["pince_v1"], VersionModelIDs: []string{id["pince_v2"], id["pince_v3"]}, Note: "révisions"})
	if err != nil || len(res.Created) != 2 || len(res.Trashed) != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	d := h.details(id["pince_v1"])
	if len(d.Versions) != 3 || len(d.Files) != 3 || d.Versions[0].Label != "v3" || d.Versions[0].Note != "révisions" {
		t.Fatalf("%+v", d.Versions)
	}
	if *d.Versions[0].ParentID != d.Versions[1].ID || *d.Versions[1].ParentID != d.Versions[2].ID {
		t.Fatal("chaîne de parents")
	}
	if d.Analysis.SizeMM == nil || d.Analysis.SizeMM.X != 14 {
		t.Fatalf("le fichier courant doit être celui de la dernière version : %+v", d.Analysis)
	}
	if r, _ := h.svc.Search(ctx, SearchQuery{}); r.Total != 1 {
		t.Fatalf("les modèles intégrés doivent être en corbeille : %d", r.Total)
	}
	if _, err := h.svc.RestoreFromTrash(ctx, []string{id["pince_v2"]}); err != nil {
		t.Fatal(err)
	}
	// Une version reste immuable : le déclencheur SQL refuse toute modification.
	if _, err := h.st.DB().Exec(`UPDATE model_versions SET note='x' WHERE id=?`, d.Versions[0].ID); err == nil {
		t.Fatal("version modifiable")
	}
}

func TestResumeAfterInterruption(t *testing.T) {
	data, root := canon(t, t.TempDir()), canon(t, t.TempDir())
	h := open(t, data, root, nil)
	for i := 0; i < 5; i++ {
		h.put("lot/p"+string(rune('a'+i))+".stl", cubeSTL(t, float64(5+i)))
	}
	in := ImportInput{Paths: []string{filepath.Join(root, "lot")}}
	// Interruption après le 2e fichier (annulation du contexte).
	ctx, cancel := context.WithCancel(context.Background())
	h.svc.testHook = func(done int) {
		if done == 2 {
			cancel()
		}
	}
	if _, err := h.svc.RunImport(ctx, in, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("interruption : %v", err)
	}
	if h.modelCount() != 2 {
		t.Fatalf("après interruption : %d modèles", h.modelCount())
	}
	h.svc.testHook = nil
	rep, err := h.svc.RunImport(context.Background(), in, nil, nil)
	if err != nil || rep.Counts[StatusUnchanged] != 2 || rep.Counts[StatusImported] != 3 || h.modelCount() != 5 || h.fileCount() != 5 {
		t.Fatalf("reprise : %+v %v modèles=%d", rep, err, h.modelCount())
	}
	h.stop()

	// Reprise par la file : un import « running » orphelin (crash) est remis en file et terminé.
	data2, root2 := canon(t, t.TempDir()), canon(t, t.TempDir())
	h1 := open(t, data2, root2, nil)
	for i := 0; i < 4; i++ {
		h1.put("lot/q"+string(rune('a'+i))+".stl", cubeSTL(t, float64(5+i)))
	}
	j, err := h1.svc.StartImport(context.Background(), ImportInput{Paths: []string{filepath.Join(root2, "lot")}})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := h1.q.Claim(context.Background(), "w1")
	if err != nil || claimed == nil || claimed.ID != j.ID {
		t.Fatalf("claim : %+v %v", claimed, err)
	}
	h1.stop() // arrêt brutal : le travail reste « running »

	h2 := open(t, data2, root2, nil)
	rec, err := h2.q.Recover(context.Background(), h2.reg.Replayable)
	if err != nil || len(rec.Requeued) != 1 || rec.Requeued[0] != j.ID {
		t.Fatalf("reprise de la file : %+v %v", rec, err)
	}
	h2.start()
	state, done, err := h2.svc.WaitJob(context.Background(), j.ID, 30*time.Second)
	if err != nil || !done || state != "succeeded" {
		t.Fatalf("job repris : %s %v %v", state, done, err)
	}
	h2.idle()
	if h2.modelCount() != 4 {
		t.Fatalf("après reprise : %d modèles", h2.modelCount())
	}
}

func TestWrongMagicRejected(t *testing.T) {
	h := newHarness(t, nil)
	h.put("m/faux.3mf", []byte("ceci n'est pas un zip"))
	h.put("m/faux.step", []byte("rien à voir"))
	rep := h.importAll(ImportInput{Paths: []string{filepath.Join(h.root, "m")}})
	if rep.Counts[StatusRejected] != 2 || h.modelCount() != 0 {
		t.Fatalf("%+v", rep.Counts)
	}
	h.put("m/corrompu.stl", []byte("solid vide\nendsolid\n"))
	rep = h.importAll(ImportInput{Paths: []string{filepath.Join(h.root, "m", "corrompu.stl")}})
	h.idle()
	if rep.Counts[StatusImported] != 1 {
		t.Fatalf("%+v", rep.Counts)
	}
	d := h.details(rep.Files[0].ModelID)
	if d.Analysis.State != StateError || d.Preview.State != PreviewError {
		t.Fatalf("un maillage vide doit être signalé en erreur sans casser l'import : %+v / %+v", d.Analysis, d.Preview)
	}
}

func TestSignedAssetsServeOnlyPreviews(t *testing.T) {
	h := newHarness(t, nil)
	rep := h.importAll(ImportInput{Paths: []string{h.put("s/c.stl", cubeSTL(t, 10))}})
	h.idle()
	d := h.details(rep.Files[0].ModelID)
	ctx := context.Background()
	url, exp := h.svc.SignedAssetURL(d.Preview.SHA256)
	if !strings.HasPrefix(url, "http://127.0.0.1:7878/api/assets/"+d.Preview.SHA256+".glb?exp=") || exp.Before(time.Now()) {
		t.Fatal(url)
	}
	expS, sig := queryOf(url, "exp"), queryOf(url, "sig")
	if !h.svc.VerifyAssetSignature(d.Preview.SHA256, expS, sig) {
		t.Fatal("signature valide refusée")
	}
	if h.svc.VerifyAssetSignature(d.Preview.SHA256, expS, sig[:len(sig)-1]+"x") || h.svc.VerifyAssetSignature(d.Files[0].SHA256, expS, sig) ||
		h.svc.VerifyAssetSignature(d.Preview.SHA256, "1", sig) {
		t.Fatal("signature falsifiée acceptée")
	}
	if f, err := h.svc.OpenPreviewAsset(ctx, d.Preview.SHA256); err != nil {
		t.Fatal(err)
	} else {
		f.Close()
	}
	// Jamais l'original ni un blob arbitraire.
	if _, err := h.svc.OpenPreviewAsset(ctx, d.Files[0].SHA256); !errors.Is(err, ErrAssetUnknown) {
		t.Fatalf("l'original ne doit pas être servi : %v", err)
	}
	h.svc.SetBaseURL("http://127.0.0.1:9999/")
	if u, _ := h.svc.SignedAssetURL(d.Preview.SHA256); !strings.HasPrefix(u, "http://127.0.0.1:9999/api/") {
		t.Fatal(u)
	}
	pa, err := h.svc.GetPreviewAsset(ctx, PreviewAssetInput{ModelID: d.Model.ID})
	if err != nil || pa.Preview.State != PreviewReady || pa.MIME != "model/gltf-binary" || pa.ExpiresAt == nil || strings.Contains(pa.Note, "base64") {
		t.Fatalf("%+v %v", pa, err)
	}
}

func queryOf(url, key string) string {
	_, q, _ := strings.Cut(url, "?")
	for _, kv := range strings.Split(q, "&") {
		if k, v, _ := strings.Cut(kv, "="); k == key {
			return v
		}
	}
	return ""
}

func TestAnalyzeGeometryUnitAndFab(t *testing.T) {
	h := newHarness(t, nil)
	rep := h.importAll(ImportInput{Paths: []string{h.put("g/c.stl", cubeSTL(t, 10))}})
	h.idle()
	id := rep.Files[0].ModelID
	ctx := context.Background()
	a, err := h.svc.AnalyzeGeometry(ctx, AnalyzeInput{ModelID: id})
	if err != nil || a.State != StateOK || !a.Cached || a.Summary.SizeMM.X != 10 || len(a.Fabricability) == 0 || a.Machine != "p1s" {
		t.Fatalf("%+v %v", a, err)
	}
	if !strings.Contains(strings.Join(a.Caveats, " "), "millimètres supposés") {
		t.Fatalf("l'hypothèse d'unité doit être signalée : %v", a.Caveats)
	}
	a, err = h.svc.AnalyzeGeometry(ctx, AnalyzeInput{ModelID: id, Unit: "cm", Machine: "a1mini", SkipFab: true})
	if err != nil || a.Cached || a.Summary.SizeMM.X != 100 || a.UnitApplied != "cm" || len(a.Fabricability) != 0 {
		t.Fatalf("%+v %v", a, err)
	}
	if h.details(id).Files[0].Unit != "cm" {
		t.Fatal("unité non mémorisée sur le fichier")
	}
	h.idle()
	if h.details(id).Model.Dimensions.X != 100 {
		t.Fatalf("dimensions du modèle : %+v", h.details(id).Model.Dimensions)
	}
	if _, err := h.svc.AnalyzeGeometry(ctx, AnalyzeInput{ModelID: id, Machine: "xyz"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("machine inconnue acceptée")
	}
	if _, err := h.svc.AnalyzeGeometry(ctx, AnalyzeInput{ModelID: id, Unit: "furlong"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("unité inconnue acceptée")
	}
	if _, err := h.svc.AnalyzeGeometry(ctx, AnalyzeInput{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("sans cible")
	}
}

func TestSeedDemoIdempotent(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	rep, err := h.svc.SeedDemo(ctx, SeedOptions{WaitIdle: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Models != 10 || rep.Counts[StatusImported] != 10 || rep.Counts[StatusNewVersion] != 1 || !rep.DerivedIdle || rep.CADSkipped != "" && len(rep.CADModels) > 0 {
		t.Fatalf("%+v", rep)
	}
	pince, _ := h.svc.Search(ctx, SearchQuery{Text: "pince_serrage"})
	if pince.Total != 1 || pince.Models[0].Versions != 2 || pince.Models[0].License != "CC0-1.0" || pince.Models[0].CollectionPath != "Démo/Mecanique" {
		t.Fatalf("%+v", pince.Models)
	}
	all, _ := h.svc.Search(ctx, SearchQuery{Limit: 50})
	for _, m := range all.Models {
		if m.Preview.State != PreviewReady || m.Dimensions == nil {
			t.Fatalf("%s : aperçu %s dimensions %v", m.Name, m.Preview.State, m.Dimensions)
		}
	}
	if r2, _ := h.svc.SeedDemo(ctx, SeedOptions{}); r2.Models != 10 || r2.Generated != 0 || r2.Counts[StatusImported] != 0 {
		t.Fatalf("la démo doit être idempotente : %+v", r2)
	}
}

func TestNormTagsAndUnits(t *testing.T) {
	got := normTags([]string{"  Pince  Haut ", "pince haut", "", "B", "a"})
	if strings.Join(got, "|") != "a|b|pince haut" {
		t.Fatalf("%v", got)
	}
	for in, want := range map[string]string{"": "", "MM": "mm", "Pouces": "inch", "m": "m", "centimetre": "cm"} {
		if got, err := normalizeUnit(in); err != nil || got != want {
			t.Fatalf("%q -> %q %v", in, got, err)
		}
	}
	if unitScale("inch") != 25.4 || unitScale("") != 1 {
		t.Fatal("facteurs")
	}
}
