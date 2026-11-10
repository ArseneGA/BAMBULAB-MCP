package projects

import (
	"fmt"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/insights"
)

// Calcul pur de l'avancement d'un projet (aucune lecture de base, aucun appel de modèle).
//
// Règle D7 : une pièce est « imprimée » quand le nombre d'unités ayant atteint 100 % (verdict ou pas) atteint sa quantité.
// Le verdict est affiché à part : parmi les unités imprimées, combien jugées réussies, ratées, à refaire, non jugées.

// PartProgress : une pièce d'un projet avec ses comptes.
type PartProgress struct {
	ModelID   string `json:"model_id"`
	ModelName string `json:"model_name,omitempty"`
	Quantity  int    `json:"quantity" jsonschema:"quantité voulue"`
	Role      string `json:"role,omitempty"`
	Implicit  bool   `json:"implicit,omitempty" jsonschema:"pièce adoptée avec la collection du projet (quantité 1), non citée explicitement"`
	Trashed   bool   `json:"trashed,omitempty" jsonschema:"le modèle est dans la corbeille"`

	Printed        int    `json:"printed" jsonschema:"unités imprimées retenues pour l'avancement (100 % atteint, verdict ou pas, au plus la quantité)"`
	JudgedSuccess  int    `json:"judged_success" jsonschema:"unités imprimées jugées réussies"`
	JudgedFailed   int    `json:"judged_failed" jsonschema:"unités imprimées jugées ratées (comptent imprimées, D7)"`
	JudgedRedo     int    `json:"judged_redo" jsonschema:"unités imprimées jugées à refaire (comptent imprimées, D7)"`
	Unjudged       int    `json:"unjudged" jsonschema:"unités imprimées sans verdict"`
	Attempts       int    `json:"attempts" jsonschema:"impressions de la pièce au journal, toutes issues (annulées avant 100 % comprises)"`
	InProgress     int    `json:"in_progress" jsonschema:"tentatives liées non terminées"`
	Satisfied      bool   `json:"satisfied" jsonschema:"imprimée : unités imprimées >= quantité"`
	TopFailReason  string `json:"top_fail_reason,omitempty"`
	TopFailReasonN int    `json:"top_fail_reason_n,omitempty"`
	Text           string `json:"text" jsonschema:"résumé lisible de la pièce"`
}

// Progress : l'avancement d'un ensemble de pièces.
type Progress struct {
	PartsDone     int    `json:"parts_done" jsonschema:"pièces imprimées (quantité atteinte)"`
	PartsTotal    int    `json:"parts_total"`
	UnitsPrinted  int    `json:"units_printed" jsonschema:"unités imprimées (100 % atteint, verdict ou pas)"`
	UnitsTotal    int    `json:"units_total"`
	UnitsSuccess  int    `json:"units_judged_success" jsonschema:"dont jugées réussies"`
	UnitsFailed   int    `json:"units_judged_failed" jsonschema:"imprimées mais jugées ratées"`
	UnitsRedo     int    `json:"units_judged_redo" jsonschema:"imprimées mais jugées à refaire"`
	UnitsUnjudged int    `json:"units_unjudged" jsonschema:"imprimées sans verdict"`
	Complete      bool   `json:"complete" jsonschema:"toutes les pièces sont imprimées"`
	Text          string `json:"text" jsonschema:"ex. 2/3 pièces imprimées, 4/6 unités (dont 3 jugées réussies)"`
}

func plural(n int, one, many string) string {
	if n > 1 {
		return many
	}
	return one
}

// ComputePart calcule la ligne d'une pièce depuis le comptage du journal (nil : jamais imprimée).
func ComputePart(modelID, name string, qty int, role string, implicit bool, c *insights.PrintCounts) PartProgress {
	pp := PartProgress{ModelID: modelID, ModelName: name, Quantity: qty, Role: role, Implicit: implicit}
	if c != nil {
		pp.Printed = min(c.PrintedUnits, qty)
		pp.JudgedSuccess, pp.JudgedFailed, pp.JudgedRedo, pp.Unjudged = c.SuccessUnits, c.FailedUnits, c.RedoUnits, c.UnjudgedUnits
		pp.Attempts, pp.InProgress = c.Attempts, c.InProgress
		pp.TopFailReason, pp.TopFailReasonN = c.TopFailReason, c.TopFailReasonN
	}
	pp.Satisfied = qty > 0 && pp.Printed >= qty
	pp.Text = partText(pp, c)
	return pp
}

