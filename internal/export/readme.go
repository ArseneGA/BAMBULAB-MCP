package export

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// settingLabels : libellés lisibles des réglages courants (les autres clés sont affichées telles quelles).
var settingLabels = map[string]string{
	"layer_height": "Hauteur de couche (mm)", "initial_layer_print_height": "Hauteur de la première couche (mm)",
	"sparse_infill_density": "Remplissage", "sparse_infill_pattern": "Motif de remplissage",
	"wall_loops": "Nombre de parois", "top_shell_layers": "Couches du dessus", "bottom_shell_layers": "Couches du dessous",
	"enable_support": "Supports", "support_type": "Type de supports", "brim_type": "Bordure", "brim_width": "Largeur de bordure (mm)",
	"nozzle_temperature": "Température de buse (°C)", "bed_temperature": "Température du plateau (°C)",
	"outer_wall_speed": "Vitesse des parois extérieures (mm/s)", "filament_flow_ratio": "Débit (ratio)",
	"xy_contour_compensation": "Compensation XY (contour)", "xy_hole_compensation": "Compensation XY (trous)",
	"seam_position": "Position de la couture", "ironing_type": "Lissage",
}

func settingLabel(k string) string {
	if l, ok := settingLabels[k]; ok {
		return l
	}
	return k
}

func mdCell(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
	return strings.ReplaceAll(s, "|", `\|`)
}

func fmtDuration(sec float64) string {
	if sec <= 0 {
		return "—"
	}
	m := int(sec/60 + 0.5)
	if m < 60 {
		return fmt.Sprintf("%d min", m)
	}
	return fmt.Sprintf("%d h %02d min", m/60, m%60)
}

func fmtGrams(g float64) string {
	if g <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f g", g)
}

func partMaterials(pt *part) []string {
	var out []string
	if pt.Print != nil {
		for _, f := range pt.Print.Filaments {
			if f.Type != "" {
				out = append(out, f.Type)
			}
		}
	}
	if len(out) == 0 && pt.Profile != nil {
		out = append(out, pt.Profile.Reference.Filaments...)
	}
	return out
}

func valueString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return fmt.Sprintf("%g", x)
	case bool:
		if x {
			return "oui"
		}
		return "non"
	}
	return fmt.Sprint(v)
}

