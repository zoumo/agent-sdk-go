#!/usr/bin/env python3
"""Generate the implemented native protocol closure from the pinned official schema.

Usage: python3 scripts/generate_protocol.py [path/to/app-server-exports-experimental.json.zst]
With an argument, verify the original Git blob and refresh the checked-in slice.
Without an argument, regenerate from the checked-in exact-definition slice.
Run gofmt on types/types_generated.go after generation.
"""
import copy
import hashlib
import json
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
PIN = '092d3acd6bec3e3a14bdc7e7a2810ab628ab759d'
BLOB = 'eef430182091bfc6f120c98dda3a3246771de8d1'
SOURCE = 'codex-rs/app-server-protocol/schema/precomputed/app-server-exports-experimental.json.zst'
ROOTS = ['InitializeParams', 'InitializeResponse', 'v2/ThreadStartParams',
         'v2/ThreadStartResponse', 'v2/ThreadReadParams', 'v2/ThreadReadResponse',
         'v2/ThreadTurnsListParams', 'v2/ThreadTurnsListResponse',
         'v2/ThreadItemsListParams', 'v2/ThreadItemsListResponse',
         'v2/ThreadResumeParams', 'v2/ThreadResumeResponse',
         'v2/ThreadUnsubscribeParams', 'v2/ThreadUnsubscribeResponse',
         'v2/TurnStartParams', 'v2/TurnStartResponse',
         'v2/TurnInterruptParams', 'v2/TurnInterruptResponse',
         'v2/TurnStartedNotification', 'v2/TurnCompletedNotification',
         'v2/ItemStartedNotification', 'v2/ItemCompletedNotification',
         'v2/AgentMessageDeltaNotification', 'v2/ErrorNotification',
         'CommandExecutionRequestApprovalParams', 'CommandExecutionRequestApprovalResponse',
         'FileChangeRequestApprovalParams', 'FileChangeRequestApprovalResponse',
         'PermissionsRequestApprovalParams', 'PermissionsRequestApprovalResponse',
         'ToolRequestUserInputParams', 'ToolRequestUserInputResponse',
         'ApplyPatchApprovalParams', 'ApplyPatchApprovalResponse',
         'ExecCommandApprovalParams', 'ExecCommandApprovalResponse',
         'v2/ServerRequestResolvedNotification',
         'v2/ThreadSettingsUpdateParams', 'v2/ThreadSettingsUpdateResponse',
         'v2/ThreadSettingsUpdatedNotification']
SNAPSHOT = ROOT / 'types/schema.json'
if len(sys.argv) > 1:
    raw = pathlib.Path(sys.argv[1]).read_bytes()
    digest = hashlib.sha1(b'blob ' + str(len(raw)).encode() + b'\0' + raw).hexdigest()
    if digest != BLOB:
        raise SystemExit('Input is not the verified upstream Git blob ' + BLOB)
    # Only needed when refreshing; use the existing zstd CLI, no Python bridge.
    import subprocess
    archive = json.loads(subprocess.check_output(['zstd', '-dc', sys.argv[1]]))
    full = json.loads(archive['json_schema']['codex_app_server_protocol.schemas.json'])
    selected = {}
    def visit(node):
        if isinstance(node, dict):
            ref = node.get('$ref')
            if ref:
                key = ref.removeprefix('#/definitions/')
                if key not in selected:
                    value = full['definitions']
                    for part in key.split('/'):
                        value = value[part]
                    selected[key] = value
                    visit(value)
            for value in node.values():
                visit(value)
        elif isinstance(node, list):
            for value in node:
                visit(value)
    for root in ROOTS:
        visit({'$ref': '#/definitions/' + root})
    snapshot = {'source': {'repository': 'https://github.com/openai/codex',
                           'commit': PIN, 'path': SOURCE, 'blob': BLOB},
                'roots': ROOTS, 'definitions': dict(sorted(selected.items()))}
    SNAPSHOT.write_text(json.dumps(snapshot, indent=2, ensure_ascii=False) + '\n')
