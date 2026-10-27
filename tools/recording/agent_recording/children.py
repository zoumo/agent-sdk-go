"""Official SDK execution of the explicitly authorized single synthetic child case."""
import asyncio
import json
from pathlib import Path
import threading

from .child_policy import CHILD_FILE, CHILD_ROLE, CHILD_TEXT, child_task
from .drivers import proxy_command, serial
from .environment import read_regular


def limitation(report, message):
    report.setdefault('limitations' if 'case' in report else 'gaps', []).append(message)


def limitations(report):
    report['facts'] += ['one-child fixed-file case explicitly selected',
                        'native shell, network tools, external integrations and writes disabled',
                        'exact synthetic read path enforced at the tool/permission callback',
                        'owned process group, activity deadline and protocol budgets retained']
    for message in ['one child and no descendants are also prompt constraints; total attempted creation is not an independently verified OS-enforced cap',
                       'read-only native sandbox is not a claim of an OS allowlist for all filesystem reads',
                       'parent/child and background facts require actual native source records']:
        limitation(report, message)


def codex(plan, report):
    from openai_codex.client import CodexClient, CodexConfig
    root_id = None
    read_done = threading.Event()
    child_ids = set()
    tool_callers = set()
    background = plan['story'].endswith('007')
    limitations(report)
    report['facts'].append('Codex multi_agent V1 selected; agents concurrency=1 and max_depth=1 configured; V2 stays disabled')

    def answer(method, params):
        report['requests'] += 1
        params = params or {}
        if method != 'item/tool/call' or params.get('tool') != 'fixture_read' or params.get('arguments') != {'path': CHILD_FILE}:
            raise ValueError('request outside exact synthetic child read policy')
        caller = params.get('threadId')
        if not caller or caller == root_id or read_done.is_set():
            raise ValueError('only one non-root native thread may read the fixture once')
        # The callback reads only the newly generated synthetic file. Native
        # membership evidence is recorded separately, never inferred from this ID.
        content = read_regular(Path(plan['project']) / CHILD_FILE).decode()
        if content != CHILD_TEXT:
            raise ValueError('synthetic child fixture changed')
        tool_callers.add(caller)
        report['receipts'].append({'fixture_read': {'nativeThreadId': caller, 'text': content}})
        read_done.set()
        return {'contentItems': [{'type': 'inputText', 'text': content}], 'success': True}

    config = CodexConfig(launch_args_override=tuple(proxy_command(plan)), cwd=plan['project'],
                         client_name='official-python-child-case', client_title='Synthetic child read case')
    with CodexClient(config=config, approval_handler=answer) as client:
        report['receipts'].append({'initialize': serial(client.initialize())})
        params = {'cwd': plan['project'], 'sandbox': 'read-only', 'approvalPolicy': 'on-request', 'ephemeral': False,
                  'baseInstructions': 'Execute only the explicit synthetic one-child task. No shell, network, writes or unrelated files. Use only spawn_agent, wait if asked, and the advertised fixture_read tool. Root must never call fixture_read.',
                  'developerInstructions': 'Only fixture_reader may read child-fixture.txt once. Never create a second child or descendants.',
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
        if background and not read_done.is_set():
            read_done.wait(timeout=5)
        for child in sorted(child_ids):
            try:
                report['receipts'].append({'native-child/read': serial(client.thread_read(child, include_turns=True))})
            except Exception as exc:
                report['receipts'].append({'native-child/read-rejected': {'threadId': child, 'type': type(exc).__name__, 'error': str(exc)}})
        if not child_ids:
            report['gaps'].append('no child identity confirmed by native collaboration records; child creation untriggered')
        if not read_done.is_set():
            report['gaps'].append('no child fixture_read observed; no read or child output was fabricated')
        if tool_callers - child_ids:
            report['gaps'].append('fixture caller has no confirmed native collaboration membership')
        if background:
            # This adapter has no post-root child completion observation. Do not
            # turn its spawn/read receipts into a background-completion pass.
            report['gaps'].append('native post-root child completion is not established')


async def claude(plan, report):
    from claude_agent_sdk import ClaudeSDKClient, ClaudeAgentOptions, AgentDefinition, Transport, PermissionResultAllow, PermissionResultDeny, ResultMessage
    limitations(report)
    used = set()
    agent_tool_id = None
    background = plan['story'].endswith('007')
    report['facts'].append('Claude child definition advertises only Read and explicitly removes Agent/Bash/write/network tools; maxTurns=2 requested')
    limitation(report, 'CLI 2.1.199 predates documented 2.1.219 recursive-limit enforcement; new depth/concurrency environment variables are not claimed as enforced')

    class RawTransport(Transport):
        def __init__(self): self.proc = None
        async def connect(self):
            self.proc = await asyncio.create_subprocess_exec(*proxy_command(plan), stdin=asyncio.subprocess.PIPE,
                         stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.DEVNULL, limit=2 << 20)
        async def write(self, data):
            self.proc.stdin.write(data.encode()); await self.proc.stdin.drain()
        async def read_messages(self):
            while line := await self.proc.stdout.readline(): yield json.loads(line)
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
        if tool == 'Agent' and not context.agent_id and 'Agent' not in used:
            role = values.get('subagent_type', values.get('name'))
            allowed = {'subagent_type', 'name', 'description', 'prompt', 'run_in_background', 'model'}
            if (role == CHILD_ROLE and values.get('prompt') == child_task('claude') and set(values) <= allowed
                    and values.get('run_in_background', background) == background and values.get('model', 'inherit') == 'inherit'):
                used.add('Agent')
                agent_tool_id = context.tool_use_id
                report['receipts'].append({'allow-single-Agent': {'toolUseId': context.tool_use_id, 'role': role}})
                return PermissionResultAllow(updated_input=values)
        if tool == 'Read' and 'Agent' in used and 'Read' not in used:
            path = Path(values.get('file_path',''))
            if not path.is_absolute(): path = Path(plan['project']) / path
            if (path == Path(plan['project']) / CHILD_FILE and not path.is_symlink()
                    and ('case' not in plan or context.agent_id)):
                used.add('Read')
                report['receipts'].append({'allow-child-Read': {'toolUseId': context.tool_use_id, 'nativeAgentId': context.agent_id}})
                return PermissionResultAllow(updated_input=values)
        report['gaps'].append('out-of-case native tool request denied with interrupt')
        return PermissionResultDeny(message='Outside the explicitly selected one-child fixed-file read case.',interrupt=True)

    definition = AgentDefinition(description='Read only the synthetic child-fixture.txt once and return its exact token.',
                 prompt=child_task('claude'), tools=['Read'], disallowedTools=['Agent','Bash','Write','Edit','WebFetch','WebSearch'],
                 maxTurns=2, background=background, model='inherit')
    options = ClaudeAgentOptions(cwd=plan['project'], tools=['Agent','Read'], can_use_tool=permission,
               setting_sources=[], agents={CHILD_ROLE: definition}, include_partial_messages=True)
    async with ClaudeSDKClient(options=options, transport=RawTransport()) as client:
        await client.query(plan['prompts'][0]); report['inputs'] += 1
        async for event in client.receive_response():
            report['events'] += 1
            if isinstance(event,ResultMessage):
                report['results'] += 1; report['receipts'].append({'root/result':serial(event)})
                if event.is_error: raise ValueError('native root child-case result is_error')
        if background:
            async def observe_background():
                async for event in client.receive_messages():
                    report['events'] += 1
                    subtype = getattr(event,'subtype','')
                    if type(event).__name__ == 'TaskNotificationMessage' or subtype == 'task_notification':
                        if 'case' in plan and (not agent_tool_id
                                or getattr(event, 'tool_use_id', None) != agent_tool_id
                                or getattr(event, 'status', None) != 'completed'):
                            continue
                        report['receipts'].append({'post-root-native-task':serial(event)})
                        return
            try: await asyncio.wait_for(observe_background(),5)
            except asyncio.TimeoutError: report['gaps'].append('no native post-root child completion within bounded observation')
    if 'Agent' not in used: report['gaps'].append('no authorized single Agent permission request observed; child creation is not inferred')
    if 'Read' not in used: report['gaps'].append('no authorized child Read permission observed; no fixed-file read was fabricated')
    if background: limitation(report, 'actual root-first ordering and native background/completion flags require raw source inspection')
