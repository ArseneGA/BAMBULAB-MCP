package mcpserver

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/filament"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/goals"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/insights"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/library"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/notify"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// Constructeurs des cartes-formulaires et outils de soumission réservés à l'application (voir forms.go pour le
// principe et les règles de sécurité). Chaque « apply* » est LA fonction qui exécute l'action : l'outil visible du
// modèle (appelé avec tous ses champs) et l'outil submit_* de la carte l'appellent tous les deux, donc la même
// validation côté serveur s'applique.

func formView(s *mcp.Server, html string) {
	addViewResource(s, FormViewURI, "form_view", "Formulaire",
		"Carte-formulaire pré-remplie (ajout d'imprimante, prix, notifications, bobine, objectifs, gabarit) : l'humain complète et valide dans la conversation. Ne lance, n'arrête ni ne reprend aucune impression, ne reçoit aucun secret.", html)
}

// AddFormTools enregistre la vue des cartes-formulaires et les outils de soumission (app-only). Les outils visibles du
// modèle restent dans leurs fichiers d'origine (print_tools.go, insight_tools.go…) qui appellent les constructeurs ci-dessous.
func AddFormTools(s *mcp.Server, c *core.Core) {
	html, _ := viewHTML()
	formView(s, html)
	f := false
	idem := &mcp.ToolAnnotations{DestructiveHint: &f, IdempotentHint: true}
	write := &mcp.ToolAnnotations{DestructiveHint: &f}
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}

	if p := c.Printing; p != nil {
		addTool(s, &mcp.Tool{
			Name: "submit_add_printer", Title: "Ajouter une imprimante (carte)",
			Description: "RÉSERVÉ À LA CARTE-FORMULAIRE d'add_printer (non destiné au modèle) : mêmes champs et même validation qu'add_printer, tous obligatoires sauf buse et AMS. N'accepte ni code d'accès ni empreinte de certificat. N'ouvre aucune connexion à l'imprimante.",
			Annotations: write, Meta: appOnlyMeta(),
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in AddPrinterToolIn) (*mcp.CallToolResult, AddPrinterOut, error) {
			if m := in.missing(); len(m) > 0 {
				return nil, AddPrinterOut{}, fmt.Errorf("champs obligatoires manquants : %s", strings.Join(m, ", "))
			}
			return applyAddPrinter(ctx, p, in)
		})
	}
	if svc := c.Insights; svc != nil {
		addTool(s, &mcp.Tool{
			Name: "submit_cost_settings", Title: "Réglages de prix (carte)",
			Description: "RÉSERVÉ À LA CARTE-FORMULAIRE de set_cost_settings (non destiné au modèle) : mêmes champs que set_cost_settings, plus la marge d'alerte de bobine (margin_percent, margin_grams). Des prix, des watts, une devise : aucun secret.",
			Annotations: idem, Meta: appOnlyMeta(),
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in CostFormInput) (*mcp.CallToolResult, CostSettingsOut, error) {
			return applyCostSettings(ctx, svc, c.Filament, in)
		})
	}
	if c.Camera != nil && c.Notify != nil {
		addTool(s, &mcp.Tool{
			Name: "submit_notifications", Title: "Notifications (carte)",
			Description: "RÉSERVÉ À LA CARTE-FORMULAIRE de configure_notifications (non destiné au modèle) : active ou désactive des canaux EXISTANTS et choisit leurs évènements. Ni création, ni destination, ni secret : cela se fait sur la page /setup/notifications.",
			Annotations: idem, Meta: appOnlyMeta(),
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in ConfigureNotificationsInput) (*mcp.CallToolResult, NotifyOut, error) {
			return applyNotifications(ctx, c.Notify, in, true)
		})
	}
	if fs := c.Filament; fs != nil {
		addTool(s, &mcp.Tool{
			Name: "submit_spool", Title: "Bobine (carte)",
			Description: "RÉSERVÉ À LA CARTE-FORMULAIRE d'update_spool (non destiné au modèle) : crée ou modifie une bobine de l'inventaire (matière, couleur, poids, prix). Ne charge rien dans l'imprimante.",
			Annotations: idem, Meta: appOnlyMeta(),
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in SpoolFormInput) (*mcp.CallToolResult, SpoolOut, error) {
			return applySpool(ctx, fs, in.update())
		})
	}
	if g := c.Goals; g != nil {
		addTool(s, &mcp.Tool{
			Name: "submit_goals", Title: "Objectifs de fabrication (carte)",
			Description: "RÉSERVÉ À LA CARTE-FORMULAIRE de set_manufacturing_goals (non destiné au modèle) : enregistre l'objectif (texte, matière, priorités solide / surface propre / rapide / translucide / économe). N'imprime rien.",
			Annotations: write, Meta: appOnlyMeta(),
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in GoalFormInput) (*mcp.CallToolResult, GoalOut, error) {
			if strings.TrimSpace(in.OriginalText) == "" {
				return nil, GoalOut{}, fmt.Errorf("décris le besoin (original_text) : il est conservé tel quel")
			}
			crit := in.criteria()
			if len(crit) == 0 {
				return nil, GoalOut{}, fmt.Errorf("choisis au moins un objectif (solide, surface propre, rapide, translucide ou économe)")
			}
			return applyGoals(ctx, g, goals.GoalInput{OriginalText: in.OriginalText, ModelID: in.ModelID, Material: in.Material, Function: in.Function, Criteria: crit})
		})
	}
	if lib := c.Library; lib != nil && c.Calibration != nil {
		addTool(s, &mcp.Tool{
			Name: "submit_part_template", Title: "Pièce depuis un gabarit (carte)",
			Description: "RÉSERVÉ À LA CARTE-FORMULAIRE de create_from_template (non destiné au modèle) : mêmes paramètres et même validation que create_from_template ; renvoie en plus l'aperçu de la pièce créée.",
			Annotations: write, Meta: appOnlyMeta(),
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in library.CreateInput) (*mcp.CallToolResult, CreateFromTemplateOut, error) {
			return applyTemplate(ctx, lib, in)
		})
		addTool(s, &mcp.Tool{
			Name: "preview_part_template", Title: "Aperçu d'un gabarit (carte)",
			Description: "RÉSERVÉ À LA CARTE-FORMULAIRE de create_from_template (non destiné au modèle) : vignette du gabarit pour les paramètres courants (image SVG en base64). Lecture seule, rien n'est enregistré.",
			Annotations: ro, Meta: appOnlyMeta(),
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in PreviewTemplateInput) (*mcp.CallToolResult, PreviewTemplateOut, error) {
			svg, err := lib.Thumbnail(ctx, in.Template, in.Params)
			if err != nil {
				return nil, PreviewTemplateOut{}, err
			}
			img, ok := svgImage(svg, "Aperçu du gabarit "+in.Template)
			if !ok {
				return nil, PreviewTemplateOut{}, fmt.Errorf("aperçu trop volumineux")
			}
			return textResult("Aperçu du gabarit " + in.Template + "."), PreviewTemplateOut{Image: img}, nil
		})
	}
}

