package mcpserver

import (
	"fmt"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/localtime"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// Le contenu texte d'un résultat est la seule chose que lit le modèle quand une vue est affichée : il liste donc
// les éléments utiles (noms + identifiants), borné à maxSearchTextRows lignes, jamais un simple compteur.

// filamentListText : un preset par ligne (famille · ligne de produit · nom exact), puis les bobines chargées.
func filamentListText(l *printing.FilamentList, printerID string) string {
	txt := fmt.Sprintf("%d filament(s) compatibles avec %s dans %d famille(s).", l.Total, printerID, len(l.Families))
	rows := 0
fam:
	for _, fam := range l.Families {
		for _, e := range fam.Filaments {
			if rows >= maxSearchTextRows {
				txt += fmt.Sprintf("\n… et %d autre(s) (voir structuredContent.families ; `family` filtre une famille).", l.Total-rows)
				break fam
			}
			txt += fmt.Sprintf("\n- %s · %s · preset « %s »", fam.Family, e.Line, e.Name)
			rows++
		}
	}
	for _, sp := range l.Loaded {
		txt += fmt.Sprintf("\n- chargé en %s : %s %s (%s)", sp.Tray, sp.Material, sp.Color, sp.Source)
	}
	return txt
}

// historyText : une impression par ligne (identifiant, fichier, état, imprimante, date locale).
func historyText(h *printing.History) string {
	txt := fmt.Sprintf("%d impression(s) dans la vue %s (actives %d, terminées %d, échecs %d, archivées %d, corbeille %d).",
		len(h.Jobs), h.View, h.Counts["active"], h.Counts["finished"], h.Counts["failed"], h.Counts["archived"], h.Counts["trash"])
	for i, j := range h.Jobs {
		if i >= maxSearchTextRows {
			txt += fmt.Sprintf("\n… et %d autre(s) (voir structuredContent.jobs).", len(h.Jobs)-i)
			break
		}
		txt += fmt.Sprintf("\n- %s · %s · %s · imprimante %s · %s", j.ID, j.Name, j.State, j.PrinterID, localtime.Full(j.UpdatedAt))
	}
	return txt
}
