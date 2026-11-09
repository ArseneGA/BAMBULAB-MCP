package mcpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image/jpeg"
	"math"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/camera"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/localtime"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// Suivi en direct d'une impression : carte « print-live » (ui://bambu/print-live.html), liée à get_print_status ET à
// wait_for_print_change.
//
// Choix du lien : ces deux outils rendent le même suivi complet (PrintStatus). get_print_status est la réponse naturelle à
// « ça en est où ? » ; wait_for_print_change sert à surveiller et rend la même forme : la carte les reconnaît tous deux.
// Aucun de ces outils n'avait de vue (les vues « progress » et « layers » sont liées à d'autres outils) : pas d'arbitrage à faire.
//
// La CSP de la vue est vide (aucun fetch) : l'image de la caméra arrive en base64 par l'outil réservé get_live_frame et l'état
// par get_live_state. La vue ne peut ni arrêter ni reprendre d'elle-même : « Arrêter » appelle cancel_print (qui ne fait que
// CRÉER la demande d'arrêt validée par l'humain) et « Reprendre » appelle resume_print (même principe).

// LiveViewURI : carte de suivi en direct.
const LiveViewURI = "ui://bambu/print-live.html"

// Bornes de l'image de suivi : un résultat d'outil doit rester très en dessous du seuil d'environ 150 000 caractères.
const (
	// LiveFrameMaxWidth : largeur maximale (pixels) de l'image rendue à la vue.
	LiveFrameMaxWidth = 640
	// LiveFrameMaxBytes : plafond d'octets JPEG (≈ 85 000 caractères en base64).
	LiveFrameMaxBytes = 64_000
	// liveFrameNextMS : cadence conseillée à la vue (la capture partagée dure 2 s, voir camera.LiveWindow).
	liveFrameNextMS = 2500
)

func init() {
	toolViewMeta["get_print_status"] = AppToolMeta(LiveViewURI)
	toolViewMeta["wait_for_print_change"] = AppToolMeta(LiveViewURI)
	// « Ça en est où ? » : le modèle appelle souvent get_printer_status au lieu de get_print_status. La carte de suivi doit
	// s'afficher dans les deux cas : elle retrouve seule l'impression active à partir de l'imprimante (get_live_state(printer_id)),
	// ou affiche l'état de l'imprimante quand aucune impression n'est suivie.
	toolViewMeta["get_printer_status"] = AppToolMeta(LiveViewURI)
}

// LiveFrameInput : imprimante (facultative s'il n'y en a qu'une).
type LiveFrameInput struct {
	PrinterID string `json:"printer_id,omitempty" jsonschema:"imprimante ; facultatif s'il n'y en a qu'une"`
}

// LiveFrame : une image du suivi en direct (JPEG réduit en base64), commune à toutes les vues ouvertes.
type LiveFrame struct {
	PrinterID    string    `json:"printer_id"`
	MIME         string    `json:"mime"`
	Data         string    `json:"data" jsonschema:"JPEG réduit en base64 (≤ 64 000 octets)"`
	TakenAt      time.Time `json:"taken_at"`
	TakenAtLocal string    `json:"taken_at_local" jsonschema:"heure de la prise de vue dans le fuseau de l'utilisateur"`
	AgeSeconds   int       `json:"age_seconds" jsonschema:"âge de l'image au moment de la réponse"`
	Width        int       `json:"width"`
	Height       int       `json:"height"`
	SizeBytes    int       `json:"size_bytes" jsonschema:"taille du JPEG réduit (avant base64)"`
	Cached       bool      `json:"cached" jsonschema:"vrai si l'image vient d'une capture partagée déjà faite pour un autre appel (une capture par fenêtre de 2 s et par imprimante)"`
	NextAfterMS  int       `json:"next_after_ms" jsonschema:"délai conseillé avant la prochaine demande"`
}

// LiveStateInput : impression suivie.
type LiveStateInput struct {
	PrintJobID     string `json:"print_job_id,omitempty" jsonschema:"impression suivie ; facultatif si printer_id est donné (l'impression active de l'imprimante est retrouvée)"`
	PrinterID      string `json:"printer_id,omitempty" jsonschema:"imprimante : sans print_job_id, retrouve l'impression active (PRINTING, START_REQUESTED, START_UNKNOWN) ; sinon rend l'état de l'imprimante sans impression"`
	WithFinalPhoto bool   `json:"with_final_photo,omitempty" jsonschema:"si l'impression est terminée : joindre la photo de fin (réduite)"`
}

