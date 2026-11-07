package export

import (
	"archive/zip"
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/insights"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Entrée de manifeste.
type manifestFile struct {
	Path    string `json:"path"`
	Role    string `json:"role" jsonschema:"original | parametric_source | profile | sliced_gcode_3mf | photo | readme"`
	ModelID string `json:"model_id,omitempty"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size_bytes"`
}

type manifestPart struct {
	ModelID    string         `json:"model_id"`
	Name       string         `json:"name"`
	Folder     string         `json:"folder"`
	Quantity   int            `json:"quantity"`
	VersionID  string         `json:"version_id,omitempty"`
	Version    string         `json:"version_label,omitempty"`
	License    string         `json:"license,omitempty"`
	Provenance map[string]any `json:"provenance,omitempty"`
	Files      []string       `json:"files"`
	Parametric string         `json:"parametric_source,omitempty"`
	Profile    string         `json:"profile,omitempty"`
	Gcode      string         `json:"sliced_gcode_3mf,omitempty"`
	Photos     []string       `json:"photos,omitempty"`
	Estimate   map[string]any `json:"estimate,omitempty"`
}

type manifest struct {
	Format      string         `json:"format"`
	GeneratedAt time.Time      `json:"generated_at"`
	Generator   string         `json:"generator"`
	Title       string         `json:"title"`
	Collection  string         `json:"collection,omitempty"`
	License     string         `json:"license"`
	Parts       []manifestPart `json:"parts"`
	Files       []manifestFile `json:"files"`
	Skipped     []Skip         `json:"skipped"`
	Warnings    []string       `json:"warnings"`
	Notice      string         `json:"notice"`
}

// archive écrit un zip en flux : mémoire constante quelle que soit la taille des fichiers.
type archive struct {
	zw      *zip.Writer
	files   []manifestFile
	names   map[string]bool
	max     int64
	written int64 // octets décompressés
	now     time.Time
	ctx     context.Context
}

func newArchive(ctx context.Context, w io.Writer, max int64, now time.Time) *archive {
	zw := zip.NewWriter(w)
	zw.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) { return flate.NewWriter(out, flate.BestSpeed) })
	return &archive{zw: zw, names: map[string]bool{}, max: max, now: now, ctx: ctx}
}

// déjà compressés : stockés sans recompression.
func stored(name string) bool {
	n := strings.ToLower(name)
	for _, ext := range []string{".3mf", ".zip", ".jpg", ".jpeg", ".png", ".webp", ".gz"} {
		if strings.HasSuffix(n, ext) {
			return true
		}
	}
	return false
}

func (a *archive) header(name string, mod time.Time) (io.Writer, error) {
	if !validEntryName(name) {
		return nil, fmt.Errorf("nom d'entrée refusé : %q", name)
	}
	if a.names[strings.ToLower(name)] {
		return nil, fmt.Errorf("entrée en double : %q", name)
	}
	a.names[strings.ToLower(name)] = true
	m := zip.Deflate
	if stored(name) {
		m = zip.Store
	}
	if mod.IsZero() {
		mod = a.now
	}
	return a.zw.CreateHeader(&zip.FileHeader{Name: name, Method: m, Modified: mod})
}

func (a *archive) account(n int64) error {
	a.written += n
	if a.written > a.max {
		return invalidf("l'archive dépasse la limite de %s (max_bytes)", humanBytes(a.max))
	}
	return nil
}

func (a *archive) addBytes(name, role, modelID string, data []byte) error {
	if err := a.account(int64(len(data))); err != nil {
		return err
	}
	w, err := a.header(name, a.now)
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	a.files = append(a.files, manifestFile{Path: name, Role: role, ModelID: modelID, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))})
	return nil
}

// ctxReader interrompt une copie dès que le travail est annulé.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// addStream copie r dans l'archive en recalculant le sha256 ; une divergence avec expect (si fourni) interrompt.
func (a *archive) addStream(name, role, modelID string, r io.Reader, expect string, mod time.Time) error {
	w, err := a.header(name, mod)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, h), ctxReader{a.ctx, r})
	if err != nil {
		return err
	}
	if err := a.account(n); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if expect != "" && got != expect {
		return fmt.Errorf("%w : l'empreinte de %s ne correspond plus au catalogue", store.ErrBlobCorrupt, name)
	}
	a.files = append(a.files, manifestFile{Path: name, Role: role, ModelID: modelID, SHA256: got, Size: n})
	return nil
}

// countWriter borne ce qui est réellement écrit sur disque (garde-fou en plus du décompte décompressé).
type countWriter struct {
	w   io.Writer
	n   int64
	max int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	if c.n > c.max {
		return 0, invalidf("l'archive dépasse la limite de %s", humanBytes(c.max))
	}
	return c.w.Write(p)
}

