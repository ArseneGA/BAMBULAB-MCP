package agentlink

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

// Formats et tailles acceptés selon le type de travail. Un agent ne peut pas déposer n'importe quoi dans le
// magasin de blobs : le contenu est contrôlé (signature) avant publication, et chaque type a sa taille maximale.

// PartMaxAge : âge au-delà duquel un fichier .part abandonné est purgé.
const PartMaxAge = 24 * time.Hour

const (
	mib = int64(1) << 20
	gib = int64(1) << 30
)

// extInfo : extensions reconnues (la plus longue d'abord) et taille maximale.
var extInfo = []struct {
	ext string
	max int64
}{
	{".gcode.3mf", gib},
	{".3mf", gib},
	{".stl", 512 * mib},
	{".obj", 512 * mib},
	{".step", 512 * mib},
	{".stp", 512 * mib},
}

// fileExt renvoie l'extension reconnue du nom (« .gcode.3mf » avant « .3mf »), ou "".
func fileExt(name string) string {
	n := strings.ToLower(path.Base(strings.ReplaceAll(name, "\\", "/")))
	for _, e := range extInfo {
		if strings.HasSuffix(n, e.ext) {
			return e.ext
		}
	}
	return ""
}

// MaxSizeFor est la taille maximale d'un fichier de cette extension (plafonnée par global si > 0).
func MaxSizeFor(ext string, global int64) int64 {
	for _, e := range extInfo {
		if e.ext == ext {
			if global > 0 && global < e.max {
				return global
			}
			return e.max
		}
	}
	return 0
}

// outputExts : extensions de SORTIE acceptées selon le type de travail (target artifact).
var outputExts = map[string][]string{
	KindSlice:           {".gcode.3mf", ".3mf"},
	KindGetProjectState: {".3mf"},
}

// importExts : extensions acceptées pour un envoi de fichiers d'inventaire (target inbox).
var importExts = []string{".stl", ".obj", ".3mf", ".step", ".stp"}

// allowedUpload dit si ce type de travail peut recevoir ce fichier sur cette cible, et renvoie son extension.
func allowedUpload(taskKind, target, name string) (string, error) {
	ext := fileExt(name)
	if ext == "" {
		return "", errf(http.StatusUnprocessableEntity, CodeInvalid, "format de fichier non pris en charge : %q", name)
	}
	switch target {
	case TargetInbox:
		if taskKind != KindUploadFiles {
			return "", errf(http.StatusForbidden, CodeNotAllowed, "le travail %s ne peut pas envoyer de fichiers d'import", taskKind)
		}
		for _, e := range importExts {
			if e == ext || (ext == ".gcode.3mf" && e == ".3mf") {
				return ext, nil
			}
		}
	case TargetArtifact:
		for _, e := range outputExts[taskKind] {
			if e == ext {
				return ext, nil
			}
		}
		return "", errf(http.StatusForbidden, CodeNotAllowed, "le travail %s ne peut pas produire de fichier %s", taskKind, ext)
	}
	return "", errf(http.StatusForbidden, CodeNotAllowed, "fichier %s non accepté pour %s", ext, taskKind)
}

// checkContent contrôle la signature du contenu d'un fichier complet (jamais d'exécution, aucune extraction).
func checkContent(file, ext string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	bad := func(format string, a ...any) error {
		return errf(http.StatusUnprocessableEntity, CodeInvalid, "contenu %s invalide : "+format, append([]any{ext}, a...)...)
	}
	head := make([]byte, 64<<10)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	switch ext {
	case ".3mf", ".gcode.3mf":
		zr, err := zip.NewReader(f, st.Size())
		if err != nil {
			return bad("archive ZIP illisible (%v)", err)
		}
		var content, model, gcode bool
		for _, e := range zr.File {
			switch name := strings.ToLower(e.Name); {
			case name == "[content_types].xml":
				content = true
			case strings.HasSuffix(name, ".model"):
				model = true
			case strings.HasSuffix(name, ".gcode"):
				gcode = true
			}
		}
		if !content {
			return bad("[Content_Types].xml absent")
		}
		if ext == ".gcode.3mf" && !gcode {
			return bad("aucun fichier .gcode dans l'archive")
		}
		if ext == ".3mf" && !model && !gcode {
			return bad("aucun modèle 3D dans l'archive")
		}
	case ".stl":
		if len(head) >= 84 {
			tri := int64(binary.LittleEndian.Uint32(head[80:84]))
			if st.Size() == 84+50*tri {
				return nil // STL binaire cohérent
			}
		}
		t := bytes.TrimLeft(head, " \t\r\n\xef\xbb\xbf")
		if !(bytes.HasPrefix(bytes.ToLower(t), []byte("solid")) && bytes.Contains(bytes.ToLower(head), []byte("facet"))) {
			return bad("ni STL binaire (taille incohérente avec le nombre de triangles) ni STL ASCII")
		}
	case ".obj":
		if bytes.IndexByte(head, 0) >= 0 {
			return bad("contenu binaire")
		}
		if !bytes.HasPrefix(head, []byte("v ")) && !bytes.Contains(head, []byte("\nv ")) {
			return bad("aucun sommet (ligne « v »)")
		}
	case ".step", ".stp":
		if !bytes.HasPrefix(bytes.TrimLeft(head[:min(len(head), 1024)], " \t\r\n\xef\xbb\xbf"), []byte("ISO-10303-21")) {
			return bad("en-tête ISO-10303-21 absent")
		}
	default:
		return bad("extension inconnue")
	}
	return nil
}

var _ = fmt.Sprintf