// LiveFinal : résumé d'une impression terminée.
type LiveFinal struct {
	Outcome         string       `json:"outcome" jsonschema:"completed | failed | cancelled"`
	DurationSeconds int          `json:"duration_seconds,omitempty" jsonschema:"durée réelle de l'impression ; absent si inconnue"`
	Duration        string       `json:"duration,omitempty" jsonschema:"durée réelle lisible, ex. « 2 h 05 min »"`
	Grams           float64      `json:"grams,omitempty" jsonschema:"masse de filament ; absente si inconnue"`
	GramsBasis      string       `json:"grams_basis,omitempty" jsonschema:"origine : journal des impressions (mesurée ou décomptée) ou estimation de la découpe"`
	FinishedAtLocal string       `json:"finished_at_local,omitempty"`
	Photo           *LaunchPhoto `json:"photo,omitempty" jsonschema:"photo de fin réduite (JPEG base64)"`
	PhotoNote       string       `json:"photo_note,omitempty"`
}

// LiveState : le suivi complet (celui de get_print_status) plus ce que la carte affiche en plus.
type LiveState struct {
	Status          printing.PrintStatus `json:"status"`
	Terminal        bool                 `json:"terminal" jsonschema:"l'impression est terminée (COMPLETED, FAILED ou CANCELLED) : la carte arrête ses rafraîchissements"`
	EndsAt          time.Time            `json:"ends_at,omitzero" jsonschema:"heure de fin estimée (maintenant + temps restant) ; absente si inconnue"`
	EndsAtLocal     string               `json:"ends_at_local,omitempty" jsonschema:"même instant, lisible, dans le fuseau de l'utilisateur (Europe/Paris par défaut)"`
	HeightMM        *float64             `json:"height_mm,omitempty" jsonschema:"hauteur imprimée ≈ hauteur totale × couche / couches ; absente si la hauteur totale est inconnue"`
	HeightTotalMM   *float64             `json:"height_total_mm,omitempty"`
	CameraAvailable bool                 `json:"camera_available" jsonschema:"une caméra est configurée sur ce serveur (le modèle d'imprimante peut quand même ne pas en avoir une lisible)"`
	Final           *LiveFinal           `json:"final,omitempty"`
	NoActivePrint   bool                 `json:"no_active_print,omitempty" jsonschema:"vrai quand la carte a été demandée par imprimante et qu'aucune impression active n'y est suivie : seul status.printer est renseigné"`
}

// liveCam partage l'image réduite entre tous les appelants : camera.LiveFrame ne capture qu'une fois par fenêtre, et la
// réduction (décodage + moyenne par blocs) n'est elle aussi faite qu'une fois par image.
type liveCam struct {
	cam *camera.Service
	mu  sync.Mutex
	sm  map[string]smallFrame
}

type smallFrame struct {
	sha  string
	jpg  []byte
	w, h int
}

func newLiveCam(cam *camera.Service) *liveCam {
	if cam == nil {
		return nil
	}
	return &liveCam{cam: cam, sm: map[string]smallFrame{}}
}

