"""Tests de la bibliothèque de gabarits et du générateur de calibration (CadQuery réel)."""

import json
import math
import subprocess
import sys
import xml.etree.ElementTree as ET
from pathlib import Path

import cadquery as cq
import pytest

from bambu_geometry import calibration, library
from bambu_geometry.errors import GeometryError
from bambu_geometry.gears import gear_dims

REG = library._load()
ALL_IDS = sorted(REG)

NEW_TEMPLATES = {"hinge_print_in_place", "hinge_pin", "snap_clip", "enclosure", "wall_mount", "spur_gear", "gear_pair",
                 "thread", "washer", "handle", "hook_reinforced", "mounting_plate"}
OLD_TEMPLATES = {"clamp", "bracket", "box", "spacer", "hook"}


def call(cmd, req):
    p = subprocess.run([sys.executable, "-m", "bambu_geometry", cmd], input=json.dumps(req), capture_output=True,
                       text=True, encoding="utf-8")
    lines = p.stdout.strip().splitlines()
    assert len(lines) == 1, (p.stdout, p.stderr)
    return json.loads(lines[0]), p.returncode


def build_parts(tpl_id, **params):
    tpl = REG[tpl_id]
    ctx = {"assumptions": [], "warnings": [], "derived": {}}
    p = tpl.resolve(params)
    parts = tpl.build(cq, p, ctx)
    return {n: wp for n, wp in parts}, ctx, p


def inside(wp, x, y, z):
    return wp.val().isInside(cq.Vector(x, y, z), 1e-6)


# --- schéma ----------------------------------------------------------------------------------


def test_registry_has_every_template():
    assert NEW_TEMPLATES | OLD_TEMPLATES <= set(ALL_IDS)


def test_schema_is_typed_and_defaults_are_valid():
    out = library.list_templates({})
    assert out["count"] == len(ALL_IDS)
    for t in out["templates"]:
        assert t["version"] and t["title"] and t["description"] and t["schema_hash"]
        assert t["params"], t["id"]
        for prm in t["params"]:
            assert prm["type"] in ("number", "integer", "enum", "boolean")
            assert prm["description"]
            if prm["type"] in ("number", "integer"):
                assert "min" in prm and "max" in prm and prm["min"] <= prm["default"] <= prm["max"], (t["id"], prm["name"])
            if prm["type"] == "enum":
                assert prm["default"] in prm["options"]
        REG[t["id"]].resolve({})  # tous les défauts passent la validation


def test_validation_errors():
    with pytest.raises(GeometryError) as e:
        REG["washer"].resolve({"outer_diameter": 1000})
    assert e.value.code == "invalid_params" and "outer_diameter" in e.value.message
    with pytest.raises(GeometryError):
        REG["washer"].resolve({"inconnu": 1})
    with pytest.raises(GeometryError):
        REG["thread"].resolve({"size": "M7"})
    with pytest.raises(GeometryError):
        REG["washer"].resolve({"thickness": True})
    with pytest.raises(GeometryError):
        REG["spur_gear"].resolve({"teeth": 20.5})
    with pytest.raises(GeometryError):
        REG["washer"].resolve({"thickness": float("nan")})


# --- tous les gabarits sont étanches ---------------------------------------------------------


@pytest.mark.parametrize("tpl_id", [i for i in ALL_IDS if i != "thread"])
def test_default_build_is_watertight(tpl_id, tmp_path):
    r = library.build({"template": tpl_id, "params": {}, "out_dir": str(tmp_path), "name": tpl_id})
    assert r["template_version"] == REG[tpl_id].version
    assert r["parts"], tpl_id
    for part in r["parts"]:
        assert part["valid"] and part["solids"] >= 1 and part["volume_mm3"] > 1
        t = part["tessellation"]
        assert t["watertight"] is True and t["open_edges"] == 0 and t["degenerate_triangles"] == 0, (tpl_id, part["name"], t)
    for k in ("stl", "step", "source", "thumbnail"):
        assert Path(r["files"][k]).stat().st_size > 100
    assert not [w for w in r["warnings"] if "non étanche" in w]
    src = json.loads(Path(r["files"]["source"]).read_text(encoding="utf-8"))
    assert src["template"] == tpl_id and src["params"] == r["params"]


