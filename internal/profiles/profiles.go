// Package profiles charge un répertoire de profils Bambu Studio (machine,
// process, filament + fichier fournisseur), résout l'héritage `inherits` en
// configurations complètes avec provenance de chaque clé, et répond aux
// questions de catalogue : imprimantes, volumes, buses, process et filaments
// compatibles.
//
// Les profils ne sont jamais copiés dans le dépôt : le paquet lit un répertoire
// existant (installation de Bambu Studio ou fixture de test).
package profiles

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Kind est la famille d'un profil.
type Kind string

const (
	KindMachine      Kind = "machine"
	KindProcess      Kind = "process"
	KindFilament     Kind = "filament"
	KindMachineModel Kind = "machine_model" // métadonnées d'un modèle d'imprimante
)

// Erreurs nommées.
var (
	ErrNotFound      = errors.New("profiles: profil introuvable")
	ErrCycle         = errors.New("profiles: cycle dans la chaîne inherits")
	ErrMissingParent = errors.New("profiles: parent inherits introuvable")
	ErrNoVendor      = errors.New("profiles: aucun profil trouvé dans le répertoire")
)

// Profile est un fichier de profil tel que lu, sans résolution.
type Profile struct {
	Kind          Kind
	Name          string
	Path          string // chemin du fichier
	Inherits      string
	Instantiation bool // false pour les bases (fdm_*), true pour les presets utilisables
	Raw           map[string]any
}

// Store est un ensemble de profils chargés. Sûr pour un usage concurrent.
type Store struct {
	Dir      string   // répertoire fournisseur (contient machine/, process/, filament/)
	Vendor   string   // nom du fournisseur (BBL)
	Version  string   // version lue dans <Vendor>.json si présent
	Warnings []string // fichiers ignorés, doublons

	byKind map[Kind]map[string]*Profile

	mu    sync.Mutex
	cache map[cacheKey]*Resolved
}

type cacheKey struct {
	k Kind
	n string
}

