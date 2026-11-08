package printing

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
)

// Fichiers de la carte SD de l'imprimante (FTPS) : liste en lecture seule reliée aux impressions et profils connus,
// nettoyage des versions obsolètes avec validation humaine (liste EXACTE), noms de dépôt parlants pour les nouveaux dépôts.

// PrinterFile : un fichier de la carte SD, relié à ce que bambud en sait.
type PrinterFile struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified,omitzero"`
	SHA12    string    `json:"sha12,omitempty" jsonschema:"empreinte courte lue dans le nom (fichier déposé par bambud)"`
	Managed  bool      `json:"managed" jsonschema:"vrai si le nom a la forme des dépôts de bambud (seuls ceux-là peuvent être nettoyés)"`
	// Liens
	PrintJobIDs     []string `json:"print_job_ids,omitempty" jsonschema:"impressions connues qui ont déposé ce fichier (la plus récente d'abord)"`
	LastJobState    string   `json:"last_job_state,omitempty"`
	ModelID         string   `json:"model_id,omitempty"`
	ModelName       string   `json:"model_name,omitempty"`
	ProfileRevision string   `json:"profile_revision,omitempty" jsonschema:"profil de découpe utilisé"`
	SHA256          string   `json:"sha256,omitempty"`
	InUse           bool     `json:"in_use" jsonschema:"vrai si une impression en cours ou en attente de lancement s'en sert : jamais supprimable"`
	Reprintable     bool     `json:"reprintable,omitempty" jsonschema:"un reprint_uploaded est possible (impression terminée ou échouée)"`
	Obsolete        bool     `json:"obsolete" jsonschema:"candidat au nettoyage : version plus ancienne d'un même modèle, ou fichier sans impression connue"`
	Reason          string   `json:"reason,omitempty"`
}

// FileList est le résultat de list_printer_files.
type FileList struct {
	PrinterID string        `json:"printer_id"`
	Files     []PrinterFile `json:"files"`
	Total     int           `json:"total"`
	Obsolete  int           `json:"obsolete"`
	Bytes     int64         `json:"bytes"`
	Note      string        `json:"note"`
}

func activeOrPending(st contracts.PrintState) bool {
	switch st {
	case contracts.PrintUploading, contracts.PrintUploaded, contracts.PrintAwaitingConfirmation, contracts.PrintStartRequested,
		contracts.PrintPrinting, contracts.PrintStartUnknown:
		return true
	}
	return false
}

// ListPrinterFiles liste (FTPS, lecture seule) les fichiers de la carte SD et les relie aux impressions connues.
// keepLatest : combien de versions récentes garder par modèle avant de marquer le reste « obsolète » (défaut 1).
func (s *Service) ListPrinterFiles(ctx context.Context, printerID string, keepLatest int) (*FileList, error) {
	pid, err := s.ResolvePrinterID(ctx, printerID)
	if err != nil {
		return nil, err
	}
	ad, _, err := s.adapter(ctx, pid)
	if err != nil {
		return nil, err
	}
	fl, ok := ad.(printer.FileLister)
	if !ok {
		return nil, s.unsupported(ctx, pid, "la lecture de la carte SD")
	}
	cctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	remote, err := fl.ListFiles(cctx)
	if err != nil {
		return nil, fmt.Errorf("%w : lecture de la carte SD impossible : %v", ErrUnavailable, err)
	}
	if keepLatest < 1 {
		keepLatest = 1
	}
	all, err := s.ListJobs(ctx, "", 0)
	if err != nil {
		return nil, err
	}
	byName := map[string][]Job{}
	bySHA := map[string][]Job{}
	for _, j := range all { // récentes d'abord
		if j.PrinterID != pid {
			continue
		}
		if j.Remote != nil {
			byName[j.Remote.Name] = append(byName[j.Remote.Name], j)
		}
		if len(j.ArtifactSHA256) >= 12 && j.Remote != nil {
			bySHA[j.ArtifactSHA256[:12]] = append(bySHA[j.ArtifactSHA256[:12]], j)
		}
	}
	out := &FileList{PrinterID: pid, Files: []PrinterFile{}}
	for _, rf := range remote {
		pf := PrinterFile{Name: rf.Name, Size: rf.Size, Modified: rf.Modified}
		pf.SHA12, pf.Managed = printer.SafeRemoteName(rf.Name)
		linked := byName[rf.Name]
		if len(linked) == 0 && pf.SHA12 != "" {
			linked = bySHA[pf.SHA12]
		}
		for _, j := range linked {
			pf.PrintJobIDs = append(pf.PrintJobIDs, j.ID)
			if activeOrPending(j.State) {
				pf.InUse = true
			}
		}
		if len(linked) > 0 {
			l := linked[0]
			pf.LastJobState, pf.ModelID, pf.ProfileRevision, pf.SHA256 = string(l.State), l.ModelID, l.ProfileRevision, l.ArtifactSHA256
			if l.ModelID != "" {
				if m, err := s.d.Store.GetModel(ctx, l.ModelID); err == nil {
					pf.ModelName = m.Name
				}
			}
			pf.Reprintable = l.State.Terminal() && l.Remote != nil && !pf.InUse
		}
		out.Files = append(out.Files, pf)
		out.Bytes += rf.Size
	}
	s.markObsolete(out.Files, byName, bySHA, keepLatest)
	sort.SliceStable(out.Files, func(i, k int) bool { return out.Files[i].Modified.After(out.Files[k].Modified) })
	for _, f := range out.Files {
		if f.Obsolete {
			out.Obsolete++
		}
	}
	out.Total = len(out.Files)
	out.Note = "Lecture seule. Seuls les fichiers déposés par bambud (nom <radical>-<empreinte>.gcode.3mf) peuvent être nettoyés, jamais un fichier en cours d'usage ; cleanup_printer_files demande la validation de l'humain avec la liste exacte."
	return out, nil
}

