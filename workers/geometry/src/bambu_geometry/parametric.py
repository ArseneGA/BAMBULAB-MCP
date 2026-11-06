"""Commande `parametric` : gabarits CadQuery -> STL + STEP + source JSON."""

from __future__ import annotations

import json
import math
from pathlib import Path
from typing import Any, Callable

from .cad import mesh_stats, require_cadquery, tessellate
from .errors import GeometryError
from .meshio import write_binary_stl

Part = tuple[str, Any]  # (nom, Workplane)


def _num(p: dict[str, Any], key: str, default: float | None = None, *, minimum: float = 0.0) -> float:
    v = p.get(key, default)
    if v is None:
        raise GeometryError("invalid_params", f"Paramètre requis manquant : {key}")
    if isinstance(v, bool) or not isinstance(v, (int, float)) or not math.isfinite(v):
        raise GeometryError("invalid_params", f"{key} doit être un nombre fini.")
    if v <= minimum:
        raise GeometryError("invalid_params", f"{key} doit être > {minimum}.", {"value": v})
    return float(v)


def _box(cq: Any, x0: float, y0: float, z0: float, dx: float, dy: float, dz: float) -> Any:
    return cq.Workplane("XY").box(dx, dy, dz, centered=False).translate((x0, y0, z0))


def _spread(n: int, lo: float, hi: float) -> list[float]:
    step = (hi - lo) / n
    return [lo + (i + 0.5) * step for i in range(n)]


# --- gabarits -----------------------------------------------------------------------------


def clamp(cq: Any, p: dict[str, Any], ctx: dict[str, Any]) -> list[Part]:
    d = _num(p, "tube_diameter")
    clr = _num(p, "clearance", 0.3, minimum=-1)
    wall = _num(p, "wall", 3.0)
    width = _num(p, "width", 15.0)
    hinge = p.get("hinge", "screw")
    if hinge not in ("screw", "none"):
        raise GeometryError("invalid_params", "hinge doit valoir 'screw' ou 'none'.")
    r_in = (d + clr) / 2
    r_out = r_in + wall
    body = cq.Workplane("XY").circle(r_out).circle(r_in).extrude(width)
    ctx["derived"].update(inner_diameter=2 * r_in, outer_diameter=2 * r_out)
    if hinge == "screw":
        hd = _num(p, "screw_hole_diameter", 3.4)
        gap = _num(p, "gap", 2.0)
        x_h = r_out + hd / 2 + wall * 0.75
        x_end = x_h + hd / 2 + wall
        ear_t = wall
        for sgn in (1, -1):
            y0 = gap / 2 if sgn == 1 else -gap / 2 - ear_t
            body = body.union(_box(cq, r_in + wall / 2, y0, 0, x_end - (r_in + wall / 2), ear_t, width))
        body = body.cut(_box(cq, 0, -gap / 2, -1, x_end + 1, gap, width + 2))
        hole = cq.Workplane("XZ").center(x_h, width / 2).circle(hd / 2).extrude(200, both=True)
        body = body.cut(hole)
        ctx["derived"].update(screw_axis_x=x_h, ear_length=x_end - r_out)
        if width < hd + 2 * wall:
            ctx["warnings"].append("Largeur < diamètre de vis + 2 parois : oreilles fragiles.")
        ctx["assumptions"].append(
            "Pince fendue + 2 oreilles percées ; la vis (perçage Ø screw_hole_diameter) serre la pince sur le tube."
        )
    else:
        opening = _num(p, "opening", 0.85 * d)
        body = body.cut(_box(cq, 0, -opening / 2, -1, r_out + 1, opening, width + 2))
        ctx["warnings"].append("Pince à clipser (C) : l'élasticité n'est pas vérifiée ; la valider avec engineering/snap_fit.")
        ctx["assumptions"].append("Ouverture du C = opening (défaut 0,85 × diamètre du tube), côté +X.")
    ctx["assumptions"].append("Axe du tube = Z ; pièce à imprimer couchée ou debout selon l'effort (anisotropie).")
    return [("clamp", body)]


