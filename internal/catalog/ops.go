package catalog

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

const actorOps = "ops"

// Operation est une opération de fichiers/modèles demandée. Rien n'est appliqué
// avant apply_file_operations avec le jeton d'un aperçu.
type Operation struct {
	Op         string   `json:"op" jsonschema:"rename (modèle) | rename_file (nom d'un fichier source, même extension) | move (vers une collection) | add_tags | remove_tags | set_license | delete (met en corbeille, récupérable)"`
	ModelIDs   []string `json:"model_ids,omitempty" jsonschema:"modèles ciblés (rename : exactement un)"`
	FileID     string   `json:"file_id,omitempty" jsonschema:"rename_file : fichier source ciblé"`
	NewName    string   `json:"new_name,omitempty" jsonschema:"rename / rename_file : nouveau nom"`
	Collection string   `json:"collection,omitempty" jsonschema:"move : identifiant, nom ou chemin « A/B » de la collection cible ; créée si elle n'existe pas"`
	ToRoot     bool     `json:"to_root,omitempty" jsonschema:"move : sortir de toute collection"`
	Tags       []string `json:"tags,omitempty" jsonschema:"add_tags / remove_tags"`
	License    string   `json:"license,omitempty" jsonschema:"set_license"`
}

// Change est une différence avant/après.
type Change struct {
	Op         string `json:"op"`
	TargetType string `json:"target_type" jsonschema:"model | file"`
	TargetID   string `json:"target_id"`
	TargetName string `json:"target_name"`
	Field      string `json:"field"`
	Before     string `json:"before"`
	After      string `json:"after"`
}

// OpPreview : portée et différence d'un lot d'opérations. Rien n'est appliqué.
type OpPreview struct {
	Token              string    `json:"token" jsonschema:"jeton à usage unique à fournir à apply_file_operations"`
	ExpiresAt          time.Time `json:"expires_at"`
	Operations         int       `json:"operations"`
	Models             int       `json:"models_affected"`
	Files              int       `json:"files_affected"`
	Changes            []Change  `json:"changes"`
	CreatesCollections []string  `json:"creates_collections,omitempty"`
	ToTrash            int       `json:"to_trash" jsonschema:"modèles qui iront en corbeille (récupérables)"`
	Warnings           []string  `json:"warnings,omitempty"`
	Note               string    `json:"note"`
}

type action struct {
	ch   Change
	exec func(ctx context.Context, tx *sql.Tx) error
}

type plannedPlan struct {
	Ops          []Operation       `json:"ops"`
	Fingerprints map[string]string `json:"fingerprints"`
}

const maxOpModels = 500

func fp(m contracts.Model) string { return fmtT(m.UpdatedAt) + "|" + m.Name }

