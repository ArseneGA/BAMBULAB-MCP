package agentlink

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// CreateTaskInput décrit un travail à confier à un agent.
type CreateTaskInput struct {
	AgentID  string          `json:"agent_id,omitempty"` // vide : l'agent en ligne capable du travail
	Executor string          `json:"executor,omitempty"` // seul « windows-agent » est pris en charge ici
	Kind     string          `json:"kind"`
	Params   json.RawMessage `json:"params,omitempty"`
	Inputs   []Artifact      `json:"inputs,omitempty"` // artefacts du magasin de blobs transmis à l'agent
}

const taskCols = `id,agent_id,executor,kind,params,inputs,state,physical,cancel_requested,progress,message,result,error,note,replaces,created_at,assigned_at,started_at,finished_at`

func scanTask(row interface{ Scan(...any) error }) (TaskInfo, error) {
	var t TaskInfo
	var params, inputs, result, errj, replaces, created, assigned, started, finished sql.NullString
	var phys, cancel int
	if err := row.Scan(&t.ID, &t.AgentID, &t.Executor, &t.Kind, &params, &inputs, &t.State, &phys, &cancel, &t.Progress,
		&t.Message, &result, &errj, &t.Note, &replaces, &created, &assigned, &started, &finished); err != nil {
		return t, err
	}
	t.Physical, t.CancelRequested = phys == 1, cancel == 1
	if params.Valid {
		t.Params = json.RawMessage(params.String)
	}
	if inputs.Valid && inputs.String != "" {
		_ = json.Unmarshal([]byte(inputs.String), &t.Inputs)
	}
	if result.Valid && result.String != "" {
		t.Result = json.RawMessage(result.String)
	}
	if errj.Valid && errj.String != "" {
		var e TaskError
		if json.Unmarshal([]byte(errj.String), &e) == nil {
			t.Error = &e
		}
	}
	t.Replaces = replaces.String
	t.CreatedAt, _ = store.ParseTime(created.String)
	if finished.Valid {
		f, _ := store.ParseTime(finished.String)
		t.FinishedAt = &f
	}
	return t, nil
}

