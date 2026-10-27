"""Native interactions driven exclusively by the pinned official Python SDKs."""
import asyncio
from dataclasses import asdict, is_dataclass
import json
import os
from pathlib import Path
import sys
import tempfile

from .environment import private_json, sdk_identity
from .policy import SYSTEM, TOOLS
from .scenarios import Flow, Untriggered, validate_recipe


def serial(value):
    if hasattr(value, 'model_dump'):
        return value.model_dump(mode='json', by_alias=True, exclude_none=True)
    if is_dataclass(value):
        return asdict(value)
    return value


def proxy_command(plan):
    if os.environ.get('AGENT_RECORDING_MOCK'):
        return [sys.executable, str(Path(__file__).with_name('bootstrap.py')), 'mock-proxy',
                '--provider', plan['provider'], '--peer', os.environ['AGENT_RECORDING_MOCK'],
                '--scenario', os.environ['AGENT_RECORDING_SCENARIO']]
    return [sys.executable, str(Path(__file__).with_name('bootstrap.py')), 'proxy',
            '--plan', str(Path(plan['root']) / 'plan.json')]


def codex(plan, report, flow):
    from openai_codex.client import CodexClient, CodexConfig
    from openai_codex.generated.v2_all import TurnStartParams
    from pydantic import BaseModel, ConfigDict

    class NativeAcknowledgement(BaseModel):
        model_config = ConfigDict(extra='allow')
    story = plan['story'][-3:]
    calls = set()

    def answer(method, params):
        report['requests'] += 1
        params = params or {}
        if method == 'item/tool/call' and story == '004':
            name = params.get('tool')
            args = params.get('arguments')
            if name in calls:
                raise ValueError('duplicate fixture tool')
            if name == 'fixture_read' and args == {'path': 'numbers.txt'} and not calls:
                text = (Path(plan.get('_fixtureProject', plan['project'])) / 'numbers.txt').read_text()
            elif name == 'fixture_write' and args == {'path': 'summary.txt', 'text': 'sum=42\n'} and calls == {'fixture_read'}:
                with (Path(plan.get('_fixtureProject', plan['project'])) / 'summary.txt').open('x') as out:
                    out.write(args['text'])
                text = 'written'
            else:
                raise ValueError('tool outside exact synthetic policy')
            flow.request(name)
            calls.add(name)
            return {'contentItems': [{'type': 'inputText', 'text': text}], 'success': True}
        if method == 'item/tool/requestUserInput' and story in ('010', '011'):
            questions = params.get('questions', [])
            if len(questions) != 1 or 'question' in calls:
                raise ValueError('unexpected synthetic question')
            question = questions[0]
            labels = [x.get('label') for x in question.get('options', [])]
            if set(labels) != set(flow.definition['answers']['options']):
                raise ValueError('unexpected question options')
            flow.request('question')
            calls.add('question')
            return {'answers': {question['id']: {'answers': [flow.definition['answers']['choice']]}}}
        # The official SDK's default approves commands/file changes. Never use it.
        if method in ('item/commandExecution/requestApproval', 'item/fileChange/requestApproval'):
            return {'decision': 'decline'}
        raise ValueError('unsupported native request; closed without approval')

    config = CodexConfig(launch_args_override=tuple(proxy_command(plan)), cwd=plan['project'],
                         client_name='official-python-story-recorder', client_title='Protocol recording')
    with CodexClient(config=config, approval_handler=answer) as client:
        report['receipts'].append({'initialize': serial(client.initialize())})
        if story == '009':
            report['gaps'].append('native approval lacks exact patch/path policy; no model launched')
            report['status'] = 'blocked'
            return
        params = {'cwd': plan['project'], 'sandbox': 'read-only', 'approvalPolicy': 'on-request',
                  'ephemeral': story != '019', 'baseInstructions': SYSTEM,
                  'developerInstructions': 'Only use the supplied synthetic project. No shell, agents, external files or integrations.'}
        if plan['model']:
            params['model'] = plan['model']
        if story == '004':
            params['dynamicTools'] = [
                {'type': 'function', 'name': 'fixture_read', 'description': 'Read synthetic numbers.txt once.',
                 'inputSchema': {'type': 'object', 'properties': {'path': {'const': 'numbers.txt'}}, 'required': ['path'], 'additionalProperties': False}},
                {'type': 'function', 'name': 'fixture_write', 'description': 'Write synthetic summary.txt once.',
                 'inputSchema': {'type': 'object', 'properties': {'path': {'const': 'summary.txt'}, 'text': {'const': 'sum=42\n'}}, 'required': ['path', 'text'], 'additionalProperties': False}}]
        # This pinned SDK's generated model omits dynamicTools. Its documented
        # JsonObject overload preserves this explicit native extension.
        thread = client.thread_start(params)
        identity = thread.thread.id
        report['receipts'].append({'thread/start': serial(thread)})
        def configure():
            requested_settings = [{'threadId': identity, 'model': thread.model}]
            thinking = flow.definition['configuration'].get('thinking')
            if thinking:
                requested_settings.append({'threadId': identity, **thinking})
            for requested in requested_settings:
                try:
                    response = client.request('thread/settings/update', requested, response_model=NativeAcknowledgement)
                    report['receipts'].append({'thread/settings/update': {'requested': requested, 'accepted': True, 'response': serial(response)}})
                except Exception as exc:
                    report['receipts'].append({'thread/settings/update': {'requested': requested, 'accepted': False, 'type': type(exc).__name__, 'error': str(exc)}})
                    report['facts'].append('native thread settings control rejected; requested value is not claimed effective')

        if flow.definition['configuration']:
            configure()
        for prompt in flow.inputs():
            value = {'threadId': identity, 'input': [{'type': 'text', 'text': prompt}]}
            if flow.definition['configuration']:
                value['model'] = thread.model
                value.update(flow.definition['configuration'].get('thinking') or {})
            flow.submit()
            turn = client.turn_start(identity, value['input'], params=TurnStartParams.model_validate(value))
            report['inputs'] += 1
            interrupted = False
            supplemental = None
            if story == '014':
                configure()
                report['facts'].append('native configuration controls acknowledged before consuming turn events')
            while True:
                event = client.next_turn_notification(turn.turn.id)
                report['events'] += 1
                item_type = serial(event.payload).get('item', {}).get('type') if event.method == 'item/started' else None
                item_progress = isinstance(item_type, str) and item_type != 'userMessage'
                action = flow.progress() if item_progress else None
                if action == 'submit-supplement':
                    interrupted = True
                    report['inputs'] += 1
                    try:
                        supplemental = client.turn_start(identity, [{'type': 'text', 'text': plan['prompts'][1]}])
                        flow.action(action, 'accepted')
                        report['receipts'].append({'active-turn/start': {'accepted': True, 'response': serial(supplemental)}})
                    except Exception as exc:
                        flow.action(action, 'rejected')
                        report['receipts'].append({'active-turn/start': {'accepted': False, 'type': type(exc).__name__, 'error': str(exc)}})
                        report['facts'].append('native supplemental turn/start rejected; no steer or retry substituted')
                if action == 'interrupt':
                    report['receipts'].append({'turn/interrupt': serial(client.turn_interrupt(identity, turn.turn.id))})
                    flow.action(action, 'accepted')
                    interrupted = True
                if event.method == 'turn/completed' and event.payload.turn.id == turn.turn.id:
                    report['results'] += 1
                    flow.result()
                    report['receipts'].append({'turn/completed': serial(event.payload)})
                    if event.payload.turn.status.value == 'failed':
                        raise ValueError('native turn failed')
                    break
            if flow.definition['submission'] == 'progress-barrier':
                if supplemental is not None and supplemental.turn.id != turn.turn.id:
                    report['receipts'].append({'supplemental/completed': serial(client.wait_for_turn_completed(supplemental.turn.id))})
                    report['results'] += 1
                break
        if story in ('008', '019'):
            report['receipts'].append({'thread/read': serial(client.thread_read(identity, include_turns=True))})
    if story == '018':
        try:
            client.turn_start(identity, 'Reply Ready.')
        except Exception:
            report['receipts'].append({'post-close-submit': 'rejected'})
        else:
            raise ValueError('post-close official SDK submit unexpectedly accepted')
    if story == '004' and calls != {'fixture_read', 'fixture_write'}:
        raise Untriggered('required fixture tools untriggered')
    if story in ('010', '011') and 'question' not in calls:
        raise Untriggered('required original question untriggered')


