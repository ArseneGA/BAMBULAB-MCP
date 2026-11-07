package filament

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printing"
)

// File d'attente d'impressions.
//
// Ce que la file FAIT : suivre l'ordre, attendre la découpe, déposer le fichier de l'élément de tête sur
// l'imprimante quand elle est libre, demander la confirmation (lien /confirm) et notifier « suivante prête,
// plateau à vider ».
// Ce que la file NE FAIT JAMAIS : lancer, confirmer ou approuver. Chaque démarrage exige l'accord frais d'un
// humain sur /confirm, pour CHAQUE impression ; terminer une impression ne démarre pas la suivante.

// Statuts affichés d'un élément de file.
const (
	QSlicing     = "slicing"           // découpe en cours (ou à faire)
	QNeedsAssign = "needs_assignments" // découpée mais l'affectation filament -> emplacement manque
	QWaiting     = "waiting"           // une impression précède dans la file / l'imprimante est occupée
	QUploading   = "uploading"         // fichier en cours d'envoi
	QReady       = "ready"             // fichier sur l'imprimante : attend l'accord humain sur /confirm
	QPrinting    = "printing"          // lancée (par un humain)
	QDone        = "done"              // terminée
	QFailed      = "failed"            // échec ou annulation
	QRemoved     = "removed"           // retirée de la file
)

type queueDoc struct {
	PrintJobID  string                        `json:"print_job_id"`
	PrinterID   string                        `json:"printer_id"`
	Plate       int                           `json:"plate,omitempty"`
	Assignments []printing.FilamentAssignment `json:"assignments,omitempty"`
	Note        string                        `json:"note,omitempty"`
	Notified    bool                          `json:"notified,omitempty"`
	ConfirmID   string                        `json:"confirmation_id,omitempty"`
	Finished    string                        `json:"finished,omitempty"` // done | failed | removed
	FinishNote  string                        `json:"finish_note,omitempty"`
	LastError   string                        `json:"last_error,omitempty"`
}

// QueueEntry est un élément de file tel que présenté.
type QueueEntry struct {
	ID           string                        `json:"id"`
	Position     int                           `json:"position" jsonschema:"rang dans la file (1 = prochaine) ; 0 pour un élément terminé"`
	PrintJobID   string                        `json:"print_job_id"`
	PrinterID    string                        `json:"printer_id"`
	FileName     string                        `json:"file_name,omitempty"`
	Plate        int                           `json:"plate,omitempty"`
	Assignments  []printing.FilamentAssignment `json:"assignments,omitempty"`
	Note         string                        `json:"note,omitempty"`
	State        string                        `json:"state" jsonschema:"slicing | needs_assignments | waiting | uploading | ready | printing | done | failed | removed"`
	JobState     string                        `json:"job_state,omitempty" jsonschema:"état du PrintJob sous-jacent"`
	Message      string                        `json:"message"`
	ConfirmURL   string                        `json:"confirm_url,omitempty" jsonschema:"page de confirmation que l'HUMAIN ouvre pour lancer cette impression (valable 10 min ; en redemander une avec le bouton ou request_print_confirmation)"`
	EstimatedMin float64                       `json:"estimated_minutes,omitempty"`
	WeightG      float64                       `json:"estimated_grams,omitempty"`
	AddedAt      time.Time                     `json:"added_at"`
}

// QueueInput : mise en file d'une impression.
type QueueInput struct {
	PrintJobID  string                        `json:"print_job_id" jsonschema:"impression créée par execute_headless_slice (ou adoptée par upload_print_artifact)"`
	PrinterID   string                        `json:"printer_id,omitempty" jsonschema:"imprimante (défaut : celle de l'impression)"`
	Plate       int                           `json:"plate,omitempty"`
	Assignments []printing.FilamentAssignment `json:"assignments,omitempty" jsonschema:"affectation filament -> emplacement utilisée pour l'envoi du fichier (par exemple celle de plan_colors)"`
	Note        string                        `json:"note,omitempty"`
}

