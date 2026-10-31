"""Offline safety tests. No CLI/model/network access."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
from types import SimpleNamespace

from support import BOOTSTRAP
from record_user_story.peer import identity
from record_user_story.environment import config_environment, codex_config, private_json, read_regular, prepare, manifest, story_prompts, recording_output, recording_temp_root
from record_user_story.policy import native_arguments
from record_user_story.proxy import validate
from record_user_story.cli import replay_source, archive_replay, validate_replay
from record_user_story.child_policy import check_output


class SafetyTests(unittest.TestCase):
    def test_live_proxy_requires_explicit_record_before_any_lookup(self):
        from record_user_story.proxy import run
        with patch.dict(os.environ, {}, clear=True), patch('record_user_story.proxy.validate', side_effect=AssertionError('unexpected lookup')):
            with self.assertRaisesRegex(ValueError, 'explicit record'):
                run({})

    def test_output_uses_program_temp_directory_and_resolves_aliases(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp).resolve()
            with patch('record_user_story.environment.tempfile.gettempdir', return_value=str(base)):
                self.assertEqual(recording_output(base / 'new-suite'), base / 'new-suite')
                for invalid in [base, base.parent / 'outside', Path('relative')]:
                    with self.assertRaises(ValueError):
                        recording_output(invalid)
                alias = base / 'alias'
                alias.symlink_to(base, target_is_directory=True)
                self.assertEqual(recording_output(alias / 'new-suite'), base / 'new-suite')
                alias.unlink()
                alias.symlink_to(base.parent, target_is_directory=True)
                with self.assertRaises(ValueError):
                    recording_output(alias / 'escaped-suite')

    def test_endpoint_credentials_are_one_source(self):
        selected = {'env': {'ANTHROPIC_BASE_URL': 'selected', 'ANTHROPIC_API_KEY': 'selected-key'}}
        got = config_environment(selected, {'ANTHROPIC_BASE_URL': 'other', 'ANTHROPIC_AUTH_TOKEN': 'other-token'})
        self.assertEqual(got, selected['env'])

    def test_codex_drops_integrations_and_projects(self):
        got = codex_config({'model': 'fixture', 'projects': {'private': {}}, 'mcp_servers': {'evil': {}}, 'notify': ['evil']})
        self.assertEqual(got, {'model': 'fixture'})

    def test_source_symlink_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'link'
            path.symlink_to('/etc/hosts')
            with self.assertRaises(OSError):
                read_regular(path)

    def test_private_json_never_overwrites(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'attempt.started'
            private_json(path, {'reserved': True})
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            with self.assertRaises(FileExistsError):
                private_json(path, {})

    def test_configured_credential_variable_is_scanned_without_key_suffix(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root/'raw').mkdir()
            (root/'config').mkdir()
            (root/'config/config.toml').write_text('model_provider="fixture"\n[model_providers.fixture]\nenv_key="FIXTURE_CREDENTIAL"\n')
            private_json(root/'native.env.private.json', {'FIXTURE_CREDENTIAL': 'synthetic-scan-canary'})
            plan = {key: 'fixture' for key in ['provider', 'story', 'cliVersion', 'cliSHA256', 'driverSHA256', 'sdkRevision', 'sourceSHA256']}
            plan.update(officialSDK={'package': 'fixture', 'version': '0'}, prompts=[])
            plan['agentMock'] = identity()
            private_json(root/'plan.json', plan)
            (root/'raw/raw.jsonl').write_text('{"direction":"s2c","wire":{"text":"synthetic-scan-canary"}}\n')
            self.assertFalse(manifest(root, {'status': 'partial'}))
            self.assertEqual(json.loads((root/'manifest.json').read_text())['credentialScan'], 'quarantined')

    def test_unavailable_sdk_fails_before_creating_auth_copies(self):
        with tempfile.TemporaryDirectory() as tmp:
            target = Path(tmp) / 'suite'
            with patch('record_user_story.environment.sdk_identity', side_effect=ValueError('SDK unavailable')):
                with self.assertRaises(ValueError):
                    prepare(SimpleNamespace(provider='codex', output=str(target)))
            self.assertFalse(target.exists())

    def test_repeated_replay_retains_previous_receipts(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            private_json(root / 'report.json', {'status': 'partial'})
            private_json(root / 'replay-report.json', {'status': 'failed'})
            private_json(root / 'replay-peer.json', {'fullConsumption': False})
            archive_replay(root)
            self.assertEqual(json.loads((root/'replays/attempt-0001/replay-report.json').read_text())['status'], 'failed')
            self.assertEqual(json.loads((root/'report.json').read_text())['status'], 'partial')
            self.assertFalse((root/'replay-report.json').exists())
            private_json(root / 'replay-report.json', {'status': 'partial'})
            archive_replay(root)
            self.assertTrue((root/'replays/attempt-0002/replay-report.json').exists())

    def test_replay_requires_receipts_full_eof_and_exact_failure(self):
        cases = [('success', False, False, True, 'passed'),
                 ('changed-receipt', False, True, True, 'failed'),
                 ('missing-eof', False, False, False, 'failed'),
                 ('same-failure', True, False, True, 'expected-failure-reproduced'),
                 ('changed-failure', True, False, True, 'failed')]
        for name, failed, changed, full, expected in cases:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                original = {'status': 'failed' if failed else 'passed', 'inputs': 1,
                            'results': 0 if failed else 1, 'requests': 0, 'receipts': [{'native': 'retained'}]}
                replay = dict(original)
                if changed:
                    replay['receipts'] = [{'native': 'changed'}]
                private_json(root/'report.json', original)
                private_json(root/'replay-report.json', replay)
                if full:
                    private_json(root/'replay-peer.json', {'fullConsumption': True, 'nativeLaunched': False, 'peer': 'Go native mock', 'clientEOF': True})
                if failed:
                    private_json(root/'diagnostics.private.json', {'error': 'native failure'})
                    private_json(root/'replay-diagnostics.private.json', {'error': 'different' if name == 'changed-failure' else 'native failure'})
                self.assertEqual(validate_replay(root, int(failed)), expected)
                self.assertEqual(json.loads((root/'report.json').read_text()), original)

    def test_native_hook_child_and_extra_tool_events_are_observed(self):
        for provider in ['codex', 'claude']:
            arguments = native_arguments(provider, 'SDK-US-006', True)
            self.assertNotIn('--max-turns', arguments)
            self.assertNotIn('features.shell_tool=false', arguments)
            self.assertNotIn('--tools', arguments)
            self.assertEqual(len(story_prompts(provider, 'SDK-US-005', case_id='SDK-CASE-016')), 1)
        plan = {'provider': 'claude', 'story': 'SDK-US-006', 'childCase': True}
        state = {}
        root = {'type': 'assistant', 'message': {'content': [{'type': 'tool_use', 'name': 'Agent', 'id': 'one'}]}}
        check_output(plan, root, state)
        check_output(plan, {'type': 'system', 'subtype': 'task_started', 'task_id': 'child', 'tool_use_id': 'one'}, state)
        check_output(plan, {'type': 'assistant', 'parent_tool_use_id': 'one', 'message': {'content': [{'type': 'tool_use', 'name': 'Read', 'id': 'read'}]}}, state)
        for event in [
            {'type': 'assistant', 'message': {'content': [{'type': 'tool_use', 'name': 'Agent', 'id': 'two'}]}},
            {'type': 'assistant', 'parent_tool_use_id': 'one', 'message': {'content': [{'type': 'tool_use', 'name': 'Agent', 'id': 'nested'}]}},
            {'type': 'assistant', 'parent_tool_use_id': 'one', 'message': {'content': [{'type': 'tool_use', 'name': 'Bash', 'id': 'shell'}]}},
        ]:
            check_output(plan, event, state)
        check_output({'provider': 'claude', 'story': 'SDK-US-001'}, {'parent_tool_use_id': 'one'}, state)
        check_output(plan, {'method': 'hook/started'}, state)
        self.assertEqual(state['hookEvents'], 1)
        self.assertGreater(state['childEvents'], 0)

    def test_claude_recording_requests_the_permissions_its_flows_observe(self):
        for profile, expected in [('fixture-tools', ['Read', 'Write']),
                                  ('deferred-approval', ['Read']),
                                  ('children', ['Agent', 'Read']),
                                  ('background-children', ['Agent', 'Read'])]:
            with self.subTest(profile=profile):
                args = native_arguments('claude', 'SDK-US-005', profile=profile)
                self.assertIn('--settings', args)
                settings = json.loads(args[args.index('--settings') + 1])
                self.assertEqual(settings['permissions']['ask'], expected)
                if profile in ('children', 'background-children'):
                    self.assertIn('--forward-subagent-text', args)

    def test_raw_replay_preserves_native_numeric_lexemes(self):
        for provider in ['codex', 'claude']:
            with self.subTest(provider=provider), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                (root / 'raw').mkdir()
                wire = b'{"opaque":0.123456789012345678901234567890,"id":9007199254740993}'
                data = b'{"ordinal":1,"direction":"s2c","channel":"protocol","epoch":1,"elapsedMs":0,"wire":' + wire + b'}\n'
                (root / 'raw/raw.jsonl').write_bytes(data)
                private_json(root / 'plan.json', {'provider': provider})
                private_json(root / 'manifest.json', {'files': {'raw.jsonl': hashlib.sha256(data).hexdigest()}})
                path = replay_source(root)
                self.assertIn(wire, path.read_bytes())
                self.assertEqual(replay_source(root), path)
                (root / 'raw/raw.jsonl').write_bytes(data.replace(b'9007199254740993', b'9007199254740992'))
                with self.assertRaises(ValueError):
                    replay_source(root)

    def test_proxy_captures_raw_large_integer_and_reaps(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve() / 'SDK-US-001' / 'SDK-CASE-001'
            root.mkdir(parents=True)
            for name in ['home', 'config', 'project', 'tmp']:
                (root / name).mkdir(mode=0o700)
            cli = root / 'native'
            # Reads only a protocol input; this fixture has no model or filesystem tools.
            cli.write_text('#!' + sys.executable + '\nimport sys\nline=sys.stdin.buffer.readline()\nsys.stdout.buffer.write(line)\nsys.stdout.buffer.flush()\n')
            cli.chmod(0o700)
            plan = {'version': 4, 'provider': 'codex', 'story': 'SDK-US-001', 'storyId': 'SDK-US-001', 'caseId': 'SDK-CASE-001', 'driverProfile': 'continuation', 'cli': str(cli),
                    'cliVersion': '0.161.0', 'cliSHA256': hashlib.sha256(cli.read_bytes()).hexdigest(),
                    'root': str(root), 'tempRoot': str(recording_temp_root()),
                    'home': str(root/'home'), 'config': str(root/'config'),
                    'project': str(root/'project'), 'raw': str(root/'raw'),
                    'args': native_arguments('codex', 'SDK-US-001'), 'model': '',
                    'prompts': story_prompts('codex', 'SDK-US-001', case_id='SDK-CASE-001')}
            from record_user_story.scenarios import recipe, digest
            definition = recipe('codex', 'SDK-US-001', case_id='SDK-CASE-001')
            plan.update(scenario=definition, scenarioSHA256=digest(definition))
            for name, value in definition['files'].items():
                (root / 'project' / name).write_text(value)
            plan['agentMock'] = identity()
            private_json(root / 'plan.json', plan)
            private_json(root / 'native.env.private.json', {'HOME': plan['home'], 'CODEX_HOME': plan['config'], 'TMPDIR': str(root/'tmp'), 'PATH': '/usr/bin:/bin'})
            validate(plan)
            # Valid launch arguments/prompts and another CLI version are data,
            # rather than an allowlist. A changed executable is still an error.
            plan['cliVersion'] = '99.0.0'
            plan['args'] += ['extra-native-option']
            plan['prompts'].append('Another configured input')
            with self.assertRaisesRegex(ValueError, 'prompts changed'):
                validate(plan)
            plan['prompts'].pop()
            validate(plan)
            plan['cliSHA256'] = '0' * 64
            with self.assertRaisesRegex(ValueError, 'native executable changed'):
                validate(plan)
            plan['cliSHA256'] = hashlib.sha256(cli.read_bytes()).hexdigest()
            # Keep input open until the echoed frame arrives; EOF itself asks the proxy to close.
            env = {'PATH': '/usr/bin:/bin', 'SDK_REAL_AGENT_E2E': '1'}
            process = subprocess.Popen([sys.executable, str(BOOTSTRAP), 'proxy', '--plan', str(root/'plan.json')], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env)
            wire = b'{"id":9007199254740993,"opaque":{"unknown":true}}\n'
            process.stdin.write(wire)
            process.stdin.flush()
            echoed = process.stdout.readline()
            self.assertEqual(echoed, wire, (process.stderr.read().decode() + repr([p.read_text() for p in root.glob('*diagnostics*')])) if not echoed else '')
            process.stdin.close()
            self.assertEqual(process.wait(timeout=8), 0)
            process.stdout.close()
            process.stderr.close()
            frames = [json.loads(x) for x in (root/'raw/raw.jsonl').read_text().splitlines()]
            self.assertEqual([f['direction'] for f in frames], ['c2s', 's2c'])
            self.assertEqual(frames[1]['wire']['id'], 9007199254740993)
            self.assertTrue(json.loads((root/'raw/transport.json').read_text())['cleanupComplete'])
            # A second native launch cannot consume the same plan.
            proc = subprocess.run([sys.executable, str(BOOTSTRAP), 'proxy', '--plan', str(root/'plan.json')], input=b'', capture_output=True, env=env)
            self.assertNotEqual(proc.returncode, 0)


if __name__ == '__main__':
    unittest.main()
