package catalog

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"archive/zip"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// Envoi de fichiers par morceaux (upload_file, interface web) et import par lien (urlimport.go).
//
// Principes : le client ne fournit JAMAIS de chemin (seulement un nom d'affichage sans séparateur) ; les
// morceaux s'accumulent dans un fichier de session nommé par le service (dossier uploads/ du dossier de
// données, purgé au démarrage et à l'expiration), puis le fichier complet est contrôlé (taille, signature
// par format, empreinte sha256 recalculée) AVANT d'entrer dans le magasin de blobs, seul stockage durable.

const actorUpload = "upload"

// Limites par défaut.
const (
	defaultMaxUploadBytes  = 512 << 20
	defaultStepUploadBytes = 256 << 20
	defaultUploadTTL       = time.Hour
	// defaultInFlightBytes : total des octets annoncés par les sessions ouvertes ; defaultUploadLifetime : durée de vie
	// ABSOLUE d'une session (le TTL d'inactivité se prolonge à chaque morceau, pas celle-ci).
	defaultInFlightBytes  = 2 << 30
	defaultUploadLifetime = 6 * time.Hour
	defaultMaxSessions    = 8
	// MaxChunkChars : taille maximale d'un morceau base64 d'un appel d'outil MCP.
	MaxChunkChars = 100_000
	// MaxChunkBytes : taille maximale d'un morceau binaire de l'API HTTP.
	MaxChunkBytes = 8 << 20
)

// UploadOptions règle les envois et l'import par lien. Zéro = valeur par défaut.
type UploadOptions struct {
	MaxBytes    int64         // taille maximale d'un fichier envoyé
	TTL         time.Duration // inactivité maximale d'une session d'envoi
	MaxSessions int           // sessions simultanées
	// MaxInFlightBytes : plafond du total des tailles annoncées par les sessions ouvertes (défaut 2 Gio ; BAMBU_UPLOAD_INFLIGHT_MB).
	MaxInFlightBytes int64
	// MaxLifetime : durée de vie absolue d'une session, quelle que soit son activité (défaut 6 h ; BAMBU_UPLOAD_MAX_LIFETIME).
	MaxLifetime time.Duration
	URL         URLImportOptions
}

type uploadSession struct {
	mu       sync.Mutex
	id       string
	name     string
	format   contracts.FileFormat
	total    int64
	sha      string // attendu (minuscules) ; vide = non annoncé
	received int64
	f        *os.File
	path     string
	meta     UploadMeta
	timer    *time.Timer
	created  time.Time
	expires  time.Time
	closed   bool
}

type uploadState struct {
	mu   sync.Mutex
	opts UploadOptions
	sess map[string]*uploadSession
	dir  string
	init bool
}

var uploadStates sync.Map // *Service -> *uploadState

func (s *Service) up() *uploadState {
	v, _ := uploadStates.LoadOrStore(s, &uploadState{sess: map[string]*uploadSession{}})
	st := v.(*uploadState)
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.init {
		st.init = true
		o := st.opts
		env := uploadOptionsFromEnv()
		if o.MaxBytes == 0 {
			o.MaxBytes = env.MaxBytes
		}
		if o.TTL == 0 {
			o.TTL = env.TTL
		}
		if o.MaxSessions == 0 {
			o.MaxSessions = env.MaxSessions
		}
		if o.MaxInFlightBytes == 0 {
			o.MaxInFlightBytes = env.MaxInFlightBytes
		}
		if o.MaxLifetime == 0 {
			o.MaxLifetime = env.MaxLifetime
		}
		if o.URL.isZero() {
			o.URL = env.URL
		}
		st.opts = o
		st.opts.URL.defaults()
		st.dir = filepath.Join(s.d.DataDir, "uploads")
		// Les sessions vivent en mémoire : tout fichier restant d'un lancement précédent est orphelin.
		_ = os.RemoveAll(st.dir)
		_ = os.MkdirAll(st.dir, 0o700)
	}
	return st
}

