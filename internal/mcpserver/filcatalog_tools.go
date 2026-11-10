package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	filcatalog "github.com/ArseneGA/BAMBULAB-MCP/internal/filament/catalog"
)

// Catalogue officiel des filaments Bambu (B3) : données embarquées dans le binaire, lecture seule, aucun appel
// réseau, aucune action sur l'imprimante (internal/filament/catalog).

// SearchFilamentCatalogInput est la recherche : critères cumulés.
type SearchFilamentCatalogInput struct {
	Family   string  `json:"family,omitempty" jsonschema:"gamme (PLA Basic, PETG HF, GFA00...) ou classe de matière (PLA, PETG, ABS, ASA, TPU, PA, PC...)"`
	Finish   string  `json:"finish,omitempty" jsonschema:"finition : mat, soie, translucide, métal, paillettes (sparkle), marbre, glow, galaxy, bois, aero, carbone, verre, standard"`
	Text     string  `json:"text,omitempty" jsonschema:"texte dans le nom de la couleur (français, anglais, allemand, espagnol, italien ; accents ignorés)"`
	Hex      string  `json:"hex,omitempty" jsonschema:"#RRGGBB : renvoie les couleurs officielles les plus proches, avec l'écart ΔE (la plus proche en premier)"`
	MaxDelta float64 `json:"max_delta_e,omitempty" jsonschema:"avec hex : écart ΔE maximal"`
	Nozzle   string  `json:"nozzle_diameter_mm,omitempty" jsonschema:"buse de l'imprimante (0.2, 0.4, 0.6, 0.8) : écarte les gammes déconseillées sur ce diamètre"`
	Hardened bool    `json:"hardened_nozzle,omitempty" jsonschema:"vrai si la buse est en acier trempé : autorise les gammes à fibres de carbone ou de verre. Faux (défaut) : elles sont écartées quand nozzle_diameter_mm est donné"`
	Kind     string  `json:"nozzle_kind,omitempty" jsonschema:"Standard (défaut) | High Flow | E3D High Flow"`
	Limit    int     `json:"limit,omitempty" jsonschema:"nombre maximal de couleurs (défaut 20, maximum 100)"`
}

// GetFilamentCatalogEntryInput désigne une entrée.
type GetFilamentCatalogEntryInput struct {
	ID       string `json:"id" jsonschema:"identifiant de couleur (GFA00-10300), de gamme (GFA00) ou nom de gamme (PLA Basic)"`
	Nozzle   string `json:"nozzle_diameter_mm,omitempty" jsonschema:"diamètre de buse pour le verdict de compatibilité"`
	Hardened bool   `json:"hardened_nozzle,omitempty" jsonschema:"buse en acier trempé"`
	Kind     string `json:"nozzle_kind,omitempty" jsonschema:"Standard (défaut) | High Flow | E3D High Flow"`
}

func nozzleFrom(d string, hardened bool, kind string) *filcatalog.Nozzle {
	if d == "" && !hardened && kind == "" {
		return nil
	}
	return &filcatalog.Nozzle{DiameterMM: strings.TrimSpace(d), Hardened: hardened, Kind: strings.TrimSpace(kind)}
}

func registerFilamentCatalogTools(s *mcp.Server) {
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}

	addTool(s, &mcp.Tool{
		Name: "search_filament_catalog", Title: "Catalogue officiel des filaments Bambu",
		Description: "Cherche dans le catalogue OFFICIEL des filaments Bambu Lab (données embarquées, sans réseau) : couleurs exactes (hex, noms FR/EN), finitions (mat, soie, translucide, métal, paillettes, marbre, glow...), gammes (PLA Basic, PETG HF, ABS, TPU, PA-CF, PC...), " +
			"températures de buse et de plateau, densité, diamètre, séchage, buse en acier trempé requise ou non. `hex` renvoie les couleurs officielles les plus proches (écart ΔE) : utile pour choisir une couleur de bobine ou un aperçu réaliste. " +
			"Chaque entrée porte sa source et un niveau de confiance (official_profile | official_site | estimated) ; une valeur absente est « non disponible », jamais inventée. Sans critère : aperçu des gammes. Lecture seule.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SearchFilamentCatalogInput) (*mcp.CallToolResult, filcatalog.SearchResult, error) {
		cat, err := filcatalog.Default()
		if err != nil {
			return nil, filcatalog.SearchResult{}, err
		}
		res, err := cat.RunSearch(filcatalog.Query{Family: in.Family, Finish: in.Finish, Text: in.Text, Hex: in.Hex, MaxDelta: in.MaxDelta,
			Nozzle: nozzleFrom(in.Nozzle, in.Hardened, in.Kind), Limit: in.Limit})
		if err != nil {
			return nil, filcatalog.SearchResult{}, err
		}
		txt := res.Note
		for i, m := range res.Colors {
			if i >= maxSearchTextRows {
				txt += fmt.Sprintf("\n… et %d autre(s) (voir structuredContent.colors).", len(res.Colors)-i)
				break
			}
			line := fmt.Sprintf("\n- %s · %s · %s %s (%s)", m.Color.ID, m.Color.NameFR(), strings.Join(m.Color.Hex, "+"), m.Color.FamilyName, m.Color.Finish)
			if m.DeltaE != nil {
				line += fmt.Sprintf(" · ΔE %.1f (%s)", *m.DeltaE, m.Quality)
			}
			txt += line
		}
		if len(res.Colors) == 0 {
			for i, f := range res.Families {
				if i >= maxSearchTextRows {
					break
				}
				txt += fmt.Sprintf("\n- %s (%s) · %s · %d couleur(s)", f.Name, f.ID, f.FinishFR, f.ColorCount)
			}
		}
		return textResult(txt), *res, nil
	})

	addTool(s, &mcp.Tool{
		Name: "get_filament_catalog_entry", Title: "Fiche d'un filament Bambu officiel",
		Description: "Fiche complète d'une couleur (id GFA00-10300) ou d'une gamme (GFA00, « PLA Basic ») du catalogue officiel Bambu : hex et noms de la couleur, finition, températures de buse et de plateau par plaque, ventilateur, densité, diamètre, séchage, dureté de buse requise, buses déconseillées, sources et niveau de confiance. " +
			"Avec nozzle_diameter_mm / hardened_nozzle : verdict de compatibilité avec la buse. Pour rattacher une bobine à une entrée : update_spool(catalog_color_id). Lecture seule.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetFilamentCatalogEntryInput) (*mcp.CallToolResult, filcatalog.EntryResult, error) {
		cat, err := filcatalog.Default()
		if err != nil {
			return nil, filcatalog.EntryResult{}, err
		}
		r, err := cat.Entry(in.ID, nozzleFrom(in.Nozzle, in.Hardened, in.Kind))
		if err != nil {
			return nil, filcatalog.EntryResult{}, err
		}
		txt := r.Note
		f := r.Family
		if f.NozzleTempC != nil {
			txt += fmt.Sprintf("\nBuse %d °C", *f.NozzleTempC)
		}
		if f.HardenedNozzleRequired {
			txt += " · buse en acier trempé requise"
		}
		if r.Compat != nil {
			if r.Compat.OK {
				txt += "\nCompatible avec la buse indiquée."
			} else {
				txt += "\nINCOMPATIBLE : " + strings.Join(r.Compat.Reasons, " ; ")
			}
		}
		txt += fmt.Sprintf("\nSource : %s (%s, relevé le %s).", f.Source.Ref, f.Confidence, f.Source.Retrieved)
		return textResult(txt), *r, nil
	})
}
