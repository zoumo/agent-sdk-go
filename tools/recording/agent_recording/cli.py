"""Prepare isolated datasets, drive official SDKs, and replay against Go mocks."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import sys

from .environment import prepare, manifest, private_json, read_regular, scrub_auth, verify_recording


def digest_module(directory=None):
    digest = hashlib.sha256()
    module = directory or Path(__file__).parent
    for path in sorted(module.glob('*.py')):
        digest.update(path.name.encode() + b'\0' + path.read_bytes())
    # Old snapshots without JSON assets retain their original Python-only hash.
    from .scenarios import scenario_assets
    for path in scenario_assets(module.parent / 'scenarios'):
        digest.update(str(path.relative_to(module.parent)).encode() + b'\0' + path.read_bytes())
    return digest.hexdigest()


def export_scenario(root):
    from decimal import Decimal
    verify_recording(root)
    plan = json.loads(read_regular(root / 'plan.json'))
    lines = read_regular(root / 'raw/raw.jsonl', 16 << 20).splitlines()
    frames = []
    for line in lines:
        if plan['provider'] == 'codex':
            # Both native Go mock and capture share this envelope. Preserve the
            # wire lexemes, including decimals beyond binary float precision.
            frames.append(line)
        else:
            frame = json.loads(line)
            prefix, marker, suffix = line.partition(b',"wire":')
            if not marker or not suffix.endswith(b'}'):
                raise ValueError('unsupported raw capture envelope')
            envelope = {'ordinal': frame['ordinal'],
                        'direction': {'c2s': 'client', 's2c': 'server'}[frame['direction']],
                        'channel': frame['channel']}
            frames.append(json.dumps(envelope).encode()[:-1] + b',"data":' + suffix[:-1] + b'}')
    envelope = {'version': 1, 'provenance': 'recording', 'source': str(root / 'manifest.json')}
    data = json.dumps(envelope).encode()[:-1] + b',"frames":[' + b','.join(frames) + b']}\n'
    path = root / 'go-mock-scenario.json'
    if path.exists():
        if json.loads(read_regular(path, 16 << 20), parse_float=Decimal) != json.loads(data, parse_float=Decimal):
            raise ValueError('existing scenario differs')
    else:
        with path.open('xb') as out:
            os.chmod(path, 0o600)
            out.write(data)
    return path


def archive_replay(root):
    names = ['replay-peer.json', 'replay-report.json', 'replay-diagnostics.private.json',
             'replay-peer-stderr.private', 'replay-worker-startup-diagnostics.private.json', 'replay-validation.json']
    existing = [root / name for name in names if (root / name).exists()]
    if not existing:
        return
    history = root / 'replays'
    history.mkdir(mode=0o700, exist_ok=True)
    number = len(list(history.glob('attempt-*'))) + 1
    destination = history / f'attempt-{number:04}'
    destination.mkdir(mode=0o700)
    for path in existing:
        if path.is_symlink():
            raise ValueError('replay output symlink refused')
        path.rename(destination / path.name)


def mock_proxy(args):
    # A small lifetime adapter lets CodexClient.close finish its stdin EOF before
    # terminating this wrapper. The actual protocol peer is the Go mock process.
    signal.signal(signal.SIGTERM, lambda *_: None)
    key = 'CODEX_MOCK_SCENARIO' if args.provider == 'codex' else 'CLAUDE_MOCK_SCENARIO'
    env = {'PATH': '/usr/bin:/bin', key: args.scenario}
    root = Path(args.scenario).parent
    with (root / 'replay-peer-stderr.private').open('wb') as stderr:
        peer = subprocess.Popen([args.peer], env=env, stdin=sys.stdin, stdout=sys.stdout, stderr=stderr)
        try:
            code = peer.wait(timeout=30)
        except subprocess.TimeoutExpired:
            peer.kill()
            peer.wait(timeout=2)
            code = 1
    if code == 0:
        private_json(root / 'replay-peer.json', {'fullConsumption': True, 'nativeLaunched': False,
                     'peer': 'Go native mock', 'peerSHA256': hashlib.sha256(Path(args.peer).read_bytes()).hexdigest()})
    return code


def validate_replay(root, worker_code):
    original = json.loads(read_regular(root / 'report.json'))
    report = json.loads(read_regular(root / 'replay-report.json'))
    peer_path = root / 'replay-peer.json'
    peer = json.loads(read_regular(peer_path)) if peer_path.exists() else {}
    fields = ['status', 'inputs', 'results', 'requests', 'receipts']
    if 'scenarioSHA256' in original or 'scenarioSHA256' in report:
        fields += ['scenarioSHA256', 'flowTrace', 'branchStatus']
    if 'case' in original or 'case' in report:
        fields += ['case', 'gaps', 'limitations']
        if 'branchStatus' not in fields:
            fields.append('branchStatus')
    matched = [name for name in fields if original.get(name) == report.get(name)]
    full = bool(peer.get('fullConsumption')) and peer.get('nativeLaunched') is False
    status = 'failed'
    if full and len(matched) == len(fields):
        if worker_code == 0 and report['status'] not in ('failed', 'untriggered', 'blocked', 'offline-only'):
            status = 'passed'
        elif worker_code == 1 and original['status'] == report['status'] == 'untriggered':
            status = 'expected-untriggered-reproduced'
        elif original['status'] == report['status'] == 'failed':
            before, after = root / 'diagnostics.private.json', root / 'replay-diagnostics.private.json'
            if before.exists() and after.exists() and read_regular(before) == read_regular(after):
                status = 'expected-failure-reproduced'
    private_json(root / 'replay-validation.json', {'status': status, 'fullConsumption': full,
                 'matchedFields': matched, 'recordingStatus': original['status'],
                 'workerExitCode': worker_code, 'recordingUnchanged': True})
    return status


def execute(args):
    target = Path(args.output).resolve(strict=True)
    suite = json.loads(read_regular(target / 'suite.json'))
    if args.action == 'record' and os.environ.get('CI'):
        raise ValueError('CI recording refused')
    code = 0
    for root in sorted(target.glob('SDK-US-*')):
        if args.stories and root.name not in args.stories.split(','):
            continue
        plan = json.loads(read_regular(root / 'plan.json'))
        if plan.get('version') not in (2, 3):
            raise ValueError('use legacy replay for historical Go-driven plans')
        if args.action == 'record':
            if plan['version'] == 3:
                from .scenarios import digest
                definition = plan['scenario']
                if plan.get('scenarioSHA256') != digest(definition):
                    raise ValueError('prepared scenario hash changed')
                if definition['mode'] != 'live-selected-path':
                    print(root.name + ': ' + definition['mode'] + '; ' + definition['reason'])
                    continue
            snapshot = root / 'driver' / 'agent_recording'
            if snapshot.exists():
                if digest_module(snapshot) != suite['moduleSHA256'] or plan.get('driver') != str(snapshot / 'bootstrap.py'):
                    raise ValueError('prepared Python driver snapshot changed')
            elif suite.get('moduleSHA256') != digest_module():
                raise ValueError('Python recording module changed without an immutable prepared snapshot')
            private_json(root / 'attempt.started', {'reserved': True})
            env = json.loads(read_regular(root / 'native.env.private.json'))
            env['SDK_REAL_AGENT_E2E'] = '1'
        else:
            if not (root / 'raw/raw.jsonl').exists():
                print(root.name + ': no native protocol; skipped')
                continue
            if not args.peer:
                raise ValueError('replay requires the provider Go mock --peer executable')
            scenario = export_scenario(root)
            archive_replay(root)
            env = {'HOME': plan['home'], 'TMPDIR': str(root / 'tmp'), 'PATH': '/usr/bin:/bin',
                   'AGENT_RECORDING_MOCK': str(Path(args.peer).resolve(strict=True)),
                   'AGENT_RECORDING_SCENARIO': str(scenario),
                   ('CODEX_HOME' if plan['provider'] == 'codex' else 'CLAUDE_CONFIG_DIR'): plan['config']}
        driver = plan.get('driver', str(Path(__file__).with_name('bootstrap.py')))
        cmd = [sys.executable, driver, 'worker', '--plan', str(root / 'plan.json')]
        proc = subprocess.Popen(cmd, cwd=plan['project'], env=env, stdout=subprocess.DEVNULL,
                                stderr=subprocess.DEVNULL, start_new_session=True)
        try:
            code = proc.wait(timeout=125 if args.action == 'record' else 40)
        except subprocess.TimeoutExpired:
            os.killpg(proc.pid, signal.SIGTERM)
            try:
                proc.wait(timeout=7)
            except subprocess.TimeoutExpired:
                os.killpg(proc.pid, signal.SIGKILL)
                proc.wait(timeout=3)
            code = 1
        finally:
            if args.action == 'record':
                if not (root / 'report.json').exists():
                    private_json(root / 'report.json', {'status': 'failed', 'gaps': ['worker deadline or startup failure']})
                try:
                    scan = manifest(root, json.loads(read_regular(root / 'report.json')))
                    if not scan:
                        code = 1
                finally:
                    scrub_auth(root)
        replay_status = None
        if args.action == 'replay':
            if not (root / 'replay-report.json').exists():
                private_json(root / 'replay-report.json', {'status': 'failed', 'gaps': ['replay worker produced no SDK report']})
            replay_status = validate_replay(root, code)
            code = int(replay_status == 'failed')
        report_path = root / ('report.json' if args.action == 'record' else 'replay-report.json')
        status = json.loads(read_regular(report_path)).get('status', 'failed') if report_path.exists() else 'failed'
        print(root.name + ': ' + status + ('; ' + replay_status if replay_status else '') + ('; Go mock full consumption' if args.action == 'replay' and code == 0 else ''))
        if code:
            print('Stopped after failure; private diagnostics retained. No retry.')
            break
    return code


def offline(args):
    """Run selected public story checks with no CLI/model fallback."""
    from .scenarios import STORIES, recipe
    providers = [args.provider] if args.provider else ['codex', 'claude']
    stories = args.stories.split(',') if args.stories else STORIES
    expected = {recipe(provider, story)['offlineTest'] for provider in providers for story in stories}
    if not expected:
        raise ValueError('empty offline selection')
    targets = '|'.join(sorted({story if story != 'SDK-US-019' else 'US-014' for story in stories}))
    provider_pattern = '|'.join(providers)
    pattern = '^TestCommonStories$/^(TestSessionTargets|TestIndependentHistoryBrowser)$/^(' + targets + ')$/^(' + provider_pattern + ')$'
    env = dict(os.environ, GOTOOLCHAIN='local', GOENV='off', GOPROXY='off', GOSUMDB='off')
    for key in ('CODEX_E2E_STDIO', 'CODEX_E2E_INTERACTIONS', 'CODEX_E2E_WEBSOCKET', 'CLAUDE_E2E', 'SDK_REAL_AGENT_E2E'):
        env[key] = '0'
    for key in list(env):
        if key.startswith(('SDK_STORY_', 'SDK_OFFICIAL_')):
            del env[key]
    proc = subprocess.run(['mise', 'exec', '--', 'go', 'test', './tests/stories', '-json', '-count=1', '-run', pattern],
                          cwd=Path(__file__).resolve().parents[3], env=env, capture_output=True, text=True, timeout=180)
    passed = set()
    for line in proc.stdout.splitlines():
        event = json.loads(line)
        if event.get('Action') == 'pass' and event.get('Test') in expected:
            passed.add(event['Test'])
    for name in sorted(expected):
        print(name + ': ' + ('synthetic-passed' if name in passed else 'failed-or-not-executed'))
    return int(proc.returncode != 0 or passed != expected)


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['catalog', 'offline', 'prepare', 'record', 'replay', 'worker', 'proxy', 'mock-proxy'])
    parser.add_argument('--provider', choices=['codex', 'claude'])
    parser.add_argument('--output')
    parser.add_argument('--cli')
    parser.add_argument('--source-config')
    parser.add_argument('--stories')
    parser.add_argument('--model')
    parser.add_argument('--plan')
    parser.add_argument('--peer')
    parser.add_argument('--scenario')
    parser.add_argument('--child-case', action='store_true', help='explicit one-child synthetic read profile; only SDK-US-006/007')
    args = parser.parse_args()
    try:
        if args.action == 'offline':
            return offline(args)
        if args.action == 'catalog':
            from .scenarios import STORIES, recipe, digest
            providers = [args.provider] if args.provider else ['codex', 'claude']
            stories = args.stories.split(',') if args.stories else STORIES
            definitions = [recipe(provider, story) for provider in providers for story in stories]
            print(json.dumps([dict(item, scenarioSHA256=digest(item)) for item in definitions], indent=2))
            return 0
        if args.action == 'prepare':
            if not args.provider or not args.output or not Path(args.output).is_relative_to('/tmp'):
                raise ValueError('prepare requires --provider and new absolute /tmp --output')
            prepare(args)
            suite = Path(args.output) / 'suite.json'
            value = json.loads(read_regular(suite))
            value['moduleSHA256'] = digest_module()
            suite.write_text(json.dumps(value, indent=2) + '\n')
            return 0
        if args.action == 'mock-proxy':
            return mock_proxy(args)
        if args.action in ('worker', 'proxy'):
            plan = json.loads(read_regular(Path(args.plan)))
            if args.action == 'proxy':
                from .proxy import run
            else:
                def deadline(_signal, _frame):
                    raise TimeoutError('official SDK activity deadline')
                signal.signal(signal.SIGALRM, deadline)
                signal.alarm(plan.get('scenario', {}).get('limits', {}).get('activitySeconds', 110))
                from .drivers import run
            return run(plan)
        if not args.output:
            raise ValueError('--output required')
        return execute(args)
    except Exception as exc:
        if args.action in ('worker', 'proxy') and args.plan:
            root = Path(args.plan).parent
            try:
                prefix = 'replay-' if os.environ.get('AGENT_RECORDING_MOCK') else ''
                private_json(root / (prefix + args.action + '-startup-diagnostics.private.json'), {'type': type(exc).__name__, 'error': str(exc)})
            except Exception:
                pass
        print('Recording operation failed; private values and diagnostics withheld.', file=sys.stderr)
        return 1