def test_thread_default_build_is_watertight(tmp_path):
    r = library.build({"template": "thread", "params": {"size": "M5", "mode": "pair", "length": 8}, "out_dir": str(tmp_path)})
    assert [p["name"] for p in r["parts"]] == ["thread_bolt", "thread_nut"]
    for part in r["parts"]:
        assert part["valid"] and part["tessellation"]["watertight"] is True and part["solids"] == 1


def test_thumbnail_is_valid_svg():
    r = library.thumbnail({"template": "gear_pair"})
    root = ET.fromstring(r["svg"])
    assert root.tag.endswith("svg")
    polys = [e for e in root if e.tag.endswith("polygon")]
    assert len(polys) > 200
    assert len(r["svg"]) < 4_000_000


def test_unknown_template_lists_available():
    with pytest.raises(GeometryError) as e:
        library.build({"template": "nope", "out_dir": "x"})
    assert e.value.code == "invalid_params" and "washer" in e.value.details["available"]


def test_name_is_confined(tmp_path):
    with pytest.raises(GeometryError):
        library.build({"template": "washer", "out_dir": str(tmp_path), "name": "../evil"})


# --- cotes vérifiées -------------------------------------------------------------------------


def test_washer_volume_and_chamfer():
    parts, _, _ = build_parts("washer", outer_diameter=20, inner_diameter=8, thickness=3, chamfer=0)
    assert parts["washer"].val().Volume() == pytest.approx(math.pi / 4 * (400 - 64) * 3, rel=1e-6)
    parts2, _, _ = build_parts("washer", outer_diameter=20, inner_diameter=8, thickness=3, chamfer=0.5)
    assert parts2["washer"].val().Volume() < parts["washer"].val().Volume()


def test_mounting_plate_holes_and_size():
    parts, ctx, _ = build_parts("mounting_plate", width=100, height=60, thickness=3, cols=5, rows=3, hole_diameter=6,
                                margin=14, mount_hole_diameter=0, corner_radius=0)
    solid = parts["mounting_plate"].val()
    assert solid.Volume() == pytest.approx(100 * 60 * 3 - 15 * math.pi * 9 * 3, rel=1e-4)
    assert ctx["derived"]["holes"] == 15
    bb = solid.BoundingBox()
    assert (bb.xlen, bb.ylen, bb.zlen) == pytest.approx((100, 60, 3), abs=1e-2)
    # un trou de la grille est bien vide, la matière entre deux trous est pleine
    px = ctx["derived"]["pitch_x"]
    assert not inside(parts["mounting_plate"], 14 + 3, 14 + 3, 1.5)
    assert inside(parts["mounting_plate"], 14 + 3 + px / 2, 14 + 3, 1.5)


def test_mounting_plate_staggered_and_mount_holes():
    parts, ctx, _ = build_parts("mounting_plate", pattern="staggered", cols=6, rows=4)
    assert ctx["derived"]["mount_holes"] == 4 and ctx["derived"]["holes"] > 0
    assert not inside(parts["mounting_plate"], 6, 6, 1.5)  # trou de fixation d'angle


def test_wall_mount_keyhole_and_screws():
    parts, ctx, _ = build_parts("wall_mount", mode="keyhole", count=2)
    s = parts["wall_mount"]
    x0, y0 = 50 * 0.25, 70 * 0.2
    assert not inside(s, x0 + 3, y0, 0.5)  # poche de tête (arrière), rayon 4
    assert inside(s, x0 + 3, y0, 4.5)  # la face avant reste fermée autour du corps de vis
    assert not inside(s, x0, y0 + 4, 4.5)  # fente étroite traversante
    assert inside(s, x0 + 3, y0 + 4, 4.5)  # hors de la fente (largeur 4,2)
    assert ctx["derived"]["pocket_depth"] == pytest.approx(3.0)
    parts, ctx, _ = build_parts("wall_mount", mode="screws", count=1, thickness=5)
    s = parts["wall_mount"]
    assert not inside(s, 25, 14, 2.5)
    assert not inside(s, 25 + 3.5, 14, 4.9)  # fraisurage côté face avant
    assert inside(s, 25 + 3.5, 14, 0.5)  # le fraisurage s'arrête avant l'arrière


