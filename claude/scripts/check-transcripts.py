#!/usr/bin/env python3
"""Offline fixture integrity/secret check. Does not launch a CLI."""
import hashlib
import importlib.util
import json
import pathlib
import sys
sys.dont_write_bytecode = True

root = pathlib.Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('sanitizer', root/'scripts/sanitize-transcript.py')
sanitizer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sanitizer)
sanitizer.selftest()
base = root/'testdata/recordings/real-sanitized/s1-auth-blocked'
manifest = json.loads((base/'manifest.json').read_text())
for path in base.iterdir():
    if sanitizer.SECRET.search(path.read_text()):
        raise SystemExit('fixture secret check failed (contents withheld)')
for name, digest in manifest['files'].items():
    if hashlib.sha256((base/name).read_bytes()).hexdigest() != digest:
        raise SystemExit('fixture digest mismatch')
if hashlib.sha256((base/'wire.jsonl').read_bytes()).hexdigest() != manifest['wire_sha256']:
    raise SystemExit('wire digest mismatch')
frames = [json.loads(line) for line in (base/'wire.jsonl').read_text().splitlines()]
for i, frame in enumerate(frames, 1):
    if frame['ordinal'] != i:
        raise SystemExit('ordinal mismatch')
for relation in json.loads((base/'id-relations.json').read_text()):
    def present(value, field, replacement):
        if isinstance(value, dict):
            return any((k == field and v == replacement) or present(v, field, replacement) for k,v in value.items())
        if isinstance(value, list):
            return any(present(v, field, replacement) for v in value)
        return False
    for ref in relation['references']:
        if not present(frames[ref['ordinal']-1]['data'], ref['field'], relation['replacement']):
            raise SystemExit('ID relationship mismatch')
print('fixture digests, ID references, ordinals and secret check passed')
