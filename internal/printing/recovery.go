package printing

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

// Relance d'un fichier DÉJÀ déposé et diagnostic de connexion.

// ReprintInput est l'entrée de reprint_uploaded.
type ReprintInput struct {
	PrintJobID string `json:"print_job_id" jsonschema:"impression terminée, échouée ou annulée dont le fichier est déjà sur la carte de l'imprimante (ou restée PRINTING alors que l'imprimante est arrêtée)"`
	Quick      bool   `json:"quick,omitempty" jsonschema:"vérification rapide : présence et taille seulement, sans relire le fichier pour recalculer son sha256 (défaut : relecture complète)"`
	// Mêmes options de lancement que request_print_confirmation (affichées à l'humain et incluses dans ce qu'il approuve).
	BedLeveling   *bool `json:"bed_leveling,omitempty"`
	FlowCali      *bool `json:"flow_cali,omitempty"`
	VibrationCali *bool `json:"vibration_cali,omitempty"`
	Timelapse     *bool `json:"timelapse,omitempty"`
}

// ReprintOutput : la nouvelle confirmation, générée SANS redécouper ni renvoyer le fichier.
type ReprintOutput struct {
	SourcePrintJobID string              `json:"source_print_job_id"`
	PrintJobID       string              `json:"print_job_id" jsonschema:"impression à lancer (nouvelle impression, en attente de confirmation ; le source reste dans l'historique)"`
	Check            printer.RemoteCheck `json:"file_check"`
	Confirmation     *ConfirmationView   `json:"confirmation"`
	Reconciled       string              `json:"reconciled,omitempty" jsonschema:"si l'impression source était restée PRINTING : ce que l'imprimante a montré"`
	Note             string              `json:"note"`
}

// ReprintUploaded recrée une confirmation de lancement pour un fichier déjà déposé : il vérifie (FTPS) que le fichier est
// toujours sur la carte, avec la bonne taille et le bon sha256, que l'imprimante est au repos, puis crée une NOUVELLE
// impression (l'historique de l'échec reste intact) et sa confirmation. Rien n'est découpé ni renvoyé ; l'accord humain
// reste requis (page /confirm ou code de lancement) : le modèle ne lance rien.
func (s *Service) ReprintUploaded(ctx context.Context, in ReprintInput) (*ReprintOutput, error) {
	src, err := s.GetJob(ctx, in.PrintJobID)
	if err != nil {
		return nil, err
	}
	if src.Remote == nil || src.ArtifactSHA256 == "" {
		return nil, fmt.Errorf("%w : l'impression %s n'a pas de fichier déposé sur l'imprimante : passer par upload_print_artifact", ErrState, src.ID)
	}
	out := &ReprintOutput{SourcePrintJobID: src.ID}
	if src.State == contracts.PrintPrinting { // impression restée PRINTING alors que l'imprimante est arrêtée : réconcilier d'abord
		note, rerr := s.reconcileForReprint(ctx, src)
		if rerr != nil {
			return nil, rerr
		}
		out.Reconciled = note
		if src, err = s.GetJob(ctx, src.ID); err != nil {
			return nil, err
		}
	}
	switch src.State {
	case contracts.PrintCompleted, contracts.PrintFailed, contracts.PrintCancelled:
	default:
		return nil, fmt.Errorf("%w : l'impression %s est en %s ; reprint_uploaded relance le fichier d'une impression terminée, échouée ou annulée (pour une impression déposée mais jamais lancée : request_print_confirmation)", ErrState, src.ID, src.State)
	}
	// 1. l'imprimante doit être au repos et lançable (consigne précise sinon)
	if err := s.ensureLaunchable(ctx, src.PrinterID); err != nil {
		return nil, err
	}
	ad, _, err := s.adapter(ctx, src.PrinterID)
	if err != nil {
		return nil, err
	}
	// 2. le fichier est-il toujours là, identique ?
	rv, ok := ad.(printer.RemoteVerifier)
	if !ok {
		return nil, fmt.Errorf("%w : cet adaptateur ne sait pas vérifier un fichier déposé", ErrUnavailable)
	}
	vctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	chk, err := rv.VerifyRemote(vctx, *src.Remote, !in.Quick)
	if err != nil {
		return nil, fmt.Errorf("%w : vérification du fichier sur la carte impossible : %v", ErrUnavailable, err)
	}
	out.Check = chk
	if !chk.Exists || !chk.SizeOK || (chk.Hashed && !chk.SHA256OK) {
		return nil, fmt.Errorf("%w : %s (fichier %s) : redéposer avec upload_print_artifact (nouvelle clé d'idempotence) ; rien n'a été créé", ErrState, chk.Mismatch, src.Remote.Name)
	}
	// 3. confirmation : sur la même impression si elle n'a jamais été lancée, sinon sur une NOUVELLE impression
	target := src
	if src.State.Terminal() {
		nj := src
		nj.State = contracts.PrintUploaded
		nj.ConfirmationID, nj.StopConfirmID, nj.StartJobID, nj.UploadJobID, nj.SliceJobID = "", "", "", "", ""
		nj.Print, nj.LastError, nj.StoppedByUser, nj.Archived, nj.Trashed = nil, "", false, false, false
		nj.Events = nil
		if nj.Remote != nil {
			r := *nj.Remote
			r.Reused = true
			nj.Remote = &r
		}
		nj.event("reprint_from", contracts.PrintUploaded, contracts.PrintUploaded, "relance du fichier déjà déposé de "+src.ID+" ("+src.Remote.Name+") : "+chk.CheckedMsg, s.now())
		created, err := s.createJob(ctx, nj)
		if err != nil {
			return nil, err
		}
		target = created
		s.journal(ctx, src.ID, "reprint_created", "nouvelle impression "+created.ID+" créée pour relancer le même fichier (sans redécoupe ni renvoi)")
	}
	cv, err := s.RequestConfirmation(ctx, RequestConfirmationInput{PrintJobID: target.ID, BedLeveling: in.BedLeveling, FlowCali: in.FlowCali, VibrationCali: in.VibrationCali, Timelapse: in.Timelapse})
	if err != nil {
		if target.ID != src.ID { // la copie n'a servi à rien : on la ferme proprement
			_, _ = s.transition(ctx, target.ID, contracts.EvCancel, "relance abandonnée : "+err.Error(), nil)
		}
		return nil, err
	}
	_ = s.d.Store.Audit(ctx, actor, "print.reprint_requested", "print_job", target.ID, map[string]any{"source": src.ID, "file": src.Remote.Name, "hashed": chk.Hashed})
	out.PrintJobID, out.Confirmation = target.ID, cv
	out.Note = "Confirmation créée pour le fichier DÉJÀ déposé (" + chk.CheckedMsg + "). Rien n'est lancé : l'humain doit valider sur confirm_url (ou avec son code de lancement dans la vue) ; il atteste que le plateau est vide."
	return out, nil
}