// Load charge un répertoire fournisseur. `dir` peut être soit le répertoire
// contenant machine/, process/ et filament/ (…/profiles/BBL), soit son parent
// contenant BBL.json.
func Load(dir string) (*Store, error) {
	vendorDir := dir
	if !isDir(filepath.Join(dir, "machine")) {
		// parent : cherche le seul sous-répertoire qui contient machine/
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		found := ""
		for _, e := range entries {
			if e.IsDir() && isDir(filepath.Join(dir, e.Name(), "machine")) {
				found = filepath.Join(dir, e.Name())
				break
			}
		}
		if found == "" {
			return nil, fmt.Errorf("%w: %s", ErrNoVendor, dir)
		}
		vendorDir = found
	}
	s := &Store{
		Dir:    vendorDir,
		Vendor: filepath.Base(vendorDir),
		byKind: map[Kind]map[string]*Profile{},
		cache:  map[cacheKey]*Resolved{},
	}
	for _, k := range []Kind{KindMachine, KindProcess, KindFilament} {
		s.byKind[k] = map[string]*Profile{}
	}
	s.byKind[KindMachineModel] = map[string]*Profile{}

	// fichier fournisseur (version)
	for _, p := range []string{
		filepath.Join(filepath.Dir(vendorDir), s.Vendor+".json"),
		filepath.Join(vendorDir, s.Vendor+".json"),
	} {
		if raw, err := readJSON(p); err == nil {
			if v, ok := raw["version"].(string); ok {
				s.Version = v
			}
			break
		}
	}

	for _, k := range []Kind{KindMachine, KindProcess, KindFilament} {
		sub := filepath.Join(vendorDir, string(k))
		entries, err := os.ReadDir(sub)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrNoVendor, err)
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
				continue
			}
			path := filepath.Join(sub, e.Name())
			raw, err := readJSON(path)
			if err != nil {
				s.Warnings = append(s.Warnings, fmt.Sprintf("%s: %v", path, err))
				continue
			}
			name, _ := raw["name"].(string)
			if name == "" {
				s.Warnings = append(s.Warnings, fmt.Sprintf("%s: pas de champ name, ignoré", path))
				continue
			}
			kind := k
			if t, _ := raw["type"].(string); t == string(KindMachineModel) {
				kind = KindMachineModel
			}
			inh, _ := raw["inherits"].(string)
			p := &Profile{Kind: kind, Name: name, Path: path, Inherits: inh, Raw: raw}
			p.Instantiation = true
			if v, ok := raw["instantiation"].(string); ok && strings.EqualFold(v, "false") {
				p.Instantiation = false
			}
			if _, dup := s.byKind[kind][name]; dup {
				s.Warnings = append(s.Warnings, fmt.Sprintf("%s: doublon du nom %q, ignoré", path, name))
				continue
			}
			s.byKind[kind][name] = p
		}
	}
	if len(s.byKind[KindMachine])+len(s.byKind[KindProcess])+len(s.byKind[KindFilament]) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoVendor, dir)
	}
	return s, nil
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func readJSON(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

// Get retourne le profil brut (non résolu).
func (s *Store) Get(k Kind, name string) (*Profile, bool) {
	p, ok := s.byKind[k][name]
	return p, ok
}

// Names retourne les noms des profils instanciables (presets utilisables) d'une
// famille, triés.
func (s *Store) Names(k Kind) []string {
	var out []string
	for n, p := range s.byKind[k] {
		if p.Instantiation {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// Count retourne le nombre total de profils chargés d'une famille (bases comprises).
func (s *Store) Count(k Kind) int { return len(s.byKind[k]) }

// Resolved est une configuration complète : toutes les clés de la chaîne
// d'héritage, la valeur la plus proche de la feuille l'emportant.
type Resolved struct {
	Kind   Kind
	Name   string
	Chain  []string          // feuille d'abord, racine en dernier
	Files  []string          // chemins des fichiers de la chaîne, même ordre
	Config map[string]any    // configuration aplatie (inherits vidé)
	Source map[string]string // clé -> nom du profil qui fournit la valeur
}

// Resolve aplatit la chaîne `inherits` d'un profil.
func (s *Store) Resolve(k Kind, name string) (*Resolved, error) {
	s.mu.Lock()
	if r, ok := s.cache[cacheKey{k, name}]; ok {
		s.mu.Unlock()
		return r, nil
	}
	s.mu.Unlock()

	var chain []*Profile
	seen := map[string]bool{}
	cur := name
	for cur != "" {
		if seen[cur] {
			names := make([]string, len(chain))
			for i, p := range chain {
				names[i] = p.Name
			}
			return nil, fmt.Errorf("%w: %s -> %s", ErrCycle, strings.Join(names, " -> "), cur)
		}
		seen[cur] = true
		p, ok := s.byKind[k][cur]
		if !ok {
			if len(chain) == 0 {
				return nil, fmt.Errorf("%w: %s %q", ErrNotFound, k, name)
			}
			return nil, fmt.Errorf("%w: %q (parent de %q)", ErrMissingParent, cur, chain[len(chain)-1].Name)
		}
		chain = append(chain, p)
		cur = p.Inherits
	}

	r := &Resolved{Kind: k, Name: name, Config: map[string]any{}, Source: map[string]string{}}
	for _, p := range chain {
		r.Chain = append(r.Chain, p.Name)
		r.Files = append(r.Files, p.Path)
	}
	for i := len(chain) - 1; i >= 0; i-- { // racine -> feuille
		p := chain[i]
		for key, v := range p.Raw {
			r.Config[key] = cloneValue(v)
			r.Source[key] = p.Name
		}
	}
	// La configuration aplatie ne se rattache plus à rien. Le nom reste celui
	// de la feuille, et la source de ces clés de métadonnées aussi.
	r.Config["inherits"] = ""
	r.Source["inherits"] = name
	r.Config["name"] = name
	r.Source["name"] = name

	s.mu.Lock()
	s.cache[cacheKey{k, name}] = r
	s.mu.Unlock()
	return r, nil
}

func cloneValue(v any) any {
	switch t := v.(type) {
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = cloneValue(t[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = cloneValue(x)
		}
		return out
	}
	return v
}

// Clone retourne une copie profonde modifiable.
func (r *Resolved) Clone() *Resolved {
	c := &Resolved{Kind: r.Kind, Name: r.Name,
		Chain:  append([]string(nil), r.Chain...),
		Files:  append([]string(nil), r.Files...),
		Config: map[string]any{}, Source: map[string]string{}}
	for k, v := range r.Config {
		c.Config[k] = cloneValue(v)
	}
	for k, v := range r.Source {
		c.Source[k] = v
	}
	return c
}

// JSON sérialise la configuration complète, clés triées, indentée (format
// accepté par --load-settings / --load-filaments).
func (r *Resolved) JSON() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "    ")
	if err := enc.Encode(r.Config); err != nil { // encoding/json trie les clés de map
		return nil, err
	}
	return buf.Bytes(), nil
}

// String retourne la valeur d'une clé scalaire (ou le premier élément d'une liste).
func (r *Resolved) String(key string) (string, bool) {
	switch t := r.Config[key].(type) {
	case string:
		return t, true
	case []any:
		if len(t) > 0 {
			if s, ok := t[0].(string); ok {
				return s, true
			}
		}
	case json.Number:
		return t.String(), true
	}
	return "", false
}

// Strings retourne les valeurs d'une clé en liste de chaînes (un scalaire donne
// une liste d'un élément).
func (r *Resolved) Strings(key string) []string {
	switch t := r.Config[key].(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
