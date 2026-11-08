package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
)

// Outils du catalogue (jalon 2, dossier §14.2 et §6). Les schémas viennent de
// contracts.MustSchemaFor via addTool ; la logique est dans internal/catalog,
// partagée avec l'API HTTP.

// ModelRefInput désigne un modèle.
type ModelRefInput struct {
	ModelID string `json:"model_id" jsonschema:"identifiant du modèle, ex. mdl_ab12..."`
}

// OpsInput : opérations à prévisualiser.
type OpsInput struct {
	Operations []catalog.Operation `json:"operations" jsonschema:"opérations à prévisualiser (aucune n'est appliquée)"`
}

// ApplyInput : jeton d'un aperçu.
type ApplyInput struct {
	Token string `json:"token" jsonschema:"jeton renvoyé par preview_file_operations (usage unique, expire)"`
}

// RestoreInput : modèles à sortir de la corbeille.
type RestoreInput struct {
	ModelIDs []string `json:"model_ids" jsonschema:"modèles en corbeille à restaurer"`
}

// registerCatalogTools ajoute les outils du catalogue au serveur.
func registerCatalogTools(s *mcp.Server, c *catalogHandlers) {
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
	f, t := false, true
	additive := &mcp.ToolAnnotations{DestructiveHint: &f}
	revers := &mcp.ToolAnnotations{DestructiveHint: &f, IdempotentHint: true}
	apply := &mcp.ToolAnnotations{DestructiveHint: &t}

	addTool(s, &mcp.Tool{
		Name: "search_models", Title: "Chercher des modèles",
		Description: "Recherche paginée dans le catalogue : texte (nom, étiquettes, fichiers), étiquettes, collection, format, disponibilité (linux | windows_only). Renvoie résumé, état d'aperçu et URL d'aperçu signée. Pagination par limit/offset (next_offset). Pour « montre-moi », « affiche » ou « voir en 3D » un modèle du catalogue, appeler cet outil (liste visuelle cliquable dans Claude) puis get_model_details : ce sont les outils qui affichent le viewer 3D, pas une description textuelle.",
		Annotations: ro,
	}, c.searchModels)
	addTool(s, &mcp.Tool{
		Name: "list_collection", Title: "Contenu d'une collection",
		Description: "Sous-collections et page de modèles d'une collection (identifiant, nom ou chemin « A/B »).",
		Annotations: ro,
	}, c.listCollection)
	addTool(s, &mcp.Tool{
		Name: "get_model_details", Title: "Fiche d'un modèle",
		Description: "Fiche complète : fichiers sources (empreinte, disponibilité), versions immuables, provenance, licence, analyse géométrique résumée, état d'aperçu, modèles de même contenu, dernier journal d'audit. À préférer pour « montre-moi ce modèle en 3D » / « ouvre la fiche » : l'hôte affiche alors le viewer 3D interactif (rotation, vues, sous-parties) ; ne pas se contenter de décrire le modèle en texte.",
		Annotations: ro,
	}, c.getModelDetails)
	addTool(s, &mcp.Tool{
		Name: "scan_folder", Title: "Inventorier un dossier",
		Description: "Inventaire EN LECTURE SEULE d'un dossier sous une racine d'import autorisée : nombre de fichiers 3D, taille, formats, espace nécessaire et disponible, déjà importé / nouveau / modifié. Rien n'est copié ni modifié. À faire avant import_files.",
		Annotations: ro,
	}, c.scanFolder)
	addTool(s, &mcp.Tool{
		Name: "import_files", Title: "Importer des fichiers",
		Description: "Import non destructif (job en file, reprenable) : copie vérifiée par empreinte dans le stockage du service, originaux jamais déplacés ni modifiés, doublons exacts ignorés, ré-import incrémental (taille+mtime+sha256), fichier modifié = nouvelle version. copy=false indexe sans copier (fichiers windows_only). Chemins hors des racines autorisées refusés. Analyse et aperçu GLB suivent en arrière-plan.",
		Annotations: additive,
	}, c.importFiles)
	addTool(s, &mcp.Tool{
		Name: "import_model", Title: "Importer un modèle",
		Description: "Importe un seul fichier (STL, OBJ, 3MF, STEP) avec unités éventuelles et attend le rapport d'import (modèle créé, doublon, analyse). Pour un dossier, utiliser scan_folder puis import_files.",
		Annotations: additive,
	}, c.importModel)
	addTool(s, &mcp.Tool{
		Name: "analyze_geometry", Title: "Analyser la géométrie",
		Description: "Mesures géométriques (dimensions, volume, aire, arêtes ouvertes, composantes, normales) et heuristiques de fabricabilité (surplombs, épaisseur, orientations) pour une machine Bambu. Les unités d'un STL/OBJ sont supposées en mm et signalées ; passer unit pour les confirmer. Aucune garantie d'imprimabilité ni de résistance.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &f, IdempotentHint: true},
	}, c.analyzeGeometry)
	addTool(s, &mcp.Tool{
		Name: "repair_mesh", Title: "Réparer le maillage",
		Description: "Réparation minimale d'un maillage : soudure des sommets, suppression des triangles dégénérés et dupliqués, orientation cohérente des normales, comblement des petits trous (max_hole_edges, défaut 32), suppression facultative des petits composants flottants (remove_floating). Renvoie le rapport avant/après (étanchéité, volume, défauts). Crée une NOUVELLE version immuable (STL en mm) : l'original et les versions précédentes ne sont jamais modifiés ; aucune version si rien n'est réparable. dry_run=true ne fait que le rapport. Ne corrige ni les arêtes non manifold, ni les auto-intersections, ni les grands trous.",
		Annotations: additive,
	}, c.repairMesh)
	addTool(s, &mcp.Tool{
		Name: "get_preview_asset", Title: "Aperçu 3D d'un modèle",
		Description: "Référence de l'aperçu GLB (viewer) avec une URL HTTP locale signée qui expire ; pas de contenu en base64. L'aperçu est dérivé et simplifié : il ne remplace jamais l'original de fabrication.",
		Annotations: revers,
	}, c.getPreviewAsset)
	addTool(s, &mcp.Tool{
		Name: "tag_models", Title: "Étiqueter des modèles",
		Description: "Ajoute ou retire des étiquettes sur des modèles (journalisé, réversible).",
		Annotations: revers,
	}, c.tagModels)
	addTool(s, &mcp.Tool{
		Name: "link_model_versions", Title: "Relier des versions",
		Description: "Déclare des modèles importés séparément comme versions successives d'un même modèle (du plus ancien au plus récent) : crée des versions immuables chaînées par parent ; les modèles intégrés vont en corbeille (restaurables).",
		Annotations: additive,
	}, c.linkVersions)
	addTool(s, &mcp.Tool{
		Name: "find_exact_duplicates", Title: "Doublons exacts",
		Description: "Groupes de fichiers de contenu identique (sha256), ou contrôle ciblé d'une empreinte / d'un fichier avant import. Ne supprime jamais rien ; pour nettoyer, passer par preview_file_operations.",
		Annotations: ro,
	}, c.findDuplicates)
	addTool(s, &mcp.Tool{
		Name: "preview_file_operations", Title: "Prévisualiser des opérations",
		Description: "Portée et différence (avant/après) d'opérations : rename, rename_file, move, add_tags, remove_tags, set_license, delete (corbeille). RIEN n'est appliqué ; renvoie un jeton à usage unique pour apply_file_operations.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &f},
	}, c.previewOps)
	addTool(s, &mcp.Tool{
		Name: "apply_file_operations", Title: "Appliquer les opérations",
		Description: "Applique un aperçu (jeton obligatoire, valable une fois, refusé si l'état a changé depuis). Tout ou rien, journal d'audit par changement. Les suppressions vont en corbeille.",
		Annotations: apply,
	}, c.applyOps)
	addTool(s, &mcp.Tool{
		Name: "restore_from_trash", Title: "Restaurer de la corbeille",
		Description: "Sort des modèles de la corbeille. Pour lister la corbeille : search_models avec only_trashed.",
		Annotations: revers,
	}, c.restore)
}

