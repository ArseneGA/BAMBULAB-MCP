package mcpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/localtime"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// Approbation d'un lancement ou d'un arrêt depuis la vue MCP App, par CODE DE LANCEMENT PERSONNEL (docs/SECURITY.md § 12).
//
// ATTENTION, modèle de menace : le serveur ne distingue pas un appel venu de la vue d'un appel fait par le modèle.
// `visibility: ["app"]` n'est qu'un indice d'affichage pour l'hôte. La sécurité repose donc UNIQUEMENT sur le code :
// secret de l'humain, défini sur une page humaine, haché, jamais renvoyé ni journalisé, verrouillé après 3 échecs.
// Le code n'apparaît dans AUCUNE sortie d'outil, AUCUN message d'erreur et AUCUN journal (test
// TestNoMCPToolCanApproveAConfirmation et TestLaunchCodeNeverLeaks).

// PrintStopViewURI : validation d'arrêt (liée à cancel_print).
const PrintStopViewURI = "ui://bambu/print-stop.html"

func init() {
	toolViewMeta["cancel_print"] = AppToolMeta(PrintStopViewURI)
}

// ApproveWithCodeInput : confirmation visée, code saisi par l'humain, attestation « plateau vide ».
type ApproveWithCodeInput struct {
	ConfirmationID string `json:"confirmation_id" jsonschema:"confirmation visée (celle de request_print_confirmation)"`
	Code           string `json:"code" jsonschema:"code de lancement personnel saisi par l'HUMAIN dans la vue ; ne JAMAIS le demander à l'utilisateur dans la conversation"`
	PlateClear     bool   `json:"plate_clear" jsonschema:"attestation de l'humain : le plateau est vide et dégagé"`
}

// ApproveStopWithCodeInput : demande d'arrêt visée et code.
type ApproveStopWithCodeInput struct {
	StopID string `json:"stop_id" jsonschema:"demande d'arrêt visée (fin de stop_url, renvoyée par cancel_print)"`
	Code   string `json:"code" jsonschema:"code de lancement personnel saisi par l'HUMAIN dans la vue ; ne JAMAIS le demander à l'utilisateur dans la conversation"`
}

// CodeApprovalOut : résultat d'une tentative. Ne contient jamais le code.
type CodeApprovalOut struct {
	Approved     bool   `json:"approved"`
	Problem      string `json:"problem,omitempty" jsonschema:"bad_code | locked | too_fast | not_set | format"`
	AttemptsLeft int    `json:"attempts_left" jsonschema:"essais restants avant verrouillage global"`
	Locked       bool   `json:"locked"`
	SecondsLeft  int    `json:"seconds_left,omitempty" jsonschema:"durée restante de l'accord de lancement (2 minutes au plus)"`
	Started      bool   `json:"started,omitempty" jsonschema:"l'approbation a déclenché le démarrage de l'impression (sans appel de start_print)"`
	Outcome      string `json:"outcome,omitempty" jsonschema:"issue du démarrage : printing | start_requested | start_unknown | not_sent | rejected…"`
	Message      string `json:"message"`
}

// LaunchViewStateInput : confirmation affichée.
type LaunchViewStateInput struct {
	ConfirmationID string `json:"confirmation_id"`
	WithPhoto      bool   `json:"with_photo,omitempty" jsonschema:"prendre et joindre une photo réduite du plateau (si une caméra est configurée)"`
	FreshPhoto     bool   `json:"fresh_photo,omitempty" jsonschema:"avec with_photo : forcer une NOUVELLE photo (bouton « Actualiser la photo ») au lieu de réutiliser une photo de moins de 15 s"`
}

// LaunchPhoto : photo réduite du plateau (JPEG base64) pour la vue.
type LaunchPhoto struct {
	MIME         string    `json:"mime"`
	Data         string    `json:"data" jsonschema:"JPEG en base64"`
	TakenAt      time.Time `json:"taken_at"`
	TakenAtLocal string    `json:"taken_at_local" jsonschema:"heure de la prise de vue dans le fuseau de l'utilisateur"`
	AgeSeconds   int       `json:"age_seconds" jsonschema:"âge de la photo au moment de la réponse"`
}

