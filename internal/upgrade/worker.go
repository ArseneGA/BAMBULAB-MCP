package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Worker CAO (workers/geometry). En production l'environnement Python est HORS du dossier du worker
// (UV_PROJECT_ENVIRONMENT=/var/lib/atelier/venvs/geometry, UV_FROZEN=1, posés par un drop-in systemd du service) et le paquet
// bambu-geometry y est installé NON éditable : remplacer le code du dossier ne suffit pas, uv doit réinstaller le paquet
// (--reinstall-package). La mise à jour lit donc l'environnement du SERVICE tel que systemd le lui donne, et le test de fumée
// compare l'empreinte du code que le worker rapporte (commande ping) à celle du code de la version installée.

// workerSyncArgs : synchronisation figée par uv.lock, sans dépendances de développement, avec réinstallation du paquet local.
var workerSyncArgs = []string{"sync", "--frozen", "--no-dev", "--extra", "cad", "--reinstall-package", "bambu-geometry"}

// workerPingArgs : exactement la commande que bambud lance pour le worker (internal/catalog/worker.go), avec la commande « ping ».
func workerPingArgs(dir string) []string {
	return []string{"run", "--frozen", "--no-dev", "--extra", "cad", "--project", dir, "python", "-m", "bambu_geometry", "ping"}
}

// splitSystemdWords découpe une valeur de « systemctl show -p Environment » : mots séparés par des blancs, éventuellement
// entre guillemets doubles ou simples (\" et \\ échappés).
func splitSystemdWords(s string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	started := false
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case quote != 0:
			switch {
			case c == quote:
				quote = 0
			case c == '\\' && quote == '"' && i+1 < len(rs) && (rs[i+1] == '"' || rs[i+1] == '\\'):
				i++
				cur.WriteRune(rs[i])
			default:
				cur.WriteRune(c)
			}
		case c == '"' || c == '\'':
			quote, started = c, true
		case c == ' ' || c == '\t':
			if started || cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteRune(c)
		}
	}
	if started || cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// ParseSystemctlShow lit la sortie de `systemctl show UNITE -p Environment -p EnvironmentFiles` : les variables posées
// directement (Environment=, drop-ins compris) et les fichiers d'environnement déclarés (EnvironmentFiles=).
func ParseSystemctlShow(out string) (env map[string]string, files []string) {
	env = map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Environment="):
			for _, w := range splitSystemdWords(strings.TrimPrefix(line, "Environment=")) {
				if k, v, ok := strings.Cut(w, "="); ok && k != "" {
					env[k] = v
				}
			}
		case strings.HasPrefix(line, "EnvironmentFiles="):
			// « /etc/atelier/atelier.env (ignore_errors=no) » : le chemin est le premier mot.
			if ws := splitSystemdWords(strings.TrimPrefix(line, "EnvironmentFiles=")); len(ws) > 0 {
				files = append(files, strings.TrimPrefix(ws[0], "-"))
			}
		}
	}
	return env, files
}

// serviceEnv : l'environnement que systemd donne au service (atelier.env déjà lu, fichiers d'environnement déclarés, puis
// Environment= des fichiers d'unité et drop-ins, qui l'emporte). Un systemctl muet laisse atelier.env seul.
func (e *SysEnv) serviceEnv(ctx context.Context) map[string]string {
	out := map[string]string{}
	for k, v := range e.Cfg.AppEnv {
		out[k] = v
	}
	txt, err := e.asRoot(ctx, time.Minute, "systemctl", "show", e.Cfg.Service, "-p", "Environment", "-p", "EnvironmentFiles")
	if err != nil {
		e.Logf("environnement du service illisible (%v) : atelier.env seul", err)
		return out
	}
	env, files := ParseSystemctlShow(txt)
	for _, f := range files {
		if m, err := ReadEnvFile(f); err == nil {
			for k, v := range m {
				out[k] = v
			}
		}
	}
	for k, v := range env {
		out[k] = v
	}
	return out
}