// frame rend l'image réduite, bornée en taille. L'erreur est lisible (pas de caméra, panne, trop de demandes).
func (l *liveCam) frame(ctx context.Context, printerID string) (*LiveFrame, []byte, error) {
	if l == nil || l.cam == nil {
		return nil, nil, errors.New("aucune caméra configurée sur ce serveur")
	}
	ph, cached, err := l.cam.LiveFrame(ctx, printerID)
	if err != nil {
		return nil, nil, err
	}
	l.mu.Lock()
	sf, ok := l.sm[printerID]
	l.mu.Unlock()
	if !ok || sf.sha != ph.SHA256 {
		small, err := shrinkJPEGMax(ph.JPEG, LiveFrameMaxWidth, 70, LiveFrameMaxBytes)
		if err != nil {
			return nil, nil, fmt.Errorf("image de la caméra illisible : %w", err)
		}
		if len(small) > LiveFrameMaxBytes {
			return nil, nil, fmt.Errorf("image de la caméra trop lourde pour la vue (%d octets, maximum %d)", len(small), LiveFrameMaxBytes)
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(small))
		if err != nil {
			return nil, nil, err
		}
		sf = smallFrame{sha: ph.SHA256, jpg: small, w: cfg.Width, h: cfg.Height}
		l.mu.Lock()
		l.sm[printerID] = sf
		l.mu.Unlock()
	}
	out := &LiveFrame{PrinterID: printerID, MIME: "image/jpeg", Data: base64.StdEncoding.EncodeToString(sf.jpg), TakenAt: ph.TakenAt, TakenAtLocal: localtime.Full(ph.TakenAt),
		AgeSeconds: max(0, int(time.Since(ph.TakenAt).Seconds())), Width: sf.w, Height: sf.h, SizeBytes: len(sf.jpg), Cached: cached, NextAfterMS: liveFrameNextMS}
	return out, sf.jpg, nil
}

// photoContent : l'instantané de repli de get_print_status, en contenu image MCP (ou une ligne d'explication).
func (l *liveCam) photoContent(ctx context.Context, printerID string) []mcp.Content {
	cctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	f, jpg, err := l.frame(cctx, printerID)
	if err != nil {
		return []mcp.Content{&mcp.TextContent{Text: "Photo de la caméra indisponible : " + err.Error()}}
	}
	return []mcp.Content{
		&mcp.TextContent{Text: fmt.Sprintf("Photo de la caméra prise à %s (%dx%d, il y a %d s). Juge-la toi-même ; elle ne prouve pas que tout va bien.", f.TakenAtLocal, f.Width, f.Height, f.AgeSeconds)},
		&mcp.ImageContent{Data: jpg, MIMEType: "image/jpeg"},
	}
}

