package printer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"
)

// Fichiers de la carte SD (FTPS), commandes MQTT annexes (chargement de la bobine externe). Interfaces OPTIONNELLES :
// l'interface Adapter n'est pas modifiée ; le service type-asserte (un adaptateur qui ne les a pas répond « indisponible »).

// RemoteFile : un fichier de la racine de la carte SD.
type RemoteFile struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified,omitzero"`
}

// FileLister liste les fichiers de la carte SD (lecture seule).
type FileLister interface {
	ListFiles(ctx context.Context) ([]RemoteFile, error)
}

// FileDeleter supprime UN fichier de la racine de la carte SD (nom sûr uniquement).
type FileDeleter interface {
	DeleteFile(ctx context.Context, name string) error
}

// RemoteCheck : résultat de la vérification d'un fichier déjà déposé.
type RemoteCheck struct {
	Exists     bool   `json:"exists"`
	Size       int64  `json:"size"`
	SizeOK     bool   `json:"size_ok"`
	Hashed     bool   `json:"hashed" jsonschema:"vrai si le fichier a été relu en entier pour recalculer son sha256"`
	SHA256     string `json:"sha256,omitempty"`
	SHA256OK   bool   `json:"sha256_ok"`
	NameSHAOK  bool   `json:"name_sha_ok" jsonschema:"l'empreinte courte portée par le nom du fichier correspond"`
	Mismatch   string `json:"mismatch,omitempty"`
	CheckedMsg string `json:"checked,omitempty"`
}

// RemoteVerifier vérifie qu'un fichier déposé est toujours sur la carte, avec la bonne taille et (hash=true) le bon sha256.
type RemoteVerifier interface {
	VerifyRemote(ctx context.Context, ref RemoteRef, hash bool) (RemoteCheck, error)
}

// FilamentLoader charge le filament de la bobine externe (commande MQTT unique, jamais renvoyée).
type FilamentLoader interface {
	LoadExternalFilament(ctx context.Context, nozzleTempC int) error
}