async def claude(plan, report, flow):
    from claude_agent_sdk import ClaudeSDKClient, ClaudeAgentOptions, Transport, PermissionResultAllow, PermissionResultDeny, ResultMessage
    story = plan['story'][-3:]
    used = set()
    client = None

    class RawTransport(Transport):
        def __init__(self):
            self.proc = None

        async def connect(self):
            self.proc = await asyncio.create_subprocess_exec(*proxy_command(plan), stdin=asyncio.subprocess.PIPE,
                            stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.DEVNULL, limit=2 << 20)

        async def write(self, data):
            self.proc.stdin.write(data.encode())
            await self.proc.stdin.drain()

        async def read_messages(self):
            while line := await self.proc.stdout.readline():
                yield json.loads(line)

        async def end_input(self):
            if self.proc and self.proc.stdin:
                self.proc.stdin.close()

        def is_ready(self):
            return self.proc is not None and self.proc.returncode is None

        async def close(self):
            await self.end_input()
            if self.proc and self.proc.returncode is None:
                try:
                    await asyncio.wait_for(self.proc.wait(), 5)
                except asyncio.TimeoutError:
                    self.proc.terminate()
                    await asyncio.wait_for(self.proc.wait(), 5)

    async def permission(tool, values, context):
        report['requests'] += 1
        if tool == 'AskUserQuestion' and story == '011' and tool not in used:
            questions = values.get('questions', [])
            if len(questions) == 1 and questions[0].get('question') == flow.definition['answers']['question'] and {v.get('label') for v in questions[0].get('options', [])} == set(flow.definition['answers']['options']):
                flow.request('question')
                used.add(tool)
                return PermissionResultAllow(updated_input={**values, 'answers': {flow.definition['answers']['question']: flow.definition['answers']['choice']}})
        if story == '009' and tool == 'Write':
            denied = Path(values.get('file_path', ''))
            if not denied.is_absolute():
                denied = Path(plan['project']) / denied
            if denied == Path(plan['project']) / 'denied.txt' and values.get('content') == 'denied\n':
                flow.request('denied_write')
                used.add('denied_write')
                return PermissionResultDeny(message='Synthetic denial fixture; do not retry')
        if tool in TOOLS.get(story, []) and tool not in used:
            expected = 'numbers.txt' if tool == 'Read' else 'summary.txt'
            path = Path(values.get('file_path', ''))
            if not path.is_absolute():
                path = Path(plan['project']) / path
            exact = path == Path(plan['project']) / expected and not path.is_symlink()
            if exact and (tool == 'Read' or values.get('content') == 'total=18\n'):
                if tool == 'Write' and (Path(plan.get('_fixtureProject', plan['project'])) / expected).exists():
                    return PermissionResultDeny(message='Synthetic output already exists')
                flow.request(tool)
                if story == '010':
                    before = report['events']
                    await client.set_model(plan['model'] or None)
                    flow.request('held-control')
                    report['receipts'].append({'held-permission-control': 'set_model acknowledged'})
                    report['facts'].append('original Read permission retained until control acknowledgement')
                    if report['events'] > before:
                        flow.request('held-event')
                    else:
                        report['facts'].append('no additional message observed during the held-permission control interval')
                used.add(tool)
                return PermissionResultAllow(updated_input=values)
        return PermissionResultDeny(message='Outside the exact synthetic fixture policy; do not retry')

    options = ClaudeAgentOptions(cwd=plan['project'], tools=TOOLS.get(story, []),
                can_use_tool=permission, setting_sources=[], include_partial_messages=True)
    async with ClaudeSDKClient(options=options, transport=RawTransport()) as client:
        if flow.definition['configuration']:
            await client.set_model(plan['model'] or None)
            report['receipts'].append({'set_model': 'acknowledged'})
        for prompt in flow.inputs():
            flow.submit()
            if story == '002':
                async def structured_input():
                    yield {'type': 'user', 'message': {'role': 'user', 'content': [{'type': 'text', 'text': prompt}]}, 'parent_tool_use_id': None}
                await client.query(structured_input())
            else:
                await client.query(prompt)
            report['inputs'] += 1
            if story == '014':
                await client.set_model(plan['model'] or None)
                report['receipts'].append({'unread-events-control': 'set_model acknowledged before receive_response'})
            interrupted = False
            async for event in client.receive_response():
                report['events'] += 1
                action = flow.progress() if type(event).__name__ == 'StreamEvent' else None
                if action == 'submit-supplement':
                    await client.query(plan['prompts'][1])
                    flow.action(action, 'written')
                    report['inputs'] += 1
                    interrupted = True
                if action == 'interrupt':
                    await client.interrupt()
                    flow.action(action, 'accepted')
                    report['receipts'].append({'interrupt': 'acknowledged'})
                    interrupted = True
                if isinstance(event, ResultMessage):
                    flow.result()
                    report['results'] += 1
                    report['receipts'].append({'result': serial(event)})
                    if event.is_error:
                        # Claude reports an acknowledged cancellation as an error
                        # result. Accept only its explicit cancellation reasons
                        # after this scenario's interrupt, retaining the raw result.
                        cancelled = (story == '016' and interrupted
                                     and event.subtype == 'error_during_execution'
                                     and event.terminal_reason in ('aborted_streaming', 'aborted_tools')
                                     and event.api_error_status is None)
                        if not cancelled:
                            raise ValueError('native result is_error')
                        report['receipts'].append({'interrupted-result': {'terminalReason': event.terminal_reason}})
            if flow.definition['submission'] == 'progress-barrier':
                # The second input is sent during progress, never replayed as a
                # new sequential request if active input was not observed.
                break
        if flow.definition['submission'] == 'progress-barrier' and report['inputs'] > report['results']:
            async def supplemental_result():
                async for event in client.receive_response():
                    report['events'] += 1
                    if isinstance(event, ResultMessage):
                        report['results'] += 1
                        report['receipts'].append({'result': serial(event)})
                        if event.is_error:
                            raise ValueError('native active-input result is_error')
            try:
                await asyncio.wait_for(supplemental_result(), timeout=20)
            except asyncio.TimeoutError:
                report['facts'].append('no separate supplemental result within bounded observation; no input resent')
    if story == '018':
        try:
            await client.query('Reply Ready.')
        except Exception:
            report['receipts'].append({'post-close-submit': 'rejected'})
        else:
            raise ValueError('post-close official SDK query unexpectedly accepted')
    if story == '009' and 'denied_write' not in used:
        raise Untriggered('required native write denial untriggered')
    if story == '004' and used != {'Read', 'Write'}:
        raise Untriggered('required native tools untriggered')
    if story == '011' and 'AskUserQuestion' not in used:
        raise Untriggered('required original question untriggered')
    if story == '019':
        from claude_agent_sdk import list_sessions, get_session_info, get_session_messages
        saved = list_sessions(directory=plan['project'], limit=10, include_worktrees=False)
        report['receipts'].append({'list_sessions': [serial(x) for x in saved]})
        if not saved:
            report['gaps'].append('official SDK found no saved synthetic session')
        for item in saved:
            report['receipts'].append({'get_session_info': serial(get_session_info(item.session_id, directory=plan['project']))})
            report['receipts'].append({'get_session_messages': [serial(x) for x in get_session_messages(item.session_id, directory=plan['project'], limit=100)]})