def bracket(cq: Any, p: dict[str, Any], ctx: dict[str, Any]) -> list[Part]:
    la = _num(p, "leg_a")
    lb = _num(p, "leg_b")
    t = _num(p, "thickness", 4.0)
    w = _num(p, "width", 20.0)
    hd = _num(p, "hole_diameter", 4.5)
    na = int(p.get("holes_a", 1))
    nb = int(p.get("holes_b", 1))
    if na < 0 or nb < 0:
        raise GeometryError("invalid_params", "holes_a/holes_b doivent être >= 0.")
    if la <= t or lb <= t:
        raise GeometryError("invalid_params", "Chaque branche doit être plus longue que l'épaisseur.")
    prof = [(0, 0), (la, 0), (la, t), (t, t), (t, lb), (0, lb)]
    body = cq.Workplane("XZ").polyline(prof).close().extrude(-w)
    if na:
        lo, hi = t + hd, la - hd
        if hi <= lo or (hi - lo) / na < 2.5 * hd:
            ctx["warnings"].append("Trous de la branche A trop serrés ou trop près des bords (< 1,25 Ø).")
        xs = _spread(na, lo, hi) if hi > lo else [la / 2]
        body = body.cut(cq.Workplane("XY").pushPoints([(x, w / 2) for x in xs]).circle(hd / 2).extrude(t))
    if nb:
        lo, hi = t + hd, lb - hd
        if hi <= lo or (hi - lo) / nb < 2.5 * hd:
            ctx["warnings"].append("Trous de la branche B trop serrés ou trop près des bords (< 1,25 Ø).")
        zs = _spread(nb, lo, hi) if hi > lo else [lb / 2]
        body = body.cut(cq.Workplane("YZ").pushPoints([(w / 2, z) for z in zs]).circle(hd / 2).extrude(t))
    if w < 2.5 * hd and (na or nb):
        ctx["warnings"].append("Largeur < 2,5 Ø de perçage : risque de rupture au perçage.")
    ctx["assumptions"].append(
        "Équerre en L, branche A selon X (plan XY), branche B selon Z ; sans congé : l'angle intérieur concentre "
        "les contraintes, imprimer avec les couches parallèles au pli."
    )
    return [("bracket", body)]


def box(cq: Any, p: dict[str, Any], ctx: dict[str, Any]) -> list[Part]:
    ix, iy, iz = _num(p, "inner_x"), _num(p, "inner_y"), _num(p, "inner_z")
    wall = _num(p, "wall", 2.0)
    floor = _num(p, "floor", wall)
    lid_t = _num(p, "lid_thickness", 2.0)
    clr = _num(p, "lid_clearance", 0.3)
    lip_h = _num(p, "lip_height", 3.0)
    ox, oy = ix + 2 * wall, iy + 2 * wall
    body = _box(cq, 0, 0, 0, ox, oy, floor + iz).cut(_box(cq, wall, wall, floor, ix, iy, iz + 1))
    lid = _box(cq, ox + 10, 0, 0, ox, oy, lid_t).union(
        _box(cq, ox + 10 + wall + clr, wall + clr, lid_t, ix - 2 * clr, iy - 2 * clr, lip_h)
    )
    if ix - 2 * clr <= 0 or iy - 2 * clr <= 0:
        raise GeometryError("invalid_params", "lid_clearance trop grand pour les dimensions intérieures.")
    ctx["derived"].update(outer=[ox, oy, floor + iz], lid_offset_x=ox + 10)
    ctx["assumptions"].append(
        "Couvercle posé à plat à côté du boîtier (lèvre vers le haut, sans support) ; jeu radial = lid_clearance "
        "par côté, à ajuster via engineering/fit_clearance."
    )
    return [("box_body", body), ("box_lid", lid)]


def spacer(cq: Any, p: dict[str, Any], ctx: dict[str, Any]) -> list[Part]:
    od, idia, h = _num(p, "outer_diameter"), _num(p, "inner_diameter", 0.0001), _num(p, "height")
    if "inner_diameter" in p and idia >= od:
        raise GeometryError("invalid_params", "inner_diameter doit être < outer_diameter.")
    body = cq.Workplane("XY").circle(od / 2)
    body = (body.circle(idia / 2) if "inner_diameter" in p else body).extrude(h)
    ctx["assumptions"].append("Entretoise cylindrique d'axe Z, imprimée debout : compression OK, flexion faible en Z.")
    return [("spacer", body)]


