"""Commande `calibration_set` : pièces de calibration (jeux, trous/arbres, retrait, ponts/surplombs, température).

Chaque pièce est un modèle STL/STEP que l'utilisateur imprime avec SA machine, SA buse et SA bobine, puis mesure
(pied à coulisse ou essai à la main). Les mesures sont ensuite saisies via `record_calibration` (côté Go) qui en tire
les corrections stockées par (imprimante, buse, matériau/bobine).
"""

from __future__ import annotations

import math
from typing import Any

from .cad import require_cadquery
from .errors import GeometryError
from .library import Template, _emit

KINDS = ("fit_comb", "hole_shaft", "shrink_cube", "bridge_overhang", "temperature")
DEFAULT_GAPS = (0.1, 0.2, 0.3, 0.4, 0.5, 0.6)
DEFAULT_DIAMETERS = (3.0, 4.0, 5.0, 6.0, 8.0, 10.0)
DEFAULT_SPANS = (10.0, 20.0, 30.0, 40.0, 50.0)
DEFAULT_ANGLES = (30.0, 40.0, 50.0, 60.0, 70.0)
DEFAULT_TEMPS = (190.0, 200.0, 210.0, 220.0, 230.0)
CUBE_MM = 50.0
SEGMENT_H = 10.0


def _box(cq: Any, x0: float, y0: float, z0: float, dx: float, dy: float, dz: float) -> Any:
    return cq.Workplane("XY").box(dx, dy, dz, centered=False).translate((x0, y0, z0))


def _cyl_z(cq: Any, x: float, y: float, r: float, z0: float, h: float) -> Any:
    return cq.Workplane("XY").center(x, y).circle(r).extrude(h).translate((0, 0, z0))


def _dots(cq: Any, x: float, y: float, z: float, n: int, spacing: float = 2.0, r: float = 0.6, h: float = 0.6) -> Any:
    """n petits plots sur la face supérieure (repère de rang)."""
    pts = [(x + (k - (n - 1) / 2) * spacing, y) for k in range(n)]
    return cq.Workplane("XY").pushPoints(pts).circle(r).extrude(h).translate((0, 0, z - 0.01))


def _numlist(req: dict[str, Any], key: str, default: tuple[float, ...], lo: float, hi: float, max_n: int) -> list[float]:
    v = req.get(key)
    if v is None:
        return list(default)
    if not isinstance(v, list) or not v or len(v) > max_n:
        raise GeometryError("invalid_params", f"{key} doit être une liste de 1 à {max_n} nombres.")
    out = []
    for x in v:
        if isinstance(x, bool) or not isinstance(x, (int, float)) or not math.isfinite(x) or x < lo or x > hi:
            raise GeometryError("invalid_params", f"{key} : valeurs attendues dans [{lo:g} ; {hi:g}].", {"value": x})
        out.append(float(x))
    return out


# --- pièces ------------------------------------------------------------------------------------


def fit_comb(cq: Any, gaps: list[float]) -> tuple[list[tuple[str, Any]], dict[str, Any]]:
    tw, th, depth, back = 10.0, 5.0, 8.0, 6.0
    sep = 5.0
    total = sep + sum(tw + g + sep for g in gaps)
    base = _box(cq, 0, 0, 0, total, back + depth, th)
    x = sep
    centers = []
    for i, g in enumerate(gaps):
        base = base.cut(_box(cq, x, back, -1, tw + g, depth + 1, th + 2))
        c = x + (tw + g) / 2
        centers.append(c)
        base = base.union(_dots(cq, c, back / 2, th, i + 1))
        x += tw + g + sep
    y0 = back + depth + 14
    tab = _box(cq, 0, y0, 0, tw, 14, th).union(_box(cq, -4, y0 + 14, 0, tw + 8, 6, th))
    info = {"gaps_mm": gaps, "tab_width_mm": tw, "notch_centers_x_mm": centers}
    return [("fit_base", base), ("fit_tab", tab)], info


