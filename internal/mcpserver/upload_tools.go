package mcpserver

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/core"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
)

// Envoi de fichiers par morceaux et import par lien (chantier A). Logique dans internal/catalog (upload.go,
// urlimport.go), partagée avec les routes HTTP (httpapi/upload.go).

var capsHooked sync.Map // *core.Core -> struct{}

// UploadFileInput : un morceau d'un fichier envoyé par Claude.
type UploadFileInput struct {
	UploadID   string `json:"upload_id,omitempty" jsonschema:"identifiant de la session d'envoi : ABSENT au premier appel (offset 0), puis celui renvoyé par l'appel précédent"`
	Filename   string `json:"filename,omitempty" jsonschema:"premier appel seulement : nom du fichier AVEC extension (.stl .obj .3mf .step .stp), sans dossier ni chemin, ex. support-camera.stl"`
	TotalSize  int64  `json:"total_size,omitempty" jsonschema:"premier appel seulement : taille totale du fichier en OCTETS (avant base64)"`
	SHA256     string `json:"sha256,omitempty" jsonschema:"premier appel seulement : empreinte sha256 hexadécimale (64 caractères) du fichier ENTIER ; recalculée par le service, l'envoi est refusé si elle diffère"`
	Offset     int64  `json:"offset,omitempty" jsonschema:"position, en octets du fichier d'origine, du premier octet de ce morceau (0 au premier appel, puis next_offset renvoyé)"`
	DataBase64 string `json:"data_base64" jsonschema:"morceau encodé en base64 STANDARD, 100000 caractères au plus (soit 75000 octets bruts) ; chaque morceau est encodé séparément"`
	catalog.UploadMeta
}

// UploadFileOutput : état après un morceau.
type UploadFileOutput struct {
	UploadID   string              `json:"upload_id"`
	Status     string              `json:"status" jsonschema:"in_progress (envoyer le morceau suivant à next_offset) | complete (fichier contrôlé et importé, voir file)"`
	Received   int64               `json:"received" jsonschema:"octets reçus jusqu'ici"`
	TotalSize  int64               `json:"total_size"`
	NextOffset int64               `json:"next_offset"`
	ExpiresAt  string              `json:"expires_at,omitempty"`
	File       *catalog.FileResult `json:"file,omitempty" jsonschema:"présent quand status=complete : modèle créé, nouvelle version ou doublon"`
	Note       string              `json:"note,omitempty"`
	// Progression (champs structurés stables, mêmes noms que jobs.Progress).
	Percent    int    `json:"percent" jsonschema:"avancement 0..100 (octets reçus / total)"`
	DoneBytes  int64  `json:"done_bytes" jsonschema:"octets reçus"`
	TotalBytes int64  `json:"total_bytes" jsonschema:"octets attendus"`
	Phase      string `json:"phase" jsonschema:"receiving | complete"`
	PhaseLabel string `json:"phase_label"`
	Bar        string `json:"bar" jsonschema:"barre texte de 10 cases"`
	Text       string `json:"text" jsonschema:"ligne lisible : « Réception de x.stl : ███████░░░ 72 % (21,6 / 30 Mo) »"`
}

// withProgress complète les champs de progression d'un envoi (octets reçus / total).
func (o UploadFileOutput) withProgress(name string) UploadFileOutput {
	o.DoneBytes, o.TotalBytes = o.Received, o.TotalSize
	if o.TotalSize > 0 {
		o.Percent = int(min(100, o.Received*100/o.TotalSize))
	}
	o.Phase, o.PhaseLabel = "receiving", "Réception de "+name
	if o.Status == "complete" {
		o.Percent, o.Phase, o.PhaseLabel = 100, "complete", "Réception terminée : "+name
	}
	if name == "" {
		o.PhaseLabel = "Réception du fichier"
	}
	o.Bar = jobs.Bar(o.Percent, jobs.BarWidth)
	o.Text = fmt.Sprintf("%s : %s %d %%", o.PhaseLabel, o.Bar, o.Percent)
	if o.TotalSize > 0 {
		o.Text += fmt.Sprintf(" (%s / %s Mo)", jobs.FormatMB(o.DoneBytes), jobs.FormatMB(o.TotalBytes))
	}
	return o
}

// UploadIDInput désigne une session d'envoi.
type UploadIDInput struct {
	UploadID string `json:"upload_id" jsonschema:"identifiant renvoyé par upload_file"`
}

