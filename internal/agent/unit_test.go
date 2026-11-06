package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agent"
)

func TestConfigNeverStoresSecretInClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.json")
	cfg, _ := agent.LoadConfig(path)
	cfg.ServerURL = "http://x:7878"
	secret := "agt_1234.abcdefSECRETsecret0123456789"
	if err := cfg.SetSecret(xorProt{}, "agt_1234", secret); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "SECRET") || strings.Contains(string(raw), "abcdef") {
		t.Fatalf("secret en clair dans le fichier : %s", raw)
	}
	back, _ := agent.LoadConfig(path)
	got, err := back.Secret(xorProt{})
	if err != nil || got != secret {
		t.Fatalf("relecture : %q %v", got, err)
	}
	empty, _ := agent.LoadConfig(filepath.Join(t.TempDir(), "absent.json"))
	if _, err := empty.Secret(xorProt{}); err == nil {
		t.Fatal("un agent non appairé n'a pas de secret")
	}
}

func TestConfigValidateFolders(t *testing.T) {
	d := t.TempDir()
	ok := &agent.Config{Folders: []agent.Folder{{Label: "Modèles 3D", Path: d}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]*agent.Config{
		"étiquette dangereuse": {Folders: []agent.Folder{{Label: "../x", Path: d}}},
		"chemin relatif":       {Folders: []agent.Folder{{Label: "A", Path: "relatif"}}},
		"dossier absent":       {Folders: []agent.Folder{{Label: "A", Path: filepath.Join(d, "nope")}}},
		"doublon":              {Folders: []agent.Folder{{Label: "A", Path: d}, {Label: "a", Path: d}}},
	} {
		if err := c.Validate(); err == nil {
			t.Errorf("%s : accepté à tort", name)
		}
	}
}

func TestOpenCommandAndNames(t *testing.T) {
	cmd := agent.OpenCommand(`C:\Program Files\Bambu Studio\bambu-studio.exe`, `C:\Users\me\mon projet.3mf`)
	if len(cmd) != 2 || cmd[0] != `C:\Program Files\Bambu Studio\bambu-studio.exe` || cmd[1] != `C:\Users\me\mon projet.3mf` {
		t.Fatalf("commande : %q", cmd) // tableau d'arguments : aucun shell, aucune concaténation
	}
	for in, want := range map[string]string{"cube.3mf": "cube.3mf", "../../évil nom.3mf": "évil nom.3mf", `C:\x\y\a b.STL`: "a b.STL"} {
		got, err := agent.SafeOpenName(in)
		if err != nil || got != want {
			t.Errorf("SafeOpenName(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"virus.exe", "a.bat", "", "x.3mf.exe", ".."} {
		if _, err := agent.SafeOpenName(bad); err == nil {
			t.Errorf("nom accepté à tort : %q", bad)
		}
	}
}

func TestWindowTitleAndRecentProjects(t *testing.T) {
	for title, want := range map[string]struct {
		name    string
		unsaved bool
	}{
		"Bambu Studio":                   {"", false},
		"BambuStudio":                    {"", false},
		"cube20.gcode - BambuStudio":     {"cube20.gcode", false},
		"*cube20 - BambuStudio":          {"cube20", true},
		"mon_projet - Bambu Studio":      {"mon_projet", false},
		"*mon_projet - Bambu Studio":     {"mon_projet", true},
		"Bambu Studio - mon_projet.3mf":  {"mon_projet.3mf", false},
		"mon_projet.3mf* - Bambu Studio": {"mon_projet.3mf", true},
	} {
		n, u := agent.ParseWindowTitle(title)
		if n != want.name || u != want.unsaved {
			t.Errorf("ParseWindowTitle(%q) = %q,%v ; attendu %+v", title, n, u, want)
		}
	}
	dir := t.TempDir()
	proj := filepath.Join(dir, "mon_projet.3mf")
	os.WriteFile(proj, []byte("x"), 0o644)
	conf := filepath.Join(dir, "BambuStudio.conf")
	js := strings.ReplaceAll(`{"app":{"x":1},"recent":{"a":"b"},"recent_projects": {"001": "PROJ","002": "C:\\autre\\ancien.3mf"},"y":2}`, "PROJ", strings.ReplaceAll(proj, `\`, `\\`))
	os.WriteFile(conf, []byte(js), 0o644)
	rec := agent.RecentProjects(conf)
	if len(rec) != 2 || rec[0] != proj {
		t.Fatalf("projets récents : %v", rec)
	}
	op := agent.ResolveOpenProject("*mon_projet - Bambu Studio", rec)
	if op.Path != proj || op.Confidence != "high" || !op.Unsaved || !strings.Contains(op.Note, "non enregistrées") {
		t.Fatalf("résolution : %+v", op)
	}
	if op := agent.ResolveOpenProject("inconnu - Bambu Studio", rec); op.Path != "" || op.Confidence != "none" {
		t.Fatalf("projet inconnu : %+v", op)
	}
	if op := agent.ResolveOpenProject("Bambu Studio", rec); op.Path != "" || op.Name != "" {
		t.Fatalf("sans projet : %+v", op)
	}
	if agent.RecentProjects(filepath.Join(dir, "absent.conf")) != nil {
		t.Fatal("conf absente")
	}
}

func TestReadProject(t *testing.T) {
	p := filepath.Join(t.TempDir(), "p.3mf")
	os.WriteFile(p, build3mf(t), 0o644)
	ps, err := agent.ReadProject(p)
	if err != nil {
		t.Fatal(err)
	}
	if ps.Application != "BambuStudio-02.08.02.61" || ps.ClientVer != "02.08.02.61" || len(ps.SHA256) != 64 {
		t.Fatalf("entête : %+v", ps)
	}
	if len(ps.Objects) != 2 || ps.Objects[0].Name != "Corps.stl" || ps.Objects[0].FaceCount != 120 || ps.Objects[1].Extruder != "2" {
		t.Fatalf("objets : %+v", ps.Objects)
	}
	if len(ps.Plates) != 1 || ps.Plates[0].Name != "Plateau A" || strings.Join(ps.Plates[0].Objects, ",") != "Corps.stl,Couvercle.stl" {
		t.Fatalf("plateaux : %+v", ps.Plates)
	}
	if ps.Settings["layer_height"] != "0.2" || ps.Settings["print_settings_id"] != "0.20mm Standard @BBL X1C" || ps.Settings["support_type"] != "tree(auto)" {
		t.Fatalf("paramètres : %+v", ps.Settings)
	}
	if _, leaked := ps.Settings["autre"]; leaked {
		t.Fatal("seuls les paramètres connus sont exposés")
	}
	if len(ps.Limits) < 2 {
		t.Fatal("les limites doivent être annoncées")
	}
	os.WriteFile(p, []byte("pas un zip"), 0o644)
	if _, err := agent.ReadProject(p); err == nil {
		t.Fatal("archive invalide acceptée")
	}
}

// Lecture d'un vrai projet Studio : AGENT_REAL_3MF=chemin (jamais modifié, lu en lecture seule).
func TestReadRealProject(t *testing.T) {
	p := os.Getenv("AGENT_REAL_3MF")
	if p == "" {
		t.Skip("AGENT_REAL_3MF non défini")
	}
	ps, err := agent.ReadProject(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps.Objects) == 0 || len(ps.Plates) == 0 || ps.Settings["printer_settings_id"] == nil {
		t.Fatalf("projet réel : %+v", ps)
	}
	t.Logf("objets=%d plateaux=%d imprimante=%v procédé=%v", len(ps.Objects), len(ps.Plates), ps.Settings["printer_settings_id"], ps.Settings["print_settings_id"])
}

// Détection réelle de Studio (lecture seule : processus, version). Ne lance rien.
func TestSystemStudioDetection(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows seulement")
	}
	st := &agent.SystemStudio{}
	info := st.Info(context.Background())
	if !info.Installed {
		t.Skip("Bambu Studio absent de ce poste")
	}
	if !regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`).MatchString(info.Version) || !strings.HasSuffix(strings.ToLower(info.Path), "bambu-studio.exe") {
		t.Fatalf("installation : %+v", info)
	}
	if info.Running != (len(info.Processes) > 0) {
		t.Fatalf("incohérence : %+v", info)
	}
}
