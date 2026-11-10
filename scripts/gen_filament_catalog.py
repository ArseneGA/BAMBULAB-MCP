#!/usr/bin/env python3
"""Régénère internal/filament/catalog/data/catalog.json depuis les profils BBL de Bambu Studio installés.

Usage : python scripts/gen_filament_catalog.py [dossier resources/profiles/BBL] [fichier de sortie]
Sources lues (hors ligne, faits seulement) : filament/filaments_color_codes.json, filament/Bambu *.json (héritage
résolu), resources/info/nozzle_incompatibles.json, BBL.json (version). Aucune valeur n'est inventée : champ absent =
non disponible.
"""
import json, os, sys, glob, datetime

root = sys.argv[1] if len(sys.argv) > 1 else r"C:\Program Files\Bambu Studio\resources\profiles\BBL"
out = sys.argv[2] if len(sys.argv) > 2 else os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "internal", "filament", "catalog", "data", "catalog.json")
fdir = os.path.join(root, "filament")
res = os.path.abspath(os.path.join(root, "..", ".."))
load = lambda p: json.load(open(p, encoding="utf-8"))

presets = {}
for p in glob.glob(os.path.join(fdir, "*.json")):
    try:
        d = load(p)
    except Exception:
        continue
    if isinstance(d, dict) and d.get("type") == "filament":
        presets[d["name"]] = d


def resolve(name, seen=()):
    d = presets[name]
    base = {}
    if d.get("inherits") and d["inherits"] in presets and d["inherits"] not in seen:
        base = resolve(d["inherits"], seen + (name,))
    r = dict(base)
    r.update(d)
    return r


def first(r, k, cast=float):
    v = r.get(k)
    if isinstance(v, list) and v:
        v = v[0]
    try:
        return cast(v)
    except Exception:
        return None


colors_raw = load(os.path.join(fdir, "filaments_color_codes.json"))["data"]
noz = None
nz = os.path.join(res, "info", "nozzle_incompatibles.json")
if os.path.exists(nz):
    noz = load(nz)
version = load(os.path.join(root, "..", "BBL.json")).get("version", "") if os.path.exists(os.path.join(root, "..", "BBL.json")) else ""
if not version and os.path.exists(os.path.join(res, "profiles", "BBL.json")):
    version = load(os.path.join(res, "profiles", "BBL.json")).get("version", "")
stamp = datetime.date.fromtimestamp(os.path.getmtime(os.path.join(fdir, "filaments_color_codes.json"))).isoformat()
today = "2026-11-10"

base_by_id = {}
for n, d in presets.items():
    if n.startswith("Bambu ") and n.endswith("@base") and d.get("filament_id"):
        base_by_id[d["filament_id"]] = n

FINISHES = [("silk", "silk", "soie"), ("matte", "matte", "mat"), ("translucent", "translucent", "translucide"),
            ("metal", "metal", "métal"), ("sparkle", "sparkle", "paillettes"), ("marble", "marble", "marbre"),
            ("glow", "glow", "phosphorescent"), ("galaxy", "galaxy", "galaxie"), ("wood", "wood", "bois"),
            ("aero", "aero", "allégé (mousse)"), ("-cf", "carbon", "fibre de carbone"), ("-gf", "glass", "fibre de verre"),
            ("dynamic", "dynamic", "dynamique")]


def finish_of(name):
    l = name.lower()
    for key, code, fr in FINISHES:
        if key in l:
            return code, fr
    return "standard", "standard"


def klass(t):
    u = t.upper()
    if "SUPPORT" in u:
        return "SUPPORT"
    for c in ("PETG", "PET", "PPA", "PPS", "PAHT", "PA", "PC", "ABS", "ASA", "TPU", "PVA", "PLA"):
        if u.startswith(c):
            return {"PAHT": "PA", "PPA": "PA"}.get(c, c)
    return "AUTRE"


incomp = {}
if noz:
    for ntype, byd in noz["incompatible_nozzles"].items():
        for dia, names in byd.items():
            for n in names:
                incomp.setdefault(n, {}).setdefault(ntype, []).append(dia)

fams = {}
for c in colors_raw:
    fams.setdefault(c["fila_id"], c["fila_type"])
