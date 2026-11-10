package printer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Ignorer un objet en cours d'impression (étape SK0 de docs/specs/SK-ignorer-un-objet.md).
//
// La commande native de Bambu Studio est publiée sur device/{serial}/request (QoS 1) :
//
//	{"print":{"sequence_id":"<n>","command":"skip_objects","obj_list":[130,174]}}
//
// Elle est IRRÉVERSIBLE côté imprimante. Elle ne figure volontairement PAS dans la liste blanche des accessoires
// (ValidateAccessoryPayload la refuse) : elle sort UNIQUEMENT par Bambu.SkipObjects, validée par
// ValidateSkipObjectsPayload, une seule fois, jamais renvoyée. Son seul appelant autorisé est printing.SkipObjectsExecute,
// lui-même atteint seulement par la sous-commande humaine `bambud skip-objects` (test de source : skip_source_test.go).

// MaxSkipObjects : nombre maximal d'identifiants dans une commande.
const MaxSkipObjects = 64

// ErrSkipNotAllowed : charge utile de skip_objects non conforme à l'unique forme admise.
var ErrSkipNotAllowed = errors.New("printer: charge utile skip_objects refusée")

// ObjectSkipper est l'interface OPTIONNELLE des adaptateurs qui savent relayer skip_objects (le transport « agent »
// de Windows ne l'implémente pas : refus « non pris en charge par ce transport »).
type ObjectSkipper interface {
	// SkipObjects publie UNE fois la commande (QoS 1, accusé de transport attendu) ; elle ne prouve pas l'effet :
	// la preuve est l'apparition des identifiants dans Status.SkippedObjects (s_obj).
	SkipObjects(ctx context.Context, ids []int) error
	// LastSkipReply : dernière réponse explicite de l'imprimante à skip_objects (« success », « fail : raison »),
	// vide si elle n'a rien répondu (les P1 ne répondent pas toujours).
	LastSkipReply() string
}

// skipObjectsPayload construit la seule forme admise (sans la valider).
func skipObjectsPayload(seq string, ids []int) map[string]any {
	return map[string]any{"print": map[string]any{"sequence_id": seq, "command": "skip_objects", "obj_list": append([]int(nil), ids...)}}
}

func toSkipInt(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), t == int64(int(t))
	case float64:
		if t != math.Trunc(t) || math.Abs(t) > math.MaxInt32 {
			return 0, false
		}
		return int(t), true
	}
	return 0, false
}

// ValidateSkipObjectsPayload est le validateur DÉDIÉ de skip_objects : un seul objet « print » aux seules clés
// sequence_id (chaîne numérique), command (« skip_objects ») et obj_list ; obj_list = entiers > 0, non vide, au plus 64,
// sans doublon. Toute autre clé ou valeur est refusée. Appelé avant chaque publication.
func ValidateSkipObjectsPayload(payload map[string]any) error {
	if len(payload) != 1 {
		return fmt.Errorf("%w : un seul objet « print » attendu", ErrSkipNotAllowed)
	}
	pr, ok := payload["print"].(map[string]any)
	if !ok {
		return fmt.Errorf("%w : objet « print » absent", ErrSkipNotAllowed)
	}
	if !onlyKeys(pr, "sequence_id", "command", "obj_list") {
		return fmt.Errorf("%w : champs de la commande (seuls sequence_id, command, obj_list)", ErrSkipNotAllowed)
	}
	if pr["command"] != "skip_objects" {
		return fmt.Errorf("%w : commande %v", ErrSkipNotAllowed, pr["command"])
	}
	seq, _ := pr["sequence_id"].(string)
	if n, err := strconv.Atoi(seq); err != nil || n < 0 || strings.TrimSpace(seq) != seq {
		return fmt.Errorf("%w : sequence_id %q", ErrSkipNotAllowed, seq)
	}
	var raw []any
	switch l := pr["obj_list"].(type) {
	case []int:
		for _, v := range l {
			raw = append(raw, v)
		}
	case []any:
		raw = l
	default:
		return fmt.Errorf("%w : obj_list doit être une liste d'entiers", ErrSkipNotAllowed)
	}
	if len(raw) == 0 {
		return fmt.Errorf("%w : obj_list vide", ErrSkipNotAllowed)
	}
	if len(raw) > MaxSkipObjects {
		return fmt.Errorf("%w : %d identifiants (au plus %d)", ErrSkipNotAllowed, len(raw), MaxSkipObjects)
	}
	seen := map[int]bool{}
	for _, v := range raw {
		n, ok := toSkipInt(v)
		if !ok || n <= 0 {
			return fmt.Errorf("%w : identifiant %v (entier > 0 attendu)", ErrSkipNotAllowed, v)
		}
		if seen[n] {
			return fmt.Errorf("%w : identifiant %d en double", ErrSkipNotAllowed, n)
		}
		seen[n] = true
	}
	return nil
}

var _ ObjectSkipper = (*Bambu)(nil)

// SkipObjects publie la commande skip_objects, UNE fois : jamais de renvoi (QoS 1 sans reconnexion automatique, aucun
// nouvel essai ici). Exige une impression en cours (printing : ni préparation ni pause). Renvoie nil quand l'accusé de
// transport est reçu ; l'effet se vérifie par s_obj (Status.SkippedObjects).
func (b *Bambu) SkipObjects(ctx context.Context, ids []int) error {
	b.skipMu.Lock()
	defer b.skipMu.Unlock()
	c := b.current()
	if c == nil {
		return ErrNotConnected
	}
	if s := b.snapshot(); s.State != StatePrinting {
		return fmt.Errorf("%w : skip_objects pendant l'état %s (il faut une impression en cours)", ErrBadState, s.State)
	}
	payload := skipObjectsPayload(b.nextSeq(), ids)
	if err := ValidateSkipObjectsPayload(payload); err != nil {
		return err
	}
	b.mu.Lock()
	b.skipReply = ""
	b.mu.Unlock()
	if err := b.publishAcked(ctx, c, payload); err != nil {
		return fmt.Errorf("printer: skip_objects not acknowledged: %w", err)
	}
	return nil
}

// LastSkipReply : voir ObjectSkipper.
func (b *Bambu) LastSkipReply() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.skipReply
}

func (b *Bambu) noteSkipReply(pm map[string]any) {
	r := str(pm["result"])
	if r == "" {
		return
	}
	if reason := str(pm["reason"]); reason != "" && !strings.EqualFold(r, reason) {
		r += " : " + reason
	}
	b.mu.Lock()
	b.skipReply = r
	b.mu.Unlock()
}

// parseSkippedObjects lit print.s_obj (liste d'identifiants, nombres ou chaînes) ; nil si vide ou absent.
func parseSkippedObjects(d map[string]any) []int {
	arr, _ := d["s_obj"].([]any)
	var out []int
	for _, v := range arr {
		switch t := v.(type) {
		case float64:
			out = append(out, int(t))
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
				out = append(out, n)
			}
		}
	}
	return out
}
