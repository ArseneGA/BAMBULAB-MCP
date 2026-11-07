"""Lot qualité 3D (audit FDM) : filetages, engrenages, charnière print-in-place, séries de calibration.

CadQuery réel ; les mesures portent sur le MAILLAGE EXPORTÉ (STL) quand le critère concerne l'impression.
"""

import math

import cadquery as cq
import numpy as np
import pytest

from bambu_geometry import calibration, gears, library, threads
from bambu_geometry.cad import tessellate
from bambu_geometry.errors import GeometryError

REG = library._load()


def load_stl(path):
    data = open(path, "rb").read()
    n = int.from_bytes(data[80:84], "little")
    rec = np.frombuffer(data[84:], dtype=[("n", "<f4", 3), ("v", "<f4", (3, 3)), ("a", "<u2")], count=n)
    v = rec["v"].reshape(-1, 3).astype(float)
    return v, np.arange(len(v)).reshape(-1, 3)


def build_parts(tpl_id, **params):
    tpl = REG[tpl_id]
    ctx = {"assumptions": [], "warnings": [], "derived": {}}
    p = tpl.resolve(params)
    return {n: wp for n, wp in tpl.build(cq, p, ctx)}, ctx, p


def inside(wp, x, y, z):
    return wp.val().isInside(cq.Vector(x, y, z), 1e-6)


# --- 1. filetages ------------------------------------------------------------------------------


def test_thread_profile_is_iso_60_degrees():
    # crête plate P/8, flancs à 30° de la normale (60° inclus), fond plat : la somme des largeurs fait un pas
    assert threads.CREST_FLAT == pytest.approx(1 / 8)
    assert math.degrees(math.atan(threads.FLANK_AXIAL / 0.6134)) == pytest.approx(30.0, abs=1e-6)
    assert threads.CREST_FLAT + 2 * threads.FLANK_AXIAL + threads.ROOT_FLAT == pytest.approx(1.0)
    assert threads.thread_depth(1.0) == pytest.approx(0.6134)
    assert threads.profile_fraction(0.5) == 1.0 and threads.profile_fraction(0.0) == 0.0
    sec = threads.thread_section(6.0, 1.0)
    radii = [math.hypot(x, y) for x, y in sec]
    assert max(radii) == pytest.approx(3.0) and min(radii) == pytest.approx(3.0 - 0.6134)
    # les ruptures de pente sont des points du contour : la crête plate est bien P/8 = 45° d'arc
    crest = [math.atan2(y, x) % (2 * math.pi) for x, y in sec if math.hypot(x, y) > 3.0 - 1e-9]
    assert math.degrees(max(crest) - min(crest)) == pytest.approx(45.0, abs=0.01)


@pytest.mark.parametrize("size", ["M3", "M4", "M6"])
def test_thread_height_measured_on_exported_mesh(size, tmp_path):
    r = library.build({"template": "thread", "params": {"size": size, "mode": "bolt", "length": 10}, "out_dir": str(tmp_path),
                       "thumbnail": False})
    pitch = threads.COARSE_PITCH[size]
    head = r["derived"]["head_height"]
    assert r["derived"]["tessellation_linear_mm"] <= 0.02
    mesh = load_stl(r["files"]["stl"])
    # fenêtre de 2 pas au milieu de la partie filetée, loin de la tête et du chanfrein
    z_lo = head + 3 * pitch
    depth = threads.measure_thread_depth(mesh, z_lo, z_lo + 2 * pitch)
    theory = threads.thread_depth(pitch)
    assert depth >= 0.9 * theory, (size, depth, theory)
    assert depth <= 1.1 * theory  # pas de dépassement non plus : le maillage ne s'écarte pas du profil
    assert r["parts"][0]["tessellation"]["watertight"] is True


