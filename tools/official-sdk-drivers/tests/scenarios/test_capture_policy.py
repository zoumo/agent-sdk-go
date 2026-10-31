"""Exercise permissive capture with local native-process fixtures only."""
import asyncio
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from types import SimpleNamespace, ModuleType
import unittest
from unittest.mock import patch

from support import BOOTSTRAP
from record_user_story.peer import identity
from record_user_story.cli import execute, validate_replay
from record_user_story.drivers import run
from record_user_story.environment import private_json, recording_temp_root, sdk_identity
from record_user_story.scenarios import recipe, digest
from record_user_story.stream import json_messages


class CapturePolicyTests(unittest.TestCase):
    def proxy_fixture(self, root, source, limits=None):
        for name in ('home', 'config', 'project', 'tmp'):
            (root / name).mkdir(mode=0o700)
        native = root / 'native'
        native.write_text('#!' + sys.executable + '\n' + source)
        native.chmod(0o700)
        plan = dict(version=4, provider='codex', story=root.parent.name, storyId=root.parent.name, caseId=root.name,
                    cli=str(native), cliVersion='99.0.0',
                    cliSHA256=hashlib.sha256(native.read_bytes()).hexdigest(),
                    root=str(root), tempRoot=str(recording_temp_root()),
                    home=str(root/'home'), config=str(root/'config'),
                    project=str(root/'project'), raw=str(root/'raw'),
                    args=[], model='', prompts=['synthetic'], capturePolicy=limits or {})
        definition = recipe('codex', root.parent.name, case_id=root.name)
        plan.update(driverProfile=definition['driverProfile'], scenario=definition, scenarioSHA256=digest(definition), prompts=definition['prompts'])
        for name, value in definition['files'].items():
            (root / 'project' / name).write_text(value)
        plan['agentMock'] = identity()
        private_json(root/'plan.json', plan)
        private_json(root/'native.env.private.json',
                     dict(HOME=plan['home'], CODEX_HOME=plan['config'], TMPDIR=str(root/'tmp')))
        return [sys.executable, str(BOOTSTRAP),
                'proxy', '--plan', str(root/'plan.json')]

    def test_v4_case_plan_launches_proxy_with_exact_story_directory(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve() / 'SDK-US-001' / 'SDK-CASE-005'
            root.mkdir(parents=True)
            command = self.proxy_fixture(root, 'import sys\nprint(sys.stdin.readline(), end="", flush=True)\n')
            plan = json.loads((root / 'plan.json').read_text())
            definition = recipe('codex', 'SDK-US-001', case_id='SDK-CASE-005')
            plan.update(version=4, story='SDK-US-001', storyId='SDK-US-001', caseId='SDK-CASE-005',
                        driverProfile=definition['driverProfile'], scenario=definition, scenarioSHA256=digest(definition), prompts=definition['prompts'])
            for name, value in definition['files'].items():
                (root / 'project' / name).write_text(value)
            (root / 'plan.json').write_text(json.dumps(plan))
            wire = b'{"synthetic":true}\n'
            process = subprocess.run(command, input=wire, capture_output=True, timeout=10,
                                     env={'SDK_REAL_AGENT_E2E': '1'})
            self.assertEqual(process.returncode, 0, process.stderr)
            self.assertEqual(process.stdout, wire)
            self.assertTrue((root / 'native.started').exists())

    def test_hooks_children_large_frames_and_stderr_do_not_abort_capture(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve() / 'SDK-US-001' / 'SDK-CASE-001'
            root.mkdir(parents=True)
            command = self.proxy_fixture(root, '''import json, sys
line = sys.stdin.buffer.readline()
sys.stderr.write('d' * 300000)
sys.stderr.flush()
print(json.dumps({'method': 'hook/started', 'params': {'text': 'h' * 2200000}}), flush=True)
print(json.dumps({'type': 'system', 'subtype': 'task_started', 'parent_tool_use_id': 'extra'}), flush=True)
sys.stdout.buffer.write(line)
sys.stdout.buffer.flush()
''')
            wire = b'{"id":9007199254740993,"unknown":true}\n'
            proc = subprocess.run(command, input=wire, capture_output=True, timeout=10,
                                  env={'SDK_REAL_AGENT_E2E': '1', 'CI': '1'})
            self.assertEqual(proc.returncode, 0, proc.stderr)
            self.assertTrue(proc.stdout.endswith(wire))
            frames = [json.loads(line) for line in (root/'raw/raw.jsonl').read_bytes().splitlines()]
            self.assertEqual(len(frames), 4)
            self.assertEqual(frames[-1]['wire']['id'], 9007199254740993)
            transport = json.loads((root/'raw/transport.json').read_text())
            self.assertTrue(transport['cleanupComplete'])
            observations = json.loads((root/'capture-observations.json').read_text())
            self.assertEqual(observations['hookEvents'], 1)
            self.assertEqual(observations['childEvents'], 1)
            self.assertGreater(transport['protocolBytes'], 2 << 20)
            self.assertEqual(transport['stderrBytes'], 300000)
            self.assertNotIn(b'Fatal Python error', proc.stderr)

    def test_native_process_error_and_explicit_limits_still_fail(self):
        cases = [('exit', {}, 'import sys\nsys.stdin.readline()\nsys.exit(7)\n', 'native-error'),
                 ('bytes', {'protocolBytes': 10}, 'import sys\nsys.stdout.write(sys.stdin.readline())\n', 'limit'),
                 ('timeout', {'timeoutSeconds': 1}, 'import time\ntime.sleep(30)\n', 'deadline')]
        for name, limits, source, reason in cases:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp).resolve() / 'SDK-US-001' / 'SDK-CASE-001'
                root.mkdir(parents=True)
                command = self.proxy_fixture(root, source, limits)
                proc = subprocess.run(command, input=b'{"synthetic":true}\n', capture_output=True,
                                      timeout=10, env={'SDK_REAL_AGENT_E2E': '1'})
                self.assertNotEqual(proc.returncode, 0)
                transport = json.loads((root/'raw/transport.json').read_text())
                self.assertEqual(transport['reason'], reason)
                self.assertTrue(transport['cleanupComplete'])

    def test_async_transport_accepts_large_json_lines(self):
        async def receive():
            reader = asyncio.StreamReader()
            reader.feed_data(json.dumps({'text': 'x' * 2200000}).encode() + b'\n')
            reader.feed_eof()
            return [message async for message in json_messages(reader)]
        self.assertEqual(len(asyncio.run(receive())[0]['text']), 2200000)

    def test_sdk_versions_are_observed_without_an_allowlist(self):
        with patch('importlib.metadata.version', return_value='99.1.2'):
            self.assertEqual(sdk_identity('codex'), {'package': 'openai-codex', 'version': '99.1.2'})

    def test_background_child_waits_for_its_native_terminal_result(self):
        from record_user_story import children
        waits = []
        class Value(SimpleNamespace):
            def model_dump(self, **_):
                return {key: value.model_dump() if isinstance(value, Value) else value
                        for key, value in vars(self).items()}
        class Client:
            def __init__(self, **_):
                self.events = iter([
                    Value(method='item/completed', payload=Value(item={'type': 'subAgentActivity', 'agentThreadId': 'child'})),
                    Value(method='turn/completed', payload=Value(turn=Value(id='root-turn', status=Value(value='completed')))),
                ])
            def __enter__(self): return self
            def __exit__(self, *_): pass
            def initialize(self): return {}
            def thread_start(self, _): return Value(thread=Value(id='root'))
            def turn_start(self, *_): return Value(turn=Value(id='root-turn'))
            def next_turn_notification(self, _): return next(self.events)
            def thread_read(self, *_ , **__):
                return {'thread': {'id': 'child', 'parentThreadId': 'root',
                                   'turns': [{'id': 'child-turn', 'status': 'completed' if waits else 'inProgress'}]}}
            def wait_for_turn_completed(self, identity):
                waits.append(identity)
                return {'threadId': 'child', 'turn': {'id': identity, 'status': 'completed'}}
        sdk = ModuleType('openai_codex.client')
        sdk.CodexClient, sdk.CodexConfig = Client, lambda **kw: kw
        report = dict(inputs=0, results=0, events=0, requests=0, receipts=[], facts=[], gaps=[])
        plan = dict(driverProfile='background-children', story='SDK-US-005', root='/unused', project='/unused', model='', prompts=['synthetic'])
        with patch.dict('sys.modules', {'openai_codex.client': sdk}):
            children.codex(plan, report)
        self.assertEqual(waits, ['child-turn'])
        self.assertTrue(any('post-root-native-child/completed' in item for item in report['receipts']))
        self.assertNotIn('native post-root child completion is not established', report['gaps'])

    def test_api_error_is_not_downgraded_to_a_case_gap(self):
        with tempfile.TemporaryDirectory() as tmp:
            definition = recipe('codex', 'SDK-US-001', case_id='SDK-CASE-001')
            plan = dict(version=4, provider='codex', story='SDK-US-001', storyId='SDK-US-001', caseId='SDK-CASE-001', driverProfile='continuation', root=tmp,
                        scenario=definition, scenarioSHA256=digest(definition),
                        prompts=definition['prompts'], cliVersion='99.0.0')
            with patch('record_user_story.drivers.sdk_identity', return_value={'package': 'fixture', 'version': '99'}), \
                 patch('codex_driver.adapter.codex', side_effect=RuntimeError('API 503')):
                self.assertEqual(run(plan), 1)
            report = json.loads((Path(tmp)/'report.json').read_text())
            self.assertEqual(report['captureStatus'], 'failed')
            self.assertEqual(report['officialSDK']['version'], '99')

    def test_changed_sdk_version_is_recorded_without_blocking_capture(self):
        with tempfile.TemporaryDirectory() as tmp:
            definition = recipe('codex', 'SDK-US-001', case_id='SDK-CASE-001')
            plan = dict(version=4, provider='codex', story='SDK-US-001', storyId='SDK-US-001', caseId='SDK-CASE-001', driverProfile='continuation', root=tmp,
                        scenario=definition, scenarioSHA256=digest(definition),
                        prompts=definition['prompts'], cliVersion='99.0.0',
                        officialSDK={'package': 'fixture', 'version': '1'})
            def complete(_plan, report, flow):
                for _ in flow.inputs():
                    flow.submit()
                    flow.result()
                    if flow.completed == 1:
                        flow.post_result_event()
            with patch('record_user_story.drivers.sdk_identity', return_value={'package': 'fixture', 'version': '2'}), \
                 patch('codex_driver.adapter.codex', side_effect=complete):
                self.assertEqual(run(plan), 0)
            report = json.loads((Path(tmp)/'report.json').read_text())
            self.assertEqual(report['captureStatus'], 'complete')
            self.assertEqual(report['officialSDK']['version'], '2')

    def test_case_gaps_and_credential_scan_do_not_stop_the_batch(self):
        with tempfile.TemporaryDirectory() as tmp:
            suite = Path(tmp).resolve()
            frozen_peer = identity()
            plan_hashes = {}
            for number in ('001', '004'):
                root = suite/'SDK-US-001'/('SDK-CASE-' + number)
                root.mkdir(parents=True)
                snapshot = root/'driver/record_user_story'
                snapshot.mkdir(parents=True)
                private_json(root/'plan.json', {'version': 4, 'story': root.parent.name, 'storyId': root.parent.name, 'caseId': root.name, 'scenario': {'mode': 'live-selected-path'}, 'scenarioSHA256': digest({'mode': 'live-selected-path'}), 'root': str(root), 'project': str(root),
                                               'agentMock': frozen_peer, 'driver': str(snapshot/'bootstrap.py')})
                plan_hashes[str(root.relative_to(suite))] = hashlib.sha256((root/'plan.json').read_bytes()).hexdigest()
                private_json(root/'native.env.private.json', {})
            private_json(suite/'suite.json', {'moduleSHA256': 'snapshot', 'plansSHA256': plan_hashes})
            calls = []
            class Worker:
                def __init__(self, command, **_):
                    self.root = Path(command[-1]).parent
                    calls.append(self.root.name)
                def wait(self, **_):
                    private_json(self.root/'report.json', {'status': 'untriggered', 'captureStatus': 'complete'})
                    return 0
            args = SimpleNamespace(action='record', output=tmp, stories=None)
            with patch('record_user_story.peer.planned_identity', return_value=frozen_peer), \
                 patch('record_user_story.cli.digest_module', return_value='snapshot'), \
                 patch('record_user_story.cli.subprocess.Popen', Worker), \
                 patch('record_user_story.cli.manifest', return_value=False), \
                 patch.dict(os.environ, {'CI': '1'}):
                self.assertEqual(execute(args), 0)
            self.assertEqual(calls, ['SDK-CASE-001', 'SDK-CASE-004'])

    def test_replay_does_not_claim_a_different_sdk_version_reproduced_the_sample(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            original = dict(status='untriggered', captureStatus='complete', cliVersion='99',
                            officialSDK={'package': 'fixture', 'version': '1'},
                            inputs=1, results=1, requests=0, receipts=[])
            private_json(root/'report.json', original)
            private_json(root/'replay-report.json', dict(original, officialSDK={'package': 'fixture', 'version': '2'}))
            private_json(root/'replay-peer.json', {'fullConsumption': True, 'nativeLaunched': False, 'peer': 'Go native mock', 'clientEOF': True})
            self.assertEqual(validate_replay(root, 0), 'failed')

    def test_large_reports_have_no_implicit_one_mib_limit(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            report = dict(status='passed', captureStatus='complete', cliVersion='99',
                          officialSDK={'package': 'fixture', 'version': '1'},
                          inputs=1, results=1, requests=0, receipts=[{'text': 'x' * 2200000}])
            private_json(root/'report.json', report)
            private_json(root/'replay-report.json', report)
            private_json(root/'replay-peer.json', {'fullConsumption': True, 'nativeLaunched': False, 'peer': 'Go native mock', 'clientEOF': True})
            self.assertEqual(validate_replay(root, 0), 'passed')