// maxFormImageBytes borne l'image jointe (le résultat reste très en dessous des ~150 000 caractères des hôtes).
const maxFormImageBytes = 90_000

func svgImage(svg []byte, alt string) (*FormImage, bool) {
	if len(svg) == 0 || len(svg) > maxFormImageBytes {
		return nil, false
	}
	return &FormImage{Mime: "image/svg+xml", Data: base64.StdEncoding.EncodeToString(svg), Alt: alt}, true
}

// PreviewTemplateInput : gabarit et paramètres courants de la carte.
type PreviewTemplateInput struct {
	Template string         `json:"template" jsonschema:"identifiant d'un gabarit ou d'une variante"`
	Params   map[string]any `json:"params,omitempty"`
}

// PreviewTemplateOut : vignette.
type PreviewTemplateOut struct {
	Image *FormImage `json:"image"`
}

// ======================= add_printer =======================

// AddPrinterToolIn : entrée d'add_printer, champs facultatifs dans le schéma pour que le modèle puisse appeler l'outil
// sans eux (la carte les demande). Avec tous les champs obligatoires, le comportement est inchangé.
type AddPrinterToolIn struct {
	Name     string  `json:"name,omitempty" jsonschema:"nom d'usage, ex. Atelier (obligatoire ; omis = carte-formulaire)"`
	ModelID  string  `json:"model_id,omitempty" jsonschema:"modèle Bambu (identifiant de list_printer_models, ex. p1s) ou nom exact (obligatoire ; omis = carte-formulaire)"`
	Host     string  `json:"host,omitempty" jsonschema:"adresse IP privée ou lien local (192.168.x.x, 10.x, 172.16-31.x, fc00::/7, fe80::/10) ou nom en .local (obligatoire ; omis = carte-formulaire)"`
	Serial   string  `json:"serial,omitempty" jsonschema:"numéro de série (alphanumérique, obligatoire ; omis = carte-formulaire)"`
	NozzleMM float64 `json:"nozzle_mm,omitempty" jsonschema:"buse installée en mm (défaut 0,4)"`
	AMSUnits int     `json:"ams_units,omitempty" jsonschema:"nombre d'AMS raccordés (défaut 0)"`
	MQTTPort int     `json:"mqtt_port,omitempty"`
	FTPPort  int     `json:"ftp_port,omitempty"`
}

func (in AddPrinterToolIn) service() printing.AddPrinterInput {
	return printing.AddPrinterInput{Name: in.Name, ModelID: in.ModelID, Host: in.Host, Serial: in.Serial, NozzleMM: in.NozzleMM, AMSUnits: in.AMSUnits, MQTTPort: in.MQTTPort, FTPPort: in.FTPPort}
}

func (in AddPrinterToolIn) missing() []string {
	var m []string
	for _, f := range []struct{ n, v string }{{"name", in.Name}, {"model_id", in.ModelID}, {"host", in.Host}, {"serial", in.Serial}} {
		if strings.TrimSpace(f.v) == "" {
			m = append(m, f.n)
		}
	}
	return m
}

func printerForm(ctx context.Context, p *printing.Service, in AddPrinterToolIn) *FormSpec {
	models, _ := p.ListPrinterModels(ctx)
	opts := make([]FormOption, 0, len(models))
	for _, m := range models {
		opts = append(opts, FormOption{Value: m.ID, Label: m.Name})
	}
	model := FormField{Name: "model_id", Label: "Modèle", Kind: "select", Required: true, Options: opts}
	if len(opts) == 0 {
		model.Kind, model.Hint, model.Placeholder = "text", "Aucun modèle chargé (Bambu Studio absent ?) : saisir l'identifiant, ex. p1s.", "p1s"
	}
	f := &FormSpec{
		FormID: "add_printer", Title: "Ajouter une imprimante", Tool: "add_printer", SubmitTool: "submit_add_printer", SubmitLabel: "Ajouter l'imprimante", Send: "all",
		Intro: "Renseigne ton imprimante Bambu Lab. Le code d'accès LAN n'est PAS demandé ici : il se saisit ensuite sur une page sécurisée.",
		Fields: []FormField{
			{Name: "name", Label: "Nom", Kind: "text", Required: true, Placeholder: "Atelier", MaxLength: 80},
			model,
			{Name: "host", Label: "Adresse IP", Kind: "text", Required: true, Placeholder: "192.168.1.42", Hint: "Réseau local uniquement : 192.168.x.x, 10.x, 172.16 à 31.x, ou un nom en .local.", Pattern: `^[A-Za-z0-9]([A-Za-z0-9.:\-\[\]]{0,251}[A-Za-z0-9\]])?$`},
			{Name: "serial", Label: "Numéro de série", Kind: "text", Required: true, Placeholder: "01P00A000000000", Hint: "6 à 32 caractères alphanumériques (écran de l'imprimante ou étiquette).", Pattern: `^[A-Za-z0-9]{6,32}$`, MaxLength: 32},
			{Name: "nozzle_mm", Label: "Buse", Kind: "segmented", Numeric: true, Unit: "mm", Options: []FormOption{{Value: "0.2", Label: "0,2"}, {Value: "0.4", Label: "0,4"}, {Value: "0.6", Label: "0,6"}, {Value: "0.8", Label: "0,8"}}},
			{Name: "ams_units", Label: "Nombre d'AMS", Kind: "segmented", Numeric: true, Options: []FormOption{{Value: "0", Label: "Aucun"}, {Value: "1", Label: "1"}, {Value: "2", Label: "2"}, {Value: "3", Label: "3"}, {Value: "4", Label: "4"}}},
		},
		Values:  map[string]any{"name": in.Name, "model_id": in.ModelID, "host": in.Host, "serial": in.Serial, "nozzle_mm": nz(in.NozzleMM), "ams_units": in.AMSUnits},
		Missing: in.missing(),
		Notice:  "Le code d'accès LAN ne se saisit jamais dans cette carte ni dans la conversation : après l'ajout, une page locale sécurisée de ton navigateur enregistré te le demandera.",
	}
	return f
}