// CancelUploadOutput : envoi abandonné.
type CancelUploadOutput struct {
	UploadID string `json:"upload_id"`
	Canceled bool   `json:"canceled"`
}

const uploadFileDescription = "Envoie à l'atelier un fichier 3D (STL, OBJ, 3MF, STEP) que tu viens de GÉNÉRER ou de recevoir, pour qu'il entre au catalogue " +
	"(modèle, versions, analyse, aperçu 3D). Les fichiers du disque de l'utilisateur ne sont PAS lisibles par le service : utilise cet outil, pas import_files. " +
	"SI TON ENVIRONNEMENT DE CODE A ACCÈS À INTERNET, PRÉFÈRE create_upload_link : une seule commande curl, sans découpage. " +
	"TAILLES : un morceau = 75000 octets bruts au plus (≈ 100000 caractères base64). Un fichier de 75000 octets ou moins tient en UN SEUL appel : 35000 caractères base64 (≈ 26 Ko) ne sont PAS « trop longs », envoie-les tels quels ; un 3MF de 300 Ko = 4 appels. " +
	"MODE D'EMPLOI : 1) dans ton environnement de code, calcule la taille et le sha256 du fichier ENTIER, découpe et encode : " +
	"data=open(p,'rb').read(); sha=hashlib.sha256(data).hexdigest(); chunks=[base64.b64encode(data[i:i+75000]).decode() for i in range(0,len(data),75000)] ; " +
	"2) premier appel : filename (avec extension, sans chemin), total_size = len(data), sha256 = sha, offset=0, data_base64 = chunks[0] (+ collection, tags, license, source si utile) ; la réponse donne upload_id et next_offset ; " +
	"3) appels suivants : upload_id, offset = next_offset renvoyé, data_base64 = morceau suivant (ne répète pas filename/total_size/sha256) ; " +
	"4) au dernier morceau le service recalcule le sha256 du fichier entier et le compare à celui annoncé (EMPREINTE FINALE : refus si elle diffère, le fichier n'est alors pas importé), contrôle la signature (STL binaire/ASCII, OBJ, 3MF zip, STEP ISO-10303), refuse les fichiers trop gros, le range dans son stockage et l'importe : la réponse a status=complete et file (model_id, statut imported | new_version | duplicate). " +
	"Garde le découpage choisi : chaque morceau garde son offset. " +
	"REPRISE : en cas de coupure, de délai dépassé ou d'erreur d'offset, appelle get_upload_status(upload_id) et repars de next_offset (jamais de zéro) ; un morceau déjà reçu est ignoré sans erreur. Une session inactive expire (TTL dans get_capabilities, section upload) ; cancel_upload l'abandonne. " +
	"Un fichier de même nom dans la même collection devient une nouvelle version du modèle ; un contenu identique déjà présent n'est pas dupliqué. Tailles maximales et dossiers d'import : get_capabilities. " +
	"APRÈS L'ENVOI, la réponse propose la carte de la pièce (viewer 3D) : appelle get_model_details(model_id) au lieu de fabriquer tes propres rendus."

const createUploadLinkDescription = "Crée une URL d'envoi À USAGE UNIQUE (jeton aléatoire, valable 10 minutes, taille maximale bornée, types .stl .obj .3mf .step .stp) et renvoie la commande curl EXACTE à exécuter depuis ton environnement de code : " +
	"le fichier entier part en un seul PUT, sans découpage ni base64, et entre dans la bibliothèque comme import_model (original non modifié, signature contrôlée, empreinte sha256 vérifiée si tu la donnes). " +
	"MODE D'EMPLOI : 1) calcule size (octets) et sha256 du fichier ; 2) appelle create_upload_link(filename, size, sha256) ; 3) exécute la commande curl renvoyée (remplace le chemin du fichier si besoin) ; sa réponse JSON donne file.model_id ; " +
	"4) si tu ne vois pas la réponse : get_upload_status(link_id) ; 5) montre la pièce avec get_model_details(model_id) (viewer 3D). " +
	"Le lien ne sert qu'UNE fois : en cas d'échec, crée-en un nouveau. Il ne donne accès à rien d'autre. " +
	"Si internet_reachable=false ou si curl ne peut pas joindre l'adresse (environnement de code sans réseau), utilise upload_file par morceaux de 75000 octets, ou propose à l'utilisateur la carte show_file_drop. Le format .glb n'est pas importable."

