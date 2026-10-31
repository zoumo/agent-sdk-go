"""Validate actual Go Receive comparison without fabricating a filtered stream."""
import copy
import importlib.util
import json
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('sdk_replay', Path(__file__).resolve().parents[4] / 'scripts/replay-sdk-recording.py')
sdk_replay = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sdk_replay)


def codex_case():
    confirmation = {'kind': 'observation', 'observation': {'Reason': 'member-confirmed', 'ThreadID': 'child'}}
    def event(method, params):
        return {'kind': 'event', 'origin': 'live', 'source': {'ThreadID': params['threadId']},
                'raw': json.dumps({'method': method, 'params': params, 'emittedAtMs': 1})}
    detail = event('item/agentMessage/delta', {'threadId': 'child', 'turnId': 'turn', 'itemId': 'item', 'delta': 'text'})
    lifecycle = event('turn/completed', {'threadId': 'child', 'turn': {'id': 'turn', 'status': 'completed'}})
    terminal = event('turn/completed', {'threadId': 'root', 'turn': {'id': 'main', 'status': 'completed'}})
    return [confirmation, detail, lifecycle, terminal], [confirmation, lifecycle, terminal], {'rootId': 'root', 'childIds': ['child']}


class DeliveryComparisonTests(unittest.TestCase):
    def test_child_acceptance_requires_actual_relations_completions_and_background_order(self):
        before, _, _ = codex_case()
        member, _, child, root = before
        parent = {'kind': 'observation', 'observation': {
            'Reason': 'parent-confirmed', 'ThreadID': 'child', 'ParentThreadID': 'root'}}
        received = [member, parent, root, child]
        frames = [{'direction': 's2c', 'wire': json.loads(child['raw'])}]
        original = {'childEvidence': [{'childThreadId': 'child', 'parentThreadId': 'root'}]}
        result = sdk_replay.validate_child_delivery('codex', frames, original, received, True)
        self.assertEqual(result['confirmedChildren'], 1)
        for changed in ([member, root, child], [member, parent, root], [member, parent, child, root]):
            with self.subTest(changed=changed), self.assertRaises(ValueError):
                sdk_replay.validate_child_delivery('codex', frames, original, changed, True)

    def test_actual_policy_difference_retains_all_other_facts(self):
        before, after, selection = codex_case()
        result = sdk_replay.compare_delivery('codex', before, after, selection)
        self.assertEqual(result['removedPureChildDetails'], 1)
        self.assertTrue(result['retainedFactsUnchanged'])

    def test_no_difference_is_not_a_delivery_pass(self):
        before, _, selection = codex_case()
        with self.assertRaisesRegex(ValueError, 'did not remove'):
            sdk_replay.compare_delivery('codex', before, before, selection)

    def test_lost_lifecycle_changed_source_or_reordered_fact_fails(self):
        for kind in ['lifecycle', 'source', 'reorder', 'raw']:
            before, after, selection = codex_case()
            after = copy.deepcopy(after)
            if kind == 'lifecycle': del after[1]
            if kind == 'source': after[1]['source']['ThreadID'] = 'other'
            if kind == 'reorder': after[0], after[1] = after[1], after[0]
            if kind == 'raw': after[1]['raw'] += ' '
            with self.subTest(kind=kind), self.assertRaises(ValueError):
                sdk_replay.compare_delivery('codex', before, after, selection)

    def test_unknown_extension_and_unconfirmed_child_cannot_be_dropped(self):
        for kind in ['extension', 'unconfirmed']:
            before, after, selection = codex_case()
            if kind == 'extension':
                wire = json.loads(before[1]['raw']); wire['newFact'] = True
                before[1]['raw'] = json.dumps(wire)
            else:
                selection['childIds'] = ['different']
            with self.subTest(kind=kind), self.assertRaises(ValueError):
                sdk_replay.compare_delivery('codex', before, after, selection)

    def test_claude_confirmed_pure_message_only(self):
        confirmation = {'kind': 'observation', 'observation': {'Reason': 'member-confirmed', 'Source': {'association_id': 'task:child'}}}
        source = {'association_id': 'task:child', 'tool_use_id': 'call'}
        wire = {'type': 'assistant', 'session_id': 'root', 'uuid': 'message', 'parent_tool_use_id': 'call',
                'message': {'role': 'assistant', 'content': [{'type': 'text', 'text': 'child'}], 'context_management': None},
                'timestamp': '2026-10-29T12:00:00Z', 'subagent_type': 'general-purpose', 'task_description': 'reply'}
        detail = {'kind': 'event', 'origin': 'live', 'source': source, 'raw': json.dumps(wire)}
        terminal = {'kind': 'event', 'origin': 'live', 'raw': '{"type":"result","session_id":"root","is_error":false}'}
        selection = {'rootId': 'root', 'childIds': ['agent']}
        for context in [{}, [], '', 'context', True, False, 0, {'edits': []}]:
            candidate = copy.deepcopy(detail)
            changed = copy.deepcopy(wire)
            changed['message']['context_management'] = context
            candidate['raw'] = json.dumps(changed)
            with self.subTest(context=context), self.assertRaisesRegex(ValueError, 'context-management'):
                sdk_replay.compare_delivery('claude', [confirmation, candidate, terminal], [confirmation, terminal], selection)
        for mixed in [False, True]:
            candidate = copy.deepcopy(detail)
            if mixed:
                wire['message']['content'].append({'type': 'tool_use', 'id': 'tool', 'name': 'Read', 'input': {}})
                candidate['raw'] = json.dumps(wire)
                with self.assertRaises(ValueError):
                    sdk_replay.compare_delivery('claude', [confirmation, candidate, terminal], [confirmation, terminal], selection)
            else:
                self.assertEqual(sdk_replay.compare_delivery('claude', [confirmation, candidate, terminal], [confirmation, terminal], selection)['removedPureChildDetails'], 1)
