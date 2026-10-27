import argparse
import contextlib
import io
import json
import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('record_stories', Path(__file__).with_name('record-stories.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

class RecordingConfigurationTests(unittest.TestCase):
    def test_user_endpoint_and_credential_replace_conflicting_process_pair(self):
        settings = {'env': {'ANTHROPIC_BASE_URL': 'https://user.example.invalid', 'ANTHROPIC_AUTH_TOKEN': 'user-secret-canary'}}
        inherited = {'ANTHROPIC_BASE_URL': 'http://127.0.0.1:1', 'ANTHROPIC_AUTH_TOKEN': 'platform-secret-canary', 'ANTHROPIC_API_KEY': 'stale-canary', 'UNRELATED_SECRET': 'never-inherit'}
        selected = module.config_environment(settings, inherited)
        self.assertEqual(selected, settings['env'])
        self.assertNotIn('ANTHROPIC_API_KEY', selected)
        self.assertNotIn('UNRELATED_SECRET', selected)

    def test_codex_connection_copy_excludes_private_projects_and_integrations(self):
        source = {'model': 'test-model', 'model_provider': 'fixture', 'projects': {'/private/user-code': {'trust_level': 'trusted'}}, 'notify': ['/private/hook'], 'mcp_servers': {'private': {}}, 'model_providers': {'fixture': {'name': 'test', 'base_url': 'https://test.invalid', 'env_key': 'TEST_KEY', 'request_max_retries': 8, 'private_extension': 'secret'}}}
        copied = module.codex_config(source)
        self.assertEqual(set(copied), {'model', 'model_provider', 'model_providers'})
        provider = copied['model_providers']['fixture']
        self.assertEqual(provider['request_max_retries'], 0)
        self.assertEqual(provider['stream_max_retries'], 0)
        self.assertNotIn('private_extension', provider)
        self.assertNotIn('/private', module.toml_value(copied))

    def test_auth_copy_refuses_symlink_and_oversize(self):
        with tempfile.TemporaryDirectory() as d:
            path = Path(d) / 'auth.json'
            path.write_text('secret-canary')
            alias = Path(d) / 'alias'
            alias.symlink_to(path)
            with self.assertRaises(OSError):
                module.read_regular(alias)
            with self.assertRaises(ValueError):
                module.read_regular(path, 4)

    def test_private_outputs_are_exclusive(self):
        with tempfile.TemporaryDirectory() as d:
            path = Path(d) / 'plan.json'
            module.private_json(path, {'story': 'SDK-US-001'})
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            with self.assertRaises(FileExistsError):
                module.private_json(path, {})

    def test_prepare_resolves_parent_alias_before_recording_absolute_paths(self):
        with tempfile.TemporaryDirectory() as d:
            base = Path(d)
            real = base / 'real'
            real.mkdir()
            alias = base / 'alias'
            alias.symlink_to(real, target_is_directory=True)
            source = base / 'source'
            source.mkdir()
            (source / 'settings.json').write_text(json.dumps({'env': {'ANTHROPIC_BASE_URL': 'https://test.invalid', 'ANTHROPIC_AUTH_TOKEN': 'canary-auth'}}))
            cli = base / 'cli'
            cli.write_text('#!/bin/sh\nprintf "2.1.199\\n"\n')
            cli.chmod(0o700)
            driver = base / 'driver'
            driver.write_text('#!/usr/bin/env python3\nprint(\'{"args":[],"cliVersions":["2.1.199"]}\')\n')
            driver.chmod(0o700)
            args = argparse.Namespace(output=str(alias / 'recording'), provider='claude', cli=str(cli), driver=str(driver), source_config=str(source), model=None, stories='SDK-US-001')
            with contextlib.redirect_stdout(io.StringIO()):
                module.prepare(args)
            root = real / 'recording/SDK-US-001'
            plan_text = (root / 'plan.json').read_text()
            plan = json.loads(plan_text)
            self.assertEqual(plan['root'], str(root.resolve()))
            self.assertNotIn('canary-auth', plan_text)
            self.assertEqual((root / 'native.env.private.json').stat().st_mode & 0o777, 0o600)

if __name__ == '__main__':
    unittest.main()
