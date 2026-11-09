package mcpserver

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Cartes-formulaires : un outil visible du modèle, appelé sans ses champs (ou avec un sous-ensemble), renvoie une
// carte formulaire pré-remplie (ui://bambu/form.html). L'humain complète et valide dans la carte ; la carte appelle un
// outil réservé à l'application (submit_*, voir form_tools.go) qui exécute la MÊME action que l'outil normal, avec la
// même validation côté serveur. Le comportement avec tous les champs est inchangé.
//
// RÈGLES DE SÉCURITÉ (docs/SECURITY.md §5, §12) :
//   - une carte-formulaire ne reçoit JAMAIS un secret (code d'accès LAN, mot de passe, jeton, code de lancement,
//     clé d'API) : aucun champ de ce genre n'existe, et FormSpec.Validate refuse tout nom qui y ressemble ;
//   - elle ne lance, n'arrête ni ne reprend aucune impression : les outils submit_* n'appellent que des services
//     d'enregistrement (imprimante déclarée, prix, canaux existants, bobine, objectif, pièce paramétrique).
//     Test : TestFormSubmitToolsDoNothingPhysical.

// FormViewURI est la ressource de la carte-formulaire (même bundle HTML que les autres vues).
const FormViewURI = "ui://bambu/form.html"

// formTools : outils visibles du modèle liés à la carte-formulaire (ajoutés à toolViewMeta, appliqué par addTool).
var formTools = []string{"add_printer", "set_cost_settings", "configure_notifications", "update_spool", "set_manufacturing_goals", "create_from_template"}

func init() {
	for _, t := range formTools {
		toolViewMeta[t] = AppToolMeta(FormViewURI)
	}
}

// FormOption est un choix d'une liste ou d'un segmenté.
type FormOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Hint  string `json:"hint,omitempty"`
	Color string `json:"color,omitempty" jsonschema:"#RRGGBB, pastille de couleur"`
}

// FormField décrit un champ. Name est un chemin (« params.length », « channels.0.enabled ») : la carte en fait
// l'objet imbriqué envoyé à l'outil de soumission.
type FormField struct {
	Name        string       `json:"name"`
	Label       string       `json:"label"`
	Kind        string       `json:"kind" jsonschema:"text | textarea | number | select | segmented | color | toggle | checks | hidden"`
	Required    bool         `json:"required,omitempty"`
	Unit        string       `json:"unit,omitempty"`
	Min         *float64     `json:"min,omitempty"`
	Max         *float64     `json:"max,omitempty"`
	Step        *float64     `json:"step,omitempty"`
	Integer     bool         `json:"integer,omitempty"`
	Numeric     bool         `json:"numeric,omitempty" jsonschema:"select ou segmented dont les valeurs sont des nombres (la carte les envoie comme nombres)"`
	Options     []FormOption `json:"options,omitempty"`
	Placeholder string       `json:"placeholder,omitempty"`
	Hint        string       `json:"hint,omitempty"`
	Group       string       `json:"group,omitempty" jsonschema:"titre de section : les champs consécutifs de même groupe sont regroupés"`
	MaxLength   int          `json:"max_length,omitempty"`
	Pattern     string       `json:"pattern,omitempty" jsonschema:"expression régulière (syntaxe JavaScript) que le texte doit respecter"`
}

// FormLink est un lien vers une page humaine (ouvert par l'hôte).
type FormLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
	Note  string `json:"note,omitempty"`
}

// FormImage est une image jointe (données base64, affichée en data: URL).
type FormImage struct {
	Mime string `json:"mime"`
	Data string `json:"data" jsonschema:"base64 standard"`
	Alt  string `json:"alt,omitempty"`
}

// FormSpec est la carte-formulaire.
type FormSpec struct {
	FormID      string         `json:"form_id" jsonschema:"identifiant stable du formulaire (add_printer, set_cost_settings…)"`
	Title       string         `json:"title"`
	Intro       string         `json:"intro,omitempty"`
	Tool        string         `json:"tool" jsonschema:"outil visible du modèle que cette carte remplace"`
	SubmitTool  string         `json:"submit_tool" jsonschema:"outil réservé à l'application appelé à la validation"`
	SubmitLabel string         `json:"submit_label"`
	Send        string         `json:"send" jsonschema:"all (tous les champs) | changed (seulement ceux que l'humain a modifiés, plus les champs hidden)"`
	Fields      []FormField    `json:"fields"`
	Values      map[string]any `json:"values,omitempty" jsonschema:"valeurs initiales par nom de champ (ce que le modèle ou le service savait déjà)"`
	Missing     []string       `json:"missing,omitempty" jsonschema:"champs obligatoires que le modèle n'avait pas fournis"`
	Links       []FormLink     `json:"links,omitempty"`
	Notice      string         `json:"notice,omitempty" jsonschema:"rappel de sécurité ou de périmètre affiché dans la carte"`
	PreviewTool string         `json:"preview_tool,omitempty" jsonschema:"outil réservé à l'application qui renvoie un aperçu (gabarit) pour les valeurs courantes"`
	Preview     *FormImage     `json:"preview,omitempty"`
}

// FormDetail est une ligne « libellé : valeur » du résultat.
type FormDetail struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// FormResult est ce que la carte affiche après la soumission (ou après un appel complet de l'outil).
type FormResult struct {
	OK      bool         `json:"ok"`
	Title   string       `json:"title"`
	Summary string       `json:"summary"`
	Details []FormDetail `json:"details,omitempty"`
	Links   []FormLink   `json:"links,omitempty"`
	Image   *FormImage   `json:"image,omitempty"`
	Notes   []string     `json:"notes,omitempty" jsonschema:"avertissements, ambiguïtés ou étapes suivantes"`
}

