package printer

import (
	"fmt"
	"strings"
)

// Table HMS Bambu (alertes du rapport MQTT : attr + code, 16 chiffres hexadécimaux « AAAA_BBBB_CCCC_DDDD »).
//
// HONNÊTETÉ DES SOURCES. Cette table n'a PAS pu être vérifiée contre le wiki officiel depuis l'environnement de
// développement (la page https://wiki.bambulab.com/... a répondu HTTP 402 à la récupération automatique). Ce qui suit
// vient de deux sources :
//   - « terrain » : les codes réellement observés sur la P1S de l'utilisateur lors d'une soirée d'impression
//     (0300-0D00-0002-0005, 07FF-5000-0002-0001, 07FF-7000-0002-0008, print_error 0300-4000 et 07FF-8012). La
//     FAMILLE (module + classe) est certaine puisqu'elle vient du rapport ; le texte français est une
//     interprétation prudente, marquée « à vérifier » ;
//   - « mémoire » : familles connues par les bases ouvertes (wiki Bambu « HMS », ha-bambulab hms_error_text,
//     OpenBambuAPI mqtt.md), écrites de mémoire sans pouvoir les relire : TOUTES marquées « à vérifier ».
//
// Aucune entrée « mémoire » n'est donnée pour exacte. Un code absent n'est jamais deviné : DescribeCode rend « code
// X (inconnu) » avec le lien vers le wiki. Les liens `hmscode/<code>` suivent le format indiqué par l'utilisateur
// (https://wiki.bambulab.com/en/x1/troubleshooting/hmscode/<code>) : le FORMAT EXACT (séparateurs « _ » ou « - »,
// casse, préfixe x1/p1/a1) n'est pas vérifié ; en cas de page introuvable, chercher le code sur WikiURL.

// HMSWikiPrefix : préfixe supposé des pages de code HMS du wiki (format NON VÉRIFIÉ, voir ci-dessus).
const HMSWikiPrefix = "https://wiki.bambulab.com/en/x1/troubleshooting/hmscode/"

// Severity : gravité lue dans le champ « code » d'une alerte HMS (bits 16 à 19 : 1 fatale, 2 grave, 3 courante,
// 4 information ; convention de ha-bambulab, à vérifier).
func hmsSeverity(code uint32) string {
	switch (code >> 16) & 0xF {
	case 1:
		return "fatale"
	case 2:
		return "grave"
	case 3:
		return "courante"
	case 4:
		return "information"
	}
	return ""
}

type hmsEntry struct {
	text, action string
	source       string // d'où vient l'entrée
	verified     bool   // jamais vrai ici : voir l'en-tête
}

const (
	srcField  = "terrain (P1S de l'utilisateur) ; texte à vérifier sur le wiki Bambu"
	srcMemory = "mémoire (wiki Bambu / ha-bambulab / OpenBambuAPI) ; à vérifier"
)

// hmsExact : alertes complètes (16 chiffres, sans séparateur).
var hmsExact = map[string]hmsEntry{
	"03000D0000020005": {source: srcField,
		text:   "la mise à l'origine (homing) du plateau / de l'axe Z a échoué pendant la préparation : l'imprimante a abandonné (à vérifier)",
		action: "vérifiez que rien ne gêne le plateau ni l'axe Z, que la buse est propre (pas de filament collé), puis acquittez l'erreur sur l'écran ou redémarrez l'imprimante avant de relancer"},
	"07FF500000020001": {source: srcField,
		text:   "bobine externe (hors AMS) : problème de filament signalé par l'imprimante, pause en attente d'une action sur l'écran (à vérifier)",
		action: "sur l'écran de l'imprimante : chargez ou rechargez le filament de la bobine externe puis validez ; reprenez ensuite l'impression (resume_print)"},
	"07FF700000020008": {source: srcField,
		text:   "bobine externe (hors AMS) : le filament n'est pas engagé ou ne s'extrude pas correctement (à vérifier)",
		action: "sur l'écran : chargez le filament de la bobine externe jusqu'à ce qu'il s'extrude proprement puis validez ; reprenez ensuite l'impression (resume_print)"},
}

