package printing

import (
	"context"
	"fmt"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

// FB14 : événements faits HORS Printalk (arrêt, pause, reprise, redémarrage, relance depuis l'écran de l'imprimante ou
// l'application Bambu). Ils sont détectés par le changement d'état rapporté par l'imprimante, inscrits dans l'historique
// de l'impression (job.events, champ `external` vrai, note préfixée « hors Printalk : ») avec leur heure. Ce code est en
// LECTURE SEULE vis-à-vis de l'imprimante : il n'envoie, n'arrête ni ne reprend jamais rien.

const externalPrefix = "hors Printalk : "

// ownResumeWindow : durée pendant laquelle un retour en impression suit la propre commande de reprise de Printalk.
const ownResumeWindow = 3 * time.Minute

// noteOwnResume mémorise que Printalk vient d'envoyer SA commande de reprise (la reprise observée ensuite n'est pas externe).
func (s *Service) noteOwnResume(id string) {
	s.mu.Lock()
	if s.ownResume == nil {
		s.ownResume = map[string]time.Time{}
	}
	s.ownResume[id] = s.now()
	s.mu.Unlock()
}

func (s *Service) ownResumeRecent(id string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, ok := s.ownResume[id]
	return ok && now.Sub(at) <= ownResumeWindow
}

// journalExternal ajoute un événement « hors Printalk » horodaté (sans changer l'état de l'impression). Un même événement
// déjà inscrit depuis moins de 10 s (deux relevés concurrents du même changement d'état) n'est pas répété.
func (s *Service) journalExternal(ctx context.Context, id, event, note string) {
	now := s.now()
	if _, err := s.update(ctx, id, func(x *Job) error {
		for i := len(x.Events) - 1; i >= 0 && i >= len(x.Events)-8; i-- {
			if l := x.Events[i]; l.External && l.Event == event && now.Sub(l.At) < 10*time.Second {
				return nil
			}
		}
		x.Events = append(x.Events, JobEvent{At: now, Event: event, From: x.State, To: x.State, Note: externalPrefix + note, External: true})
		if over := len(x.Events) - 150; over > 0 {
			x.Events = x.Events[over:]
		}
		return nil
	}); err != nil {
		s.log.Warn("évènement hors Printalk non journalisé", "print_job", id, "event", event, "err", err)
	}
}

// detectExternal compare l'état précédent (enregistré) au rapport courant d'une impression dont le fichier est reconnu.
// Il ne conclut que sur un CHANGEMENT d'état d'un rapport complet ; un arrêt, une pause ou une reprise commandés par
// Printalk (stopping, ownResume) ne sont jamais présentés comme externes.
func (s *Service) detectExternal(ctx context.Context, id string, old *PrintProgress, pg *PrintProgress, st printer.Status, now time.Time, stopping bool) {
	if stopping {
		return
	}
	if tid := st.TaskID; tid != "" && tid != "0" {
		switch {
		case pg.TaskID == "":
			pg.TaskID = tid
		case pg.TaskID != tid:
			s.journalExternal(ctx, id, "external_relaunch", fmt.Sprintf("relance depuis l'imprimante : nouveau numéro de tâche (%s au lieu de %s) pour le même fichier", tid, pg.TaskID))
			pg.TaskID = tid
		}
	}
	if old == nil || !old.SeenActive || old.PrinterState == "" {
		return
	}
	was, is := printer.State(old.PrinterState), st.State
	running := func(x printer.State) bool { return x == printer.StatePreparing || x == printer.StatePrinting }
	switch {
	case running(was) && is == printer.StatePaused:
		if es := decodeErrors(st); len(es) > 0 {
			s.journalExternal(ctx, id, "external_pause", "l'imprimante est passée en pause sur erreur (non demandée par Printalk) : "+errorsText(es, false))
		} else {
			s.journalExternal(ctx, id, "external_pause", "pause demandée sur l'écran de l'imprimante ou dans l'application Bambu (Printalk n'a envoyé aucune pause)")
		}
	case was == printer.StatePaused && running(is):
		if !s.ownResumeRecent(id, now) {
			s.journalExternal(ctx, id, "external_resume", "reprise faite sur l'écran de l'imprimante ou dans l'application Bambu (Printalk n'a envoyé aucune reprise)")
		}
	}
}

// journalExternalClose : motif d'une fin vue sans arrêt commandé par Printalk (appelé avant closeFromPrinter).
func (s *Service) journalExternalClose(ctx context.Context, id string, pg *PrintProgress, st printer.Status, kind string) {
	switch kind {
	case "restart":
		s.journalExternal(ctx, id, "external_restart", "l'imprimante a redémarré (liaison coupée puis rétablie, retour au repos) : l'impression est perdue")
	case "relaunch":
		cur := st.SubtaskName
		if cur == "" {
			cur = "aucun fichier"
		}
		s.journalExternal(ctx, id, "external_relaunch", fmt.Sprintf("l'imprimante ne désigne plus ce fichier (maintenant : %s) : autre impression lancée ou fichier changé depuis l'imprimante", cur))
	default: // stop
		if es := decodeErrors(st); len(es) > 0 {
			s.journalExternal(ctx, id, "external_stop", "l'imprimante a interrompu l'impression sur erreur : "+errorsText(es, false))
		} else {
			s.journalExternal(ctx, id, "external_stop", fmt.Sprintf("l'impression s'est arrêtée sans commande de Printalk (annulation sur l'écran de l'imprimante, état %s)", st.State))
		}
	}
}
