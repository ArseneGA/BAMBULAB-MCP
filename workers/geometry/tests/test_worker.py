import json
import math
import struct
import subprocess
import sys
import zipfile
from pathlib import Path

import cadquery as cq
import pytest

from bambu_geometry import cli, parametric, step_import
from bambu_geometry.errors import GeometryError


def call(cmd, req):
    """Appelle la CLI en sous-processus, comme le fera le serveur TS."""
    p = subprocess.run(
        [sys.executable, "-m", "bambu_geometry", cmd],
        input=json.dumps(req), capture_output=True, text=True, encoding="utf-8",
    )
    lines = p.stdout.strip().splitlines()
    assert len(lines) == 1, (p.stdout, p.stderr)
    return json.loads(lines[0]), p.returncode


@pytest.fixture
def step_box(tmp_path):
    f = tmp_path / "cube.step"
    cq.exporters.export(cq.Workplane("XY").box(10, 20, 30), str(f))
    return f


@pytest.fixture
def step_two(tmp_path):
    f = tmp_path / "two.step"
    a = cq.Workplane("XY").box(10, 10, 10)
    b = cq.Workplane("XY").cylinder(5, 4).translate((50, 0, 0))
    cq.exporters.export(cq.Workplane(obj=cq.Compound.makeCompound([a.val(), b.val()])), str(f))
    return f


# --- step_import -------------------------------------------------------------------------


def test_step_import_box(step_box, tmp_path):
    out, rc = call("step_import", {"path": str(step_box), "out_dir": str(tmp_path / "o")})
    assert rc == 0 and out["ok"]
    r = out["result"]
    assert len(r["objects"]) == 1
    o = r["objects"][0]
    assert o["volume_mm3"] == pytest.approx(6000, rel=1e-6)
    assert o["area_mm2"] == pytest.approx(2 * (200 + 300 + 600), rel=1e-6)
    assert o["bbox_mm"]["size"] == pytest.approx([10, 20, 30], abs=1e-6)
    assert o["tessellation"]["watertight"] is True and o["tessellation"]["open_edges"] == 0
    assert r["units"]["output"] == "mm"
    assert len(r["source"]["sha256"]) == 64
    stl = Path(r["files"]["stl"]).read_bytes()
    n = struct.unpack("<I", stl[80:84])[0]
    assert n == r["tessellation"]["triangles"] == 12
    assert len(stl) == 84 + 50 * n


def test_step_import_multi_objects_and_3mf(step_two, tmp_path):
    r = call("step_import", {"path": str(step_two), "out_dir": str(tmp_path / "o")})[0]["result"]
    assert len(r["objects"]) == 2
    with zipfile.ZipFile(r["files"]["3mf"]) as z:
        model = z.read("3D/3dmodel.model").decode()
    assert model.count("<object ") == 2 and 'unit="millimeter"' in model


def test_step_import_tolerance_controls_triangles(step_two, tmp_path):
    coarse = step_import.run({"path": str(step_two), "out_dir": str(tmp_path / "a"), "tolerance_mm": 1.0, "angular_tolerance_rad": 1.0})
    fine = step_import.run({"path": str(step_two), "out_dir": str(tmp_path / "b"), "tolerance_mm": 0.001, "angular_tolerance_rad": 0.05})
    assert fine["tessellation"]["triangles"] > coarse["tessellation"]["triangles"]


def test_step_import_corrupt(tmp_path):
    bad = tmp_path / "bad.step"
    bad.write_text("ceci n'est pas un STEP")
    out, rc = call("step_import", {"path": str(bad), "out_dir": str(tmp_path)})
    assert rc == 1 and out["error"]["code"] == "corrupt_file"


def test_step_import_missing_file(tmp_path):
    out, _ = call("step_import", {"path": str(tmp_path / "nope.step"), "out_dir": str(tmp_path)})
    assert out["error"]["code"] == "file_not_found"


def test_step_backend_missing(step_box, tmp_path, monkeypatch):
    import builtins

    real = builtins.__import__

    def fake(name, *a, **k):
        if name == "cadquery":
            raise ImportError("simulé")
        return real(name, *a, **k)

    monkeypatch.setattr(builtins, "__import__", fake)
    with pytest.raises(GeometryError) as e:
        step_import.run({"path": str(step_box), "out_dir": str(tmp_path)})
    assert e.value.code == "step_backend_missing"


# --- protocole ---------------------------------------------------------------------------


def test_protocol_errors():
    out, rc = call("inconnue", {})
    assert rc == 1 and out["ok"] is False and out["error"]["code"] == "unknown_command"
    p = subprocess.run([sys.executable, "-m", "bambu_geometry", "step_import"], input="{pas json",
                       capture_output=True, text=True)
    assert json.loads(p.stdout)["error"]["code"] == "invalid_json"
    out, _ = call("step_import", {})
    assert out["error"]["code"] == "invalid_params"


# --- parametric --------------------------------------------------------------------------


def run_tpl(tmp_path, tpl, params):
    return parametric.run({"template": tpl, "params": params, "out_dir": str(tmp_path), "name": tpl})


def test_spacer_volume(tmp_path):
    r = run_tpl(tmp_path, "spacer", {"outer_diameter": 20, "inner_diameter": 8, "height": 10})
    assert r["parts"][0]["volume_mm3"] == pytest.approx(math.pi / 4 * (400 - 64) * 10, rel=1e-6)
    assert all(Path(p).exists() for p in r["files"].values())
    src = json.loads(Path(r["files"]["source"]).read_text(encoding="utf-8"))
    assert src["template"] == "spacer" and src["params"]["height"] == 10


