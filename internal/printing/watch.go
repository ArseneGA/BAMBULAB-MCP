package printing

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

// Supervision d'une impression PRINTING : réconciliation avec ce que l'imprimante rapporte, à CHAQUE rapport.
//
// Cause du défaut de terrain (impression restée PRINTING 50 minutes après un abandon en préparation) : l'ancien suivi ne
// concluait que sur FINISH ou FAILED du MÊME fichier. Une P1S qui abandonne en préparation revient en IDLE, puis un
// redémarrage efface le fichier courant : ni FINISH, ni FAILED, ni fichier reconnu -> rien ne se passait jamais.
//
// Règles (aucune ne lance, n'arrête ni ne reprend quoi que ce soit : lecture et écriture de l'état serveur seulement) :
//   - IDLE après avoir vu l'impression active (au-delà de IdleGrace, ou tout de suite après une coupure de liaison :
//     redémarrage) -> FAILED avec les codes décodés ;
//   - le rapport ne désigne plus ce fichier (autre fichier, nom effacé) au-delà de MismatchGrace -> FAILED ;
//   - FINISH -> COMPLETED ; FAILED -> FAILED (comme avant, avec les codes décodés) ;
//   - PAUSE : « attend une action à l'écran : <texte HMS> » (alerte, pas de changement d'état) ;
//   - RUNNING dont couche / % / temps restant sont figés depuis StallAfter, ou consignes incohérentes (plateau à 0 °C
//     en pleine impression) : « bloquée : … » (alerte, pas de changement d'état) ;
//   - injoignable depuis GoneAfter : alerte « état inconnu » (pas de changement d'état : l'impression peut continuer).

// WatchConfig règle les délais de la supervision (tests : valeurs courtes).
type WatchConfig struct {
	StallAfter     time.Duration // progression figée avant « bloquée » (défaut 5 min)
	PrepStallAfter time.Duration // pendant une étape de préparation / calibration (chauffe, nettoyage, mise à niveau...) : durée de l'étape avant que « bloquée » redevienne possible (défaut 20 min)
	IdleGrace      time.Duration // IDLE persistant avant de conclure à un abandon (défaut 8 s)
	MismatchGrace  time.Duration // fichier non reconnu avant de conclure (défaut 30 s)
	InconsistAfter time.Duration // consignes incohérentes avant l'alerte (défaut 45 s)
	GoneAfter      time.Duration // injoignable avant l'alerte (défaut 3 min)
	TrackTick      time.Duration // période du relevé (défaut 2 s)
	StopVerifyWait time.Duration // vérification d'un arrêt dont l'état n'a pas changé (défaut 30 s)
	CommandWait    time.Duration // attente de l'effet d'une pause/reprise/arrêt par l'adaptateur (défaut 10 s) ; recrée les connexions
}

func (c WatchConfig) withDefaults() WatchConfig {
	d := func(p *time.Duration, v time.Duration) {
		if *p <= 0 {
			*p = v
		}
	}
	d(&c.StallAfter, 5*time.Minute)
	d(&c.PrepStallAfter, 20*time.Minute)
	d(&c.IdleGrace, 8*time.Second)
	d(&c.MismatchGrace, 30*time.Second)
	d(&c.InconsistAfter, 45*time.Second)
	d(&c.GoneAfter, 3*time.Minute)
	d(&c.TrackTick, 2*time.Second)
	d(&c.StopVerifyWait, 30*time.Second)
	return c
}

// SetWatchConfig règle les délais de supervision (tests, exploitation). Les valeurs nulles gardent le défaut.
func (s *Service) SetWatchConfig(c WatchConfig) {
	s.mu.Lock()
	s.wc = c
	changed := c.CommandWait != s.d.CommandWait
	s.d.CommandWait = c.CommandWait
	var ids []string
	if changed {
		for id := range s.adapters {
			ids = append(ids, id)
		}
	}
	s.mu.Unlock()
	for _, id := range ids { // l'adaptateur est recréé avec le nouveau délai
		s.dropAdapter(id)
	}
}