def test_wall_mount_rejects_thin_plate():
    with pytest.raises(GeometryError):
        build_parts("wall_mount", thickness=3.0)


def test_hook_reinforced_has_ribs_and_holes():
    parts, ctx, p = build_parts("hook_reinforced")
    plain, _, _ = build_parts("hook", width=24, plate_height=60, thickness=5, arm_length=40, tip_height=14, hole_diameter=4.5, holes=2)
    assert parts["hook_reinforced"].val().Volume() > plain["hook"].val().Volume()  # nervures + congé ajoutent de la matière
    assert not inside(parts["hook_reinforced"], 2.5, 12, 17.5)  # trou de vis (axe X), 1er des 2 trous
    assert inside(parts["hook_reinforced"], 2.5, 12, 25.5)  # entre les trous : plein


def test_handle_dimensions():
    parts, ctx, _ = build_parts("handle", span=90, grip_diameter=20, standoff=38, post_diameter=14)
    bb = parts["handle"].val().BoundingBox()
    assert bb.zlen == pytest.approx(38 + 10, abs=1e-2)  # axe + rayon de la barre
    assert ctx["derived"]["finger_clearance"] == pytest.approx(28)
    fx = ctx["derived"]["foot_hole_x"][1]
    assert not inside(parts["handle"], fx, 0, 2)


def test_hinge_print_in_place_clearances():
    parts, ctx, p = build_parts("hinge_print_in_place", clearance=0.3)
    a, b = parts["hinge_leaf_a"].val(), parts["hinge_leaf_b"].val()
    assert a.isValid() and b.isValid()
    assert a.intersect(b).Volume() == pytest.approx(0, abs=1e-6)
    dist = a.distance(b)
    assert dist == pytest.approx(0.3, abs=0.02)  # jeu minimal entre pièces mobiles = clearance
    assert ctx["derived"]["hole_diameter"] == pytest.approx(4.6)
    # l'axe (fusionné à A) traverse les nœuds de B : au centre d'un nœud de B, ni A ni B
    R = ctx["derived"]["knuckle_radius"]
    x_b = 1 * (ctx["derived"]["knuckle_length"] + 0.3) + 1.0
    assert inside(parts["hinge_leaf_a"], x_b, 0, R)  # l'axe est dans A
    assert not inside(parts["hinge_leaf_b"], x_b, 0, R)
    assert inside(parts["hinge_leaf_b"], x_b, 0, R + 2.3 + 0.7)  # paroi du nœud
    # le bord extérieur des lames
    assert a.BoundingBox().ymin == pytest.approx(-20, abs=1e-2) and b.BoundingBox().ymax == pytest.approx(20, abs=1e-2)


def test_hinge_pip_needs_odd_knuckles_and_thin_leaf():
    with pytest.raises(GeometryError):
        build_parts("hinge_print_in_place", knuckles=4)
    with pytest.raises(GeometryError):
        build_parts("hinge_print_in_place", thickness=9)


def test_hinge_pin_hole_and_pin():
    parts, ctx, p = build_parts("hinge_pin", pin_diameter=3, hole_clearance=0.3, axial_gap=0.4, knuckles=4)
    assert set(parts) == {"hinge_leaf_a", "hinge_leaf_b", "hinge_pin"}
    a, b, pin = (parts[k].val() for k in ("hinge_leaf_a", "hinge_leaf_b", "hinge_pin"))
    assert a.distance(b) == pytest.approx(0.4, abs=0.02)
    R = ctx["derived"]["knuckle_radius"]
    assert ctx["derived"]["hole_diameter"] == pytest.approx(3.3)
    assert R == pytest.approx(1.65 + 2.0)
    assert not inside(parts["hinge_leaf_a"], 1.0, 0, R)  # perçage vide
    assert pin.BoundingBox().xlen == pytest.approx(3 + 2.4, abs=1e-2) and pin.BoundingBox().zlen == pytest.approx(40, abs=1e-2)


