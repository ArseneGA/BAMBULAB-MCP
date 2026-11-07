package export_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/export"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

type env struct {
	t    *testing.T
	c    *core.Core
	data string
	seq  int
	base time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	data := t.TempDir()
	c, err := core.Open(ctx, core.Config{DataDir: data, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		PollInterval: 10 * time.Millisecond, HeartbeatInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done; c.Close() })
	return &env{t: t, c: c, data: c.Cfg.DataDir, base: time.Now().UTC().Add(-time.Hour)}
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.c.Store.DB().Exec(q, args...); err != nil {
		e.t.Fatalf("%s : %v", q, err)
	}
}

func (e *env) next() string {
	e.seq++
	return store.FormatTime(e.base.Add(time.Duration(e.seq) * time.Second))
}

func (e *env) collection(name string, parent string) string {
	id := contracts.NewID("col")
	var p any
	if parent != "" {
		p = parent
	}
	e.exec(`INSERT INTO collections(id,name,parent_id,created_at) VALUES(?,?,?,?)`, id, name, p, e.next())
	return id
}

type fileSpec struct {
	Name    string
	Format  string
	Content []byte
	Windows bool
}

// blob range un contenu dans le magasin de blobs.
func (e *env) blob(b []byte) string {
	sha, _, err := e.c.Blobs.Put(bytes.NewReader(b), "")
	if err != nil {
		e.t.Fatal(err)
	}
	return sha
}

// model insère un modèle avec UNE version par fichier (la dernière est la plus récente). Renvoie model id et ids des versions.
func (e *env) model(collection, name, desc string, files ...fileSpec) (string, []string) {
	e.t.Helper()
	mid := contracts.NewID("mod")
	var coll any
	if collection != "" {
		coll = collection
	}
	e.exec(`INSERT INTO models(id,collection_id,name,tags,created_at,updated_at,description,license) VALUES(?,?,?,?,?,?,?,?)`,
		mid, coll, name, `["test"]`, e.next(), e.next(), desc, "")
	var vids []string
	for i, f := range files {
		sha := e.blob(f.Content)
		avail := "linux"
		if f.Windows {
			avail = "windows_only"
		}
		fid := contracts.NewID("fil")
		e.exec(`INSERT INTO source_files(id,model_id,name,format,size_bytes,sha256,availability,origin_path,created_at,rel_path,unit) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			fid, mid, f.Name, f.Format, len(f.Content), sha, avail, `C:\Users\bob\Documents\`+f.Name, e.next(), "", "mm")
		vid := contracts.NewID("ver")
		e.exec(`INSERT INTO model_versions(id,model_id,parent_id,label,note,source_file_ids,created_at) VALUES(?,?,?,?,?,?,?)`,
			vid, mid, nil, fmt.Sprintf("v%d", i+1), "", fmt.Sprintf(`["%s"]`, fid), e.next())
		vids = append(vids, vid)
	}
	return mid, vids
}

func stl(tag string, n int) []byte {
	var b strings.Builder
	b.WriteString("solid " + tag + "\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "facet normal 0 0 1\nouter loop\nvertex %d 0 0\nvertex 1 %d 0\nvertex 0 1 %d\nendloop\nendfacet\n", i, i, i)
	}
	b.WriteString("endsolid " + tag + "\n")
	return []byte(b.String())
}

// zip3mf fabrique un petit zip « 3MF » dont les entrées sont données.
func zip3mf(entries map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, c := range entries {
		w, _ := zw.Create(n)
		_, _ = w.Write([]byte(c))
	}
	_ = zw.Close()
	return buf.Bytes()
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// run lance un export et attend la fin du travail ; renvoie l'enregistrement final.
func (e *env) run(in export.Input) export.Record {
	e.t.Helper()
	ctx := context.Background()
	st, err := e.c.Export.Start(ctx, in)
	if err != nil {
		e.t.Fatalf("Start : %v", err)
	}
	return e.wait(st)
}

func (e *env) wait(st *export.Started) export.Record {
	e.t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		j, err := e.c.Queue.Get(ctx, st.JobID)
		if err != nil {
			e.t.Fatal(err)
		}
		if j.State.Terminal() {
			rec, err := e.c.Export.Get(ctx, st.ExportID)
			if err != nil {
				e.t.Fatal(err)
			}
			if j.State != contracts.JobSucceeded {
				e.t.Logf("travail %s : %+v", j.State, j.Error)
			}
			return rec
		}
		time.Sleep(15 * time.Millisecond)
	}
	e.t.Fatal("export non terminé")
	return export.Record{}
}

// open lit l'archive d'un export prêt : nom -> contenu.
func (e *env) open(rec export.Record) map[string][]byte {
	e.t.Helper()
	if rec.State != export.StateReady {
		e.t.Fatalf("export non prêt : %+v", rec)
	}
	f, _, err := e.c.Export.Open(context.Background(), rec.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	defer f.Close()
	st, _ := f.Stat()
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, zf := range zr.File {
		rc, err := zf.Open()
		if err != nil {
			e.t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			e.t.Fatal(err)
		}
		out[zf.Name] = b
	}
	return out
}

func (e *env) manifest(files map[string][]byte) map[string]any {
	e.t.Helper()
	var m map[string]any
	if err := json.Unmarshal(files["manifest.json"], &m); err != nil {
		e.t.Fatal(err)
	}
	return m
}

func names(files map[string][]byte) []string {
	var out []string
	for n := range files {
		out = append(out, n)
	}
	return out
}

func has(files map[string][]byte, prefix string) bool {
	for n := range files {
		if strings.HasPrefix(n, prefix) {
			return true
		}
	}
	return false
}

var _ = jobs.EnqueueParams{}

// modelMulti insère un modèle dont UNE version regroupe tous les fichiers (STL + 3MF par exemple).
func (e *env) modelMulti(collection, name string, files ...fileSpec) string {
	e.t.Helper()
	mid := contracts.NewID("mod")
	e.exec(`INSERT INTO models(id,collection_id,name,tags,created_at,updated_at,description,license) VALUES(?,?,?,?,?,?,?,?)`,
		mid, collection, name, `[]`, e.next(), e.next(), "", "")
	var fids []string
	for _, f := range files {
		sha := e.blob(f.Content)
		fid := contracts.NewID("fil")
		e.exec(`INSERT INTO source_files(id,model_id,name,format,size_bytes,sha256,availability,created_at,rel_path,unit) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			fid, mid, f.Name, f.Format, len(f.Content), sha, "linux", e.next(), "", "mm")
		fids = append(fids, `"`+fid+`"`)
	}
	e.exec(`INSERT INTO model_versions(id,model_id,parent_id,label,note,source_file_ids,created_at) VALUES(?,?,?,?,?,?,?)`,
		contracts.NewID("ver"), mid, nil, "v1", "", "["+strings.Join(fids, ",")+"]", e.next())
	return mid
}
