package jobs

import (
	"fmt"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
)

// Barre de progression en texte et champs structurés stables (percent, done_bytes, total_bytes, eta_seconds, phase,
// phase_label) : le client MCP peut les lire tels quels pour dessiner sa propre barre ; le texte des statuts porte la
// même information sous forme lisible : « Dépôt sur la P1S : ███████░░░ 72 % (21,6 / 30 Mo, ~1 min) ».

// BarWidth : largeur de la barre texte, en cases.
const BarWidth = 10

// Bar dessine une barre de largeur width : « ███████░░░ ». percent est borné à [0 ; 100].
func Bar(percent, width int) string {
	if width <= 0 {
		width = BarWidth
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	n := (percent*width + 50) / 100 // arrondi au plus proche
	if percent > 0 && n == 0 {
		n = 1
	}
	if percent < 100 && n == width {
		n = width - 1
	}
	return strings.Repeat("█", n) + strings.Repeat("░", width-n)
}

// FormatMB : « 21,6 » (virgule décimale).
func FormatMB(b int64) string {
	s := strings.Replace(fmt.Sprintf("%.1f", float64(b)/(1<<20)), ".", ",", 1)
	return strings.TrimSuffix(s, ",0")
}

// FormatETA : « ~45 s », « ~3 min », « ~1 h 05 ».
func FormatETA(sec int) string {
	switch {
	case sec < 0:
		sec = 0
		fallthrough
	case sec < 60:
		return fmt.Sprintf("~%d s", sec)
	case sec < 5400:
		return fmt.Sprintf("~%d min", (sec+30)/60)
	}
	return fmt.Sprintf("~%d h %02d", sec/3600, (sec%3600)/60)
}

// phaseOf donne le code de phase (stable) et le libellé de repli d'un travail d'après son genre et son état.
func phaseOf(j contracts.Job) (code, label string) {
	switch j.State {
	case contracts.JobQueued:
		return "queued", "En file d'attente"
	case contracts.JobSucceeded:
		return "done", "Terminé"
	case contracts.JobFailed:
		return "failed", "Échec"
	case contracts.JobCancelled:
		return "cancelled", "Annulé"
	case contracts.JobUnknown:
		return "unknown", "Issue inconnue"
	}
	switch j.Kind {
	case "printing.slice", "goals.slice_candidate":
		return "slicing", "Découpe par Bambu Studio"
	case "upload_print_artifact":
		return "uploading", "Dépôt sur l'imprimante"
	case "start_print":
		return "starting", "Lancement de l'impression"
	case "catalog.import":
		return "importing", "Import"
	case "mesh.analyze":
		return "analyzing", "Analyse du maillage"
	case "mesh.preview":
		return "rendering", "Aperçu 3D"
	case "cad.step_import":
		return "converting", "Conversion STEP"
	case "project.export":
		return "exporting", "Export du projet"
	}
	return "running", "Travail en cours"
}

// finalize remplit les champs dérivés (stables) : done_bytes / total_bytes, phase_code, phase_label, bar, text.
func (p *Progress) finalize(j contracts.Job, label string) {
	p.DoneBytes, p.TotalBytes = p.BytesDone, p.BytesTotal
	code, fallback := phaseOf(j)
	p.PhaseCode = code
	switch {
	case label != "":
		p.PhaseLabel = label
	case code == "running" && p.Phase != "":
		r := []rune(p.Phase)
		p.PhaseLabel = strings.ToUpper(string(r[:1])) + string(r[1:])
	default:
		p.PhaseLabel = fallback
	}
	if p.Phase == "" {
		p.Phase = p.PhaseLabel
	}
	p.Bar = Bar(p.Percent, BarWidth)
	p.Text = p.textLine()
}

func (p Progress) textLine() string {
	label := p.PhaseLabel
	if label == "" {
		label = "Travail"
	}
	switch p.State {
	case string(contracts.JobQueued):
		s := label
		if p.QueuePosition != nil {
			s += fmt.Sprintf(" (position %d)", *p.QueuePosition)
		}
		return s
	case string(contracts.JobFailed), string(contracts.JobCancelled), string(contracts.JobUnknown):
		return fmt.Sprintf("%s : %s %d %%", label, p.Bar, p.Percent)
	}
	var det []string
	if p.TotalBytes > 0 {
		det = append(det, fmt.Sprintf("%s / %s Mo", FormatMB(p.DoneBytes), FormatMB(p.TotalBytes)))
	}
	if p.Estimated {
		det = append(det, "estimation")
	}
	if p.ETASeconds != nil && !p.Terminal {
		det = append(det, FormatETA(*p.ETASeconds))
	}
	s := fmt.Sprintf("%s : %s %d %%", label, p.Bar, p.Percent)
	if len(det) > 0 {
		s += " (" + strings.Join(det, ", ") + ")"
	}
	if p.Stalled {
		s += " — ATTENTION : aucun signe de vie"
	}
	return s
}
