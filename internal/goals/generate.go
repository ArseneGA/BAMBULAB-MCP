package goals

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
)

// OrientationSpec est une orientation proposée (heuristique de internal/mesh).
type OrientationSpec struct {
	Mode             string      `json:"mode"`
	Label            string      `json:"label"`
	Up               [3]float64  `json:"up" jsonschema:"direction du modèle qui devient +Z"`
	Rotation         [12]float64 `json:"rotation" jsonschema:"matrice 3x4 convention 3MF appliquée au modèle"`
	HeightMM         float64     `json:"height_mm"`
	BedContactMM2    float64     `json:"bed_contact_mm2"`
	OverhangFraction float64     `json:"overhang_fraction"`
}

// Change est un réglage modifié, justifié.
type Change struct {
	Criterion     string `json:"criterion" jsonschema:"identifiant du critère de l'objectif"`
	CriterionType string `json:"criterion_type"`
	Lever         string `json:"lever"`
	Key           string `json:"key,omitempty"`
	Target        string `json:"target,omitempty"`
	From          any    `json:"from,omitempty"`
	To            any    `json:"to,omitempty"`
	Effect        string `json:"effect"`
	Tradeoff      string `json:"expected_tradeoff"`
	Note          string `json:"note,omitempty"`
}

// Candidate est une variante bornée.
type Candidate struct {
	ID          string            `json:"id"`
	Index       int               `json:"index"`
	Kind        string            `json:"kind" jsonschema:"reference | variant"`
	Label       string            `json:"label"`
	Rationale   string            `json:"rationale"`
	Overrides   []slicer.Override `json:"overrides"`
	Orientation *OrientationSpec  `json:"orientation,omitempty"`
	Changes     []Change          `json:"changes"`
	Factors     int               `json:"factors" jsonschema:"nombre de facteurs modifiés"`
	Tradeoffs   []string          `json:"expected_tradeoffs"`
	Notes       []string          `json:"notes,omitempty"`
}

// Skipped explique un levier écarté.
type Skipped struct {
	Criterion string `json:"criterion"`
	Lever     string `json:"lever"`
	Reason    string `json:"reason"`
}

// GenInput est l'entrée du générateur.
type GenInput struct {
	Goal       *Goal
	Rules      *Rules
	Process    *profiles.Resolved
	Filament   *profiles.Resolved
	Family     string // famille du filament de base ("" si inconnue)
	Nozzle     float64
	Count      int // variantes demandées (hors référence)
	MaxFactors int // facteurs modifiés par variante (défaut 3)
	Mesh       *mesh.Mesh
	Bed        mesh.Target
}

// GenOutput est le résultat de la génération.
type GenOutput struct {
	Candidates []Candidate `json:"candidates"`
	Skipped    []Skipped   `json:"skipped"`
	Notes      []string    `json:"notes"`
}

type leverInst struct {
	crit   Criterion
	lever  Lever
	change Change
	ov     *slicer.Override
	orient *OrientationSpec
	num    *numChange // pour la fusion de compromis
}

type numChange struct {
	from, to []float64
	pct      bool
	integer  bool
}

