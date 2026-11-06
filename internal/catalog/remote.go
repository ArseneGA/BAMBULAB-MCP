package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

// Fichiers inventoriés par l'agent Windows (jalon 8, dossier §15.4) : le service
// n'a pas encore le contenu, il n'a que l'empreinte annoncée par l'agent. Le
// fichier est enregistré « windows_only » (hors des capacités hors ligne) jusqu'à
// ce que la copie contrôlée soit terminée ; l'import normal d'un fichier de même
// empreinte le fait alors passer à « linux » sans créer de doublon.

const actorAgent = "agent"

// RemoteFile décrit un fichier connu seulement par son inventaire.
type RemoteFile struct {
	Agent      string    // nom de l'agent (provenance)
	Root       string    // étiquette du dossier autorisé (devient la collection racine)
	RelPath    string    // chemin relatif, séparateur « / », déjà validé
	OriginPath string    // chemin d'origine côté Windows (information)
	Size       int64     // octets
	MTime      time.Time // date de modification d'origine
	SHA256     string    // empreinte calculée par l'agent
}

// RegisterRemote enregistre (ou retrouve) un fichier windows_only. Aucun contenu
// n'est lu ni copié. Renvoie le statut (StatusIndexed, StatusUnchanged,
// StatusDuplicate, StatusNewVersion, StatusRejected) et l'identifiant du fichier source.
func (s *Service) RegisterRemote(ctx context.Context, rf RemoteFile) (FileResult, error) {
	fr := FileResult{Path: rf.OriginPath, RelPath: rf.RelPath, SizeBytes: rf.Size, SHA256: rf.SHA256}
	name := path.Base(rf.RelPath)
	format, ok := formatOf(name)
	if !ok {
		fr.Status, fr.Note = StatusRejected, "format non supporté"
		return fr, nil
	}
	fr.Format = string(format)
	if rf.Size > s.cfg.MaxFileBytes {
		fr.Status, fr.Note = StatusRejected, fmt.Sprintf("fichier trop gros (%d octets > limite %d)", rf.Size, s.cfg.MaxFileBytes)
		return fr, nil
	}
	if len(rf.SHA256) != 64 {
		return fr, invalidf("empreinte invalide")
	}
	err := s.d.Store.Tx(ctx, func(tx *sql.Tx) error {
		au := s.auditor(ctx, tx, actorAgent, "source_file")
		now := s.d.Store.Now()
		mt := rf.MTime.UTC()
		origin := rf.OriginPath
		newFile := func(modelID string) contracts.SourceFile {
			return contracts.SourceFile{ID: contracts.NewID("src"), ModelID: modelID, Name: name, Format: format,
				SizeBytes: rf.Size, SHA256: rf.SHA256, Availability: contracts.AvailableWindowsOnly, OriginPath: &origin,
				CreatedAt: now, RelPath: rf.RelPath, OriginMTime: &mt}
		}
		// Même contenu déjà au catalogue (modèles en corbeille ignorés).
		dups, err := queryFiles(ctx, tx, `WHERE sha256=? AND model_id IN (SELECT id FROM models WHERE deleted_at IS NULL)`, rf.SHA256)
		if err != nil {
			return err
		}
		if len(dups) > 0 {
			d := dups[0]
			fr.ModelID, fr.SourceFileID, fr.Availability = d.ModelID, d.ID, string(d.Availability)
			fr.Status = StatusUnchanged
			if d.OriginPath == nil || *d.OriginPath != rf.OriginPath {
				fr.Status, fr.DuplicateOf = StatusDuplicate, d.ModelID
				fr.Note = "contenu déjà au catalogue : rien n'a été ajouté"
			}
			return nil
		}
		// Même chemin d'origine, contenu différent : nouvelle version du même modèle.
		prev, err := queryFiles(ctx, tx, `WHERE origin_path=? AND model_id IN (SELECT id FROM models WHERE deleted_at IS NULL)`, rf.OriginPath)
		if err != nil {
			return err
		}
		if len(prev) > 0 {
			old := prev[len(prev)-1]
			nf := newFile(old.ModelID)
			if err := insertFile(ctx, tx, nf); err != nil {
				return err
			}
			vs, err := queryVersions(ctx, tx, old.ModelID)
			if err != nil {
				return err
			}
			var parent *string
			if len(vs) > 0 {
				parent = &vs[0].ID
			}
			v := contracts.ModelVersion{ID: contracts.NewID("ver"), ModelID: old.ModelID, ParentID: parent,
				Label: fmt.Sprintf("v%d", len(vs)+1), Note: "fichier modifié côté Windows (inventaire)", SourceFileIDs: []string{nf.ID}, CreatedAt: now}
			if err := insertVersion(ctx, tx, v); err != nil {
				return err
			}
			if err := touchModel(ctx, tx, old.ModelID, now); err != nil {
				return err
			}
			if err := au("source_file.add", nf.ID, map[string]any{"model_id": old.ModelID, "sha256": rf.SHA256, "availability": "windows_only"}); err != nil {
				return err
			}
			fr.Status, fr.ModelID, fr.SourceFileID, fr.Availability = StatusNewVersion, old.ModelID, nf.ID, string(nf.Availability)
			return nil
		}
		// Nouveau modèle, collections reproduisant la structure relative.
		dirs := strings.Split(path.Dir(rf.RelPath), "/")
		var colID *string
		top := strings.TrimSpace(rf.Root)
		if top != "" {
			cid, err := s.ensureCollection(ctx, tx, actorAgent, s.auditor(ctx, tx, actorAgent, "collection"), nil, top)
			if err != nil {
				return err
			}
			for _, sd := range dirs {
				if sd == "." || sd == "" {
					continue
				}
				if cid, err = s.ensureCollection(ctx, tx, actorAgent, s.auditor(ctx, tx, actorAgent, "collection"), &cid, sd); err != nil {
					return err
				}
			}
			colID = &cid
		}
		m := contracts.Model{ID: contracts.NewID("mdl"), CollectionID: colID, Name: strings.TrimSuffix(name, path.Ext(name)),
			CreatedAt: now, UpdatedAt: now,
			Provenance: &contracts.Provenance{Source: "agent Windows " + rf.Agent, OriginPath: rf.OriginPath, ImportedAt: now}}
		if err := insertModel(ctx, tx, m); err != nil {
			return err
		}
		nf := newFile(m.ID)
		if err := insertFile(ctx, tx, nf); err != nil {
			return err
		}
		v := contracts.ModelVersion{ID: contracts.NewID("ver"), ModelID: m.ID, Label: "v1", Note: "inventaire Windows", SourceFileIDs: []string{nf.ID}, CreatedAt: now}
		if err := insertVersion(ctx, tx, v); err != nil {
			return err
		}
		if err := s.d.Store.AuditTx(ctx, tx, actorAgent, "model.create", "model", m.ID, map[string]any{"name": m.Name, "origin_path": rf.OriginPath}); err != nil {
			return err
		}
		if err := au("source_file.add", nf.ID, map[string]any{"model_id": m.ID, "sha256": rf.SHA256, "availability": "windows_only"}); err != nil {
			return err
		}
		fr.Status, fr.ModelID, fr.SourceFileID, fr.Availability = StatusIndexed, m.ID, nf.ID, string(nf.Availability)
		fr.Note = "indexé sans copie : disponible uniquement sur Windows (windows_only), hors des capacités hors ligne"
		return nil
	})
	if err != nil {
		return fr, err
	}
	return fr, nil
}