func (s *Service) loadEntries(ctx context.Context, includeRemoved bool) ([]QueueEntry, []queueDoc, error) {
	q := `SELECT id, position, doc, removed_at, created_at FROM print_queue`
	if !includeRemoved {
		q += ` WHERE removed_at IS NULL`
	}
	q += ` ORDER BY removed_at IS NOT NULL, position, created_at`
	rows, err := s.d.Store.DB().QueryContext(ctx, q)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var es []QueueEntry
	var ds []queueDoc
	for rows.Next() {
		var id, doc, created string
		var pos int
		var removed sql.NullString
		if err := rows.Scan(&id, &pos, &doc, &removed, &created); err != nil {
			return nil, nil, err
		}
		var d queueDoc
		if json.Unmarshal([]byte(doc), &d) != nil {
			continue
		}
		e := QueueEntry{ID: id, Position: pos, PrintJobID: d.PrintJobID, PrinterID: d.PrinterID, Plate: d.Plate, Assignments: d.Assignments, Note: d.Note}
		e.AddedAt, _ = time.Parse(time.RFC3339Nano, created)
		if removed.Valid {
			e.Position = 0
		}
		es = append(es, e)
		ds = append(ds, d)
	}
	return es, ds, rows.Err()
}

// QueuePrint ajoute une impression à la file (en dernier). Rien n'est lancé.
func (s *Service) QueuePrint(ctx context.Context, in QueueInput) (*QueueEntry, error) {
	if s.d.Printing == nil {
		return nil, fmt.Errorf("%w : chaîne d'impression indisponible", ErrConflict)
	}
	if in.PrintJobID == "" {
		return nil, invalidf("print_job_id requis")
	}
	j, err := s.d.Printing.GetJob(ctx, in.PrintJobID)
	if err != nil {
		return nil, fmt.Errorf("%w : %v", ErrNotFound, err)
	}
	if j.State.Terminal() || j.State == contracts.PrintStartRequested || j.State == contracts.PrintPrinting || j.State == contracts.PrintStartUnknown {
		return nil, fmt.Errorf("%w : l'impression %s est en %s : on ne met en file que des impressions pas encore lancées", ErrConflict, j.ID, j.State)
	}
	pid := in.PrinterID
	if pid == "" {
		pid = j.PrinterID
	}
	if pid == "" {
		return nil, invalidf("printer_id requis (l'impression n'en a pas)")
	}
	if _, err := s.d.Printing.GetPrinterRecord(ctx, pid); err != nil {
		return nil, invalidf("imprimante %q inconnue", pid)
	}
	for _, a := range in.Assignments {
		if _, _, _, err := parseTray(a.Tray); err != nil {
			return nil, invalidf("assignments : %v", err)
		}
	}
	d := queueDoc{PrintJobID: j.ID, PrinterID: pid, Plate: in.Plate, Assignments: in.Assignments, Note: in.Note}
	id := contracts.NewID("qe")
	now := formatTime(s.now())
	err = s.d.Store.Tx(ctx, func(tx *sql.Tx) error {
		var max sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT MAX(position) FROM print_queue WHERE removed_at IS NULL`).Scan(&max); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO print_queue(id,print_job_id,position,doc,created_at,updated_at) VALUES(?,?,?,?,?,?)`,
			id, j.ID, max.Int64+1, mustJSON(d), now, now); err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return fmt.Errorf("%w : l'impression %s est déjà dans la file", ErrConflict, j.ID)
			}
			return err
		}
		return s.d.Store.AuditTx(ctx, tx, actor, "queue.add", "print_queue", id, map[string]any{"print_job": j.ID})
	})
	if err != nil {
		return nil, err
	}
	s.wg.Add(1)
	go func() { // préparation immédiate, hors de la requête
		defer s.wg.Done()
		bg, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_ = s.PrepareQueue(bg)
	}()
	return s.GetQueueEntry(ctx, id)
}

