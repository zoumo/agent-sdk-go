#!/usr/bin/env bash
# Reject reintroduction of the retired external testify CLI workflow.
set -euo pipefail

root=${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}
cd "$root"
go_binary=${GO:-go}

python3 - <<'PY'
import os
from pathlib import Path
import re
import stat
import sys

excluded_directories = {'.artifacts', '.git', '.scratch'}
excluded_files = {
    'scripts/check-removed-cli-surfaces.sh',
    'tests/gates/contraction_test.go',
    'go.mod',
    'go.sum',
}
documentation = {
    'harness/architecture.md', 'harness/testing.md', 'docs/testing.md',
    'tests/simulation/drivers/README.md', 'README.md',
}
rules = [
    ('all', r'AGENT_SDK_TESTIFY_BIN|CLAUDE_GO_REPLAY_COMPLETE',
     'external testify executable environment and completion file must remain removed'),
    ('all', r'with-agent-sdk-testify|scripts/agent-sdk-testify-version\.json',
     'external testify wrapper and version manifest must remain removed'),
    ('documentation',
     r'(?i)launcher|wrapper|result\s+(descriptor|notice)|progress\s+descriptor|process\s+receipt|receipt\.json|executions/index\.json|schema-[34]|schemaVersion\s+[34]',
     'retired testify process workflow terms must remain absent from required documentation'),
    ('all',
     r"""(^\s*|[;&|()]\s*|(exec|command|env|nohup|sudo)\s+)([.]?/)?([\w.-]+/)*agent-sdk-testify([\s;&|)]|$)|["']agent-sdk-testify["']""",
     'external testify executable must not be launched'),
    ('go',
     r'func \([\w]+ \*?Session\) (Run|Command|Launcher)\(|Options\.Complete|\.options\.Complete|options\.Complete|\bComplete\s+func|ErrDisconnected',
     'bridge byte-copy, completion, command, and launcher entry points must remain removed'),
    ('connection', r'json:"(command|launcher|pid|resultDescriptor|progressDescriptor)"',
     'process-oriented simulation connection contract must remain removed'),
    ('production', r'resultDescriptor|progressDescriptor|executions/index\.json|receipt\.json|receipts/',
     'result notice, progress descriptor, and process receipt parsing must remain removed'),
    ('production',
     r'(schemaVersion|SchemaVersion)[^\x00-\x1f\x7f]*(==|!=|<|>|\brange\b)[^\x00-\x1f\x7f]*(^|[^0-9])[34]([^0-9]|$)',
     'schema-3 and schema-4 comparison readers must remain removed'),
    ('production', r'(?is)(schemaVersion|SchemaVersion)[\s\S]{0,300}?case\s+[^:]*\b[34]\b',
     'schema-3 and schema-4 switch readers must remain removed'),
]
rules = [(scope, re.compile(pattern), description) for scope, pattern, description in rules]
failed = False

def scan_error(error):
    raise error

try:
    for directory, directories, names in os.walk('.', onerror=scan_error):
        if directory == '.':
            directories[:] = sorted(set(directories) - excluded_directories)
        for name in sorted(names):
            path = Path(directory) / name
            relative = path.as_posix()
            if relative in excluded_files or path.is_symlink():
                continue
            if not stat.S_ISREG(path.stat().st_mode):
                continue
            content = path.read_bytes()
            if b'\0' in content:
                continue
            text = content.decode('utf-8', errors='replace')
            scopes = {'all'}
            if relative in documentation or relative.startswith('harness/modules/agent-sdk-testify/'):
                scopes.add('documentation')
            if name.endswith('.go'):
                scopes.add('go')
                if not name.endswith('_test.go'):
                    scopes.add('production')
                    if not relative.startswith(('codex/types/', 'claude/types/', 'agent/')):
                        scopes.add('connection')
            for scope, expression, description in rules:
                if scope not in scopes:
                    continue
                matches = []
                if expression.flags & re.DOTALL:
                    match = expression.search(text)
                    if match:
                        number = text.count('\n', 0, match.start()) + 1
                        matches.append((number, text.split('\n')[number - 1]))
                else:
                    matches = [(number, line) for number, line in enumerate(text.split('\n'), 1)
                               if expression.search(line)]
                if matches:
                    for number, line in matches:
                        print(f'{relative}:{number}:{line}', file=sys.stderr)
                    print(description, file=sys.stderr)
                    failed = True
except OSError as error:
    print(f'surface scan failed: {error}', file=sys.stderr)
    failed = True

for name in ('scripts/with-agent-sdk-testify.sh', 'scripts/agent-sdk-testify-version.json'):
    path = Path(name)
    if path.exists() or path.is_symlink():
        print(f'removed testify wrapper path must remain absent: {name}', file=sys.stderr)
        failed = True

sys.exit(1 if failed else 0)
PY

if ! bridge_imports=$("$go_binary" list -f '{{range .Imports}}{{println .}}{{end}}' ./internal/mocktest); then
  echo "unable to inspect internal/mocktest imports" >&2
  exit 1
elif printf '%s\n' "$bridge_imports" | grep -qx 'os/exec'; then
  echo "the in-process bridge must not launch an executable" >&2
  exit 1
fi

printf 'removed testify CLI surfaces are absent\n'
