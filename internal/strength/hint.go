package strength

import (
	"strings"
	"unicode"
)

// Heuristique de la proposition « cette pièce va-t-elle porter une charge ? ».
// Une pièce semble porteuse si :
//   - son gabarit est un support, une équerre, un crochet ou un « porte-… » ;
//   - son nom ou ses étiquettes contiennent support, porte, crochet, étagère, fixation, bras, charnière (et équivalents) ;
//   - la demande contient explicitement résistance, charge, supporter, porter, tenir… (texte libre fourni).
// Elle est volontairement large : mieux vaut proposer un test de trop qu'oublier une pièce qui va porter.

// Mots (préfixes de mots entiers : « support » trouve « supports », mais « porte » ne trouve pas « importer »).
var loadBearingPrefixes = []string{
	"support", "equerre", "équerre", "bracket", "crochet", "hook", "porte", "etagere", "étagère", "shelf",
	"fixation", "bras", "charniere", "charnière", "hinge", "console", "patere", "patère", "tablette", "suspension", "accroche",
	"holder", "collier", "clamp", "poignee", "poignée", "handle", "anse",
}

// Mots entiers seulement.
var loadBearingExact = []string{"stand", "mount", "rack", "arm"}

var explicitPrefixes = []string{"resistance", "résistance", "charge", "supporter", "porter", "tenir", "poids", "solide", "casse", "load", "strength", "weight", "kg"}

func tokens(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r))
	})
}

// LooksLoadBearing dit si la pièce semble porteuse. texts : gabarit, nom, étiquettes, demande.
func LooksLoadBearing(texts ...string) bool {
	for _, tok := range tokens(strings.Join(texts, " ")) {
		for _, w := range loadBearingPrefixes {
			if strings.HasPrefix(tok, w) {
				return true
			}
		}
		for _, w := range loadBearingExact {
			if tok == w {
				return true
			}
		}
		for _, w := range explicitPrefixes {
			if strings.HasPrefix(tok, w) {
				return true
			}
		}
	}
	return false
}

// SuggestionText : phrase ajoutée au texte rendu au modèle.
const SuggestionText = "Cette pièce va-t-elle porter une charge ? Je peux lancer un test de résistance (assess_strength) : je te poserai quelques questions (plan_strength_test), et je ne devine jamais les charges sans te le dire."

// InstructionText : consigne des instructions du serveur.
const InstructionText = "Quand une pièce créée, modifiée ou importée semble porter une charge (support, crochet, bras, étagère, fixation...), propose à l'humain un test de résistance : appelle plan_strength_test, pose ses questions UNE par une en français simple, puis assess_strength. " +
	"Ne jamais inventer les charges en silence : tu peux SUPPOSER une valeur raisonnable (guitare 3,5 à 4,5 kg, etc.) mais annonce-la comme hypothèse, laisse l'humain la corriger, et marque-la source « assumed » ; le verdict dit alors qu'il repose sur des hypothèses. " +
	"Le résultat est un calcul simplifié qui ne remplace pas un essai réel."
