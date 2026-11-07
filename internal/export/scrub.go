package export

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// scrubber retire des textes générés (README, JSON) tout ce qui ne doit pas quitter le serveur : chemins du serveur
// (dossier de données), adresses et numéros de série des imprimantes déclarées, adresses IP privées, chemins absolus.
// Il sert aussi à CONTRÔLER les fichiers .3mf avant de les joindre (find).
type scrubber struct {
	lits   []string // littéraux sensibles (déjà variantes de séparateurs et d'échappement JSON)
	labels map[string]string
}

var (
	reWinPath  = regexp.MustCompile(`(?i)\b[a-z]:(?:\\\\|\\|/)[^\s"'<>|]*`)
	reUnixPath = regexp.MustCompile(`(^|[\s"'(=:])(/(?:home|Users|root|var|opt|srv|mnt|tmp|etc|data)/[^\s"'<>|)]+)`)
	reUNCPath  = regexp.MustCompile(`\\\\{1,2}[A-Za-z0-9_.-]+\\{1,2}[A-Za-z0-9_$.-]+[^\s"'<>|]*`)
	rePrivIP   = regexp.MustCompile(`\b(?:10\.\d{1,3}\.\d{1,3}\.\d{1,3}|127\.\d{1,3}\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3}|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3})\b`)
)

const (
	redactedPath = "[chemin retiré]"
	redactedIP   = "[adresse retirée]"
	redactedData = "[donnée retirée]"
)

// newScrubber construit le filtre : dataDir (chemin du serveur) et les données des imprimantes déclarées.
func newScrubber(ctx context.Context, db *sql.DB, dataDir string) *scrubber {
	s := &scrubber{labels: map[string]string{}}
	add := func(v, label string) {
		v = strings.TrimSpace(v)
		if len(v) < 4 {
			return
		}
		for _, x := range variants(v) {
			if _, dup := s.labels[x]; !dup {
				s.lits = append(s.lits, x)
				s.labels[x] = label
			}
		}
	}
	if dataDir != "" {
		add(dataDir, "dossier de données du serveur")
		if abs, err := filepath.Abs(dataDir); err == nil {
			add(abs, "dossier de données du serveur")
		}
	}
	if db != nil {
		if rows, err := db.QueryContext(ctx, `SELECT doc FROM printers`); err == nil {
			defer rows.Close()
			for rows.Next() {
				var doc string
				if rows.Scan(&doc) != nil {
					continue
				}
				var p struct {
					Host   string `json:"host"`
					Serial string `json:"serial"`
				}
				if json.Unmarshal([]byte(doc), &p) == nil {
					add(p.Host, "adresse d'imprimante")
					add(p.Serial, "numéro de série d'imprimante")
				}
			}
		}
	}
	// Plus longs d'abord : un chemin complet est remplacé avant son préfixe.
	sort.Slice(s.lits, func(i, j int) bool { return len(s.lits[i]) > len(s.lits[j]) })
	return s
}

// variants : le littéral, ses écritures avec / ou \, et sa forme échappée dans un JSON.
func variants(v string) []string {
	set := map[string]bool{v: true}
	set[filepath.ToSlash(v)] = true
	set[strings.ReplaceAll(v, "/", `\`)] = true
	for x := range map[string]bool{v: true, filepath.ToSlash(v): true, strings.ReplaceAll(v, "/", `\`): true} {
		set[strings.ReplaceAll(x, `\`, `\\`)] = true
	}
	out := make([]string, 0, len(set))
	for x := range set {
		out = append(out, x)
	}
	return out
}

// Text filtre un texte (README, valeur de réglage…).
func (s *scrubber) Text(in string) string {
	out := in
	for _, l := range s.lits {
		if strings.Contains(out, l) {
			out = strings.ReplaceAll(out, l, redactedData)
		}
	}
	out = reWinPath.ReplaceAllString(out, redactedPath)
	out = reUNCPath.ReplaceAllString(out, redactedPath)
	out = reUnixPath.ReplaceAllString(out, "${1}"+redactedPath)
	out = rePrivIP.ReplaceAllString(out, redactedIP)
	return out
}

// Bytes filtre un document texte (JSON compris : les littéraux échappés sont couverts par variants).
func (s *scrubber) Bytes(in []byte) []byte { return []byte(s.Text(string(in))) }

// find renvoie les NATURES de données sensibles trouvées dans b (jamais les valeurs). paths/ips : chercher aussi
// les chemins absolus et les adresses privées (fichiers produits par la découpe) ; sinon seulement les littéraux.
func (s *scrubber) find(b []byte, paths, ips bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range s.lits {
		if bytes.Contains(b, []byte(l)) && !seen[s.labels[l]] {
			seen[s.labels[l]] = true
			out = append(out, s.labels[l])
		}
	}
	if paths && (reWinPath.Match(b) || reUNCPath.Match(b)) {
		out = append(out, "chemin absolu")
	}
	if ips && rePrivIP.Match(b) {
		out = append(out, "adresse IP privée")
	}
	sort.Strings(out)
	return out
}

const (
	scanWhole  = 8 << 20   // entrées entières lues jusqu'à cette taille
	scanWindow = 256 << 10 // sinon : début et fin seulement
)

// scanArchive contrôle chaque entrée d'une archive zip (3MF) et renvoie les natures de données sensibles trouvées.
// strict : cherche aussi chemins absolus et adresses IP privées (fichiers découpés) ; sinon seulement les données
// propres au serveur et aux imprimantes déclarées (fichiers d'origine de l'utilisateur, conservés tels quels).
func (s *scrubber) scanArchive(r io.ReaderAt, size int64, strict bool) ([]string, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		var parts [][]byte
		if f.UncompressedSize64 <= scanWhole {
			b, err := io.ReadAll(io.LimitReader(rc, scanWhole+1))
			if err != nil {
				rc.Close()
				return nil, err
			}
			parts = [][]byte{b}
		} else {
			head := make([]byte, scanWindow)
			n, _ := io.ReadFull(rc, head)
			parts = append(parts, head[:n])
			// la fin : lire en glissant une fenêtre (flux, mémoire constante)
			win := make([]byte, 0, scanWindow)
			buf := make([]byte, 64<<10)
			for {
				m, err := rc.Read(buf)
				win = append(win, buf[:m]...)
				if len(win) > scanWindow {
					win = append(win[:0], win[len(win)-scanWindow:]...)
				}
				if err != nil {
					break
				}
			}
			parts = append(parts, win)
		}
		rc.Close()
		for _, p := range parts {
			for _, k := range s.find(p, strict, strict) {
				if !seen[k] {
					seen[k] = true
					out = append(out, k)
				}
			}
		}
	}
	sort.Strings(out)
	return out, nil
}