families = []
for fid, ftype in sorted(fams.items()):
    pn = base_by_id.get(fid)
    f = {"id": fid, "name": ftype}
    if pn:
        r = resolve(pn)
        line = pn[:-len(" @base")]
        f["preset_line"] = line
        t = (r.get("filament_type") or [""])[0]
        f["type"] = t
        f["class"] = klass(t)
        f["vendor"] = (r.get("filament_vendor") or ["Bambu Lab"])[0]
        code, fr = finish_of(line)
        f["finish"], f["finish_fr"] = code, fr
        f["translucent"] = "translucent" in line.lower()
        f["support_material"] = first(r, "filament_is_support", int) == 1 or f["class"] == "SUPPORT"
        f["soluble"] = first(r, "filament_soluble", int) == 1
        if f["support_material"]:
            f["class"] = "SUPPORT"
        f["nozzle_temp_c"] = first(r, "nozzle_temperature", int)
        f["nozzle_temp_first_layer_c"] = first(r, "nozzle_temperature_initial_layer", int)
        bed = {}
        for k, a in (("cool_plate", "cool_plate_temp"), ("engineering_plate", "eng_plate_temp"),
                     ("high_temp_plate", "hot_plate_temp"), ("textured_pei", "textured_plate_temp")):
            v = first(r, a, int)
            if v:  # 0 = plaque non recommandée par le profil : omise
                bed[k] = v
        f["bed_temp_c"] = bed
        f["fan_min_pct"] = first(r, "fan_min_speed", int)
        f["fan_max_pct"] = first(r, "fan_max_speed", int)
        f["density_g_cm3"] = first(r, "filament_density")
        f["diameter_mm"] = 2.85 if "2.85" in line else first(r, "filament_diameter")
        f["max_volumetric_speed_mm3_s"] = first(r, "filament_max_volumetric_speed")
        f["drying_bed_temp_c"] = first(r, "filament_dev_chamber_drying_bed_temperature", int)
        hrc = first(r, "required_nozzle_HRC", int)
        f["required_nozzle_hrc"] = hrc
        f["hardened_nozzle_required"] = bool(hrc is not None and hrc >= 40)
        f["incompatible_nozzles"] = incomp.get(line, {})
        f["confidence"] = "official_profile"
        f["source"] = {"kind": "official_profile", "ref": "Bambu Studio profiles/BBL/filament/" + pn + ".json (+ héritage fdm_filament_*), info/nozzle_incompatibles.json", "profile_version": version, "retrieved": today}
    else:
        f["class"] = klass(ftype)
        f["finish"], f["finish_fr"] = finish_of(ftype)
        f["confidence"] = "estimated"
        f["source"] = {"kind": "estimated", "ref": "famille présente dans filaments_color_codes.json sans preset de base", "profile_version": version, "retrieved": today}
    f = {k: v for k, v in f.items() if v is not None and v != {}}
    families.append(f)

KIND = {"单色": "single", "渐变色": "gradient", "多拼色": "multicolor"}
colors = []
for c in colors_raw:
    names = {k: v for k, v in c["fila_color_name"].items() if k in ("fr", "en", "de", "es", "it") and v.strip()}
    hexes = [h[:7].upper() for h in c["fila_color"]]
    colors.append({"id": c["fila_id"] + "-" + c["fila_color_code"], "family_id": c["fila_id"], "color_code": c["fila_color_code"],
                   "tray_color_code": c["color_code"], "name": names, "hex": hexes, "kind": KIND.get(c["fila_color_type"], "single"),
                   "confidence": "official_profile"})
colors.sort(key=lambda c: c["id"])

doc = {"schema": 1, "generated": today,
       "sources": [
           {"kind": "official_profile", "ref": "Bambu Studio resources/profiles/BBL (version %s) : filament/filaments_color_codes.json (fichier du %s), filament/Bambu *.json, info/nozzle_incompatibles.json" % (version, stamp), "retrieved": today,
            "note": "profils installés avec Bambu Studio, lus hors ligne ; couleurs, noms FR/EN, températures, densité, diamètre, dureté de buse requise"},
           {"kind": "official_site", "ref": "https://wiki.bambulab.com/en/general/bambu-filament-drying-cover", "retrieved": today,
            "note": "plages de séchage sur plateau chauffant, 12 h, rapportées par une recherche web (page non téléchargeable en direct : HTTP 402) ; valeur indicative"}],
       "drying_heatbed": {"source_kind": "official_site", "source": "https://wiki.bambulab.com/en/general/bambu-filament-drying-cover", "retrieved": today, "hours": 12,
                          "ranges_c": {"PLA": [65, 75], "PETG": [75, 85], "ABS": [90, 100], "ASA": [90, 100], "TPU": [80, 90]}},
       "families": families, "colors": colors}
os.makedirs(os.path.dirname(os.path.abspath(out)), exist_ok=True)
json.dump(doc, open(out, "w", encoding="utf-8", newline="\n"), ensure_ascii=False, indent=1)
print(len(families), "familles", len(colors), "couleurs ->", out)
