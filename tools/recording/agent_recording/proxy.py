"""Bounded byte-preserving native stdio capture; never uses the Go SDK."""
import hashlib
import json
import os
from pathlib import Path
import queue
import signal
import subprocess
import sys
import threading
import time

from .environment import private_json, read_regular, story_prompts, STORIES
from .policy import native_arguments, reviewed_versions
from .child_policy import check_output
from .scenarios import validate_recipe

PROTOCOL_LIMIT = 2 << 20
STDERR_LIMIT = 256 << 10


def validate(plan):
    root = Path(plan['root'])
    if plan['story'] not in STORIES or plan['prompts'] != story_prompts(plan['provider'], plan['story'], bool(plan.get('childCase'))):
        raise ValueError('unreviewed story inputs')
    if plan['version'] == 3:
        definition = validate_recipe(plan)
        if definition['mode'] != 'live-selected-path':
            raise ValueError('scenario does not allow a native launch')
        for name, content in definition['files'].items():
            if read_regular(Path(plan['project']) / name) != content.encode():
                raise ValueError('scenario input fixture changed')
    if plan['version'] not in (2, 3) or plan['cliVersion'] not in reviewed_versions(plan['provider']):
        raise ValueError('unsupported recording identity')
    if not root.is_absolute() or not root.is_relative_to('/tmp'):
        raise ValueError('recording roots must be absolute private /tmp paths')
    for name in ['root', 'home', 'project', 'config', 'raw']:
        path = Path(plan[name])
        if path.resolve(strict=True) != path or path.stat().st_mode & 0o777 != 0o700:
            raise ValueError('private owned directories required')
        if name != 'root' and path.parent != root:
            raise ValueError('owned directory escaped story root')
    if plan.get('childCase'):
        from .child_policy import CHILD_FILE, CHILD_TEXT, role_file
        if read_regular(root / 'project' / CHILD_FILE).decode() != CHILD_TEXT:
            raise ValueError('synthetic child fixture changed')
        if plan['provider'] == 'codex' and read_regular(root / 'config/agents/fixture_reader.toml').decode() != role_file():
            raise ValueError('synthetic child role changed')
    if plan['args'] != native_arguments(plan['provider'], plan['story'], bool(plan.get('childCase'))):
        raise ValueError('unreviewed native arguments')
    if hashlib.sha256(read_regular(Path(plan['cli']), 512 << 20)).hexdigest() != plan['cliSHA256']:
        raise ValueError('native executable changed')
    env = json.loads(read_regular(root / 'native.env.private.json'))
    key = 'CODEX_HOME' if plan['provider'] == 'codex' else 'CLAUDE_CONFIG_DIR'
    if env.get('HOME') != plan['home'] or env.get(key) != plan['config'] or env.get('TMPDIR') != str(root / 'tmp'):
        raise ValueError('isolated native environment required')
    return env


def run(plan):
    if os.environ.get('SDK_REAL_AGENT_E2E') != '1' or os.environ.get('CI'):
        raise ValueError('explicit live opt-in required; CI refused')
    env = validate(plan)
    limits = plan.get('scenario', {}).get('limits', {})
    protocol_limit = limits.get('protocolBytes', PROTOCOL_LIMIT)
    stderr_limit = limits.get('stderrBytes', STDERR_LIMIT)
    transport_seconds = limits.get('transportSeconds', 120)
    root, raw = Path(plan['root']), Path(plan['raw'])
    private_json(root / 'native.started', {'reserved': True})
    started = time.monotonic()
    counts = {'protocolBytes': 0, 'stderrBytes': 0}
    lock, done = threading.Lock(), queue.Queue()
    observation = {}
    args = [plan['cli'], *plan['args']]
    if plan['provider'] == 'claude' and plan['model']:
        args += ['--model', plan['model']]
    proc = None
    workers = []
    reason, reaped = 'EOF', False
    with (raw / 'raw.jsonl').open('xb') as capture, (raw / 'stderr.raw').open('xb') as stderr:
        def relay(source, target, direction):
            try:
                while line := source.readline(protocol_limit + 2):
                    if not line.endswith(b'\n') or len(line) > protocol_limit:
                        raise ValueError('oversized or incomplete native frame')
                    obj = json.loads(line)
                    with lock:
                        counts['protocolBytes'] += len(line)
                        if counts['protocolBytes'] > protocol_limit:
                            raise ValueError('protocol byte budget exceeded')
                        ordinal = counts.setdefault('frames', 0) + 1
                        if ordinal > 10000:
                            raise ValueError('protocol frame budget exceeded')
                        counts['frames'] = ordinal
                        frame = {'ordinal': ordinal, 'direction': direction, 'channel': 'protocol',
                                 'epoch': 1, 'elapsedMs': int((time.monotonic() - started) * 1000)}
                        # Keep the original JSON bytes, including integer precision and unknown fields.
                        capture.write(json.dumps(frame).encode()[:-1] + b',"wire":' + line.rstrip(b'\r\n') + b'}\n')
                        capture.flush()
                    if direction == 's2c':
                        check_output(plan, obj, observation)
                    target.write(line)
                    target.flush()
                done.put(None)
            except Exception as exc:
                done.put(exc)

        def drain():
            try:
                while data := proc.stderr.read(4096):
                    counts['stderrBytes'] += len(data)
                    if counts['stderrBytes'] > stderr_limit:
                        raise ValueError('stderr byte budget exceeded')
                    stderr.write(data)
                done.put(None)
            except Exception as exc:
                done.put(exc)

        def terminate(_signum, _frame):
            raise InterruptedError('owned proxy interrupted')

        signal.signal(signal.SIGTERM, terminate)
        try:
            proc = subprocess.Popen(args, cwd=plan['project'], env=env, stdin=subprocess.PIPE,
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
            for fn, values in [(relay, (sys.stdin.buffer, proc.stdin, 'c2s')),
                               (relay, (proc.stdout, sys.stdout.buffer, 's2c')), (drain, ())]:
                worker = threading.Thread(target=fn, args=values, daemon=True)
                workers.append(worker)
                worker.start()
            outcome = done.get(timeout=transport_seconds)
            if outcome:
                reason = 'transport-error'
                raise outcome
        except queue.Empty:
            reason = 'deadline'
            raise TimeoutError('native transport deadline')
        except InterruptedError:
            reason = 'SDK-close'
        finally:
            signal.signal(signal.SIGTERM, signal.SIG_IGN)
            if proc is not None:
                try:
                    os.killpg(proc.pid, signal.SIGTERM)
                    proc.wait(timeout=2)
                except (ProcessLookupError, subprocess.TimeoutExpired):
                    pass
                finally:
                    try:
                        os.killpg(proc.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                    proc.wait(timeout=2)
                    reaped = True
            for worker in workers[1:]:
                worker.join(timeout=2)
            drained = all(not worker.is_alive() for worker in workers[1:])
            private_json(raw / 'transport.json', {**counts, 'reason': reason, 'reaped': reaped,
                         'cleanupComplete': reaped and drained, 'milliseconds': int((time.monotonic()-started)*1000)})
    return 0
