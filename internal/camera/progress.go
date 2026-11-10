package camera

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"strings"
	"sync"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Journal photo à chaque 10 % de progression (G9 D8, étend N19).
//
// Règles : (a) une seule tentative par palier (10, 20, … 90 pendant l'impression ; le palier 100 % est la photo de fin
// d'impression existante, kind « final », simplement étiquetée percent=100 : jamais de doublon) ; (b) un palier sauté
// (38 → 52 %) ne produit que la photo du palier atteint le plus récent (50), jamais de rafale ; (c) un échec de capture
// n'est JAMAIS bloquant : le palier est consigné « unavailable » avec sa raison et l'impression continue ; (d) LECTURE de
// la caméra seulement : aucune commande n'est envoyée à l'imprimante ; (e) photos redimensionnées (largeur bornée,
// JPEG qualité réglable) puis rangées en blobs sha256 (dédoublonnage du magasin).

// Statuts d'un palier.
const (
	ProgressOK          = "ok"
	ProgressUnavailable = "unavailable"
)

// ProgressPhoto : un palier du journal photo d'une impression (lecture pour PJ5/PJ10).
type ProgressPhoto struct {
	JobID   string    `json:"print_job_id"`
	Percent int       `json:"percent" jsonschema:"palier de progression, 10 à 100"`
	Status  string    `json:"status" jsonschema:"ok | unavailable"`
	Reason  string    `json:"reason,omitempty" jsonschema:"raison quand le palier est indisponible (photo non disponible)"`
	Kind    string    `json:"kind,omitempty" jsonschema:"progress, ou final pour le palier 100 %"`
	SHA256  string    `json:"sha256,omitempty"`
	Size    int       `json:"size_bytes,omitempty"`
	TakenAt time.Time `json:"taken_at"`
	URL     string    `json:"url,omitempty" jsonschema:"route qui sert l'image ; vide si le palier est indisponible"`
}

// ProgressPhotos renvoie les paliers consignés d'une impression, du plus petit au plus grand pourcentage (au plus une ligne
// par palier). Un palier absent de la liste n'a pas été atteint (ou a été sauté) ; un palier « unavailable » doit être
// affiché « photo non disponible » avec sa raison.
func (s *Service) ProgressPhotos(ctx context.Context, jobID string) ([]ProgressPhoto, error) {
	rows, err := s.c.Store.DB().QueryContext(ctx, `SELECT percent,status,reason,kind,sha256,size,taken_at FROM camera_photos
		WHERE print_job_id=? AND percent IS NOT NULL ORDER BY percent, taken_at, id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProgressPhoto{}
	for rows.Next() {
		var p ProgressPhoto
		var at string
		if err := rows.Scan(&p.Percent, &p.Status, &p.Reason, &p.Kind, &p.SHA256, &p.Size, &at); err != nil {
			return nil, err
		}
		p.JobID = jobID
		p.TakenAt, _ = store.ParseTime(at)
		if p.Status == ProgressOK && p.SHA256 != "" {
			p.URL = "/api/camera/photos/" + p.SHA256
		}
		if n := len(out); n > 0 && out[n-1].Percent == p.Percent {
			out[n-1] = p // une seule ligne par palier : la plus récente
			continue
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// startProgress lance (une seule fois par impression) la boucle du journal photo.
func (s *Service) startProgress(ctx context.Context, j JobInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.progs[j.ID]; ok {
		return
	}
	pctx, cancel := context.WithCancel(ctx)
	s.progs[j.ID] = cancel
	s.wg.Add(1)
	go s.progressLoop(pctx, j, cancel)
}

func (s *Service) progressLoop(ctx context.Context, j JobInfo, cancel context.CancelFunc) {
	defer s.wg.Done()
	defer func() {
		_ = recover() // le journal photo ne doit jamais faire tomber le service
		s.mu.Lock()
		delete(s.progs, j.ID)
		s.mu.Unlock()
		cancel()
	}()
	last := s.lastRecordedPalier(ctx, j.ID) // reprise après redémarrage : jamais de second essai pour un palier déjà consigné
	for ctx.Err() == nil {
		cur, err := s.c.Backend.Job(ctx, j.ID)
		if err == nil && cur.State != contracts.PrintPrinting {
			return
		}
		if st := s.Settings(ctx); err == nil && st.ProgressPhotos {
			if ps, perr := s.c.Backend.State(ctx, j.PrinterID); perr == nil && ps.Reachable && ps.Printing {
				p := ps.Percent / 10 * 10
				if p > 90 {
					p = 90 // 100 % : photo de fin d'impression (finishProgress)
				}
				if p >= 10 && p > last {
					last = p
					s.captureProgress(ctx, j, p, st)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.c.Scan):
		}
	}
}

func (s *Service) lastRecordedPalier(ctx context.Context, jobID string) int {
	var n int
	_ = s.c.Store.DB().QueryRowContext(ctx, `SELECT COALESCE(MAX(percent),0) FROM camera_photos WHERE print_job_id=? AND percent IS NOT NULL`, jobID).Scan(&n)
	return n
}

func (s *Service) paliers(ctx context.Context, jobID string, pct int) int {
	var n int
	_ = s.c.Store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM camera_photos WHERE print_job_id=? AND percent=?`, jobID, pct).Scan(&n)
	return n
}

