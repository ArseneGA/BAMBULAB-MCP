package sheets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/insights"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// frozenNote : le profil d'une fiche est relevé à la découpe (job d'impression) puis conservé dans la fiche de journal.
const frozenNote = "Réglages effectifs relevés à la découpe : ils ne changent pas si le profil est modifié ensuite."

// PrintSheet assemble la fiche d'une impression terminée : en-tête, verdict (déclaré), profil FIGÉ, bobines et grammes
// réels (estimés signalés), durée prévue contre réelle, coût, planche de paliers, alertes de caméra, accord humain lu dans
// l'audit. Étend get_print_log (inchangé). Lecture seule : aucune commande d'imprimante, aucun secret, aucune URL signée.
func (s *Service) PrintSheet(ctx context.Context, in PrintSheetInput) (*PrintSheet, error) {
	id := strings.TrimSpace(in.PrintJobID)
	if id == "" {
		return nil, invalid("print_job_id requis")
	}
	e, err := s.cfg.Insights.GetLog(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			if state := s.jobState(ctx, id); state != "" {
				return nil, fmt.Errorf("l'impression %q n'est pas terminée (état %s) : sa fiche se crée à la fin de l'impression : %w", id, state, store.ErrNotFound)
			}
			return nil, fmt.Errorf("impression %q inconnue (ni fiche de journal ni impression de ce nom) : %w", id, store.ErrNotFound)
		}
		return nil, err
	}
	declared := e.IsExternal()
	origin := e.Origin
	if origin == "" {
		origin = insights.OriginPrintalk
	}
	pc := e.PartCount
	if pc < 1 {
		pc = 1
	}
	sh := &PrintSheet{PrintJobID: e.PrintJobID, LogID: e.ID, Unavailable: map[string]string{}, Levels: map[string]string{}}
	sh.Header = PrintHeader{Name: e.Name, ModelID: e.ModelID, ModelName: e.ModelName, PrinterID: e.PrinterID, PrinterModel: e.PrinterModel, State: e.State, Outcome: e.Outcome,
		Origin: origin, Declared: declared, StartedAt: e.StartedAt, FinishedAt: e.FinishedAt, Reached100: e.IsPrinted(), PartCount: pc}

	// Verdict : jugement de l'humain (déclaré).
	sh.Verdict = VerdictBlock{Value: e.Verdict, Text: verdictText(e.Verdict), FailReason: e.FailReason, At: e.VerdictAt, Source: e.VerdictSource, Level: LevelDeclared, FailReasons: insights.FailReasons}
	if e.Verdict != "" {
		sh.Verdict.Note = s.cfg.Insights.VerdictNote(ctx, e.PrintJobID)
	}
	sh.Levels["verdict"] = LevelDeclared
	sh.Comment, sh.Rating, sh.Defects = e.Comment, e.Rating, e.Defects

	// Profil figé.
	lvl := LevelMeasured
	if declared {
		lvl = LevelDeclared
	}
	sh.Profile = FrozenProfile{Signature: insights.SettingsSignature(e.Settings), Label: insights.SettingsLabel(e.Settings), Settings: e.Settings, Frozen: frozenNote, Level: lvl}
	sh.Levels["profile"] = lvl
	if e.Settings.Machine == "" && e.Settings.Process == "" && len(e.Settings.Filaments) == 0 {
		sh.Unavailable["profile"] = "réglages non relevés à la découpe"
	}

	// Matière.
	sh.Material = s.material(ctx, e, declared)
	sh.Levels["material"] = sh.Material.Level
	if sh.Material.ActualGrams == nil {
		sh.Unavailable["actual_grams"] = "aucune masse relevée pour cette impression"
	}

	// Durée prévue contre réelle (CA10.2).
	sh.Duration = duration(e, declared)
	sh.Levels["duration"] = sh.Duration.Level
	switch {
	case sh.Duration.ActualSeconds == nil:
		sh.Unavailable["actual_seconds"] = "durée réelle non relevée"
		sh.Unavailable["duration_delta"] = "durée réelle non relevée"
	case sh.Duration.PlannedSeconds == nil:
		sh.Unavailable["planned_seconds"] = "durée prévue absente de la fiche"
		sh.Unavailable["duration_delta"] = "durée prévue absente de la fiche"
	}

	// Coût (calculé ; absent pour une impression déclarée).
	sh.Cost = CostBlock{Level: LevelComputed}
	if e.PlannedCost != nil {
		sh.Cost.Planned, sh.Cost.Currency = fp(e.PlannedCost.Total), e.PlannedCost.Currency
	}
	if e.ActualCost != nil && !declared {
		sh.Cost.Actual, sh.Cost.Currency = fp(e.ActualCost.Total), e.ActualCost.Currency
	}
	sh.Levels["cost"] = LevelComputed
	if sh.Cost.Actual == nil {
		if declared {
			sh.Unavailable["actual_cost"] = "impression déclarée hors Printalk : hors coût mesuré"
		} else {
			sh.Unavailable["actual_cost"] = "coût réel non calculé pour cette impression"
		}
	}

	// Photos, caméra, accord humain.
	sh.Photos = s.photoStrip(ctx, e.PrintJobID, declared)
	sh.Camera = s.cameraBlock(ctx, *e, declared, sh.Photos)
	sh.Levels["camera"] = LevelMeasured
	sh.Approval = s.approval(ctx, e.PrintJobID, declared)
	sh.Levels["approval"] = LevelMeasured

	if len(sh.Unavailable) == 0 {
		sh.Unavailable = nil
	}
	sh.Text = printText(sh)
	return sh, nil
}