// FormOut : champs communs ajoutés à la sortie structurée des outils à carte. Form absent = l'action a eu lieu.
type FormOut struct {
	Form       *FormSpec   `json:"form,omitempty" jsonschema:"carte-formulaire à compléter par l'humain (outil appelé sans tous ses champs)"`
	FormResult *FormResult `json:"form_result,omitempty" jsonschema:"résumé de l'action effectuée, affiché par la carte"`
}

// secretFieldRe : tout nom de champ qui ressemble à un secret est refusé dans une carte-formulaire.
var secretFieldRe = regexp.MustCompile(`(?i)(access[_.-]?code|accesscode|code[_.-]?d[_.-]?acc|password|passwd|passphrase|secret|token|launch[_.-]?code|api[_.-]?key|apikey|credential|private[_.-]?key|webhook[_.-]?url|ntfy[_.-]?url|smtp[_.-]?pass)`)

// IsSecretFieldName dit si un nom de champ désigne un secret (refusé partout dans les cartes-formulaires).
func IsSecretFieldName(name string) bool { return secretFieldRe.MatchString(name) }

// Validate vérifie la spécification : types connus, noms uniques, aucun nom de secret, outils renseignés.
func (f *FormSpec) Validate() error {
	if f.FormID == "" || f.Tool == "" || f.SubmitTool == "" {
		return fmt.Errorf("formulaire incomplet (form_id, tool, submit_tool)")
	}
	if f.Send != "all" && f.Send != "changed" {
		return fmt.Errorf("formulaire %s : send doit valoir all ou changed", f.FormID)
	}
	seen := map[string]bool{}
	for _, fl := range f.Fields {
		if fl.Name == "" || seen[fl.Name] {
			return fmt.Errorf("formulaire %s : nom de champ vide ou en double (%q)", f.FormID, fl.Name)
		}
		seen[fl.Name] = true
		if IsSecretFieldName(fl.Name) || IsSecretFieldName(fl.Label) {
			return fmt.Errorf("formulaire %s : le champ %q ressemble à un secret (interdit dans une carte)", f.FormID, fl.Name)
		}
		switch fl.Kind {
		case "text", "textarea", "number", "select", "segmented", "color", "toggle", "checks", "hidden":
		default:
			return fmt.Errorf("formulaire %s : type de champ inconnu %q", f.FormID, fl.Kind)
		}
	}
	for k := range f.Values {
		if IsSecretFieldName(k) {
			return fmt.Errorf("formulaire %s : la valeur %q ressemble à un secret", f.FormID, k)
		}
	}
	return nil
}

// formText est le texte COMPLET pour les clients sans cartes : champs, valeurs connues, consigne de rappel de l'outil.
func formText(f *FormSpec, intro string) string {
	var b strings.Builder
	b.WriteString(intro)
	b.WriteString(" Si l'hôte n'affiche pas de carte, demande ces valeurs à l'utilisateur dans la conversation (jamais un secret), puis rappelle ")
	b.WriteString(f.Tool)
	b.WriteString(" avec TOUS les champs obligatoires.")
	if len(f.Missing) > 0 {
		b.WriteString("\nManquent : " + strings.Join(f.Missing, ", ") + ".")
	}
	b.WriteString("\nChamps :")
	for _, fl := range f.Fields {
		if fl.Kind == "hidden" {
			continue
		}
		b.WriteString("\n- " + fl.Name)
		if fl.Required {
			b.WriteString(" (obligatoire)")
		}
		b.WriteString(" : " + fl.Label)
		if fl.Unit != "" {
			b.WriteString(" [" + fl.Unit + "]")
		}
		if len(fl.Options) > 0 {
			opts := make([]string, 0, len(fl.Options))
			for i, o := range fl.Options {
				if i >= 12 {
					opts = append(opts, "…")
					break
				}
				opts = append(opts, o.Value)
			}
			b.WriteString(" ; valeurs : " + strings.Join(opts, " | "))
		}
		if v, ok := f.Values[fl.Name]; ok && v != nil && fmt.Sprint(v) != "" {
			b.WriteString(fmt.Sprintf(" ; actuel : %v", v))
		}
	}
	for _, l := range f.Links {
		b.WriteString("\n" + l.Label + " : " + l.URL)
		if l.Note != "" {
			b.WriteString(" (" + l.Note + ")")
		}
	}
	if f.Notice != "" {
		b.WriteString("\n" + f.Notice)
	}
	return b.String()
}

// formResultText résume FormResult en texte (résultat complet des clients sans cartes).
func formResultText(r *FormResult) string {
	var b strings.Builder
	b.WriteString(r.Summary)
	for _, d := range r.Details {
		b.WriteString("\n- " + d.Label + " : " + d.Value)
	}
	for _, n := range r.Notes {
		b.WriteString("\n" + n)
	}
	for _, l := range r.Links {
		b.WriteString("\n" + l.Label + " : " + l.URL)
	}
	return b.String()
}

// SubmitToolNames liste les outils réservés à l'application ajoutés par les cartes-formulaires (tests, documentation).
var SubmitToolNames = []string{"submit_add_printer", "submit_cost_settings", "submit_notifications", "submit_spool", "submit_goals", "submit_part_template", "preview_part_template"}

func fptr(v float64) *float64 { return &v }

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// formResultCall : résultat d'un outil de soumission (texte complet + structuré).
func formResultCall(r *FormResult) *mcp.CallToolResult { return textResult(formResultText(r)) }
