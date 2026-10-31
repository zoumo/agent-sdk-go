#!/usr/bin/env python3
"""Verify a private recording, run its Go SDK adapter, verify full consumption."""
import argparse
import hashlib
import json
from pathlib import Path
import shlex
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'tools/official-sdk-drivers/common'))
from record_user_story.environment import verify_recording
from record_user_story.peer import planned_identity, verify_receipt


def compare_delivery(provider, preserved, suppressed, selection):
    """Check actual Go Receive output; never project a Python replacement stream."""
    position, removed = 0, []
    for value in preserved:
        if position < len(suppressed) and value == suppressed[position]:
            position += 1
        else:
            removed.append(value)
    if position != len(suppressed):
        raise ValueError('suppression changed, reordered or invented a retained Go fact')
    if not removed:
        raise ValueError('child suppression did not remove any confirmed pure detail')
    root, children = selection['rootId'], selection['childIds']
    confirmed = set()
    for value in preserved:
        observation = value.get('observation', {})
        if observation.get('Reason') == 'member-confirmed':
            identity = observation.get('ThreadID') or observation.get('Source', {}).get('association_id')
            if identity:
                confirmed.add(identity)
    for value in removed:
        if value.get('kind') != 'event' or value.get('origin') != 'live' or not value.get('raw'):
            raise ValueError('suppression removed a request, observation or non-live fact')
        native, source = json.loads(value['raw']), value['source']
        if provider == 'codex':
            child = source.get('ThreadID')
            params = native.get('params', {})
            # This recipe produces child text. Lifecycle, complete items and any
            # mixed extension must survive byte-for-byte, including their source.
            if (child not in children or child not in confirmed or child == root
                    or native.get('method') != 'item/agentMessage/delta'
                    or set(native) - {'method', 'params', 'emittedAtMs'}
                    or set(params) != {'threadId', 'turnId', 'itemId', 'delta'}
                    or params['threadId'] != child or not isinstance(params['delta'], str)):
                raise ValueError('suppression removed a protected or unconfirmed Codex fact')
        else:
            if (not source.get('association_id') or source['association_id'] not in confirmed
                    or native.get('session_id') != root or not native.get('parent_tool_use_id')
                    or source.get('tool_use_id') != native['parent_tool_use_id']):
                raise ValueError('suppression removed an unconfirmed Claude fact')
            if native.get('type') != 'assistant' or set(native) - {
                    'type', 'session_id', 'uuid', 'parent_tool_use_id', 'message',
                    'timestamp', 'subagent_type', 'task_description'}:
                raise ValueError('suppression removed a protected Claude envelope')
            message = native.get('message', {})
            if set(message) - {'id', 'type', 'role', 'model', 'content', 'stop_reason', 'stop_sequence', 'usage', 'context_management'}:
                raise ValueError('suppression removed mixed Claude message metadata')
            if message.get('context_management') is not None:
                raise ValueError('suppression removed Claude context-management facts')
            blocks = message.get('content')
            if not isinstance(blocks, list) or not blocks:
                raise ValueError('suppression removed an unknown Claude content shape')
            for block in blocks:
                fields = {'text': {'type', 'text'}, 'thinking': {'type', 'thinking', 'signature'},
                          'redacted_thinking': {'type', 'data'}}.get(block.get('type'))
                if fields is None or set(block) != fields:
                    raise ValueError('suppression removed a tool, lifecycle or mixed content block')
    terminals = sum(bool(value.get('raw')) and
                    (json.loads(value['raw']).get('method') == 'turn/completed'
                     and json.loads(value['raw']).get('params', {}).get('threadId') == root
                     if provider == 'codex' else json.loads(value['raw']).get('type') == 'result')
                    for value in suppressed)
    if terminals != 1:
        raise ValueError('policy comparison lacks the single original root result')
    return {'status': 'passed', 'preserved': len(preserved), 'suppressed': len(suppressed),
            'removedPureChildDetails': len(removed), 'retainedFactsUnchanged': True}