func partText(p PartProgress, c *insights.PrintCounts) string {
	if c == nil || (c.Attempts == 0 && c.InProgress == 0) {
		return fmt.Sprintf("0/%d imprimée%s : jamais imprimée", p.Quantity, plural(p.Quantity, "", "s"))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d/%d imprimée%s", p.Printed, p.Quantity, plural(p.Quantity, "", "s"))
	var det []string
	if p.JudgedSuccess > 0 {
		det = append(det, fmt.Sprintf("%d jugée%s réussie%s", p.JudgedSuccess, plural(p.JudgedSuccess, "", "s"), plural(p.JudgedSuccess, "", "s")))
	}
	if p.JudgedFailed > 0 {
		det = append(det, fmt.Sprintf("%d jugée%s ratée%s", p.JudgedFailed, plural(p.JudgedFailed, "", "s"), plural(p.JudgedFailed, "", "s")))
	}
	if p.JudgedRedo > 0 {
		det = append(det, fmt.Sprintf("%d jugée%s à refaire", p.JudgedRedo, plural(p.JudgedRedo, "", "s")))
	}
	if p.Unjudged > 0 {
		det = append(det, fmt.Sprintf("%d non jugée%s", p.Unjudged, plural(p.Unjudged, "", "s")))
	}
	if len(det) > 0 {
		b.WriteString(" (" + strings.Join(det, ", ") + ")")
	}
	if p.InProgress > 0 {
		fmt.Fprintf(&b, " ; %d tentative%s en cours", p.InProgress, plural(p.InProgress, "", "s"))
	}
	if p.Printed == 0 && p.InProgress == 0 {
		b.WriteString(" : aucune impression n'a atteint 100 %")
	}
	return b.String()
}

// Summarize agrège des pièces en un avancement.
func Summarize(parts []PartProgress) Progress {
	var p Progress
	for _, x := range parts {
		p.PartsTotal++
		p.UnitsTotal += x.Quantity
		p.UnitsPrinted += x.Printed
		p.UnitsSuccess += min(x.JudgedSuccess, x.Printed)
		p.UnitsFailed += x.JudgedFailed
		p.UnitsRedo += x.JudgedRedo
		p.UnitsUnjudged += x.Unjudged
		if x.Satisfied {
			p.PartsDone++
		}
	}
	return finishProgress(p)
}

// AddProgress additionne deux avancements (un projet et son sous-projet, chaque pièce comptée une fois par chemin).
func AddProgress(a, b Progress) Progress {
	a.PartsDone += b.PartsDone
	a.PartsTotal += b.PartsTotal
	a.UnitsPrinted += b.UnitsPrinted
	a.UnitsTotal += b.UnitsTotal
	a.UnitsSuccess += b.UnitsSuccess
	a.UnitsFailed += b.UnitsFailed
	a.UnitsRedo += b.UnitsRedo
	a.UnitsUnjudged += b.UnitsUnjudged
	return finishProgress(a)
}

func finishProgress(p Progress) Progress {
	p.Complete = p.PartsTotal > 0 && p.PartsDone == p.PartsTotal
	if p.PartsTotal == 0 {
		p.Text = "aucune pièce"
		return p
	}
	p.Text = fmt.Sprintf("%d/%d pièce%s imprimée%s, %d/%d unité%s", p.PartsDone, p.PartsTotal, plural(p.PartsTotal, "", "s"), plural(p.PartsDone, "", "s"),
		p.UnitsPrinted, p.UnitsTotal, plural(p.UnitsTotal, "", "s"))
	var det []string
	if p.UnitsSuccess > 0 {
		det = append(det, fmt.Sprintf("%d jugée%s réussie%s", p.UnitsSuccess, plural(p.UnitsSuccess, "", "s"), plural(p.UnitsSuccess, "", "s")))
	}
	if p.UnitsFailed+p.UnitsRedo > 0 {
		det = append(det, fmt.Sprintf("%d jugée%s ratée%s ou à refaire", p.UnitsFailed+p.UnitsRedo, plural(p.UnitsFailed+p.UnitsRedo, "", "s"), plural(p.UnitsFailed+p.UnitsRedo, "", "s")))
	}
	if p.UnitsUnjudged > 0 {
		det = append(det, fmt.Sprintf("%d non jugée%s", p.UnitsUnjudged, plural(p.UnitsUnjudged, "", "s")))
	}
	if len(det) > 0 {
		p.Text += " (dont " + strings.Join(det, ", ") + ")"
	}
	return p
}

// SuggestStatus propose un statut d'après l'avancement : printing dès qu'une impression est liée, done quand tout est
// imprimé. Renvoie ("", "") quand rien ne change ou quand aucune suggestion n'a de sens. La suggestion n'est JAMAIS
// appliquée seule : seul update_project change le statut.
func SuggestStatus(current string, archived bool, p Progress, attempts, inProgress int) (status, reason string) {
	if archived || current == StatusAbandoned || p.PartsTotal == 0 {
		return "", ""
	}
	switch {
	case p.Complete && current != StatusDone && current != StatusInUse:
		return StatusDone, "toutes les pièces ont atteint leur quantité imprimée (100 % atteint, verdict ou pas)"
	case !p.Complete && (attempts > 0 || inProgress > 0) && (current == StatusIdea || current == StatusDesign):
		return StatusPrinting, "au moins une impression est liée aux pièces du projet"
	}
	return "", ""
}

// NextAction propose la prochaine étape, en une phrase, d'après les pièces propres du projet et son avancement.
// Déterministe : la première pièce non imprimée dans l'ordre du projet, puis les impressions à juger, puis la clôture.
func NextAction(status string, archived bool, parts []PartProgress, sub int, agg Progress) string {
	if archived {
		return "Projet archivé : le désarchiver avec update_project (archived=false) pour le reprendre."
	}
	if len(parts) == 0 && sub == 0 {
		return "Ajouter des pièces avec add_project_part (ou créer des sous-projets)."
	}
	for _, p := range parts {
		if p.InProgress > 0 {
			return fmt.Sprintf("Une impression de « %s » est en cours : suivre avec get_print_status.", nameOr(p))
		}
	}
	for _, p := range parts {
		if !p.Satisfied {
			return fmt.Sprintf("Imprimer « %s » : %d/%d.", nameOr(p), p.Printed, p.Quantity)
		}
	}
	if agg.UnitsUnjudged > 0 {
		return fmt.Sprintf("Juger %d impression%s terminée%s sans verdict (get_pending_verdicts, set_print_verdict).", agg.UnitsUnjudged, plural(agg.UnitsUnjudged, "", "s"), plural(agg.UnitsUnjudged, "", "s"))
	}
	if agg.UnitsFailed+agg.UnitsRedo > 0 {
		return "Des impressions sont jugées ratées ou à refaire : décider de les refaire (get_part_sheet pour le détail)."
	}
	if agg.Complete && status != StatusDone && status != StatusInUse {
		return "Tout est imprimé : passer le projet en « terminé » avec update_project (status=done) si c'est bien le cas."
	}
	if agg.Complete {
		return "Rien à faire : toutes les pièces sont imprimées."
	}
	return "Poursuivre avec les sous-projets non terminés."
}

func nameOr(p PartProgress) string {
	if p.ModelName != "" {
		return p.ModelName
	}
	return p.ModelID
}

// DescribeCounts rend le compteur d'une pièce en une phrase (fiche pièce, PJ5) : « imprimée 5 fois (100 % atteint),
// 4 jugées réussies, 1 jugée ratée (raison la plus fréquente : stringing, n = 1) » ; « jamais imprimée » sans impression.
func DescribeCounts(c insights.PrintCounts) string {
	if c.Printed == 0 {
		if c.Attempts == 0 && c.InProgress == 0 {
			return "jamais imprimée"
		}
		return fmt.Sprintf("jamais imprimée jusqu'à 100 %% (%d tentative%s)", c.Attempts+c.InProgress, plural(c.Attempts+c.InProgress, "", "s"))
	}
	s := fmt.Sprintf("imprimée %d fois (100 %% atteint)", c.Printed)
	if c.Success > 0 {
		s += fmt.Sprintf(", %d jugée%s réussie%s", c.Success, plural(c.Success, "", "s"), plural(c.Success, "", "s"))
	}
	if c.Failed > 0 {
		s += fmt.Sprintf(", %d jugée%s ratée%s", c.Failed, plural(c.Failed, "", "s"), plural(c.Failed, "", "s"))
	}
	if c.Redo > 0 {
		s += fmt.Sprintf(", %d jugée%s à refaire", c.Redo, plural(c.Redo, "", "s"))
	}
	if c.Unjudged > 0 {
		s += fmt.Sprintf(", %d non jugée%s", c.Unjudged, plural(c.Unjudged, "", "s"))
	}
	if c.TopFailReason != "" {
		s += fmt.Sprintf(" (raison la plus fréquente : %s, n = %d)", c.TopFailReason, c.TopFailReasonN)
	}
	if c.External > 0 {
		s += fmt.Sprintf(" ; dont %d déclarée%s hors Printalk", c.External, plural(c.External, "", "s"))
	}
	return s
}
