package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

var invExt = map[string]bool{".stl": true, ".obj": true, ".3mf": true, ".step": true, ".stp": true}

const (
	maxInventoryFiles = 100_000
	maxFileBytes      = 2 << 30
)

// resolveInFolder renvoie le chemin absolu d'un fichier relatif d'un dossier
// autorisé, après résolution des liens symboliques : jamais en dehors du dossier.
func resolveInFolder(f Folder, rel string) (string, error) {
	rel, err := agentlink.CleanRel(rel)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(f.Path)
	if err != nil {
		return "", err
	}
	abs, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	r, err := filepath.Rel(root, abs)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(r) {
		return "", fmt.Errorf("%q sort du dossier autorisé %q", rel, f.Label)
	}
	return abs, nil
}

// ---- inventaire (lecture seule) ----

type inventoryParams struct {
	RootLabel string `json:"root_label"`
}

// InventoryReport est le résultat d'un inventaire.
type InventoryReport struct {
	RootLabel string         `json:"root_label"`
	Files     int            `json:"files"`
	Bytes     int64          `json:"bytes"`
	Skipped   int            `json:"skipped"`
	ByStatus  map[string]int `json:"by_status"`
	ByFormat  map[string]int `json:"by_format"`
}

func (a *Agent) doInventory(env *taskEnv) (any, error) {
	var p inventoryParams
	_ = json.Unmarshal(env.task.Params, &p)
	f, ok := a.cfg.Folder(p.RootLabel)
	if !ok {
		return nil, terr("folder_not_allowed", "le dossier %q n'est pas dans la liste des dossiers autorisés de l'agent", p.RootLabel)
	}
	return a.Inventory(env.ctx, f, env.progress)
}

// Inventory parcourt un dossier autorisé en LECTURE SEULE (aucune écriture, aucun
// renommage, aucune suppression), calcule les empreintes et les envoie au serveur.
func (a *Agent) Inventory(ctx context.Context, f Folder, progress func(float64, string)) (*InventoryReport, error) {
	root, err := filepath.EvalSymlinks(f.Path)
	if err != nil {
		return nil, terr("folder_unreadable", "%v", err)
	}
	rep := &InventoryReport{RootLabel: f.Label, ByStatus: map[string]int{}, ByFormat: map[string]int{}}
	var batch []agentlink.InventoryEntry
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		resp, err := a.cl.Inventory(ctx, agentlink.InventoryRequest{RootLabel: f.Label, Entries: batch})
		if err != nil {
			return err
		}
		for k, v := range resp.ByStatus {
			rep.ByStatus[k] += v
		}
		batch = batch[:0]
		return nil
	}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if werr != nil {
			rep.Skipped++
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() { // dossiers, liens symboliques, périphériques : ignorés (un lien ne sort jamais du dossier)
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if !invExt[ext] {
			return nil
		}
		if rep.Files >= maxInventoryFiles {
			rep.Skipped++
			return nil
		}
		st1, err := d.Info()
		if err != nil || st1.Size() > maxFileBytes {
			rep.Skipped++
			return nil
		}
		sha, n, err := agentlink.SHA256File(p)
		if err != nil {
			rep.Skipped++
			return nil
		}
		if st2, err := os.Stat(p); err != nil || st2.Size() != n || !st2.ModTime().Equal(st1.ModTime()) {
			rep.Skipped++ // modifié pendant la lecture : sera repris au prochain inventaire
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		batch = append(batch, agentlink.InventoryEntry{RelPath: filepath.ToSlash(rel), Size: n, MTimeNS: st1.ModTime().UnixNano(), SHA256: sha, OriginPath: p})
		rep.Files++
		rep.Bytes += n
		rep.ByFormat[strings.TrimPrefix(ext, ".")]++
		if progress != nil {
			progress(0.5, fmt.Sprintf("%d fichier(s) inventoriés", rep.Files))
		}
		if len(batch) >= 100 {
			return flush()
		}
		return nil
	})
	if err != nil {
		return rep, err
	}
	if err := flush(); err != nil {
		return rep, err
	}
	return rep, nil
}

// ---- envoi contrôlé ----

type uploadParams struct {
	RootLabel string `json:"root_label"`
	Entries   []struct {
		RelPath string `json:"rel_path"`
		SHA256  string `json:"sha256"`
		Size    int64  `json:"size_bytes"`
	} `json:"entries"`
}

// UploadReport est le résultat d'un envoi de fichiers.
type UploadReport struct {
	Uploaded       int      `json:"uploaded"`
	Bytes          int64    `json:"bytes"`
	SkippedChanged []string `json:"skipped_changed,omitempty"`
	Failed         []string `json:"failed,omitempty"`
}

