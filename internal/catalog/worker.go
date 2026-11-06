package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// stepBackend localise uv et le projet du worker géométrie. Absence = ErrBackendMissing.
func (s *Service) stepBackend() (uv, dir string, err error) {
	uv = s.cfg.UVPath
	if uv == "" {
		if uv, err = exec.LookPath("uv"); err != nil {
			return "", "", fmt.Errorf("%w : uv introuvable dans le PATH (configurer BAMBU_UV_PATH)", ErrBackendMissing)
		}
	} else if st, serr := os.Stat(uv); serr != nil || st.IsDir() {
		return "", "", fmt.Errorf("%w : uv introuvable à %q", ErrBackendMissing, uv)
	}
	candidates := []string{s.cfg.WorkerDir}
	if s.cfg.WorkerDir == "" {
		candidates = []string{filepath.Join("workers", "geometry")}
		if exe, e := os.Executable(); e == nil {
			d := filepath.Dir(exe)
			candidates = append(candidates, filepath.Join(d, "workers", "geometry"), filepath.Join(d, "..", "workers", "geometry"))
		}
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		abs, e := filepath.Abs(c)
		if e != nil {
			continue
		}
		if _, e := os.Stat(filepath.Join(abs, "pyproject.toml")); e == nil {
			return uv, abs, nil
		}
	}
	return "", "", fmt.Errorf("%w : projet workers/geometry introuvable (configurer BAMBU_GEOMETRY_DIR)", ErrBackendMissing)
}

// capBuffer garde au plus max octets (le reste est ignoré).
type capBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *capBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

// execWorker lance `uv run --project <dir> python -m bambu_geometry <commande>`
// (protocole : workers/geometry/PROTOCOL.md) : requête JSON sur stdin, une ligne
// JSON sur stdout. Les arguments sont passés en tableau, sans shell.
func (s *Service) execWorker(ctx context.Context, command string, req map[string]any) (json.RawMessage, error) {
	uv, dir, err := s.stepBackend()
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.StepTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, uv, "run", "--project", dir, "python", "-m", "bambu_geometry", command)
	cmd.Stdin = bytes.NewReader(body)
	out, errb := &capBuffer{max: 16 << 20}, &capBuffer{max: 64 << 10}
	cmd.Stdout, cmd.Stderr = out, errb
	cmd.WaitDelay = 5 * time.Second
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	line := lastLine(out.buf.String())
	var env struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if line != "" && json.Unmarshal([]byte(line), &env) == nil {
		if env.OK {
			return env.Result, nil
		}
		return nil, &WorkerError{Code: env.Error.Code, Message: env.Error.Message}
	}
	tail := strings.TrimSpace(errb.buf.String())
	if len(tail) > 1500 {
		tail = tail[len(tail)-1500:]
	}
	if runErr == nil {
		runErr = errors.New("réponse vide")
	}
	return nil, &WorkerError{Code: "worker_failed", Message: runErr.Error() + " : " + tail}
}

// moveWorkerOutputs copie le STL (et le STEP s'il existe) d'un résultat « parametric » vers dstDir/name.*.
func moveWorkerOutputs(raw json.RawMessage, outDir, dstDir, name string) error {
	var res struct {
		Files struct {
			STL  string `json:"stl"`
			STEP string `json:"step"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.Files.STL == "" {
		return errors.New("réponse du worker sans fichier STL")
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	cp := func(src, ext string) error {
		if !filepath.IsAbs(src) {
			src = filepath.Join(outDir, src)
		}
		if !within(outDir, src) {
			return fmt.Errorf("fichier du worker hors du dossier de travail : %s", src)
		}
		b, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dstDir, name+ext), b, 0o644)
	}
	if err := cp(res.Files.STL, ".stl"); err != nil {
		return err
	}
	if res.Files.STEP != "" {
		return cp(res.Files.STEP, ".step")
	}
	return nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