// workerEnv : environnement d'uv pour le worker, identique à celui du service : toutes les variables UV_* (UV_PROJECT_ENVIRONMENT,
// UV_FROZEN, UV_CACHE_DIR, UV_PYTHON_INSTALL_DIR, UV_NO_EDITABLE...). Sans UV_PROJECT_ENVIRONMENT ni .venv dans le dossier du
// worker, bambud place l'environnement dans <données>/venvs/geometry (internal/catalog/worker.go) : même règle ici.
func (e *SysEnv) workerEnv(ctx context.Context, cad string) []string {
	se := e.serviceEnv(ctx)
	var keys []string
	for k, v := range se {
		if strings.HasPrefix(k, "UV_") && v != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var extra []string
	for _, k := range keys {
		extra = append(extra, k+"="+se[k])
	}
	if se["UV_PROJECT_ENVIRONMENT"] == "" && e.Cfg.DataDir != "" {
		if _, err := os.Stat(filepath.Join(cad, ".venv")); err != nil {
			extra = append(extra, "UV_PROJECT_ENVIRONMENT="+filepath.Join(e.Cfg.DataDir, "venvs", "geometry"))
		}
	}
	return e.appEnv(extra...)
}

// syncWorker : `uv sync --frozen --no-dev --extra cad --reinstall-package bambu-geometry` sous l'utilisateur du service.
func (e *SysEnv) syncWorker(ctx context.Context, uv, cad string) error {
	_, err := e.Run.Run(ctx, Cmd{Name: uv, Args: workerSyncArgs, Dir: cad, Env: e.workerEnv(ctx, cad), User: e.Cfg.AppUser, Timeout: 30 * time.Minute})
	return err
}

// WorkerFingerprint : empreinte du code Python d'un paquet bambu_geometry. DOIT rester identique à
// workers/geometry/src/bambu_geometry/fingerprint.py : SHA-256 de, pour chaque fichier .py (hors __pycache__) classé par chemin
// relatif en « / », le chemin, un octet nul, le contenu, un octet nul.
func WorkerFingerprint(dir string) (string, error) {
	var rels []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".py") {
			rel, rerr := filepath.Rel(dir, p)
			if rerr != nil {
				return rerr
			}
			rels = append(rels, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(rels) == 0 {
		return "", fmt.Errorf("aucun fichier .py dans %s", dir)
	}
	sort.Strings(rels)
	h := sha256.New()
	for _, rel := range rels {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		h.Write([]byte(rel))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// WorkerPing : ce que la commande `ping` du worker rapporte (version et empreinte du code RÉELLEMENT importé).
type WorkerPing struct {
	Version    string
	CodeSHA256 string
	Path       string
}

// ParseWorkerPing lit la réponse JSON de `python -m bambu_geometry ping` (une ligne) au milieu des journaux de stderr.
func ParseWorkerPing(out string) (WorkerPing, error) {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(l, "{") || !strings.Contains(l, `"ok"`) {
			continue
		}
		var r struct {
			OK     bool `json:"ok"`
			Result struct {
				Worker struct {
					Version    string `json:"version"`
					CodeSHA256 string `json:"code_sha256"`
					Path       string `json:"path"`
				} `json:"worker"`
			} `json:"result"`
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			continue
		}
		if !r.OK {
			return WorkerPing{}, fmt.Errorf("le worker répond une erreur %s : %s", r.Error.Code, r.Error.Message)
		}
		w := r.Result.Worker
		return WorkerPing{Version: w.Version, CodeSHA256: w.CodeSHA256, Path: w.Path}, nil
	}
	return WorkerPing{}, fmt.Errorf("réponse du worker illisible : %q", truncate(out, 160))
}

// checkWorkerVersion : le worker que le service lancera exécute-t-il bien le code de la version installée ? (pas seulement : répond-il ?)
// Un paquet installé non éditable resté sur l'ancien code répond au ping mais avec une autre empreinte : échec, donc retour arrière.
func (e *SysEnv) checkWorkerVersion(ctx context.Context) error {
	uv, cad := e.Cfg.AppEnv["BAMBU_UV_PATH"], e.Cfg.CadDir
	if uv == "" || cad == "" {
		return nil
	}
	src := filepath.Join(e.Cfg.CurrentLink(), "geometry", "src", "bambu_geometry")
	want, err := WorkerFingerprint(src)
	if err != nil {
		e.Logf("version du worker géométrie non vérifiable (%v) : contrôle ignoré", err)
		return nil
	}
	out, err := e.Run.Run(ctx, Cmd{Name: uv, Args: workerPingArgs(cad), Dir: cad, Env: e.workerEnv(ctx, cad), User: e.Cfg.AppUser, Timeout: 10 * time.Minute, Stdin: "{}"})
	if err != nil {
		return fmt.Errorf("ping du worker géométrie : %w", err)
	}
	got, err := ParseWorkerPing(out)
	if err != nil {
		return err
	}
	if got.CodeSHA256 == "" {
		return fmt.Errorf("le worker géométrie ne rapporte pas l'empreinte de son code (ancien paquet resté installé : uv sync --reinstall-package bambu-geometry n'a pas pris effet)")
	}
	if got.CodeSHA256 != want {
		return fmt.Errorf("le worker géométrie exécute un autre code que la version installée (empreinte %s, attendue %s, paquet %s) : "+
			"uv sync --reinstall-package bambu-geometry n'a pas pris effet", short(got.CodeSHA256), short(want), got.Path)
	}
	e.Logf("worker géométrie : version %s, empreinte du code %s conforme à la version installée", got.Version, short(got.CodeSHA256))
	return nil
}