// reconcileForReprint : une impression PRINTING que l'imprimante ne montre plus en cours est amenée à FAILED sur le champ
// (l'humain demande de relancer, le délai de grâce de la supervision n'a plus lieu d'être).
func (s *Service) reconcileForReprint(ctx context.Context, j Job) (string, error) {
	ad, _, err := s.adapter(ctx, j.PrinterID)
	if err != nil {
		return "", err
	}
	sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	st, err := ad.Status(sctx)
	cancel()
	if err != nil || !st.Connected || st.State == printer.StateUnknown {
		return "", fmt.Errorf("%w : imprimante injoignable ou état inconnu : impossible de dire si l'impression %s tourne encore", ErrUnavailable, j.ID)
	}
	m := matches(st, j.Remote.Name)
	active := st.State == printer.StatePreparing || st.State == printer.StatePrinting || st.State == printer.StatePaused
	if m && active {
		return "", fmt.Errorf("%w : l'imprimante montre CE fichier en cours (%s) : arrêter d'abord avec cancel_print (validé par l'humain)", ErrState, st.State)
	}
	if m && st.State == printer.StateFinished {
		s.observe(ctx, j.ID, st)
		return "l'imprimante rapporte FINISH : impression marquée terminée", nil
	}
	pg := copyProgress(j.Print)
	pg.PrinterState, pg.ObservedAt, pg.MatchesFile = string(st.State), s.now(), m
	if es := decodeErrors(st); len(es) > 0 {
		pg.ErrorDetails = es
	}
	why := fmt.Sprintf("constaté à la demande de relance : l'imprimante est en état %s et ne montre plus cette impression (fichier courant : %q)", st.State, st.SubtaskName)
	s.closeFromPrinter(ctx, j.ID, contracts.EvFail, &pg, why)
	return why, nil
}

// ---- diagnostic de connexion ----

