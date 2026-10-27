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

from agent_recording.environment import config_environment, codex_config, private_json, read_regular, prepare, manifest, story_prompts
from agent_recording.policy import native_arguments
from agent_recording.proxy import validate
from agent_recording.cli import export_scenario, archive_replay, validate_replay
from agent_recording.child_policy import check_output


class SafetyTests(unittest.TestCase):
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
            private_json(root/'plan.json', plan)
            (root/'raw/raw.jsonl').write_text('{"direction":"s2c","wire":{"text":"synthetic-scan-canary"}}\n')
            self.assertFalse(manifest(root, {'status': 'partial'}))
            self.assertEqual(json.loads((root/'manifest.json').read_text())['credentialScan'], 'quarantined')

    def test_unreviewed_sdk_fails_before_creating_auth_copies(self):
        with tempfile.TemporaryDirectory() as tmp:
            target = Path(tmp) / 'suite'
            with patch('agent_recording.environment.sdk_identity', side_effect=ValueError('version mismatch')):
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
                original = {'status': 'failed' if failed else 'partial', 'inputs': 1,
                            'results': 0 if failed else 1, 'requests': 0, 'receipts': [{'native': 'retained'}]}
                replay = dict(original)
                if changed:
                    replay['receipts'] = [{'native': 'changed'}]
                private_json(root/'report.json', original)
                private_json(root/'replay-report.json', replay)
                if full:
                    private_json(root/'replay-peer.json', {'fullConsumption': True, 'nativeLaunched': False})
                if failed:
                    private_json(root/'diagnostics.private.json', {'error': 'native failure'})
                    private_json(root/'replay-diagnostics.private.json', {'error': 'different' if name == 'changed-failure' else 'native failure'})
                self.assertEqual(validate_replay(root, int(failed)), expected)
                self.assertEqual(json.loads((root/'report.json').read_text()), original)

    def test_child_profile_limits_tools_and_rejects_second_or_nested_agent(self):
        for provider in ['codex', 'claude']:
            arguments = native_arguments(provider, 'SDK-US-006', True)
            self.assertIn('--tools' if provider == 'claude' else 'features.shell_tool=false', arguments)
            self.assertEqual(len(story_prompts(provider, 'SDK-US-006', True)), 1)
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
            with self.assertRaises(ValueError):
                check_output(plan, event, dict(state, agentTools={'one'}))
        with self.assertRaises(ValueError):
            check_output({'provider': 'claude', 'story': 'SDK-US-006'}, {'parent_tool_use_id': 'one'}, {})

    def test_go_export_preserves_native_numeric_lexemes(self):
        for provider in ['codex', 'claude']:
            with self.subTest(provider=provider), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                (root / 'raw').mkdir()
                wire = b'{"opaque":0.123456789012345678901234567890,"id":9007199254740993}'
                data = b'{"ordinal":1,"direction":"s2c","channel":"protocol","epoch":1,"elapsedMs":0,"wire":' + wire + b'}\n'
                (root / 'raw/raw.jsonl').write_bytes(data)
                private_json(root / 'plan.json', {'provider': provider})
                private_json(root / 'manifest.json', {'files': {'raw.jsonl': hashlib.sha256(data).hexdigest()}})
                path = export_scenario(root)
                self.assertIn(wire, path.read_bytes())
                self.assertEqual(export_scenario(root), path)
                (root / 'raw/raw.jsonl').write_bytes(data.replace(b'9007199254740993', b'9007199254740992'))
                with self.assertRaises(ValueError):
                    export_scenario(root)

    def test_proxy_captures_raw_large_integer_and_reaps(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            for name in ['home', 'config', 'project', 'raw', 'tmp']:
                (root / name).mkdir(mode=0o700)
            cli = root / 'native'
            # Reads only a protocol input; this fixture has no model or filesystem tools.
            cli.write_text('#!' + sys.executable + '\nimport sys\nline=sys.stdin.buffer.readline()\nsys.stdout.buffer.write(line)\nsys.stdout.buffer.flush()\n')
            cli.chmod(0o700)
            plan = {'version': 2, 'provider': 'codex', 'story': 'SDK-US-001', 'cli': str(cli),
                    'cliVersion': '0.161.0', 'cliSHA256': hashlib.sha256(cli.read_bytes()).hexdigest(),
                    'root': str(root), 'home': str(root/'home'), 'config': str(root/'config'),
                    'project': str(root/'project'), 'raw': str(root/'raw'),
                    'args': native_arguments('codex', 'SDK-US-001'), 'model': '',
                    'prompts': story_prompts('codex', 'SDK-US-001')}
            private_json(root / 'plan.json', plan)
            private_json(root / 'native.env.private.json', {'HOME': plan['home'], 'CODEX_HOME': plan['config'], 'TMPDIR': str(root/'tmp'), 'PATH': '/usr/bin:/bin'})
            validate(plan)
            plan['prompts'].append('Unreviewed extra task')
            with self.assertRaises(ValueError):
                validate(plan)
            plan['prompts'] = story_prompts('codex', 'SDK-US-001')
            plan['args'] += ['unreviewed']
            with self.assertRaises(ValueError):
                validate(plan)
            plan['prompts'].append('Unreviewed extra task')
            with self.assertRaises(ValueError):
                validate(plan)
            plan['prompts'] = story_prompts('codex', 'SDK-US-001')
            plan['args'] = native_arguments('codex', 'SDK-US-001')
            # Keep input open until the echoed frame arrives; EOF itself asks the proxy to close.
            env = {'PATH': '/usr/bin:/bin', 'SDK_REAL_AGENT_E2E': '1'}
            process = subprocess.Popen([sys.executable, str(Path(__file__).parent/'agent_recording/bootstrap.py'), 'proxy', '--plan', str(root/'plan.json')], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env)
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
            proc = subprocess.run([sys.executable, str(Path(__file__).parent/'agent_recording/bootstrap.py'), 'proxy', '--plan', str(root/'plan.json')], input=b'', capture_output=True, env=env)
            self.assertNotEqual(proc.returncode, 0)


if __name__ == '__main__':
    unittest.main()
