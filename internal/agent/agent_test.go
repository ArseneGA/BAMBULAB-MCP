package agent_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agent"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink/agentlinktest"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

var bg = context.Background()

func shaOf(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// stl produit un cube ASCII de côté s (12 triangles) ; l'échelle rend chaque fichier unique.
func stl(name string, s float64) []byte {
	v := [8][3]float64{{0, 0, 0}, {s, 0, 0}, {s, s, 0}, {0, s, 0}, {0, 0, s}, {s, 0, s}, {s, s, s}, {0, s, s}}
	f := [12][3]int{{0, 2, 1}, {0, 3, 2}, {4, 5, 6}, {4, 6, 7}, {0, 1, 5}, {0, 5, 4}, {1, 2, 6}, {1, 6, 5}, {2, 3, 7}, {2, 7, 6}, {3, 0, 4}, {3, 4, 7}}
	var b strings.Builder
	fmt.Fprintf(&b, "solid %s\n", name)
	for _, t := range f {
		b.WriteString("facet normal 0 0 0\n outer loop\n")
		for _, i := range t {
			fmt.Fprintf(&b, "  vertex %g %g %g\n", v[i][0], v[i][1], v[i][2])
		}
		b.WriteString(" endloop\nendfacet\n")
	}
	fmt.Fprintf(&b, "endsolid %s\n", name)
	return []byte(b.String())
}

// xorProt est un protecteur de test (réversible, NON sûr).
type xorProt struct{}

func (xorProt) Protect(p []byte, _ bool) ([]byte, error) {
	o := make([]byte, len(p))
	for i, c := range p {
		o[i] = c ^ 0x5a
	}
	return o, nil
}
func (x xorProt) Unprotect(b []byte, m bool) ([]byte, error) { return x.Protect(b, m) }

type fakeStudio struct {
	mu     sync.Mutex
	info   agent.StudioInfo
	opened []string
}

func (f *fakeStudio) Info(context.Context) agent.StudioInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.info
}
func (f *fakeStudio) Open(_ context.Context, file string) (agent.OpenResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = append(f.opened, file)
	return agent.OpenResult{File: file, Command: agent.OpenCommand("bambu-studio.exe", file), LaunchedPID: 4242, Instance: "new_process"}, nil
}

type fakeSlicing struct {
	started chan string   // reçoit l'ID de chaque slicing démarré
	gate    chan struct{} // si non nil, bloque jusqu'à fermeture ou annulation
	mu      sync.Mutex
	models  [][]byte
}

func (f *fakeSlicing) Available() (bool, string) { return true, "02.08.02.61" }
func (f *fakeSlicing) Slice(ctx context.Context, req slicer.Request) (*slicer.Result, error) {
	b, err := os.ReadFile(req.Model)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.models = append(f.models, b)
	f.mu.Unlock()
	if f.started != nil {
		f.started <- req.ID
	}
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	dir, _ := os.MkdirTemp("", "fakeslice-")
	out := []byte("PK-fake gcode.3mf pour " + req.Label)
	p := filepath.Join(dir, req.Label+".gcode.3mf")
	os.WriteFile(p, out, 0o644)
	return &slicer.Result{JobID: req.ID, WorkDir: dir, ArtifactPath: p, ArtifactSHA256: shaOf(out), ArtifactSize: int64(len(out)), BinaryVersion: "02.08.02.61"}, nil
}

type rig struct {
	realSlicing bool // vrai : le slicer local réel (Bambu Studio CLI) au lieu du faux
	t           *testing.T
	env         *agentlinktest.Env
	cfg         *agent.Config
	cl          *agentlink.Client
	pair        agentlink.PairResponse
	studio      *fakeStudio
	slice       *fakeSlicing
	folder      string
}

