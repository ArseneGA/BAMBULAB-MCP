"""Engrenages droits à denture en développante de cercle (profil polygonal exact aux points de calcul)."""

from __future__ import annotations

import math
from typing import Any

from .errors import GeometryError


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


def gear_profile(module: float, teeth: int, pressure_angle_deg: float, tooth_reduction: float = 0.0,
                 flank_points: int = 7) -> list[tuple[float, float]]:
    """Contour fermé (sens trigonométrique) d'un engrenage dont une dent est centrée sur l'angle 0.

    tooth_reduction : amincissement de la dent mesuré sur l'arc du cercle primitif (mm) ; sert au jeu entre flancs.
    """
    if teeth < 8:
        raise GeometryError("invalid_params", "teeth doit être >= 8 (en dessous, la denture est sous-taillée).")
    d = gear_dims(module, teeth, pressure_angle_deg)
    rb, rp, ra, rf = d["base_radius"], d["pitch_radius"], d["tip_radius"], d["root_radius"]
    alpha = math.radians(pressure_angle_deg)
    if rf <= module * 0.5:
        raise GeometryError("invalid_params", "Engrenage trop petit : le pied de dent tombe sous l'axe.")

    def half_angle(r: float) -> float:
        # demi-épaisseur angulaire de la dent à la distance r de l'axe (r >= rb)
        ar = math.acos(min(1.0, rb / r))
        return math.pi / (2 * teeth) + inv(alpha) - inv(ar) - tooth_reduction / (2 * rp)

    r0 = max(rb, rf)
    if half_angle(ra) <= 0.01:
        raise GeometryError("invalid_params", "Dent pointue : augmenter le nombre de dents ou réduire l'angle de pression.",
                            {"teeth": teeth, "pressure_angle": pressure_angle_deg})
    step = 2 * math.pi / teeth
    if step - 2 * half_angle(r0) <= 0.02:
        raise GeometryError("invalid_params", "Creux entre dents nul ou négatif (jeu trop faible ou denture trop petite).",
                            {"teeth": teeth})
    flank_r = [r0 + (ra - r0) * (k / (flank_points - 1)) ** 0.85 for k in range(flank_points)]
    pts: list[tuple[float, float]] = []
    for i in range(teeth):
        c = i * step
        phi0 = half_angle(r0)
        # pied -> flanc droit (angle -phi) -> sommet -> flanc gauche (+phi) -> pied
        if rf < rb:
            pts.append((rf * math.cos(c - phi0), rf * math.sin(c - phi0)))
        for r in flank_r:
            t = c - half_angle(r)
            pts.append((r * math.cos(t), r * math.sin(t)))
        ph_tip = half_angle(ra)
        for k in (1, 2):  # arc de tête
            t = c - ph_tip + 2 * ph_tip * k / 3
            pts.append((ra * math.cos(t), ra * math.sin(t)))
        for r in reversed(flank_r):
            t = c + half_angle(r)
            pts.append((r * math.cos(t), r * math.sin(t)))
        if rf < rb:
            pts.append((rf * math.cos(c + phi0), rf * math.sin(c + phi0)))
        # arc de pied jusqu'à la dent suivante
        t0, t1 = c + phi0, (i + 1) * step - phi0
        for k in (1, 2):
            t = t0 + (t1 - t0) * k / 3
            pts.append((rf * math.cos(t), rf * math.sin(t)))
    return pts


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