// enrichPart rassemble tout ce qui accompagne les fichiers d'une pièce. Les échecs partiels deviennent des
// avertissements : seule une fuite détectée dans un fichier retire ce fichier de l'archive.
func (s *Service) enrichPart(ctx context.Context, pl *plan, pt *part, sc *scrubber) {
	warn := func(f string, a ...any) { pt.Warnings = append(pt.Warnings, fmt.Sprintf(f, a...)) }
	o := pl.in.Options

	// Fichiers .3mf d'origine : contrôlés (adresse ou série d'imprimante, chemin du serveur) avant d'être joints.
	kept := pt.Files[:0]
	for _, fi := range pt.Files {
		if fi.File.Format == contracts.Format3MF {
			if f, err := s.d.Blobs.Open(fi.File.SHA256); err == nil {
				found, serr := sc.scanArchive(f, fi.File.SizeBytes, false)
				f.Close()
				if serr == nil && len(found) > 0 {
					pl.originalBytes -= fi.File.SizeBytes
					pl.skipped = append(pl.skipped, Skip{ModelID: pt.Model.ID, Name: fi.File.Name, Reason: "le fichier contient des données à ne pas partager (" + strings.Join(found, ", ") + ") : non inclus ; le ré-enregistrer sans ces données depuis Bambu Studio"})
					continue
				}
			}
		}
		kept = append(kept, fi)
	}
	pt.Files = kept

	var err error
	if pt.Template, err = s.loadTemplate(ctx, pt); err != nil {
		warn("source paramétrique illisible : %v", err)
	}
	if pt.Print, err = s.loadPrint(ctx, pt); err != nil {
		warn("impressions illisibles : %v", err)
	}
	if pt.Profile, err = s.loadProfile(ctx, pt); err != nil {
		warn("profil de découpe illisible : %v", err)
	}
	if pt.Print != nil && pt.Print.Seconds > 0 && s.d.Insights != nil {
		if est, err := s.d.Insights.EstimateCost(ctx, insights.EstimateInput{PrintJobID: pt.Print.JobID}); err == nil && est != nil {
			e := est.Estimate
			pt.Cost = &e
		}
	}
	if o.IncludeGcode3MF {
		switch {
		case pt.Print == nil:
			warn("aucune découpe enregistrée : pas de .gcode.3mf à joindre")
		case !pt.Print.Validated:
			warn("la découpe la plus récente n'est pas validée : .gcode.3mf non joint (valider l'archive avec validate_print_artifact)")
		default:
			if f, err := s.d.Blobs.Open(pt.Print.ArtifactSHA); err != nil {
				warn(".gcode.3mf absent du stockage : non joint")
			} else {
				st, _ := f.Stat()
				found, serr := sc.scanArchive(f, st.Size(), true)
				f.Close()
				switch {
				case serr != nil:
					warn(".gcode.3mf illisible (%v) : non joint", serr)
				case len(found) > 0:
					warn(".gcode.3mf contient des données à ne pas partager (%s) : non joint", strings.Join(found, ", "))
				default:
					name := safeSegment(strings.TrimSuffix(pt.Print.Name, ".gcode.3mf"), pt.Slug)
					pt.Gcode = &fileItem{File: contracts.SourceFile{SHA256: pt.Print.ArtifactSHA, SizeBytes: st.Size(), Name: name + ".gcode.3mf"},
						Path: "printer-specific/" + pt.Slug + "/" + name + ".gcode.3mf"}
				}
			}
		}
	}
	if o.IncludePhotos {
		if pt.Photos, err = s.loadPhotos(ctx, pt); err != nil {
			warn("photos du journal illisibles : %v", err)
		}
	}
}

// extraBytes : poids des fichiers ajoutés par enrichPart (G-code, photos).
func (p *plan) extraBytes() int64 {
	var n int64
	for _, pt := range p.parts {
		if pt.Gcode != nil {
			n += pt.Gcode.File.SizeBytes
		}
		for _, ph := range pt.Photos {
			n += ph.Size
		}
	}
	return n
}

