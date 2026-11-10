// Package projects : suivi de projet de premier rang (G9, PJ2). Un projet regroupe des pièces (modèles) avec une quantité
// voulue, peut contenir des sous-projets (assemblages, D9) et rend son avancement (« 4/6 pièces imprimées »), son coût et
// son temps réels, un statut SUGGÉRÉ et la prochaine action.
//
// Règles du paquet :
//   - calcul DÉTERMINISTE seulement, aucune IA, aucun appel réseau ; une valeur inconnue se dit « non disponible » avec sa raison ;
//   - « imprimée » = l'impression a atteint 100 %, verdict ou pas (D7) ; le verdict (réussie, ratée, à refaire) est affiché à part ;
//   - le statut suggéré n'est JAMAIS appliqué seul : seul update_project change le statut ;
//   - aucune suppression définitive d'un projet (archivage seulement) ;
//   - aucune fonction du paquet ne lance, n'arrête ni ne reprend d'impression, ne crée ni n'approuve de confirmation :
//     le paquet ne lit que le journal (via internal/insights) et n'écrit que dans ses propres tables.
package projects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/insights"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// ErrInvalid : entrée invalide (traduite en 400 / erreur d'outil).
var ErrInvalid = errors.New("entrée invalide")

// ErrNotFound : projet, pièce ou modèle introuvable.
var ErrNotFound = store.ErrNotFound

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w : %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Statuts d'un projet (transitions libres ; abandoned et in_use posent closed_at).
const (
	StatusIdea      = "idea"
	StatusDesign    = "design"
	StatusPrinting  = "printing"
	StatusDone      = "done"
	StatusInUse     = "in_use"
	StatusAbandoned = "abandoned"
)

// Statuses liste les statuts connus, dans l'ordre d'un projet qui avance.
var Statuses = []string{StatusIdea, StatusDesign, StatusPrinting, StatusDone, StatusInUse, StatusAbandoned}

// StatusLabel : libellé français d'un statut.
func StatusLabel(s string) string {
	switch s {
	case StatusIdea:
		return "idée"
	case StatusDesign:
		return "conception"
	case StatusPrinting:
		return "impression"
	case StatusDone:
		return "terminé"
	case StatusInUse:
		return "en usage"
	case StatusAbandoned:
		return "abandonné"
	}
	return s
}

func validStatus(s string) bool {
	for _, x := range Statuses {
		if x == s {
			return true
		}
	}
	return false
}

// Bornes.
const (
	MaxNameLen      = 120
	MaxDescription  = 2000
	MaxRole         = 80
	MaxQuantity     = 1000
	DefaultMaxDepth = 4   // niveaux de projets (un projet racine = niveau 1)
	MaxPartsOut     = 500 // pièces rendues au plus par projet
	maxTraversal    = 16  // garde de parcours contre une base incohérente
	maxList         = 200
)

const actor = "projects"

// Counter est la part du journal dont le suivi se sert : le comptage des impressions par pièce.
type Counter interface {
	PrintCountsByModel(ctx context.Context, modelIDs []string) (map[string]*insights.PrintCounts, error)
}

// Config câble le service.
type Config struct {
	Store  *store.Store
	Counts Counter
	// MaxDepth : profondeur maximale de la hiérarchie (défaut 4).
	MaxDepth int
	// Now est injectable pour les tests.
	Now func() time.Time
}

// Service est le point d'entrée du paquet.
type Service struct {
	cfg Config
}

// New construit le service (la base doit être migrée : migration 0300).
func New(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, errors.New("projects : magasin requis")
	}
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = DefaultMaxDepth
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return cfg.Store.Now() }
	}
	return &Service{cfg: cfg}, nil
}

func (s *Service) now() time.Time { return s.cfg.Now().UTC() }

// MaxDepth : profondeur maximale appliquée.
func (s *Service) MaxDepth() int { return s.cfg.MaxDepth }