// SetUploadOptions fixe les options (tests, câblage). À appeler avant le premier envoi.
func (s *Service) SetUploadOptions(o UploadOptions) {
	v, _ := uploadStates.LoadOrStore(s, &uploadState{sess: map[string]*uploadSession{}})
	st := v.(*uploadState)
	st.mu.Lock()
	st.opts = o
	st.init = false
	st.mu.Unlock()
}

func uploadOptionsFromEnv() UploadOptions {
	o := UploadOptions{MaxBytes: defaultMaxUploadBytes, TTL: defaultUploadTTL, MaxSessions: defaultMaxSessions, MaxInFlightBytes: defaultInFlightBytes, MaxLifetime: defaultUploadLifetime}
	if mb, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("BAMBU_UPLOAD_INFLIGHT_MB")), 10, 64); err == nil && mb > 0 {
		o.MaxInFlightBytes = mb << 20
	}
	if d, err := time.ParseDuration(strings.TrimSpace(os.Getenv("BAMBU_UPLOAD_MAX_LIFETIME"))); err == nil && d > 0 {
		o.MaxLifetime = d
	}
	if mb, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("BAMBU_MAX_UPLOAD_MB")), 10, 64); err == nil && mb > 0 {
		o.MaxBytes = mb << 20
	}
	if d, err := time.ParseDuration(strings.TrimSpace(os.Getenv("BAMBU_UPLOAD_TTL"))); err == nil && d > 0 {
		o.TTL = d
	}
	o.URL = urlOptionsFromEnv()
	return o
}

// UploadLimits : ce que le service accepte (get_capabilities, /api/capabilities).
type UploadLimits = contracts.UploadInfo

// URLImportInfo : état de l'import par lien.
type URLImportInfo = contracts.URLImportInfo

// UploadLimits renvoie les limites effectives.
func (s *Service) UploadLimits() UploadLimits {
	st := s.up()
	l := UploadLimits{
		ImportRoots: s.Roots(), MaxUploadBytes: st.opts.MaxBytes, MaxChunkChars: MaxChunkChars,
		Formats: []string{"stl", "obj", "3mf", "step"}, TTLSeconds: int(st.opts.TTL / time.Second),
		MaxByFormatMB: map[string]int{}, URLImport: s.URLImportInfo(),
	}
	if l.ImportRoots == nil {
		l.ImportRoots = []string{}
	}
	for _, f := range []contracts.FileFormat{contracts.FormatSTL, contracts.FormatOBJ, contracts.Format3MF, contracts.FormatSTEP} {
		l.MaxByFormatMB[string(f)] = int(s.maxFor(f) >> 20)
	}
	return l
}

func (s *Service) maxFor(f contracts.FileFormat) int64 {
	st := s.up()
	m := st.opts.MaxBytes
	if f == contracts.FormatSTEP && m > defaultStepUploadBytes {
		m = defaultStepUploadBytes
	}
	if m > s.cfg.MaxFileBytes {
		m = s.cfg.MaxFileBytes
	}
	return m
}

// UploadMeta : options d'import communes à l'envoi et à l'import par lien.
type UploadMeta struct {
	Collection  string   `json:"collection,omitempty" jsonschema:"collection de destination (nom, chemin « A/B » ou identifiant col_...) ; créée si absente ; vide = aucune collection"`
	Tags        []string `json:"tags,omitempty" jsonschema:"étiquettes ajoutées au modèle créé"`
	License     string   `json:"license,omitempty" jsonschema:"licence du modèle (vide = inconnue)"`
	Source      string   `json:"source,omitempty" jsonschema:"provenance déclarée (ex. « généré par Claude », nom d'un site ou d'un auteur)"`
	SourceURL   string   `json:"source_url,omitempty" jsonschema:"URL d'origine, informative"`
	Author      string   `json:"author,omitempty" jsonschema:"auteur du modèle"`
	Note        string   `json:"note,omitempty" jsonschema:"remarque libre conservée dans la provenance"`
	Unit        string   `json:"unit,omitempty" jsonschema:"unité des coordonnées si le fichier n'en porte pas : mm | cm | m | inch (vide = inconnue, millimètres supposés et signalés)"`
	Name        string   `json:"name,omitempty" jsonschema:"nom du modèle (défaut : nom du fichier sans extension)"`
	OnDuplicate string   `json:"on_duplicate,omitempty" jsonschema:"skip (défaut : un contenu déjà au catalogue n'est pas ajouté une seconde fois) | keep (crée un second modèle)"`
}

