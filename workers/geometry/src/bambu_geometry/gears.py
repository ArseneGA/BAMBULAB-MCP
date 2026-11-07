"""Engrenages droits à denture en développante de cercle (profil polygonal : >= 20 points par flanc, congé de pied 0,38·m)."""

from __future__ import annotations

import math
from typing import Any

from .errors import GeometryError

ROOT_FILLET = 0.38  # rayon de congé de pied de la crémaillère de référence : 0,38·m (ISO 53 / DIN 867)
FLANK_POINTS = 24   # points par flanc (développante), >= 20 imposés
FILLET_POINTS = 8   # points du congé de pied, par côté


def inv(a: float) -> float:
    """Fonction développante inv(a) = tan(a) - a."""
    return math.tan(a) - a


def gear_dims(module: float, teeth: int, pressure_angle_deg: float) -> dict[str, float]:
    a = math.radians(pressure_angle_deg)
    rp = module * teeth / 2
    return {
        "pitch_radius": rp,
        "base_radius": rp * math.cos(a),
        "tip_radius": rp + module,
        "root_radius": rp - 1.25 * module,
        "pitch_diameter": 2 * rp,
        "tip_diameter": 2 * (rp + module),
        "root_diameter": 2 * (rp - 1.25 * module),
        "base_diameter": 2 * rp * math.cos(a),
    }


def undercut_limit(pressure_angle_deg: float) -> float:
    """Nombre de dents théorique en dessous duquel une crémaillère de hauteur de tête 1·m sous-taille : 2/sin²(α)."""
    return 2.0 / math.sin(math.radians(pressure_angle_deg)) ** 2


def interference_check(module: float, teeth_1: int, teeth_2: int, pressure_angle_deg: float) -> dict[str, Any]:
    """Interférence de développante d'une paire sans déport (entraxe m·(z1+z2)/2).

    Le contact doit rester sur le segment N1N2 de la ligne d'action (longueur a·sin α, N1/N2 = tangences avec les cercles de
    base) : au-delà, la tête d'une roue attaquerait la partie non développante du pied de l'autre. Le point de tête de la roue k
    est à sqrt(ra_k² - rb_k²) de sa tangence : condition ra_k² <= rb_k² + (a·sin α)².
    """
    a = math.radians(pressure_angle_deg)
    cd = module * (teeth_1 + teeth_2) / 2
    d1, d2 = gear_dims(module, teeth_1, pressure_angle_deg), gear_dims(module, teeth_2, pressure_angle_deg)
    max_tip_1 = math.hypot(d1["base_radius"], cd * math.sin(a))  # rayon de tête max de la roue 1
    max_tip_2 = math.hypot(d2["base_radius"], cd * math.sin(a))
    lim = undercut_limit(pressure_angle_deg)
    return {
        "center_distance": cd,
        "gear_1_tip_margin_mm": max_tip_1 - d1["tip_radius"], "gear_2_tip_margin_mm": max_tip_2 - d2["tip_radius"],
        "interference": d1["tip_radius"] > max_tip_1 or d2["tip_radius"] > max_tip_2,
        "undercut_limit_teeth": lim, "undercut_1": teeth_1 < lim, "undercut_2": teeth_2 < lim,
    }


def gear_warnings(teeth: int, pressure_angle_deg: float) -> list[str]:
    zmin = undercut_limit(pressure_angle_deg)
    if teeth >= zmin:
        return []
    sev = "fort" if teeth < 0.8 * zmin else "léger"
    return [f"{teeth} dents < {zmin:.1f} (limite de taillage à {pressure_angle_deg:g}°) : sous-taillage {sev} du pied, dent affaiblie ; "
            "le pied est généré par enveloppe de la crémaillère (la boucle de sous-taillage est retirée) mais un déport de profil "
            "(non géré ici) ou plus de dents est conseillé."]


def _fillet_radius(module: float, alpha: float, reduction: float) -> float:
    """Rayon de congé effectif : 0,38·m, réduit si le plat de tête de la crémaillère ne peut pas l'accueillir."""
    rho = ROOT_FILLET * module
    half_t = math.pi * module / 4 + reduction / 2
    rho_max = (half_t - 1.25 * module * math.tan(alpha)) / (1 / math.cos(alpha) - math.tan(alpha))
    return max(0.0, min(rho, 0.98 * rho_max))


