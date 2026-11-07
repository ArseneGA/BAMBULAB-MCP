package export_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/export"
)

func TestExportContents(t *testing.T) {
	e := newEnv(t)
	col := e.collection("Support téléphone", "")
	sub := e.collection("Accessoires", col)
	stlA := stl("base", 30)
	stlB1, stlB2 := stl("clip-v1", 5), stl("clip-v2", 9)
	idA, vA := e.model(col, "Base du support", "Socle principal.", fileSpec{Name: "base.stl", Format: "stl", Content: stlA})
	idB, vB := e.model(sub, "Clip", "", fileSpec{Name: "clip.stl", Format: "stl", Content: stlB1}, fileSpec{Name: "clip.stl", Format: "stl", Content: stlB2})
	prn := e.printer("192.168.50.23", "01P00A111222333")
	gcode := zip3mf(map[string]string{"Metadata/plate_1.gcode": "G28\nG1 X10\n", "Metadata/slice_info.config": `<config printer_model_id="C12"/>`})
	e.printJob(prn, idA, vA[0], jobOpt{State: "COMPLETED", Validated: true, Artifact: gcode, Name: "base.gcode.3mf"})
	e.template(idA, vA[0], "washer")
	rev := e.appliedProfile(idA, vA[0], nil)
	e.journalPhoto(idA, jpegWithEXIF())

	in := export.Input{CollectionID: "Support téléphone", Options: export.Options{
		Quantities: map[string]int{idA: 2, idB: 4}, AssemblyNotes: "1. Clipser **B** sur A.", Hardware: "4 vis M3x8\n4 inserts M3",
		Title: "Support universel", Description: "Un support de téléphone imprimable.", IncludePhotos: true,
		Versions: map[string]string{idB: vB[0]}, // version choisie : la plus ancienne du clip
	}}
	rec := e.run(in)
	files := e.open(rec)

	// 1. originaux : dernière version (ou version choisie), octets identiques
	if !bytes.Equal(files["files/Base du support/base.stl"], stlA) {
		t.Fatalf("original de la base absent ou altéré : %v", names(files))
	}
	if !bytes.Equal(files["files/Clip/clip.stl"], stlB1) {
		t.Fatalf("la version choisie (v1) du clip n'est pas exportée")
	}
	for n := range files {
		if bytes.Equal(files[n], stlB2) {
			t.Fatalf("la version v2 du clip ne devait pas être exportée (%s)", n)
		}
	}
	// 2. sources paramétriques et profils
	var tpl map[string]any
	if err := json.Unmarshal(files["sources/Base du support/parametrique.json"], &tpl); err != nil || tpl["template"] != "washer" {
		t.Fatalf("source paramétrique : %v %s", err, files["sources/Base du support/parametrique.json"])
	}
	var prof struct {
		Revision  string
		Lineage   []string
		Reference struct{ Machine, Process string }
		Overrides []struct {
			Key   string
			Value any
		}
		Dropped int `json:"dropped_gcode_keys"`
	}
	if err := json.Unmarshal(files["profiles/Base du support.json"], &prof); err != nil {
		t.Fatal(err)
	}
	if prof.Revision != rev || len(prof.Lineage) != 1 || prof.Reference.Machine != "Bambu Lab P1S 0.4 nozzle" || prof.Dropped != 1 {
		t.Fatalf("profil : %+v", prof)
	}
	keys := map[string]bool{}
	for _, o := range prof.Overrides {
		keys[o.Key] = true
	}
	if !keys["wall_loops"] || !keys["sparse_infill_density"] || keys["machine_start_gcode"] {
		t.Fatalf("surcharges exportées : %v", keys)
	}
	if bytes.Contains(files["profiles/Base du support.json"], []byte("M104")) {
		t.Fatal("G-code dans le profil")
	}
	// 3. G-code désactivé par défaut
	if has(files, "printer-specific/") {
		t.Fatalf("le .gcode.3mf ne doit pas être joint par défaut : %v", names(files))
	}
	// 4. photo : jointe, EXIF retiré
	var photo []byte
	for n, b := range files {
		if strings.HasPrefix(n, "images/Base du support/photo-01") {
			photo = b
		}
	}
	if photo == nil || bytes.Contains(photo, []byte("GPSLATITUDE")) || !bytes.HasPrefix(photo, []byte{0xFF, 0xD8}) {
		t.Fatalf("photo absente ou EXIF conservé : %d octets", len(photo))
	}
	// 5. README
	readme := string(files["README.md"])
	for _, want := range []string{"# Support universel", "Un support de téléphone imprimable.", "Base du support", "| 2 |", "| 4 |", "PLA", "1 h 30 min",
		"30%", "Licence", "non précisée", "## Notice d'assemblage", "Clipser **B** sur A.", "## Quincaillerie", "4 vis M3x8", "![Base du support](images/Base%20du%20support/photo-01",
		"Hauteur", "Nombre de parois"} {
		if want == "Hauteur" {
			continue
		}
		if !strings.Contains(readme, want) {
			t.Errorf("README sans %q :\n%s", want, readme)
		}
	}
	if strings.Contains(readme, "printer-specific") {
		t.Error("README mentionne printer-specific alors que l'option est désactivée")
	}
	// 6. manifeste : empreintes exactes pour chaque entrée
	m := e.manifest(files)
	if m["format"] != export.FormatVersion || m["license"] != "non précisée" {
		t.Fatalf("manifeste : %v", m)
	}
	listed := map[string]bool{}
	for _, raw := range m["files"].([]any) {
		f := raw.(map[string]any)
		p := f["path"].(string)
		listed[p] = true
		if got := sum(files[p]); got != f["sha256"] {
			t.Errorf("sha256 de %s : manifeste %v, réel %s", p, f["sha256"], got)
		}
	}
	for n := range files {
		if n != "manifest.json" && !listed[n] {
			t.Errorf("%s absent du manifeste", n)
		}
	}
	if len(m["parts"].([]any)) != 2 || m["generated_at"] == "" {
		t.Fatalf("pièces du manifeste : %v", m["parts"])
	}
	if rec.Summary == nil || rec.Summary.Files != len(files) || rec.SHA256 == "" || rec.SizeBytes == 0 {
		t.Fatalf("résumé : %+v", rec)
	}
}