func (s *Service) watchCfg() WatchConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wc.withDefaults()
}

// Alert : situation à signaler à l'humain, sans transition d'état (pause sur erreur, impression bloquée, injoignable,
// arrêt non appliqué). Les abonnés (notifications) l'observent sans pouvoir rien provoquer.
type Alert struct {
	JobID     string
	PrinterID string
	JobName   string
	Kind      string // attention | stalled | unreachable | stop_not_applied
	Message   string
	At        time.Time
}

// OnAlert abonne fn aux alertes de supervision.
func (s *Service) OnAlert(fn func(Alert)) {
	s.evMu.Lock()
	s.alertSubs = append(s.alertSubs, fn)
	s.evMu.Unlock()
}

func (s *Service) emitAlert(j Job, kind, msg string) {
	s.evMu.Lock()
	subs := append([]func(Alert){}, s.alertSubs...)
	s.evMu.Unlock()
	a := Alert{JobID: j.ID, PrinterID: j.PrinterID, JobName: j.Name, Kind: kind, Message: msg, At: s.now()}
	for _, fn := range subs {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					s.log.Warn("abonné d'alerte d'impression en panique", "panic", r)
				}
			}()
			fn(a)
		}()
	}
}

// journal ajoute une ligne horodatée à l'historique de l'impression, sans changer son état.
func (s *Service) journal(ctx context.Context, id, event, note string) {
	if _, err := s.update(ctx, id, func(x *Job) error {
		x.event(contracts.PrintEvent(event), x.State, x.State, note, s.now())
		return nil
	}); err != nil {
		s.log.Warn("évènement non journalisé", "print_job", id, "event", event, "err", err)
	}
}

// decodeErrors décode print_error et alertes HMS du rapport.
func decodeErrors(st printer.Status) []printer.ErrorInfo {
	var out []printer.ErrorInfo
	for _, h := range st.HMS {
		out = append(out, printer.DescribeHMS(h))
	}
	if st.PrintError != 0 {
		out = append(out, printer.DescribePrintError(st.PrintError))
	}
	return out
}

// errorsText : « 0300_0D00_0002_0005 (grave) : texte ; … » (avec la consigne si withAction).
func errorsText(es []printer.ErrorInfo, withAction bool) string {
	var parts []string
	for _, e := range es {
		t := e.SummaryLine()
		if withAction && e.Action != "" {
			t += " → " + e.Action
		}
		parts = append(parts, t)
	}
	return strings.Join(parts, " ; ")
}

const maxErrorLog = 20

// errorLogRefresh : une erreur toujours active voit sa dernière observation réécrite en base au plus toutes les 30 s.
const errorLogRefresh = 30 * time.Second

// noteErrors range les codes d'erreur du rapport courant : ErrorDetails ne contient QUE ce qui est actif maintenant (vide si
// l'imprimante ne rapporte plus rien), ErrorLog garde l'historique horodaté (première et dernière observation active, drapeau
// Active). Renvoie vrai si l'ensemble des actifs a changé (nouveau code, code disparu) ou si une observation doit être
// réécrite : seul cas qui justifie une écriture en base.
func (p *PrintProgress) noteErrors(es []printer.ErrorInfo, now time.Time) bool {
	changed := len(es) != len(p.ErrorDetails)
	active := map[string]bool{}
	for _, e := range es {
		key := errorKey(e)
		active[key] = true
		found := false
		for i := range p.ErrorLog {
			if errorKey(p.ErrorLog[i].ErrorInfo) != key {
				continue
			}
			found = true
			c := &p.ErrorLog[i]
			if !c.Active {
				c.FirstSeenAt, c.Active, changed = now, true, true // revenu après une disparition : nouvelle occurrence
			}
			if now.Sub(c.LastSeenAt) >= errorLogRefresh {
				c.LastSeenAt, changed = now, true
			}
			c.ErrorInfo = e
		}
		if !found {
			p.ErrorLog = append(p.ErrorLog, Cause{ErrorInfo: e, Active: true, FirstSeenAt: now, LastSeenAt: now})
			changed = true
		}
	}
	for i := range p.ErrorLog {
		if c := &p.ErrorLog[i]; c.Active && !active[errorKey(c.ErrorInfo)] {
			c.Active, changed = false, true // périmé : reste dans l'historique avec sa dernière observation
		}
	}
	if over := len(p.ErrorLog) - maxErrorLog; over > 0 {
		p.ErrorLog = p.ErrorLog[over:]
	}
	p.ErrorDetails = append([]printer.ErrorInfo(nil), es...)
	return changed
}