// buildReadme génère le README.md (français) de l'archive.
func buildReadme(pl *plan, now time.Time, version string) string {
	var b strings.Builder
	o := pl.in.Options
	w := func(f string, a ...any) { fmt.Fprintf(&b, f, a...) }

	w("# %s\n\n", pl.title)
	if o.Description != "" {
		w("%s\n\n", o.Description)
	} else if len(pl.parts) == 1 {
		w("Projet d'impression 3D : %s.\n\n", pl.parts[0].Model.Name)
	} else {
		w("Projet d'impression 3D de %d pièces.\n\n", len(pl.parts))
	}
	w("> Archive générée le %s par Atelier Bambu %s. Les fichiers d'origine sont copiés tels quels (empreintes SHA-256 dans `manifest.json`).\n\n", now.UTC().Format("02/01/2006"), version)

	// ---- pièces ----
	w("## Pièces à imprimer\n\n| Pièce | Quantité | Matière | Dimensions (mm) | Fichiers |\n|---|---:|---|---|---|\n")
	for _, pt := range pl.parts {
		mats := "non précisée"
		if m := partMaterials(pt); len(m) > 0 {
			mats = strings.Join(dedupe(m), ", ")
		}
		dims := "—"
		if a := pt.Detail.Analysis.SizeMM; a != nil && pt.Detail.Analysis.State == "ok" {
			dims = fmt.Sprintf("%.1f × %.1f × %.1f", a.X, a.Y, a.Z)
		}
		var fl []string
		for _, f := range pt.Files {
			fl = append(fl, "`"+f.Path+"`")
		}
		w("| %s | %d | %s | %s | %s |\n", mdCell(pt.Model.Name), pt.Qty, mdCell(mats), dims, strings.Join(fl, "<br>"))
	}
	w("\n")

	// ---- détail par pièce ----
	w("## Détail par pièce\n\n")
	for _, pt := range pl.parts {
		w("### %s\n\n", pt.Model.Name)
		if pt.Model.Description != "" {
			w("%s\n\n", pt.Model.Description)
		}
		w("- Quantité : %d\n", pt.Qty)
		if pt.Version != nil {
			w("- Version exportée : %s\n", pt.Version.Label)
			if pt.Version.Note != "" {
				w("- Note de version : %s\n", pt.Version.Note)
			}
		}
		if len(pt.Model.Tags) > 0 {
			w("- Étiquettes : %s\n", strings.Join(pt.Model.Tags, ", "))
		}
		if pt.Model.License != "" {
			w("- Licence d'origine de la pièce : %s\n", pt.Model.License)
		}
		if p := pt.Model.Provenance; p != nil && (p.Source != "" || p.URL != "" || p.Author != "") {
			var src []string
			for _, v := range []string{p.Source, p.Author, p.URL} {
				if v != "" {
					src = append(src, v)
				}
			}
			w("- Provenance : %s\n", strings.Join(src, " — "))
		}
		if m := partMaterials(pt); len(m) > 0 {
			w("- Matière : %s\n", strings.Join(dedupe(m), ", "))
		}
		if pt.Template != nil {
			w("- Pièce paramétrique : gabarit `%s` (version %s), paramètres dans `sources/%s/parametrique.json`\n", pt.Template.Template, pt.Template.TemplateVersion, pt.Slug)
		}
		if pt.Print != nil && pt.Print.Seconds > 0 {
			w("- Estimation du slicer (par exemplaire) : %s, %s de filament", fmtDuration(pt.Print.Seconds), fmtGrams(pt.Print.Grams))
			if pt.Print.Layers > 0 {
				w(", %d couches", pt.Print.Layers)
			}
			w("\n")
		}
		if c := pt.Cost; c != nil && c.Total > 0 {
			w("- Coût estimé (par exemplaire) : %.2f %s — estimation indicative (filament, électricité, usure), pas une facture\n", c.Total, c.Currency)
		}
		if pr := pt.Profile; pr != nil {
			w("- Réglages de découpe : ")
			ref := []string{}
			if pr.Reference.Machine != "" {
				ref = append(ref, "machine « "+pr.Reference.Machine+" »")
			}
			if pr.Reference.Process != "" {
				ref = append(ref, "process « "+pr.Reference.Process+" »")
			}
			if len(pr.Reference.Filaments) > 0 {
				ref = append(ref, "filament « "+strings.Join(pr.Reference.Filaments, " », « ")+" »")
			}
			if len(ref) > 0 {
				w("préréglages officiels de Bambu Studio : %s. ", strings.Join(ref, ", "))
			}
			w("Détail dans `profiles/%s.json`.\n", pt.Slug)
			if len(pr.Overrides) > 0 {
				w("\n  | Réglage modifié | Valeur | Cible |\n  |---|---|---|\n")
				for _, ov := range pr.Overrides {
					tg := ov.Target
					if tg == "" {
						tg = "process"
					}
					w("  | %s (`%s`) | %s | %s |\n", mdCell(settingLabel(ov.Key)), mdCell(ov.Key), mdCell(valueString(ov.Value)), tg)
				}
			} else {
				w("  Aucun réglage modifié par rapport aux préréglages.\n")
			}
			if pr.Orientation != nil && pr.Orientation.Label != "" {
				w("\n  Orientation conseillée : %s.\n", pr.Orientation.Label)
			}
		} else {
			w("- Réglages de découpe : préréglages par défaut de Bambu Studio (aucune surcharge enregistrée).\n")
		}
		if pt.Gcode != nil {
			w("- G-code découpé : `%s` (validé ; spécifique à une imprimante et à des réglages, voir l'avertissement plus bas)\n", pt.Gcode.Path)
		}
		for _, wn := range pt.Warnings {
			w("- Attention : %s\n", wn)
		}
		w("\n")
	}

	// ---- matériaux et totaux ----
	grams := map[string]float64{}
	var totSec, totG, totCost float64
	var cur string
	haveTime, haveCost := true, true
	for _, pt := range pl.parts {
		q := float64(pt.Qty)
		if pt.Print != nil && pt.Print.Seconds > 0 {
			totSec += pt.Print.Seconds * q
			totG += pt.Print.Grams * q
			for _, f := range pt.Print.Filaments {
				if f.Type != "" {
					grams[f.Type] += f.Grams * q
				}
			}
		} else {
			haveTime = false
		}
		if pt.Cost != nil && pt.Cost.Total > 0 {
			totCost += pt.Cost.Total * q
			cur = pt.Cost.Currency
		} else {
			haveCost = false
		}
	}
	w("## Matériaux et estimations\n\n")
	if len(grams) > 0 {
		keys := make([]string, 0, len(grams))
		for k := range grams {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		w("| Matière | Masse totale estimée |\n|---|---:|\n")
		for _, k := range keys {
			w("| %s | %s |\n", mdCell(k), fmtGrams(grams[k]))
		}
		w("\n")
	} else {
		w("Matières : non précisées (aucune découpe enregistrée pour ces pièces).\n\n")
	}
	switch {
	case totSec > 0:
		w("Temps d'impression total estimé (quantités comprises) : **%s** pour **%s** de filament", fmtDuration(totSec), fmtGrams(totG))
		if !haveTime {
			w(" (certaines pièces sans estimation ne sont pas comptées)")
		}
		w(".\n")
	default:
		w("Temps et masse : non disponibles (aucune découpe enregistrée).\n")
	}
	if totCost > 0 {
		w("Coût estimé total : **%.2f %s**", totCost, cur)
		if !haveCost {
			w(" (certaines pièces sans estimation ne sont pas comptées)")
		}
		w(" — estimation indicative, pas une facture.\n")
	}
	w("\n")

	// ---- champs libres ----
	if o.AssemblyNotes != "" {
		w("## Notice d'assemblage\n\n%s\n\n", o.AssemblyNotes)
	}
	if o.Hardware != "" {
		w("## Quincaillerie\n\n%s\n\n", o.Hardware)
	}

	w("## Licence\n\n%s\n\n", o.License)
	if o.License == DefaultLicense {
		w("_La licence n'a pas été choisie : à préciser avant toute publication._\n\n")
	}

	// ---- photos ----
	var photos []photoItem
	for _, pt := range pl.parts {
		photos = append(photos, pt.Photos...)
	}
	if len(photos) > 0 {
		w("## Photos\n\n")
		for _, pt := range pl.parts {
			for _, ph := range pt.Photos {
				w("![%s](%s)\n\n", mdCell(pt.Model.Name), strings.ReplaceAll(ph.Path, " ", "%20"))
			}
		}
	}

	// ---- structure ----
	w("## Contenu de l'archive\n\n")
	w("- `files/` : fichiers sources d'origine (STL, STEP, 3MF, OBJ), un dossier par pièce\n")
	w("- `sources/` : sources paramétriques (JSON de gabarit) des pièces générées\n")
	w("- `profiles/` : réglages de découpe effectifs (uniquement les valeurs modifiées, jamais de G-code)\n")
	if o.IncludeGcode3MF {
		w("- `printer-specific/` : fichiers .gcode.3mf validés\n")
	}
	if len(photos) > 0 {
		w("- `images/` : photos de fin d'impression\n")
	}
	w("- `manifest.json` : versions, empreintes SHA-256, provenance, date\n\n")
	if o.IncludeGcode3MF {
		w("> **Attention : les fichiers de `printer-specific/` sont spécifiques à une imprimante, une buse et des réglages précis.** Ne les utilisez pas tels quels sur un autre modèle d'imprimante : redécoupez plutôt les fichiers de `files/` avec Bambu Studio.\n\n")
	}
	w("Aucun secret, aucune donnée d'imprimante (adresse, numéro de série, code d'accès) ni chemin du serveur n'est inclus dans cette archive.\n")
	return b.String()
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		k := strings.ToLower(v)
		if !seen[k] {
			seen[k] = true
			out = append(out, v)
		}
	}
	return out
}
