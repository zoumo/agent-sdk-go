"""Official SDK execution of the explicitly authorized single synthetic child case."""
import asyncio
import json
from pathlib import Path
import threading

from .child_policy import CHILD_FILE, CHILD_ROLE, CHILD_TEXT, child_task
from .adapters.common import proxy_command, serial
from .environment import read_regular
from .policy import driver_profile


def limitation(report, message):
    report.setdefault('limitations', []).append(message)


def limitations(report):
    report['facts'] += ['synthetic child task selected; native hook and tool activity retained',
                        'case evidence is assessed independently of capture completion']
    limitation(report, 'parent/child and background facts require actual native source records')


def codex(plan, report):
    from openai_codex.client import CodexClient, CodexConfig
    root_id = None
    read_done = threading.Event()
    child_ids = set()
    tool_callers = set()
    tool_turns = {}
    background = driver_profile(plan) == 'background-children'
    limitations(report)

    def answer(method, params):
        report['requests'] += 1
        params = params or {}
        if method in ('item/commandExecution/requestApproval', 'item/fileChange/requestApproval'):
            return {'decision': 'accept'}
        if method != 'item/tool/call' or params.get('tool') != 'fixture_read':
            raise NotImplementedError('unsupported native child request: ' + method)
        caller = params.get('threadId')
        # The callback reads only the newly generated synthetic file. Native
        # membership evidence is recorded separately, never inferred from this ID.
        content = read_regular(Path(plan['project']) / CHILD_FILE).decode()
        tool_callers.add(caller)
        tool_turns.setdefault(caller, set()).add(params.get('turnId'))
        report['receipts'].append({'fixture_read': {'nativeThreadId': caller, 'text': content}})
        read_done.set()
        return {'contentItems': [{'type': 'inputText', 'text': content}], 'success': True}

    config = CodexConfig(launch_args_override=tuple(proxy_command(plan)), cwd=plan['project'],
                         client_name='official-python-child-case', client_title='Synthetic child read case')
    with CodexClient(config=config, approval_handler=answer) as client:
        report['receipts'].append({'initialize': serial(client.initialize())})
        params = {'cwd': plan['project'], 'sandbox': 'workspace-write', 'approvalPolicy': 'on-request', 'ephemeral': False,
                  'baseInstructions': 'Complete the supplied synthetic child task.',
                  'developerInstructions': 'Delegate the fixture read to fixture_reader.',
                  'dynamicTools': [{'type': 'function', 'name': 'fixture_read', 'description': 'Child-only: read the single synthetic fixture exactly once.',
                    'inputSchema': {'type': 'object', 'properties': {'path': {'type': 'string', 'enum': [CHILD_FILE]}}, 'required': ['path'], 'additionalProperties': False}}]}
        if plan['model']:
            params['model'] = plan['model']
        started = client.thread_start(params)
        root_id = started.thread.id
        report['receipts'].append({'thread/start': serial(started)})
        turn = client.turn_start(root_id, plan['prompts'][0])
        report['inputs'] += 1
        while True:
            event = client.next_turn_notification(turn.turn.id)
            report['events'] += 1
            native = serial(event.payload)
            item = native.get('item', {})
            if item.get('type') == 'subAgentActivity':
                if item.get('agentThreadId'):
                    child_ids.add(item['agentThreadId'])
            if item.get('type') == 'collabAgentToolCall':
                child_ids.update(item.get('receiverThreadIds', []))
                if event.method == 'item/completed':
                    report['receipts'].append({'native-collaboration': native})
            if event.method == 'turn/completed' and event.payload.turn.id == turn.turn.id:
                report['results'] += 1
                report['receipts'].append({'root/completed': native})
                if event.payload.turn.status.value == 'failed':
                    raise ValueError('native root child-case turn failed')
                break
        post_root_complete = False
        for child in sorted(child_ids):
            try:
                snapshot = serial(client.thread_read(child, include_turns=True))
                report['receipts'].append({'native-child/read': snapshot})
                if snapshot.get('thread', {}).get('id') != child:
                    raise ValueError('native child query returned a different identity')
                if any(turn.get('status') == 'failed' for turn in snapshot['thread'].get('turns', [])):
                    raise RuntimeError('native child turn failed')
                if background:
                    for child_turn in snapshot.get('thread', {}).get('turns', []):
                        if child_turn.get('status') == 'failed':
                            raise RuntimeError('native child turn failed')
                        if child_turn.get('status') == 'inProgress':
                            completed = serial(client.wait_for_turn_completed(child_turn['id']))
                            report['receipts'].append({'post-root-native-child/completed': completed})
                            if completed.get('turn', {}).get('status') == 'failed':
                                raise RuntimeError('native child turn failed')
                            if completed.get('threadId') != child or completed.get('turn', {}).get('id') != child_turn['id']:
                                raise ValueError('native child completion returned a different identity')
                            post_root_complete = completed.get('turn', {}).get('status') == 'completed'
                            snapshot = serial(client.thread_read(child, include_turns=True))
                            report['receipts'].append({'post-root-native-child/read': snapshot})
                thread = snapshot.get('thread', {})
                if thread.get('id') != child:
                    raise ValueError('native child query returned a different identity')
                if child in tool_callers:
                    completed_turns = [turn for turn in thread.get('turns', [])
                                       if turn.get('id') in tool_turns[child] and turn.get('status') == 'completed']
                    output = any(item.get('type') == 'agentMessage' and item.get('text')
                                 for turn in completed_turns for item in turn.get('items', []))
                    if thread.get('parentThreadId') != root_id:
                        report['gaps'].append('native child parent edge to the root is not established')
                    if None in tool_turns[child] or not tool_turns[child].issubset({turn['id'] for turn in completed_turns}):
                        report['gaps'].append('native child fixture-read execution completion is not established')
                    if not output:
                        report['gaps'].append('native completed child output is not established')
                    if thread.get('parentThreadId') == root_id and completed_turns and output:
                        report.setdefault('childEvidence', []).append({'childThreadId': child, 'parentThreadId': root_id,
                            'completedTurnIds': [turn['id'] for turn in completed_turns], 'outputObserved': True})
            except Exception as exc:
                report['receipts'].append({'native-child/read-rejected': {'threadId': child, 'type': type(exc).__name__, 'error': str(exc)}})
                raise
        if not child_ids:
            report['gaps'].append('no child identity confirmed by native collaboration records; child creation untriggered')
        if not read_done.is_set():
            report['gaps'].append('no child fixture_read observed; no read or child output was fabricated')
        if tool_callers - child_ids:
            report['gaps'].append('fixture caller has no confirmed native collaboration membership')
        if background and not post_root_complete:
            report['gaps'].append('native post-root child completion is not established')


