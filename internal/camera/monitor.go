package camera

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/notify"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Surveillance : pendant PRINTING, un instantané périodique est analysé par le détecteur local. Une alerte enregistre le
// rapport et la photo, notifie (photo + lien /stop). JAMAIS d'arrêt automatique : l'arrêt reste la page humaine /stop.

type monitor struct {
	cancel context.CancelFunc
	det    *Detector
	streak int
	lastAt time.Time // dernière alerte
}

// Run recherche les impressions en cours et lance une surveillance par impression, jusqu'à l'annulation de ctx.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(s.c.Scan)
	defer t.Stop()
	var lastPurge time.Time
	for {
		s.scan(ctx)
		if s.now().Sub(lastPurge) >= time.Hour {
			lastPurge = s.now()
			if _, err := s.PurgeOldPhotos(ctx); err != nil {
				s.log.Warn("purge des photos d'alerte impossible", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Service) scan(ctx context.Context) {
	detect := s.Settings(ctx).Enabled
	jobs, err := s.c.Backend.PrintingJobs(ctx)
	if err != nil {
		return
	}
	for _, j := range jobs {
		s.startProgress(ctx, j) // journal photo par paliers de 10 % (D8) : indépendant de la détection d'échec
		if !detect {
			continue
		}
		s.mu.Lock()
		_, running := s.mons[j.ID]
		if !running {
			mctx, cancel := context.WithCancel(ctx)
			m := &monitor{cancel: cancel, det: NewDetector(s.Settings(ctx).Detector)}
			s.mons[j.ID] = m
			s.wg.Add(1)
			go s.watchJob(mctx, j, m)
		}
		s.mu.Unlock()
	}
}

func (s *Service) watchJob(ctx context.Context, j JobInfo, m *monitor) {
	defer s.wg.Done()
	defer func() {
		s.mu.Lock()
		delete(s.mons, j.ID)
		s.mu.Unlock()
		m.cancel()
	}()
	for ctx.Err() == nil {
		st := s.Settings(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(st.IntervalSeconds * float64(time.Second))):
		}
		cur, err := s.c.Backend.Job(ctx, j.ID)
		if err != nil || cur.State != contracts.PrintPrinting {
			return
		}
		st = s.Settings(ctx)
		if !st.Enabled || contains(st.DisabledPrinters, j.PrinterID) {
			continue
		}
		m.det.SetConfig(st.Detector)
		s.analyzeOnce(ctx, cur, m, st)
	}
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// analyzeOnce prend un instantané, l'analyse et déclenche l'alerte éventuelle.
func (s *Service) analyzeOnce(ctx context.Context, j JobInfo, m *monitor, st Settings) {
	ps, err := s.c.Backend.State(ctx, j.PrinterID)
	if err != nil || !ps.Reachable {
		return
	}
	t, err := s.c.Backend.Target(ctx, j.PrinterID)
	if err != nil {
		return
	}
	fctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	f, err := s.frame(fctx, t, time.Duration((st.IntervalSeconds/2+1)*float64(time.Second)))
	cancel()
	if err != nil {
		s.log.Debug("surveillance : pas d'image", "print_job", j.ID, "err", err)
		return
	}
	img, err := decodeJPEG(f.JPEG)
	if err != nil {
		return
	}
	if !m.det.HasReference() {
		s.mu.Lock()
		ref, ok := s.refs[j.PrinterID]
		s.mu.Unlock()
		if jr, jok := s.jobReference(ctx, j.ID); jok { // la référence de CETTE impression prime sur la photo de la page de confirmation
			ref, ok = jr, true
		}
		switch {
		case ok && (s.now().Sub(ref.at) < 3*time.Hour || ref.job):
			if ri, err := decodeJPEG(ref.jpeg); err == nil {
				m.det.SetReference(ri) // photo prise à l'ouverture de /confirm : plateau vide attesté par l'humain
			}
		case ps.Layer <= 1:
			m.det.SetReference(img) // début d'impression : la pièce est encore (presque) invisible
		}
	}
	rep := m.det.Observe(img, Observation{At: s.now(), Layer: ps.Layer, TotalLayers: ps.TotalLayers, Printing: ps.Printing, HMSCount: ps.HMSCount, PrintError: ps.PrintError})
	s.mu.Lock()
	s.last[j.ID] = lastAnalysis{Report: rep, JPEG: f.JPEG, At: f.At}
	if rep.Alert {
		m.streak++
	} else {
		m.streak = 0
	}
	fire := m.streak >= st.Detector.Consecutive && (m.lastAt.IsZero() || s.now().Sub(m.lastAt) >= time.Duration(st.AlertCooldownMin)*time.Minute)
	if fire {
		m.lastAt = s.now()
	}
	s.mu.Unlock()
	if fire {
		s.raiseAlert(ctx, j, rep, f)
	}
}

// raiseAlert enregistre l'alerte (rapport + photo) et notifie. Elle ne ferme rien, n'arrête rien.
func (s *Service) raiseAlert(ctx context.Context, j JobInfo, rep Report, f Frame) {
	photo, err := s.store(ctx, j.PrinterID, "alert", j.ID, f)
	sha := ""
	if err == nil {
		sha = photo.SHA256
	}
	doc, _ := json.Marshal(rep)
	id := contracts.NewID("al")
	_, _ = s.c.Store.DB().ExecContext(ctx, `INSERT INTO camera_alerts(id,printer_id,print_job_id,at,score,level,report,photo_sha256) VALUES(?,?,?,?,?,?,?,?)`,
		id, j.PrinterID, j.ID, store.FormatTime(s.now()), rep.Score, rep.Level, string(doc), sha)
	_ = s.c.Store.Audit(ctx, "camera", "camera.failure_alert", "print_job", j.ID, map[string]any{"score": rep.Score, "alert": id})
	s.log.Warn("alerte de détection d'échec (aucun arrêt automatique)", "print_job", j.ID, "score", rep.Score)
	if s.c.Notifier == nil {
		return
	}
	stop, _ := s.c.Backend.StopURL(ctx, j.ID) // crée seulement la DEMANDE d'arrêt ; l'humain valide ou non sur /stop
	e := notify.Event{Kind: notify.EvDetectionAlert, PrinterID: j.PrinterID, JobID: j.ID, Link: stop,
		Title:    "Alerte : échec probable de " + firstNonEmpty(j.Name, j.ID),
		Message:  alertMessage(rep),
		DedupKey: "alert:" + j.ID + ":" + id}
	if photo != nil {
		e.Photo = photo.JPEG
		e.PhotoURL = s.c.Backend.BaseURL() + photo.URL
	}
	if e.Link == "" {
		e.Link = s.c.Backend.BaseURL() + "/impression"
	}
	ctx2, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s.c.Notifier.Emit(ctx2, e)
}

func alertMessage(rep Report) string {
	sig := append([]Signal(nil), rep.Signals...)
	sort.Slice(sig, func(i, k int) bool { return sig[i].Contribution > sig[k].Contribution })
	var b strings.Builder
	fmt.Fprintf(&b, "Score %.2f (seuil %.2f), couche %d. ", rep.Score, rep.Threshold, rep.Layer)
	for i, g := range sig {
		if g.Contribution <= 0 || i >= 3 {
			break
		}
		fmt.Fprintf(&b, "%s : %s. ", g.Name, g.Detail)
	}
	b.WriteString("Rien n'a été arrêté : regarde la photo, puis ouvre le lien pour décider d'arrêter (page de validation humaine).")
	return b.String()
}

// Analysis : la dernière image et le dernier rapport d'une imprimante (outil analyze_print_frame).
type Analysis struct {
	PrinterID  string    `json:"printer_id"`
	PrintJobID string    `json:"print_job_id,omitempty"`
	TakenAt    time.Time `json:"taken_at"`
	Photo      *Photo    `json:"photo,omitempty"`
	Report     *Report   `json:"detector_report,omitempty"`
	ReportAt   time.Time `json:"detector_report_at,omitzero"`
	Note       string    `json:"note"`
	JPEG       []byte    `json:"-"`
}

// AnalyzeLatest renvoie une image fraîche de la caméra (ou, à défaut, la dernière analysée) avec le dernier rapport du détecteur
// pour l'impression en cours de cette imprimante. L'image est destinée à être jugée par Claude lui-même.
func (s *Service) AnalyzeLatest(ctx context.Context, printerID string) (*Analysis, error) {
	jobs, _ := s.c.Backend.PrintingJobs(ctx)
	var job *JobInfo
	for i := range jobs {
		if jobs[i].PrinterID == printerID {
			job = &jobs[i]
		}
	}
	out := &Analysis{PrinterID: printerID}
	kind := "snapshot"
	jobID := ""
	if job != nil {
		out.PrintJobID, jobID, kind = job.ID, job.ID, "snapshot"
	}
	if !s.allow(printerID, "snapshot") {
		return nil, ErrRateLimited
	}
	p, err := s.Capture(ctx, printerID, kind, jobID, 2*time.Second)
	if err != nil {
		// repli : dernière image analysée de l'impression, si elle existe
		if job != nil {
			s.mu.Lock()
			la, ok := s.last[job.ID]
			s.mu.Unlock()
			if ok {
				out.TakenAt, out.JPEG = la.At, la.JPEG
				rep := la.Report
				out.Report, out.ReportAt = &rep, la.At
				out.Note = "Caméra injoignable maintenant (" + err.Error() + ") : voici la dernière image analysée."
				return out, nil
			}
		}
		return nil, err
	}
	out.Photo, out.TakenAt, out.JPEG = p, p.TakenAt, p.JPEG
	if job != nil {
		s.mu.Lock()
		la, ok := s.last[job.ID]
		s.mu.Unlock()
		if ok {
			rep := la.Report
			out.Report, out.ReportAt = &rep, la.At
		}
		// Pas encore d'analyse du détecteur, ou analyse sans référence alors qu'une référence de cette impression existe :
		// analyse ponctuelle de l'image fraîche contre la référence prise après la confirmation (FB1).
		if out.Report == nil || !out.Report.ReferenceSet {
			if rep, ok := s.analyzeAgainstReference(ctx, *job, p.JPEG, p.TakenAt); ok {
				out.Report, out.ReportAt = &rep, p.TakenAt
			}
		}
	}
	switch {
	case job == nil:
		out.Note = "Aucune impression en cours sur cette imprimante : image seule, pas d'analyse automatique."
	case out.Report == nil:
		out.Note = "Impression en cours, mais le détecteur n'a pas encore analysé d'image (premier instantané dans moins de l'intervalle configuré) et aucune image de référence du plateau vide n'a pu être prise après la confirmation. Juge l'image toi-même."
	case !out.Report.ReferenceSet:
		out.Note = "Aucune image de référence du plateau vide n'a pu être prise pour cette impression (caméra injoignable à la confirmation ?) : l'analyse visuelle automatique est limitée. Juge l'image toi-même : filaments en vrac (spaghetti), pièce décollée, panache autour de la buse."
	default:
		out.Note = "Juge toi-même l'image : cherche des filaments en vrac (spaghetti) hors de la pièce, une pièce décollée ou déplacée, un panache de filament autour de la buse. Le score du détecteur local et ses signaux sont donnés à titre indicatif. Tu ne peux pas arrêter l'impression : si elle est perdue, propose à l'utilisateur d'ouvrir la page d'arrêt (cancel_print renvoie le lien)."
	}
	return out, nil
}

// LastReport renvoie le dernier rapport du détecteur d'une impression (sans image).
func (s *Service) LastReport(jobID string) (Report, time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	la, ok := s.last[jobID]
	return la.Report, la.At, ok
}

// AttachEvents abonne le service aux fins d'impression : photo de fin rattachée à l'impression, mémoire libérée.
func (s *Service) AttachEvents(subscribe func(func(PrintEvent))) {
	subscribe(func(ev PrintEvent) {
		if ev.To == contracts.PrintStartRequested { // l'humain vient de confirmer : le plateau est vide, la tête n'a pas bougé
			go func() {
				defer func() { _ = recover() }()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := s.captureReference(ctx, ev.PrinterID, ev.JobID); err != nil {
					s.log.Warn("image de référence du plateau non prise (détection visuelle sans référence propre à l'impression)", "print_job", ev.JobID, "err", err)
				}
			}()
			return
		}
		if ev.To != contracts.PrintCompleted && ev.To != contracts.PrintFailed {
			return
		}
		completed := ev.To == contracts.PrintCompleted
		s.mu.Lock()
		delete(s.jobRefs, ev.JobID)
		s.mu.Unlock()
		go func() {
			defer func() { _ = recover() }()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, _, err := s.FinalPhoto(ctx, ev.PrinterID, ev.JobID)
			if err != nil {
				s.log.Debug("photo de fin non prise", "print_job", ev.JobID, "err", err)
			}
			if completed {
				s.finishProgress(ctx, ev.JobID, ev.PrinterID, err) // palier 100 % = photo de fin (D8)
			}
		}()
	})
}

// PrintEvent : la part des évènements d'impression utile à la caméra.
type PrintEvent struct {
	JobID, PrinterID string
	To               contracts.PrintState
}