// captureProgress fait UNE tentative de photo pour le palier pct. Aucune erreur ne remonte : un échec est consigné.
func (s *Service) captureProgress(ctx context.Context, j JobInfo, pct int, st Settings) {
	if s.paliers(ctx, j.ID, pct) > 0 {
		return // au plus une photo par palier
	}
	var reason string
	func() {
		defer func() {
			if r := recover(); r != nil {
				reason = fmt.Sprintf("erreur interne de capture (%v)", r)
			}
		}()
		cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		jpg, at, err := s.progressFrame(cctx, j.PrinterID, st)
		if err != nil {
			reason = progressReason(err)
			return
		}
		sum := sha256.Sum256(jpg)
		sha := hex.EncodeToString(sum[:])
		if _, _, err := s.c.Blobs.Put(bytes.NewReader(jpg), sha); err != nil {
			reason = "stockage de la photo impossible"
			return
		}
		if err := s.insertProgress(ctx, j.PrinterID, j.ID, "progress", pct, ProgressOK, "", sha, len(jpg), at); err != nil {
			reason = "enregistrement de la photo impossible"
		}
	}()
	if reason != "" {
		s.log.Debug("journal photo : palier indisponible", "print_job", j.ID, "percent", pct, "reason", reason)
		if err := s.insertProgress(ctx, j.PrinterID, j.ID, "progress", pct, ProgressUnavailable, reason, "", 0, s.now()); err != nil {
			s.log.Warn("journal photo : palier non consigné", "print_job", j.ID, "percent", pct, "err", err)
		}
	}
}

func progressReason(err error) string {
	msg := strings.TrimSpace(err.Error())
	switch {
	case errors.Is(err, ErrNotFound):
		msg = "caméra ou imprimante introuvable"
	case msg == "":
		msg = "erreur inconnue"
	}
	if r := []rune(msg); len(r) > 200 {
		msg = string(r[:200])
	}
	return msg
}

// progressFrame lit une image de la caméra (lecture seule), la redimensionne et la ré-encode.
func (s *Service) progressFrame(ctx context.Context, printerID string, st Settings) ([]byte, time.Time, error) {
	t, err := s.c.Backend.Target(ctx, printerID)
	if err != nil {
		return nil, time.Time{}, err
	}
	s.mu.Lock()
	mu := s.snap[printerID]
	if mu == nil {
		mu = &sync.Mutex{}
		s.snap[printerID] = mu
	}
	s.mu.Unlock()
	mu.Lock()
	defer mu.Unlock()
	f, err := s.frame(ctx, t, 3*time.Second)
	if err != nil {
		return nil, time.Time{}, err
	}
	if len(f.JPEG) == 0 {
		return nil, time.Time{}, errors.New("image vide")
	}
	jpg, err := ResizeJPEG(f.JPEG, st.ProgressPhotoWidth, st.ProgressPhotoQuality)
	if err != nil {
		return nil, time.Time{}, err
	}
	if len(jpg) == 0 {
		return nil, time.Time{}, errors.New("image vide")
	}
	return jpg, f.At, nil
}

