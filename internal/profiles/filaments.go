package profiles

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// FilamentEntry est une ligne de filament proposée pour une machine (un preset par « ligne » de produit).
type FilamentEntry struct {
	Name   string `json:"name"`   // preset à utiliser (« Bambu PETG Translucent @BBL X1C »)
	Line   string `json:"line"`   // ligne de produit sans suffixe machine (« Bambu PETG Translucent »)
	Vendor string `json:"vendor"` // « Bambu Lab », « Generic »…
	Type   string `json:"type"`   // matière du preset (filament_type) : PLA, PETG…
	Family string `json:"family"` // regroupement : PLA, PETG, ABS/ASA, TPU, PA/PC, Support, Autre
	Color  string `json:"color,omitempty"`
	Match  string `json:"match,omitempty"`
	Note   string `json:"note,omitempty"`
}

var (
	filCacheMu sync.Mutex
	filCache   = map[*Store]map[string][]FilamentEntry{}
)

func normNozzle(m *Resolved) string {
	nz := m.Strings("nozzle_diameter")
	if len(nz) == 0 {
		return ""
	}
	return strings.TrimSpace(nz[0])
}

// nozzleOK : un nom « … 0.6 nozzle » ne vaut que pour une buse de 0,6 ; sans suffixe de buse il vaut pour 0,4.
func nozzleOK(name, nozzle string) bool {
	i := strings.LastIndex(name, " nozzle")
	if i < 0 {
		return nozzle == "0.4" || nozzle == ""
	}
	j := strings.LastIndex(name[:i], " ")
	return name[j+1:i] == nozzle
}

func familyOfType(t string) string {
	u := strings.ToUpper(t)
	switch {
	case strings.HasPrefix(u, "PLA"):
		return "PLA"
	case strings.HasPrefix(u, "PETG"), u == "PET":
		return "PETG"
	case strings.HasPrefix(u, "ABS"), strings.HasPrefix(u, "ASA"):
		return "ABS/ASA"
	case strings.HasPrefix(u, "TPU"), strings.HasPrefix(u, "TPE"):
		return "TPU"
	case strings.HasPrefix(u, "PA"), strings.HasPrefix(u, "PC"), strings.HasPrefix(u, "PPS"):
		return "PA/PC"
	case strings.Contains(u, "SUPPORT"), strings.HasPrefix(u, "PVA"), strings.HasPrefix(u, "HIPS"):
		return "Support"
	}
	return "Autre"
}

// ListFilaments liste, pour une machine, UN preset par ligne de produit compatible (le plus propre à la machine
// et à sa buse), regroupable par Family. Triée par famille puis nom.
func (s *Store) ListFilaments(machine string) ([]FilamentEntry, error) {
	filCacheMu.Lock()
	if c, ok := filCache[s][machine]; ok {
		filCacheMu.Unlock()
		return append([]FilamentEntry(nil), c...), nil
	}
	filCacheMu.Unlock()
	m, err := s.Resolve(KindMachine, machine)
	if err != nil {
		return nil, err
	}
	model, _ := m.String("printer_model")
	code := MachineCode(model)
	nozzle := normNozzle(m)
	names, err := s.CompatibleFilaments(machine)
	if err != nil {
		return nil, err
	}
	type cand struct {
		name  string
		score Match
	}
	best := map[string]cand{}
	for _, n := range names {
		p, ok := s.Get(KindFilament, n)
		if ok && !p.Instantiation {
			continue
		}
		if !nozzleOK(n, nozzle) {
			continue
		}
		line := presetBase(n)
		sc, ok2, err := s.matchFor(KindFilament, n, m, code, model)
		if err != nil || !ok2 {
			continue
		}
		if c, have := best[line]; !have || sc < c.score || (sc == c.score && len(n) < len(c.name)) {
			best[line] = cand{n, sc}
		}
	}
	var out []FilamentEntry
	for line, c := range best {
		r, err := s.Resolve(KindFilament, c.name)
		if err != nil {
			continue
		}
		typ := ""
		if t := r.Strings("filament_type"); len(t) > 0 {
			typ = t[0]
		}
		vendor := ""
		if v := r.Strings("filament_vendor"); len(v) > 0 {
			vendor = v[0]
		}
		e := FilamentEntry{Name: c.name, Line: line, Vendor: vendor, Type: typ, Family: familyOfType(typ), Match: c.score.String()}
		if pc := presetCode(c.name); pc != "" && pc != code {
			e.Note = "preset dessiné pour " + pc + ", déclaré compatible avec " + code
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Family != out[j].Family {
			return out[i].Family < out[j].Family
		}
		return out[i].Line < out[j].Line
	})
	filCacheMu.Lock()
	if filCache[s] == nil {
		filCache[s] = map[string][]FilamentEntry{}
	}
	filCache[s][machine] = out
	filCacheMu.Unlock()
	return append([]FilamentEntry(nil), out...), nil
}

