package printing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// Ignorer des objets en cours d'impression : étape SK0 (docs/specs/SK-ignorer-un-objet.md).
//
// Ce fichier est le SEUL appelant de printer.ObjectSkipper.SkipObjects (test de source : skip_source_test.go), et ses
// points d'entrée SkipObjectsList / SkipObjectsPrepare / SkipObjectsExecute ne sont appelés que par les routes locales et
// réservées de httpapi/skip_local.go, elles-mêmes appelées par la seule sous-commande humaine `bambud skip-objects`.
// Aucun outil MCP, aucune page /action, aucune route accessible au modèle ou au navigateur n'y mène.
//
// Une demande = une commande : le jeton de confirmation (émis par Prepare après contrôle) est consommé AVANT l'envoi,
// l'envoi est unique et jamais renvoyé, et la preuve de l'effet est l'apparition des identifiants dans s_obj.

const (
	skipTTL         = 2 * time.Minute
	defaultSkipWait = 20 * time.Second
	maxSkipWait     = 120 * time.Second
)

// Statuts de SkipObjectsExecute.
const (
	SkipVerified        = "verified"         // les identifiants sont dans s_obj
	SkipSentUnconfirmed = "sent_unconfirmed" // commande envoyée une fois, s_obj inchangé dans le délai : jamais renvoyée
)

// SkipObject : un objet de la plaque, tel que Bambu Studio le désigne dans slice_info.config.
type SkipObject struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Skipped bool   `json:"skipped" jsonschema:"déjà ignoré (présent dans print.s_obj du dernier rapport)"`
}

// SkipListOutput : les objets d'une impression (lecture seule, aucun envoi).
type SkipListOutput struct {
	PrintJobID   string       `json:"print_job_id"`
	PrinterID    string       `json:"printer_id"`
	File         string       `json:"file,omitempty"`
	JobState     string       `json:"job_state"`
	PrinterState string       `json:"printer_state,omitempty"`
	Objects      []SkipObject `json:"objects"`
	Skipped      []int        `json:"skipped_objects,omitempty"`
	CanSkip      bool         `json:"can_skip"`
	Refusal      string       `json:"refusal,omitempty" jsonschema:"pourquoi on ne peut pas ignorer d'objet maintenant ; vide si c'est possible"`
}

// SkipPrepareOutput : ce que la sous-commande montre à l'humain avant sa confirmation.
type SkipPrepareOutput struct {
	PrintJobID   string       `json:"print_job_id"`
	File         string       `json:"file,omitempty"`
	Targets      []SkipObject `json:"targets"`
	Remaining    []SkipObject `json:"remaining" jsonschema:"objets qui continueront d'être imprimés"`
	ConfirmToken string       `json:"confirm_token" jsonschema:"à usage unique, valable 2 minutes, lié à cette impression et à ces identifiants"`
	ExpiresAt    time.Time    `json:"expires_at"`
}

// SkipResultOutput : issue de l'envoi.
type SkipResultOutput struct {
	Status       string       `json:"status"`
	PrintJobID   string       `json:"print_job_id"`
	Targets      []SkipObject `json:"targets"`
	Skipped      []int        `json:"skipped_objects,omitempty" jsonschema:"s_obj tel que rapporté par l'imprimante à la fin de l'attente"`
	PrinterReply string       `json:"printer_reply,omitempty" jsonschema:"réponse explicite de l'imprimante à skip_objects, si elle en a donné une"`
	WaitedMS     int64        `json:"waited_ms"`
	Message      string       `json:"message"`
}

type skipReq struct {
	jobID   string
	ids     []int
	names   map[int]string
	expires time.Time
}

func newSkipToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "skp_" + hex.EncodeToString(b)
}

// cleanObjectName : nom d'objet sûr à afficher dans un terminal (pas de caractère de contrôle, longueur bornée).
func cleanObjectName(n string) string {
	n = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, n)
	n = strings.Join(strings.Fields(n), " ")
	if r := []rune(n); len(r) > 80 {
		n = string(r[:80]) + "…"
	}
	return n
}

type skipView struct {
	job     Job
	objs    []SkipObject // objets du fichier, avec Skipped d'après s_obj
	skipped []int
	ad      printer.Adapter
	sk      printer.ObjectSkipper
	st      printer.Status
	refusal error // non nul : pas d'envoi possible maintenant (la liste reste lisible)
}

func (v *skipView) find(id int) *SkipObject {
	for i := range v.objs {
		if v.objs[i].ID == id {
			return &v.objs[i]
		}
	}
	return nil
}