// hmsFamily : familles « attr » (8 chiffres : module, classe) pour un code absent de la table exacte.
var hmsFamily = map[string]hmsEntry{
	"03000D00": {source: srcField, text: "mise à l'origine (homing) du plateau / de l'axe Z (module de mouvement) (à vérifier)",
		action: "vérifiez l'axe Z et le plateau (rien ne doit le gêner), nettoyez la buse, acquittez l'erreur à l'écran ou redémarrez l'imprimante"},
	"07FF5000": {source: srcField, text: "bobine externe (hors AMS) : filament (à vérifier)",
		action: "chargez ou rechargez le filament de la bobine externe sur l'écran, puis reprenez l'impression"},
	"07FF7000": {source: srcField, text: "bobine externe (hors AMS) : engagement / extrusion du filament (à vérifier)",
		action: "chargez le filament de la bobine externe sur l'écran, puis reprenez l'impression"},
	"07FF8000": {source: srcField, text: "bobine externe (hors AMS) : chargement du filament en attente (à vérifier)",
		action: "chargez le filament de la bobine externe sur l'écran, puis reprenez l'impression"},
	"03000100": {source: srcMemory, text: "plateau chauffant : température anormale (capteur ou chauffage) (à vérifier)",
		action: "arrêtez l'impression, laissez refroidir, vérifiez le câblage et le capteur du plateau ; contactez le support Bambu si l'alerte persiste"},
	"03000200": {source: srcMemory, text: "buse / tête : température anormale (capteur ou chauffage) (à vérifier)",
		action: "arrêtez l'impression, laissez refroidir, vérifiez la tête d'outil ; contactez le support Bambu si l'alerte persiste"},
	"0C000300": {source: srcMemory, text: "inspection de la première couche / détection de défaut par la caméra (module xcam) (à vérifier)",
		action: "regardez la première couche à la caméra ou sur place ; arrêtez ou poursuivez selon ce que vous voyez"},
	"05000200": {source: srcMemory, text: "réseau / Wi-Fi de l'imprimante (module carte mère) (à vérifier)",
		action: "vérifiez le Wi-Fi de l'imprimante (écran > réseau) ; diagnose_connection aide à distinguer réseau et service"},
}

// hmsKnownPrintErrors : print_error (8 chiffres) observés mais absents de errcodes.go (qui garde les reliquats bénins).
var hmsKnownPrintErrors = map[string]hmsEntry{
	"03004000": {source: srcField,
		text:   "impression interrompue par une erreur du module de mouvement pendant la préparation (alerte HMS associée à lire) (à vérifier)",
		action: "lisez l'alerte HMS jointe (souvent la mise à l'origine du plateau), corrigez, acquittez à l'écran ou redémarrez l'imprimante, puis relancez"},
	"07FF8012": {source: srcField,
		text:   "bobine externe (hors AMS) : l'imprimante attend que vous chargiez le filament à l'écran (pause) (à vérifier)",
		action: "chargez le filament de la bobine externe sur l'écran (ou load_filament) puis reprenez l'impression (resume_print)"},
}

// HMSCodeFormat : « 0300_0D00_0002_0005 » à partir des 16 chiffres.
func HMSCodeFormat(code16 string) string {
	code16 = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code16), "_", ""))
	if len(code16) != 16 {
		return code16
	}
	return code16[0:4] + "_" + code16[4:8] + "_" + code16[8:12] + "_" + code16[12:16]
}

// HMSWikiLink : lien supposé vers la page du code (format non vérifié, voir HMSWikiPrefix).
func HMSWikiLink(code16 string) string { return HMSWikiPrefix + HMSCodeFormat(code16) }

// lookupExtended consulte les tables de ce fichier : 16 chiffres (exact puis famille) ou 8 chiffres (print_error).
func lookupExtended(code string) (hmsEntry, bool) {
	switch len(code) {
	case 16:
		if e, ok := hmsExact[code]; ok {
			return e, true
		}
		if e, ok := hmsFamily[code[:8]]; ok {
			return e, true
		}
	case 8:
		if e, ok := hmsKnownPrintErrors[code]; ok {
			return e, true
		}
		if e, ok := hmsFamily[code]; ok {
			return e, true
		}
	}
	return hmsEntry{}, false
}

// SummaryLine : « 0300_0D00_0002_0005 (grave) : texte » pour un message à l'humain.
func (e ErrorInfo) SummaryLine() string {
	s := e.Short
	if e.Severity != "" {
		s += " (" + e.Severity + ")"
	}
	return fmt.Sprintf("%s : %s", s, e.Text)
}
