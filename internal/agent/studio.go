package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// StudioProcess est un processus bambu-studio.exe.
type StudioProcess struct {
	PID int    `json:"pid"`
	Exe string `json:"exe,omitempty"`
}

// StudioWindow est une fenêtre de premier niveau d'un processus Studio.
type StudioWindow struct {
	PID     int    `json:"pid"`
	Title   string `json:"title"`
	Visible bool   `json:"visible"`
}

// OpenProject est le projet que l'agent pense ouvert dans Studio, avec sa méthode.
type OpenProject struct {
	Title      string `json:"window_title"`
	Name       string `json:"name,omitempty"`
	Unsaved    bool   `json:"unsaved_changes,omitempty"`
	Path       string `json:"path,omitempty"`
	Method     string `json:"method"`
	Confidence string `json:"confidence"` // high | medium | none
	Note       string `json:"note,omitempty"`
}

// StudioInfo est l'état de Studio sur le poste.
type StudioInfo struct {
	Installed bool            `json:"installed"`
	Path      string          `json:"path,omitempty"`
	Version   string          `json:"version,omitempty"`
	Running   bool            `json:"running"`
	Processes []StudioProcess `json:"processes,omitempty"`
	Windows   []StudioWindow  `json:"windows,omitempty"`
	Project   *OpenProject    `json:"open_project,omitempty"`
}

// OpenResult est l'issue d'une ouverture de fichier dans Studio.
type OpenResult struct {
	File          string   `json:"file"`
	Command       []string `json:"command"`
	LaunchedPID   int      `json:"launched_pid"`
	RunningBefore []int    `json:"running_before,omitempty"`
	RunningAfter  []int    `json:"running_after,omitempty"`
	// Instance : new_process | forwarded_or_exited (le processus lancé n'existe plus : Studio a repris
	// le fichier dans une instance existante, ou a quitté) | unknown.
	Instance string `json:"instance"`
	Note     string `json:"note,omitempty"`
}

// Studio abstrait la session Bambu Studio (remplaçable dans les tests).
type Studio interface {
	Info(ctx context.Context) StudioInfo
	Open(ctx context.Context, file string) (OpenResult, error)
}

// OpenCommand construit la ligne de commande d'ouverture : bambu-studio.exe <fichier>.
// Tableau d'arguments, jamais de shell.
func OpenCommand(exe, file string) []string { return []string{exe, file} }

// openable : extensions que Studio ouvre en projet/modèle.
var openable = map[string]bool{".3mf": true, ".stl": true, ".obj": true, ".step": true, ".stp": true}

// SafeOpenName assainit un nom de fichier à ouvrir (nom seul, extension connue).
func SafeOpenName(name string) (string, error) {
	n := filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	n = regexp.MustCompile(`[^\p{L}\p{N}._ -]+`).ReplaceAllString(n, "_")
	n = strings.Trim(n, ". ")
	if !openable[strings.ToLower(filepath.Ext(n))] {
		return "", fmt.Errorf("extension non ouvrable dans Studio : %q (attendu : .3mf .stl .obj .step)", name)
	}
	if len(n) > 120 {
		return "", errors.New("nom de fichier trop long")
	}
	return n, nil
}

// ParseWindowTitle extrait le nom de projet d'un titre de fenêtre de Studio.
// Le format exact est mesuré dans docs/jalons/J8.md ; l'analyse est tolérante :
// retire « Bambu Studio », les séparateurs et le marqueur « * » de modification.
func ParseWindowTitle(title string) (name string, unsaved bool) {
	t := strings.TrimSpace(title)
	for _, sep := range []string{" - ", " – ", " — "} {
		parts := strings.Split(t, sep)
		if len(parts) > 1 {
			var keep []string
			for _, p := range parts {
				if !strings.EqualFold(strings.TrimSpace(p), "Bambu Studio") {
					keep = append(keep, p)
				}
			}
			t = strings.TrimSpace(strings.Join(keep, sep))
			break
		}
	}
	if strings.EqualFold(t, "Bambu Studio") {
		return "", false
	}
	if strings.HasPrefix(t, "*") || strings.HasSuffix(t, "*") {
		unsaved = true
		t = strings.Trim(t, "* ")
	}
	return t, unsaved
}

var recentRe = regexp.MustCompile(`"recent_projects"\s*:\s*(\{[^}]*\})`)

// RecentProjects lit la liste des projets récents de Studio (BambuStudio.conf,
// lecture seule), du plus récent au plus ancien.
func RecentProjects(confPath string) []string {
	b, err := os.ReadFile(confPath)
	if err != nil {
		return nil
	}
	m := recentRe.FindSubmatch(b)
	if m == nil {
		return nil
	}
	var kv map[string]string
	if json.Unmarshal(m[1], &kv) != nil {
		return nil
	}
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, kv[k])
	}
	return out
}