func nz(v float64) float64 {
	if v == 0 {
		return 0.4
	}
	return v
}

func applyAddPrinter(ctx context.Context, p *printing.Service, in AddPrinterToolIn) (*mcp.CallToolResult, AddPrinterOut, error) {
	v, err := p.AddPrinter(ctx, in.service())
	if err != nil {
		return nil, AddPrinterOut{}, err
	}
	res := &FormResult{OK: true, Title: "Imprimante ajoutée", Summary: fmt.Sprintf("Imprimante %s (%s) enregistrée sous %s.", v.Name, v.ModelName, v.ID),
		Details: []FormDetail{{"Modèle", v.ModelName}, {"Adresse", v.Host}, {"Numéro de série", v.Serial}, {"Buse", fmt.Sprintf("%.1f mm", v.NozzleMM)}, {"AMS", fmt.Sprint(v.AMSUnits)}},
		Notes:   []string{"Dernière étape : ouvre la page sécurisée pour comparer l'empreinte du certificat et saisir le code d'accès LAN. Tant que ce n'est pas fait, aucune connexion à l'imprimante n'est tentée."}}
	if v.SetupURL != "" {
		res.Links = []FormLink{{Label: "Saisir le code d'accès LAN (page sécurisée)", URL: v.SetupURL, Note: "ouvre ton navigateur enregistré"}}
	}
	out := AddPrinterOut{Printer: v, SetupURL: v.SetupURL,
		Note:    "Demande à l'utilisateur d'ouvrir setup_url pour comparer l'empreinte du certificat et saisir le code d'accès affiché sur l'écran de l'imprimante. Ne lui demande jamais ce code dans la conversation. Tant que ce n'est pas fait, aucune connexion à l'imprimante n'est tentée.",
		FormOut: FormOut{FormResult: res}}
	return textResult(fmt.Sprintf("Imprimante %s (%s) enregistrée sous %s. Page de saisie du code d'accès : %s", v.Name, v.ModelName, v.ID, v.SetupURL)), out, nil
}

// handleAddPrinter : carte si des champs obligatoires manquent, sinon l'ajout.
func handleAddPrinter(ctx context.Context, p *printing.Service, in AddPrinterToolIn) (*mcp.CallToolResult, AddPrinterOut, error) {
	if m := in.missing(); len(m) > 0 {
		f := printerForm(ctx, p, in)
		return textResult(formText(f, "Il manque des informations pour ajouter l'imprimante : une carte-formulaire s'affiche à l'utilisateur.")), AddPrinterOut{FormOut: FormOut{Form: f}}, nil
	}
	return applyAddPrinter(ctx, p, in)
}

// ======================= set_cost_settings =======================

// CostFormInput : SettingsPatch + marge d'alerte de bobine (réglage voisin du filament, présenté dans la même carte).
type CostFormInput struct {
	insights.SettingsPatch
	MarginPercent *float64 `json:"margin_percent,omitempty" jsonschema:"marge d'alerte de manque de bobine en % du besoin"`
	MarginGrams   *float64 `json:"margin_grams,omitempty" jsonschema:"marge d'alerte fixe en grammes"`
}

// CostSettingsOut : réglages (inchangés) + carte ou résumé.
type CostSettingsOut struct {
	insights.Settings
	FormOut
}

func patchEmpty(in insights.SettingsPatch) bool {
	return in.Currency == nil && in.ElectricityPerKWh == nil && len(in.FilamentPerKg) == 0 && len(in.SpoolPerKg) == 0 &&
		in.MachineWearPerHr == nil && in.NozzleWearPerHr == nil && len(in.PowerOverrides) == 0 && !in.ApplyToHistory
}

func costForm(st insights.Settings, fs *filament.Service, ctx context.Context) *FormSpec {
	cur := st.Currency
	if cur == "" {
		cur = "EUR"
	}
	vals := map[string]any{"currency": cur, "electricity_per_kwh": st.ElectricityPerKWh, "machine_wear_per_hour": st.MachineWearPerHr, "nozzle_wear_per_hour": st.NozzleWearPerHr}
	f := &FormSpec{
		FormID: "set_cost_settings", Title: "Réglages de prix", Tool: "set_cost_settings", SubmitTool: "submit_cost_settings", SubmitLabel: "Enregistrer les prix", Send: "changed",
		Intro: "Prix servant à estimer le coût de chaque impression. Seules les valeurs que tu modifies sont enregistrées.",
		Fields: []FormField{
			{Name: "currency", Label: "Devise", Kind: "text", Group: "Général", Pattern: `^[A-Za-z]{3}$`, MaxLength: 3, Hint: "Code à 3 lettres, ex. EUR."},
			{Name: "electricity_per_kwh", Label: "Électricité", Kind: "number", Unit: cur + "/kWh", Min: fptr(0), Step: fptr(0.01), Group: "Électricité"},
		},
		Values: vals,
	}
	for _, fam := range sortedKeys(st.FilamentPerKg) {
		name := "filament_per_kg." + fam
		f.Fields = append(f.Fields, FormField{Name: name, Label: fam, Kind: "number", Unit: cur + "/kg", Min: fptr(0), Step: fptr(0.5), Group: "Prix du kilo par matière"})
		vals[name] = st.FilamentPerKg[fam]
	}
	f.Fields = append(f.Fields,
		FormField{Name: "machine_wear_per_hour", Label: "Usure machine", Kind: "number", Unit: cur + "/h", Min: fptr(0), Step: fptr(0.01), Group: "Usure", Hint: "Amortissement par heure d'impression (0 = non compté)."},
		FormField{Name: "nozzle_wear_per_hour", Label: "Usure buse", Kind: "number", Unit: cur + "/h", Min: fptr(0), Step: fptr(0.01), Group: "Usure"},
	)
	if fs != nil {
		m := fs.GetSettings(ctx)
		vals["margin_percent"], vals["margin_grams"] = m.MarginPercent, m.MarginGrams
		f.Fields = append(f.Fields,
			FormField{Name: "margin_percent", Label: "Marge de manque", Kind: "number", Unit: "%", Min: fptr(0), Max: fptr(100), Step: fptr(1), Group: "Marge d'alerte de bobine"},
			FormField{Name: "margin_grams", Label: "Marge fixe", Kind: "number", Unit: "g", Min: fptr(0), Step: fptr(1), Group: "Marge d'alerte de bobine", Hint: "Alerte quand une bobine risque de manquer."},
		)
	}
	f.Fields = append(f.Fields, FormField{Name: "apply_to_history", Label: "Recalculer le coût réel de l'historique avec ces prix", Kind: "toggle", Group: "Historique"})
	if len(st.DefaultedPrices) > 0 {
		f.Notice = fmt.Sprintf("%d prix sont encore des valeurs par défaut INDICATIVES : remplace-les par tes prix réels.", len(st.DefaultedPrices))
	}
	return f
}

