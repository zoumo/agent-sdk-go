"""Offline branch/order tests; native SDK substitutes never launch a process."""
import asyncio
from copy import deepcopy
from dataclasses import dataclass
import json
from pathlib import Path
import tempfile
from types import ModuleType, SimpleNamespace
import unittest
from unittest.mock import patch

from agent_recording.scenarios import STORIES, Flow, Untriggered, recipe, digest, validate_recipe, scenario_directory
from agent_recording.drivers import codex, claude, run
from agent_recording.cli import validate_replay, digest_module
from agent_recording.environment import private_json, verify_recording, prepare


class Value(SimpleNamespace):
    def model_dump(self, **_):
        def convert(item):
            if isinstance(item, Value):
                return {k: convert(v) for k, v in vars(item).items()}
            return item
        return convert(self)


def report():
    return {'inputs': 0, 'events': 0, 'results': 0, 'requests': 0,
            'receipts': [], 'facts': [], 'gaps': []}


class ScenarioTests(unittest.TestCase):
    def test_catalog_covers_all_goals_and_validates_frozen_definition(self):
        for provider in ('codex', 'claude'):
            for story in STORIES:
                with self.subTest(provider=provider, story=story):
                    definition = recipe(provider, story)
                    self.assertTrue(definition['case']['id'].startswith(story + '/'))
                    self.assertEqual(definition['case']['scope'], definition['scope'])
                    self.assertTrue(definition['case']['outOfScope'])
                    plan = {'provider': provider, 'story': story, 'scenario': definition,
                            'scenarioSHA256': digest(definition), 'prompts': definition['prompts']}
                    self.assertEqual(validate_recipe(plan), definition)
                    changed = deepcopy(plan)
                    changed['scenario']['limits']['inputs'] += 1
                    changed['scenarioSHA256'] = digest(changed['scenario'])
                    with self.assertRaises(ValueError):
                        validate_recipe(changed)
                    self.assertTrue(definition['offlineTest'].endswith(provider))
                    self.assertEqual(bool(definition['prompts']), definition['mode'] == 'live-selected-path')
        native = {key for provider in ('codex', 'claude') for story in STORIES for key in recipe(provider, story)['nativeStories']}
        self.assertEqual(len(native), 12)
        with self.assertRaises(ValueError):
            recipe('codex', 'SDK-US-020')

    def test_prepare_freezes_live_plan_and_preserves_child_profile(self):
        import sys
        for child in (False, True):
            with self.subTest(child=child), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                source = root / 'source'
                source.mkdir()
                native = root / 'native'
                native.write_text('#!' + sys.executable + '\nprint("0.161.0")\n')
                native.chmod(0o700)
                args = SimpleNamespace(provider='codex', stories='SDK-US-006' if child else 'SDK-US-001,SDK-US-005',
                                       output=str(root / 'suite'), source_config=str(source), cli=str(native),
                                       child_case=child, model='fixture')
                with patch('agent_recording.environment.sdk_identity', return_value={'package': 'fixture', 'version': '0'}), patch.dict('os.environ', {'PATH': '/usr/bin:/bin'}, clear=True):
                    prepare(args)
                path = Path(args.output) / ('SDK-US-006' if child else 'SDK-US-001')
                plan = json.loads((path / 'plan.json').read_text())
                if child:
                    self.assertEqual(plan['version'], 2)
                    self.assertNotIn('scenario', plan)
                    self.assertTrue((path / 'project/child-fixture.txt').exists())
                else:
                    self.assertEqual(plan['version'], 3)
                    validate_recipe(plan)
                    self.assertFalse((Path(args.output) / 'SDK-US-005').exists())
                    self.assertEqual((path / 'project/numbers.txt').read_text(), '17\n25\n')
                self.assertTrue((path / 'driver/agent_recording/scenarios.py').exists())
                self.assertTrue((path / 'driver/scenarios/SDK-US-003.json').exists())
                import subprocess
                result = subprocess.run([sys.executable, '-c', "from agent_recording.scenarios import recipe; print(recipe('codex', 'SDK-US-003')['progressAction'])"],
                                        cwd=tmp, env={'PYTHONPATH': str(path / 'driver'), 'PATH': '/usr/bin:/bin'},
                                        capture_output=True, text=True, check=True)
                self.assertEqual(result.stdout.strip(), 'submit-supplement')
                before = digest_module(path / 'driver/agent_recording')
                (path / 'driver/scenarios/SDK-US-003.json').write_text('{}')
                self.assertNotEqual(before, digest_module(path / 'driver/agent_recording'))

    def test_prepare_offline_selection_never_reads_sdk_or_auth(self):
        with patch('agent_recording.environment.sdk_identity', side_effect=AssertionError('SDK accessed')):
            with self.assertRaisesRegex(ValueError, 'no live recording path'):
                prepare(SimpleNamespace(provider='codex', stories='SDK-US-015'))

    def test_v2_child_adapter_is_not_claimed_as_v3_branch_pass(self):
        with tempfile.TemporaryDirectory() as tmp:
            plan = dict(version=2, childCase=True, provider='codex', story='SDK-US-006',
                        root=tmp, officialSDK={'package': 'fixture', 'version': '0'})
            with patch('agent_recording.drivers.sdk_identity', return_value=plan['officialSDK']), patch('agent_recording.children.codex') as child:
                self.assertEqual(run(plan), 0)
                child.assert_called_once()
            saved = json.loads((Path(tmp) / 'report.json').read_text())
            self.assertEqual(saved['status'], 'partial')
            self.assertNotIn('branchStatus', saved)
            self.assertNotIn('scenarioSHA256', saved)

    def test_scoped_case_pass_does_not_require_other_us_branches(self):
        for number in ('001', '002', '003', '004', '011', '012', '013', '016'):
            with self.subTest(number=number), tempfile.TemporaryDirectory() as tmp:
                definition = recipe('codex', 'SDK-US-' + number)
                plan = dict(provider='codex', story=definition['story'], root=tmp,
                            scenario=definition, scenarioSHA256=digest(definition),
                            prompts=definition['prompts'], officialSDK={'package': 'fixture', 'version': '0'})
                def adapter(_plan, saved, flow):
                    for _ in flow.inputs():
                        flow.submit()
                        action = flow.progress()
                        if action:
                            flow.action(action, 'accepted')
                        for name in definition['requiredRequests']:
                            flow.request(name)
                        flow.result()
                with patch('agent_recording.drivers.sdk_identity', return_value=plan['officialSDK']), patch('agent_recording.drivers.codex', side_effect=adapter):
                    self.assertEqual(run(plan), 0)
                saved = json.loads((Path(tmp) / 'report.json').read_text())
                self.assertEqual(saved['status'], 'passed')
                self.assertEqual(saved['branchStatus'], 'passed')
                self.assertEqual(saved['case'], definition['case'])
                self.assertEqual(saved['gaps'], [])

    def test_scoped_missing_evidence_cannot_be_excluded(self):
        with tempfile.TemporaryDirectory() as tmp:
            definition = recipe('codex', 'SDK-US-004')
            plan = dict(provider='codex', story=definition['story'], root=tmp,
                        scenario=definition, scenarioSHA256=digest(definition),
                        prompts=definition['prompts'], officialSDK={'package': 'fixture', 'version': '0'})
            def adapter(_plan, saved, flow):
                flow.submit()
                flow.result()
            with patch('agent_recording.drivers.sdk_identity', return_value=plan['officialSDK']), patch('agent_recording.drivers.codex', side_effect=adapter):
                self.assertEqual(run(plan), 1)
            saved = json.loads((Path(tmp) / 'report.json').read_text())
            self.assertEqual(saved['status'], 'untriggered')
            self.assertTrue(saved['gaps'])

    def test_scoped_child_missing_native_evidence_is_untriggered(self):
        from agent_recording.child_policy import case_contract
        with tempfile.TemporaryDirectory() as tmp:
            plan = dict(version=2, childCase=True, provider='codex', story='SDK-US-006',
                        root=tmp, officialSDK={'package': 'fixture', 'version': '0'},
                        case=case_contract('SDK-US-006'))
            def adapter(_plan, saved):
                saved.update(inputs=1, results=1)
                saved['gaps'].append('no native child identity')
            with patch('agent_recording.drivers.sdk_identity', return_value=plan['officialSDK']), patch('agent_recording.children.codex', side_effect=adapter):
                self.assertEqual(run(plan), 1)
            saved = json.loads((Path(tmp) / 'report.json').read_text())
            self.assertEqual(saved['status'], 'untriggered')
            self.assertIn('no native child identity', saved['gaps'])

    def test_approval_case_is_one_denied_write(self):
        definition = recipe('claude', 'SDK-US-009')
        self.assertEqual(definition['limits']['inputs'], 1)
        self.assertEqual(definition['requiredRequests'], ['denied_write'])
        self.assertEqual(len(definition['prompts']), 1)
        self.assertIn('denied.txt', definition['prompts'][0])

    def test_claude_denied_write_case_never_needs_an_allowed_write(self):
        @dataclass
        class ResultMessage:
            is_error: bool = False
        class Client:
            def __init__(self, options, **_): self.permission = options['can_use_tool']
            async def __aenter__(self): return self
            async def __aexit__(self, *_): pass
            async def query(self, _): pass
            async def receive_response(self):
                decision = await self.permission('Write', {'file_path': 'denied.txt', 'content': 'denied\n'}, None)
                self_test.assertEqual(decision['message'], 'Synthetic denial fixture; do not retry')
                yield ResultMessage()
        self_test = self
        sdk = ModuleType('claude_agent_sdk')
        sdk.ClaudeSDKClient = Client
        sdk.ClaudeAgentOptions = lambda **kw: kw
        sdk.Transport = object
        sdk.PermissionResultAllow = sdk.PermissionResultDeny = lambda **kw: kw
        sdk.ResultMessage = ResultMessage
        definition = recipe('claude', 'SDK-US-009')
        flow, saved = Flow(definition), report()
        with patch.dict('sys.modules', {'claude_agent_sdk': sdk}):
            asyncio.run(claude(dict(definition, project='/unused', model='fixture'), saved, flow))
        flow.finish()
        self.assertEqual(saved['requests'], 1)
        self.assertEqual(saved['inputs'], 1)
        self.assertEqual(saved['results'], 1)

    def test_configuration_changes_inputs_and_workflow_without_python_changes(self):
        import shutil
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp) / 'scenarios'
            shutil.copytree(scenario_directory(), directory)
            path = directory / 'SDK-US-003.json'
            core = json.loads(path.read_text())
            original = recipe('codex', 'SDK-US-003', directory)
            core['inputs']['input-1'] = 'Reply First.'
            core['inputs']['input-2'] = 'Reply Second.'
            core['steps'] = [{'op': 'session.open'},
                             {'op': 'input.submit', 'input': 'input-1'},
                             {'op': 'event.wait', 'event': 'execution.result', 'onMissing': 'untriggered'},
                             {'op': 'input.submit', 'input': 'input-2'},
                             {'op': 'event.wait', 'event': 'execution.result', 'onMissing': 'untriggered'},
                             {'op': 'session.close'}]
            path.write_text(json.dumps(core))
            definition = recipe('codex', 'SDK-US-003', directory)
            self.assertNotEqual(digest(original), digest(definition))
            self.assertEqual(definition['prompts'], ['Reply First.', 'Reply Second.'])
            flow = Flow(definition)
            self.assertIsNone(definition['progressAction'])
            for _ in flow.inputs():
                flow.submit()
                self.assertIsNone(flow.progress())
                flow.result()
            flow.finish()

    def test_new_scenario_requires_only_configuration_for_supported_operations(self):
        import shutil
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp) / 'scenarios'
            shutil.copytree(scenario_directory(), directory)
            index_path = directory / 'index.json'
            index = json.loads(index_path.read_text())
            index['scenarios'].append('SDK-US-020')
            index_path.write_text(json.dumps(index))
            core = json.loads((directory / 'SDK-US-001.json').read_text())
            core['id'] = 'SDK-US-020'
            core['goals'] = ['SDK-US-020']
            core['expect']['caseId'] = 'SDK-US-020/' + core['name']
            (directory / 'SDK-US-020.json').write_text(json.dumps(core))
            binding_path = directory / 'bindings/codex.json'
            binding = json.loads(binding_path.read_text())
            binding['scenarios']['SDK-US-020'] = deepcopy(binding['scenarios']['SDK-US-001'])
            binding_path.write_text(json.dumps(binding))
            definition = recipe('codex', 'SDK-US-020', directory)
            self.assertEqual(definition['story'], 'SDK-US-020')
            self.assertEqual(len(definition['prompts']), 2)
            flow = Flow(definition)
            for _ in flow.inputs():
                flow.submit()
                flow.result()
            flow.finish()

    def test_invalid_declarative_configuration_is_rejected(self):
        import shutil
        def unknown(core): core['python'] = 'eval()'
        def operation(core): core['steps'][1]['op'] = 'python.eval'
        def reference(core): core['steps'][1]['input'] = 'missing'
        def order(core): core['steps'].reverse()
        def escape(core): core['setup']['files']['../escape'] = 'invalid'
        def budget(core): core['limits']['activitySeconds'] = 111
        def missing(core): core.pop('expect')
        def missing_case(core): core['expect'].pop('caseId')
        def wrong_case(core): core['expect']['caseId'] = 'SDK-US-004/other'
        def empty_scope(core): core['expect']['scope'] = ''
        for change in (unknown, operation, reference, order, escape, budget, missing, missing_case, wrong_case, empty_scope):
            with self.subTest(change=change.__name__), tempfile.TemporaryDirectory() as tmp:
                directory = Path(tmp) / 'scenarios'
                shutil.copytree(scenario_directory(), directory)
                path = directory / 'SDK-US-003.json'
                core = json.loads(path.read_text())
                change(core)
                path.write_text(json.dumps(core))
                with self.assertRaises(ValueError):
                    recipe('codex', 'SDK-US-003', directory)

    def test_duplicate_json_keys_are_rejected(self):
        import shutil
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp) / 'scenarios'
            shutil.copytree(scenario_directory(), directory)
            path = directory / 'SDK-US-003.json'
            path.write_text('{"version":1,"version":2}')
            with self.assertRaisesRegex(ValueError, 'duplicate scenario JSON key'):
                recipe('codex', 'SDK-US-003', directory)

    def test_no_supplement_fallback_when_progress_is_absent(self):
        flow = Flow(recipe('codex', 'SDK-US-003'))
        self.assertEqual(len(flow.inputs()), 1)
        flow.submit()
        flow.result()
        self.assertIsNone(flow.progress())
        with self.assertRaises(Untriggered):
            flow.finish()
        self.assertEqual(flow.sent, 1)

    def test_native_rejection_is_retained_without_second_result(self):
        flow = Flow(recipe('codex', 'SDK-US-003'))
        flow.submit()
        action = flow.progress()
        flow.action(action, 'rejected')
        self.assertIsNone(flow.progress())
        with self.assertRaises(ValueError):
            flow.action(action, 'accepted')
        flow.result()
        flow.finish()
        self.assertIn({'step': 'submit-supplement', 'outcome': 'rejected'}, flow.trace)

    def test_request_authority_order_and_input_budget(self):
        flow = Flow(recipe('codex', 'SDK-US-004'))
        with self.assertRaises(ValueError):
            flow.request('fixture_read')
        flow.submit()
        with self.assertRaises(ValueError):
            flow.submit()
        with self.assertRaises(ValueError):
            flow.request('fixture_write')
        flow.request('fixture_read')
        with self.assertRaises(ValueError):
            flow.request('fixture_read')
        flow.result()
        with self.assertRaises(Untriggered):
            flow.finish()
        with self.assertRaises(ValueError):
            flow.submit()

    def test_non_live_paths_do_not_import_or_launch_sdk(self):
        with tempfile.TemporaryDirectory() as tmp:
            for number in ('005', '006', '007', '008', '014', '015', '017', '018', '019'):
                root = Path(tmp) / number
                root.mkdir()
                definition = recipe('codex', 'SDK-US-' + number)
                plan = dict(provider='codex', story='SDK-US-' + number, root=str(root),
                            scenario=definition, scenarioSHA256=digest(definition), prompts=definition['prompts'])
                with patch('agent_recording.drivers.sdk_identity', side_effect=AssertionError('SDK accessed')):
                    self.assertEqual(run(plan), 0)
                saved = json.loads((root / 'report.json').read_text())
                self.assertEqual(saved['inputs'], 0)
                self.assertEqual(saved['status'], definition['mode'])

    def test_replay_requires_flow_and_branch_identity_in_addition_to_wire_eof(self):
        for field, changed in [('flowTrace', []), ('scenarioSHA256', 'changed'), ('branchStatus', 'untriggered'), ('branchStatus', 'passed')]:
            with self.subTest(field=field), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                original = dict(status='partial', inputs=1, results=1, requests=0, receipts=[],
                                scenarioSHA256='frozen', branchStatus='passed', flowTrace=[{'step': 'result'}])
                private_json(root / 'report.json', original)
                private_json(root / 'replay-report.json', dict(original, **{field: changed}))
                private_json(root / 'replay-peer.json', {'fullConsumption': True, 'nativeLaunched': False})
                self.assertEqual(validate_replay(root, 0), 'passed' if field == 'branchStatus' and changed == 'passed' else 'failed')

    def test_replay_reproduces_untriggered_without_promoting_it_to_pass(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            original = dict(status='untriggered', inputs=1, results=1, requests=0, receipts=[],
                            scenarioSHA256='frozen', branchStatus='untriggered', flowTrace=[{'step': 'result'}])
            private_json(root / 'report.json', original)
            private_json(root / 'replay-report.json', original)
            private_json(root / 'replay-peer.json', {'fullConsumption': True, 'nativeLaunched': False})
            self.assertEqual(validate_replay(root, 1), 'expected-untriggered-reproduced')
            self.assertEqual(json.loads((root / 'report.json').read_text()), original)

    def test_replay_requires_same_case_scope_and_missing_evidence(self):
        for field, changed in [('case', {'id': 'different'}), ('gaps', ['missing child']),
                               ('limitations', ['changed scope']), ('branchStatus', 'failed')]:
            with self.subTest(field=field), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                original = dict(status='passed', inputs=1, results=1, requests=0, receipts=[],
                                case={'id': 'child-read', 'scope': 'fixed read', 'outOfScope': ['nested']},
                                gaps=[], limitations=[], branchStatus='passed')
                private_json(root/'report.json', original)
                private_json(root/'replay-report.json', dict(original, **{field: changed}))
                private_json(root/'replay-peer.json', {'fullConsumption': True, 'nativeLaunched': False})
                self.assertEqual(validate_replay(root, 0), 'failed')

    def test_manifest_refuses_changed_plan(self):
        import hashlib
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            private_json(root / 'plan.json', {'fixture': 'original'})
            private_json(root / 'manifest.json', {'files': {}, 'planSHA256': hashlib.sha256((root / 'plan.json').read_bytes()).hexdigest()})
            verify_recording(root)
            (root / 'plan.json').write_text('{}')
            with self.assertRaises(ValueError):
                verify_recording(root)

    def test_codex_native_adapter_uses_barrier_once_and_waits_between_turns(self):
        for number, progress in [('001', True), ('003', True), ('003', False), ('016', True), ('016', False)]:
            with self.subTest(number=number, progress=progress):
                actions = []
                class Client:
                    def __init__(self, **_):
                        self.turns = 0
                        self.events = {}
                    def __enter__(self): return self
                    def __exit__(self, *_): actions.append('closed')
                    def initialize(self): return {}
                    def thread_start(self, _): return Value(thread=Value(id='root'), model='fixture')
                    def turn_start(self, *_, **__):
                        self.turns += 1
                        identity = str(self.turns)
                        actions.append('submit-' + identity)
                        done = Value(method='turn/completed', payload=Value(turn=Value(id=identity, status=Value(value='completed'))))
                        events = [done]
                        if progress:
                            events.insert(0, Value(method='item/started', payload=Value(item={'type': 'agentMessage'})))
                        self.events[identity] = iter(events)
                        return Value(turn=Value(id=identity))
                    def next_turn_notification(self, identity):
                        event = next(self.events[identity])
                        actions.append(event.method + '-' + identity)
                        return event
                    def wait_for_turn_completed(self, identity): return {'id': identity}
                    def turn_interrupt(self, *_): actions.append('interrupt'); return {}
                client_module = ModuleType('openai_codex.client')
                client_module.CodexClient = Client
                client_module.CodexConfig = lambda **kw: kw
                types_module = ModuleType('openai_codex.generated.v2_all')
                types_module.TurnStartParams = SimpleNamespace(model_validate=lambda value: value)
                model_module = ModuleType('pydantic')
                model_module.BaseModel = object
                model_module.ConfigDict = lambda **kw: kw
                definition = recipe('codex', 'SDK-US-' + number)
                flow = Flow(definition)
                plan = dict(definition, root='/unused', project='/unused', model='fixture')
                modules = {'openai_codex.client': client_module, 'openai_codex.generated.v2_all': types_module, 'pydantic': model_module}
                with patch.dict('sys.modules', modules):
                    codex(plan, report(), flow)
                if not progress and number in ('003', '016'):
                    with self.assertRaises(Untriggered): flow.finish()
                    self.assertNotIn('submit-2', actions)
                else:
                    flow.finish()
                if number == '001':
                    self.assertLess(actions.index('turn/completed-1'), actions.index('submit-2'))
                if number == '003' and progress:
                    self.assertLess(actions.index('item/started-1'), actions.index('submit-2'))
                    self.assertLess(actions.index('submit-2'), actions.index('turn/completed-1'))
                if number == '016' and progress:
                    self.assertEqual(actions.count('interrupt'), 1)

    def test_claude_interrupt_result_requires_acknowledged_cancellation(self):
        cases = [
            ('stream-aborted', '016', True, True, 'error_during_execution', 'aborted_streaming', None, False),
            ('tools-aborted', '016', True, True, 'error_during_execution', 'aborted_tools', None, False),
            ('no-progress', '016', False, True, 'error_during_execution', 'aborted_streaming', None, True),
            ('interrupt-rejected', '016', True, False, 'error_during_execution', 'aborted_streaming', None, True),
            ('ordinary-task', '001', True, True, 'error_during_execution', 'aborted_streaming', None, True),
            ('unrelated-error', '016', True, True, 'error_during_execution', 'completed', None, True),
            ('missing-reason', '016', True, True, 'error_during_execution', None, None, True),
            ('wrong-subtype', '016', True, True, 'error_max_turns', 'aborted_streaming', None, True),
            ('api-error', '016', True, True, 'error_during_execution', 'aborted_streaming', 500, True),
        ]
        for name, number, progress, accepted, result_subtype, reason, api_status, fails in cases:
            with self.subTest(name=name):
                @dataclass
                class ResultMessage:
                    is_error: bool = True
                    terminal_reason: str = reason
                    subtype: str = result_subtype
                    api_error_status: int = api_status
                class StreamEvent: pass
                class Client:
                    def __init__(self, **_): pass
                    async def __aenter__(self): return self
                    async def __aexit__(self, *_): pass
                    async def query(self, _): pass
                    async def interrupt(self):
                        if not accepted:
                            raise ValueError('interrupt rejected')
                    async def receive_response(self):
                        if progress:
                            yield StreamEvent()
                        yield ResultMessage()
                sdk = ModuleType('claude_agent_sdk')
                sdk.ClaudeSDKClient = Client
                sdk.ClaudeAgentOptions = lambda **kw: kw
                sdk.Transport = object
                sdk.PermissionResultAllow = sdk.PermissionResultDeny = lambda **kw: kw
                sdk.ResultMessage = ResultMessage
                definition = recipe('claude', 'SDK-US-' + number)
                flow, saved = Flow(definition), report()
                with patch.dict('sys.modules', {'claude_agent_sdk': sdk}):
                    call = claude(dict(definition, project='/unused', model='fixture'), saved, flow)
                    if fails:
                        with self.assertRaises(ValueError):
                            asyncio.run(call)
                    else:
                        asyncio.run(call)
                        flow.finish()
                        self.assertEqual(saved['results'], 1)
                        self.assertTrue(saved['receipts'][-2]['result']['is_error'])
                        self.assertEqual(saved['receipts'][-1], {'interrupted-result': {'terminalReason': reason}})

    def test_claude_native_adapter_uses_barrier_once(self):
        for number, progress in [('001', True), ('003', True), ('003', False), ('016', True), ('016', False)]:
            with self.subTest(number=number, progress=progress):
                actions = []
                @dataclass
                class ResultMessage:
                    is_error: bool = False
                class StreamEvent: pass
                class Client:
                    def __init__(self, **_): pass
                    async def __aenter__(self): return self
                    async def __aexit__(self, *_): actions.append('closed')
                    async def query(self, _): actions.append('submit')
                    async def interrupt(self): actions.append('interrupt')
                    async def receive_response(self):
                        if progress:
                            actions.append('progress')
                            yield StreamEvent()
                        actions.append('result')
                        yield ResultMessage()
                sdk = ModuleType('claude_agent_sdk')
                sdk.ClaudeSDKClient = Client
                sdk.ClaudeAgentOptions = lambda **kw: kw
                sdk.Transport = object
                sdk.PermissionResultAllow = sdk.PermissionResultDeny = lambda **kw: kw
                sdk.ResultMessage = ResultMessage
                definition = recipe('claude', 'SDK-US-' + number)
                flow = Flow(definition)
                with patch.dict('sys.modules', {'claude_agent_sdk': sdk}):
                    asyncio.run(claude(dict(definition, project='/unused', model='fixture'), report(), flow))
                if not progress and number in ('003', '016'):
                    with self.assertRaises(Untriggered): flow.finish()
                    self.assertEqual(actions.count('submit'), 1)
                else:
                    flow.finish()
                if number == '001':
                    self.assertEqual(actions, ['submit', 'progress', 'result', 'submit', 'progress', 'result', 'closed'])
                if number == '003' and progress:
                    self.assertEqual(actions[:4], ['submit', 'progress', 'submit', 'result'])
                if number == '016' and progress:
                    self.assertEqual(actions.count('interrupt'), 1)


if __name__ == '__main__':
    unittest.main()
