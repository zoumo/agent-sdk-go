#!/usr/bin/env python3
"""Fail-closed publication of private captures. No raw/config/hook payload output."""
import argparse
import copy
import hashlib
import json
import pathlib
import re
import shutil
import tempfile

SCHEMA = json.loads(pathlib.Path(__file__).with_name('recording-schema.json').read_text())
SECRET = re.compile(r'(?i)(?:sk-[a-z0-9_-]{12,}|bearer\s+[a-z0-9._-]+|[a-z0-9_.+-]+@[a-z0-9.-]+\.[a-z]{2,}|/Users/|/home/|\bjim\b)')
SECRET_KEYS = {'authorization', 'headers', 'access_token', 'refresh_token', 'api_key', 'password', 'id_token'}
OMIT = {'hook/started', 'hook/completed', 'configWarning', 'deprecationNotice', 'mcpServerStatus/updated', 'account/updated', 'account/rateLimits/updated', 'codex/event', 'remoteControl/status/changed', 'warning', 'turn/moderationMetadata'}


def scan(value):
    if isinstance(value, dict):
        for k, v in value.items():
            if k.lower() in SECRET_KEYS or SECRET.search(k):
                raise ValueError('sensitive key')
            scan(v)
    elif isinstance(value, list):
        for v in value:
            scan(v)
    elif isinstance(value, str) and SECRET.search(value):
        raise ValueError('sensitive text')


def validate(v, schema):
    if '$ref' in schema:
        return validate(v, SCHEMA['definitions'][schema['$ref'].split('/')[-1]])
    if 'allOf' in schema:
        for option in schema['allOf']:
            validate(v, option)
        if not any(k in schema for k in ('type', 'properties', 'anyOf', 'oneOf')):
            return
    for union in ('anyOf', 'oneOf'):
        if union in schema:
            for option in schema[union]:
                try:
                    validate(v, option)
                    return
                except ValueError:
                    pass
            raise ValueError('no allowed schema variant')
    if 'enum' in schema and v not in schema['enum']:
        raise ValueError('unknown enum')
    if 'const' in schema and v != schema['const']:
        raise ValueError('wrong constant')
    t = schema.get('type')
    types = t if isinstance(t, list) else [t]
    kind = 'null' if v is None else 'boolean' if isinstance(v, bool) else 'integer' if isinstance(v, int) else 'number' if isinstance(v, float) else 'string' if isinstance(v, str) else 'array' if isinstance(v, list) else 'object'
    if t and kind not in types and not (kind == 'integer' and 'number' in types):
        raise ValueError('wrong JSON type')
    if isinstance(v, dict):
        properties = schema.get('properties', {})
        if any(k not in v for k in schema.get('required', [])):
            raise ValueError('missing required member')
        for k, x in v.items():
            if k in properties:
                # These are explicitly bounded synthetic JSON extension surfaces.
                if k == 'inputSchema':
                    if x is not None:
                        allowed = {'type', 'properties', 'required', 'additionalProperties', 'description', 'enum'}
                        def check_schema(node):
                            if isinstance(node, dict):
                                if set(node) - allowed:
                                    raise ValueError('unexpected tool schema')
                                for name, child in node.get('properties', {}).items():
                                    if name not in ('path', 'text'):
                                        raise ValueError('unexpected tool argument')
                                    check_schema(child)
                        check_schema(x)
                    continue
                if k == 'arguments' and isinstance(x, dict):
                    if set(x) - {'path', 'text'}:
                        raise ValueError('unexpected tool arguments')
                    continue
                validate(x, properties[k])
            elif isinstance(schema.get('additionalProperties'), dict):
                validate(x, schema['additionalProperties'])
            else:
                raise ValueError('unknown object field')
    elif isinstance(v, list):
        for x in v:
            validate(x, schema.get('items', {}))