// GetQueueEntry lit un élément de file.
func (s *Service) GetQueueEntry(ctx context.Context, id string) (*QueueEntry, error) {
	es, _, err := s.listQueue(ctx, true)
	if err != nil {
		return nil, err
	}
	for i := range es {
		if es[i].ID == id {
			return &es[i], nil
		}
	}
	return nil, fmt.Errorf("%w : élément de file %q", ErrNotFound, id)
}

// ListQueue renvoie la file (éléments actifs par rang) ; includeFinished ajoute les 20 derniers terminés/retirés.
func (s *Service) ListQueue(ctx context.Context, includeFinished bool) ([]QueueEntry, error) {
	es, _, err := s.listQueue(ctx, includeFinished)
	if err != nil {
		return nil, err
	}
	if !includeFinished {
		out := es[:0:0]
		for _, e := range es {
			if e.State != QDone && e.State != QFailed && e.State != QRemoved {
				out = append(out, e)
			}
		}
		es = out
	}
	if es == nil {
		es = []QueueEntry{}
	}
	return es, nil
}

func (s *Service) listQueue(ctx context.Context, includeRemoved bool) ([]QueueEntry, []queueDoc, error) {
	es, ds, err := s.loadEntries(ctx, includeRemoved)
	if err != nil {
		return nil, nil, err
	}
	rank := 0
	headBlocked := map[string]bool{}
	for i := range es {
		e, d := &es[i], ds[i]
		j, jerr := s.d.Printing.GetJob(ctx, e.PrintJobID)
		if d.Finished != "" || jerr != nil {
			e.State, e.Message = d.Finished, d.FinishNote
			if jerr != nil && d.Finished == "" {
				e.State, e.Message = QFailed, "impression introuvable : "+jerr.Error()
			}
			if e.State == "" {
				e.State = QDone
			}
			if jerr == nil {
				e.JobState, e.FileName = string(j.State), j.Name
			}
			continue
		}
		rank++
		e.Position = rank
		e.JobState, e.FileName = string(j.State), j.Name
		if j.Plate > 0 && e.Plate == 0 {
			e.Plate = j.Plate
		}
		e.Message, e.State = s.describe(ctx, j, d, headBlocked[e.PrinterID], e)
		if e.State == QReady || e.State == QPrinting || e.State == QUploading || e.State == QNeedsAssign {
			headBlocked[e.PrinterID] = true
		}
		if e.State == QSlicing && headBlocked[e.PrinterID] {
			// découpe d'un élément qui attend derrière
			e.State = QWaiting
		}
		if p := plateUse(j); p != nil {
			e.EstimatedMin, e.WeightG = p.PredictedSeconds/60, p.WeightGrams
		}
		if e.State == QReady && d.ConfirmID != "" {
			if v, err := s.d.Printing.GetConfirmation(ctx, d.ConfirmID); err == nil && !v.Approved && s.now().Before(v.ExpiresAt) && v.State == contracts.PrintAwaitingConfirmation {
				e.ConfirmURL = v.ConfirmURL
			}
		}
	}
	return es, ds, nil
}

