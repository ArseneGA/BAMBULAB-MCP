package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/filament"
)

// Outils bobines / AMS / multicolore / file d'attente (internal/filament).
// Aucun de ces outils ne lance, ne confirme ni n'arrête une impression : la file prépare et informe, chaque
// démarrage reste un geste humain sur la page /confirm (docs/SECURITY.md).

// OptPrinterIDInput : imprimante visée ; facultative quand une seule est enregistrée.
type OptPrinterIDInput struct {
	PrinterID string `json:"printer_id,omitempty" jsonschema:"imprimante visée ; facultatif quand une seule imprimante est enregistrée (sinon list_printers)"`
}

// ListSpoolsInput filtre l'inventaire.
type ListSpoolsInput struct {
	PrinterID       string `json:"printer_id,omitempty" jsonschema:"seulement les bobines chargées sur cette imprimante"`
	Material        string `json:"material,omitempty" jsonschema:"famille de matière (PLA, PETG...)"`
	LowOnly         bool   `json:"low_only,omitempty" jsonschema:"seulement les bobines presque vides ou vides"`
	IncludeArchived bool   `json:"include_archived,omitempty"`
}

// ListSpoolsOut est l'inventaire.
type ListSpoolsOut struct {
	Spools   []filament.Spool  `json:"spools"`
	Settings filament.Settings `json:"alert_margin" jsonschema:"marge d'alerte de manque actuellement appliquée"`
	Note     string            `json:"note"`
}

// SpoolOut est une bobine.
type SpoolOut struct {
	Spool filament.Spool `json:"spool"`
	Note  string         `json:"note"`
}

// QueueListOut est la file d'attente.
type QueueListOut struct {
	Queue []filament.QueueEntry `json:"queue"`
	Note  string                `json:"note"`
}

// QueueEntryOut est un élément de file.
type QueueEntryOut struct {
	Entry filament.QueueEntry `json:"entry"`
	Note  string              `json:"note"`
}

// ReorderInput place des éléments en tête de file.
type ReorderInput struct {
	QueueIDs []string `json:"queue_ids" jsonschema:"identifiants d'éléments de file dans l'ordre voulu ; ils passent en tête, les autres suivent dans leur ordre actuel"`
}

// QueueIDInput identifie un élément de file.
type QueueIDInput struct {
	QueueID string `json:"queue_id"`
}

// ListQueueInput : option d'historique.
type ListQueueInput struct {
	IncludeFinished bool `json:"include_finished,omitempty" jsonschema:"ajoute les impressions terminées, en échec ou retirées"`
}

// MarginInput règle l'alerte de manque.
type MarginInput struct {
	MarginPercent float64 `json:"margin_percent" jsonschema:"marge en % du besoin (défaut 10)"`
	MarginGrams   float64 `json:"margin_grams" jsonschema:"marge fixe en grammes (défaut 5)"`
}

// MarginOut : réglage enregistré.
type MarginOut struct {
	Settings filament.Settings `json:"alert_margin"`
	Note     string            `json:"note"`
}