func errorKey(e printer.ErrorInfo) string { return e.Code }

// causes sépare les codes actifs de l'historique (plus récents d'abord dans l'historique).
func (p *PrintProgress) causes() (active, history []Cause) {
	if p == nil {
		return nil, nil
	}
	for _, c := range p.ErrorLog {
		if c.Active {
			active = append(active, c)
		} else {
			history = append(history, c)
		}
	}
	sort.SliceStable(history, func(i, k int) bool { return history[i].LastSeenAt.After(history[k].LastSeenAt) })
	return active, history
}

func copyProgress(p *PrintProgress) PrintProgress {
	if p == nil {
		return PrintProgress{}
	}
	c := *p
	c.ErrorLog = append([]Cause(nil), p.ErrorLog...)
	c.Stages = append([]StageSeen(nil), p.Stages...)
	c.ErrorDetails = append([]printer.ErrorInfo(nil), p.ErrorDetails...)
	c.Alerted = append([]string(nil), p.Alerted...)
	return c
}

// seenKey : vrai si la clé d'alerte a déjà été émise (sinon la mémorise, 12 dernières).
func (p *PrintProgress) seenKey(k string) bool {
	for _, a := range p.Alerted {
		if a == k {
			return true
		}
	}
	p.Alerted = append(p.Alerted, k)
	if len(p.Alerted) > 12 {
		p.Alerted = p.Alerted[len(p.Alerted)-12:]
	}
	return false
}

func progressChanged(old *PrintProgress, n *PrintProgress, stageNew bool) bool {
	if old == nil || stageNew {
		return true
	}
	return old.Percent != n.Percent || old.Layer != n.Layer || old.RemainingMin != n.RemainingMin || old.PrinterState != n.PrinterState ||
		old.MatchesFile != n.MatchesFile || old.Attention != n.Attention || old.Blocked != n.Blocked || old.Unreachable != n.Unreachable ||
		old.WasGone != n.WasGone || old.IdleSince.IsZero() != n.IdleSince.IsZero() || old.MismatchAt.IsZero() != n.MismatchAt.IsZero() ||
		old.InconsistAt.IsZero() != n.InconsistAt.IsZero() || old.GoneSince.IsZero() != n.GoneSince.IsZero() || old.SeenActive != n.SeenActive ||
		len(old.ErrorDetails) != len(n.ErrorDetails) || old.Sig != n.Sig || !old.SigAt.Equal(n.SigAt)
}