// Generate produit la référence et jusqu'à Count variantes.
func Generate(in GenInput) (*GenOutput, error) {
	if in.Rules == nil || in.Goal == nil || in.Process == nil || in.Filament == nil {
		return nil, invalid("générateur : règles, objectif et profils requis")
	}
	if in.Count <= 0 {
		in.Count = 3
	}
	if in.MaxFactors <= 0 {
		in.MaxFactors = 3
	}
	if in.Nozzle <= 0 {
		in.Nozzle = 0.4
	}
	out := &GenOutput{}
	out.Candidates = append(out.Candidates, Candidate{
		ID: "cand_0", Index: 0, Kind: "reference", Label: "Référence (profil de base sans modification)",
		Rationale: "Point de comparaison : le profil de base tel quel.", Overrides: []slicer.Override{}, Changes: []Change{}, Tradeoffs: []string{},
	})

	// Critères actifs, par ordre de priorité (non négociables d'abord).
	var crits []Criterion
	for _, c := range in.Goal.Criteria {
		if c.Known {
			crits = append(crits, c)
		}
	}
	sort.SliceStable(crits, func(i, j int) bool {
		if crits[i].NonNegotiable != crits[j].NonNegotiable {
			return crits[i].NonNegotiable
		}
		return crits[i].Priority < crits[j].Priority
	})

	var orientRep *mesh.OrientationReport
	if in.Mesh != nil {
		if rep, err := mesh.Fabricability(in.Mesh, mesh.FabOptions{Target: in.Bed}); err == nil {
			orientRep = rep.Orientation
		} else {
			out.Notes = append(out.Notes, "orientation : analyse géométrique impossible ("+err.Error()+")")
		}
	}

	per := map[string][]leverInst{}
	var active []Criterion
	for _, c := range crits {
		rule := in.Rules.Criteria[c.Type]
		for _, l := range rule.Levers {
			li, why := resolveLever(in, c, l, orientRep)
			if li == nil {
				out.Skipped = append(out.Skipped, Skipped{Criterion: c.ID, Lever: l.ID, Reason: why})
				continue
			}
			per[c.ID] = append(per[c.ID], *li)
		}
		if len(per[c.ID]) > 0 {
			active = append(active, c)
		} else if len(rule.Levers) == 0 {
			out.Notes = append(out.Notes, fmt.Sprintf("%s : aucun réglage ne peut y répondre (%s)", c.Label, rule.NoLeversReason))
		}
	}
	if len(active) == 0 {
		out.Notes = append(out.Notes, "aucun levier applicable au profil de base et au matériau : seule la référence est proposée")
		return out, nil
	}

	type recipe struct {
		label     string
		rationale string
		insts     []leverInst
	}
	var recipes []recipe
	focus := func(c Criterion, n int, name string) recipe {
		l := per[c.ID]
		if n > len(l) {
			n = len(l)
		}
		return recipe{label: name + c.Label, rationale: fmt.Sprintf("Favorise « %s » (priorité %d%s) sans chercher à préserver les autres critères.", c.Label, c.Priority, nn(c)), insts: l[:n]}
	}
	compromise := func() recipe {
		var insts []leverInst
		idx := make([]int, len(active))
		for len(insts) < in.MaxFactors+len(active) { // borne large, tronquée par la fusion
			progressed := false
			for i, c := range active {
				if idx[i] < len(per[c.ID]) {
					insts = append(insts, per[c.ID][idx[i]])
					idx[i]++
					progressed = true
				}
			}
			if !progressed {
				break
			}
		}
		names := make([]string, len(active))
		for i, c := range active {
			names[i] = "« " + c.Label + " »"
		}
		return recipe{label: "Compromis " + strings.Join(names, " / "),
			rationale: "Prend un levier de chaque critère à tour de rôle (ordre de priorité) ; en cas d'opposition sur un même réglage, valeur intermédiaire.", insts: insts}
	}
	switch {
	case len(active) == 1:
		c := active[0]
		n := len(per[c.ID])
		if n > in.MaxFactors {
			n = in.MaxFactors
		}
		names := []string{"Léger : ", "Modéré : ", "Marqué : "}
		for k := 1; k <= n && k <= 3; k++ {
			recipes = append(recipes, focus(c, k, names[k-1]))
		}
	default:
		recipes = append(recipes, focus(active[0], in.MaxFactors, "Priorité : "))
		recipes = append(recipes, focus(active[1], in.MaxFactors, "Priorité : "))
		recipes = append(recipes, compromise())
		for _, c := range active[2:] {
			recipes = append(recipes, focus(c, in.MaxFactors, "Priorité : "))
		}
	}

	seen := map[string]bool{}
	for _, rc := range recipes {
		if len(out.Candidates)-1 >= in.Count {
			break
		}
		cand := buildCandidate(rc.insts, in.MaxFactors, in.Goal)
		if len(cand.Changes) == 0 && cand.Orientation == nil {
			continue
		}
		sig := candSig(cand)
		if seen[sig] {
			continue
		}
		seen[sig] = true
		cand.Index = len(out.Candidates)
		cand.ID = fmt.Sprintf("cand_%d", cand.Index)
		cand.Kind = "variant"
		cand.Label = rc.label
		cand.Rationale = rc.rationale
		out.Candidates = append(out.Candidates, cand)
	}
	if len(out.Candidates)-1 < in.Count {
		out.Notes = append(out.Notes, fmt.Sprintf("%d variante(s) distincte(s) possible(s) sur %d demandée(s) : les leviers disponibles sont épuisés", len(out.Candidates)-1, in.Count))
	}
	return out, nil
}