def hole_shaft(cq: Any, dias: list[float]) -> tuple[list[tuple[str, Any]], dict[str, Any]]:
    pitch_pad = 7.0
    xs = []
    x = pitch_pad
    for d in dias:
        xs.append(x + d / 2)
        x += d + pitch_pad
    total = x
    pt = 5.0
    pv = _box(cq, 0, 0, 0, total, 16, pt)
    for cx, d in zip(xs, dias):
        pv = pv.cut(_cyl_z(cq, cx, 8, d / 2, -1, pt + 2))
    # plaque à trous horizontaux (axe Y) : bloc debout, trous traversant l'épaisseur 8 mm
    hy0 = 26.0
    ph = _box(cq, 0, hy0, 0, total, 8, 16)
    for cx, d in zip(xs, dias):
        ph = ph.cut(cq.Workplane("XZ").center(cx, 8).circle(d / 2).extrude(-12).translate((0, hy0 - 2, 0)))
    # arbres verticaux sur une barre
    sy0 = 50.0
    rack = _box(cq, 0, sy0, 0, total, 14, 3)
    for cx, d in zip(xs, dias):
        rack = rack.union(_cyl_z(cq, cx, sy0 + 7, d / 2, 2.9, 12))
    info = {"diameters_mm": dias, "hole_centers_x_mm": xs}
    return [("holes_vertical", pv), ("holes_horizontal", ph), ("shafts", rack)], info


def shrink_cube(cq: Any) -> tuple[list[tuple[str, Any]], dict[str, Any]]:
    s = CUBE_MM
    c = _box(cq, 0, 0, 0, s, s, s)
    c = c.cut(_box(cq, 10, 10, -1, 30, 30, s + 2))
    c = c.faces("<Z").edges().chamfer(0.8)
    c = c.cut(_box(cq, s - 5, -1, s - 5, 6, 6, 6))  # repère : coin X max / Y min / Z max
    return [("shrink_cube_50", c)], {"nominal_mm": s, "mark": "encoche au coin X max / Y min en haut"}


def bridge_overhang(cq: Any, spans: list[float], angles: list[float]) -> tuple[list[tuple[str, Any]], dict[str, Any]]:
    pw, ph, bt, depth = 6.0, 10.0, 2.0, 10.0
    parts = None
    body = None
    for j, s in enumerate(spans):
        y = j * 16.0
        row = (_box(cq, 0, y, 0, pw, depth, ph + bt)
               .union(_box(cq, pw + s, y, 0, pw, depth, ph + bt))
               .union(_box(cq, 0, y, ph, 2 * pw + s, depth, bt)))
        row = row.union(_dots(cq, pw / 2, y + depth / 2, ph + bt, j + 1, spacing=1.6, r=0.45, h=0.5))
        body = row if body is None else body.union(row)
    y0 = len(spans) * 16.0 + 6
    H = 10.0
    for k, a in enumerate(angles):
        y = y0 + k * 16.0
        reach = H * math.tan(math.radians(a))
        wedge = cq.Workplane("XZ").polyline([(0, 0), (8, 0), (8 + reach, H), (0, H)]).close().extrude(-depth).translate((0, y, 0))
        wedge = wedge.union(_dots(cq, 4, y + depth / 2, H, k + 1, spacing=1.6, r=0.45, h=0.5))
        body = body.union(wedge)
    info = {"spans_mm": spans, "overhang_angles_deg": angles, "overhang_height_mm": H,
            "note": "angle mesuré depuis la verticale ; rangées de 1 à n points = portée / angle croissants"}
    parts = [("bridge_overhang", body)]
    return parts, info