else:
    snapshot = json.loads(SNAPSHOT.read_text())
    if snapshot['source']['commit'] != PIN or snapshot['source']['blob'] != BLOB:
        raise SystemExit('Snapshot provenance mismatch')

INITIALISMS = {'id': 'ID', 'ids': 'IDs', 'os': 'OS', 'cli': 'CLI', 'api': 'API',
              'mcp': 'MCP', 'http': 'HTTP', 'url': 'URL', 'uri': 'URI', 'ui': 'UI',
              'oauth': 'OAuth', 'pty': 'PTY'}
def ident(value):
    words = re.findall(r'[A-Z]+(?=[A-Z][a-z]|\b)|[A-Z]?[a-z]+|[0-9]+', value)
    return ''.join(INITIALISMS.get(w.lower(), w[0].upper() + w[1:]) for w in words)
def comment(text):
    return '\n'.join('// ' + line for line in text.splitlines()) + '\n' if text else ''
def opaque(node):
    # Schema annotations alone do not restrict the JSON value (e.g. outputSchema).
    return node is True or not (set(node) - {'description', 'title', 'default', 'deprecated', 'examples', '$comment'})
def nullable(node):
    if opaque(node):
        return False
    return ('null' in node.get('type', []) if isinstance(node.get('type'), list) else node.get('type') == 'null') or any(n.get('type') == 'null' for n in node.get('anyOf', []) if isinstance(n, dict))
def nonnull(node):
    node = copy.deepcopy(node)
    if node is True:
        return node
    if isinstance(node.get('type'), list):
        types = [t for t in node['type'] if t != 'null']
        if len(types) == 1:
            node['type'] = types[0]
    if 'anyOf' in node and any(n.get('type') == 'null' for n in node['anyOf'] if isinstance(n, dict)):
        options = [n for n in node.pop('anyOf') if n.get('type') != 'null']
        if len(options) == 1:
            node.update(options[0])
        else:
            node['anyOf'] = options
    if 'allOf' in node and len(node['allOf']) == 1:
        node.update(node.pop('allOf')[0])
    return node

schemas = {ident(k.split('/')[-1]): v for k, v in snapshot['definitions'].items()}
queue = list(schemas)
output = []

def register(name, node):
    if name in schemas:
        if schemas[name] != node:
            raise ValueError('Schema name conflict: ' + name)
    else:
        schemas[name] = node
        queue.append(name)
    return name

def type_of(node, hint):
    node = nonnull(node)
    if opaque(node):
        return 'json.RawMessage'
    if '$ref' in node:
        return ident(node['$ref'].split('/')[-1])
    typ = node.get('type')
    if typ == 'string' and 'enum' not in node:
        return 'string'
    if typ == 'boolean':
        return 'bool'
    if typ == 'integer':
        return {'uint': 'uint64', 'uint64': 'uint64', 'uint32': 'uint32', 'uint16': 'uint16', 'int32': 'int32'}.get(node.get('format'), 'int64')
    if typ == 'number':
        return 'float64'
    if typ == 'array':
        return '[]' + type_of(node.get('items', True), hint + 'Item')
    if typ == 'object' and 'properties' not in node:
        return 'map[string]' + type_of(node.get('additionalProperties', True), hint + 'Value')
    return register(ident(node.get('title', hint)), node)

def enums(node):
    if node.get('type') == 'string' and 'enum' in node:
        return node['enum']
    variants = node.get('oneOf', node.get('anyOf', []))
    if variants and all(v.get('type') == 'string' and 'enum' in v for v in variants):
        return [x for v in variants for x in v['enum']]
    return None