// observe applique un rapport d'imprimante à une impression PRINTING (voir l'en-tête du fichier).
func (s *Service) observe(ctx context.Context, id string, st printer.Status) {
	j, err := s.GetJob(ctx, id)
	if err != nil || j.State != contracts.PrintPrinting || j.Remote == nil {
		return
	}
	wc, now := s.watchCfg(), s.now()
	pg := copyProgress(j.Print)
	save := func() {
		if progressChanged(j.Print, &pg, false) {
			cp := pg
			_, _ = s.update(ctx, id, func(x *Job) error { x.Print = &cp; return nil })
		}
	}

	// 1. Liaison coupée : état inconnu (jamais de conclusion), alerte au-delà de GoneAfter.
	if !st.Connected {
		if pg.GoneSince.IsZero() {
			pg.GoneSince = now
		}
		if now.Sub(pg.GoneSince) >= wc.GoneAfter && pg.Unreachable == "" {
			pg.Unreachable = fmt.Sprintf("imprimante injoignable depuis %s : l'état réel de l'impression est inconnu (elle peut continuer, ou avoir redémarré)", now.Sub(pg.GoneSince).Round(time.Second))
			if !pg.seenKey("unreachable") {
				s.journal(ctx, id, "printer_unreachable", pg.Unreachable)
				s.emitAlert(j, "unreachable", pg.Unreachable)
			}
		}
		save()
		return
	}
	// Reconnecté mais pas encore de rapport complet de la nouvelle session : rien à conclure.
	if st.Stale || st.State == printer.StateUnknown || !st.FullReportSeen {
		return
	}
	if !pg.GoneSince.IsZero() { // liaison rétablie : l'imprimante a pu redémarrer, son état est NEUF
		pg.WasGone, pg.GoneSince, pg.Unreachable = true, time.Time{}, ""
		pg.Alerted = nil
		s.journal(ctx, id, "printer_reconnected", "liaison rétablie : l'état de l'imprimante est relu (redémarrage possible)")
	}

	m := matches(st, j.Remote.Name)
	active := st.State == printer.StatePreparing || st.State == printer.StatePrinting || st.State == printer.StatePaused
	terminal := st.State == printer.StateFinished || st.State == printer.StateFailed
	pg.PrinterState, pg.Percent, pg.Layer, pg.TotalLayers, pg.RemainingMin = string(st.State), st.Progress, st.Layer, st.TotalLayers, st.RemainingMin
	pg.ObservedAt, pg.MatchesFile, pg.PrinterReason = now, m, st.FailReason
	stageNew := pg.noteErrors(decodeErrors(st), now) // FB3 : actifs (error_details) séparés de l'historique horodaté (error_log)
	if m {
		stageNew = pg.noteStage(st, now) || stageNew
	}
	if m && active {
		pg.SeenActive = true
		pg.IdleSince, pg.MismatchAt = time.Time{}, time.Time{}
	}
	saveAll := func() {
		if progressChanged(j.Print, &pg, stageNew) {
			cp := pg
			_, _ = s.update(ctx, id, func(x *Job) error { x.Print = &cp; return nil })
		}
	}
	s.mu.Lock()
	stopping := s.stopping[id]
	s.mu.Unlock()

	// 2. FINISH / FAILED d'un fichier vu avant toute activité : non attribuable (état d'une impression antérieure).
	if m && !pg.SeenActive && terminal {
		pg.PrinterReason = "état final du même fichier observé sans activité préalable : non attribuable à cette impression"
		if j.Print == nil || j.Print.PrinterState != pg.PrinterState {
			cp := pg
			_, _ = s.update(ctx, id, func(x *Job) error { x.Print = &cp; return nil })
		}
		return
	}
	// 3. IDLE après activité : abandon (la P1S revient en IDLE après une erreur de préparation, sans FINISH ni FAILED).
	if st.State == printer.StateIdle && pg.SeenActive {
		if stopping {
			return
		}
		if pg.IdleSince.IsZero() {
			pg.IdleSince = now
		}
		if pg.WasGone || now.Sub(pg.IdleSince) >= wc.IdleGrace {
			why := "l'imprimante est revenue au repos (IDLE) sans avoir terminé cette impression : abandon"
			if pg.WasGone {
				why = "l'imprimante a redémarré (liaison coupée puis rétablie) et est au repos (IDLE) : l'impression est perdue"
			}
			s.closeFromPrinter(ctx, id, contracts.EvFail, &pg, why)
			return
		}
		saveAll()
		return
	}
	pg.IdleSince = time.Time{}
	// 4. Le rapport ne désigne plus ce fichier alors qu'il a été vu en cours.
	if !m {
		if !pg.SeenActive { // jamais vu actif : comportement d'avant (progression non confirmée)
			if j.Print == nil || j.Print.MatchesFile || j.Print.PrinterState != pg.PrinterState {
				cp := pg
				_, _ = s.update(ctx, id, func(x *Job) error { x.Print = &cp; return nil })
			}
			return
		}
		if stopping {
			return
		}
		if pg.MismatchAt.IsZero() {
			pg.MismatchAt = now
		}
		if pg.WasGone || now.Sub(pg.MismatchAt) >= wc.MismatchGrace {
			cur := st.SubtaskName
			if cur == "" {
				cur = "(aucun fichier)"
			}
			why := fmt.Sprintf("l'imprimante ne désigne plus ce fichier (état %s, fichier courant : %s) : l'impression n'est plus la sienne", st.State, cur)
			s.closeFromPrinter(ctx, id, contracts.EvFail, &pg, why)
			return
		}
		saveAll()
		return
	}
	// 5. Fichier reconnu.
	switch st.State {
	case printer.StateFinished:
		_, _ = s.transition(ctx, id, contracts.EvPrintDone, "l'imprimante rapporte la fin de l'impression (FINISH)", func(x *Job) error {
			pg.Percent = 100
			pg.Attention, pg.Blocked = "", ""
			x.Print = &pg
			return nil
		})
	case printer.StateFailed:
		if stopping {
			return // arrêt humain en cours : l'arrêt enregistre CANCELLED (sinon le relevé suivant verra FAILED)
		}
		why := fmt.Sprintf("l'imprimante rapporte un échec (%s, print_error=%d)", st.FailReason, st.PrintError)
		s.closeFromPrinter(ctx, id, contracts.EvFail, &pg, why)
	case printer.StatePaused:
		pg.Blocked, pg.InconsistAt = "", time.Time{}
		pg.Sig, pg.SigAt = "", now
		txt := "en pause sans erreur signalée (pause manuelle ou à l'écran) : reprise possible avec resume_print"
		if es := decodeErrors(st); len(es) > 0 {
			txt = "attend une action à l'écran : " + errorsText(es, true)
		} else if st.PrintError != 0 || st.HasError() {
			txt = "attend une action à l'écran"
		}
		if pg.Attention != txt {
			pg.Attention = txt
			if !pg.seenKey("attention:" + txt) {
				s.journal(ctx, id, "attention", txt)
				s.emitAlert(j, "attention", txt)
			}
		}
		saveAll()
	default: // préparation / impression
		pg.Attention = ""
		if st.State == printer.StatePrinting {
			s.checkStall(ctx, j, &pg, st, wc, now)
		} else {
			pg.Blocked, pg.InconsistAt = "", time.Time{}
			pg.Sig, pg.SigAt = "", now
		}
		saveAll()
	}
}

