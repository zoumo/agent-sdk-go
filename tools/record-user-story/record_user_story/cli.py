"""Prepare isolated datasets, drive official SDKs, and replay against the Go mock executable."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import sys

from .environment import prepare, manifest, private_json, read_regular, scrub_auth, verify_recording
from .peer import IdentityError


def digest_module(directory=None):
    digest = hashlib.sha256()
    module = directory or Path(__file__).parent
    for path in sorted(p for p in module.rglob('*') if p.suffix in ('.py', '.json')):
        digest.update(path.relative_to(module).as_posix().encode() + b'\0' + path.read_bytes())
    from .scenarios import scenario_assets
    for path in scenario_assets(module.parent / 'scenarios'):
        digest.update(str(path.relative_to(module.parent)).encode() + b'\0' + path.read_bytes())
    return digest.hexdigest()


def snapshot_directory(root):
    return root / 'driver/record_user_story'


def replay_source(root):
    verify_recording(root)
    return root / 'raw/raw.jsonl'


def archive_replay(root):
    names = ['replay-peer.json', 'replay-report.json', 'replay-diagnostics.private.json',
             'replay-peer-stderr.private', 'replay-peer-identity.json', 'replay-worker-startup-diagnostics.private.json', 'replay-validation.json']
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
    from .peer import planned_identity, verify_receipt
    plan = json.loads(read_regular(Path(args.plan)))
    peer = planned_identity(plan, args.peer)
    root = Path(plan['root'])
    receipt = root / 'replay-peer.json'
    scenario = Path(args.scenario)
    command = [peer['path'], 'mock', '--provider', plan['provider'],
               '--scenario', str(scenario), '--receipt', str(receipt)]
    # SDK shutdown closes stdin before terminating this lifetime adapter. Go
    # needs the EOF to issue its receipt. Its own deadline bounds the wait.
    previous = signal.signal(signal.SIGTERM, lambda *_: None)
    try:
        with (root / 'replay-peer-stderr.private').open('xb') as stderr:
            process = subprocess.Popen(command, env={'PATH': os.defpath},
                                       stdin=sys.stdin, stdout=sys.stdout, stderr=stderr)
            try:
                code = process.wait(timeout=35)
            except subprocess.TimeoutExpired:
                process.terminate()
                try:
                    process.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=3)
                return 1
        if code == 0:
            verify_receipt(receipt, scenario, plan['provider'])
            private_json(root / 'replay-peer-identity.json', peer)
        return code
    finally:
        signal.signal(signal.SIGTERM, previous)


def validate_replay(root, worker_code):
    original = json.loads(read_regular(root / 'report.json'))
    report = json.loads(read_regular(root / 'replay-report.json'))
    peer_path = root / 'replay-peer.json'
    peer = json.loads(read_regular(peer_path)) if peer_path.exists() else {}
    fields = ['status', 'inputs', 'results', 'requests', 'receipts', 'configurationEvidence', 'sessionClose', 'interruptClose', 'childEvidence']
    if 'storyId' in original or 'storyId' in report:
        fields += ['storyId', 'caseId']
    if 'captureStatus' in original or 'captureStatus' in report:
        fields += ['captureStatus', 'officialSDK', 'cliVersion']
    if 'scenarioSHA256' in original or 'scenarioSHA256' in report:
        fields += ['scenarioSHA256', 'flowTrace', 'branchStatus']
    if 'case' in original or 'case' in report:
        fields += ['case', 'gaps', 'limitations']
        if 'branchStatus' not in fields:
            fields.append('branchStatus')
    matched = [name for name in fields if original.get(name) == report.get(name)]
    full = (peer.get('fullConsumption') is True and peer.get('nativeLaunched') is False
            and peer.get('peer') == 'Go native mock' and peer.get('clientEOF') is True)
    plan_path = root / 'plan.json'
    if plan_path.exists():
        plan = json.loads(read_regular(plan_path))
        try:
            from .peer import planned_identity, verify_receipt
            actual = json.loads(read_regular(root / 'replay-peer-identity.json'))
            planned_identity(plan, actual['path'])
            if actual['sha256'] != plan['agentMock']['sha256'] or actual['version'] != plan['agentMock']['version']:
                raise ValueError('replay peer identity differs from the frozen binary')
            verify_receipt(peer_path, root / 'raw/raw.jsonl', plan['provider'])
        except (OSError, ValueError, KeyError):
            full = False
    status = 'failed'
    if full and len(matched) == len(fields):
        if worker_code == 0 and report['status'] == 'passed':
            status = 'passed'
        elif worker_code in (0, 1) and original['status'] == report['status'] == 'untriggered':
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
    code = 0
    roots = sorted(path.parent for path in target.glob('*/*/plan.json'))
    available = {json.loads(read_regular(root / 'plan.json')).get('caseId') for root in roots}
    if getattr(args, 'cases', None) and set(args.cases.split(',')) - available:
        raise ValueError('selected case is absent from the frozen dataset')
    selected_count = 0
    for root in roots:
        plan = json.loads(read_regular(root / 'plan.json'))
        if args.stories and plan['story'] not in args.stories.split(','):
            continue
        if getattr(args, 'cases', None) and plan.get('caseId') not in args.cases.split(','):
            continue
        selected_count += 1
        if plan.get('version') != 4:
            raise IdentityError('current simulation plan version required')
        snapshot = snapshot_directory(root)
        if not snapshot.is_dir() or not plan.get('agentMock'):
            raise IdentityError('current simulation driver snapshot and Go mock identity required')
        if digest_module(snapshot) != suite['moduleSHA256'] or plan.get('driver') != str(snapshot / 'bootstrap.py'):
            raise IdentityError('prepared Python driver snapshot changed')
        expected_plan = suite.get('plansSHA256', {}).get(str(root.relative_to(target)))
        if not expected_plan or hashlib.sha256(read_regular(root / 'plan.json')).hexdigest() != expected_plan:
            raise IdentityError('prepared plan identity changed')
        from .peer import planned_identity
        peer_identity = planned_identity(plan, getattr(args, 'peer', None))
        if args.action == 'record':
            if (root / 'attempt.started').exists():
                print(root.name + ': existing attempt retained; prepare a new output to record again')
                continue
            from .scenarios import digest
            definition = plan['scenario']
            if plan.get('scenarioSHA256') != digest(definition):
                raise ValueError('prepared scenario hash changed')
            if definition['mode'] != 'live-selected-path':
                print(root.name + ': ' + definition['mode'] + '; ' + definition['reason'])
                continue
            private_json(root / 'attempt.started', {'reserved': True})
            env = json.loads(read_regular(root / 'native.env.private.json'))
            env['AGENT_MOCK_BIN'] = peer_identity['path']
            env['AGENT_MOCK_RECORD'] = '1'
        else:
            if not (root / 'raw/raw.jsonl').exists():
                print(root.name + ': no native protocol; skipped')
                continue
            scenario = replay_source(root)
            archive_replay(root)
            env = {'HOME': plan['home'], 'TMPDIR': str(root / 'tmp'), 'PATH': '/usr/bin:/bin',
                   'AGENT_RECORDING_MOCK': peer_identity['path'],
                   'AGENT_RECORDING_SCENARIO': str(scenario),
                   ('CODEX_HOME' if plan['provider'] == 'codex' else 'CLAUDE_CONFIG_DIR'): plan['config']}
        driver = plan['driver']
        cmd = [sys.executable, driver, 'worker', '--plan', str(root / 'plan.json')]
        proc = subprocess.Popen(cmd, cwd=plan['project'], env=env, stdout=subprocess.DEVNULL,
                                stderr=subprocess.DEVNULL, start_new_session=True)
        try:
            seconds = plan.get('capturePolicy', {}).get('timeoutSeconds', 0)
            code = proc.wait(timeout=(seconds + 10 if seconds else None) if args.action == 'record' else 40)
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
                    private_json(root / 'report.json', {'status': 'failed', 'captureStatus': 'failed', 'gaps': ['worker deadline or startup failure']})
                try:
                    scan = manifest(root, json.loads(read_regular(root / 'report.json')))
                    if not scan:
                        print(root.name + ': credential scan flagged private output; review before publishing')
                finally:
                    scrub_auth(root)
        replay_status = None
        if args.action == 'replay':
            if not (root / 'replay-report.json').exists():
                private_json(root / 'replay-report.json', {'status': 'failed', 'gaps': ['replay worker produced no SDK report']})
            replay_status = validate_replay(root, code)
            code = int(replay_status == 'failed')
        report_path = root / ('report.json' if args.action == 'record' else 'replay-report.json')
        report = json.loads(read_regular(report_path)) if report_path.exists() else {}
        status = report.get('status', 'failed')
        summary = ('capture=' + report['captureStatus'] + '; case=' + status) if 'captureStatus' in report else status
        print(root.name + ': ' + summary + ('; ' + replay_status if replay_status else '') + ('; Go mock full consumption' if args.action == 'replay' and code == 0 else ''))
        if code:
            print('Stopped after failure; private diagnostics retained. No retry.')
            break
    if not selected_count:
        raise ValueError('story and case selection have no intersection in frozen dataset')
    return code



def main(argv=None):
    argv = list(sys.argv[1:] if argv is None else argv)
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['catalog', 'prepare', 'record', 'replay', 'worker', 'proxy', 'mock-proxy'])
    parser.add_argument('--provider', choices=['codex', 'claude'])
    parser.add_argument('--scenarios', type=Path, help='language-neutral scenario catalog directory')
    parser.add_argument('--source-revision', help='optional caller source revision; module bytes are always hashed')
    parser.add_argument('--output')
    parser.add_argument('--cli')
    parser.add_argument('--source-config')
    parser.add_argument('--stories', help='comma-separated story IDs')
    parser.add_argument('--cases', help='comma-separated case IDs')
    parser.add_argument('--model')
    parser.add_argument('--plan')
    parser.add_argument('--peer', help='absolute Go agent-mock executable (default: AGENT_MOCK_BIN or frozen identity)')
    parser.add_argument('--scenario')
    parser.add_argument('--child-case', action='store_true', help='explicit child adapter; selected live cases must use a child profile')
    parser.add_argument('--timeout', type=int, default=0, help='optional live timeout in seconds; 0 disables it')
    parser.add_argument('--max-protocol-bytes', type=int, default=0, help='optional protocol byte limit; 0 disables it')
    parser.add_argument('--max-stderr-bytes', type=int, default=0, help='optional stderr byte limit; 0 disables it')
    args = parser.parse_args(argv)
    if args.scenarios:
        os.environ['AGENT_MOCK_SCENARIOS'] = str(args.scenarios.resolve(strict=True))
    try:
        if args.stories:
            from .scenarios import select_story_groups
            dataset = args.output if args.action in ('record', 'replay') else None
            args.stories = select_story_groups(args.stories, dataset=dataset)
        if min(args.timeout, args.max_protocol_bytes, args.max_stderr_bytes) < 0:
            raise ValueError('capture limits must be nonnegative')
        if args.action == 'catalog':
            from .scenarios import case_recipes, digest
            providers = [args.provider] if args.provider else ['codex', 'claude']
            definitions = [item for provider in providers for item in case_recipes(provider, args.stories, args.cases)]
            print(json.dumps([dict(item, scenarioSHA256=digest(item)) for item in definitions], indent=2))
            return 0
        if args.action == 'prepare':
            if not args.provider:
                raise ValueError('prepare requires --provider')
            prepare(args)
            suite = Path(args.output) / 'suite.json'
            value = json.loads(read_regular(suite))
            value['moduleSHA256'] = digest_module(next(Path(args.output).glob('*/*/driver/record_user_story')))
            value['plansSHA256'] = {str(p.parent.relative_to(Path(args.output))): hashlib.sha256(read_regular(p)).hexdigest()
                                    for p in Path(args.output).glob('*/*/plan.json')}
            suite.write_text(json.dumps(value, indent=2) + '\n')
            print('Output: ' + args.output)
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
                signal.alarm(40 if os.environ.get('AGENT_RECORDING_MOCK') else plan.get('capturePolicy', {}).get('timeoutSeconds', 0))
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
        if isinstance(exc, IdentityError):
            print(str(exc), file=sys.stderr)
        else:
            print('Recording operation failed; private values and diagnostics withheld.', file=sys.stderr)
        return 1