// AddLiveTools enregistre la carte de suivi en direct et ses deux outils réservés à l'application. Lecture seule : aucun
// n'envoie la moindre commande à l'imprimante.
func AddLiveTools(s *mcp.Server, c *core.Core) {
	p := c.Printing
	html, _ := viewHTML()
	addViewResource(s, LiveViewURI, "print_live_view", "Suivi d'impression en direct",
		"Carte de suivi d'une impression (get_print_status, wait_for_print_change) : caméra rafraîchie toutes les 2 à 3 s, progression, couche, temps restant et heure de fin, températures, étapes, alertes, boutons Arrêter et Reprendre (demandes validées par l'humain).", html)

	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	f := false
	obs := &mcp.ToolAnnotations{DestructiveHint: &f, IdempotentHint: true} // relit l'imprimante et met à jour l'impression, comme get_print_status
	live := newLiveCam(c.Camera)

	addTool(s, &mcp.Tool{
		Name: "get_live_frame", Title: "Image caméra du suivi en direct",
		Description: "RÉSERVÉ À LA VUE DE SUIVI (non destiné au modèle ; pour voir l'imprimante utiliser get_camera_snapshot) : image JPEG réduite (640 px, ≤ 64 000 octets) en base64. " +
			"Une seule capture est faite par fenêtre de 2 s et par imprimante quel que soit le nombre de vues ouvertes (la caméra de la P1S n'accepte que peu de clients) ; refusée si aucune imprimante ou caméra.",
		Annotations: ro, Meta: appOnlyMeta(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in LiveFrameInput) (*mcp.CallToolResult, LiveFrame, error) {
		if live == nil {
			return nil, LiveFrame{}, errors.New("aucune caméra configurée sur ce serveur")
		}
		id, err := resolvePrinter(ctx, c, in.PrinterID)
		if err != nil {
			return nil, LiveFrame{}, err
		}
		fr, _, err := live.frame(ctx, id)
		if err != nil {
			return nil, LiveFrame{}, err
		}
		return textResult(fmt.Sprintf("Image de %s prise à %s (%d octets).", id, fr.TakenAtLocal, fr.SizeBytes)), *fr, nil
	})

	addTool(s, &mcp.Tool{
		Name: "get_live_state", Title: "État du suivi en direct",
		Description: "RÉSERVÉ À LA VUE DE SUIVI (non destiné au modèle ; utiliser get_print_status ou get_printer_status) : avec print_job_id OU printer_id (impression active de l'imprimante, sinon no_active_print=true et l'état de l'imprimante) le suivi complet de l'impression (progression, couche, températures, étapes, alertes) plus l'heure de fin locale, la hauteur imprimée et, " +
			"une fois terminée, le résumé final (durée réelle, grammes, photo de fin). Même effet de bord local que get_print_status.",
		Annotations: obs, Meta: appOnlyMeta(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in LiveStateInput) (*mcp.CallToolResult, LiveState, error) {
		if in.PrintJobID == "" { // carte ouverte depuis get_printer_status : retrouver l'impression active de l'imprimante
			id, err := resolvePrinter(ctx, c, in.PrinterID)
			if err != nil {
				return nil, LiveState{}, err
			}
			jobID, err := activePrintJobID(ctx, p, id)
			if err != nil {
				return nil, LiveState{}, err
			}
			if jobID == "" {
				ps, err := p.GetPrinterStatus(ctx, id)
				if err != nil {
					return nil, LiveState{}, err
				}
				out := LiveState{Status: printing.PrintStatus{Job: printing.Job{PrinterID: id}, Printer: ps}, NoActivePrint: true, CameraAvailable: live != nil}
				return textResult(fmt.Sprintf("Aucune impression suivie sur %s : imprimante %s.", id, ps.State)), out, nil
			}
			in.PrintJobID = jobID
		}
		st, err := p.GetPrintStatus(ctx, in.PrintJobID)
		if err != nil {
			return nil, LiveState{}, err
		}
		out := buildLiveState(ctx, c, live, st, in.WithFinalPhoto)
		return textResult(printStatusText(st)), out, nil
	})
}

// activePrintJobID : l'impression la plus récente de l'imprimante qui est en cours (ou dont le lancement est en cours ou
// incertain) ; vide s'il n'y en a pas.
func activePrintJobID(ctx context.Context, p *printing.Service, printerID string) (string, error) {
	jobs, err := p.ListJobs(ctx, "", 0) // du plus récent au plus ancien
	if err != nil {
		return "", err
	}
	for _, j := range jobs {
		if j.PrinterID != printerID {
			continue
		}
		switch j.State {
		case contracts.PrintPrinting, contracts.PrintStartRequested, contracts.PrintStartUnknown:
			return j.ID, nil
		}
	}
	return "", nil
}

func isTerminalPrint(s contracts.PrintState) bool {
	return s == contracts.PrintCompleted || s == contracts.PrintFailed || s == contracts.PrintCancelled
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// buildLiveState dérive de PrintStatus ce que la carte affiche en plus : heure de fin locale, hauteur, résumé final.
func buildLiveState(ctx context.Context, c *core.Core, live *liveCam, st *printing.PrintStatus, finalPhoto bool) LiveState {
	j := st.Job
	out := LiveState{Status: *st, Terminal: isTerminalPrint(j.State), CameraAvailable: live != nil}
	var plate *plateInfo
	if j.Summary != nil && len(j.Summary.Plates) > 0 {
		pl := j.Summary.Plates[0]
		for _, q := range j.Summary.Plates {
			if q.Index == j.Plate {
				pl = q
			}
		}
		plate = &plateInfo{maxZ: pl.MaxZ, grams: pl.WeightGrams}
	}
	layer, total, remaining := 0, 0, 0
	if j.Print != nil {
		layer, total, remaining = j.Print.Layer, j.Print.TotalLayers, j.Print.RemainingMin
	}
	if pr := st.Printer; pr != nil && j.State == contracts.PrintPrinting && pr.Reachable && !pr.Stale {
		if pr.TotalLayers > 0 {
			layer, total = pr.Layer, pr.TotalLayers
		}
		remaining = pr.RemainingMin
	}
	if !out.Terminal && remaining > 0 && (j.State == contracts.PrintPrinting || j.State == contracts.PrintStartRequested) {
		end := time.Now().UTC().Add(time.Duration(remaining) * time.Minute)
		out.EndsAt = end
		out.EndsAtLocal = endLocal(end, time.Now())
	}
	if plate != nil && plate.maxZ > 0 && total > 0 && layer >= 0 {
		h, tot := round1(plate.maxZ*math.Min(1, float64(layer)/float64(total))), round1(plate.maxZ)
		out.HeightMM, out.HeightTotalMM = &h, &tot
	}
	if out.Terminal {
		out.Final = buildFinal(ctx, c, live, st, plate, finalPhoto)
	}
	return out
}

type plateInfo struct{ maxZ, grams float64 }

// endLocal : « 21:05 » le jour même, « 21:05 (10/11/2026) » un autre jour, dans le fuseau de l'utilisateur.
func endLocal(end, now time.Time) string {
	e := localtime.In(end)
	s := e.Format("15:04")
	if localtime.Date(end) != localtime.Date(now) {
		s += " (" + localtime.Date(end) + ")"
	}
	return s
}

func fmtDuration(sec int) string {
	switch {
	case sec < 60:
		return fmt.Sprintf("%d s", sec)
	case sec < 3600:
		return fmt.Sprintf("%d min %02d s", sec/60, sec%60)
	}
	return fmt.Sprintf("%d h %02d min", sec/3600, (sec%3600)/60)
}

func buildFinal(ctx context.Context, c *core.Core, live *liveCam, st *printing.PrintStatus, plate *plateInfo, withPhoto bool) *LiveFinal {
	j := st.Job
	fin := &LiveFinal{Outcome: map[contracts.PrintState]string{contracts.PrintCompleted: "completed", contracts.PrintFailed: "failed", contracts.PrintCancelled: "cancelled"}[j.State]}
	var started, ended time.Time
	for _, e := range j.Events {
		if e.To == contracts.PrintPrinting && started.IsZero() {
			started = e.At
		}
		if e.To == j.State {
			ended = e.At
		}
	}
	if !started.IsZero() && !ended.IsZero() && ended.After(started) {
		fin.DurationSeconds = int(ended.Sub(started).Seconds())
	}
	if !ended.IsZero() {
		fin.FinishedAtLocal = localtime.Full(ended)
	}
	if plate != nil && plate.grams > 0 {
		fin.Grams, fin.GramsBasis = round1(plate.grams), "estimé par la découpe"
	}
	if c.Insights != nil { // le journal connaît la durée réelle et la masse (pesée ou décomptée de la bobine)
		if e, err := c.Insights.GetLog(ctx, j.ID); err == nil && e != nil {
			if e.Actual.Seconds > 0 {
				fin.DurationSeconds = int(e.Actual.Seconds)
			}
			if e.Actual.Grams > 0 {
				fin.Grams, fin.GramsBasis = round1(e.Actual.Grams), "journal des impressions ("+firstNonEmptyStr(e.Actual.Basis, "mesure")+")"
			}
			if !e.FinishedAt.IsZero() {
				fin.FinishedAtLocal = localtime.Full(e.FinishedAt)
			}
		}
	}
	if fin.DurationSeconds > 0 {
		fin.Duration = fmtDuration(fin.DurationSeconds)
	}
	if withPhoto {
		fin.Photo, fin.PhotoNote = finalPhotoForView(ctx, c, j)
	}
	return fin
}

// finalPhotoForView : la photo de fin déjà prise (ou une nouvelle si aucune), réduite.
func finalPhotoForView(ctx context.Context, c *core.Core, j printing.Job) (*LaunchPhoto, string) {
	if c.Camera == nil {
		return nil, "Aucune caméra configurée."
	}
	cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	raw, _, err := c.Camera.FinalPhoto(cctx, j.PrinterID, j.ID)
	if err != nil {
		return nil, "Photo de fin indisponible (" + err.Error() + ")."
	}
	small, err := shrinkJPEGMax(raw, LiveFrameMaxWidth, 70, LiveFrameMaxBytes)
	if err != nil || len(small) > LiveFrameMaxBytes {
		return nil, "Photo de fin illisible."
	}
	return &LaunchPhoto{MIME: "image/jpeg", Data: base64.StdEncoding.EncodeToString(small)}, "" // l'heure exacte de la prise de vue n'est pas rendue par FinalPhoto
}