// build construit l'archive dans <données>/exports/<id>.zip (écriture atomique) et renvoie résumé, taille, sha256.
func (s *Service) build(ctx context.Context, jc *jobs.JobCtx, id, _ string, in Input) (sum *Summary, size int64, sha string, err error) {
	pl, err := s.resolve(ctx, in)
	if err != nil {
		return nil, 0, "", err
	}
	sc := newScrubber(ctx, s.db(), s.d.DataDir)
	now := s.now()
	n := len(pl.parts)
	for i, pt := range pl.parts {
		if err := ctx.Err(); err != nil {
			return nil, 0, "", err
		}
		jc.Progress(0.02+0.18*float64(i)/float64(n), fmt.Sprintf("Collecte : %s", pt.Model.Name))
		s.enrichPart(ctx, pl, pt, sc)
	}
	// pièces vidées par le contrôle des fichiers : retirées (et dites)
	parts := pl.parts[:0]
	for _, pt := range pl.parts {
		if len(pt.Files) == 0 {
			pl.skipped = append(pl.skipped, Skip{ModelID: pt.Model.ID, Name: pt.Model.Name, Reason: "aucun fichier partageable : pièce laissée de côté"})
			continue
		}
		parts = append(parts, pt)
	}
	pl.parts = parts
	if len(pl.parts) == 0 {
		return nil, 0, "", invalidf("aucun fichier partageable dans cet export")
	}
	if total := pl.originalBytes + pl.extraBytes(); total > in.Options.MaxBytes {
		return nil, 0, "", invalidf("l'archive pèserait %s, au-delà de la limite de %s (max_bytes)", humanBytes(total), humanBytes(in.Options.MaxBytes))
	}

	tmp := s.path(id) + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, 0, "", fmt.Errorf("création de l'archive : %w", err)
	}
	defer func() {
		if err != nil {
			f.Close()
			_ = os.Remove(tmp)
		}
	}()
	h := sha256.New()
	cw := &countWriter{w: io.MultiWriter(f, h), max: in.Options.MaxBytes + 64<<20}
	a := newArchive(ctx, cw, in.Options.MaxBytes, now)

	if err = a.addBytes("README.md", "readme", "", sc.Bytes([]byte(buildReadme(pl, now, s.d.Version)))); err != nil {
		return nil, 0, "", err
	}
	mf := manifest{Format: FormatVersion, GeneratedAt: now.UTC(), Generator: "Atelier Bambu " + s.d.Version, Title: sc.Text(pl.title),
		Collection: sc.Text(pl.collectionPath), License: sc.Text(in.Options.License), Skipped: []Skip{}, Warnings: []string{}}
	for i, pt := range pl.parts {
		jc.Progress(0.2+0.75*float64(i)/float64(len(pl.parts)), fmt.Sprintf("Écriture : %s", pt.Model.Name))
		mp := manifestPart{ModelID: pt.Model.ID, Name: sc.Text(pt.Model.Name), Folder: pt.Slug, Quantity: pt.Qty, License: sc.Text(pt.Model.License), Files: []string{}}
		if pt.Version != nil {
			mp.VersionID, mp.Version = pt.Version.ID, sc.Text(pt.Version.Label)
		}
		if p := pt.Model.Provenance; p != nil {
			mp.Provenance = map[string]any{}
			for k, v := range map[string]string{"source": p.Source, "url": p.URL, "author": p.Author} {
				if v != "" {
					mp.Provenance[k] = sc.Text(v)
				}
			}
			if !p.ImportedAt.IsZero() {
				mp.Provenance["imported_at"] = p.ImportedAt.UTC()
			}
		}
		for _, fi := range pt.Files {
			if err = s.addBlob(ctx, a, fi, "original", pt.Model.ID); err != nil {
				return nil, 0, "", err
			}
			mp.Files = append(mp.Files, fi.Path)
		}
		if pt.Template != nil {
			name := "sources/" + pt.Slug + "/parametrique.json"
			if err = a.addBytes(name, "parametric_source", pt.Model.ID, sc.Bytes(mustJSON(pt.Template))); err != nil {
				return nil, 0, "", err
			}
			mp.Parametric = name
		}
		if pt.Profile != nil {
			name := "profiles/" + pt.Slug + ".json"
			if err = a.addBytes(name, "profile", pt.Model.ID, sc.Bytes(mustJSON(pt.Profile))); err != nil {
				return nil, 0, "", err
			}
			mp.Profile = name
		}
		if pt.Gcode != nil {
			if err = s.addBlob(ctx, a, *pt.Gcode, "sliced_gcode_3mf", pt.Model.ID); err != nil {
				return nil, 0, "", err
			}
			mp.Gcode = pt.Gcode.Path
		}
		for _, ph := range pt.Photos {
			if err = s.addPhoto(ctx, a, ph, pt); err != nil {
				return nil, 0, "", err
			}
			mp.Photos = append(mp.Photos, ph.Path)
		}
		if pt.Print != nil && pt.Print.Seconds > 0 {
			mp.Estimate = map[string]any{"seconds_per_copy": pt.Print.Seconds, "grams_per_copy": pt.Print.Grams, "basis": "archive découpée par Bambu Studio"}
			if pt.Cost != nil {
				mp.Estimate["cost_per_copy"] = pt.Cost.Total
				mp.Estimate["currency"] = pt.Cost.Currency
				mp.Estimate["note"] = "estimation indicative, pas une facture"
			}
		}
		mf.Parts = append(mf.Parts, mp)
		for _, w := range pt.Warnings {
			pl.warnings = append(pl.warnings, fmt.Sprintf("%s : %s", pt.Model.Name, w))
		}
	}
	jc.Progress(0.97, "Manifeste")
	mf.Skipped = append(mf.Skipped, pl.skipped...)
	mf.Warnings = append(mf.Warnings, pl.warnings...)
	for i := range mf.Skipped {
		mf.Skipped[i].Name, mf.Skipped[i].Reason = sc.Text(mf.Skipped[i].Name), sc.Text(mf.Skipped[i].Reason)
	}
	for i := range mf.Warnings {
		mf.Warnings[i] = sc.Text(mf.Warnings[i])
	}
	mf.Notice = "Rien n'est publié automatiquement. Aucun secret, aucune donnée d'imprimante ni aucun chemin du serveur dans cette archive. Les sha256 couvrent chaque fichier de l'archive hors manifest.json."
	sort.Slice(a.files, func(i, j int) bool { return a.files[i].Path < a.files[j].Path })
	mf.Files = a.files
	if err = a.addBytes("manifest.json", "manifest", "", sc.Bytes(mustJSONIndent(mf))); err != nil {
		return nil, 0, "", err
	}
	if err = a.zw.Close(); err != nil {
		return nil, 0, "", err
	}
	if err = f.Sync(); err != nil {
		return nil, 0, "", err
	}
	if err = f.Close(); err != nil {
		return nil, 0, "", err
	}
	if err = os.Rename(tmp, s.path(id)); err != nil {
		return nil, 0, "", err
	}
	sum = &Summary{Parts: len(pl.parts), Files: len(a.files), Licence: in.Options.License, Warnings: mf.Warnings, Skipped: mf.Skipped}
	for _, e := range a.files {
		switch e.Role {
		case "sliced_gcode_3mf":
			sum.IncludesGcode = true
		case "photo":
			sum.IncludesPhotos = true
		}
	}
	jc.Progress(1, "Archive prête")
	return sum, cw.n, hex.EncodeToString(h.Sum(nil)), nil
}

