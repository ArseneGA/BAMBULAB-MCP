package agent

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Lecture d'un projet Bambu Studio (.3mf) : objets, plateaux, paramètres.
// Seules les entrées de métadonnées sont lues ; 3D/3dmodel.model (qui peut peser
// plusieurs dizaines de Mo) n'est lu que sur ses premiers Ko pour l'application
// auteur. Structure vérifiée sur un projet réel enregistré par Studio (voir
// docs/jalons/J8.md) : Metadata/model_settings.config, project_settings.config,
// slice_info.config, plate_N.json.

// ProjectObject est un objet du projet.
type ProjectObject struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Extruder  string   `json:"extruder,omitempty"`
	FaceCount int      `json:"face_count,omitempty"`
	Parts     []string `json:"parts,omitempty"`
}

// ProjectPlate est un plateau avec les objets qu'il porte.
type ProjectPlate struct {
	Index     int      `json:"index"`
	Name      string   `json:"name,omitempty"`
	Locked    bool     `json:"locked,omitempty"`
	ObjectIDs []string `json:"object_ids"`
	Objects   []string `json:"objects,omitempty"`
}

// ProjectState est l'état SAUVEGARDÉ d'un projet.
type ProjectState struct {
	File        string          `json:"file"`
	SizeBytes   int64           `json:"size_bytes"`
	SHA256      string          `json:"sha256"`
	ModifiedAt  time.Time       `json:"modified_at"`
	Application string          `json:"application,omitempty"`
	Objects     []ProjectObject `json:"objects"`
	Plates      []ProjectPlate  `json:"plates"`
	Settings    map[string]any  `json:"settings,omitempty"`
	Sliced      bool            `json:"sliced"`
	ClientVer   string          `json:"client_version,omitempty"`
	Limits      []string        `json:"limits"`
}

// settingKeys : paramètres du projet exposés (le fichier en contient des centaines).
var settingKeys = []string{
	"printer_settings_id", "print_settings_id", "filament_settings_id", "printer_model", "nozzle_diameter",
	"curr_bed_type", "layer_height", "initial_layer_print_height", "wall_loops", "sparse_infill_density",
	"sparse_infill_pattern", "top_shell_layers", "bottom_shell_layers", "enable_support", "support_type",
	"support_style", "seam_position", "brim_type", "filament_type", "enable_prime_tower", "version",
}

type xmlMeta struct {
	Key       string `xml:"key,attr"`
	Value     string `xml:"value,attr"`
	FaceCount string `xml:"face_count,attr"`
}

type xmlPart struct {
	ID   string    `xml:"id,attr"`
	Meta []xmlMeta `xml:"metadata"`
}

type xmlObject struct {
	ID    string    `xml:"id,attr"`
	Meta  []xmlMeta `xml:"metadata"`
	Parts []xmlPart `xml:"part"`
}

type xmlInstance struct {
	Meta []xmlMeta `xml:"metadata"`
}

type xmlPlate struct {
	Meta      []xmlMeta     `xml:"metadata"`
	Instances []xmlInstance `xml:"model_instance"`
}

type xmlConfig struct {
	Objects []xmlObject `xml:"object"`
	Plates  []xmlPlate  `xml:"plate"`
}

func meta(ms []xmlMeta, key string) string {
	for _, m := range ms {
		if m.Key == key {
			return m.Value
		}
	}
	return ""
}

const maxMetaBytes = 32 << 20 // une entrée de métadonnées au-delà est refusée

func readEntry(zr *zip.Reader, name string, limit int64) ([]byte, bool, error) {
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		if int64(f.UncompressedSize64) > limit {
			return nil, true, fmt.Errorf("entrée %s trop grosse (%d octets)", name, f.UncompressedSize64)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, true, err
		}
		defer rc.Close()
		b, err := io.ReadAll(io.LimitReader(rc, limit))
		return b, true, err
	}
	return nil, false, nil
}

var appRe = regexp.MustCompile(`<metadata name="Application">([^<]*)</metadata>`)