type catalogHandlers struct{ svc *catalog.Service }

func (h *catalogHandlers) searchModels(ctx context.Context, _ *mcp.CallToolRequest, in catalog.SearchQuery) (*mcp.CallToolResult, catalog.SearchResult, error) {
	r, err := h.svc.Search(ctx, in)
	if err != nil {
		return nil, catalog.SearchResult{}, err
	}
	txt := fmt.Sprintf("%d modèle(s) trouvé(s), page de %d à partir de %d.", r.Total, len(r.Models), r.Offset)
	// Le contenu texte reste la seule chose que lit le modèle quand la vue cartes est affichée : il liste donc chaque
	// résultat (nom, identifiant mdl_…, dimensions), borné pour rester court.
	for i, m := range r.Models {
		if i >= maxSearchTextRows {
			txt += fmt.Sprintf("\n… et %d autre(s) de cette page (voir structuredContent.models).", len(r.Models)-i)
			break
		}
		txt += "\n- " + modelLine(m)
	}
	if r.HasMore {
		txt += fmt.Sprintf("\nSuite : offset=%d.", *r.NextOffset)
	}
	return withView(textResult(txt), "search_models"), *r, nil
}

// maxSearchTextRows : nombre maximal de résultats détaillés dans le texte de search_models.
const maxSearchTextRows = 20

