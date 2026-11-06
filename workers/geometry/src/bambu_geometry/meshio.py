"""Écriture STL binaire et 3MF (sans dépendance autre que numpy)."""

from __future__ import annotations

import hashlib
import struct
import zipfile
from pathlib import Path
from xml.sax.saxutils import quoteattr

import numpy as np

MeshData = tuple[np.ndarray, np.ndarray]  # (vertices Nx3 float, triangles Mx3 int)


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def write_binary_stl(path: Path, meshes: list[MeshData], name: str = "bambu_geometry") -> int:
    """Écrit toutes les mailles dans un seul STL binaire. Retourne le nb de triangles."""
    tris = []
    for v, t in meshes:
        if len(t):
            tris.append(v[t])
    arr = np.concatenate(tris) if tris else np.zeros((0, 3, 3))
    n = len(arr)
    e1 = arr[:, 1] - arr[:, 0]
    e2 = arr[:, 2] - arr[:, 0]
    normals = np.cross(e1, e2)
    ln = np.linalg.norm(normals, axis=1)
    ln[ln == 0] = 1.0
    normals = normals / ln[:, None]
    rec = np.zeros(n, dtype=[("n", "<f4", 3), ("v", "<f4", (3, 3)), ("a", "<u2")])
    rec["n"] = normals
    rec["v"] = arr
    with open(path, "wb") as f:
        f.write(name.encode("ascii", "replace")[:80].ljust(80, b" "))
        f.write(struct.pack("<I", n))
        f.write(rec.tobytes())
    return n


def write_3mf(path: Path, objects: list[tuple[str, MeshData]]) -> None:
    """3MF minimal : un objet par solide, unité millimètre."""
    res = []
    items = []
    for i, (name, (v, t)) in enumerate(objects, start=1):
        vs = "".join(f'<vertex x="{x:.6g}" y="{y:.6g}" z="{z:.6g}"/>' for x, y, z in v)
        ts = "".join(f'<triangle v1="{a}" v2="{b}" v3="{c}"/>' for a, b, c in t)
        res.append(
            f'<object id="{i}" name={quoteattr(name)} type="model"><mesh>'
            f"<vertices>{vs}</vertices><triangles>{ts}</triangles></mesh></object>"
        )
        items.append(f'<item objectid="{i}"/>')
    model = (
        '<?xml version="1.0" encoding="UTF-8"?>'
        '<model unit="millimeter" xml:lang="en-US" '
        'xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02">'
        f'<metadata name="Application">bambu_geometry</metadata>'
        f'<resources>{"".join(res)}</resources><build>{"".join(items)}</build></model>'
    )
    ct = (
        '<?xml version="1.0" encoding="UTF-8"?>'
        '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">'
        '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>'
        '<Default Extension="model" ContentType="application/vnd.ms-package.3dmanufacturing-3dmodel+xml"/>'
        "</Types>"
    )
    rels = (
        '<?xml version="1.0" encoding="UTF-8"?>'
        '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
        '<Relationship Target="/3D/3dmodel.model" Id="rel0" '
        'Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/></Relationships>'
    )
    with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED) as z:
        z.writestr("[Content_Types].xml", ct)
        z.writestr("_rels/.rels", rels)
        z.writestr("3D/3dmodel.model", model)
