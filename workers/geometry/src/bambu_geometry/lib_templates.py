"""Gabarits de la bibliothèque (enregistrés dans library.REGISTRY à l'import).

Repère commun : mm, Z vers le haut, pièce à plat sur le plateau (Z = 0) sauf mention contraire dans les hypothèses.
"""

from __future__ import annotations

import math
from typing import Any

from . import parametric as legacy
from .errors import GeometryError
from .gears import ROOT_FILLET, gear_dims, gear_solid, gear_warnings, interference_check, space_side
from .library import P, Template, register
from .threads import (ACROSS_FLATS, COARSE_PITCH, SIZES, bolt_tip_chamfer, hex_prism, nominal, nut_entry_chamfer,
                      thread_depth, thread_tessellation, threaded_rod)

Part = tuple[str, Any]


def _box(cq: Any, x0: float, y0: float, z0: float, dx: float, dy: float, dz: float) -> Any:
    return cq.Workplane("XY").box(dx, dy, dz, centered=False).translate((x0, y0, z0))


def _cyl_x(cq: Any, y: float, z: float, r: float, x0: float, length: float) -> Any:
    """Cylindre d'axe X, centre (y, z)."""
    return cq.Workplane("YZ").center(y, z).circle(r).extrude(length).translate((x0, 0, 0))


def _teardrop_x(cq: Any, y: float, z: float, r: float, x0: float, length: float, down: bool = True, up: bool = False,
                flat: float = 1.0) -> Any:
    """Prisme d'axe X de section « goutte » : cercle de rayon r (centre (y, z)) prolongé par une ou deux pointes à 45°.

    Les pointes (vers le bas et/ou le haut) sont tangentes au cercle et tronquées par un plat horizontal de largeur `flat`
    (pont court imprimable) : toutes les parois restent à <= 45° de la verticale, donc sans support. down=True suffit pour un axe
    plein (son dessous est en surplomb) ; un perçage a son plafond en surplomb : up=True (et down=True s'il loge un axe-goutte).
    """
    c = r * math.cos(math.radians(45.0))
    ht = r * math.sqrt(2.0) - flat / 2  # hauteur du plat de troncature par rapport au centre
    if not (down or up):
        return _cyl_x(cq, y, z, r, x0, length)
    # contour parcouru de la droite vers la gauche par le haut : (dessous plat) -> flanc bas droit -> arc droit -> (dessus plat)
    w = cq.Workplane("YZ")
    if down:
        w = w.moveTo(y + flat / 2, z - ht).lineTo(y + c, z - c)
    else:
        w = w.moveTo(y + c, z - c)
    if up:
        w = w.threePointArc((y + r, z), (y + c, z + c)).lineTo(y + flat / 2, z + ht).lineTo(y - flat / 2, z + ht).lineTo(y - c, z + c)
        w = w.threePointArc((y - r, z), (y - c, z - c))
    else:
        w = w.threePointArc((y, z + r), (y - c, z - c))  # arc par le haut : de (c, -c) à (-c, -c)
    if down:
        w = w.lineTo(y - flat / 2, z - ht)
    return w.close().extrude(length).translate((x0, 0, 0))


def _cyl_z(cq: Any, x: float, y: float, r: float, z0: float, height: float) -> Any:
    return cq.Workplane("XY").center(x, y).circle(r).extrude(height).translate((0, 0, z0))


def _fail(msg: str, **details: Any) -> None:
    raise GeometryError("invalid_params", msg, details)


# =========================================================================================
# Gabarits historiques (enveloppes du module parametric)
# =========================================================================================


def _legacy(fn: Any, drop_zero: tuple[str, ...] = ()) -> Any:
    def build(cq: Any, p: dict[str, Any], ctx: dict[str, Any]) -> list[Part]:
        q = {k: v for k, v in p.items() if not (k in drop_zero and v == 0)}
        return fn(cq, q, ctx)

    return build


register(Template(
    id="clamp", title="Collier de serrage", category="fixation",
    description="Collier fendu à oreilles percées (vis de serrage) ou pince à clipser pour un tube. Axe du tube = Z.",
    tags=["tube", "serrage", "fixation"],
    params=[
        P("tube_diameter", "number", 25.0, "Diamètre extérieur du tube à serrer.", "mm", 3, 200),
        P("clearance", "number", 0.3, "Jeu diamétral ajouté au diamètre du tube.", "mm", -1, 3, auto="fit:glissant"),
        P("wall", "number", 3.0, "Épaisseur de paroi du collier.", "mm", 0.8, 20),
        P("width", "number", 15.0, "Largeur du collier (le long du tube).", "mm", 3, 200),
        P("hinge", "enum", "screw", "screw = oreilles + vis ; none = pince en C à clipser.", options=("screw", "none")),
        P("screw_hole_diameter", "number", 3.4, "Diamètre du perçage de la vis de serrage.", "mm", 1.5, 12, auto="hole:horizontal"),
        P("gap", "number", 2.0, "Fente entre les oreilles.", "mm", 0.5, 10),
        P("opening", "number", 0.0, "Ouverture du C (mode none) ; 0 = 0,85 × diamètre du tube.", "mm", 0, 200),
    ],
    build=_legacy(legacy.clamp, ("opening",)),
))

register(Template(
    id="bracket", title="Équerre", category="fixation",
    description="Équerre en L à deux branches percées.",
    tags=["équerre", "fixation", "vis"],
    params=[
        P("leg_a", "number", 40.0, "Longueur de la branche A (selon X).", "mm", 5, 300),
        P("leg_b", "number", 30.0, "Longueur de la branche B (selon Z).", "mm", 5, 300),
        P("thickness", "number", 4.0, "Épaisseur.", "mm", 0.8, 30),
        P("width", "number", 20.0, "Largeur.", "mm", 3, 300),
        P("hole_diameter", "number", 4.5, "Diamètre des trous de vis.", "mm", 1.5, 20, auto="hole:vertical"),
        P("holes_a", "integer", 1, "Nombre de trous sur la branche A.", "", 0, 50),
        P("holes_b", "integer", 1, "Nombre de trous sur la branche B.", "", 0, 50),
    ],
    build=_legacy(legacy.bracket),
))

register(Template(
    id="box", title="Boîte à couvercle", category="boîtier",
    description="Boîte rectangulaire avec couvercle à lèvre posé à côté (impression à plat, sans support).",
    tags=["boîte", "couvercle"],
    params=[
        P("inner_x", "number", 60.0, "Dimension intérieure X.", "mm", 5, 300),
        P("inner_y", "number", 40.0, "Dimension intérieure Y.", "mm", 5, 300),
        P("inner_z", "number", 25.0, "Hauteur intérieure.", "mm", 3, 300),
        P("wall", "number", 2.0, "Épaisseur des parois.", "mm", 0.8, 20),
        P("floor", "number", 2.0, "Épaisseur du fond.", "mm", 0.8, 20),
        P("lid_thickness", "number", 2.0, "Épaisseur du couvercle.", "mm", 0.8, 20),
        P("lid_clearance", "number", 0.3, "Jeu radial de la lèvre, par côté.", "mm", 0.05, 2, auto="fitside:libre"),
        P("lip_height", "number", 3.0, "Hauteur de la lèvre du couvercle.", "mm", 1, 30),
    ],
    build=_legacy(legacy.box),
))

register(Template(
    id="spacer", title="Entretoise", category="rondelle-entretoise",
    description="Entretoise cylindrique (axe Z), pleine ou percée.",
    tags=["entretoise", "cylindre"],
    params=[
        P("outer_diameter", "number", 20.0, "Diamètre extérieur.", "mm", 2, 300),
        P("inner_diameter", "number", 8.4, "Diamètre du perçage ; 0 = pleine.", "mm", 0, 299, auto="hole:vertical"),
        P("height", "number", 10.0, "Hauteur.", "mm", 0.5, 300),
    ],
    build=_legacy(legacy.spacer, ("inner_diameter",)),
))