// modelLine : « nom (mdl_…) · formats · X × Y × Z mm » (nom tronqué à 80 caractères).
func modelLine(m catalog.ModelSummary) string {
	name := []rune(m.Name)
	n := string(name)
	if len(name) > 80 {
		n = string(name[:80]) + "…"
	}
	line := fmt.Sprintf("%s (%s)", n, m.ID)
	if len(m.Formats) > 0 {
		line += " · " + strings.ToUpper(strings.Join(m.Formats, "/"))
	}
	if d := m.Dimensions; d != nil {
		line += fmt.Sprintf(" · %.1f × %.1f × %.1f mm", d.X, d.Y, d.Z)
	} else {
		line += " · dimensions inconnues"
	}
	if m.Availability == "windows_only" {
		line += " · Windows seul"
	}
	return line
}

func (h *catalogHandlers) listCollection(ctx context.Context, _ *mcp.CallToolRequest, in catalog.ListCollectionInput) (*mcp.CallToolResult, catalog.CollectionListing, error) {
	r, err := h.svc.ListCollection(ctx, in)
	if err != nil {
		return nil, catalog.CollectionListing{}, err
	}
	return textResult(fmt.Sprintf("Collection « %s » : %d sous-collection(s), %d modèle(s).", r.Collection.Path, len(r.Subcollections), r.Models.Total)), *r, nil
}

func (h *catalogHandlers) getModelDetails(ctx context.Context, _ *mcp.CallToolRequest, in ModelRefInput) (*mcp.CallToolResult, catalog.ModelDetails, error) {
	d, err := h.svc.GetModelDetails(ctx, in.ModelID)
	if err != nil {
		return nil, catalog.ModelDetails{}, err
	}
	txt := fmt.Sprintf("%s : %d fichier(s), %d version(s), aperçu %s, analyse %s.", d.Model.Name, len(d.Files), len(d.Versions), d.Preview.State, d.Analysis.State)
	if d.Model.DeletedAt != nil {
		txt += " (en corbeille)"
	}
	return withView(textResult(txt), "get_model_details"), *d, nil
}

func (h *catalogHandlers) scanFolder(ctx context.Context, _ *mcp.CallToolRequest, in catalog.ScanInput) (*mcp.CallToolResult, catalog.ScanReport, error) {
	r, err := h.svc.ScanFolder(ctx, in)
	if err != nil {
		return nil, catalog.ScanReport{}, err
	}
	var fm []string
	for k, v := range r.ByFormat {
		fm = append(fm, fmt.Sprintf("%s ×%d", k, v.Count))
	}
	return textResult(fmt.Sprintf("Scan lecture seule de %s : %d fichier(s) 3D (%s), %.1f Mio ; %d nouveau(x), %d modifié(s), %d déjà importé(s).",
		r.Path, r.TotalFiles, strings.Join(fm, ", "), float64(r.TotalBytes)/(1<<20), r.NewFiles, r.ChangedFiles, r.AlreadyImported)), *r, nil
}

func (h *catalogHandlers) importFiles(ctx context.Context, _ *mcp.CallToolRequest, in catalog.ImportInput) (*mcp.CallToolResult, catalog.ImportStarted, error) {
	r, err := h.svc.StartImportWait(ctx, in)
	if err != nil {
		return nil, catalog.ImportStarted{}, err
	}
	return textResult(fmt.Sprintf("Travail %s : %s. %s", r.JobID, r.State, r.Note)), *r, nil
}

func (h *catalogHandlers) importModel(ctx context.Context, _ *mcp.CallToolRequest, in catalog.ImportModelInput) (*mcp.CallToolResult, catalog.ImportModelOutput, error) {
	r, err := h.svc.ImportModel(ctx, in)
	if err != nil {
		return nil, catalog.ImportModelOutput{}, err
	}
	return textResult(fmt.Sprintf("Travail %s : %s. %s", r.JobID, r.State, r.Note)), *r, nil
}

func (h *catalogHandlers) analyzeGeometry(ctx context.Context, _ *mcp.CallToolRequest, in catalog.AnalyzeInput) (*mcp.CallToolResult, catalog.AnalyzeOutput, error) {
	r, err := h.svc.AnalyzeGeometry(ctx, in)
	if err != nil {
		return nil, catalog.AnalyzeOutput{}, err
	}
	txt := fmt.Sprintf("Analyse de %s : %s.", r.FileName, r.State)
	if r.Summary.SizeMM != nil {
		txt += fmt.Sprintf(" %.1f × %.1f × %.1f mm, étanche : %v.", r.Summary.SizeMM.X, r.Summary.SizeMM.Y, r.Summary.SizeMM.Z, r.Summary.Watertight)
	}
	if r.Note != "" {
		txt += " " + r.Note
	}
	return textResult(txt), *r, nil
}

