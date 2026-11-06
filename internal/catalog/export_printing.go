package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

// RunWorker exécute une commande du worker géométrie Python (parametric,
// step_import...) et renvoie le champ result. Utilisé par internal/printing
// (create_parametric_model). L'appelant confine out_dir et les chemins.
func (s *Service) RunWorker(ctx context.Context, command string, req map[string]any) (json.RawMessage, error) {
	return s.worker()(ctx, command, req)
}

// EnsureDerived met en file l'analyse et l'aperçu (ou l'import STEP) d'un
// fichier source copié sur le stockage. Idempotent.
func (s *Service) EnsureDerived(ctx context.Context, f contracts.SourceFile) { s.ensureDerived(ctx, f) }

// GeneratedFile est un contenu déjà conservé dans le magasin de blobs.
type GeneratedFile struct {
	Name   string
	Format contracts.FileFormat
	SHA256 string
	Size   int64
	Unit   string
}

// RegisterGenerated enregistre, en une transaction, un modèle (nouveau si m.ID est vide,
// sinon une NOUVELLE VERSION du modèle existant, jamais d'écrasement) avec ses fichiers
// sources et sa version immuable. parentVersion peut être vide.
func (s *Service) RegisterGenerated(ctx context.Context, actor string, m contracts.Model, parentVersion, label, note string, files []GeneratedFile) (contracts.Model, contracts.ModelVersion, []contracts.SourceFile, error) {
	var ver contracts.ModelVersion
	var out []contracts.SourceFile
	if len(files) == 0 {
		return m, ver, nil, errors.New("au moins un fichier requis")
	}
	now := s.d.Store.Now()
	isNew := m.ID == ""
	if isNew {
		m.ID = contracts.NewID("mdl")
		m.CreatedAt, m.UpdatedAt = now, now
	}
	err := s.d.Store.Tx(ctx, func(tx *sql.Tx) error {
		if isNew {
			if err := insertModel(ctx, tx, m); err != nil {
				return err
			}
			if err := s.d.Store.AuditTx(ctx, tx, actor, "model.create", "model", m.ID, map[string]any{"name": m.Name, "generated": true}); err != nil {
				return err
			}
		} else {
			cur, err := getModel(ctx, tx, m.ID)
			if err != nil {
				return err
			}
			m = cur
			if err := touchModel(ctx, tx, m.ID, now); err != nil {
				return err
			}
		}
		var ids []string
		for _, f := range files {
			if !s.d.Blobs.Has(f.SHA256) {
				return fmt.Errorf("blob %s absent du magasin", f.SHA256)
			}
			sf := contracts.SourceFile{ID: contracts.NewID("src"), ModelID: m.ID, Name: f.Name, Format: f.Format, SizeBytes: f.Size, SHA256: f.SHA256,
				Availability: contracts.AvailableLinux, CreatedAt: now, Unit: f.Unit}
			if err := insertFile(ctx, tx, sf); err != nil {
				return err
			}
			if err := s.d.Store.AuditTx(ctx, tx, actor, "source_file.add", "source_file", sf.ID, map[string]any{"model_id": m.ID, "sha256": f.SHA256}); err != nil {
				return err
			}
			ids = append(ids, sf.ID)
			out = append(out, sf)
		}
		ver = contracts.ModelVersion{ID: contracts.NewID("ver"), ModelID: m.ID, Label: label, Note: note, SourceFileIDs: ids, CreatedAt: now}
		if parentVersion != "" {
			p := parentVersion
			ver.ParentID = &p
		}
		if err := insertVersion(ctx, tx, ver); err != nil {
			return err
		}
		return s.d.Store.AuditTx(ctx, tx, actor, "version.create", "model_version", ver.ID, map[string]any{"model_id": m.ID, "label": label})
	})
	if err != nil {
		return m, ver, nil, err
	}
	for _, f := range out {
		s.ensureDerived(ctx, f)
	}
	return m, ver, out, nil
}
