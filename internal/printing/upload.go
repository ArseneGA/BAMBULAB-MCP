package printing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
)

// UploadInput est l'entrée de upload_print_artifact.
type UploadInput struct {
	PrintJobID     string               `json:"print_job_id,omitempty" jsonschema:"impression VALIDATED (execute_headless_slice puis validate_print_artifact)"`
	ArtifactID     string               `json:"artifact_id,omitempty" jsonschema:"à défaut de print_job_id : empreinte sha256 d'un artefact du stockage"`
	PrinterID      string               `json:"printer_id"`
	IdempotencyKey string               `json:"idempotency_key" jsonschema:"obligatoire : un doublon de requête ne renvoie jamais le fichier deux fois"`
	Plate          int                  `json:"plate,omitempty" jsonschema:"plaque à imprimer (défaut 1)"`
	Assignments    []FilamentAssignment `json:"assignments" jsonschema:"obligatoire : pour chaque filament utilisé, l'emplacement chargé et la matière"`
}

// PromptDetails rassemble ce que l'humain doit voir avant de répondre.
type PromptDetails struct {
	File          string               `json:"file"`
	PrinterName   string               `json:"printer_name"`
	PrinterModel  string               `json:"printer_model"`
	SHA256        string               `json:"sha256"`
	Plate         int                  `json:"plate"`
	Materials     []FilamentAssignment `json:"materials"`
	EstimatedMin  float64              `json:"estimated_minutes,omitempty"`
	WeightG       float64              `json:"estimated_grams,omitempty"`
	Alerts        []string             `json:"alerts,omitempty" jsonschema:"alertes de matière (bobine qui risque de manquer) : à lire avant de lancer"`
	BedLeveling   bool                 `json:"bed_leveling" jsonschema:"mise à niveau automatique du plateau au démarrage (oui par défaut) ; fait partie de ce que l'humain approuve"`
	FlowCali      bool                 `json:"flow_cali" jsonschema:"calibration du débit au démarrage (oui par défaut) ; fait partie de ce que l'humain approuve"`
	VibrationCali bool                 `json:"vibration_cali" jsonschema:"calibration des vibrations au démarrage (oui par défaut) ; fait partie de ce que l'humain approuve"`
	Timelapse     bool                 `json:"timelapse" jsonschema:"timelapse enregistré pendant l'impression (non par défaut) ; fait partie de ce que l'humain approuve"`
	Adhesion      []string             `json:"adhesion_warnings,omitempty" jsonschema:"avertissements de risque de décollement (pièce élancée sur peu de surface, sans bordure) : non bloquants, à lire avant de lancer"`
	Overhang      []string             `json:"overhang_warnings,omitempty" jsonschema:"avertissement FORT : surplombs significatifs (> 200 mm² cumulés ou une zone > 100 mm²) alors que les supports sont désactivés ; non bloquant mais à lire et accepter avant de lancer"`
	Colors        []FilamentColor      `json:"colors,omitempty" jsonschema:"couleur de chaque filament d'après la bobine RÉELLEMENT chargée (inventaire, AMS, bobine externe déclarée) ; « inconnue » sinon, jamais la couleur par défaut du profil"`
	Reminder      string               `json:"reminder"`
}

// UploadOutput : état du dépôt et question à poser à l'utilisateur.
type UploadOutput struct {
	PrintJobID string               `json:"print_job_id"`
	State      contracts.PrintState `json:"state"`
	QueueJobID string               `json:"queue_job_id"`
	Done       bool                 `json:"done"`
	Replayed   bool                 `json:"replayed" jsonschema:"vrai si la clé d'idempotence avait déjà servi : rien n'a été renvoyé"`
	Question   string               `json:"question,omitempty" jsonschema:"message exact à poser à l'utilisateur ; ne lance RIEN"`
	Details    *PromptDetails       `json:"details,omitempty"`
	Note       string               `json:"note"`
	Job        Job                  `json:"job"`
	JobID      string               `json:"job_id,omitempty" jsonschema:"travail de file à suivre avec get_job_status quand done est faux"`
	Progress   *jobs.Progress       `json:"progress,omitempty"`
}