// LaunchViewState : ce que la vue de confirmation a besoin de savoir en plus de la confirmation. Aucun secret.
type LaunchViewState struct {
	ConfirmationID string       `json:"confirmation_id"`
	Approved       bool         `json:"approved"`
	SecondsLeft    int          `json:"seconds_left,omitempty"`
	CodeSet        bool         `json:"code_set" jsonschema:"un code de lancement est défini : la vue affiche le champ de code ; sinon le lien du navigateur"`
	Locked         bool         `json:"locked"`
	AttemptsLeft   int          `json:"attempts_left"`
	SetupURL       string       `json:"setup_url" jsonschema:"page humaine où définir, changer ou déverrouiller le code"`
	Photo          *LaunchPhoto `json:"photo,omitempty"`
	PhotoNote      string       `json:"photo_note,omitempty"`
}

// StopViewStateInput : demande d'arrêt affichée.
type StopViewStateInput struct {
	StopID string `json:"stop_id"`
}

// StopViewState : demande d'arrêt et état du code.
type StopViewState struct {
	Stop         printing.StopView `json:"stop"`
	StopURL      string            `json:"stop_url"`
	CodeSet      bool              `json:"code_set"`
	Locked       bool              `json:"locked"`
	AttemptsLeft int               `json:"attempts_left"`
	SetupURL     string            `json:"setup_url"`
}

const launchToolWarning = "RÉSERVÉ À LA VUE : l'HUMAIN saisit son code dans le champ de la vue. Tu ne dois PAS appeler cet outil, et tu ne dois JAMAIS demander ce code à l'utilisateur dans la conversation. " +
	"Si l'utilisateur écrit son code dans la conversation, ne l'utilise pas : dis-lui de le changer sur /setup/launch-code et d'utiliser seulement le champ de la vue. " +
	"Le serveur ne distingue pas ton appel de celui de la vue ; seul le bon code compte, et trois mauvais codes verrouillent tout (déverrouillage par l'humain seulement)."