func nn(c Criterion) string {
	if c.NonNegotiable {
		return ", non négociable"
	}
	return ""
}

func candSig(c Candidate) string {
	var parts []string
	for _, o := range c.Overrides {
		parts = append(parts, fmt.Sprintf("%s/%s=%v", o.Target, o.Key, o.Value))
	}
	sort.Strings(parts)
	if c.Orientation != nil {
		parts = append(parts, fmt.Sprintf("orient=%.3f,%.3f,%.3f", c.Orientation.Up[0], c.Orientation.Up[1], c.Orientation.Up[2]))
	}
	return strings.Join(parts, "|")
}

// buildCandidate fusionne des leviers (même clé : fusion de compromis) et
// tronque à maxFactors facteurs.
func buildCandidate(insts []leverInst, maxFactors int, g *Goal) Candidate {
	banned := map[string]bool{}
	var notes []string
	for {
		c, cancelledKeys, cn := buildOnce(insts, maxFactors, banned)
		notes = append(notes, cn...)
		if len(cancelledKeys) == 0 || len(banned) > 20 {
			c.Notes = append(c.Notes, notes...)
			return c
		}
		for _, k := range cancelledKeys {
			banned[k] = true // les leviers opposés s'annulent : on libère la place pour d'autres leviers
		}
	}
}

func buildOnce(insts []leverInst, maxFactors int, banned map[string]bool) (Candidate, []string, []string) {
	c := Candidate{Overrides: []slicer.Override{}, Changes: []Change{}, Tradeoffs: []string{}}
	byKey := map[string]int{} // target/key -> index dans c.Changes
	var orient *leverInst
	nf := 0
	dead := map[int]bool{}
	var cancelled, cancelledKeys []string
	for i := range insts {
		li := insts[i]
		if li.orient != nil {
			if orient != nil || nf >= maxFactors {
				continue
			}
			orient = &insts[i]
			nf++
			continue
		}
		k := li.lever.Target + "/" + li.lever.Key
		if banned[k] {
			continue
		}
		if j, ok := byKey[k]; ok {
			if dead[j] {
				continue
			}
			c.mergeChange(j, li)
			if fmt.Sprint(displayVal(c.Overrides[j].Value)) == fmt.Sprint(c.Changes[j].From) {
				// leviers opposés qui s'annulent : le réglage revient à sa valeur de base
				dead[j] = true
				cancelledKeys = append(cancelledKeys, k)
				nf--
				cancelled = append(cancelled, fmt.Sprintf("%s : les leviers de « %s » et « %s » s'annulent, réglage laissé à sa valeur de base", c.Changes[j].Key, c.Changes[j].CriterionType, li.change.CriterionType))
			}
			continue
		}
		if nf >= maxFactors {
			continue
		}
		nf++
		byKey[k] = len(c.Changes)
		c.Changes = append(c.Changes, li.change)
		c.Overrides = append(c.Overrides, *li.ov)
	}
	if len(dead) > 0 {
		var ch []Change
		var ov []slicer.Override
		for j := range c.Changes {
			if !dead[j] {
				ch = append(ch, c.Changes[j])
				ov = append(ov, c.Overrides[j])
			}
		}
		c.Changes, c.Overrides = ch, ov
		if c.Changes == nil {
			c.Changes, c.Overrides = []Change{}, []slicer.Override{}
		}
	}
	if orient != nil {
		c.Orientation = orient.orient
		c.Changes = append(c.Changes, orient.change)
	}
	c.Factors = nf
	seenT := map[string]bool{}
	for _, ch := range c.Changes {
		if !seenT[ch.Tradeoff] {
			seenT[ch.Tradeoff] = true
			c.Tradeoffs = append(c.Tradeoffs, ch.Tradeoff)
		}
	}
	return c, cancelledKeys, cancelled
}

