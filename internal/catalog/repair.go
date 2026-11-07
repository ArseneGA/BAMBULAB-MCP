package catalog

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// RepairInput : réparation minimale du maillage d'un modèle.
type RepairInput struct {
	ModelID        string `json:"model_id,omitempty" jsonschema:"modèle à réparer (son fichier le plus récent)"`
	FileID         string `json:"file_id,omitempty" jsonschema:"fichier source précis"`
	MaxHoleEdges   int    `json:"max_hole_edges,omitempty" jsonschema:"comble les trous d'au plus ce nombre d'arêtes (défaut 32 ; -1 = ne pas combler)"`
	RemoveFloating bool   `json:"remove_floating,omitempty" jsonschema:"supprime les petits composants flottants (volume < 0,1 % de la plus grosse pièce) ; jamais par défaut"`
	DryRun         bool   `json:"dry_run,omitempty" jsonschema:"ne renvoie que le rapport avant/après, sans créer de version"`
	Label          string `json:"label,omitempty" jsonschema:"nom de la nouvelle version (défaut : vN)"`
}

// RepairOutput : rapport avant / après et nouvelle version créée.
type RepairOutput struct {
	ModelID         string                `json:"model_id"`
	FromFileID      string                `json:"from_file_id"`
	Created         bool                  `json:"created" jsonschema:"vrai si une nouvelle version a été enregistrée"`
	VersionID       string                `json:"version_id,omitempty"`
	ParentVersionID string                `json:"parent_version_id,omitempty"`
	File            *contracts.SourceFile `json:"file,omitempty"`
	Report          *mesh.RepairReport    `json:"report"`
	Note            string                `json:"note"`
	Caveats         []string              `json:"caveats"`
}

var repairNameClean = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// RepairMesh répare le maillage (soudure, dégénérés, doublons, orientation, petits trous, éclats si demandé) et
// enregistre le résultat comme NOUVELLE version immuable (STL en mm) ; l'original et ses versions ne sont jamais modifiés.
func (s *Service) RepairMesh(ctx context.Context, in RepairInput) (*RepairOutput, error) {
	m, f, err := s.resolveFile(ctx, in.ModelID, in.FileID)
	if err != nil {
		return nil, err
	}
	if f.Availability != contracts.AvailableLinux {
		return nil, invalidf("fichier disponible uniquement sur Windows (non copié) : importer avec copie pour le réparer")
	}
	if f.Format == contracts.FormatSTEP {
		return nil, invalidf("un fichier STEP est un solide exact, pas un maillage : il n'y a rien à réparer ici (importer son STL dérivé si nécessaire)")
	}
	md, _, err := s.loadMesh(deriveParams{SHA256: f.SHA256, Name: f.Name, Unit: f.Unit})
	if err != nil {
		return nil, invalidf("maillage illisible : %v", err)
	}
	out := &RepairOutput{ModelID: m.ID, FromFileID: f.ID, Caveats: []string{}}
	if len(md.Objects) > 1 {
		out.Caveats = append(out.Caveats, fmt.Sprintf("%d objets fusionnés en un seul maillage", len(md.Objects)))
	}
	if f.Format == contracts.Format3MF {
		out.Caveats = append(out.Caveats, "le fichier de départ est un 3MF : la nouvelle version est un STL (les métadonnées de projet ne sont pas reprises) ; l'original reste intact")
	}
	fixed, rep, err := mesh.Repair(md.Combined(), mesh.RepairOptions{MaxHoleEdges: in.MaxHoleEdges, RemoveFloating: in.RemoveFloating})
	if err != nil {
		return nil, invalidf("réparation impossible : %v", err)
	}
	out.Report = rep
	out.Caveats = append(out.Caveats, rep.Warnings...)
	out.Caveats = append(out.Caveats, "Réparation minimale et géométrique : elle ne corrige ni les arêtes non manifold, ni les auto-intersections, ni les grands trous ; vérifier le résultat avant d'imprimer.")
	switch {
	case !rep.Changed:
		out.Note = "Aucun défaut réparable : aucune nouvelle version créée."
		return out, nil
	case in.DryRun:
		out.Note = "Simulation (dry_run) : aucune version créée."
		return out, nil
	}
	stl, err := mesh.STLBytes(fixed)
	if err != nil {
		return nil, err
	}
	sha, size, err := s.d.Blobs.Put(bytes.NewReader(stl), "")
	if err != nil {
		return nil, err
	}
	vs, err := s.d.Store.ListVersions(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	parent := ""
	if len(vs) > 0 {
		parent = vs[0].ID
	}
	label := strings.TrimSpace(in.Label)
	if label == "" {
		label = fmt.Sprintf("v%d", len(vs)+1)
	}
	stem := strings.Trim(repairNameClean.ReplaceAllString(strings.TrimSuffix(f.Name, filepath.Ext(f.Name)), "_"), "_")
	if stem == "" {
		stem = "model"
	}
	name := fmt.Sprintf("%s_repaired_v%d.stl", stem, len(vs)+1)
	note := fmt.Sprintf("repair_mesh : %d défaut(s) avant, %d après", rep.Before.Defects, rep.After.Defects)
	_, ver, files, err := s.RegisterGenerated(ctx, "mcp", contracts.Model{ID: m.ID}, parent, label, note,
		[]GeneratedFile{{Name: name, Format: contracts.FormatSTL, SHA256: sha, Size: size, Unit: "mm"}})
	if err != nil {
		return nil, err
	}
	out.Created, out.VersionID, out.ParentVersionID = true, ver.ID, parent
	if len(files) > 0 {
		out.File = &files[0]
	}
	out.Note = "Nouvelle version immuable " + ver.ID
	if parent != "" {
		out.Note += " ; la version " + parent + " et son fichier d'origine sont intacts."
	}
	return out, nil
}