def run(plan):
    if plan.get('version') == 2 and plan.get('childCase'):
        return run_child_profile(plan)
    definition = validate_recipe(plan)
    flow = Flow(definition)
    replay = bool(os.environ.get('AGENT_RECORDING_MOCK'))
    fixture = tempfile.TemporaryDirectory(prefix='official-sdk-replay-') if replay else None
    if fixture:
        plan = dict(plan, _fixtureProject=fixture.name)
        for name, content in definition['files'].items():
            (Path(fixture.name) / name).write_text(content)
    report = {'provider': plan['provider'], 'story': plan['story'], 'status': 'passed',
              'inputs': 0, 'results': 0, 'events': 0, 'requests': 0, 'receipts': [],
              'facts': ['driver uses official Python SDK; raw bytes captured at native process boundary'],
              'case': definition['case'], 'gaps': []}
    report['scenarioSHA256'] = plan['scenarioSHA256']
    report['flowTrace'] = flow.trace
    if definition['mode'] != 'live-selected-path':
        report['status'] = definition['mode']
        report['gaps'].append(definition['reason'])
        report['offlineTest'] = definition['offlineTest']
    else:
        try:
            if sdk_identity(plan['provider']) != plan['officialSDK']:
                raise ValueError('prepared official SDK identity changed')
            if plan['provider'] == 'codex':
                codex(plan, report, flow)
            elif plan['provider'] == 'claude':
                asyncio.run(claude(plan, report, flow))
            else:
                raise ValueError('no Python native adapter for provider')
            flow.finish()
            report['branchStatus'] = 'passed'
        except Untriggered as exc:
            report['status'] = 'untriggered'
            report['branchStatus'] = 'untriggered'
            report['gaps'].append(str(exc))
        except Exception as exc:
            report['status'] = 'failed'
            report['branchStatus'] = 'failed'
            private_json(Path(plan['root']) / ('replay-diagnostics.private.json' if replay else 'diagnostics.private.json'), {'error': str(exc), 'type': type(exc).__name__})
    if not replay and (Path(plan['root']) / 'native.started').exists():
        try:
            transport = json.loads((Path(plan['raw']) / 'transport.json').read_text())
            if not transport.get('cleanupComplete'):
                raise ValueError('native cleanup incomplete')
        except Exception:
            report['status'] = 'failed'
            report['gaps'].append('native cleanup receipt unavailable or incomplete')
    if fixture:
        fixture.cleanup()
    private_json(Path(plan['root']) / ('replay-report.json' if replay else 'report.json'), report)
    return int(report['status'] in ('failed', 'untriggered'))