// ---- résolution tolérante d'un nom de matière ----

// synonymes (normalisés sans accent) → jeton canonique des noms de presets Bambu.
var filSynonyms = map[string]string{
	"transparent": "translucent", "transparente": "translucent", "translucide": "translucent", "translucent": "translucent",
	"clear": "translucent", "clair": "translucent", "cristal": "translucent", "natural": "translucent", "naturel": "translucent",
	"mat": "matte", "mate": "matte", "matte": "matte", "matt": "matte",
	"soie": "silk", "soyeux": "silk", "silk": "silk", "satine": "silk",
	"basique": "basic", "basic": "basic", "standard": "basic", "classique": "basic",
	"carbone": "cf", "carbon": "cf", "cf": "cf", "fibre": "", "de": "", "du": "", "la": "", "le": "", "en": "", "d": "",
	"rapide": "hf", "hf": "hf", "speed": "hf", "high": "hf", "vitesse": "hf", "haute": "hf",
	"bois": "wood", "wood": "wood", "paillette": "sparkle", "paillettes": "sparkle", "sparkle": "sparkle",
	"glow": "glow", "phosphorescent": "glow", "fluo": "glow",
	"flexible": "tpu", "souple": "tpu", "caoutchouc": "tpu",
	"filament": "", "bambu": "", "lab": "", "generic": "", "generique": "", "bobine": "", "pla+": "plus", "plus": "plus",
	"support": "support", "supports": "support", "soluble": "pva",
	"glass": "gf", "verre": "gf", "gf": "gf", "aero": "aero", "metal": "metal", "marbre": "marble", "marble": "marble",
	"galaxy": "galaxy", "galaxie": "galaxy", "gradient": "gradient", "degrade": "gradient",
}

func stripAccents(s string) string {
	r := strings.NewReplacer("é", "e", "è", "e", "ê", "e", "ë", "e", "à", "a", "â", "a", "î", "i", "ï", "i", "ô", "o", "ö", "o", "ù", "u", "û", "u", "ü", "u", "ç", "c")
	return r.Replace(s)
}