func (s *Service) insertProgress(ctx context.Context, printerID, jobID, kind string, pct int, status, reason, sha string, size int, at time.Time) error {
	_, err := s.c.Store.DB().ExecContext(ctx, `INSERT INTO camera_photos(id,printer_id,print_job_id,kind,sha256,size,taken_at,percent,status,reason) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		contracts.NewID("ph"), printerID, jobID, kind, sha, size, store.FormatTime(at), pct, status, reason)
	return err
}

// finishProgress rattache le palier 100 % à la photo de fin d'impression (kind « final », déjà prise à la fin d'une
// impression réussie) : pas de seconde photo. Si la photo de fin a échoué, le palier 100 % est consigné « unavailable ».
func (s *Service) finishProgress(ctx context.Context, jobID, printerID string, finalErr error) {
	defer func() { _ = recover() }()
	if !s.Settings(ctx).ProgressPhotos || s.paliers(ctx, jobID, 100) > 0 {
		return
	}
	if finalErr == nil {
		res, err := s.c.Store.DB().ExecContext(ctx, `UPDATE camera_photos SET percent=100 WHERE id=(SELECT id FROM camera_photos
			WHERE print_job_id=? AND kind='final' AND status='ok' ORDER BY taken_at DESC LIMIT 1)`, jobID)
		if err == nil {
			if n, _ := res.RowsAffected(); n > 0 {
				return
			}
		}
		finalErr = errors.New("photo de fin introuvable")
	}
	if err := s.insertProgress(ctx, printerID, jobID, "progress", 100, ProgressUnavailable, progressReason(finalErr), "", 0, s.now()); err != nil {
		s.log.Warn("journal photo : palier 100 % non consigné", "print_job", jobID, "err", err)
	}
}

// ResizeJPEG réduit une image JPEG à maxWidth pixels de large au plus (jamais d'agrandissement) par moyenne de surface et la
// ré-encode en JPEG (qualité 1-100). Les dimensions sont bornées avant tout décodage (MaxJPEGSide).
func ResizeJPEG(b []byte, maxWidth, quality int) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("image vide")
	}
	src, err := decodeJPEG(b)
	if err != nil {
		return nil, err
	}
	if maxWidth < 16 {
		maxWidth = 1280
	}
	if quality < 1 || quality > 100 {
		quality = 80
	}
	bd := src.Bounds()
	w, h := bd.Dx(), bd.Dy()
	dst := src
	if w > maxWidth {
		nw := maxWidth
		nh := h * nw / w
		if nh < 1 {
			nh = 1
		}
		rgba := image.NewRGBA(image.Rect(0, 0, nw, nh))
		for y := 0; y < nh; y++ {
			y0, y1 := y*h/nh, (y+1)*h/nh
			if y1 <= y0 {
				y1 = y0 + 1
			}
			for x := 0; x < nw; x++ {
				x0, x1 := x*w/nw, (x+1)*w/nw
				if x1 <= x0 {
					x1 = x0 + 1
				}
				var r, g, bl, n uint64
				for yy := y0; yy < y1; yy++ {
					for xx := x0; xx < x1; xx++ {
						cr, cg, cb, _ := src.At(bd.Min.X+xx, bd.Min.Y+yy).RGBA()
						r, g, bl, n = r+uint64(cr), g+uint64(cg), bl+uint64(cb), n+1
					}
				}
				o := rgba.PixOffset(x, y)
				rgba.Pix[o], rgba.Pix[o+1], rgba.Pix[o+2], rgba.Pix[o+3] = uint8(r/n>>8), uint8(g/n>>8), uint8(bl/n>>8), 255
			}
		}
		dst = rgba
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// ---- volume, rétention et plafond (D8) ----

// ProgressStorage : volume occupé par les photos de palier (lecture pour le tableau de bord PJ1).
type ProgressStorage struct {
	Photos        int     `json:"photos" jsonschema:"nombre de photos de palier rangées"`
	UsedBytes     int64   `json:"used_bytes" jsonschema:"volume des blobs distincts des photos de palier"`
	CapBytes      int64   `json:"cap_bytes" jsonschema:"plafond réglé (0 = aucun)"`
	UsedPercent   float64 `json:"used_percent_of_cap,omitempty"`
	RetentionDays int     `json:"retention_days" jsonschema:"rétention des paliers intermédiaires (0 = illimitée)"`
	Level         string  `json:"level" jsonschema:"ok | warning (80 % du plafond atteint) | full (plafond atteint)"`
	Message       string  `json:"message" jsonschema:"message clair pour le tableau de bord"`
}

// ProgressStorage calcule le volume des photos de palier et le message du plafond.
func (s *Service) ProgressStorage(ctx context.Context) (ProgressStorage, error) {
	st := s.Settings(ctx)
	out := ProgressStorage{CapBytes: int64(st.ProgressCapMB) * 1024 * 1024, RetentionDays: st.ProgressRetentionDays, Level: "ok"}
	if err := s.c.Store.DB().QueryRowContext(ctx, `SELECT COALESCE(SUM(sz),0), COUNT(*) FROM
		(SELECT MAX(size) sz FROM camera_photos WHERE percent IS NOT NULL AND status='ok' GROUP BY sha256)`).Scan(&out.UsedBytes, new(int)); err != nil {
		return out, err
	}
	if err := s.c.Store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM camera_photos WHERE percent IS NOT NULL AND status='ok'`).Scan(&out.Photos); err != nil {
		return out, err
	}
	mb := float64(out.UsedBytes) / (1024 * 1024)
	switch {
	case out.CapBytes <= 0:
		out.Message = fmt.Sprintf("Photos de palier : %.0f Mo, aucun plafond réglé.", mb)
	default:
		out.UsedPercent = float64(out.UsedBytes) * 100 / float64(out.CapBytes)
		switch {
		case out.UsedBytes >= out.CapBytes:
			out.Level = "full"
			out.Message = fmt.Sprintf("Plafond des photos de palier atteint (%.0f Mo sur %d Mo) : les plus anciens paliers intermédiaires sont purgés ; les photos 100 %% et celles des impressions réussies sont conservées. Augmente le plafond ou réduis la rétention.", mb, st.ProgressCapMB)
		case out.UsedPercent >= 80:
			out.Level = "warning"
			out.Message = fmt.Sprintf("Les photos de palier approchent du plafond : %.0f Mo sur %d Mo (%.0f %%). Les plus anciens paliers intermédiaires seront purgés au-delà.", mb, st.ProgressCapMB, out.UsedPercent)
		default:
			out.Message = fmt.Sprintf("Photos de palier : %.0f Mo sur %d Mo (%.0f %%).", mb, st.ProgressCapMB, out.UsedPercent)
		}
	}
	return out, nil
}