// UploadStatus : état d'une session d'envoi.
type UploadStatus struct {
	UploadID   string    `json:"upload_id"`
	Filename   string    `json:"filename"`
	Format     string    `json:"format"`
	Total      int64     `json:"total_size"`
	Received   int64     `json:"received"`
	NextOffset int64     `json:"next_offset" jsonschema:"offset du prochain morceau à envoyer (reprise après coupure)"`
	Complete   bool      `json:"complete"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// UploadResult : issue d'un envoi terminé ou d'un import par lien.
type UploadResult struct {
	File FileResult `json:"file"`
	// Note : suite à donner (analyse et aperçu en arrière-plan, doublon...).
	Note string `json:"note,omitempty"`
}

// ValidUploadName contrôle le nom d'affichage d'un envoi : jamais un chemin.
func ValidUploadName(name string) (string, contracts.FileFormat, error) {
	n := strings.TrimSpace(name)
	switch {
	case n == "" || len(n) > 200:
		return "", "", invalidf("nom de fichier requis (1 à 200 caractères)")
	case strings.ContainsAny(n, `/\`) || strings.Contains(n, ".."):
		return "", "", invalidf("le nom de fichier ne doit contenir ni séparateur de chemin ni « .. » : donner seulement le nom, ex. support.stl")
	case !utf8.ValidString(n):
		return "", "", invalidf("nom de fichier invalide")
	}
	for _, r := range n {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`:*?"<>|`, r) {
			return "", "", invalidf("nom de fichier invalide (caractère %q)", r)
		}
	}
	if strings.HasPrefix(n, ".") || strings.HasSuffix(n, ".") || strings.HasSuffix(n, " ") {
		return "", "", invalidf("nom de fichier invalide")
	}
	f, ok := formatOf(n)
	if !ok {
		return "", "", invalidf("format non pris en charge : extensions acceptées .stl .obj .3mf .step .stp")
	}
	return n, f, nil
}

func newUploadID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "upl_" + hex.EncodeToString(b)
}

// UploadBegin ouvre une session d'envoi. sha256 est facultatif (obligatoire côté outil MCP) mais, s'il est
// donné, l'envoi est refusé à la fin si l'empreinte recalculée diffère.
func (s *Service) UploadBegin(_ context.Context, name string, total int64, sha string, meta UploadMeta) (UploadStatus, error) {
	st := s.up()
	n, format, err := ValidUploadName(name)
	if err != nil {
		return UploadStatus{}, err
	}
	if total <= 0 {
		return UploadStatus{}, invalidf("total_size requis (octets, > 0)")
	}
	if mx := s.maxFor(format); total > mx {
		return UploadStatus{}, invalidf("fichier trop gros pour le format %s : %d octets > limite %d (%d Mo) ; BAMBU_MAX_UPLOAD_MB règle la limite", format, total, mx, mx>>20)
	}
	sha = strings.ToLower(strings.TrimSpace(sha))
	if sha != "" && !validHex64(sha) {
		return UploadStatus{}, invalidf("sha256 invalide : 64 caractères hexadécimaux attendus")
	}
	if meta.OnDuplicate != "" && meta.OnDuplicate != "skip" && meta.OnDuplicate != "keep" {
		return UploadStatus{}, invalidf("on_duplicate : skip | keep")
	}
	u, err := normalizeUnit(meta.Unit)
	if err != nil {
		return UploadStatus{}, err
	}
	meta.Unit = u
	st.mu.Lock()
	if len(st.sess) >= st.opts.MaxSessions {
		st.mu.Unlock()
		return UploadStatus{}, fmt.Errorf("%w : trop d'envois en cours (max %d) ; terminer ou annuler (cancel_upload) l'un d'eux, ou attendre leur expiration", ErrConflict, st.opts.MaxSessions)
	}
	var inflight int64
	for _, o := range st.sess {
		inflight += o.total
	}
	if inflight+total > st.opts.MaxInFlightBytes {
		st.mu.Unlock()
		return UploadStatus{}, fmt.Errorf("%w : trop d'octets en cours d'envoi (%d Mio déjà annoncés, plafond %d Mio) ; terminer ou annuler un envoi (BAMBU_UPLOAD_INFLIGHT_MB règle le plafond)", ErrConflict, inflight>>20, st.opts.MaxInFlightBytes>>20)
	}
	id := newUploadID()
	p := filepath.Join(st.dir, id+".part")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		st.mu.Unlock()
		if errors.Is(err, os.ErrNotExist) { // dossier purgé sous nos pieds
			_ = os.MkdirAll(st.dir, 0o700)
		}
		return UploadStatus{}, fmt.Errorf("session d'envoi : %w", err)
	}
	us := &uploadSession{id: id, name: n, format: format, total: total, sha: sha, f: f, path: p, meta: meta, created: time.Now()}
	st.sess[id] = us
	st.mu.Unlock()
	us.mu.Lock()
	s.touch(st, us)
	status := us.status()
	us.mu.Unlock()
	return status, nil
}

func validHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// touch (verrou de la session tenu) prolonge la session d'un TTL.
func (s *Service) touch(st *uploadState, us *uploadSession) {
	ttl := st.opts.TTL
	now := time.Now()
	us.expires = now.Add(ttl)
	if st.opts.MaxLifetime > 0 { // durée de vie absolue : l'activité ne la prolonge pas
		if hard := us.created.Add(st.opts.MaxLifetime); hard.Before(us.expires) {
			us.expires = hard
			if ttl = hard.Sub(now); ttl < 0 {
				ttl = 0
			}
		}
	}
	if us.timer != nil {
		us.timer.Stop()
	}
	id := us.id
	us.timer = time.AfterFunc(ttl, func() { s.dropUpload(id) })
}

func (us *uploadSession) status() UploadStatus {
	return UploadStatus{UploadID: us.id, Filename: us.name, Format: string(us.format), Total: us.total, Received: us.received,
		NextOffset: us.received, Complete: us.received == us.total, ExpiresAt: us.expires.UTC()}
}

func (s *Service) session(id string) (*uploadSession, error) {
	st := s.up()
	st.mu.Lock()
	us := st.sess[id]
	st.mu.Unlock()
	if us == nil {
		return nil, fmt.Errorf("%w : session d'envoi %q inconnue ou expirée ; recommencer avec un nouvel envoi", ErrNotFound, id)
	}
	return us, nil
}

// dropUpload supprime la session et son fichier (idempotent).
func (s *Service) dropUpload(id string) {
	st := s.up()
	st.mu.Lock()
	us := st.sess[id]
	delete(st.sess, id)
	st.mu.Unlock()
	if us == nil {
		return
	}
	us.mu.Lock()
	defer us.mu.Unlock()
	us.closeLocked()
}

func (us *uploadSession) closeLocked() {
	if us.closed {
		return
	}
	us.closed = true
	if us.timer != nil {
		us.timer.Stop()
	}
	if us.f != nil {
		us.f.Close()
	}
	_ = os.Remove(us.path)
}

// UploadChunkMismatchError : l'offset ne correspond pas à ce qui a été reçu.
type UploadChunkMismatchError struct{ Status UploadStatus }

func (e *UploadChunkMismatchError) Error() string {
	return fmt.Sprintf("offset incorrect : %d octets déjà reçus, le prochain morceau doit commencer à l'offset %d", e.Status.Received, e.Status.NextOffset)
}
func (e *UploadChunkMismatchError) Is(t error) bool { return t == ErrConflict }