class Sanitizer:
    def __init__(self, root, home):
        self.root, self.home = str(root), str(home)
        self.maps = {}
        self.occurrences = {}
        self.values = {}

    def ident(self, namespace, value, ordinal):
        if value is None:
            return value
        if not isinstance(value, (str, int)) or isinstance(value, bool):
            raise ValueError('invalid identity type')
        key = (namespace, type(value).__name__, value)
        if key not in self.maps:
            n = 1 + sum(k[0] == namespace and k[1] == type(value).__name__ for k in self.maps)
            self.maps[key] = n + 100 if isinstance(value, int) else f'{namespace}-{n:03d}'
            if isinstance(value, str):
                self.values.setdefault(value, self.maps[key])
        self.occurrences.setdefault(key, set()).add(ordinal)
        return self.maps[key]

    def discover(self, v, ordinal, parent=''):
        if isinstance(v, dict):
            for k, x in v.items():
                ns = {'sessionId': 'session', 'forkedFromId': 'thread', 'threadId': 'thread', 'parentThreadId': 'thread', 'agentThreadId': 'thread', 'turnId': 'turn', 'rootTurnId': 'turn', 'parentTurnId': 'turn', 'itemId': 'item', 'callId': 'item', 'requestId': 'rpc-server'}.get(k)
                if k == 'id':
                    ns = {'thread': 'thread', 'turn': 'turn', 'item': 'item', 'questions': 'question'}.get(parent)
                if ns and x is not None:
                    self.ident(ns, x, ordinal)
                self.discover(x, ordinal, k)
        elif isinstance(v, list):
            for x in v:
                self.discover(x, ordinal, parent)

    def rewrite(self, v, ordinal, parent=''):
        if isinstance(v, dict):
            out = {}
            for k, x in v.items():
                ns = {'sessionId': 'session', 'forkedFromId': 'thread', 'threadId': 'thread', 'parentThreadId': 'thread', 'agentThreadId': 'thread', 'turnId': 'turn', 'rootTurnId': 'turn', 'parentTurnId': 'turn', 'itemId': 'item', 'callId': 'item', 'requestId': 'rpc-server'}.get(k)
                if k == 'id':
                    ns = {'thread': 'thread', 'turn': 'turn', 'item': 'item', 'questions': 'question'}.get(parent)
                key = self.values.get(k, k) if parent == 'answers' else k
                if ns and x is not None:
                    out[key] = self.ident(ns, x, ordinal)
                elif k == 'userAgent':
                    out[key] = 'codex-fixture/0.162.1'
                elif k == 'codexHome':
                    out[key] = '/fixture/home'
                elif k == 'instructionSources' and isinstance(x, list):
                    out[key] = [f'/fixture/instructions/source-{i}' for i in range(1, len(x)+1)]
                elif k == 'disabledPluginIds' and isinstance(x, list):
                    out[key] = [f'plugin-{i}' for i in range(1, len(x)+1)]
                else:
                    out[key] = self.rewrite(x, ordinal, k)
            return out
        if isinstance(v, list):
            return [self.rewrite(x, ordinal, parent) for x in v]
        if isinstance(v, str):
            if v in self.values:
                return self.values[v]
            text = v.replace(self.root, '/fixture/project').replace(self.home, '/fixture/home')
            for raw, replacement in sorted(self.values.items(), key=lambda pair: -len(pair[0])):
                if len(raw) >= 8:
                    text = text.replace(raw, str(replacement))
            return text
        return v


def transform(frames, root, home, sanitizer=None):
    sanitizer = sanitizer or Sanitizer(root, home)
    rpc = {}
    retained, omitted = [], []
    for f in frames:
        wire = f['wire']
        method = wire.get('method')
        if method in OMIT or (method and method.startswith('codex/event/')) or wire.get('params', {}).get('item', {}).get('type') == 'hookPrompt':
            omitted.append({'ordinal': f['ordinal'], 'reason': 'excluded hook/integration/legacy diagnostic payload'})
            continue
        if set(wire) - {'id', 'method', 'params', 'result', 'error', 'jsonrpc', 'emittedAtMs'}:
            raise ValueError('unexpected envelope member')
        if 'emittedAtMs' in wire and not isinstance(wire['emittedAtMs'], int):
            raise ValueError('invalid emission timestamp')
        if method:
            if method not in SCHEMA['methods']:
                raise ValueError('method not whitelisted')
            rule = SCHEMA['methods'][method]
            if rule['params']:
                validate(wire.get('params'), SCHEMA['definitions'][rule['params']])
            if 'id' in wire:
                rpc[(f['direction'], type(wire['id']).__name__, wire['id'])] = method
        elif 'id' in wire:
            opposite = 'c2s' if f['direction'] == 's2c' else 's2c'
            key = (opposite, type(wire['id']).__name__, wire['id'])
            if key not in rpc:
                raise ValueError('uncorrelated response')
            rule = SCHEMA['methods'][rpc[key]]
            if 'error' in wire:
                raise ValueError('error response requires quarantine review')
            if rule['response']:
                validate(wire.get('result'), SCHEMA['definitions'][rule['response']])
        sanitizer.discover(wire, f['ordinal'])
        retained.append(f)
    for f in retained:
        w = f['wire']
        if 'id' in w:
            ns = 'rpc-client' if ('method' in w) == (f['direction'] == 'c2s') else 'rpc-server'
            sanitizer.ident(ns, w['id'], f['ordinal'])
    out = []
    timestamps = [f['wire']['emittedAtMs'] for f in retained if 'emittedAtMs' in f['wire']]
    baseline = min(timestamps) if timestamps else 0
    for f in retained:
        clean = copy.deepcopy(f)
        clean['wire'] = sanitizer.rewrite(f['wire'], f['ordinal'])
        if 'id' in f['wire']:
            ns = 'rpc-client' if ('method' in f['wire']) == (f['direction'] == 'c2s') else 'rpc-server'
            clean['wire']['id'] = sanitizer.ident(ns, f['wire']['id'], f['ordinal'])
        if 'emittedAtMs' in clean['wire']:
            clean['wire']['emittedAtMs'] -= baseline
        scan(clean)
        out.append(clean)
    # Bijection and wire scalar ID types must survive. Original values never publish.
    if len(set((k[0], k[1], v) for k, v in sanitizer.maps.items())) != len(sanitizer.maps):
        raise ValueError('mapping collision')
    relations = [{'namespace': k[0], 'jsonType': k[1], 'replacement': v, 'frames': sorted(sanitizer.occurrences[k])} for k, v in sanitizer.maps.items()]
    return out, omitted, relations


