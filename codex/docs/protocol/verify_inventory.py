#!/usr/bin/env python3
"""Check the pinned inventory; optionally verify an exact official source checkout.

This is offline documentation verification, not server execution.
"""
import hashlib
import json
import pathlib
import re
import sys

HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parent.parent
inventory = json.loads((HERE / 'capabilities.json').read_text())
provenance = json.loads((HERE / 'sources.json').read_text())
evidence = json.loads((HERE / 'evidence.json').read_text())
source_records = {row['path']: row for row in provenance['sources']}
assert len(source_records) == len(provenance['sources']), 'duplicate source record'
assert inventory['protocol_commit'] == provenance['commit']
assert inventory['version_label'] == provenance['version_label']
records = inventory['methods'] + inventory['controls']
assert len({(r['kind'], r['method']) for r in inventory['methods']}) == len(inventory['methods'])
assert len({r['name'] for r in inventory['controls']}) == len(inventory['controls'])
for kind, count in inventory['counts'].items():
    assert sum(r['kind'] == kind for r in inventory['methods']) == count, kind
for derived in provenance['derived_material']:
    snapshot_path = ROOT / derived['path']
    assert hashlib.sha256(snapshot_path.read_bytes()).hexdigest() == derived['snapshot_sha256'], 'derived snapshot mismatch'
known_evidence = {r['id'] for r in evidence['records']}
for row in records:
    assert row['support'] in inventory['status_definitions']
    assert row['source_path'] in source_records, row
    assert row['source_line'] > 0
    assert set(row['evidence']) == {'unit', 'mock', 'race', 'live'}
    for refs in row['evidence'].values():
        assert set(refs) <= known_evidence, refs
# Historical records retain their original paths and outcomes. Explicit migrations
# validate the current check locations without relabeling those old outcomes.
migration = json.loads((HERE / 'test-migrations.json').read_text())
assert re.fullmatch(r'[0-9a-f]{40}', migration['historical_base']), 'invalid migration base'
assert (ROOT / migration['audit']).is_file(), 'missing guarantee audit'
retirements = {row['retired_path']: row for row in migration['retirements']}
assert len(retirements) == len(migration['retirements']), 'duplicate retired path'
used_retirements = {}
for row in evidence['records']:
    for test_file in row.get('test_files', []):
        if (ROOT / test_file).is_file():
            assert test_file not in retirements, 'migration for an existing path: ' + test_file
            continue
        assert test_file in retirements, 'unmapped missing historical path: ' + test_file
        retired = retirements[test_file]
        assert row['id'] in retired['evidence_ids'], 'unmapped historical evidence: ' + row['id']
        used_retirements.setdefault(test_file, set()).add(row['id'])
for path, retired in retirements.items():
    assert set(retired['evidence_ids']) == used_retirements.get(path), 'stale or incomplete migration: ' + path
    assert len(retired['evidence_ids']) == len(set(retired['evidence_ids'])), 'duplicate migration evidence'
    assert retired['replacements'], 'migration without current checks: ' + path
    assert len(retired['replacements']) == len(set(retired['replacements'])), 'duplicate replacement path'
    for replacement in retired['replacements']:
        assert replacement != path and (ROOT / replacement).is_file(), 'missing replacement: ' + replacement

if len(sys.argv) > 1:
    source_root = pathlib.Path(sys.argv[1])
    for path, row in source_records.items():
        raw = (source_root / path).read_bytes()
        assert len(raw) == row['bytes'], path + ': size mismatch'
        assert hashlib.sha256(raw).hexdigest() == row['sha256'], path + ': SHA-256 mismatch'
        blob = hashlib.sha1(b'blob ' + str(len(raw)).encode() + b'\0' + raw).hexdigest()
        assert blob == row['git_blob_sha'], path + ': Git blob mismatch'
    common = 'codex-rs/app-server-protocol/src/protocol/common.rs'
    source = (source_root / common).read_text()
    mapping = {
        'client_request': ('client_request_definitions', 'ClientRequest'),
        'server_request': ('server_request_definitions', 'ServerRequest'),
        'server_notification': ('server_notification_definitions', 'ServerNotification'),
        'client_notification': ('client_notification_definitions', 'ClientNotification'),
    }
    declared = {}
    for kind, (macro, _) in mapping.items():
        start = source.index(macro + '! {')
        end = source.index('\n}', start) + 2
        section = source[start:end]
        request = kind.endswith('request')
        pattern = (r'^    (\w+)(?: => "([^\"]+)")? \{\n(.*?)^    \}' if request else
                   r'^    (\w+)(?: => "([^\"]+)")?\s*(?:\(([^\n]+)\))?,')
        previous = 0
        for match in re.finditer(pattern, section, re.M | re.S if request else re.M):
            variant, method, _ = match.groups()
            prefix = section[previous:match.start()]
            previous = match.end()
            renames = re.findall(r'#\[serde\(rename = "([^\"]+)"\)\]', prefix)
            method = method or (renames[-1] if renames else variant[0].lower() + variant[1:])
            declared[(kind, method)] = (variant, source[:start + match.start()].count('\n') + 1)
    assert set(declared) == {(r['kind'], r['method']) for r in inventory['methods']}, 'registry mismatch'
    for row in inventory['methods']:
        assert declared[(row['kind'], row['method'])] == (row['source_variant'], row['source_line'])
    stable_path = 'codex-rs/app-server-protocol/schema/json/codex_app_server_protocol.schemas.json'
    stable = json.loads((source_root / stable_path).read_text())
    import zstandard  # Needed only for optional verification of the official archive.
    archive_path = 'codex-rs/app-server-protocol/schema/precomputed/app-server-exports-experimental.json.zst'
    archive = json.loads(zstandard.ZstdDecompressor().stream_reader((source_root / archive_path).read_bytes()).read())
    experimental = json.loads(archive['json_schema']['codex_app_server_protocol.schemas.json'])
    for label, schema in [('stable', stable), ('experimental', experimental)]:
        for kind, (_, union) in mapping.items():
            methods = {item['properties']['method']['enum'][0] for item in schema['definitions'][union]['oneOf']}
            for row in inventory['methods']:
                if row['kind'] == kind:
                    assert row['in_' + label + '_schema'] == (row['method'] in methods), row['method']
    print('Verified exact upstream files, registries, and schema memberships.')
print('Inventory consistent:', inventory['counts'], 'controls:', len(inventory['controls']))