func (r *Registry) listTasksRaw(ctx context.Context, where string, args ...any) ([]TaskInfo, error) {
	rows, err := r.db().QueryContext(ctx, `SELECT `+taskCols+` FROM agent_tasks `+where+` ORDER BY created_at, rowid`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskInfo
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTask lit un travail (après balayage des agents perdus) avec ses sorties.
func (r *Registry) GetTask(ctx context.Context, id string) (TaskInfo, error) {
	_ = r.Sweep(ctx)
	t, err := scanTask(r.db().QueryRowContext(ctx, `SELECT `+taskCols+` FROM agent_tasks WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return t, errf(http.StatusNotFound, CodeNotFound, "travail introuvable")
	}
	if err != nil {
		return t, err
	}
	t.Outputs, err = r.outputs(ctx, id)
	return t, err
}

// ListTasks liste les travaux (filtres facultatifs).
func (r *Registry) ListTasks(ctx context.Context, agentID, state string, limit int) ([]TaskInfo, error) {
	_ = r.Sweep(ctx)
	var conds []string
	var args []any
	if agentID != "" {
		conds, args = append(conds, "agent_id=?"), append(args, agentID)
	}
	if state != "" {
		conds, args = append(conds, "state=?"), append(args, state)
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	ts, err := r.listTasksRaw(ctx, where, args...)
	if len(ts) > limit {
		ts = ts[len(ts)-limit:] // les plus récents
	}
	return ts, err
}

// CreateTask confie un travail à un agent EN LIGNE qui annonce savoir le faire.
func (r *Registry) CreateTask(ctx context.Context, in CreateTaskInput) (TaskInfo, error) {
	return r.createTask(ctx, in, "", nil)
}

func (r *Registry) createTask(ctx context.Context, in CreateTaskInput, replaces string, uploads []UploadSpec) (TaskInfo, error) {
	if in.Executor != "" && in.Executor != ExecutorWindowsAgent {
		return TaskInfo{}, errf(http.StatusBadRequest, CodeInvalid, "exécuteur %q : les travaux « linux » passent par la file de travaux de bambud, pas par ce protocole", in.Executor)
	}
	if PhysicalKinds[in.Kind] {
		return TaskInfo{}, errf(http.StatusConflict, CodePhysicalRefused, "%s est un travail physique : il n'est jamais assigné ni transféré automatiquement à un agent", in.Kind)
	}
	if !slices.Contains(AgentKinds, in.Kind) {
		return TaskInfo{}, errf(http.StatusBadRequest, CodeInvalid, "type de travail inconnu %q (attendus : %s)", in.Kind, strings.Join(AgentKinds, ", "))
	}
	if in.Kind == KindModifyOpenProject {
		return TaskInfo{}, errf(http.StatusNotImplemented, CodeNotSupported,
			"modifier en direct le projet ouvert dans Bambu Studio n'est pas possible sans passerelle C++ (aucune API publique de Studio, pas d'IPC). Alternative : modifier une copie du projet (catalogue/3MF) puis open_in_studio sur le résultat. Voir docs/jalons/J8.md")
	}
	params := in.Params
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(params, &obj) != nil {
		return TaskInfo{}, errf(http.StatusBadRequest, CodeInvalid, "params doit être un objet JSON")
	}
	for _, a := range in.Inputs {
		if len(a.SHA256) != 64 || !r.d.Blobs.Has(a.SHA256) {
			return TaskInfo{}, errf(http.StatusBadRequest, CodeInvalid, "artefact d'entrée %q absent du magasin de blobs", a.SHA256)
		}
	}
	// Choix de l'agent.
	agents, err := r.ListAgents(ctx)
	if err != nil {
		return TaskInfo{}, err
	}
	var chosen *Agent
	for i := range agents {
		a := agents[i]
		if in.AgentID != "" && a.ID != in.AgentID {
			continue
		}
		if !r.Online(a) || !a.HasCaps || !slices.Contains(a.Caps.Tasks, in.Kind) {
			continue
		}
		if chosen == nil || (a.LastSeen != nil && chosen.LastSeen != nil && a.LastSeen.After(*chosen.LastSeen)) {
			chosen = &agents[i]
		}
	}
	if chosen == nil {
		return TaskInfo{}, errf(http.StatusConflict, CodeAgentOffline,
			"aucun agent Windows en ligne ne sait faire %q : les opérations sur la session Windows attendent son retour (les copies stockées sur Linux restent traitables)", in.Kind)
	}
	// Les entrées sont figées avec leur taille réelle.
	inputs := make([]Artifact, 0, len(in.Inputs))
	for _, a := range in.Inputs {
		f, err := r.d.Blobs.Open(a.SHA256)
		if err != nil {
			return TaskInfo{}, err
		}
		st, _ := f.Stat()
		f.Close()
		inputs = append(inputs, Artifact{SHA256: a.SHA256, Name: a.Name, Size: st.Size()})
	}
	now := r.now()
	id := contracts.NewID("atk")
	ij, _ := json.Marshal(inputs)
	err = r.d.Store.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_tasks(id,agent_id,executor,kind,params,inputs,state,physical,note,replaces,created_at,updated_at)
			VALUES(?,?,?,?,?,?,'queued',0,'',?,?,?)`, id, chosen.ID, ExecutorWindowsAgent, in.Kind, string(params), string(ij),
			nullStr(replaces), store.FormatTime(now), store.FormatTime(now)); err != nil {
			return err
		}
		for _, u := range uploads {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO agent_uploads(agent_id,task_id,sha256,size_bytes,target,root_label,rel_path,name,state,updated_at)
				VALUES(?,?,?,?,?,?,?,?, 'pending', ?)`, chosen.ID, id, u.SHA256, u.Size, u.Target, u.RootLabel, u.RelPath, "", store.FormatTime(now)); err != nil {
				return err
			}
		}
		return r.d.Store.AuditTx(ctx, tx, "agent", "agent_task.create", "agent_task", id, map[string]any{"kind": in.Kind, "agent": chosen.ID})
	})
	if err != nil {
		return TaskInfo{}, err
	}
	r.broadcast()
	return r.GetTask(ctx, id)
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// setState change l'état si l'état courant est l'un des états `from`.
func (r *Registry) setState(ctx context.Context, id, to, note string, from ...string) error {
	ph := strings.TrimSuffix(strings.Repeat("?,", len(from)), ",")
	args := []any{to, note, store.FormatTime(r.now()), id}
	for _, f := range from {
		args = append(args, f)
	}
	_, err := r.db().ExecContext(ctx, `UPDATE agent_tasks SET state=?, note=?, updated_at=? WHERE id=? AND state IN (`+ph+`)`, args...)
	return err
}

// ownTask lit un travail et vérifie qu'il appartient à l'agent.
func (r *Registry) ownTask(ctx context.Context, a Agent, id string) (TaskInfo, error) {
	t, err := r.GetTask(ctx, id)
	if err != nil {
		return t, err
	}
	if t.AgentID != a.ID {
		return t, errf(http.StatusNotFound, CodeNotFound, "travail introuvable")
	}
	return t, nil
}

// NextTask est le long-polling : renvoie le plus ancien travail en file de
// l'agent, ou nil après `wait` sans rien.
func (r *Registry) NextTask(ctx context.Context, a Agent, wait time.Duration) (*Task, error) {
	if wait > r.cfg.MaxPollWait {
		wait = r.cfg.MaxPollWait
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		ch := r.waitCh()
		t, err := r.claim(ctx, a)
		if err != nil || t != nil {
			return t, err
		}
		r.touch(ctx, a.ID)
		select {
		case <-ch:
		case <-deadline.C:
			return nil, nil
		case <-ctx.Done():
			return nil, nil
		}
	}
}

func (r *Registry) claim(ctx context.Context, a Agent) (*Task, error) {
	var out *Task
	err := r.d.Store.Tx(ctx, func(tx *sql.Tx) error {
		out = nil
		row := tx.QueryRowContext(ctx, `SELECT `+taskCols+` FROM agent_tasks WHERE agent_id=? AND state='queued' AND cancel_requested=0 ORDER BY created_at, rowid LIMIT 1`, a.ID)
		t, err := scanTask(row)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		now := store.FormatTime(r.now())
		res, err := tx.ExecContext(ctx, `UPDATE agent_tasks SET state='assigned', assigned_at=?, updated_at=? WHERE id=? AND state='queued'`, now, now, t.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return nil
		}
		wt := Task{ID: t.ID, Kind: t.Kind, Params: t.Params, Inputs: t.Inputs, CreatedAt: t.CreatedAt}
		rows, err := tx.QueryContext(ctx, `SELECT sha256,size_bytes,target,root_label,rel_path FROM agent_uploads WHERE task_id=? AND state='pending' AND target='inbox' ORDER BY rel_path`, t.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var u UploadSpec
			if err := rows.Scan(&u.SHA256, &u.Size, &u.Target, &u.RootLabel, &u.RelPath); err != nil {
				return err
			}
			wt.Uploads = append(wt.Uploads, u)
		}
		out = &wt
		return rows.Err()
	})
	return out, err
}

// StartTask confirme le démarrage (assigned -> running ; unknown -> running si l'agent le détient toujours).
func (r *Registry) StartTask(ctx context.Context, a Agent, id string) error {
	t, err := r.ownTask(ctx, a, id)
	if err != nil {
		return err
	}
	switch t.State {
	case StateRunning:
		return nil
	case StateAssigned, StateUnknown:
		now := store.FormatTime(r.now())
		_, err = r.db().ExecContext(ctx, `UPDATE agent_tasks SET state='running', started_at=COALESCE(started_at,?), updated_at=? WHERE id=? AND state IN ('assigned','unknown')`, now, now, id)
		return err
	}
	return errf(http.StatusConflict, CodeConflict, "le travail est %s : démarrage refusé", t.State)
}

// ReportProgress met à jour l'avancement.
func (r *Registry) ReportProgress(ctx context.Context, a Agent, id string, p ProgressReport) error {
	t, err := r.ownTask(ctx, a, id)
	if err != nil {
		return err
	}
	if IsTerminal(t.State) {
		return errf(http.StatusConflict, CodeConflict, "le travail est %s", t.State)
	}
	if p.Progress < 0 {
		p.Progress = 0
	}
	if p.Progress > 1 {
		p.Progress = 1
	}
	if len(p.Message) > 500 {
		p.Message = p.Message[:500]
	}
	_, err = r.db().ExecContext(ctx, `UPDATE agent_tasks SET progress=?, message=?, updated_at=? WHERE id=?`, p.Progress, p.Message, store.FormatTime(r.now()), id)
	return err
}

// Complete enregistre le rapport final. Idempotent pour un même état final ; un
// travail « unknown » est réconcilié par ce rapport ; un travail annulé ou déjà
// terminé autrement refuse (409) : pas de publication en double.
func (r *Registry) Complete(ctx context.Context, a Agent, id string, res TaskResult) (TaskInfo, error) {
	t, err := r.ownTask(ctx, a, id)
	if err != nil {
		return t, err
	}
	switch res.State {
	case StateSucceeded, StateFailed, StateCancelled:
	default:
		return t, errf(http.StatusBadRequest, CodeInvalid, "état final invalide %q", res.State)
	}
	if IsTerminal(t.State) {
		if t.State == res.State && t.State != StateCancelled {
			return t, nil
		}
		return t, errf(http.StatusConflict, CodeConflict, "le travail est déjà %s : rapport refusé", t.State)
	}
	if res.State == StateSucceeded {
		for _, o := range res.Outputs {
			if !r.hasDoneUpload(ctx, id, o.SHA256) || !r.d.Blobs.Has(o.SHA256) {
				return t, errf(http.StatusConflict, CodeOutputMissing, "sortie %s non transférée ou non vérifiée : envoyer l'artefact avant le rapport", o.SHA256)
			}
		}
	}
	var rj, ej any
	if len(res.Result) > 0 {
		rj = string(res.Result)
	}
	if res.Error != nil {
		b, _ := json.Marshal(res.Error)
		ej = string(b)
	}
	note := ""
	if t.State == StateUnknown {
		note = "réconcilié : l'agent a rapporté l'issue après sa reconnexion"
	}
	now := store.FormatTime(r.now())
	// Garde d'état : seul un travail non terminé et déjà remis à l'agent peut recevoir son rapport. Un
	// changement concurrent (annulation, remplacement) est détecté par RowsAffected.
	up, err := r.db().ExecContext(ctx, `UPDATE agent_tasks SET state=?, result=?, error=?, note=CASE WHEN ?='' THEN note ELSE ? END,
		progress=CASE WHEN ?='succeeded' THEN 1 ELSE progress END, finished_at=?, updated_at=? WHERE id=? AND state IN ('assigned','running','unknown')`,
		res.State, rj, ej, note, note, res.State, now, now, id)
	if err != nil {
		return t, err
	}
	if n, _ := up.RowsAffected(); n != 1 {
		cur, gerr := r.GetTask(ctx, id)
		if gerr == nil && cur.State == res.State && cur.State != StateCancelled {
			return cur, nil // rapport identique déjà enregistré en parallèle
		}
		return t, errf(http.StatusConflict, CodeConflict, "le travail a changé d'état pendant le rapport (%s) : rapport refusé", cur.State)
	}
	_ = r.d.Store.Audit(ctx, "agent", "agent_task.finish", "agent_task", id, map[string]any{"state": res.State})
	r.dropParts(ctx, a.ID, id)
	r.onTaskFinished(t, res)
	return r.GetTask(ctx, id)
}

// CancelTask : en file = immédiat ; remis/en cours = demande relayée par heartbeat. Chaque mise à jour est
// gardée par l'état lu (RowsAffected) : si le travail change d'état entre la lecture et l'écriture, on relit
// (au plus 3 fois) au lieu d'écraser un état plus récent.
func (r *Registry) CancelTask(ctx context.Context, id string) (TaskInfo, error) {
	for attempt := 0; attempt < 3; attempt++ {
		t, err := r.GetTask(ctx, id)
		if err != nil {
			return t, err
		}
		now := store.FormatTime(r.now())
		var res sql.Result
		switch t.State {
		case StateQueued:
			res, err = r.db().ExecContext(ctx, `UPDATE agent_tasks SET state='cancelled', finished_at=?, updated_at=?, note='annulé avant remise à l''agent' WHERE id=? AND state='queued'`, now, now, id)
		case StateAssigned, StateRunning:
			res, err = r.db().ExecContext(ctx, `UPDATE agent_tasks SET cancel_requested=1, updated_at=? WHERE id=? AND state IN ('assigned','running')`, now, id)
		case StateUnknown:
			res, err = r.db().ExecContext(ctx, `UPDATE agent_tasks SET state='cancelled', cancel_requested=1, finished_at=?, updated_at=?, note='annulé alors que l''issue était inconnue' WHERE id=? AND state='unknown'`, now, now, id)
		default:
			return t, nil // terminé : sans effet
		}
		if err != nil {
			return t, err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			continue // l'état a changé entre la lecture et l'écriture : on relit
		}
		if t.State == StateQueued || t.State == StateUnknown {
			r.dropParts(ctx, t.AgentID, id)
		}
		r.broadcast()
		return r.GetTask(ctx, id)
	}
	return TaskInfo{}, errf(http.StatusConflict, CodeConflict, "le travail change d'état en continu : réessayer l'annulation")
}

// Requeue recrée un travail (nouvel identifiant) à la place d'un travail
// inconnu/échoué/annulé. Refusé pour un travail physique. L'ancien passe à
// « cancelled » (remplacé) : un rapport tardif de l'agent est refusé, donc pas
// de publication en double.
func (r *Registry) Requeue(ctx context.Context, id string) (TaskInfo, error) {
	t, err := r.GetTask(ctx, id)
	if err != nil {
		return t, err
	}
	if t.Physical || PhysicalKinds[t.Kind] {
		return t, errf(http.StatusConflict, CodePhysicalRefused, "un travail physique n'est jamais ré-assigné automatiquement")
	}
	if t.State != StateUnknown && t.State != StateFailed && t.State != StateCancelled {
		return t, errf(http.StatusConflict, CodeConflict, "seul un travail inconnu, échoué ou annulé peut être recréé (état : %s)", t.State)
	}
	var uploads []UploadSpec
	rows, err := r.db().QueryContext(ctx, `SELECT sha256,size_bytes,target,root_label,rel_path FROM agent_uploads WHERE task_id=? AND target='inbox' AND state='pending'`, id)
	if err != nil {
		return t, err
	}
	for rows.Next() {
		var u UploadSpec
		if err := rows.Scan(&u.SHA256, &u.Size, &u.Target, &u.RootLabel, &u.RelPath); err != nil {
			rows.Close()
			return t, err
		}
		uploads = append(uploads, u)
	}
	rows.Close()
	nt, err := r.createTask(ctx, CreateTaskInput{AgentID: t.AgentID, Kind: t.Kind, Params: t.Params, Inputs: t.Inputs}, id, uploads)
	if err != nil {
		return t, err
	}
	now := store.FormatTime(r.now())
	up, err := r.db().ExecContext(ctx, `UPDATE agent_tasks SET state='cancelled', cancel_requested=1, finished_at=COALESCE(finished_at,?), updated_at=?, note=? WHERE id=? AND state IN ('unknown','failed','cancelled')`,
		now, now, "remplacé par "+nt.ID, id)
	if n := int64(0); err == nil {
		n, _ = up.RowsAffected()
		if n == 1 {
			return nt, nil
		}
	}
	// L'ancien travail a changé d'état pendant la recréation (l'agent a rapporté, par exemple) : le remplaçant
	// est annulé, aucune double exécution.
	_, _ = r.db().ExecContext(ctx, `UPDATE agent_tasks SET state='cancelled', cancel_requested=1, finished_at=?, updated_at=?, note=? WHERE id=? AND state IN ('queued','assigned')`,
		now, now, "annulé : le travail d'origine "+id+" a changé d'état pendant le remplacement", nt.ID)
	r.broadcast()
	if err != nil {
		return t, err
	}
	return t, errf(http.StatusConflict, CodeConflict, "le travail %s a changé d'état pendant le remplacement : rien n'a été recréé", id)
}
