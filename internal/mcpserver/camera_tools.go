package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/camera"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/localtime"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/notify"
)

// Outils caméra, détection d'échec et notifications (chantier D). Aucun ne lance ni n'arrête une impression :
// la détection ALERTE (photo + lien vers la page humaine d'arrêt), les secrets des canaux se saisissent sur une page humaine.

// CameraPrinterInput : l'imprimante peut être omise s'il n'y en a qu'une.
type CameraPrinterInput struct {
	PrinterID string `json:"printer_id,omitempty" jsonschema:"imprimante ; facultatif s'il n'y en a qu'une"`
}

// SnapshotOut : métadonnées d'un instantané (l'image elle-même est renvoyée comme contenu image).
type SnapshotOut struct {
	PrinterID string    `json:"printer_id"`
	TakenAt   time.Time `json:"taken_at"`
	SHA256    string    `json:"sha256"`
	SizeBytes int       `json:"size_bytes"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	PhotoURL  string    `json:"photo_url,omitempty" jsonschema:"toujours vide : un instantané à la demande n'est pas conservé (seules les photos de fin et d'alerte le sont)"`
	Note      string    `json:"note"`
}

// AnalyzeOut : image + rapport du détecteur local.
type AnalyzeOut struct {
	PrinterID  string         `json:"printer_id"`
	PrintJobID string         `json:"print_job_id,omitempty"`
	TakenAt    time.Time      `json:"taken_at"`
	PhotoURL   string         `json:"photo_url,omitempty"`
	Report     *camera.Report `json:"detector_report,omitempty" jsonschema:"dernier rapport du détecteur local (score, signaux expliqués) ; absent si aucune impression n'est analysée"`
	ReportAt   time.Time      `json:"detector_report_at,omitzero"`
	Note       string         `json:"note"`
}

// DetectionOut : réglages courants.
type DetectionOut struct {
	Settings camera.Settings `json:"settings"`
	Note     string          `json:"note"`
}

// NotifyOut : canaux (sans secret) et lien de saisie des secrets.
type NotifyOut struct {
	Channels  []notify.ChannelView `json:"channels"`
	SetupURL  string               `json:"setup_url" jsonschema:"page où l'HUMAIN saisit les secrets (URL ntfy, URL et clé du webhook, mot de passe SMTP) ; tu ne les vois jamais"`
	Events    []string             `json:"known_events"`
	EnvHint   string               `json:"env_hint"`
	Note      string               `json:"note"`
	RecentLog []notify.LogEntry    `json:"recent_log,omitempty"`
}

// ConfigureNotificationsInput : canaux existants à activer, désactiver ou dont choisir les évènements. Ni création, ni
// suppression, ni destination (hôte, port, utilisateur, destinataires, URL), ni secret : tout cela se fait sur la page humaine.
type ConfigureNotificationsInput struct {
	Channels []notify.ChannelPatch `json:"channels,omitempty" jsonschema:"canaux EXISTANTS à activer/désactiver ou dont régler les évènements"`
}

// TestNotificationInput : canal visé (tous les canaux actifs si omis).
type TestNotificationInput struct {
	Channel string `json:"channel,omitempty" jsonschema:"nom du canal ; omis = tous les canaux actifs"`
}

// TestNotificationOut : issue de l'essai.
type TestNotificationOut struct {
	Results []notify.Result `json:"results"`
	Note    string          `json:"note"`
}

func imageResult(text string, jpg []byte) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}, &mcp.ImageContent{Data: jpg, MIMEType: "image/jpeg"}}}
}

func resolvePrinter(ctx context.Context, c *core.Core, id string) (string, error) {
	if id != "" {
		return id, nil
	}
	ps, err := c.Printing.ListPrinters(ctx)
	if err != nil {
		return "", err
	}
	if len(ps) != 1 {
		return "", fmt.Errorf("printer_id requis (%d imprimantes enregistrées)", len(ps))
	}
	return ps[0].ID, nil
}

// AddCameraTools enregistre les outils caméra / détection / notifications.
func AddCameraTools(s *mcp.Server, c *core.Core) {
	f := false
	obs := &mcp.ToolAnnotations{DestructiveHint: &f, IdempotentHint: false}
	idem := &mcp.ToolAnnotations{DestructiveHint: &f, IdempotentHint: true}

	addTool(s, &mcp.Tool{
		Name: "get_camera_snapshot", Title: "Instantané de la caméra",
		Description: "Prend une photo avec la caméra de l'imprimante (P1/A1 : flux JPEG du port 6000, via le code d'accès enregistré) et la renvoie comme IMAGE que tu peux voir, plus ses métadonnées. " +
			"Pour vérifier le plateau, suivre une impression ou juger une pièce. X1/P2/H2 (RTSP) ne sont pas pris en charge. Échoue clairement si le LAN liveview est désactivé ou la caméra injoignable.",
		Annotations: obs,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CameraPrinterInput) (*mcp.CallToolResult, SnapshotOut, error) {
		id, err := resolvePrinter(ctx, c, in.PrinterID)
		if err != nil {
			return nil, SnapshotOut{}, err
		}
		p, err := c.Camera.Snapshot(ctx, id)
		if err != nil {
			return nil, SnapshotOut{}, err
		}
		cfg := struct{ Width, Height int }{p.Width, p.Height}
		out := SnapshotOut{PrinterID: id, TakenAt: p.TakenAt, SHA256: p.SHA256, SizeBytes: p.Size, Width: cfg.Width, Height: cfg.Height,
			Note: "Image prise à " + localtime.Full(p.TakenAt) + ". Une photo ne prouve pas que le plateau est vide : l'attestation reste humaine (page de confirmation)."}
		return imageResult(fmt.Sprintf("Instantané de %s pris à %s (%dx%d).", id, p.TakenAt.Format(time.RFC3339), cfg.Width, cfg.Height), p.JPEG), out, nil
	})

	addTool(s, &mcp.Tool{
		Name: "analyze_print_frame", Title: "Analyser l'image de l'impression",
		Description: "Renvoie l'image la plus récente de la caméra pendant une impression, avec le dernier rapport du détecteur local (score, signaux expliqués : masse de filaments hors de la pièce, empreinte vidée, arrêt de progression des couches, erreurs HMS). " +
			"JUGE L'IMAGE TOI-MÊME dans la conversation (spaghetti, pièce décollée, buse noyée de filament). Ne peut rien arrêter : si l'impression est perdue, appelle cancel_print pour obtenir le lien de la page d'arrêt que l'utilisateur validera.",
		Annotations: obs,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CameraPrinterInput) (*mcp.CallToolResult, AnalyzeOut, error) {
		id, err := resolvePrinter(ctx, c, in.PrinterID)
		if err != nil {
			return nil, AnalyzeOut{}, err
		}
		a, err := c.Camera.AnalyzeLatest(ctx, id)
		if err != nil {
			return nil, AnalyzeOut{}, err
		}
		out := AnalyzeOut{PrinterID: id, PrintJobID: a.PrintJobID, TakenAt: a.TakenAt, Report: a.Report, ReportAt: a.ReportAt, Note: a.Note}
		if a.Photo != nil {
			out.PhotoURL = a.Photo.URL
		}
		txt := a.Note
		if a.Report != nil {
			txt += fmt.Sprintf("\nDétecteur local : score %.2f / seuil %.2f (%s), couche %d.", a.Report.Score, a.Report.Threshold, a.Report.Level, a.Report.Layer)
			for _, g := range a.Report.Signals {
				if g.Contribution > 0 {
					txt += fmt.Sprintf("\n- %s : %s", g.Name, g.Detail)
				}
			}
		}
		return imageResult(txt, a.JPEG), out, nil
	})

	addTool(s, &mcp.Tool{
		Name: "configure_failure_detection", Title: "Régler la détection d'échec",
		Description: "Lit ou règle la détection d'échec par la caméra : activation, intervalle des instantanés (5 à 3600 s), seuil d'alerte (0.1 à 1), sensibilité, persistance, minutes sans progression de couche. " +
			"Sans argument : renvoie les réglages. La détection ne fait QU'ALERTER (photo + lien de la page d'arrêt) ; elle n'arrête jamais une impression.",
		Annotations: idem,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in camera.SettingsPatch) (*mcp.CallToolResult, DetectionOut, error) {
		st, err := c.Camera.UpdateSettings(ctx, in)
		if err != nil {
			return nil, DetectionOut{}, err
		}
		note := fmt.Sprintf("Surveillance %s, un instantané toutes les %g s pendant PRINTING, alerte à partir du score %.2f sur %d analyses consécutives. Aucun arrêt automatique.",
			map[bool]string{true: "active", false: "désactivée"}[st.Enabled], st.IntervalSeconds, st.Detector.Threshold, st.Detector.Consecutive)
		return textResult(note), DetectionOut{Settings: st, Note: note}, nil
	})

	addTool(s, &mcp.Tool{
		Name: "configure_notifications", Title: "Configurer les notifications",
		Description: "Liste les canaux de notification (ntfy, webhook signé HMAC, e-mail SMTP en TLS) et permet d'ACTIVER ou DÉSACTIVER un canal existant et de choisir ses évènements (print_completed, print_failed, detection_alert, start_unknown, printer_offline, queue_event). " +
			"Ne crée, ne supprime et ne redirige aucun canal : l'hôte, le port, l'utilisateur SMTP, les destinataires, les URL et les secrets se saisissent sur setup_url (/setup/notifications), page réservée à l'HUMAIN. Sans argument : liste les canaux (sans secret).",
		Annotations: idem,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ConfigureNotificationsInput) (*mcp.CallToolResult, NotifyOut, error) {
		out := NotifyOut{SetupURL: c.Notify.SetupURL(), Events: notify.AllEvents,
			EnvHint: "Un secret peut aussi venir de la variable BAMBU_NOTIFY_<CANAL>_<CHAMP> (ou …_FILE vers un fichier), ex. " + notify.EnvName("mon canal", "url") + "."}
		for _, ch := range in.Channels {
			if _, err := c.Notify.PatchChannel(ctx, ch); err != nil {
				return nil, NotifyOut{}, err
			}
		}
		chs, err := c.Notify.ListChannels(ctx)
		if err != nil {
			return nil, NotifyOut{}, err
		}
		out.Channels = chs
		out.RecentLog, _ = c.Notify.Log(ctx, 10)
		var todo []string
		for _, ch := range chs {
			if !ch.Ready {
				todo = append(todo, ch.Name)
			}
		}
		out.Note = "Canaux : " + fmt.Sprint(len(chs)) + "."
		if len(todo) > 0 {
			out.Note += " À créer ou compléter par l'utilisateur sur " + out.SetupURL + " : " + strings.Join(todo, ", ") + " (secrets manquants). Ensuite lance test_notification."
		}
		return textResult(out.Note), out, nil
	})

	addTool(s, &mcp.Tool{
		Name: "test_notification", Title: "Tester une notification",
		Description: "Envoie une notification d'essai (sans secret) à un canal, ou à tous les canaux actifs, et dit pour chacun si l'envoi a réussi (erreur expurgée des URL). Un canal sans ses secrets échoue en indiquant la page de saisie.",
		Annotations: obs,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in TestNotificationInput) (*mcp.CallToolResult, TestNotificationOut, error) {
		rs, err := c.Notify.Test(ctx, in.Channel)
		if err != nil {
			if errors.Is(err, notify.ErrNotFound) {
				return nil, TestNotificationOut{}, err
			}
			return nil, TestNotificationOut{}, err
		}
		out := TestNotificationOut{Results: rs}
		if len(rs) == 0 {
			out.Note = "Aucun canal actif. L'utilisateur en crée sur la page /setup/notifications."
		} else {
			ok := 0
			for _, r := range rs {
				if r.OK {
					ok++
				}
			}
			out.Note = fmt.Sprintf("%d/%d envoi(s) réussi(s).", ok, len(rs))
		}
		return textResult(out.Note), out, nil
	})
}