// mergeChange fusionne un second levier sur le même réglage.
func (c *Candidate) mergeChange(j int, li leverInst) {
	cur := &c.Changes[j]
	curNum := numOf(c.Overrides[j].Value)
	newNum := numOf(li.ov.Value)
	if li.num == nil || curNum == nil || newNum == nil || len(curNum) != len(newNum) {
		cur.Note = joinNote(cur.Note, fmt.Sprintf("réglage aussi visé par « %s » (%s) : valeur du critère prioritaire conservée", li.change.CriterionType, li.lever.ID))
		return
	}
	fromV := li.num.from
	opposite := (curNum[0]-fromV[0])*(newNum[0]-fromV[0]) < 0
	if !opposite {
		cur.Note = joinNote(cur.Note, fmt.Sprintf("même direction que « %s » : valeur du critère prioritaire conservée", li.change.CriterionType))
		return
	}
	mid := make([]float64, len(curNum))
	for i := range mid {
		mid[i] = (curNum[i] + newNum[i]) / 2
		if li.num.integer {
			mid[i] = math.Round(mid[i])
		}
	}
	val := formatList(mid, li.num.pct, li.lever.Op == "scale")
	c.Overrides[j].Value = val
	cur.To = displayVal(val)
	cur.Note = joinNote(cur.Note, fmt.Sprintf("compromis : valeur intermédiaire entre « %s » et « %s » (objectifs opposés sur ce réglage)", cur.CriterionType, li.change.CriterionType))
	cur.Tradeoff = cur.Tradeoff + " ; " + li.change.Tradeoff + " (effets partiellement atténués par la valeur intermédiaire)"
}

func joinNote(a, b string) string {
	if a == "" {
		return b
	}
	return a + " ; " + b
}

func numOf(v any) []float64 {
	switch t := v.(type) {
	case string:
		f, _, ok := parseNum(t)
		if !ok {
			return nil
		}
		return []float64{f}
	case []string:
		out := make([]float64, len(t))
		for i, s := range t {
			f, _, ok := parseNum(s)
			if !ok {
				return nil
			}
			out[i] = f
		}
		return out
	}
	return nil
}

func displayVal(v any) any {
	if l, ok := v.([]string); ok {
		if len(l) > 0 {
			same := true
			for _, x := range l {
				if x != l[0] {
					same = false
				}
			}
			if same {
				return l[0]
			}
		}
	}
	return v
}

func parseNum(s string) (v float64, pct bool, ok bool) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "%") {
		pct = true
		s = strings.TrimSuffix(s, "%")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false, false
	}
	return f, pct, true
}

func fmtNum(f float64, pct bool, roundInt bool) string {
	if roundInt {
		f = math.Round(f)
	} else {
		f = math.Round(f*10000) / 10000
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if pct {
		s += "%"
	}
	return s
}

func formatList(vs []float64, pct bool, roundInt bool) any {
	if len(vs) == 1 {
		return fmtNum(vs[0], pct, roundInt && math.Abs(vs[0]) >= 10)
	}
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = fmtNum(v, pct, roundInt && math.Abs(v) >= 10)
	}
	return out
}

func strsOf(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			s, ok := e.(string)
			if !ok {
				return nil
			}
			out = append(out, s)
		}
		return out
	}
	return nil
}

func whenOK(rule *CriterionRule, c Criterion, w *When) bool {
	if w == nil {
		return true
	}
	val := c.ParamString(w.Param)
	if val == "" {
		if sp, ok := rule.Params[w.Param]; ok && contains(sp.Values, "unknown") {
			val = "unknown"
		}
	}
	if len(w.In) > 0 && !contains(w.In, val) {
		return false
	}
	if len(w.NotIn) > 0 && contains(w.NotIn, val) {
		return false
	}
	return true
}