register(Template(
    id="hook", title="Crochet mural", category="support",
    description="Crochet en J : plaque verticale à visser et bras avec bec.",
    tags=["crochet", "mural"],
    params=[
        P("width", "number", 20.0, "Largeur.", "mm", 3, 200),
        P("plate_height", "number", 40.0, "Hauteur de la plaque.", "mm", 5, 300),
        P("thickness", "number", 4.0, "Épaisseur.", "mm", 0.8, 30),
        P("arm_length", "number", 30.0, "Longueur du bras.", "mm", 6, 300),
        P("tip_height", "number", 12.0, "Hauteur du bec.", "mm", 3, 100),
        P("hole_diameter", "number", 4.5, "Diamètre des trous de vis.", "mm", 1.5, 20, auto="hole:horizontal"),
        P("holes", "integer", 2, "Nombre de trous.", "", 0, 50),
    ],
    build=_legacy(legacy.hook),
))


# =========================================================================================
# Rondelle
# =========================================================================================


def washer(cq: Any, p: dict[str, Any], ctx: dict[str, Any]) -> list[Part]:
    od, idia, t, ch = p["outer_diameter"], p["inner_diameter"], p["thickness"], p["chamfer"]
    if idia >= od:
        _fail("inner_diameter doit être < outer_diameter.")
    if ch * 2 >= (od - idia) / 2 or ch >= t:
        _fail("chamfer trop grand pour l'épaisseur ou la largeur de la couronne.")
    w = cq.Workplane("XY").circle(od / 2).circle(idia / 2).extrude(t)
    if ch > 0:
        w = w.faces(">Z").edges().chamfer(ch)
    ctx["derived"].update(ring_width=(od - idia) / 2)
    ctx["assumptions"].append("Rondelle plate imprimée à plat : l'écrasement par la vis se fait dans le plan des couches (bon).")
    if (od - idia) / 2 < 2.0:
        ctx["warnings"].append("Couronne < 2 mm : fragile à l'écrasement.")
    return [("washer", w)]


register(Template(
    id="washer", title="Rondelle", category="rondelle-entretoise",
    description="Rondelle plate chanfreinée pour répartir l'appui d'une vis sur une pièce imprimée.",
    tags=["rondelle", "vis"],
    params=[
        P("outer_diameter", "number", 18.0, "Diamètre extérieur.", "mm", 3, 200),
        P("inner_diameter", "number", 6.6, "Diamètre du perçage (compensé selon l'imprimante).", "mm", 1, 150, auto="hole:vertical"),
        P("thickness", "number", 2.0, "Épaisseur.", "mm", 0.4, 20),
        P("chamfer", "number", 0.4, "Chanfrein du dessus ; 0 = aucun.", "mm", 0, 5),
    ],
    build=washer,
))


# =========================================================================================
# Plaque de montage perforée
# =========================================================================================


def mounting_plate(cq: Any, p: dict[str, Any], ctx: dict[str, Any]) -> list[Part]:
    W, H, T = p["width"], p["height"], p["thickness"]
    cols, rows, hd, margin = p["cols"], p["rows"], p["hole_diameter"], p["margin"]
    cr = p["corner_radius"]
    if cols * rows > 400:
        _fail("cols × rows ne doit pas dépasser 400.")
    if cr * 2 >= min(W, H):
        _fail("corner_radius trop grand.")
    plate = _box(cq, 0, 0, 0, W, H, T)
    if cr > 0:
        plate = plate.edges("|Z").fillet(cr)
    span_x, span_y = W - 2 * margin, H - 2 * margin
    if span_x <= hd or span_y <= hd:
        _fail("margin trop grand : la zone perforée est plus petite qu'un trou.")
    pts: list[tuple[float, float]] = []
    px = (span_x - hd) / (cols - 1) if cols > 1 else 0.0
    py = (span_y - hd) / (rows - 1) if rows > 1 else 0.0
    for j in range(rows):
        for i in range(cols):
            x = margin + hd / 2 + i * px if cols > 1 else W / 2
            y = margin + hd / 2 + j * py if rows > 1 else H / 2
            if p["pattern"] == "staggered" and j % 2 == 1 and cols > 1:
                x += px / 2
                if x + hd / 2 > W - margin + 1e-6:
                    continue
            pts.append((x, y))
    web = min([px - hd if cols > 1 else 99.0, py - hd if rows > 1 else 99.0])
    if web < 1.2:
        ctx["warnings"].append(f"Entre-trous {web:.2f} mm < 1,2 mm : réduire le nombre de trous ou leur diamètre.")
    if pts:
        plate = plate.cut(cq.Workplane("XY").pushPoints(pts).circle(hd / 2).extrude(T + 2).translate((0, 0, -1)))
    md, mm_ = p["mount_hole_diameter"], p["mount_margin"]
    mounts: list[tuple[float, float]] = []
    if md > 0:
        if mm_ - md / 2 < 1.0:
            _fail("mount_margin trop petit pour mount_hole_diameter.")
        mounts = [(mm_, mm_), (W - mm_, mm_), (mm_, H - mm_), (W - mm_, H - mm_)]
        plate = plate.cut(cq.Workplane("XY").pushPoints(mounts).circle(md / 2).extrude(T + 2).translate((0, 0, -1)))
        if margin < mm_ + md / 2 + 1:
            ctx["warnings"].append("Les trous de fixation d'angle chevauchent la zone perforée (margin trop faible).")
    ctx["derived"].update(holes=len(pts), mount_holes=len(mounts), pitch_x=px, pitch_y=py,
                          open_area_pct=100 * len(pts) * math.pi * (hd / 2) ** 2 / (W * H))
    ctx["assumptions"].append("Plaque à plat ; trous verticaux (axe Z) ; pas régulier calculé entre les marges.")
    return [("mounting_plate", plate)]


register(Template(
    id="mounting_plate", title="Plaque de montage perforée", category="support",
    description="Plaque rectangulaire à grille de trous (droite ou quinconce) et 4 trous de fixation d'angle.",
    tags=["plaque", "perforée", "montage"],
    params=[
        P("width", "number", 100.0, "Largeur (X).", "mm", 10, 400),
        P("height", "number", 60.0, "Hauteur (Y).", "mm", 10, 400),
        P("thickness", "number", 3.0, "Épaisseur.", "mm", 0.8, 30),
        P("cols", "integer", 8, "Colonnes de trous.", "", 1, 40),
        P("rows", "integer", 4, "Rangées de trous.", "", 1, 40),
        P("hole_diameter", "number", 5.0, "Diamètre des trous de la grille.", "mm", 1, 40, auto="hole:vertical"),
        P("margin", "number", 12.0, "Marge entre le bord et la zone perforée.", "mm", 1, 100),
        P("pattern", "enum", "grid", "grid = alignés ; staggered = quinconce.", options=("grid", "staggered")),
        P("corner_radius", "number", 3.0, "Rayon des angles.", "mm", 0, 50),
        P("mount_hole_diameter", "number", 4.5, "Diamètre des trous de fixation d'angle ; 0 = aucun.", "mm", 0, 20, auto="hole:vertical"),
        P("mount_margin", "number", 6.0, "Distance des trous de fixation aux bords.", "mm", 2, 50),
    ],
    build=mounting_plate,
))


# =========================================================================================
# Support mural (vis ou trou de serrure)
# =========================================================================================