def object_type(name, node):
    props = dict(node.get('properties', {}))
    required = set(node.get('required', []))
    alternatives = node.get('anyOf', [])
    for branch in alternatives:
        if branch.get('type') != 'object':
            raise ValueError('Unsupported object composition: ' + name)
        props.update(branch.get('properties', {}))
    code = comment(node.get('description', '')) + f'type {name} struct {{\n'
    for key, prop in sorted(props.items()):
        typ = type_of(prop, name + ident(key))
        opt = key not in required or nullable(prop)
        if opt:
            typ = f'Optional[{typ}]'
        flags = []
        if key in required:
            flags.append('required')
        if nullable(prop) or opaque(prop):
            flags.append('nullable')
        if isinstance(prop, dict) and len(prop.get('enum', [])) == 1:
            flags.append('const=' + prop['enum'][0])
        code += comment(prop.get('description', '') if isinstance(prop, dict) else '')
        code += f'\t{ident(key)} {typ} `json:"{key}" protocol:"{",".join(flags)}"`\n'
    code += '\t// UnknownFields preserves unrecognized members for forward-compatible round trips.\n\tUnknownFields map[string]json.RawMessage `json:"-"`\n}\n'
    groups = ''
    if alternatives:
        groups = ', ' + ', '.join('[]string{' + ','.join(json.dumps(x) for x in b.get('required', [])) + '}' for b in alternatives)
    code += f'func (v {name}) MarshalJSON() ([]byte, error) {{ return marshalObject(v{groups}) }}\n'
    code += f'func (v *{name}) UnmarshalJSON(data []byte) error {{ return unmarshalObject(data, v{groups}) }}\n'
    return code

