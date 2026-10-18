import copy
import importlib.util
import pathlib
import json
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('sanitize', pathlib.Path(__file__).with_name('recording_sanitize.py'))
sanitize = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sanitize)


class Publication(unittest.TestCase):
    def test_synthetic_lifecycle_whitelist_and_associations(self):
        p = pathlib.Path(__file__).resolve().parents[1]/'internal/recording/testdata/synthetic-faults/lifecycle/protocol.jsonl'
        frames = [json.loads(line) for line in p.read_text().splitlines()]
        clean, omitted, ids = sanitize.transform(frames, '/synthetic-project', '/synthetic-home')
        self.assertEqual(len(clean), len(frames))
        self.assertEqual(omitted, [])
        self.assertEqual(clean[0]['wire']['id'], clean[1]['wire']['id'])
        self.assertEqual(clean[8]['wire']['id'], clean[9]['wire']['id'])
        self.assertEqual(clean[4]['wire']['result']['thread']['id'], clean[5]['wire']['params']['threadId'])
        self.assertTrue(ids)

    def test_live_guard_and_user_hook_identity(self):
        spec = importlib.util.spec_from_file_location('record_real', pathlib.Path(__file__).with_name('record_real.py'))
        runner = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(runner)
        for mode, env in [(False, {}), (True, {}), (True, {'SDK_REAL_AGENT_E2E': '1', 'CI': '1'})]:
            with self.assertRaises(ValueError):
                runner.guard(mode, env)
        runner.guard(True, {'SDK_REAL_AGENT_E2E': '1'})
        states = runner.hook_states(pathlib.Path('/synthetic'), {'hooks': {'PreToolUse': [{'hooks': [{'type': 'command'}]}]}})
        self.assertEqual(states, {'/synthetic/config.toml:pre_tool_use:0:0': {'enabled': False}})

    def test_secret_canaries(self):
        for value in [{'authorization': 'x'}, {'headers': {}}, {'text': 'sk-canary012345678901234'}, {'text': '/Users/canary/private'}, {'text': 'user@example.invalid'}, {'text': 'Bearer canaryABC'}]:
            with self.subTest(value=value), self.assertRaises(ValueError):
                sanitize.scan(value)

    def test_unknown_nested_fields_rejected(self):
        schema = {'type': 'object', 'properties': {'known': {'type': 'string'}}}
        with self.assertRaises(ValueError):
            sanitize.validate({'known': 'ok', 'futureSecret': 'x'}, schema)

    def test_mapping_preserves_type_namespace_and_references(self):
        s = sanitize.Sanitizer('/private/synthetic', '/private/account')
        self.assertNotEqual(s.ident('rpc-client', 7, 1), s.ident('rpc-client', '7', 2))
        self.assertEqual(s.ident('item', 'original-item', 1), s.ident('item', 'original-item', 3))
        self.assertNotEqual(s.ident('thread', 'same', 1), s.ident('turn', 'same', 2))
        v = {'thread': {'id': 'thread-native'}, 'threadId': 'thread-native', 'itemId': 'item-native', 'callId': 'item-native'}
        s.discover(v, 1)
        clean = s.rewrite(v, 1)
        self.assertEqual(clean['thread']['id'], clean['threadId'])
        self.assertEqual(clean['itemId'], clean['callId'])
        self.assertNotIn('native', str(clean))

    def test_hook_payload_excluded_before_scan_or_publication(self):
        f = {'ordinal': 1, 'direction': 's2c', 'wire': {'method': 'hook/completed', 'params': {'private': 'sk-neverpublish0123456789'}}}
        out, omitted, relations = sanitize.transform([f], '/fixture', '/private')
        self.assertEqual(out, [])
        self.assertEqual(len(omitted), 1)
        self.assertNotIn('private', str(omitted))

    def test_quarantine_does_not_publish(self):
        with tempfile.TemporaryDirectory() as root:
            root = pathlib.Path(root)
            raw = root/'raw'; raw.mkdir()
            (raw/'raw.jsonl').write_text('{"ordinal":1,"direction":"s2c","wire":{"method":"unknown/method","params":{}}}\n')
            with self.assertRaises(ValueError):
                sanitize.publish(raw, root/'public', 's1', {'root': '/synthetic', 'home': '/private'})
            self.assertFalse((root/'public').exists())


if __name__ == '__main__':
    unittest.main()