def wall_mount(cq: Any, p: dict[str, Any], ctx: dict[str, Any]) -> list[Part]:
    W, H, T = p["width"], p["height"], p["thickness"]
    mode, n = p["mode"], p["count"]
    shank, head, hh = p["screw_shank_diameter"], p["screw_head_diameter"], p["screw_head_height"]
    if head <= shank:
        _fail("screw_head_diameter doit être > screw_shank_diameter.")
    plate = _box(cq, 0, 0, 0, W, H, T)
    r = min(3.0, W / 4, H / 4)
    plate = plate.edges("|Z").fillet(r)
    xs = [W / 2] if n == 1 else [W * 0.25, W * 0.75]
    if mode == "screws":
        ys = H * 0.2
        csk_r = head / 2
        depth = csk_r - shank / 2  # fraisurage à 90°
        if depth > T - 0.8:
            _fail("Plaque trop mince pour le fraisurage (épaisseur >= profondeur du fraisurage + 0.8).", depth=depth)
        for x in xs:
            plate = plate.cut(_cyl_z(cq, x, ys, shank / 2, -1, T + 2))
            cone = cq.Solid.makeCone(shank / 2, csk_r, depth, pnt=cq.Vector(x, ys, T - depth), dir=cq.Vector(0, 0, 1))
            plate = plate.cut(cq.Workplane(obj=cone))
        ctx["derived"].update(hole_height_from_bottom=ys, hole_x=xs, countersink_depth=depth)
        ctx["assumptions"].append("Plaque à plat, face de pose contre le mur côté Z=0 ; vis à tête fraisée 90° côté Z=T.")
    else:
        pocket = hh + 0.4
        if pocket > T - 1.2:
            _fail("Plaque trop mince : épaisseur >= profondeur de poche (tête + 0,4) + 1,2 mm.", pocket=pocket)
        slot = p["slot_length"]
        y0 = H * 0.2
        for x in xs:
            # poche arrière : cercle (tête) + glissière de la largeur de la tête
            pocket_s = (_cyl_z(cq, x, y0, head / 2, -1, pocket + 1)
                        .union(_box(cq, x - head / 2, y0, -1, head, slot, pocket + 1))
                        .union(_cyl_z(cq, x, y0 + slot, head / 2, -1, pocket + 1)))
            # fente étroite traversante (corps de la vis)
            thru = (_cyl_z(cq, x, y0, shank / 2, -1, T + 2)
                    .union(_box(cq, x - shank / 2, y0, -1, shank, slot, T + 2))
                    .union(_cyl_z(cq, x, y0 + slot, shank / 2, -1, T + 2)))
            plate = plate.cut(pocket_s).cut(thru)
        ctx["derived"].update(pocket_depth=pocket, slot_length=slot, hole_x=xs)
        ctx["assumptions"].append(
            "Trou de serrure : la tête de vis entre par l'arrière dans la poche ronde, la plaque glisse vers le bas ; "
            "face de pose = Z=0 (poche contre le mur). La poche s'imprime en pont (≈ diamètre de tête)."
        )
    if W < 2.5 * head:
        ctx["warnings"].append("Largeur < 2,5 × diamètre de tête : bords fragiles autour des trous.")
    return [("wall_mount", plate)]


register(Template(
    id="wall_mount", title="Support mural (vis / trou de serrure)", category="support",
    description="Plaque murale à 1-2 fixations : vis fraisées ou trous de serrure (tête de vis encastrée à l'arrière).",
    tags=["mural", "trou de serrure", "vis"],
    params=[
        P("width", "number", 50.0, "Largeur de la plaque.", "mm", 15, 300),
        P("height", "number", 70.0, "Hauteur de la plaque.", "mm", 15, 300),
        P("thickness", "number", 5.0, "Épaisseur.", "mm", 2, 30),
        P("mode", "enum", "keyhole", "keyhole = trous de serrure ; screws = vis fraisées.", options=("keyhole", "screws")),
        P("count", "integer", 2, "Nombre de fixations (1 centrée ou 2 côte à côte).", "", 1, 2),
        P("screw_shank_diameter", "number", 4.2, "Diamètre du corps de la vis (trou/fente).", "mm", 1.5, 12, auto="hole:vertical"),
        P("screw_head_diameter", "number", 8.0, "Diamètre de la tête de vis.", "mm", 3, 25),
        P("screw_head_height", "number", 2.6, "Hauteur de la tête de vis (poche du trou de serrure).", "mm", 1, 10),
        P("slot_length", "number", 8.0, "Course de la glissière du trou de serrure.", "mm", 3, 40),
    ],
    build=wall_mount,
))


# =========================================================================================
# Poignée
# =========================================================================================


def handle(cq: Any, p: dict[str, Any], ctx: dict[str, Any]) -> list[Part]:
    span, gd, sh, pd = p["span"], p["grip_diameter"], p["standoff"], p["post_diameter"]
    fh, ft = p["foot_hole_diameter"], p["foot_thickness"]
    if sh <= gd / 2 + 1:
        _fail("standoff doit dépasser grip_diameter / 2 + 1.")
    half = span / 2
    grip = _cyl_x(cq, 0, sh, gd / 2, -(half + pd / 2), span + pd)
    body = grip
    out_len = fh + 8
    fw = max(pd, fh + 6)
    for sgn in (1, -1):
        post = _cyl_z(cq, sgn * half, 0, pd / 2, 0, sh)
        x_in, x_out = half - pd / 2, half + pd / 2 + out_len
        foot = _box(cq, min(sgn * x_in, sgn * x_out), -fw / 2, 0, x_out - x_in, fw, ft)
        hole = _cyl_z(cq, sgn * (half + pd / 2 + out_len / 2), 0, fh / 2, -1, ft + 2)
        body = body.union(post).union(foot).cut(hole)
    ctx["derived"].update(finger_clearance=sh - gd / 2, overall_length=span + pd + 2 * out_len,
                          foot_hole_x=[-(half + pd / 2 + out_len / 2), half + pd / 2 + out_len / 2])
    if sh - gd / 2 < 25:
        ctx["warnings"].append("Passage des doigts < 25 mm sous la poignée : prise inconfortable.")
    ctx["assumptions"].append(
        "Poignée en U renversé : deux pieds à visser (trous accessibles de dessus), grip cylindrique. À imprimer retournée "
        "(grip au plateau, supports sous les pieds) ou pieds au plateau avec supports sous le grip ; "
        "pieds au plateau = couches perpendiculaires à l'effort de traction sur les montants (plus faible)."
    )
    return [("handle", body)]


register(Template(
    id="handle", title="Poignée", category="support",
    description="Poignée en U à deux pieds percés pour la fixer sur un coffret, un tiroir ou un couvercle.",
    tags=["poignée", "tiroir"],
    params=[
        P("span", "number", 90.0, "Distance entre les axes des deux montants.", "mm", 20, 400),
        P("grip_diameter", "number", 20.0, "Diamètre de la barre de prise.", "mm", 8, 60),
        P("standoff", "number", 38.0, "Hauteur de l'axe de la barre au-dessus de la base.", "mm", 12, 120),
        P("post_diameter", "number", 14.0, "Diamètre des montants.", "mm", 6, 40),
        P("foot_hole_diameter", "number", 4.5, "Diamètre des trous de vis des pieds.", "mm", 2, 12, auto="hole:vertical"),
        P("foot_thickness", "number", 4.0, "Épaisseur des pieds.", "mm", 2, 15),
    ],
    build=handle,
))


# =========================================================================================
# Crochet renforcé
# =========================================================================================


