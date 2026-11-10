package insights

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
)

// Comptage des impressions par pièce (G9, PJ2 et PJ5) : fonctions de lecture réutilisables, calcul pur sur le journal.
//
// Règle D7 : une impression est « imprimée » dès qu'elle a atteint 100 % (reached_100, ou état final COMPLETED pour une
// fiche d'avant la migration), verdict ou pas. Le verdict (success | failed | redo) qualifie la tentative à part ; les
// impressions imprimées sans verdict sont « non jugées ». Une impression annulée ou arrêtée avant 100 % n'est pas
// imprimée (elle compte seulement dans Attempts). Les impressions « external » (déclarées hors Printalk) sont comptées
// mais signalées, et ne comptent ni dans le coût ni dans le temps mesurés.

// PrintCounts : ce que le journal dit d'une pièce (un modèle). Tous les comptes « Units » pondèrent par part_count
// (exemplaires par plaque) ; les autres comptent des impressions.
type PrintCounts struct {
	ModelID  string `json:"model_id"`
	Attempts int    `json:"attempts" jsonschema:"fiches de journal de la pièce, toutes issues confondues (y compris annulées avant 100 %)"`

	Printed       int `json:"printed" jsonschema:"impressions ayant atteint 100 % (D7), verdict ou pas"`
	PrintedUnits  int `json:"printed_units"`
	Success       int `json:"judged_success" jsonschema:"parmi les imprimées : jugées réussies"`
	SuccessUnits  int `json:"judged_success_units"`
	Failed        int `json:"judged_failed" jsonschema:"parmi les imprimées : jugées ratées"`
	FailedUnits   int `json:"judged_failed_units"`
	Redo          int `json:"judged_redo" jsonschema:"parmi les imprimées : jugées à refaire"`
	RedoUnits     int `json:"judged_redo_units"`
	Unjudged      int `json:"unjudged" jsonschema:"parmi les imprimées : sans verdict"`
	UnjudgedUnits int `json:"unjudged_units"`
	External      int `json:"external" jsonschema:"parmi les imprimées : déclarées hors Printalk (niveau déclaré)"`

	TopFailReason  string `json:"top_fail_reason,omitempty" jsonschema:"raison d'échec la plus fréquente parmi les jugées ratées ou à refaire"`
	TopFailReasonN int    `json:"top_fail_reason_n,omitempty"`

	ActualCost    float64 `json:"actual_cost" jsonschema:"somme des coûts réels (calculés) des impressions Printalk de la pièce"`
	Currency      string  `json:"currency,omitempty"`
	ActualSeconds float64 `json:"actual_seconds" jsonschema:"somme des durées réelles mesurées"`
	ActualGrams   float64 `json:"actual_grams" jsonschema:"somme des grammes (pesés ou estimés selon la fiche)"`

	InProgress  int     `json:"in_progress" jsonschema:"tentatives liées non terminées (préparation, découpe, impression en cours...)"`
	PlannedCost float64 `json:"planned_cost,omitempty" jsonschema:"coût estimé des tentatives en cours, à part du coût réel"`
}

// Judged : impressions imprimées portant un verdict.
func (c PrintCounts) Judged() int { return c.Success + c.Failed + c.Redo }

// PrintCountsByModel compte, pour chaque modèle demandé, les impressions du journal. Le journal est d'abord synchronisé
// (les impressions terminées sans fiche reçoivent la leur). Un modèle sans impression renvoie des zéros (jamais d'erreur).
func (s *Service) PrintCountsByModel(ctx context.Context, modelIDs []string) (map[string]*PrintCounts, error) {
	out := map[string]*PrintCounts{}
	ids := make([]string, 0, len(modelIDs))
	for _, id := range modelIDs {
		if id == "" || out[id] != nil {
			continue
		}
		out[id] = &PrintCounts{ModelID: id}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return out, nil
	}
	if _, err := s.SyncJournal(ctx); err != nil {
		return nil, err
	}
	reasons := map[string]map[string]int{}
	for start := 0; start < len(ids); start += 400 {
		end := min(start+400, len(ids))
		chunk := ids[start:end]
		ph := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := s.cfg.Store.DB().QueryContext(ctx, `SELECT model_id, doc, verdict, fail_reason, origin, part_count, reached_100 FROM print_logs WHERE model_id IN (`+ph+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var modelID, doc, verdict, reason, origin string
			var parts, reached int
			if err := rows.Scan(&modelID, &doc, &verdict, &reason, &origin, &parts, &reached); err != nil {
				rows.Close()
				return nil, err
			}
			c := out[modelID]
			if c == nil {
				continue
			}
			var e LogEntry
			_ = json.Unmarshal([]byte(doc), &e)
			if parts < 1 {
				parts = 1
			}
			c.Attempts++
			external := origin == OriginExternal
			if !external {
				if e.ActualCost != nil {
					c.ActualCost += e.ActualCost.Total
					if c.Currency == "" {
						c.Currency = e.ActualCost.Currency
					}
				}
				c.ActualSeconds += e.Actual.Seconds
				c.ActualGrams += e.Actual.Grams
			}
			if !(reached == 1 || e.State == "COMPLETED") {
				continue // annulée ou arrêtée avant 100 % : pas imprimée (D7)
			}
			c.Printed++
			c.PrintedUnits += parts
			if external {
				c.External++
			}
			switch verdict {
			case VerdictSuccess:
				c.Success++
				c.SuccessUnits += parts
			case VerdictFailed:
				c.Failed++
				c.FailedUnits += parts
			case VerdictRedo:
				c.Redo++
				c.RedoUnits += parts
			default:
				c.Unjudged++
				c.UnjudgedUnits += parts
			}
			if (verdict == VerdictFailed || verdict == VerdictRedo) && reason != "" {
				if reasons[modelID] == nil {
					reasons[modelID] = map[string]int{}
				}
				reasons[modelID][reason]++
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	for id, m := range reasons {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { // le plus fréquent, égalités départagées par ordre alphabétique
			if m[keys[i]] != m[keys[j]] {
				return m[keys[i]] > m[keys[j]]
			}
			return keys[i] < keys[j]
		})
		out[id].TopFailReason, out[id].TopFailReasonN = keys[0], m[keys[0]]
	}
	s.addInProgress(ctx, out)
	for _, c := range out {
		c.ActualCost, c.ActualSeconds, c.ActualGrams = round2(c.ActualCost), round2(c.ActualSeconds), round2(c.ActualGrams)
	}
	return out, nil
}

// addInProgress ajoute les tentatives liées non terminées (nombre et coût estimé, à part du coût réel).
func (s *Service) addInProgress(ctx context.Context, out map[string]*PrintCounts) {
	set, err := s.GetSettings(ctx)
	if err != nil {
		return
	}
	jobs, err := s.listJobs(ctx)
	if err != nil {
		return
	}
	for _, j := range jobs {
		c := out[j.ModelID]
		if c == nil || !pendingStates[j.State] {
			continue
		}
		c.InProgress++
		if cp, err := s.planFromJob(ctx, j, 0); err == nil {
			c.PlannedCost = round2(c.PlannedCost + compute(cp, set).Total)
		}
	}
}
