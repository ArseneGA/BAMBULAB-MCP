package filament

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ArseneGA/BAMBULAB-MCP/internal/contracts"
	"github.com/ArseneGA/BAMBULAB-MCP/internal/store"
)

// SpoolObserved est la dernière lecture de l'AMS pour la bobine (jamais la saisie de l'humain).
type SpoolObserved struct {
	Type          string    `json:"type,omitempty"`
	Color         string    `json:"color,omitempty"`
	RemainPercent int       `json:"remain_percent"`
	Slot          string    `json:"slot,omitempty"`
	At            time.Time `json:"at"`
}

// Spool est une bobine de l'inventaire.
type Spool struct {
	ID              string         `json:"id"`
	Material        string         `json:"material" jsonschema:"matière (PLA, PETG, ABS, TPU, ...)"`
	Brand           string         `json:"brand,omitempty"`
	Name            string         `json:"name,omitempty" jsonschema:"nom de la couleur (ex. Blanc os)"`
	ColorHex        string         `json:"color_hex,omitempty" jsonschema:"#RRGGBB"`
	DiameterMM      float64        `json:"diameter_mm"`
	NetWeightG      float64        `json:"net_weight_g" jsonschema:"poids de filament d'une bobine neuve"`
	RemainingG      float64        `json:"remaining_g" jsonschema:"poids restant estimé"`
	RemainingSource string         `json:"remaining_source" jsonschema:"human (saisi par l'utilisateur : jamais écrasé par l'AMS) | ams (déduit du % rapporté par l'AMS) | consumption (décompté après impression) | default (bobine neuve présumée)"`
	PricePerKg      *float64       `json:"price_per_kg,omitempty" jsonschema:"prix du kilo de filament, dans la monnaie de l'utilisateur (lu par le calcul de coût)"`
	OpenedAt        string         `json:"opened_at,omitempty" jsonschema:"date d'ouverture (AAAA-MM-JJ)"`
	LastDriedAt     string         `json:"last_dried_at,omitempty" jsonschema:"date du dernier séchage (AAAA-MM-JJ)"`
	DryingNote      string         `json:"drying_note,omitempty" jsonschema:"réglage ou remarque de séchage (ex. 55 °C 8 h)"`
	Notes           string         `json:"notes,omitempty"`
	PrinterID       string         `json:"printer_id,omitempty" jsonschema:"imprimante où la bobine est chargée"`
	Slot            string         `json:"slot,omitempty" jsonschema:"emplacement ams1:1..ams4:4 ou ext, vide si rangée"`
	RFIDUUID        string         `json:"rfid_uuid,omitempty" jsonschema:"identifiant RFID lu par l'AMS (bobines Bambu)"`
	Archived        bool           `json:"archived"`
	HumanFields     []string       `json:"human_fields,omitempty" jsonschema:"champs saisis par l'utilisateur : la lecture de l'AMS ne les modifie jamais"`
	Observed        *SpoolObserved `json:"observed,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`

	// Calculés à la lecture.
	RemainingPercent int    `json:"remaining_percent" jsonschema:"restant en % du poids net, -1 si le poids net est inconnu"`
	Status           string `json:"status" jsonschema:"ok | low (moins de 10 % ou 50 g) | empty | archived"`
}

func (sp *Spool) isHuman(f string) bool { return slices.Contains(sp.HumanFields, f) }

func (sp *Spool) markHuman(f string) {
	if !sp.isHuman(f) {
		sp.HumanFields = append(sp.HumanFields, f)
	}
}

func (sp *Spool) compute() {
	sp.RemainingPercent = -1
	if sp.NetWeightG > 0 {
		sp.RemainingPercent = int(sp.RemainingG / sp.NetWeightG * 100)
	}
	switch {
	case sp.Archived:
		sp.Status = "archived"
	case sp.RemainingG <= 0:
		sp.Status = "empty"
	case sp.RemainingG < 50 || (sp.NetWeightG > 0 && sp.RemainingG < 0.1*sp.NetWeightG):
		sp.Status = "low"
	default:
		sp.Status = "ok"
	}
}

