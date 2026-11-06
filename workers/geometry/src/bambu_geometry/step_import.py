"""Commande `step_import` : STEP -> STL binaire + 3MF + métadonnées."""

from __future__ import annotations

import re
from pathlib import Path
from typing import Any

from .cad import mesh_stats, require_cadquery, tessellate
from .errors import GeometryError
from .meshio import sha256_file, write_3mf, write_binary_stl

_UNIT_RE = re.compile(rb"CONVERSION_BASED_UNIT\s*\(\s*'([^']+)'|SI_UNIT\s*\(\s*(\.\w+\.)\s*,\s*\.METRE\.")


def _detect_source_unit(path: Path) -> str:
    """Lit l'unité de longueur déclarée (best effort sur l'en-tête DATA)."""
    try:
        head = path.read_bytes()[:200_000]
    except OSError:
        return "unknown"
    for m in _UNIT_RE.finditer(head):
        if m.group(1):
            return m.group(1).decode("ascii", "replace").lower()
        if m.group(2):
            prefix = m.group(2).decode().strip(".").lower()
            return {"milli": "mm", "centi": "cm", "": "m"}.get(prefix, f"{prefix}metre")
    # SI_UNIT(*,.METRE.) sans préfixe
    if re.search(rb"SI_UNIT\s*\(\s*\*\s*,\s*\.METRE\.", head):
        return "m"
    return "unknown"


def run(req: dict[str, Any]) -> dict[str, Any]:
    path = req.get("path")
    out_dir = req.get("out_dir")
    if not isinstance(path, str) or not isinstance(out_dir, str):
        raise GeometryError("invalid_params", "`path` et `out_dir` (chaînes) sont requis.")
    tol = float(req.get("tolerance_mm", 0.05))
    ang = float(req.get("angular_tolerance_rad", 0.2))
    if tol <= 0 or ang <= 0:
        raise GeometryError("invalid_params", "tolerance_mm et angular_tolerance_rad doivent être > 0.")
    src = Path(path)
    if not src.is_file():
        raise GeometryError("file_not_found", f"Fichier introuvable : {path}")
    cq = require_cadquery("step_backend_missing")

    try:
        wp = cq.importers.importStep(str(src))
        shapes = wp.solids().vals() or wp.vals()
    except Exception as e:
        raise GeometryError("corrupt_file", "STEP illisible ou invalide.", {"cause": repr(e)}) from e
    if not shapes:
        raise GeometryError("corrupt_file", "Aucune géométrie dans le STEP.")

    out = Path(out_dir)
    out.mkdir(parents=True, exist_ok=True)
    stem = src.stem
    objects = []
    meshes = []
    for i, s in enumerate(shapes):
        mesh = tessellate(s, tol, ang)
        meshes.append((f"{stem}_{i + 1}", mesh))
        bb = s.BoundingBox()
        objects.append(
            {
                "index": i,
                "name": f"{stem}_{i + 1}",
                "volume_mm3": float(s.Volume()),
                "area_mm2": float(s.Area()),
                "bbox_mm": {
                    "min": [bb.xmin, bb.ymin, bb.zmin],
                    "max": [bb.xmax, bb.ymax, bb.zmax],
                    "size": [bb.xlen, bb.ylen, bb.zlen],
                },
                "tessellation": mesh_stats(mesh),
            }
        )
    stl_path = out / f"{stem}.stl"
    mf_path = out / f"{stem}.3mf"
    n_tri = write_binary_stl(stl_path, [m for _, m in meshes], name=stem)
    write_3mf(mf_path, meshes)
    srcunit = _detect_source_unit(src)
    return {
        "source": {"path": str(src), "sha256": sha256_file(src), "size_bytes": src.stat().st_size},
        "units": {
            "output": "mm",
            "source_declared": srcunit,
            "note": "OpenCASCADE convertit en millimètres à la lecture ; `source_declared` est lu "
            "dans l'en-tête (best effort) à titre de contrôle.",
        },
        "tessellation": {"tolerance_mm": tol, "angular_tolerance_rad": ang, "triangles": n_tri},
        "objects": objects,
        "files": {"stl": str(stl_path), "3mf": str(mf_path)},
        "level": "calculé",
        "assumptions": [
            "STL/3MF dérivés par tessellation : approximation de la CAO, l'original STEP reste la référence.",
            "STL en un seul fichier (tous les solides concaténés) ; le 3MF garde un objet par solide.",
        ],
    }
