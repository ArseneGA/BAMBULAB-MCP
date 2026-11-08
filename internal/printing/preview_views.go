package printing

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"io"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// Planche de vues d'un maillage (capture_preview mode mesh, option views) : jusqu'à quatre vues PNG en un appel.

type viewSpec struct {
	key, label string
	yaw, elev  float64
	note       string
}

var meshViews = []viewSpec{
	{"iso", "ISO", 45, 35.264, "isometrique (lacet 45, elevation 35.3 deg)"},
	{"front", "FACE", 0, 0, "on regarde vers +Y, X vers la droite, Z vers le haut"},
	{"side", "COTE", 90, 0, "on regarde vers +X, Y vers la gauche, Z vers le haut"},
	{"top", "DESSUS", 0, 90, "vue du dessus, X vers la droite, Y vers le haut"},
}

var viewAliases = map[string]string{
	"iso": "iso", "isometric": "iso", "isometrique": "iso",
	"front": "front", "face": "front", "avant": "front",
	"side": "side", "cote": "side", "côté": "side", "left": "side",
	"top": "top", "dessus": "top", "plan": "top",
}

// normalizeViews résout les noms de vues (« all » = les quatre), sans doublon, dans l'ordre demandé.
func normalizeViews(req []string) ([]viewSpec, error) {
	var out []viewSpec
	seen := map[string]bool{}
	add := func(key string) {
		if seen[key] {
			return
		}
		seen[key] = true
		for _, v := range meshViews {
			if v.key == key {
				out = append(out, v)
			}
		}
	}
	for _, r := range req {
		r = strings.ToLower(strings.TrimSpace(r))
		if r == "all" || r == "tout" || r == "toutes" {
			for _, v := range meshViews {
				add(v.key)
			}
			continue
		}
		k, ok := viewAliases[r]
		if !ok {
			return nil, invalidf("vue %q inconnue : iso, front, side, top ou all", r)
		}
		add(k)
	}
	if len(out) == 0 {
		return nil, invalidf("views : au moins une vue (iso, front, side, top ou all)")
	}
	return out, nil
}

func (s *Service) previewMeshViews(ctx context.Context, in PreviewInput, w, h int) (*PreviewOutput, error) {
	if in.ModelID == "" {
		return nil, invalidf("mode mesh : model_id requis")
	}
	vs, err := normalizeViews(in.Views)
	if err != nil {
		return nil, err
	}
	_, src, serr := s.sourceFor(ctx, in.ModelID, in.VersionID)
	if serr != nil {
		return nil, serr
	}
	f, oerr := s.d.Blobs.Open(src.SHA256)
	if oerr != nil {
		return nil, oerr
	}
	data, rerr := io.ReadAll(io.LimitReader(f, 512<<20))
	f.Close()
	if rerr != nil {
		return nil, rerr
	}
	md, perr := mesh.Parse(src.Name, data)
	if perr != nil {
		return nil, invalidf("modèle illisible : %v", perr)
	}
	return renderMeshViews(md.Combined(), src.Name, src.SHA256, vs, w, h)
}

// renderMeshViews : planche de vues en grille de deux colonnes, chaque vue avec son étiquette.
func renderMeshViews(m *mesh.Mesh, name, sha string, vs []viewSpec, w, h int) (*PreviewOutput, error) {
	if len(vs) == 0 {
		return nil, invalidf("aucune vue demandée")
	}
	bb := m.Bounds()
	cols := 2
	if len(vs) == 1 {
		cols = 1
	}
	rows := (len(vs) + cols - 1) / cols
	tw, th := w/cols, h/2
	if len(vs) == 1 {
		th = h
	}
	sheet := image.NewRGBA(image.Rect(0, 0, cols*tw, rows*th))
	for i := range sheet.Pix {
		sheet.Pix[i] = 0x2a
	}
	var keys []string
	for i, v := range vs {
		tile := projectMesh(m, v.yaw, v.elev, tw-2, th-2)
		blit(sheet, tile, (i%cols)*tw+1, (i/cols)*th+1)
		label(sheet, (i%cols)*tw+8, (i/cols)*th+8, 2, color.RGBA{0x2a, 0x4d, 0x8f, 255}, v.label)
		keys = append(keys, v.key)
	}
	lines := []string{
		"Apercu maillage - " + name,
		fmt.Sprintf("Boite englobante %.1f x %.1f x %.1f mm (X x Y x Z), %d triangles", bb.Size.X, bb.Size.Y, bb.Size.Z, len(m.T)),
	}
	for _, v := range vs {
		lines = append(lines, v.label+" : "+v.note)
	}
	lines = append(lines,
		"Projections orthographiques, eclairage Lambert, z-buffer logiciel 2x ; chaque vue est cadree a sa propre echelle (ne pas comparer les tailles entre vues)",
		"Representation de la geometrie seule: ni materiau, ni couleur, ni supports, ni trajectoires")
	img := withLegendPanel(sheet, plain(lines...))
	b, err := encodePNG(img)
	if err != nil {
		return nil, err
	}
	return &PreviewOutput{Kind: "mesh_views", Width: img.Bounds().Dx(), Height: img.Bounds().Dy(), Legend: lines, PNG: b,
		Represented:    []string{"surface triangulée du modèle sous éclairage directionnel fixe", "jusqu'à quatre vues orthographiques : iso, face, côté, dessus"},
		NotRepresented: []string{"matériau et couleur réels", "supports et jupe", "trajectoires d'impression", "défauts de maillage (non surlignés)"},
		Render: map[string]any{"views": keys, "projection": "orthographique", "lighting": "Lambert, 1 source", "supersampling": 2,
			"triangles": len(m.T), "source_sha256": sha, "bbox_mm": []float64{bb.Size.X, bb.Size.Y, bb.Size.Z}}}, nil
}