const importURLDescription = "Importe un modèle depuis un LIEN https (page de modèle ou fichier direct) : seuls les domaines autorisés par le service sont acceptés (par défaut MakerWorld et Printables et leurs CDN ; liste exacte dans get_capabilities, upload.url_import). " +
	"HTTPS uniquement, redirections hors liste refusées, adresses privées refusées, taille et délai bornés ; les archives zip et 3MF sont validées. La provenance (site, URL, auteur) et la licence, quand la page les expose, sont enregistrées au catalogue. " +
	"Beaucoup de pages exigent une connexion pour télécharger : l'outil renvoie alors une erreur claire avec la marche à suivre (télécharger le fichier dans un navigateur connecté, puis upload_file). Cet outil n'ouvre jamais de page en dehors de la liste."

func registerUploadTools(s *mcp.Server, c *core.Core) {
	svc := c.Catalog
	if _, loaded := capsHooked.LoadOrStore(c, struct{}{}); !loaded {
		c.CapabilityHooks = append(c.CapabilityHooks, func(cp *contracts.Capabilities) {
			l := svc.UploadLimits()
			cp.Upload = &l
			cp.Features = append(cp.Features,
				contracts.Feature{ID: "file_upload", Available: true, Note: fmt.Sprintf("upload_file par morceaux base64 (≤ %d caractères), %d Mo max par fichier", l.MaxChunkChars, l.MaxUploadBytes>>20)},
				contracts.Feature{ID: "url_import", Available: l.URLImport.Enabled, Note: l.URLImport.Note})
		})
	}
	f, t := false, true
	additive := &mcp.ToolAnnotations{DestructiveHint: &f}
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	idem := &mcp.ToolAnnotations{DestructiveHint: &t, IdempotentHint: true}
	h := &uploadHandlers{svc: svc}
	addTool(s, &mcp.Tool{Name: "upload_file", Title: "Envoyer un fichier 3D", Description: uploadFileDescription, Annotations: additive}, h.uploadFile)
	addTool(s, &mcp.Tool{Name: "create_upload_link", Title: "Lien d'envoi à usage unique", Description: createUploadLinkDescription, Annotations: additive}, h.createLink)
	addTool(s, &mcp.Tool{
		Name: "show_file_drop", Title: "Déposer un fichier",
		Description: "Affiche dans la conversation une carte « Déposer un fichier » : l'utilisateur y glisse un fichier 3D (STL, OBJ, 3MF, STEP) de son ordinateur ; il part par morceaux avec une barre de progression, puis la carte montre l'aperçu 3D de la pièce. " +
			"À proposer quand l'utilisateur a un fichier sur son disque (tu ne peux pas le lire). Sans carte affichée, l'utilisateur peut utiliser la page Importer de l'interface web de l'atelier.",
		Annotations: ro,
	}, h.showDrop)
	addTool(s, &mcp.Tool{
		Name: "drop_file_chunk", Title: "Morceau déposé depuis la carte",
		Description: "RÉSERVÉ À LA VUE « Déposer un fichier » (non destiné au modèle) : mêmes paramètres et même effet que upload_file. Pour envoyer un fichier toi-même : create_upload_link ou upload_file.",
		Annotations: additive, Meta: appOnlyMeta(),
	}, h.uploadFile)
	addTool(s, &mcp.Tool{Name: "get_upload_status", Title: "État d'un envoi", Description: "État d'une session d'envoi (octets reçus, next_offset pour reprendre après une coupure).", Annotations: ro}, h.status)
	addTool(s, &mcp.Tool{Name: "cancel_upload", Title: "Annuler un envoi", Description: "Abandonne une session d'envoi et supprime ses octets temporaires.", Annotations: idem}, h.cancel)
	addTool(s, &mcp.Tool{Name: "import_from_url", Title: "Importer depuis un lien", Description: importURLDescription, Annotations: &mcp.ToolAnnotations{DestructiveHint: &f, OpenWorldHint: &t}}, h.importURL)
}

type uploadHandlers struct{ svc *catalog.Service }

func decodeChunk(s string) ([]byte, error) {
	if len(s) > catalog.MaxChunkChars {
		return nil, fmt.Errorf("morceau trop grand : %d caractères base64 (max %d) ; découper en morceaux de 75000 octets", len(s), catalog.MaxChunkChars)
	}
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("data_base64 n'est pas du base64 valide (chaque morceau doit être encodé séparément en base64 standard)")
}