// markObsolete : par famille (modèle, sinon radical du nom), garde les keepLatest plus récentes ; les autres fichiers de
// bambud sans usage en cours sont obsolètes ; un fichier sans impression connue l'est s'il a plus de 24 h.
func (s *Service) markObsolete(files []PrinterFile, byName, bySHA map[string][]Job, keepLatest int) {
	type ent struct {
		i  int
		at time.Time
	}
	fam := map[string][]ent{}
	for i := range files {
		f := &files[i]
		if !f.Managed {
			continue
		}
		key := f.ModelID
		if key == "" {
			key = "stem:" + stemOfRemote(f.Name)
		}
		at := f.Modified
		if linked := byName[f.Name]; len(linked) > 0 && linked[0].CreatedAt.After(at) {
			at = linked[0].CreatedAt
		} else if len(linked) == 0 && f.SHA12 != "" && len(bySHA[f.SHA12]) > 0 && bySHA[f.SHA12][0].CreatedAt.After(at) {
			at = bySHA[f.SHA12][0].CreatedAt
		}
		fam[key] = append(fam[key], ent{i: i, at: at})
	}
	now := s.now()
	for _, es := range fam {
		sort.SliceStable(es, func(a, b int) bool { return es[a].at.After(es[b].at) })
		for rank, e := range es {
			f := &files[e.i]
			switch {
			case f.InUse:
				f.Reason = "utilisé par une impression en cours ou en attente de lancement"
			case len(f.PrintJobIDs) == 0 && now.Sub(f.Modified) > 24*time.Hour && !f.Modified.IsZero():
				f.Obsolete, f.Reason = true, "aucune impression connue pour ce fichier (déposé hors de cette base ?) et plus de 24 h"
			case len(f.PrintJobIDs) == 0:
				f.Reason = "aucune impression connue, mais déposé il y a moins de 24 h : conservé"
			case rank >= keepLatest:
				f.Obsolete, f.Reason = true, fmt.Sprintf("version plus ancienne (%de) du même modèle ; les %d plus récentes sont gardées", rank+1, keepLatest)
			default:
				f.Reason = "version la plus récente de son modèle : conservée"
			}
		}
	}
}

// stemOfRemote : « porte_guitare_supports_v3-ab12cd34ef56.gcode.3mf » -> « porte_guitare_supports_v3 ».
func stemOfRemote(name string) string {
	if i := strings.LastIndex(name, "-"); i > 0 {
		return name[:i]
	}
	return name
}

// RequestCleanup crée la demande de nettoyage : liste EXACTE des fichiers obsolètes, à valider par l'humain sur /action/{id}.
// names (facultatif) restreint la liste ; il ne peut citer que des candidats.
func (s *Service) RequestCleanup(ctx context.Context, printerID string, keepLatest int, names []string) (*ActionOutput, error) {
	fl, err := s.ListPrinterFiles(ctx, printerID, keepLatest)
	if err != nil {
		return nil, err
	}
	cand := map[string]PrinterFile{}
	var order []string
	for _, f := range fl.Files {
		if f.Obsolete && f.Managed && !f.InUse {
			cand[f.Name] = f
			order = append(order, f.Name)
		}
	}
	sel := order
	if len(names) > 0 {
		sel = nil
		for _, n := range names {
			if _, ok := cand[n]; !ok {
				return nil, invalidf("%q n'est pas un candidat au nettoyage (list_printer_files : obsolete=true)", n)
			}
			sel = append(sel, n)
		}
	}
	if len(sel) == 0 {
		return nil, fmt.Errorf("%w : aucun fichier obsolète à supprimer sur la carte (%d fichier(s) vus)", ErrState, fl.Total)
	}
	sort.Strings(sel)
	var det []string
	var total int64
	for _, n := range sel {
		f := cand[n]
		total += f.Size
		det = append(det, fmt.Sprintf("%s (%.1f Mo) : %s", n, float64(f.Size)/1e6, f.Reason))
	}
	r := actionReq{kind: ActionCleanup, printerID: fl.PrinterID, names: sel}
	id, exp := s.putAction(r)
	_ = s.d.Store.Audit(ctx, actor, "printer.cleanup_requested", "printer", fl.PrinterID, map[string]any{"action": id, "files": sel})
	q := fmt.Sprintf("Veux-tu supprimer ces %d fichier(s) obsolète(s) de la carte SD de l'imprimante (%.1f Mo) ?", len(sel), float64(total)/1e6)
	return s.actionOut(id, exp, r, q, "Rien n'est supprimé. Demande à l'utilisateur de valider (carte ou action_url dans son navigateur enregistré) : il voit la liste exacte.", det), nil
}