def _segments_intersect(p1, p2, p3, p4):
    d1x, d1y, d2x, d2y = p2[0] - p1[0], p2[1] - p1[1], p4[0] - p3[0], p4[1] - p3[1]
    den = d1x * d2y - d1y * d2x
    if abs(den) < 1e-15:
        return None
    t = ((p3[0] - p1[0]) * d2y - (p3[1] - p1[1]) * d2x) / den
    u = ((p3[0] - p1[0]) * d1y - (p3[1] - p1[1]) * d1x) / den
    if 0.0 <= t <= 1.0 and 0.0 <= u <= 1.0:
        return (p1[0] + t * d1x, p1[1] + t * d1y)
    return None


def _remove_loops(pts: list[tuple[float, float]]) -> list[tuple[float, float]]:
    """Supprime les boucles d'une polyligne (sous-taillage : la trochoïde recoupe la développante)."""
    pts = list(pts)
    changed = True
    while changed:
        changed = False
        n = len(pts)
        for i in range(n - 1):
            for j in range(i + 2, n - 1):
                x = _segments_intersect(pts[i], pts[i + 1], pts[j], pts[j + 1])
                if x is not None:
                    pts = pts[: i + 1] + [x] + pts[j + 1:]
                    changed = True
                    break
            if changed:
                break
    return pts


def space_side(module: float, teeth: int, pressure_angle_deg: float, reduction: float = 0.0,
               flank_points: int = FLANK_POINTS, fillet_points: int = FILLET_POINTS) -> tuple[list[tuple[float, float]], float, float]:
    """Côté « + » d'un creux centré sur l'angle 0, du fond vers la tête : points (r, angle).

    Obtenu en simulant la crémaillère de référence (tête arrondie au rayon 0,38·m) qui roule sans glisser sur le cercle
    primitif : le flanc droit de la crémaillère engendre la développante, son arrondi le congé de pied (trochoïde).
    Retourne (points, demi-ouverture angulaire du plat de fond en rad, rayon de congé effectif en mm).
    """
    alpha = math.radians(pressure_angle_deg)
    rp = module * teeth / 2
    ra = rp + module
    rf = rp - 1.25 * module
    rho = _fillet_radius(module, alpha, reduction)
    xc = rf + rho
    dc = rp - xc
    uc = math.pi * module / 4 + reduction / 2 - dc * math.tan(alpha) - rho / math.cos(alpha)
    bf = math.pi / 2 - alpha

    def at(qx: float, qy: float, s: float) -> tuple[float, float]:
        return math.hypot(qx, qy), math.atan2(qy, qx) - s / rp

    pts: list[tuple[float, float]] = []
    for k in range(fillet_points + 1):
        b = bf * k / fillet_points
        yc = dc * math.tan(b)
        pts.append(at(xc - rho * math.cos(b), yc + rho * math.sin(b), yc - uc))
    # flanc rectiligne de la crémaillère : son enveloppe est la développante (avec boucle en cas de sous-taillage)
    big_l = dc / math.cos(bf) + rho  # distance de la tangence congé/flanc au centre instantané I = (rp, 0)
    mx, my = math.cos(bf), -math.sin(bf)  # normale au flanc, orientée vers l'axe
    p_t = rp * math.cos(bf)
    l_end = p_t - math.sqrt(max(p_t * p_t - rp * rp + ra * ra, 0.0))
    for k in range(1, flank_points + 1):
        ell = big_l + (l_end - big_l) * k / flank_points
        qx, qy = rp - ell * mx, -ell * my
        s = qy - (math.pi * module / 4 + reduction / 2 - (rp - qx) * math.tan(alpha))
        pts.append(at(qx, qy, s))
    cart = _remove_loops([(r * math.cos(t), r * math.sin(t)) for r, t in pts])
    polar = [(math.hypot(x, y), math.atan2(y, x)) for x, y in cart]
    return polar, (uc / rp if uc > 0 else 0.0), rho