func (h *uploadHandlers) uploadFile(ctx context.Context, _ *mcp.CallToolRequest, in UploadFileInput) (*mcp.CallToolResult, UploadFileOutput, error) {
	data, err := decodeChunk(in.DataBase64)
	if err != nil {
		return nil, UploadFileOutput{}, err
	}
	id := strings.TrimSpace(in.UploadID)
	if id == "" {
		if in.SHA256 == "" {
			return nil, UploadFileOutput{}, errors.New("sha256 requis au premier appel (empreinte du fichier entier, 64 caractères hexadécimaux)")
		}
		if in.Offset != 0 {
			return nil, UploadFileOutput{}, errors.New("le premier appel (sans upload_id) doit avoir offset=0")
		}
		st, err := h.svc.UploadBegin(ctx, in.Filename, in.TotalSize, in.SHA256, in.UploadMeta)
		if err != nil {
			return nil, UploadFileOutput{}, err
		}
		id = st.UploadID
	}
	st, err := h.svc.UploadWrite(ctx, id, in.Offset, data)
	if err != nil {
		var mm *catalog.UploadChunkMismatchError
		if errors.As(err, &mm) {
			o := outFrom(mm.Status)
			o.Note = "offset incorrect : renvoyer le morceau qui commence à next_offset=" + fmt.Sprint(mm.Status.NextOffset)
			return textResult(o.Note), o, nil
		}
		// Premier appel échoué après ouverture : ne pas laisser de session orpheline.
		if strings.TrimSpace(in.UploadID) == "" {
			_ = h.svc.UploadCancel(id)
		}
		return nil, UploadFileOutput{}, err
	}
	out := outFrom(st)
	if !st.Complete {
		out.Note = fmt.Sprintf("%s — %d/%d octets reçus ; envoyer le morceau suivant avec upload_id et offset=%d.", out.Text, st.Received, st.Total, st.NextOffset)
		return textResult(out.Note), out, nil
	}
	res, err := h.svc.UploadFinish(ctx, id)
	if err != nil {
		return nil, UploadFileOutput{UploadID: id}, fmt.Errorf("envoi terminé mais refusé (la session est fermée, recommencer l'envoi) : %w", err)
	}
	out.Status, out.File, out.Note = "complete", &res.File, res.Note
	out = out.withProgress(st.Filename)
	txt := fmt.Sprintf("Fichier %s reçu et contrôlé : %s", st.Filename, res.File.Status)
	if res.File.ModelID != "" {
		txt += " (modèle " + res.File.ModelID + ")"
	}
	return withModelCard(textResult(txt+". "+res.Note), res.File.ModelID), out, nil
}

// modelCardHint : phrase qui propose la carte de la pièce (viewer 3D existant) après un import ou un envoi réussi.
func modelCardHint(modelID string) string {
	return " Carte de la pièce : get_model_details(model_id=\"" + modelID + "\") ouvre le viewer 3D dans la conversation ; ne fabrique pas tes propres rendus."
}

// withModelCard ajoute au résultat d'un import/envoi réussi la proposition de la carte de la pièce, et le lien vers la
// vue du modèle sur le RÉSULTAT seulement (jamais sur la définition de l'outil : chaque morceau d'upload_file ne doit
// pas ouvrir de carte).
func withModelCard(r *mcp.CallToolResult, modelID string) *mcp.CallToolResult {
	if modelID == "" {
		return r
	}
	if t, ok := r.Content[0].(*mcp.TextContent); ok {
		t.Text += modelCardHint(modelID)
	}
	return withView(r, "get_model_details")
}

// CreateUploadLinkInput : fichier à envoyer par lien.
type CreateUploadLinkInput struct {
	Filename string `json:"filename" jsonschema:"nom du fichier AVEC extension (.stl .obj .3mf .step .stp), sans dossier ni chemin"`
	Size     int64  `json:"size,omitempty" jsonschema:"taille du fichier en octets (recommandé : sert de plafond et de contrôle)"`
	SHA256   string `json:"sha256,omitempty" jsonschema:"empreinte sha256 hexadécimale (64 caractères) du fichier ENTIER (recommandé : l'envoi est refusé si elle diffère)"`
	catalog.UploadMeta
}