def temp_segments(cq: Any, temps: list[float], rank0: int = 0) -> tuple[Any, list[dict[str, Any]]]:
    """Tour : 2 colonnes reliées par un pont à chaque étage + bras en surplomb à 45° + repère de rang."""
    n = len(temps)
    col, gap = 8.0, 16.0
    h_total = n * SEGMENT_H
    depth = 8.0
    tower = _box(cq, 0, 0, 0, col, depth, h_total).union(_box(cq, col + gap, 0, 0, col, depth, h_total))
    segs = []
    for i, t in enumerate(temps):
        z0 = i * SEGMENT_H
        tower = tower.union(_box(cq, col - 0.01, 0, z0 + SEGMENT_H - 1.6, gap + 0.02, depth, 1.6))  # pont
        arm = cq.Workplane("XZ").polyline([(2 * col + gap - 0.01, z0 + 2.0), (2 * col + gap + 6, z0 + 2.0 + 6),
                                           (2 * col + gap - 0.01, z0 + 2.0 + 6)]).close().extrude(-depth)
        tower = tower.union(arm)
        for k in range(rank0 + i + 1):  # repère de rang : plots sur la face avant de la colonne gauche
            tower = tower.union(cq.Workplane("XZ").center(col / 2, z0 + 2.0 + k * 1.8).circle(0.55).extrude(0.6))
        segs.append({"index": i + 1, "z_from_mm": z0, "z_to_mm": z0 + SEGMENT_H, "nozzle_temperature_c": t})
    return tower, segs


# --- commande ------------------------------------------------------------------------------------


