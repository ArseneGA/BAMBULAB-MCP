package filament

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// ColorWish est la couleur voulue pour un filament du fichier (pièce -> couleur choisie par l'utilisateur).
type ColorWish struct {
	FilamentSlot int    `json:"filament_slot" jsonschema:"numéro (1..n) du filament dans le fichier découpé (les pièces d'une couleur partagent un filament)"`
	ColorHex     string `json:"color_hex,omitempty" jsonschema:"#RRGGBB voulu ; absent = couleur du filament dans le fichier"`
	Material     string `json:"material,omitempty" jsonschema:"matière voulue (PLA, PETG...) ; absente = celle du fichier"`
	Part         string `json:"part,omitempty" jsonschema:"nom de la pièce ou de la zone (information, repris dans la réponse)"`
}

// PlanColorsInput : de quoi proposer l'affectation emplacement AMS -> couleur.
type PlanColorsInput struct {
	PrintJobID string                        `json:"print_job_id" jsonschema:"impression découpée (execute_headless_slice) sur un 3MF multi-pièces / multi-couleurs"`
	PrinterID  string                        `json:"printer_id,omitempty" jsonschema:"imprimante visée (défaut : celle de l'impression)"`
	Plate      int                           `json:"plate,omitempty"`
	Colors     []ColorWish                   `json:"colors,omitempty" jsonschema:"affectation pièce -> couleur choisie par l'utilisateur (défaut : couleurs du fichier)"`
	Fixed      []printing.FilamentAssignment `json:"fixed,omitempty" jsonschema:"affectations imposées par l'utilisateur (filament -> emplacement) : conservées telles quelles, les autres filaments sont répartis autour"`
}

// ColorSlotPlan est la proposition pour un filament du fichier.
type ColorSlotPlan struct {
	FilamentSlot   int            `json:"filament_slot"`
	Parts          string         `json:"parts,omitempty"`
	WantedColor    string         `json:"wanted_color,omitempty"`
	WantedType     string         `json:"wanted_type,omitempty"`
	WantedOfficial *OfficialColor `json:"wanted_official,omitempty" jsonschema:"couleur officielle Bambu la plus proche de la couleur voulue (catalogue embarqué) : à charger ou acheter si aucune bobine ne convient"`
	Tray           string         `json:"tray,omitempty" jsonschema:"emplacement proposé ; vide si aucune bobine ne convient"`
	SpoolID        string         `json:"spool_id,omitempty"`
	SpoolLabel     string         `json:"spool_label,omitempty"`
	LoadedColor    string         `json:"loaded_color,omitempty" jsonschema:"couleur de la bobine proposée (lue par l'AMS, sinon inventaire)"`
	DeltaE         float64        `json:"delta_e" jsonschema:"écart de couleur ΔE (CIE76) entre la couleur voulue et la bobine : < 12 exact, < 30 proche, < 55 approximatif"`
	Quality        string         `json:"quality" jsonschema:"exact | proche | approximatif | aucune"`
	Pinned         bool           `json:"pinned,omitempty" jsonschema:"vrai si imposé par l'utilisateur"`
	NeededG        float64        `json:"needed_g" jsonschema:"masse extrudée pour ce filament (pièce + purge)"`
	PurgeG         float64        `json:"purge_g" jsonschema:"dont matière jetée (purge de buse + tour)"`
	PurgeCost      *float64       `json:"purge_cost,omitempty" jsonschema:"coût de la matière jetée pour ce filament"`
	Note           string         `json:"note,omitempty"`
}

// PurgeCost résume le coût de la purge.
type PurgeCost struct {
	WasteG   float64  `json:"waste_g"`
	Cost     *float64 `json:"cost,omitempty" jsonschema:"coût de la matière jetée ; absent si aucun prix n'est connu"`
	Basis    string   `json:"basis" jsonschema:"spool_price_per_kg (prix/kg des bobines proposées) | cost_service (calcul de coût) | unknown"`
	Complete bool     `json:"complete" jsonschema:"faux si le prix d'au moins un filament est inconnu (coût partiel)"`
}