func newRig(t *testing.T, rc agentlink.Config) *rig {
	t.Helper()
	e := agentlinktest.New(t, rc)
	cl, pr := e.Pair("PC-Atelier")
	cfgDir := t.TempDir()
	cfg, _ := agent.LoadConfig(filepath.Join(cfgDir, "agent.json"))
	cfg.ServerURL, cfg.PollWaitSeconds = e.Server.URL, 1
	folder := t.TempDir()
	cfg.Folders = []agent.Folder{{Label: "Modeles", Path: folder}}
	return &rig{t: t, env: e, cfg: cfg, cl: cl, pair: pr, folder: folder,
		studio: &fakeStudio{info: agent.StudioInfo{Installed: true, Path: `C:\fake\bambu-studio.exe`, Version: "02.08.02.61"}},
		slice:  &fakeSlicing{}}
}

func (r *rig) start() (cancel context.CancelFunc, done chan struct{}) {
	r.t.Helper()
	var sl agent.Slicing = r.slice
	if r.realSlicing {
		sl = nil
	}
	a, err := agent.New(agent.Options{Config: r.cfg, Secret: r.pair.Secret, Studio: r.studio, Slicing: sl, Log: agentlinktest.Quiet(),
		Client: agentlink.NewClient(r.env.Server.URL, r.pair.Secret)})
	if err != nil {
		r.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(bg)
	done = make(chan struct{})
	go func() { defer close(done); _ = a.Run(ctx) }()
	r.t.Cleanup(func() { cancel(); <-done })
	// Attend que l'agent soit en ligne avec ses capacités.
	waitFor(r.t, 5*time.Second, "agent en ligne", func() bool {
		st := r.env.Reg.Status(bg)
		return len(st) == 1 && st[0].Online && len(st[0].Tasks) > 0
	})
	return cancel, done
}

func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("délai dépassé : %s", what)
}

func (r *rig) waitState(id string, states ...string) agentlink.TaskInfo {
	r.t.Helper()
	var last agentlink.TaskInfo
	waitFor(r.t, 15*time.Second, "travail "+id+" -> "+strings.Join(states, "|"), func() bool {
		last, _ = r.env.Reg.GetTask(bg, id)
		for _, s := range states {
			if last.State == s {
				return true
			}
		}
		return false
	})
	return last
}

