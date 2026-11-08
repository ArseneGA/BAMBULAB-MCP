package printer

import (
	"fmt"
	"strings"
	"time"
)

// Décodage des codes d'erreur Bambu (print_error et HMS).
//
// SOURCES (à compléter, la table est volontairement PARTIELLE : un code absent n'est jamais deviné) :
//   - wiki Bambu Lab, « HMS » / « Print errors » (https://wiki.bambulab.com/en/hms/home) ;
//   - projet open source ha-bambulab (print_errors / hms_error_text) et OpenBambuAPI (mqtt.md), qui
//     reprennent les mêmes codes.
//
// print_error est un entier de 32 bits : en hexadécimal sur 8 chiffres, « 0300400C » (= 50348044), il se lit
// comme le code HMS court « 0300_400C ». Un HMS du rapport (attr, code) donne 16 chiffres : attr puis code.
// Les entrées marquées Benign décrivent un reliquat SANS gravité (annulation) : l'imprimante les garde affichés
// en état FAILED jusqu'à son redémarrage, ils ne doivent pas bloquer un nouveau lancement.

// WikiURL : où chercher un code absent de la table.
const WikiURL = "https://wiki.bambulab.com/en/hms/home"

// ErrorInfo décrit un code d'erreur décodé.
type ErrorInfo struct {
	Code   string `json:"code"`             // 8 chiffres hexadécimaux « 0300400C » (ou 16 pour un HMS)
	Short  string `json:"short"`            // forme lisible « 0300_400C » ou « 0C00_0000_0001_0001 »
	Text   string `json:"text"`             // explication en français
	Known  bool   `json:"known"`            // vrai si le code figure dans la table
	Benign bool   `json:"benign,omitempty"` // reliquat sans gravité (annulation) : ne bloque pas un lancement
	Module string `json:"module,omitempty"` // module probable d'après le préfixe
	Action string `json:"action,omitempty"` // consigne à l'utilisateur
	Wiki   string `json:"wiki"`             // où chercher le code
}

type knownError struct {
	text, action string
	benign       bool
}

// knownPrintErrors : codes à 8 chiffres (print_error ou HMS court).
var knownPrintErrors = map[string]knownError{
	"0300400C": {text: "l'impression a été annulée (écran de l'imprimante, Bambu Studio ou commande d'arrêt)", benign: true,
		action: "reliquat d'annulation : sans gravité, l'imprimante l'affiche jusqu'à son redémarrage ou l'acquittement du message à l'écran"},
	"0500400E": {text: "l'impression a été annulée", benign: true,
		action: "reliquat d'annulation : sans gravité"},
	"0500400D": {text: "l'imprimante demande d'exécuter l'auto-test (calibration) avant de relancer",
		action: "lancez l'auto-test depuis l'écran de l'imprimante (Paramètres > Calibration), puis relancez"},
}

// modules : préfixe de 4 chiffres -> module probable (indication, jamais une certitude).
var modules = map[string]string{
	"0300": "mouvement / températures (MC)",
	"0500": "système / carte mère",
	"0700": "AMS",
	"0C00": "lidar / inspection de première couche",
	"1200": "AMS Lite",
}

func moduleOf(code string) string {
	if len(code) >= 4 {
		if m, ok := modules[strings.ToUpper(code[:4])]; ok {
			return m
		}
		if strings.HasPrefix(strings.ToUpper(code), "07") {
			return "AMS"
		}
	}
	return ""
}

func shortForm(code string) string {
	var parts []string
	for i := 0; i+4 <= len(code); i += 4 {
		parts = append(parts, code[i:i+4])
	}
	return strings.Join(parts, "_")
}

// DescribeCode décode un code hexadécimal (8 ou 16 chiffres). Un code inconnu rend « code X (inconnu) » + lien wiki.
func DescribeCode(code string) ErrorInfo {
	code = strings.ToUpper(strings.TrimSpace(code))
	info := ErrorInfo{Code: code, Short: shortForm(code), Wiki: WikiURL, Module: moduleOf(code)}
	// un HMS de 16 chiffres se décode aussi par son code court (les 8 derniers chiffres)
	key := code
	if len(code) == 16 {
		key = code[8:]
	}
	if k, ok := knownPrintErrors[key]; ok {
		info.Known, info.Text, info.Benign, info.Action = true, k.text, k.benign, k.action
		return info
	}
	info.Text = fmt.Sprintf("code %s (inconnu)", info.Short)
	if info.Module != "" {
		info.Text += ", module probable : " + info.Module
	}
	info.Action = "chercher ce code sur " + WikiURL + " ; acquittez le message sur l'écran de l'imprimante ou redémarrez-la"
	return info
}