def run_child_profile(plan):
    """Keep the separate opt-in child profile on its existing native contract.

    This profile is not promoted to v3 deterministic-branch acceptance. Its
    native evidence and limits are reported by the dedicated child adapters.
    """
    replay = bool(os.environ.get('AGENT_RECORDING_MOCK'))
    report = {'provider': plan['provider'], 'story': plan['story'], 'status': 'partial',
              'inputs': 0, 'results': 0, 'events': 0, 'requests': 0, 'receipts': [],
              'facts': ['dedicated v2 child profile; outside deterministic v3 branch acceptance'],
              'gaps': ['one selected child path does not establish complete user-story acceptance']}
    if 'case' in plan:
        from .child_policy import case_contract
        if plan['case'] != case_contract(plan['story']):
            raise ValueError('child case scope changed')
        report.update(case=plan['case'], status='passed', gaps=[], limitations=[])
    try:
        if sdk_identity(plan['provider']) != plan['officialSDK']:
            raise ValueError('prepared official SDK identity changed')
        from . import children
        if plan['provider'] == 'codex':
            children.codex(plan, report)
        else:
            asyncio.run(children.claude(plan, report))
        if 'case' in plan:
            if report['gaps']:
                raise Untriggered('required child evidence absent')
            if report['inputs'] != 1 or report['results'] != 1:
                raise Untriggered('required child root result absent')
            report['branchStatus'] = 'passed'
    except Untriggered as exc:
        report['status'] = 'untriggered'
        report['branchStatus'] = 'untriggered'
        report['gaps'].append(str(exc))
    except Exception as exc:
        report['status'] = 'failed'
        private_json(Path(plan['root']) / ('replay-diagnostics.private.json' if replay else 'diagnostics.private.json'),
                     {'error': str(exc), 'type': type(exc).__name__})
    if not replay and (Path(plan['root']) / 'native.started').exists():
        try:
            transport = json.loads((Path(plan['raw']) / 'transport.json').read_text())
            if not transport.get('cleanupComplete'):
                raise ValueError('native cleanup incomplete')
        except Exception:
            report['status'] = 'failed'
            report['gaps'].append('native cleanup receipt unavailable or incomplete')
    private_json(Path(plan['root']) / ('replay-report.json' if replay else 'report.json'), report)
    return int(report['status'] in ('failed', 'untriggered'))
