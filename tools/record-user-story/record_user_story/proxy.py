"""Launch Go record on inherited stdio; no Python capture or forwarding loop."""
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import sys

from .environment import private_json, read_regular, recording_temp_root, stdin_eof_action
from .child_policy import check_output
from .scenarios import validate_recipe, valid_identity
from .peer import planned_identity

def validate(plan):
    stdin_eof_action(plan)
    root = Path(plan['root'])
    if not valid_identity(plan['story']):
        raise ValueError('unknown recording story')
    definition = validate_recipe(plan)
    if definition['mode'] != 'live-selected-path':
        raise ValueError('scenario does not allow a native launch')
    for name, content in definition['files'].items():
        if read_regular(Path(plan['project']) / name) != content.encode():
            raise ValueError('scenario input fixture changed')
    if root.name != plan['caseId'] or root.parent.name != plan['storyId']:
        raise ValueError('recording directory differs from story/case identity')
    # The worker's TMPDIR is isolated inside the case. Freeze the preparation
    # process's temporary root rather than rediscovering it in that worker.
    temp_root = Path(plan['tempRoot']) if 'tempRoot' in plan else recording_temp_root()
    if (not temp_root.is_absolute() or temp_root.resolve(strict=True) != temp_root
            or not root.is_absolute() or root == temp_root or not root.is_relative_to(temp_root)):
        raise ValueError('recording roots must be inside the program temporary directory')
    for name in ['root', 'home', 'project', 'config', 'raw']:
        path = Path(plan[name])
        if name == 'raw' and not path.exists():
            if path.parent != root or path.is_symlink():
                raise ValueError('owned raw directory escaped story root')
            continue
        if path.resolve(strict=True) != path or not path.is_dir():
            raise ValueError('private owned directories required')
        if name != 'root' and path.parent != root:
            raise ValueError('owned directory escaped story root')
    if plan.get('childCase'):
        from .child_policy import CHILD_FILE, CHILD_TEXT, role_file
        if read_regular(root / 'project' / CHILD_FILE).decode() != CHILD_TEXT:
            raise ValueError('synthetic child fixture changed')
        if plan['provider'] == 'codex' and read_regular(root / 'config/agents/fixture_reader.toml').decode() != role_file():
            raise ValueError('synthetic child role changed')
    if hashlib.sha256(read_regular(Path(plan['cli']))).hexdigest() != plan['cliSHA256']:
        raise ValueError('native executable changed')
    env = json.loads(read_regular(root / 'native.env.private.json'))
    key = 'CODEX_HOME' if plan['provider'] == 'codex' else 'CLAUDE_CONFIG_DIR'
    if env.get('HOME') != plan['home'] or env.get(key) != plan['config'] or env.get('TMPDIR') != str(root / 'tmp'):
        raise ValueError('isolated native environment required')
    return env


def run(plan):
    if os.environ.get('AGENT_MOCK_RECORD', os.environ.get('SDK_REAL_AGENT_E2E')) != '1':
        raise ValueError('use the explicit record command for live capture')
    env = validate(plan)
    peer = planned_identity(plan, os.environ.get('AGENT_MOCK_BIN'))
    limits = plan.get('capturePolicy', {})
    root, raw = Path(plan['root']), Path(plan['raw'])
    if raw.exists():
        raise ValueError('Go record requires a new raw output directory')
    private_json(root / 'native.started', {'reserved': True, 'agentMock': peer})
    command = [peer['path'], 'record', '--output', str(raw), '--cwd', plan['project'],
               '--native-version', plan.get('cliVersion', ''),
               '--timeout', str(limits.get('timeoutSeconds', 0)),
               '--protocol-bytes', str(limits.get('protocolBytes', 0)),
               '--stderr-bytes', str(limits.get('stderrBytes', 0)),
               '--stdin-eof-action', stdin_eof_action(plan), '--', plan['cli'], *plan['args']]
    if plan['provider'] == 'claude' and plan['model']:
        command += ['--model', plan['model']]
    # Only the selected native environment reaches Go and its child. Secrets are
    # never placed in argv. Go owns all protocol pipes and native process cleanup.
    with (root / 'capture-peer-stderr.private').open('xb') as diagnostics:
        process = subprocess.Popen(command, env=env, stdin=sys.stdin, stdout=sys.stdout, stderr=diagnostics)
        def terminate(signum, _frame):
            # SDK close sends EOF then TERM. Give Go time to drain and reap; a
            # stalled capture is canceled by Go, retaining its failed reports.
            try:
                process.wait(timeout=2)
            except subprocess.TimeoutExpired:
                process.send_signal(signum)
        previous = {sig: signal.signal(sig, terminate) for sig in (signal.SIGTERM, signal.SIGINT)}
        try:
            code = process.wait()
        finally:
            for sig, handler in previous.items():
                signal.signal(sig, handler)
    # Native semantic observations belong to the external story report. Keep
    # Go transport.json and its manifest byte-for-byte intact.
    observations = {}
    if (raw / 'raw.jsonl').exists():
        for line in (raw / 'raw.jsonl').read_bytes().splitlines():
            frame = json.loads(line)
            if frame['direction'] == 's2c':
                check_output(plan, frame['wire'], observations)
    private_json(root / 'capture-observations.json', observations)
    return code
