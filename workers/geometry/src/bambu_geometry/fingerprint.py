"""Empreinte du code du worker : permet à la mise à jour automatique de vérifier que le paquet RÉELLEMENT importé (installé non
éditable hors du dossier du worker) est bien celui de la version installée. DOIT rester identique à upgrade.WorkerFingerprint
(internal/upgrade/worker.go) : SHA-256 de, pour chaque fichier .py (hors __pycache__) classé par chemin relatif en « / », le
chemin, un octet nul, le contenu, un octet nul."""

from __future__ import annotations

import hashlib
from pathlib import Path


def code_fingerprint(package_dir: Path | None = None) -> str:
    root = Path(package_dir) if package_dir is not None else Path(__file__).resolve().parent
    files = sorted(
        (p.relative_to(root).as_posix(), p)
        for p in root.rglob("*.py")
        if "__pycache__" not in p.relative_to(root).parts
    )
    h = hashlib.sha256()
    for rel, p in files:
        h.update(rel.encode("utf-8"))
        h.update(b"\0")
        h.update(p.read_bytes())
        h.update(b"\0")
    return h.hexdigest()
