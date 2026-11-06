package printing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/catalog"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/goals"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/jobs"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/printer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/profiles"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/slicer"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

const actor = "printing"

// Types de travaux de la file.
const (
	JobSlice  = "printing.slice"
	JobUpload = "upload_print_artifact" // physique : clé d'idempotence obligatoire (jobs.physicalKind)
	JobStart  = "start_print"           // physique et non rejouable (jobs.neverReplay)
)

// ArchiveValidator valide un .gcode.3mf (par défaut slicer.ValidateArchive).
type ArchiveValidator func(path string, exp slicer.Expect) (*slicer.ArchiveInfo, *slicer.Validation, error)

// AdapterFactory construit l'adaptateur d'une imprimante (par défaut Bambu LAN).
type AdapterFactory func(ctx context.Context, rec PrinterRecord, secret printer.SecretProvider, verifier printer.ProofVerifier) (printer.Adapter, error)

// Deps : ce que le cœur fournit.
type Deps struct {
	Store    *store.Store
	Blobs    *store.BlobStore
	Queue    *jobs.Queue
	Registry *jobs.Registry
	Goals    *goals.Service   // facultatif : variante appliquée au modèle
	Catalog  *catalog.Service // facultatif : worker CAO, URL de base
	Log      *slog.Logger
	DataDir  string
	// Slicer : construction paresseuse (peut échouer si Bambu Studio est absent).
	Slicer func() (*slicer.Slicer, error)

	// Substituables (tests) ; défauts : coffre fichier, Bambu LAN, slicer.ValidateArchive.
	Secrets   SecretStore
	Adapters  AdapterFactory
	Validator ArchiveValidator
	Now       func() time.Time
	// ConfirmationTTL : durée de vie d'une confirmation (défaut 10 min, max 24 h).
	ConfirmationTTL time.Duration
	// BaseURL : base des liens locaux (/setup, /confirm, /stop) ; défaut Catalog.BaseURL().
	BaseURL func() string
	// AllowAnyPrinterHost lève la restriction « adresses privées / lien local / .local » de l'hôte
	// d'une imprimante (option serveur --allow-any-printer-host ; tests avec le simulateur en loopback).
	AllowAnyPrinterHost bool
	// ApprovalTTL : validité d'un accord humain après le clic (défaut 2 min).
	ApprovalTTL time.Duration
	// CertFetch lit le certificat présenté par l'imprimante (défaut printer.FetchCertInfo).
	CertFetch func(ctx context.Context, hostport string) (printer.CertInfo, error)
	// ReconcileSettle : délai de stabilisation de la réconciliation (défaut de l'adaptateur : 10 s).
	ReconcileSettle time.Duration
}

// Service est la chaîne d'impression.
type Service struct {
	d       Deps
	secrets SecretStore
	log     *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.Mutex
	adapters map[string]*adapterEntry
	tracking map[string]context.CancelFunc
	approv   map[string]*approval // par identifiant de confirmation
	pages    map[string]pageNonce
	stopping map[string]bool    // impressions dont l'arrêt humain est en cours (le suivi ne les passe pas en FAILED avant le CANCELLED)
	stops    map[string]stopReq // identifiant de demande d'arrêt -> print job (TTL, purgé)
	// consumedBy : confirmation -> travail de lancement qui l'a consommée (un autre lancement
	// concurrent ne doit jamais faire passer l'impression en START_UNKNOWN). TTL, purgé.
	consumedBy map[string]consumedEntry

	jobMu sync.Mutex // sérialise les mises à jour d'un même PrintJob dans le processus

	// Modèles : pas de sync.Once (un ctx annulé ne doit pas figer l'échec) ; on réessaie tant qu'il n'a pas réussi.
	modelsMu   sync.Mutex
	modelsDone bool
	modelsN    int
	modelsErr  error

	// confirmTransition : passage AWAITING -> START_REQUESTED après consommation (substituable en test).
	confirmTransition func(ctx context.Context, jobID, confID string) error
}

type adapterEntry struct {
	ad  printer.Adapter
	rec PrinterRecord
}

