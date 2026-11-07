package export

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/insights"
)

// fileItem : un fichier de l'archive tiré du stockage par empreintes.
type fileItem struct {
	File contracts.SourceFile
	Path string // chemin dans l'archive
}

// part : une pièce de l'export.
type part struct {
	Model   contracts.Model
	Detail  *catalog.ModelDetails
	Slug    string
	Qty     int
	Version *contracts.ModelVersion
	Files   []fileItem

	// Renseignés par enrich().
	Template *templateDoc
	Profile  *profileDoc
	Print    *printInfo
	Cost     *insights.CostEstimate
	Gcode    *fileItem
	Photos   []photoItem
	Warnings []string
}

type plan struct {
	title          string
	collectionPath string
	in             Input
	parts          []*part
	skipped        []Skip
	warnings       []string
	originalBytes  int64
}

func (p *plan) summary() PlanSummary {
	ps := PlanSummary{Title: p.title, MaxBytes: p.in.Options.MaxBytes, Warnings: p.warnings, Skipped: p.skipped, TotalBytes: p.originalBytes, Parts: []PartSummary{}}
	for _, pt := range p.parts {
		s := PartSummary{ModelID: pt.Model.ID, Name: pt.Model.Name, Quantity: pt.Qty, Files: len(pt.Files)}
		if pt.Version != nil {
			s.VersionID = pt.Version.ID
		}
		for _, f := range pt.Files {
			s.Bytes += f.File.SizeBytes
		}
		ps.Files += s.Files
		ps.Parts = append(ps.Parts, s)
	}
	return ps
}

// resolve choisit les modèles, leurs versions et leurs fichiers, et refuse tout ce qui ne peut pas être exporté
// (corbeille, original absent, taille hors borne) avant le moindre octet écrit.
func (s *Service) resolve(ctx context.Context, in Input) (*plan, error) {
	pl := &plan{in: in}
	var ids []string
	if in.CollectionID != "" {
		offset := 0
		for {
			l, err := s.d.Catalog.ListCollection(ctx, catalog.ListCollectionInput{Collection: in.CollectionID, Limit: 200, Offset: offset, Recursive: !in.Options.SkipSubcollections})
			if err != nil {
				return nil, err
			}
			if offset == 0 {
				pl.title, pl.collectionPath = l.Collection.Name, l.Collection.Path
			}
			if l.Models == nil {
				break
			}
			for _, m := range l.Models.Models {
				ids = append(ids, m.ID)
			}
			if !l.Models.HasMore || l.Models.NextOffset == nil || len(ids) > MaxModels {
				break
			}
			offset = *l.Models.NextOffset
		}
		if len(ids) == 0 {
			return nil, invalidf("la collection « %s » ne contient aucun modèle", in.CollectionID)
		}
	} else {
		ids = in.ModelIDs
	}
	seen := map[string]bool{}
	var uniq []string
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	if len(uniq) > MaxModels {
		return nil, invalidf("trop de pièces (%d, maximum %d) : exporter des sous-collections séparément", len(uniq), MaxModels)
	}
	for id := range in.Options.Versions {
		if !seen[id] {
			return nil, invalidf("versions : le modèle %s ne fait pas partie de l'export", id)
		}
	}
	for id := range in.Options.Quantities {
		if !seen[id] {
			return nil, invalidf("quantities : le modèle %s ne fait pas partie de l'export", id)
		}
	}
	slugs := map[string]int{}
	for _, id := range uniq {
		d, err := s.d.Catalog.GetModelDetails(ctx, id)
		if err != nil {
			return nil, err
		}
		if d.Model.DeletedAt != nil {
			if in.CollectionID != "" {
				continue
			}
			return nil, invalidf("le modèle %s est à la corbeille", id)
		}
		pt := &part{Model: d.Model, Detail: d, Qty: 1}
		if q, ok := in.Options.Quantities[id]; ok {
			pt.Qty = q
		}
		var files []contracts.SourceFile
		if want := in.Options.Versions[id]; want != "" {
			for i := range d.Versions {
				if d.Versions[i].ID == want {
					pt.Version = &d.Versions[i]
				}
			}
			if pt.Version == nil {
				return nil, invalidf("modèle %s : version %s inconnue", id, want)
			}
		} else if len(d.Versions) > 0 {
			pt.Version = &d.Versions[0] // la plus récente
		}
		if pt.Version != nil {
			for _, fid := range pt.Version.SourceFileIDs {
				for _, f := range d.Files {
					if f.ID == fid {
						files = append(files, f)
					}
				}
			}
		} else {
			for _, f := range d.Files {
				if f.ID == d.CurrentFileID {
					files = append(files, f)
				}
			}
		}
		pt.Slug = uniqueSlug(slugs, safeSegment(pt.Model.Name, "piece"))
		used := map[string]int{}
		shas := map[string]bool{}
		for _, f := range files {
			switch {
			case f.Availability != contracts.AvailableLinux:
				pl.skipped = append(pl.skipped, Skip{ModelID: id, Name: f.Name, Reason: "non copié sur le serveur (disponible seulement sur Windows)"})
				continue
			case !s.d.Blobs.Has(f.SHA256):
				pl.skipped = append(pl.skipped, Skip{ModelID: id, Name: f.Name, Reason: "contenu absent du stockage"})
				continue
			case shas[f.SHA256]:
				continue // même contenu déjà retenu pour cette pièce
			}
			shas[f.SHA256] = true
			pt.Files = append(pt.Files, fileItem{File: f, Path: "files/" + pt.Slug + "/" + uniqueName(used, safeSegment(f.Name, "fichier"))})
			pl.originalBytes += f.SizeBytes
		}
		if len(pt.Files) == 0 {
			pl.skipped = append(pl.skipped, Skip{ModelID: id, Name: pt.Model.Name, Reason: "aucun fichier exportable : pièce laissée de côté"})
			continue
		}
		if pt.Model.Provenance != nil && pt.Model.Provenance.URL != "" {
			pl.warnings = append(pl.warnings, fmt.Sprintf("« %s » vient d'une source tierce (%s) : vérifier que sa licence permet de la republier.", pt.Model.Name, pt.Model.Provenance.URL))
		}
		pl.parts = append(pl.parts, pt)
	}
	if len(pl.parts) == 0 {
		return nil, invalidf("aucun fichier exportable : les originaux ne sont pas copiés sur le serveur ou les modèles sont à la corbeille")
	}
	switch {
	case in.Options.Title != "":
		pl.title = in.Options.Title
	case pl.title == "" && len(pl.parts) == 1:
		pl.title = pl.parts[0].Model.Name
	case pl.title == "":
		pl.title = fmt.Sprintf("Projet de %d pièces", len(pl.parts))
	}
	if pl.originalBytes > in.Options.MaxBytes {
		return nil, invalidf("les fichiers originaux pèsent %s, au-delà de la limite de %s (max_bytes) : exporter moins de pièces ou relever la limite (plafond %d Gio)",
			humanBytes(pl.originalBytes), humanBytes(in.Options.MaxBytes), HardMaxBytes>>30)
	}
	if in.Options.License == DefaultLicense {
		pl.warnings = append(pl.warnings, "Licence non précisée : à choisir avant toute publication.")
	}
	return pl, nil
}