// resolveLever évalue un levier contre le profil de base. Renvoie nil et la
// raison s'il ne s'applique pas.
func resolveLever(in GenInput, c Criterion, l Lever, orientRep *mesh.OrientationReport) (*leverInst, string) {
	rule := in.Rules.Criteria[c.Type]
	if !whenOK(rule, c, l.When) {
		return nil, "condition non remplie (paramètre du critère)"
	}
	if !contains(l.Families, "*") {
		if in.Family == "" {
			return nil, "famille de matériau inconnue : levier dépendant du matériau non appliqué (aucun paramètre d'un autre matériau n'est transféré)"
		}
		if !contains(l.Families, in.Family) {
			return nil, fmt.Sprintf("levier non validé pour %s (validé : %s)", in.Family, strings.Join(l.Families, ", "))
		}
	}
	if l.Kind == "orientation" {
		return resolveOrientation(in, c, l, orientRep)
	}
	var cfg *profiles.Resolved
	switch l.Target {
	case "process":
		cfg = in.Process
	case "filament":
		cfg = in.Filament
	default:
		return nil, "cible " + l.Target + " non gérée par le générateur"
	}
	olds := strsOf(cfg.Config[l.Key])
	if len(olds) == 0 {
		return nil, fmt.Sprintf("clé %s absente du profil %s de base", l.Key, l.Target)
	}
	var news []string
	var nc *numChange
	if l.Op == "set" {
		sv, ok := l.Value.(string)
		if !ok {
			if f, isF := l.Value.(float64); isF {
				sv = strconv.FormatFloat(f, 'f', -1, 64)
			} else {
				return nil, "valeur de levier illisible"
			}
		}
		news = make([]string, len(olds))
		for i := range news {
			news[i] = sv
		}
	} else {
		step, ok := l.Value.(float64)
		if !ok {
			return nil, "pas de levier illisible"
		}
		var fromV, toV []float64
		pct := false
		integer := true
		for _, o := range olds {
			f, p, ok := parseNum(o)
			if !ok {
				return nil, fmt.Sprintf("valeur actuelle %q non numérique", o)
			}
			pct = p
			if strings.Contains(o, ".") {
				integer = false
			}
			nv := f + step
			if l.Op == "scale" {
				nv = f * step
			}
			if l.Min != nil && nv < *l.Min {
				nv = *l.Min
			}
			if l.Max != nil && nv > *l.Max {
				nv = *l.Max
			}
			if l.MaxNozzleFraction > 0 {
				if hi := l.MaxNozzleFraction * in.Nozzle; nv > hi {
					nv = math.Floor(hi*100) / 100
				}
			}
			if l.BoundKey != "" {
				if b := strsOf(cfg.Config[l.BoundKey]); len(b) > 0 {
					if bv, _, ok := parseNum(b[0]); ok && nv > bv {
						nv = bv
					}
				} else {
					return nil, "borne " + l.BoundKey + " absente du profil : levier non appliqué"
				}
			}
			fromV = append(fromV, f)
			toV = append(toV, nv)
		}
		for _, v := range toV {
			news = append(news, fmtNum(v, pct, l.Op == "scale" && math.Abs(v) >= 10))
		}
		nc = &numChange{from: fromV, to: toV, pct: pct, integer: integer && !pct}
	}
	same := len(news) == len(olds)
	for i := range news {
		if same && news[i] != olds[i] {
			same = false
		}
	}
	if same {
		return nil, fmt.Sprintf("déjà à la valeur visée ou à la borne (%s = %s)", l.Key, strings.Join(olds, "/"))
	}
	var val any = news
	if allSame(news) {
		val = news[0]
		if len(olds) > 1 {
			val = news[0] // diffusé par le slicer sur toutes les entrées de la liste
		}
	}
	ov := slicer.Override{Key: l.Key, Value: val, Target: l.Target}
	li := &leverInst{crit: c, lever: l, ov: &ov, num: nc}
	li.change = Change{
		Criterion: c.ID, CriterionType: c.Type, Lever: l.ID, Key: l.Key, Target: l.Target,
		From: shortList(olds), To: shortList(news), Effect: l.Effect, Tradeoff: l.Tradeoff,
	}
	if len(news) > 1 && !allSame(news) {
		ov.Value = news
	}
	return li, ""
}

