package upgrade

import "strings"

// Codes d'occupation du service (internal/printing/busy.go) que la politique « impression en cours » laisse passer.
const (
	busyPrintRunning  = "print_running"  // PRINTING : le fichier est sur la carte SD de l'imprimante, le suivi reprend à la reconnexion
	busyPrinterActive = "printer_active" // l'imprimante travaille (Studio, téléphone, écran) : elle n'a pas besoin du service
)

// ApplyBusyPolicy sépare les raisons d'occupation qui BLOQUENT de celles qu'on tolère.
//
// Avec allowDuringPrint (défaut), une impression en cours et une imprimante au travail ne retardent plus la mise à jour :
// redémarrer bambud n'interrompt pas l'impression. Restent bloquants, quelle que soit la politique : un dépôt ou une
// découpe en cours (deposit_in_progress), une confirmation de lancement, un arrêt ou une action en attente de l'humain
// (valables 10 min au plus), un lancement en cours d'accusé ou d'issue incertaine (print_active : START_REQUESTED,
// START_UNKNOWN), un travail lourd en cours (job_running) et toute raison inconnue (un service plus récent qui en ajouterait).
// Une occupation « inconnue » (service et base illisibles) reste traitée comme occupée.
func ApplyBusyPolicy(in BusyState, allowDuringPrint bool) BusyState {
	if in.Unknown || !allowDuringPrint {
		return in
	}
	out := BusyState{Unknown: in.Unknown}
	for _, r := range in.Reasons {
		if code := reasonCode(r); code == busyPrintRunning || code == busyPrinterActive {
			out.Tolerated = append(out.Tolerated, r)
			continue
		}
		out.Reasons = append(out.Reasons, r)
	}
	out.Tolerated = append(out.Tolerated, in.Tolerated...)
	// Busy venait du service : s'il disait « occupé » sans aucune raison lisible, on le croit.
	out.Busy = len(out.Reasons) > 0 || (in.Busy && len(in.Reasons) == 0)
	return out
}

// reasonCode : « print_running: 1 impression(s) » -> « print_running ».
func reasonCode(reason string) string {
	code, _, _ := strings.Cut(reason, ":")
	return strings.TrimSpace(code)
}
