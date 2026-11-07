package mesh

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// LayerRange est une plage de hauteurs (Z, mm) avec ses propres réglages
// (Metadata/layer_config_ranges.xml de Bambu Studio).
type LayerRange struct {
	MinZ, MaxZ float64
	Settings   map[string]string
}

// ProjectObject est un objet d'un projet Bambu Studio multi-objets.
type ProjectObject struct {
	Name     string
	Mesh     *Mesh             // posé sur Z=0, coordonnées locales
	Plate    int               // plaque (1..n)
	X, Y     float64           // position du coin minimal de la boîte englobante sur la plaque (mm)
	Settings map[string]string // réglages PAR OBJET (clés de configuration Studio : wall_loops, sparse_infill_density…)
	Ranges   []LayerRange
}

func xmlEsc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return strings.ReplaceAll(b.String(), "\"", "&quot;")
}

// WriteBambuProject écrit un 3MF multi-objets lisible par Bambu Studio : un
// <object> par pièce, un <item> de build posé à sa position, la répartition
// par plaque et les réglages par objet dans Metadata/model_settings.config.
// plateOrigin(n) donne le décalage monde de la plaque n (Studio range ses
// plaques côte à côte).
func WriteBambuProject(objs []ProjectObject, plateOrigin func(plate int) (x, y float64)) ([]byte, error) {
	if len(objs) == 0 {
		return nil, fmt.Errorf("%w : aucun objet", ErrEmpty)
	}
	var model, cfg bytes.Buffer
	model.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	model.WriteString(`<model unit="millimeter" xml:lang="en-US" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02" xmlns:BambuStudio="http://schemas.bambulab.com/package/2021">` + "\n")
	model.WriteString(` <metadata name="Application">BambuStudio-02.08.02.61</metadata>` + "\n")
	model.WriteString(` <metadata name="BambuStudio:3mfVersion">1</metadata>` + "\n")
	model.WriteString(" <resources>\n")
	var build bytes.Buffer
	cfg.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n<config>\n")
	maxPlate := 1
	byPlate := map[int][]int{}
	for i, o := range objs {
		if o.Mesh == nil || len(o.Mesh.T) == 0 {
			return nil, fmt.Errorf("%w : objet %q", ErrEmpty, o.Name)
		}
		if err := o.Mesh.Validate(); err != nil {
			return nil, fmt.Errorf("objet %q : %w", o.Name, err)
		}
		if o.Plate < 1 {
			return nil, fmt.Errorf("objet %q : plaque %d invalide", o.Name, o.Plate)
		}
		if o.Plate > maxPlate {
			maxPlate = o.Plate
		}
		byPlate[o.Plate] = append(byPlate[o.Plate], i)
		id := i + 1
		fmt.Fprintf(&model, "  <object id=\"%d\" name=\"%s\" type=\"model\">\n   <mesh>\n    <vertices>\n", id, xmlEsc(o.Name))
		for _, v := range o.Mesh.V {
			fmt.Fprintf(&model, "     <vertex x=\"%s\" y=\"%s\" z=\"%s\"/>\n", ff(v.X), ff(v.Y), ff(v.Z))
		}
		model.WriteString("    </vertices>\n    <triangles>\n")
		for _, t := range o.Mesh.T {
			fmt.Fprintf(&model, "     <triangle v1=\"%d\" v2=\"%d\" v3=\"%d\"/>\n", t[0], t[1], t[2])
		}
		model.WriteString("    </triangles>\n   </mesh>\n  </object>\n")
		b := o.Mesh.Bounds()
		ox, oy := plateOrigin(o.Plate)
		tx, ty := ox+o.X-b.Min.X, oy+o.Y-b.Min.Y
		fmt.Fprintf(&build, "  <item objectid=\"%d\" transform=\"1 0 0 0 1 0 0 0 1 %s %s %s\" printable=\"1\"/>\n", id, ff(tx), ff(ty), ff(-b.Min.Z))

		fmt.Fprintf(&cfg, " <object id=\"%d\">\n  <metadata key=\"name\" value=\"%s\"/>\n", id, xmlEsc(o.Name))
		keys := make([]string, 0, len(o.Settings))
		for k := range o.Settings {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&cfg, "  <metadata key=\"%s\" value=\"%s\"/>\n", xmlEsc(k), xmlEsc(o.Settings[k]))
		}
		fmt.Fprintf(&cfg, "  <part id=\"1\" subtype=\"normal_part\">\n   <metadata key=\"name\" value=\"%s\"/>\n   <metadata key=\"matrix\" value=\"1 0 0 0 0 1 0 0 0 0 1 0 0 0 0 1\"/>\n   <metadata key=\"source_file\" value=\"%s.stl\"/>\n   <mesh_stat face_count=\"%d\" edges_fixed=\"0\" degenerate_facets=\"0\" facets_removed=\"0\" facets_reversed=\"0\" backwards_edges=\"0\"/>\n  </part>\n </object>\n",
			xmlEsc(o.Name), xmlEsc(o.Name), len(o.Mesh.T))
	}
	model.WriteString(" </resources>\n <build>\n")
	model.Write(build.Bytes())
	model.WriteString(" </build>\n</model>\n")
	for p := 1; p <= maxPlate; p++ {
		fmt.Fprintf(&cfg, " <plate>\n  <metadata key=\"plater_id\" value=\"%d\"/>\n  <metadata key=\"plater_name\" value=\"\"/>\n  <metadata key=\"locked\" value=\"false\"/>\n", p)
		for _, i := range byPlate[p] {
			fmt.Fprintf(&cfg, "  <model_instance>\n   <metadata key=\"object_id\" value=\"%d\"/>\n   <metadata key=\"instance_id\" value=\"0\"/>\n   <metadata key=\"identify_id\" value=\"%d\"/>\n  </model_instance>\n", i+1, 100+i)
		}
		cfg.WriteString(" </plate>\n")
	}
	cfg.WriteString("</config>\n")

	var ranges bytes.Buffer
	hasRanges := false
	ranges.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n<objects>\n")
	for i, o := range objs {
		if len(o.Ranges) == 0 {
			continue
		}
		hasRanges = true
		fmt.Fprintf(&ranges, " <object id=\"%d\">\n", i+1)
		for _, r := range o.Ranges {
			fmt.Fprintf(&ranges, "  <range min_z=\"%s\" max_z=\"%s\">\n", ff(r.MinZ), ff(r.MaxZ))
			keys := make([]string, 0, len(r.Settings))
			for k := range r.Settings {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(&ranges, "   <option opt_key=\"%s\">%s</option>\n", xmlEsc(k), xmlEsc(r.Settings[k]))
			}
			ranges.WriteString("  </range>\n")
		}
		ranges.WriteString(" </object>\n")
	}
	ranges.WriteString("</objects>\n")

	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	add := func(name string, data []byte) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	ct := `<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="model" ContentType="application/vnd.ms-package.3dmanufacturing-3dmodel+xml"/><Default Extension="config" ContentType="text/xml"/></Types>`
	rels := `<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Target="/3D/3dmodel.model" Id="rel0" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/></Relationships>`
	for _, e := range []struct {
		n string
		d []byte
	}{{"[Content_Types].xml", []byte(ct)}, {"_rels/.rels", []byte(rels)}, {"3D/3dmodel.model", model.Bytes()}, {"Metadata/model_settings.config", cfg.Bytes()}} {
		if err := add(e.n, e.d); err != nil {
			return nil, err
		}
	}
	if hasRanges {
		if err := add("Metadata/layer_config_ranges.xml", ranges.Bytes()); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func ff(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// AddZipEntry ajoute (ou remplace) une entrée d'une archive ZIP en mémoire.
func AddZipEntry(archive []byte, name string, data []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		if f.Name == name {
			continue
		}
		w, err := zw.Create(f.Name)
		if err != nil {
			return nil, err
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		_, err = io.Copy(w, rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
	}
	w, err := zw.Create(name)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