// ColorPlan est la réponse de plan_colors.
type ColorPlan struct {
	PrintJobID  string                        `json:"print_job_id"`
	PrinterID   string                        `json:"printer_id"`
	Plate       int                           `json:"plate"`
	Slots       []ColorSlotPlan               `json:"slots"`
	Assignments []printing.FilamentAssignment `json:"assignments" jsonschema:"à passer tel quel à upload_print_artifact (affectation matière liée à la confirmation humaine)"`
	AMSMapping  []int                         `json:"ams_mapping,omitempty" jsonschema:"ams_mapping qui en résulte (position = filament - 1, valeur = unité*4 + bac, 254 = externe, -1 = inutilisé)"`
	UseAMS      bool                          `json:"use_ams"`
	Unmatched   []int                         `json:"unmatched_filaments,omitempty" jsonschema:"filaments sans bobine convenable : charger la couleur indiquée ou choisir une autre couleur"`
	Complete    bool                          `json:"complete" jsonschema:"vrai si chaque filament a un emplacement et qu'aucune alerte bloquante ne reste"`
	Purge       *PurgeReport                  `json:"purge,omitempty"`
	PurgeCost   *PurgeCost                    `json:"purge_cost,omitempty"`
	Check       *CheckOutput                  `json:"filament_check,omitempty" jsonschema:"suffisance des bobines proposées"`
	Warnings    []string                      `json:"warnings,omitempty"`
	Next        string                        `json:"next"`
}

type candidate struct {
	tray      string
	typ       string
	color     string
	spoolID   string
	label     string
	remaining float64
}

