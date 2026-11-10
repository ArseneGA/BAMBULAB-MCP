package sim

import "time"

// SetExtraPrepareStage fait rapporter une étape supplémentaire (ex. 54, absente des tables publiées) à la fin de la
// préparation du PROCHAIN project_file (FB7). 0 = aucune. Pour les tests.
func (s *Sim) SetExtraPrepareStage(id int) {
	s.mu.Lock()
	s.p.extraStage = id
	s.mu.Unlock()
}

// ScreenCommand reproduit un geste fait SUR L'ÉCRAN de l'imprimante (ou dans l'application Bambu) : "pause", "resume" ou
// "stop". Contrairement à une commande MQTT, il n'est pas compté dans Commands() : le service n'en est pas l'auteur (FB14).
func (s *Sim) ScreenCommand(cmd string) error {
	s.mu.Lock()
	err := s.p.control(cmd, time.Now())
	s.mu.Unlock()
	s.publishDelta(false)
	return err
}

// ScreenRelaunch reproduit une relance faite depuis l'écran : l'imprimante repart en préparation avec un NOUVEAU task_id
// (FB14). Le fichier est celui de la dernière impression.
func (s *Sim) ScreenRelaunch() error {
	s.mu.Lock()
	file, gcode, sub := s.p.file, s.p.gcodeFile, s.p.subtask
	s.p.gcodeState = "IDLE" // l'écran impose son propre départ, quel que soit l'état précédent
	err := s.p.begin(time.Now(), file, gcode, sub)
	if err == nil {
		s.p.setStages(map[string]any{})
	}
	s.mu.Unlock()
	s.publishDelta(false)
	return err
}
