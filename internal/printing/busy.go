package printing

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

// Raisons pour lesquelles un redémarrage du service est déconseillé (mise à jour automatique : internal/upgrade).
// Codes stables : ils sont journalisés et notifiés.
const (
	BusyPrintActive     = "print_active"         // une impression est en cours (ou son lancement, ou son état est incertain)
	BusyPrinterActive   = "printer_active"       // l'imprimante déclare travailler, même si ce service ne l'a pas lancée
	BusyConfirmPending  = "confirmation_pending" // une confirmation de lancement attend l'humain (jeton encore valable)
	BusyStopPending     = "stop_pending"         // une demande d'arrêt attend l'humain, ou un arrêt est en cours d'application
	BusyActionPending   = "action_pending"       // reprise / chargement / nettoyage en attente de validation humaine
	BusyDepositProgress = "deposit_in_progress"  // découpe ou dépôt du fichier sur l'imprimante en cours
)

// BusyReason est une raison d'occupation, avec un détail lisible (sans secret).
type BusyReason struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// activeJobStates : états d'un PrintJob qui interdisent un redémarrage.
// START_UNKNOWN y figure : on ne sait pas si l'imprimante imprime ; c'est à l'humain de trancher (reconcile_print).
var activeJobStates = map[contracts.PrintState]string{
	contracts.PrintPrinting:       BusyPrintActive,
	contracts.PrintStartRequested: BusyPrintActive,
	contracts.PrintStartUnknown:   BusyPrintActive,
	contracts.PrintUploading:      BusyDepositProgress,
	contracts.PrintSlicing:        BusyDepositProgress,
}

// BusyReasons renvoie ce qui rend un redémarrage du service risqué en ce moment (liste vide : libre). Lecture seule : aucune
// commande n'est envoyée aux imprimantes ; l'état des imprimantes lu est celui que leurs adaptateurs déjà ouverts
// connaissent (borné à quelques secondes).
// ActiveJobStates liste les états de PrintJob qui comptent comme « occupé » (utilisé par le contrôle de cohérence de internal/upgrade).
func ActiveJobStates() []string {
	out := make([]string, 0, len(activeJobStates))
	for s := range activeJobStates {
		out = append(out, string(s))
	}
	sort.Strings(out)
	return out
}

func (s *Service) BusyReasons(ctx context.Context) []BusyReason {
	var out []BusyReason
	now := s.now()

	// 1. Impressions en base.
	if jobs, err := s.ListJobs(ctx, "", 0); err == nil {
		counts := map[string]int{}
		first := map[string]string{}
		for _, j := range jobs {
			if code, ok := activeJobStates[j.State]; ok {
				counts[code]++
				if first[code] == "" {
					first[code] = fmt.Sprintf("%s (%s)", j.ID, j.State)
				}
			}
		}
		for _, code := range []string{BusyPrintActive, BusyDepositProgress} {
			if counts[code] > 0 {
				out = append(out, BusyReason{Code: code, Detail: fmt.Sprintf("%d impression(s), dont %s", counts[code], first[code])})
			}
		}
	} else {
		// Base illisible : on ne peut pas affirmer que c'est libre.
		out = append(out, BusyReason{Code: BusyPrintActive, Detail: "impressions illisibles : " + err.Error()})
	}

	// 2. Demandes humaines en attente (en mémoire : confirmations, arrêts, actions).
	s.mu.Lock()
	confirms, stops, acts := 0, 0, 0
	for _, a := range s.approv {
		if now.Before(a.expires) {
			confirms++
		}
	}
	for _, r := range s.stops {
		if now.Before(r.expires) {
			stops++
		}
	}
	stops += len(s.stopping)
	for _, a := range s.actions {
		if now.Before(a.expires) {
			acts++
		}
	}
	cached := map[string]printer.Adapter{}
	for id, e := range s.adapters {
		cached[id] = e.ad
	}
	s.mu.Unlock()
	if confirms > 0 {
		out = append(out, BusyReason{Code: BusyConfirmPending, Detail: fmt.Sprintf("%d confirmation(s) de lancement en attente de l'humain", confirms)})
	}
	if stops > 0 {
		out = append(out, BusyReason{Code: BusyStopPending, Detail: fmt.Sprintf("%d demande(s) d'arrêt en attente ou en cours", stops)})
	}
	if acts > 0 {
		out = append(out, BusyReason{Code: BusyActionPending, Detail: fmt.Sprintf("%d action(s) physique(s) en attente de l'humain", acts)})
	}

	// 3. L'imprimante elle-même (impression lancée depuis Bambu Studio, le téléphone ou l'écran).
	ids := make([]string, 0, len(cached))
	for id := range cached {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		st, err := cached[id].Status(cctx)
		cancel()
		if err != nil {
			continue
		}
		switch st.State {
		case printer.StatePrinting, printer.StatePreparing, printer.StatePaused:
			out = append(out, BusyReason{Code: BusyPrinterActive, Detail: fmt.Sprintf("imprimante %s : %s", id, st.State)})
		}
	}
	return out
}
