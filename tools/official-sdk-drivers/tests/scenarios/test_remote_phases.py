import json
from pathlib import Path
import tempfile
import unittest

from codex_driver.remote import phase_files, verify_phase_files, REQUIRED_PHASE_FILES


class RemotePhaseTests(unittest.TestCase):
    def test_probe_raw_and_service_identity_are_immutable(self):
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            raw = root / 'remote-probe/raw'
            raw.mkdir(parents=True)
            (raw / 'raw.jsonl').write_text('{"native":9007199254740993}\n')
            for name in ('manifest.json', 'transport.json', 'stderr.raw'):
                (raw / name).write_text('{}')
            (root / 'remote-service.json').write_text(json.dumps({'serverPID': 42, 'probeSucceeded': True}))
            files = phase_files(root)
            self.assertEqual(set(files), REQUIRED_PHASE_FILES)
            verify_phase_files(root, files)
            (raw / 'raw.jsonl').write_text('{"native":9007199254740992}\n')
            with self.assertRaises(ValueError):
                verify_phase_files(root, files)

    def test_missing_phase_cannot_verify_as_empty_success(self):
        with tempfile.TemporaryDirectory() as name:
            with self.assertRaises(ValueError):
                phase_files(Path(name))
            with self.assertRaises(ValueError):
                verify_phase_files(Path(name), {})

    def test_rejects_escape_and_symlink(self):
        with tempfile.TemporaryDirectory() as name:
            root = Path(name)
            for relative in ('../outside', '/absolute', 'remote-probe/../../outside', 'remote-probe/other/raw.jsonl'):
                with self.subTest(relative=relative), self.assertRaises(ValueError):
                    verify_phase_files(root, {relative: 'irrelevant'})
            source = root / 'source'
            source.write_text('{}')
            (root / 'remote-service.json').symlink_to(source)
            with self.assertRaises(ValueError):
                verify_phase_files(root, {'remote-service.json': 'irrelevant'})