func tokenize(s string) []string {
	s = stripAccents(strings.ToLower(s))
	var cur strings.Builder
	var out []string
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '+' {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

var materialTokens = map[string]bool{"pla": true, "petg": true, "abs": true, "asa": true, "tpu": true, "pc": true, "pa": true, "pet": true, "pva": true, "hips": true, "ppa": true, "pps": true}

// canonTokens ramène un libellé libre à des jetons canoniques (matière + qualificatifs), sans doublons.
func canonTokens(s string) (material string, mods []string) {
	seen := map[string]bool{}
	for _, t := range tokenize(s) {
		if c, ok := filSynonyms[t]; ok {
			t = c
		}
		if t == "" {
			continue
		}
		// « petg-cf » arrive découpé en petg, cf ; « pla-cf » idem
		if materialTokens[t] {
			if material == "" {
				material = t
			}
			continue
		}
		if t == "tpu" {
			material = "tpu"
			continue
		}
		if !seen[t] {
			seen[t] = true
			mods = append(mods, t)
		}
	}
	return
}

// FilamentResolution est le résultat d'une résolution de nom de matière.
type FilamentResolution struct {
	Entry FilamentEntry `json:"entry"`
	// Note : réserve à afficher (variante demandée absente → repli, preset d'un autre modèle…).
	Note string `json:"note,omitempty"`
}

// FilamentNotFound porte les 5 noms les plus proches.
type FilamentNotFound struct {
	Query   string
	Closest []string
}

func (e *FilamentNotFound) Error() string {
	return fmt.Sprintf("matière %q non reconnue ; les 5 plus proches : %s (liste complète : list_filaments)", e.Query, strings.Join(e.Closest, ", "))
}

func entryTokens(e FilamentEntry) (string, map[string]bool) {
	mat, mods := canonTokens(strings.TrimPrefix(strings.TrimPrefix(e.Line, "Bambu "), "Generic "))
	if mat == "" {
		mat = strings.ToLower(e.Type)
	}
	m := map[string]bool{}
	for _, x := range mods {
		m[x] = true
	}
	return mat, m
}

// ResolveFilament trouve le preset filament d'une machine à partir d'un nom libre (« PETG transparent »,
// « pla mat », « Bambu PLA Basic », un nom de preset exact…). Les synonymes français/anglais sont reconnus ;
// si la variante demandée n'est pas installée, repli sur la variante de base de la matière avec une note.
func (s *Store) ResolveFilament(machine, query string) (FilamentResolution, error) {
	list, err := s.ListFilaments(machine)
	if err != nil {
		return FilamentResolution{}, err
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return FilamentResolution{}, fmt.Errorf("%w: nom de matière vide", ErrNotFound)
	}
	for _, e := range list { // nom exact (preset ou ligne), insensible à la casse
		if strings.EqualFold(e.Name, q) || strings.EqualFold(e.Line, q) {
			return FilamentResolution{Entry: e, Note: e.Note}, nil
		}
	}
	qmat, qmods := canonTokens(q)
	if qmat == "" {
		return FilamentResolution{}, &FilamentNotFound{Query: q, Closest: s.closestFilaments(list, q, qmat, qmods)}
	}
	type scored struct {
		e     FilamentEntry
		score int
	}
	var cands []scored
	for _, e := range list {
		emat, emods := entryTokens(e)
		if emat != qmat {
			continue
		}
		sc := 0
		hit := 0
		for _, m := range qmods {
			if emods[m] {
				hit++
			}
		}
		if hit != len(qmods) {
			continue
		}
		sc = 100 - 10*(len(emods)-hit) // moins de qualificatifs en trop = mieux
		if e.Vendor == "Bambu Lab" {
			sc += 3
		}
		if emods["basic"] {
			sc += 2
		}
		cands = append(cands, scored{e, sc})
	}
	if len(cands) > 0 {
		sort.Slice(cands, func(i, j int) bool {
			if cands[i].score != cands[j].score {
				return cands[i].score > cands[j].score
			}
			return cands[i].e.Line < cands[j].e.Line
		})
		return FilamentResolution{Entry: cands[0].e, Note: cands[0].e.Note}, nil
	}
	// variante demandée absente : repli sur la variante de base de la matière, signalé
	if len(qmods) > 0 {
		base := q
		fb, err := s.ResolveFilament(machine, strings.ToUpper(qmat))
		if err == nil {
			fb.Note = strings.TrimSpace(fmt.Sprintf("la variante « %s » n'est pas installée pour cette imprimante : repli sur « %s » (vérifier que cela convient). %s", base, fb.Entry.Line, fb.Note))
			return fb, nil
		}
	}
	return FilamentResolution{}, &FilamentNotFound{Query: q, Closest: s.closestFilaments(list, q, qmat, qmods)}
}

func (s *Store) closestFilaments(list []FilamentEntry, q, qmat string, qmods []string) []string {
	type sc struct {
		line string
		d    float64
	}
	nq := strings.Join(tokenize(q), " ")
	var all []sc
	for _, e := range list {
		d := float64(levenshtein(nq, strings.Join(tokenize(e.Line), " ")))
		emat, emods := entryTokens(e)
		if qmat != "" && emat == qmat {
			d -= 6
			for _, m := range qmods {
				if emods[m] {
					d -= 3
				}
			}
		}
		all = append(all, sc{e.Line, d})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].d != all[j].d {
			return all[i].d < all[j].d
		}
		return all[i].line < all[j].line
	})
	var out []string
	for _, a := range all {
		out = append(out, a.line)
		if len(out) == 5 {
			break
		}
	}
	return out
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			c := 1
			if ra[i-1] == rb[j-1] {
				c = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+c)
		}
		prev = cur
	}
	return prev[len(rb)]
}