def hook(cq: Any, p: dict[str, Any], ctx: dict[str, Any]) -> list[Part]:
    w = _num(p, "width", 20.0)
    ph = _num(p, "plate_height", 40.0)
    t = _num(p, "thickness", 4.0)
    arm = _num(p, "arm_length", 30.0)
    tip = _num(p, "tip_height", 12.0)
    hd = _num(p, "hole_diameter", 4.5)
    nh = int(p.get("holes", 2))
    if arm <= t or ph <= t or tip <= t:
        raise GeometryError("invalid_params", "arm_length, plate_height, tip_height doivent dépasser thickness.")
    prof = [(0, 0), (arm, 0), (arm, tip), (arm - t, tip), (arm - t, t), (t, t), (t, ph), (0, ph)]
    body = cq.Workplane("XZ").polyline(prof).close().extrude(-w)
    if nh:
        zs = _spread(nh, t + hd, ph - hd)
        body = body.cut(cq.Workplane("YZ").pushPoints([(w / 2, z) for z in zs]).circle(hd / 2).extrude(t))
        if w < 2.5 * hd:
            ctx["warnings"].append("Largeur < 2,5 Ø de perçage.")
    ctx["assumptions"].append(
        "Crochet mural en J : plaque verticale (plan YZ) vissée, bras selon +X, bec de hauteur tip_height ; "
        "charge sur le bras = flexion (voir engineering/beam_stress)."
    )
    return [("hook", body)]


TEMPLATES: dict[str, Callable[[Any, dict[str, Any], dict[str, Any]], list[Part]]] = {
    "clamp": clamp,
    "bracket": bracket,
    "box": box,
    "spacer": spacer,
    "hook": hook,
}


def run(req: dict[str, Any]) -> dict[str, Any]:
    tpl = req.get("template")
    params = req.get("params", {})
    out_dir = req.get("out_dir")
    if tpl not in TEMPLATES:
        raise GeometryError("invalid_params", f"template inconnu : {tpl!r}", {"available": sorted(TEMPLATES)})
    if not isinstance(params, dict) or not isinstance(out_dir, str):
        raise GeometryError("invalid_params", "`params` (objet) et `out_dir` (chaîne) sont requis.")
    nozzle = _num(req, "nozzle_diameter", 0.4)
    cq = require_cadquery("cad_backend_missing")
    ctx: dict[str, Any] = {"assumptions": [], "warnings": [], "derived": {}}
    try:
        parts = TEMPLATES[tpl](cq, params, ctx)
    except GeometryError:
        raise
    except Exception as e:
        raise GeometryError("cad_failure", "Échec de construction CadQuery.", {"cause": repr(e)}) from e
    for key in ("wall", "thickness"):
        if key in params and isinstance(params[key], (int, float)) and params[key] < 2 * nozzle:
            ctx["warnings"].append(f"{key} < 2 × buse ({2 * nozzle:g} mm) : moins de 2 périmètres.")

    name = str(req.get("name") or tpl)
    out = Path(out_dir)
    out.mkdir(parents=True, exist_ok=True)
    compound = cq.Compound.makeCompound([wp.val() for _, wp in parts])
    meshes = [tessellate(wp.val(), 0.02, 0.1) for _, wp in parts]
    stl_path, step_path, json_path = out / f"{name}.stl", out / f"{name}.step", out / f"{name}.source.json"
    write_binary_stl(stl_path, meshes, name=name)
    cq.exporters.export(cq.Workplane(obj=compound), str(step_path))
    bb = compound.BoundingBox()
    info_parts = [
        {
            "name": n,
            "volume_mm3": float(wp.val().Volume()),
            "solids": len(wp.solids().vals()),
            "tessellation": mesh_stats(m),
        }
        for (n, wp), m in zip(parts, meshes)
    ]
    result = {
        "template": tpl,
        "params": params,
        "parts": info_parts,
        "bbox_mm": {"size": [bb.xlen, bb.ylen, bb.zlen], "min": [bb.xmin, bb.ymin, bb.zmin]},
        "derived": ctx["derived"],
        "assumptions": ctx["assumptions"],
        "warnings": ctx["warnings"],
        "files": {"stl": str(stl_path), "step": str(step_path), "source": str(json_path)},
        "level": "calculé",
        "validity": "Géométrie exacte pour les paramètres donnés ; aucune vérification mécanique (voir engineering).",
    }
    json_path.write_text(
        json.dumps({"schema": 1, "template": tpl, "params": params, "nozzle_diameter": nozzle,
                    "assumptions": ctx["assumptions"]}, indent=2, ensure_ascii=False),
        encoding="utf-8",
    )
    return result
