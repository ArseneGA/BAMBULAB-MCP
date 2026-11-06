package agentlink

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

func jsonMarshal(v any) (json.RawMessage, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(b.Bytes()), nil
}

// importInbox importe au catalogue les fichiers reçus (état « uploaded ») d'un
// agent par le chemin d'import normal : copie dans le magasin, vérification
// d'empreinte, passage de windows_only à linux sans doublon. La zone de
// réception est ensuite vidée de ce qui a été importé (les blobs restent).
func (r *Registry) importInbox(ctx context.Context, agentID string) {
	r.importMu.Lock()
	defer r.importMu.Unlock()
	a, err := r.GetAgent(ctx, agentID)
	if err != nil {
		return
	}
	rows, err := r.db().QueryContext(ctx, `SELECT DISTINCT root_label FROM agent_inventory WHERE agent_id=? AND state='uploaded'`, agentID)
	if err != nil {
		r.d.Log.Error("réception : lecture", "err", err)
		return
	}
	var labels []string
	for rows.Next() {
		var l string
		if rows.Scan(&l) == nil {
			labels = append(labels, l)
		}
	}
	rows.Close()
	for _, label := range labels {
		dir := filepath.Join(r.inbox, agentID, SafeLabel(label))
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		rep, err := r.d.Catalog.RunImport(ctx, catalog.ImportInput{Paths: []string{dir}, Collection: label,
			Source: "agent Windows " + a.Name}, nil, nil)
		if err != nil {
			r.d.Log.Error("import de réception", "agent", agentID, "dossier", label, "err", err)
			_, _ = r.db().ExecContext(ctx, `UPDATE agent_inventory SET state='error', note=?, updated_at=? WHERE agent_id=? AND root_label=? AND state='uploaded'`,
				"import au catalogue échoué : "+err.Error(), store.FormatTime(r.now()), agentID, label)
			continue
		}
		for _, f := range rep.Files {
			state, note := "imported", f.Note
			if f.Status == catalog.StatusError || f.Status == catalog.StatusRejected {
				state, note = "error", f.Status+" : "+f.Note
			}
			_, _ = r.db().ExecContext(ctx, `UPDATE agent_inventory SET state=?, note=?, source_file_id=COALESCE(NULLIF(?,''),source_file_id), updated_at=?
				WHERE agent_id=? AND root_label=? AND rel_path=? AND state='uploaded'`,
				state, note, f.SourceFileID, store.FormatTime(r.now()), agentID, label, f.RelPath)
		}
		// Vide la zone de réception (copie de travail seulement).
		_ = os.RemoveAll(dir)
	}
}
