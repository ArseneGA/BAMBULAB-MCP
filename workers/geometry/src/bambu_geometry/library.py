"""Bibliothèque de gabarits paramétriques versionnés (commandes `library_list`, `library_build`, `library_thumbnail`).

Chaque gabarit déclare un schéma de paramètres typé (unité, bornes, défaut, description) : le schéma est la source
de vérité pour la validation, l'affichage (web) et la description donnée à Claude. Tous les paramètres ont un défaut,
ce qui permet de générer une vignette et un exemple pour chaque gabarit.

Les calculs d'ingénierie (jeux, compensation de trous, inserts) sont faits côté Go (internal/engineering,
éventuellement calibrés) : le champ `auto` d'un paramètre indique comment Go le calcule quand l'appelant ne le
fournit pas. Le worker, lui, construit la géométrie exacte pour les valeurs reçues.
"""

from __future__ import annotations

import hashlib
import json
import math
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable

from .cad import mesh_stats, require_cadquery, tessellate
from .errors import GeometryError
from .meshio import write_binary_stl
from .parametric import NAME_RE, Part
from .thumbnail import render_svg

Builder = Callable[[Any, dict[str, Any], dict[str, Any]], list[Part]]


@dataclass(frozen=True)
class P:
    """Un paramètre typé d'un gabarit."""

    name: str
    type: str  # number | integer | enum | boolean
    default: Any
    description: str
    unit: str = ""
    min: float | None = None
    max: float | None = None
    options: tuple[str, ...] = ()
    auto: str = ""  # règle de calcul côté Go (voir internal/library/rules.go), vide si aucune

    def schema(self) -> dict[str, Any]:
        d: dict[str, Any] = {"name": self.name, "type": self.type, "default": self.default, "description": self.description}
        if self.unit:
            d["unit"] = self.unit
        if self.min is not None:
            d["min"] = self.min
        if self.max is not None:
            d["max"] = self.max
        if self.options:
            d["options"] = list(self.options)
        if self.auto:
            d["auto"] = self.auto
        return d

    def coerce(self, raw: Any) -> Any:
        if self.type == "boolean":
            if not isinstance(raw, bool):
                raise GeometryError("invalid_params", f"{self.name} doit être un booléen.")
            return raw
        if self.type == "enum":
            if raw not in self.options:
                raise GeometryError("invalid_params", f"{self.name} doit valoir l'une des options {list(self.options)}.",
                                    {"value": raw})
            return raw
        if isinstance(raw, bool) or not isinstance(raw, (int, float)) or not math.isfinite(raw):
            raise GeometryError("invalid_params", f"{self.name} doit être un nombre fini.", {"value": raw})
        if self.type == "integer":
            if raw != int(raw):
                raise GeometryError("invalid_params", f"{self.name} doit être un entier.", {"value": raw})
            raw = int(raw)
        else:
            raw = float(raw)
        if self.min is not None and raw < self.min:
            raise GeometryError("invalid_params", f"{self.name} doit être >= {self.min:g} {self.unit}.", {"value": raw})
        if self.max is not None and raw > self.max:
            raise GeometryError("invalid_params", f"{self.name} doit être <= {self.max:g} {self.unit}.", {"value": raw})
        return raw


@dataclass
class Template:
    id: str
    title: str
    category: str
    description: str
    params: list[P]
    build: Builder
    version: str = "1.0.0"
    tags: list[str] = field(default_factory=list)
    tess: tuple[float, float] = (0.02, 0.1)  # (tolérance linéaire mm, tolérance angulaire rad) de la tessellation STL
    summary: str = ""  # une phrase pour les listes

    def schema(self) -> dict[str, Any]:
        params = [p.schema() for p in self.params]
        digest = hashlib.sha256(json.dumps(params, sort_keys=True, ensure_ascii=False).encode()).hexdigest()[:12]
        return {
            "id": self.id,
            "version": self.version,
            "schema_hash": digest,
            "title": self.title,
            "category": self.category,
            "summary": self.summary or self.description.split(". ")[0],
            "description": self.description,
            "tags": self.tags,
            "params": params,
        }

    def resolve(self, raw: dict[str, Any]) -> dict[str, Any]:
        if not isinstance(raw, dict):
            raise GeometryError("invalid_params", "`params` doit être un objet.")
        known = {p.name: p for p in self.params}
        unknown = sorted(set(raw) - set(known))
        if unknown:
            raise GeometryError("invalid_params", f"Paramètre(s) inconnu(s) pour {self.id} : {unknown}",
                                {"known": sorted(known)})
        out: dict[str, Any] = {}
        for p in self.params:
            out[p.name] = p.coerce(raw[p.name]) if p.name in raw else p.default
        return out


REGISTRY: dict[str, Template] = {}


def register(t: Template) -> Template:
    if t.id in REGISTRY:
        raise RuntimeError(f"gabarit en double : {t.id}")
    REGISTRY[t.id] = t
    return t


def _load() -> dict[str, Template]:
    from . import lib_templates  # noqa: PLC0415, F401  (enregistre les gabarits)

    return REGISTRY


def get_template(tpl_id: Any) -> Template:
    reg = _load()
    if not isinstance(tpl_id, str) or tpl_id not in reg:
        raise GeometryError("invalid_params", f"gabarit inconnu : {tpl_id!r}", {"available": sorted(reg)})
    return reg[tpl_id]


# --- commandes -------------------------------------------------------------------------------


def list_templates(_req: dict[str, Any]) -> dict[str, Any]:
    """Catalogue complet des gabarits et de leurs schémas (ne requiert pas CadQuery)."""
    reg = _load()
    return {"templates": [reg[k].schema() for k in sorted(reg)], "count": len(reg)}