func (s *Service) describe(ctx context.Context, j printing.Job, d queueDoc, behindOthers bool, e *QueueEntry) (msg, state string) {
	switch j.State {
	case contracts.PrintDraft, contracts.PrintPrepared, contracts.PrintSlicing:
		return "Découpe en cours ou à faire (execute_headless_slice).", QSlicing
	case contracts.PrintValidated:
		if behindOthers {
			return "En attente : une impression précède dans la file.", QWaiting
		}
		if len(d.Assignments) == 0 && len(j.Assignments) == 0 {
			return "Découpée. Il manque l'affectation filament -> emplacement (plan_colors, puis remettre l'impression en file avec assignments).", QNeedsAssign
		}
		return "Découpée et validée ; le fichier sera envoyé sur l'imprimante quand elle sera libre.", QWaiting
	case contracts.PrintUploading:
		return "Fichier en cours d'envoi à l'imprimante.", QUploading
	case contracts.PrintUploaded, contracts.PrintAwaitingConfirmation:
		return "Fichier sur l'imprimante. Plateau à vider, puis l'humain ouvre le lien de confirmation pour lancer : rien ne démarre seul.", QReady
	case contracts.PrintStartRequested, contracts.PrintPrinting, contracts.PrintStartUnknown:
		return "Impression lancée.", QPrinting
	case contracts.PrintCompleted:
		return "Terminée.", QDone
	}
	return "Impression en " + string(j.State) + " : " + j.LastError, QFailed
}

// PrepareQueue avance la préparation de la file : dépôt du fichier de l'élément de tête quand l'imprimante est
// libre, demande de confirmation, notification, clôture des éléments terminés. Elle ne lance rien.
func (s *Service) PrepareQueue(ctx context.Context) error {
	s.prepMu.Lock()
	defer s.prepMu.Unlock()
	if s.d.Printing == nil {
		return nil
	}
	es, ds, err := s.loadEntries(ctx, false)
	if err != nil {
		return err
	}
	blocked := map[string]bool{}
	var firstErr error
	for i := range es {
		e, d := es[i], ds[i]
		j, jerr := s.d.Printing.GetJob(ctx, e.PrintJobID)
		if jerr != nil {
			s.finish(ctx, e.ID, d, QFailed, "impression introuvable")
			continue
		}
		switch j.State {
		case contracts.PrintCompleted:
			s.finish(ctx, e.ID, d, QDone, "Terminée.")
			continue
		case contracts.PrintFailed, contracts.PrintCancelled, contracts.PrintStartAbortedBeforeSend:
			note := fmt.Sprintf("Impression %s : %s", j.State, j.LastError)
			s.finish(ctx, e.ID, d, QFailed, note)
			s.publish(Event{Kind: "queue_item_failed", QueueID: e.ID, PrintJobID: j.ID, PrinterID: e.PrinterID, Message: "File d'attente : " + note})
			continue
		}
		if blocked[e.PrinterID] {
			continue
		}
		switch j.State {
		case contracts.PrintDraft, contracts.PrintPrepared, contracts.PrintSlicing:
			blocked[e.PrinterID] = true // l'ordre est respecté : rien ne passe devant
		case contracts.PrintValidated:
			blocked[e.PrinterID] = true
			as := d.Assignments
			if len(as) == 0 {
				as = j.Assignments
			}
			if len(as) == 0 {
				continue // needs_assignments
			}
			if !s.printerFree(ctx, e.PrinterID, j.ID) {
				continue
			}
			plate := d.Plate
			if plate == 0 {
				plate = j.Plate
			}
			if _, err := s.d.Printing.UploadArtifact(ctx, printing.UploadInput{PrintJobID: j.ID, PrinterID: e.PrinterID, Plate: plate, Assignments: as,
				IdempotencyKey: "queue-" + e.ID}); err != nil {
				d.LastError = err.Error()
				s.saveDoc(ctx, e.ID, d)
				if firstErr == nil && ctx.Err() == nil {
					firstErr = fmt.Errorf("envoi du fichier de %s : %w", j.ID, err)
				}
				if errors.Is(err, printing.ErrIncompatible) || errors.Is(err, printing.ErrInvalid) {
					s.finish(ctx, e.ID, d, QFailed, "envoi refusé : "+err.Error())
					s.publish(Event{Kind: "queue_item_failed", QueueID: e.ID, PrintJobID: j.ID, PrinterID: e.PrinterID, Message: "File d'attente : envoi refusé pour " + j.Name + " : " + err.Error()})
					blocked[e.PrinterID] = false
				}
				continue
			}
			if cur, err := s.d.Printing.GetJob(ctx, j.ID); err == nil {
				j = cur
			}
			if j.State == contracts.PrintAwaitingConfirmation || j.State == contracts.PrintUploaded {
				s.notifyReady(ctx, e, d, j)
			}
		case contracts.PrintUploading:
			blocked[e.PrinterID] = true
		case contracts.PrintUploaded, contracts.PrintAwaitingConfirmation:
			blocked[e.PrinterID] = true
			if !d.Notified && s.printerFree(ctx, e.PrinterID, j.ID) {
				s.notifyReady(ctx, e, d, j)
			}
		default: // START_REQUESTED, PRINTING, START_UNKNOWN
			blocked[e.PrinterID] = true
		}
	}
	return firstErr
}