// PlanColors propose l'affectation filament du fichier -> emplacement AMS (depuis l'état AMS et l'inventaire),
// construit l'ams_mapping, mesure la purge dans le G-code et chiffre la matière jetée. N'envoie rien.
func (s *Service) PlanColors(ctx context.Context, in PlanColorsInput) (*ColorPlan, error) {
	if s.d.Printing == nil {
		return nil, fmt.Errorf("%w : chaîne d'impression indisponible", ErrConflict)
	}
	if in.PrintJobID == "" {
		return nil, invalidf("print_job_id requis")
	}
	j, err := s.d.Printing.GetJob(ctx, in.PrintJobID)
	if err != nil {
		return nil, fmt.Errorf("%w : %v", ErrNotFound, err)
	}
	if in.Plate > 0 {
		j.Plate = in.Plate
	}
	if j.Plate == 0 {
		j.Plate = 1
	}
	pid := in.PrinterID
	if pid == "" {
		pid = j.PrinterID
	}
	if pid == "" {
		return nil, invalidf("printer_id requis")
	}
	p := plateUse(j)
	if p == nil {
		return nil, fmt.Errorf("%w : l'impression %s n'a pas encore de mesures par plaque (état %s) : attendre la fin de la découpe", ErrConflict, j.ID, j.State)
	}
	plan := &ColorPlan{PrintJobID: j.ID, PrinterID: pid, Plate: j.Plate, Slots: []ColorSlotPlan{}, Assignments: []printing.FilamentAssignment{}}

	// 1. candidats : bacs lus sur l'imprimante (ou, à défaut, bobines inscrites de l'inventaire).
	cands, warn := s.candidates(ctx, pid)
	plan.Warnings = append(plan.Warnings, warn...)

	// 2. souhaits par filament.
	wishes := map[int]ColorWish{}
	for _, w := range in.Colors {
		if w.FilamentSlot < 1 {
			return nil, invalidf("colors : filament_slot doit être ≥ 1")
		}
		if w.ColorHex != "" && normHex(w.ColorHex) == "" {
			return nil, invalidf("colors : couleur %q invalide (#RRGGBB)", w.ColorHex)
		}
		wishes[w.FilamentSlot] = w
	}
	used := map[string]bool{}
	pinned := map[int]string{}
	for _, a := range in.Fixed {
		if _, _, _, err := parseTray(a.Tray); err != nil {
			return nil, invalidf("fixed : %v", err)
		}
		if used[a.Tray] {
			return nil, invalidf("fixed : emplacement %s imposé deux fois", a.Tray)
		}
		used[a.Tray] = true
		pinned[a.FilamentSlot] = a.Tray
	}

	// 3. purge mesurée dans le G-code de l'archive.
	var purge *PurgeReport
	if j.ArtifactSHA256 != "" && s.d.Blobs != nil {
		if path, err := s.d.Blobs.Path(j.ArtifactSHA256); err == nil {
			if pr, err := ReadPurge(path, j.Plate); err == nil {
				purge = pr
				plan.Purge = pr
			} else {
				plan.Warnings = append(plan.Warnings, "Purge non mesurée : "+err.Error())
			}
		}
	}
	purgeOf := func(slot int) float64 {
		if purge == nil {
			return 0
		}
		for _, f := range purge.PerFilament {
			if f.Slot == slot {
				return f.WasteG
			}
		}
		return 0
	}

	// 4. appariement : épingles d'abord, puis meilleurs couples (ΔE croissant), une bobine par filament.
	type pair struct {
		slot int
		c    int
		de   float64
	}
	var fils []slicer.FilamentUse
	fils = append(fils, p.Filaments...)
	sort.Slice(fils, func(a, b int) bool { return fils[a].Slot < fils[b].Slot })
	chosen := map[int]int{} // filament -> index candidat
	for _, f := range fils {
		if tray, ok := pinned[f.Slot]; ok {
			for i, c := range cands {
				if c.tray == tray {
					chosen[f.Slot] = i
				}
			}
		}
	}
	var pairs []pair
	for _, f := range fils {
		if _, ok := chosen[f.Slot]; ok {
			continue
		}
		if _, ok := pinned[f.Slot]; ok {
			continue
		}
		w := wishes[f.Slot]
		wantType, wantColor := orStr(w.Material, f.Type), orStr(normHex(w.ColorHex), normHex(f.WantedColor()))
		for i, c := range cands {
			if used[c.tray] || !sameMaterial(c.typ, wantType) {
				continue
			}
			de := 0.0
			if wantColor != "" && c.color != "" {
				de = colorDistance(wantColor, c.color)
			} else if wantColor != "" {
				de = 40 // couleur de la bobine inconnue
			}
			pairs = append(pairs, pair{f.Slot, i, de})
		}
	}
	sort.SliceStable(pairs, func(a, b int) bool { return pairs[a].de < pairs[b].de })
	takenC := map[int]bool{}
	for _, i := range chosen {
		takenC[i] = true
	}
	for _, pr := range pairs {
		if _, ok := chosen[pr.slot]; ok || takenC[pr.c] || colorQuality(pr.de) == "aucune" {
			continue
		}
		chosen[pr.slot] = pr.c
		takenC[pr.c] = true
	}

	var totalWaste float64
	var costSum float64
	costKnown, allPriced, usedSettings := false, true, false
	for _, f := range fils {
		w := wishes[f.Slot]
		sl := ColorSlotPlan{FilamentSlot: f.Slot, Parts: w.Part, WantedColor: orStr(normHex(w.ColorHex), normHex(f.WantedColor())), WantedType: orStr(w.Material, f.Type),
			NeededG: round2(f.Grams), PurgeG: round2(purgeOf(f.Slot)), Quality: "aucune", DeltaE: 999}
		if sl.WantedColor != "" {
			sl.WantedOfficial = nearestOfficial(sl.WantedColor, sl.WantedType, 30)
		}
		if tray, ok := pinned[f.Slot]; ok {
			sl.Pinned = true
			if _, ok := chosen[f.Slot]; !ok {
				// emplacement imposé mais sans bobine lue : on le garde (l'utilisateur sait), sans couleur connue
				sl.Tray, sl.Note = tray, "emplacement imposé ; aucune bobine lue à cet emplacement"
				sl.Quality = "inconnue"
			}
		}
		if i, ok := chosen[f.Slot]; ok {
			c := cands[i]
			sl.Tray, sl.SpoolID, sl.SpoolLabel, sl.LoadedColor = c.tray, c.spoolID, c.label, c.color
			if sl.WantedColor != "" && c.color != "" {
				sl.DeltaE = round2(colorDistance(sl.WantedColor, c.color))
				sl.Quality = colorQuality(sl.DeltaE)
			} else {
				sl.DeltaE, sl.Quality = 0, "inconnue"
			}
			if sl.Pinned && !sameMaterial(c.typ, sl.WantedType) {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("Filament %d : %s imposé sur %s mais l'emplacement porte du %s.", f.Slot, sl.WantedType, c.tray, c.typ))
			}
		}
		if sl.Tray == "" {
			plan.Unmatched = append(plan.Unmatched, f.Slot)
			sl.Note = fmt.Sprintf("Aucune bobine %s %s chargée : charger cette couleur dans un emplacement libre, ou choisir une autre couleur.", sl.WantedType, sl.WantedColor)
			if o := sl.WantedOfficial; o != nil {
				sl.Note += fmt.Sprintf(" Couleur officielle Bambu la plus proche : %s %s (%s, ΔE %.0f).", o.Family, o.Name, o.Hex, o.DeltaE)
			}
		} else {
			plan.Assignments = append(plan.Assignments, printing.FilamentAssignment{FilamentSlot: f.Slot, Tray: sl.Tray, MaterialID: orStr(sl.SpoolLabel, orStr(sl.WantedType, "filament")+" "+sl.LoadedColor)})
			if sl.Quality == "approximatif" {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("Filament %d : la bobine de %s est une approximation de %s (ΔE %.0f).", f.Slot, sl.Tray, sl.WantedColor, sl.DeltaE))
			}
			// coût de la matière jetée
			if sl.PurgeG > 0 {
				totalWaste += sl.PurgeG
				if c, viaSettings, ok := s.purgeCost(ctx, pid, sl); ok {
					usedSettings = usedSettings || viaSettings
					sl.PurgeCost = &c
					costSum += c
					costKnown = true
				} else {
					allPriced = false
				}
			}
		}
		plan.Slots = append(plan.Slots, sl)
	}
	if len(plan.Assignments) > 0 {
		plan.AMSMapping, plan.UseAMS = printing.BuildAMSMapping(plan.Assignments)
	}
	if purge != nil {
		pc := &PurgeCost{WasteG: round2(totalWaste), Basis: "unknown", Complete: allPriced}
		if costKnown {
			c := round2(costSum)
			pc.Cost = &c
			pc.Basis = "spool_price_per_kg"
			if usedSettings {
				pc.Basis = "cost_service" // au moins un emplacement chiffré aux réglages de prix (bobine sans prix)
			}
		}
		if purge.WastePercent >= 40 {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("La purge représente %.0f %% de la matière de la plaque (%.1f g jetés sur %.1f g) : regrouper les pièces par couleur sur la même couche, ou réduire le nombre de changements, réduit nettement ce gaspillage.", purge.WastePercent, purge.WasteG, purge.TotalG))
		}
		plan.PurgeCost = pc
	}
	if len(plan.Assignments) > 0 && len(plan.Unmatched) == 0 {
		if chk, err := s.CheckFilament(ctx, CheckInput{PrintJobID: j.ID, PrinterID: pid, Plate: j.Plate, Assignments: plan.Assignments}); err == nil {
			plan.Check = chk
			if chk.Verdict == "insufficient" {
				plan.Warnings = append(plan.Warnings, "Une bobine proposée risque de manquer : "+chk.Summary)
			}
		}
	}
	plan.Complete = len(plan.Unmatched) == 0 && len(plan.Assignments) > 0 && (plan.Check == nil || plan.Check.Verdict != "insufficient")
	if plan.Complete {
		plan.Next = "Présenter cette affectation à l'utilisateur ; s'il l'accepte, la passer telle quelle (assignments) à upload_print_artifact. Rien n'est lancé : le lancement exige son accord sur la page de confirmation, et toute modification de l'affectation après coup invalide la confirmation."
	} else {
		plan.Next = "Affectation incomplète ou risquée : régler les alertes (charger la couleur manquante, changer de bobine ou de couleur), puis relancer plan_colors."
	}
	return plan, nil
}