// compute valide les opérations contre l'état courant (q) et construit actions et empreintes.
func (s *Service) compute(ctx context.Context, q dbx, ops []Operation) (acts []action, prints map[string]string, creates []string, warns []string, err error) {
	if len(ops) == 0 {
		return nil, nil, nil, nil, invalidf("aucune opération")
	}
	if len(ops) > 100 {
		return nil, nil, nil, nil, invalidf("trop d'opérations (max 100)")
	}
	cols, err := allCollections(ctx, q)
	if err != nil {
		return
	}
	prints = map[string]string{}
	seen := map[string]bool{} // op|cible déjà utilisés
	models := 0
	getLive := func(id string) (contracts.Model, error) {
		m, err := getModel(ctx, q, id)
		if err != nil {
			return m, err
		}
		if m.DeletedAt != nil {
			return m, invalidf("le modèle %s est en corbeille", id)
		}
		prints[id] = fp(m)
		return m, nil
	}
	createdPaths := map[string]bool{}
	for i, op := range ops {
		bad := func(format string, a ...any) error {
			return invalidf("opération %d (%s) : %s", i+1, op.Op, fmt.Sprintf(format, a...))
		}
		dedupe := func(id string) error {
			k := op.Op + "|" + id
			if op.Op == "add_tags" || op.Op == "remove_tags" {
				k = "tags|" + id
			}
			if seen[k] {
				return bad("le modèle %s est visé deux fois par la même opération", id)
			}
			seen[k] = true
			return nil
		}
		ids := uniqueStrings(op.ModelIDs)
		models += len(ids)
		if models > maxOpModels {
			return nil, nil, nil, nil, invalidf("trop de modèles ciblés (max %d)", maxOpModels)
		}
		switch op.Op {
		case "rename":
			if len(ids) != 1 {
				return nil, nil, nil, nil, bad("exactement un model_id requis")
			}
			name := strings.TrimSpace(op.NewName)
			if name == "" || len(name) > 200 {
				return nil, nil, nil, nil, bad("new_name requis (1 à 200 caractères)")
			}
			m, err := getLive(ids[0])
			if err != nil {
				return nil, nil, nil, nil, err
			}
			if err := dedupe(m.ID); err != nil {
				return nil, nil, nil, nil, err
			}
			id, before := m.ID, m.Name
			acts = append(acts, action{ch: Change{"rename", "model", id, before, "name", before, name},
				exec: func(ctx context.Context, tx *sql.Tx) error {
					return execTouch(ctx, tx, s, id, `UPDATE models SET name=?, updated_at=? WHERE id=?`, name)
				}})
		case "rename_file":
			f, err := getFile(ctx, q, op.FileID)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			m, err := getLive(f.ModelID)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			name := strings.TrimSpace(op.NewName)
			if name == "" || strings.ContainsAny(name, `/\`) || len(name) > 255 {
				return nil, nil, nil, nil, bad("new_name invalide (sans séparateur de dossier)")
			}
			if nf, ok := formatOf(name); !ok || nf != f.Format {
				return nil, nil, nil, nil, bad("l'extension doit rester celle du format %s", f.Format)
			}
			if err := dedupe(f.ID); err != nil {
				return nil, nil, nil, nil, err
			}
			fid, mid := f.ID, m.ID
			acts = append(acts, action{ch: Change{"rename_file", "file", fid, f.Name, "name", f.Name, name},
				exec: func(ctx context.Context, tx *sql.Tx) error {
					if _, err := tx.ExecContext(ctx, `UPDATE source_files SET name=? WHERE id=?`, name, fid); err != nil {
						return err
					}
					return touchModel(ctx, tx, mid, s.d.Store.Now())
				}})
			warns = append(warns, "renommer un fichier source ne change que son nom au catalogue : le blob (adressé par contenu) et l'original sur disque ne sont pas touchés")
		case "move":
			if len(ids) == 0 {
				return nil, nil, nil, nil, bad("model_ids requis")
			}
			var targetID *string
			targetPath := ""
			var createChain []string
			switch {
			case op.ToRoot:
			case strings.TrimSpace(op.Collection) == "":
				return nil, nil, nil, nil, bad("collection ou to_root requis")
			default:
				spec := strings.TrimSpace(op.Collection)
				found, rerr := resolveCollections(cols, spec, true)
				switch {
				case rerr == nil && len(found) == 1:
					targetID, targetPath = &found[0], collectionPath(cols, &found[0])
				case rerr == nil:
					return nil, nil, nil, nil, bad("« %s » désigne %d collections : préciser l'identifiant ou le chemin complet", spec, len(found))
				case !errors.Is(rerr, ErrNotFound):
					return nil, nil, nil, nil, rerr
				default:
					if strings.HasPrefix(spec, "col_") {
						return nil, nil, nil, nil, bad("collection %s introuvable", spec)
					}
					createChain = splitPath(spec)
					targetPath = strings.Join(createChain, "/")
					if !createdPaths[strings.ToLower(targetPath)] {
						createdPaths[strings.ToLower(targetPath)] = true
						creates = append(creates, targetPath)
					}
				}
			}
			for _, id := range ids {
				m, err := getLive(id)
				if err != nil {
					return nil, nil, nil, nil, err
				}
				if err := dedupe(id); err != nil {
					return nil, nil, nil, nil, err
				}
				mid, mname := m.ID, m.Name
				chain, tid := createChain, targetID
				acts = append(acts, action{ch: Change{"move", "model", mid, mname, "collection", collectionPathOrRoot(cols, m.CollectionID), orRoot(targetPath)},
					exec: func(ctx context.Context, tx *sql.Tx) error {
						dest := tid
						if len(chain) > 0 {
							var parent *string
							au := s.auditor(ctx, tx, actorOps, "collection")
							for _, seg := range chain {
								cid, err := s.ensureCollection(ctx, tx, actorOps, au, parent, seg)
								if err != nil {
									return err
								}
								parent = &cid
							}
							dest = parent
						}
						return execTouch(ctx, tx, s, mid, `UPDATE models SET collection_id=?, updated_at=? WHERE id=?`, nullS(dest))
					}})
			}
		case "add_tags", "remove_tags":
			tags := normTags(op.Tags)
			if len(ids) == 0 || len(tags) == 0 {
				return nil, nil, nil, nil, bad("model_ids et tags requis")
			}
			for _, id := range ids {
				m, err := getLive(id)
				if err != nil {
					return nil, nil, nil, nil, err
				}
				if err := dedupe(id); err != nil {
					return nil, nil, nil, nil, err
				}
				var next []string
				if op.Op == "add_tags" {
					next = normTags(append(append([]string(nil), m.Tags...), tags...))
				} else {
					drop := map[string]bool{}
					for _, t := range tags {
						drop[t] = true
					}
					for _, t := range m.Tags {
						if !drop[t] {
							next = append(next, t)
						}
					}
					next = normTags(next)
				}
				if len(next) > 50 {
					return nil, nil, nil, nil, bad("plus de 50 étiquettes sur %s", id)
				}
				if strings.Join(next, ",") == strings.Join(normTags(m.Tags), ",") {
					warns = append(warns, fmt.Sprintf("%s : étiquettes déjà à jour, aucune différence", m.Name))
					continue
				}
				mid, raw := m.ID, jsonTags(next)
				acts = append(acts, action{ch: Change{op.Op, "model", mid, m.Name, "tags", strings.Join(m.Tags, ", "), strings.Join(next, ", ")},
					exec: func(ctx context.Context, tx *sql.Tx) error {
						return execTouch(ctx, tx, s, mid, `UPDATE models SET tags=?, updated_at=? WHERE id=?`, raw)
					}})
			}
		case "set_license":
			if len(ids) == 0 {
				return nil, nil, nil, nil, bad("model_ids requis")
			}
			lic := strings.TrimSpace(op.License)
			if len(lic) > 200 {
				return nil, nil, nil, nil, bad("licence trop longue")
			}
			for _, id := range ids {
				m, err := getLive(id)
				if err != nil {
					return nil, nil, nil, nil, err
				}
				if err := dedupe(id); err != nil {
					return nil, nil, nil, nil, err
				}
				mid := m.ID
				acts = append(acts, action{ch: Change{"set_license", "model", mid, m.Name, "license", m.License, lic},
					exec: func(ctx context.Context, tx *sql.Tx) error {
						return execTouch(ctx, tx, s, mid, `UPDATE models SET license=?, updated_at=? WHERE id=?`, lic)
					}})
			}
		case "delete":
			if len(ids) == 0 {
				return nil, nil, nil, nil, bad("model_ids requis")
			}
			for _, id := range ids {
				m, err := getLive(id)
				if err != nil {
					return nil, nil, nil, nil, err
				}
				if err := dedupe(id); err != nil {
					return nil, nil, nil, nil, err
				}
				mid := m.ID
				acts = append(acts, action{ch: Change{"delete", "model", mid, m.Name, "état", "au catalogue", "corbeille (récupérable)"},
					exec: func(ctx context.Context, tx *sql.Tx) error {
						now := s.d.Store.Now()
						_, err := tx.ExecContext(ctx, `UPDATE models SET deleted_at=?, updated_at=? WHERE id=?`, fmtT(now), fmtT(now), mid)
						return err
					}})
			}
		default:
			return nil, nil, nil, nil, invalidf("opération %d : op inconnue %q (rename | rename_file | move | add_tags | remove_tags | set_license | delete)", i+1, op.Op)
		}
	}
	if len(acts) == 0 {
		warns = append(warns, "aucune différence à appliquer")
	}
	return acts, prints, creates, uniqueStrings(warns), nil
}

func execTouch(ctx context.Context, tx *sql.Tx, s *Service, id, query string, val any) error {
	_, err := tx.ExecContext(ctx, query, val, fmtT(s.d.Store.Now()), id)
	return err
}

func jsonTags(t []string) string {
	b, _ := json.Marshal(t)
	return string(b)
}

func splitPath(spec string) []string {
	var out []string
	for _, p := range strings.Split(spec, "/") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func orRoot(p string) string {
	if p == "" {
		return "(racine, sans collection)"
	}
	return p
}

func collectionPathOrRoot(cols map[string]contracts.Collection, id *string) string {
	return orRoot(collectionPath(cols, id))
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func hashToken(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// PreviewOperations calcule portée et différence, enregistre l'aperçu et renvoie
// un jeton à usage unique. Rien n'est modifié au catalogue.
func (s *Service) PreviewOperations(ctx context.Context, ops []Operation) (*OpPreview, error) {
	acts, prints, creates, warns, err := s.compute(ctx, s.db(), ops)
	if err != nil {
		return nil, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	tok := hex.EncodeToString(raw)
	now := s.d.Store.Now()
	exp := now.Add(s.cfg.PreviewTTL)
	plan, _ := json.Marshal(plannedPlan{Ops: ops, Fingerprints: prints})
	if _, err := s.db().ExecContext(ctx, `INSERT INTO op_previews(token_hash,plan,created_at,expires_at) VALUES(?,?,?,?)`,
		hashToken(tok), string(plan), fmtT(now), fmtT(exp)); err != nil {
		return nil, err
	}
	pv := &OpPreview{Token: tok, ExpiresAt: exp, Operations: len(ops), Changes: []Change{}, CreatesCollections: creates, Warnings: warns,
		Note: "Aperçu seulement : rien n'a été modifié. Appliquer avec apply_file_operations(token) ; les suppressions vont en corbeille et se restaurent avec restore_from_trash."}
	mset, fset := map[string]bool{}, map[string]bool{}
	for _, a := range acts {
		pv.Changes = append(pv.Changes, a.ch)
		if a.ch.TargetType == "file" {
			fset[a.ch.TargetID] = true
		} else {
			mset[a.ch.TargetID] = true
		}
		if a.ch.Op == "delete" {
			pv.ToTrash++
		}
	}
	pv.Models, pv.Files = len(mset), len(fset)
	return pv, nil
}

// ApplyResult : issue de apply_file_operations.
type ApplyResult struct {
	Applied     int      `json:"applied"`
	Changes     []Change `json:"changes"`
	Trashed     []string `json:"trashed,omitempty" jsonschema:"modèles mis en corbeille (restore_from_trash)"`
	Collections []string `json:"collections_created,omitempty"`
	Note        string   `json:"note"`
}

// ApplyOperations applique un aperçu : jeton valide, non expiré, jamais utilisé,
// état inchangé depuis l'aperçu. Tout ou rien, audit de chaque changement.
func (s *Service) ApplyOperations(ctx context.Context, token string) (*ApplyResult, error) {
	if len(token) < 16 {
		return nil, invalidf("jeton d'aperçu requis : appeler d'abord preview_file_operations")
	}
	h := hashToken(token)
	res := &ApplyResult{Changes: []Change{}}
	err := s.d.Store.Tx(ctx, func(tx *sql.Tx) error {
		res.Changes, res.Trashed, res.Collections = []Change{}, nil, nil
		var planS, expS string
		var used sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT plan,expires_at,used_at FROM op_previews WHERE token_hash=?`, h).Scan(&planS, &expS, &used)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w : jeton d'aperçu inconnu", ErrConflict)
		}
		if err != nil {
			return err
		}
		if used.Valid {
			return fmt.Errorf("%w : ce jeton a déjà été utilisé", ErrConflict)
		}
		if s.d.Store.Now().After(parseT(expS)) {
			return fmt.Errorf("%w : aperçu expiré, refaire preview_file_operations", ErrConflict)
		}
		var plan plannedPlan
		if err := json.Unmarshal([]byte(planS), &plan); err != nil {
			return err
		}
		acts, prints, creates, _, err := s.compute(ctx, tx, plan.Ops)
		if err != nil {
			return fmt.Errorf("%w : l'état a changé depuis l'aperçu (%v)", ErrConflict, err)
		}
		for id, want := range plan.Fingerprints {
			if prints[id] != want {
				return fmt.Errorf("%w : le modèle %s a été modifié depuis l'aperçu, refaire preview_file_operations", ErrConflict, id)
			}
		}
		if r, err := tx.ExecContext(ctx, `UPDATE op_previews SET used_at=? WHERE token_hash=? AND used_at IS NULL`, fmtT(s.d.Store.Now()), h); err != nil {
			return err
		} else if n, _ := r.RowsAffected(); n != 1 {
			return fmt.Errorf("%w : ce jeton a déjà été utilisé", ErrConflict)
		}
		for _, a := range acts {
			if err := a.exec(ctx, tx); err != nil {
				return err
			}
			op := "ops." + a.ch.Op
			if err := s.d.Store.AuditTx(ctx, tx, actorOps, op, a.ch.TargetType, a.ch.TargetID,
				map[string]any{"field": a.ch.Field, "before": a.ch.Before, "after": a.ch.After, "preview": h[:12]}); err != nil {
				return err
			}
			res.Changes = append(res.Changes, a.ch)
			if a.ch.Op == "delete" {
				res.Trashed = append(res.Trashed, a.ch.TargetID)
			}
		}
		res.Collections = creates
		res.Applied = len(acts)
		return s.d.Store.AuditTx(ctx, tx, actorOps, "ops.apply", "operation", h[:12], map[string]any{"changes": len(acts), "operations": len(plan.Ops)})
	})
	if err != nil {
		return nil, err
	}
	res.Note = "Appliqué et journalisé. Les suppressions sont en corbeille : restore_from_trash les récupère."
	return res, nil
}

