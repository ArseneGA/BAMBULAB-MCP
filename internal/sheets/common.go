package sheets

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/camera"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/insights"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/localtime"
)

func r2(v float64) float64 { return math.Round(v*100) / 100 }

func fp(v float64) *float64 { v = r2(v); return &v }

// median : médiane des valeurs (nil si aucune). Pure et déterministe.
func median(v []float64) *float64 {
	if len(v) == 0 {
		return nil
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	n := len(c)
	if n%2 == 1 {
		return fp(c[n/2])
	}
	return fp((c[n/2-1] + c[n/2]) / 2)
}

// frNum formate un nombre à la française (virgule décimale).
func frNum(v float64, decimals int) string {
	return strings.Replace(fmt.Sprintf("%.*f", decimals, v), ".", ",", 1)
}

func fmtDuration(sec float64) string {
	m := int(sec/60 + 0.5)
	if m < 60 {
		return fmt.Sprintf("%d min", m)
	}
	return fmt.Sprintf("%d h %02d", m/60, m%60)
}

// photoPercents : les dix paliers du journal photo.
var photoPercents = []int{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}

// photoStrip construit le bandeau de paliers d'une impression : les paliers consignés (ok ou unavailable), les autres
// « absent ». Sans aucun relevé, la liste est vide et Note explique pourquoi. Ne lit que la base et la caméra : aucune
// commande à l'imprimante, aucune URL signée (les images passent par la route existante /api/camera/photos/{sha256}).
func (s *Service) photoStrip(ctx context.Context, jobID string, declared bool) PhotoStrip {
	out := PhotoStrip{Steps: []PhotoStep{}}
	if declared {
		out.Note = "impression déclarée hors Printalk : aucun journal photo"
		return out
	}
	var recorded []camera.ProgressPhoto
	if s.cfg.Camera != nil {
		recorded, _ = s.cfg.Camera.ProgressPhotos(ctx, jobID)
	}
	byPct := map[int]camera.ProgressPhoto{}
	for _, p := range recorded {
		byPct[p.Percent] = p
	}
	final := s.finalPhoto(ctx, jobID)
	if len(recorded) == 0 {
		if final != nil {
			t := *final
			out.Thumbnail, out.Available = &t, 1
			out.Note = "pas de journal par paliers pour cette impression : seule la photo de fin existe"
			return out
		}
		if s.cfg.Camera == nil {
			out.Note = "photo non disponible : caméra non branchée"
		} else {
			out.Note = "photo non disponible : aucune photo de palier relevée pour cette impression (impression d'avant le journal photo, caméra absente ou hors ligne)"
		}
		return out
	}
	var lastOK *PhotoStep
	for _, pc := range photoPercents {
		st := PhotoStep{Percent: pc, Status: StepAbsent, Reason: "aucun relevé pour ce palier (non atteint ou sauté)"}
		if p, ok := byPct[pc]; ok {
			switch {
			case p.Status == camera.ProgressOK && p.SHA256 != "":
				st = PhotoStep{Percent: pc, Status: StepOK, SHA256: p.SHA256, URL: p.URL}
				out.Available++
				c := st
				lastOK = &c
			default:
				reason := p.Reason
				if reason == "" {
					reason = "raison non consignée"
				}
				st = PhotoStep{Percent: pc, Status: StepUnavailable, Reason: reason}
			}
		}
		out.Steps = append(out.Steps, st)
	}
	switch {
	case byPct[100].Status == camera.ProgressOK && byPct[100].SHA256 != "":
		for i := range out.Steps {
			if out.Steps[i].Percent == 100 {
				c := out.Steps[i]
				out.Thumbnail = &c
			}
		}
	case final != nil:
		out.Thumbnail = final
	case lastOK != nil:
		out.Thumbnail = lastOK
	}
	return out
}

// finalPhoto : la photo de fin (kind final) d'une impression, hors journal par paliers.
func (s *Service) finalPhoto(ctx context.Context, jobID string) *PhotoStep {
	var sha string
	err := s.cfg.Store.DB().QueryRowContext(ctx, `SELECT sha256 FROM camera_photos WHERE print_job_id=? AND kind='final' AND COALESCE(status,'ok')='ok' AND sha256<>'' ORDER BY taken_at DESC, id LIMIT 1`, jobID).Scan(&sha)
	if err != nil || sha == "" {
		return nil
	}
	return &PhotoStep{Percent: 100, Status: StepOK, SHA256: sha, URL: "/api/camera/photos/" + sha}
}

// cameraBlock : la caméra pendant l'impression. « Caméra non disponible » (on ne peut pas dire qu'il n'y a pas eu d'alerte)
// est distinct de « aucune alerte ». La caméra est réputée disponible quand elle a laissé une trace (photo ou alerte) ou
// quand la surveillance est active pour cette imprimante.
func (s *Service) cameraBlock(ctx context.Context, e insights.LogEntry, declared bool, strip PhotoStrip) CameraBlock {
	out := CameraBlock{Alerts: []AlertView{}, Level: LevelMeasured}
	if declared {
		out.AlertsStatus, out.Reason = AlertsUnavailable, "impression déclarée hors Printalk"
		out.Text = "caméra non disponible (" + out.Reason + ")"
		return out
	}
	var alerts []camera.Alert
	if s.cfg.Camera != nil {
		alerts, _ = s.cfg.Camera.Alerts(ctx, e.PrintJobID, 100)
	}
	sort.SliceStable(alerts, func(i, j int) bool { return alerts[i].At.Before(alerts[j].At) })
	traces := len(alerts) > 0 || len(strip.Steps) > 0 || strip.Thumbnail != nil
	switch {
	case traces:
		out.Available = true
	case s.cfg.Camera == nil:
		out.Reason = "caméra non branchée"
	default:
		st := s.cfg.Camera.Settings(ctx)
		disabled := false
		for _, p := range st.DisabledPrinters {
			disabled = disabled || p == e.PrinterID
		}
		switch {
		case !st.Enabled:
			out.Reason = "détection par caméra désactivée"
		case disabled:
			out.Reason = "caméra de cette imprimante exclue de la surveillance"
		default:
			out.Available = true
		}
	}
	for _, a := range alerts {
		v := AlertView{At: a.At, Score: r2(a.Score), Level: a.Level}
		if a.PhotoURL != "" {
			v.PhotoURL = a.PhotoURL
		}
		out.Alerts = append(out.Alerts, v)
	}
	switch {
	case len(out.Alerts) > 0:
		out.AlertsStatus = AlertsListed
		top := 0.0
		for _, a := range out.Alerts {
			top = math.Max(top, a.Score)
		}
		out.Text = fmt.Sprintf("%d alerte%s de la caméra (score le plus élevé %s)", len(out.Alerts), map[bool]string{true: "s", false: ""}[len(out.Alerts) > 1], frNum(top, 2))
	case out.Available:
		out.AlertsStatus = AlertsNone
		out.Text = "caméra disponible : aucune alerte levée pendant l'impression"
	default:
		out.AlertsStatus = AlertsUnavailable
		out.Text = "caméra non disponible (" + out.Reason + ") : on ne peut pas dire qu'aucune alerte n'a été levée"
	}
	return out
}

// Opérations d'audit lues pour l'accord humain (liste blanche : aucun jeton, aucun code ne sort).
var approvalOps = map[string]bool{
	"print.confirmation_approved":         true,
	"print.confirmation_code_accepted":    true,
	"print.confirmation_passkey_accepted": true,
}

const opRequested = "print.confirmation_requested"

// approval lit dans l'audit l'accord humain de lancement d'une impression. Ne lit que des dates et le canal.
func (s *Service) approval(ctx context.Context, jobID string, declared bool) ApprovalBlock {
	out := ApprovalBlock{Level: LevelMeasured}
	if declared {
		out.Status = ApprovalNotApplicable
		out.Text = "Impression déclarée hors Printalk : aucun accord de lancement à relever."
		return out
	}
	entries, err := s.cfg.Store.ListAudit(ctx, "print_job", jobID, 500)
	if err != nil {
		out.Status = ApprovalNone
		out.Text = "Accord humain non disponible (journal d'audit illisible)."
		return out
	}
	for _, a := range entries { // plus récentes d'abord : on garde la première de chaque sorte
		switch {
		case approvalOps[a.Operation] && out.ApprovedAt.IsZero():
			out.ApprovedAt, out.Via = a.At, viaOf(a.Actor, a.Operation)
		case a.Operation == opRequested && out.RequestedAt.IsZero():
			out.RequestedAt = a.At
		}
	}
	switch {
	case !out.ApprovedAt.IsZero():
		out.Status = ApprovalApproved
		out.Text = "Lancement approuvé par l'humain le " + localtime.Date(out.ApprovedAt) + " à " + localtime.Clock(out.ApprovedAt)[:5]
		if out.Via != "" {
			out.Text += " (" + out.Via + ")"
		}
		out.Text += "."
	case !out.RequestedAt.IsZero():
		out.Status = ApprovalRequested
		out.Text = "Confirmation demandée le " + localtime.Date(out.RequestedAt) + " : aucun accord relevé dans l'audit."
	default:
		out.Status = ApprovalNone
		out.Text = "Aucune trace d'accord de lancement dans l'audit."
	}
	return out
}

// viaOf : canal de l'accord (« web », « code personnel », « clé d'accès »), jamais le moindre secret.
func viaOf(actor, op string) string {
	switch op {
	case "print.confirmation_code_accepted":
		return "code personnel"
	case "print.confirmation_passkey_accepted":
		return "clé d'accès"
	}
	v := strings.TrimPrefix(actor, "human:")
	var b strings.Builder
	for _, r := range strings.ToLower(v) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
		if b.Len() >= 24 {
			break
		}
	}
	return b.String()
}

