package upgrade

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// FailedEntry : un commit qui a échoué sur ce serveur (construction, tests, démarrage, santé...). Il n'est pas
// retenté tant qu'un nouveau commit n'arrive pas : sans cela, chaque passage du timer referait le même redémarrage raté.
type FailedEntry struct {
	At     time.Time `json:"at"`
	Reason string    `json:"reason"`
}

// State est l'état persistant du service de mise à jour (/var/lib/atelier-update/state.json).
type State struct {
	Failed     map[string]FailedEntry `json:"failed"`
	Notified   map[string]time.Time   `json:"notified"`   // « <sha>:<genre> » -> date : une notification par commit et par genre
	LastOK     string                 `json:"last_ok"`    // dernier commit installé avec succès par ce mécanisme
	LastOKAt   time.Time              `json:"last_ok_at"` //
	LastCheck  time.Time              `json:"last_check"`
	LastResult string                 `json:"last_result"`
	// Pending : mise à jour commencée et pas terminée (coupure de courant, reboot, arrêt de l'unité pendant la bascule).
	// Le passage suivant la reprend : santé de la nouvelle version, sinon retour arrière avec les mêmes informations.
	Pending *PendingUpdate `json:"pending,omitempty"`
	// LastLog : dernière journalisation d'une décision répétitive (report, CI en cours...), pour ne pas écrire toutes les 5 minutes.
	LastLog map[string]time.Time `json:"last_log,omitempty"`
}

func newState() *State {
	return &State{Failed: map[string]FailedEntry{}, Notified: map[string]time.Time{}}
}

// LoadState lit l'état ; un fichier absent ou illisible donne un état neuf (jamais bloquant).
func LoadState(path string) *State {
	s := newState()
	b, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	if json.Unmarshal(b, s) != nil {
		return newState()
	}
	if s.Failed == nil {
		s.Failed = map[string]FailedEntry{}
	}
	if s.Notified == nil {
		s.Notified = map[string]time.Time{}
	}
	return s
}

// Save écrit l'état atomiquement.
func (s *State) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	_ = os.Chmod(tmp.Name(), 0o644)
	return os.Rename(tmp.Name(), path)
}

// Once dit s'il faut émettre la notification (sha, genre) : vrai la première fois, faux ensuite. Les clés de plus de
// 60 jours sont oubliées.
func (s *State) Once(sha, kind string, now time.Time) bool {
	for k, t := range s.Notified {
		if now.Sub(t) > 60*24*time.Hour {
			delete(s.Notified, k)
		}
	}
	key := sha + ":" + kind
	if _, done := s.Notified[key]; done {
		return false
	}
	s.Notified[key] = now
	return true
}

// LogEvery dit s'il faut journaliser la clé maintenant : vrai au plus une fois par période d.
func (s *State) LogEvery(key string, d time.Duration, now time.Time) bool {
	if s.LastLog == nil {
		s.LastLog = map[string]time.Time{}
	}
	for k, t := range s.LastLog {
		if now.Sub(t) > 7*24*time.Hour {
			delete(s.LastLog, k)
		}
	}
	if t, ok := s.LastLog[key]; ok && now.Sub(t) < d {
		return false
	}
	s.LastLog[key] = now
	return true
}

// PendingUpdate est le journal d'intention écrit AVANT la bascule du lien current.
type PendingUpdate struct {
	Target    Release   `json:"target"`
	Previous  Release   `json:"previous"`
	Archive   string    `json:"archive"`
	PreSchema int       `json:"pre_schema"`
	Since     time.Time `json:"since"`
}
