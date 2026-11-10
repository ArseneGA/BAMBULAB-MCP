package printing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

// Pilotage des accessoires de l'imprimante (N4) : lumières, ventilateurs, vitesse d'impression en cours.
//
// Ce chemin ne lance, n'arrête, ne met en pause ni ne reprend JAMAIS une impression et n'envoie jamais de G-code libre :
// il n'appelle que les trois méthodes de printer.AccessoryController, dont chacune ne publie qu'une commande de la liste
// blanche (printer.ValidateAccessoryPayload). Les capacités du modèle (printer.AccessoriesFor) sont contrôlées AVANT tout
// contact avec l'imprimante : un accessoire absent est refusé « non disponible sur ce modèle ». Chaque commande envoyée
// écrit une entrée d'audit printer.accessory_set (qui, quoi, valeur ; aucun secret).

// AccessoryResult est la réponse de set_printer_light, set_printer_fan et set_print_speed_level.
type AccessoryResult struct {
	PrinterID string `json:"printer_id"`
	Accessory string `json:"accessory" jsonschema:"light:chamber | light:work | fan:part | fan:aux | fan:chamber | speed"`
	Value     string `json:"value" jsonschema:"on | off, pourcentage (0 à 100) ou niveau de vitesse (silent, normal, sport, ludicrous)"`
	Applied   bool   `json:"applied" jsonschema:"vrai si l'imprimante a répondu succès ou si l'état voulu est observé ; faux si la commande est partie sans confirmation (jamais renvoyée)"`
	Message   string `json:"message"`
	// Accessories : état des accessoires dans le dernier rapport de l'imprimante après la commande (peut ne pas encore
	// refléter le changement : la lecture date de quelques instants).
	Accessories *printer.AccessoryState `json:"accessories,omitempty"`
	Available   contracts.AccessoryCaps `json:"available_accessories"`
}

// accessoryTarget retrouve l'imprimante, ses capacités et son contrôleur d'accessoires. check est appelé avec les
// capacités avant tout contact avec l'imprimante.
func (s *Service) accessoryTarget(ctx context.Context, printerID string, check func(PrinterRecord, contracts.AccessoryCaps) error) (printer.AccessoryController, PrinterRecord, contracts.AccessoryCaps, error) {
	rec, err := s.GetPrinterRecord(ctx, printerID)
	if err != nil {
		return nil, rec, contracts.AccessoryCaps{}, err
	}
	caps, known := printer.AccessoriesFor(rec.ModelID)
	if !known {
		if m, merr := s.GetPrinterModel(ctx, rec.ModelID); merr == nil {
			caps, known = printer.AccessoriesFor(m.Name)
		}
	}
	if !known {
		return nil, rec, caps, fmt.Errorf("%w : non disponible sur ce modèle (%q n'est pas dans la table des accessoires)", ErrIncompatible, rec.ModelID)
	}
	if err := check(rec, caps); err != nil {
		return nil, rec, caps, err
	}
	ad, rec, err := s.adapter(ctx, printerID)
	if err != nil {
		return nil, rec, caps, err
	}
	ctl, ok := ad.(printer.AccessoryController)
	if !ok {
		return nil, rec, caps, s.unsupported(ctx, printerID, "le pilotage des accessoires (lumières, ventilateurs, vitesse)")
	}
	return ctl, rec, caps, nil
}

func unavailable(rec PrinterRecord, what string) error {
	return fmt.Errorf("%w : %s non disponible sur ce modèle (%s)", ErrIncompatible, what, rec.ModelID)
}

