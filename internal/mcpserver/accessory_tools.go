package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// Outils de pilotage des accessoires (N4) : lumières, ventilateurs, vitesse de l'impression en cours.
// AUCUN ne lance, n'arrête, ne met en pause ni ne reprend une impression, et aucun n'envoie de G-code libre : chaque appel
// publie une seule commande de la liste blanche (internal/printer/accessories.go, docs/ACCESSOIRES.md). Pas d'approbation
// humaine : l'effet est réversible, sans risque thermique ni mécanique (le modèle n'a aucun moyen de chauffer ou de déplacer).

// SetPrinterLightInput règle une lumière.
type SetPrinterLightInput struct {
	PrinterID string `json:"printer_id,omitempty" jsonschema:"imprimante visée ; facultatif quand une seule imprimante est enregistrée (sinon list_printers)"`
	Light     string `json:"light" jsonschema:"chamber (lumière de chambre) | work (lumière de la tête, si le modèle en a une)"`
	On        bool   `json:"on" jsonschema:"true = allumer, false = éteindre"`
}

// SetPrinterFanInput règle un ventilateur.
type SetPrinterFanInput struct {
	PrinterID string `json:"printer_id,omitempty" jsonschema:"imprimante visée ; facultatif quand une seule imprimante est enregistrée (sinon list_printers)"`
	Fan       string `json:"fan" jsonschema:"part (refroidissement de la pièce) | aux (auxiliaire) | chamber (chambre), selon le modèle"`
	Percent   int    `json:"percent" jsonschema:"vitesse de 0 (arrêt) à 100 (maximum)"`
}

// SetPrintSpeedInput règle la vitesse de l'impression en cours.
type SetPrintSpeedInput struct {
	PrinterID string `json:"printer_id,omitempty" jsonschema:"imprimante visée ; facultatif quand une seule imprimante est enregistrée (sinon list_printers)"`
	Level     string `json:"level" jsonschema:"silent (1) | normal (2) | sport (3) | ludicrous (4) ; seulement pendant une impression"`
}

// AddAccessoryTools enregistre set_printer_light, set_printer_fan et set_print_speed_level. Une seule ligne dans New().
func AddAccessoryTools(s *mcp.Server, p *printing.Service) {
	f, t := false, true
	// Effet physique, réversible et idempotent (poser une valeur) ; jamais destructif.
	ann := &mcp.ToolAnnotations{DestructiveHint: &f, IdempotentHint: true, OpenWorldHint: &t}

	addTool(s, &mcp.Tool{
		Name: "set_printer_light", Title: "Allumer ou éteindre une lumière de l'imprimante",
		Description: "Allume ou éteint la lumière de la chambre (light=chamber) ou de la tête (light=work) de l'imprimante, à tout moment (même pendant une impression). " +
			"Refus clair « non disponible sur ce modèle » si le modèle n'a pas cette lumière (list_printer_models, get_printer_status : available_accessories). " +
			"Ne lance, n'arrête ni ne reprend aucune impression. Une trace d'audit est écrite.",
		Annotations: ann,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SetPrinterLightInput) (*mcp.CallToolResult, printing.AccessoryResult, error) {
		id, err := p.ResolvePrinterID(ctx, in.PrinterID)
		if err != nil {
			return nil, printing.AccessoryResult{}, err
		}
		r, err := p.SetPrinterLight(ctx, id, in.Light, in.On, "mcp")
		if err != nil {
			return nil, printing.AccessoryResult{}, err
		}
		return textResult(r.Message), *r, nil
	})

	addTool(s, &mcp.Tool{
		Name: "set_printer_fan", Title: "Régler un ventilateur de l'imprimante",
		Description: "Règle un ventilateur en pourcentage (0 à 100) : part (refroidissement de la pièce), aux (auxiliaire) ou chamber (chambre), selon le modèle. " +
			"Refus clair « non disponible sur ce modèle » si le ventilateur n'existe pas, et refus pendant la préparation (mise à niveau, calibration). " +
			"Pendant une impression, le prochain réglage du G-code peut reprendre la main. Ne lance, n'arrête ni ne reprend aucune impression ; aucun G-code libre. Une trace d'audit est écrite.",
		Annotations: ann,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SetPrinterFanInput) (*mcp.CallToolResult, printing.AccessoryResult, error) {
		id, err := p.ResolvePrinterID(ctx, in.PrinterID)
		if err != nil {
			return nil, printing.AccessoryResult{}, err
		}
		r, err := p.SetPrinterFan(ctx, id, in.Fan, in.Percent, "mcp")
		if err != nil {
			return nil, printing.AccessoryResult{}, err
		}
		return textResult(r.Message), *r, nil
	})

	addTool(s, &mcp.Tool{
		Name: "set_print_speed_level", Title: "Régler la vitesse de l'impression en cours",
		Description: "Règle le niveau de vitesse de l'impression EN COURS : silent (1), normal (2), sport (3) ou ludicrous (4). Refusé tant qu'aucune impression n'est en cours " +
			"(préparation, impression ou pause requises). Ne lance, n'arrête ni ne reprend aucune impression. Une trace d'audit est écrite.",
		Annotations: ann,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SetPrintSpeedInput) (*mcp.CallToolResult, printing.AccessoryResult, error) {
		id, err := p.ResolvePrinterID(ctx, in.PrinterID)
		if err != nil {
			return nil, printing.AccessoryResult{}, err
		}
		r, err := p.SetPrintSpeedLevel(ctx, id, in.Level, "mcp")
		if err != nil {
			return nil, printing.AccessoryResult{}, err
		}
		return textResult(r.Message), *r, nil
	})
}
