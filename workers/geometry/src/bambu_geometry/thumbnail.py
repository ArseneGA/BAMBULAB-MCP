"""Vignette SVG isométrique d'un ou plusieurs maillages (algorithme du peintre, ombrage plat, sans dépendance)."""

from __future__ import annotations

from typing import Any

import numpy as np

PALETTE = [(74, 144, 226), (233, 140, 62), (96, 176, 120), (190, 110, 190), (200, 170, 70), (110, 190, 200)]
MAX_TRIANGLES = 40000


def render_svg(named_meshes: list[tuple[str, Any]], size: int = 256) -> str:
    """named_meshes : [(nom, (vertices Nx3, triangles Mx3))]. Retourne le texte SVG (vue de dessus-côté, Z vers le haut)."""
    tri_list: list[np.ndarray] = []
    colors: list[tuple[int, int, int]] = []
    for i, (_, (v, t)) in enumerate(named_meshes):
        if len(t) == 0:
            continue
        tri_list.append(v[t])
        colors.extend([PALETTE[i % len(PALETTE)]] * len(t))
    if not tri_list:
        return f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {size} {size}"></svg>'
    tris = np.concatenate(tri_list)  # (M,3,3)
    col = np.array(colors, dtype=float)
    if len(tris) > MAX_TRIANGLES:  # garde-fou : on garde un échantillon régulier (vignette seulement)
        idx = np.linspace(0, len(tris) - 1, MAX_TRIANGLES).astype(int)
        tris, col = tris[idx], col[idx]

    eye = np.array([1.0, -1.25, 0.95])
    w = eye / np.linalg.norm(eye)
    u = np.cross([0.0, 0.0, 1.0], w)
    u /= np.linalg.norm(u)
    vv = np.cross(w, u)
    sx = tris @ u
    sy = -(tris @ vv)
    depth = (tris @ w).mean(axis=1)
    n = np.cross(tris[:, 1] - tris[:, 0], tris[:, 2] - tris[:, 0])
    ln = np.linalg.norm(n, axis=1)
    ln[ln == 0] = 1.0
    n = n / ln[:, None]
    flip = (n @ w) < 0
    n[flip] *= -1
    light = np.array([0.35, -0.5, 0.85])
    light /= np.linalg.norm(light)
    inten = 0.38 + 0.62 * np.clip(n @ light, 0, 1)

    minx, maxx, miny, maxy = sx.min(), sx.max(), sy.min(), sy.max()
    span = max(maxx - minx, maxy - miny, 1e-9)
    pad = size * 0.08
    scale = (size - 2 * pad) / span
    ox = pad + ((size - 2 * pad) - (maxx - minx) * scale) / 2
    oy = pad + ((size - 2 * pad) - (maxy - miny) * scale) / 2
    px = (sx - minx) * scale + ox
    py = (sy - miny) * scale + oy

    order = np.argsort(depth)  # loin -> près
    out = [f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {size} {size}" width="{size}" height="{size}" '
           f'role="img" aria-label="Vignette">']
    for i in order:
        c = np.clip(col[i] * inten[i], 0, 255).astype(int)
        fill = f"#{c[0]:02x}{c[1]:02x}{c[2]:02x}"
        pts = " ".join(f"{px[i, k]:.1f},{py[i, k]:.1f}" for k in range(3))
        out.append(f'<polygon points="{pts}" fill="{fill}" stroke="{fill}" stroke-width="0.4"/>')
    out.append("</svg>")
    return "".join(out)