// ConnectionDiagnosis est le résultat de diagnose_connection (lecture seule).
type ConnectionDiagnosis struct {
	PrinterID     string             `json:"printer_id"`
	PrinterName   string             `json:"printer_name"`
	Host          string             `json:"host"`
	Report        printer.DiagReport `json:"report"`
	Session       *printer.ConnInfo  `json:"mqtt_session,omitempty" jsonschema:"état de la session MQTT de bambud (dernière connexion, erreurs, prochain essai) ; absent si aucune session n'a jamais été ouverte depuis le démarrage"`
	LastConnected time.Time          `json:"last_connected,omitzero" jsonschema:"dernière connexion MQTT réussie (depuis le démarrage de bambud)"`
	LastError     string             `json:"last_error,omitempty"`
	Summary       string             `json:"summary" jsonschema:"diagnostic en français : réseau, service ou imprimante"`
}

type linkRecord struct {
	ok      time.Time
	err     string
	errAt   time.Time
	hasConn bool
}

func (s *Service) noteLink(printerID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.links == nil {
		s.links = map[string]*linkRecord{}
	}
	r := s.links[printerID]
	if r == nil {
		r = &linkRecord{}
		s.links[printerID] = r
	}
	if err == nil {
		r.ok, r.err, r.hasConn = s.now(), "", true
		return
	}
	r.err, r.errAt = err.Error(), s.now()
}

// DiagnoseConnection sonde l'imprimante enregistrée (DNS, TCP sur 8883 / 990 / 6000, ARP) et conclut. Lecture seule.
func (s *Service) DiagnoseConnection(ctx context.Context, printerID string) (*ConnectionDiagnosis, error) {
	pid, err := s.ResolvePrinterID(ctx, printerID)
	if err != nil {
		return nil, err
	}
	rec, err := s.GetPrinterRecord(ctx, pid)
	if err != nil {
		return nil, err
	}
	var ci *printer.ConnInfo
	s.mu.Lock()
	if e := s.adapters[pid]; e != nil {
		if cr, ok := e.ad.(printer.ConnectionReporter); ok {
			v := cr.ConnInfo()
			ci = &v
		}
	}
	s.mu.Unlock()
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rep := printer.Diagnose(dctx, printer.DiagTarget{Host: rec.Host, MQTTPort: rec.MQTTPort, FTPPort: rec.FTPPort, CameraPort: s.diagCameraPort()}, ci)
	out := &ConnectionDiagnosis{PrinterID: pid, PrinterName: rec.Name, Host: rec.Host, Report: rep, Session: ci}
	s.mu.Lock()
	if l := s.links[pid]; l != nil {
		out.LastConnected, out.LastError = l.ok, l.err
	}
	s.mu.Unlock()
	if ci != nil {
		if !ci.LastConnected.IsZero() && ci.LastConnected.After(out.LastConnected) {
			out.LastConnected = ci.LastConnected
		}
		if ci.LastError != "" {
			out.LastError = ci.LastError
		}
	}
	// MQTT ouvert mais aucune session : tenter une connexion (lecture seule) pour que le diagnostic soit complet
	if ci == nil && len(rep.Ports) > 0 && rep.Ports[0].Open && s.secrets.HasAccessCode(pid) && rec.PinnedSHA256 != "" {
		if _, _, aerr := s.adapter(ctx, pid); aerr != nil {
			out.LastError = aerr.Error()
			rep.Layer, rep.Diagnosis = "service", "Le port MQTT répond mais la connexion de bambud échoue : "+aerr.Error()+" (code d'accès, empreinte du certificat ou client concurrent)."
			out.Report = rep
		} else {
			s.noteLink(pid, nil)
			out.LastConnected = s.now()
		}
	}
	var sb strings.Builder
	sb.WriteString(out.Report.Diagnosis)
	if !out.LastConnected.IsZero() {
		sb.WriteString(" Dernière connexion réussie : " + out.LastConnected.Local().Format("15:04:05 02/01") + ".")
	} else {
		sb.WriteString(" Aucune connexion réussie depuis le démarrage de bambud.")
	}
	if out.LastError != "" {
		sb.WriteString(" Dernière erreur : " + out.LastError + ".")
	}
	out.Summary = sb.String()
	return out, nil
}

func (s *Service) diagCameraPort() int {
	if v := s.d.CameraPort; v != 0 {
		return v
	}
	return 0
}

var _ = errors.New
var _ = strconv.Itoa