func (v *skipView) remainingAfter(ids []int) []SkipObject {
	var out []SkipObject
	for _, o := range v.objs {
		if !o.Skipped && !slices.Contains(ids, o.ID) {
			out = append(out, o)
		}
	}
	return out
}

// skipView relit l'impression, les objets du fichier (slice_info.config de l'artefact) et l'état courant de l'imprimante.
// Les erreurs renvoyées sont définitives (impression ou fichier inconnus, aucun objet étiqueté) ; ce qui empêche seulement
// l'envoi (impression pas en cours, transport, imprimante injoignable) va dans refusal.
func (s *Service) skipView(ctx context.Context, jobID string) (*skipView, error) {
	j, err := s.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if j.ArtifactSHA256 == "" || s.d.Blobs == nil {
		return nil, fmt.Errorf("%w : l'impression %s n'a pas d'artefact local", ErrNotFound, j.ID)
	}
	path, err := s.d.Blobs.Path(j.ArtifactSHA256)
	if err != nil {
		return nil, fmt.Errorf("%w : artefact de l'impression %s introuvable : %v", ErrNotFound, j.ID, err)
	}
	refs, err := slicer.PlateObjects(path, j.Plate)
	if err != nil {
		return nil, fmt.Errorf("%w : objets du fichier illisibles : %v", ErrInvalid, err)
	}
	v := &skipView{job: j}
	for _, r := range refs {
		id, aerr := strconv.Atoi(r.IdentifyID)
		if aerr != nil || id <= 0 {
			continue // identifiant inutilisable : jamais proposé
		}
		v.objs = append(v.objs, SkipObject{ID: id, Name: cleanObjectName(r.Name)})
	}
	if len(v.objs) == 0 {
		return nil, fmt.Errorf("%w : aucun objet étiqueté dans le fichier de cette impression (étiquetage des objets désactivé à la découpe, ou plaque d'une seule pièce) : on ne peut rien ignorer", ErrInvalid)
	}
	if j.State != contracts.PrintPrinting {
		v.refusal = fmt.Errorf("%w : l'impression %s est en %s ; on n'ignore un objet que pendant une impression en cours (PRINTING)", ErrState, j.ID, j.State)
		return v, nil
	}
	ad, _, err := s.adapter(ctx, j.PrinterID)
	if err != nil {
		v.refusal = err
		return v, nil
	}
	v.ad = ad
	sk, ok := ad.(printer.ObjectSkipper)
	if !ok {
		v.refusal = fmt.Errorf("%w : ignorer un objet : non pris en charge par ce transport (agent local ou adaptateur sans cette commande)", ErrUnavailable)
		return v, nil
	}
	v.sk = sk
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	st, serr := ad.Status(cctx)
	v.st = st
	switch {
	case serr != nil || !st.Connected:
		v.refusal = fmt.Errorf("%w : imprimante injoignable", ErrUnavailable)
	case st.State != printer.StatePrinting:
		v.refusal = fmt.Errorf("%w : l'imprimante est en état %s ; il faut une impression en cours (ni préparation, ni pause)", ErrState, st.State)
	case j.Remote != nil && !matches(st, j.Remote.Name):
		v.refusal = fmt.Errorf("%w : l'imprimante n'imprime pas le fichier de cette impression", ErrState)
	}
	v.skipped = append([]int(nil), st.SkippedObjects...)
	for i := range v.objs {
		v.objs[i].Skipped = slices.Contains(v.skipped, v.objs[i].ID)
	}
	return v, nil
}

// checkTargets : identifiants présents dans le fichier, pas déjà ignorés, sans doublon, et jamais tous les objets restants.
func (v *skipView) checkTargets(ids []int) error {
	if len(ids) == 0 {
		return invalidf("aucun identifiant d'objet")
	}
	if len(ids) > printer.MaxSkipObjects {
		return invalidf("%d identifiants (au plus %d)", len(ids), printer.MaxSkipObjects)
	}
	seen := map[int]bool{}
	for _, id := range ids {
		if id <= 0 {
			return invalidf("identifiant %d invalide (entier > 0)", id)
		}
		if seen[id] {
			return invalidf("identifiant %d en double", id)
		}
		seen[id] = true
		o := v.find(id)
		if o == nil {
			var have []string
			for _, x := range v.objs {
				have = append(have, strconv.Itoa(x.ID))
			}
			return invalidf("identifiant %d absent du fichier de cette impression (identifiants du fichier : %s)", id, strings.Join(have, ", "))
		}
		if o.Skipped {
			return invalidf("l'objet « %s » (id %d) est déjà ignoré (s_obj) : rien à envoyer pour lui", o.Name, id)
		}
	}
	if len(v.remainingAfter(ids)) == 0 {
		return invalidf("cela ignorerait tous les objets restants de la plaque : utilisez l'arrêt de l'impression à la place")
	}
	return nil
}