type progressRow struct {
	id, job, sha string
	size         int64
	status       string
	at           string
}

// successJobs : impressions jugées « success » (print_logs.verdict, posé par G9 PJ11). Tant que la colonne n'existe pas
// (migration 0300 absente), le critère est ignoré.
func (s *Service) successJobs(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	var n int
	if s.c.Store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('print_logs') WHERE name='verdict'`).Scan(&n) != nil || n == 0 {
		return out
	}
	rows, err := s.c.Store.DB().QueryContext(ctx, `SELECT print_job_id FROM print_logs WHERE verdict='success'`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out[id] = true
		}
	}
	return out
}

// PurgeProgressPhotos applique la rétention (paliers intermédiaires plus vieux que ProgressRetentionDays) puis le plafond de
// volume (plus anciens paliers intermédiaires d'abord). Jamais supprimés : les photos 100 % (et toute photo « final »), celles
// des impressions jugées « success », une photo référencée ailleurs (Config.PhotoReferenced : galerie, projet). Renvoie le
// nombre de lignes supprimées.
func (s *Service) PurgeProgressPhotos(ctx context.Context) (int, error) {
	st := s.Settings(ctx)
	if st.ProgressRetentionDays <= 0 && st.ProgressCapMB <= 0 {
		return 0, nil
	}
	rows, err := s.c.Store.DB().QueryContext(ctx, `SELECT id,print_job_id,sha256,size,status,taken_at FROM camera_photos
		WHERE kind='progress' AND percent IS NOT NULL AND percent<>100 ORDER BY taken_at, id`)
	if err != nil {
		return 0, err
	}
	var cands []progressRow
	for rows.Next() {
		var r progressRow
		var job *string
		if err := rows.Scan(&r.id, &job, &r.sha, &r.size, &r.status, &r.at); err != nil {
			rows.Close()
			return 0, err
		}
		if job != nil {
			r.job = *job
		}
		cands = append(cands, r)
	}
	rows.Close()
	success := s.successJobs(ctx)
	protected := func(r progressRow) bool {
		if success[r.job] {
			return true
		}
		return r.sha != "" && s.c.PhotoReferenced != nil && s.c.PhotoReferenced(ctx, r.sha)
	}
	deleted, byRetention, byCap := 0, 0, 0
	gone := map[string]bool{}
	if st.ProgressRetentionDays > 0 {
		cutoff := store.FormatTime(s.now().AddDate(0, 0, -st.ProgressRetentionDays))
		for _, r := range cands {
			if r.at >= cutoff || protected(r) {
				continue
			}
			if _, err := s.deleteProgressRow(ctx, r); err != nil {
				return deleted, err
			}
			gone[r.id] = true
			deleted++
			byRetention++
		}
	}
	if st.ProgressCapMB > 0 {
		ps, err := s.ProgressStorage(ctx)
		if err != nil {
			return deleted, err
		}
		used, capB := ps.UsedBytes, ps.CapBytes
		for _, r := range cands {
			if used <= capB {
				break
			}
			if gone[r.id] || r.status != ProgressOK || protected(r) {
				continue
			}
			freed, err := s.deleteProgressRow(ctx, r)
			if err != nil {
				return deleted, err
			}
			used -= freed
			deleted++
			byCap++
		}
	}
	if deleted > 0 {
		_ = s.c.Store.Audit(ctx, "camera", "camera.progress_photos_purged", "camera_photos", "progress",
			map[string]any{"count": deleted, "by_retention": byRetention, "by_cap": byCap, "retention_days": st.ProgressRetentionDays, "cap_mb": st.ProgressCapMB})
	}
	return deleted, nil
}

// deleteProgressRow supprime la ligne et, si plus aucune ligne ni référence ne l'utilise, son blob. Renvoie les octets libérés.
func (s *Service) deleteProgressRow(ctx context.Context, r progressRow) (int64, error) {
	if _, err := s.c.Store.DB().ExecContext(ctx, `DELETE FROM camera_photos WHERE id=?`, r.id); err != nil {
		return 0, err
	}
	if r.sha == "" {
		return 0, nil
	}
	var left int
	if s.c.Store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM camera_photos WHERE sha256=?`, r.sha).Scan(&left) != nil || left > 0 {
		return 0, nil
	}
	if err := s.c.Blobs.Delete(r.sha); err != nil {
		s.log.Warn("purge des photos de palier : contenu non supprimé", "err", err)
		return 0, nil
	}
	return r.size, nil
}
