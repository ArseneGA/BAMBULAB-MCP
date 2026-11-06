package contracts

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Convention des champs secrets (params/résultats de travaux, journaux) : toute
// clé « secret_* », « password » ou « access_code » (casse et tirets ignorés).
// Un secret ne doit JAMAIS être stocké dans les params d'un travail : le handler
// le lit dans la configuration ou le coffre. Ce filtre n'est qu'une seconde ligne
// de défense à l'affichage.

// IsSecretKey dit si une clé JSON désigne un secret.
func IsSecretKey(k string) bool {
	l := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(k)), "-", "_")
	return strings.HasPrefix(l, "secret_") || l == "password" || l == "access_code" || l == "accesscode"
}

// ContainsSecret dit si un JSON contient (à n'importe quelle profondeur) une clé secrète.
func ContainsSecret(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	return hasSecret(v)
}

func hasSecret(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if IsSecretKey(k) || hasSecret(e) {
				return true
			}
		}
	case []any:
		for _, e := range x {
			if hasSecret(e) {
				return true
			}
		}
	}
	return false
}

// RedactJSON retire les clés secrètes d'un JSON. Un JSON invalide est renvoyé tel quel.
func RedactJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || !ContainsSecret(raw) {
		return raw
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return raw
	}
	out, err := json.Marshal(redact(v))
	if err != nil {
		return raw
	}
	return out
}

func redact(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			if !IsSecretKey(k) {
				m[k] = redact(e)
			}
		}
		return m
	case []any:
		s := make([]any, len(x))
		for i, e := range x {
			s[i] = redact(e)
		}
		return s
	}
	return v
}

// Redacted renvoie une copie du travail sans champs secrets (params, résultat) :
// à utiliser pour toute sortie vers un client (API HTTP, outils MCP).
func (j Job) Redacted() Job {
	j.Params = RedactJSON(j.Params)
	j.Result = RedactJSON(j.Result)
	return j
}
