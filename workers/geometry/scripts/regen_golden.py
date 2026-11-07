"""Régénère les golden files des gabarits : `uv run --extra cad python scripts/regen_golden.py [id ...]`.

À lancer depuis workers/geometry après un changement VOULU de géométrie, puis relire le diff de
`tests/golden/*.json` avant de committer. Sans argument, régénère tous les gabarits.
"""

import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "tests"))

from golden_util import GOLDEN_DIR, summarize  # noqa: E402

from bambu_geometry import library  # noqa: E402


def main(argv: list[str]) -> int:
    ids = argv or sorted(library._load())
    GOLDEN_DIR.mkdir(parents=True, exist_ok=True)
    for tpl_id in ids:
        s = summarize(tpl_id)
        (GOLDEN_DIR / f"{tpl_id}.json").write_text(json.dumps(s, indent=2) + "\n", encoding="utf-8")
        print(f"{tpl_id}: volume={s['volume_mm3']} solids={s['solids']} watertight={s['watertight']}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