// Project : la ligne d'un projet.
type Project struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description,omitempty"`
	Status       string    `json:"status" jsonschema:"idea | design | printing | done | in_use | abandoned"`
	CollectionID string    `json:"collection_id,omitempty" jsonschema:"collection adoptée : ses modèles comptent comme pièces de quantité 1 si aucune ligne de pièce ne les cite"`
	ParentID     string    `json:"parent_id,omitempty" jsonschema:"projet parent (ce projet est un sous-projet) ; vide : projet racine"`
	DueDate      string    `json:"due_date,omitempty" jsonschema:"échéance AAAA-MM-JJ"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	ClosedAt     time.Time `json:"closed_at,omitzero" jsonschema:"posé quand le projet passe en abandonné ou en usage"`
	Archived     bool      `json:"archived,omitempty"`
	Rev          int       `json:"rev"`
}

const projectCols = `id,name,description,status,collection_id,parent_id,due_date,created_at,updated_at,closed_at,archived,rev`

type rowScanner interface{ Scan(dest ...any) error }

func scanProject(r rowScanner) (Project, error) {
	var p Project
	var coll, parent, due, closed sql.NullString
	var created, updated string
	var archived int
	if err := r.Scan(&p.ID, &p.Name, &p.Description, &p.Status, &coll, &parent, &due, &created, &updated, &closed, &archived, &p.Rev); err != nil {
		return p, err
	}
	p.CollectionID, p.ParentID, p.DueDate = coll.String, parent.String, due.String
	p.CreatedAt, _ = store.ParseTime(created)
	p.UpdatedAt, _ = store.ParseTime(updated)
	if closed.Valid && closed.String != "" {
		p.ClosedAt, _ = store.ParseTime(closed.String)
	}
	p.Archived = archived == 1
	return p, nil
}

// execer : la part commune de *sql.DB et *sql.Tx dont les lectures se servent.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func loadProject(ctx context.Context, q querier, id string) (Project, error) {
	p, err := scanProject(q.QueryRowContext(ctx, `SELECT `+projectCols+` FROM projects WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, fmt.Errorf("projet %q : %w", id, ErrNotFound)
	}
	return p, err
}

