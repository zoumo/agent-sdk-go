"""Two official-SDK connections to one owned native WebSocket service.

Each connection has its own immutable Go capture. Closing the first connection
must leave the same service process alive and able to answer the second one.
"""
import hashlib
import json
import os
from pathlib import Path
import secrets
import signal
import socket
import subprocess
import sys
import time

from record_user_story.adapters.common import serial
from record_user_story.environment import private_json, read_regular
from record_user_story.peer import planned_identity, verify_receipt

REQUIRED_PHASE_FILES = frozenset(('remote-service.json', 'remote-probe/raw/raw.jsonl',
    'remote-probe/raw/manifest.json', 'remote-probe/raw/transport.json', 'remote-probe/raw/stderr.raw'))


def phase_files(root):
    """Additional immutable remote evidence for the normal case manifest."""
    root = Path(root)
    files = [root / 'remote-service.json']
    files += list((root / 'remote-probe/raw').glob('*'))
    result = {str(path.relative_to(root)): hashlib.sha256(read_regular(path, None)).hexdigest()
              for path in files if path.is_file()}
    if not REQUIRED_PHASE_FILES.issubset(result):
        raise ValueError('remote phase evidence is incomplete')
    return result


def verify_phase_files(root, files):
    root = Path(root)
    if not REQUIRED_PHASE_FILES.issubset(files):
        raise ValueError('remote phase evidence is incomplete')
    for name, digest in files.items():
        relative = Path(name)
        if (relative.is_absolute() or '..' in relative.parts or
                (name != 'remote-service.json' and relative.parent != Path('remote-probe/raw'))):
            raise ValueError('remote phase evidence path escaped its scope')
        path = root / relative
        if path.is_symlink() or hashlib.sha256(read_regular(path, None)).hexdigest() != digest:
            raise ValueError('remote phase recording changed')