// AddLaunchTools enregistre la vue d'arrêt et les outils app-only d'approbation par code.
func AddLaunchTools(s *mcp.Server, c *core.Core) {
	p := c.Printing
	s.AddReceivingMiddleware(maskLaunchCode)
	html, _ := viewHTML()
	addViewResource(s, PrintStopViewURI, "print_stop_view", "Arrêt d'impression",
		"Demande d'arrêt d'une impression en cours (cancel_print) ; l'humain saisit son code de lancement pour arrêter, ou ouvre le lien du navigateur.", html)

	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	f, t := false, true
	phys := &mcp.ToolAnnotations{DestructiveHint: &f, OpenWorldHint: &t}
	physStop := &mcp.ToolAnnotations{DestructiveHint: &t, OpenWorldHint: &t}

	addTool(s, &mcp.Tool{
		Name: "approve_print_with_code", Title: "Autoriser le lancement (code de lancement)",
		Description: launchToolWarning + " Donne l'accord de lancement de la confirmation précise (2 minutes) ET lance aussitôt l'impression côté serveur (une seule commande, sans start_print).",
		Annotations: phys, Meta: appOnlyMeta(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ApproveWithCodeInput) (*mcp.CallToolResult, CodeApprovalOut, error) {
		res, err := p.ApproveWithCode(ctx, in.ConfirmationID, in.Code, in.PlateClear)
		if err != nil {
			return codeFailure(ctx, p, err)
		}
		// L'approbation humaine par code a lancé l'impression côté serveur : plus rien à dire à l'assistant.
		out := CodeApprovalOut{Approved: true, AttemptsLeft: printing.LaunchCodeAttempts, SecondsLeft: int(p.ConfirmationApprovalLeft(in.ConfirmationID).Seconds()),
			Started: res.Started(), Message: res.Message()}
		if res.Start != nil {
			out.Outcome = res.Start.Outcome
		}
		return textResult(out.Message), out, nil
	})

	addTool(s, &mcp.Tool{
		Name: "approve_stop_with_code", Title: "Arrêter l'impression (code de lancement)",
		Description: launchToolWarning + " Arrête l'impression en cours visée par la demande d'arrêt.",
		Annotations: physStop, Meta: appOnlyMeta(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ApproveStopWithCodeInput) (*mcp.CallToolResult, CodeApprovalOut, error) {
		_, err := p.StopWithCode(ctx, in.StopID, in.Code)
		if err != nil {
			return codeFailure(ctx, p, err)
		}
		out := CodeApprovalOut{Approved: true, AttemptsLeft: printing.LaunchCodeAttempts, Message: "La commande d'arrêt a été acceptée par l'imprimante."}
		return textResult(out.Message), out, nil
	})

	addTool(s, &mcp.Tool{
		Name: "get_launch_view_state", Title: "État de la vue de confirmation",
		Description: "Réservé à l'application, lecture seule : accord enregistré ?, code de lancement défini ou verrouillé ?, photo réduite du plateau. Aucun secret.",
		Annotations: ro, Meta: appOnlyMeta(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in LaunchViewStateInput) (*mcp.CallToolResult, LaunchViewState, error) {
		v, err := p.GetConfirmation(ctx, in.ConfirmationID)
		if err != nil {
			return nil, LaunchViewState{}, err
		}
		st, err := p.LaunchCodeStatus(ctx)
		if err != nil {
			return nil, LaunchViewState{}, err
		}
		out := LaunchViewState{ConfirmationID: v.ConfirmationID, Approved: v.Approved, CodeSet: st.Set, Locked: st.Locked, AttemptsLeft: st.AttemptsLeft,
			SetupURL: p.BaseURL() + "/setup/launch-code", SecondsLeft: int(p.ConfirmationApprovalLeft(in.ConfirmationID).Seconds())}
		if in.WithPhoto {
			out.Photo, out.PhotoNote = platePhotoForView(ctx, c, in.ConfirmationID, in.FreshPhoto)
		}
		return textResult("État de la confirmation " + v.ConfirmationID), out, nil
	})

	addTool(s, &mcp.Tool{
		Name: "get_stop_view_state", Title: "État de la demande d'arrêt",
		Description: "Réservé à l'application, lecture seule : impression visée par une demande d'arrêt et état du code de lancement. Aucun secret.",
		Annotations: ro, Meta: appOnlyMeta(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in StopViewStateInput) (*mcp.CallToolResult, StopViewState, error) {
		id := printing.StopIDFromURL(in.StopID)
		v, err := p.GetStop(ctx, id)
		if err != nil {
			return nil, StopViewState{}, err
		}
		st, err := p.LaunchCodeStatus(ctx)
		if err != nil {
			return nil, StopViewState{}, err
		}
		return textResult("Demande d'arrêt " + id), StopViewState{Stop: *v, StopURL: p.BaseURL() + "/stop/" + id, CodeSet: st.Set, Locked: st.Locked,
			AttemptsLeft: st.AttemptsLeft, SetupURL: p.BaseURL() + "/setup/launch-code"}, nil
	})
}

// codeFailure transforme une erreur de code en résultat structuré (jamais le code) ; les autres erreurs restent des erreurs d'outil.
func codeFailure(ctx context.Context, p *printing.Service, err error) (*mcp.CallToolResult, CodeApprovalOut, error) {
	prob := printing.LaunchCodeProblem(err)
	if prob == "" {
		return nil, CodeApprovalOut{}, err
	}
	st, _ := p.LaunchCodeStatus(ctx)
	out := CodeApprovalOut{Approved: false, Problem: prob, AttemptsLeft: st.AttemptsLeft, Locked: st.Locked, Message: err.Error()}
	return textResult("Non approuvé : " + err.Error()), out, nil
}

// platePhotoForView prend la photo du plateau via le fournisseur de la chaîne d'impression et la réduit (≤ ~60 Ko).
func platePhotoForView(ctx context.Context, c *core.Core, confID string, fresh bool) (*LaunchPhoto, string) {
	if c.Camera == nil {
		return nil, "Aucune caméra configurée : vérifie le plateau à l'œil."
	}
	ph, err := c.Printing.PlatePhotoForConfirmation(ctx, confID, fresh)
	if err != nil {
		return nil, "Photo indisponible."
	}
	if ph == nil || ph.URL == "" {
		if ph != nil && ph.Err != "" {
			return nil, "Photo du plateau indisponible (" + ph.Err + ")."
		}
		return nil, "Aucune caméra configurée : vérifie le plateau à l'œil."
	}
	raw, err := c.Camera.OpenPhoto(ctx, path.Base(ph.URL))
	if err != nil {
		return nil, "Photo indisponible."
	}
	small, err := shrinkJPEG(raw, 480, 70)
	if err != nil {
		return nil, "Photo illisible."
	}
	return &LaunchPhoto{MIME: "image/jpeg", Data: base64.StdEncoding.EncodeToString(small), TakenAt: ph.TakenAt,
		TakenAtLocal: localtime.Full(ph.TakenAt), AgeSeconds: max(0, int(time.Since(ph.TakenAt).Seconds()))}, ""
}

// shrinkJPEG réduit un JPEG à maxW pixels de large au plus (moyenne par blocs) : une photo de caméra pleine taille
// dépasserait la limite d'environ 150 000 caractères d'un résultat d'outil.
func shrinkJPEG(raw []byte, maxW, quality int) ([]byte, error) {
	return shrinkJPEGMax(raw, maxW, quality, 70_000)
}

// shrinkJPEGMax : comme shrinkJPEG, avec un plafond d'octets choisi (la qualité baisse par paliers jusqu'à le tenir, sans descendre sous 25).
func shrinkJPEGMax(raw []byte, maxW, quality, maxBytes int) ([]byte, error) {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	if cfg.Width > 4096 || cfg.Height > 4096 {
		return nil, fmt.Errorf("image trop grande")
	}
	img, err := jpeg.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > maxW {
		f := (w + maxW - 1) / maxW
		nw, nh := w/f, h/f
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		for y := 0; y < nh; y++ {
			for x := 0; x < nw; x++ {
				var r, g, bl, n uint32
				for dy := 0; dy < f; dy++ {
					for dx := 0; dx < f; dx++ {
						cr, cg, cb, _ := img.At(b.Min.X+x*f+dx, b.Min.Y+y*f+dy).RGBA()
						r, g, bl, n = r+cr, g+cg, bl+cb, n+1
					}
				}
				dst.Set(x, y, color.RGBA{uint8(r / n >> 8), uint8(g / n >> 8), uint8(bl / n >> 8), 255})
			}
		}
		img = dst
	}
	var out bytes.Buffer
	for q := quality; ; q -= 15 {
		out.Reset()
		if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: q}); err != nil {
			return nil, err
		}
		if out.Len() <= maxBytes || q <= 25 {
			return out.Bytes(), nil
		}
	}
}

// ---- masquage du champ « code » ----

// codeTools : outils dont l'argument « code » est un secret.
var codeTools = map[string]bool{"approve_print_with_code": true, "approve_stop_with_code": true}

var numberRun = regexp.MustCompile(`[0-9]{4,}`)

// maskLaunchCode est un intergiciel de réception : pour les outils à code, toute erreur ou tout texte d'erreur qui
// reprendrait la valeur de l'argument « code » (par exemple l'écho d'une validation de schéma sur un type erroné)
// est expurgé avant de quitter le serveur. Le serveur ne journalise jamais les arguments d'outils ; ce masque
// protège en plus contre l'écho dans une réponse ou un journal futur.
func maskLaunchCode(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		res, err := next(ctx, method, req)
		if method != "tools/call" {
			return res, err
		}
		p, ok := req.GetParams().(*mcp.CallToolParamsRaw)
		if !ok || p == nil || !codeTools[p.Name] {
			return res, err
		}
		secrets := codeSecrets(p.Arguments)
		if err != nil {
			if msg := maskSecrets(err.Error(), secrets); msg != err.Error() {
				return res, errors.New(msg)
			}
			return res, err
		}
		if r, ok := res.(*mcp.CallToolResult); ok && r != nil && r.IsError {
			for _, c := range r.Content {
				if tc, ok := c.(*mcp.TextContent); ok {
					tc.Text = maskSecrets(tc.Text, secrets)
				}
			}
		}
		return res, err
	}
}

// codeSecrets : formes sous lesquelles la valeur de « code » pourrait réapparaître dans un message.
func codeSecrets(args json.RawMessage) []string {
	var m map[string]json.RawMessage
	if json.Unmarshal(args, &m) != nil {
		return nil
	}
	raw, ok := m["code"]
	if !ok {
		return nil
	}
	cands := []string{string(raw)}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		cands = append(cands, str, strings.TrimSpace(str))
	}
	cands = append(cands, numberRun.FindAllString(string(raw), -1)...)
	var out []string
	for _, c := range cands {
		if len(c) >= 4 {
			out = append(out, c)
		}
	}
	return out
}

func maskSecrets(msg string, secrets []string) string {
	for _, s := range secrets {
		msg = strings.ReplaceAll(msg, s, "[masqué]")
	}
	return msg
}