def validate_child_delivery(provider, frames, original, received, background=False):
    """Require actual Go membership, parent facts and native child completions."""
    observations = [item['observation'] for item in received if item.get('observation')]
    events = [(index, json.loads(item['raw'])) for index, item in enumerate(received)
              if item.get('raw') and item.get('kind') == 'event' and item.get('origin') == 'live']
    if provider == 'codex':
        parents = {}
        for evidence in original.get('childEvidence') or []:
            parents[evidence['childThreadId']] = evidence['parentThreadId']
        for receipt in original.get('receipts', []):
            for name in ('native-child/read', 'topology/thread-read'):
                thread = receipt.get(name, {}).get('thread', {})
                if thread.get('parentThreadId'):
                    parents[thread['id']] = thread['parentThreadId']
        members = {item['ThreadID'] for item in observations if item['Reason'] == 'member-confirmed'}
        relations = {(item['ThreadID'], item['ParentThreadID']) for item in observations
                     if item['Reason'] == 'parent-confirmed'}
        if not parents or not set(parents) <= members or not set(parents.items()) <= relations:
            raise ValueError('Go child membership or parent relationship differs from native evidence')
        roots = set(parents.values()) - set(parents)
        if len(roots) != 1:
            raise ValueError('recorded child topology has no unique root')
        root = roots.pop()
        terminal = lambda wire: wire.get('method') == 'turn/completed'
        identity = lambda wire: wire.get('params', {}).get('threadId')
        expected = [frame['wire'] for frame in frames if frame['direction'] == 's2c'
                    and terminal(frame['wire']) and identity(frame['wire']) in parents]
    else:
        started = [frame['wire'] for frame in frames if frame['direction'] == 's2c'
                   and frame['wire'].get('subtype') == 'task_started'
                   and frame['wire'].get('task_type') == 'local_agent']
        root = started[0]['session_id'] if started else None
        members = {item['Source'].get('task_id') for item in observations if item['Reason'] == 'member-confirmed'}
        relations = {item['Source'].get('task_id') for item in observations
                     if item['Reason'] == 'parent-confirmed' and item.get('Parent') is not None
                     and item['Parent'].get('session_id') == root and not item['Parent'].get('task_id')}
        children = {wire['task_id'] for wire in started}
        if not children or not children <= members or not children <= relations:
            raise ValueError('Go task membership or parent relationship differs from native evidence')
        terminal = lambda wire: wire.get('type') == 'result' and not wire.get('parent_tool_use_id')
        identity = lambda wire: wire.get('session_id')
        expected = [frame['wire'] for frame in frames if frame['direction'] == 's2c'
                    and frame['wire'].get('subtype') == 'task_notification'
                    and frame['wire'].get('task_id') in children]
        evidence = original.get('childEvidence') or {}
        if evidence.get('outputObserved') and not any(wire.get('parent_tool_use_id') == evidence['parentToolUseId']
                                                     for _, wire in events):
            raise ValueError('Go stream omitted the recorded child output')
        parents = dict.fromkeys(children, root)
    if not expected:
        raise ValueError('recording lacks child completion evidence')
    positions = []
    for wire in expected:
        matches = [index for index, actual in events if actual == wire]
        if not matches:
            raise ValueError('Go stream omitted or changed a native child completion')
        positions.append(matches[0])
    if background:
        roots = [index for index, wire in events if terminal(wire) and identity(wire) == root]
        if not roots or not any(index > roots[0] for index in positions):
            raise ValueError('Go stream lacks child completion after root completion')
    return {'status': 'passed', 'confirmedChildren': len(parents),
            'nativeChildCompletions': len(expected), 'backgroundCompletionChecked': background}


