package printing

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

// startPolicy : états de départ acceptés par le service. Comme Bambu Studio, un lancement part de IDLE, de FINISH
// ou de FAILED (une P1S reste en FAILED après une annulation jusqu'à son redémarrage). Les garde-fous sont
// inchangés : état frais, aucune alerte HMS active, aucun print_error en cours (hors reliquat bénin d'annulation),
// plateau attesté vide par l'humain, confirmation à usage unique.
var startPolicy = printer.FullStartPolicy

// launchableNow lit l'état de l'imprimante et dit si elle peut recevoir un lancement. L'erreur est un
// *printer.NotReadyError (consigne précise) ou une erreur de lecture.
func (s *Service) launchableNow(ctx context.Context, ad printer.Adapter, strictFresh bool) (printer.Status, error) {
	sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	st, err := ad.Status(sctx)
	cancel()
	if err != nil {
		return st, fmt.Errorf("%w : état de l'imprimante illisible (%v)", printer.ErrNotReady, err)
	}
	if !strictFresh {
		st.Stale = false // à la demande de confirmation, la fraîcheur stricte est exigée au lancement, pas ici
	}
	return st, printer.CheckLaunchable(st, 0, s.now(), startPolicy)
}

// ensureLaunchable : AVANT d'émettre une confirmation, vérifie que l'imprimante est lançable. Sinon refuse avec une
// consigne précise (ex. « l'imprimante signale une erreur HMS 0300_… : … ; acquittez-la sur l'écran ou redémarrez »)
// plutôt que de laisser l'humain approuver un lancement qui échouera ensuite.
func (s *Service) ensureLaunchable(ctx context.Context, printerID string) error {
	ad, _, err := s.adapter(ctx, printerID)
	if err != nil {
		// Imprimante non configurée (certificat non épinglé, code absent) : aucun état à lire ; le comportement
		// historique est conservé (la confirmation est émise, le lancement dira précisément ce qui manque).
		s.log.Debug("lançabilité non vérifiée : adaptateur indisponible", "printer", printerID, "err", err)
		return nil
	}
	if _, err := s.launchableNow(ctx, ad, false); err != nil {
		return fmt.Errorf("l'imprimante n'est pas lançable, aucune confirmation n'a été émise : %w", err)
	}
	return nil
}

// startOptionsOf : options de démarrage envoyées à l'imprimante, lues sur l'impression liée (jamais sur un paramètre
// de start_print) : exactement ce que l'humain a vu et approuvé. Valeurs par défaut = celles envoyées jusqu'ici.
func startOptionsOf(j Job) printer.StartOptions {
	o := printer.DefaultStartOptions()
	o.BedLeveling = !j.NoBedLeveling
	o.FlowCali = !j.NoFlowCali
	o.VibrationCali = !j.NoVibrationCali
	o.Timelapse = j.Timelapse
	return o
}

// vibrationCaliSupported : familles Bambu FDM qui acceptent vibration_cali dans project_file (P1, X1, A1, H2).
func vibrationCaliSupported(modelID string) bool {
	m := strings.ToLower(strings.TrimSpace(modelID))
	for _, p := range []string{"p1", "x1", "a1", "h2"} {
		if strings.HasPrefix(m, p) {
			return true
		}
	}
	return false
}