// parentMap : id -> parent_id de tous les projets (la hiérarchie tient en mémoire, quelques centaines de lignes au plus).
func parentMap(ctx context.Context, q querier) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, COALESCE(parent_id,'') FROM projects`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var id, parent string
		if err := rows.Scan(&id, &parent); err != nil {
			return nil, err
		}
		m[id] = parent
	}
	return m, rows.Err()
}

// checkHierarchy applique les garde-fous D9 pour rattacher `id` sous `parentID` (id vide : projet en création, sans
// descendants) : le parent existe, aucun cycle (le parent n'est ni le projet ni l'un de ses descendants), profondeur
// maximale respectée pour le projet ET son sous-arbre.
func (s *Service) checkHierarchy(ctx context.Context, q querier, id, parentID string) error {
	if parentID == "" {
		return nil
	}
	pm, err := parentMap(ctx, q)
	if err != nil {
		return err
	}
	if _, ok := pm[parentID]; !ok {
		return fmt.Errorf("projet parent %q : %w", parentID, ErrNotFound)
	}
	if id != "" {
		if id == parentID {
			return invalid("un projet ne peut pas être son propre parent")
		}
		for cur, n := parentID, 0; cur != "" && n <= len(pm); cur, n = pm[cur], n+1 {
			if cur == id {
				return invalid("rattachement refusé : %q est un descendant de %q (cela créerait un cycle)", parentID, id)
			}
		}
	}
	level := 1 // niveau du parent (racine = 1)
	for cur, n := pm[parentID], 0; cur != "" && n <= len(pm); cur, n = pm[cur], n+1 {
		level++
	}
	height := 1 // hauteur du sous-arbre rattaché
	if id != "" {
		kids := map[string][]string{}
		for c, p := range pm {
			if p != "" {
				kids[p] = append(kids[p], c)
			}
		}
		var h func(n string, d int) int
		h = func(n string, d int) int {
			best := 1
			if d > len(pm) {
				return best
			}
			for _, c := range kids[n] {
				if x := 1 + h(c, d+1); x > best {
					best = x
				}
			}
			return best
		}
		height = h(id, 0)
	}
	if level+height > s.cfg.MaxDepth {
		return invalid("profondeur maximale dépassée : %d niveaux au plus (le parent est au niveau %d, le projet rattaché avec ses sous-projets en occupe %d)", s.cfg.MaxDepth, level, height)
	}
	return nil
}

func cleanName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if n == "" {
		return "", invalid("name requis")
	}
	if utf8.RuneCountInString(n) > MaxNameLen {
		return "", invalid("name : %d caractères au plus", MaxNameLen)
	}
	return n, nil
}

func cleanDescription(d string) (string, error) {
	d = strings.TrimSpace(d)
	if utf8.RuneCountInString(d) > MaxDescription {
		return "", invalid("description : %d caractères au plus", MaxDescription)
	}
	return d, nil
}

func cleanDue(d string) (string, error) {
	d = strings.TrimSpace(d)
	if d == "" {
		return "", nil
	}
	if _, err := time.Parse("2006-01-02", d); err != nil {
		return "", invalid("due_date %q : format AAAA-MM-JJ attendu", d)
	}
	return d, nil
}

// CreateInput : un nouveau projet.
type CreateInput struct {
	Name         string `json:"name" jsonschema:"nom du projet (120 caractères au plus)"`
	Description  string `json:"description,omitempty" jsonschema:"description libre (2000 caractères au plus) ; ne jamais y mettre de secret"`
	Status       string `json:"status,omitempty" jsonschema:"idea (défaut) | design | printing | done | in_use | abandoned"`
	CollectionID string `json:"collection_id,omitempty" jsonschema:"adopter une collection : ses modèles comptent comme pièces de quantité 1"`
	ParentID     string `json:"parent_id,omitempty" jsonschema:"projet parent : crée un sous-projet (assemblage), profondeur maximale 4 niveaux"`
	DueDate      string `json:"due_date,omitempty" jsonschema:"échéance AAAA-MM-JJ"`
}

// Create crée un projet.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Project, error) {
	name, err := cleanName(in.Name)
	if err != nil {
		return nil, err
	}
	desc, err := cleanDescription(in.Description)
	if err != nil {
		return nil, err
	}
	due, err := cleanDue(in.DueDate)
	if err != nil {
		return nil, err
	}
	status := strings.TrimSpace(in.Status)
	if status == "" {
		status = StatusIdea
	}
	if !validStatus(status) {
		return nil, invalid("status %q inconnu (%s)", status, strings.Join(Statuses, ", "))
	}
	id, now := contracts.NewID("prj"), s.now()
	var out Project
	err = s.cfg.Store.Tx(ctx, func(tx *sql.Tx) error {
		if in.CollectionID != "" {
			if err := s.checkCollection(ctx, tx, in.CollectionID); err != nil {
				return err
			}
		}
		if err := s.checkHierarchy(ctx, tx, "", in.ParentID); err != nil {
			return err
		}
		var closed any
		if status == StatusAbandoned || status == StatusInUse {
			closed = store.FormatTime(now)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO projects(id,name,description,status,collection_id,parent_id,due_date,created_at,updated_at,closed_at,archived,rev)
			VALUES(?,?,?,?,?,?,?,?,?,?,0,1)`, id, name, desc, status, nullStr(in.CollectionID), nullStr(in.ParentID), nullStr(due), store.FormatTime(now), store.FormatTime(now), closed); err != nil {
			return err
		}
		if err := s.cfg.Store.AuditTx(ctx, tx, actor, "project.create", "project", id, map[string]any{"status": status, "parent": in.ParentID}); err != nil {
			return err
		}
		out, err = loadProject(ctx, tx, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func nullStr(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func (s *Service) checkCollection(ctx context.Context, q querier, id string) error {
	var n int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM collections WHERE id=?`, id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("collection %q : %w", id, ErrNotFound)
	}
	return nil
}

// UpdateInput : les champs omis ne changent pas. parent_id vide détache le projet (il redevient racine).
type UpdateInput struct {
	ID           string  `json:"id" jsonschema:"identifiant du projet (prj_...)"`
	Name         *string `json:"name,omitempty"`
	Description  *string `json:"description,omitempty"`
	Status       string  `json:"status,omitempty" jsonschema:"idea | design | printing | done | in_use | abandoned (le statut suggéré n'est jamais appliqué seul : c'est cet appel qui le change)"`
	CollectionID *string `json:"collection_id,omitempty" jsonschema:"collection adoptée ; chaîne vide : ne plus adopter"`
	ParentID     *string `json:"parent_id,omitempty" jsonschema:"nouveau projet parent ; chaîne vide : redevient projet racine. Refusé si cela crée un cycle ou dépasse 4 niveaux"`
	DueDate      *string `json:"due_date,omitempty" jsonschema:"AAAA-MM-JJ ; chaîne vide : aucune échéance"`
	Archived     *bool   `json:"archived,omitempty" jsonschema:"true : archive le projet (masqué de list_projects par défaut, ses sous-projets restent consultables). Aucune suppression définitive n'existe"`
}

// Update modifie un projet.
func (s *Service) Update(ctx context.Context, in UpdateInput) (*Project, error) {
	if strings.TrimSpace(in.ID) == "" {
		return nil, invalid("id requis")
	}
	status := strings.TrimSpace(in.Status)
	if status != "" && !validStatus(status) {
		return nil, invalid("status %q inconnu (%s)", status, strings.Join(Statuses, ", "))
	}
	var out Project
	err := s.cfg.Store.Tx(ctx, func(tx *sql.Tx) error {
		p, err := loadProject(ctx, tx, in.ID)
		if err != nil {
			return err
		}
		changed := []string{}
		now := s.now()
		if in.Name != nil {
			n, err := cleanName(*in.Name)
			if err != nil {
				return err
			}
			if n != p.Name {
				p.Name = n
				changed = append(changed, "name")
			}
		}
		if in.Description != nil {
			d, err := cleanDescription(*in.Description)
			if err != nil {
				return err
			}
			if d != p.Description {
				p.Description = d
				changed = append(changed, "description")
			}
		}
		if in.DueDate != nil {
			d, err := cleanDue(*in.DueDate)
			if err != nil {
				return err
			}
			if d != p.DueDate {
				p.DueDate = d
				changed = append(changed, "due_date")
			}
		}
		if in.CollectionID != nil && *in.CollectionID != p.CollectionID {
			if *in.CollectionID != "" {
				if err := s.checkCollection(ctx, tx, *in.CollectionID); err != nil {
					return err
				}
			}
			p.CollectionID = *in.CollectionID
			changed = append(changed, "collection_id")
		}
		if in.ParentID != nil && *in.ParentID != p.ParentID {
			if err := s.checkHierarchy(ctx, tx, p.ID, *in.ParentID); err != nil {
				return err
			}
			p.ParentID = *in.ParentID
			changed = append(changed, "parent_id")
		}
		if in.Archived != nil && *in.Archived != p.Archived {
			p.Archived = *in.Archived
			changed = append(changed, "archived")
		}
		if status != "" && status != p.Status {
			p.Status = status
			changed = append(changed, "status")
			if status == StatusAbandoned || status == StatusInUse {
				p.ClosedAt = now
			} else {
				p.ClosedAt = time.Time{}
			}
		}
		if len(changed) == 0 {
			out = p
			return nil
		}
		var closed any
		if !p.ClosedAt.IsZero() {
			closed = store.FormatTime(p.ClosedAt)
		}
		arch := 0
		if p.Archived {
			arch = 1
		}
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET name=?, description=?, status=?, collection_id=?, parent_id=?, due_date=?, closed_at=?, archived=?, updated_at=?, rev=rev+1 WHERE id=?`,
			p.Name, p.Description, p.Status, nullStr(p.CollectionID), nullStr(p.ParentID), nullStr(p.DueDate), closed, arch, store.FormatTime(now), p.ID); err != nil {
			return err
		}
		if err := s.cfg.Store.AuditTx(ctx, tx, actor, "project.update", "project", p.ID, map[string]any{"fields": changed, "status": p.Status}); err != nil {
			return err
		}
		out, err = loadProject(ctx, tx, p.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// PartInput : une pièce d'un projet.
type PartInput struct {
	ProjectID string `json:"project_id"`
	ModelID   string `json:"model_id" jsonschema:"pièce (modèle du catalogue)"`
	Quantity  int    `json:"quantity,omitempty" jsonschema:"quantité voulue, 1 à 1000 (défaut 1 ; à la mise à jour, omise = inchangée)"`
	Role      string `json:"role,omitempty" jsonschema:"rôle libre dans le projet (80 caractères au plus), ex. support, couvercle"`
}

// AddPart ajoute une pièce au projet, ou met à jour sa quantité et son rôle si elle y est déjà.
func (s *Service) AddPart(ctx context.Context, in PartInput) (*Project, error) {
	if in.ProjectID == "" || in.ModelID == "" {
		return nil, invalid("project_id et model_id requis")
	}
	if in.Quantity < 0 || in.Quantity > MaxQuantity {
		return nil, invalid("quantity : entre 1 et %d", MaxQuantity)
	}
	role := strings.TrimSpace(in.Role)
	if utf8.RuneCountInString(role) > MaxRole {
		return nil, invalid("role : %d caractères au plus", MaxRole)
	}
	var out Project
	err := s.cfg.Store.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := loadProject(ctx, tx, in.ProjectID); err != nil {
			return err
		}
		var deleted sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT deleted_at FROM models WHERE id=?`, in.ModelID).Scan(&deleted); errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("pièce %q (modèle inconnu du catalogue) : %w", in.ModelID, ErrNotFound)
		} else if err != nil {
			return err
		}
		if deleted.Valid && deleted.String != "" {
			return invalid("la pièce %q est dans la corbeille : la restaurer avant de l'ajouter", in.ModelID)
		}
		now := store.FormatTime(s.now())
		var qty int
		err := tx.QueryRowContext(ctx, `SELECT quantity FROM project_parts WHERE project_id=? AND model_id=?`, in.ProjectID, in.ModelID).Scan(&qty)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			q := in.Quantity
			if q == 0 {
				q = 1
			}
			var pos int
			_ = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position),0)+1 FROM project_parts WHERE project_id=?`, in.ProjectID).Scan(&pos)
			if _, err := tx.ExecContext(ctx, `INSERT INTO project_parts(project_id,model_id,quantity,role,position,added_at) VALUES(?,?,?,?,?,?)`, in.ProjectID, in.ModelID, q, role, pos, now); err != nil {
				return err
			}
			if err := s.cfg.Store.AuditTx(ctx, tx, actor, "project.part.add", "project", in.ProjectID, map[string]any{"model": in.ModelID, "quantity": q}); err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			if in.Quantity > 0 {
				qty = in.Quantity
			}
			if _, err := tx.ExecContext(ctx, `UPDATE project_parts SET quantity=?, role=CASE WHEN ?<>'' THEN ? ELSE role END WHERE project_id=? AND model_id=?`, qty, role, role, in.ProjectID, in.ModelID); err != nil {
				return err
			}
			if err := s.cfg.Store.AuditTx(ctx, tx, actor, "project.part.update", "project", in.ProjectID, map[string]any{"model": in.ModelID, "quantity": qty}); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET updated_at=?, rev=rev+1 WHERE id=?`, now, in.ProjectID); err != nil {
			return err
		}
		out, err = loadProject(ctx, tx, in.ProjectID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// RemovePart retire une pièce du projet (le lien seulement : le modèle et ses impressions restent intacts).
func (s *Service) RemovePart(ctx context.Context, projectID, modelID string) (*Project, error) {
	if projectID == "" || modelID == "" {
		return nil, invalid("project_id et model_id requis")
	}
	var out Project
	err := s.cfg.Store.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := loadProject(ctx, tx, projectID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM project_parts WHERE project_id=? AND model_id=?`, projectID, modelID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("la pièce %q n'est pas une pièce du projet %q (une pièce adoptée avec une collection se retire en changeant collection_id) : %w", modelID, projectID, ErrNotFound)
		}
		now := store.FormatTime(s.now())
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET updated_at=?, rev=rev+1 WHERE id=?`, now, projectID); err != nil {
			return err
		}
		if err := s.cfg.Store.AuditTx(ctx, tx, actor, "project.part.remove", "project", projectID, map[string]any{"model": modelID}); err != nil {
			return err
		}
		out, err = loadProject(ctx, tx, projectID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ProjectsOfModel liste (id, nom, statut) les projets non archivés qui citent une pièce : sert à la fiche pièce (PJ5).
func (s *Service) ProjectsOfModel(ctx context.Context, modelID string) ([]ProjectRef, error) {
	rows, err := s.cfg.Store.DB().QueryContext(ctx, `SELECT p.id, p.name, p.status FROM projects p JOIN project_parts pp ON pp.project_id=p.id WHERE pp.model_id=? AND p.archived=0 ORDER BY p.name, p.id`, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjectRef{}
	for rows.Next() {
		var r ProjectRef
		if err := rows.Scan(&r.ID, &r.Name, &r.Status); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ProjectRef : référence courte à un projet.
type ProjectRef struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status,omitempty"`
}