def run_adapter(adapter, root, plan, peer, output, policy):
    output.mkdir(mode=0o700)
    receipt = output / 'peer.json'
    command = [str(adapter), '--provider', plan['provider'], '--peer', peer['path'],
               '--recording', str(root / 'raw/raw.jsonl'), '--receipt', str(receipt), '--case', str(root),
               '--policy', policy, '--observations', str(output / 'deliveries.jsonl')]
    if plan['provider'] == 'claude':
        launcher = output / 'mock-cli'
        launcher.write_text('#!/bin/sh\nexec ' + shlex.join([peer['path'], 'protocol', 'mock', '--provider', 'claude',
                            '--scenario', str(root / 'raw/raw.jsonl'), '--receipt', str(receipt)]) + '\n')
        launcher.chmod(0o700)
        command += ['--launcher', str(launcher)]
    result = subprocess.run(command, capture_output=True, timeout=90, env={})
    (output / 'stderr.private').write_bytes(result.stderr)
    result.check_returncode()
    if plan['driverProfile'] == 'remote-close':
        verified = verify_receipt(output / 'main-peer.json', root / 'raw/raw.jsonl', plan['provider'])
        verify_receipt(output / 'probe-peer.json', root / 'remote-probe/raw/raw.jsonl', plan['provider'])
    else:
        verified = verify_receipt(receipt, root / 'raw/raw.jsonl', plan['provider'])
    return verified


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    selection = parser.add_mutually_exclusive_group(required=True)
    selection.add_argument('--case', type=Path)
    selection.add_argument('--cases-file', type=Path, help='private JSON list of {provider, caseId, case} recording paths')
    parser.add_argument('--adapter', required=True, type=Path)
    parser.add_argument('--output', required=True, type=Path)
    parser.add_argument('--policy', choices=['preserve', 'suppress'], help='single-policy diagnostic; delivery case defaults to both')
    args = parser.parse_args()
    if args.cases_file is not None:
        return run_batch(args)
    root = args.case.resolve(strict=True)
    verify_recording(root)
    plan = json.loads((root / 'plan.json').read_text())
    original = json.loads((root / 'report.json').read_text())
    replay = json.loads((root / 'replay-validation.json').read_text())
    manifest = json.loads((root / 'manifest.json').read_text())
    transport = json.loads((root / 'raw/transport.json').read_text())
    if (manifest.get('provenance') != 'real-private' or original.get('captureStatus') != 'complete'
            or transport.get('captureStatus') != 'complete' or not transport.get('cleanupComplete')):
        raise ValueError('complete real recording and process cleanup required')
    verify_receipt(root / 'replay-peer.json', root / 'raw/raw.jsonl', plan['provider'])
    if original['status'] != 'passed' or replay['status'] != 'passed' or not replay['fullConsumption']:
        raise ValueError('successful native flow and strict official SDK replay required')
    if plan['driverProfile'] not in ('history', 'continuation', 'existing-children', 'remote-close',
                                    'structured-input', 'active-input', 'fixture-tools', 'approval',
                                    'deferred-approval', 'questions', 'children', 'topology',
                                    'background-children', 'configuration-timing', 'interrupt', 'session-close'):
        raise ValueError('this Go adapter does not implement the selected driver profile')
    if plan['driverProfile'] == 'existing-children':
        from record_user_story.existing_children import verify_seed
        verify_seed(plan, check_source=False)
    peer = planned_identity(plan)
    adapter = args.adapter.resolve(strict=True)
    output = args.output.resolve()
    requires_comparison = bool(plan['scenario'].get('parameters', {}).get('childDetailPolicy'))
    comparison = None
    if requires_comparison and args.policy is None:
        output.mkdir(mode=0o700)
        streams = {}
        for policy in ['preserve', 'suppress']:
            verified = run_adapter(adapter, root, plan, peer, output / policy, policy)
            streams[policy] = [json.loads(line) for line in (output / policy / 'deliveries.jsonl').read_text().splitlines()]
        selections = [item['child-detail-selection'] for item in original['receipts'] if 'child-detail-selection' in item]
        if len(selections) != 1:
            raise ValueError('recording lacks the original native child selection')
        comparison = compare_delivery(plan['provider'], streams['preserve'], streams['suppress'], selections[0])
        (output / 'policy-comparison.json').write_text(json.dumps(comparison, indent=2) + '\n')
    else:
        verified = run_adapter(adapter, root, plan, peer, output, args.policy or 'preserve')
    verify_recording(root)
    if plan['driverProfile'] == 'existing-children':
        verify_seed(plan, check_source=False)
    evidence = {'status': 'incomplete' if requires_comparison and comparison is None else 'passed',
                'layer': 'Go SDK simulation acceptance', 'caseId': plan['caseId'],
                'provider': plan['provider'], 'adapterSHA256': hashlib.sha256(adapter.read_bytes()).hexdigest(),
                'recordingSHA256': verified['recordingSHA256'], 'fullConsumption': True,
                'clientEOF': True, 'nativeLaunched': False, 'childDetailPolicy': args.policy or ('both' if comparison else 'preserve')}
    if plan['driverProfile'] in ('children', 'topology', 'background-children'):
        frames = [json.loads(line) for line in (root / 'raw/raw.jsonl').read_text().splitlines()]
        received = [json.loads(line) for line in (output / 'deliveries.jsonl').read_text().splitlines()]
        evidence['flowAssertions'] = validate_child_delivery(plan['provider'], frames, original, received,
                                                             plan['driverProfile'] == 'background-children')
    if comparison is not None:
        evidence['deliveryComparison'] = comparison
    (output / 'result.json').write_text(json.dumps(evidence, indent=2) + '\n')
    print(plan['provider'], plan['caseId'], 'Go SDK simulation', evidence['status'])


