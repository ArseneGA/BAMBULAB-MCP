package agentlink

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

var shaRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidSHA indique une empreinte sha256 hexadécimale minuscule.
func ValidSHA(s string) bool { return shaRe.MatchString(s) }

// CleanRel valide un chemin relatif reçu d'un agent (séparateur « / ») : jamais
// absolu, ni « .. », ni lecteur, ni caractère réservé, ni nom réservé Windows.
func CleanRel(p string) (string, error) {
	if p == "" || len(p) > 1024 || strings.ContainsAny(p, "\\:\x00*?\"<>|") || strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("chemin relatif invalide %q", p)
	}
	parts := strings.Split(p, "/")
	for _, s := range parts {
		if s == "" || s == "." || s == ".." || strings.HasSuffix(s, ".") || strings.HasSuffix(s, " ") {
			return "", fmt.Errorf("chemin relatif invalide %q", p)
		}
		base := strings.ToUpper(strings.SplitN(s, ".", 2)[0])
		switch base {
		case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "LPT1", "LPT2", "LPT3":
			return "", fmt.Errorf("nom réservé dans %q", p)
		}
	}
	return path.Clean(p), nil
}

var labelBad = regexp.MustCompile(`[^\p{L}\p{N}._ -]+`)

// SafeLabel assainit une étiquette de dossier (nom de dossier et de collection).
func SafeLabel(l string) string {
	l = strings.TrimSpace(labelBad.ReplaceAllString(l, "_"))
	l = strings.TrimLeft(l, ".")
	l = strings.TrimRight(l, ". ")
	if len(l) > 64 {
		l = l[:64]
	}
	return l
}

func (r *Registry) partPath(agentID, sha string) string {
	return filepath.Join(r.stage, agentID, sha+".part")
}