def test_bracket_volume_and_holes(tmp_path):
    r = run_tpl(tmp_path, "bracket", {"leg_a": 40, "leg_b": 30, "thickness": 4, "width": 20,
                                      "hole_diameter": 4, "holes_a": 1, "holes_b": 1})
    solid = (40 * 4 + (30 - 4) * 4) * 20
    holes = 2 * math.pi / 4 * 16 * 4
    assert r["parts"][0]["volume_mm3"] == pytest.approx(solid - holes, rel=1e-4)
    assert r["bbox_mm"]["size"] == pytest.approx([40, 20, 30], abs=1e-2)  # la bbox OCC inclut une marge ~1e-3


def test_box_two_parts(tmp_path):
    r = run_tpl(tmp_path, "box", {"inner_x": 50, "inner_y": 30, "inner_z": 20, "wall": 2, "floor": 2,
                                  "lid_thickness": 2, "lid_clearance": 0.3, "lip_height": 3})
    body, lid = r["parts"]
    assert body["volume_mm3"] == pytest.approx(54 * 34 * 22 - 50 * 30 * 20, rel=1e-6)
    assert lid["volume_mm3"] == pytest.approx(54 * 34 * 2 + (50 - 0.6) * (30 - 0.6) * 3, rel=1e-6)
    assert r["derived"]["outer"] == [54, 34, 22]


def test_hook_and_clamp(tmp_path):
    h = run_tpl(tmp_path, "hook", {})
    assert h["parts"][0]["volume_mm3"] > 0 and h["parts"][0]["solids"] == 1
    c = run_tpl(tmp_path, "clamp", {"tube_diameter": 25, "clearance": 0.4, "wall": 3, "width": 15})
    assert c["derived"]["inner_diameter"] == pytest.approx(25.4)
    assert c["parts"][0]["solids"] == 1
    c2 = run_tpl(tmp_path, "clamp", {"tube_diameter": 25, "hinge": "none"})
    assert c2["warnings"]


def test_parametric_validation(tmp_path):
    with pytest.raises(GeometryError) as e:
        run_tpl(tmp_path, "spacer", {"outer_diameter": -1, "height": 3})
    assert e.value.code == "invalid_params"
    with pytest.raises(GeometryError):
        run_tpl(tmp_path, "inconnu", {})
    r = run_tpl(tmp_path, "spacer", {"outer_diameter": 10, "height": 3, "wall": 0.5})
    assert any("2 × buse" in w for w in r["warnings"])


def test_parametric_cli_roundtrip(tmp_path):
    out, rc = call("parametric", {"template": "spacer", "params": {"outer_diameter": 10, "height": 5},
                                  "out_dir": str(tmp_path)})
    assert rc == 0 and out["result"]["level"] == "calculé"


# --- durcissement (relecture) ------------------------------------------------------------


@pytest.mark.parametrize("name", ["../evil", "a/b", "a\\b", "", "x" * 65, "..", "nom avec espace", "é"])
def test_parametric_rejects_unsafe_names(tmp_path, name):
    out = tmp_path / "out"
    with pytest.raises(GeometryError) as e:
        parametric.run({"template": "spacer", "params": {"outer_diameter": 10, "height": 3},
                        "out_dir": str(out), "name": name or 5})
    assert e.value.code == "invalid_params"
    assert not list(tmp_path.glob("*.stl")) and not list(out.glob("*.stl")) if out.exists() else True


def test_parametric_outputs_stay_in_out_dir(tmp_path):
    out = tmp_path / "out"
    r = parametric.run({"template": "spacer", "params": {"outer_diameter": 10, "height": 3},
                        "out_dir": str(out), "name": "ok.v1-2_x"})
    for f in r["files"].values():
        assert Path(f).resolve().parent == out.resolve()


@pytest.mark.parametrize("bad", [float("nan"), float("inf"), "abc", 0, -1, 0.0000001, 1e9, True, None])
def test_step_import_tolerances_rejected(step_box, tmp_path, bad):
    for key in ("tolerance_mm", "angular_tolerance_rad"):
        with pytest.raises(GeometryError) as e:
            step_import.run({"path": str(step_box), "out_dir": str(tmp_path / "o"), key: bad})
        assert e.value.code == "invalid_params"


def test_step_import_triangle_cap(step_two, tmp_path, monkeypatch):
    monkeypatch.setattr(step_import, "MAX_TRIANGLES", 5)
    with pytest.raises(GeometryError) as e:
        step_import.run({"path": str(step_two), "out_dir": str(tmp_path / "o")})
    assert e.value.code == "too_complex"


def test_hole_counts_capped_and_integer(tmp_path):
    for tpl, params in (("bracket", {"leg_a": 40, "leg_b": 30, "holes_a": 10**6}),
                        ("bracket", {"leg_a": 40, "leg_b": 30, "holes_b": 1.5}),
                        ("hook", {"holes": 51}),
                        ("hook", {"holes": True})):
        with pytest.raises(GeometryError) as e:
            run_tpl(tmp_path, tpl, params)
        assert e.value.code == "invalid_params"


def test_hook_arm_must_exceed_twice_thickness(tmp_path):
    with pytest.raises(GeometryError) as e:
        run_tpl(tmp_path, "hook", {"arm_length": 8, "thickness": 4})
    assert e.value.code == "invalid_params"


def test_hook_tiny_plate_does_not_crash(tmp_path):
    r = run_tpl(tmp_path, "hook", {"plate_height": 10, "hole_diameter": 6, "holes": 3})
    assert r["warnings"] and r["parts"][0]["volume_mm3"] > 0