// SafeRemoteName : nom de fichier déposé par bambud (<radical>-<sha12>.gcode.3mf ou .3mf), sans chemin ni caractère exotique.
var safeRemoteName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,200}-([0-9a-f]{12})\.(gcode\.)?3mf$`)

// SafeRemoteName dit si le nom a la forme des dépôts de bambud ; renvoie l'empreinte courte.
func SafeRemoteName(name string) (sha12 string, ok bool) {
	m := safeRemoteName.FindStringSubmatch(name)
	if m == nil || strings.Contains(name, "..") {
		return "", false
	}
	return m[1], true
}

// ListFiles liste les fichiers de la racine de la carte SD.
func (b *Bambu) ListFiles(ctx context.Context) ([]RemoteFile, error) {
	b.uploadMu.Lock()
	defer b.uploadMu.Unlock()
	c, err := b.dialFTP(ctx)
	if err != nil {
		return nil, err
	}
	defer c.Quit()
	ents, err := c.List("/")
	if err != nil {
		return nil, fmt.Errorf("printer: ftps list: %w", err)
	}
	var out []RemoteFile
	for _, e := range ents {
		if e.Type != ftp.EntryTypeFile {
			continue
		}
		out = append(out, RemoteFile{Name: e.Name, Size: int64(e.Size), Modified: e.Time})
	}
	return out, nil
}

// DeleteFile supprime un fichier déposé par bambud (le nom doit avoir la forme sûre).
func (b *Bambu) DeleteFile(ctx context.Context, name string) error {
	if _, ok := SafeRemoteName(name); !ok {
		return fmt.Errorf("printer: refus de supprimer %q : ce n'est pas un fichier déposé par bambud", name)
	}
	b.uploadMu.Lock()
	defer b.uploadMu.Unlock()
	c, err := b.dialFTP(ctx)
	if err != nil {
		return err
	}
	defer c.Quit()
	if err := c.Delete(name); err != nil {
		return fmt.Errorf("printer: ftps delete %q: %w", name, err)
	}
	return nil
}

// VerifyRemote vérifie la présence, la taille et (hash) le sha256 d'un fichier déjà déposé, sans rien envoyer.
func (b *Bambu) VerifyRemote(ctx context.Context, ref RemoteRef, hash bool) (RemoteCheck, error) {
	var rc RemoteCheck
	sha12, nameOK := SafeRemoteName(ref.Name)
	rc.NameSHAOK = nameOK && strings.HasPrefix(strings.ToLower(ref.SHA256), sha12)
	b.uploadMu.Lock()
	defer b.uploadMu.Unlock()
	c, err := b.dialFTP(ctx)
	if err != nil {
		return rc, err
	}
	defer c.Quit()
	n, err := c.FileSize(ref.Name)
	if err != nil {
		rc.Mismatch = "le fichier n'est plus sur la carte de l'imprimante"
		return rc, nil
	}
	rc.Exists, rc.Size, rc.SizeOK = true, n, n == ref.Size
	if !rc.SizeOK {
		rc.Mismatch = fmt.Sprintf("taille différente : %d octets sur la carte, %d attendus", n, ref.Size)
		return rc, nil
	}
	if !hash {
		rc.CheckedMsg = "présence et taille vérifiées (empreinte courte du nom comparée, contenu non relu)"
		return rc, nil
	}
	r, err := c.Retr(ref.Name)
	if err != nil {
		return rc, fmt.Errorf("printer: ftps retr: %w", err)
	}
	h := sha256.New()
	_, cerr := io.Copy(h, io.LimitReader(r, ref.Size+1))
	_ = r.Close()
	if cerr != nil {
		return rc, fmt.Errorf("printer: relecture du fichier : %w", cerr)
	}
	rc.Hashed, rc.SHA256 = true, hex.EncodeToString(h.Sum(nil))
	rc.SHA256OK = strings.EqualFold(rc.SHA256, ref.SHA256)
	if !rc.SHA256OK {
		rc.Mismatch = "le contenu du fichier sur la carte ne correspond plus à l'empreinte sha256 attendue"
	} else {
		rc.CheckedMsg = "présence, taille et sha256 vérifiés (fichier relu en entier)"
	}
	return rc, nil
}

// LoadExternalFilament envoie UNE commande de chargement du filament de la bobine externe (ams_change_filament, cible 254
// = bobine externe, température de buse cible). Elle attend l'accusé de transport puis la réponse de l'imprimante ; aucun
// renvoi, aucun état attendu (le chargement se suit à l'écran). FORMAT DE LA COMMANDE À VÉRIFIER sur la vraie P1S
// (OpenBambuAPI / ha-bambulab : « ams_change_filament », target 254) : voir docs/UTILISATION.md.
func (b *Bambu) LoadExternalFilament(ctx context.Context, nozzleTempC int) error {
	c := b.current()
	if c == nil {
		return ErrNotConnected
	}
	if nozzleTempC < 150 || nozzleTempC > 320 {
		return fmt.Errorf("printer: température de chargement hors plage (%d °C)", nozzleTempC)
	}
	st := b.snapshot()
	if st.State == StatePrinting || st.State == StatePreparing {
		return fmt.Errorf("%w: load_filament while %s", ErrBadState, st.State)
	}
	seq := b.nextSeq()
	ch := b.registerWaiter(seq)
	defer b.dropWaiter(seq)
	cur := int(st.Temps.Nozzle)
	if err := b.publishAcked(ctx, c, map[string]any{"print": map[string]any{"sequence_id": seq, "command": "ams_change_filament",
		"target": 254, "curr_temp": cur, "tar_temp": nozzleTempC}}); err != nil {
		return fmt.Errorf("printer: load_filament not acknowledged: %w", err)
	}
	err := b.waitEval(ctx, c, ch, b.cfg.CommandTimeout, func(resp map[string]any, _ Status) (bool, error) {
		if resp == nil {
			return false, nil
		}
		if r := str(resp["result"]); r != "" && !strings.EqualFold(r, "success") {
			return false, fmt.Errorf("printer: load_filament refused: %s %s", r, str(resp["reason"]))
		}
		return true, nil
	})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errTimeout):
		return fmt.Errorf("printer: load_filament: %w (pas de réponse de l'imprimante)", ErrStateNotObserved)
	case errors.Is(err, errLost):
		return fmt.Errorf("printer: load_filament: %w", ErrNotConnected)
	}
	return err
}