// purgeCost chiffre la purge d'un emplacement avec la même priorité que estimate_cost : prix/kg réel de la bobine
// proposée d'abord, puis (bobine sans prix ou absente) les réglages de prix de l'utilisateur (famille de la matière).
// Le second résultat vaut true quand ce sont les réglages qui ont servi.
func (s *Service) purgeCost(ctx context.Context, printerID string, sl ColorSlotPlan) (float64, bool, bool) {
	if sl.SpoolID != "" {
		if pr, ok := s.PriceForSpool(ctx, sl.SpoolID); ok && pr.PricePerKg > 0 {
			return round2(sl.PurgeG / 1000 * pr.PricePerKg), false, true
		}
	}
	s.mu.Lock()
	cf := s.costFn
	s.mu.Unlock()
	if cf != nil {
		if c, ok := cf(ctx, sl.WantedType, sl.PurgeG); ok {
			return round2(c), true, true
		}
	}
	return 0, false, false
}

// candidates : bacs lus sur l'imprimante rapprochés de l'inventaire ; à défaut, bobines inscrites.
func (s *Service) candidates(ctx context.Context, printerID string) ([]candidate, []string) {
	var out []candidate
	var warn []string
	seen := map[string]bool{}
	if v, err := s.GetAMS(ctx, printerID); err == nil && v.Present {
		for _, tv := range v.Trays {
			if tv.Empty {
				continue
			}
			c := candidate{tray: tv.Slot, typ: tv.Type, color: tv.Color, label: trayLabel(tv.AMSTray)}
			if tv.Spool != nil {
				c.spoolID, c.label, c.remaining = tv.Spool.ID, tv.Spool.Label(), tv.Spool.RemainingG
				if c.typ == "" {
					c.typ = tv.Spool.Material
				}
				if c.color == "" {
					c.color = tv.Spool.ColorHex
				}
			}
			out = append(out, c)
			seen[tv.Slot] = true
		}
	} else if err == nil {
		warn = append(warn, "État AMS indisponible : l'affectation s'appuie sur l'inventaire seul.")
	}
	loaded, _ := s.ListSpools(ctx, ListFilter{PrinterID: printerID})
	for _, sp := range loaded {
		if sp.Slot == "" || seen[sp.Slot] || sp.Archived {
			continue
		}
		out = append(out, candidate{tray: sp.Slot, typ: sp.Material, color: sp.ColorHex, spoolID: sp.ID, label: sp.Label(), remaining: sp.RemainingG})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].tray < out[b].tray })
	if len(out) == 0 {
		warn = append(warn, "Aucune bobine chargée connue sur cette imprimante : lire l'AMS (get_ams_state) ou inscrire les bobines (update_spool).")
	}
	return out, warn
}

func orStr(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func parseTray(t string) (id int, ext bool, unit int, err error) {
	if t == "ext" {
		return 254, true, 0, nil
	}
	if !slotRe.MatchString(t) {
		return 0, false, 0, fmt.Errorf("emplacement %q invalide (ams1:1..ams4:4 ou ext)", t)
	}
	var u, b int
	fmt.Sscanf(t, "ams%d:%d", &u, &b)
	return (u-1)*4 + (b - 1), false, u, nil
}