func TestInventoryUploadImportEndToEnd(t *testing.T) {
	r := newRig(t, agentlink.Config{})
	files := map[string][]byte{
		"cube10.stl":           stl("cube10", 10),
		"sous/dossier/c20.stl": stl("c20", 20),
		"sous/c30.stl":         stl("c30", 30),
	}
	for rel, b := range files {
		p := filepath.Join(r.folder, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, b, 0o644)
	}
	os.WriteFile(filepath.Join(r.folder, "notes.txt"), []byte("ignoré"), 0o644)
	before := map[string]string{}
	snap := func(m map[string]string) {
		filepath.WalkDir(r.folder, func(p string, d os.DirEntry, _ error) error {
			if !d.IsDir() {
				b, _ := os.ReadFile(p)
				st, _ := os.Stat(p)
				m[p] = shaOf(b) + st.ModTime().String()
			}
			return nil
		})
	}
	snap(before)
	r.start()

	// 1. Inventaire lecture seule, confié par le serveur.
	ti, err := r.env.Reg.CreateTask(bg, agentlink.CreateTaskInput{Kind: agentlink.KindInventory, Params: json.RawMessage(`{"root_label":"Modeles"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if ti = r.waitState(ti.ID, agentlink.StateSucceeded, agentlink.StateFailed); ti.State != agentlink.StateSucceeded {
		t.Fatalf("inventaire : %+v", ti)
	}
	var rep agent.InventoryReport
	json.Unmarshal(ti.Result, &rep)
	if rep.Files != 3 || rep.ByFormat["stl"] != 3 {
		t.Fatalf("rapport d'inventaire : %+v", rep)
	}
	items, sum, err := r.env.Reg.ListInventory(bg, r.pair.AgentID, "Modeles", "", 0, 0)
	if err != nil || len(items) != 3 || sum.ByState["inventoried"] != 3 || sum.Bytes == 0 || sum.WindowsOnlyBytes != sum.Bytes {
		t.Fatalf("inventaire serveur : %+v %+v %v", items, sum, err)
	}
	// Le catalogue les connaît mais « windows_only » (rien n'est copié).
	res, _ := r.env.Core.Catalog.Search(bg, catalog.SearchQuery{Availability: "windows_only"})
	if res.Total != 3 {
		t.Fatalf("modèles windows_only : %d", res.Total)
	}
	if lin, _ := r.env.Core.Catalog.Search(bg, catalog.SearchQuery{Availability: "linux"}); lin.Total != 0 {
		t.Fatalf("rien ne doit être copié avant la demande : %d", lin.Total)
	}
	for _, it := range items {
		if r.env.Core.Blobs.Has(it.SHA256) {
			t.Fatal("un blob existe avant la copie contrôlée")
		}
	}

	// 2. Un fichier est modifié après l'inventaire : il ne doit JAMAIS être envoyé.
	changed := filepath.Join(r.folder, "sous", "c30.stl")
	os.WriteFile(changed, stl("c30", 31), 0o644)
	before2 := map[string]string{}
	snap(before2)

	// 3. Sélection puis envoi contrôlé.
	sel, err := r.env.Reg.SelectForUpload(bg, r.pair.AgentID, agentlink.SelectInput{RootLabel: "Modeles"})
	if err != nil || sel.Files != 3 || sel.Bytes != sum.Bytes {
		t.Fatalf("sélection : %+v %v", sel, err)
	}
	up := r.waitState(sel.Task.ID, agentlink.StateSucceeded, agentlink.StateFailed)
	if up.State != agentlink.StateSucceeded {
		t.Fatalf("envoi : %+v", up)
	}
	var urep agent.UploadReport
	json.Unmarshal(up.Result, &urep)
	if urep.Uploaded != 2 || len(urep.SkippedChanged) != 1 || urep.SkippedChanged[0] != "sous/c30.stl" {
		t.Fatalf("rapport d'envoi : %+v", urep)
	}
	r.env.Reg.WaitImports()

	// 4. Catalogue : 2 modèles passés à linux, vérifiés par empreinte, le modifié reste windows_only.
	items, sum, _ = r.env.Reg.ListInventory(bg, r.pair.AgentID, "Modeles", "", 0, 0)
	states := map[string]string{}
	for _, it := range items {
		states[it.RelPath] = it.State
		if it.State == "imported" {
			if !r.env.Core.Blobs.Has(it.SHA256) || r.env.Core.Blobs.Verify(it.SHA256) != nil {
				t.Fatalf("blob invalide pour %s", it.RelPath)
			}
		}
	}
	if states["cube10.stl"] != "imported" || states["sous/dossier/c20.stl"] != "imported" || states["sous/c30.stl"] == "imported" {
		t.Fatalf("états : %v", states)
	}
	if lin, _ := r.env.Core.Catalog.Search(bg, catalog.SearchQuery{Availability: "linux"}); lin.Total != 2 {
		t.Fatalf("modèles linux : %d", lin.Total)
	}
	if wo, _ := r.env.Core.Catalog.Search(bg, catalog.SearchQuery{Availability: "windows_only"}); wo.Total != 1 {
		t.Fatalf("modèles windows_only restants : %d", wo.Total)
	}
	// Pas de doublon : toujours 3 modèles, structure relative conservée en collections.
	if all, _ := r.env.Core.Catalog.Search(bg, catalog.SearchQuery{}); all.Total != 3 {
		t.Fatalf("modèles au catalogue : %d", all.Total)
	}
	cols, _ := r.env.Core.Catalog.ListCollections(bg)
	var names []string
	for _, c := range cols {
		names = append(names, c.Path)
	}
	if !strings.Contains(strings.Join(names, ","), "Modeles/sous/dossier") {
		t.Fatalf("collections : %v", names)
	}
	// La zone de réception est vidée.
	if ents, _ := os.ReadDir(filepath.Join(r.env.Core.Cfg.DataDir, "agent-inbox", r.pair.AgentID)); len(ents) != 0 {
		t.Fatalf("zone de réception non vidée : %v", ents)
	}

	// 5. Originaux strictement intacts (contenu et date) — hors le fichier modifié exprès par le test.
	after := map[string]string{}
	snap(after)
	for p, v := range before2 {
		if after[p] != v {
			t.Fatalf("original modifié : %s", p)
		}
	}
	if len(after) != len(before) {
		t.Fatal("des fichiers ont été ajoutés ou supprimés dans le dossier d'origine")
	}
	_ = before
}

func TestSliceReinforcement(t *testing.T) {
	r := newRig(t, agentlink.Config{})
	model := stl("piece", 25)
	sha, _, _ := r.env.Core.Blobs.Put(bytes.NewReader(model), "")
	r.start()
	params, _ := json.Marshal(map[string]any{"label": "piece", "printer": "Bambu Lab P1S", "nozzle": 0.4})
	ti, err := r.env.Reg.CreateTask(bg, agentlink.CreateTaskInput{Kind: agentlink.KindSlice, Params: params, Inputs: []agentlink.Artifact{{SHA256: sha, Name: "piece.stl"}}})
	if err != nil {
		t.Fatal(err)
	}
	ti = r.waitState(ti.ID, agentlink.StateSucceeded, agentlink.StateFailed)
	if ti.State != agentlink.StateSucceeded {
		t.Fatalf("slicing : %+v", ti)
	}
	if len(r.slice.models) != 1 || !bytes.Equal(r.slice.models[0], model) {
		t.Fatal("le slicer local n'a pas reçu le modèle vérifié")
	}
	want := []byte("PK-fake gcode.3mf pour piece")
	if len(ti.Outputs) != 1 || ti.Outputs[0].SHA256 != shaOf(want) || ti.Outputs[0].Name != "piece.gcode.3mf" {
		t.Fatalf("sorties : %+v", ti.Outputs)
	}
	// L'artefact est rapatrié dans le magasin du serveur, empreinte vérifiée.
	if err := r.env.Core.Blobs.Verify(shaOf(want)); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Executor string `json:"executor"`
		Slice    struct {
			BinaryVersion string `json:"binary_version"`
		} `json:"slice"`
	}
	json.Unmarshal(ti.Result, &out)
	if out.Executor != agentlink.ExecutorWindowsAgent || out.Slice.BinaryVersion != "02.08.02.61" {
		t.Fatalf("exécuteur et versions liés au résultat : %s", ti.Result)
	}
	// Le répertoire de travail du travail est nettoyé.
	if _, err := os.Stat(filepath.Join(filepath.Dir(r.cfg.Path()), "work", "tasks", ti.ID)); err == nil {
		t.Fatal("répertoire de travail conservé")
	}
}

func TestProjectStateAndOpenInStudio(t *testing.T) {
	r := newRig(t, agentlink.Config{})
	r.start()
	get := func() agent.ProjectStateReport {
		ti, err := r.env.Reg.CreateTask(bg, agentlink.CreateTaskInput{Kind: agentlink.KindGetProjectState})
		if err != nil {
			t.Fatal(err)
		}
		ti = r.waitState(ti.ID, agentlink.StateSucceeded, agentlink.StateFailed)
		if ti.State != agentlink.StateSucceeded {
			t.Fatalf("get_project_state : %+v", ti)
		}
		var rep agent.ProjectStateReport
		json.Unmarshal(ti.Result, &rep)
		return rep
	}
	// Studio non lancé : réponse honnête, pas d'invention.
	if rep := get(); rep.Project != nil || !strings.Contains(rep.Reason, "pas lancé") {
		t.Fatalf("Studio arrêté : %+v", rep)
	}
	// Studio lancé avec un projet identifiable.
	proj := filepath.Join(t.TempDir(), "mon_projet.3mf")
	os.WriteFile(proj, build3mf(t), 0o644)
	r.studio.mu.Lock()
	r.studio.info.Running = true
	r.studio.info.Project = &agent.OpenProject{Title: "mon_projet - Bambu Studio", Name: "mon_projet", Path: proj, Method: "window_title+recent_projects", Confidence: "high"}
	r.studio.mu.Unlock()
	rep := get()
	if rep.Project == nil || rep.Project.File != proj || len(rep.Project.Objects) != 2 || len(rep.Project.Plates) != 1 {
		t.Fatalf("projet : %+v", rep.Project)
	}
	// open_in_studio : l'artefact est transféré, vérifié, puis ouvert (fake Studio).
	data := build3mf(t)
	sha, _, _ := r.env.Core.Blobs.Put(bytes.NewReader(data), "")
	params, _ := json.Marshal(map[string]any{"sha256": sha, "file_name": "../../évil nom.3mf"})
	ti, err := r.env.Reg.CreateTask(bg, agentlink.CreateTaskInput{Kind: agentlink.KindOpenInStudio, Params: params, Inputs: []agentlink.Artifact{{SHA256: sha, Name: "x.3mf"}}})
	if err != nil {
		t.Fatal(err)
	}
	if ti = r.waitState(ti.ID, agentlink.StateSucceeded, agentlink.StateFailed); ti.State != agentlink.StateSucceeded {
		t.Fatalf("open_in_studio : %+v", ti)
	}
	if len(r.studio.opened) != 1 {
		t.Fatal("Studio n'a pas été sollicité")
	}
	opened := r.studio.opened[0]
	if !strings.HasPrefix(opened, filepath.Join(filepath.Dir(r.cfg.Path()), "work", "open")) || filepath.Base(opened) != "évil nom.3mf" {
		t.Fatalf("fichier ouvert hors du dossier de travail : %s", opened)
	}
	if got, _ := os.ReadFile(opened); !bytes.Equal(got, data) {
		t.Fatal("contenu ouvert différent")
	}
	// modify_open_project est refusé (serveur) et, si l'agent le reçoit quand même, échoue proprement.
	if _, err := r.env.Reg.CreateTask(bg, agentlink.CreateTaskInput{Kind: agentlink.KindModifyOpenProject}); err == nil {
		t.Fatal("modify_open_project accepté")
	}
}

func TestAgentLostDuringTaskThenRestartReconciles(t *testing.T) {
	r := newRig(t, agentlink.Config{OfflineAfter: 30 * time.Second})
	r.slice.started = make(chan string, 4)
	r.slice.gate = make(chan struct{}) // le slicing ne finit jamais seul
	sha, _, _ := r.env.Core.Blobs.Put(bytes.NewReader(stl("p", 5)), "")
	cancel, done := r.start()

	ti, _ := r.env.Reg.CreateTask(bg, agentlink.CreateTaskInput{Kind: agentlink.KindSlice, Inputs: []agentlink.Artifact{{SHA256: sha, Name: "p.stl"}}})
	select {
	case <-r.slice.started:
	case <-time.After(10 * time.Second):
		t.Fatal("slicing non démarré")
	}
	if cur := r.waitState(ti.ID, agentlink.StateRunning); cur.State != agentlink.StateRunning {
		t.Fatal(cur.State)
	}
	// L'agent disparaît (crash simulé : arrêt sans rapport).
	cancel()
	<-done
	r.env.Clock.Advance(31 * time.Second)
	cur, _ := r.env.Reg.GetTask(bg, ti.ID)
	if cur.State != agentlink.StateUnknown {
		t.Fatalf("après perte de l'agent : %s", cur.State)
	}
	if st := r.env.Reg.Status(bg); st[0].Online {
		t.Fatal("agent annoncé en ligne")
	}
	// Rien n'est relancé tout seul ailleurs.
	if all, _ := r.env.Reg.ListTasks(bg, "", "", 0); len(all) != 1 {
		t.Fatalf("travaux : %d", len(all))
	}

	// Retour de l'agent (nouveau processus, même dossier d'état) : le journal montre un travail
	// interrompu, rapporté « failed/agent_restarted » -> réconciliation, sans relance ni doublon.
	r.slice.gate = nil
	r.start()
	cur = r.waitState(ti.ID, agentlink.StateFailed)
	if cur.Error == nil || cur.Error.Code != "agent_restarted" || !strings.Contains(cur.Note, "réconcilié") {
		t.Fatalf("après retour : %+v", cur)
	}
	if st := r.env.Reg.Status(bg); !st[0].Online {
		t.Fatal("agent de retour non en ligne")
	}
	if len(r.env.Reg.Status(bg)) != 1 {
		t.Fatal("un second agent est apparu")
	}
	// Le serveur a la main : recréation explicite possible (non physique), qui s'exécute normalement.
	nt, err := r.env.Reg.Requeue(bg, ti.ID)
	if err != nil {
		t.Fatal(err)
	}
	if nt = r.waitState(nt.ID, agentlink.StateSucceeded, agentlink.StateFailed); nt.State != agentlink.StateSucceeded || nt.Replaces != ti.ID {
		t.Fatalf("recréation : %+v", nt)
	}
}

func TestServerCancelsRunningTask(t *testing.T) {
	r := newRig(t, agentlink.Config{HeartbeatSeconds: 1})
	r.slice.started = make(chan string, 1)
	r.slice.gate = make(chan struct{})
	sha, _, _ := r.env.Core.Blobs.Put(bytes.NewReader(stl("p", 5)), "")
	r.start()
	ti, _ := r.env.Reg.CreateTask(bg, agentlink.CreateTaskInput{Kind: agentlink.KindSlice, Inputs: []agentlink.Artifact{{SHA256: sha, Name: "p.stl"}}})
	<-r.slice.started
	r.waitState(ti.ID, agentlink.StateRunning)
	if c, err := r.env.Reg.CancelTask(bg, ti.ID); err != nil || !c.CancelRequested {
		t.Fatalf("annulation : %+v %v", c, err)
	}
	if cur := r.waitState(ti.ID, agentlink.StateCancelled); cur.State != agentlink.StateCancelled {
		t.Fatal(cur.State)
	}
}

func TestCapabilitiesAnnounced(t *testing.T) {
	r := newRig(t, agentlink.Config{})
	r.start()
	st := r.env.Reg.Status(bg)[0]
	if !st.Online || !st.StudioInstalled || st.StudioVersion != "02.08.02.61" || !st.LocalSlicer {
		t.Fatalf("statut : %+v", st)
	}
	if len(st.SharedFolders) != 1 || st.SharedFolders[0] != "Modeles" {
		t.Fatalf("dossiers annoncés : %v", st.SharedFolders)
	}
	got := strings.Join(st.Tasks, ",")
	for _, k := range []string{"inventory", "upload_files", "slice", "get_project_state", "open_in_studio"} {
		if !strings.Contains(got, k) {
			t.Fatalf("capacité %s absente : %s", k, got)
		}
	}
	if strings.Contains(got, "modify_open_project") || strings.Contains(got, "start_print") {
		t.Fatalf("capacité annoncée à tort : %s", got)
	}
}

// Slicing RÉEL par l'agent (Bambu Studio CLI local) : modèle téléchargé et vérifié, slicing,
// artefact rapatrié sur le serveur et validé (archive Studio). Ignoré si Studio est absent.
func TestSliceReinforcementReal(t *testing.T) {
	if _, err := slicer.Locate(""); err != nil {
		t.Skip("Bambu Studio absent : " + err.Error())
	}
	r := newRig(t, agentlink.Config{})
	r.realSlicing = true
	model := stl("cube20", 20)
	sha, _, _ := r.env.Core.Blobs.Put(bytes.NewReader(model), "")
	r.start()
	params, _ := json.Marshal(map[string]any{"label": "cube20", "printer": "Bambu Lab P1S", "nozzle": 0.4})
	ti, err := r.env.Reg.CreateTask(bg, agentlink.CreateTaskInput{Kind: agentlink.KindSlice, Params: params, Inputs: []agentlink.Artifact{{SHA256: sha, Name: "cube20.stl"}}})
	if err != nil {
		t.Fatal(err)
	}
	ti = r.waitState(ti.ID, agentlink.StateSucceeded, agentlink.StateFailed)
	if ti.State != agentlink.StateSucceeded {
		t.Fatalf("slicing réel : %+v %s", ti.Error, ti.Result)
	}
	if len(ti.Outputs) != 1 || !strings.HasSuffix(ti.Outputs[0].Name, ".gcode.3mf") || ti.Outputs[0].Size < 10_000 {
		t.Fatalf("sorties : %+v", ti.Outputs)
	}
	if err := r.env.Core.Blobs.Verify(ti.Outputs[0].SHA256); err != nil {
		t.Fatal(err)
	}
	p, err := r.env.Core.Blobs.Path(ti.Outputs[0].SHA256)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	found := false
	for _, f := range zr.File {
		found = found || f.Name == "Metadata/plate_1.gcode"
	}
	if !found {
		t.Fatal("l'archive rapatriée ne contient pas plate_1.gcode")
	}
	var out struct {
		Slice struct {
			BinaryVersion string `json:"binary_version"`
			Validation    struct {
				OK bool `json:"ok"`
			} `json:"validation"`
		} `json:"slice"`
	}
	json.Unmarshal(ti.Result, &out)
	if out.Slice.BinaryVersion == "" || !out.Slice.Validation.OK {
		t.Fatalf("validation et version liées au résultat : %s", ti.Result)
	}
}

// build3mf fabrique un petit projet Bambu Studio synthétique (même structure que Studio).
func build3mf(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	add := func(name, body string) {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	add("3D/3dmodel.model", `<?xml version="1.0"?><model><metadata name="Application">BambuStudio-02.08.02.61</metadata></model>`)
	add("Metadata/model_settings.config", `<?xml version="1.0" encoding="UTF-8"?>
<config>
  <object id="2"><metadata key="name" value="Corps.stl"/><metadata key="extruder" value="1"/><metadata face_count="120"/>
    <part id="1" subtype="normal_part"><metadata key="name" value="Corps.stl"/></part></object>
  <object id="4"><metadata key="name" value="Couvercle.stl"/><metadata key="extruder" value="2"/><metadata face_count="80"/>
    <part id="3" subtype="normal_part"><metadata key="name" value="Couvercle.stl"/></part></object>
  <plate><metadata key="plater_id" value="1"/><metadata key="plater_name" value="Plateau A"/><metadata key="locked" value="false"/>
    <model_instance><metadata key="object_id" value="2"/><metadata key="instance_id" value="0"/></model_instance>
    <model_instance><metadata key="object_id" value="4"/><metadata key="instance_id" value="0"/></model_instance></plate>
</config>`)
	add("Metadata/project_settings.config", `{"printer_settings_id":"Bambu Lab P1S 0.4 nozzle","print_settings_id":"0.20mm Standard @BBL X1C","layer_height":"0.2","sparse_infill_density":"15%","enable_support":"0","support_type":"tree(auto)","filament_settings_id":["Bambu PLA Basic @BBL P1S 0.4 nozzle","Bambu PLA Basic @BBL P1S 0.4 nozzle"],"autre":"x"}`)
	add("Metadata/slice_info.config", `<config><header><header_item key="X-BBL-Client-Version" value="02.08.02.61"/></header></config>`)
	zw.Close()
	return b.Bytes()
}