// runAccessory exécute la commande, l'audite et formule le résultat.
func (s *Service) runAccessory(ctx context.Context, who string, rec PrinterRecord, caps contracts.AccessoryCaps, accessory, value, what string, run func(context.Context) error) (*AccessoryResult, error) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err := run(cctx)
	outcome := "ok"
	res := &AccessoryResult{PrinterID: rec.ID, Accessory: accessory, Value: value, Available: caps}
	switch {
	case err == nil:
		res.Applied = true
		res.Message = what + " : appliqué."
	case errors.Is(err, printer.ErrStateNotObserved):
		outcome = "unconfirmed"
		res.Message = what + " : commande envoyée mais l'imprimante n'a pas confirmé dans le délai. Rien n'est renvoyé ; vérifier l'état avec get_printer_status."
	default:
		outcome = "failed"
	}
	_ = s.d.Store.Audit(ctx, who, "printer.accessory_set", "printer", rec.ID, map[string]any{"accessory": accessory, "value": value, "outcome": outcome})
	if err != nil && outcome == "failed" {
		switch {
		case errors.Is(err, printer.ErrBadState):
			return nil, fmt.Errorf("%w : %v", ErrState, err)
		case errors.Is(err, printer.ErrNotConnected):
			return nil, fmt.Errorf("%w : imprimante %s non connectée : %v", ErrUnavailable, rec.Name, err)
		case errors.Is(err, printer.ErrAccessoryNotAllowed):
			return nil, invalidf("%v", err)
		}
		return nil, fmt.Errorf("%s en échec : %w", what, err)
	}
	if ad, _, aerr := s.adapter(ctx, rec.ID); aerr == nil {
		sctx, scancel := context.WithTimeout(ctx, 3*time.Second)
		if st, serr := ad.Status(sctx); serr == nil {
			res.Accessories = st.Accessories
		}
		scancel()
	}
	return res, nil
}

// SetPrinterLight allume ou éteint une lumière (chamber | work). who = « mcp » ou « web » (audit).
func (s *Service) SetPrinterLight(ctx context.Context, printerID, light string, on bool, who string) (*AccessoryResult, error) {
	node, err := printer.ParseLight(light)
	if err != nil {
		return nil, invalidf("%v", err)
	}
	ctl, rec, caps, err := s.accessoryTarget(ctx, printerID, func(rec PrinterRecord, c contracts.AccessoryCaps) error {
		if !printer.HasLight(c, node) {
			return unavailable(rec, "lumière "+map[printer.LightNode]string{printer.LightChamber: "de chambre", printer.LightWork: "de la tête (work)"}[node])
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	v := map[bool]string{true: "on", false: "off"}[on]
	return s.runAccessory(ctx, who, rec, caps, "light:"+strings.TrimSuffix(string(node), "_light"), v, "Lumière "+string(node)+" "+v,
		func(c context.Context) error { return ctl.SetLight(c, node, on) })
}

// SetPrinterFan règle un ventilateur (part | aux | chamber) en pourcentage de 0 à 100.
func (s *Service) SetPrinterFan(ctx context.Context, printerID, fan string, percent int, who string) (*AccessoryResult, error) {
	kind, err := printer.ParseFan(fan)
	if err != nil {
		return nil, invalidf("%v", err)
	}
	if _, err := printer.FanPWM(percent); err != nil {
		return nil, invalidf("%v", err)
	}
	ctl, rec, caps, err := s.accessoryTarget(ctx, printerID, func(rec PrinterRecord, c contracts.AccessoryCaps) error {
		if !printer.HasFan(c, kind) {
			return unavailable(rec, "ventilateur "+string(kind))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.runAccessory(ctx, who, rec, caps, "fan:"+string(kind), fmt.Sprint(percent), fmt.Sprintf("Ventilateur %s à %d %%", kind, percent),
		func(c context.Context) error { return ctl.SetFan(c, kind, percent) })
}

// SetPrintSpeedLevel règle la vitesse de l'impression EN COURS (silent | normal | sport | ludicrous, ou 1 à 4).
func (s *Service) SetPrintSpeedLevel(ctx context.Context, printerID, level string, who string) (*AccessoryResult, error) {
	lv, err := printer.ParseSpeed(level)
	if err != nil {
		return nil, invalidf("%v", err)
	}
	ctl, rec, caps, err := s.accessoryTarget(ctx, printerID, func(rec PrinterRecord, c contracts.AccessoryCaps) error { return nil })
	if err != nil {
		return nil, err
	}
	return s.runAccessory(ctx, who, rec, caps, "speed", lv.Name(), "Vitesse "+lv.Name(),
		func(c context.Context) error { return ctl.SetSpeed(c, lv) })
}

// withAccessories ajoute les accessoires du modèle (calculés, jamais stockés) ; inchangé si le modèle est inconnu de la table.
func withAccessories(m contracts.PrinterModel) contracts.PrinterModel {
	caps, known := printer.AccessoriesFor(m.ID)
	if !known {
		caps, known = printer.AccessoriesFor(m.Name)
	}
	if known {
		m.Accessories = &caps
	}
	return m
}