// UploadWrite ajoute un morceau à l'offset donné. Un morceau déjà reçu (réponse perdue) est accepté sans effet ;
// un trou (offset au-delà de ce qui est reçu) est refusé avec le bon offset.
func (s *Service) UploadWrite(_ context.Context, id string, offset int64, data []byte) (UploadStatus, error) {
	us, err := s.session(id)
	if err != nil {
		return UploadStatus{}, err
	}
	us.mu.Lock()
	defer us.mu.Unlock()
	if us.closed {
		return UploadStatus{}, fmt.Errorf("%w : session d'envoi %q expirée ; recommencer", ErrNotFound, id)
	}
	if offset < 0 {
		return UploadStatus{}, invalidf("offset négatif")
	}
	if offset > us.received {
		return UploadStatus{}, &UploadChunkMismatchError{Status: us.status()}
	}
	end := offset + int64(len(data))
	if end > us.total {
		return UploadStatus{}, invalidf("le morceau dépasse la taille annoncée (%d + %d > %d)", offset, len(data), us.total)
	}
	if end > us.received {
		skip := us.received - offset
		if _, err := us.f.WriteAt(data[skip:], us.received); err != nil {
			return UploadStatus{}, fmt.Errorf("écriture de la session : %w", err)
		}
		us.received = end
	}
	s.touch(s.up(), us)
	return us.status(), nil
}

// UploadStatus renvoie l'état d'une session (reprise).
func (s *Service) UploadStatus(_ context.Context, id string) (UploadStatus, error) {
	us, err := s.session(id)
	if err != nil {
		return UploadStatus{}, err
	}
	us.mu.Lock()
	defer us.mu.Unlock()
	return us.status(), nil
}

// CloseUploads abandonne toutes les sessions d'envoi (arrêt du service, tests).
func (s *Service) CloseUploads() {
	st := s.up()
	st.mu.Lock()
	ids := make([]string, 0, len(st.sess))
	for id := range st.sess {
		ids = append(ids, id)
	}
	st.mu.Unlock()
	for _, id := range ids {
		s.dropUpload(id)
	}
}

// UploadCancel abandonne un envoi.
func (s *Service) UploadCancel(id string) error {
	if _, err := s.session(id); err != nil {
		return err
	}
	s.dropUpload(id)
	return nil
}

// UploadFinish contrôle le fichier complet, le range dans le magasin de blobs et l'importe au catalogue. La
// session est supprimée dans tous les cas, sauf si l'envoi est incomplet (il peut alors continuer).
func (s *Service) UploadFinish(ctx context.Context, id string) (*UploadResult, error) {
	us, err := s.session(id)
	if err != nil {
		return nil, err
	}
	us.mu.Lock()
	if us.closed {
		us.mu.Unlock()
		return nil, fmt.Errorf("%w : session d'envoi %q expirée ; recommencer", ErrNotFound, id)
	}
	if us.received != us.total {
		e := &UploadChunkMismatchError{Status: us.status()}
		us.mu.Unlock()
		return nil, e
	}
	res, err := s.finalize(ctx, us.path, us.name, us.format, us.total, us.sha, us.meta)
	us.mu.Unlock()
	s.dropUpload(id)
	return res, err
}