func costText(st insights.Settings) string {
	return fmt.Sprintf("Réglages enregistrés : devise %s, kWh à %.4g, usure machine %.2f/h, buse %.2f/h.", st.Currency, st.ElectricityPerKWh, st.MachineWearPerHr, st.NozzleWearPerHr)
}

func applyCostSettings(ctx context.Context, svc *insights.Service, fs *filament.Service, in CostFormInput) (*mcp.CallToolResult, CostSettingsOut, error) {
	st, err := svc.SetSettings(ctx, in.SettingsPatch)
	if err != nil {
		return nil, CostSettingsOut{}, err
	}
	var notes []string
	if fs != nil && (in.MarginPercent != nil || in.MarginGrams != nil) {
		m := fs.GetSettings(ctx)
		if in.MarginPercent != nil {
			m.MarginPercent = *in.MarginPercent
		}
		if in.MarginGrams != nil {
			m.MarginGrams = *in.MarginGrams
		}
		m, err = fs.SetSettings(ctx, m)
		if err != nil {
			return nil, CostSettingsOut{}, err
		}
		notes = append(notes, fmt.Sprintf("Marge d'alerte de bobine : %.0f %% + %.0f g.", m.MarginPercent, m.MarginGrams))
	}
	res := &FormResult{OK: true, Title: "Prix enregistrés", Summary: costText(st),
		Details: []FormDetail{{"Électricité", fmt.Sprintf("%.4g %s/kWh", st.ElectricityPerKWh, st.Currency)}, {"Usure machine", fmt.Sprintf("%.2f %s/h", st.MachineWearPerHr, st.Currency)}, {"Usure buse", fmt.Sprintf("%.2f %s/h", st.NozzleWearPerHr, st.Currency)}},
		Notes:   append(notes, st.Note)}
	return textResult(costText(st) + strings.Join(append([]string{""}, notes...), " ")), CostSettingsOut{Settings: st, FormOut: FormOut{FormResult: res}}, nil
}

// handleCostSettings : sans aucun champ, la carte (valeurs actuelles) ; sinon l'enregistrement.
func handleCostSettings(ctx context.Context, svc *insights.Service, fs *filament.Service, in insights.SettingsPatch) (*mcp.CallToolResult, CostSettingsOut, error) {
	if patchEmpty(in) {
		st, err := svc.GetSettings(ctx)
		if err != nil {
			return nil, CostSettingsOut{}, err
		}
		f := costForm(st, fs, ctx)
		txt := fmt.Sprintf("Réglages actuels : devise %s, kWh à %.4g, %d prix de matière (%d encore par défaut indicatif). Une carte-formulaire s'affiche pour les modifier.", st.Currency, st.ElectricityPerKWh, len(st.FilamentPerKg), len(st.DefaultedPrices))
		return textResult(formText(f, txt)), CostSettingsOut{Settings: st, FormOut: FormOut{Form: f}}, nil
	}
	return applyCostSettings(ctx, svc, nil, CostFormInput{SettingsPatch: in})
}

// ======================= configure_notifications =======================

var eventLabels = map[string]string{
	notify.EvPrintCompleted: "Impression terminée", notify.EvPrintFailed: "Impression échouée", notify.EvDetectionAlert: "Alerte de détection",
	notify.EvStartUnknown: "Démarrage incertain", notify.EvPrinterOffline: "Imprimante hors ligne", notify.EvQueue: "File d'attente",
	notify.EvPrintAttention: "Impression à surveiller", notify.EvSystemUpdate: "Mise à jour du serveur",
}

func notifyForm(chs []notify.ChannelView, setupURL string) *FormSpec {
	evOpts := make([]FormOption, 0, len(notify.AllEvents))
	for _, e := range notify.AllEvents {
		l := eventLabels[e]
		if l == "" {
			l = e
		}
		evOpts = append(evOpts, FormOption{Value: e, Label: l})
	}
	f := &FormSpec{
		FormID: "configure_notifications", Title: "Notifications", Tool: "configure_notifications", SubmitTool: "submit_notifications", SubmitLabel: "Enregistrer", Send: "all",
		Intro:  "Active ou désactive tes canaux et choisis les évènements qui t'écrivent.",
		Fields: []FormField{},
		Values: map[string]any{},
		Links:  []FormLink{{Label: "Créer ou modifier un canal, saisir les secrets", URL: setupURL, Note: "page sécurisée de ton navigateur enregistré"}},
		Notice: "Les destinations (adresse du serveur, destinataires, URL) et tous les secrets se saisissent uniquement sur la page sécurisée ci-dessous : jamais dans cette carte ni dans la conversation.",
	}
	for i, ch := range chs {
		g := fmt.Sprintf("%s (%s)%s", ch.Name, ch.Kind, map[bool]string{true: "", false: " : secrets à compléter"}[ch.Ready])
		en, ev := fmt.Sprintf("channels.%d.enabled", i), fmt.Sprintf("channels.%d.events", i)
		nm := fmt.Sprintf("channels.%d.name", i)
		f.Fields = append(f.Fields,
			FormField{Name: nm, Label: "Canal", Kind: "hidden"},
			FormField{Name: en, Label: "Canal actif", Kind: "toggle", Group: g},
			FormField{Name: ev, Label: "Évènements", Kind: "checks", Options: evOpts, Group: g, Hint: "Aucun coché = tous les évènements."},
		)
		sel := ch.Events
		if len(sel) == 0 {
			sel = notify.AllEvents
		}
		f.Values[nm], f.Values[en], f.Values[ev] = ch.Name, ch.Enabled, sel
	}
	return f
}