def gear_profile(module: float, teeth: int, pressure_angle_deg: float, tooth_reduction: float = 0.0,
                 flank_points: int = FLANK_POINTS) -> list[tuple[float, float]]:
    """Contour fermé (sens trigonométrique) d'un engrenage dont une dent est centrée sur l'angle 0.

    Flancs : développante de cercle (24 points par défaut, >= 20 imposés). Pied : congé de rayon 0,38·m obtenu par enveloppe
    de la crémaillère de référence (trochoïde), sous-taillage inclus pour les petites denturées.
    tooth_reduction : amincissement de la dent mesuré sur l'arc du cercle primitif (mm) ; sert au jeu entre flancs.
    """
    if teeth < 8:
        raise GeometryError("invalid_params", "teeth doit être >= 8 (en dessous, la denture est sous-taillée).")
    if flank_points < 20:
        raise GeometryError("invalid_params", "flank_points doit être >= 20.")
    d = gear_dims(module, teeth, pressure_angle_deg)
    rb, rp, ra, rf = d["base_radius"], d["pitch_radius"], d["tip_radius"], d["root_radius"]
    alpha = math.radians(pressure_angle_deg)
    if rf <= module * 0.5:
        raise GeometryError("invalid_params", "Engrenage trop petit : le pied de dent tombe sous l'axe.")

    def half_angle(r: float) -> float:
        ar = math.acos(min(1.0, rb / r))
        return math.pi / (2 * teeth) + inv(alpha) - inv(ar) - tooth_reduction / (2 * rp)

    if half_angle(ra) <= 0.01:
        raise GeometryError("invalid_params", "Dent pointue : augmenter le nombre de dents ou réduire l'angle de pression.",
                            {"teeth": teeth, "pressure_angle": pressure_angle_deg})
    step = 2 * math.pi / teeth
    if step - 2 * half_angle(max(rb, rf)) <= 0.02:
        raise GeometryError("invalid_params", "Creux entre dents nul ou négatif (jeu trop faible ou denture trop petite).",
                            {"teeth": teeth})
    side, root_half, _ = space_side(module, teeth, pressure_angle_deg, tooth_reduction, flank_points)
    r_top, a_top = side[-1]
    tip_half = step / 2 - a_top  # demi-ouverture angulaire de la dent à la tête
    if tip_half <= 0.005:
        raise GeometryError("invalid_params", "Dent pointue : augmenter le nombre de dents ou réduire l'angle de pression.",
                            {"teeth": teeth, "pressure_angle": pressure_angle_deg})
    root_pts_n = max(0, int(math.ceil(2 * root_half / 0.06)) - 1)  # subdivisions du plat de fond
    pts: list[tuple[float, float]] = []
    for i in range(teeth):
        c = i * step
        for k in range(5):  # arc de tête de la dent i
            t = c - tip_half + 2 * tip_half * k / 4
            pts.append((r_top * math.cos(t), r_top * math.sin(t)))
        cs = c + step / 2  # axe du creux suivant
        for r, a in reversed(side):  # flanc gauche de la dent i, de la tête vers le pied
            pts.append((r * math.cos(cs - a), r * math.sin(cs - a)))
        for k in range(1, root_pts_n + 1):  # plat de fond
            t = cs - root_half + 2 * root_half * k / (root_pts_n + 1)
            pts.append((rf * math.cos(t), rf * math.sin(t)))
        for r, a in side:  # flanc droit de la dent i+1, du pied vers la tête
            pts.append((r * math.cos(cs + a), r * math.sin(cs + a)))
    clean = [pts[0]]
    for p in pts[1:]:
        if math.hypot(p[0] - clean[-1][0], p[1] - clean[-1][1]) > 1e-9:
            clean.append(p)
    if math.hypot(clean[0][0] - clean[-1][0], clean[0][1] - clean[-1][1]) < 1e-9:
        clean.pop()
    return clean


def gear_solid(cq: Any, module: float, teeth: int, pressure_angle_deg: float, thickness: float, bore: float,
               tooth_reduction: float = 0.0, keyway_width: float = 0.0, keyway_depth: float = 0.0,
               hub_diameter: float = 0.0, hub_height: float = 0.0) -> Any:
    pts = gear_profile(module, teeth, pressure_angle_deg, tooth_reduction)
    body = cq.Workplane("XY").polyline(pts).close().extrude(thickness)
    if hub_diameter > 0 and hub_height > 0:
        body = body.union(cq.Workplane("XY").circle(hub_diameter / 2).extrude(thickness + hub_height))
    if bore > 0:
        body = body.cut(cq.Workplane("XY").circle(bore / 2).extrude(thickness + hub_height + 2).translate((0, 0, -1)))
        if keyway_width > 0 and keyway_depth > 0:
            kw = cq.Workplane("XY").box(keyway_depth + bore / 2, keyway_width, thickness + hub_height + 2, centered=(False, True, False))
            body = body.cut(kw.translate((0, 0, -1)))
    return body