async def claude(plan, report):
    from claude_agent_sdk import ClaudeSDKClient, ClaudeAgentOptions, AgentDefinition, Transport, PermissionResultAllow, PermissionResultDeny, ResultMessage
    limitations(report)
    used = set()
    agent_tool_id = None
    child_completed = False
    child_tasks = {}
    events = []
    read_requests = {}
    background = driver_profile(plan) == 'background-children'

    class RawTransport(Transport):
        def __init__(self): self.proc = None
        async def connect(self):
            self.proc = await asyncio.create_subprocess_exec(*proxy_command(plan), stdin=asyncio.subprocess.PIPE,
                         stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.DEVNULL)
        async def write(self, data):
            self.proc.stdin.write(data.encode()); await self.proc.stdin.drain()
        async def read_messages(self):
            from .stream import json_messages
            async for message in json_messages(self.proc.stdout):
                yield message
        async def end_input(self):
            if self.proc and self.proc.stdin: self.proc.stdin.close()
        def is_ready(self): return self.proc is not None and self.proc.returncode is None
        async def close(self):
            await self.end_input()
            if self.proc and self.proc.returncode is None:
                try: await asyncio.wait_for(self.proc.wait(),5)
                except asyncio.TimeoutError:
                    self.proc.terminate(); await asyncio.wait_for(self.proc.wait(),5)

    async def permission(tool, values, context):
        nonlocal agent_tool_id
        report['requests'] += 1
        if tool == 'Agent' and getattr(context, 'agent_id', None) is None:
            used.add('Agent')
            agent_tool_id = context.tool_use_id
            report['receipts'].append({'allow-Agent': {'toolUseId': context.tool_use_id}})
        if tool == 'Read' and getattr(context, 'agent_id', None):
            used.add('Read')
            read_requests[context.tool_use_id] = context.agent_id
            report['receipts'].append({'allow-child-Read': {'toolUseId': context.tool_use_id, 'nativeAgentId': context.agent_id}})
        return PermissionResultAllow(updated_input=values)

    def observe(event):
        report['events'] += 1
        kind, native = type(event).__name__, serial(event)
        events.append({'kind': kind, 'native': native})
        report['receipts'].append({'native-child/event': {'kind': kind, 'native': native}})
        if (kind == 'TaskStartedMessage' or native.get('subtype') == 'task_started') and native.get('tool_use_id') == agent_tool_id:
            if agent_tool_id and native.get('task_id') and native.get('session_id'):
                child_tasks[native['task_id']] = native['session_id']
        if kind == 'TaskNotificationMessage' or native.get('subtype') == 'task_notification':
            if (agent_tool_id and native.get('tool_use_id') == agent_tool_id
                    and native.get('task_id') in child_tasks
                    and native.get('session_id') == child_tasks[native['task_id']]):
                if native.get('status') == 'failed':
                    raise RuntimeError('native child task failed')
                return native.get('status') == 'completed'
        return False

    definition = AgentDefinition(description='Read the synthetic child-fixture.txt and return its token.',
                 prompt=child_task('claude'), background=background, model='inherit')
    options = ClaudeAgentOptions(cwd=plan['project'], can_use_tool=permission,
               agents={CHILD_ROLE: definition}, include_partial_messages=True)
    async with ClaudeSDKClient(options=options, transport=RawTransport()) as client:
        await client.query(plan['prompts'][0]); report['inputs'] += 1
        async for event in client.receive_response():
            child_completed = observe(event) or child_completed
            if isinstance(event,ResultMessage):
                report['results'] += 1; report['receipts'].append({'root/result':serial(event)})
                if event.is_error: raise ValueError('native root child-case result is_error')
        if background and agent_tool_id and not child_completed:
            async def observe_background():
                async for event in client.receive_messages():
                    if observe(event):
                        report['receipts'].append({'post-root-native-task':serial(event)})
                        return True
            if not await observe_background():
                report['gaps'].append('no native post-root child completion observed')
        elif background:
            report['gaps'].append('native child did not complete after the root result')
    if 'Agent' not in used: report['gaps'].append('no authorized root Agent permission request observed; child creation is not inferred')
    if 'Read' not in used: report['gaps'].append('no authorized child Read permission observed; no fixed-file read was fabricated')
    assess_claude_child(report, events, agent_tool_id, read_requests, background)