func applyNotifications(ctx context.Context, ns *notify.Service, in ConfigureNotificationsInput, fromCard bool) (*mcp.CallToolResult, NotifyOut, error) {
	out := NotifyOut{SetupURL: ns.SetupURL(), Events: notify.AllEvents,
		EnvHint: "Un secret peut aussi venir de la variable BAMBU_NOTIFY_<CANAL>_<CHAMP> (ou …_FILE vers un fichier), ex. " + notify.EnvName("mon canal", "url") + "."}
	for _, ch := range in.Channels {
		if _, err := ns.PatchChannel(ctx, ch); err != nil {
			return nil, NotifyOut{}, err
		}
	}
	chs, err := ns.ListChannels(ctx)
	if err != nil {
		return nil, NotifyOut{}, err
	}
	out.Channels = chs
	out.RecentLog, _ = ns.Log(ctx, 10)
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
	if len(in.Channels) == 0 && !fromCard {
		f := notifyForm(chs, out.SetupURL)
		out.Form = f
		return textResult(formText(f, out.Note+" Une carte-formulaire s'affiche pour régler les canaux existants.")), out, nil
	}
	res := &FormResult{OK: true, Title: "Notifications enregistrées", Summary: fmt.Sprintf("%d canal(aux) réglé(s).", len(in.Channels))}
	for _, ch := range chs {
		res.Details = append(res.Details, FormDetail{Label: ch.Name, Value: map[bool]string{true: "actif", false: "inactif"}[ch.Enabled]})
	}
	if len(todo) > 0 {
		res.Notes = []string{"Secrets manquants pour : " + strings.Join(todo, ", ") + "."}
		res.Links = []FormLink{{Label: "Compléter sur la page sécurisée", URL: out.SetupURL}}
	}
	out.FormResult = res
	return textResult(out.Note), out, nil
}

// ======================= update_spool =======================

// SpoolFormInput : champs de la carte bobine (sous-ensemble de filament.UpdateInput, sans emplacement ni imprimante).
type SpoolFormInput struct {
	SpoolID     string   `json:"spool_id,omitempty" jsonschema:"bobine à modifier ; vide = créer"`
	Material    string   `json:"material,omitempty"`
	Brand       string   `json:"brand,omitempty"`
	Name        string   `json:"name,omitempty" jsonschema:"nom de la couleur"`
	ColorHex    string   `json:"color_hex,omitempty" jsonschema:"#RRGGBB"`
	Translucent *bool    `json:"translucent,omitempty"`
	DiameterMM  *float64 `json:"diameter_mm,omitempty"`
	NetWeightG  *float64 `json:"net_weight_g,omitempty"`
	RemainingG  *float64 `json:"remaining_g,omitempty"`
	PricePerKg  *float64 `json:"price_per_kg,omitempty"`
}

func (in SpoolFormInput) update() filament.UpdateInput {
	return filament.UpdateInput{SpoolID: in.SpoolID, Material: in.Material, Brand: in.Brand, Name: in.Name, ColorHex: in.ColorHex, Translucent: in.Translucent,
		DiameterMM: in.DiameterMM, NetWeightG: in.NetWeightG, RemainingG: in.RemainingG, PricePerKg: in.PricePerKg}
}

var spoolMaterials = []string{"PLA", "PLA-CF", "PETG", "PETG-CF", "ABS", "ASA", "TPU", "PC", "PA", "PA-CF", "PVA", "PET"}

var colorPresets = []FormOption{
	{Value: "#ffffff", Label: "Blanc", Color: "#ffffff"}, {Value: "#111111", Label: "Noir", Color: "#111111"}, {Value: "#8a8d91", Label: "Gris", Color: "#8a8d91"},
	{Value: "#d62d20", Label: "Rouge", Color: "#d62d20"}, {Value: "#f28c28", Label: "Orange", Color: "#f28c28"}, {Value: "#f4d03f", Label: "Jaune", Color: "#f4d03f"},
	{Value: "#16a06b", Label: "Vert", Color: "#16a06b"}, {Value: "#2a6fdb", Label: "Bleu", Color: "#2a6fdb"}, {Value: "#7a3fc4", Label: "Violet", Color: "#7a3fc4"},
}

// spoolEmpty : aucun champ utile (seul spool_id peut être donné).
func spoolEmpty(in filament.UpdateInput) bool {
	return in.Material == "" && in.Brand == "" && in.Name == "" && in.ColorHex == "" && in.Translucent == nil && in.DiameterMM == nil && in.NetWeightG == nil &&
		in.RemainingG == nil && in.PricePerKg == nil && in.OpenedAt == "" && in.LastDriedAt == "" && in.DryingNote == "" && in.Notes == "" && in.PrinterID == "" &&
		in.Slot == "" && !in.Unassign && !in.ReplaceExisting && in.Archived == nil
}