def hook_reinforced(cq: Any, p: dict[str, Any], ctx: dict[str, Any]) -> list[Part]:
    w, ph, t = p["width"], p["plate_height"], p["thickness"]
    arm, tip, hd, nh = p["arm_length"], p["tip_height"], p["hole_diameter"], p["holes"]
    fil, gl, gh, rt = p["fillet"], p["gusset_length"], p["gusset_height"], p["rib_thickness"]
    if arm <= 2 * t or ph <= t + gh or tip <= t:
        _fail("arm_length > 2 × thickness, plate_height > thickness + gusset_height et tip_height > thickness requis.")
    if gl >= arm - t or 2 * rt >= w:
        _fail("gusset_length < arm_length - thickness et 2 × rib_thickness < width requis.")
    prof = [(0, 0), (arm, 0), (arm, tip), (arm - t, tip), (arm - t, t), (t, t), (t, ph), (0, ph)]
    body = cq.Workplane("XZ").polyline(prof).close().extrude(-w)
    if fil > 0:
        try:
            sel = cq.selectors.BoxSelector((t - 0.05, -1, t - 0.05), (t + 0.05, w + 1, t + 0.05))
            body = body.edges(sel).fillet(min(fil, arm - 2 * t - 0.1, ph - 2 * t - 0.1))
        except Exception as e:  # noqa: BLE001
            ctx["warnings"].append(f"Congé d'angle intérieur non appliqué ({e!r}).")
    # nervures latérales triangulaires (plan XZ, épaisseur rt selon Y)
    rib = [(t - 0.05, t - 0.05), (t + gl, t - 0.05), (t - 0.05, t + gh)]
    for y0 in (0.0, w - rt):
        r_ = cq.Workplane("XZ").polyline(rib).close().extrude(-rt).translate((0, y0, 0))
        body = body.union(r_)
    if nh:
        lo, hi = t + hd, ph - gh - hd
        if hi <= lo:
            lo, hi = t + hd, ph - hd
        zs = legacy._spread(nh, lo, hi) if hi > lo else [ph / 2]
        if hi > lo and (hi - lo) / nh < 2.5 * hd:
            ctx["warnings"].append("Trous de la plaque trop serrés ou trop près des bords (< 1,25 Ø).")
        body = body.cut(cq.Workplane("YZ").pushPoints([(w / 2, z) for z in zs]).circle(hd / 2).extrude(t + 1).translate((-0.5, 0, 0)))
        if w < 2.5 * hd:
            ctx["warnings"].append("Largeur < 2,5 Ø de perçage.")
    ctx["derived"].update(usable_arm=arm - 2 * t, rib_thickness=rt)
    ctx["assumptions"].append(
        "Crochet en J renforcé : congé d'angle intérieur + 2 nervures latérales triangulaires ; "
        "imprimer plaque à plat sur le plateau (couches parallèles au pli) pour la meilleure tenue en flexion."
    )
    return [("hook_reinforced", body)]


register(Template(
    id="hook_reinforced", title="Crochet renforcé", category="support",
    description="Crochet mural en J avec congé d'angle et nervures latérales contre la flexion du bras.",
    tags=["crochet", "mural", "renfort"],
    params=[
        P("width", "number", 24.0, "Largeur.", "mm", 8, 200),
        P("plate_height", "number", 60.0, "Hauteur de la plaque.", "mm", 15, 300),
        P("thickness", "number", 5.0, "Épaisseur plaque et bras.", "mm", 1.6, 30),
        P("arm_length", "number", 40.0, "Longueur du bras.", "mm", 14, 300),
        P("tip_height", "number", 14.0, "Hauteur du bec.", "mm", 4, 100),
        P("fillet", "number", 4.0, "Rayon du congé à l'angle intérieur ; 0 = aucun.", "mm", 0, 20),
        P("gusset_length", "number", 14.0, "Longueur des nervures sur le bras.", "mm", 3, 100),
        P("gusset_height", "number", 14.0, "Hauteur des nervures sur la plaque.", "mm", 3, 100),
        P("rib_thickness", "number", 3.0, "Épaisseur des nervures.", "mm", 0.8, 20),
        P("hole_diameter", "number", 4.5, "Diamètre des trous de vis.", "mm", 1.5, 20, auto="hole:horizontal"),
        P("holes", "integer", 2, "Nombre de trous.", "", 0, 50),
    ],
    build=hook_reinforced,
))


# =========================================================================================
# Charnières
# =========================================================================================


def _hinge(cq: Any, p: dict[str, Any], ctx: dict[str, Any], pip: bool) -> list[Part]:
    L, W, t, n = p["length"], p["leaf_width"], p["thickness"], p["knuckles"]
    pd, kw = p["pin_diameter"], p["knuckle_wall"]
    if pip:
        c = p["clearance"]
        hole_r, gap = pd / 2 + c, c
        if n % 2 == 0:
            _fail("knuckles doit être impair (3, 5, 7...) pour une charnière imprimée en place.")
    else:
        hole_r, gap = (pd + p["hole_clearance"]) / 2, p["axial_gap"]
    R = hole_r + kw
    seg = (L - (n - 1) * gap) / n
    if seg < 2.0:
        _fail("Segments de nœud < 2 mm : réduire knuckles ou augmenter length.", segment=seg)
    if t > R:
        _fail("thickness doit être <= rayon des nœuds (pin_diameter/2 + jeu + knuckle_wall).", knuckle_radius=R)
    if W <= R + gap + 1:
        _fail("leaf_width trop petit : doit dépasser le rayon des nœuds + jeu + 1 mm.")

    def knuckle(x0: float, w: float) -> Any:
        return _cyl_x(cq, 0, R, R, x0, w).union(_box(cq, x0, -R, 0, w, 2 * R, R))

    a_parts = [_box(cq, 0, -W, 0, L, W - (R + gap), t)]
    b_parts = [_box(cq, 0, R + gap, 0, L, W - (R + gap), t)]
    for i in range(n):
        x0 = i * (seg + gap)
        if i % 2 == 0:
            a_parts += [knuckle(x0, seg), _box(cq, x0, -(R + gap), 0, seg, R + gap, t)]
        else:
            b_parts += [knuckle(x0, seg), _box(cq, x0, 0, 0, seg, R + gap, t)]
    # le plateau pleine longueur n'est pas coupé : il reste à distance `gap` du nœud adverse (rayon R)
    A = a_parts[0]
    for s in a_parts[1:]:
        A = A.union(s)
    B = b_parts[0]
    for s in b_parts[1:]:
        B = B.union(s)
    parts: list[Part]
    if pip:
        flat = 1.0
        tear = bool(p.get("teardrop", True))
        ht = hole_r * math.sqrt(2.0) - flat / 2
        if tear and (R - ht < 0.6 or math.hypot(flat / 2, ht) > R - 0.8):
            tear = False
            ctx["warnings"].append("Axe en goutte désactivé : paroi des nœuds trop mince pour loger la pointe à 45° "
                                   "(augmenter knuckle_wall ou réduire pin_diameter) ; l'axe rond demandera du support ou un pont.")
        if tear:
            A = A.union(_teardrop_x(cq, 0, R, pd / 2, 0, L, down=True, up=False, flat=flat))
            B = B.cut(_teardrop_x(cq, 0, R, hole_r, -1, L + 2, down=True, up=True, flat=flat))
        else:
            A = A.union(_cyl_x(cq, 0, R, pd / 2, 0, L))
            B = B.cut(_cyl_x(cq, 0, R, hole_r, -1, L + 2))
        parts = [("hinge_leaf_a", A), ("hinge_leaf_b", B)]
        ctx["derived"].update(teardrop=tear, radial_clearance=hole_r - pd / 2)
        ctx["assumptions"].append(
            f"Charnière imprimée en place : axe fusionné avec la lame A, jeu de {gap:g} mm (radial et axial) avec la lame B. "
            + ("Axe et perçage en goutte (pointes à 45° tronquées par un plat de 1 mm) : l'axe s'imprime à plat sans support. " if tear else "")
            + "Le jeu `clearance` est calculé par l'ingénierie : jeu glissant MESURÉ par la calibration (peigne de jeux) quand elle existe "
              "pour l'imprimante, la buse et la bobine, sinon 0,75 × jeu « libre » générique. "
            + "Imprimer à plat ; ne pas utiliser de bordure/brim reliant les deux lames ; décoller en les faisant pivoter."
        )
    else:
        hole = _cyl_x(cq, 0, R, hole_r, -1, L + 2)
        A = A.cut(hole)
        B = B.cut(hole)
        pin = _cyl_z(cq, L + 2 * R + 6, 0, pd / 2, 0, L).union(_cyl_z(cq, L + 2 * R + 6, 0, pd / 2 + 1.2, 0, 1.6))
        parts = [("hinge_leaf_a", A), ("hinge_leaf_b", B), ("hinge_pin", pin)]
        ctx["assumptions"].append(
            "Charnière à axe séparé : le perçage des nœuds est (pin_diameter + hole_clearance) ; l'axe (tête 1,6 mm) "
            "s'imprime debout à côté ; il peut être remplacé par une tige métallique de même diamètre."
        )
    ctx["derived"].update(knuckle_radius=R, knuckle_length=seg, hole_diameter=2 * hole_r, axial_gap=gap,
                          axis_height=R, opening_angle_deg=180)
    if seg < 4:
        ctx["warnings"].append("Nœuds < 4 mm de long : fragiles ; moins de nœuds ou plus de longueur.")
    if kw < 1.2:
        ctx["warnings"].append("knuckle_wall < 1,2 mm : paroi des nœuds trop mince autour de l'axe.")
    return parts