def assess_claude_child(report, events, parent_tool_id, read_requests, background):
    """Relate native task, tool request, output and completion without ID aliases."""
    root = next((item['root/result'].get('session_id') for item in report['receipts'] if 'root/result' in item), None)
    starts = [(index, event['native']) for index, event in enumerate(events)
              if (event['kind'] == 'TaskStartedMessage' or event['native'].get('subtype') == 'task_started')
              and parent_tool_id and event['native'].get('tool_use_id') == parent_tool_id
              and root and event['native'].get('session_id') == root and event['native'].get('task_id')]
    if len({native['task_id'] for _, native in starts}) != 1:
        report['gaps'].append('native child task identity and parent request association are not established')
        return
    started, task = starts[0]
    finished, output, agents = None, [], set()
    root_result = None
    for index, event in enumerate(events):
        kind, native = event['kind'], event['native']
        if kind == 'ResultMessage' and native.get('session_id') == root:
            root_result = index
        if index <= started or native.get('session_id') != root:
            continue
        if kind == 'AssistantMessage' and native.get('parent_tool_use_id') == parent_tool_id:
            for block in native.get('content', []):
                if block.get('name') == 'Read' and block.get('id') in read_requests:
                    agents.add(read_requests[block['id']])
                if block.get('type', 'text') == 'text' and block.get('text'):
                    output.append(index)
        if ((kind == 'TaskNotificationMessage' or native.get('subtype') == 'task_notification')
                and native.get('tool_use_id') == parent_tool_id and native.get('task_id') == task['task_id']
                and native.get('status') == 'completed'):
            finished = index
    if not agents:
        report['gaps'].append('native child Read identity has no correlated parent tool-use context')
    if finished is None:
        report['gaps'].append('native child task completion is not established')
    if not any(finished is not None and index < finished for index in output):
        report['gaps'].append('native child output before its completion is not established')
    if background and (root_result is None or finished is None or finished <= root_result):
        report['gaps'].append('native post-root child completion is not established')
    if not report['gaps']:
        report['childEvidence'] = {'sessionId': root, 'parentToolUseId': parent_tool_id,
                                  'taskId': task['task_id'], 'agentIds': sorted(agents),
                                  'outputObserved': True, 'completed': True}