def run_batch(args):
    items = json.loads(args.cases_file.read_text())
    if not isinstance(items, list) or not items:
        raise ValueError('nonempty private recording selection required')
    identities = set()
    for item in items:
        plan = json.loads((Path(item['case']) / 'plan.json').read_text())
        identity = (item['provider'], item['caseId'])
        if identity in identities or identity != (plan['provider'], plan['caseId']):
            raise ValueError('duplicate or mismatched recording identity')
        identities.add(identity)
    output = args.output.resolve()
    output.mkdir(mode=0o700)
    adapter = args.adapter.resolve(strict=True)

    def check(item):
        destination = output / (item['provider'] + '-' + item['caseId'])
        command = [sys.executable, str(Path(__file__).resolve()), '--case', item['case'],
                   '--adapter', str(adapter), '--output', str(destination)]
        if args.policy:
            command += ['--policy', args.policy]
        try:
            process = subprocess.run(command, capture_output=True, timeout=200)
            (output / (destination.name + '.log')).write_bytes(process.stdout + process.stderr)
            evidence_path = destination / 'result.json'
            evidence = json.loads(evidence_path.read_text()) if evidence_path.exists() else {}
            passed = process.returncode == 0 and evidence.get('status') == 'passed'
            result = dict(item, status='passed' if passed else 'failed', exitCode=process.returncode,
                          output=str(destination), evidence=evidence)
        except (OSError, subprocess.TimeoutExpired, ValueError) as exc:
            result = dict(item, status='failed', error=str(exc), output=str(destination))
        print(item['provider'], item['caseId'], result['status'], flush=True)
        return result

    with ThreadPoolExecutor(max_workers=4) as pool:
        results = list(pool.map(check, items))
    passed = sum(item['status'] == 'passed' for item in results)
    report = {'status': 'passed' if passed == len(results) else 'failed',
              'selected': len(results), 'passed': passed, 'nativeLaunched': False,
              'adapterSHA256': hashlib.sha256(adapter.read_bytes()).hexdigest(), 'results': results}
    (output / 'batch-result.json').write_text(json.dumps(report, indent=2) + '\n')
    print(f'Go SDK simulation: {passed}/{len(results)} passed; no native Agent launched')
    if report['status'] != 'passed':
        raise SystemExit(1)


if __name__ == '__main__':
    main()
