"""Validate design trace completeness; never claims visual or user-research success."""
import argparse
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
DOC = ROOT / 'docs/design/round-03'
GROUPS = {'G': 7, 'N': 3, 'L': 9, 'C': 8, 'B': 5, 'V': 8, 'T': 6, 'H': 5, 'Q': 4}

def check(stage='handoff'):
    doc = json.loads((DOC / 'coverage.json').read_text())
    expected = {f'{p}{i:02d}' for p, n in GROUPS.items() for i in range(1, n + 1)}
    rows = {item['id']: item for item in doc['items']}
    assert len(rows) == len(doc['items']) == 55 and set(rows) == expected
    screens = {f'S{i:02d}' for i in range(1, 17)}
    visited, visiting = set(), set()

    def walk(key):
        assert key not in visiting, f'cycle at {key}'
        if key in visited:
            return
        visiting.add(key)
        for parent in rows[key]['parents']:
            assert parent in rows and parent != key, f'invalid parent {key}: {parent}'
            walk(parent)
        visiting.remove(key)
        visited.add(key)

    used = set()
    for key, item in rows.items():
        walk(key)
        assert item['screens'] and set(item['screens']) <= screens, key
        used.update(item['screens'])
        p = DOC / item['decision_file']
        assert p.is_file() and key in p.read_text(), f'missing decision {key}'
        assert (DOC / item['build_spec']).is_file() and (DOC / item['acceptance']).is_file()
        assert item['status'] in {'documented', 'implemented', 'lead-reviewed'}, key
        if stage == 'ui':
            assert item['status'] == 'lead-reviewed' and item['evidence'], f'UI not reviewed: {key}'
            for path in item['evidence']:
                assert (ROOT / path).is_file(), f'missing evidence {path}'
    assert used == screens
    if stage in {'handoff', 'ui'}:
        for name in ['00_CONTEXT_AND_SCOPE.md', '01_INTENT_TO_EXPERIENCE.md',
                     '02_SCENARIOS_AND_INTERACTIONS.md', '03_VISUAL_AND_MOTION.md',
                     '04_TRADEOFFS_AND_VALIDATION.md', '05_UI_BUILD_SPEC.md',
                     '06_EXECUTION_PACKET.md', '07_ACCEPTANCE.md', 'STATUS.md']:
            assert (DOC / name).is_file(), name
    return {'valid': True, 'stage': stage, 'items': len(rows), 'screen_families': len(screens),
            'lead_reviewed': sum(x['status'] == 'lead-reviewed' for x in rows.values()),
            'ui_completion_proven': stage == 'ui'}

if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--stage', choices=['reasoning', 'handoff', 'ui'], default='handoff')
    args = parser.parse_args()
    print(json.dumps(check(args.stage), ensure_ascii=False))