def test_snap_clip_geometry():
    parts, ctx, p = build_parts("snap_clip")
    d = ctx["derived"]
    assert d["ramp_length"] == pytest.approx(1.2 / math.tan(math.radians(30)), rel=1e-6)
    clip = parts["snap_clip"]
    L, h = p["beam_length"], p["beam_thickness"]
    hl = d["ramp_length"]
    assert inside(clip, L - hl + 0.3, h + 0.5, 4)  # dans le crochet, près de la face d'accroche
    assert not inside(clip, L - hl + 0.3, h + 1.15, 4)  # au-dessus de la rampe
    assert not inside(clip, L - 0.2, h + 0.5, 4)  # près de la pointe la rampe est basse
    assert inside(clip, L - d["ramp_length"] + 0.05, h + 1.0, 4)  # face d'accroche verticale
    assert not inside(clip, L - d["ramp_length"] - 0.1, h + 0.5, 4)
    recv = parts["snap_receiver"]
    wy = d["receiver_origin_y"]
    rim = p["catch_rim"]
    assert not inside(recv, rim + 1, wy + rim + 1, 1)  # fenêtre
    assert inside(recv, 1, wy + 1, 1)  # bord plein
    bb = recv.val().BoundingBox()
    assert bb.zlen == pytest.approx(p["catch_thickness"], abs=1e-2)
    assert d["catch_overlap"] == pytest.approx(0.9)


def test_snap_clip_rejects_too_long_ramp():
    with pytest.raises(GeometryError):
        build_parts("snap_clip", hook_depth=4, beam_length=8, insertion_angle=15)


def test_enclosure_screw_mode():
    parts, ctx, p = build_parts("enclosure", inner_x=60, inner_y=40, inner_z=25, wall=2.4, floor=2.4, corner_radius=0)
    body, lid = parts["enclosure_body"], parts["enclosure_lid"]
    bb = body.val().BoundingBox()
    assert (bb.xlen, bb.ylen, bb.zlen) == pytest.approx((64.8, 44.8, 27.4), abs=1e-2)
    cx, cy = ctx["derived"]["boss_centers"][0]
    h = 27.4
    assert not inside(body, cx, cy, h - 2)  # trou d'insert
    assert inside(body, cx, cy, h - p["insert_depth"] - 1)  # sous le fond du trou : matière
    assert not inside(body, 32, 22, 10)  # cavité
    assert inside(body, 32, 22, 1)  # fond
    lx = ctx["derived"]["lid_offset_x"]
    assert not inside(lid, lx + cx, cy, 1.5)  # trou de vis du couvercle
    assert inside(lid, lx + 32, 22, 1.5)
    assert ctx["derived"]["screws"] == 4 and ctx["derived"]["screw_length_mm"] >= 8
    assert body.val().isValid() and lid.val().isValid()


def test_enclosure_clip_mode_has_bumps_and_grooves():
    parts, ctx, p = build_parts("enclosure", lid_mode="clip", corner_radius=2)
    assert "screws" not in ctx["derived"] and ctx["derived"]["bump_height"] == p["clip_bump_height"]
    screw, _, _ = build_parts("enclosure", lid_mode="screw", corner_radius=2)
    # le mode clip n'a pas de plots : le corps est plus léger que le corps vissé
    assert parts["enclosure_body"].val().Volume() < screw["enclosure_body"].val().Volume()


def test_enclosure_validation():
    with pytest.raises(GeometryError):
        build_parts("enclosure", insert_depth=26)
    with pytest.raises(GeometryError):
        build_parts("enclosure", boss_diameter=5, insert_hole_diameter=4)