func (s *Service) promptFor(j Job) (string, *PromptDetails) {
	rec, _ := s.GetPrinterRecord(context.Background(), j.PrinterID)
	model, _ := s.GetPrinterModel(context.Background(), rec.ModelID)
	short := shortName(model.Name)
	if short == "" {
		short = rec.Name
	}
	d := &PromptDetails{Colors: s.colorsFor(j), File: j.Name, PrinterName: rec.Name, PrinterModel: model.Name, SHA256: j.ArtifactSHA256, Plate: j.Plate, Materials: j.Assignments, BedLeveling: !j.NoBedLeveling, FlowCali: !j.NoFlowCali, VibrationCali: !j.NoVibrationCali, Timelapse: j.Timelapse, Adhesion: j.adhesionNotes(), Overhang: j.overhangNotes(),
		Reminder: "Vérifie que le plateau est vide et dégagé : l'imprimante ne peut pas le savoir. Le dépôt du fichier n'a déclenché ni chauffe ni mouvement."}
	if j.Validation != nil {
		for _, w := range j.Validation.Warnings {
			if strings.HasPrefix(w, "filament_") {
				d.Alerts = append(d.Alerts, w)
			}
		}
	}
	if j.Summary != nil {
		for _, p := range j.Summary.Plates {
			if p.Index == j.Plate {
				d.EstimatedMin, d.WeightG = p.PredictedSeconds/60, p.WeightGrams
			}
		}
	}
	return fmt.Sprintf("Le fichier %s est envoyé sur la %s. Veux-tu lancer cette impression ?", j.Name, short), d
}

func plateOrOne(p int) int {
	if p == 0 {
		return 1
	}
	return p
}

