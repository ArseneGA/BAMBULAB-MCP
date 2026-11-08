package mcpserver

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

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
	"MODE D'EMPLOI : 1) calcule la taille en octets et le sha256 du fichier entier (ex. hashlib.sha256 / sha256sum), 2) découpe les octets en morceaux de 75000 octets au plus et encode chacun en base64 séparément (≤ 100000 caractères), " +
	"3) premier appel : filename, total_size, sha256, offset=0, data_base64 du premier morceau (+ collection, tags, license, source si utile) ; la réponse donne upload_id et next_offset, " +
	"4) appels suivants : upload_id, offset=next_offset renvoyé, data_base64 du morceau suivant (ne répète pas filename/total_size/sha256) ; un fichier de 75000 octets ou moins tient en UN seul appel. " +
	"Au dernier morceau le service recalcule le sha256, contrôle la signature (STL binaire/ASCII, OBJ, 3MF zip, STEP ISO-10303), refuse les fichiers trop gros, range le fichier dans son stockage et l'importe : la réponse a status=complete et file (model_id, statut imported | new_version | duplicate). " +
	"REPRISE : en cas de coupure ou d'erreur d'offset, appelle get_upload_status(upload_id) et repars de next_offset ; un morceau déjà reçu est ignoré sans erreur. Une session inactive expire (TTL dans get_capabilities, section upload) ; cancel_upload l'abandonne. " +
	"Un fichier de même nom dans la même collection devient une nouvelle version du modèle ; un contenu identique déjà présent n'est pas dupliqué. Tailles maximales et dossiers d'import : get_capabilities."

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
	return textResult(txt + ". " + res.Note), out, nil
}

func outFrom(st catalog.UploadStatus) UploadFileOutput {
	return UploadFileOutput{UploadID: st.UploadID, Status: "in_progress", Received: st.Received, TotalSize: st.Total, NextOffset: st.NextOffset,
		ExpiresAt: st.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")}.withProgress(st.Filename)
}

func (h *uploadHandlers) status(ctx context.Context, _ *mcp.CallToolRequest, in UploadIDInput) (*mcp.CallToolResult, UploadFileOutput, error) {
	st, err := h.svc.UploadStatus(ctx, in.UploadID)
	if err != nil {
		return nil, UploadFileOutput{}, err
	}
	o := outFrom(st)
	o.Note = fmt.Sprintf("%s — %d/%d octets reçus ; reprendre à l'offset %d.", o.Text, st.Received, st.Total, st.NextOffset)
	return textResult(o.Note), o, nil
}

func (h *uploadHandlers) cancel(_ context.Context, _ *mcp.CallToolRequest, in UploadIDInput) (*mcp.CallToolResult, CancelUploadOutput, error) {
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
	return textResult(fmt.Sprintf("Import depuis %s : %d fichier(s). %s", r.Source, len(r.Files), r.Note)), *r, nil
}