def union_type(name, node):
    variants = node.get('oneOf', node.get('anyOf'))
    # Native history cursors combine a string with a referenced tagged union.
    # Expand those schema branches without changing the authoritative snapshot.
    def expand(branch):
        if '$ref' in branch:
            target = snapshot['definitions'][branch['$ref'].removeprefix('#/definitions/')]
            return expand(target)
        nested = branch.get('oneOf', branch.get('anyOf'))
        if nested and branch.get('type') != 'object':
            return [v for child in nested for v in expand(child)]
        return [branch]
    variants = [v for branch in variants for v in expand(branch)]
    fields = []
    # Current native error unions explicitly admit raw string/object fallbacks.
    # Preserve them through Unknown instead of manufacturing a typed variant.
    raw_kinds = set()
    string_variants = [branch for branch in variants if branch.get('type') == 'string']
    object_variants = [branch for branch in variants if branch.get('type') == 'object']
    tag_name = next((key for key in ('type', 'kind') if object_variants and all(
        key in branch.get('required', []) and
        len(branch.get('properties', {}).get(key, {}).get('enum', [])) == 1
        for branch in object_variants)), 'type')
    # Native approval unions interleave several string enum branches with object
    # branches. They share one Go string variant, with the union of enum values.
    string_seen = False
    for i, branch in enumerate(variants):
        typ = branch.get('type')
        if isinstance(typ, list) and set(branch) == {'type'} and set(typ) <= {'string', 'object'}:
            raw_kinds.update(typ)
            continue
        if typ == 'string':
            if string_seen:
                continue
            string_seen = True
            bname = name + 'Value'
            if all('enum' in variant for variant in string_variants):
                register(bname, branch if len(string_variants) == 1 else
                         {'type': 'string', 'enum': list(dict.fromkeys(
                             value for variant in string_variants for value in variant['enum']))})
            else:
                bname = 'string'
            fields.append(('Value', bname, 'string', '', branch))
        elif typ == 'integer':
            fields.append(('Integer', type_of(branch, name + 'Integer'), 'number', '', branch))
        elif typ == 'array':
            bname = type_of(branch, name + 'Items')
            fields.append(('Items', bname, 'array', '', branch))
        elif typ == 'object':
            bname = register(ident(branch.get('title', name + 'Variant' + str(i))), branch)
            fname = bname.removesuffix(name) or bname
            if fname == 'Unknown':
                fname = 'UnknownVariant'
            props = branch.get('properties', {})
            if tag_name in props and len(props[tag_name].get('enum', [])) == 1:
                fields.append((fname, bname, 'tag', props[tag_name]['enum'][0], branch))
            else:
                req = branch.get('required', [])
                if len(req) != 1:
                    raise ValueError('Unsupported union branch: ' + name)
                fields.append((fname, bname, 'key', req[0], branch))
        else:
            raise ValueError('Unsupported union: ' + name)
    code = comment(node.get('description', ''))
    code += f'// {name} contains exactly one typed variant or Unknown.\n'
    code += f'type {name} struct {{\n'
    for field, typ, *_ in fields:
        code += f'\t{field} *{typ}\n'
    code += '\t// Unknown preserves an unrecognized future variant as raw JSON.\n\tUnknown json.RawMessage\n}\n'
    numeric = any(kind == 'number' for _, _, kind, _, _ in fields)
    marshal = 'data, err := ' if numeric else 'return '
    code += f'func (v {name}) MarshalJSON() ([]byte, error) {{\n\t{marshal}marshalUnion(v.Unknown'
    for field, *_ in fields:
        code += ', v.' + field
    code += ')\n'
    if numeric:
        # An integer union (notably native RequestId) must not use its raw escape
        # hatch to emit floats, out-of-range integers, null, or unrelated kinds.
        code += f'\tif err != nil {{ return nil, err }}\n\tvar checked {name}\n\tif err := checked.UnmarshalJSON(data); err != nil {{ return nil, err }}\n\treturn data, nil\n'
    code += '}\n'
    code += f'func (v *{name}) UnmarshalJSON(data []byte) error {{\n'
    tag_arg = ', ' + json.dumps(tag_name) if tag_name != 'type' else ''
    code += f'\tvar out {name}\n\tkind, tag, fields, err := inspectUnion(data{tag_arg})\n\tif err != nil {{ return err }}\n\t_, _, _ = kind, tag, fields\n'
    has_tag = any(k == 'tag' for _, _, k, _, _ in fields)
    allowed_kinds = sorted(set('object' if k in ('key', 'tag') else k for _, _, k, _, _ in fields) | raw_kinds)
    code += '\tif ' + ' && '.join('kind != ' + json.dumps(k) for k in allowed_kinds) + ' { return fmt.Errorf("protocol: invalid JSON kind for ' + name + '") }\n'
    external_keys = [value for _, _, kind, value, _ in fields if kind == 'key']
    if external_keys:
        code += '\tif kind == "object" {\n\t\tmatches := 0\n'
        for key in external_keys:
            code += '\t\tif fields[' + json.dumps(key) + '] != nil { matches++ }\n'
        if 'object' in raw_kinds:
            code += '\t\tif matches > 1 || len(fields) == 0 { out.Unknown = append(json.RawMessage(nil), data...); *v = out; return nil }\n\t}\n'
        else:
            code += '\t\tif matches > 1 || len(fields) == 0 { return fmt.Errorf("protocol: ambiguous or empty external union object") }\n\t}\n'
    for field, typ, kind, value, _ in fields:
        cond = {'string': 'kind == "string"', 'number': 'kind == "number"', 'array': 'kind == "array"', 'tag': 'kind == "object" && tag == ' + json.dumps(value), 'key': 'kind == "object" && fields[' + json.dumps(value) + '] != nil'}[kind]
        failure = 'out.Unknown = append(json.RawMessage(nil), data...); *v = out; return nil' if ('object' if kind in ('tag', 'key') else kind) in raw_kinds else 'return err'
        code += f'\tif {cond} {{\n\t\tvar value {typ}\n\t\tif err := json.Unmarshal(data, &value); err != nil {{ {failure} }}\n\t\tout.{field} = &value\n\t\t*v = out\n\t\treturn nil\n\t}}\n'
    if has_tag:
        code += '\tif kind == "object" && tag == "" { return fmt.Errorf("protocol: missing union ' + tag_name + ' discriminator") }\n'
    code += '\tout.Unknown = append(json.RawMessage(nil), data...)\n\t*v = out\n\treturn nil\n}\n'
    return code

