#!/usr/bin/env python3
"""Enumerate nullable properties without resolving or changing the contract."""
import hashlib
import json
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[4]
sys.path.insert(0, str(ROOT / 'api/tests'))
from contract_support import Contract  # noqa: E402


def collect(value, path=(), required=None, rows=None):
    if rows is None:
        rows = []
    if isinstance(value, dict):
        if value.get('nullable') is True:
            rows.append({
                'path': '/' + '/'.join(part.replace('~', '~0').replace('/', '~1') for part in path),
                'required': required,
                'read_only': value.get('readOnly') is True,
            })
        for key, child in value.items():
            if key == 'properties' and isinstance(child, dict):
                names = value.get('required', [])
                for name, prop in child.items():
                    collect(prop, path + ('properties', str(name)), name in names, rows)
            elif key != 'nullable':
                collect(child, path + (str(key),), None, rows)
    elif isinstance(value, list):
        for index, child in enumerate(value):
            collect(child, path + (str(index),), None, rows)
    return rows


contract = Contract(ROOT / 'api/openapi.yaml')
fields = collect(contract.doc)
summary = {
    'contract_sha256': hashlib.sha256(contract.source.encode('utf-8')).hexdigest(),
    'total': len(fields),
    'components': sum(row['path'].startswith('/components/') for row in fields),
    'paths': sum(row['path'].startswith('/paths/') for row in fields),
    'required': sum(row['required'] is True for row in fields),
    'optional': sum(row['required'] is False for row in fields),
    'other': sum(row['required'] is None for row in fields),
    'fields': fields,
}
if not fields:
    raise SystemExit('no nullable properties found; inspect the contract and inventory')
print(json.dumps(summary, ensure_ascii=False, indent=2))
