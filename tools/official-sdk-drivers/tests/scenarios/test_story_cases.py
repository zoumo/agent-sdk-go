"""Story composition and case identity are independent of driver selection."""
from copy import deepcopy
import json
from pathlib import Path
import shutil
import tempfile
import unittest
from types import SimpleNamespace
from unittest.mock import patch

from support import BOOTSTRAP
from record_user_story.scenarios import case_recipes, recipe, scenario_directory, story_documents, summarize_story
from record_user_story.policy import driver_profile
from record_user_story.cli import execute


class StoryCaseTests(unittest.TestCase):
    def test_case_identity_survives_story_text_and_case_order_changes(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp) / 'scenarios'
            shutil.copytree(scenario_directory(), directory)
            before = recipe('codex', 'SDK-US-001', directory, case_id='SDK-CASE-001')
            path = directory / 'SDK-US-001.json'
            document = json.loads(path.read_text())
            document['goal'] = 'Reworded complete user story'
            document['cases'].reverse()
            path.write_text(json.dumps(document))
            after = recipe('codex', 'SDK-US-001', directory, case_id='SDK-CASE-001')
            self.assertEqual((before['storyId'], before['caseId']), (after['storyId'], after['caseId']))
            self.assertEqual(before['prompts'], after['prompts'])
            self.assertEqual(before['driverProfile'], after['driverProfile'])

    def test_frozen_story_case_empty_intersection_never_launches_a_worker(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'suite.json').write_text('{}')
            case = root / 'SDK-US-002' / 'SDK-CASE-004'
            case.mkdir(parents=True)
            (case / 'plan.json').write_text(json.dumps({'version': 4, 'story': 'SDK-US-002',
                                                       'storyId': 'SDK-US-002', 'caseId': 'SDK-CASE-004'}))
            args = SimpleNamespace(output=str(root), action='record', stories='SDK-US-001', cases='SDK-CASE-004')
            with patch('subprocess.Popen', side_effect=AssertionError('worker launched')):
                with self.assertRaisesRegex(ValueError, 'no intersection'):
                    execute(args)

    def test_worker_rejects_old_or_incomplete_plans_before_sdk_access(self):
        from record_user_story.cli import main
        for version in (2, 3, 4):
            with self.subTest(version=version), tempfile.TemporaryDirectory() as tmp:
                path = Path(tmp) / 'plan.json'
                path.write_text(json.dumps({'version': version, 'provider': 'codex', 'story': 'SDK-US-001',
                                            'root': tmp, 'childCase': True}))
                with patch('record_user_story.drivers.sdk_identity', side_effect=AssertionError('SDK accessed')):
                    self.assertEqual(main(['worker', '--plan', str(path)]), 1)
                diagnostic = json.loads((Path(tmp) / 'worker-startup-diagnostics.private.json').read_text())
                self.assertEqual(diagnostic['type'], 'ValueError')
                self.assertIn('current simulation plan', diagnostic['error'])

    def test_unavailable_routes_cannot_be_promoted_by_forged_pass_reports(self):
        for provider in ('codex', 'claude'):
            checked = 0
            for story in story_documents():
                cases = case_recipes(provider, stories=story['id'])
                unavailable = [c['caseId'] for c in cases if c['mode'] != 'live-selected-path']
                if not unavailable:
                    continue
                results = {case['caseId']: {'status': 'passed', 'evidenceLayer': 'protocol-replay'} for case in cases}
                summary = summarize_story(provider, story['id'], results, 'protocol-replay')
                self.assertEqual(summary['status'], 'partial')
                for case_id in unavailable:
                    self.assertIn(case_id, summary['missingCases'])
                    checked += 1
            self.assertGreater(checked, 0)

    def test_catalog_retains_stable_stories_and_all_small_validation_targets(self):
        stories = story_documents()
        self.assertEqual([item['id'] for item in stories], [f'SDK-US-{i:03}' for i in range(1, 9)])
        cases = [case for story in stories for case in story['cases']]
        self.assertEqual(len(cases), 36)
        self.assertEqual(sum(case['required'] for case in cases), 36)
        self.assertEqual(len({case['id'] for case in cases}), len(cases))
        self.assertTrue(all(len(story['cases']) > 1 for story in stories))
        for provider in ('codex', 'claude'):
            selected = case_recipes(provider, stories='SDK-US-001')
            self.assertEqual([case['caseId'] for case in selected], ['SDK-CASE-001', 'SDK-CASE-002', 'SDK-CASE-003', 'SDK-CASE-004', 'SDK-CASE-005'])
            self.assertEqual(selected[1]['mode'], 'live-selected-path')
            self.assertNotIn('offlineTest', selected[1])
            with self.assertRaises(ValueError):
                case_recipes(provider, stories='SDK-US-002', cases='SDK-CASE-001')

    def test_small_case_success_cannot_pass_whole_story_or_mix_evidence_layers(self):
        results = {'SDK-CASE-001': {'status': 'passed', 'evidenceLayer': 'protocol-replay'}}
        summary = summarize_story('claude', 'SDK-US-001', results, 'protocol-replay')
        self.assertEqual(summary['status'], 'partial')
        self.assertEqual(summary['missingCases'], ['SDK-CASE-002', 'SDK-CASE-003', 'SDK-CASE-004', 'SDK-CASE-005'])
        results.update({key: {'status': 'passed', 'evidenceLayer': 'go-sdk'} for key in summary['missingCases']})
        self.assertEqual(summarize_story('claude', 'SDK-US-001', results, 'protocol-replay')['status'], 'partial')
        for result in results.values():
            result['evidenceLayer'] = 'go-sdk'
        self.assertEqual(summarize_story('claude', 'SDK-US-001', results, 'go-sdk')['status'], 'passed')
        results['SDK-CASE-002']['status'] = 'failed'
        self.assertEqual(summarize_story('claude', 'SDK-US-001', results, 'go-sdk')['status'], 'failed')

    def test_multiple_executable_cases_in_one_story_do_not_select_a_default(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp) / 'scenarios'
            shutil.copytree(scenario_directory(), directory)
            path = directory / 'SDK-US-001.json'
            document = json.loads(path.read_text())
            additional = deepcopy(document['cases'][0])
            additional['id'] = 'SDK-CASE-200'
            additional['inputs']['input-1'] = 'Different case input.'
            document['cases'].append(additional)
            path.write_text(json.dumps(document))
            binding = directory / 'bindings/codex.json'
            data = json.loads(binding.read_text())
            data['cases']['SDK-CASE-200'] = deepcopy(data['cases']['SDK-CASE-001'])
            binding.write_text(json.dumps(data))
            with self.assertRaisesRegex(ValueError, 'explicit case'):
                recipe('codex', 'SDK-US-001', directory)
            selected = case_recipes('codex', stories='SDK-US-001', cases='SDK-CASE-001,SDK-CASE-200', directory=directory)
            self.assertEqual(len(selected), 2)
            self.assertNotEqual(selected[0]['prompts'], selected[1]['prompts'])

    def test_driver_profile_does_not_depend_on_story_or_case_numbers(self):
        self.assertEqual(driver_profile({'version': 4, 'story': 'SDK-US-999', 'caseId': 'SDK-CASE-555',
                                       'driverProfile': 'continuation'}), 'continuation')
        with self.assertRaisesRegex(ValueError, 'explicit driver profile'):
            driver_profile({'version': 4, 'story': 'SDK-US-001', 'caseId': 'SDK-CASE-001'})

    def test_duplicate_case_identity_and_wrong_owner_are_rejected(self):
        for duplicate in (True, False):
            with self.subTest(duplicate=duplicate), tempfile.TemporaryDirectory() as tmp:
                directory = Path(tmp) / 'scenarios'
                shutil.copytree(scenario_directory(), directory)
                path = directory / 'SDK-US-001.json'
                document = json.loads(path.read_text())
                if duplicate:
                    document['cases'].append(deepcopy(document['cases'][0]))
                else:
                    document['cases'][0]['storyId'] = 'SDK-US-002'
                path.write_text(json.dumps(document))
                with self.assertRaises(ValueError):
                    story_documents(directory)


if __name__ == '__main__':
    unittest.main()