def publish(raw_dir, destination, story, metadata):
    frames = [json.loads(line) for line in (raw_dir / 'raw.jsonl').read_text().splitlines()]
    sanitizer = Sanitizer(metadata['root'], metadata['home'])
    clean, omitted, relations = transform(frames, metadata['root'], metadata['home'], sanitizer)
    if destination.exists():
        raise ValueError('destination already exists')
    stage = pathlib.Path(tempfile.mkdtemp(prefix='publish-', dir=destination.parent))
    try:
        (stage / 'protocol.jsonl').write_text(''.join(json.dumps(f, separators=(',', ':'))+'\n' for f in clean))
        (stage / 'id-relations.json').write_text(json.dumps(relations, indent=2)+'\n')
        observed = [sanitizer.rewrite(o, 0) for o in metadata.get('observations', []) if o.get('method') not in OMIT]
        scan(observed)
        (stage / 'expected-observations.json').write_text(json.dumps({'provenance': 'live-sdk-derived-sanitized-subset', 'note': 'Local sequence gaps reflect omitted diagnostics; replay sequences need not match.', 'completed': metadata['completed'], 'tools': metadata['tools'], 'questions': metadata['questions'], 'observations': observed}, indent=2)+'\n')
        transport = json.loads((raw_dir/'transport.json').read_text())
        methods = {}
        for f in clean:
            name = f['wire'].get('method')
            if name:
                methods.setdefault(name, []).append(f['ordinal'])
        manifest = {'schemaVersion': 1, 'story': story, 'provenance': 'real-sanitized', 'cliVersion': '0.162.1', 'cliSHA256': metadata['cliSHA256'], 'sdkCommit': metadata['sdkCommit'], 'storyDocsCommit': metadata['sdkCommit'], 'protocolCommit': SCHEMA['source_commit'], 'recorderVersion': 1, 'sanitizerVersion': 1, 'launchPolicy': 'existing-auth; managed enterprise audit unchanged; other integrations disabled; no environments; bounded dynamic tools only', 'promptSHA256': metadata['promptSHA256'], 'inputSHA256': hashlib.sha256(b'17\n25\n').hexdigest(), 'transport': transport, 'rootCalls': 1, 'childAgents': 0, 'usage': None, 'costUSD': None, 'usageCostNote': 'No reliable per-scenario monetary meter; usage frames retained where observed.', 'coverageFrames': methods, 'omissions': omitted, 'gaps': ['Managed hook/integration diagnostic payloads excluded; ordinal gaps retained.', 'No native shell/approval or subagent/background coverage; managed auto-attach remains best-effort.'], 'scan': 'pass', 'relations': 'typed bijection and direction/order preserved', 'rawPublished': False}
        usages = [f['wire']['params']['tokenUsage'] for f in clean if f['wire'].get('method') == 'thread/tokenUsage/updated']
        manifest['usage'] = {'scope': 'last observed root native cumulative total; not sum of updates', 'native': usages[-1]} if usages else None
        manifest['actualSessionMilliseconds'] = metadata['milliseconds']
        manifest['captureComplete'] = bool(metadata['completed'] and transport['reaped'] and not metadata['sessionError'])
        manifest['files'] = {f.name: hashlib.sha256(f.read_bytes()).hexdigest() for f in stage.iterdir()}
        scan(manifest)
        (stage/'manifest.json').write_text(json.dumps(manifest, indent=2)+'\n')
        stage.rename(destination)
    except BaseException:
        shutil.rmtree(stage)
        raise


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('raw_dir', type=pathlib.Path)
    parser.add_argument('destination', type=pathlib.Path)
    parser.add_argument('--story', required=True, choices=['s1', 's2'])
    args = parser.parse_args()
    metadata = json.loads((args.raw_dir/'metadata.private.json').read_text())
    publish(args.raw_dir, args.destination, args.story, metadata)
    print('Published scanned sanitized capture; raw content not printed.')


if __name__ == '__main__':
    main()