// addBlob copie un fichier du stockage par empreintes, sha256 recalculé à la volée.
func (s *Service) addBlob(ctx context.Context, a *archive, fi fileItem, role, modelID string) error {
	if fi.File.ID != "" && role == "original" {
		// OpenOriginal refuse un fichier Windows seul ou d'un modèle en corbeille.
		o, err := s.d.Catalog.OpenOriginal(ctx, fi.File.ID)
		if err != nil {
			return err
		}
		defer o.Body.Close()
		return a.addStream(fi.Path, role, modelID, o.Body, fi.File.SHA256, fi.File.CreatedAt)
	}
	body, err := s.d.Blobs.Open(fi.File.SHA256)
	if err != nil {
		return fmt.Errorf("fichier %s absent du stockage : %w", fi.File.Name, err)
	}
	defer body.Close()
	return a.addStream(fi.Path, role, modelID, body, fi.File.SHA256, time.Time{})
}

func (s *Service) addPhoto(ctx context.Context, a *archive, ph photoItem, pt *part) error {
	f, _, err := s.d.Insights.OpenPhoto(ctx, ph.LogID, ph.SHA)
	if err != nil {
		return fmt.Errorf("photo de %s : %w", pt.Model.Name, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 64<<20))
	if err != nil {
		return err
	}
	if strings.HasSuffix(ph.Path, ".jpg") {
		data = stripJPEGMetadata(data)
	}
	return a.addBytes(ph.Path, "photo", pt.Model.ID, data)
}

// stripJPEGMetadata retire les segments EXIF/XMP (APP1), IPTC (APP13) et commentaires d'un JPEG (position GPS,
// appareil, date…). Un fichier mal formé est renvoyé tel quel.
func stripJPEGMetadata(b []byte) []byte {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return b
	}
	out := make([]byte, 0, len(b))
	out = append(out, 0xFF, 0xD8)
	i := 2
	for i+3 < len(b) {
		if b[i] != 0xFF {
			return b
		}
		m := b[i+1]
		switch {
		case m == 0xFF:
			i++
			continue
		case m == 0xDA: // début des données image : le reste est copié tel quel
			return append(out, b[i:]...)
		case m == 0x01 || (m >= 0xD0 && m <= 0xD9):
			out = append(out, b[i], b[i+1])
			i += 2
			continue
		}
		l := int(b[i+2])<<8 | int(b[i+3])
		if l < 2 || i+2+l > len(b) {
			return b
		}
		if m != 0xE1 && m != 0xED && m != 0xFE {
			out = append(out, b[i:i+2+l]...)
		}
		i += 2 + l
	}
	return b
}

func mustJSON(v any) []byte {
	b, _ := json.MarshalIndent(v, "", "  ")
	return append(b, '\n')
}

func mustJSONIndent(v any) []byte { return mustJSON(v) }