def test_thread_default_tessellation_would_lose_the_thread(tmp_path):
    # contre-épreuve : le maillage grossier d'avant (0,1 mm / 0,9 rad) est plus pauvre en triangles sur la même vis
    wp = build_parts("thread", size="M4", mode="bolt", length=10)[0]["thread_bolt"]
    from OCP.BRepTools import BRepTools  # noqa: PLC0415

    BRepTools.Clean_s(wp.val().wrapped)
    coarse = tessellate(wp.val(), 0.1, 0.9)
    BRepTools.Clean_s(wp.val().wrapped)
    fine = tessellate(wp.val(), *threads.thread_tessellation(4.0))
    assert len(fine[1]) > len(coarse[1])


def test_thread_entry_chamfers():
    parts, ctx, _ = build_parts("thread", size="M6", mode="pair", length=8, nut_height=5, head_height=4)
    bolt, nut = parts["thread_bolt"], parts["thread_nut"]
    hh, pitch, D = 4.0, 1.0, 6.0
    z_end = hh - 0.01 + 8
    r_minor = D / 2 - threads.thread_depth(pitch)
    th = lambda z: math.pi + 2 * math.pi * (z - (hh - 0.01)) / pitch  # noqa: E731  (angle de la crête à la hauteur z)
    # au bout de la vis, la crête est rabotée à 45° : à 0,05 mm du bout le rayon admis ≈ r_minor + 0,05
    z = z_end - 0.05
    r = r_minor + 0.2
    assert not inside(bolt, r * math.cos(th(z)), r * math.sin(th(z)), z)
    # 1 mm plus bas, la même crête est entière
    z2 = z_end - 1.05
    r2 = D / 2 - 0.12
    assert inside(bolt, r2 * math.cos(th(z2)), r2 * math.sin(th(z2)), z2)
    # écrou (posé à côté de la vis) : fraisure d'entrée aux deux faces
    dx = D * 0 + (10 / math.cos(math.radians(30)) + 4)
    r_out = D / 2 + 0.2 + 0.1  # juste au-dessus du diamètre extérieur du filet de l'écrou
    for z_face, z_in in ((0.0, 0.03), (5.0, 5.0 - 0.03)):
        assert not inside(nut, dx + r_out, 0, z_in)  # dans la fraisure
    assert inside(nut, dx + r_out, 0, 2.5)  # à mi-hauteur, matière pleine


def test_thread_clearance_documented():
    _, ctx, _ = build_parts("thread", size="M6", mode="pair", length=6, nut_height=4, head_height=3)
    text = " ".join(ctx["assumptions"]).lower().replace("iso 68-1", "ISO 68-1")
    assert "ISO 68-1" in text and "radial" in text and "jeu axial" in text and "chanfrein" in text


# --- 2. engrenages -----------------------------------------------------------------------------


def _fit_circle(pts):
    a = np.array([[x, y, 1.0] for x, y in pts])
    b = np.array([-(x * x + y * y) for x, y in pts])
    sol = np.linalg.lstsq(a, b, rcond=None)[0]
    cx, cy = -sol[0] / 2, -sol[1] / 2
    return math.sqrt(max(cx * cx + cy * cy - sol[2], 0.0))


def test_gear_flank_has_at_least_20_points():
    side, root_half, rho = gears.space_side(2.0, 24, 20.0)
    flank = [p for p in side[gears.FILLET_POINTS + 1:]]
    assert len(flank) >= 20
    prof = gears.gear_profile(2.0, 24, 20.0)
    per_tooth = len(prof) / 24
    assert per_tooth >= 2 * 20 + 5
    with pytest.raises(GeometryError):
        gears.gear_profile(2.0, 24, 20.0, flank_points=10)


def test_gear_flank_is_exact_involute():
    m, z, a = 2.0, 40, 20.0
    side, _, _ = gears.space_side(m, z, a)
    d = gears.gear_dims(m, z, a)
    rb, rp = d["base_radius"], d["pitch_radius"]
    alpha = math.radians(a)
    worst = 0.0
    for r, ang in side[gears.FILLET_POINTS + 2:]:
        if r < rb * 1.0005:
            continue
        half = math.pi / (2 * z) + gears.inv(alpha) - gears.inv(math.acos(rb / r))
        expect = math.pi / z - half  # demi-ouverture du creux à ce rayon
        worst = max(worst, abs(ang - expect) * r)
    assert worst < 1e-6  # mm