def test_spur_gear_dimensions():
    m, z = 2.0, 24
    parts, ctx, _ = build_parts("spur_gear", module=m, teeth=z, bore_diameter=8, thickness=6, tooth_clearance=0)
    g = parts["spur_gear"].val()
    dims = gear_dims(m, z, 20)
    assert ctx["derived"]["pitch_diameter"] == pytest.approx(m * z)
    assert ctx["derived"]["tip_diameter"] == pytest.approx(m * (z + 2))
    bb = g.BoundingBox()
    assert bb.xlen == pytest.approx(dims["tip_diameter"], abs=0.05) and bb.zlen == pytest.approx(6, abs=1e-2)
    assert bb.ymin < 0 < bb.ymax and abs(bb.ymax + bb.ymin) < 0.2
    # la dent n°0 est sur l'axe +X : plein à r = rayon primitif, vide au creux entre deux dents
    wp = parts["spur_gear"]
    rp = m * z / 2
    assert inside(wp, rp, 0, 3)
    a = math.pi / z
    assert not inside(wp, rp * math.cos(a), rp * math.sin(a), 3)  # milieu du creux, au cercle primitif
    assert not inside(wp, 3.9, 0, 3) and inside(wp, 4.1, 0, 3)  # alésage Ø8
    # épaisseur de dent au cercle primitif : demi-arc = π m / 4 (jeu nul)
    t = math.pi * m / 2
    ang = t / rp
    assert inside(wp, rp * math.cos(ang / 2 - 0.01), rp * math.sin(ang / 2 - 0.01), 3)
    assert not inside(wp, rp * math.cos(ang / 2 + 0.03), rp * math.sin(ang / 2 + 0.03), 3)


def test_spur_gear_keyway_and_hub():
    parts, ctx, _ = build_parts("spur_gear", bore_diameter=6, keyway_width=2, keyway_depth=1, hub_diameter=14, hub_height=5)
    wp = parts["spur_gear"]
    assert not inside(wp, 3.5, 0, 4)  # rainure (au-delà de r=3)
    assert inside(wp, 0, 3.5, 4)  # pas de rainure ailleurs
    assert wp.val().BoundingBox().zlen == pytest.approx(13, abs=1e-2)  # 8 + moyeu 5


def test_gear_pair_meshes_without_interference():
    parts, ctx, p = build_parts("gear_pair", module=2, teeth_1=18, teeth_2=30, backlash=0.3)
    d = ctx["derived"]
    assert d["center_distance"] == pytest.approx(2 * (18 + 30) / 2)
    assert d["ratio"] == pytest.approx(30 / 18)
    assert d["contact_ratio"] > 1.2
    g1, g2 = parts["gear_1"].val(), parts["gear_2"].val()
    assert g1.intersect(g2).Volume() == pytest.approx(0, abs=1e-6)
    dist = g1.distance(g2)
    assert 0 < dist < 0.6  # les dents sont en prise à ce jeu de flanc, sans se toucher
    bb2 = g2.BoundingBox()
    assert (bb2.xmin + bb2.xmax) / 2 == pytest.approx(d["center_distance"], abs=0.1)


def test_gear_pair_overlaps_when_center_distance_too_small():
    parts, ctx, _ = build_parts("gear_pair", module=2, teeth_1=18, teeth_2=30)
    g1 = parts["gear_1"].val()
    moved = parts["gear_2"].val().translate(cq.Vector(-0.8, 0, 0))
    assert g1.intersect(moved).Volume() > 0.5  # contre-épreuve : le test détecte bien une interférence


def test_gear_validation():
    with pytest.raises(GeometryError):
        build_parts("spur_gear", teeth=8, module=1, bore_diameter=50)
    with pytest.raises(GeometryError):
        build_parts("spur_gear", teeth=20, bore_diameter=60)