// checkStall : RUNNING dont la progression est figée ou dont les consignes sont incohérentes. Alerte seulement.
func (s *Service) checkStall(ctx context.Context, j Job, pg *PrintProgress, st printer.Status, wc WatchConfig, now time.Time) {
	sig := fmt.Sprintf("%d/%d/%d", st.Layer, st.Progress, st.RemainingMin)
	if pg.Sig != sig || pg.SigAt.IsZero() {
		pg.Sig, pg.SigAt = sig, now
		pg.Blocked = ""
	}
	// Étape de préparation ou de calibration (chauffe, nettoyage de la buse, mise à niveau du plateau, scans, calibrations) :
	// l'imprimante rapporte « running » sans que couche, % ni temps restant bougent, et les consignes de chauffe se
	// succèdent : ce n'est pas un blocage (FB2, fausse alerte pendant la mise à niveau du 9/11). Les horloges repartent à
	// zéro tant que l'étape dure ; au-delà de PrepStallAfter l'étape est réellement anormale et la règle habituelle reprend.
	if st.StageSeen && printer.IsPreparationStage(st.Stage) {
		since := now
		if n := len(pg.Stages); n > 0 && pg.Stages[n-1].ID == st.Stage && !pg.Stages[n-1].FirstSeen.IsZero() {
			since = pg.Stages[n-1].FirstSeen
		}
		if now.Sub(since) < wc.PrepStallAfter {
			pg.Sig, pg.SigAt = sig, now
			pg.Blocked, pg.InconsistAt = "", time.Time{}
			return
		}
	}
	// consignes incohérentes en pleine impression (hors fin d'impression où le G-code coupe les chauffes)
	endPhase := st.Progress >= 97 || (st.TotalLayers > 0 && st.Layer >= st.TotalLayers) || (st.RemainingMin <= 1 && st.Progress > 0)
	var inconsist string
	if !endPhase {
		switch {
		case st.Temps.BedTarget == 0 && st.Temps.NozzleTarget > 0:
			inconsist = fmt.Sprintf("consigne du plateau à 0 °C pendant une impression en cours (buse consigne %.0f °C, mesurée %.0f °C)", st.Temps.NozzleTarget, st.Temps.Nozzle)
		case st.Temps.NozzleTarget == 0 && st.Temps.BedTarget > 0:
			inconsist = fmt.Sprintf("consigne de la buse à 0 °C pendant une impression en cours (plateau consigne %.0f °C)", st.Temps.BedTarget)
		case st.Temps.NozzleTarget == 0 && st.Temps.BedTarget == 0:
			inconsist = "toutes les consignes de chauffe sont à 0 °C pendant une impression en cours"
		}
	}
	if inconsist == "" {
		pg.InconsistAt = time.Time{}
	} else if pg.InconsistAt.IsZero() {
		pg.InconsistAt = now
	}
	var blocked string
	switch {
	case inconsist != "" && now.Sub(pg.InconsistAt) >= wc.InconsistAfter:
		blocked = "bloquée : " + inconsist
	case now.Sub(pg.SigAt) >= wc.StallAfter:
		blocked = fmt.Sprintf("bloquée : couche %d/%d, %d %% et temps restant (%d min) figés depuis %s", st.Layer, st.TotalLayers, st.Progress, st.RemainingMin, now.Sub(pg.SigAt).Round(time.Second))
		if inconsist != "" {
			blocked += " ; " + inconsist
		}
	}
	if blocked == "" {
		pg.Blocked = ""
		return
	}
	if es := decodeErrors(st); len(es) > 0 {
		blocked += " (erreurs de l'imprimante : " + errorsText(es, false) + ")"
	}
	blocked += ". Regarde l'imprimante ; si elle ne reprend pas : cancel_print (validé par l'humain) puis relance."
	if pg.Blocked != blocked {
		prevEmpty := pg.Blocked == ""
		pg.Blocked = blocked
		if prevEmpty && !pg.seenKey("stalled:"+pg.Sig+inconsist) {
			s.journal(ctx, j.ID, "stalled", blocked)
			s.emitAlert(j, "stalled", blocked)
		}
	}
}

