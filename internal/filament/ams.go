package filament

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

// Rapprochement entre l'état AMS lu sur l'imprimante et l'inventaire.
//
// Règles (sans jamais écraser la saisie de l'humain) :
//   - l'AMS ne modifie que Observed, rfid_uuid (si vide) et remaining_g quand sa source n'est PAS « human » ;
//   - un bac qui porte une bobine inscrite mais dont la lecture diverge (matière ou couleur) est signalé
//     (match = mismatch), sans rien modifier ;
//   - un bac sans bobine inscrite à cet emplacement : si l'étiquette RFID correspond à une bobine rangée, elle y
//     est chargée (emplacement vide, rien n'est écrasé) ; sinon une proposition (matière + couleur proches) ou
//     « unregistered ».

// Seuils de rapprochement (ΔE CIE76).
const (
	deMatch   = 30.0 // bobine inscrite jugée cohérente avec la lecture de l'AMS
	deSuggest = 20.0 // rapprochement proposé pour une bobine rangée
)

// AMSTrayView est un emplacement lu sur l'imprimante, rapproché de l'inventaire.
type AMSTrayView struct {
	printer.AMSTray
	Spool           *Spool   `json:"spool,omitempty" jsonschema:"bobine de l'inventaire inscrite à cet emplacement"`
	Match           string   `json:"match" jsonschema:"assigned (bobine inscrite et cohérente) | mismatch (inscrite mais l'AMS lit autre chose) | rfid_assigned (chargée d'après l'étiquette RFID) | suggested | unregistered | empty"`
	SuggestedSpool  *Spool   `json:"suggested_spool,omitempty"`
	Warnings        []string `json:"warnings,omitempty"`
	AMSEstimateG    float64  `json:"ams_estimate_g,omitempty" jsonschema:"restant estimé d'après le % de l'AMS et le poids net de l'étiquette ou de la bobine"`
	EffectiveRemain float64  `json:"effective_remaining_g,omitempty" jsonschema:"restant retenu pour les alertes : bobine inscrite sinon estimation AMS"`
}

// AMSView est l'état AMS rapproché.
type AMSView struct {
	PrinterID  string         `json:"printer_id"`
	Reachable  bool           `json:"reachable"`
	Stale      bool           `json:"stale"`
	Error      string         `json:"error,omitempty"`
	Present    bool           `json:"ams_present" jsonschema:"vrai si le rapport de l'imprimante contient un AMS ou une bobine externe"`
	ActiveSlot string         `json:"active_slot,omitempty"`
	Trays      []AMSTrayView  `json:"trays"`
	Humidity   map[string]int `json:"humidity_levels,omitempty" jsonschema:"indice d'humidité (1 sec .. 5 humide) par numéro d'AMS"`
	Loaded     []Spool        `json:"inventory_loaded" jsonschema:"bobines de l'inventaire inscrites sur cette imprimante (dernier état connu si l'imprimante est injoignable)"`
	Notes      []string       `json:"notes,omitempty"`
	ObservedAt time.Time      `json:"observed_at,omitzero"`
}

// GetAMS lit l'état AMS de l'imprimante et le rapproche de l'inventaire. Aucune commande n'est envoyée à
// l'imprimante (lecture du dernier rapport MQTT).
func (s *Service) GetAMS(ctx context.Context, printerID string) (*AMSView, error) {
	if s.d.Printing == nil {
		return nil, fmt.Errorf("%w : chaîne d'impression indisponible", ErrConflict)
	}
	printerID, err := s.d.Printing.ResolvePrinterID(ctx, printerID) // une seule imprimante enregistrée : défaut
	if err != nil {
		return nil, err
	}
	if _, err := s.d.Printing.GetPrinterRecord(ctx, printerID); err != nil {
		return nil, fmt.Errorf("%w : imprimante %q", ErrNotFound, printerID)
	}
	view := &AMSView{PrinterID: printerID, Trays: []AMSTrayView{}}
	ams, st, err := s.d.Printing.PrinterAMS(ctx, printerID)
	view.Reachable, view.Stale, view.ObservedAt = st.Connected, st.Stale, st.UpdatedAt
	loaded, lerr := s.ListSpools(ctx, ListFilter{PrinterID: printerID})
	if lerr != nil {
		return nil, lerr
	}
	for _, sp := range loaded {
		if sp.Slot != "" {
			view.Loaded = append(view.Loaded, sp)
		}
	}
	if view.Loaded == nil {
		view.Loaded = []Spool{}
	}
	switch {
	case err != nil:
		view.Error = err.Error()
		view.Notes = append(view.Notes, "Imprimante injoignable : l'état AMS réel n'est pas disponible, seul l'inventaire est affiché.")
		return view, nil
	case !st.Connected:
		view.Notes = append(view.Notes, "Imprimante non connectée en MQTT : aucun état AMS.")
		return view, nil
	case ams == nil || !ams.Present:
		if st.UpdatedAt.IsZero() {
			view.Notes = append(view.Notes, "Aucun rapport reçu pour l'instant (réessayer dans quelques secondes).")
		} else {
			view.Notes = append(view.Notes, "Le rapport de l'imprimante ne contient pas d'AMS ni de bobine externe.")
		}
		return view, nil
	}
	view.Present, view.ActiveSlot = true, ams.ActiveSlot
	view.Humidity = map[string]int{}
	for _, u := range ams.Units {
		if u.Humidity > 0 {
			view.Humidity[fmt.Sprintf("ams%d", u.Unit)] = u.Humidity
		}
	}
	if st.Stale {
		view.Notes = append(view.Notes, "Rapport périmé : l'état affiché peut ne plus refléter l'imprimante.")
	}
	for _, tr := range ams.Trays() {
		tv, rerr := s.reconcileTray(ctx, printerID, tr, !st.Stale)
		if rerr != nil {
			return nil, rerr
		}
		view.Trays = append(view.Trays, tv)
	}
	for u, h := range view.Humidity {
		if h >= 4 {
			view.Notes = append(view.Notes, fmt.Sprintf("%s : humidité élevée (indice %d/5) — envisager de sécher les bobines sensibles (PETG, nylon, TPU).", u, h))
		}
	}
	return view, nil
}