// uploadRequestHash résume ce qui définit une demande de dépôt (imprimante, plateau, affectations) :
// rejouer la même clé avec autre chose est refusé.
func uploadRequestHash(printerID string, plate int, a []FilamentAssignment) string {
	b, _ := json.Marshal(struct {
		P  string
		Pl int
		A  []FilamentAssignment
	}{printerID, plate, a})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

func sameUploadParams(raw json.RawMessage, printJobID string, in UploadInput, plate int) bool {
	var p struct {
		ID  string `json:"print_job_id"`
		Req string `json:"request"`
	}
	return json.Unmarshal(raw, &p) == nil && p.ID == printJobID && p.Req == uploadRequestHash(in.PrinterID, plate, in.Assignments)
}

// findByUploadKey retrouve l'impression dont le dépôt a été lancé avec cette clé d'idempotence.
func (s *Service) findByUploadKey(ctx context.Context, sha, printerID, key string) (Job, bool) {
	all, err := s.ListJobs(ctx, "", 500)
	if err != nil {
		return Job{}, false
	}
	for _, j := range all {
		if j.ArtifactSHA256 != sha || j.PrinterID != printerID || j.UploadJobID == "" {
			continue
		}
		if q, err := s.d.Queue.Get(ctx, j.UploadJobID); err == nil && q.IdempotencyKey == idemKey(j.ID, "upload", key) {
			return j, true
		}
	}
	return Job{}, false
}

func (s *Service) waitQueue(ctx context.Context, id string, d time.Duration) (contracts.Job, bool) {
	deadline := time.Now().Add(d)
	sink := progressSinkOf(ctx)
	var lastSink time.Time
	for {
		q, err := s.d.Queue.Get(ctx, id)
		if err == nil && q.State.Terminal() {
			return q, true
		}
		if sink != nil && err == nil && time.Since(lastSink) >= time.Second { // notifications/progress du client MCP
			lastSink = time.Now()
			sink(s.d.Queue.ProgressOf(ctx, q))
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return q, false
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// UploadArtifact dépose l'artefact validé sur l'imprimante (aucun lancement).
func (s *Service) UploadArtifact(ctx context.Context, in UploadInput) (*UploadOutput, error) {
	key := strings.TrimSpace(in.IdempotencyKey)
	if len(key) < 8 || len(key) > 128 {
		return nil, invalidf("idempotency_key obligatoire (8 à 128 caractères)")
	}
	if in.PrinterID == "" {
		return nil, invalidf("printer_id requis")
	}
	var j Job
	var err error
	switch {
	case in.PrintJobID != "":
		j, err = s.GetJob(ctx, in.PrintJobID)
	case in.ArtifactID != "":
		if !s.d.Blobs.Has(in.ArtifactID) {
			return nil, fmt.Errorf("%w : artefact %q", ErrNotFound, in.ArtifactID)
		}
		// Rejeu par artefact : la même clé d'idempotence désigne l'impression déjà créée pour ce dépôt.
		if prev, ok := s.findByUploadKey(ctx, in.ArtifactID, in.PrinterID, key); ok {
			j = prev
		} else {
			j, err = s.adoptArtifact(ctx, in.ArtifactID, in.PrinterID)
		}
	default:
		return nil, invalidf("print_job_id ou artifact_id requis")
	}
	if err != nil {
		return nil, err
	}
	// Rejeu : même clé que le dépôt déjà lancé -> aucun nouveau travail.
	if j.UploadJobID != "" && j.State != contracts.PrintValidated {
		if q, err := s.d.Queue.Get(ctx, j.UploadJobID); err == nil && q.IdempotencyKey == idemKey(j.ID, "upload", key) {
			if !sameUploadParams(q.Params, j.ID, in, plateOrOne(in.Plate)) {
				return nil, fmt.Errorf("%w : upload_print_artifact avec cette clé a déjà été demandé avec une autre imprimante, un autre plateau ou d'autres matières", ErrReplayParams)
			}
			q, done := s.waitQueue(ctx, q.ID, 100*time.Millisecond)
			_ = q
			cur, _ := s.GetJob(ctx, j.ID)
			out := &UploadOutput{PrintJobID: cur.ID, State: cur.State, QueueJobID: j.UploadJobID, Done: done, Replayed: true, Job: cur,
				Note: "Clé d'idempotence déjà utilisée : le fichier n'a pas été renvoyé."}
			if cur.State == contracts.PrintAwaitingConfirmation || cur.State == contracts.PrintUploaded {
				out.Question, out.Details = s.promptFor(cur)
			}
			return out, nil
		}
	}
	if j.State != contracts.PrintValidated {
		return nil, fmt.Errorf("%w : l'impression %s est en %s ; le dépôt exige VALIDATED", ErrState, j.ID, j.State)
	}
	if len(in.Assignments) == 0 {
		return nil, invalidf("assignments obligatoire : pour chaque filament utilisé, l'emplacement chargé (ams1:1..ams4:4, ext) et la matière")
	}
	plate := in.Plate
	if plate == 0 {
		plate = 1
	}
	vo, err := s.ValidateArtifact(ctx, ValidateInput{PrintJobID: j.ID, PrinterID: in.PrinterID, Plate: plate, Assignments: in.Assignments})
	if err != nil {
		return nil, err
	}
	if !vo.Report.OK {
		return nil, fmt.Errorf("%w : %s", ErrIncompatible, strings.Join(vo.Report.Blocking, " ; "))
	}
	// L'imprimante doit être configurée et joignable AVANT de changer d'état.
	if _, _, err := s.adapter(ctx, in.PrinterID); err != nil {
		return nil, err
	}
	mapping, useAMS := buildAMS(in.Assignments)
	loaded := s.loadedSpools(in.PrinterID) // lecture hors du verrou des impressions
	if _, err := s.update(ctx, j.ID, func(x *Job) error {
		if x.State != contracts.PrintValidated {
			return fmt.Errorf("%w : %s", ErrState, x.State)
		}
		x.Assignments, x.AMSMapping, x.UseAMS, x.Plate, x.PrinterID = in.Assignments, mapping, useAMS, plate, in.PrinterID
		applyRealColors(x, loaded)
		return nil
	}); err != nil {
		return nil, err
	}
	params, _ := json.Marshal(map[string]string{"print_job_id": j.ID, "request": uploadRequestHash(in.PrinterID, plate, in.Assignments)})
	qj, created, err := s.d.Queue.Enqueue(ctx, jobs.EnqueueParams{Kind: JobUpload, Class: contracts.ClassLight, Params: params, IdempotencyKey: idemKey(j.ID, "upload", key)})
	if err != nil {
		return nil, err
	}
	if !created && !sameUploadParams(qj.Params, j.ID, in, plate) {
		return nil, fmt.Errorf("%w : upload_print_artifact avec cette clé a déjà été demandé avec une autre imprimante, un autre plateau ou d'autres matières", ErrReplayParams)
	}
	if created {
		if _, err := s.update(ctx, j.ID, func(x *Job) error { x.UploadJobID = qj.ID; return nil }); err != nil {
			return nil, err
		}
	}
	_, done := s.waitQueue(ctx, qj.ID, s.syncWait())
	cur, _ := s.GetJob(ctx, j.ID)
	out := &UploadOutput{PrintJobID: cur.ID, State: cur.State, QueueJobID: qj.ID, Done: done, Replayed: !created, Job: cur}
	switch cur.State {
	case contracts.PrintAwaitingConfirmation:
		out.Question, out.Details = s.promptFor(cur)
		out.Note = "Fichier déposé sur l'imprimante. RIEN n'est lancé. Pose exactement la question à l'utilisateur ; s'il répond oui, appelle request_print_confirmation puis fais-lui ouvrir le lien : seul son clic sur cette page autorise le lancement."
	case contracts.PrintFailed:
		out.Note = "Dépôt en échec : " + cur.LastError
	default:
		out.Note = "Le dépôt continue en arrière-plan (ne PAS considérer l'appel comme échoué) : suivre avec get_job_status(job_id = queue_job_id) ou get_print_status jusqu'à AWAITING_PRINT_CONFIRMATION."
		out.Progress = s.progressOfQueue(ctx, qj.ID)
	}
	return out, nil
}

// uploadHandler : VALIDATED -> UPLOADING -> UPLOADED -> AWAITING_PRINT_CONFIRMATION.
func (s *Service) uploadHandler(ctx context.Context, jc *jobs.JobCtx) (json.RawMessage, error) {
	var p struct {
		ID string `json:"print_job_id"`
	}
	if err := json.Unmarshal(jc.Job.Params, &p); err != nil {
		return nil, &jobs.HandlerError{Code: "bad_params", Err: err}
	}
	bg := context.Background()
	j, err := s.GetJob(bg, p.ID)
	if err != nil {
		return nil, &jobs.HandlerError{Code: "not_found", Err: err}
	}
	switch j.State {
	case contracts.PrintValidated:
		if j, err = s.transition(bg, j.ID, contracts.EvStartUpload, "dépôt démarré", nil); err != nil {
			return nil, &jobs.HandlerError{Code: "state", Err: err}
		}
	case contracts.PrintUploading: // reprise : le dépôt est idempotent (nom déterministe + taille)
	default:
		return nil, &jobs.HandlerError{Code: "state", Err: fmt.Errorf("%w : %s", ErrState, j.State)}
	}
	fail := func(code string, err error) (json.RawMessage, error) {
		if !isCtx(err) {
			s.failJob(bg, j.ID, code, err)
		}
		return nil, &jobs.HandlerError{Code: code, Err: err}
	}
	ad, rec, err := s.adapter(ctx, j.PrinterID)
	if err != nil {
		return fail("printer_unavailable", err)
	}
	f, err := s.d.Blobs.Open(j.ArtifactSHA256)
	if err != nil {
		return fail("artifact_unreadable", err)
	}
	defer f.Close()
	label := "Dépôt sur l'imprimante"
	if m, merr := s.GetPrinterModel(bg, rec.ModelID); merr == nil && shortName(m.Name) != "" {
		label = "Dépôt sur la " + shortName(m.Name) // ex. « Dépôt sur la P1S »
	}
	jc.Progress(0.05, "connexion FTPS à l'imprimante")
	jc.SetDetail(jobs.Detail{Phase: "connexion FTPS à l'imprimante", Label: label, BytesTotal: j.ArtifactSize})
	cr := &countingReader{r: f, total: j.ArtifactSize, jc: jc, label: label}
	ref, err := ad.Upload(ctx, cr, j.Name, j.ArtifactSize, j.ArtifactSHA256)
	jobs.ForgetDetail(jc.Job.ID)
	if err != nil {
		return fail("upload_failed", err)
	}
	if _, err = s.transition(bg, j.ID, contracts.EvUploadOK, fmt.Sprintf("déposé sous %s (réutilisé=%v)", ref.Name, ref.Reused), func(x *Job) error {
		r := ref
		x.Remote = &r
		return nil
	}); err != nil {
		return nil, &jobs.HandlerError{Code: "state", Err: err}
	}
	if _, err = s.transition(bg, j.ID, contracts.EvRequestConfirmation, "en attente de la décision de l'utilisateur", nil); err != nil {
		return nil, &jobs.HandlerError{Code: "state", Err: err}
	}
	_ = s.d.Store.Audit(bg, actor, "print.uploaded", "print_job", j.ID, map[string]any{"remote": ref.Name, "reused": ref.Reused})
	out, _ := json.Marshal(map[string]any{"print_job_id": j.ID, "remote": ref.Name, "reused": ref.Reused})
	return out, nil
}

var _ = errors.New