// finalize : contrôles, copie dans le magasin de blobs, import au catalogue. path est un fichier temporaire du
// service (session d'envoi ou téléchargement), jamais un chemin fourni par un client.
func (s *Service) finalize(ctx context.Context, path, name string, format contracts.FileFormat, total int64, wantSHA string, meta UploadMeta) (*UploadResult, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Size() != total {
		return nil, invalidf("taille reçue %d différente de la taille annoncée %d", st.Size(), total)
	}
	if mx := s.maxFor(format); st.Size() > mx {
		return nil, invalidf("fichier trop gros (%d octets > limite %d)", st.Size(), mx)
	}
	if err := ValidateSignature(path, format); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sha, size, err := s.d.Blobs.Put(f, wantSHA)
	if err != nil {
		if errors.Is(err, store.ErrBlobHashMismatch) {
			return nil, invalidf("sha256 incorrect : le contenu reçu ne correspond pas à l'empreinte annoncée (%s) ; renvoyer le fichier entier avec la bonne empreinte et vérifier qu'aucun morceau n'a été altéré", err.Error())
		}
		return nil, err
	}
	if err := s.d.Blobs.Verify(sha); err != nil {
		return nil, fmt.Errorf("vérification après copie : %w", err)
	}
	fr, err := s.importStored(ctx, meta, name, format, sha, size)
	if err != nil {
		return nil, err
	}
	res := &UploadResult{File: fr}
	switch fr.Status {
	case StatusImported:
		res.Note = "Modèle créé ; l'analyse géométrique et l'aperçu 3D sont calculés en arrière-plan (get_model_details pour suivre)."
	case StatusNewVersion:
		res.Note = "Un fichier de même nom existait : nouvelle version ajoutée au même modèle (l'ancienne reste disponible)."
	case StatusDuplicate:
		res.Note = "Ce contenu est déjà au catalogue : rien n'a été ajouté (on_duplicate=keep pour créer un second modèle)."
	case StatusUnchanged:
		res.Note = "Fichier identique à celui déjà présent sous ce nom : rien n'a changé."
	}
	return res, nil
}

// importStored enregistre au catalogue un contenu déjà présent dans le magasin de blobs.
func (s *Service) importStored(ctx context.Context, meta UploadMeta, name string, format contracts.FileFormat, sha string, size int64) (FileResult, error) {
	fr := FileResult{Path: name, RelPath: name, Format: string(format), SizeBytes: size, SHA256: sha, Availability: string(contracts.AvailableLinux)}
	var created []contracts.SourceFile
	err := s.d.Store.Tx(ctx, func(tx *sql.Tx) error {
		created = nil
		au := s.auditor(ctx, tx, actorUpload, "source_file")
		now := s.d.Store.Now()
		newFile := func(modelID string) contracts.SourceFile {
			return contracts.SourceFile{ID: contracts.NewID("src"), ModelID: modelID, Name: name, Format: format, SizeBytes: size,
				SHA256: sha, Availability: contracts.AvailableLinux, CreatedAt: now, RelPath: name, Unit: meta.Unit}
		}
		var colID *string
		if c := strings.TrimSpace(meta.Collection); c != "" {
			id, err := s.resolveUploadCollection(ctx, tx, c)
			if err != nil {
				return err
			}
			colID = &id
		}
		// Même nom dans la même collection : nouvelle version (ou inchangé si même contenu).
		same, err := queryFiles(ctx, tx, `WHERE lower(name)=lower(?) AND model_id IN (SELECT id FROM models WHERE deleted_at IS NULL AND collection_id IS ?)`, name, nullS(colID))
		if err != nil {
			return err
		}
		if len(same) > 0 && meta.OnDuplicate != "keep" {
			old := same[0]
			for _, f := range same {
				if f.CreatedAt.After(old.CreatedAt) {
					old = f
				}
			}
			if old.SHA256 == sha {
				fr.Status, fr.ModelID, fr.SourceFileID = StatusUnchanged, old.ModelID, old.ID
				created = append(created, old)
				return nil
			}
			nf := newFile(old.ModelID)
			if err := insertFile(ctx, tx, nf); err != nil {
				return err
			}
			vs, err := queryVersions(ctx, tx, old.ModelID)
			if err != nil {
				return err
			}
			var parent *string
			if len(vs) > 0 {
				parent = &vs[0].ID
			}
			v := contracts.ModelVersion{ID: contracts.NewID("ver"), ModelID: old.ModelID, ParentID: parent,
				Label: fmt.Sprintf("v%d", len(vs)+1), Note: "fichier de même nom renvoyé", SourceFileIDs: []string{nf.ID}, CreatedAt: now}
			if err := insertVersion(ctx, tx, v); err != nil {
				return err
			}
			if err := touchModel(ctx, tx, old.ModelID, now); err != nil {
				return err
			}
			if err := au("source_file.add", nf.ID, map[string]any{"model_id": old.ModelID, "sha256": sha, "version": v.Label}); err != nil {
				return err
			}
			fr.Status, fr.ModelID, fr.SourceFileID = StatusNewVersion, old.ModelID, nf.ID
			fr.Note = "version " + v.Label + " (l'ancienne reste disponible)"
			created = append(created, nf)
			return nil
		}
		// Doublon exact ailleurs au catalogue.
		if meta.OnDuplicate != "keep" {
			dups, err := queryFiles(ctx, tx, `WHERE sha256=? AND model_id IN (SELECT id FROM models WHERE deleted_at IS NULL)`, sha)
			if err != nil {
				return err
			}
			if len(dups) > 0 {
				d := dups[0]
				fr.Status, fr.ModelID, fr.SourceFileID, fr.DuplicateOf = StatusDuplicate, d.ModelID, d.ID, d.ModelID
				fr.Note = "doublon exact d'un fichier déjà au catalogue : rien n'a été ajouté"
				created = append(created, d)
				return nil
			}
		}
		mname := strings.TrimSpace(meta.Name)
		if mname == "" {
			mname = strings.TrimSuffix(name, filepath.Ext(name))
		}
		m := contracts.Model{ID: contracts.NewID("mdl"), CollectionID: colID, Name: mname, Tags: normTags(meta.Tags), CreatedAt: now, UpdatedAt: now,
			License: strings.TrimSpace(meta.License),
			Provenance: &contracts.Provenance{Source: firstNonEmpty(meta.Source, "envoi de fichier"), URL: meta.SourceURL, Author: meta.Author,
				Note: meta.Note, ImportedAt: now}}
		if err := insertModel(ctx, tx, m); err != nil {
			return err
		}
		nf := newFile(m.ID)
		if err := insertFile(ctx, tx, nf); err != nil {
			return err
		}
		v := contracts.ModelVersion{ID: contracts.NewID("ver"), ModelID: m.ID, Label: "v1", Note: "envoi initial", SourceFileIDs: []string{nf.ID}, CreatedAt: now}
		if err := insertVersion(ctx, tx, v); err != nil {
			return err
		}
		if err := s.d.Store.AuditTx(ctx, tx, actorUpload, "model.create", "model", m.ID, map[string]any{"name": m.Name, "source": m.Provenance.Source, "url": meta.SourceURL}); err != nil {
			return err
		}
		if err := au("source_file.add", nf.ID, map[string]any{"model_id": m.ID, "sha256": sha, "availability": "linux"}); err != nil {
			return err
		}
		fr.Status, fr.ModelID, fr.SourceFileID = StatusImported, m.ID, nf.ID
		created = append(created, nf)
		return nil
	})
	if err != nil {
		return fr, fmt.Errorf("enregistrement au catalogue : %w", err)
	}
	for _, nf := range created {
		s.ensureDerived(ctx, nf)
	}
	return fr, nil
}

