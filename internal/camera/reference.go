package camera

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Image de référence « plateau vide » (retour d'usage FB1).
//
// Elle est prise automatiquement JUSTE APRÈS la confirmation humaine du lancement (transition vers START_REQUESTED) : à cet
// instant l'humain vient d'attester le plateau vide et la tête n'a pas encore bougé (chauffe, nettoyage, mise à niveau
// suivent, plusieurs minutes). Elle est rangée avec l'impression (photo de type « reference », donc relue après un
// redémarrage du serveur) et sert de référence à la détection visuelle de CETTE impression, y compris pour
// analyze_print_frame. Capturer une photo est une lecture : aucune règle de lancement ou d'arrêt n'est touchée.

// captureReference prend (si elle n'existe pas déjà) la référence de l'impression.
func (s *Service) captureReference(ctx context.Context, printerID, jobID string) error {
	if _, ok := s.jobReference(ctx, jobID); ok {
		return nil
	}
	p, err := s.Capture(ctx, printerID, "reference", jobID, 500*time.Millisecond)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.jobRefs[jobID] = refImage{at: p.TakenAt, jpeg: p.JPEG, job: true}
	s.mu.Unlock()
	return nil
}

// jobReference renvoie la référence de l'impression : en mémoire, sinon relue en base (photo « reference »).
func (s *Service) jobReference(ctx context.Context, jobID string) (refImage, bool) {
	s.mu.Lock()
	r, ok := s.jobRefs[jobID]
	s.mu.Unlock()
	if ok {
		return r, true
	}
	var sha, at string
	err := s.c.Store.DB().QueryRowContext(ctx, `SELECT sha256,taken_at FROM camera_photos WHERE print_job_id=? AND kind='reference' ORDER BY taken_at DESC LIMIT 1`, jobID).Scan(&sha, &at)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return refImage{}, false
	}
	f, err := s.c.Blobs.Open(sha)
	if err != nil {
		return refImage{}, false
	}
	defer f.Close()
	var b bytes.Buffer
	if _, err := b.ReadFrom(f); err != nil {
		return refImage{}, false
	}
	r = refImage{jpeg: b.Bytes(), job: true}
	r.at, _ = store.ParseTime(at)
	s.mu.Lock()
	s.jobRefs[jobID] = r
	s.mu.Unlock()
	return r, true
}

// analyzeAgainstReference : analyse ponctuelle d'une image contre la référence de l'impression (détecteur jetable : le
// détecteur de la surveillance n'est pas perturbé).
func (s *Service) analyzeAgainstReference(ctx context.Context, j JobInfo, jpg []byte, at time.Time) (Report, bool) {
	ref, ok := s.jobReference(ctx, j.ID)
	if !ok {
		return Report{}, false
	}
	ri, err := decodeJPEG(ref.jpeg)
	if err != nil {
		return Report{}, false
	}
	img, err := decodeJPEG(jpg)
	if err != nil {
		return Report{}, false
	}
	ps, _ := s.c.Backend.State(ctx, j.PrinterID)
	d := NewDetector(s.Settings(ctx).Detector)
	d.SetReference(ri)
	rep := d.Observe(img, Observation{At: at, Layer: ps.Layer, TotalLayers: ps.TotalLayers, Printing: ps.Printing, HMSCount: ps.HMSCount, PrintError: ps.PrintError})
	rep.Notes = append(rep.Notes, fmt.Sprintf("analyse ponctuelle contre l'image de référence prise après la confirmation (%s) ; la persistance sur plusieurs instantanés est évaluée par la surveillance périodique", ref.at.Format("15:04:05")))
	return rep, true
}
