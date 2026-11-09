package upgrade

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Cmd décrit une commande externe.
type Cmd struct {
	Name    string
	Args    []string
	Dir     string
	Env     []string      // environnement COMPLET (KEY=VALUE) ; vide : environnement du processus
	User    string        // si non vide, la commande s'exécute sous cet utilisateur (runuser), avec exactement Env
	Timeout time.Duration // 0 : pas de délai propre (celui du contexte s'applique)
	Stdin   string        // entrée standard (vide : aucune)
}

// String : forme lisible pour les journaux (jamais l'environnement : il peut porter des jetons).
func (c Cmd) String() string {
	s := c.Name + " " + strings.Join(c.Args, " ")
	if c.User != "" {
		s = "[" + c.User + "] " + s
	}
	return strings.TrimSpace(s)
}

// Runner exécute des commandes ; la sortie combinée (stdout + stderr) est renvoyée, tronquée à la fin.
type Runner interface {
	Run(ctx context.Context, c Cmd) (string, error)
}

// ExecRunner est le Runner réel. SwitchUser faux : ignore Cmd.User (tests locaux, exécution déjà non privilégiée).
type ExecRunner struct {
	SwitchUser bool
}

const maxOutput = 64 << 10

type tailBuffer struct{ b bytes.Buffer }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b.Write(p)
	if t.b.Len() > 2*maxOutput {
		keep := append([]byte(nil), t.b.Bytes()[t.b.Len()-maxOutput:]...)
		t.b.Reset()
		t.b.Write(keep)
	}
	return len(p), nil
}

// Run exécute c. Sous un utilisateur donné : `runuser -u USER -- env -i K=V... cmd args` (environnement vidé : rien du
// root ne fuit vers du code téléchargé).
func (r ExecRunner) Run(ctx context.Context, c Cmd) (string, error) {
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	name, args := c.Name, c.Args
	if r.SwitchUser && c.User != "" {
		wrapped := []string{"-u", c.User, "--", "env", "-i"}
		wrapped = append(wrapped, c.Env...)
		wrapped = append(wrapped, name)
		wrapped = append(wrapped, args...)
		name, args = "runuser", wrapped
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = effectiveDir(c, r.SwitchUser)
	if c.Stdin != "" {
		cmd.Stdin = strings.NewReader(c.Stdin)
	}
	if len(c.Env) > 0 && !(r.SwitchUser && c.User != "") {
		cmd.Env = c.Env
	}
	var out tailBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.WaitDelay = 10 * time.Second
	err := cmd.Run()
	text := strings.TrimRight(out.b.String(), "\r\n")
	if err != nil {
		if ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return text, fmt.Errorf("%s : délai dépassé", c)
		}
		return text, fmt.Errorf("%s : %w%s", c, err, tailHint(text))
	}
	return text, nil
}

// effectiveDir : dossier de départ de la commande. Sous un autre utilisateur (runuser), sans dossier explicite, la commande
// hériterait du dossier courant de root (souvent /home/<admin>, illisible pour le compte de service : « go: cannot determine
// current directory ») : elle part de « / ».
func effectiveDir(c Cmd, switchUser bool) string {
	if c.Dir == "" && switchUser && c.User != "" {
		return "/"
	}
	return c.Dir
}

// tailHint : les dernières lignes de sortie, pour comprendre un échec dans le journal.
func tailHint(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return ""
	}
	if len(lines) > 25 {
		lines = lines[len(lines)-25:]
	}
	return "\n" + strings.Join(lines, "\n")
}