// ResolveOpenProject associe le titre d'une fenêtre à un fichier : d'abord la
// liste des projets récents de Studio (nom identique), puis, à défaut, rien.
// Un nom sans fichier correspondant est un projet « sans nom » ou jamais enregistré.
func ResolveOpenProject(title string, recent []string) *OpenProject {
	name, unsaved := ParseWindowTitle(title)
	op := &OpenProject{Title: title, Name: name, Unsaved: unsaved, Method: "window_title", Confidence: "none"}
	if name == "" {
		op.Note = "la fenêtre de Studio ne porte pas de nom de projet (projet neuf non enregistré, ou titre non reconnu)"
		return op
	}
	base := strings.TrimSuffix(strings.ToLower(name), ".3mf")
	for _, p := range recent {
		if strings.TrimSuffix(strings.ToLower(filepath.Base(p)), ".3mf") == base {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				op.Path, op.Method, op.Confidence = p, "window_title+recent_projects", "high"
				if unsaved {
					op.Note = "des modifications non enregistrées existent dans Studio : le fichier lu est l'état sauvegardé"
				}
				return op
			}
		}
	}
	op.Note = "aucun fichier .3mf récent de ce nom (projet jamais enregistré, ou absent des projets récents)"
	return op
}

// SystemStudio est l'implémentation réelle (processus et fenêtres : studio_windows.go).
type SystemStudio struct {
	ConfiguredPath string
	// ConfPath : BambuStudio.conf (défaut %APPDATA%\BambuStudio\BambuStudio.conf).
	ConfPath string
}

func (s *SystemStudio) confPath() string {
	if s.ConfPath != "" {
		return s.ConfPath
	}
	if d := os.Getenv("APPDATA"); d != "" {
		return filepath.Join(d, "BambuStudio", "BambuStudio.conf")
	}
	return ""
}

// Info détecte l'installation, les processus et le projet ouvert.
func (s *SystemStudio) Info(ctx context.Context) StudioInfo {
	var info StudioInfo
	if b, err := slicer.Locate(s.ConfiguredPath); err == nil {
		info.Installed, info.Path, info.Version = true, b.Path, b.Version
	}
	info.Processes = studioProcesses()
	info.Running = len(info.Processes) > 0
	if !info.Running {
		return info
	}
	pids := map[int]bool{}
	for _, p := range info.Processes {
		pids[p.PID] = true
	}
	for _, w := range topWindows() {
		if pids[w.PID] && w.Title != "" {
			info.Windows = append(info.Windows, w)
		}
	}
	// Fenêtre principale : visible, titre non vide, la plus « riche ».
	var best *StudioWindow
	for i := range info.Windows {
		w := &info.Windows[i]
		if !w.Visible || !strings.Contains(strings.ToLower(w.Title), "bambu studio") && len(info.Windows) > 1 {
			continue
		}
		if best == nil || len(w.Title) > len(best.Title) {
			best = w
		}
	}
	if best != nil {
		info.Project = ResolveOpenProject(best.Title, RecentProjects(s.confPath()))
	}
	return info
}

// Open lance « bambu-studio.exe <fichier> » sans attendre, puis observe si le
// processus lancé reste (nouvelle instance) ou disparaît (fichier repris par une
// instance existante). N'ouvre jamais d'impression : ouvrir n'est pas imprimer.
func (s *SystemStudio) Open(ctx context.Context, file string) (OpenResult, error) {
	res := OpenResult{File: file}
	b, err := slicer.Locate(s.ConfiguredPath)
	if err != nil {
		return res, fmt.Errorf("Bambu Studio introuvable : %w", err)
	}
	if st, err := os.Stat(file); err != nil || st.IsDir() {
		return res, fmt.Errorf("fichier à ouvrir introuvable : %s", file)
	}
	for _, p := range studioProcesses() {
		res.RunningBefore = append(res.RunningBefore, p.PID)
	}
	res.Command = OpenCommand(b.Path, file)
	cmd := exec.Command(res.Command[0], res.Command[1:]...)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return res, err
	}
	res.LaunchedPID = cmd.Process.Pid
	go func() { _ = cmd.Wait() }() // récolte le processus sans bloquer
	time.Sleep(3 * time.Second)
	alive := false
	for _, p := range studioProcesses() {
		res.RunningAfter = append(res.RunningAfter, p.PID)
		if p.PID == res.LaunchedPID {
			alive = true
		}
	}
	switch {
	case alive && len(res.RunningBefore) == 0:
		res.Instance = "new_process"
	case alive:
		res.Instance = "new_process"
		res.Note = "une instance de Studio tournait déjà ; le processus lancé est resté : seconde instance"
	case len(res.RunningBefore) > 0:
		res.Instance = "forwarded_or_exited"
		res.Note = "le processus lancé s'est terminé alors qu'une instance existait : le fichier a probablement été transmis à l'instance existante"
	default:
		res.Instance = "unknown"
		res.Note = "le processus lancé n'est plus visible (Studio relance parfois un processus fils)"
	}
	return res, nil
}