register(Template(
    id="hinge_print_in_place", title="Charnière imprimée en place", category="mécanisme",
    description="Charnière dont l'axe est imprimé à l'intérieur des nœuds de l'autre lame avec un jeu : sort de l'imprimante prête à pivoter, sans assemblage. Jeu calculé (calibré si disponible).",
    tags=["charnière", "print-in-place", "mécanisme"],
    params=[
        P("length", "number", 40.0, "Longueur totale de la charnière (le long de l'axe).", "mm", 10, 300),
        P("leaf_width", "number", 20.0, "Largeur de chaque lame, mesurée depuis l'axe.", "mm", 5, 150),
        P("thickness", "number", 3.0, "Épaisseur des lames.", "mm", 1, 15),
        P("knuckles", "integer", 5, "Nombre de nœuds (impair).", "", 3, 15),
        P("pin_diameter", "number", 4.0, "Diamètre de l'axe.", "mm", 1.5, 20),
        P("knuckle_wall", "number", 2.0, "Épaisseur de paroi des nœuds autour de l'axe.", "mm", 0.8, 10),
        P("clearance", "number", 0.3, "Jeu radial et axial entre les pièces mobiles, par côté.", "mm", 0.1, 1.5, auto="gap_pip"),
        P("teardrop", "boolean", True, "Axe et perçage en goutte (pointes à 45°) pour imprimer à plat sans support ; false = axe rond.", ""),
    ],
    build=lambda cq, p, ctx: _hinge(cq, p, ctx, True),
))

register(Template(
    id="hinge_pin", title="Charnière à axe", category="mécanisme",
    description="Deux lames à nœuds alternés percés et un axe séparé (imprimé ou tige métallique).",
    tags=["charnière", "axe", "mécanisme"],
    params=[
        P("length", "number", 40.0, "Longueur totale de la charnière.", "mm", 10, 300),
        P("leaf_width", "number", 20.0, "Largeur de chaque lame depuis l'axe.", "mm", 5, 150),
        P("thickness", "number", 3.0, "Épaisseur des lames.", "mm", 1, 15),
        P("knuckles", "integer", 5, "Nombre de nœuds.", "", 2, 15),
        P("pin_diameter", "number", 3.0, "Diamètre de l'axe.", "mm", 1.5, 20),
        P("knuckle_wall", "number", 2.0, "Paroi des nœuds autour du perçage.", "mm", 0.8, 10),
        P("hole_clearance", "number", 0.3, "Jeu diamétral du perçage par rapport à l'axe.", "mm", 0.05, 2, auto="fit:glissant"),
        P("axial_gap", "number", 0.4, "Jeu entre nœuds voisins (axial).", "mm", 0.1, 2),
    ],
    build=lambda cq, p, ctx: _hinge(cq, p, ctx, False),
))


# =========================================================================================
# Clip cantilever
# =========================================================================================


def snap_clip(cq: Any, p: dict[str, Any], ctx: dict[str, Any]) -> list[Part]:
    L, h, H = p["beam_length"], p["beam_thickness"], p["beam_width"]
    d, alpha = p["hook_depth"], p["insertion_angle"]
    bl, m = p["base_length"], p["base_margin"]
    wt, cl, rim = p["catch_thickness"], p["window_clearance"], p["catch_rim"]
    hl = d / math.tan(math.radians(alpha))
    if hl > L * 0.6:
        _fail("Rampe d'insertion trop longue pour le bras : réduire hook_depth, augmenter l'angle ou beam_length.",
              ramp_length=hl)
    if cl >= d * 0.6:
        _fail("window_clearance doit rester < 0,6 × hook_depth (sinon le crochet n'accroche pas).")
    base = _box(cq, -bl, -m, 0, bl, h + 2 * m, H)
    beam = _box(cq, -0.01, 0, 0, L + 0.01, h, H)
    hook = cq.Workplane("XY").polyline([(L - hl, h - 0.01), (L - hl, h + d), (L, h - 0.01)]).close().extrude(H)
    clip = base.union(beam).union(hook)
    wy0 = h + m + 12
    win_x, win_y = H + 2 * cl, h + 2 * cl
    rx, ry = win_x + 2 * rim, win_y + 2 * rim
    recv = _box(cq, 0, wy0, 0, rx, ry, wt)
    recv = recv.cut(_box(cq, rim, wy0 + rim, -1, win_x, win_y, wt + 2))
    ctx["derived"].update(hook_depth=d, ramp_length=hl, catch_overlap=d - cl, window_mm=[win_x, win_y],
                          deflection_needed=d, beam_aspect_L_over_h=L / h, receiver_origin_y=wy0)
    ctx["assumptions"].append(
        f"Clip cantilever à plat (flexion dans le plan XY, couches parallèles à la flexion), rampe d'insertion à {alpha:g}°. "
        "Le récepteur est une paroi perçée d'une fenêtre : le crochet la traverse en fléchissant de hook_depth puis s'accroche "
        "sur le bord de la fenêtre (recouvrement hook_depth − window_clearance). Déformation et force : voir `calculs` (SnapFit)."
    )
    if L / h < 4:
        ctx["warnings"].append("L/h < 4 : bras court, la théorie des poutres est optimiste.")
    return [("snap_clip", clip), ("snap_receiver", recv)]


register(Template(
    id="snap_clip", title="Clip cantilever", category="mécanisme",
    description="Crochet à bras flexible (cantilever) avec rampe d'insertion et son récepteur à fenêtre ; dimensionné avec le calcul de déformation admissible (SnapFit) du matériau.",
    tags=["clip", "snap-fit", "cantilever"],
    params=[
        P("beam_length", "number", 18.0, "Longueur du bras flexible.", "mm", 4, 100),
        P("beam_thickness", "number", 1.6, "Épaisseur du bras dans le sens de la flexion.", "mm", 0.8, 8),
        P("beam_width", "number", 8.0, "Largeur du bras (hors flexion).", "mm", 2, 40),
        P("hook_depth", "number", 1.2, "Retour du crochet = flèche d'insertion.", "mm", 0.3, 6),
        P("insertion_angle", "number", 30.0, "Angle de la rampe d'insertion (depuis l'axe de poussée).", "deg", 10, 60),
        P("base_length", "number", 8.0, "Longueur du bloc d'ancrage.", "mm", 3, 40),
        P("base_margin", "number", 3.0, "Débord du bloc d'ancrage de chaque côté du bras.", "mm", 1, 20),
        P("catch_thickness", "number", 2.4, "Épaisseur de la paroi du récepteur.", "mm", 1, 10),
        P("window_clearance", "number", 0.3, "Jeu de la fenêtre autour du bras, par côté.", "mm", 0.05, 1.5, auto="fitside:libre"),
        P("catch_rim", "number", 3.0, "Largeur de matière autour de la fenêtre.", "mm", 1.2, 15),
    ],
    build=snap_clip,
))