// spoolUses lit les bobines consommées par une impression (spool_usage) avec leur libellé quand elles sont inventoriées.
func (s *Service) spoolUses(ctx context.Context, jobID string, declared bool) []SpoolUse {
	rows, err := s.cfg.Store.DB().QueryContext(ctx, `SELECT u.spool_id, u.filament_slot, u.grams, u.estimated, u.price_per_kg, u.cost, u.note,
		COALESCE(json_extract(sp.doc,'$.material'),''), COALESCE(json_extract(sp.doc,'$.brand'),''), COALESCE(json_extract(sp.doc,'$.name'),'')
		FROM spool_usage u LEFT JOIN spools sp ON sp.id=u.spool_id WHERE u.print_job_id=? ORDER BY u.filament_slot, u.spool_id`, jobID)
	out := []SpoolUse{}
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var u SpoolUse
		var est int
		var price, cost sql.NullFloat64
		var mat, brand, name string
		if rows.Scan(&u.SpoolID, &u.Slot, &u.Grams, &est, &price, &cost, &u.Note, &mat, &brand, &name) != nil {
			continue
		}
		u.Grams, u.Estimated = r2(u.Grams), est == 1
		if price.Valid {
			u.PricePerKg = fp(price.Float64)
		}
		if cost.Valid && !declared {
			u.Cost = fp(cost.Float64)
		}
		u.Label = strings.TrimSpace(strings.Join(nonEmpty(mat, brand, name), " "))
		switch {
		case declared:
			u.Level = LevelDeclared
		case u.Estimated:
			u.Level = LevelComputed
		default:
			u.Level = LevelMeasured
		}
		out = append(out, u)
	}
	return out
}

func nonEmpty(v ...string) []string {
	var o []string
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			o = append(o, strings.TrimSpace(s))
		}
	}
	return o
}

func verdictText(v string) string { return insights.VerdictLabel(v) }

func dateOf(t time.Time) string { return localtime.Date(t) }