// notifyReady demande la confirmation (aucune approbation) et publie « suivante prête, plateau à vider ».
func (s *Service) notifyReady(ctx context.Context, e QueueEntry, d queueDoc, j printing.Job) {
	if d.Notified {
		return
	}
	cv, err := s.d.Printing.RequestConfirmation(ctx, printing.RequestConfirmationInput{PrintJobID: j.ID})
	if err != nil {
		s.log.Warn("file : confirmation non demandée", "print_job", j.ID, "err", err)
		return
	}
	d.Notified, d.ConfirmID = true, cv.ConfirmationID
	s.saveDoc(ctx, e.ID, d)
	s.publish(Event{Kind: "queue_next_ready", QueueID: e.ID, PrintJobID: j.ID, PrinterID: e.PrinterID, ConfirmURL: cv.ConfirmURL,
		Message: fmt.Sprintf("Impression suivante prête : %s. Vider le plateau, puis ouvrir %s pour la lancer (rien ne démarre seul).", orStr(j.Name, j.ID), cv.ConfirmURL)})
}

// RenewConfirmation redemande un lien de confirmation pour un élément « prêt » (l'ancien a expiré). N'approuve rien.
func (s *Service) RenewConfirmation(ctx context.Context, queueID string) (*QueueEntry, error) {
	es, ds, err := s.loadEntries(ctx, false)
	if err != nil {
		return nil, err
	}
	for i := range es {
		if es[i].ID != queueID {
			continue
		}
		j, err := s.d.Printing.GetJob(ctx, es[i].PrintJobID)
		if err != nil {
			return nil, err
		}
		if j.State != contracts.PrintUploaded && j.State != contracts.PrintAwaitingConfirmation {
			return nil, fmt.Errorf("%w : l'impression %s est en %s : pas de confirmation à renouveler", ErrConflict, j.ID, j.State)
		}
		cv, err := s.d.Printing.RequestConfirmation(ctx, printing.RequestConfirmationInput{PrintJobID: j.ID})
		if err != nil {
			return nil, err
		}
		d := ds[i]
		d.Notified, d.ConfirmID = true, cv.ConfirmationID
		s.saveDoc(ctx, queueID, d)
		return s.GetQueueEntry(ctx, queueID)
	}
	return nil, fmt.Errorf("%w : élément de file %q", ErrNotFound, queueID)
}

// printerFree : l'imprimante est joignable, au repos (idle / finished) et aucune de nos impressions ne l'occupe.
func (s *Service) printerFree(ctx context.Context, printerID, exceptJob string) bool {
	st, err := s.d.Printing.GetPrinterStatus(ctx, printerID)
	if err != nil || st == nil || !st.Connected {
		return false
	}
	switch printer.State(st.State) {
	case printer.StateIdle, printer.StateFinished:
	default:
		return false
	}
	jobs, err := s.d.Printing.ListJobs(ctx, "", 200)
	if err != nil {
		return false
	}
	for _, o := range jobs {
		if o.ID == exceptJob || o.PrinterID != printerID {
			continue
		}
		switch o.State {
		case contracts.PrintStartRequested, contracts.PrintPrinting, contracts.PrintStartUnknown:
			return false
		}
	}
	return true
}

