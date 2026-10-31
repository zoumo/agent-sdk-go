"""Synthetic admission tests; these files are never recording evidence."""
import copy
import importlib.util
import json
import re
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


SPEC = importlib.util.spec_from_file_location('simulation_corpus', Path(__file__).with_name('simulation-corpus.py'))


class CorpusTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.corpus = importlib.util.module_from_spec(SPEC)
        SPEC.loader.exec_module(cls.corpus)

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.source = self.root / 'capture'
        self.replay = self.root / 'replay'
        self.source.mkdir()
        self.replay.mkdir()

    def write(self, root, name, value):
        path = root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(value) + '\n')

    def empty_index(self):
        index = self.corpus.load_index(self.corpus.DEFAULT_INDEX)
        index['cases'] = [{**{k: entry[k] for k in ('provider', 'caseId', 'storyId', 'requirement')},
                           'adoption': 'missing'} for entry in index['cases']]
        self.write(self.root, 'corpus/index.json', index)
        return self.root / 'corpus/index.json'

    def test_fixed_coverage_is_not_discovered_from_available_recordings(self):
        index = self.corpus.load_index(self.corpus.DEFAULT_INDEX)
        self.assertEqual(72, len(index['cases']))
        self.assertEqual(67, sum(x['requirement'] == 'required' for x in index['cases']))
        self.assertEqual(2, sum(x['requirement'] == 'planned' for x in index['cases']))
        self.assertEqual(3, sum(x['requirement'] == 'unsupported' for x in index['cases']))
        for change in ('empty', 'omit', 'downgrade', 'duplicate'):
            with self.subTest(change=change):
                bad = copy.deepcopy(index)
                if change == 'empty':
                    bad['cases'] = []
                elif change == 'omit':
                    bad['cases'].pop()
                elif change == 'downgrade':
                    bad['cases'][0]['requirement'] = 'planned'
                else:
                    bad['cases'].append(bad['cases'][0])
                path = self.root / 'index.json'
                path.write_text(json.dumps(bad))
                with self.assertRaises(ValueError):
                    self.corpus.load_index(path)

    def test_inventory_rejects_symlinks_and_pins_all_private_originals(self):
        self.write(self.source, 'unselected.private.json', {'diagnostic': 'retain'})
        self.write(self.source, 'plan.json', {'caseId': 'SDK-CASE-001'})
        files = self.corpus.inventory(self.source)
        self.assertEqual({'plan.json', 'unselected.private.json'}, set(files))
        (self.source / 'linked').symlink_to(self.source / 'plan.json')
        with self.assertRaises(ValueError):
            self.corpus.inventory(self.source)

    def test_public_review_entry_never_approves_its_generated_candidate(self):
        self.evidence()
        for name in ('config/projects', 'config/sessions', 'history-seed'):
            (self.source / name / 'empty').mkdir(parents=True)
        candidate = self.root / 'candidate.json'
        result = subprocess.run(['bash', str(self.corpus.ROOT / 'scripts/simulation.sh'), 'review',
                                 '--source', str(self.source), '--replay', str(self.replay),
                                 '--review', str(candidate)], capture_output=True, text=True)
        self.assertEqual(0, result.returncode, result.stderr)
        review = self.corpus.read(candidate)
        self.assertEqual('unreviewed', review['origin'])
        self.assertFalse(review['sourceReviewed'])
        with self.assertRaises(ValueError):
            self.corpus.validate_admission(self.source, self.replay, review)

    def test_optional_history_inventory_preserves_files_and_rejects_links(self):
        self.evidence()
        plan = self.corpus.source_plan(self.source)
        history = 'config/projects/project/session.jsonl'
        self.write(self.source, history, {'cwd': '/original/project'})
        (self.source / 'config/sessions').mkdir()
        selected = self.corpus.required_materials(self.source, plan)
        self.assertIn(history, selected)
        (self.source / 'config/sessions/linked').symlink_to(self.source / history)
        with self.assertRaises(ValueError):
            self.corpus.required_materials(self.source, plan)

    def test_selected_paths_cannot_escape_or_include_credentials(self):
        for path in ('../secret', '/absolute', 'config/auth.json', 'native.env.private.json', 'config/settings.json', 'stdout.log'):
            with self.subTest(path=path), self.assertRaises(ValueError):
                self.corpus.validate_publish_path(path)
        for path in ('plan.json', 'raw/raw.jsonl', 'config/projects/key/session.jsonl', 'history-seed/config/sessions/old.jsonl'):
            self.corpus.validate_publish_path(path)

    def evidence(self):
        # Fabricated metadata exercises validation only, never the real index.
        plan = {'provider': 'codex', 'caseId': 'SDK-CASE-001', 'storyId': 'SDK-US-001',
                'driverProfile': 'continuation', 'project': '/original/project',
                'scenario': {'files': {'numbers.txt': '7\n11\n'}}}
        self.write(self.source, 'plan.json', plan)
        self.write(self.source, 'raw/raw.jsonl', {'ordinal': 1, 'direction': 'c2s', 'channel': 'protocol',
                   'epoch': 1, 'elapsedMs': 0, 'wire': {'id': 1, 'method': 'ping'}})
        (self.source / 'raw/stderr.raw').write_bytes(b'')
        self.write(self.source, 'raw/transport.json', {'captureStatus': 'complete', 'cleanupComplete': True,
                   'nativeLaunched': True, 'reaped': True, 'clientEOF': True, 'frames': 1})
        rawfiles = self.corpus.inventory(self.source / 'raw')
        self.write(self.source, 'raw/manifest.json', {'captureStatus': 'complete', 'files': rawfiles,
                   'native': {'executable': '/observed/native', 'sha256': 'a' * 64, 'cwd': '/original/project'},
                   'recordingFormat': {'name': 'agent-mock-jsonl', 'version': 1, 'transport': 'stdio-jsonl'}})
        rawhash = self.corpus.digest(self.source / 'raw/raw.jsonl')
        sdk = {'package': 'openai-codex', 'version': 'test-only'}
        self.write(self.source, 'result.json', {'status': 'passed'})
        business = {'status': 'passed', 'branchStatus': 'passed', 'captureStatus': 'complete',
                    'provider': 'codex', 'caseId': 'SDK-CASE-001', 'storyId': 'SDK-US-001', 'officialSDK': sdk}
        self.write(self.source, 'report.json', business)
        self.write(self.replay, 'replay-report.json', business)
        self.write(self.source, 'manifest.json', {'provenance': 'capture', 'status': 'passed',
                   'captureStatus': 'complete', 'credentialScan': 'pass',
                   'planSHA256': self.corpus.digest(self.source / 'plan.json'),
                   'files': self.corpus.inventory(self.source / 'raw')})
        summary = {'provider': 'codex', 'case': 'SDK-CASE-001', 'mode': 'record',
                   'result': {'status': 'passed'}, 'recordingSHA256': rawhash}
        self.write(self.source, 'summary.json', summary)
        summary['mode'] = 'replay'
        summary['source'] = {name: self.corpus.digest(self.source / name) for name in self.corpus.SOURCE_EVIDENCE}
        self.write(self.replay, 'summary.json', summary)
        self.write(self.replay, 'result.json', {'status': 'passed'})
        self.write(self.replay, 'replay-peer.json', {'provider': 'codex', 'recordingSHA256': rawhash,
                   'fullConsumption': True, 'clientEOF': True, 'nativeLaunched': False, 'frames': 1})
        review = {'version': 1, 'origin': 'real-agent', 'reviewer': 'test reviewer',
                  'reviewedAt': '2026-10-31T00:00:00Z', 'sourceReviewed': True,
                  'contentReviewed': True, 'credentialsReviewed': True,
                  'officialSDK': sdk, 'captureFiles': self.corpus.inventory(self.source),
                  'replayFiles': self.corpus.inventory(self.replay),
                  'publishFiles': sorted(self.corpus.inventory(self.source))}
        return review

    def test_admission_requires_business_capture_replay_and_frozen_review(self):
        review = self.evidence()
        self.corpus.validate_admission(self.source, self.replay, review)
        for field, value in (('origin', 'synthetic'), ('sourceReviewed', False), ('credentialsReviewed', False),
                             ('captureFiles', {}), ('officialSDK', ''), ('reviewer', '')):
            bad = copy.deepcopy(review)
            bad[field] = value
            with self.subTest(field=field), self.assertRaises(ValueError):
                self.corpus.validate_admission(self.source, self.replay, bad)
        for name, field, value in (
                ('summary.json', 'mode', 'simulate'),
                ('summary.json', 'source', {}),
                ('result.json', 'status', 'untriggered'),
                ('replay-peer.json', 'fullConsumption', False),
                ('replay-peer.json', 'clientEOF', False),
                ('replay-peer.json', 'nativeLaunched', True),
                ('replay-peer.json', 'recordingSHA256', 'f' * 64)):
            path = self.replay / name
            original = path.read_bytes()
            changed = json.loads(original)
            changed[field] = value
            path.write_text(json.dumps(changed))
            bad = copy.deepcopy(review)
            bad['replayFiles'] = self.corpus.inventory(self.replay)
            with self.subTest(file=name, field=field), self.assertRaises(ValueError):
                self.corpus.validate_admission(self.source, self.replay, bad)
            path.write_bytes(original)
        with (self.source / 'raw/raw.jsonl').open('ab') as stream:
            stream.write(b'\n')
        with self.assertRaises(ValueError):
            self.corpus.validate_admission(self.source, self.replay, review)

    def test_adoption_preserves_complete_original_and_reuses_without_source(self):
        review = self.evidence()
        self.write(self.source, 'diagnostics.private.json', {'retain': 'private original'})
        review['captureFiles'] = self.corpus.inventory(self.source)
        review_path = self.root / 'review.json'
        self.write(self.root, 'review.json', review)
        index = self.empty_index()
        archive = self.root / 'archive'
        cli = Path(__file__).with_name('simulation-corpus.py')
        result = subprocess.run([sys.executable, str(cli), 'adopt', '--source', str(self.source),
                                 '--replay', str(self.replay), '--review', str(review_path),
                                 '--index', str(index), '--archive', str(archive)], capture_output=True, text=True)
        self.assertEqual(0, result.returncode, result.stderr)
        adopted = Path(result.stdout.strip())
        self.assertFalse((adopted / 'diagnostics.private.json').exists())
        self.assertEqual(1, len(list(archive.glob('*/capture/diagnostics.private.json'))))
        shutil.rmtree(self.source)
        shutil.rmtree(self.replay)
        entry = next(e for e in self.corpus.load_index(index)['cases'] if e.get('adoption') == 'admitted')
        self.assertEqual(adopted, self.corpus.verify_adopted(index, entry))
        self.assertEqual('/original/project', self.corpus.source_plan(adopted)['project'])
        (adopted / 'raw/raw.jsonl').write_text('changed')
        with self.assertRaises(ValueError):
            self.corpus.verify_adopted(index, entry)

    def test_failed_adoption_archives_originals_without_index_changes(self):
        review = self.evidence()
        review['origin'] = 'synthetic'
        self.write(self.root, 'review.json', review)
        index = self.empty_index()
        before = index.read_bytes()
        with self.assertRaises(ValueError):
            self.corpus.adopt(self.source, self.replay, self.root / 'review.json', index, self.root / 'archive')
        self.assertEqual(before, index.read_bytes())
        saved = list((self.root / 'archive').glob('*/adoption-result.json'))
        self.assertEqual(1, len(saved))
        self.assertEqual('rejected', json.loads(saved[0].read_text())['status'])
        self.assertEqual(self.corpus.inventory(self.source), self.corpus.inventory(saved[0].parent / 'capture'))

    def test_source_failures_cannot_be_reviewed_into_success(self):
        for name, field, value in (('manifest.json', 'provenance', 'synthetic'),
                                   ('result.json', 'status', 'untriggered'),
                                   ('raw/transport.json', 'cleanupComplete', False),
                                   ('report.json', 'branchStatus', 'untriggered'),
                                   ('report.json', 'gaps', ['missing target behavior'])):
            with self.subTest(name=name):
                review = self.evidence()
                changed = self.corpus.read(self.source / name)
                changed[field] = value
                self.write(self.source, name, changed)
                review['captureFiles'] = self.corpus.inventory(self.source)
                with self.assertRaises(ValueError):
                    self.corpus.validate_admission(self.source, self.replay, review)

    def test_claude_runtime_derivation_requires_source_and_output_hashes(self):
        self.evidence()
        plan = self.corpus.source_plan(self.source)
        plan['provider'] = 'claude'
        self.write(self.source, 'plan.json', plan)
        name = 'config/projects/-original-project/session.jsonl'
        self.write(self.source, name, {'cwd': '/original/project', 'id': 'unchanged'})
        execution = '/gone-replay/project'
        target = 'config/projects/' + re.sub('[^a-zA-Z0-9]', '-', execution) + '/session.jsonl'
        self.write(self.replay, target, {'cwd': execution, 'id': 'unchanged'})
        self.write(self.replay, 'materialization.json', {'version': 1, 'originalProject': '/original/project',
                   'originalHistoryProject': None, 'executionProject': execution, 'transformations': [{
                       'kind': 'claude-project-cwd-copy', 'source': name, 'derived': target,
                       'sourceSHA256': self.corpus.digest(self.source / name),
                       'derivedSHA256': self.corpus.digest(self.replay / target),
                       'sourceCWD': '/original/project', 'executionCWD': execution}]})
        summary = self.corpus.read(self.replay / 'summary.json')
        summary.update(provider='claude', output='/gone-replay',
                       source={n: self.corpus.digest(self.source / n) for n in self.corpus.SOURCE_EVIDENCE},
                       materializationSHA256=self.corpus.digest(self.replay / 'materialization.json'))
        self.write(self.replay, 'summary.json', summary)
        receipt = self.corpus.read(self.replay / 'replay-peer.json')
        receipt['provider'] = 'claude'
        self.write(self.replay, 'replay-peer.json', receipt)
        self.corpus.validate_replay(self.source, self.replay, plan)
        self.write(self.replay, target, {'cwd': execution, 'id': 'changed'})
        with self.assertRaises(ValueError):
            self.corpus.validate_replay(self.source, self.replay, plan)

    def test_adopted_materials_execute_current_public_go_sdk_and_retain_failure(self):
        review = self.evidence()
        fixture = self.corpus.ROOT / 'codex/tests/testdata/integration/lifecycle.jsonl'
        frames = [json.loads(line) for line in fixture.read_bytes().splitlines()][:5]
        for direction, wire in (
                ('c2s', {'id': 103, 'method': 'turn/start', 'params': {'threadId': 'thread-001',
                         'input': [{'type': 'text', 'text': 'synthetic task'}]}}),
                ('s2c', {'id': 103, 'result': {'turn': {'id': 'turn-001', 'items': [], 'status': 'inProgress'}}}),
                ('s2c', {'method': 'turn/completed', 'params': {'threadId': 'thread-001',
                         'turn': {'id': 'turn-001', 'items': [], 'status': 'completed'}}})):
            frames.append({'ordinal': len(frames) + 1, 'direction': direction, 'wire': wire,
                           'epoch': 1, 'elapsedMs': 0, 'channel': 'protocol'})
        (self.source / 'raw/raw.jsonl').write_text(''.join(json.dumps(f) + '\n' for f in frames))
        plan = self.corpus.source_plan(self.source)
        plan.update(caseId='SDK-CASE-002', driverProfile='structured-input')
        self.write(self.source, 'plan.json', plan)
        transport = self.corpus.read(self.source / 'raw/transport.json')
        transport['frames'] = len(frames)
        self.write(self.source, 'raw/transport.json', transport)
        manifest = self.corpus.read(self.source / 'raw/manifest.json')
        manifest['files'] = {n: self.corpus.digest(self.source / 'raw' / n)
                             for n in ('raw.jsonl', 'stderr.raw', 'transport.json')}
        self.write(self.source, 'raw/manifest.json', manifest)
        manifest = self.corpus.read(self.source / 'manifest.json')
        manifest.update(planSHA256=self.corpus.digest(self.source / 'plan.json'),
                        files=self.corpus.inventory(self.source / 'raw'))
        self.write(self.source, 'manifest.json', manifest)
        rawhash = self.corpus.digest(self.source / 'raw/raw.jsonl')
        for root, mode, business_name in ((self.source, 'record', 'report.json'), (self.replay, 'replay', 'replay-report.json')):
            summary = self.corpus.read(root / 'summary.json')
            summary.update(case='SDK-CASE-002', recordingSHA256=rawhash)
            if mode == 'replay':
                summary['source'] = {n: self.corpus.digest(self.source / n) for n in self.corpus.SOURCE_EVIDENCE}
            self.write(root, 'summary.json', summary)
            business = self.corpus.read(root / business_name)
            business['caseId'] = 'SDK-CASE-002'
            self.write(root, business_name, business)
        summary = self.corpus.read(self.replay / 'summary.json')
        summary['source'] = {n: self.corpus.digest(self.source / n) for n in self.corpus.SOURCE_EVIDENCE}
        self.write(self.replay, 'summary.json', summary)
        receipt = self.corpus.read(self.replay / 'replay-peer.json')
        receipt.update(recordingSHA256=rawhash, frames=len(frames))
        self.write(self.replay, 'replay-peer.json', receipt)
        review.update(captureFiles=self.corpus.inventory(self.source), replayFiles=self.corpus.inventory(self.replay))
        self.write(self.root, 'review.json', review)
        index = self.empty_index()
        adopted = self.corpus.adopt(self.source, self.replay, self.root / 'review.json', index, self.root / 'archive')
        shutil.rmtree(self.source)
        shutil.rmtree(self.replay)
        binaries = self.root / 'bin'
        binaries.mkdir()
        for name, package in (('agent-sdk-testify', './tools/agent-sdk-testify'), ('sdk-driver', './tests/simulation/drivers')):
            subprocess.run(['mise', 'exec', '--', 'go', 'build', '-o', str(binaries / name), package],
                           cwd=self.corpus.ROOT, check=True, capture_output=True)
        output = self.root / 'simulation'
        result = subprocess.run([str(binaries / 'agent-sdk-testify'), 'simulate', '--materials', str(adopted),
                                 '--driver', str(binaries / 'sdk-driver'), '--output', str(output)], capture_output=True, text=True)
        self.assertEqual(0, result.returncode, result.stderr)
        self.corpus.validate_replay(adopted, output, plan, 'simulate')
        self.assertEqual('7\n11\n', (output / 'project/numbers.txt').read_text())
        # A current SDK/driver failure must not demote or remove admitted data.
        before_index = index.read_bytes()
        before_corpus = self.corpus.inventory(adopted)
        result = subprocess.run([str(binaries / 'agent-sdk-testify'), 'simulate', '--materials', str(adopted),
                                 '--driver', '/usr/bin/false', '--output', str(self.root / 'failed')], capture_output=True)
        self.assertNotEqual(0, result.returncode)
        self.assertEqual(before_index, index.read_bytes())
        self.assertEqual(before_corpus, self.corpus.inventory(adopted))
        self.assertTrue((self.root / 'failed/summary.json').exists())
        # A successful available subset is still an incomplete fixed gate.
        self.assertFalse(self.corpus.gate(index, self.root / 'gate'))
        coverage = self.corpus.read(self.root / 'gate/coverage.json')
        self.assertEqual({'passed': 1, 'missing': 66, 'planned': 2, 'unsupported': 3}, coverage['counts'])
        self.assertEqual(before_index, index.read_bytes())
        self.assertEqual(before_corpus, self.corpus.inventory(adopted))


if __name__ == '__main__':
    unittest.main()