def test_thread_is_right_handed_helix():
    parts, ctx, p = build_parts("thread", size="M6", mode="bolt", length=10)
    bolt = parts["thread_bolt"]
    D, pitch = 6.0, 1.0
    hh = ctx["derived"]["head_height"]
    assert ctx["derived"]["pitch"] == 1.0 and ctx["derived"]["major_diameter"] == 6.0
    # crête à θ = π + 2π (z - z0)/pas (hélice à droite) ; fond à l'opposé
    r_crest, r_root = D / 2 - 0.12, D / 2 - 0.6134 * pitch - 0.12
    for z in (hh + 2.0, hh + 2.25, hh + 2.5, hh + 3.1):
        th = math.pi + 2 * math.pi * (z - (hh - 0.01)) / pitch
        assert inside(bolt, r_crest * math.cos(th), r_crest * math.sin(th), z), z
        assert not inside(bolt, r_crest * math.cos(th + math.pi), r_crest * math.sin(th + math.pi), z), z
        assert inside(bolt, r_root * math.cos(th + math.pi), r_root * math.sin(th + math.pi), z), z
    bb = bolt.val().BoundingBox()
    assert bb.zlen == pytest.approx(hh + 10, abs=0.02)
    assert max(bb.xlen, bb.ylen) >= 10 - 0.1  # tête hexagonale 10 mm sur plats


def test_thread_nut_fits_bolt():
    parts, ctx, _ = build_parts("thread", size="M5", mode="pair", length=6, nut_height=4, head_height=3)
    bolt, nut = parts["thread_bolt"].val(), parts["thread_nut"].val()
    assert nut.isValid() and bolt.isValid()
    back = -(8 / math.cos(math.radians(30)) + 4)  # l'écrou est posé à côté de la vis dans la pièce
    dz = 3 + 0.99  # même phase d'hélice que la vis (tige commençant à z = 2,99 ; cutter à z = -1)
    assert bolt.intersect(nut.translate(cq.Vector(back, 0, dz))).Volume() == pytest.approx(0, abs=1e-3)
    # contre-épreuve : l'écrou décalé d'un demi-pas interfère avec les filets de la vis
    assert bolt.intersect(nut.translate(cq.Vector(back, 0, dz + 0.4))).Volume() > 0.5


def test_thread_insert_boss():
    parts, ctx, p = build_parts("thread", mode="insert_boss", insert_hole_diameter=4.0, insert_depth=6, boss_diameter=8, boss_height=10)
    boss = parts["insert_boss"]
    assert not inside(boss, 0, 0, 9)
    assert inside(boss, 0, 0, 3)
    assert boss.val().BoundingBox().zlen == pytest.approx(10, abs=1e-2)
    with pytest.raises(GeometryError):
        build_parts("thread", mode="insert_boss", insert_depth=13, boss_height=10)


def test_thread_warns_small_sizes():
    _, ctx, _ = build_parts("thread", size="M3", mode="bolt", length=6)
    assert any("M5" in w for w in ctx["warnings"])


# --- calibration ----------------------------------------------------------------------------


def test_calibration_all_pieces_watertight(tmp_path):
    r = calibration.run({"out_dir": str(tmp_path), "temperature_mode": "series", "temperatures": [200, 210]})
    kinds = [p["kind"] for p in r["pieces"]]
    assert kinds == ["fit_comb", "hole_shaft", "shrink_cube", "bridge_overhang", "temperature", "temperature"]
    for piece in r["pieces"]:
        assert piece["measure"] and Path(piece["files"]["stl"]).stat().st_size > 84
        for part in piece["parts"]:
            assert part["valid"] and part["tessellation"]["watertight"] is True, (piece["name"], part)
    temp = [p for p in r["pieces"] if p["kind"] == "temperature"]
    assert [p["slicing_overrides"][0]["value"] for p in temp] == ["200", "210"]


def test_calibration_shrink_cube_is_50mm(tmp_path):
    r = calibration.run({"out_dir": str(tmp_path), "kinds": ["shrink_cube"]})
    assert r["pieces"][0]["bbox_mm"]["size"] == pytest.approx([50, 50, 50], abs=0.01)