func (s *Service) reconcileTray(ctx context.Context, printerID string, tr printer.AMSTray, fresh bool) (AMSTrayView, error) {
	tv := AMSTrayView{AMSTray: tr}
	sp, err := s.SpoolAt(ctx, printerID, tr.Slot)
	if err != nil {
		return tv, err
	}
	if tr.Empty {
		tv.Match = "empty"
		if sp != nil {
			tv.Spool = sp
			tv.Warnings = append(tv.Warnings, fmt.Sprintf("L'AMS ne lit aucun filament dans %s mais la bobine %s y est inscrite.", tr.Slot, sp.Label()))
		}
		return tv, nil
	}
	if sp == nil {
		sp = s.bindByRFID(ctx, printerID, tr)
		if sp != nil {
			tv.Match = "rfid_assigned"
		}
	}
	if sp == nil {
		tv.Match = "unregistered"
		if cand := s.suggest(ctx, tr); cand != nil {
			tv.Match, tv.SuggestedSpool = "suggested", cand
			tv.Warnings = append(tv.Warnings, fmt.Sprintf("Bobine proposée : %s (%s). Confirmer avec update_spool(spool_id=%s, printer_id=%s, slot=%s).", cand.Label(), cand.ID, cand.ID, printerID, tr.Slot))
		} else {
			tv.Warnings = append(tv.Warnings, fmt.Sprintf("Aucune bobine de l'inventaire n'est inscrite à %s : créer ou charger une bobine avec update_spool pour suivre son poids.", tr.Slot))
		}
		tv.AMSEstimateG = amsEstimate(nil, tr)
		tv.EffectiveRemain = tv.AMSEstimateG
		return tv, nil
	}
	// Bobine inscrite : cohérence puis mise à jour des seules données observées.
	if tv.Match == "" {
		tv.Match = "assigned"
	}
	if tr.Type != "" && sp.Material != "" && !sameMaterial(tr.Type, sp.Material) {
		tv.Match = "mismatch"
		tv.Warnings = append(tv.Warnings, fmt.Sprintf("L'AMS lit %s dans %s mais la bobine inscrite est du %s (%s).", tr.Type, tr.Slot, sp.Material, sp.Label()))
	}
	if tr.Color != "" && sp.ColorHex != "" {
		if d := colorDistance(tr.Color, sp.ColorHex); d > deMatch {
			tv.Match = "mismatch"
			tv.Warnings = append(tv.Warnings, fmt.Sprintf("Couleur lue par l'AMS (%s) différente de la bobine inscrite (%s, %s) dans %s.", tr.Color, sp.ColorHex, sp.Label(), tr.Slot))
		}
	}
	if fresh {
		upd, err := s.mutate(ctx, sp.ID, func(x *Spool) bool {
			changed := false
			obs := &SpoolObserved{Type: tr.Type, Color: tr.Color, RemainPercent: tr.RemainPercent, Slot: tr.Slot, At: s.now()}
			if x.Observed == nil || x.Observed.Type != obs.Type || x.Observed.Color != obs.Color || x.Observed.RemainPercent != obs.RemainPercent || x.Observed.Slot != obs.Slot {
				x.Observed, changed = obs, true
			}
			if x.RFIDUUID == "" && tr.UUID != "" {
				x.RFIDUUID, changed = tr.UUID, true
			}
			// Le restant ne vient de l'AMS que si l'utilisateur ne l'a pas saisi.
			if tr.RemainPercent >= 0 && x.RemainingSource != "human" {
				base := x.NetWeightG
				if tr.WeightG > 0 && !x.isHuman("net_weight_g") {
					base = tr.WeightG
				}
				if base > 0 {
					g := float64(tr.RemainPercent) / 100 * base
					if x.RemainingSource != "ams" || absf(x.RemainingG-g) > 0.5 {
						x.RemainingG, x.RemainingSource, changed = g, "ams", true
					}
				}
			}
			return changed
		})
		if err != nil {
			return tv, err
		}
		sp = upd
	}
	if sp.RemainingSource == "human" && tr.RemainPercent >= 0 && sp.NetWeightG > 0 {
		ams := float64(tr.RemainPercent) / 100 * sp.NetWeightG
		if absf(ams-sp.RemainingG) > 0.15*sp.NetWeightG {
			tv.Warnings = append(tv.Warnings, fmt.Sprintf("Le restant saisi (%s) diffère de l'estimation de l'AMS (%s) : la saisie est conservée.", fmtG(sp.RemainingG), fmtG(ams)))
		}
	}
	tv.Spool = sp
	tv.AMSEstimateG = amsEstimate(sp, tr)
	tv.EffectiveRemain = sp.RemainingG
	return tv, nil
}