func (h *catalogHandlers) repairMesh(ctx context.Context, _ *mcp.CallToolRequest, in catalog.RepairInput) (*mcp.CallToolResult, catalog.RepairOutput, error) {
	r, err := h.svc.RepairMesh(ctx, in)
	if err != nil {
		return nil, catalog.RepairOutput{}, err
	}
	txt := fmt.Sprintf("Réparation : défauts %d -> %d, étanche : %v -> %v. %s", r.Report.Before.Defects, r.Report.After.Defects, r.Report.Before.Watertight, r.Report.After.Watertight, r.Note)
	return textResult(txt), *r, nil
}

func (h *catalogHandlers) getPreviewAsset(ctx context.Context, _ *mcp.CallToolRequest, in catalog.PreviewAssetInput) (*mcp.CallToolResult, catalog.PreviewAsset, error) {
	r, err := h.svc.GetPreviewAsset(ctx, in)
	if err != nil {
		return nil, catalog.PreviewAsset{}, err
	}
	return textResult(fmt.Sprintf("Aperçu %s. %s", r.Preview.State, r.Note)), *r, nil
}

func (h *catalogHandlers) tagModels(ctx context.Context, _ *mcp.CallToolRequest, in catalog.TagInput) (*mcp.CallToolResult, catalog.TagResult, error) {
	r, err := h.svc.TagModels(ctx, in)
	if err != nil {
		return nil, catalog.TagResult{}, err
	}
	return textResult(fmt.Sprintf("%d modèle(s) modifié(s), %d inchangé(s).", len(r.Updated), r.Unchanged)), *r, nil
}

func (h *catalogHandlers) linkVersions(ctx context.Context, _ *mcp.CallToolRequest, in catalog.LinkInput) (*mcp.CallToolResult, catalog.LinkResult, error) {
	r, err := h.svc.LinkVersions(ctx, in)
	if err != nil {
		return nil, catalog.LinkResult{}, err
	}
	return textResult(fmt.Sprintf("%d version(s) créée(s) sur %s ; %d modèle(s) intégré(s) mis en corbeille.", len(r.Created), r.ModelID, len(r.Trashed))), *r, nil
}

func (h *catalogHandlers) findDuplicates(ctx context.Context, _ *mcp.CallToolRequest, in catalog.DuplicatesInput) (*mcp.CallToolResult, catalog.DuplicatesResult, error) {
	r, err := h.svc.FindExactDuplicates(ctx, in)
	if err != nil {
		return nil, catalog.DuplicatesResult{}, err
	}
	return textResult(fmt.Sprintf("%d groupe(s) de doublons exacts.", r.TotalGroups)), *r, nil
}

func (h *catalogHandlers) previewOps(ctx context.Context, _ *mcp.CallToolRequest, in OpsInput) (*mcp.CallToolResult, catalog.OpPreview, error) {
	r, err := h.svc.PreviewOperations(ctx, in.Operations)
	if err != nil {
		return nil, catalog.OpPreview{}, err
	}
	return textResult(fmt.Sprintf("Aperçu : %d changement(s) sur %d modèle(s), %d fichier(s), %d vers la corbeille. Rien n'est appliqué ; jeton valable jusqu'à %s.",
		len(r.Changes), r.Models, r.Files, r.ToTrash, r.ExpiresAt.Format("15:04:05"))), *r, nil
}

func (h *catalogHandlers) applyOps(ctx context.Context, _ *mcp.CallToolRequest, in ApplyInput) (*mcp.CallToolResult, catalog.ApplyResult, error) {
	r, err := h.svc.ApplyOperations(ctx, in.Token)
	if err != nil {
		return nil, catalog.ApplyResult{}, err
	}
	return textResult(fmt.Sprintf("%d changement(s) appliqué(s), %d modèle(s) en corbeille.", r.Applied, len(r.Trashed))), *r, nil
}

func (h *catalogHandlers) restore(ctx context.Context, _ *mcp.CallToolRequest, in RestoreInput) (*mcp.CallToolResult, catalog.RestoreResult, error) {
	r, err := h.svc.RestoreFromTrash(ctx, in.ModelIDs)
	if err != nil {
		return nil, catalog.RestoreResult{}, err
	}
	return textResult(fmt.Sprintf("%d modèle(s) restauré(s), %d ignoré(s).", len(r.Restored), len(r.Skipped))), *r, nil
}