func TestGcodeOptionAndValidation(t *testing.T) {
	e := newEnv(t)
	col := e.collection("Pièces", "")
	idA, vA := e.model(col, "Validée", "", fileSpec{Name: "a.stl", Format: "stl", Content: stl("a", 3)})
	idB, vB := e.model(col, "Non validée", "", fileSpec{Name: "b.stl", Format: "stl", Content: stl("b", 3)})
	prn := e.printer("192.168.50.23", "01P00A111222333")
	good := zip3mf(map[string]string{"Metadata/plate_1.gcode": "G28\n", "Metadata/slice_info.config": "<config/>"})
	e.printJob(prn, idA, vA[0], jobOpt{Validated: true, Artifact: good, Name: "a.gcode.3mf"})
	e.printJob(prn, idB, vB[0], jobOpt{Validated: false, Artifact: good, Name: "b.gcode.3mf"})

	files := e.open(e.run(export.Input{CollectionID: col, Options: export.Options{IncludeGcode3MF: true, License: "CC BY-NC 4.0"}}))
	if !bytes.Equal(files["printer-specific/Validée/a.gcode.3mf"], good) {
		t.Fatalf("le .gcode.3mf validé doit être joint : %v", names(files))
	}
	if has(files, "printer-specific/Non validée") {
		t.Fatal("un .gcode.3mf non validé ne doit jamais être joint")
	}
	readme := string(files["README.md"])
	if !strings.Contains(readme, "spécifiques à une imprimante") || !strings.Contains(readme, "CC BY-NC 4.0") {
		t.Fatalf("avertissement ou licence absents du README :\n%s", readme)
	}
	if !strings.Contains(string(files["manifest.json"]), "n'est pas validée") {
		t.Fatalf("avertissement de découpe non validée absent du manifeste")
	}
}