// DescribePrintError décode la valeur entière print_error du rapport (0 = aucune erreur).
func DescribePrintError(v int) ErrorInfo {
	if v == 0 {
		return ErrorInfo{Known: true, Text: "aucune erreur", Wiki: WikiURL}
	}
	return DescribeCode(fmt.Sprintf("%08X", uint32(v)))
}

// DescribeHMS décode une alerte HMS du rapport.
func DescribeHMS(h HMSError) ErrorInfo { return DescribeCode(h.Code16()) }

// NotReadyError : l'imprimante n'est pas lançable ; Instruction dit précisément quoi faire. Elle enveloppe
// ErrNotReady (ou ErrPrinterError quand une erreur active est en cause).
type NotReadyError struct {
	Reason      string // « hms_active », « print_error », « busy », « stale », « offline », « state »
	Instruction string // consigne à l'utilisateur
	wrapped     error
}

func (e *NotReadyError) Error() string { return e.Instruction }
func (e *NotReadyError) Unwrap() error { return e.wrapped }

// StartPolicy dit quels états de départ sont acceptés en plus de IDLE.
type StartPolicy struct {
	FromFinish bool // FINISH : la dernière impression est allée au bout
	FromFailed bool // FAILED : annulée ou en erreur ; la P1S y reste jusqu'au redémarrage (Bambu Studio lance quand même)
}

// FullStartPolicy : FINISH et FAILED acceptés (comportement de Bambu Studio).
var FullStartPolicy = StartPolicy{FromFinish: true, FromFailed: true}

// TerminalState dit si l'état est un état final de la dernière impression.
func TerminalState(s State) bool { return s == StateFinished || s == StateFailed }

// CheckLaunchable dit si l'imprimante peut recevoir un lancement : connectée, état frais (mis à jour il y a moins de
// maxAge), état de départ accepté, aucune alerte HMS active, aucun print_error en cours (hors reliquat bénin
// d'annulation en FAILED/FINISH). nil = lançable. L'erreur est un *NotReadyError.
func CheckLaunchable(st Status, maxAge time.Duration, now time.Time, pol StartPolicy) error {
	mk := func(sentinel error, reason, msg string) error {
		return &NotReadyError{Reason: reason, Instruction: msg, wrapped: fmt.Errorf("%w: %s", sentinel, msg)}
	}
	if !st.Connected {
		return mk(ErrNotReady, "offline", "l'imprimante est injoignable (hors ligne) : allumez-la, vérifiez le réseau, puis réessayez")
	}
	if st.Stale || (maxAge > 0 && !st.UpdatedAt.IsZero() && now.Sub(st.UpdatedAt) > maxAge) {
		return mk(ErrNotReady, "stale", "l'état de l'imprimante est périmé (aucun rapport récent) : réessayez dans quelques secondes")
	}
	okState := st.State == StateIdle || (pol.FromFinish && st.State == StateFinished) || (pol.FromFailed && st.State == StateFailed)
	if !okState {
		switch st.State {
		case StatePrinting, StatePreparing, StatePaused:
			return mk(ErrNotReady, "busy", fmt.Sprintf("l'imprimante est occupée (état %s) : attendez la fin ou arrêtez l'impression en cours", st.State))
		case StateFinished:
			return mk(ErrNotReady, "state", "l'imprimante est en état FINISH et le départ depuis cet état n'est pas autorisé pour elle")
		case StateFailed:
			return mk(ErrNotReady, "state", "l'imprimante est en état FAILED et le départ depuis cet état n'est pas autorisé pour elle : acquittez le message sur son écran ou redémarrez-la")
		}
		return mk(ErrNotReady, "state", fmt.Sprintf("état de l'imprimante inattendu (%s)", st.State))
	}
	if len(st.HMS) > 0 {
		var parts []string
		for _, h := range st.HMS {
			i := DescribeHMS(h)
			parts = append(parts, fmt.Sprintf("%s (%s)", i.Short, i.Text))
		}
		return mk(ErrPrinterError, "hms_active", fmt.Sprintf("l'imprimante signale une erreur HMS %s ; acquittez-la sur l'écran de l'imprimante ou redémarrez-la (codes : %s)", strings.Join(parts, " ; "), WikiURL))
	}
	if st.PrintError != 0 {
		i := DescribePrintError(st.PrintError)
		if i.Benign && TerminalState(st.State) {
			return nil // reliquat d'annulation : n'empêche pas un nouveau lancement
		}
		return mk(ErrPrinterError, "print_error", fmt.Sprintf("l'imprimante signale l'erreur %s : %s ; %s", i.Short, i.Text, i.Action))
	}
	return nil
}
