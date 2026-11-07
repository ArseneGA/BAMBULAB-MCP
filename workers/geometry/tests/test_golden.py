"""Compare chaque gabarit (paramètres par défaut) à son golden file avec tolérance.

Régénération volontaire : voir scripts/regen_golden.py.
"""

import json
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).parent))
pytest.importorskip("cadquery")

from golden_util import GOLDEN_DIR, summarize  # noqa: E402

from bambu_geometry import library  # noqa: E402

IDS = sorted(library._load())
REL = 0.005  # 0,5 % : tolère les écarts de tessellation / de version d'OCCT sans masquer une vraie dérive
ABS = 0.05


def test_every_template_has_a_golden():
    missing = [i for i in IDS if not (GOLDEN_DIR / f"{i}.json").exists()]
    assert not missing, f"golden manquant (lancer scripts/regen_golden.py) : {missing}"
    extra = [p.stem for p in GOLDEN_DIR.glob("*.json") if p.stem not in IDS]
    assert not extra, f"golden orphelin : {extra}"


@pytest.mark.parametrize("tpl_id", IDS)
def test_template_matches_golden(tpl_id):
    gold = json.loads((GOLDEN_DIR / f"{tpl_id}.json").read_text(encoding="utf-8"))
    cur = summarize(tpl_id)
    assert cur["template_version"] == gold["template_version"], "version du gabarit changée : régénérer le golden"
    assert cur["volume_mm3"] == pytest.approx(gold["volume_mm3"], rel=REL, abs=ABS)
    assert cur["area_mm2"] == pytest.approx(gold["area_mm2"], rel=0.02, abs=ABS)  # l'aire dépend de la tessellation
    assert cur["bbox_size_mm"] == pytest.approx(gold["bbox_size_mm"], rel=REL, abs=ABS)
    for k in ("parts", "solids", "components", "watertight"):
        assert cur[k] == gold[k], k
    assert len(cur["fingerprint"]) == 16 and len(gold["fingerprint"]) == 16