def test_gear_root_fillet_radius_is_038_module():
    m, z = 2.0, 80
    side, root_half, rho = gears.space_side(m, z, 20.0)
    assert rho == pytest.approx(0.38 * m)
    fil = [(r * math.cos(a), r * math.sin(a)) for r, a in side[: gears.FILLET_POINTS + 1]]
    assert _fit_circle(fil) == pytest.approx(0.38 * m, rel=0.15)  # trochoïde ≈ arc de rayon 0,38·m pour un grand z
    # pas d'arête vive entre le flanc et le pied : virage maximal d'un segment au suivant < 25° sur le profil complet
    prof = gears.gear_profile(m, 24, 20.0)
    prof = prof + prof[:2]
    turns = []
    for p0, p1, p2 in zip(prof, prof[1:], prof[2:]):
        a1 = math.atan2(p1[1] - p0[1], p1[0] - p0[0])
        a2 = math.atan2(p2[1] - p1[1], p2[0] - p1[0])
        turns.append(abs((a2 - a1 + math.pi) % (2 * math.pi) - math.pi))
    # les seuls virages marqués sont à la tête (dent vive) ; le pied est lissé : on contrôle les points proches du fond
    rf = gears.gear_dims(m, 24, 20.0)["root_radius"]
    near_root = [t for t, p in zip(turns, prof[1:]) if math.hypot(*p) < rf + 0.5 * m]
    assert max(near_root) < math.radians(25)


def test_gear_warnings_undercut_and_interference():
    assert gears.undercut_limit(20.0) == pytest.approx(17.097, abs=0.01)
    assert gears.gear_warnings(24, 20.0) == []
    w = gears.gear_warnings(12, 20.0)
    assert w and "sous-taillage" in w[0]
    assert "fort" in gears.gear_warnings(9, 20.0)[0]
    ok = gears.interference_check(2.0, 18, 36, 20.0)
    assert not ok["interference"] and ok["gear_1_tip_margin_mm"] > 0
    bad = gears.interference_check(2.0, 9, 60, 20.0)
    assert bad["interference"] and bad["gear_2_tip_margin_mm"] < 0  # la grande roue attaque le pied sous-taillé du pignon
    _, ctx, _ = build_parts("gear_pair", module=2.0, teeth_1=9, teeth_2=60)
    assert any("Interférence" in x for x in ctx["warnings"]) and any("sous-taillage" in x for x in ctx["warnings"])
    _, ctx2, _ = build_parts("gear_pair", module=2.0, teeth_1=18, teeth_2=36)
    assert not any("Interférence" in x or "sous-taillage" in x for x in ctx2["warnings"])
    _, ctx3, _ = build_parts("spur_gear", teeth=12)
    assert any("sous-taillage" in x for x in ctx3["warnings"])


def _rot(wp, deg, center=(0, 0)):
    return wp.val().rotate(cq.Vector(center[0], center[1], 0), cq.Vector(center[0], center[1], 1), deg)


def test_gear_pair_meshes_center_distance_and_flank_backlash():
    m, z1, z2, bl = 2.0, 18, 30, 0.4
    parts, ctx, p = build_parts("gear_pair", module=m, teeth_1=z1, teeth_2=z2, backlash=bl, bore_1=0, bore_2=0, thickness=4)
    cd = m * (z1 + z2) / 2
    assert ctx["derived"]["center_distance"] == pytest.approx(cd)  # entraxe = m (z1+z2)/2
    g1, g2 = parts["gear_1"], parts["gear_2"]
    bb1, bb2 = g1.val().BoundingBox(), g2.val().BoundingBox()
    assert (bb2.center.x - bb1.center.x) == pytest.approx(cd, abs=0.05)
    base2 = g2.val()

    def overlap(deg1):
        # g1 tourne de deg1 ; g2 est maintenu : on mesure le volume commun
        return _rot(g1, deg1).intersect(base2).Volume()

    # 1) engrènement sans interférence sur un pas angulaire (g2 entraîné : rotation inverse au rapport des dents)
    pitch_deg = 360.0 / z1
    for k in range(5):
        a1 = k * pitch_deg / 5
        a2 = -a1 * z1 / z2
        g2r = _rot(g2, a2, (cd, 0))
        assert _rot(g1, a1).intersect(g2r).Volume() < 0.05, (k, a1)
    # 2) jeu au flanc : amplitude de rotation libre de g1, g2 immobile, == jeu / rayon primitif
    def contact_limit(sign):
        lo, hi = 0.0, 3.0
        for _ in range(11):
            mid = (lo + hi) / 2
            if overlap(sign * mid) > 0.05:
                hi = mid
            else:
                lo = mid
        return lo

    free = contact_limit(+1) + contact_limit(-1)  # degrés
    expect = math.degrees(bl / (m * z1 / 2))
    assert free == pytest.approx(expect, rel=0.3), (free, expect)


