package mcpserver

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/sheets"
)

// Fiche pièce (G9, PJ5 : get_part_sheet) et fiche d'impression (PJ10 : get_print_sheet), plus l'outil réservé aux cartes
// get_sheet_photo (miniature d'un palier du journal photo).
//
// RÈGLE ABSOLUE : aucun de ces outils ne lance, n'arrête ni ne reprend une impression, ne crée ni n'approuve de
// confirmation : lecture seule, calcul déterministe sur le journal, aucune IA, aucun secret ni URL signée dans les sorties.

// SheetPhotoInput : miniature d'un palier (empreinte sha256 lue dans une fiche).
type SheetPhotoInput struct {
	SHA256 string `json:"sha256" jsonschema:"empreinte sha256 (64 hexadécimaux) d'une photo de la caméra rangée avec une impression"`
}

// SheetPhotoOut : métadonnées de l'image renvoyée dans le contenu du résultat.
type SheetPhotoOut struct {
	SHA256    string `json:"sha256"`
	MIME      string `json:"mime"`
	SizeBytes int    `json:"size_bytes"`
}

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

func registerSheetTools(s *mcp.Server, c *core.Core) {
	svc := c.Sheets
	if svc == nil {
		return
	}
	html, _ := viewHTML()
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}

	addViewResource(s, PartViewURI, "part_view", "Fiche pièce",
		"Carte Fiche pièce (get_part_sheet) : compteurs, onglets Versions / Profils / Impressions / Résistance, bandeau de paliers à 10 % par impression, coût et temps cumulés. Lecture seule : ne lance, n'arrête ni ne reprend aucune impression.", html)
	addViewResource(s, PrintSheetViewURI, "print_sheet_view", "Fiche d'impression",
		"Carte Fiche d'impression (get_print_sheet) : bandeau de verdict, profil figé à la découpe, bobines et grammes, durée prévue contre réelle, planche de paliers à 10 %, alertes de caméra, accord humain. Lecture seule.", html)

	addTool(s, &mcp.Tool{
		Name: "get_part_sheet", Title: "Fiche d'une pièce",
		Description: "Tout savoir d'une pièce (un modèle du catalogue) : compteur « imprimée 5 fois (100 % atteint), 4 jugées réussies, 1 jugée ratée (raison la plus fréquente : …) », versions de géométrie suivant parent_id, profils testés (temps, grammes, coût médians par signature de réglages), " +
			"impressions (date, profil, résultat, verdict, raison, bandeau de photos par palier de 10 %, « photo non disponible » avec sa raison), coût et temps cumulés (mesurés ; les impressions déclarées hors Printalk sont listées « déclarée » et exclues du coût réel), dernier profil réussi, dernier test de résistance PAR VERSION (verdict, assumed), projets qui citent la pièce. " +
			"« Imprimée » = l'impression a atteint 100 %, verdict ou pas ; le verdict (réussie, ratée, à refaire) est un jugement humain, affiché à part. Pièce jamais imprimée : zéros et « jamais imprimée », sans erreur. " +
			"include restreint les sections (versions, profiles, prints, strength, projects) ; limit borne la liste des impressions (défaut 20, max 100). Complète get_model_details (inchangé). Lecture seule : calcul déterministe, aucune IA, aucun lancement. Une valeur absente est « non disponible » avec sa raison (champ unavailable).",
		Annotations: ro, Meta: AppToolMeta(PartViewURI),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sheets.PartSheetInput) (*mcp.CallToolResult, sheets.PartSheet, error) {
		sh, err := svc.PartSheet(ctx, in)
		if err != nil {
			return nil, sheets.PartSheet{}, err
		}
		return textResult(partSheetText(sh)), *sh, nil
	})

	addTool(s, &mcp.Tool{
		Name: "get_print_sheet", Title: "Fiche d'une impression",
		Description: "Fiche lisible d'une impression terminée : profil FIGÉ (réglages effectifs relevés à la découpe : ils ne changent pas si le profil est modifié ensuite), bobines et grammes réels (estimés signalés), durée prévue contre réelle (écart en %, « non disponible » sans durée réelle), coût calculé, " +
			"planche de paliers à 10 % (« photo non disponible » avec la raison), alertes de la caméra (score, instant ; « caméra non disponible » est distinct de « aucune alerte »), accord humain de lancement lu dans l'audit, verdict et sa note. Une impression faite hors Printalk est « déclarée ». Le niveau de chaque valeur est indiqué (mesuré, calculé, déclaré). " +
			"Étend get_print_log (inchangé). Lecture seule : aucune IA, aucune commande d'imprimante, aucun secret ni URL signée. Le verdict se donne avec set_print_verdict.",
		Annotations: ro, Meta: AppToolMeta(PrintSheetViewURI),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sheets.PrintSheetInput) (*mcp.CallToolResult, sheets.PrintSheet, error) {
		sh, err := svc.PrintSheet(ctx, in)
		if err != nil {
			return nil, sheets.PrintSheet{}, err
		}
		return textResult(sh.Text), *sh, nil
	})

	if c.Camera != nil {
		addTool(s, &mcp.Tool{
			Name: "get_sheet_photo", Title: "Photo d'un palier (carte)",
			Description: "RÉSERVÉ AUX CARTES FICHE PIÈCE ET FICHE D'IMPRESSION (non destiné au modèle) : renvoie en image la photo de la caméra rangée avec une impression, d'après son empreinte sha256. Lecture seule ; ne sert que les photos de la caméra (jamais un autre fichier).",
			Annotations: ro, Meta: appOnlyMeta(),
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in SheetPhotoInput) (*mcp.CallToolResult, SheetPhotoOut, error) {
			sha := strings.ToLower(strings.TrimSpace(in.SHA256))
			if !sha256Re.MatchString(sha) {
				return nil, SheetPhotoOut{}, fmt.Errorf("sha256 invalide : 64 caractères hexadécimaux attendus")
			}
			jpg, err := c.Camera.OpenPhoto(ctx, sha)
			if err != nil {
				return nil, SheetPhotoOut{}, err
			}
			return imageResult("Photo du journal.", jpg), SheetPhotoOut{SHA256: sha, MIME: "image/jpeg", SizeBytes: len(jpg)}, nil
		})
	}
}