// ---- noms ----

var reservedNames = map[string]bool{"con": true, "prn": true, "aux": true, "nul": true, "com1": true, "com2": true, "com3": true, "com4": true,
	"com5": true, "com6": true, "com7": true, "com8": true, "com9": true, "lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true}

// safeSegment donne un nom de dossier ou de fichier lisible et inoffensif : jamais de séparateur, de « .. », de
// caractère de contrôle, de nom réservé Windows, ni de point ou d'espace final.
func safeSegment(s, fallback string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x20 || r == 0x7f, strings.ContainsRune(`/\:*?"<>|`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(strings.ReplaceAll(b.String(), "..", "_")), "."))
	if r := []rune(out); len(r) > 80 {
		out = strings.TrimSpace(string(r[:80]))
	}
	out = strings.TrimRight(out, ". ")
	base := strings.ToLower(out)
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	if out == "" {
		return fallback
	}
	if reservedNames[base] {
		out = "_" + out
	}
	return out
}

func uniqueSlug(used map[string]int, s string) string {
	k := strings.ToLower(s)
	used[k]++
	if n := used[k]; n > 1 {
		return fmt.Sprintf("%s-%d", s, n)
	}
	return s
}

// uniqueName ajoute « (n) » avant l'extension quand deux fichiers portent le même nom (jamais d'écrasement).
func uniqueName(used map[string]int, name string) string {
	k := strings.ToLower(name)
	used[k]++
	n := used[k]
	if n == 1 {
		return name
	}
	ext := path.Ext(name)
	if strings.HasSuffix(strings.ToLower(name), ".gcode.3mf") {
		ext = name[len(name)-len(".gcode.3mf"):]
	}
	return fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), n, ext)
}

// validEntryName refuse tout nom d'entrée qui sortirait de l'archive (zip slip) ou serait ambigu.
func validEntryName(n string) bool {
	if n == "" || strings.HasPrefix(n, "/") || strings.ContainsAny(n, "\\:") || !utf8.ValidString(n) {
		return false
	}
	for _, seg := range strings.Split(n, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// archiveName : nom de fichier proposé au téléchargement.
func archiveName(title string, t time.Time) string {
	return safeSegment(title, "projet") + "-" + t.UTC().Format("2006-01-02") + ".zip"
}

func humanBytes(n int64) string {
	const k = 1024.0
	f := float64(n)
	switch {
	case f >= k*k*k:
		return fmt.Sprintf("%.1f Gio", f/(k*k*k))
	case f >= k*k:
		return fmt.Sprintf("%.1f Mio", f/(k*k))
	case f >= k:
		return fmt.Sprintf("%.1f Kio", f/k)
	}
	return fmt.Sprintf("%d o", n)
}
