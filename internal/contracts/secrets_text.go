package contracts

import (
	"regexp"
	"strings"
)

// Masked remplace toute valeur secrète retirée d'un texte.
const Masked = "[masqué]"

// RedactText retire d'un TEXTE LIBRE (message d'erreur, constat d'un agent, note) tout ce qui ressemble à un secret
// avant qu'il soit stocké ou renvoyé : codes de lancement, codes d'accès, mots de passe, jetons d'URL (/upload/<jeton>,
// ?token=, ?sig=), en-têtes Authorization, identifiants dans une URL, clés d'API et longues chaînes opaques.
// C'est le pendant, pour le texte, de RedactJSON (qui filtre les clés d'un JSON). Heuristique prudente : mieux vaut
// masquer un mot de trop qu'enregistrer un secret. Une chaîne vide ou propre est renvoyée telle quelle.
func RedactText(s string) string {
	if s == "" {
		return s
	}
	out := reURLUserinfo.ReplaceAllString(s, "${1}"+Masked+"@")
	out = reUploadToken.ReplaceAllString(out, "${1}"+Masked)
	out = reAuthHeader.ReplaceAllString(out, "${1} "+Masked)
	out = reSecretPair.ReplaceAllString(out, "${1}${2}"+Masked)
	out = reCodePair.ReplaceAllString(out, "${1}${2}"+Masked)
	out = reCodePhrase.ReplaceAllString(out, "${1}${2}"+Masked)
	out = reKnownKey.ReplaceAllString(out, Masked)
	out = reOpaque.ReplaceAllStringFunc(out, func(m string) string {
		if looksOpaqueToken(m) {
			return Masked
		}
		return m
	})
	return out
}

var (
	reURLUserinfo = regexp.MustCompile(`(://)[^/\s:@]+:[^/\s@]+@`)
	reUploadToken = regexp.MustCompile(`(/upload/)[A-Za-z0-9_-]{16,}`)
	reAuthHeader  = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
	// nom évocateur d'un secret suivi de « : » ou « = » puis de sa valeur (JSON, URL, ligne de configuration, phrase).
	reSecretPair = regexp.MustCompile(`(?i)\b([\w-]*(?:token|jeton|secret|passwd|password|pwd|api[_-]?key|apikey|access[_-]?code|launch[_-]?code|code[_-]?de[_-]?lancement|signature|authorization)[\w-]*|mot de passe|sig)(["']?\s*[:=]\s*["']?)[^\s"',;&]+`)
	// « code » seul : seulement une valeur d'au moins 4 chiffres (un code HTTP à 3 chiffres reste lisible).
	reCodePair   = regexp.MustCompile(`(?i)\b(code)(["']?\s*[:=]\s*["']?)\d{4,}`)
	reCodePhrase = regexp.MustCompile(`(?i)(code de lancement|launch code|code d['’]acc[eè]s|access code)(\D{0,12}?)\d{4,}`)
	reKnownKey   = regexp.MustCompile(`\b(?:sk|pk|ghp|gho|ghs|github_pat|xox[a-z])[-_][A-Za-z0-9_-]{16,}`)
	reOpaque     = regexp.MustCompile(`[A-Za-z0-9_-]{32,}`)
)

// looksOpaqueToken : chaîne longue mêlant majuscules, minuscules et chiffres (jeton base64, clé) ; un identifiant en
// hexadécimal (empreinte sha256, id de travail) ou un nom de fichier en minuscules n'est pas un secret.
func looksOpaqueToken(m string) bool {
	var up, lo, dg bool
	for _, c := range m {
		switch {
		case c >= 'A' && c <= 'Z':
			up = true
		case c >= 'a' && c <= 'z':
			lo = true
		case c >= '0' && c <= '9':
			dg = true
		}
	}
	return up && lo && dg && !strings.ContainsAny(m, " ")
}
