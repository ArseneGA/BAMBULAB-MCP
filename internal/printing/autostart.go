package printing

import (
	"context"
	"fmt"
)

// Lancement déclenché PAR L'APPROBATION HUMAINE (page /confirm ou code de lancement saisi dans la vue MCP App).
//
// Avant : l'humain approuvait, puis devait « dire à Claude que c'est fait » pour que le modèle appelle start_print.
// Maintenant : l'approbation humaine enchaîne côté serveur, sans le modèle, le même StartPrint que l'outil (contrôle
// d'état frais, consommation atomique de la confirmation, une seule commande, jamais de renvoi : START_UNKNOWN sur accusé
// lent reste réconcilié en lecture). Le modèle n'obtient AUCUN pouvoir de plus : seul l'humain (page enregistrée ou code)
// peut approuver, donc seul son geste déclenche le démarrage. L'outil start_print reste utilisable : si le démarrage a
// déjà été fait par l'approbation il le dit, sans rien renvoyer à l'imprimante (voir StartPrint).

// autoStartKey : clé d'idempotence réservée au démarrage déclenché par l'approbation (propre à la confirmation).
func autoStartKey(confID string) string { return "auto-start-" + confID }

// ApprovalResult : issue d'une approbation humaine suivie du lancement automatique.
type ApprovalResult struct {
	View       *ConfirmationView
	Start      *StartOutput // nil si le lancement n'a pas pu être tenté (voir StartError)
	StartError string       // raison pour laquelle rien n'a été envoyé (imprimante pas prête…) ; l'accord reste valable 2 minutes
}

// ApproveAndStart enregistre l'accord humain puis lance l'impression. À n'appeler que depuis un canal humain.
func (s *Service) ApproveAndStart(ctx context.Context, confID string, plateClear bool, via string) (*ApprovalResult, error) {
	v, err := s.Approve(ctx, confID, plateClear, via)
	if err != nil {
		return nil, err
	}
	res := &ApprovalResult{View: v}
	out, err := s.StartPrint(ctx, StartInput{PrintJobID: v.PrintJobID, ConfirmationID: confID, IdempotencyKey: autoStartKey(confID)})
	if err != nil {
		res.StartError = err.Error()
		_ = s.d.Store.Audit(ctx, "human:"+via, "print.auto_start_refused", "print_job", v.PrintJobID, map[string]any{"confirmation": confID, "error": err.Error()})
		return res, nil
	}
	res.Start = out
	_ = s.d.Store.Audit(ctx, "human:"+via, "print.auto_start", "print_job", v.PrintJobID, map[string]any{"confirmation": confID, "outcome": out.Outcome})
	return res, nil
}

// Message dit à l'humain ce qui s'est passé après son approbation (jamais de secret).
func (r *ApprovalResult) Message() string {
	if r.Start == nil {
		return fmt.Sprintf("Autorisation enregistrée, mais l'impression n'a PAS démarré : %s. L'autorisation reste valable 2 minutes : Claude peut relancer (start_print) quand l'imprimante est prête.", r.StartError)
	}
	switch r.Start.Outcome {
	case "printing":
		return "Impression lancée. Claude suit l'avancement (get_print_status)."
	case "start_requested":
		return "Commande de lancement envoyée : l'imprimante met parfois plusieurs dizaines de secondes à accuser réception. Rien ne sera renvoyé ; Claude suit l'état (get_print_status)."
	case "start_unknown":
		return "Commande envoyée mais l'accusé de l'imprimante est incertain : rien ne sera renvoyé, l'état est réconcilié en lecture (get_print_status)."
	default:
		return "Lancement non effectué (" + r.Start.Outcome + ") : " + r.Start.Note
	}
}

// Started dit si une commande de démarrage est partie (ou en cours d'accusé).
func (r *ApprovalResult) Started() bool {
	if r.Start == nil {
		return false
	}
	switch r.Start.Outcome {
	case "printing", "start_requested", "start_unknown":
		return true
	}
	return false
}