def run(req: dict[str, Any]) -> dict[str, Any]:
    kinds = req.get("kinds") or list(KINDS)
    if not isinstance(kinds, list) or not kinds or any(k not in KINDS for k in kinds) or len(set(kinds)) != len(kinds):
        raise GeometryError("invalid_params", "kinds doit lister des types distincts parmi " + ", ".join(KINDS) + ".",
                            {"kinds": kinds})
    out_dir = req.get("out_dir")
    if not isinstance(out_dir, str):
        raise GeometryError("invalid_params", "`out_dir` (chaîne) est requis.")
    prefix = req.get("name_prefix", "calib")
    nozzle = req.get("nozzle_diameter", 0.4)
    if isinstance(nozzle, bool) or not isinstance(nozzle, (int, float)) or not 0.1 <= nozzle <= 1.2:
        raise GeometryError("invalid_params", "nozzle_diameter doit être dans [0,1 ; 1,2] mm.")
    gaps = _numlist(req, "clearance_gaps", DEFAULT_GAPS, 0.02, 1.5, 10)
    dias = _numlist(req, "hole_diameters", DEFAULT_DIAMETERS, 1.0, 30.0, 8)
    spans = _numlist(req, "bridge_spans", DEFAULT_SPANS, 5.0, 100.0, 8)
    angles = _numlist(req, "overhang_angles", DEFAULT_ANGLES, 10.0, 80.0, 8)
    temps = _numlist(req, "temperatures", DEFAULT_TEMPS, 150.0, 320.0, 9)
    mode = req.get("temperature_mode", "series")
    if mode not in ("series", "tower"):
        raise GeometryError("invalid_params", "temperature_mode doit valoir 'series' ou 'tower'.")
    cq = require_cadquery("cad_backend_missing")
    pieces: list[dict[str, Any]] = []

    def emit(kind: str, name: str, parts: list[tuple[str, Any]], info: dict[str, Any], description: str,
             measure: list[str], extra: dict[str, Any] | None = None) -> None:
        ctx: dict[str, Any] = {"assumptions": [], "warnings": [], "derived": info}
        tpl = Template(id=f"calibration_{kind}", title=kind, category="calibration", description=description, params=[],
                       build=lambda *_: [], version="1.0.0")
        try:
            res = _emit(cq, tpl, {}, parts, ctx, out_dir, f"{prefix}_{name}", float(nozzle), False)
        except GeometryError:
            raise
        piece = {"kind": kind, "name": f"{prefix}_{name}", "description": description, "measure": measure,
                 "files": {"stl": res["files"]["stl"], "step": res["files"]["step"]}, "parts": res["parts"],
                 "bbox_mm": res["bbox_mm"], "info": info, "warnings": res["warnings"]}
        if extra:
            piece.update(extra)
        pieces.append(piece)

    try:
        for kind in kinds:
            if kind == "fit_comb":
                parts, info = fit_comb(cq, gaps)
                emit(kind, "jeux", parts, info,
                     "Base à encoches de largeur 10 mm + jeu croissant (1 à n points) et un languette de 10 mm. "
                     "Essayer la languette dans chaque encoche, de la plus serrée à la plus large.",
                     ["fit.slides_gap_mm : plus petit jeu où la languette coulisse librement à la main",
                      "fit.snug_gap_mm (facultatif) : plus petit jeu où elle entre avec un léger serrage"])
            elif kind == "hole_shaft":
                parts, info = hole_shaft(cq, dias)
                emit(kind, "trous_arbres", parts, info,
                     "Plaque à trous verticaux, bloc à trous horizontaux et barre d'arbres verticaux aux diamètres nominaux. "
                     "Mesurer chaque trou et chaque arbre au pied à coulisse (deux mesures croisées, moyenne).",
                     ["holes[] : {nominal, measured, axis: vertical|horizontal}", "shafts[] : {nominal, measured}"])
            elif kind == "shrink_cube":
                parts, info = shrink_cube(cq)
                emit(kind, "retrait", parts, info,
                     "Cube creux de 50 mm de référence. Mesurer les trois dimensions extérieures à mi-hauteur / mi-largeur "
                     "(pas au pied du cube : pied d'éléphant), pièce refroidie.",
                     ["cube_mm : [X, Y, Z] mesurés (nominal 50)"])
            elif kind == "bridge_overhang":
                parts, info = bridge_overhang(cq, spans, angles)
                emit(kind, "ponts_surplombs", parts, info,
                     "Ponts de portées croissantes (1 à n points) puis surplombs d'angles croissants. Repérer la dernière "
                     "portée sans affaissement et le dernier angle sans défaut (fils pendants, surface rugueuse).",
                     ["bridge_max_span_mm : plus grande portée propre", "overhang_max_deg : plus grand angle propre (depuis la verticale)"])
            elif kind == "temperature":
                if mode == "tower":
                    tower, segs = temp_segments(cq, temps)
                    changes = [{"layer_z_mm": s["z_from_mm"], "nozzle_temperature_c": s["nozzle_temperature_c"]} for s in segs]
                    emit(kind, "tour_temperature", [("temp_tower", tower)], {"segments": segs, "segment_height_mm": SEGMENT_H},
                         "Tour de température : un étage de 10 mm par température (du bas vers le haut = ordre de la liste). "
                         "Ajouter un changement de température de buse à chaque hauteur d'étage (G-code personnalisé à la couche "
                         "dans Bambu Studio : M104 S<température>). Comparer pont, surplomb et fils de chaque étage.",
                         ["best_temp_c : température de l'étage le plus propre (compromis pont/surplomb/fils/adhérence)"],
                         {"temperature_mode": "tower", "segments": segs, "layer_changes": changes,
                          "gcode_hint": [f"M104 S{int(t['nozzle_temperature_c'])} ; à la hauteur z={t['z_from_mm']:g} mm"
                                         for t in segs[1:]] or []})
                else:
                    for idx, t in enumerate(temps):
                        tower, segs = temp_segments(cq, [t], idx)
                        emit(kind, f"temp_{int(t)}", [(f"temp_{int(t)}", tower)], {"segments": segs, "segment_height_mm": SEGMENT_H},
                             f"Petite pièce (pont + surplomb) à imprimer à {t:g} °C de buse ; imprimer toute la série, "
                             "mêmes réglages sauf la température, puis comparer.",
                             ["best_temp_c : température de la pièce la plus propre"],
                             {"temperature_mode": "series", "nozzle_temperature_c": t,
                              "slicing_overrides": [{"key": "nozzle_temperature", "value": str(int(t)), "target": "filament"}]})
    except GeometryError:
        raise
    except Exception as e:
        raise GeometryError("cad_failure", "Échec de construction CadQuery.", {"cause": repr(e)}) from e
    return {"pieces": pieces, "count": len(pieces), "nozzle_diameter": float(nozzle), "level": "calculé"}
