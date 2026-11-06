package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/agentlink"
)

// Journal persiste l'état des travaux de l'agent : après un redémarrage ou une
// coupure réseau, l'agent peut rapporter l'issue réelle au serveur (réconciliation)
// au lieu de laisser un travail « inconnu ».

// JournalEntry est un travail connu de l'agent.
type JournalEntry struct {
	ID     string                `json:"id"`
	Kind   string                `json:"kind"`
	Result *agentlink.TaskResult `json:"result,omitempty"` // nil tant que le travail n'est pas terminé
}

// Journal est un dossier de petits fichiers JSON, un par travail non acquitté.
type Journal struct {
	dir string
	mu  sync.Mutex
}

// OpenJournal crée le dossier d'état.
func OpenJournal(dir string) (*Journal, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Journal{dir: dir}, nil
}

func (j *Journal) file(id string) string {
	return filepath.Join(j.dir, strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(id)+".json")
}

func (j *Journal) write(e JournalEntry) {
	b, _ := json.Marshal(e)
	tmp := j.file(e.ID) + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, j.file(e.ID))
	}
}

// Begin note le début d'un travail.
func (j *Journal) Begin(t agentlink.Task) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.write(JournalEntry{ID: t.ID, Kind: t.Kind})
}

// Finish note l'issue d'un travail (avant tout envoi au serveur).
func (j *Journal) Finish(id string, r *agentlink.TaskResult) {
	j.mu.Lock()
	defer j.mu.Unlock()
	e := JournalEntry{ID: id, Result: r}
	if old, ok := j.read(id); ok {
		e.Kind = old.Kind
	}
	j.write(e)
}

// Ack oublie un travail dont le serveur a pris acte.
func (j *Journal) Ack(id string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	_ = os.Remove(j.file(id))
}

func (j *Journal) read(id string) (JournalEntry, bool) {
	b, err := os.ReadFile(j.file(id))
	if err != nil {
		return JournalEntry{}, false
	}
	var e JournalEntry
	return e, json.Unmarshal(b, &e) == nil
}

// Unacked liste les travaux non acquittés (ordre stable).
func (j *Journal) Unacked() []JournalEntry {
	j.mu.Lock()
	defer j.mu.Unlock()
	ents, _ := os.ReadDir(j.dir)
	var out []JournalEntry
	for _, d := range ents {
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			continue
		}
		if e, ok := j.read(strings.TrimSuffix(d.Name(), ".json")); ok {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out
}