def _emit(cq: Any, tpl: Template, params: dict[str, Any], parts: list[Part], ctx: dict[str, Any],
          out_dir: Any, name: Any, nozzle: float, thumbnail: bool) -> dict[str, Any]:
    if not isinstance(name, str) or not NAME_RE.fullmatch(name) or name in (".", ".."):
        raise GeometryError("invalid_params", "`name` doit correspondre à ^[A-Za-z0-9._-]{1,64}$.")
    if not isinstance(out_dir, str):
        raise GeometryError("invalid_params", "`out_dir` (chaîne) est requis.")
    out = Path(out_dir)
    out.mkdir(parents=True, exist_ok=True)
    out_root = out.resolve()
    stl_path, step_path, json_path, svg_path = (out_root / f"{name}.{ext}" for ext in ("stl", "step", "source.json", "thumb.svg"))
    for target in (stl_path, step_path, json_path, svg_path):
        if target.resolve().parent != out_root:
            raise GeometryError("invalid_params", "Sortie hors de out_dir refusée.", {"path": str(target)})
    shapes = [wp.val() for _, wp in parts]
    compound = cq.Compound.makeCompound(shapes)
    meshes = [tessellate(s, *tpl.tess) for s in shapes]
    write_binary_stl(stl_path, meshes, name=name)
    cq.exporters.export(cq.Workplane(obj=compound), str(step_path))
    bb = compound.BoundingBox()
    info_parts = []
    for (n, wp), m in zip(parts, meshes):
        v = wp.val()
        st = mesh_stats(m)
        info_parts.append({"name": n, "volume_mm3": float(v.Volume()), "solids": len(wp.solids().vals()),
                           "valid": bool(v.isValid()), "tessellation": st})
        if not st.get("watertight"):
            ctx["warnings"].append(f"Pièce {n} : maillage non étanche (arêtes ouvertes : {st.get('open_edges')}).")
    for key in ("wall", "thickness"):
        if isinstance(params.get(key), (int, float)) and not isinstance(params[key], bool) and params[key] < 2 * nozzle:
            ctx["warnings"].append(f"{key} < 2 × buse ({2 * nozzle:g} mm) : moins de 2 périmètres.")
    files = {"stl": str(stl_path), "step": str(step_path), "source": str(json_path)}
    if thumbnail:
        size = max(bb.xlen, bb.ylen, bb.zlen, 1.0)
        coarse = [(n, tessellate(sh, max(tpl.tess[0], size / 120), max(tpl.tess[1], 0.5))) for (n, _), sh in zip(parts, shapes)]
        svg_path.write_text(render_svg(coarse), encoding="utf-8")
        files["thumbnail"] = str(svg_path)
    json_path.write_text(
        json.dumps({"schema": 2, "template": tpl.id, "template_version": tpl.version, "params": params,
                    "nozzle_diameter": nozzle, "assumptions": ctx["assumptions"]}, indent=2, ensure_ascii=False),
        encoding="utf-8",
    )
    return {
        "template": tpl.id,
        "template_version": tpl.version,
        "params": params,
        "parts": info_parts,
        "bbox_mm": {"size": [bb.xlen, bb.ylen, bb.zlen], "min": [bb.xmin, bb.ymin, bb.zmin]},
        "derived": ctx["derived"],
        "assumptions": ctx["assumptions"],
        "warnings": ctx["warnings"],
        "files": files,
        "level": "calculé",
        "validity": "Géométrie exacte pour les paramètres donnés ; aucune vérification mécanique (voir engineering).",
    }


def build(req: dict[str, Any]) -> dict[str, Any]:
    """`library_build` : {template, params, out_dir, name?, nozzle_diameter?, thumbnail?}."""
    tpl = get_template(req.get("template"))
    params = tpl.resolve(req.get("params", {}))
    nozzle = req.get("nozzle_diameter", 0.4)
    if isinstance(nozzle, bool) or not isinstance(nozzle, (int, float)) or not math.isfinite(nozzle) or nozzle <= 0:
        raise GeometryError("invalid_params", "nozzle_diameter doit être un nombre > 0.")
    cq = require_cadquery("cad_backend_missing")
    ctx: dict[str, Any] = {"assumptions": [], "warnings": [], "derived": {}}
    try:
        parts = tpl.build(cq, params, ctx)
    except GeometryError:
        raise
    except Exception as e:
        raise GeometryError("cad_failure", "Échec de construction CadQuery.", {"cause": repr(e)}) from e
    return _emit(cq, tpl, params, parts, ctx, req.get("out_dir"), req.get("name") or tpl.id, float(nozzle),
                 bool(req.get("thumbnail", True)))


def thumbnail(req: dict[str, Any]) -> dict[str, Any]:
    """`library_thumbnail` : {template, params?} -> {svg}. Vignette isométrique (SVG) sans écrire de fichier."""
    tpl = get_template(req.get("template"))
    params = tpl.resolve(req.get("params", {}))
    cq = require_cadquery("cad_backend_missing")
    ctx: dict[str, Any] = {"assumptions": [], "warnings": [], "derived": {}}
    try:
        parts = tpl.build(cq, params, ctx)
    except GeometryError:
        raise
    except Exception as e:
        raise GeometryError("cad_failure", "Échec de construction CadQuery.", {"cause": repr(e)}) from e
    # vignette : tessellation grossière (taille proportionnelle à la pièce)
    shapes = [wp.val() for _, wp in parts]
    bb = cq.Compound.makeCompound(shapes).BoundingBox()
    size = max(bb.xlen, bb.ylen, bb.zlen, 1.0)
    tol = max(tpl.tess[0], size / 120)
    meshes = [(n, tessellate(s, tol, max(tpl.tess[1], 0.5))) for (n, _), s in zip(parts, shapes)]
    return {"template": tpl.id, "svg": render_svg(meshes)}