func spoolForm(ctx context.Context, fs *filament.Service, in filament.UpdateInput) (*FormSpec, error) {
	vals := map[string]any{"spool_id": in.SpoolID, "material": in.Material, "brand": in.Brand, "name": in.Name, "color_hex": in.ColorHex, "net_weight_g": 1000.0, "diameter_mm": 1.75}
	title, label := "Ajouter une bobine", "Ajouter la bobine"
	send := "all"
	if in.SpoolID != "" {
		sp, err := fs.GetSpool(ctx, in.SpoolID)
		if err != nil {
			return nil, err
		}
		title, label, send = "Modifier la bobine "+sp.Label(), "Enregistrer", "changed"
		vals["material"], vals["brand"], vals["name"], vals["color_hex"] = sp.Material, sp.Brand, sp.Name, sp.ColorHex
		vals["translucent"], vals["net_weight_g"], vals["remaining_g"], vals["diameter_mm"] = sp.Translucent, sp.NetWeightG, sp.RemainingG, sp.DiameterMM
		if sp.PricePerKg != nil {
			vals["price_per_kg"] = *sp.PricePerKg
		}
	}
	if in.Translucent != nil {
		vals["translucent"] = *in.Translucent
	}
	if in.NetWeightG != nil {
		vals["net_weight_g"] = *in.NetWeightG
	}
	if in.RemainingG != nil {
		vals["remaining_g"] = *in.RemainingG
	}
	if in.PricePerKg != nil {
		vals["price_per_kg"] = *in.PricePerKg
	}
	if in.DiameterMM != nil {
		vals["diameter_mm"] = *in.DiameterMM
	}
	mats := append([]string{}, spoolMaterials...)
	if m, _ := vals["material"].(string); m != "" {
		found := false
		for _, x := range mats {
			found = found || strings.EqualFold(x, m)
		}
		if !found {
			mats = append(mats, m)
		}
	}
	mo := make([]FormOption, len(mats))
	for i, m := range mats {
		mo[i] = FormOption{Value: m, Label: m}
	}
	f := &FormSpec{
		FormID: "update_spool", Title: title, Tool: "update_spool", SubmitTool: "submit_spool", SubmitLabel: label, Send: send,
		Intro: "Renseigne la bobine telle que tu la tiens : l'inventaire sert au calcul de coût et à l'alerte de manque.",
		Fields: []FormField{
			{Name: "spool_id", Label: "Bobine", Kind: "hidden"},
			{Name: "material", Label: "Matière", Kind: "select", Required: true, Options: mo},
			{Name: "brand", Label: "Marque", Kind: "text", Placeholder: "Bambu Lab"},
			{Name: "name", Label: "Nom de la couleur", Kind: "text", Placeholder: "Blanc os"},
			{Name: "color_hex", Label: "Couleur", Kind: "color", Options: colorPresets},
			{Name: "translucent", Label: "Translucide ou transparente", Kind: "toggle"},
			{Name: "net_weight_g", Label: "Poids net neuve", Kind: "number", Unit: "g", Min: fptr(1), Step: fptr(10), Group: "Quantité et prix"},
			{Name: "remaining_g", Label: "Poids restant", Kind: "number", Unit: "g", Min: fptr(0), Step: fptr(1), Group: "Quantité et prix", Hint: "Mesuré à la balance : jamais écrasé par la lecture de l'AMS."},
			{Name: "price_per_kg", Label: "Prix du kilo", Kind: "number", Unit: "€/kg", Min: fptr(0), Step: fptr(0.5), Group: "Quantité et prix"},
			{Name: "diameter_mm", Label: "Diamètre", Kind: "segmented", Numeric: true, Unit: "mm", Options: []FormOption{{Value: "1.75", Label: "1,75"}, {Value: "2.85", Label: "2,85"}}, Group: "Quantité et prix"},
		},
		Values:  vals,
		Missing: map[bool][]string{true: {"material"}, false: nil}[in.Material == "" && in.SpoolID == ""],
	}
	return f, nil
}

func applySpool(ctx context.Context, fs *filament.Service, in filament.UpdateInput) (*mcp.CallToolResult, SpoolOut, error) {
	sp, err := fs.UpdateSpool(ctx, in)
	if err != nil {
		return nil, SpoolOut{}, err
	}
	note := fmt.Sprintf("Bobine %s (%s) : %s restant", sp.ID, sp.Label(), fmt.Sprintf("%.0f g", sp.RemainingG))
	if sp.Slot != "" {
		note += ", chargée en " + sp.Slot
	}
	res := &FormResult{OK: true, Title: "Bobine enregistrée", Summary: note + ".",
		Details: []FormDetail{{"Matière", sp.Material}, {"Restant", fmt.Sprintf("%.0f g (%d %%)", sp.RemainingG, sp.RemainingPercent)}, {"État", sp.Status}}}
	return textResult(note + "."), SpoolOut{Spool: sp, Note: note + ".", FormOut: FormOut{FormResult: res}}, nil
}

func handleSpool(ctx context.Context, fs *filament.Service, in filament.UpdateInput) (*mcp.CallToolResult, SpoolOut, error) {
	if (in.SpoolID == "" && in.Material == "") || (in.SpoolID != "" && spoolEmpty(in)) {
		f, err := spoolForm(ctx, fs, in)
		if err != nil {
			return nil, SpoolOut{}, err
		}
		return textResult(formText(f, "Une carte-formulaire s'affiche pour "+strings.ToLower(f.Title)+".")), SpoolOut{FormOut: FormOut{Form: f}, Note: "Carte-formulaire affichée."}, nil
	}
	return applySpool(ctx, fs, in)
}

// ======================= set_manufacturing_goals =======================

// GoalToolInput : GoalInput avec texte et critères facultatifs dans le schéma (carte-formulaire quand ils manquent).
type GoalToolInput struct {
	OriginalText string                 `json:"original_text,omitempty" jsonschema:"texte original de la demande, conservé tel quel (omis avec criteria = carte-formulaire)"`
	ModelID      string                 `json:"model_id,omitempty"`
	Material     string                 `json:"material,omitempty" jsonschema:"matériau disponible : famille (PLA, PETG...) ou nom de filament"`
	Function     string                 `json:"function,omitempty" jsonschema:"fonction de la pièce"`
	Finish       string                 `json:"finish,omitempty" jsonschema:"finition recherchée, texte libre"`
	Constraints  []string               `json:"constraints,omitempty" jsonschema:"contraintes de production (machine, délai, budget...)"`
	Criteria     []goals.CriterionInput `json:"criteria,omitempty" jsonschema:"critères extraits du texte (voir list_goal_criteria) ; omis = carte-formulaire où l'utilisateur choisit ses objectifs"`
}

// GoalOut : objectif enregistré + carte ou résumé.
type GoalOut struct {
	goals.Goal
	FormOut
}