// Label est un libellé court pour les affectations et les messages.
func (sp *Spool) Label() string {
	parts := []string{}
	if sp.Brand != "" {
		parts = append(parts, sp.Brand)
	}
	parts = append(parts, sp.Material)
	if sp.Name != "" {
		parts = append(parts, sp.Name)
	} else if sp.ColorHex != "" {
		parts = append(parts, sp.ColorHex)
	}
	return strings.Join(parts, " ")
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

var formatTime = store.FormatTime

func slotKey(printerID, slot string) string {
	if printerID == "" || slot == "" {
		return ""
	}
	return printerID + "|" + slot
}

var (
	slotRe = regexp.MustCompile(`^(ams[1-4]:[1-4]|ext)$`)
	dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

func (s *Service) loadSpool(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (*Spool, int64, error) {
	var doc string
	var rev int64
	err := q.QueryRowContext(ctx, `SELECT doc, rev FROM spools WHERE id=?`, id).Scan(&doc, &rev)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, fmt.Errorf("%w : bobine %q", ErrNotFound, id)
	}
	if err != nil {
		return nil, 0, err
	}
	var sp Spool
	if err := json.Unmarshal([]byte(doc), &sp); err != nil {
		return nil, 0, err
	}
	sp.compute()
	return &sp, rev, nil
}

// GetSpool lit une bobine.
func (s *Service) GetSpool(ctx context.Context, id string) (*Spool, error) {
	sp, _, err := s.loadSpool(ctx, s.d.Store.DB(), id)
	return sp, err
}

// ListFilter filtre l'inventaire.
type ListFilter struct {
	PrinterID       string
	Material        string
	Slot            string
	IncludeArchived bool
	LowOnly         bool
	Loaded          *bool
}

// ListSpools liste l'inventaire (bobines chargées d'abord, puis par matière).
func (s *Service) ListSpools(ctx context.Context, f ListFilter) ([]Spool, error) {
	rows, err := s.d.Store.DB().QueryContext(ctx, `SELECT doc FROM spools ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Spool{}
	for rows.Next() {
		var doc string
		if err := rows.Scan(&doc); err != nil {
			return nil, err
		}
		var sp Spool
		if json.Unmarshal([]byte(doc), &sp) != nil {
			continue
		}
		sp.compute()
		if sp.Archived && !f.IncludeArchived {
			continue
		}
		if f.PrinterID != "" && sp.PrinterID != f.PrinterID {
			continue
		}
		if f.Material != "" && !sameMaterial(sp.Material, f.Material) {
			continue
		}
		if f.Slot != "" && sp.Slot != f.Slot {
			continue
		}
		if f.Loaded != nil && (sp.Slot != "") != *f.Loaded {
			continue
		}
		if f.LowOnly && sp.Status != "low" && sp.Status != "empty" {
			continue
		}
		out = append(out, sp)
	}
	slices.SortStableFunc(out, func(a, b Spool) int {
		if (a.Slot != "") != (b.Slot != "") {
			if a.Slot != "" {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Material, b.Material)
	})
	return out, rows.Err()
}

// SpoolAt renvoie la bobine active chargée à cet emplacement, ou nil.
func (s *Service) SpoolAt(ctx context.Context, printerID, slot string) (*Spool, error) {
	k := slotKey(printerID, slot)
	if k == "" {
		return nil, nil
	}
	var id string
	err := s.d.Store.DB().QueryRowContext(ctx, `SELECT id FROM spools WHERE slot_key=? AND archived=0`, k).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.GetSpool(ctx, id)
}

// UpdateInput crée (SpoolID vide) ou modifie une bobine. Les champs absents sont inchangés ; tout champ fourni
// est une saisie de l'utilisateur et n'est plus jamais écrasé par la lecture de l'AMS.
type UpdateInput struct {
	SpoolID         string   `json:"spool_id,omitempty" jsonschema:"bobine à modifier ; vide = créer une bobine"`
	Material        string   `json:"material,omitempty" jsonschema:"PLA, PETG, ABS... (obligatoire à la création)"`
	Brand           string   `json:"brand,omitempty"`
	Name            string   `json:"name,omitempty" jsonschema:"nom de la couleur"`
	ColorHex        string   `json:"color_hex,omitempty" jsonschema:"#RRGGBB"`
	DiameterMM      *float64 `json:"diameter_mm,omitempty" jsonschema:"1.75 (défaut) ou 2.85"`
	NetWeightG      *float64 `json:"net_weight_g,omitempty" jsonschema:"poids net d'une bobine neuve (défaut 1000)"`
	RemainingG      *float64 `json:"remaining_g,omitempty" jsonschema:"poids restant (mesuré à la balance par exemple)"`
	PricePerKg      *float64 `json:"price_per_kg,omitempty" jsonschema:"prix du kilo"`
	OpenedAt        string   `json:"opened_at,omitempty" jsonschema:"AAAA-MM-JJ ou « today »"`
	LastDriedAt     string   `json:"last_dried_at,omitempty" jsonschema:"AAAA-MM-JJ ou « today » : bobine séchée"`
	DryingNote      string   `json:"drying_note,omitempty"`
	Notes           string   `json:"notes,omitempty"`
	PrinterID       string   `json:"printer_id,omitempty" jsonschema:"imprimante où la bobine est chargée (avec slot)"`
	Slot            string   `json:"slot,omitempty" jsonschema:"emplacement où la bobine est chargée : ams1:1..ams4:4 ou ext"`
	Unassign        bool     `json:"unassign,omitempty" jsonschema:"retire la bobine de son emplacement (rangée)"`
	ReplaceExisting bool     `json:"replace_existing,omitempty" jsonschema:"si l'emplacement porte déjà une autre bobine : la range au lieu de refuser"`
	Archived        *bool    `json:"archived,omitempty" jsonschema:"archive (vide, terminée) ou restaure la bobine"`
}

func today(now time.Time) string { return now.Format("2006-01-02") }

func (s *Service) normDate(v string) (string, error) {
	v = strings.TrimSpace(v)
	if strings.EqualFold(v, "today") || v == "aujourd'hui" {
		return today(s.now()), nil
	}
	if !dateRe.MatchString(v) {
		return "", invalidf("date %q : format AAAA-MM-JJ attendu", v)
	}
	if _, err := time.Parse("2006-01-02", v); err != nil {
		return "", invalidf("date %q invalide", v)
	}
	return v, nil
}

// UpdateSpool crée ou modifie une bobine.
func (s *Service) UpdateSpool(ctx context.Context, in UpdateInput) (*Spool, error) {
	create := in.SpoolID == ""
	var out *Spool
	// La base n'a qu'une connexion : aucune autre requête pendant la transaction (contrôle fait avant).
	if in.PrinterID != "" && s.d.Printing != nil {
		if _, err := s.d.Printing.GetPrinterRecord(ctx, in.PrinterID); err != nil {
			return nil, invalidf("imprimante %q inconnue", in.PrinterID)
		}
	}
	err := s.d.Store.Tx(ctx, func(tx *sql.Tx) error {
		var sp *Spool
		var rev int64
		if create {
			if strings.TrimSpace(in.Material) == "" {
				return invalidf("material requis pour créer une bobine")
			}
			now := s.now()
			sp = &Spool{ID: contracts.NewID("sp"), DiameterMM: 1.75, NetWeightG: 1000, RemainingSource: "default", CreatedAt: now}
			sp.RemainingG = sp.NetWeightG
		} else {
			var err error
			if sp, rev, err = s.loadSpool(ctx, tx, in.SpoolID); err != nil {
				return err
			}
		}
		if err := s.apply(ctx, tx, sp, in, create); err != nil {
			return err
		}
		sp.UpdatedAt = s.now()
		sp.compute()
		raw, _ := json.Marshal(sp)
		key := slotKey(sp.PrinterID, sp.Slot)
		if sp.Archived {
			key = ""
		}
		arch := 0
		if sp.Archived {
			arch = 1
		}
		if create {
			if _, err := tx.ExecContext(ctx, `INSERT INTO spools(id,doc,slot_key,archived,created_at,updated_at,rev) VALUES(?,?,?,?,?,?,1)`,
				sp.ID, string(raw), key, arch, store.FormatTime(sp.CreatedAt), store.FormatTime(sp.UpdatedAt)); err != nil {
				return err
			}
		} else {
			res, err := tx.ExecContext(ctx, `UPDATE spools SET doc=?, slot_key=?, archived=?, updated_at=?, rev=rev+1 WHERE id=? AND rev=?`,
				string(raw), key, arch, store.FormatTime(sp.UpdatedAt), sp.ID, rev)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return fmt.Errorf("%w : bobine %s modifiée en parallèle, réessayer", ErrConflict, sp.ID)
			}
		}
		op := "spool.update"
		if create {
			op = "spool.create"
		}
		out = sp
		return s.d.Store.AuditTx(ctx, tx, actor, op, "spool", sp.ID, nil)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) apply(ctx context.Context, tx *sql.Tx, sp *Spool, in UpdateInput, create bool) error {
	if v := strings.TrimSpace(in.Material); v != "" {
		sp.Material = v
		sp.markHuman("material")
	}
	if in.Brand != "" {
		sp.Brand = strings.TrimSpace(in.Brand)
		sp.markHuman("brand")
	}
	if in.Name != "" {
		sp.Name = strings.TrimSpace(in.Name)
		sp.markHuman("name")
	}
	if in.ColorHex != "" {
		h := normHex(in.ColorHex)
		if h == "" {
			return invalidf("color_hex %q : #RRGGBB attendu", in.ColorHex)
		}
		sp.ColorHex = h
		sp.markHuman("color_hex")
	}
	if in.DiameterMM != nil {
		if d := *in.DiameterMM; d != 1.75 && d != 2.85 {
			return invalidf("diameter_mm %g : 1.75 ou 2.85", d)
		}
		sp.DiameterMM = *in.DiameterMM
	}
	if in.NetWeightG != nil {
		if *in.NetWeightG <= 0 || *in.NetWeightG > 20000 {
			return invalidf("net_weight_g %g hors plage (0..20000)", *in.NetWeightG)
		}
		sp.NetWeightG = *in.NetWeightG
		sp.markHuman("net_weight_g")
		if create && in.RemainingG == nil {
			sp.RemainingG = sp.NetWeightG
		}
	}
	if in.RemainingG != nil {
		if *in.RemainingG < 0 || *in.RemainingG > 20000 {
			return invalidf("remaining_g %g hors plage (0..20000)", *in.RemainingG)
		}
		sp.RemainingG = *in.RemainingG
		sp.RemainingSource = "human"
		sp.markHuman("remaining_g")
	}
	if in.PricePerKg != nil {
		if *in.PricePerKg < 0 || *in.PricePerKg > 100000 {
			return invalidf("price_per_kg %g hors plage", *in.PricePerKg)
		}
		p := *in.PricePerKg
		sp.PricePerKg = &p
		sp.markHuman("price_per_kg")
	}
	if in.OpenedAt != "" {
		d, err := s.normDate(in.OpenedAt)
		if err != nil {
			return err
		}
		sp.OpenedAt = d
		sp.markHuman("opened_at")
	}
	if in.LastDriedAt != "" {
		d, err := s.normDate(in.LastDriedAt)
		if err != nil {
			return err
		}
		sp.LastDriedAt = d
		sp.markHuman("last_dried_at")
	}
	if in.DryingNote != "" {
		sp.DryingNote = in.DryingNote
	}
	if in.Notes != "" {
		sp.Notes = in.Notes
	}
	if in.Archived != nil {
		sp.Archived = *in.Archived
		if sp.Archived {
			sp.PrinterID, sp.Slot = "", ""
		}
	}
	if in.Unassign {
		sp.PrinterID, sp.Slot = "", ""
		sp.markHuman("slot")
	}
	if in.Slot != "" || in.PrinterID != "" {
		slot, pid := in.Slot, in.PrinterID
		if slot == "" {
			slot = sp.Slot
		}
		if pid == "" {
			pid = sp.PrinterID
		}
		if !slotRe.MatchString(slot) {
			return invalidf("slot %q : ams1:1..ams4:4 ou ext attendu", slot)
		}
		if pid == "" {
			return invalidf("printer_id requis pour charger une bobine dans %s", slot)
		}
		if sp.Archived {
			return invalidf("la bobine est archivée : la restaurer d'abord")
		}
		var other string
		err := tx.QueryRowContext(ctx, `SELECT id FROM spools WHERE slot_key=? AND archived=0 AND id<>?`, slotKey(pid, slot), sp.ID).Scan(&other)
		switch {
		case err == nil && !in.ReplaceExisting:
			return fmt.Errorf("%w : %s porte déjà la bobine %s ; ranger celle-ci d'abord ou passer replace_existing=true", ErrConflict, slot, other)
		case err == nil:
			if err := s.unloadTx(ctx, tx, other); err != nil {
				return err
			}
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		sp.PrinterID, sp.Slot = pid, slot
		sp.markHuman("slot")
		sp.Observed = nil
	}
	if sp.Material == "" {
		return invalidf("material requis")
	}
	return nil
}

// unloadTx range une bobine (la retire de son emplacement) dans la transaction.
func (s *Service) unloadTx(ctx context.Context, tx *sql.Tx, id string) error {
	sp, rev, err := s.loadSpool(ctx, tx, id)
	if err != nil {
		return err
	}
	sp.PrinterID, sp.Slot, sp.Observed = "", "", nil
	sp.UpdatedAt = s.now()
	raw, _ := json.Marshal(sp)
	_, err = tx.ExecContext(ctx, `UPDATE spools SET doc=?, slot_key='', updated_at=?, rev=rev+1 WHERE id=? AND rev=?`, string(raw), store.FormatTime(sp.UpdatedAt), id, rev)
	return err
}

// mutate applique une modification interne (lecture AMS, décompte) sous contrôle de version, avec nouvelles tentatives.
func (s *Service) mutate(ctx context.Context, id string, fn func(*Spool) (changed bool)) (*Spool, error) {
	for attempt := 0; attempt < 8; attempt++ {
		sp, rev, err := s.loadSpool(ctx, s.d.Store.DB(), id)
		if err != nil {
			return nil, err
		}
		if !fn(sp) {
			return sp, nil
		}
		sp.UpdatedAt = s.now()
		sp.compute()
		raw, _ := json.Marshal(sp)
		res, err := s.d.Store.DB().ExecContext(ctx, `UPDATE spools SET doc=?, updated_at=?, rev=rev+1 WHERE id=? AND rev=?`, string(raw), store.FormatTime(sp.UpdatedAt), id, rev)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			return sp, nil
		}
		time.Sleep(time.Duration(attempt+1) * 3 * time.Millisecond)
	}
	return nil, fmt.Errorf("%w : bobine %s modifiée en continu", ErrConflict, id)
}

// ---- réglages ----

// Settings : marges de l'alerte de manque.
type Settings struct {
	MarginPercent float64 `json:"margin_percent" jsonschema:"marge d'alerte en % du besoin (défaut 10)"`
	MarginGrams   float64 `json:"margin_grams" jsonschema:"marge d'alerte fixe en grammes, ajoutée (défaut 5)"`
}

// GetSettings lit les réglages (valeurs par défaut si jamais enregistrés).
func (s *Service) GetSettings(ctx context.Context) Settings {
	st := Settings{MarginPercent: 10, MarginGrams: 5}
	var v string
	if s.d.Store.DB().QueryRowContext(ctx, `SELECT value FROM filament_settings WHERE key='margin'`).Scan(&v) == nil {
		_ = json.Unmarshal([]byte(v), &st)
	}
	return st
}

// SetSettings enregistre les marges d'alerte.
func (s *Service) SetSettings(ctx context.Context, st Settings) (Settings, error) {
	if st.MarginPercent < 0 || st.MarginPercent > 200 || st.MarginGrams < 0 || st.MarginGrams > 5000 {
		return st, invalidf("marge hors plage (0..200 %% et 0..5000 g)")
	}
	raw, _ := json.Marshal(st)
	_, err := s.d.Store.DB().ExecContext(ctx, `INSERT INTO filament_settings(key,value,updated_at) VALUES('margin',?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		string(raw), store.FormatTime(s.now()))
	if err == nil {
		_ = s.d.Store.Audit(ctx, actor, "filament.settings", "filament_settings", "margin", st)
	}
	return st, err
}