func (a *Agent) doUploadFiles(env *taskEnv) (any, error) {
	var p uploadParams
	if err := json.Unmarshal(env.task.Params, &p); err != nil {
		return nil, terr("bad_params", "%v", err)
	}
	f, ok := a.cfg.Folder(p.RootLabel)
	if !ok {
		return nil, terr("folder_not_allowed", "le dossier %q n'est pas dans la liste des dossiers autorisés de l'agent", p.RootLabel)
	}
	rep := &UploadReport{}
	done := map[string]bool{}
	for i, e := range p.Entries {
		if err := env.ctx.Err(); err != nil {
			return rep, err
		}
		abs, err := resolveInFolder(f, e.RelPath)
		if err != nil {
			rep.Failed = append(rep.Failed, e.RelPath+" : "+err.Error())
			continue
		}
		sha, n, err := agentlink.SHA256File(abs)
		if err != nil {
			rep.Failed = append(rep.Failed, e.RelPath+" : "+err.Error())
			continue
		}
		if sha != e.SHA256 || n != e.Size {
			rep.SkippedChanged = append(rep.SkippedChanged, e.RelPath) // modifié depuis l'inventaire : jamais envoyé
			continue
		}
		if !done[sha] { // contenu identique dans plusieurs chemins : un seul transfert
			if err := a.cl.Upload(env.ctx, env.task.ID, abs, sha, n, agentlink.TargetInbox, "", func(sent, total int64) {
				env.progress((float64(i)+float64(sent)/float64(max(total, 1)))/float64(len(p.Entries)), fmt.Sprintf("envoi de %s", filepath.Base(abs)))
			}); err != nil {
				if env.ctx.Err() != nil {
					return rep, env.ctx.Err()
				}
				rep.Failed = append(rep.Failed, e.RelPath+" : "+err.Error())
				continue
			}
			done[sha] = true
			rep.Bytes += n
		}
		rep.Uploaded++
	}
	if len(rep.Failed) > 0 && rep.Uploaded == 0 {
		return rep, terr("upload_failed", "aucun fichier envoyé : %s", rep.Failed[0])
	}
	return rep, nil
}

// ---- slicing local de renfort ----

type localSlicing struct {
	studioPath string
	work       string
	once       sync.Once
	sl         *slicer.Slicer
	err        error
}

func (l *localSlicing) get() (*slicer.Slicer, error) {
	l.once.Do(func() { l.sl, l.err = slicer.New(slicer.Config{BinaryPath: l.studioPath, WorkRoot: l.work}) })
	return l.sl, l.err
}

func (l *localSlicing) Available() (bool, string) {
	b, err := slicer.Locate(l.studioPath)
	if err != nil {
		return false, ""
	}
	return true, b.Version
}

func (l *localSlicing) Slice(ctx context.Context, req slicer.Request) (*slicer.Result, error) {
	s, err := l.get()
	if err != nil {
		return nil, err
	}
	return s.Slice(ctx, req)
}

type sliceParams struct {
	Label          string            `json:"label"`
	Machine        string            `json:"machine"`
	Printer        string            `json:"printer"`
	Nozzle         float64           `json:"nozzle"`
	Process        string            `json:"process"`
	Filaments      []string          `json:"filaments"`
	Plate          int               `json:"plate"`
	Arrange        *bool             `json:"arrange"`
	Orient         bool              `json:"orient"`
	BedType        string            `json:"bed_type"`
	Overrides      []slicer.Override `json:"overrides"`
	TimeoutSeconds int               `json:"timeout_seconds"`
}