// GoalFormInput : champs de la carte objectifs.
type GoalFormInput struct {
	OriginalText string `json:"original_text,omitempty"`
	ModelID      string `json:"model_id,omitempty"`
	Material     string `json:"material,omitempty"`
	Function     string `json:"function,omitempty"`
	Strength     int    `json:"strength,omitempty" jsonschema:"priorité de « solide » : 0 non retenu, 1 (la plus haute) à 5"`
	Surface      int    `json:"surface,omitempty" jsonschema:"priorité de « surface propre »"`
	Speed        int    `json:"speed,omitempty" jsonschema:"priorité de « rapide »"`
	Translucent  int    `json:"translucent,omitempty" jsonschema:"priorité de « translucide »"`
	Economy      int    `json:"economy,omitempty" jsonschema:"priorité de « économe en matière »"`
}

var goalChoices = []struct{ Field, Type, Label string }{
	{"strength", "strength", "Solide"}, {"surface", "surface_aspect", "Surface propre"}, {"speed", "speed", "Rapide"},
	{"translucent", "transparency", "Translucide"}, {"economy", "material_economy", "Économe"},
}

func (in GoalFormInput) prio(field string) int {
	switch field {
	case "strength":
		return in.Strength
	case "surface":
		return in.Surface
	case "speed":
		return in.Speed
	case "translucent":
		return in.Translucent
	}
	return in.Economy
}

func (in GoalFormInput) criteria() []goals.CriterionInput {
	var out []goals.CriterionInput
	for _, g := range goalChoices {
		if p := in.prio(g.Field); p > 0 {
			out = append(out, goals.CriterionInput{Type: g.Type, Priority: min(p, 5), Text: g.Label})
		}
	}
	return out
}

func goalForm(svc *goals.Service, in GoalToolInput) *FormSpec {
	fams := svc.ListCriteria().Families
	mo := []FormOption{{Value: "", Label: "Pas de préférence"}}
	for _, f := range fams {
		mo = append(mo, FormOption{Value: f, Label: f})
	}
	prio := []FormOption{{Value: "0", Label: "Non"}, {Value: "5", Label: "Faible"}, {Value: "3", Label: "Normal"}, {Value: "2", Label: "Haute"}, {Value: "1", Label: "Prioritaire"}}
	vals := map[string]any{"original_text": in.OriginalText, "model_id": in.ModelID, "material": in.Material, "function": in.Function}
	for _, g := range goalChoices {
		vals[g.Field] = 0
	}
	for _, c := range in.Criteria {
		for _, g := range goalChoices {
			if c.Type == g.Type {
				p := c.Priority
				if p <= 0 {
					p = 3
				}
				vals[g.Field] = p
			}
		}
	}
	f := &FormSpec{
		FormID: "set_manufacturing_goals", Title: "Objectifs de fabrication", Tool: "set_manufacturing_goals", SubmitTool: "submit_goals", SubmitLabel: "Enregistrer l'objectif", Send: "all",
		Intro: "Dis ce que doit faire la pièce et ce qui compte le plus : l'atelier en déduit des réglages et te signale les compromis.",
		Fields: []FormField{
			{Name: "model_id", Label: "Modèle", Kind: "hidden"},
			{Name: "original_text", Label: "Le besoin, avec tes mots", Kind: "textarea", Required: true, Placeholder: "Un support de câble qui supporte le poids d'un écran…", MaxLength: 2000},
			{Name: "material", Label: "Matière disponible", Kind: "select", Options: mo},
			{Name: "function", Label: "Fonction de la pièce", Kind: "text", Placeholder: "support, boîtier, charnière…"},
		},
		Values: vals,
		Notice: "Choisis au moins un objectif. Les critères qui ne se jugent que par un essai physique te seront signalés après l'enregistrement.",
	}
	for _, g := range goalChoices {
		f.Fields = append(f.Fields, FormField{Name: g.Field, Label: g.Label, Kind: "segmented", Numeric: true, Options: prio, Group: "Ce qui compte (priorité)"})
	}
	if in.OriginalText == "" {
		f.Missing = []string{"original_text", "criteria"}
	} else {
		f.Missing = []string{"criteria"}
	}
	return f
}

func goalResult(g *goals.Goal) (*mcp.CallToolResult, GoalOut) {
	txt := fmt.Sprintf("Objectif %s : %d critère(s), %d ambiguïté(s), %d conflit(s), %d non évaluable(s).", g.ID, len(g.Criteria), len(g.Ambiguities), len(g.Conflicts), len(g.NonEvaluable))
	res := &FormResult{OK: true, Title: "Objectif enregistré", Summary: txt}
	for _, c := range g.Criteria {
		pr := c.Priority
		if pr == 0 {
			pr = 3
		}
		res.Details = append(res.Details, FormDetail{Label: c.Label, Value: fmt.Sprintf("priorité %d, %s", pr, map[string]string{"evaluable": "évaluable", "non_evaluable": "à vérifier par essai"}[c.Status])})
	}
	for _, a := range g.Ambiguities {
		res.Notes = append(res.Notes, "Question : "+a.Question)
	}
	for _, c := range g.Conflicts {
		res.Notes = append(res.Notes, "Compromis : "+c.Why)
	}
	return textResult(txt), GoalOut{Goal: *g, FormOut: FormOut{FormResult: res}}
}

func applyGoals(ctx context.Context, svc *goals.Service, in goals.GoalInput) (*mcp.CallToolResult, GoalOut, error) {
	g, err := svc.SetGoals(ctx, in)
	if err != nil {
		return nil, GoalOut{}, err
	}
	r, o := goalResult(g)
	return r, o, nil
}

func handleGoals(ctx context.Context, svc *goals.Service, in GoalToolInput) (*mcp.CallToolResult, GoalOut, error) {
	if len(in.Criteria) == 0 {
		f := goalForm(svc, in)
		return textResult(formText(f, "Il manque les objectifs : une carte-formulaire s'affiche à l'utilisateur. Sans carte, extrais toi-même les critères du texte libre (voir list_goal_criteria) et rappelle set_manufacturing_goals avec criteria.")), GoalOut{FormOut: FormOut{Form: f}}, nil
	}
	return applyGoals(ctx, svc, goals.GoalInput{OriginalText: in.OriginalText, ModelID: in.ModelID, Material: in.Material, Function: in.Function, Finish: in.Finish, Constraints: in.Constraints, Criteria: in.Criteria})
}

