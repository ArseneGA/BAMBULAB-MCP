"""Résumé déterministe d'un gabarit (paramètres par défaut) pour les golden files.

Partagé par `tests/test_golden.py` et `scripts/regen_golden.py`. Aucun STL n'est conservé : seul un résumé léger
(volume, aire, boîte englobante, composants, étanchéité, empreinte arrondie) est stocké dans `tests/golden/<id>.json`.
"""

from __future__ import annotations

import hashlib
import json
import struct
import tempfile
from pathlib import Path
from typing import Any

import numpy as np

from bambu_geometry import library

GOLDEN_DIR = Path(__file__).parent / "golden"


def read_binary_stl(path: Path) -> np.ndarray:
    raw = path.read_bytes()
    (n,) = struct.unpack_from("<I", raw, 80)
    dt = np.dtype([("n", "<f4", 3), ("v", "<f4", (3, 3)), ("a", "<u2")])
    return np.frombuffer(raw, dtype=dt, count=n, offset=84)["v"].astype(np.float64)  # (n, 3, 3)


def _components(tris: np.ndarray) -> int:
    """Composants connexes (sommets soudés à 1e-4 mm)."""
    keys = np.round(tris.reshape(-1, 3) / 1e-4).astype(np.int64)
    _, inv = np.unique(keys, axis=0, return_inverse=True)
    inv = inv.reshape(-1, 3)
    parent = list(range(int(inv.max()) + 1))

    def find(a: int) -> int:
        while parent[a] != a:
            parent[a] = parent[parent[a]]
            a = parent[a]
        return a

    for a, b, c in inv:
        ra, rb, rc = find(int(a)), find(int(b)), find(int(c))
        parent[rb] = ra
        parent[find(rc)] = ra
    return len({find(int(v)) for v in inv[:, 0]})


def summarize(tpl_id: str) -> dict[str, Any]:
    with tempfile.TemporaryDirectory() as td:
        res = library.build({"template": tpl_id, "out_dir": td, "name": "g", "thumbnail": False})
        tris = read_binary_stl(Path(td) / "g.stl")
    cross = np.cross(tris[:, 1] - tris[:, 0], tris[:, 2] - tris[:, 0])
    area = float(np.linalg.norm(cross, axis=1).sum() / 2)
    parts = res["parts"]
    volume = float(sum(p["volume_mm3"] for p in parts))
    size = [float(x) for x in res["bbox_mm"]["size"]]
    summary = {
        "template": tpl_id,
        "template_version": res["template_version"],
        "volume_mm3": round(volume, 3),
        "area_mm2": round(area, 3),
        "bbox_size_mm": [round(x, 3) for x in size],
        "parts": len(parts),
        "solids": int(sum(p["solids"] for p in parts)),
        "components": _components(tris),
        "watertight": all(p["tessellation"]["watertight"] for p in parts),
    }
    key = json.dumps([round(volume, 0), round(area, 0), [round(x, 0) for x in size], summary["solids"]])
    summary["fingerprint"] = hashlib.sha256(key.encode()).hexdigest()[:16]
    return summary