func (a *Agent) doSlice(env *taskEnv) agentlink.TaskResult {
	var p sliceParams
	if err := json.Unmarshal(env.task.Params, &p); err != nil {
		return failed("bad_params", err.Error())
	}
	if len(env.task.Inputs) != 1 {
		return failed("bad_params", "un seul artefact d'entrée (le modèle) est attendu")
	}
	in := env.task.Inputs[0]
	dir, err := env.workDir()
	if err != nil {
		return failed("workdir", err.Error())
	}
	defer os.RemoveAll(dir)
	name := in.Name
	if name == "" {
		name = in.SHA256[:12] + ".3mf"
	}
	if name, err = SafeOpenName(name); err != nil {
		return failed("bad_params", err.Error())
	}
	model := filepath.Join(dir, "in", name)
	env.progress(0.05, "téléchargement du modèle")
	if err := a.cl.Download(env.ctx, in.SHA256, model); err != nil { // empreinte vérifiée
		return failed("download_failed", err.Error())
	}
	label := p.Label
	if label == "" {
		label = strings.TrimSuffix(name, filepath.Ext(name))
	}
	req := slicer.Request{ID: env.task.ID, Label: label, Model: model, Machine: p.Machine, Printer: p.Printer, Nozzle: p.Nozzle,
		Process: p.Process, Filaments: p.Filaments, Plate: p.Plate, Arrange: p.Arrange, Orient: p.Orient, BedType: p.BedType,
		Overrides: p.Overrides, Timeout: time.Duration(p.TimeoutSeconds) * time.Second}
	env.progress(0.2, "slicing local")
	res, err := a.slicer.Slice(env.ctx, req)
	if res != nil {
		defer res.Cleanup()
	}
	if err != nil {
		if env.ctx.Err() != nil {
			return agentlink.TaskResult{State: agentlink.StateCancelled}
		}
		return failed("slice_failed", err.Error())
	}
	if res.ArtifactPath == "" {
		return failed("slice_failed", "aucun artefact produit")
	}
	outName := filepath.Base(res.ArtifactPath)
	env.progress(0.8, "envoi de l'artefact")
	if err := a.cl.Upload(env.ctx, env.task.ID, res.ArtifactPath, res.ArtifactSHA256, res.ArtifactSize, agentlink.TargetArtifact, outName, nil); err != nil {
		if env.ctx.Err() != nil {
			return agentlink.TaskResult{State: agentlink.StateCancelled}
		}
		return failed("upload_failed", err.Error())
	}
	res.Stdout, res.Stderr = tailStr(res.Stdout), tailStr(res.Stderr)
	raw, _ := json.Marshal(map[string]any{"executor": agentlink.ExecutorWindowsAgent, "agent_version": Version, "slice": res})
	return agentlink.TaskResult{State: agentlink.StateSucceeded, Result: raw,
		Outputs: []agentlink.Artifact{{SHA256: res.ArtifactSHA256, Name: outName, Size: res.ArtifactSize}}}
}

func tailStr(s string) string {
	if len(s) > 2000 {
		return s[len(s)-2000:]
	}
	return s
}

// ---- Studio : état du projet ouvert et ouverture ----

// ProjectStateReport est le résultat de get_project_state.
type ProjectStateReport struct {
	Studio  StudioInfo    `json:"studio"`
	Project *ProjectState `json:"project,omitempty"`
	Reason  string        `json:"reason,omitempty"`
}

func (a *Agent) doProjectState(env *taskEnv) (any, error) {
	return a.ProjectState(env.ctx, env.workDir)
}

// ProjectState détecte Studio et lit (sur une copie) le projet ouvert.
func (a *Agent) ProjectState(ctx context.Context, workDir func() (string, error)) (*ProjectStateReport, error) {
	info := a.studio.Info(ctx)
	rep := &ProjectStateReport{Studio: info}
	switch {
	case !info.Installed:
		rep.Reason = "Bambu Studio n'est pas installé sur ce poste"
	case !info.Running:
		rep.Reason = "Bambu Studio n'est pas lancé : aucun projet ouvert"
	case info.Project == nil:
		rep.Reason = "Studio tourne mais aucune fenêtre principale n'a été reconnue"
	case info.Project.Path == "":
		rep.Reason = "projet ouvert sans fichier identifiable : " + info.Project.Note
	default:
		dir, err := workDir()
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		cp := filepath.Join(dir, "project.3mf")
		if err := CopyForRead(info.Project.Path, cp); err != nil {
			return nil, terr("project_unreadable", "copie du projet : %v", err)
		}
		ps, err := ReadProject(cp)
		if err != nil {
			return nil, terr("project_unreadable", "%v", err)
		}
		ps.File = info.Project.Path
		rep.Project = ps
	}
	return rep, nil
}

func (a *Agent) doOpenInStudio(env *taskEnv) (any, error) {
	var p struct {
		SHA256   string `json:"sha256"`
		FileName string `json:"file_name"`
	}
	_ = json.Unmarshal(env.task.Params, &p)
	if len(env.task.Inputs) != 1 || env.task.Inputs[0].SHA256 != p.SHA256 {
		return nil, terr("bad_params", "l'artefact à ouvrir doit être l'unique entrée du travail")
	}
	name, err := SafeOpenName(firstNonEmpty(p.FileName, env.task.Inputs[0].Name))
	if err != nil {
		return nil, terr("bad_params", "%v", err)
	}
	dest := filepath.Join(a.cfg.workDir(), "open", p.SHA256[:12], name)
	if err := a.cl.Download(env.ctx, p.SHA256, dest); err != nil {
		return nil, terr("download_failed", "%v", err)
	}
	res, err := a.studio.Open(env.ctx, dest)
	if err != nil {
		return nil, terr("open_failed", "%v", err)
	}
	return res, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

var _ = errors.New
