package slicer

import (
	"fmt"
	"strings"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/mesh"
)

// PlacementError est l'échec de Studio sur la position des pièces (-50 plaque vide / aucun objet entièrement
// dedans, -52 hors plateau, -63/-64 collision), reformulé honnêtement avec les dimensions de la pièce et la
// zone utile réelle de la machine. errors.Is(err, ErrStudioEmptyPlate…) reste vrai.
type PlacementError struct {
	Studio  *StudioError
	Message string
}

func (e *PlacementError) Error() string { return e.Message + " [" + e.Studio.Error() + "]" }
func (e *PlacementError) Unwrap() error { return e.Studio }

// explainPlacement traduit les codes de placement ; nil pour tout autre code.
func explainPlacement(se *StudioError, t mesh.Target, sizes []mesh.Vec3) error {
	switch se.Code {
	case -50, -52, -63, -64:
	default:
		return nil
	}
	var parts []string
	for _, s := range sizes {
		c := t.CheckFootprint(s)
		if c.Message != "" {
			parts = append(parts, c.Message)
		} else {
			parts = append(parts, fmt.Sprintf("pièce de %s : tient dans le plateau, mais Studio l'a refusée (position ou collision entre pièces)", mesh.DimsText(s)))
		}
	}
	msg := ""
	switch {
	case len(parts) > 0:
		msg = strings.Join(parts, " ; ")
	default:
		msg = "pièce hors zone imprimable : " + t.UsableSummary()
	}
	switch se.Code {
	case -50:
		msg = "Bambu Studio a trouvé la plaque vide (aucune pièce entièrement dans la zone imprimable) : " + msg
	case -52:
		msg = "Bambu Studio signale des pièces partiellement hors plateau : " + msg
	default:
		msg = "Bambu Studio signale une collision entre pièces ou avec une zone exclue : " + msg
	}
	return &PlacementError{Studio: se, Message: msg}
}
