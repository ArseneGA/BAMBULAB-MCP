package mcpserver

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// uiExtensionID : clé sous laquelle un client MCP annonce la prise en charge des vues MCP Apps dans
// `initialize` (capabilities.extensions). Vérifié dans @modelcontextprotocol/ext-apps 2.0.4 :
// EXTENSION_ID = "io.modelcontextprotocol/ui", avec { mimeTypes: ["text/html;profile=mcp-app", …] }.
const uiExtensionID = "io.modelcontextprotocol/ui"

// clientViewSupport lit, dans l'`initialize` que CE serveur a reçu, si le client annonce les vues MCP Apps.
// Attention : derrière `mcp-remote` ou le pont `bambu-mcp`, c'est l'`initialize` relayé qui arrive ici ; s'il ne contient
// pas l'extension, la réponse est « non annoncé » alors que l'hôte final peut quand même afficher les vues (le rendu ne
// dépend pas de ce que le serveur voit : l'hôte lit _meta.ui.resourceUri dans tools/list). Le serveur n'en fait donc
// qu'un choix de TEXTE (mode dégradé), jamais un refus ni un changement de sécurité.
func clientViewSupport(ss *mcp.ServerSession) (announced bool, client string) {
	if ss == nil {
		return false, ""
	}
	p := ss.InitializeParams()
	if p == nil {
		return false, ""
	}
	if p.ClientInfo != nil {
		client = strings.TrimSpace(p.ClientInfo.Name + " " + p.ClientInfo.Version)
	}
	if p.Capabilities == nil {
		return false, client
	}
	raw, ok := p.Capabilities.Extensions[uiExtensionID]
	if !ok {
		return false, client
	}
	m, _ := raw.(map[string]any)
	list, _ := m["mimeTypes"].([]any)
	for _, v := range list {
		if s, _ := v.(string); s == AppMIMEType {
			return true, client
		}
	}
	return false, client
}

// logClientInitialized journalise ce que le client a annoncé : preuve à lire dans les journaux du serveur quand une
// vue ne s'affiche pas (« ui_extension=false » = l'hôte ou le relais n'a pas annoncé les vues).
func logClientInitialized(c *slog.Logger, req *mcp.InitializedRequest) {
	if req == nil || req.Session == nil {
		return
	}
	p := req.Session.InitializeParams()
	if p == nil {
		return
	}
	var exts []string
	if p.Capabilities != nil {
		for k := range p.Capabilities.Extensions {
			exts = append(exts, k)
		}
		sort.Strings(exts)
	}
	ann, client := clientViewSupport(req.Session)
	c.Info("client MCP initialisé", "client", client, "ui_extension", ann, "extensions", strings.Join(exts, ","), "protocol", p.ProtocolVersion)
}

// confirmationText : texte du résultat de request_print_confirmation. Court et clair pour l'humain ; les consignes au
// modèle sont ici et pas dans le contenu structuré. views : le client a annoncé les vues MCP Apps.
func confirmationText(v *printing.ConfirmationView, baseURL string, views bool) string {
	b := v.Binding
	bl := "oui"
	if !b.BedLeveling {
		bl = "NON (désactivée)"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Confirmation %s créée : %s sur %s, plateau %d, empreinte %s, mise à niveau du plateau : %s. Valable jusqu'à %s.\n",
		v.ConfirmationID, b.File, b.PrinterName, b.Plate, shortSHA(b.SHA256), bl, v.ExpiresAtLocal)
	onoff := func(v bool) string {
		if v {
			return "oui"
		}
		return "NON"
	}
	fmt.Fprintf(&sb, "Options de démarrage approuvées : calibration du débit %s, calibration des vibrations %s, timelapse %s.\n", onoff(b.FlowCali), onoff(b.VibrationCali), map[bool]string{true: "OUI", false: "non"}[b.Timelapse])
	for _, a := range b.Adhesion {
		sb.WriteString("AVERTISSEMENT (non bloquant, à dire à l'utilisateur) : " + a + "\n")
	}
	for _, a := range b.Overhang {
		sb.WriteString("AVERTISSEMENT FORT (non bloquant, à dire à l'utilisateur AVANT qu'il lance, il doit en prendre acte) : " + a + "\n")
	}
	for _, a := range b.Slender {
		sb.WriteString("AVERTISSEMENT (non bloquant, à dire à l'utilisateur AVANT qu'il lance, il doit l'acquitter) : " + a + "\n")
	}
	for _, l := range b.Filament {
		switch {
		case l.NeedsRemaining:
			sb.WriteString(fmt.Sprintf("AVERTISSEMENT MATIÈRE (non bloquant, à acquitter) : filament %d (%s) : besoin %.0f g (%.0f g avec marge), restant INCONNU (bobine non déclarée). DEMANDE à l'utilisateur combien de grammes restent sur cette bobine, puis rappelle request_print_confirmation avec remaining_g=[{\"filament_slot\": %d, \"grams\": <restant>}].\n", l.FilamentSlot, l.Tray, l.NeededG, l.RequiredG, l.FilamentSlot))
		case l.Verdict == "insufficient" || l.Verdict == "marginal":
			sb.WriteString("AVERTISSEMENT MATIÈRE (non bloquant, à dire à l'utilisateur) : " + l.Detail + "\n")
		}
	}
	if views {
		sb.WriteString("La confirmation s'affiche dans la conversation (carte avec photo du plateau et températures) : dis à l'utilisateur de la compléter (cocher « plateau vide », saisir son code de lancement personnel DANS LA CARTE, Lancer). ")
		sb.WriteString("Ne lui demande JAMAIS son code dans la conversation. Lien de secours, seulement s'il ne voit aucune carte : " + v.ConfirmURL + " .\n")
	} else {
		sb.WriteString("Ce client n'a pas annoncé l'affichage de vues : il est possible qu'aucune carte n'apparaisse. Si l'utilisateur ne voit pas de carte, donne-lui ce lien (page de son navigateur enregistré : photo du plateau, case « plateau vide », bouton Lancer) : " + v.ConfirmURL + " . ")
		sb.WriteString("Sinon, la carte suffit (code de lancement saisi dans la carte ; ne lui demande JAMAIS son code dans la conversation).\n")
	}
	sb.WriteString("Tu ne peux pas confirmer à sa place. Dès qu'il approuve, le SERVEUR LANCE L'IMPRESSION LUI-MÊME : n'appelle PAS start_print et n'attends pas qu'il te dise « c'est bon » ; suis get_print_status. ")
	sb.WriteString("start_print ne sert qu'à reprendre (confirmation_id + idempotency_key) si le démarrage automatique a échoué (imprimante pas prête) dans les 2 minutes de l'accord. Sans code défini, la carte invite à le définir sur " + baseURL + "/setup/launch-code.")
	return sb.String()
}