// registerAMSTools enregistre les outils du chantier bobines / AMS / file. Une seule ligne dans New().
func registerAMSTools(s *mcp.Server, fs *filament.Service) {
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	f := false
	write := &mcp.ToolAnnotations{DestructiveHint: &f}
	idem := &mcp.ToolAnnotations{DestructiveHint: &f, IdempotentHint: true}

	addTool(s, &mcp.Tool{
		Name: "list_spools", Title: "Inventaire des bobines",
		Description: "Bobines de filament : matière, marque, couleur, poids restant estimé (et sa source : saisi, AMS, décompté), prix/kg, dates d'ouverture et de séchage, emplacement. " +
			"Le restant se met à jour tout seul (lecture de l'AMS, décompte après chaque impression) sans jamais écraser ce que l'utilisateur a saisi.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ListSpoolsInput) (*mcp.CallToolResult, ListSpoolsOut, error) {
		sp, err := fs.ListSpools(ctx, filament.ListFilter{PrinterID: in.PrinterID, Material: in.Material, LowOnly: in.LowOnly, IncludeArchived: in.IncludeArchived})
		if err != nil {
			return nil, ListSpoolsOut{}, err
		}
		low := 0
		for _, x := range sp {
			if x.Status == "low" || x.Status == "empty" {
				low++
			}
		}
		note := fmt.Sprintf("%d bobine(s), dont %d presque vide(s) ou vide(s).", len(sp), low)
		if len(sp) == 0 {
			note += " Inventaire vide : créer des bobines avec update_spool (ou lire l'AMS avec get_ams_state)."
		}
		return textResult(note), ListSpoolsOut{Spools: sp, Settings: fs.GetSettings(ctx), Note: note}, nil
	})

	addTool(s, &mcp.Tool{
		Name: "update_spool", Title: "Créer ou modifier une bobine",
		Description: "Sans spool_id : crée une bobine (material obligatoire). Avec spool_id : modifie les champs fournis (poids restant mesuré, prix/kg, couleur, dates d'ouverture/séchage, notes). " +
			"printer_id + slot (ams1:1..ams4:4, ext) charge la bobine dans un emplacement ; unassign la range ; archived l'archive. " +
			"Tout champ fourni est une saisie de l'utilisateur : la lecture de l'AMS ne l'écrase jamais.",
		Annotations: idem,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in filament.UpdateInput) (*mcp.CallToolResult, SpoolOut, error) {
		sp, err := fs.UpdateSpool(ctx, in)
		if err != nil {
			return nil, SpoolOut{}, err
		}
		note := fmt.Sprintf("Bobine %s (%s) : %s restant", sp.ID, sp.Label(), fmt.Sprintf("%.0f g", sp.RemainingG))
		if sp.Slot != "" {
			note += ", chargée en " + sp.Slot
		}
		return textResult(note + "."), SpoolOut{Spool: *sp, Note: note + "."}, nil
	})

	addTool(s, &mcp.Tool{
		Name: "get_ams_state", Title: "État de l'AMS",
		Description: "Lit l'AMS réel dans le dernier rapport MQTT de l'imprimante (emplacements, matière et couleur lues, % restant si l'AMS le fournit, emplacement actif, humidité) " +
			"et le rapproche de l'inventaire : bobine inscrite, divergence de lecture, bobine proposée, emplacement non inscrit. " +
			"Aucune commande n'est envoyée à l'imprimante. Met à jour les données observées des bobines (jamais leurs saisies).",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &f, IdempotentHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in OptPrinterIDInput) (*mcp.CallToolResult, *filament.AMSView, error) {
		v, err := fs.GetAMS(ctx, in.PrinterID)
		if err != nil {
			return nil, nil, err
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "AMS de %s : ", v.PrinterID)
		switch {
		case v.Error != "":
			sb.WriteString("imprimante injoignable (" + v.Error + ").")
		case !v.Present:
			sb.WriteString("aucun AMS dans le rapport.")
		default:
			fmt.Fprintf(&sb, "%d emplacement(s)", len(v.Trays))
			if v.ActiveSlot != "" {
				sb.WriteString(", actif : " + v.ActiveSlot)
			}
			warn := 0
			for _, t := range v.Trays {
				warn += len(t.Warnings)
			}
			fmt.Fprintf(&sb, ", %d avertissement(s).", warn)
		}
		return textResult(sb.String()), v, nil
	})

	addTool(s, &mcp.Tool{
		Name: "check_filament_for_print", Title: "Les bobines suffisent-elles ?",
		Description: "AVANT l'envoi : confronte la masse que l'impression va extruder par filament (pièce + purge, mesurée par Bambu Studio) au restant des bobines chargées, avec une marge configurable. " +
			"Verdict par filament : ok, marginal, insufficient (il manque X g, avec bobines de remplacement possibles), unknown. Lecture seule.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in filament.CheckInput) (*mcp.CallToolResult, *filament.CheckOutput, error) {
		o, err := fs.CheckFilament(ctx, in)
		if err != nil {
			return nil, nil, err
		}
		return textResult(o.Summary), o, nil
	})

	addTool(s, &mcp.Tool{
		Name: "set_filament_margin", Title: "Régler la marge d'alerte de manque",
		Description: "Enregistre la marge (en % du besoin et en grammes) sous laquelle check_filament_for_print et la validation d'une impression signalent qu'une bobine risque de manquer.",
		Annotations: idem,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in MarginInput) (*mcp.CallToolResult, MarginOut, error) {
		st, err := fs.SetSettings(ctx, filament.Settings{MarginPercent: in.MarginPercent, MarginGrams: in.MarginGrams})
		if err != nil {
			return nil, MarginOut{}, err
		}
		note := fmt.Sprintf("Marge d'alerte : %.0f %% + %.0f g.", st.MarginPercent, st.MarginGrams)
		return textResult(note), MarginOut{Settings: st, Note: note}, nil
	})

	addTool(s, &mcp.Tool{
		Name: "plan_colors", Title: "Plan multicolore AMS",
		Description: "Pour une impression multicolore découpée : propose l'affectation filament -> emplacement AMS d'après les bobines chargées (lues sur l'AMS et dans l'inventaire) et les couleurs voulues " +
			"(couleurs du fichier, ou pièce -> couleur choisie par l'utilisateur dans colors ; fixed impose certains choix). Renvoie les assignments à passer à upload_print_artifact, l'ams_mapping, " +
			"la purge MESURÉE dans le G-code (masse jetée par filament, tour de purge) et son coût, et signale les couleurs sans bobine. Ne lance rien.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in filament.PlanColorsInput) (*mcp.CallToolResult, *filament.ColorPlan, error) {
		p, err := fs.PlanColors(ctx, in)
		if err != nil {
			return nil, nil, err
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Plan multicolore : %d filament(s), %d affecté(s)", len(p.Slots), len(p.Assignments))
		if len(p.Unmatched) > 0 {
			fmt.Fprintf(&sb, ", %d sans bobine convenable", len(p.Unmatched))
		}
		if p.Purge != nil {
			fmt.Fprintf(&sb, ". Purge : %.1f g jetés sur %.1f g (%.0f %%)", p.Purge.WasteG, p.Purge.TotalG, p.Purge.WastePercent)
			if p.PurgeCost != nil && p.PurgeCost.Cost != nil {
				fmt.Fprintf(&sb, ", coût %.2f", *p.PurgeCost.Cost)
			}
		}
		sb.WriteString(".")
		return textResult(sb.String()), p, nil
	})

	addTool(s, &mcp.Tool{
		Name: "queue_print", Title: "Mettre une impression en file",
		Description: "Ajoute une impression (print_job_id) en fin de file. La file attend la découpe, dépose le fichier sur l'imprimante quand elle est libre, demande la confirmation et prévient « suivante prête, plateau à vider ». " +
			"Elle ne lance JAMAIS rien : chaque impression exige l'accord de l'utilisateur sur sa page de confirmation, y compris la suivante après la fin d'une impression.",
		Annotations: write,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in filament.QueueInput) (*mcp.CallToolResult, QueueEntryOut, error) {
		e, err := fs.QueuePrint(ctx, in)
		if err != nil {
			return nil, QueueEntryOut{}, err
		}
		note := fmt.Sprintf("Impression %s en file (rang %d). Elle ne démarrera que sur accord humain à la page de confirmation.", e.PrintJobID, e.Position)
		return textResult(note), QueueEntryOut{Entry: *e, Note: note}, nil
	})

	addTool(s, &mcp.Tool{
		Name: "list_print_queue", Title: "File d'attente d'impressions",
		Description: "Éléments de la file dans l'ordre, avec leur statut (découpe, affectation manquante, en attente, envoi, prêt = attend l'accord humain, lancée, terminée) et, pour un élément prêt, le lien de confirmation à donner à l'utilisateur.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ListQueueInput) (*mcp.CallToolResult, QueueListOut, error) {
		_ = fs.PrepareQueue(ctx) // rafraîchit ; ne lance rien
		q, err := fs.ListQueue(ctx, in.IncludeFinished)
		if err != nil {
			return nil, QueueListOut{}, err
		}
		ready := 0
		for _, e := range q {
			if e.State == filament.QReady {
				ready++
			}
		}
		note := fmt.Sprintf("%d élément(s) en file, %d prêt(s) à lancer (en attente de l'accord de l'utilisateur).", len(q), ready)
		return textResult(note), QueueListOut{Queue: q, Note: note}, nil
	})

	addTool(s, &mcp.Tool{
		Name: "reorder_queue", Title: "Réordonner la file",
		Description: "Place les éléments indiqués en tête de file dans l'ordre donné ; les autres suivent dans leur ordre actuel.",
		Annotations: idem,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ReorderInput) (*mcp.CallToolResult, QueueListOut, error) {
		q, err := fs.ReorderQueue(ctx, in.QueueIDs)
		if err != nil {
			return nil, QueueListOut{}, err
		}
		note := fmt.Sprintf("File réordonnée (%d élément(s)).", len(q))
		return textResult(note), QueueListOut{Queue: q, Note: note}, nil
	})

	addTool(s, &mcp.Tool{
		Name: "remove_from_queue", Title: "Retirer de la file",
		Description: "Retire un élément de la file (l'impression n'est ni supprimée ni arrêtée ; le fichier déjà envoyé reste sur l'imprimante). Refusé si l'impression est lancée.",
		Annotations: idem,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in QueueIDInput) (*mcp.CallToolResult, QueueEntryOut, error) {
		e, err := fs.RemoveFromQueue(ctx, in.QueueID)
		if err != nil {
			return nil, QueueEntryOut{}, err
		}
		note := "Retiré de la file : " + e.PrintJobID + "."
		return textResult(note), QueueEntryOut{Entry: *e, Note: note}, nil
	})
}
