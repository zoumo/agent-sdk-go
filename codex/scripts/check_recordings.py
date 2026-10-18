#!/usr/bin/env python3
"""Offline fixture hashes, publication allowlist, correlations and secret gate."""
import hashlib
import json
import pathlib

from recording_sanitize import SCHEMA, scan, validate


def check(root):
    for story in ('s1', 's2'):
        directory = root/'testdata/recordings/real-sanitized'/story
        manifest = json.loads((directory/'manifest.json').read_text())
        scan(manifest)
        assert manifest['provenance'] == 'real-sanitized'
        assert manifest['cliVersion'] == '0.162.1'
        for name, digest in manifest['files'].items():
            assert '/' not in name and '\\' not in name
            assert hashlib.sha256((directory/name).read_bytes()).hexdigest() == digest
            if name.endswith('.json'):
                scan(json.loads((directory/name).read_text()))
        frames = [json.loads(line) for line in (directory/'protocol.jsonl').read_text().splitlines()]
        requests, active = {}, set()
        last = 0
        for frame in frames:
            scan(frame)
            assert frame['ordinal'] > last
            last = frame['ordinal']
            wire = frame['wire']
            method = wire.get('method')
            if method:
                assert method in SCHEMA['methods']
                rule = SCHEMA['methods'][method]
                if rule['params']:
                    validate(wire.get('params'), SCHEMA['definitions'][rule['params']])
                if 'id' in wire:
                    key = (frame['direction'], type(wire['id']).__name__, wire['id'])
                    assert key not in requests
                    requests[key] = method
                    active.add(key)
            elif 'id' in wire:
                opposite = 'c2s' if frame['direction'] == 's2c' else 's2c'
                key = (opposite, type(wire['id']).__name__, wire['id'])
                assert key in active
                active.remove(key)
                rule = SCHEMA['methods'][requests[key]]
                if rule['response']:
                    validate(wire['result'], SCHEMA['definitions'][rule['response']])
        assert not active
        assert manifest['transport']['reaped']
        assert manifest['captureComplete']
        prompt = next(f['wire']['params']['input'][0]['text'] for f in frames if f['wire'].get('method') == 'turn/start')
        assert hashlib.sha256(prompt.encode()).hexdigest() == manifest['promptSHA256']
        relations = json.loads((directory/'id-relations.json').read_text())
        by_ordinal = {f['ordinal']: f['wire'] for f in frames}
        def occurs(value, node):
            if type(value) is type(node) and value == node:
                return True
            if isinstance(node, dict):
                return any(occurs(value, x) for x in node.values()) or value in node
            if isinstance(node, list):
                return any(occurs(value, x) for x in node)
            return False
        for relation in relations:
            assert all(occurs(relation['replacement'], by_ordinal[i]) for i in relation['frames'])
        print(f'{story}: hashes, whitelist, secrets and request correlations passed ({len(frames)} retained frames)')


if __name__ == '__main__':
    check(pathlib.Path(__file__).resolve().parents[1])