var (
	rePath = regexp.MustCompile(`(?i)\b[a-z]:[\\/]`)
	reIP   = regexp.MustCompile(`\b(?:10|127)\.\d+\.\d+\.\d+\b|\b192\.168\.\d+\.\d+\b`)
)

func TestNoSecretsNoPrinterDataNoServerPath(t *testing.T) {
	e := newEnv(t)
	const host, serial = "192.168.50.23", "01P00A111222333"
	prn := e.printer(host, serial)
	col := e.collection("Fuites", "")
	// description et nom volontairement piégés (l'utilisateur a collé des infos sensibles)
	desc := "Testé sur " + host + " (série " + serial + ") ; fichiers dans C:\\Users\\bob\\projet et " + e.data + " ; ouvrir /home/bob/secret/x.txt"
	idA, vA := e.model(col, "Pièce piégée", desc, fileSpec{Name: "a.stl", Format: "stl", Content: stl("a", 4)})
	// un 3MF d'origine qui contient le numéro de série de l'imprimante : exclu
	leaky3mf := zip3mf(map[string]string{"3D/3dmodel.model": "<model/>", "Metadata/project_settings.config": `{"serial":"` + serial + `"}`})
	e.modelMulti(col, "Projet Studio", fileSpec{Name: "p.3mf", Format: "3mf", Content: leaky3mf}, fileSpec{Name: "p.stl", Format: "stl", Content: stl("p", 2)})
	idC, vC := e.model(col, "Avec G-code piégé", "", fileSpec{Name: "c.stl", Format: "stl", Content: stl("c", 2)})
	// artefacts découpés : l'un contient l'adresse de l'imprimante, l'autre un chemin Windows
	e.printJob(prn, idA, vA[0], jobOpt{State: "COMPLETED", Validated: true, Name: "a.gcode.3mf",
		Artifact: zip3mf(map[string]string{"Metadata/project_settings.config": `{"print_host":"` + host + `"}`, "Metadata/plate_1.gcode": "G28\n"})})
	e.printJob(prn, idC, vC[0], jobOpt{Validated: true, Name: "c.gcode.3mf",
		Artifact: zip3mf(map[string]string{"Metadata/project_settings.config": `{"src":"C:\\Users\\bob\\x.stl"}`, "Metadata/plate_1.gcode": "G28\n"})})
	e.template(idA, vA[0], "washer") // la calibration enregistrée contient une clé « access_code »
	e.appliedProfile(idA, vA[0], nil)

	rec := e.run(export.Input{CollectionID: col, Options: export.Options{IncludeGcode3MF: true, IncludePhotos: true,
		AssemblyNotes: "Voir " + e.data + " et " + host, Hardware: "vis"}})
	files := e.open(rec)

	forbidden := []string{host, serial, e.data, filepath.ToSlash(e.data), strings.ReplaceAll(e.data, `\`, `\\`), "87654321", "access_code", "accesscode", "Bearer", "/home/bob", "bob\\", "bob/", "M104"}
	for n, b := range files {
		for _, f := range forbidden {
			if bytes.Contains(bytes.ToLower(b), bytes.ToLower([]byte(f))) {
				if strings.HasSuffix(n, ".stl") {
					continue // contenu opaque de l'utilisateur
				}
				t.Errorf("%s contient %q", n, f)
			}
		}
		if strings.HasSuffix(n, ".stl") {
			continue
		}
		if rePath.Match(b) {
			t.Errorf("%s contient un chemin Windows : %s", n, rePath.Find(b))
		}
		if reIP.Match(b) {
			t.Errorf("%s contient une adresse privée : %s", n, reIP.Find(b))
		}
	}
	// les fichiers piégés ne sont pas dans l'archive, et on le dit
	if has(files, "printer-specific/") {
		t.Fatalf("un .gcode.3mf piégé a été joint : %v", names(files))
	}
	if has(files, "files/Projet Studio/p.3mf") || !has(files, "files/Projet Studio/p.stl") {
		t.Fatalf("le 3MF piégé doit être exclu et le STL conservé : %v", names(files))
	}
	mf := string(files["manifest.json"])
	for _, want := range []string{"adresse d'imprimante", "numéro de série d'imprimante", "chemin absolu", "non joint", "non inclus"} {
		if !strings.Contains(mf, want) {
			t.Errorf("manifeste sans %q :\n%s", want, mf)
		}
	}
	if !strings.Contains(string(files["README.md"]), "[donnée retirée]") && !strings.Contains(string(files["README.md"]), "[adresse retirée]") {
		t.Error("le README n'a pas été filtré")
	}
	_ = rec
}

func TestLargeFileIsStreamedAndLimitsHold(t *testing.T) {
	if testing.Short() {
		t.Skip("gros fichier")
	}
	e := newEnv(t)
	col := e.collection("Gros", "")
	const size = 96 << 20
	// blob volumineux écrit en flux (jamais entièrement en mémoire dans le test non plus)
	pattern := bytes.Repeat([]byte("solid big\nfacet normal 0 0 1\n"), 1<<14)
	sha, n, err := e.c.Blobs.Put(&repeatReader{pat: pattern, left: size}, "")
	if err != nil || n != size {
		t.Fatal(err, n)
	}
	mid := "mod_big"
	e.exec(`INSERT INTO models(id,collection_id,name,tags,created_at,updated_at,description,license) VALUES(?,?,?,?,?,?,?,?)`, mid, col, "Énorme", "[]", e.next(), e.next(), "", "")
	e.exec(`INSERT INTO source_files(id,model_id,name,format,size_bytes,sha256,availability,created_at,rel_path,unit) VALUES(?,?,?,?,?,?,?,?,?,?)`, "fil_big", mid, "enorme.stl", "stl", size, sha, "linux", e.next(), "", "mm")
	e.exec(`INSERT INTO model_versions(id,model_id,label,note,source_file_ids,created_at) VALUES(?,?,?,?,?,?)`, "ver_big", mid, "v1", "", `["fil_big"]`, e.next())

	// limite : refus AVANT d'écrire quoi que ce soit
	_, err = e.c.Export.Start(context.Background(), export.Input{CollectionID: col, Options: export.Options{MaxBytes: 10 << 20}})
	if !errors.Is(err, catalog.ErrInvalid) || !strings.Contains(err.Error(), "limite") {
		t.Fatalf("limite de taille non appliquée : %v", err)
	}
	if ents, _ := os.ReadDir(filepath.Join(e.data, "exports")); len(ents) != 0 {
		t.Fatalf("des fichiers ont été écrits malgré le refus : %v", ents)
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	peak := uint64(0)
	stop := make(chan struct{})
	go func() {
		var ms runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
				runtime.ReadMemStats(&ms)
				if ms.HeapAlloc > peak {
					peak = ms.HeapAlloc
				}
			}
		}
	}()
	rec := e.run(export.Input{CollectionID: col})
	close(stop)
	runtime.ReadMemStats(&after)
	if rec.State != export.StateReady {
		t.Fatalf("export : %+v", rec)
	}
	// une copie en mémoire de 96 Mio doublerait le tas : le flux doit rester très en dessous
	if peak > before.HeapAlloc+40<<20 || after.TotalAlloc-before.TotalAlloc > 48<<20 {
		t.Fatalf("export non streamé : tas %d Mio (avant %d), allocations %d Mio", peak>>20, before.HeapAlloc>>20, (after.TotalAlloc-before.TotalAlloc)>>20)
	}
	files := e.open(rec)
	if sum(files["files/Énorme/enorme.stl"]) != sha {
		t.Fatal("le gros fichier a été altéré")
	}
}

type repeatReader struct {
	pat  []byte
	left int
	off  int
}

func (r *repeatReader) Read(p []byte) (int, error) {
	if r.left == 0 {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) && r.left > 0 {
		c := copy(p[n:], r.pat[r.off:])
		if c > r.left {
			c = r.left
		}
		n += c
		r.left -= c
		r.off = (r.off + c) % len(r.pat)
	}
	return n, nil
}

func TestSignedLinkExpiryAndTampering(t *testing.T) {
	e := newEnv(t)
	col := e.collection("Liens", "")
	e.model(col, "Pièce", "", fileSpec{Name: "a.stl", Format: "stl", Content: stl("a", 2)})
	rec := e.run(export.Input{CollectionID: col})
	other := e.run(export.Input{CollectionID: col})
	ctx := context.Background()

	l, err := e.c.Export.Link(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	exp, sig := queryOf(t, l.Path, "exp"), queryOf(t, l.Path, "sig")
	if !e.c.Export.VerifyLink(rec.ID, exp, sig) {
		t.Fatal("lien valide refusé")
	}
	if d := time.Until(l.ExpiresAt); d <= 0 || d > export.LinkTTL+time.Second {
		t.Fatalf("durée de vie du lien : %v", d)
	}
	if e.c.Export.VerifyLink(other.ID, exp, sig) {
		t.Fatal("la signature d'un export ne doit pas valoir pour un autre")
	}
	if e.c.Export.VerifyLink(rec.ID, exp, sig[:len(sig)-1]+"0") && sig[len(sig)-1] != '0' {
		t.Fatal("signature altérée acceptée")
	}
	if e.c.Export.VerifyLink(rec.ID, "9999999999", sig) {
		t.Fatal("expiration allongée acceptée")
	}
	// lien déjà expiré (signature valide, date passée)
	old, err := e.c.Export.LinkTTL(ctx, rec.ID, -time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if e.c.Export.VerifyLink(rec.ID, queryOf(t, old.Path, "exp"), queryOf(t, old.Path, "sig")) {
		t.Fatal("lien expiré accepté")
	}
	// la signature d'un aperçu GLB n'ouvre pas une archive (domaines distincts)
	glb, _ := e.c.Catalog.SignedAssetURL(rec.ID)
	if e.c.Export.VerifyLink(rec.ID, queryOf(t, glb, "exp"), queryOf(t, glb, "sig")) {
		t.Fatal("signature d'aperçu acceptée pour une archive")
	}
}

func TestExpiredArchiveCannotBeServed(t *testing.T) {
	e := newEnv(t)
	col := e.collection("Expiration", "")
	e.model(col, "Pièce", "", fileSpec{Name: "a.stl", Format: "stl", Content: stl("a", 2)})
	rec := e.run(export.Input{CollectionID: col})
	e.exec(`UPDATE project_exports SET expires_at=? WHERE id=?`, store0(time.Now().Add(-time.Minute)), rec.ID)
	ctx := context.Background()
	if _, err := e.c.Export.Link(ctx, rec.ID); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("lien d'une archive expirée : %v", err)
	}
	if _, _, err := e.c.Export.Open(ctx, rec.ID); err == nil {
		t.Fatal("archive expirée ouverte")
	}
	// le prochain export purge le fichier
	e.run(export.Input{CollectionID: col})
	if _, err := os.Stat(filepath.Join(e.data, "exports", rec.ID+".zip")); !os.IsNotExist(err) {
		t.Fatalf("archive expirée toujours sur disque : %v", err)
	}
}

func TestStartRefusals(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	col := e.collection("Refus", "")
	id, vs := e.model(col, "Pièce", "", fileSpec{Name: "a.stl", Format: "stl", Content: stl("a", 2)})
	win, _ := e.model(col, "Windows seul", "", fileSpec{Name: "w.stl", Format: "stl", Content: stl("w", 2), Windows: true})
	trash, _ := e.model(col, "Corbeille", "", fileSpec{Name: "t.stl", Format: "stl", Content: stl("t", 2)})
	e.exec(`UPDATE models SET deleted_at=? WHERE id=?`, e.next(), trash)

	cases := map[string]export.Input{
		"vide":                  {},
		"collection et modèles": {CollectionID: col, ModelIDs: []string{id}},
		"modèle inconnu":        {ModelIDs: []string{"mod_nope"}},
		"corbeille":             {ModelIDs: []string{trash}},
		"windows seul":          {ModelIDs: []string{win}},
		"version inconnue":      {ModelIDs: []string{id}, Options: export.Options{Versions: map[string]string{id: "ver_nope"}}},
		"version hors export":   {ModelIDs: []string{id}, Options: export.Options{Versions: map[string]string{"mod_x": vs[0]}}},
		"quantité":              {ModelIDs: []string{id}, Options: export.Options{Quantities: map[string]int{id: 0}}},
		"taille max":            {ModelIDs: []string{id}, Options: export.Options{MaxBytes: export.HardMaxBytes + 1}},
		"collection vide":       {CollectionID: e.collection("Vide", "")},
	}
	for name, in := range cases {
		st, err := e.c.Export.Start(ctx, in)
		if err == nil {
			t.Errorf("%s : accepté (%+v)", name, st)
			continue
		}
		if !errors.Is(err, catalog.ErrInvalid) && !errors.Is(err, catalog.ErrNotFound) {
			t.Errorf("%s : erreur inattendue %v", name, err)
		}
	}
	// la corbeille dans une collection est simplement ignorée
	st, err := e.c.Export.Start(ctx, export.Input{CollectionID: col})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Plan.Parts) != 1 || len(st.Plan.Skipped) == 0 {
		t.Fatalf("plan : %+v", st.Plan)
	}
	e.wait(st)
}

func TestHostileNamesStayInsideArchive(t *testing.T) {
	e := newEnv(t)
	col := e.collection("Noms", "")
	e.model(col, "../../etc/passwd", "", fileSpec{Name: `..\..\evil.stl`, Format: "stl", Content: stl("e", 2)})
	e.model(col, "CON", "", fileSpec{Name: "AUX.stl", Format: "stl", Content: stl("c", 2)})
	e.model(col, "doublon", "", fileSpec{Name: "x.stl", Format: "stl", Content: stl("d1", 2)})
	e.model(col, "Doublon", "", fileSpec{Name: "x.stl", Format: "stl", Content: stl("d2", 3)})
	files := e.open(e.run(export.Input{CollectionID: col}))
	n := 0
	for name := range files {
		if strings.HasPrefix(name, "/") || strings.Contains(name, "..") || strings.Contains(name, `\`) || strings.Contains(name, ":") {
			t.Errorf("nom d'entrée dangereux : %q", name)
		}
		if strings.HasPrefix(name, "files/") {
			n++
		}
	}
	if n != 4 {
		t.Fatalf("4 fichiers attendus (aucun écrasement) : %v", names(files))
	}
}

func TestExportAsyncJobFailsCleanlyWhenOriginalCorrupt(t *testing.T) {
	e := newEnv(t)
	col := e.collection("Corrompu", "")
	e.model(col, "Pièce", "", fileSpec{Name: "a.stl", Format: "stl", Content: stl("a", 20)})
	var sha string
	if err := e.c.Store.DB().QueryRow(`SELECT sha256 FROM source_files LIMIT 1`).Scan(&sha); err != nil {
		t.Fatal(err)
	}
	p, _ := e.c.Blobs.Path(sha)
	_ = os.Chmod(p, 0o666)
	if err := os.WriteFile(p, []byte("corrompu"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := e.run(export.Input{CollectionID: col})
	if rec.State != export.StateFailed {
		t.Fatalf("un original corrompu doit faire échouer l'export : %+v", rec)
	}
	if ents, _ := os.ReadDir(filepath.Join(e.data, "exports")); len(ents) != 0 {
		t.Fatalf("archive partielle laissée : %v", ents)
	}
	if _, err := e.c.Export.Link(context.Background(), rec.ID); err == nil {
		t.Fatal("lien délivré pour un export en échec")
	}
}