def test_calibration_fit_comb_notch_widths(tmp_path):
    gaps = [0.1, 0.3, 0.5]
    r = calibration.run({"out_dir": str(tmp_path), "kinds": ["fit_comb"], "clearance_gaps": gaps})
    info = r["pieces"][0]["info"]
    cq_, _ = cq, None
    parts, _ = calibration.fit_comb(cq, gaps)
    base = dict(parts)["fit_base"]
    tw = info["tab_width_mm"]
    for c, g in zip(info["notch_centers_x_mm"], gaps):
        half = (tw + g) / 2
        assert not inside(base, c + half - 0.02, 10, 2.5)  # juste dans l'encoche
        assert inside(base, c + half + 0.02, 10, 2.5)  # juste hors encoche
        assert not inside(base, c - half + 0.02, 10, 2.5)
    tab = dict(parts)["fit_tab"]
    assert tab.val().BoundingBox().xlen == pytest.approx(tw + 8, abs=1e-2)


def test_calibration_hole_shaft_nominal_diameters():
    parts, info = calibration.hole_shaft(cq, [4.0, 8.0])
    d = dict(parts)
    for cx, dia in zip(info["hole_centers_x_mm"], info["diameters_mm"]):
        assert not inside(d["holes_vertical"], cx + dia / 2 - 0.02, 8, 2.5)
        assert inside(d["holes_vertical"], cx + dia / 2 + 0.02, 8, 2.5)
        assert inside(d["shafts"], cx + dia / 2 - 0.02, 57, 8)
        assert not inside(d["shafts"], cx + dia / 2 + 0.02, 57, 8)
        assert not inside(d["holes_horizontal"], cx, 30, 8 + dia / 2 - 0.02)  # trou à axe horizontal (Y)
        assert inside(d["holes_horizontal"], cx, 30, 8 + dia / 2 + 0.02)


def test_calibration_temperature_tower_segments(tmp_path):
    r = calibration.run({"out_dir": str(tmp_path), "kinds": ["temperature"], "temperature_mode": "tower", "temperatures": [190, 200, 210]})
    p = r["pieces"][0]
    assert [s["nozzle_temperature_c"] for s in p["segments"]] == [190, 200, 210]
    assert p["bbox_mm"]["size"][2] == pytest.approx(30, abs=0.01)
    assert [c["layer_z_mm"] for c in p["layer_changes"]] == [0, 10, 20]
    assert len(p["gcode_hint"]) == 2


def test_calibration_bridge_spans_and_angles(tmp_path):
    parts, info = calibration.bridge_overhang(cq, [10.0, 30.0], [30.0, 60.0])
    body = dict(parts)["bridge_overhang"]
    assert not inside(body, 6 + 5, 5, 6)  # sous le pont de 10 mm : vide
    assert inside(body, 6 + 5, 5, 11)  # le pont
    assert not inside(body, 8 + 10 * math.tan(math.radians(60)) + 0.5, 16 * 2 + 6 + 16 + 5, 5)  # hors du coin de surplomb
    with pytest.raises(GeometryError):
        calibration.run({"out_dir": str(tmp_path), "kinds": ["inconnu"]})
    with pytest.raises(GeometryError):
        calibration.run({"out_dir": str(tmp_path), "kinds": ["fit_comb"], "clearance_gaps": [5.0]})


# --- protocole CLI ---------------------------------------------------------------------------


def test_cli_library_list_and_build(tmp_path):
    out, rc = call("library_list", {})
    assert rc == 0 and out["ok"] and out["result"]["count"] == len(ALL_IDS)
    out, rc = call("library_build", {"template": "washer", "params": {"outer_diameter": 12, "inner_diameter": 5}, "out_dir": str(tmp_path), "name": "w1"})
    assert rc == 0 and out["result"]["parts"][0]["volume_mm3"] > 0
    out, rc = call("library_build", {"template": "washer", "params": {"inconnu": 1}, "out_dir": str(tmp_path)})
    assert rc == 1 and out["error"]["code"] == "invalid_params"
    out, rc = call("library_thumbnail", {"template": "washer"})
    assert rc == 0 and out["result"]["svg"].startswith("<svg")


def test_cli_calibration_set(tmp_path):
    out, rc = call("calibration_set", {"out_dir": str(tmp_path), "kinds": ["shrink_cube"], "name_prefix": "c1"})
    assert rc == 0 and out["result"]["pieces"][0]["name"] == "c1_retrait"
