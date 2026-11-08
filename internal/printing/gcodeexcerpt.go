package printing

import (
	"archive/zip"
	"bufio"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// read_gcode_excerpt : lecture SEULE, bornée, d'une plaque de G-code dans un .gcode.3mf du stockage. Sert à vérifier
// ce que le fichier demande réellement (mise à niveau G29, calibrations M622 / « ; leveling »…) sans télécharger
// l'archive. Seule l'entrée Metadata/plate_N.gcode est lue : aucun autre fichier de l'archive (profils, réglages)
// n'est exposé, et toute ligne qui ressemble à un secret est masquée.

const (
	gcodeDefaultHead   = 40
	gcodeMaxHead       = 200
	gcodeDefaultMax    = 60
	gcodeMaxMax        = 200
	gcodeMaxLineChars  = 300
	gcodeMaxScanBytes  = 64 << 20 // au plus 64 Mio de G-code décompressé parcourus
	gcodeMaxPatternLen = 120
)

// GcodeExcerptInput est l'entrée de read_gcode_excerpt.
type GcodeExcerptInput struct {
	ArtifactID string `json:"artifact_id,omitempty" jsonschema:"empreinte sha256 d'un .gcode.3mf du stockage"`
	PrintJobID string `json:"print_job_id,omitempty" jsonschema:"à la place d'artifact_id : l'artefact (et la plaque) de cette impression"`
	Plate      int    `json:"plate,omitempty" jsonschema:"plaque à lire (défaut : celle de l'impression, sinon 1)"`
	HeadLines  int    `json:"head_lines,omitempty" jsonschema:"nombre de lignes à lire au début du G-code (0 à 200 ; défaut 40 si grep est absent, 0 sinon)"`
	Grep       string `json:"grep,omitempty" jsonschema:"motif (expression régulière RE2, 120 caractères au plus ; (?i) pour ignorer la casse) cherché sur tout le G-code, ex. G29, M622, (?i); leveling"`
	MaxLines   int    `json:"max_lines,omitempty" jsonschema:"nombre maximal de lignes rendues au total (1 à 200, défaut 60)"`
}

// GcodeLine est une ligne rendue (numérotée à partir de 1).
type GcodeLine struct {
	N    int    `json:"n"`
	Text string `json:"text"`
}

// GcodeExcerptOutput est le résultat de read_gcode_excerpt.
type GcodeExcerptOutput struct {
	ArtifactID   string      `json:"artifact_id"`
	Plate        int         `json:"plate"`
	Entry        string      `json:"entry" jsonschema:"entrée lue dans l'archive"`
	Head         []GcodeLine `json:"head,omitempty" jsonschema:"premières lignes du G-code"`
	Matches      []GcodeLine `json:"matches,omitempty" jsonschema:"lignes qui correspondent au motif (numéro de ligne dans le G-code)"`
	MatchCount   int         `json:"match_count" jsonschema:"nombre total de lignes qui correspondent, dans la partie parcourue"`
	ScannedLines int         `json:"scanned_lines"`
	ScannedBytes int64       `json:"scanned_bytes"`
	Truncated    bool        `json:"truncated" jsonschema:"vrai si des lignes ont été omises (limite max_lines) ou si la limite de parcours (64 Mio) est atteinte"`
	Note         string      `json:"note"`
}

var secretLine = regexp.MustCompile(`(?i)(access[_ -]?code|password|passwd|secret|api[_ -]?key|bearer |token)\s*[=:]`)

func clipLine(s string) string {
	s = strings.TrimRight(s, "\r\n")
	if secretLine.MatchString(s) {
		return "[ligne masquée : ressemble à un secret]"
	}
	if len(s) > gcodeMaxLineChars {
		return s[:gcodeMaxLineChars] + "…"
	}
	return s
}

// ReadGcodeExcerpt lit des lignes d'une plaque de G-code (lecture seule, bornée).
func (s *Service) ReadGcodeExcerpt(ctx context.Context, in GcodeExcerptInput) (*GcodeExcerptOutput, error) {
	sha := strings.TrimSpace(in.ArtifactID)
	plate := in.Plate
	if in.PrintJobID != "" {
		j, err := s.GetJob(ctx, in.PrintJobID)
		if err != nil {
			return nil, err
		}
		sha = j.ArtifactSHA256
		if plate == 0 {
			plate = j.Plate
		}
	}
	if sha == "" {
		return nil, invalidf("artifact_id ou print_job_id requis")
	}
	if plate <= 0 {
		plate = 1
	}
	if plate > 64 {
		return nil, invalidf("plate : 1 à 64")
	}
	head, maxLines := in.HeadLines, in.MaxLines
	if head < 0 || head > gcodeMaxHead {
		return nil, invalidf("head_lines : 0 à %d", gcodeMaxHead)
	}
	if maxLines < 0 || maxLines > gcodeMaxMax {
		return nil, invalidf("max_lines : 1 à %d", gcodeMaxMax)
	}
	if maxLines == 0 {
		maxLines = gcodeDefaultMax
	}
	var re *regexp.Regexp
	if g := in.Grep; g != "" {
		if len(g) > gcodeMaxPatternLen {
			return nil, invalidf("grep : %d caractères au plus", gcodeMaxPatternLen)
		}
		var err error
		if re, err = regexp.Compile(g); err != nil { // RE2 : temps linéaire, pas de retour arrière catastrophique
			return nil, invalidf("grep : motif invalide (%v)", err)
		}
	}
	if head == 0 && re == nil {
		head = gcodeDefaultHead
	}
	if head > maxLines {
		head = maxLines
	}
	if !s.d.Blobs.Has(sha) {
		return nil, fmt.Errorf("%w : artefact absent du stockage", ErrNotFound)
	}
	path, err := s.d.Blobs.Path(sha)
	if err != nil {
		return nil, err
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, invalidf("archive illisible : %v", err)
	}
	defer zr.Close()
	entry := fmt.Sprintf("Metadata/plate_%d.gcode", plate)
	var zf *zip.File
	for _, f := range zr.File {
		if f.Name == entry {
			zf = f
			break
		}
	}
	if zf == nil {
		return nil, fmt.Errorf("%w : %s absent de l'archive", ErrNotFound, entry)
	}
	rc, err := zf.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	br := bufio.NewReaderSize(io.LimitReader(rc, gcodeMaxScanBytes), 1<<20)
	out := &GcodeExcerptOutput{ArtifactID: sha, Plate: plate, Entry: entry}
	room := maxLines
	for n := 1; ; n++ {
		if n%4096 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		line, rerr := br.ReadString('\n')
		if line != "" {
			out.ScannedLines = n
			out.ScannedBytes += int64(len(line))
			if n <= head && room > 0 {
				out.Head = append(out.Head, GcodeLine{N: n, Text: clipLine(line)})
				room--
			}
			if re != nil && re.MatchString(strings.TrimRight(line, "\r\n")) {
				out.MatchCount++
				if room > 0 {
					out.Matches = append(out.Matches, GcodeLine{N: n, Text: clipLine(line)})
					room--
				} else {
					out.Truncated = true
				}
			}
			if head == 0 && re == nil {
				break
			}
			if re == nil && n >= head {
				break // rien d'autre à chercher : inutile de lire la suite
			}
		}
		if rerr != nil {
			break
		}
	}
	if out.ScannedBytes >= gcodeMaxScanBytes {
		out.Truncated = true
	}
	out.Note = fmt.Sprintf("Lecture seule de %s (%d ligne(s) parcourues). Seule la plaque de G-code est lue ; les lignes de plus de %d caractères sont tronquées.", entry, out.ScannedLines, gcodeMaxLineChars)
	if re != nil && out.MatchCount == 0 {
		out.Note += " Aucune ligne ne correspond au motif dans la partie parcourue."
	}
	if out.Truncated {
		out.Note += " Résultat tronqué (limite de lignes ou de 64 Mio de parcours) : affiner le motif."
	}
	return out, nil
}