// closeFromPrinter amène l'impression PRINTING à un état final (échec) avec la cause décodée, l'événement horodaté.
func (s *Service) closeFromPrinter(ctx context.Context, id string, ev contracts.PrintEvent, pg *PrintProgress, why string) {
	cause := ""
	if len(pg.ErrorDetails) > 0 {
		cause = " ; cause : " + errorsText(pg.ErrorDetails, false)
	} else if _, hist := pg.causes(); len(hist) > 0 { // plus rien d'actif : le dernier code vu, signalé comme tel
		var es []printer.ErrorInfo
		for i, c := range hist {
			if i >= 3 {
				break
			}
			es = append(es, c.ErrorInfo)
		}
		cause = " ; dernier(s) code(s) vu(s) avant sa disparition : " + errorsText(es, false)
	}
	msg := why + cause
	pg.Attention, pg.Blocked, pg.Unreachable = "", "", ""
	cp := *pg
	if _, err := s.transition(ctx, id, ev, msg, func(x *Job) error { x.Print, x.LastError = &cp, msg; return nil }); err != nil {
		s.log.Warn("réconciliation avec l'imprimante non enregistrée", "print_job", id, "err", err)
		return
	}
	_ = s.d.Store.Audit(ctx, actor, "print.reconciled_from_printer", "print_job", id, map[string]any{"event": string(ev), "why": why})
}

// alertSentence : phrase unique, la plus importante, à montrer dans get_print_status.
func alertSentence(p *PrintProgress) string {
	if p == nil {
		return ""
	}
	switch {
	case p.Blocked != "":
		return p.Blocked
	case p.Attention != "":
		return p.Attention
	case p.Unreachable != "":
		return p.Unreachable
	}
	return ""
}