func (s *Service) saveDoc(ctx context.Context, id string, d queueDoc) {
	_, err := s.d.Store.DB().ExecContext(ctx, `UPDATE print_queue SET doc=?, updated_at=? WHERE id=?`, mustJSON(d), formatTime(s.now()), id)
	if err != nil {
		s.log.Warn("file : élément non enregistré", "queue", id, "err", err)
	}
}

func (s *Service) finish(ctx context.Context, id string, d queueDoc, kind, note string) {
	d.Finished, d.FinishNote = kind, note
	if kind == QDone || kind == QFailed || kind == QRemoved {
		_, err := s.d.Store.DB().ExecContext(ctx, `UPDATE print_queue SET doc=?, removed_at=?, updated_at=? WHERE id=?`, mustJSON(d), formatTime(s.now()), formatTime(s.now()), id)
		if err != nil {
			s.log.Warn("file : élément non clos", "queue", id, "err", err)
		}
	}
}

// RemoveFromQueue retire un élément de la file (bookkeeping seulement : ne supprime ni n'arrête rien).
func (s *Service) RemoveFromQueue(ctx context.Context, id string) (*QueueEntry, error) {
	s.prepMu.Lock()
	defer s.prepMu.Unlock()
	es, ds, err := s.loadEntries(ctx, false)
	if err != nil {
		return nil, err
	}
	for i := range es {
		if es[i].ID != id {
			continue
		}
		if j, err := s.d.Printing.GetJob(ctx, es[i].PrintJobID); err == nil {
			switch j.State {
			case contracts.PrintStartRequested, contracts.PrintPrinting, contracts.PrintStartUnknown:
				return nil, fmt.Errorf("%w : l'impression %s est lancée : la file ne l'arrête pas (cancel_print crée une demande d'arrêt que l'humain valide)", ErrConflict, j.ID)
			}
		}
		s.finish(ctx, id, ds[i], QRemoved, "Retirée de la file par l'utilisateur ; le fichier déjà envoyé reste sur l'imprimante.")
		_ = s.d.Store.Audit(ctx, actor, "queue.remove", "print_queue", id, map[string]any{"print_job": es[i].PrintJobID})
		return s.GetQueueEntry(ctx, id)
	}
	return nil, fmt.Errorf("%w : élément de file actif %q", ErrNotFound, id)
}

// ReorderQueue place les éléments donnés en tête de file dans cet ordre ; les autres suivent dans leur ordre actuel.
func (s *Service) ReorderQueue(ctx context.Context, order []string) ([]QueueEntry, error) {
	s.prepMu.Lock()
	defer s.prepMu.Unlock()
	es, _, err := s.loadEntries(ctx, false)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, e := range es {
		have[e.ID] = true
	}
	seen := map[string]bool{}
	var final []string
	for _, id := range order {
		if !have[id] {
			return nil, fmt.Errorf("%w : élément de file actif %q", ErrNotFound, id)
		}
		if seen[id] {
			return nil, invalidf("élément %s cité deux fois", id)
		}
		seen[id] = true
		final = append(final, id)
	}
	for _, e := range es {
		if !seen[e.ID] {
			final = append(final, e.ID)
		}
	}
	err = s.d.Store.Tx(ctx, func(tx *sql.Tx) error {
		for i, id := range final {
			if _, err := tx.ExecContext(ctx, `UPDATE print_queue SET position=?, updated_at=? WHERE id=?`, i+1, formatTime(s.now()), id); err != nil {
				return err
			}
		}
		return s.d.Store.AuditTx(ctx, tx, actor, "queue.reorder", "print_queue", "", slices.Clone(final))
	})
	if err != nil {
		return nil, err
	}
	return s.ListQueue(ctx, false)
}