// ======================= create_from_template =======================

// CreateFromTemplateInput : library.CreateInput + no_form (créer tout de suite avec les valeurs par défaut).
type CreateFromTemplateInput struct {
	library.CreateInput
	NoForm bool `json:"no_form,omitempty" jsonschema:"vrai = créer tout de suite avec les valeurs par défaut au lieu d'afficher la carte-formulaire des paramètres (utile sans interface)"`
}

// CreateFromTemplateOut : sortie de create_from_template + carte ou résumé.
type CreateFromTemplateOut struct {
	library.CreateOutput
	FormOut
}

func templateForm(ctx context.Context, lib *library.Service, in library.CreateInput) (*FormSpec, error) {
	t, err := lib.Describe(ctx, in.Template)
	if err != nil {
		return nil, err
	}
	mats := []string{"PLA", "PETG", "ABS", "ASA", "TPU-95A", "PC", "PA-CF"}
	mo := make([]FormOption, len(mats))
	for i, m := range mats {
		mo[i] = FormOption{Value: m, Label: m}
	}
	vals := map[string]any{"template": t.ID, "name": in.Name, "material": firstNonEmptyStr(in.Material, "PLA"), "nozzle_diameter": nz(in.NozzleMM)}
	f := &FormSpec{
		FormID: "create_from_template", Title: t.Title, Tool: "create_from_template", SubmitTool: "submit_part_template", SubmitLabel: "Créer la pièce", Send: "all",
		Intro:       t.Summary + " Ajuste les cotes puis crée la pièce : elle est ajoutée au catalogue et vérifiée étanche.",
		PreviewTool: "preview_part_template",
		Fields: []FormField{
			{Name: "template", Label: "Gabarit", Kind: "hidden"},
			{Name: "name", Label: "Nom du modèle", Kind: "text", Placeholder: t.ID, Pattern: `^[A-Za-z0-9._-]*$`, Hint: "Lettres, chiffres, point, tiret (facultatif).", MaxLength: 80},
			{Name: "material", Label: "Matière", Kind: "select", Options: mo},
			{Name: "nozzle_diameter", Label: "Buse", Kind: "segmented", Numeric: true, Unit: "mm", Options: []FormOption{{Value: "0.2", Label: "0,2"}, {Value: "0.4", Label: "0,4"}, {Value: "0.6", Label: "0,6"}, {Value: "0.8", Label: "0,8"}}},
		},
		Values: vals,
	}
	for _, p := range t.Params {
		name := "params." + p.Name
		fl := FormField{Name: name, Label: paramLabel(p.Name), Group: "Paramètres de la pièce", Hint: p.Description}
		var def any = p.Default
		if v, ok := in.Params[p.Name]; ok {
			def = v
		}
		if p.Auto != "" && !strings.HasPrefix(p.Auto, "hole") {
			if _, given := in.Params[p.Name]; !given {
				def = nil // calculé par le moteur quand vide
			}
			fl.Hint = strings.TrimSpace(fl.Hint + " Calculé automatiquement si laissé vide (" + p.Auto + ").")
		}
		switch p.Type {
		case "enum":
			fl.Kind = "select"
			for _, o := range p.Options {
				fl.Options = append(fl.Options, FormOption{Value: o, Label: o})
			}
		case "boolean":
			fl.Kind = "toggle"
		default:
			fl.Kind, fl.Unit, fl.Min, fl.Max = "number", p.Unit, p.Min, p.Max
			fl.Integer = p.Type == "integer"
			if fl.Integer {
				fl.Step = fptr(1)
			}
		}
		f.Fields = append(f.Fields, fl)
		if def != nil {
			vals[name] = def
		}
	}
	return f, nil
}

func paramLabel(n string) string {
	s := strings.ReplaceAll(n, "_", " ")
	if s == "" {
		return n
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func applyTemplate(ctx context.Context, lib *library.Service, in library.CreateInput) (*mcp.CallToolResult, CreateFromTemplateOut, error) {
	o, err := lib.Create(ctx, in)
	if err != nil {
		return nil, CreateFromTemplateOut{}, err
	}
	txt := fmt.Sprintf("Modèle %s créé (version %s, gabarit %s v%s) ; %d paramètre(s) calculé(s), %d calcul(s), %d avertissement(s). %s",
		o.ModelID, o.VersionID, o.Template, o.TemplateVersion, len(o.Auto), len(o.Calcs), len(o.Warnings), o.Calibration.Provenance)
	res := &FormResult{OK: true, Title: "Pièce créée", Summary: txt, Notes: o.Warnings,
		Details: []FormDetail{{"Modèle", o.ModelID}, {"Version", o.VersionID}, {"Gabarit", o.Template + " v" + o.TemplateVersion}}}
	for _, a := range o.Auto {
		res.Details = append(res.Details, FormDetail{Label: a.Param + " (calculé)", Value: fmt.Sprintf("%.3g %s", a.Value, a.Unit)})
	}
	if svg, err := lib.ModelThumbnail(ctx, o.ModelID); err == nil {
		res.Image, _ = svgImage(svg, "Aperçu de la pièce créée")
	}
	return textResult(txt), CreateFromTemplateOut{CreateOutput: *o, FormOut: FormOut{FormResult: res}}, nil
}

func handleTemplate(ctx context.Context, lib *library.Service, in CreateFromTemplateInput) (*mcp.CallToolResult, CreateFromTemplateOut, error) {
	if len(in.Params) == 0 && in.Name == "" && in.ModelID == "" && !in.NoForm {
		f, err := templateForm(ctx, lib, in.CreateInput)
		if err != nil {
			return nil, CreateFromTemplateOut{}, err
		}
		return textResult(formText(f, "Une carte-formulaire des paramètres du gabarit s'affiche à l'utilisateur. Pour créer tout de suite avec les valeurs par défaut (ou sans interface), rappelle create_from_template avec no_form=true ou avec les params voulus.")),
			CreateFromTemplateOut{CreateOutput: library.CreateOutput{Params: map[string]any{}, Requested: map[string]any{}}, FormOut: FormOut{Form: f}}, nil
	}
	return applyTemplate(ctx, lib, in.CreateInput)
}
