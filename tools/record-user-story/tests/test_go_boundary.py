"""Offline story orchestration and identity tests using actual Go subprocesses."""
import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
from types import ModuleType, SimpleNamespace
import unittest
from unittest.mock import patch

from record_user_story import drivers
from record_user_story.cli import main, execute, snapshot_directory, validate_replay
from record_user_story.environment import manifest, verify_recording
from record_user_story.peer import identity, planned_identity


class Value(SimpleNamespace):
    def model_dump(self, **_):
        def convert(value):
            if isinstance(value, Value):
                return {k: convert(v) for k, v in vars(value).items()}
            if isinstance(value, list):
                return [convert(v) for v in value]
            return value
        return convert(self)


def value(data):
    return Value(**{k: value(v) if isinstance(v, dict) else v for k, v in data.items()})


class GoBoundaryTests(unittest.TestCase):
    def prepare(self, base, binary=None):
        source = base/'source'
        source.mkdir()
        native = base/'synthetic-native'
        native.write_text('#!' + sys.executable + '''
import json, sys
if '--version' in sys.argv:
    print('synthetic 1.0.0')
    raise SystemExit
for line in sys.stdin:
    request = json.loads(line)
    method = request['method']
    if method == 'initialize': result = {'synthetic': True}
    elif method == 'thread/start': result = {'thread': {'id': 'thread'}, 'model': 'synthetic'}
    else: result = {'turn': {'id': 'turn'}}
    print(json.dumps({'id': request['id'], 'result': result}), flush=True)
    if method == 'turn/start':
        print('{"method":"turn/completed","params":{"turn":{"id":"turn","status":{"value":"completed"}},"opaque":900719925474099312345,"ratio":1.2300}}', flush=True)
''')
        native.chmod(0o700)
        command = ['prepare', '--provider', 'codex', '--stories', 'conversation', '--cases', 'two-inputs',
                   '--cli', str(native), '--source-config', str(source), '--output', str(base/'dataset')]
        if binary:
            command += ['--peer', str(binary)]
        with patch('record_user_story.environment.sdk_identity', return_value={'package': 'synthetic-sdk', 'version': '1'}), \
             patch.dict(os.environ, {'AGENT_MOCK_SCENARIOS': str(Path(drivers.__file__).parent/'examples')}):
            self.assertEqual(main(command), 0)
        root = base/'dataset/conversation/two-inputs'
        return root, json.loads((root/'plan.json').read_bytes())

    def test_official_adapter_substitute_records_and_replays_through_go(self):
        # Only the SDK implementation is replaced. The real story adapter,
        # frozen process launcher, Go record and Go mock all execute.
        class Client:
            def __init__(self, config, **_):
                self.command = config.launch_args_override
                self.cwd = config.cwd
                self.number = 0
            def __enter__(self):
                self.process = subprocess.Popen(self.command, cwd=self.cwd, stdin=subprocess.PIPE,
                                                stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                return self
            def request(self, method, params=None, **_):
                self.number += 1
                self.process.stdin.write((json.dumps({'id': self.number, 'method': method, 'params': params})+'\n').encode())
                self.process.stdin.flush()
                reply = json.loads(self.process.stdout.readline())
                return value(reply['result'])
            def initialize(self): return self.request('initialize')
            def thread_start(self, params): return self.request('thread/start', params)
            def turn_start(self, thread, inputs, **_): return self.request('turn/start', {'threadId':thread, 'input':inputs})
            def next_turn_notification(self, _):
                item = json.loads(self.process.stdout.readline())
                return Value(method=item['method'], payload=value(item['params']))
            def __exit__(self, *_):
                self.process.stdin.close()
                self.process.stdin = None
                _, diagnostic = self.process.communicate(timeout=10)
                if self.process.returncode:
                    raise RuntimeError(diagnostic.decode())
        sdk, generated, pydantic = ModuleType('openai_codex.client'), ModuleType('openai_codex.generated.v2_all'), ModuleType('pydantic')
        sdk.CodexClient, sdk.CodexConfig = Client, SimpleNamespace
        generated.TurnStartParams = SimpleNamespace(model_validate=lambda v:v)
        pydantic.BaseModel, pydantic.ConfigDict = type('BaseModel', (), {}), dict
        with tempfile.TemporaryDirectory() as temporary:
            root, plan = self.prepare(Path(temporary).resolve())
            modules = {'openai_codex.client': sdk, 'openai_codex.generated.v2_all': generated, 'pydantic': pydantic}
            with patch.dict(sys.modules, modules), patch('record_user_story.drivers.sdk_identity', return_value=plan['officialSDK']):
                with patch.dict(os.environ, {'AGENT_MOCK_RECORD': '1'}):
                    self.assertEqual(drivers.run(plan), 0)
                report = json.loads((root/'report.json').read_bytes())
                self.assertEqual((report['inputs'], report['results'], report['status']), (2, 2, 'passed'))
                self.assertTrue(manifest(root, report))
                verify_recording(root)
                before = {p.name: p.read_bytes() for p in (root/'raw').iterdir()}
                go_manifest = json.loads(before['manifest.json'])
                for name, digest in go_manifest['files'].items():
                    self.assertEqual(hashlib.sha256(before[name]).hexdigest(), digest)
                Path(plan['cli']).unlink()  # Replay must never look up or run native.
                with patch.dict(os.environ, {'AGENT_RECORDING_MOCK': plan['agentMock']['path'],
                                             'AGENT_RECORDING_SCENARIO': str(root/'raw/raw.jsonl')}):
                    self.assertEqual(drivers.run(plan), 0)
                self.assertEqual(validate_replay(root, 0), 'passed')
                self.assertEqual(before, {p.name: p.read_bytes() for p in (root/'raw').iterdir()})
                self.assertIn(b'1.2300', before['raw.jsonl'])
                self.assertIn(b'900719925474099312345', before['raw.jsonl'])
                self.assertEqual(json.loads((root/'replay-peer-identity.json').read_bytes()), plan['agentMock'])
                verify_recording(root)

    def test_changed_or_missing_go_binary_fails_before_launch_and_keeps_stdout_private(self):
        with tempfile.TemporaryDirectory() as temporary:
            base = Path(temporary).resolve()
            binary = base/'agent-mock'
            shutil.copyfile(identity()['path'], binary)
            binary.chmod(0o700)
            root, plan = self.prepare(base, binary)
            binary.write_bytes(b'private-config-canary-not-an-executable')
            for action in ('record', 'replay'):
                stdout, stderr = io.StringIO(), io.StringIO()
                with patch('sys.stdout', stdout), patch('sys.stderr', stderr):
                    self.assertEqual(main([action, '--output', str(base/'dataset')]), 1)
                self.assertEqual(stdout.getvalue(), '')
                self.assertNotIn('private-config-canary', stderr.getvalue())
                self.assertIn('Go agent-mock binary changed', stderr.getvalue())
                self.assertFalse((root/'native.started').exists())
                self.assertFalse((root/'attempt.started').exists())
            binary.unlink()
            relocated = planned_identity(plan, identity()['path'])
            self.assertEqual(relocated['sha256'], plan['agentMock']['sha256'])
            with patch.dict(os.environ, {}, clear=True), self.assertRaisesRegex(ValueError, 'unavailable'):
                planned_identity(plan)
            with patch.dict(os.environ, {}, clear=True), self.assertRaisesRegex(ValueError, 'AGENT_MOCK_BIN'):
                identity()

    def test_prepared_plan_tampering_is_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            base = Path(temporary).resolve()
            root, plan = self.prepare(base)
            plan['args'].append('unfrozen-argument')
            (root/'plan.json').write_text(json.dumps(plan))
            with self.assertRaisesRegex(ValueError, 'plan identity changed'):
                execute(SimpleNamespace(action='record', output=str(base/'dataset'), stories=None, peer=None))
            self.assertFalse((root/'attempt.started').exists())

    def test_noncurrent_snapshots_cannot_launch_even_when_they_contain_a_mock(self):
        for old in ('agent_mock', 'agent_recording'):
            with self.subTest(package=old), tempfile.TemporaryDirectory() as temporary:
                base = Path(temporary)
                root = base/'story'/'case'
                snapshot = root/'driver'/old
                snapshot.mkdir(parents=True)
                (snapshot/'mock.py').write_text('raise AssertionError("old mock executed")\n')
                (snapshot/'bootstrap.py').write_text('raise AssertionError("old driver executed")\n')
                (base/'suite.json').write_text('{}')
                (root/'plan.json').write_text(json.dumps({'version':4, 'story':'story', 'storyId':'story', 'caseId':'case'}))
                before = {str(p):p.read_bytes() for p in base.rglob('*') if p.is_file()}
                self.assertEqual(snapshot_directory(root), root/'driver/record_user_story')
                for action in ('record', 'replay'):
                    with self.assertRaisesRegex(ValueError, 'current simulation driver snapshot'):
                        execute(SimpleNamespace(action=action, output=temporary, stories=None, peer=None))
                self.assertEqual(before, {str(p):p.read_bytes() for p in base.rglob('*') if p.is_file()})


if __name__ == '__main__':
    unittest.main()
