"""Filetages métriques imprimables (vis à droite) : section transversale tordue le long de l'axe (twistExtrude).

À chaque hauteur z, la section d'un filetage hélicoïdal est le même contour r(θ) tourné de 360·z/pas degrés : on construit
donc le contour une fois et on le vrille. Résultat exact (hélice continue), sans balayage de profil.
"""

from __future__ import annotations

import math
from typing import Any

# Pas ISO « gros » (mm) et dimensions de tête/écrou hexagonaux (ouverture sur plats, mm).
COARSE_PITCH = {"M3": 0.5, "M4": 0.7, "M5": 0.8, "M6": 1.0, "M8": 1.25, "M10": 1.5, "M12": 1.75}
ACROSS_FLATS = {"M3": 5.5, "M4": 7.0, "M5": 8.0, "M6": 10.0, "M8": 13.0, "M10": 16.0, "M12": 18.0}
SIZES = tuple(COARSE_PITCH)
SAMPLES = 120  # échantillons réguliers par tour (3°), en plus des ruptures de pente du profil


def nominal(size: str) -> float:
    return float(size[1:])


def thread_depth(pitch: float) -> float:
    """Profondeur du filet externe (mm) : h3 = 0,6134·P (ISO 68-1 / ISO 724, profil de base 60°)."""
    return 0.6134 * pitch


# Profil ISO de base (60°) tel que modélisé : crête plate P/8, flancs à 30° de part et d'autre de la normale à l'axe,
# fond plat (le congé de fond ISO est remplacé par un plat, plus simple à imprimer). Largeurs en fraction du pas :
#   crête 1/8 ; fond = 1 - 1/8 - 2·h3·tan(30°)/P ≈ 1/6 ; flanc axial = h3·tan(30°) ≈ 0,354·P.
CREST_FLAT = 1 / 8
FLANK_AXIAL = 0.6134 * math.tan(math.radians(30.0))  # fraction du pas (ne dépend pas du pas)
ROOT_FLAT = 1 - CREST_FLAT - 2 * FLANK_AXIAL


def profile_fraction(s: float) -> float:
    """Hauteur relative (0 = fond, 1 = crête) du profil à la phase s (fraction de pas) ; crête centrée sur s = 0,5."""
    half = abs((s % 1.0) - 0.5)
    if half <= CREST_FLAT / 2:
        return 1.0
    if half >= CREST_FLAT / 2 + FLANK_AXIAL:
        return 0.0
    return 1.0 - (half - CREST_FLAT / 2) / FLANK_AXIAL


def thread_section(major_d: float, pitch: float, samples: int = SAMPLES) -> list[tuple[float, float]]:
    """Contour r(θ) : profil ISO de crête plate P/8 et flancs à 60° inclus, crête à θ = π (phase 0,5).

    Les ruptures de pente (début/fin de crête, de flanc) sont des points imposés du contour, complétés par des
    échantillons réguliers : la fidélité du profil ne dépend pas du nombre d'échantillons.
    """
    depth = thread_depth(pitch)
    breaks = {0.5 - CREST_FLAT / 2, 0.5 + CREST_FLAT / 2, 0.5 - CREST_FLAT / 2 - FLANK_AXIAL,
              0.5 + CREST_FLAT / 2 + FLANK_AXIAL, 0.0}
    phases = sorted({round(i / samples, 9) for i in range(samples)} | {round(b, 9) for b in breaks})
    pts = []
    for ph in phases:
        th = 2 * math.pi * ph
        r = major_d / 2 - depth + depth * profile_fraction(ph)
        pts.append((r * math.cos(th), r * math.sin(th)))
    return pts


def auto_samples(major_d: float, sagitta: float = 0.012) -> int:
    """Échantillons par tour tels que la flèche des cordes de la section reste <= sagitta (0,012 mm) au rayon extérieur."""
    r = max(major_d / 2, 0.5)
    step = 2 * math.acos(max(0.0, 1 - sagitta / r))
    return max(24, int(math.ceil(2 * math.pi / step)))


def threaded_rod(cq: Any, major_d: float, pitch: float, length: float, z0: float = 0.0) -> Any:
    """Tige filetée à droite de diamètre extérieur major_d sur [z0, z0+length]."""
    w = (cq.Workplane("XY").polyline(thread_section(major_d, pitch, auto_samples(major_d))).close()
         .twistExtrude(length, 360.0 * length / pitch))
    return w.translate((0, 0, z0)) if z0 else w