// resolveUploadCollection : identifiant existant, sinon chemin « A/B » (créé au besoin).
func (s *Service) resolveUploadCollection(ctx context.Context, tx *sql.Tx, c string) (string, error) {
	if strings.HasPrefix(c, "col_") {
		var id string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM collections WHERE id=?`, c).Scan(&id); err == nil {
			return id, nil
		}
	}
	var parent *string
	var id string
	for _, part := range strings.Split(strings.ReplaceAll(c, `\`, "/"), "/") {
		part = strings.TrimSpace(part)
		if part == "" || part == "." || part == ".." {
			return "", invalidf("nom de collection invalide %q", c)
		}
		var err error
		id, err = s.ensureCollection(ctx, tx, actorUpload, s.auditor(ctx, tx, actorUpload, "collection"), parent, part)
		if err != nil {
			return "", err
		}
		parent = &id
	}
	return id, nil
}

// ---- contrôle des signatures ----

// ValidateSignature vérifie que le fichier est bien du format annoncé (pas seulement son extension).
func ValidateSignature(path string, format contracts.FileFormat) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	head := make([]byte, 1<<16)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	switch format {
	case contracts.FormatSTL:
		return checkSTL(head, st.Size())
	case contracts.FormatOBJ:
		return checkOBJ(f)
	case contracts.Format3MF:
		return check3MF(path, st.Size())
	case contracts.FormatSTEP:
		h := bytes.TrimPrefix(head, []byte("\xef\xbb\xbf"))
		h = bytes.TrimLeft(h, " \t\r\n")
		if !bytes.HasPrefix(h, []byte("ISO-10303-21")) {
			return invalidf("le contenu n'est pas un fichier STEP : l'en-tête ISO-10303-21; est absent")
		}
		return nil
	}
	return invalidf("format inconnu %q", format)
}

func checkSTL(head []byte, size int64) error {
	if size >= 84 && len(head) >= 84 {
		n := int64(binary.LittleEndian.Uint32(head[80:84]))
		if n > 0 && size == 84+50*n {
			return nil // STL binaire cohérent : taille = 84 + 50 × triangles
		}
	}
	h := bytes.TrimPrefix(head, []byte("\xef\xbb\xbf"))
	h = bytes.TrimLeft(h, " \t\r\n")
	if len(h) >= 5 && strings.EqualFold(string(h[:5]), "solid") && bytes.IndexByte(head, 0) < 0 {
		low := strings.ToLower(string(head))
		if strings.Contains(low, "facet") || strings.Contains(low, "endsolid") {
			return nil // STL ASCII
		}
	}
	return invalidf("le contenu n'est ni un STL binaire (taille ≠ 84 + 50 × triangles) ni un STL ASCII (« solid … facet »)")
}

func checkOBJ(f *os.File) error {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	sc := bufio.NewScanner(io.LimitReader(f, 8<<20))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	vertices := 0
	for sc.Scan() {
		line := sc.Bytes()
		if bytes.IndexByte(line, 0) >= 0 || !utf8.Valid(line) {
			return invalidf("le contenu n'est pas un OBJ texte (octets binaires)")
		}
		t := strings.TrimSpace(string(line))
		if strings.HasPrefix(t, "v ") {
			vertices++
			if vertices >= 1 {
				return nil
			}
		}
	}
	return invalidf("le contenu n'est pas un OBJ : aucune ligne de sommet « v x y z » trouvée")
}

const (
	max3MFEntries      = 20_000
	max3MFUncompressed = 4 << 30
)

func check3MF(path string, size int64) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return invalidf("le contenu n'est pas une archive 3MF (zip) valide : %v", err)
	}
	defer zr.Close()
	if len(zr.File) == 0 || len(zr.File) > max3MFEntries {
		return invalidf("archive 3MF invalide (%d entrées)", len(zr.File))
	}
	var total uint64
	hasModel, hasTypes := false, false
	for _, e := range zr.File {
		name := strings.ReplaceAll(e.Name, `\`, "/")
		if strings.ContainsRune(name, 0) || strings.HasPrefix(name, "/") || (len(name) > 1 && name[1] == ':') {
			return invalidf("archive 3MF refusée : chemin d'entrée dangereux %q", e.Name)
		}
		for _, seg := range strings.Split(name, "/") {
			if seg == ".." {
				return invalidf("archive 3MF refusée : entrée avec « .. » (%q)", e.Name)
			}
		}
		total += e.UncompressedSize64
		if total > max3MFUncompressed {
			return invalidf("archive 3MF refusée : contenu décompressé trop gros (bombe de décompression ?)")
		}
		if e.CompressedSize64 > 0 && e.UncompressedSize64/e.CompressedSize64 > 2000 && e.UncompressedSize64 > 256<<20 {
			return invalidf("archive 3MF refusée : taux de compression suspect pour %q", e.Name)
		}
		switch strings.ToLower(name) {
		case "3d/3dmodel.model":
			hasModel = true
		case "[content_types].xml":
			hasTypes = true
		}
	}
	if !hasTypes || !hasModel {
		return invalidf("l'archive zip n'est pas un 3MF : [Content_Types].xml ou 3D/3dmodel.model manquant")
	}
	return nil
}