// New construit le service et enregistre ses handlers de travaux.
func New(d Deps) (*Service, error) {
	if d.Store == nil || d.Blobs == nil || d.Queue == nil {
		return nil, errors.New("printing : store, blobs et file de travaux requis")
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Now == nil {
		d.Now = func() time.Time { return d.Store.Now() }
	}
	if d.ConfirmationTTL <= 0 {
		d.ConfirmationTTL = 10 * time.Minute
	}
	secrets := d.Secrets
	if secrets == nil {
		if d.DataDir == "" {
			return nil, errors.New("printing : DataDir ou Secrets requis")
		}
		fs, err := OpenFileSecretStore(filepath.Join(d.DataDir, "secrets"), d.Log)
		if err != nil {
			return nil, err
		}
		secrets = fs
	}
	if d.Validator == nil {
		d.Validator = slicer.ValidateArchive
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{d: d, secrets: secrets, log: d.Log, ctx: ctx, cancel: cancel,
		adapters: map[string]*adapterEntry{}, tracking: map[string]context.CancelFunc{},
		approv: map[string]*approval{}, pages: map[string]pageNonce{}, stops: map[string]stopReq{}, stopping: map[string]bool{}, consumedBy: map[string]consumedEntry{}}
	s.confirmTransition = s.defaultConfirmTransition
	if d.CertFetch == nil {
		s.d.CertFetch = printer.FetchCertInfo
	}
	if d.Adapters == nil {
		s.d.Adapters = s.defaultAdapter
	}
	s.wg.Add(1)
	go func() { // maintenance : purge des nonces, demandes d'arrêt, approbations et traces expirées
		defer s.wg.Done()
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.purge()
			}
		}
	}()
	if d.Registry != nil {
		d.Registry.Register(JobSlice, true, s.sliceHandler)
		d.Registry.Register(JobUpload, true, s.uploadHandler)
		d.Registry.Register(JobStart, false, s.startHandler)
	}
	return s, nil
}

// Close arrête le suivi et ferme les connexions imprimante.
func (s *Service) Close() error {
	s.cancel()
	s.wg.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, e := range s.adapters {
		if c, ok := e.ad.(interface{ Close() error }); ok {
			_ = c.Close()
		}
		delete(s.adapters, id)
	}
	return nil
}

// Secrets expose le coffre (la page de saisie et les tests l'utilisent ; jamais l'API MCP).
func (s *Service) Secrets() SecretStore { return s.secrets }

func (s *Service) now() time.Time { return s.d.Now().UTC() }

// BaseURL renvoie la base des liens locaux.
func (s *Service) BaseURL() string {
	if s.d.BaseURL != nil {
		return strings.TrimRight(s.d.BaseURL(), "/")
	}
	if s.d.Catalog != nil {
		return strings.TrimRight(s.d.Catalog.BaseURL(), "/")
	}
	return "http://127.0.0.1:7878"
}

// ---- documents JSON (tables du store) ----