# =========================================================================================
# Boîtier à couvercle vissé ou clipsé
# =========================================================================================


def enclosure(cq: Any, p: dict[str, Any], ctx: dict[str, Any]) -> list[Part]:
    ix, iy, iz = p["inner_x"], p["inner_y"], p["inner_z"]
    w, fl, lt, clr, lh = p["wall"], p["floor"], p["lid_thickness"], p["lid_clearance"], p["lip_height"]
    cr, mode = p["corner_radius"], p["lid_mode"]
    lip_t = p["lip_thickness"]
    ox, oy, h = ix + 2 * w, iy + 2 * w, fl + iz
    if cr * 2 >= min(ox, oy):
        _fail("corner_radius trop grand.")
    if ix - 2 * clr - 2 * lip_t <= 1 or iy - 2 * clr - 2 * lip_t <= 1:
        _fail("Dimensions intérieures trop petites pour la lèvre du couvercle.")
    body = _box(cq, 0, 0, 0, ox, oy, h)
    if cr > 0:
        body = body.edges("|Z").fillet(cr)
    body = body.cut(_box(cq, w, w, fl, ix, iy, iz + 1))
    lx = ox + 10  # décalage du couvercle posé à côté
    lid = _box(cq, lx, 0, 0, ox, oy, lt)
    if cr > 0:
        lid = lid.edges("|Z").fillet(cr)
    lip_o = _box(cq, lx + w + clr, w + clr, lt, ix - 2 * clr, iy - 2 * clr, lh)
    lip_i = _box(cq, lx + w + clr + lip_t, w + clr + lip_t, lt - 1, ix - 2 * clr - 2 * lip_t, iy - 2 * clr - 2 * lip_t, lh + 2)
    lid = lid.union(lip_o.cut(lip_i))
    if mode == "screw":
        bd, hd_i, dep = p["boss_diameter"], p["insert_hole_diameter"], p["insert_depth"]
        sh, head = p["lid_hole_diameter"], p["lid_head_diameter"]
        if dep >= iz - 1:
            _fail("insert_depth trop profond pour la hauteur intérieure.")
        if bd <= hd_i + 2.4:
            _fail("boss_diameter doit dépasser insert_hole_diameter + 2,4 (paroi de 1,2 mm autour de l'insert).")
        off = w + bd / 2 - 0.3
        centers = [(off, off), (ox - off, off), (off, oy - off), (ox - off, oy - off)]
        for (cx, cy) in centers:
            body = body.union(_cyl_z(cq, cx, cy, bd / 2, fl - 0.1, iz + 0.1))
            body = body.cut(_cyl_z(cq, cx, cy, hd_i / 2, h - dep, dep + 1))
        csk_r, depth = head / 2, (head / 2 - sh / 2)
        depth = min(depth, lt - 0.8)
        for (cx, cy) in centers:
            lcx = lx + cx
            lid = lid.cut(_cyl_z(cq, lcx, cy, bd / 2 + clr, lt - 0.01, lh + 1))  # dégagement du plot dans la lèvre
            lid = lid.cut(_cyl_z(cq, lcx, cy, sh / 2, -1, lt + 2))
            if depth > 0.2:
                cone = cq.Solid.makeCone(sh / 2 + depth, sh / 2, depth, pnt=cq.Vector(lcx, cy, 0), dir=cq.Vector(0, 0, 1))
                lid = lid.cut(cq.Workplane(obj=cone))
        screw_len = lt + dep - 1.0
        std = next((s for s in (6, 8, 10, 12, 16, 20, 25) if s >= screw_len), round(screw_len))
        ctx["derived"].update(screws=4, screw_length_mm=std, boss_centers=centers, insert_depth=dep)
        ctx["assumptions"].append(
            f"Couvercle vissé : 4 plots d'angle avec trou d'insert à chaud Ø{hd_i:g} × {dep:g} mm ; vis {p['screw_size']} "
            f"de longueur ≈ {std} mm (épaisseur du couvercle + profondeur d'insert − 1 mm), tête fraisée côté extérieur."
        )
        if bd / 2 + w < 0.1:
            ctx["warnings"].append("Plots débordant des parois.")
    else:
        bh = p["clip_bump_height"]
        if w < 1.6:
            ctx["warnings"].append("wall < 1,6 mm : rainures de clipsage fragiles.")
        bl = min(12.0, ix * 0.5)
        zlo, zhi = lt + 0.15 * lh, lt + 0.6 * lh
        for sgn_y in (0, 1):
            ybase = (w + clr - bh) if sgn_y == 0 else (w + clr + iy - 2 * clr)
            lid = lid.union(_box(cq, lx + ox / 2 - bl / 2, ybase, zlo, bl, bh, zhi - zlo))
        # rainures dans le corps : le couvercle retourné place la lèvre (z lt..lt+lh) sur le bord haut (z h)
        gz_hi, gz_lo = h - (zlo - lt) + 0.2, h - (zhi - lt) - 0.2
        for sgn_y in (0, 1):
            ygrove = (w - bh - clr - 0.2) if sgn_y == 0 else (w + iy - 0.0)
            body = body.cut(_box(cq, ox / 2 - bl / 2 - 0.3, ygrove, gz_lo, bl + 0.6, bh + clr + 0.2, gz_hi - gz_lo))
        ctx["derived"].update(bump_length=bl, bump_height=bh)
        ctx["assumptions"].append(
            "Couvercle clipsé : lèvre à friction avec deux ergots sur les grands côtés et rainures correspondantes dans les parois ; "
            "aucune vis. L'effort d'ouverture dépend du jeu et de l'élasticité du matériau : à essayer sur un prototype."
        )
        ctx["warnings"].append("Clipsage à friction : élasticité non vérifiée ; pour un clip dimensionné, utiliser le gabarit snap_clip.")
    ctx["derived"].update(outer=[ox, oy, h], lid_offset_x=lx)
    ctx["assumptions"].append("Boîtier à plat, couvercle posé à côté, lèvre vers le haut (sans support).")
    return [("enclosure_body", body), ("enclosure_lid", lid)]