# --- 3. charnière print-in-place -----------------------------------------------------------------


def overhang_area(wp):
    """Aire (mm²) des facettes en surplomb > 45° (hors plat horizontal) hors du plateau."""
    v, t = tessellate(wp.val(), 0.02, 0.2)
    tri = v[t]
    n = np.cross(tri[:, 1] - tri[:, 0], tri[:, 2] - tri[:, 0])
    ln = np.maximum(np.linalg.norm(n, axis=1), 1e-12)
    nz = n[:, 2] / ln
    sel = (nz < -0.72) & (nz > -0.9999) & (tri[:, :, 2].max(axis=1) > 0.05)
    return float((0.5 * ln)[sel].sum())


def test_hinge_pin_teardrop_prints_without_support():
    round_parts, rctx, _ = build_parts("hinge_print_in_place", teardrop=False)
    tear_parts, tctx, _ = build_parts("hinge_print_in_place", teardrop=True)
    for k in ("hinge_leaf_a", "hinge_leaf_b"):
        assert overhang_area(tear_parts[k]) == pytest.approx(0.0, abs=0.05), k
    assert overhang_area(round_parts["hinge_leaf_a"]) + overhang_area(round_parts["hinge_leaf_b"]) > 20  # l'axe rond, lui, surplombe
    assert tctx["derived"]["teardrop"] is True and rctx["derived"]["teardrop"] is False
    a, b = tear_parts["hinge_leaf_a"].val(), tear_parts["hinge_leaf_b"].val()
    assert a.isValid() and b.isValid()
    assert a.intersect(b).Volume() == pytest.approx(0, abs=1e-6)
    assert a.distance(b) == pytest.approx(0.3, abs=0.02)  # jeu radial conservé (pas de contact, pas de soudure)
    # l'axe reste dans A et le nœud de B l'entoure
    R = tctx["derived"]["knuckle_radius"]
    x_b = 1 * (tctx["derived"]["knuckle_length"] + 0.3) + 1.0
    assert inside(tear_parts["hinge_leaf_a"], x_b, 0, R) and not inside(tear_parts["hinge_leaf_b"], x_b, 0, R)


def test_hinge_teardrop_falls_back_when_wall_too_thin():
    _, ctx, _ = build_parts("hinge_print_in_place", knuckle_wall=0.8, pin_diameter=6.0, thickness=2.0)
    assert ctx["derived"]["teardrop"] is False and any("goutte" in w for w in ctx["warnings"])


def test_hinge_clearance_comes_from_calibration_rule():
    assert REG["hinge_print_in_place"].schema()["params"][-2]["auto"] == "gap_pip"


# --- 4-6. séries de calibration --------------------------------------------------------------------