func (s *Service) listDocs(ctx context.Context, table string) ([]string, error) {
	rows, err := s.d.Store.DB().QueryContext(ctx, `SELECT doc FROM `+table+` ORDER BY updated_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ---- modèles d'imprimantes (profils Bambu Studio) ----

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func modelSlug(name string) string {
	n := strings.ToLower(strings.TrimPrefix(name, "Bambu Lab "))
	return strings.Trim(slugRe.ReplaceAllString(n, "_"), "_")
}

// shortName : « Bambu Lab P1S » -> « P1S ».
func shortName(modelName string) string { return strings.TrimPrefix(modelName, "Bambu Lab ") }

// SeedPrinterModels construit les profils machine depuis les profils Bambu Studio
// (remplace le TODO de contracts.SeedPrinterModels). Seul ce que les profils
// disent est renseigné : volume, buses, plateaux. Le reste reste nil (inconnu).
func SeedPrinterModels(ps *profiles.Store) ([]contracts.PrinterModel, error) {
	list, err := ps.Printers()
	if err != nil {
		return nil, err
	}
	var out []contracts.PrinterModel
	for _, p := range list {
		if len(p.Variants) == 0 {
			continue
		}
		m := contracts.PrinterModel{ID: modelSlug(p.Model), Name: p.Model, Vendor: "Bambu Lab",
			NozzleDiametersMM: append([]float64(nil), p.Nozzles...),
			Source:            fmt.Sprintf("profils Bambu Studio %s (%s)", ps.Version, ps.Vendor)}
		v := p.Variants[0]
		for _, c := range p.Variants {
			if c.Nozzle > 0.39 && c.Nozzle < 0.41 {
				v = c
				break
			}
		}
		if v.BedX > 0 && v.BedY > 0 && v.PrintableZ > 0 {
			m.BuildVolume = &contracts.Dimensions{X: v.BedX, Y: v.BedY, Z: v.PrintableZ}
		}
		seen := map[string]bool{}
		for _, c := range p.Variants {
			for _, b := range c.BedTypes {
				if !seen[b] {
					seen[b] = true
					m.Plates = append(m.Plates, b)
				}
			}
		}
		if len(m.Plates) == 0 && p.DefaultBedType != "" {
			m.Plates = []string{p.DefaultBedType}
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Service) slicerInst() (*slicer.Slicer, error) {
	if s.d.Slicer == nil {
		return nil, fmt.Errorf("%w : slicer non configuré", ErrUnavailable)
	}
	sl, err := s.d.Slicer()
	if err != nil {
		return nil, fmt.Errorf("%w : slicer_missing : %v", ErrUnavailable, err)
	}
	return sl, nil
}

// EnsureModels sème les modèles depuis les profils Bambu Studio quand ils sont disponibles. Renvoie le
// nombre de modèles semés et l'éventuelle raison d'absence. Un échec (Bambu Studio absent, contexte
// annulé) n'est PAS mémorisé définitivement : le prochain appel réessaie.
func (s *Service) EnsureModels(ctx context.Context) (int, error) {
	s.modelsMu.Lock()
	defer s.modelsMu.Unlock()
	if s.modelsDone {
		return s.modelsN, nil
	}
	sl, err := s.slicerInst()
	if err != nil {
		s.modelsErr = err
		return 0, err
	}
	ms, err := SeedPrinterModels(sl.Profiles())
	if err != nil {
		s.modelsErr = err
		return 0, err
	}
	for _, m := range ms {
		if err := s.d.Store.PutDoc(ctx, actor, "printer_models", m.ID, m); err != nil {
			s.modelsErr = err
			return 0, err
		}
	}
	s.modelsDone, s.modelsN, s.modelsErr = true, len(ms), nil
	return s.modelsN, nil
}

func (s *Service) modelsError() error {
	s.modelsMu.Lock()
	defer s.modelsMu.Unlock()
	return s.modelsErr
}

// PutPrinterModel enregistre un modèle (tests, imports hors profils).
func (s *Service) PutPrinterModel(ctx context.Context, m contracts.PrinterModel) error {
	if m.ID == "" || m.Name == "" {
		return invalidf("identifiant et nom du modèle requis")
	}
	return s.d.Store.PutDoc(ctx, actor, "printer_models", m.ID, m)
}

// ListPrinterModels liste les modèles connus.
func (s *Service) ListPrinterModels(ctx context.Context) ([]contracts.PrinterModel, error) {
	_, _ = s.EnsureModels(ctx)
	raws, err := s.listDocs(ctx, "printer_models")
	if err != nil {
		return nil, err
	}
	out := []contracts.PrinterModel{}
	for _, r := range raws {
		var m contracts.PrinterModel
		if json.Unmarshal([]byte(r), &m) == nil {
			out = append(out, m)
		}
	}
	return out, nil
}

// GetPrinterModel retrouve un modèle par identifiant ou nom exact.
func (s *Service) GetPrinterModel(ctx context.Context, idOrName string) (contracts.PrinterModel, error) {
	_, _ = s.EnsureModels(ctx)
	var m contracts.PrinterModel
	if err := s.d.Store.GetDoc(ctx, "printer_models", idOrName, &m); err == nil {
		return m, nil
	}
	all, err := s.ListPrinterModels(ctx)
	if err != nil {
		return m, err
	}
	for _, c := range all {
		if strings.EqualFold(c.Name, idOrName) || strings.EqualFold(shortName(c.Name), idOrName) {
			return c, nil
		}
	}
	return m, fmt.Errorf("%w : modèle d'imprimante %q (modèles connus : %d ; profils Bambu Studio %s)", ErrNotFound, idOrName, len(all), availability(s.modelsError()))
}

func availability(err error) string {
	if err == nil {
		return "chargés"
	}
	return "indisponibles (" + err.Error() + ")"
}

// ---- registre des imprimantes ----

// AddPrinterInput décrit une imprimante. Le code d'accès n'est PAS une entrée (l'humain le saisit sur
// /setup/printer/{id}), ni l'empreinte du certificat (récupérée par le serveur et validée par l'humain sur
// cette même page), ni aucune option de contournement (désactiver la vérification TLS, départ depuis FINISH).
type AddPrinterInput struct {
	Name     string  `json:"name" jsonschema:"nom d'usage, ex. Atelier"`
	ModelID  string  `json:"model_id" jsonschema:"modèle Bambu (identifiant de list_printer_models, ex. p1s) ou nom exact"`
	Host     string  `json:"host" jsonschema:"adresse IP privée ou lien local (192.168.x.x, 10.x, 172.16-31.x, fc00::/7, fe80::/10) ou nom en .local"`
	Serial   string  `json:"serial" jsonschema:"numéro de série (alphanumérique)"`
	NozzleMM float64 `json:"nozzle_mm,omitempty" jsonschema:"buse installée en mm (défaut 0,4)"`
	AMSUnits int     `json:"ams_units,omitempty" jsonschema:"nombre d'AMS raccordés (défaut 0)"`
	MQTTPort int     `json:"mqtt_port,omitempty"`
	FTPPort  int     `json:"ftp_port,omitempty"`
}

var (
	serialRe = regexp.MustCompile(`^[A-Za-z0-9]{6,32}$`)
	hostRe   = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
)

// CheckPrinterHost applique la politique d'hôte : une imprimante Bambu LAN est sur un réseau local.
// Adresse privée (RFC 1918, fc00::/7), lien local (169.254/16, fe80::/10) ou nom en .local ; le
// loopback et les adresses publiques sont refusés (le serveur ne doit pas être détourné vers un tiers),
// sauf option serveur (allowAny).
func CheckPrinterHost(host string, allowAny bool) error {
	h := strings.TrimSpace(host)
	if ip := net.ParseIP(strings.Trim(h, "[]")); ip != nil {
		if allowAny || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
			return nil
		}
		return invalidf("hôte %s refusé : seules les adresses privées ou de lien local sont acceptées (option serveur --allow-any-printer-host pour lever)", host)
	}
	if !hostRe.MatchString(h) {
		return invalidf("hôte invalide")
	}
	if allowAny || strings.HasSuffix(strings.ToLower(h), ".local") {
		return nil
	}
	return invalidf("hôte %q refusé : un nom d'hôte doit se terminer par .local (sinon utiliser l'adresse IP privée)", host)
}

// AddPrinter enregistre une imprimante (sans code d'accès, sans empreinte) et renvoie le lien de saisie.
// Tant que l'humain n'a pas validé l'empreinte du certificat et saisi le code sur /setup/printer/{id},
// aucune connexion n'est tentée.
func (s *Service) AddPrinter(ctx context.Context, in AddPrinterInput) (*PrinterView, error) {
	in.Name, in.Host, in.Serial = strings.TrimSpace(in.Name), strings.TrimSpace(in.Host), strings.TrimSpace(in.Serial)
	if in.Name == "" || len(in.Name) > 80 {
		return nil, invalidf("nom requis (80 caractères max.)")
	}
	if !serialRe.MatchString(in.Serial) {
		return nil, invalidf("numéro de série invalide (6 à 32 caractères alphanumériques)")
	}
	if err := CheckPrinterHost(in.Host, s.d.AllowAnyPrinterHost); err != nil {
		return nil, err
	}
	if in.AMSUnits < 0 || in.AMSUnits > 4 {
		return nil, invalidf("ams_units entre 0 et 4")
	}
	if in.MQTTPort < 0 || in.MQTTPort > 65535 || in.FTPPort < 0 || in.FTPPort > 65535 {
		return nil, invalidf("port hors plage")
	}
	model, err := s.GetPrinterModel(ctx, in.ModelID)
	if err != nil {
		return nil, err
	}
	noz := in.NozzleMM
	if noz == 0 {
		noz = 0.4
	}
	if len(model.NozzleDiametersMM) > 0 {
		ok := false
		for _, n := range model.NozzleDiametersMM {
			if n > noz-1e-6 && n < noz+1e-6 {
				ok = true
			}
		}
		if !ok {
			return nil, fmt.Errorf("%w : buse %.2g mm non prévue pour %s (%v)", ErrIncompatible, noz, model.Name, model.NozzleDiametersMM)
		}
	}
	if model.AMSSupported != nil && !*model.AMSSupported && in.AMSUnits > 0 {
		return nil, fmt.Errorf("%w : %s ne prend pas en charge l'AMS", ErrIncompatible, model.Name)
	}
	rec := PrinterRecord{ID: contracts.NewID("prn"), ModelID: model.ID, Name: in.Name, Host: in.Host, Serial: in.Serial,
		NozzleMM: noz, AMSUnits: in.AMSUnits, MQTTPort: in.MQTTPort, FTPPort: in.FTPPort, CreatedAt: s.now()}
	if _, err := s.d.Store.PutDocIfRev(ctx, actor, "printers", rec.ID, rec, 0); err != nil {
		return nil, err
	}
	_ = s.d.Store.Audit(ctx, actor, "printer.add", "printer", rec.ID, map[string]any{"model": model.ID, "host": in.Host})
	return s.view(ctx, rec, model), nil
}

// updatePrinter applique une mutation à l'enregistrement d'une imprimante (version optimiste).
func (s *Service) updatePrinter(ctx context.Context, id string, mutate func(*PrinterRecord) error) (PrinterRecord, error) {
	for attempt := 0; attempt < 8; attempt++ {
		var rec PrinterRecord
		rev, err := s.d.Store.GetDocRev(ctx, "printers", id, &rec)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return rec, fmt.Errorf("%w : imprimante %q", ErrNotFound, id)
			}
			return rec, err
		}
		if err := mutate(&rec); err != nil {
			return rec, err
		}
		if _, err := s.d.Store.PutDocIfRev(ctx, actor, "printers", id, rec, rev); err != nil {
			if errors.Is(err, store.ErrConflict) {
				continue
			}
			return rec, err
		}
		return rec, nil
	}
	return PrinterRecord{}, errors.New("imprimante modifiée en continu par ailleurs : réessayer")
}

// UpdatePrinterHost change l'hôte d'une imprimante. L'empreinte de certificat épinglée est EFFACÉE : le
// nouvel hôte doit être re-épinglé par l'humain sur /setup/printer/{id} (aucune connexion avant).
func (s *Service) UpdatePrinterHost(ctx context.Context, id, host string) (*PrinterView, error) {
	host = strings.TrimSpace(host)
	if err := CheckPrinterHost(host, s.d.AllowAnyPrinterHost); err != nil {
		return nil, err
	}
	rec, err := s.updatePrinter(ctx, id, func(r *PrinterRecord) error {
		if r.Host != host {
			r.Host, r.PinnedSHA256, r.PinnedAt = host, "", time.Time{}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.dropAdapter(id)
	_ = s.d.Store.Audit(ctx, actor, "printer.host_changed", "printer", id, map[string]any{"host": host, "repin_required": true})
	model, _ := s.GetPrinterModel(ctx, rec.ModelID)
	return s.view(ctx, rec, model), nil
}

// SetAllowStartFromFinish autorise (ou non) un départ depuis l'état « finished » pour cette imprimante.
// Réservé au code de confiance (tests, futur outil CLI) : jamais exposé par MCP ni par l'API JSON, car
// ce comportement n'est pas validé sur le matériel réel (J7 §5 point 8).
func (s *Service) SetAllowStartFromFinish(ctx context.Context, id string, allow bool) error {
	if _, err := s.updatePrinter(ctx, id, func(r *PrinterRecord) error { r.AllowStartFromFinish = allow; return nil }); err != nil {
		return err
	}
	s.dropAdapter(id)
	return nil
}

// SetReconcileSettle règle le délai de stabilisation de la réconciliation (tests).
func (s *Service) SetReconcileSettle(d time.Duration) {
	s.mu.Lock()
	s.d.ReconcileSettle = d
	s.mu.Unlock()
}

// PinAndSetAccessCode épingle l'empreinte de certificat que l'humain a vue (vide : conserver l'épinglage
// existant) et enregistre le code d'accès. Appelé UNIQUEMENT par la page /setup/printer/{id}.
func (s *Service) PinAndSetAccessCode(ctx context.Context, printerID, fingerprint, code string) error {
	fingerprint = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(fingerprint), ":", ""))
	if fingerprint != "" && !shaRe.MatchString(fingerprint) {
		return invalidf("empreinte invalide")
	}
	if fingerprint == "" {
		rec, err := s.GetPrinterRecord(ctx, printerID)
		if err != nil {
			return err
		}
		if rec.PinnedSHA256 == "" {
			return invalidf("aucune empreinte de certificat à épingler : l'imprimante n'a pas pu être jointe pour la lire")
		}
	}
	code = strings.TrimSpace(code)
	if len(code) < 4 || len(code) > 64 {
		return invalidf("code d'accès : 4 à 64 caractères")
	}
	if fingerprint != "" {
		if _, err := s.updatePrinter(ctx, printerID, func(r *PrinterRecord) error {
			r.PinnedSHA256, r.PinnedAt = fingerprint, s.now()
			return nil
		}); err != nil {
			return err
		}
		_ = s.d.Store.Audit(ctx, "human:web", "printer.cert_pinned", "printer", printerID, map[string]any{"sha256": fingerprint})
	}
	return s.SetAccessCode(ctx, printerID, code)
}

var shaRe = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

func (s *Service) view(_ context.Context, rec PrinterRecord, model contracts.PrinterModel) *PrinterView {
	return &PrinterView{PrinterRecord: rec, ModelName: model.Name, AccessCodeSet: s.secrets.HasAccessCode(rec.ID),
		SetupURL: s.BaseURL() + "/setup/printer/" + rec.ID}
}

// GetPrinterRecord lit la configuration d'une imprimante.
func (s *Service) GetPrinterRecord(ctx context.Context, id string) (PrinterRecord, error) {
	var rec PrinterRecord
	if err := s.d.Store.GetDoc(ctx, "printers", id, &rec); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return rec, fmt.Errorf("%w : imprimante %q", ErrNotFound, id)
		}
		return rec, err
	}
	return rec, nil
}

// GetPrinter renvoie la vue (sans secret) d'une imprimante.
func (s *Service) GetPrinter(ctx context.Context, id string) (*PrinterView, error) {
	rec, err := s.GetPrinterRecord(ctx, id)
	if err != nil {
		return nil, err
	}
	model, _ := s.GetPrinterModel(ctx, rec.ModelID)
	return s.view(ctx, rec, model), nil
}

// ListPrinters liste les imprimantes du parc.
func (s *Service) ListPrinters(ctx context.Context) ([]PrinterView, error) {
	raws, err := s.listDocs(ctx, "printers")
	if err != nil {
		return nil, err
	}
	out := []PrinterView{}
	for _, r := range raws {
		var rec PrinterRecord
		if json.Unmarshal([]byte(r), &rec) != nil {
			continue
		}
		model, _ := s.GetPrinterModel(ctx, rec.ModelID)
		out = append(out, *s.view(ctx, rec, model))
	}
	return out, nil
}

// SetAccessCode enregistre le code d'accès (appelé par la page locale uniquement).
func (s *Service) SetAccessCode(ctx context.Context, printerID, code string) error {
	if _, err := s.GetPrinterRecord(ctx, printerID); err != nil {
		return err
	}
	code = strings.TrimSpace(code)
	if len(code) < 4 || len(code) > 64 {
		return invalidf("code d'accès : 4 à 64 caractères")
	}
	if err := s.secrets.SetAccessCode(ctx, printerID, code); err != nil {
		return err
	}
	s.dropAdapter(printerID)
	_ = s.d.Store.Audit(ctx, actor, "printer.access_code_set", "printer", printerID, nil) // jamais la valeur
	return nil
}

// ---- adaptateurs ----

func (s *Service) defaultAdapter(_ context.Context, rec PrinterRecord, secret printer.SecretProvider, verifier printer.ProofVerifier) (printer.Adapter, error) {
	if rec.PinnedSHA256 == "" {
		return nil, fmt.Errorf("%w : le certificat de %s n'est pas épinglé — l'humain doit valider son empreinte sur %s", ErrUnavailable, rec.Name, s.BaseURL()+"/setup/printer/"+rec.ID)
	}
	s.mu.Lock()
	settle := s.d.ReconcileSettle
	s.mu.Unlock()
	return printer.NewBambu(printer.BambuConfig{
		PrinterID: rec.ID, Host: rec.Host, Serial: rec.Serial, MQTTPort: rec.MQTTPort, FTPPort: rec.FTPPort,
		Secrets: secret, TLS: printer.TLSOptions{PinnedSHA256: rec.PinnedSHA256}, Verifier: verifier, Logger: s.log,
		AllowStartFromFinish: rec.AllowStartFromFinish, ReconcileSettle: settle,
	})
}

func (s *Service) dropAdapter(id string) {
	s.mu.Lock()
	e := s.adapters[id]
	delete(s.adapters, id)
	s.mu.Unlock()
	if e != nil {
		if c, ok := e.ad.(interface{ Close() error }); ok {
			_ = c.Close()
		}
	}
}

// adapter renvoie l'adaptateur connecté de l'imprimante (créé à la demande).
func (s *Service) adapter(ctx context.Context, printerID string) (printer.Adapter, PrinterRecord, error) {
	rec, err := s.GetPrinterRecord(ctx, printerID)
	if err != nil {
		return nil, rec, err
	}
	s.mu.Lock()
	if e := s.adapters[printerID]; e != nil {
		s.mu.Unlock()
		return e.ad, e.rec, nil
	}
	s.mu.Unlock()
	if !s.secrets.HasAccessCode(printerID) {
		return nil, rec, fmt.Errorf("%w : %w — l'humain doit le saisir sur %s", ErrUnavailable, ErrNoAccessCode, s.BaseURL()+"/setup/printer/"+printerID)
	}
	ad, err := s.d.Adapters(ctx, rec, accessCodeProvider{store: s.secrets, id: printerID}, s.verifyProof)
	if err != nil {
		return nil, rec, err
	}
	if c, ok := ad.(interface{ Connect(context.Context) error }); ok {
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := c.Connect(cctx)
		cancel()
		if err != nil {
			if cl, ok := ad.(interface{ Close() error }); ok {
				_ = cl.Close()
			}
			return nil, rec, fmt.Errorf("%w : connexion à l'imprimante %s impossible : %v", ErrUnavailable, rec.Name, err)
		}
	}
	s.mu.Lock()
	if e := s.adapters[printerID]; e != nil { // course : garder le premier
		s.mu.Unlock()
		if cl, ok := ad.(interface{ Close() error }); ok {
			_ = cl.Close()
		}
		return e.ad, e.rec, nil
	}
	s.adapters[printerID] = &adapterEntry{ad: ad, rec: rec}
	s.mu.Unlock()
	return ad, rec, nil
}

// PrinterStatus est l'état d'une imprimante avec sa fraîcheur.
type PrinterStatus struct {
	PrinterID    string        `json:"printer_id"`
	Reachable    bool          `json:"reachable"`
	Error        string        `json:"error,omitempty"`
	State        string        `json:"state"`
	GcodeState   string        `json:"gcode_state,omitempty"`
	Percent      int           `json:"progress_percent"`
	Layer        int           `json:"layer"`
	TotalLayers  int           `json:"total_layers"`
	RemainingMin int           `json:"remaining_minutes"`
	Temps        printer.Temps `json:"temps"`
	PrintError   int           `json:"print_error"`
	HMSCount     int           `json:"hms_count"`
	FailReason   string        `json:"fail_reason,omitempty"`
	CurrentFile  string        `json:"current_file,omitempty"`
	Firmware     string        `json:"firmware,omitempty"`
	UpdatedAt    time.Time     `json:"updated_at,omitzero"`
	AgeSeconds   *int          `json:"age_seconds,omitempty" jsonschema:"âge de la dernière information reçue ; absent si jamais reçue"`
	Stale        bool          `json:"stale"`
	Connected    bool          `json:"connected"`
	FullReport   bool          `json:"full_report_seen"`
	PlateEmpty   string        `json:"plate_empty" jsonschema:"toujours « inconnu » : une imprimante idle ne prouve pas un plateau vide"`
	Note         string        `json:"note,omitempty"`
	MaxStartAgeS int           `json:"max_start_age_seconds"`
}

// StatusFromPrinter normalise le statut d'un adaptateur.
func statusOut(id string, st printer.Status, now time.Time) PrinterStatus {
	o := PrinterStatus{PrinterID: id, Reachable: st.Connected, State: string(st.State), GcodeState: st.GcodeState, Percent: st.Progress,
		Layer: st.Layer, TotalLayers: st.TotalLayers, RemainingMin: st.RemainingMin, Temps: st.Temps, PrintError: st.PrintError,
		HMSCount: len(st.HMS), FailReason: st.FailReason, CurrentFile: st.SubtaskName, Firmware: st.Firmware, UpdatedAt: st.UpdatedAt,
		Stale: st.Stale, Connected: st.Connected, FullReport: st.FullReportSeen, PlateEmpty: "inconnu", MaxStartAgeS: 30}
	if !st.UpdatedAt.IsZero() {
		a := int(now.Sub(st.UpdatedAt).Seconds())
		if a < 0 {
			a = 0
		}
		o.AgeSeconds = &a
	}
	return o
}

// GetPrinterStatus lit l'état de l'imprimante via son adaptateur.
func (s *Service) GetPrinterStatus(ctx context.Context, printerID string) (*PrinterStatus, error) {
	ad, rec, err := s.adapter(ctx, printerID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, err
		}
		return &PrinterStatus{PrinterID: printerID, State: string(printer.StateOffline), PlateEmpty: "inconnu", Error: err.Error(), MaxStartAgeS: 30,
			Note: "imprimante non joignable ou non configurée : aucune information d'état"}, nil
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	st, err := ad.Status(cctx)
	out := statusOut(printerID, st, s.now())
	if err != nil {
		out.Error = err.Error()
	}
	if st.Firmware != "" && rec.Firmware == nil {
		fw := st.Firmware
		rec.Firmware = &fw
		_ = s.d.Store.PutDoc(ctx, actor, "printers", rec.ID, rec)
	}
	out.Note = "L'état « idle » ne prouve pas que le plateau est vide : seul l'humain peut l'attester (page de confirmation)."
	return &out, nil
}

// ---- PrintJob : persistance et transitions ----

func (s *Service) createJob(ctx context.Context, j Job) (Job, error) {
	j.ID = contracts.NewID("pj")
	j.CreatedAt, j.UpdatedAt = s.now(), s.now()
	if j.State == "" {
		j.State = contracts.PrintDraft
	}
	if _, err := s.d.Store.PutDocIfRev(ctx, actor, "print_jobs", j.ID, j, 0); err != nil {
		return j, err
	}
	return j, nil
}

// GetJob lit un PrintJob.
func (s *Service) GetJob(ctx context.Context, id string) (Job, error) {
	var j Job
	if err := s.d.Store.GetDoc(ctx, "print_jobs", id, &j); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return j, fmt.Errorf("%w : impression %q", ErrNotFound, id)
		}
		return j, err
	}
	j.Next = nextStep(j)
	return j, nil
}

// ListJobs liste les impressions (récentes d'abord).
func (s *Service) ListJobs(ctx context.Context, state contracts.PrintState, limit int) ([]Job, error) {
	raws, err := s.listDocs(ctx, "print_jobs")
	if err != nil {
		return nil, err
	}
	out := []Job{}
	for i := len(raws) - 1; i >= 0; i-- {
		var j Job
		if json.Unmarshal([]byte(raws[i]), &j) != nil || (state != "" && j.State != state) {
			continue
		}
		j.Next = nextStep(j)
		out = append(out, j)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// update applique mutate sous contrôle de version optimiste (rejoue en cas de conflit).
func (s *Service) update(ctx context.Context, id string, mutate func(*Job) error) (Job, error) {
	for attempt := 0; attempt < 12; attempt++ {
		var j Job
		rev, err := s.d.Store.GetDocRev(ctx, "print_jobs", id, &j)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return j, fmt.Errorf("%w : impression %q", ErrNotFound, id)
			}
			return j, err
		}
		if err := mutate(&j); err != nil {
			return j, err
		}
		j.UpdatedAt = s.now()
		if _, err := s.d.Store.PutDocIfRev(ctx, actor, "print_jobs", id, j, rev); err != nil {
			if errors.Is(err, store.ErrConflict) {
				time.Sleep(time.Duration(attempt+1) * 3 * time.Millisecond)
				continue
			}
			return j, err
		}
		j.Next = nextStep(j)
		return j, nil
	}
	return Job{}, errors.New("impression modifiée en continu par ailleurs : réessayer")
}

// transition applique un événement de la machine d'états (refus si illégal) puis mutate.
func (s *Service) transition(ctx context.Context, id string, ev contracts.PrintEvent, note string, mutate func(*Job) error) (Job, error) {
	return s.update(ctx, id, func(j *Job) error {
		from := j.State
		next, err := contracts.Transition(from, ev)
		if err != nil {
			return fmt.Errorf("%w : %v", ErrState, err)
		}
		j.State = next
		j.event(ev, from, next, note, s.now())
		if mutate != nil {
			return mutate(j)
		}
		return nil
	})
}

func nextStep(j Job) string {
	switch j.State {
	case contracts.PrintDraft, contracts.PrintPrepared:
		return "execute_headless_slice (ou validate_print_artifact sur un artefact existant)"
	case contracts.PrintSlicing:
		return "attendre la fin de la découpe (get_print_status / get_job_status)"
	case contracts.PrintValidated:
		return "validate_print_artifact (compatibilité imprimante) puis upload_print_artifact"
	case contracts.PrintUploading:
		return "attendre la fin du dépôt (get_print_status)"
	case contracts.PrintUploaded, contracts.PrintAwaitingConfirmation:
		return "demander explicitement à l'utilisateur s'il veut lancer, puis request_print_confirmation ; l'humain valide sur la page locale ; ensuite start_print"
	case contracts.PrintStartRequested:
		return "suivre avec get_print_status ; ne pas relancer"
	case contracts.PrintStartUnknown:
		return "reconcile_print (jamais de relance automatique)"
	case contracts.PrintPrinting:
		return "get_print_status"
	}
	return ""
}

// errors utilitaires
func isCtx(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// SetValidator substitue le validateur d'archive (tests, validateurs qualifiés). nil rétablit
// slicer.ValidateArchive. À appeler avant tout usage concurrent.
func (s *Service) SetValidator(v ArchiveValidator) {
	if v == nil {
		v = slicer.ValidateArchive
	}
	s.d.Validator = v
}

var _ = sql.ErrNoRows