// ---- étiquettes ----

// TagInput : ajout/retrait direct d'étiquettes (journalisé).
type TagInput struct {
	ModelIDs []string `json:"model_ids" jsonschema:"modèles ciblés"`
	Add      []string `json:"add,omitempty"`
	Remove   []string `json:"remove,omitempty"`
}

// TagResult : étiquettes après modification.
type TagResult struct {
	Updated   []TaggedModel `json:"updated"`
	Unchanged int           `json:"unchanged"`
}

// TaggedModel : modèle et ses étiquettes.
type TaggedModel struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

// TagModels ajoute/retire des étiquettes (mutation légère, auditée, réversible).
func (s *Service) TagModels(ctx context.Context, in TagInput) (*TagResult, error) {
	ids := uniqueStrings(in.ModelIDs)
	add, rem := normTags(in.Add), normTags(in.Remove)
	if len(ids) == 0 || (len(add) == 0 && len(rem) == 0) {
		return nil, invalidf("model_ids et au moins une étiquette à ajouter ou retirer requis")
	}
	if len(ids) > maxOpModels {
		return nil, invalidf("trop de modèles (max %d)", maxOpModels)
	}
	out := &TagResult{Updated: []TaggedModel{}}
	err := s.d.Store.Tx(ctx, func(tx *sql.Tx) error {
		out.Updated, out.Unchanged = []TaggedModel{}, 0
		for _, id := range ids {
			m, err := getModel(ctx, tx, id)
			if err != nil {
				return err
			}
			if m.DeletedAt != nil {
				return invalidf("le modèle %s est en corbeille", id)
			}
			drop := map[string]bool{}
			for _, t := range rem {
				drop[t] = true
			}
			var next []string
			for _, t := range m.Tags {
				if !drop[t] {
					next = append(next, t)
				}
			}
			next = normTags(append(next, add...))
			if len(next) > 50 {
				return invalidf("plus de 50 étiquettes sur %s", id)
			}
			if strings.Join(next, ",") == strings.Join(normTags(m.Tags), ",") {
				out.Unchanged++
				continue
			}
			if err := execTouch(ctx, tx, s, id, `UPDATE models SET tags=?, updated_at=? WHERE id=?`, jsonTags(next)); err != nil {
				return err
			}
			if err := s.d.Store.AuditTx(ctx, tx, "mcp", "model.tag", "model", id, map[string]any{"before": m.Tags, "after": next}); err != nil {
				return err
			}
			out.Updated = append(out.Updated, TaggedModel{ID: id, Name: m.Name, Tags: next})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---- corbeille ----

// RestoreResult : issue d'une restauration.
type RestoreResult struct {
	Restored []TaggedModel `json:"restored"`
	Skipped  []string      `json:"skipped,omitempty" jsonschema:"identifiants qui n'étaient pas en corbeille"`
}

// RestoreFromTrash sort des modèles de la corbeille.
func (s *Service) RestoreFromTrash(ctx context.Context, ids []string) (*RestoreResult, error) {
	ids = uniqueStrings(ids)
	if len(ids) == 0 {
		return nil, invalidf("model_ids requis")
	}
	out := &RestoreResult{Restored: []TaggedModel{}}
	err := s.d.Store.Tx(ctx, func(tx *sql.Tx) error {
		out.Restored, out.Skipped = []TaggedModel{}, nil
		for _, id := range ids {
			m, err := getModel(ctx, tx, id)
			if err != nil {
				return err
			}
			if m.DeletedAt == nil {
				out.Skipped = append(out.Skipped, id)
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE models SET deleted_at=NULL, updated_at=? WHERE id=?`, fmtT(s.d.Store.Now()), id); err != nil {
				return err
			}
			if err := s.d.Store.AuditTx(ctx, tx, "mcp", "model.restore", "model", id, nil); err != nil {
				return err
			}
			out.Restored = append(out.Restored, TaggedModel{ID: id, Name: m.Name, Tags: m.Tags})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---- liaison de versions ----

// LinkInput : déclare des modèles importés séparément comme versions successives d'un même modèle.
type LinkInput struct {
	TargetModelID   string   `json:"target_model_id" jsonschema:"modèle logique à conserver ; il reçoit les versions"`
	VersionModelIDs []string `json:"version_model_ids" jsonschema:"modèles à intégrer comme versions suivantes, du plus ancien au plus récent ; ils vont ensuite en corbeille (récupérables)"`
	Note            string   `json:"note,omitempty" jsonschema:"note ajoutée à chaque version créée"`
}

// LinkResult : versions créées.
type LinkResult struct {
	ModelID string                   `json:"model_id"`
	Created []contracts.ModelVersion `json:"created"`
	Trashed []string                 `json:"trashed"`
	Note    string                   `json:"note"`
}

// LinkVersions chaîne des modèles comme versions immuables d'un modèle cible.
// Les fichiers sources sont rattachés au modèle cible (le blob ne bouge pas) ;
// l'historique des modèles absorbés reste dans leurs propres versions, en corbeille.
func (s *Service) LinkVersions(ctx context.Context, in LinkInput) (*LinkResult, error) {
	others := uniqueStrings(in.VersionModelIDs)
	if in.TargetModelID == "" || len(others) == 0 {
		return nil, invalidf("target_model_id et version_model_ids requis")
	}
	for _, o := range others {
		if o == in.TargetModelID {
			return nil, invalidf("le modèle cible ne peut pas être sa propre version")
		}
	}
	out := &LinkResult{ModelID: in.TargetModelID, Created: []contracts.ModelVersion{}, Trashed: []string{}}
	err := s.d.Store.Tx(ctx, func(tx *sql.Tx) error {
		out.Created, out.Trashed = []contracts.ModelVersion{}, []string{}
		tgt, err := getModel(ctx, tx, in.TargetModelID)
		if err != nil {
			return err
		}
		if tgt.DeletedAt != nil {
			return invalidf("le modèle cible est en corbeille")
		}
		vs, err := queryVersions(ctx, tx, tgt.ID)
		if err != nil {
			return err
		}
		var parent *string
		if len(vs) > 0 {
			parent = &vs[0].ID
		}
		n := len(vs)
		for _, oid := range others {
			om, err := getModel(ctx, tx, oid)
			if err != nil {
				return err
			}
			if om.DeletedAt != nil {
				return invalidf("le modèle %s est en corbeille", oid)
			}
			lf, ok, err := currentFile(ctx, tx, oid)
			if err != nil {
				return err
			}
			if !ok {
				return invalidf("le modèle %s n'a aucun fichier", oid)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE source_files SET model_id=? WHERE model_id=?`, tgt.ID, oid); err != nil {
				return err
			}
			n++
			note := in.Note
			if note == "" {
				note = fmt.Sprintf("intégré depuis le modèle « %s » (%s)", om.Name, oid)
			}
			v := contracts.ModelVersion{ID: contracts.NewID("ver"), ModelID: tgt.ID, ParentID: parent, Label: fmt.Sprintf("v%d", n),
				Note: note, SourceFileIDs: []string{lf.ID}, CreatedAt: s.d.Store.Now()}
			if err := insertVersion(ctx, tx, v); err != nil {
				return err
			}
			parent = &v.ID
			now := fmtT(s.d.Store.Now())
			if _, err := tx.ExecContext(ctx, `UPDATE models SET deleted_at=?, updated_at=? WHERE id=?`, now, now, oid); err != nil {
				return err
			}
			if om.Dimensions != nil {
				b, _ := json.Marshal(om.Dimensions)
				if _, err := tx.ExecContext(ctx, `UPDATE models SET dimensions=? WHERE id=?`, string(b), tgt.ID); err != nil {
					return err
				}
			}
			if err := s.d.Store.AuditTx(ctx, tx, "mcp", "model.link_version", "model", tgt.ID,
				map[string]any{"from_model": oid, "version": v.Label, "version_id": v.ID}); err != nil {
				return err
			}
			if err := s.d.Store.AuditTx(ctx, tx, "mcp", "model.trash", "model", oid, map[string]any{"reason": "intégré comme version de " + tgt.ID}); err != nil {
				return err
			}
			out.Created = append(out.Created, v)
			out.Trashed = append(out.Trashed, oid)
		}
		return touchModel(ctx, tx, tgt.ID, s.d.Store.Now())
	})
	if err != nil {
		return nil, err
	}
	out.Note = "Versions créées (immuables, chaînées par parent). Les modèles intégrés sont en corbeille et restaurables ; leurs fichiers sont désormais rattachés au modèle cible."
	return out, nil
}

// ---- doublons exacts ----

// DuplicatesInput : sans argument, tous les groupes de doublons exacts ; avec sha256 ou path, un contrôle ciblé.
type DuplicatesInput struct {
	SHA256         string `json:"sha256,omitempty" jsonschema:"contrôler cette empreinte"`
	Path           string `json:"path,omitempty" jsonschema:"contrôler ce fichier (chemin absolu sous une racine d'import) : l'empreinte est calculée, rien n'est copié"`
	IncludeTrashed bool   `json:"include_trashed,omitempty"`
	Limit          int    `json:"limit,omitempty" jsonschema:"groupes par page (défaut 25, max 200)"`
	Offset         int    `json:"offset,omitempty"`
}

// DupFile : un exemplaire d'un contenu.
type DupFile struct {
	FileID         string `json:"file_id"`
	Name           string `json:"name"`
	ModelID        string `json:"model_id"`
	ModelName      string `json:"model_name"`
	CollectionPath string `json:"collection_path,omitempty"`
	Availability   string `json:"availability"`
	OriginPath     string `json:"origin_path,omitempty"`
	Trashed        bool   `json:"trashed,omitempty"`
}

// DupGroup : un contenu présent plusieurs fois.
type DupGroup struct {
	SHA256    string    `json:"sha256"`
	SizeBytes int64     `json:"size_bytes"`
	Copies    int       `json:"copies"`
	Files     []DupFile `json:"files"`
	// LogicalWastedBytes : octets économisables si ces exemplaires étaient fusionnés ; le stockage par contenu ne garde de toute façon qu'un blob.
	LogicalWastedBytes int64 `json:"logical_wasted_bytes"`
}

// DuplicatesResult : groupes de doublons exacts (jamais de suppression automatique).
type DuplicatesResult struct {
	Groups      []DupGroup `json:"groups"`
	TotalGroups int        `json:"total_groups"`
	Queried     string     `json:"queried_sha256,omitempty"`
	InCatalog   *bool      `json:"in_catalog,omitempty" jsonschema:"pour un contrôle ciblé : le contenu est-il déjà au catalogue ?"`
	Note        string     `json:"note"`
}

// FindExactDuplicates liste les doublons exacts par empreinte sha256.
func (s *Service) FindExactDuplicates(ctx context.Context, in DuplicatesInput) (*DuplicatesResult, error) {
	res := &DuplicatesResult{Groups: []DupGroup{}, Note: "Doublons exacts par empreinte sha256 ; aucune suppression automatique. Pour nettoyer : preview_file_operations avec delete (corbeille récupérable)."}
	live := ` AND m.deleted_at IS NULL`
	if in.IncludeTrashed {
		live = ""
	}
	sha := strings.ToLower(strings.TrimSpace(in.SHA256))
	if in.Path != "" {
		abs, _, err := s.authorize(in.Path)
		if err != nil {
			return nil, err
		}
		f, err := openRegular(abs)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		if sha, err = hashFile(f); err != nil {
			return nil, err
		}
	}
	if sha != "" {
		if len(sha) != 64 {
			return nil, invalidf("sha256 invalide")
		}
		g, err := s.dupGroup(ctx, sha, in.IncludeTrashed)
		if err != nil {
			return nil, err
		}
		found := len(g.Files) > 0
		res.Queried, res.InCatalog = sha, &found
		if found {
			res.Groups, res.TotalGroups = []DupGroup{*g}, 1
		}
		return res, nil
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 25
	}
	if limit > 200 {
		limit = 200
	}
	base := `FROM source_files f JOIN models m ON m.id=f.model_id WHERE 1=1` + live
	if err := s.db().QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT f.sha256 `+base+` GROUP BY f.sha256 HAVING COUNT(*)>1)`).Scan(&res.TotalGroups); err != nil {
		return nil, err
	}
	rows, err := s.db().QueryContext(ctx, `SELECT f.sha256 `+base+` GROUP BY f.sha256 HAVING COUNT(*)>1 ORDER BY COUNT(*) DESC, f.sha256 LIMIT ? OFFSET ?`, limit, in.Offset)
	if err != nil {
		return nil, err
	}
	var shas []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			rows.Close()
			return nil, err
		}
		shas = append(shas, h)
	}
	rows.Close()
	for _, h := range shas {
		g, err := s.dupGroup(ctx, h, in.IncludeTrashed)
		if err != nil {
			return nil, err
		}
		res.Groups = append(res.Groups, *g)
	}
	return res, nil
}

func (s *Service) dupGroup(ctx context.Context, sha string, withTrashed bool) (*DupGroup, error) {
	files, err := queryFiles(ctx, s.db(), `WHERE sha256=?`, sha)
	if err != nil {
		return nil, err
	}
	cols, err := allCollections(ctx, s.db())
	if err != nil {
		return nil, err
	}
	g := &DupGroup{SHA256: sha, Files: []DupFile{}}
	for _, f := range files {
		m, err := getModel(ctx, s.db(), f.ModelID)
		if err != nil {
			return nil, err
		}
		if m.DeletedAt != nil && !withTrashed {
			continue
		}
		df := DupFile{FileID: f.ID, Name: f.Name, ModelID: m.ID, ModelName: m.Name, CollectionPath: collectionPath(cols, m.CollectionID),
			Availability: string(f.Availability), Trashed: m.DeletedAt != nil}
		if f.OriginPath != nil {
			df.OriginPath = *f.OriginPath
		}
		g.Files = append(g.Files, df)
		g.SizeBytes = f.SizeBytes
	}
	g.Copies = len(g.Files)
	if g.Copies > 1 {
		g.LogicalWastedBytes = int64(g.Copies-1) * g.SizeBytes
	}
	return g, nil
}
