package printing

import (
	"fmt"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/localtime"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

// Étapes réellement exécutées par l'imprimante pour CETTE impression, d'après l'étape courante qu'elle transmet
// (stg_cur du rapport MQTT). L'historique est propre à l'impression : il repart de zéro à chaque lancement.

const maxStagesKept = 60

// StageSeen : une étape vue sur l'imprimante pendant cette impression.
type StageSeen struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

// noteStage enregistre l'étape courante de l'imprimante ; renvoie vrai si une NOUVELLE étape a été ajoutée
// (seul cas qui justifie une écriture en base : la dernière observation n'est mise à jour qu'en mémoire).
func (p *PrintProgress) noteStage(st printer.Status, now time.Time) bool {
	if !st.StageSeen || printer.StageIdle(st.Stage) {
		return false
	}
	switch st.State {
	case printer.StatePreparing, printer.StatePrinting, printer.StatePaused:
	default:
		return false
	}
	p.Stage, p.StageName = st.Stage, printer.StageName(st.Stage)
	if n := len(p.Stages); n > 0 && p.Stages[n-1].ID == st.Stage {
		p.Stages[n-1].LastSeen = now
		return false
	}
	p.Stages = append(p.Stages, StageSeen{ID: st.Stage, Name: printer.StageName(st.Stage), FirstSeen: now, LastSeen: now})
	if over := len(p.Stages) - maxStagesKept; over > 0 {
		p.Stages = p.Stages[over:]
	}
	return true
}

// StageReport est une étape rapportée au client.
type StageReport struct {
	ID            int       `json:"id" jsonschema:"numéro d'étape Bambu (stg_cur)"`
	Name          string    `json:"name" jsonschema:"libellé de l'étape"`
	State         string    `json:"state" jsonschema:"done (effectuée) | in_progress (en cours)"`
	FirstSeenAt   time.Time `json:"first_seen_at" jsonschema:"RFC 3339"`
	FirstSeenAtLC string    `json:"first_seen_local" jsonschema:"heure locale de la première observation (ex. 21:05:12)"`
	Label         string    `json:"label" jsonschema:"phrase prête à lire : « Mise à niveau du plateau : effectuée à 21:05 »"`
}

// StageSummary regroupe ce que l'imprimante a réellement fait pour cette impression.
type StageSummary struct {
	Current       string        `json:"current,omitempty" jsonschema:"étape en cours d'après l'imprimante"`
	Stages        []StageReport `json:"stages" jsonschema:"étapes vues pour CETTE impression, dans l'ordre (historique)"`
	BedLeveling   string        `json:"bed_leveling" jsonschema:"performed (mise à niveau vue) | not_observed (demandée mais non vue) | not_requested (désactivée par l'humain) | unknown"`
	FlowCali      string        `json:"flow_cali" jsonschema:"performed | not_observed | not_requested | unknown"`
	VibrationCali string        `json:"vibration_cali" jsonschema:"performed | not_observed | not_requested | unknown"`
	Note          string        `json:"note,omitempty"`
}

// stageSummary construit le rapport d'étapes d'une impression (nil si l'imprimante n'a transmis aucune étape).
func stageSummary(j Job, cur *printer.Status) *StageSummary {
	if j.Print == nil || (len(j.Print.Stages) == 0 && (cur == nil || !cur.StageSeen)) {
		return nil
	}
	out := &StageSummary{Stages: []StageReport{}}
	st := j.Print.Stages
	curID := -1
	if cur != nil && cur.StageSeen && !printer.StageIdle(cur.Stage) && cur.Connected && !cur.Stale {
		curID = cur.Stage
		out.Current = printer.StageName(cur.Stage)
	} else if j.Print.PrinterState == "preparing" || j.Print.PrinterState == "printing" {
		out.Current = j.Print.StageName
	}
	for i, s := range st {
		state := "done"
		verb := "effectuée"
		if i == len(st)-1 && (curID == s.ID || (cur == nil && out.Current == s.Name)) {
			state, verb = "in_progress", "en cours depuis"
		}
		hh := localtime.In(s.FirstSeen).Format("15:04")
		out.Stages = append(out.Stages, StageReport{ID: s.ID, Name: s.Name, State: state, FirstSeenAt: s.FirstSeen, FirstSeenAtLC: localtime.Clock(s.FirstSeen),
			Label: fmt.Sprintf("%s : %s %s", s.Name, verb, hh)})
	}
	seen := map[int]bool{}
	for _, s := range st {
		seen[s.ID] = true
	}
	verdict := func(requested bool, ids ...int) string {
		if !requested {
			return "not_requested"
		}
		for _, id := range ids {
			if seen[id] {
				return "performed"
			}
		}
		if len(st) == 0 {
			return "unknown"
		}
		return "not_observed"
	}
	out.BedLeveling = verdict(!j.NoBedLeveling, printer.StageBedLeveling)
	out.FlowCali = verdict(!j.NoFlowCali, printer.StageCalibratingFlow, printer.StageCalibratingExtr)
	out.VibrationCali = verdict(!j.NoVibrationCali, printer.StageMotorNoiseCali)
	var notes []string
	if out.BedLeveling == "not_observed" {
		notes = append(notes, "la mise à niveau était demandée mais l'imprimante n'a transmis aucune étape de mise à niveau (étape trop brève pour être vue, ou non exécutée par cette machine) : ne pas la considérer comme prouvée")
	}
	if len(notes) > 0 {
		out.Note = strings.Join(notes, " ; ") + "."
	}
	return out
}

// stageLine : une ligne de texte pour le contenu du résultat de get_print_status.
func (o *StageSummary) StageLine() string {
	if o == nil || len(o.Stages) == 0 {
		return ""
	}
	var parts []string
	for _, s := range o.Stages {
		parts = append(parts, s.Label)
	}
	return "Étapes vues sur l'imprimante : " + strings.Join(parts, " ; ") + "."
}