func allSame(l []string) bool {
	for _, x := range l {
		if x != l[0] {
			return false
		}
	}
	return len(l) > 0
}

func shortList(l []string) any {
	if allSame(l) && len(l) > 0 {
		return l[0]
	}
	return l
}

func resolveOrientation(in GenInput, c Criterion, l Lever, rep *mesh.OrientationReport) (*leverInst, string) {
	if rep == nil || len(rep.Candidates) == 0 {
		return nil, "géométrie indisponible : aucune orientation évaluée"
	}
	var cur *mesh.OrientationCandidate
	for i := range rep.Candidates {
		if rep.Candidates[i].Source == "current" {
			cur = &rep.Candidates[i]
		}
	}
	var best *mesh.OrientationCandidate
	pick := func(ok func(mesh.OrientationCandidate) bool, better func(a, b mesh.OrientationCandidate) bool) {
		for i := range rep.Candidates {
			cd := rep.Candidates[i]
			if !cd.Fits || !ok(cd) {
				continue
			}
			if best == nil || better(cd, *best) {
				cp := cd
				best = &cp
			}
		}
	}
	switch l.Mode {
	case "load_in_plane":
		axis := [3]float64{0, 0, 1}
		if a, ok := c.LoadAxis(); ok {
			axis = a
		}
		an := mesh.Vec3{X: axis[0], Y: axis[1], Z: axis[2]}.Norm()
		if an.Len() == 0 {
			return nil, "axe d'effort nul"
		}
		inPlane := func(cd mesh.OrientationCandidate) bool { return math.Abs(cd.Up.Norm().Dot(an)) <= 0.5 }
		if cur != nil && inPlane(*cur) {
			return nil, "orientation actuelle : effort déjà dans le plan des couches"
		}
		pick(inPlane, func(a, b mesh.OrientationCandidate) bool { return a.Score < b.Score })
	case "max_contact":
		pick(func(mesh.OrientationCandidate) bool { return true }, func(a, b mesh.OrientationCandidate) bool { return a.ContactMM2 > b.ContactMM2 })
		if best != nil && cur != nil && best.ContactMM2 <= cur.ContactMM2*1.02 {
			return nil, "orientation actuelle : surface de contact déjà quasi maximale"
		}
	case "min_overhang":
		pick(func(mesh.OrientationCandidate) bool { return true }, func(a, b mesh.OrientationCandidate) bool { return a.OverhangFrac < b.OverhangFrac })
		if best != nil && cur != nil && best.OverhangFrac >= cur.OverhangFrac-0.005 {
			return nil, "orientation actuelle : surplombs déjà quasi minimaux"
		}
	}
	if best == nil {
		return nil, "aucune orientation compatible qui tienne sur le plateau"
	}
	spec := &OrientationSpec{Mode: l.Mode, Label: best.Label, Up: [3]float64{best.Up.X, best.Up.Y, best.Up.Z},
		Rotation: [12]float64(best.Rotation), HeightMM: best.HeightMM, BedContactMM2: best.ContactMM2, OverhangFraction: best.OverhangFrac}
	li := &leverInst{crit: c, lever: l, orient: spec}
	li.change = Change{
		Criterion: c.ID, CriterionType: c.Type, Lever: l.ID, Key: "orientation",
		From: "orientation actuelle", To: best.Label, Effect: l.Effect, Tradeoff: l.Tradeoff,
		Note: "heuristique de internal/mesh (surplomb, appui, hauteur) ; ignore l'aspect des faces visibles",
	}
	return li, ""
}
