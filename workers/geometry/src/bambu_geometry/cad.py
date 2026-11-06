"""Accès à CadQuery/OCP (import paresseux) et tessellation."""

from __future__ import annotations

from typing import Any

import numpy as np

from .errors import GeometryError
from .meshio import MeshData


def require_cadquery(code: str) -> Any:
    try:
        import cadquery as cq  # noqa: PLC0415
    except Exception as e:  # ImportError ou échec de chargement des binaires OCP
        raise GeometryError(
            code,
            "CadQuery/OCP indisponible : installer avec `uv sync --extra cad`.",
            {"cause": repr(e)},
        ) from e
    return cq


def tessellate(shape: Any, tolerance: float, angular_tolerance: float) -> MeshData:
    verts, tris = shape.tessellate(tolerance, angular_tolerance)
    v = np.array([(p.x, p.y, p.z) for p in verts], dtype=float).reshape(-1, 3)
    t = np.array(tris, dtype=np.int64).reshape(-1, 3)
    return v, t


def mesh_stats(mesh: MeshData) -> dict[str, Any]:
    """Statistiques de contrôle de la tessellation (pas de la CAO)."""
    v, t = mesh
    if len(t) == 0:
        return {"triangles": 0, "bbox_mm": None, "open_edges": None}
    tri = v[t]
    area = 0.5 * np.linalg.norm(np.cross(tri[:, 1] - tri[:, 0], tri[:, 2] - tri[:, 0]), axis=1)
    # arêtes ouvertes : indices de sommets non fusionnés entre faces => on fusionne par position
    key = np.round(v, 6)
    _, inv = np.unique(key, axis=0, return_inverse=True)
    inv = inv.reshape(-1)
    ft = inv[t]
    edges = np.sort(np.concatenate([ft[:, [0, 1]], ft[:, [1, 2]], ft[:, [2, 0]]]), axis=1)
    _, counts = np.unique(edges, axis=0, return_counts=True)
    return {
        "triangles": int(len(t)),
        "degenerate_triangles": int((area < 1e-12).sum()),
        "open_edges": int((counts == 1).sum()),
        "watertight": bool((counts == 2).all()),
    }
