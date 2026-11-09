package sim

import "time"

// Scénarios de la soirée de terrain (P1S) : abandon en préparation, redémarrage, pause sur erreur de filament, arrêt dont
// l'état reste RUNNING, reprise bancale (RUNNING figé, consigne plateau à 0 °C). Ils ne servent qu'aux tests.

const (
	FaultFreeze           = "freeze"             // RUNNING mais couche, % et temps restant ne bougent plus
	FaultStopStaysRunning = "stop_stays_running" // « stop » coupe les chauffes mais gcode_state reste RUNNING
	FaultResumeBroken     = "resume_broken"      // « resume » : RUNNING figé, buse 230 °C, consigne plateau 0 °C
)

// HMSEntry : une alerte HMS du rapport (attr, code).
type HMSEntry struct{ Attr, Code uint32 }

func (s *Sim) applyScenarioFault(name string, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch name {
	case FaultFreeze:
		s.p.frozen = on
	case FaultStopStaysRunning:
		s.p.stopKeepsRunning = on
	case FaultResumeBroken:
		s.p.resumeBroken = on
	}
}

func (s *Sim) noteCommand(cmd string) {
	s.mu.Lock()
	s.cmds = append(s.cmds, cmd)
	s.mu.Unlock()
}

// CommandCount : nombre de commandes d'impression reçues (pause, resume, stop, ams_change_filament…) de ce nom.
func (s *Sim) CommandCount(cmd string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.cmds {
		if c == cmd {
			n++
		}
	}
	return n
}

func hmsMaps(es []HMSEntry) []map[string]any {
	var out []map[string]any
	for _, e := range es {
		out = append(out, map[string]any{"attr": e.Attr, "code": e.Code})
	}
	return out
}

// AbortPreparation : l'imprimante abandonne en préparation (HMS + print_error), chauffes coupées, état IDLE.
func (s *Sim) AbortPreparation(hms []HMSEntry, printError int) {
	s.mu.Lock()
	s.p.gcodeState = "IDLE"
	s.p.hms, s.p.printError, s.p.hmsFault = hmsMaps(hms), printError, len(hms) > 0
	s.p.nozzleT, s.p.bedT, s.p.percent, s.p.layer, s.p.frozen = 0, 0, 0, 0, false
	s.mu.Unlock()
	s.publishDelta(false)
}

// PauseWithHMS : pause sur erreur (ex. bobine externe sans filament) avec ses alertes ; la reprise est refusée tant
// que l'erreur n'est pas levée (chargement du filament ou ClearErrors).
func (s *Sim) PauseWithHMS(hms []HMSEntry, printError int) {
	s.mu.Lock()
	if s.p.gcodeState == "RUNNING" {
		s.p.pausedAt = time.Now()
	}
	s.p.gcodeState = "PAUSE"
	s.p.hms, s.p.printError, s.p.hmsFault = hmsMaps(hms), printError, true
	s.mu.Unlock()
	s.publishDelta(false)
}

// ClearErrors acquitte les alertes (comme un chargement réussi ou un acquittement à l'écran).
func (s *Sim) ClearErrors() {
	s.mu.Lock()
	s.p.hms, s.p.printError, s.p.hmsFault = nil, 0, false
	s.mu.Unlock()
	s.publishDelta(false)
}

// Reboot : coupure complète (plus aucune connexion) pendant downFor, puis retour en IDLE avec un état NEUF (fichier
// courant effacé, aucune erreur), comme une P1S redémarrée. Non bloquant.
func (s *Sim) Reboot(downFor time.Duration) {
	_ = s.SetFault(FaultOffline, true)
	time.AfterFunc(downFor, func() {
		s.mu.Lock()
		s.p.gcodeState = "IDLE"
		s.p.subtask, s.p.gcodeFile, s.p.taskID, s.p.file = "", "", "", ""
		s.p.hms, s.p.printError, s.p.hmsFault, s.p.failReason = nil, 0, false, ""
		s.p.nozzleT, s.p.bedT, s.p.percent, s.p.layer, s.p.remaining, s.p.frozen = 0, 0, 0, 0, 0, false
		s.lastSent = map[string]string{}
		s.mu.Unlock()
		_ = s.SetFault(FaultOffline, false)
	})
}

// onFilamentCommand : « ams_change_filament » (chargement de la bobine externe) acquitte et lève l'erreur de filament.
func (s *Sim) onFilamentCommand(cmd, seq string) {
	if cmd == "ams_change_filament" {
		s.mu.Lock()
		s.p.hms, s.p.printError, s.p.hmsFault = nil, 0, false
		s.mu.Unlock()
	}
	s.respond(cmd, seq, nil)
	s.publishDelta(false)
}

// Commands : toutes les commandes d'impression reçues, dans l'ordre (project_file, pause, resume, stop, ams_*...). Pour prouver
// qu'un redémarrage du service n'a envoyé AUCUNE commande à l'imprimante.
func (s *Sim) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.cmds...)
}