// jobState : état d'une impression connue mais sans fiche de journal (« » si inconnue).
func (s *Service) jobState(ctx context.Context, id string) string {
	var st sql.NullString
	if s.cfg.Store.DB().QueryRowContext(ctx, `SELECT json_extract(doc,'$.state') FROM print_jobs WHERE id=?`, id).Scan(&st) != nil {
		return ""
	}
	return st.String
}

func (s *Service) material(ctx context.Context, e *insights.LogEntry, declared bool) MaterialBlock {
	m := MaterialBlock{Family: e.MaterialFamily, Spools: s.spoolUses(ctx, e.PrintJobID, declared)}
	if e.Estimated.Grams > 0 {
		m.EstimatedGrams = fp(e.Estimated.Grams)
	}
	est := strings.HasPrefix(e.Actual.Basis, "estimée")
	spoolGrams := 0.0
	for _, u := range m.Spools {
		spoolGrams += u.Grams
		est = est || u.Estimated
	}
	switch {
	case e.Actual.Grams > 0:
		m.ActualGrams = fp(e.Actual.Grams)
	case spoolGrams > 0:
		m.ActualGrams = fp(spoolGrams)
		est = est || len(m.Spools) == 0
	}
	m.Estimated, m.Basis = m.ActualGrams != nil && est, e.Actual.Basis
	switch {
	case declared:
		m.Level = LevelDeclared
	case m.Estimated:
		m.Level = LevelComputed
	default:
		m.Level = LevelMeasured
	}
	return m
}

// duration : durée prévue contre durée réelle. Écart = (réel − prévu) / prévu en % ; sans durée réelle : « non disponible ».
func duration(e *insights.LogEntry, declared bool) DurationBlock {
	d := DurationBlock{Level: LevelMeasured}
	if declared {
		d.Level = LevelDeclared
	}
	if e.Estimated.Seconds > 0 {
		d.PlannedSeconds = fp(e.Estimated.Seconds)
	}
	if e.Actual.Seconds > 0 {
		d.ActualSeconds = fp(e.Actual.Seconds)
	}
	switch {
	case d.ActualSeconds == nil && d.PlannedSeconds == nil:
		d.Text = "non disponible (ni durée prévue ni durée réelle)"
	case d.ActualSeconds == nil:
		d.Text = "non disponible (durée réelle non relevée) ; prévu " + fmtDuration(*d.PlannedSeconds)
	case d.PlannedSeconds == nil:
		d.Text = "réel " + fmtDuration(*d.ActualSeconds) + " ; écart non disponible (durée prévue absente)"
	default:
		pct := (*d.ActualSeconds - *d.PlannedSeconds) / *d.PlannedSeconds * 100
		d.DeltaPct = fp(pct)
		sign := ""
		if *d.DeltaPct > 0 {
			sign = "+"
		}
		d.Text = fmt.Sprintf("%s%s %% par rapport au prévu (prévu %s, réel %s)", sign, frNum(*d.DeltaPct, 1), fmtDuration(*d.PlannedSeconds), fmtDuration(*d.ActualSeconds))
	}
	return d
}

func printText(sh *PrintSheet) string {
	var b strings.Builder
	name := sh.Header.ModelName
	if name == "" {
		name = sh.Header.Name
	}
	if name == "" {
		name = sh.PrintJobID
	}
	b.WriteString(name + " : ")
	switch {
	case sh.Header.Declared:
		b.WriteString("impression déclarée hors Printalk (niveau déclaré)")
	case sh.Header.Reached100:
		b.WriteString("imprimée (100 % atteint)")
	default:
		b.WriteString("arrêtée avant 100 % (état " + sh.Header.State + ")")
	}
	b.WriteString(", " + sh.Verdict.Text)
	if sh.Verdict.FailReason != "" {
		b.WriteString(" (" + sh.Verdict.FailReason + ")")
	}
	b.WriteString(".\nProfil figé à la découpe : " + sh.Profile.Label + ".\n")
	if sh.Material.ActualGrams != nil {
		b.WriteString("Matière : " + frNum(*sh.Material.ActualGrams, 1) + " g")
		if sh.Material.Estimated {
			b.WriteString(" (estimée, non pesée)")
		}
		b.WriteString(".\n")
	} else {
		b.WriteString("Matière : non disponible.\n")
	}
	b.WriteString("Durée : " + sh.Duration.Text + ".\n")
	if sh.Cost.Actual != nil {
		b.WriteString("Coût réel (calculé) : " + frNum(*sh.Cost.Actual, 2) + " " + sh.Cost.Currency + ".\n")
	}
	if sh.Photos.Available > 0 {
		b.WriteString(fmt.Sprintf("Photos du journal : %d palier(s) avec photo.\n", sh.Photos.Available))
	} else if sh.Photos.Note != "" {
		b.WriteString("Photos : " + sh.Photos.Note + ".\n")
	}
	for _, st := range sh.Photos.Steps {
		if st.Status == StepUnavailable {
			b.WriteString(fmt.Sprintf("  palier %d %% : photo non disponible (%s)\n", st.Percent, st.Reason))
		}
	}
	b.WriteString("Caméra : " + sh.Camera.Text + ".\n")
	b.WriteString(sh.Approval.Text + "\n")
	return b.String()
}