def codex(plan, report, flow):
    if plan.get('_nativeCase'):
        return native_case(plan, report, flow)
    from openai_codex.client import CodexClient, CodexConfig
    from openai_codex.errors import TransportClosedError
    from record_user_story.proxy import validate

    if plan['provider'] != 'codex' or plan['model'] != 'gpt-6.1-terra':
        raise ValueError('remote recording requires the explicit Codex model')
    replay = bool(os.environ.get('AGENT_RECORDING_MOCK'))
    root = Path(plan['root'])
    peer = planned_identity(plan)
    service = None
    token = root / 'remote-token.private'
    evidence = {}
    log = None
    if replay:
        evidence = json.loads(read_regular(root / 'remote-service.json'))
        if evidence.get('serverExitCode') != 0 or not all(evidence.get(key) is True for key in ('survivedFirstClose', 'probeSucceeded', 'cleanupComplete')):
            raise ValueError('recorded remote lifecycle did not succeed')
        endpoint = None
    else:
        env = validate(plan)
        with socket.socket() as listener:
            listener.bind(('127.0.0.1', 0))
            port = listener.getsockname()[1]
        endpoint = f'ws://127.0.0.1:{port}'
        token.write_text(secrets.token_urlsafe(32))
        token.chmod(0o600)
        native_args = [arg for arg in plan['args'] if arg != '--stdio']
        if '--listen' in native_args:
            raise ValueError('remote plan must not override the selected listener')
        native_args += ['--listen', endpoint, '--ws-auth', 'capability-token', '--ws-token-file', str(token)]
        log = (root / 'remote-server-stderr.private').open('xb')
        private_json(root / 'native.started', {'reserved': True, 'agentMock': peer, 'transport': 'native-websocket'})
        try:
            service = subprocess.Popen([plan['cli'], *native_args], cwd=plan['project'], env=env,
                                       stdin=subprocess.DEVNULL, stdout=log, stderr=log)
        except BaseException:
            log.close()
            token.unlink(missing_ok=True)
            raise
        evidence = {'transport': 'native-websocket', 'serverPID': service.pid,
                    'nativeSHA256': plan['cliSHA256'], 'endpoint': endpoint,
                    'captureChild': 'transparent-stdio-websocket-bridge',
                    'remoteService': 'separately-owned-native-cli',
                    'survivedFirstClose': False, 'probeSucceeded': False, 'cleanupComplete': False}

    def client(phase):
        base = root if phase == 'main' else root / 'remote-probe'
        if replay:
            if phase == 'probe' and (base / 'replay-peer.json').exists():
                history = base / 'replays'
                history.mkdir(mode=0o700, exist_ok=True)
                previous = history / f'attempt-{len(list(history.glob("attempt-*"))) + 1:04}.json'
                (base / 'replay-peer.json').rename(previous)
            private_json(base / 'replay-peer-identity.json', peer)
            command = [peer['path'], 'protocol', 'mock', '--provider', 'codex', '--scenario', str(base / 'raw/raw.jsonl'),
                       '--receipt', str(base / 'replay-peer.json')]
        else:
            if phase != 'main':
                base.mkdir(mode=0o700)
            command = [peer['path'], 'protocol', 'record', '--output', str(base / 'raw'), '--cwd', plan['project'],
                       '--native-version', plan['cliVersion'], '--timeout', str(plan['capturePolicy']['timeoutSeconds']),
                       '--', peer['path'], 'protocol', 'connect', '--url', endpoint, '--token-file', str(token)]
        command = [sys.executable, str(Path(__import__('record_user_story').__file__).with_name('remote_proxy.py')), *command]
        return CodexClient(config=CodexConfig(launch_args_override=tuple(command), cwd=plan['project'],
                           client_name='official-python-remote-case', client_title='Remote connection close'))

    try:
        if service:
            deadline = time.monotonic() + 15
            while True:
                if service.poll() is not None:
                    raise RuntimeError('native remote service exited during startup')
                try:
                    with socket.create_connection(('127.0.0.1', port), timeout=0.2):
                        break
                except OSError:
                    if time.monotonic() >= deadline:
                        raise TimeoutError('native remote listener did not become ready')
                    time.sleep(0.05)
        with client('main') as first:
            report['receipts'].append({'remote/initialize': serial(first.initialize())})
            started = first.thread_start({'cwd': plan['project'], 'model': plan['model'], 'ephemeral': False,
                'sandbox': 'workspace-write', 'approvalPolicy': 'on-request',
                'baseInstructions': 'Complete the supplied synthetic task. Be concise.'})
            identity = started.thread.id
            report['receipts'].append({'remote/thread-start': serial(started)})
            if started.model != plan['model']:
                raise ValueError('remote native model differs from requested model')
            for prompt in flow.inputs():
                flow.submit()
                turn = first.turn_start(identity, prompt)
                report['inputs'] += 1
                report['receipts'].append({'remote/input-submitted': serial(turn)})
                while True:
                    event = first.next_turn_notification(turn.turn.id)
                    report['events'] += 1
                    if event.method == 'turn/completed':
                        native = serial(event.payload)
                        if native.get('threadId') != identity or native.get('turn', {}).get('id') != turn.turn.id:
                            raise ValueError('remote terminal identity differs')
                        if native['turn']['status'] != 'completed':
                            raise ValueError('remote native execution did not succeed')
                        flow.result()
                        report['results'] += 1
                        report['receipts'].append({'remote/turn-completed': native})
                        break
        try:
            first.turn_start(identity, 'This closed-client probe must never be sent.')
        except TransportClosedError:
            pass
        else:
            raise ValueError('closed official SDK client accepted a submission')
        if service and service.poll() is not None:
            raise RuntimeError('closing the connection terminated the native remote service')
        evidence['survivedFirstClose'] = True
        with client('probe') as second:
            report['receipts'].append({'remote/probe-initialize': serial(second.initialize())})
            saved = serial(second.thread_read(identity, include_turns=True))
            if saved.get('thread', {}).get('id') != identity or not saved['thread'].get('turns'):
                raise ValueError('second connection could not read the same completed remote thread')
            report['receipts'].append({'remote/probe-read': saved})
        if service and service.poll() is not None:
            raise RuntimeError('remote service exited before explicit cleanup')
        evidence['probeSucceeded'] = True
        if replay:
            verify_receipt(root / 'replay-peer.json', root / 'raw/raw.jsonl', 'codex')
            verify_receipt(root / 'remote-probe/replay-peer.json', root / 'remote-probe/raw/raw.jsonl', 'codex')
        else:
            for base in (root, root / 'remote-probe'):
                transport = json.loads(read_regular(base / 'raw/transport.json'))
                if transport.get('captureStatus') != 'complete' or transport.get('cleanupComplete') is not True:
                    raise RuntimeError('remote connection capture did not drain and close cleanly')
        report['receipts'].append({'remote-close': {'firstClientRejected': True, 'sameServiceSurvived': True,
                                                  'secondConnectionReadSucceeded': True}})
    finally:
        if service:
            if service.poll() is None:
                service.send_signal(signal.SIGINT)
            try:
                service.wait(timeout=10)
            except subprocess.TimeoutExpired:
                service.kill()
                service.wait(timeout=5)
                evidence['forcedCleanup'] = True
            evidence['serverExitCode'] = service.returncode
            evidence['cleanupComplete'] = service.returncode == 0 and not evidence.get('forcedCleanup', False)
            private_json(root / 'remote-service.json', evidence)
        if log:
            log.close()
        token.unlink(missing_ok=True)
    if not evidence.get('cleanupComplete'):
        raise RuntimeError('native remote service cleanup did not complete')


