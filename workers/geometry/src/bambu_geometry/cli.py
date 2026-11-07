"""Point d'entrée : une requête JSON sur stdin, une réponse JSON sur stdout, logs sur stderr."""

from __future__ import annotations

import json
import logging
import os
import sys
import traceback
from typing import Any, Callable

from .errors import GeometryError

log = logging.getLogger("bambu_geometry")


def _commands() -> dict[str, Callable[[dict[str, Any]], dict[str, Any]]]:
    from . import cad, calibration, library, parametric, step_import  # noqa: PLC0415

    return {
        "step_import": step_import.run, "parametric": parametric.run, "ping": cad.ping,
        "library_list": library.list_templates, "library_build": library.build, "library_thumbnail": library.thumbnail,
        "calibration_set": calibration.run,
    }


_real_out: Any = None


def _write(obj: dict[str, Any]) -> None:
    data = json.dumps(obj, ensure_ascii=False, allow_nan=False) + "\n"
    out = _real_out or sys.stdout
    out.write(data)
    out.flush()


def main(argv: list[str] | None = None) -> int:
    """OpenCASCADE écrit parfois sur le fd 1 (C++) : on le redirige vers stderr et on garde
    l'ancien stdout pour la seule réponse JSON."""
    global _real_out
    sys.stdout.flush()
    saved = os.dup(1)
    os.dup2(2, 1)
    _real_out = open(saved, "w", encoding="utf-8", closefd=True, newline="\n")
    try:
        return _main(argv)
    finally:
        _real_out.flush()


def _main(argv: list[str] | None) -> int:
    logging.basicConfig(stream=sys.stderr, level=logging.INFO, format="%(levelname)s %(message)s")
    argv = sys.argv[1:] if argv is None else argv
    try:
        if hasattr(sys.stdin, "reconfigure"):
            sys.stdin.reconfigure(encoding="utf-8")
        cmds = _commands()
        if len(argv) != 1 or argv[0] not in cmds:
            raise GeometryError("unknown_command", f"Commande attendue parmi {sorted(cmds)}.", {"argv": argv})
        raw = sys.stdin.read()
        try:
            req = json.loads(raw) if raw.strip() else {}
        except json.JSONDecodeError as e:
            raise GeometryError("invalid_json", "Requête JSON invalide sur stdin.", {"cause": str(e)}) from e
        if not isinstance(req, dict):
            raise GeometryError("invalid_json", "La requête doit être un objet JSON.")
        log.info("command=%s", argv[0])
        _write({"ok": True, "result": cmds[argv[0]](req)})
        return 0
    except GeometryError as e:
        _write({"ok": False, "error": e.to_dict()})
        return 1
    except Exception as e:  # noqa: BLE001
        log.error("%s", traceback.format_exc())
        _write({"ok": False, "error": {"code": "internal_error", "message": str(e), "details": {}}})
        return 2