def test_series_kinds_pieces_watertight_and_bounded(tmp_path):
    r = calibration.run({"out_dir": str(tmp_path), "kinds": list(calibration.SERIES_KINDS), "flow_ratios": [0.96, 1.0],
                         "pressure_advance_values": [0.02, 0.05], "retraction_lengths": [0.4, 0.8],
                         "volumetric_speeds": [12, 20], "first_layer_values": [0.2, 0.24]})
    by = {}
    for p in r["pieces"]:
        by.setdefault(p["kind"], []).append(p)
        for part in p["parts"]:
            assert part["valid"] and part["tessellation"]["watertight"] is True, (p["name"], part)
    assert {k: len(v) for k, v in by.items()} == {"shrink_sizes": 2, "flow_ratio": 2, "pressure_advance": 2, "retraction": 2,
                                                  "volumetric_speed": 2, "first_layer": 2}
    # flux : une pièce par valeur, mode vase pour une paroi d'une ligne
    assert [x["slicing_overrides"][0] for x in by["flow_ratio"]] == [
        {"key": "filament_flow_ratio", "value": "0.96", "target": "filament"},
        {"key": "filament_flow_ratio", "value": "1", "target": "filament"}]
    assert by["flow_ratio"][0]["slicing_overrides"][1]["key"] == "spiral_mode"
    # aucune surcharge G-code
    for p in r["pieces"]:
        for o in p.get("slicing_overrides", []) + p.get("slicing_overrides_optional", []):
            assert not o["key"].endswith("_gcode")
    # avance de pression : la clé n'existe pas dans les profils BBL -> aucune surcharge, réglage manuel documenté
    pa = by["pressure_advance"][0]
    assert "slicing_overrides" not in pa and pa["profile_key_available"] is False and pa["manual_settings"][0]["value"] == 0.02
    # rétraction : clé machine seule (filament_retraction_length n'est pas dans la liste blanche des surcharges)
    rt = by["retraction"][1]
    assert rt["slicing_overrides"] == [{"key": "retraction_length", "value": "0.8", "target": "machine"}]
    assert "slicing_overrides_optional" not in rt
    # débit volumétrique : la vitesse de paroi dépasse la limite testée (sinon la limite ne plafonne rien)
    for p in by["volumetric_speed"]:
        v = float(p["series_value"])
        wall_speed = float(next(o["value"] for o in p["slicing_overrides"] if o["key"] == "outer_wall_speed"))
        assert wall_speed * 0.2 * 0.45 > v
    # première couche : clé par paramètre
    assert by["first_layer"][0]["slicing_overrides"][0]["key"] == "initial_layer_print_height"
    assert by["shrink_sizes"][0]["bbox_mm"]["size"] == pytest.approx([20, 20, 20], abs=0.01)
    assert by["shrink_sizes"][1]["info"]["inner_nominal_mm"] == 68.0


def test_series_values_are_bounded(tmp_path):
    for key, bad in (("flow_ratios", [1.5]), ("flow_ratios", [0.5]), ("retraction_lengths", [5.0]), ("volumetric_speeds", [80]),
                     ("pressure_advance_values", [0.9]), ("first_layer_values", [3.0]), ("shrink_sizes", [20, 25]),
                     ("shrink_sizes", [20, 20, 80])):
        kind = {"flow_ratios": "flow_ratio", "retraction_lengths": "retraction", "volumetric_speeds": "volumetric_speed",
                "pressure_advance_values": "pressure_advance", "first_layer_values": "first_layer", "shrink_sizes": "shrink_sizes"}[key]
        with pytest.raises(GeometryError):
            calibration.run({"out_dir": str(tmp_path), "kinds": [kind], key: bad})
    with pytest.raises(GeometryError):
        calibration.run({"out_dir": str(tmp_path), "kinds": ["first_layer"], "first_layer_param": "inconnu"})
    r = calibration.run({"out_dir": str(tmp_path), "kinds": ["first_layer"], "first_layer_param": "temperature",
                         "first_layer_values": [205, 215]})
    assert r["pieces"][0]["slicing_overrides"][0] == {"key": "nozzle_temperature_initial_layer", "value": "205", "target": "filament"}


def test_default_kinds_unchanged(tmp_path):
    r = calibration.run({"out_dir": str(tmp_path), "temperatures": [200]})
    assert {p["kind"] for p in r["pieces"]} == set(calibration.CLASSIC_KINDS)