register(Template(
    id="enclosure", title="Boîtier à couvercle vissé ou clipsé", category="boîtier",
    description="Boîtier rectangulaire à angles arrondis et couvercle à lèvre : vissé (4 plots pour inserts à chaud) ou clipsé (ergots à friction). Trous d'inserts et jeux calculés par le moteur d'ingénierie.",
    tags=["boîtier", "couvercle", "insert", "vis"],
    params=[
        P("inner_x", "number", 70.0, "Dimension intérieure X.", "mm", 10, 300),
        P("inner_y", "number", 50.0, "Dimension intérieure Y.", "mm", 10, 300),
        P("inner_z", "number", 28.0, "Hauteur intérieure.", "mm", 8, 200),
        P("wall", "number", 2.4, "Épaisseur des parois.", "mm", 1.2, 10),
        P("floor", "number", 2.4, "Épaisseur du fond.", "mm", 1.2, 10),
        P("lid_thickness", "number", 3.0, "Épaisseur du couvercle.", "mm", 1.6, 10),
        P("lid_clearance", "number", 0.25, "Jeu radial de la lèvre, par côté.", "mm", 0.05, 1.5, auto="fitside:libre"),
        P("lip_height", "number", 4.0, "Hauteur de la lèvre.", "mm", 2, 20),
        P("lip_thickness", "number", 1.6, "Épaisseur de la lèvre.", "mm", 0.8, 6),
        P("corner_radius", "number", 3.0, "Rayon des angles extérieurs.", "mm", 0, 30),
        P("lid_mode", "enum", "screw", "screw = vissé sur plots d'inserts ; clip = clipsé à friction.", options=("screw", "clip")),
        P("screw_size", "enum", "M3", "Taille de vis (mode screw), pour les dimensions d'insert.", options=("M2", "M2.5", "M3", "M4", "M5")),
        P("insert_hole_diameter", "number", 4.0, "Diamètre du trou d'insert à chaud.", "mm", 1.5, 12, auto="insert:diameter"),
        P("insert_depth", "number", 7.2, "Profondeur du trou d'insert.", "mm", 2, 20, auto="insert:depth"),
        P("boss_diameter", "number", 7.2, "Diamètre des plots d'angle.", "mm", 3, 20, auto="insert:boss"),
        P("lid_hole_diameter", "number", 3.4, "Diamètre du passage de vis dans le couvercle.", "mm", 1.5, 10, auto="hole:vertical:screw"),
        P("lid_head_diameter", "number", 6.4, "Diamètre de tête de vis (fraisurage extérieur).", "mm", 3, 15),
        P("clip_bump_height", "number", 0.5, "Hauteur des ergots de clipsage (mode clip).", "mm", 0.2, 1.5),
    ],
    build=enclosure,
))


# =========================================================================================
# Engrenages
# =========================================================================================


def spur_gear(cq: Any, p: dict[str, Any], ctx: dict[str, Any]) -> list[Part]:
    m, z, a = p["module"], p["teeth"], p["pressure_angle"]
    if p["bore_diameter"] >= gear_dims(m, z, a)["root_diameter"] - 2 * m:
        _fail("bore_diameter trop grand : il ne reste pas assez de matière sous la denture (>= 1 module).")
    g = gear_solid(cq, m, z, a, p["thickness"], p["bore_diameter"], p["tooth_clearance"], p["keyway_width"],
                   p["keyway_depth"], p["hub_diameter"], p["hub_height"])
    d = gear_dims(m, z, a)
    ctx["derived"].update({k: round(v, 4) for k, v in d.items()}, circular_pitch=math.pi * m)
    ctx["assumptions"].append(
        "Denture droite en développante de cercle, profil de référence 1 module (tête) / 1,25 module (pied) ; "
        "dent amincie de tooth_clearance sur l'arc primitif pour le jeu entre flancs. Imprimer à plat (dents dans le plan des couches)."
    )
    ctx["derived"].update(root_fillet_radius=round(space_side(m, z, a, p["tooth_clearance"])[2], 4), flank_points=24,
                          nominal_root_fillet=ROOT_FILLET * m)
    ctx["warnings"].extend(gear_warnings(z, a))
    if m * 2 < 0.8:
        ctx["warnings"].append("Module très petit : dent plus fine que 2 passes de buse.")
    return [("spur_gear", g)]


_GEAR_COMMON = [
    P("module", "number", 1.5, "Module (diamètre primitif / nombre de dents).", "mm", 0.5, 10),
    P("pressure_angle", "number", 20.0, "Angle de pression.", "deg", 14.5, 25),
    P("thickness", "number", 8.0, "Épaisseur (largeur de denture).", "mm", 1, 60),
]

register(Template(
    id="spur_gear", title="Engrenage droit", category="mécanisme",
    description="Engrenage droit à denture en développante : module, nombre de dents, angle de pression, alésage, clavette et moyeu optionnels.",
    tags=["engrenage", "pignon", "mécanisme"],
    params=[
        _GEAR_COMMON[0],
        P("teeth", "integer", 20, "Nombre de dents.", "", 8, 150),
        _GEAR_COMMON[1], _GEAR_COMMON[2],
        P("bore_diameter", "number", 5.0, "Diamètre de l'alésage (compensé selon l'imprimante) ; 0 = plein.", "mm", 0, 100, auto="hole:vertical"),
        P("keyway_width", "number", 0.0, "Largeur de la rainure de clavette ; 0 = aucune.", "mm", 0, 20),
        P("keyway_depth", "number", 1.2, "Profondeur de la rainure au-delà de l'alésage.", "mm", 0, 10),
        P("hub_diameter", "number", 0.0, "Diamètre du moyeu ; 0 = aucun.", "mm", 0, 100),
        P("hub_height", "number", 0.0, "Hauteur du moyeu au-dessus de la denture.", "mm", 0, 50),
        P("tooth_clearance", "number", 0.1, "Amincissement de la dent sur l'arc primitif (jeu de flanc).", "mm", 0, 1.5),
    ],
    build=spur_gear,
))


def gear_pair(cq: Any, p: dict[str, Any], ctx: dict[str, Any]) -> list[Part]:
    m, z1, z2, a = p["module"], p["teeth_1"], p["teeth_2"], p["pressure_angle"]
    bl = p["backlash"]
    d1, d2 = gear_dims(m, z1, a), gear_dims(m, z2, a)
    cd = m * (z1 + z2) / 2
    for bore, dd, n in ((p["bore_1"], d1, 1), (p["bore_2"], d2, 2)):
        if bore >= dd["root_diameter"] - 2 * m:
            _fail(f"bore_{n} trop grand pour l'engrenage {n}.")
    g1 = gear_solid(cq, m, z1, a, p["thickness"], p["bore_1"], bl / 2)
    g2 = gear_solid(cq, m, z2, a, p["thickness"], p["bore_2"], bl / 2)
    # engrenage 2 : tourné pour qu'un creux fasse face à la dent n°1 de l'engrenage 1
    g2 = g2.rotate((0, 0, 0), (0, 0, 1), 180.0 + 180.0 / z2).translate((cd, 0, 0))
    ca = (math.sqrt(d1["tip_radius"] ** 2 - d1["base_radius"] ** 2) + math.sqrt(d2["tip_radius"] ** 2 - d2["base_radius"] ** 2)
          - cd * math.sin(math.radians(a))) / (math.pi * m * math.cos(math.radians(a)))
    ctx["derived"].update(center_distance=cd, ratio=z2 / z1, contact_ratio=round(ca, 3),
                          pitch_diameter_1=d1["pitch_diameter"], pitch_diameter_2=d2["pitch_diameter"],
                          tip_diameter_1=d1["tip_diameter"], tip_diameter_2=d2["tip_diameter"])
    ctx["assumptions"].append(
        f"Paire en position de montage : entraxe = module × (z1 + z2) / 2 = {cd:g} mm, jeu de flanc total {bl:g} mm "
        "(chaque dent amincie de moitié) ; l'engrenage 2 est à +X de l'axe de l'engrenage 1. Alésages à compenser selon l'imprimante."
    )
    if ca < 1.2:
        ctx["warnings"].append(f"Rapport de conduite {ca:.2f} < 1,2 : engrènement peu continu (augmenter les dents).")
    ic = interference_check(m, z1, z2, a)
    ctx["derived"].update(gear_1_tip_margin_mm=round(ic["gear_1_tip_margin_mm"], 4), gear_2_tip_margin_mm=round(ic["gear_2_tip_margin_mm"], 4),
                          interference=ic["interference"], undercut_limit_teeth=round(ic["undercut_limit_teeth"], 2),
                          root_fillet_radius=round(space_side(m, z1, a, bl / 2)[2], 4))
    if ic["interference"]:
        ctx["warnings"].append("Interférence de développante : la tête d'une roue dépasse le point de tangence de la ligne d'action "
                               "(marges : roue 1 %.2f mm, roue 2 %.2f mm) ; augmenter les dents, réduire la tête ou prévoir un déport." %
                               (ic["gear_1_tip_margin_mm"], ic["gear_2_tip_margin_mm"]))
    for n, zz in ((1, z1), (2, z2)):
        ctx["warnings"].extend(f"Roue {n} : {w}" for w in gear_warnings(zz, a))
    return [("gear_1", g1), ("gear_2", g2)]