for name in queue:
    node = schemas[name]
    vals = enums(node)
    if vals is not None:
        code = comment(node.get('description', '')) + f'type {name} string\nconst (\n'
        for value in vals:
            code += f'\t{name}{ident(value)} {name} = {json.dumps(value)}\n'
        code += ')\n'
    elif node.get('type') == 'object' and ('properties' in node or not node.get('additionalProperties')):
        code = object_type(name, node)
    elif node.get('oneOf') or node.get('anyOf'):
        code = union_type(name, node)
    else:
        code = comment(node.get('description', '')) + f'type {name} = {type_of(node, name)}\n'
    output.append(code)

header = '''// Code generated by scripts/generate_protocol.py; DO NOT EDIT.
// Source: https://github.com/openai/codex/blob/''' + PIN + '/' + SOURCE + '''
// Upstream Git blob: ''' + BLOB + '''
// Upstream license: Apache-2.0; see NOTICE.

package types

import (
    "encoding/json"
    "fmt"
)

'''
(ROOT / 'types/types_generated.go').write_text(header + '\n'.join(output))
print(f'Generated {len(output)} types from {len(snapshot["definitions"])} pinned definitions.')

# Exercise every exported model and union branch using synthetic schema-derived
# values. This checks the public Go surface, not a live Codex session.
def sample(node, full=False):
    if opaque(node):
        return {'opaque': 9007199254740993, 'nested': [None, False]}
    node = nonnull(node)
    if '$ref' in node:
        return sample(schemas[ident(node['$ref'].split('/')[-1])], full)
    variants = node.get('oneOf') or (node.get('anyOf') if node.get('type') != 'object' else None)
    if variants:
        return sample(variants[0], full)
    if 'enum' in node:
        return node['enum'][0]
    typ = node.get('type')
    if isinstance(typ, list):
        return sample(dict(node, type=typ[0]), full)
    if typ == 'string':
        return 'synthetic-value'
    if typ in ('integer', 'number'):
        return 7
    if typ == 'boolean':
        return False
    if typ == 'array':
        return [sample(node.get('items', True), full)] if full else []
    if typ == 'object':
        if 'properties' not in node and node.get('additionalProperties'):
            return {'sample': sample(node['additionalProperties'], full)}
        props = dict(node.get('properties', {}))
        required = set(node.get('required', []))
        alternatives = node.get('anyOf', [])
        if alternatives:
            props.update(alternatives[0].get('properties', {}))
            required.update(alternatives[0].get('required', []))
            if full:
                for alt in alternatives[1:]:
                    props.update(alt.get('properties', {}))
        result = {key: sample(prop, full) for key, prop in props.items() if full or key in required}
        if full:
            result['futureMember'] = {'big': 9007199254740993, 'nullable': None}
        return result
    raise ValueError('Cannot synthesize ' + str(node))

checks = []
for name in queue:
    node = schemas[name]
    cases = [sample(node), sample(node, True)]
    variants = node.get('oneOf', node.get('anyOf', [])) if node.get('type') != 'object' else []
    cases += [sample(branch, True) for branch in variants]
    unique = list(dict.fromkeys(json.dumps(v, separators=(',', ':'), ensure_ascii=False) for v in cases))
    for index, raw in enumerate(unique):
        checks.append(f'\t\t{{{json.dumps(name + "/" + str(index))}, func() any {{ return new(types.{name}) }}, {json.dumps(raw)}}},')
(ROOT / 'types/schema_generated_test.go').write_text('''// Code generated by scripts/generate_protocol.py; DO NOT EDIT.
// Synthetic cases derived from the pinned official schemas; no live session data.
package types_test

import (
    "testing"
    "github.com/zoumo/agent-sdk-go/codex/types"
)

func TestGeneratedSchemaRoundTrips(t *testing.T) {
    cases := []struct {
        name string
        target func() any
        raw string
    }{
''' + '\n'.join(checks) + '\n    }\n    for _, tc := range cases {\n        t.Run(tc.name, func(t *testing.T) { schemaRoundTrip(t, tc.target(), tc.raw) })\n    }\n}\n')
print(f'Generated {len(checks)} synthetic public-model round-trip cases.')