def bolt_tip_chamfer(cq: Any, rod: Any, major_d: float, pitch: float, z_end: float) -> Any:
    """Chanfrein d'entrée à 45° au bout de la vis (z = z_end) : part du diamètre de fond et rejoint le diamètre extérieur."""
    r_major, r_minor = major_d / 2, major_d / 2 - thread_depth(pitch)
    big = r_major + 0.5
    span = big - r_minor
    ring = (cq.Workplane("XZ").polyline([(r_minor, z_end + 0.01), (big, z_end + 0.01), (big, z_end - span)]).close()
            .revolve(360.0, (0, 0, 0), (0, 1, 0)))
    return rod.cut(ring)


def nut_entry_chamfer(cq: Any, nut: Any, hole_r_crest: float, major_r: float, height: float) -> Any:
    """Fraisure d'entrée à 45° aux deux faces de l'écrou : du diamètre extérieur du filet (+0,2 mm) au diamètre intérieur."""
    top_r = major_r + 0.2
    inner_r = hole_r_crest - 0.1  # le cône dépasse le fond du filet : évite une tangence avec le cylindre des crêtes internes
    depth = top_r - inner_r
    for z_face, sign in ((0.0, 1.0), (height, -1.0)):
        cone = (cq.Workplane("XZ").polyline([(0, z_face - sign * 0.01), (top_r, z_face - sign * 0.01),
                                              (inner_r, z_face + sign * depth), (0, z_face + sign * depth)]).close()
                .revolve(360.0, (0, 0, 0), (0, 1, 0)))
        nut = nut.cut(cone)
    return nut


def thread_tessellation(major_d: float) -> tuple[float, float]:
    """(tolérance linéaire mm, angulaire rad) pour la tessellation d'une pièce filetée.

    Flèche linéaire <= 0,02 mm (le gabarit générique est à 0,1 mm : un filet de 0,3 à 0,6 mm de haut y disparaît).
    L'angle est volontairement lâche (0,5 rad) : sous 0,2 rad OCC raffine les faces hélicoïdales jusqu'à 800 000 triangles
    pour 14 mm de filet (40 Mo de STL) sans gain de fidélité, la flèche étant déjà bornée par la tolérance linéaire.
    """
    return 0.02, 0.5


def measure_thread_depth(mesh: tuple[Any, Any], z_lo: float, z_hi: float, planes: int = 4) -> float:
    """Hauteur de filet mesurée SUR UN MAILLAGE (sommets Nx3, triangles Mx3) : coupe par des demi-plans contenant l'axe Z,
    écart radial max-min des points de coupe pour z dans [z_lo, z_hi] (au moins 2 pas) ; minimum sur `planes` demi-plans."""
    import numpy as np  # noqa: PLC0415

    v, t = mesh
    tri = v[t]  # (M, 3, 3)
    best = math.inf
    for k in range(planes):
        psi = math.pi * k / planes * 2 + 0.1
        ux, uy = math.cos(psi), math.sin(psi)
        d = -uy * tri[:, :, 0] + ux * tri[:, :, 1]  # distance signée au plan
        u = ux * tri[:, :, 0] + uy * tri[:, :, 1]
        radii = []
        for a, b in ((0, 1), (1, 2), (2, 0)):
            da, db = d[:, a], d[:, b]
            m = (da * db) < 0
            if not m.any():
                continue
            f = da[m] / (da[m] - db[m])
            uu = u[m, a] + f * (u[m, b] - u[m, a])
            zz = tri[m, a, 2] + f * (tri[m, b, 2] - tri[m, a, 2])
            sel = (uu > 0) & (zz >= z_lo) & (zz <= z_hi)
            radii.append(uu[sel])
        r = np.concatenate(radii) if radii else np.zeros(0)
        if len(r):
            best = min(best, float(r.max() - r.min()))
    return best


def hex_prism(cq: Any, across_flats: float, height: float, z0: float = 0.0, chamfer: float = 0.0) -> Any:
    across_corners = across_flats / math.cos(math.radians(30))
    h = cq.Workplane("XY").polygon(6, across_corners).extrude(height)
    if chamfer > 0:
        h = h.faces(">Z").chamfer(chamfer)
    return h.translate((0, 0, z0)) if z0 else h