// ReadProject lit un .3mf (ouvert en lecture seule ; l'appelant travaille sur une copie).
func ReadProject(path string) (*ProjectState, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		return nil, fmt.Errorf("%s n'est pas une archive 3MF : %w", path, err)
	}
	ps := &ProjectState{File: path, SizeBytes: st.Size(), SHA256: hex.EncodeToString(h.Sum(nil)), ModifiedAt: st.ModTime().UTC(),
		Objects: []ProjectObject{}, Plates: []ProjectPlate{}}
	ps.Limits = []string{
		"état SAUVEGARDÉ du fichier : les modifications non enregistrées dans Studio ne sont pas visibles",
		"lecture seule d'une copie ; aucune écriture dans Studio ni dans le projet",
	}

	// Application auteur : premiers Ko du modèle seulement.
	for _, e := range zr.File {
		if e.Name == "3D/3dmodel.model" {
			if rc, err := e.Open(); err == nil {
				head, _ := io.ReadAll(io.LimitReader(rc, 8192))
				rc.Close()
				if m := appRe.FindSubmatch(head); m != nil {
					ps.Application = string(m[1])
				}
			}
		}
	}

	objName := map[string]string{}
	if b, ok, err := readEntry(zr, "Metadata/model_settings.config", maxMetaBytes); err != nil {
		return nil, err
	} else if ok {
		var cfg xmlConfig
		if err := xml.Unmarshal(b, &cfg); err != nil {
			return nil, fmt.Errorf("model_settings.config : %w", err)
		}
		for _, o := range cfg.Objects {
			po := ProjectObject{ID: o.ID, Name: meta(o.Meta, "name"), Extruder: meta(o.Meta, "extruder")}
			for _, m := range o.Meta {
				if m.FaceCount != "" {
					fmt.Sscan(m.FaceCount, &po.FaceCount)
				}
			}
			for _, p := range o.Parts {
				po.Parts = append(po.Parts, meta(p.Meta, "name"))
			}
			objName[po.ID] = po.Name
			ps.Objects = append(ps.Objects, po)
		}
		for i, p := range cfg.Plates {
			pp := ProjectPlate{Index: i + 1, Name: meta(p.Meta, "plater_name"), Locked: meta(p.Meta, "locked") == "true", ObjectIDs: []string{}}
			fmt.Sscan(meta(p.Meta, "plater_id"), &pp.Index)
			for _, in := range p.Instances {
				id := meta(in.Meta, "object_id")
				pp.ObjectIDs = append(pp.ObjectIDs, id)
				if n := objName[id]; n != "" {
					pp.Objects = append(pp.Objects, n)
				}
			}
			ps.Plates = append(ps.Plates, pp)
		}
		sort.Slice(ps.Plates, func(i, j int) bool { return ps.Plates[i].Index < ps.Plates[j].Index })
	}

	if b, ok, err := readEntry(zr, "Metadata/project_settings.config", maxMetaBytes); err != nil {
		return nil, err
	} else if ok {
		var all map[string]any
		if err := json.Unmarshal(b, &all); err != nil {
			return nil, fmt.Errorf("project_settings.config : %w", err)
		}
		ps.Settings = map[string]any{}
		for _, k := range settingKeys {
			if v, ok := all[k]; ok {
				ps.Settings[k] = v
			}
		}
		ps.Settings["_total_keys"] = len(all)
	}

	if b, ok, _ := readEntry(zr, "Metadata/slice_info.config", maxMetaBytes); ok {
		s := string(b)
		ps.Sliced = strings.Contains(s, "<plate>") || strings.Contains(s, "<plate ")
		if m := regexp.MustCompile(`key="X-BBL-Client-Version" value="([^"]*)"`).FindStringSubmatch(s); m != nil {
			ps.ClientVer = m[1]
		}
	}
	return ps, nil
}

// CopyForRead copie un fichier vers dst (copie de travail : Studio garde l'original ouvert).
func CopyForRead(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}
