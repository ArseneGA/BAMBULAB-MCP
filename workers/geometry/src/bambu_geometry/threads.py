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
SAMPLES = 72  # points par tour : multiple de 12 pour que les arêtes du profil tombent sur des échantillons


def nominal(size: str) -> float:
    return float(size[1:])


def thread_depth(pitch: float) -> float:
    """Profondeur du filet (mm) : 0,6134·P (profil ISO tronqué)."""
    return 0.6134 * pitch


def _tri(phase: float) -> float:
    return 1 - abs(2 * phase - 1)  # 0 -> 1 -> 0 sur une période


def thread_section(major_d: float, pitch: float, samples: int = SAMPLES) -> list[tuple[float, float]]:
    """Contour r(θ) : crête plate 1/6 du pas, fond plat 1/6, flancs à ~60°. Crête à θ = π (phase 0,5)."""
    depth = thread_depth(pitch)
    pts = []
    for i in range(samples):
        th = 2 * math.pi * i / samples
        f = min(1.0, max(0.0, 1.5 * _tri(i / samples) - 0.25))
        r = major_d / 2 - depth + depth * f
        pts.append((r * math.cos(th), r * math.sin(th)))
    return pts


def threaded_rod(cq: Any, major_d: float, pitch: float, length: float, z0: float = 0.0) -> Any:
    """Tige filetée à droite de diamètre extérieur major_d sur [z0, z0+length]."""
    w = cq.Workplane("XY").polyline(thread_section(major_d, pitch)).close().twistExtrude(length, 360.0 * length / pitch)
    return w.translate((0, 0, z0)) if z0 else w


def hex_prism(cq: Any, across_flats: float, height: float, z0: float = 0.0, chamfer: float = 0.0) -> Any:
    across_corners = across_flats / math.cos(math.radians(30))
    h = cq.Workplane("XY").polygon(6, across_corners).extrude(height)
    if chamfer > 0:
        h = h.faces(">Z").chamfer(chamfer)
    return h.translate((0, 0, z0)) if z0 else h