func absf(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// amsEstimate : restant d'après le % de l'AMS et le poids net (étiquette RFID, sinon bobine inscrite).
func amsEstimate(sp *Spool, tr printer.AMSTray) float64 {
	if tr.RemainPercent < 0 {
		return 0
	}
	base := tr.WeightG
	if base <= 0 && sp != nil {
		base = sp.NetWeightG
	}
	return float64(tr.RemainPercent) / 100 * base
}

// bindByRFID charge dans cet emplacement la bobine RANGÉE dont l'étiquette RFID est celle du bac.
func (s *Service) bindByRFID(ctx context.Context, printerID string, tr printer.AMSTray) *Spool {
	if tr.UUID == "" {
		return nil
	}
	all, err := s.ListSpools(ctx, ListFilter{})
	if err != nil {
		return nil
	}
	for _, sp := range all {
		if sp.RFIDUUID != tr.UUID || sp.Slot != "" {
			continue
		}
		upd, err := s.UpdateSpoolInternalLoad(ctx, sp.ID, printerID, tr.Slot)
		if err == nil {
			return upd
		}
	}
	return nil
}

// UpdateSpoolInternalLoad inscrit une bobine rangée dans un emplacement libre (sans passer par la saisie humaine :
// le champ « slot » n'est pas marqué human). Refuse si l'emplacement est déjà pris.
func (s *Service) UpdateSpoolInternalLoad(ctx context.Context, spoolID, printerID, slot string) (*Spool, error) {
	if other, _ := s.SpoolAt(ctx, printerID, slot); other != nil {
		return nil, fmt.Errorf("%w : %s occupé par %s", ErrConflict, slot, other.ID)
	}
	return s.setSlot(ctx, spoolID, printerID, slot)
}

func (s *Service) setSlot(ctx context.Context, spoolID, printerID, slot string) (*Spool, error) {
	for attempt := 0; attempt < 5; attempt++ {
		sp, rev, err := s.loadSpool(ctx, s.d.Store.DB(), spoolID)
		if err != nil {
			return nil, err
		}
		if sp.Slot != "" || sp.Archived {
			return nil, fmt.Errorf("%w : la bobine %s n'est pas rangée", ErrConflict, spoolID)
		}
		sp.PrinterID, sp.Slot, sp.UpdatedAt = printerID, slot, s.now()
		raw := mustJSON(sp)
		res, err := s.d.Store.DB().ExecContext(ctx, `UPDATE spools SET doc=?, slot_key=?, updated_at=?, rev=rev+1 WHERE id=? AND rev=?`,
			raw, slotKey(printerID, slot), formatTime(sp.UpdatedAt), spoolID, rev)
		if err != nil {
			return nil, err // typiquement : index unique (emplacement pris entre-temps)
		}
		if n, _ := res.RowsAffected(); n == 1 {
			_ = s.d.Store.Audit(ctx, actor, "spool.rfid_load", "spool", spoolID, map[string]string{"printer": printerID, "slot": slot})
			sp.compute()
			return sp, nil
		}
	}
	return nil, fmt.Errorf("%w : %s", ErrConflict, spoolID)
}

// suggest cherche, parmi les bobines rangées, la plus proche de la lecture de l'AMS (même matière, couleur proche).
func (s *Service) suggest(ctx context.Context, tr printer.AMSTray) *Spool {
	all, err := s.ListSpools(ctx, ListFilter{})
	if err != nil {
		return nil
	}
	var best *Spool
	bestD := deSuggest
	for i := range all {
		sp := all[i]
		if sp.Slot != "" || sp.Archived || sp.RemainingG <= 0 || !sameMaterial(sp.Material, tr.Type) || sp.ColorHex == "" || tr.Color == "" {
			continue
		}
		if d := colorDistance(sp.ColorHex, tr.Color); d <= bestD {
			best, bestD = &all[i], d
		}
	}
	return best
}

func trayLabel(tr printer.AMSTray) string {
	var p []string
	if tr.SubBrand != "" {
		p = append(p, tr.SubBrand)
	} else if tr.Type != "" {
		p = append(p, tr.Type)
	}
	if tr.Color != "" {
		p = append(p, tr.Color)
	}
	return strings.Join(p, " ")
}