func (v *skipView) targets(ids []int) []SkipObject {
	var out []SkipObject
	for _, id := range ids {
		if o := v.find(id); o != nil {
			out = append(out, *o)
		}
	}
	return out
}

// SkipObjectsList liste les objets d'une impression et dit si on peut en ignorer maintenant. Lecture seule : rien n'est
// envoyé, rien n'est créé.
func (s *Service) SkipObjectsList(ctx context.Context, jobID string) (*SkipListOutput, error) {
	v, err := s.skipView(ctx, jobID)
	if err != nil {
		return nil, err
	}
	out := &SkipListOutput{PrintJobID: v.job.ID, PrinterID: v.job.PrinterID, File: v.job.Name, JobState: string(v.job.State),
		PrinterState: string(v.st.State), Objects: v.objs, Skipped: v.skipped, CanSkip: v.refusal == nil}
	if v.refusal != nil {
		out.Refusal = v.refusal.Error()
	} else if len(v.remainingAfter(nil)) < 2 {
		out.CanSkip, out.Refusal = false, "il ne reste qu'un objet à imprimer : utilisez l'arrêt"
	}
	return out, nil
}

// SkipObjectsPrepare contrôle la demande (D3 de la spec) et émet le jeton de confirmation à usage unique que la
// sous-commande présente à l'humain. Rien n'est envoyé à l'imprimante.
func (s *Service) SkipObjectsPrepare(ctx context.Context, jobID string, ids []int) (*SkipPrepareOutput, error) {
	v, err := s.skipView(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if v.refusal != nil {
		return nil, v.refusal
	}
	if err := v.checkTargets(ids); err != nil {
		return nil, err
	}
	tg := v.targets(ids)
	names := map[int]string{}
	for _, o := range tg {
		names[o.ID] = o.Name
	}
	s.mu.Lock()
	if s.skipReqs == nil {
		s.skipReqs = map[string]*skipReq{}
	}
	now := s.now()
	for k, r := range s.skipReqs { // purge
		if !now.Before(r.expires) {
			delete(s.skipReqs, k)
		}
	}
	tok := newSkipToken()
	exp := now.Add(skipTTL)
	s.skipReqs[tok] = &skipReq{jobID: v.job.ID, ids: append([]int(nil), ids...), names: names, expires: exp}
	s.mu.Unlock()
	s.journal(ctx, v.job.ID, "skip_requested", "ignorer "+describeSkip(tg)+" demandé par la ligne de commande humaine : en attente de sa confirmation")
	_ = s.d.Store.Audit(ctx, "human:cli", "print.skip_requested", "print_job", v.job.ID, map[string]any{"ids": ids, "names": skipNames(tg)})
	return &SkipPrepareOutput{PrintJobID: v.job.ID, File: v.job.Name, Targets: tg, Remaining: v.remainingAfter(ids), ConfirmToken: tok, ExpiresAt: exp}, nil
}

func skipNames(tg []SkipObject) []string {
	var out []string
	for _, o := range tg {
		out = append(out, o.Name)
	}
	return out
}

func describeSkip(tg []SkipObject) string {
	var parts []string
	for _, o := range tg {
		parts = append(parts, fmt.Sprintf("« %s » (id %d)", o.Name, o.ID))
	}
	n := "1 objet"
	if len(tg) > 1 {
		n = fmt.Sprintf("%d objets", len(tg))
	}
	return n + " " + strings.Join(parts, ", ")
}

// SkipObjectsExecute envoie la commande après la confirmation interactive de l'humain (jeton de Prepare). Le jeton est
// consommé AVANT l'envoi ; les contrôles sont refaits sur l'état courant ; UNE seule commande skip_objects part, jamais
// renvoyée ; l'effet est constaté dans s_obj pendant au plus wait (défaut 20 s). Aucune pause, reprise, arrêt ni G-code.
func (s *Service) SkipObjectsExecute(ctx context.Context, token string, wait time.Duration) (*SkipResultOutput, error) {
	if wait <= 0 {
		wait = defaultSkipWait
	}
	if wait > maxSkipWait {
		wait = maxSkipWait
	}
	s.mu.Lock()
	r := s.skipReqs[token]
	if r != nil {
		delete(s.skipReqs, token) // consommé avant l'envoi : une demande ne vaut qu'une fois
	}
	s.mu.Unlock()
	if r == nil {
		return nil, fmt.Errorf("%w : demande inconnue, déjà utilisée ou expirée : relancez la commande", ErrNotFound)
	}
	if !s.now().Before(r.expires) {
		return nil, fmt.Errorf("%w : la confirmation a expiré (elle vaut 2 minutes) : relancez la commande", ErrNotFound)
	}
	v, err := s.skipView(ctx, r.jobID)
	if err != nil {
		return nil, err
	}
	if v.refusal != nil {
		s.journal(ctx, r.jobID, "skip_failed", "ignorer des objets validé mais refusé avant l'envoi : "+v.refusal.Error())
		return nil, v.refusal
	}
	if err := v.checkTargets(r.ids); err != nil {
		s.journal(ctx, r.jobID, "skip_failed", "ignorer des objets validé mais refusé avant l'envoi : "+err.Error())
		return nil, err
	}
	tg := v.targets(r.ids)
	who := "human:cli"
	s.journal(ctx, r.jobID, "skip_approved", "ignorer "+describeSkip(tg)+" validé par l'humain (ligne de commande)")
	_ = s.d.Store.Audit(ctx, who, "print.skip_approved", "print_job", r.jobID, map[string]any{"ids": r.ids, "names": skipNames(tg)})
	s.journal(ctx, r.jobID, "skip_sent", "commande skip_objects envoyée à l'imprimante (une seule, jamais renvoyée automatiquement)")
	_ = s.d.Store.Audit(ctx, who, "print.skip_sent", "print_job", r.jobID, map[string]any{"ids": r.ids, "names": skipNames(tg)})

	cctx, cancel := context.WithTimeout(ctx, wait+60*time.Second)
	defer cancel()
	start := time.Now()
	if serr := v.sk.SkipObjects(cctx, r.ids); serr != nil {
		s.journal(ctx, r.jobID, "skip_failed", "envoi de skip_objects en échec : "+serr.Error())
		if errors.Is(serr, printer.ErrNotConnected) || errors.Is(serr, printer.ErrBadState) || errors.Is(serr, printer.ErrSkipNotAllowed) {
			return nil, fmt.Errorf("%w : rien n'a été envoyé : %v", ErrState, serr)
		}
		return nil, fmt.Errorf("%w : envoi de skip_objects en échec (la commande a pu partir : ne la renvoyez pas sans avoir regardé s_obj avec bambud skip-objects --list) : %v", ErrUnavailable, serr)
	}
	deadline := start.Add(wait)
	var last printer.Status
	for {
		if st, e := v.ad.Status(cctx); e == nil {
			last = st
			if containsAll(st.SkippedObjects, r.ids) {
				break
			}
		}
		if !time.Now().Before(deadline) || cctx.Err() != nil {
			break
		}
		sleepCtx(cctx, 200*time.Millisecond)
	}
	out := &SkipResultOutput{PrintJobID: r.jobID, Targets: tg, Skipped: last.SkippedObjects, PrinterReply: v.sk.LastSkipReply(), WaitedMS: time.Since(start).Milliseconds()}
	for i := range out.Targets {
		out.Targets[i].Skipped = slices.Contains(last.SkippedObjects, out.Targets[i].ID)
	}
	if containsAll(last.SkippedObjects, r.ids) {
		out.Status = SkipVerified
		out.Message = "Vérifié : l'imprimante liste ces objets comme ignorés (s_obj). Les autres objets continuent."
		s.journal(ctx, r.jobID, "skip_verified", "vérifié par l'imprimante (s_obj) : "+describeSkip(tg)+" ignoré(s)")
		_ = s.d.Store.Audit(ctx, who, "print.skip_verified", "print_job", r.jobID, map[string]any{"ids": r.ids, "names": skipNames(tg), "s_obj": last.SkippedObjects})
		return out, nil
	}
	out.Status = SkipSentUnconfirmed
	out.Message = fmt.Sprintf("Envoyée, non confirmée : s_obj n'a pas changé en %s. La commande n'est PAS renvoyée. Regardez l'imprimante (et la caméra), puis bambud skip-objects --job %s --list.", wait.Round(time.Second), r.jobID)
	if out.PrinterReply != "" {
		out.Message += " Réponse de l'imprimante : " + out.PrinterReply + "."
	}
	s.journal(ctx, r.jobID, "skip_unconfirmed", "commande envoyée mais s_obj inchangé dans le délai : non confirmée, jamais renvoyée automatiquement")
	_ = s.d.Store.Audit(ctx, who, "print.skip_unconfirmed", "print_job", r.jobID, map[string]any{"ids": r.ids, "names": skipNames(tg), "reply": out.PrinterReply})
	return out, nil
}

func containsAll(have, want []int) bool {
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}