def native_case(plan, report, flow):
    """Case behavior only. Go owns the service and both protocol peers."""
    from openai_codex.client import CodexClient, CodexConfig
    from openai_codex.errors import TransportClosedError

    def client(phase):
        peer = plan['_peers'][phase]
        return CodexClient(config=CodexConfig(
            launch_args_override=tuple([peer['command'], *peer.get('args', [])]),
            cwd=plan['project'], client_name='official-python-remote-case',
            client_title='Remote connection close'))

    with client('main') as first:
        report['receipts'].append({'remote/initialize': serial(first.initialize())})
        started = first.thread_start({'cwd': plan['project'], 'model': plan['model'], 'ephemeral': False,
            'sandbox': 'workspace-write', 'approvalPolicy': 'on-request',
            'baseInstructions': 'Complete the supplied synthetic task. Be concise.'})
        identity = started.thread.id
        report['receipts'].append({'remote/thread-start': serial(started)})
        if started.model != plan['model']:
            raise ValueError('remote native model differs from requested model')
        for prompt in flow.inputs():
            flow.submit()
            turn = first.turn_start(identity, prompt)
            report['inputs'] += 1
            report['receipts'].append({'remote/input-submitted': serial(turn)})
            while True:
                event = first.next_turn_notification(turn.turn.id)
                report['events'] += 1
                if event.method == 'turn/completed':
                    native = serial(event.payload)
                    if (native.get('threadId') != identity or native.get('turn', {}).get('id') != turn.turn.id
                            or native['turn']['status'] != 'completed'):
                        raise ValueError('remote terminal identity or status differs')
                    flow.result()
                    report['results'] += 1
                    report['receipts'].append({'remote/turn-completed': native})
                    break
    try:
        first.turn_start(identity, 'This closed-client probe must never be sent.')
    except TransportClosedError:
        pass
    else:
        raise ValueError('closed official SDK client accepted a submission')
    with client('probe') as second:
        report['receipts'].append({'remote/probe-initialize': serial(second.initialize())})
        saved = serial(second.thread_read(identity, include_turns=True))
        if saved.get('thread', {}).get('id') != identity or not saved['thread'].get('turns'):
            raise ValueError('second connection could not read the completed remote thread')
        report['receipts'].append({'remote/probe-read': saved})
    report['receipts'].append({'remote-close': {'firstClientRejected': True,
        'sameServiceSurvived': True, 'secondConnectionReadSucceeded': True}})
