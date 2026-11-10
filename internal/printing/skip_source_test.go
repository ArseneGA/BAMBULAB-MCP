package printing_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// CA0.4 : skip_objects ne sort QUE par la sous-commande humaine. Test de source (aucun outil MCP, aucune route ouverte au
// modèle ou au navigateur) puis test d'exécution (tools/list).

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod introuvable")
		}
		dir = parent
	}
}

// goSources : (chemin relatif en slash, contenu) des fichiers .go hors tests, hors worktrees et dépendances.
func goSources(t *testing.T) map[string]string {
	t.Helper()
	root := repoRoot(t)
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", ".claude", "vendor", "web":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 50 {
		t.Fatalf("trop peu de sources lues (%d) : mauvais dossier ?", len(out))
	}
	return out
}

func filesMatching(src map[string]string, re *regexp.Regexp) []string {
	var out []string
	for f, c := range src {
		if re.MatchString(c) {
			out = append(out, f)
		}
	}
	return out
}

func only(t *testing.T, what string, got []string, allowed ...string) {
	t.Helper()
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	for _, g := range got {
		if !ok[g] {
			t.Fatalf("%s : appelé depuis %s, seuls %v sont autorisés (CA0.4 : skip_objects ne sort que par la sous-commande humaine)", what, g, allowed)
		}
	}
	if len(got) == 0 {
		t.Fatalf("%s : plus aucun appelant : le test de source ne garde plus rien", what)
	}
}

func TestSkipObjectsOnlyReachableFromTheHumanSubcommand(t *testing.T) {
	src := goSources(t)

	// 1. L'envoi : un seul appelant, le service d'impression (internal/printing/skip.go) ; la définition est dans printer/skip.go.
	only(t, "méthode SkipObjects", filesMatching(src, regexp.MustCompile(`\.SkipObjects\(`)), "internal/printing/skip.go")

	// 2. Le service n'est atteint que par les routes locales réservées de la sous-commande (httpapi/skip_local.go).
	only(t, "SkipObjectsPrepare/Execute/List", filesMatching(src, regexp.MustCompile(`\bSkipObjects(Prepare|Execute|List)\(`)), "internal/printing/skip.go", "internal/httpapi/skip_local.go")

	// 3. Le validateur dédié n'est appelé que par le client MQTT.
	only(t, "ValidateSkipObjectsPayload", filesMatching(src, regexp.MustCompile(`\bValidateSkipObjectsPayload\(`)), "internal/printer/skip.go")

	// 4. Aucune route, aucun outil, aucune page, aucun agent, aucune interface web ne parle de skip_objects.
	for f, c := range src {
		low := strings.ToLower(c)
		reserved := f == "internal/printer/skip.go" || f == "internal/printer/sim/skip.go" || f == "internal/printing/skip.go" ||
			f == "internal/httpapi/skip_local.go" || f == "cmd/bambud/skip.go" || f == "cmd/bambud/main.go" || f == "internal/printer/sim/sim.go" || f == "internal/printer/sim/model.go" ||
			f == "internal/slicer/objects.go" || f == "internal/printer/bambulan.go" || f == "internal/printer/status.go" || f == "internal/printer/printer.go" ||
			f == "internal/printing/watch.go" || f == "internal/printing/types.go" || f == "internal/printing/service.go" || f == "internal/httpapi/httpapi.go"
		if reserved {
			continue
		}
		for _, w := range []string{"skip_objects", "skipobjects", "skip-objects", "s_obj"} {
			if strings.Contains(low, w) {
				t.Fatalf("%s mentionne %q : seuls les fichiers de l'étape SK0 le peuvent", f, w)
			}
		}
	}
	for f := range src {
		if strings.HasPrefix(f, "internal/mcpserver/") || strings.HasPrefix(f, "internal/webui/") || strings.HasPrefix(f, "internal/agent") {
			if strings.Contains(strings.ToLower(src[f]), "skip") && strings.Contains(strings.ToLower(src[f]), "object") {
				t.Fatalf("%s : un outil MCP, l'interface web ou l'agent ne doivent pas toucher à l'ignorance d'objets (SK0)", f)
			}
		}
	}

	// 5. Les routes ne sont montées que par registerSkipLocal, sous /api/system/skip-objects/, et le mot clé n'est pas dans les pages.
	only(t, "chemin /api/system/skip-objects", filesMatching(src, regexp.MustCompile(`/api/system/skip-objects`)), "internal/httpapi/skip_local.go", "cmd/bambud/skip.go")
	var mounts int
	for f, c := range src {
		if strings.HasPrefix(f, "internal/httpapi/") {
			mounts += strings.Count(c, "registerSkipLocal(")
		}
	}
	if mounts != 2 { // la définition + l'unique appel dans buildHandlers
		t.Fatalf("registerSkipLocal : %d occurrences (définition + un seul montage attendus)", mounts)
	}
	pub, ok := src["internal/httpapi/public.go"]
	if !ok || strings.Contains(pub, "registerSkipLocal") || strings.Contains(pub, "skip-objects") {
		t.Fatalf("la surface publique ne doit pas monter les routes d'ignorance d'objets")
	}
}

// Aucun outil MCP ne peut envoyer skip_objects : ni par son nom, ni par son schéma, ni par sa description.
func TestNoMCPToolCanSkipObjects(t *testing.T) {
	r := newRig(t, "Z:/absent/bambu-studio.exe")
	tools, err := r.cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) < 50 {
		t.Fatalf("tools/list : %d outils", len(tools.Tools))
	}
	for _, tl := range tools.Tools {
		b, _ := json.Marshal(tl)
		low := strings.ToLower(string(b))
		if strings.Contains(strings.ToLower(tl.Name), "skip") || strings.Contains(low, "skip_objects") || strings.Contains(low, "obj_list") {
			t.Fatalf("outil %s : parle d'ignorer des objets (réservé à la sous-commande humaine) : %s", tl.Name, b)
		}
	}
}
