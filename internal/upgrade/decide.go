package upgrade

import (
	"fmt"
	"strings"
	"time"
)

// Action est ce que la décision ordonne.
type Action string

const (
	ActUpToDate  Action = "a_jour"       // rien de nouveau : sortie silencieuse
	ActDisabled  Action = "desactive"    // interrupteur : /etc/atelier/auto-update.disabled
	ActKnownFail Action = "deja_echoue"  // ce commit a déjà échoué ici : on attend un nouveau commit
	ActWaitCI    Action = "attendre_ci"  // CI en cours / inconnue : réessai au prochain passage
	ActRejectCI  Action = "ci_rouge"     // CI rouge : jamais installé
	ActWindow    Action = "hors_fenetre" // hors de la fenêtre horaire autorisée
	ActBusy      Action = "occupe"       // impression en cours, confirmation / arrêt en attente...
	ActInstall   Action = "installer"    // feu vert
)

// BusyState : occupation du service.
type BusyState struct {
	Busy    bool
	Unknown bool     // impossible de le savoir : traité comme occupé (sécurité d'abord)
	Reasons []string // codes et détails lisibles
}

// Inputs rassemble tout ce dont dépend la décision. Aucun accès réseau ni disque : fonction pure, testée en table.
type Inputs struct {
	Now      time.Time
	Disabled bool
	Manual   bool // lancé à la main (--now) : l'interrupteur et la fenêtre horaire ne s'appliquent pas
	Retry    bool // --retry : retente un commit déjà échoué
	Window   Window
	Current  string // sha installé ("" : inconnu)
	Target   string // sha visé : épingle résolue, sinon tête de la branche
	Pinned   bool
	Failed   map[string]FailedEntry
	CI       CIResult
	Busy     BusyState
}

// Decision est le verdict. Quiet : sortie silencieuse (rien à journaliser au-delà de la ligne de passage).
type Decision struct {
	Action Action
	Reason string
	Quiet  bool
}

// Decide applique les règles dans l'ordre : interrupteur, rien de neuf, échec connu, CI, fenêtre horaire, occupation.
// La CI passe avant l'occupation : on ne retient jamais une mise à jour dont on ne voudrait pas.
func Decide(in Inputs) Decision {
	if in.Disabled && !in.Manual {
		return Decision{ActDisabled, "mises à jour automatiques désactivées (fichier " + DisabledFileName + " ou ATELIER_UPDATE_DISABLED=1)", true}
	}
	if in.Target != "" && in.Target == in.Current {
		if in.Pinned {
			return Decision{ActUpToDate, "version épinglée " + short(in.Target) + " déjà installée", true}
		}
		return Decision{ActUpToDate, "déjà à jour (" + short(in.Current) + ")", true}
	}
	if f, bad := in.Failed[in.Target]; bad && !in.Retry {
		return Decision{ActKnownFail, fmt.Sprintf("le commit %s a déjà échoué ici le %s (%s) : attente d'un nouveau commit ou d'un --retry",
			short(in.Target), f.At.Local().Format("2006-01-02 15:04"), f.Reason), true}
	}
	switch in.CI.State {
	case CIGreen:
	case CIRed:
		return Decision{ActRejectCI, short(in.Target) + " refusé : " + in.CI.Detail, false}
	default:
		return Decision{ActWaitCI, short(in.Target) + " en attente : " + in.CI.Detail, false}
	}
	if !in.Manual && !in.Window.Contains(in.Now) {
		return Decision{ActWindow, fmt.Sprintf("%s prêt à installer mais hors de la fenêtre horaire (%s) : reporté", short(in.Target), in.Window), false}
	}
	if in.Busy.Busy || in.Busy.Unknown {
		why := strings.Join(in.Busy.Reasons, " ; ")
		if in.Busy.Unknown {
			why = "occupation impossible à déterminer (" + why + ") : traité comme occupé"
		}
		return Decision{ActBusy, short(in.Target) + " prêt à installer mais le service est occupé : " + why, false}
	}
	return Decision{ActInstall, "installation de " + short(in.Target) + " (" + in.CI.Detail + ")", false}
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