func partSheetText(sh *sheets.PartSheet) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s) : %s.\n", sh.Model.Name, sh.Model.ID, sh.Counts.Text)
	if sh.Totals.Available {
		fmt.Fprintf(&b, "Coût réel cumulé (calculé) : %.2f %s ; temps machine mesuré : %s ; matière : %.0f g.", sh.Totals.ActualCost, sh.Totals.Currency, hms(sh.Totals.ActualSeconds), sh.Totals.ActualGrams)
		if sh.Totals.CostPerSuccess != nil {
			fmt.Fprintf(&b, " Coût par impression jugée réussie : %.2f.", *sh.Totals.CostPerSuccess)
		}
		b.WriteString("\n")
	} else if r := sh.Unavailable["cost"]; r != "" {
		b.WriteString("Coût et temps : non disponibles (" + r + ").\n")
	}
	if sh.Totals.DeclaredCount > 0 {
		fmt.Fprintf(&b, "%d impression(s) déclarée(s) hors Printalk : listées « déclarée », exclues du coût réel mesuré.\n", sh.Totals.DeclaredCount)
	}
	if sh.LastSuccess != nil {
		fmt.Fprintf(&b, "Dernier profil réussi : %s (impression %s).\n", sh.LastSuccess.Label, sh.LastSuccess.PrintJobID)
	}
	if len(sh.Versions) > 0 {
		b.WriteString("Versions :\n")
		for _, v := range sh.Versions {
			st := "jamais testée en résistance"
			if v.Strength != nil {
				st = "dernier test de résistance : " + v.Strength.Verdict
				if v.Strength.Assumed {
					st += " (hypothèses : assumed)"
				}
			}
			fmt.Fprintf(&b, "%s- %s (%s)%s : %s\n", strings.Repeat("  ", v.Depth), v.Label, v.ID, map[bool]string{true: " [courante]", false: ""}[v.IsLatest], st)
		}
	}
	if len(sh.Profiles) > 0 {
		b.WriteString("Profils testés :\n")
		for _, p := range sh.Profiles {
			fmt.Fprintf(&b, "- %s : %d impression(s), %d jugée(s) réussie(s), %d ratée(s)", p.Label, p.Attempts, p.Success, p.Failed)
			if p.MedianSeconds != nil {
				b.WriteString(", " + hms(*p.MedianSeconds) + " médian")
			}
			if p.MedianGrams != nil {
				fmt.Fprintf(&b, ", %.0f g", *p.MedianGrams)
			}
			if p.MedianCost != nil {
				fmt.Fprintf(&b, ", %.2f %s", *p.MedianCost, p.Currency)
			}
			b.WriteString("\n")
		}
	}
	if len(sh.Prints) > 0 {
		b.WriteString("Impressions (les plus récentes d'abord) :\n")
		for _, p := range sh.Prints {
			d := p.FinishedAt.Format("02/01/2006")
			tag := ""
			if p.Declared {
				tag = " [déclarée]"
			}
			fmt.Fprintf(&b, "- %s · %s%s · %s", p.PrintJobID, d, tag, p.VerdictText)
			if p.FailReason != "" {
				b.WriteString(" (" + p.FailReason + ")")
			}
			if !p.Reached100 {
				b.WriteString(" · arrêtée avant 100 %")
			}
			if p.Photos.Available > 0 {
				fmt.Fprintf(&b, " · %d photo(s) de palier", p.Photos.Available)
			} else {
				b.WriteString(" · photo non disponible")
			}
			b.WriteString("\n")
		}
		if sh.Truncated {
			fmt.Fprintf(&b, "… %d impression(s) au total (liste bornée ; augmenter limit).\n", sh.PrintsTotal)
		}
	}
	if len(sh.Projects) > 0 {
		names := make([]string, len(sh.Projects))
		for i, p := range sh.Projects {
			names[i] = p.Name + " (" + p.ID + ")"
		}
		b.WriteString("Projets qui citent la pièce : " + strings.Join(names, ", ") + ".\n")
	}
	b.WriteString("Verdicts : déclarés par l'humain (set_print_verdict). Aucun lancement n'est possible depuis cette fiche.\n")
	return b.String()
}
