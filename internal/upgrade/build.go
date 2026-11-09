package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const coreVersionVar = "github.com/ArseneGA/BAMBULAB-MCP/internal/core.Version"

// Binaires construits dans chaque version.
var releaseBinaries = []string{"bambud", "bambu-mcp", "bambu-agent"}

// Build construit la version sha dans releases/<sha> sans toucher à ce qui tourne : extraction de l'arbre git (pas de
// .git, pas de hooks), interface web, binaires, tests, validateur MCP, puis publication atomique (renommage d'un
// dossier .partial) sous root:root. Tout le code téléchargé s'exécute sous l'utilisateur de construction.
func (e *SysEnv) Build(ctx context.Context, sha string) (Release, error) {
	var zero Release
	if len(sha) != 40 || strings.Trim(sha, "0123456789abcdef") != "" {
		return zero, fmt.Errorf("sha invalide %q", sha)
	}
	cfg := e.Cfg
	final := filepath.Join(cfg.ReleasesDir(), sha)
	partial := filepath.Join(cfg.ReleasesDir(), ".partial-"+sha)
	work := filepath.Join(cfg.BuildDir(), sha)
	src := filepath.Join(work, "src")

	if err := os.MkdirAll(cfg.ReleasesDir(), 0o755); err != nil {
		return zero, err
	}
	// Nettoyage d'une construction interrompue.
	if _, err := e.asRoot(ctx, 5*time.Minute, "rm", "-rf", partial, final); err != nil {
		return zero, err
	}
	if _, err := e.asBuilder(ctx, cfg.CacheDir, 5*time.Minute, e.buildEnv(), "rm", "-rf", work); err != nil {
		return zero, err
	}
	// La construction écrit directement dans le dossier partiel : il appartient à l'utilisateur de construction le temps de la
	// construction, puis repasse à root avant publication.
	if _, err := e.asRoot(ctx, time.Minute, "install", "-d", "-m", "0755", "-o", cfg.BuildUser, partial); err != nil {
		return zero, err
	}
	cleanup := func() {
		_, _ = e.asRoot(context.WithoutCancel(ctx), 5*time.Minute, "rm", "-rf", partial)
		_, _ = e.asBuilder(context.WithoutCancel(ctx), cfg.CacheDir, 5*time.Minute, e.buildEnv(), "rm", "-rf", work)
	}
	fail := func(step string, err error) (Release, error) {
		cleanup()
		return zero, fmt.Errorf("%s : %w", step, err)
	}
	step := func(name string) { e.Logf("construction %s : %s", short(sha), name) }

	step("extraction de l'arbre git")
	verOut, err := e.git(ctx, "describe", "--always", "--tags", sha)
	if err != nil {
		return fail("version (git describe)", err)
	}
	version := strings.TrimSpace(verOut)
	if version == "" || strings.ContainsAny(version, " \t\n\"'$`\\") {
		return fail("version", fmt.Errorf("version illisible %q", truncate(version, 60)))
	}
	treeOut, _ := e.git(ctx, "rev-parse", "--verify", "--quiet", sha+":workers/geometry")
	geomTree := strings.TrimSpace(treeOut)
	if _, err := e.asBuilder(ctx, cfg.CacheDir, 5*time.Minute, e.buildEnv(), "mkdir", "-p", src); err != nil {
		return fail("dossier de construction", err)
	}
	tarball := filepath.Join(work, "src.tar")
	if _, err := e.git(ctx, "archive", "--format=tar", "-o", tarball, sha); err != nil {
		return fail("git archive", err)
	}
	if _, err := e.asBuilder(ctx, work, 10*time.Minute, e.buildEnv(), "tar", "-xf", tarball, "-C", src); err != nil {
		return fail("extraction", err)
	}

	step("interface web (npm ci, npm run build)")
	webDir := filepath.Join(src, "web")
	if _, err := e.asBuilder(ctx, webDir, 20*time.Minute, e.buildEnv(), "npm", "ci", "--no-audit", "--no-fund"); err != nil {
		return fail("npm ci", err)
	}
	if _, err := e.asBuilder(ctx, webDir, 20*time.Minute, e.buildEnv(), "npm", "run", "build"); err != nil {
		return fail("npm run build", err)
	}
	for _, f := range []string{"internal/webui/assets/index.html", "internal/mcpserver/ui/views/mcp-app.html"} {
		if _, err := e.asBuilder(ctx, src, time.Minute, e.buildEnv(), "test", "-s", f); err != nil {
			return fail("interface non construite ("+f+" absent ou vide)", err)
		}
	}

	step("binaires")
	if _, err := e.asBuilder(ctx, src, time.Minute, e.buildEnv(), "mkdir", "-p", filepath.Join(partial, "bin")); err != nil {
		return fail("dossier bin", err)
	}
	goEnv := e.buildEnv("CGO_ENABLED=0")
	ldflags := "-s -w -X " + coreVersionVar + "=" + version
	for _, b := range releaseBinaries {
		if _, err := e.asBuilder(ctx, src, 30*time.Minute, goEnv, "go", "build", "-trimpath", "-ldflags", ldflags,
			"-o", filepath.Join(partial, "bin", b), "./cmd/"+b); err != nil {
			return fail("go build "+b, err)
		}
	}

	step("tests (go test -short)")
	tmpOut, err := e.asBuilder(ctx, src, time.Minute, e.buildEnv(), "mktemp", "-d", filepath.Join(work, "tmp-XXXXXX"))
	if err != nil {
		return fail("répertoire temporaire des tests", err)
	}
	tmp := strings.TrimSpace(tmpOut)
	// Environnement vidé : ni BAMBU_STUDIO_BIN (les tests visent Studio 02.08 ; la version installée peut différer), ni BAMBU_*.
	testEnv := e.buildEnv("TMPDIR="+tmp, "CGO_ENABLED=0")
	if testOut, err := e.asBuilder(ctx, src, 50*time.Minute, testEnv, "go", "test", "-short", "./...", "-count=1", "-timeout", "30m"); err != nil {
		// Les tests en échec (« --- FAIL: ») et les paquets (« FAIL ») sont extraits de la sortie complète et placés en tête :
		// la fin de la sortie, seule conservée par les journaux, ne les contient pas toujours.
		if d := FailureDigest(testOut); d != "" {
			e.Logf("tests en échec : %s", d)
		}
		return fail("go test", withDigest(err, testOut))
	}

	step("validateur MCP (scripts/mcp-sdk-validate)")
	valDir := filepath.Join(src, "scripts", "mcp-sdk-validate")
	if _, err := e.asBuilder(ctx, valDir, 20*time.Minute, e.buildEnv(), "npm", "ci", "--no-audit", "--no-fund"); err != nil {
		return fail("npm ci du validateur", err)
	}
	if _, err := e.asBuilder(ctx, valDir, 10*time.Minute, e.buildEnv("TMPDIR="+tmp, "BAMBUD_BIN="+filepath.Join(partial, "bin", "bambud")), "node", "validate.mjs"); err != nil {
		return fail("validateur MCP", err)
	}

	step("worker géométrie, scripts")
	if _, err := e.asBuilder(ctx, src, 5*time.Minute, e.buildEnv(), "sh", "-c",
		"mkdir -p '"+partial+"/scripts' && cp -a workers/geometry '"+partial+"/geometry' && cp scripts/smoke-linux.sh '"+partial+"/scripts/smoke-linux.sh'"); err != nil {
		return fail("copie du worker et des scripts", err)
	}

	step("schéma de la base connu du binaire")
	schemaOut, err := e.asBuilder(ctx, src, time.Minute, e.buildEnv(), filepath.Join(partial, "bin", "bambud"), "schema-version")
	if err != nil {
		return fail("bambud schema-version", err)
	}
	schema, err := strconv.Atoi(strings.TrimSpace(schemaOut))
	if err != nil || schema <= 0 {
		return fail("bambud schema-version", fmt.Errorf("sortie illisible %q", truncate(schemaOut, 60)))
	}

	rel := Release{SHA: sha, Version: version, Schema: schema, GeometryTree: geomTree, BuiltAt: e.Now().UTC(), Dir: final}
	meta, _ := json.MarshalIndent(rel, "", "  ")
	if err := os.WriteFile(filepath.Join(partial, "REVISION"), append(meta, '\n'), 0o644); err != nil {
		return fail("REVISION", err)
	}
	if err := os.WriteFile(filepath.Join(partial, ".ready"), []byte(e.Now().UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		return fail("marqueur .ready", err)
	}

	step("publication")
	// Aucun processus de l'utilisateur de construction ne doit survivre à la construction : une commande hostile pourrait en
	// laisser un en arrière-plan pour modifier le dossier pendant que root le publie (code de sortie 1 = aucun : normal).
	_, _ = e.asRoot(ctx, time.Minute, "pkill", "-KILL", "-u", cfg.BuildUser)
	if _, err := e.asRoot(ctx, 10*time.Minute, "chown", "-R", "root:root", partial); err != nil {
		return fail("chown", err)
	}
	if _, err := e.asRoot(ctx, 5*time.Minute, "chmod", "-R", "go-w", partial); err != nil {
		return fail("chmod", err)
	}
	if err := os.Rename(partial, final); err != nil {
		return fail("publication de la version", err)
	}
	_, _ = e.asBuilder(ctx, cfg.CacheDir, 5*time.Minute, e.buildEnv(), "rm", "-rf", work)
	e.Logf("version %s construite et testée : %s (schéma %d)", short(sha), final, schema)
	return rel, nil
}