// CreateUploadLinkOutput : l'URL et la commande à exécuter.
type CreateUploadLinkOutput struct {
	LinkID            string   `json:"link_id" jsonschema:"suivre le résultat avec get_upload_status(link_id)"`
	URL               string   `json:"url" jsonschema:"URL d'envoi à usage unique (secrète : ne la montre pas à l'utilisateur)"`
	Method            string   `json:"method" jsonschema:"PUT"`
	Curl              string   `json:"curl" jsonschema:"commande curl exacte à exécuter depuis l'environnement de code (remplacer CHEMIN par le fichier si besoin)"`
	ExpiresAt         string   `json:"expires_at"`
	ExpiresInSeconds  int      `json:"expires_in_seconds"`
	MaxBytes          int64    `json:"max_bytes"`
	AllowedTypes      []string `json:"allowed_types"`
	SingleUse         bool     `json:"single_use"`
	InternetReachable bool     `json:"internet_reachable" jsonschema:"l'URL est celle de l'écouteur public (joignable depuis Internet) ; false = réseau privé seulement"`
	Note              string   `json:"note"`
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (h *uploadHandlers) createLink(ctx context.Context, _ *mcp.CallToolRequest, in CreateUploadLinkInput) (*mcp.CallToolResult, CreateUploadLinkOutput, error) {
	info, err := h.svc.CreateUploadLink(ctx, in.Filename, in.Size, in.SHA256, in.UploadMeta)
	if err != nil {
		return nil, CreateUploadLinkOutput{}, err
	}
	base, public := h.svc.UploadLinkBase()
	if base == "" {
		_ = h.svc.CancelUploadLink(info.LinkID)
		return nil, CreateUploadLinkOutput{}, errors.New("adresse du service inconnue : utiliser upload_file par morceaux")
	}
	url := base + "/upload/" + info.Token
	out := CreateUploadLinkOutput{
		LinkID: info.LinkID, URL: url, Method: "PUT",
		Curl:      "curl -sS -X PUT -T " + shQuote("CHEMIN/"+info.Filename) + " -H 'Content-Type: application/octet-stream' " + shQuote(url),
		ExpiresAt: info.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"), ExpiresInSeconds: int(catalog.UploadLinkTTL / time.Second),
		MaxBytes: info.MaxBytes, AllowedTypes: []string{".stl", ".obj", ".3mf", ".step", ".stp"}, SingleUse: true, InternetReachable: public,
	}
	out.Note = "Exécute la commande curl (remplace CHEMIN par le dossier du fichier) : le lien n'est valable que 10 minutes et pour UN envoi. Réponse HTTP 200 = fichier importé (JSON avec file.model_id) ; sinon get_upload_status(link_id)."
	if !public {
		out.Note += " ATTENTION : cette adresse n'est joignable que depuis le réseau privé de l'atelier ; si curl ne l'atteint pas, utilise upload_file par morceaux."
	}
	txt := "Lien d'envoi créé (usage unique, valable 10 min, max " + fmt.Sprint(info.MaxBytes>>10) + " Kio). Exécute : " + out.Curl + " . " + out.Note
	return textResult(txt), out, nil
}

// ShowFileDropInput : options d'import appliquées aux fichiers déposés.
type ShowFileDropInput struct {
	catalog.UploadMeta
}

// ShowFileDropOutput : ce que la carte a besoin de connaître.
type ShowFileDropOutput struct {
	Kind          string              `json:"kind" jsonschema:"toujours file_drop"`
	Accept        []string            `json:"accept" jsonschema:"extensions acceptées"`
	MaxUploadByte int64               `json:"max_upload_bytes"`
	ChunkBytes    int                 `json:"chunk_bytes" jsonschema:"octets bruts par morceau envoyé par la carte"`
	Meta          catalog.UploadMeta  `json:"meta" jsonschema:"options d'import appliquées au fichier déposé"`
	Limits        catalog.UploadLimits `json:"limits"`
}

// DropChunkBytes : taille des morceaux envoyés par la carte (≤ 75000 octets bruts = 100 000 caractères base64).
const DropChunkBytes = 72_000

func (h *uploadHandlers) showDrop(_ context.Context, _ *mcp.CallToolRequest, in ShowFileDropInput) (*mcp.CallToolResult, ShowFileDropOutput, error) {
	l := h.svc.UploadLimits()
	out := ShowFileDropOutput{Kind: "file_drop", Accept: []string{".stl", ".obj", ".3mf", ".step", ".stp"}, MaxUploadByte: l.MaxUploadBytes, ChunkBytes: DropChunkBytes, Meta: in.UploadMeta, Limits: l}
	return textResult("Carte « Déposer un fichier » affichée dans la conversation : l'utilisateur y glisse son fichier 3D (STL, OBJ, 3MF, STEP), qui part par morceaux puis s'affiche en 3D. Sans carte visible, il peut utiliser la page Importer de l'interface web."), out, nil
}

func (h *uploadHandlers) linkStatus(ctx context.Context, id string) (*mcp.CallToolResult, UploadFileOutput, error) {
	st, err := h.svc.UploadLinkStatus(ctx, id)
	if err != nil {
		return nil, UploadFileOutput{}, err
	}
	o := UploadFileOutput{UploadID: id, Status: "in_progress", Received: st.Received, TotalSize: st.Total, ExpiresAt: st.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")}
	switch st.State {
	case "complete":
		o.Status = "complete"
		if st.Result != nil {
			o.File, o.Note = &st.Result.File, st.Result.Note
		}
	case "failed", "expired":
		o.Status = st.State
		o.Note = st.Error
	}
	o = o.withProgress(st.Filename)
	switch st.State {
	case "pending":
		o.Note = "En attente du PUT sur l'URL du lien (valable jusqu'à " + o.ExpiresAt + ")."
	case "expired":
		o.Note = strings.TrimSpace("Lien expiré ou annulé : en créer un nouveau (create_upload_link). " + o.Note)
	case "failed":
		o.Note = "Envoi refusé : " + st.Error + " (le lien est consommé : en créer un nouveau)."
	}
	r := textResult(o.Note)
	if o.File != nil {
		return withModelCard(textResult(fmt.Sprintf("Fichier %s reçu et contrôlé : %s. %s", st.Filename, o.File.Status, o.Note)), o.File.ModelID), o, nil
	}
	return r, o, nil
}

func outFrom(st catalog.UploadStatus) UploadFileOutput {
	return UploadFileOutput{UploadID: st.UploadID, Status: "in_progress", Received: st.Received, TotalSize: st.Total, NextOffset: st.NextOffset,
		ExpiresAt: st.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")}.withProgress(st.Filename)
}

func (h *uploadHandlers) status(ctx context.Context, _ *mcp.CallToolRequest, in UploadIDInput) (*mcp.CallToolResult, UploadFileOutput, error) {
	if catalog.IsUploadLinkID(in.UploadID) {
		return h.linkStatus(ctx, in.UploadID)
	}
	st, err := h.svc.UploadStatus(ctx, in.UploadID)
	if err != nil {
		return nil, UploadFileOutput{}, err
	}
	o := outFrom(st)
	o.Note = fmt.Sprintf("%s — %d/%d octets reçus ; reprendre à l'offset %d.", o.Text, st.Received, st.Total, st.NextOffset)
	return textResult(o.Note), o, nil
}

func (h *uploadHandlers) cancel(_ context.Context, _ *mcp.CallToolRequest, in UploadIDInput) (*mcp.CallToolResult, CancelUploadOutput, error) {
	if catalog.IsUploadLinkID(in.UploadID) {
		if err := h.svc.CancelUploadLink(in.UploadID); err != nil {
			return nil, CancelUploadOutput{}, err
		}
		return textResult("Lien d'envoi annulé."), CancelUploadOutput{UploadID: in.UploadID, Canceled: true}, nil
	}
	if err := h.svc.UploadCancel(in.UploadID); err != nil {
		return nil, CancelUploadOutput{}, err
	}
	return textResult("Envoi annulé."), CancelUploadOutput{UploadID: in.UploadID, Canceled: true}, nil
}

func (h *uploadHandlers) importURL(ctx context.Context, _ *mcp.CallToolRequest, in catalog.URLImportInput) (*mcp.CallToolResult, catalog.URLImportResult, error) {
	r, err := h.svc.ImportFromURL(ctx, in)
	if err != nil {
		return nil, catalog.URLImportResult{}, err
	}
	first := ""
	for _, f := range r.Files {
		if f.ModelID != "" {
			first = f.ModelID
			break
		}
	}
	return withModelCard(textResult(fmt.Sprintf("Import depuis %s : %d fichier(s). %s", r.Source, len(r.Files), r.Note)), first), *r, nil
}