register(Template(
    id="gear_pair", title="Paire d'engrenages", category="mécanisme",
    description="Deux engrenages droits au même module placés à l'entraxe calculé, avec jeu de flanc ; rapport de réduction et rapport de conduite fournis.",
    tags=["engrenage", "réducteur", "mécanisme"],
    params=[
        _GEAR_COMMON[0],
        P("teeth_1", "integer", 18, "Dents du pignon (engrenage 1).", "", 8, 150),
        P("teeth_2", "integer", 36, "Dents de la roue (engrenage 2).", "", 8, 150),
        _GEAR_COMMON[1], _GEAR_COMMON[2],
        P("bore_1", "number", 5.0, "Alésage de l'engrenage 1.", "mm", 0, 100, auto="hole:vertical"),
        P("bore_2", "number", 5.0, "Alésage de l'engrenage 2.", "mm", 0, 100, auto="hole:vertical"),
        P("backlash", "number", 0.25, "Jeu de flanc total sur l'arc primitif.", "mm", 0.0, 2, auto="fit:glissant"),
    ],
    build=gear_pair,
))


# =========================================================================================
# Filetage métrique imprimable / trou pour insert
# =========================================================================================


def thread(cq: Any, p: dict[str, Any], ctx: dict[str, Any]) -> list[Part]:
    size, mode = p["size"], p["mode"]
    D = nominal(size)
    pitch = p["pitch"] or COARSE_PITCH[size]
    parts: list[Part] = []
    L = p["length"]
    af = ACROSS_FLATS[size]
    clr = p["thread_clearance"]
    if mode in ("bolt", "pair"):
        hh = p["head_height"] or round(0.7 * D, 2)
        if L < 2 * pitch:
            _fail("length doit couvrir au moins 2 pas de filet.")
        rod = threaded_rod(cq, D, pitch, L, z0=hh - 0.01)
        rod = bolt_tip_chamfer(cq, rod, D, pitch, hh - 0.01 + L)
        head = hex_prism(cq, af, hh, chamfer=min(0.5, hh / 4))
        parts.append(("thread_bolt", head.union(rod)))
        ctx["derived"].update(head_height=hh, across_flats=af, major_diameter=D, pitch=pitch,
                              minor_diameter=D - 2 * 0.6134 * pitch)
    if mode in ("nut", "pair"):
        nh = p["nut_height"] or round(0.8 * D, 2)
        blank = hex_prism(cq, af, nh, chamfer=min(0.5, nh / 4))
        cutter = threaded_rod(cq, D + 2 * clr, pitch, nh + 2, z0=-1)
        nut = blank.cut(cutter)
        nut = nut_entry_chamfer(cq, nut, D / 2 + clr - thread_depth(pitch), D / 2 + clr, nh)
        if mode == "pair":
            nut = nut.translate((af / math.cos(math.radians(30)) + 4, 0, 0))
        parts.append(("thread_nut", nut))
        ctx["derived"].update(nut_height=nh, nut_clearance_radial=clr)
    if mode == "insert_boss":
        bd, hole, dep, bh = p["boss_diameter"], p["insert_hole_diameter"], p["insert_depth"], p["boss_height"]
        if dep >= bh:
            _fail("insert_depth doit être < boss_height.")
        if bd <= hole + 2.4:
            _fail("boss_diameter doit dépasser insert_hole_diameter + 2,4.")
        boss = _cyl_z(cq, 0, 0, bd / 2, 0, bh)
        boss = boss.cut(_cyl_z(cq, 0, 0, hole / 2, bh - dep, dep + 1))
        parts.append(("insert_boss", boss))
        ctx["derived"].update(insert_size=p["insert_size"], insert_hole_diameter=hole, insert_depth=dep)
        ctx["assumptions"].append(
            f"Plot cylindrique pour insert fileté à chaud {p['insert_size']} : trou Ø{hole:g} × {dep:g} mm (profondeur = insert + refoulement). "
            "Chauffer l'insert (≈ 200 °C pour PLA/PETG), l'enfoncer à la main bien droit."
        )
    if mode != "insert_boss":
        ctx["tess"] = thread_tessellation(D)
        ctx["derived"].update(thread_depth=round(thread_depth(pitch), 4), tessellation_linear_mm=ctx["tess"][0],
                              tessellation_angular_rad=round(ctx["tess"][1], 4))
        ctx["assumptions"].append(
            f"Filetage métrique {size}×{pitch:g} à droite, profil ISO 68-1 de base (flancs 60°, crête plate P/8, profondeur "
            f"h3 = 0,6134 P = {thread_depth(pitch):.3f} mm, fond plat au lieu du congé), imprimé axe Z, chanfrein d'entrée 45° sur la vis et l'écrou. "
            f"Jeu imprimable : l'écrou est taillé au profil de la vis agrandi de {clr:g} mm en RADIAL, soit {2 * clr * math.tan(math.radians(30)):.2f} mm "
            "de jeu axial total entre flancs (cf. classes ISO 6H/6g pour l'usinage, trop serrées en FDM). "
            "Maillage exporté plus fin que les autres gabarits (flèche <= 0,015 mm). Les petits filets (< M5) sont fragiles et peu précis en FDM ; "
            "préférer un insert pour les assemblages répétés ou très chargés."
        )
        if D < 5:
            ctx["warnings"].append(f"{size} : filetage < M5 : résolution FDM insuffisante, prévoir un perçage de reprise ou un insert.")
    return parts


register(Template(
    id="thread", title="Vis / écrou métrique ou plot d'insert", category="fixation",
    description="Vis hexagonale et/ou écrou métriques imprimables (hélice exacte, jeu sur le filet calculé), ou plot cylindrique avec trou pour insert à chaud.",
    tags=["filetage", "vis", "écrou", "insert"],
    params=[
        P("size", "enum", "M6", "Taille métrique (pas gros ISO par défaut).", options=SIZES),
        P("mode", "enum", "pair", "bolt = vis ; nut = écrou ; pair = les deux ; insert_boss = plot pour insert.",
          options=("bolt", "nut", "pair", "insert_boss")),
        P("length", "number", 14.0, "Longueur filetée de la vis (sous la tête).", "mm", 3, 80),
        P("pitch", "number", 0.0, "Pas ; 0 = pas gros ISO de la taille.", "mm", 0, 3),
        P("head_height", "number", 0.0, "Hauteur de tête ; 0 = 0,7 × diamètre.", "mm", 0, 20),
        P("nut_height", "number", 0.0, "Hauteur de l'écrou ; 0 = 0,8 × diamètre.", "mm", 0, 30),
        P("thread_clearance", "number", 0.2, "Jeu radial de l'écrou sur le filet.", "mm", 0.05, 1, auto="fitside:libre"),
        P("insert_size", "enum", "M3", "Taille de l'insert (mode insert_boss).", options=("M2", "M2.5", "M3", "M4", "M5", "M6", "M8")),
        P("insert_hole_diameter", "number", 4.0, "Diamètre du trou d'insert.", "mm", 1.5, 14, auto="insert:diameter"),
        P("insert_depth", "number", 7.2, "Profondeur du trou d'insert.", "mm", 2, 25, auto="insert:depth"),
        P("boss_diameter", "number", 7.2, "Diamètre du plot.", "mm", 3, 30, auto="insert:boss"),
        P("boss_height", "number", 12.0, "Hauteur du plot.", "mm", 3, 60),
    ],
    build=thread,
))
