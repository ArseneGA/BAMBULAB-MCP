package profiles

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// EvalCondition évalue une condition de compatibilité de Bambu Studio sur une
// configuration (machine). Sous-ensemble supporté : identifiants de clés avec
// indice optionnel (nozzle_diameter[0]), chaînes "…", nombres, regex /…/,
// opérateurs == != < <= > >= =~ !~, and/or/not, &&, ||, !, parenthèses.
// Toute syntaxe inconnue retourne une erreur plutôt qu'un résultat deviné.
func EvalCondition(expr string, cfg map[string]any) (bool, error) {
	p := &condParser{src: expr, cfg: cfg}
	if err := p.lex(); err != nil {
		return false, err
	}
	v, err := p.parseOr()
	if err != nil {
		return false, err
	}
	if p.pos != len(p.toks) {
		return false, fmt.Errorf("condition %q: jeton inattendu %q", expr, p.toks[p.pos].s)
	}
	return truthy(v), nil
}

type tokKind int

const (
	tIdent tokKind = iota
	tStr
	tNum
	tRegex
	tOp
)

type token struct {
	k tokKind
	s string
}

type condParser struct {
	src  string
	cfg  map[string]any
	toks []token
	pos  int
}

func (p *condParser) lex() error {
	s := p.src
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(s) && s[j] != c {
				j++
			}
			if j >= len(s) {
				return fmt.Errorf("condition %q: chaîne non fermée", p.src)
			}
			p.toks = append(p.toks, token{tStr, s[i+1 : j]})
			i = j + 1
		case c == '/':
			j := i + 1
			for j < len(s) && s[j] != '/' {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(s) {
				return fmt.Errorf("condition %q: regex non fermée", p.src)
			}
			p.toks = append(p.toks, token{tRegex, s[i+1 : j]})
			i = j + 1
		case c >= '0' && c <= '9' || (c == '-' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9'):
			j := i + 1
			for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.') {
				j++
			}
			p.toks = append(p.toks, token{tNum, s[i:j]})
			i = j
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			j := i + 1
			for j < len(s) && (s[j] == '_' || s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z' || s[j] >= '0' && s[j] <= '9') {
				j++
			}
			// indice [n]
			if j < len(s) && s[j] == '[' {
				k := strings.IndexByte(s[j:], ']')
				if k < 0 {
					return fmt.Errorf("condition %q: [ non fermé", p.src)
				}
				j += k + 1
			}
			p.toks = append(p.toks, token{tIdent, s[i:j]})
			i = j
		default:
			two := ""
			if i+1 < len(s) {
				two = s[i : i+2]
			}
			switch two {
			case "==", "!=", "<=", ">=", "=~", "!~", "&&", "||":
				p.toks = append(p.toks, token{tOp, two})
				i += 2
				continue
			}
			switch c {
			case '<', '>', '!', '(', ')':
				p.toks = append(p.toks, token{tOp, string(c)})
				i++
			default:
				return fmt.Errorf("condition %q: caractère inattendu %q", p.src, string(c))
			}
		}
	}
	return nil
}

func (p *condParser) peek() *token {
	if p.pos < len(p.toks) {
		return &p.toks[p.pos]
	}
	return nil
}

func (p *condParser) isOp(t *token, ops ...string) bool {
	if t == nil {
		return false
	}
	if t.k == tIdent && (t.s == "and" || t.s == "or" || t.s == "not") {
		for _, o := range ops {
			if o == t.s {
				return true
			}
		}
		return false
	}
	if t.k != tOp {
		return false
	}
	for _, o := range ops {
		if o == t.s {
			return true
		}
	}
	return false
}

func (p *condParser) parseOr() (any, error) {
	l, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.isOp(p.peek(), "||", "or") {
		p.pos++
		r, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l = truthy(l) || truthy(r)
	}
	return l, nil
}

func (p *condParser) parseAnd() (any, error) {
	l, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.isOp(p.peek(), "&&", "and") {
		p.pos++
		r, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		l = truthy(l) && truthy(r)
	}
	return l, nil
}

func (p *condParser) parseNot() (any, error) {
	if p.isOp(p.peek(), "!", "not") {
		p.pos++
		v, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return !truthy(v), nil
	}
	return p.parseCmp()
}

func (p *condParser) parseCmp() (any, error) {
	l, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	t := p.peek()
	if t == nil || t.k != tOp {
		return l, nil
	}
	switch t.s {
	case "==", "!=", "<", "<=", ">", ">=", "=~", "!~":
	default:
		return l, nil
	}
	p.pos++
	if t.s == "=~" || t.s == "!~" {
		rt := p.peek()
		if rt == nil || rt.k != tRegex {
			return nil, fmt.Errorf("condition %q: regex attendue après %s", p.src, t.s)
		}
		p.pos++
		re, err := regexp.Compile(rt.s)
		if err != nil {
			return nil, fmt.Errorf("condition %q: %w", p.src, err)
		}
		m := re.MatchString(toStr(l))
		if t.s == "!~" {
			m = !m
		}
		return m, nil
	}
	r, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	lf, lok := toNum(l)
	rf, rok := toNum(r)
	if lok && rok {
		switch t.s {
		case "==":
			return lf == rf, nil
		case "!=":
			return lf != rf, nil
		case "<":
			return lf < rf, nil
		case "<=":
			return lf <= rf, nil
		case ">":
			return lf > rf, nil
		case ">=":
			return lf >= rf, nil
		}
	}
	ls, rs := toStr(l), toStr(r)
	switch t.s {
	case "==":
		return ls == rs, nil
	case "!=":
		return ls != rs, nil
	}
	return nil, fmt.Errorf("condition %q: comparaison %s sur des non-nombres", p.src, t.s)
}

func (p *condParser) parsePrimary() (any, error) {
	t := p.peek()
	if t == nil {
		return nil, fmt.Errorf("condition %q: fin inattendue", p.src)
	}
	p.pos++
	switch t.k {
	case tStr:
		return t.s, nil
	case tNum:
		f, err := strconv.ParseFloat(t.s, 64)
		if err != nil {
			return nil, err
		}
		return f, nil
	case tIdent:
		switch t.s {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return p.lookup(t.s), nil
	case tOp:
		if t.s == "(" {
			v, err := p.parseOr()
			if err != nil {
				return nil, err
			}
			if c := p.peek(); c == nil || c.s != ")" {
				return nil, fmt.Errorf("condition %q: ) manquante", p.src)
			}
			p.pos++
			return v, nil
		}
	}
	return nil, fmt.Errorf("condition %q: jeton inattendu %q", p.src, t.s)
}

func (p *condParser) lookup(id string) any {
	idx := -1
	if i := strings.IndexByte(id, '['); i >= 0 {
		idx, _ = strconv.Atoi(strings.Trim(id[i:], "[]"))
		id = id[:i]
	}
	v, ok := p.cfg[id]
	if !ok {
		return ""
	}
	if l, ok := v.([]any); ok {
		if idx < 0 {
			idx = 0
		}
		if idx < len(l) {
			return l[idx]
		}
		return ""
	}
	return v
}

func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t != "" && t != "0"
	case float64:
		return t != 0
	}
	return v != nil
}

func toStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "1"
		}
		return "0"
	}
	return fmt.Sprint(v)
}

func toNum(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil
	}
	return 0, false
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	}
	return ""
}