func (s *Service) doCleanup(ctx context.Context, req actionReq, who string) (*ActionResult, error) {
	ad, _, err := s.adapter(ctx, req.printerID)
	if err != nil {
		return nil, err
	}
	del, ok := ad.(printer.FileDeleter)
	if !ok {
		return nil, s.unsupported(ctx, req.printerID, "la suppression de fichiers de la carte SD")
	}
	// Revérification au moment de supprimer : un fichier devenu utile depuis la demande n'est JAMAIS supprimé.
	all, _ := s.ListJobs(ctx, "", 0)
	res := &ActionResult{Kind: ActionCleanup}
	dctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for _, n := range req.names {
		inUse := false
		for _, j := range all {
			if j.PrinterID == req.printerID && j.Remote != nil && j.Remote.Name == n && activeOrPending(j.State) {
				inUse = true
			}
		}
		if inUse {
			res.Failed = append(res.Failed, n+" : désormais utilisé par une impression, conservé")
			continue
		}
		if err := del.DeleteFile(dctx, n); err != nil {
			res.Failed = append(res.Failed, n+" : "+err.Error())
			continue
		}
		res.Deleted = append(res.Deleted, n)
	}
	res.Done = len(res.Failed) == 0
	res.Message = fmt.Sprintf("%d fichier(s) supprimé(s), %d en échec.", len(res.Deleted), len(res.Failed))
	_ = s.d.Store.Audit(ctx, who, "printer.cleanup_done", "printer", req.printerID, map[string]any{"deleted": res.Deleted, "failed": res.Failed})
	return res, nil
}

// ---- noms de dépôt parlants (NOUVEAUX dépôts seulement) ----

var nameSlug = func(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prev := '_'
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if !ok {
			switch r {
			case 'à', 'â', 'ä':
				r, ok = 'a', true
			case 'é', 'è', 'ê', 'ë':
				r, ok = 'e', true
			case 'î', 'ï':
				r, ok = 'i', true
			case 'ô', 'ö':
				r, ok = 'o', true
			case 'ù', 'û', 'ü':
				r, ok = 'u', true
			case 'ç':
				r, ok = 'c', true
			}
		}
		if ok {
			b.WriteRune(r)
			prev = r
		} else if prev != '_' {
			b.WriteRune('_')
			prev = '_'
		}
	}
	out := strings.Trim(b.String(), "_")
	if len(out) > 40 {
		out = strings.Trim(out[:40], "_")
	}
	return out
}

// depositName compose le nom sous lequel un NOUVEAU dépôt est envoyé : <modèle>[_supports][_bordure][_radeau]_v<n>.gcode.3mf,
// l'empreinte courte étant ajoutée par l'adaptateur (« …_v3-<sha12>.gcode.3mf »). Le numéro de version compte les
// artefacts distincts déjà découpés pour ce modèle jusqu'à celui-ci. Les dépôts existants ne sont jamais renommés.
// Repli : le nom de fichier de la découpe.
func (s *Service) depositName(ctx context.Context, j Job) string {
	base := ""
	if j.ModelID != "" {
		if m, err := s.d.Store.GetModel(ctx, j.ModelID); err == nil {
			base = nameSlug(m.Name)
		}
	}
	if base == "" {
		return j.Name
	}
	tags := ""
	if j.Summary != nil {
		if j.Summary.EnableSupport != nil && *j.Summary.EnableSupport {
			tags += "_supports"
		}
		if brimPresent(j.Summary.BrimType, j.Summary.BrimWidth) {
			tags += "_bordure"
		}
		if raftPresent(j.Summary.RaftLayers) {
			tags += "_radeau"
		}
	}
	n := 1
	if all, err := s.ListJobs(ctx, "", 0); err == nil {
		seen := map[string]bool{}
		for _, o := range all {
			if o.ModelID == j.ModelID && o.ArtifactSHA256 != "" && o.ArtifactSHA256 != j.ArtifactSHA256 && !o.CreatedAt.After(j.CreatedAt) {
				seen[o.ArtifactSHA256] = true
			}
		}
		n = len(seen) + 1
	}
	return fmt.Sprintf("%s%s_v%d.gcode.3mf", base, tags, n)
}