func (r *Registry) hasDoneUpload(ctx context.Context, taskID, sha string) bool {
	var n int
	_ = r.db().QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_uploads WHERE task_id=? AND sha256=? AND state='done'`, taskID, sha).Scan(&n)
	return n > 0
}

func (r *Registry) outputs(ctx context.Context, taskID string) ([]Artifact, error) {
	rows, err := r.db().QueryContext(ctx, `SELECT sha256,size_bytes,name FROM agent_uploads WHERE task_id=? AND target='artifact' AND state='done' ORDER BY updated_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Artifact
	for rows.Next() {
		var a Artifact
		if err := rows.Scan(&a.SHA256, &a.Size, &a.Name); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

type uploadRow struct {
	Size   int64
	Target string
	Root   string
	Rel    string
	State  string
}

func (r *Registry) uploadRows(ctx context.Context, taskID, sha string) ([]uploadRow, error) {
	rows, err := r.db().QueryContext(ctx, `SELECT size_bytes,target,root_label,rel_path,state FROM agent_uploads WHERE task_id=? AND sha256=?`, taskID, sha)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uploadRow
	for rows.Next() {
		var u uploadRow
		if err := rows.Scan(&u.Size, &u.Target, &u.Root, &u.Rel, &u.State); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// authorizeUpload vérifie qu'un envoi est autorisé pour ce travail et cette empreinte.
func (r *Registry) authorizeUpload(ctx context.Context, a Agent, taskID, sha string, size int64, declare *UploadBegin) ([]uploadRow, error) {
	if !ValidSHA(sha) {
		return nil, errf(http.StatusBadRequest, CodeInvalid, "empreinte sha256 invalide")
	}
	t, err := r.ownTask(ctx, a, taskID)
	if err != nil {
		return nil, err
	}
	if t.State != StateAssigned && t.State != StateRunning && t.State != StateUnknown {
		return nil, errf(http.StatusConflict, CodeConflict, "le travail est %s : transfert refusé", t.State)
	}
	rows, err := r.uploadRows(ctx, taskID, sha)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		// Seul un artefact de sortie déclaré par l'agent pour son propre travail est accepté.
		if declare == nil || declare.Target != TargetArtifact {
			return nil, errf(http.StatusForbidden, CodeNotAllowed, "aucun transfert autorisé pour %s dans ce travail", sha)
		}
		if size < 0 || size > r.cfg.MaxUploadBytes {
			return nil, errf(http.StatusRequestEntityTooLarge, CodeInvalid, "taille invalide ou supérieure à la limite (%d octets)", r.cfg.MaxUploadBytes)
		}
		name := filepath.Base(strings.ReplaceAll(declare.Name, "\\", "/"))
		if name == "." || name == "/" {
			name = "artefact"
		}
		if _, err := r.db().ExecContext(ctx, `INSERT OR IGNORE INTO agent_uploads(agent_id,task_id,sha256,size_bytes,target,root_label,rel_path,name,state,updated_at)
			VALUES(?,?,?,?, 'artifact','','',?, 'pending', ?)`, a.ID, taskID, sha, size, name, store.FormatTime(r.now())); err != nil {
			return nil, err
		}
		rows, err = r.uploadRows(ctx, taskID, sha)
		if err != nil {
			return nil, err
		}
	}
	for _, u := range rows {
		if u.Size != size {
			return nil, errf(http.StatusConflict, CodeConflict, "taille annoncée %d différente de la taille autorisée %d", size, u.Size)
		}
	}
	return rows, nil
}

// BeginUpload ouvre ou reprend un transfert : renvoie l'offset à partir duquel envoyer.
func (r *Registry) BeginUpload(ctx context.Context, a Agent, b UploadBegin) (UploadStatus, error) {
	rows, err := r.authorizeUpload(ctx, a, b.TaskID, b.SHA256, b.Size, &b)
	if err != nil {
		return UploadStatus{}, err
	}
	done := true
	for _, u := range rows {
		if u.State != "done" {
			done = false
		}
	}
	if done {
		return UploadStatus{Offset: b.Size, Size: b.Size, Complete: true}, nil
	}
	if r.d.Blobs.Has(b.SHA256) { // contenu déjà au magasin : rien à retransférer
		if err := r.finishUpload(ctx, a, b.TaskID, b.SHA256); err != nil {
			return UploadStatus{}, err
		}
		return UploadStatus{Offset: b.Size, Size: b.Size, Complete: true}, nil
	}
	off := int64(0)
	if st, err := os.Stat(r.partPath(a.ID, b.SHA256)); err == nil {
		off = st.Size()
		if off > b.Size {
			_ = os.Remove(r.partPath(a.ID, b.SHA256))
			off = 0
		}
	}
	return UploadStatus{Offset: off, Size: b.Size}, nil
}

// PutChunk ajoute un morceau à partir de `start`. Le morceau doit commencer à
// l'offset courant (sinon 409 et l'offset réel est renvoyé). À la dernière
// octet, l'empreinte est vérifiée avant toute publication dans le magasin.
func (r *Registry) PutChunk(ctx context.Context, a Agent, taskID, sha string, start, total int64, body io.Reader) (UploadStatus, error) {
	rows, err := r.authorizeUpload(ctx, a, taskID, sha, total, nil)
	if err != nil {
		return UploadStatus{}, err
	}
	allDone := true
	for _, u := range rows {
		if u.State != "done" {
			allDone = false
		}
	}
	if allDone {
		return UploadStatus{Offset: total, Size: total, Complete: true}, nil
	}
	part := r.partPath(a.ID, sha)
	if err := os.MkdirAll(filepath.Dir(part), 0o755); err != nil {
		return UploadStatus{}, err
	}
	cur := int64(0)
	if st, err := os.Stat(part); err == nil {
		cur = st.Size()
	}
	if start != cur {
		return UploadStatus{Offset: cur, Size: total}, errf(http.StatusConflict, CodeConflict, "décalage inattendu : le serveur attend l'octet %d", cur)
	}
	room := total - cur
	if room > r.cfg.MaxChunkBytes {
		room = r.cfg.MaxChunkBytes
	}
	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return UploadStatus{}, err
	}
	n, cerr := io.Copy(f, io.LimitReader(body, room+1))
	if n > room { // trop de données : on tronque à ce qui était permis
		_ = f.Truncate(cur)
		f.Close()
		return UploadStatus{Offset: cur, Size: total}, errf(http.StatusRequestEntityTooLarge, CodeInvalid, "morceau trop grand (max %d octets, reste %d)", r.cfg.MaxChunkBytes, total-cur)
	}
	f.Close()
	if cerr != nil { // connexion coupée : on garde ce qui est arrivé, l'agent reprendra
		nw := cur + n
		return UploadStatus{Offset: nw, Size: total}, errf(http.StatusBadRequest, CodeInvalid, "lecture interrompue : %v", cerr)
	}
	now := cur + n
	if now < total {
		return UploadStatus{Offset: now, Size: total}, nil
	}
	// Dernier octet reçu : vérification complète de l'empreinte.
	got, err := fileSHA(part)
	if err != nil {
		return UploadStatus{}, err
	}
	if got != sha {
		_ = os.Remove(part)
		return UploadStatus{Offset: 0, Size: total}, errf(http.StatusUnprocessableEntity, CodeHashMismatch, "empreinte reçue %s différente de l'empreinte annoncée %s : transfert rejeté, à recommencer", got, sha)
	}
	pf, err := os.Open(part)
	if err != nil {
		return UploadStatus{}, err
	}
	_, _, err = r.d.Blobs.Put(pf, sha)
	pf.Close()
	if err != nil {
		return UploadStatus{}, err
	}
	_ = os.Remove(part)
	if err := r.finishUpload(ctx, a, taskID, sha); err != nil {
		return UploadStatus{}, err
	}
	return UploadStatus{Offset: total, Size: total, Complete: true}, nil
}

func fileSHA(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// finishUpload marque le transfert terminé : blob déjà vérifié ; pour un envoi
// d'inventaire, matérialise le fichier dans la zone de réception (nom et date
// d'origine) en vue de l'import catalogue.
func (r *Registry) finishUpload(ctx context.Context, a Agent, taskID, sha string) error {
	rows, err := r.uploadRows(ctx, taskID, sha)
	if err != nil {
		return err
	}
	for _, u := range rows {
		if u.Target != TargetInbox || u.State == "done" {
			continue
		}
		rel, err := CleanRel(u.Rel)
		if err != nil {
			return errf(http.StatusBadRequest, CodeInvalid, "%v", err)
		}
		dest := filepath.Join(r.inbox, a.ID, SafeLabel(u.Root), filepath.FromSlash(rel))
		if err := r.materialize(sha, dest, a.ID, u.Root, rel); err != nil {
			return err
		}
		if _, err := r.db().ExecContext(ctx, `UPDATE agent_inventory SET state='uploaded', note='', updated_at=? WHERE agent_id=? AND root_label=? AND rel_path=? AND sha256=?`,
			store.FormatTime(r.now()), a.ID, u.Root, rel, sha); err != nil {
			return err
		}
	}
	_, err = r.db().ExecContext(ctx, `UPDATE agent_uploads SET state='done', updated_at=? WHERE task_id=? AND sha256=?`, store.FormatTime(r.now()), taskID, sha)
	return err
}

// materialize copie un blob vers la zone de réception avec la date d'origine.
func (r *Registry) materialize(sha, dest, agentID, root, rel string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	src, err := r.d.Blobs.Open(sha)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp := dest + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return err
	}
	var mt int64
	if err := r.db().QueryRow(`SELECT mtime_ns FROM agent_inventory WHERE agent_id=? AND root_label=? AND rel_path=?`, agentID, root, rel).Scan(&mt); err == nil && mt > 0 {
		t := time.Unix(0, mt)
		_ = os.Chtimes(dest, t, t)
	}
	return nil
}

// OpenInput ouvre un artefact d'entrée, seulement s'il figure dans les entrées
// d'un travail non terminé de cet agent (aucune lecture arbitraire du magasin).
func (r *Registry) OpenInput(ctx context.Context, a Agent, sha string) (*os.File, error) {
	if !ValidSHA(sha) {
		return nil, errf(http.StatusBadRequest, CodeInvalid, "empreinte sha256 invalide")
	}
	var n int
	err := r.db().QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_tasks WHERE agent_id=? AND state IN ('queued','assigned','running','unknown') AND inputs LIKE ?`,
		a.ID, `%"`+sha+`"%`).Scan(&n)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, errf(http.StatusForbidden, CodeNotAllowed, "cet artefact n'est entrée d'aucun de vos travaux en cours")
	}
	f, err := r.d.Blobs.Open(sha)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errf(http.StatusNotFound, CodeNotFound, "artefact introuvable")
	}
	return f, err
}

// ---- inventaire ----

// InventoryItem est une ligne d'inventaire côté serveur.
type InventoryItem struct {
	RootLabel    string `json:"root_label"`
	RelPath      string `json:"rel_path"`
	Size         int64  `json:"size_bytes"`
	SHA256       string `json:"sha256"`
	State        string `json:"state"`
	OriginPath   string `json:"origin_path,omitempty"`
	SourceFileID string `json:"source_file_id,omitempty"`
	Note         string `json:"note,omitempty"`
}

// InventorySummary résume l'inventaire d'un agent.
type InventorySummary struct {
	Files        int              `json:"files"`
	Bytes        int64            `json:"bytes"`
	ByState      map[string]int   `json:"by_state"`
	BytesByState map[string]int64 `json:"bytes_by_state"`
	// WindowsOnlyBytes : octets encore disponibles uniquement sur Windows.
	WindowsOnlyBytes int64 `json:"windows_only_bytes"`
}

var inventoryExt = map[string]bool{".stl": true, ".obj": true, ".3mf": true, ".step": true, ".stp": true}

// ListInventory liste l'inventaire (filtres facultatifs) et son résumé.
func (r *Registry) ListInventory(ctx context.Context, agentID, label, state string, limit, offset int) ([]InventoryItem, InventorySummary, error) {
	sum := InventorySummary{ByState: map[string]int{}, BytesByState: map[string]int64{}}
	rows, err := r.db().QueryContext(ctx, `SELECT state,COUNT(*),COALESCE(SUM(size_bytes),0) FROM agent_inventory WHERE agent_id=? AND (?='' OR root_label=?) GROUP BY state`, agentID, label, label)
	if err != nil {
		return nil, sum, err
	}
	for rows.Next() {
		var s string
		var n int
		var b int64
		if err := rows.Scan(&s, &n, &b); err != nil {
			rows.Close()
			return nil, sum, err
		}
		sum.ByState[s], sum.BytesByState[s] = n, b
		sum.Files += n
		sum.Bytes += b
		if s != "uploaded" && s != "imported" {
			sum.WindowsOnlyBytes += b
		}
	}
	rows.Close()
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err = r.db().QueryContext(ctx, `SELECT root_label,rel_path,size_bytes,sha256,state,origin_path,COALESCE(source_file_id,''),note FROM agent_inventory
		WHERE agent_id=? AND (?='' OR root_label=?) AND (?='' OR state=?) ORDER BY root_label, rel_path LIMIT ? OFFSET ?`, agentID, label, label, state, state, limit, offset)
	if err != nil {
		return nil, sum, err
	}
	defer rows.Close()
	var out []InventoryItem
	for rows.Next() {
		var it InventoryItem
		if err := rows.Scan(&it.RootLabel, &it.RelPath, &it.Size, &it.SHA256, &it.State, &it.OriginPath, &it.SourceFileID, &it.Note); err != nil {
			return nil, sum, err
		}
		out = append(out, it)
	}
	return out, sum, rows.Err()
}

// RecordInventory enregistre un lot d'inventaire d'un dossier autorisé. Rien
// n'est copié ; les fichiers connus seulement par leur empreinte sont marqués
// « windows_only » au catalogue.
func (r *Registry) RecordInventory(ctx context.Context, a Agent, req InventoryRequest) (InventoryResponse, error) {
	label := SafeLabel(req.RootLabel)
	if label == "" || label != req.RootLabel {
		return InventoryResponse{}, errf(http.StatusBadRequest, CodeInvalid, "étiquette de dossier invalide %q", req.RootLabel)
	}
	if len(req.Entries) > 5000 {
		return InventoryResponse{}, errf(http.StatusBadRequest, CodeInvalid, "lot trop grand (5000 entrées max)")
	}
	resp := InventoryResponse{ByStatus: map[string]int{}}
	for _, e := range req.Entries {
		rel, err := CleanRel(e.RelPath)
		if err != nil || !ValidSHA(e.SHA256) || e.Size < 0 || !inventoryExt[strings.ToLower(path.Ext(rel))] {
			resp.Rejected++
			continue
		}
		now := store.FormatTime(r.now())
		var oldSHA, oldState string
		err = r.db().QueryRowContext(ctx, `SELECT sha256,state FROM agent_inventory WHERE agent_id=? AND root_label=? AND rel_path=?`, a.ID, label, rel).Scan(&oldSHA, &oldState)
		state := "inventoried"
		if err == nil && oldSHA == e.SHA256 && (oldState == "uploaded" || oldState == "imported" || oldState == "requested") {
			state = oldState
		}
		if _, err := r.db().ExecContext(ctx, `INSERT INTO agent_inventory(agent_id,root_label,rel_path,size_bytes,mtime_ns,sha256,origin_path,state,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?)
			ON CONFLICT(agent_id,root_label,rel_path) DO UPDATE SET size_bytes=excluded.size_bytes, mtime_ns=excluded.mtime_ns, sha256=excluded.sha256,
			origin_path=excluded.origin_path, state=excluded.state, updated_at=excluded.updated_at`,
			a.ID, label, rel, e.Size, e.MTimeNS, e.SHA256, e.OriginPath, state, now); err != nil {
			return resp, err
		}
		resp.Accepted++
		if r.d.Catalog == nil {
			resp.ByStatus["recorded"]++
			continue
		}
		fr, err := r.d.Catalog.RegisterRemote(ctx, catalog.RemoteFile{Agent: a.Name, Root: label, RelPath: rel, OriginPath: e.OriginPath,
			Size: e.Size, MTime: time.Unix(0, e.MTimeNS), SHA256: e.SHA256})
		if err != nil {
			r.d.Log.Warn("inventaire : enregistrement au catalogue", "path", e.OriginPath, "err", err)
			resp.ByStatus["error"]++
			continue
		}
		resp.ByStatus[fr.Status]++
		_, _ = r.db().ExecContext(ctx, `UPDATE agent_inventory SET source_file_id=?, note=? WHERE agent_id=? AND root_label=? AND rel_path=?`, nullStr(fr.SourceFileID), fr.Note, a.ID, label, rel)
	}
	return resp, nil
}

// SelectInput choisit les fichiers à copier.
type SelectInput struct {
	RootLabel string   `json:"root_label"`
	RelPaths  []string `json:"rel_paths,omitempty"` // vide : tous les fichiers inventoriés non encore copiés
}

// SelectResult : travail créé et estimation du volume.
type SelectResult struct {
	Task  TaskInfo `json:"task"`
	Files int      `json:"files"`
	Bytes int64    `json:"bytes"`
}

// SelectForUpload crée un travail d'envoi contrôlé des fichiers choisis.
func (r *Registry) SelectForUpload(ctx context.Context, agentID string, in SelectInput) (SelectResult, error) {
	label := SafeLabel(in.RootLabel)
	if label == "" {
		return SelectResult{}, errf(http.StatusBadRequest, CodeInvalid, "root_label requis")
	}
	q := `SELECT rel_path,sha256,size_bytes FROM agent_inventory WHERE agent_id=? AND root_label=? AND state IN ('inventoried','requested','error')`
	args := []any{agentID, label}
	if len(in.RelPaths) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(in.RelPaths)), ",")
		q += ` AND rel_path IN (` + ph + `)`
		for _, p := range in.RelPaths {
			args = append(args, p)
		}
	}
	rows, err := r.db().QueryContext(ctx, q+` ORDER BY rel_path LIMIT 2001`, args...)
	if err != nil {
		return SelectResult{}, err
	}
	var specs []UploadSpec
	type ent struct {
		Rel  string `json:"rel_path"`
		SHA  string `json:"sha256"`
		Size int64  `json:"size_bytes"`
	}
	var ents []ent
	var total int64
	for rows.Next() {
		var e ent
		if err := rows.Scan(&e.Rel, &e.SHA, &e.Size); err != nil {
			rows.Close()
			return SelectResult{}, err
		}
		ents = append(ents, e)
		specs = append(specs, UploadSpec{SHA256: e.SHA, Size: e.Size, Target: TargetInbox, RootLabel: label, RelPath: e.Rel})
		total += e.Size
	}
	rows.Close()
	if len(ents) == 0 {
		return SelectResult{}, errf(http.StatusConflict, CodeConflict, "aucun fichier inventorié à copier pour ce dossier")
	}
	if len(ents) > 2000 {
		return SelectResult{}, errf(http.StatusBadRequest, CodeInvalid, "plus de 2000 fichiers : choisir un sous-ensemble")
	}
	params, _ := jsonMarshal(map[string]any{"root_label": label, "entries": ents})
	t, err := r.createTask(ctx, CreateTaskInput{AgentID: agentID, Kind: KindUploadFiles, Params: params}, "", specs)
	if err != nil {
		return SelectResult{}, err
	}
	for _, e := range ents {
		_, _ = r.db().ExecContext(ctx, `UPDATE agent_inventory SET state='requested', updated_at=? WHERE agent_id=? AND root_label=? AND rel_path=? AND state IN ('inventoried','error','requested')`,
			store.FormatTime(r.now()), agentID, label, e.Rel)
	}
	return SelectResult{Task: t, Files: len(ents), Bytes: total}, nil
}

// onTaskFinished : après un envoi de fichiers, import catalogue en arrière-plan.
func (r *Registry) onTaskFinished(t TaskInfo, _ TaskResult) {
	if t.Kind != KindUploadFiles || r.d.Catalog == nil {
		return
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.importInbox(context.Background(), t.AgentID)
	}()
}

// WaitImports attend la fin des imports en arrière-plan (tests, arrêt propre).
func (r *Registry) WaitImports() { r.wg.Wait() }

var _ = sql.ErrNoRows
