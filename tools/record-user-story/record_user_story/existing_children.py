"""Observe a resumed session whose recorded children predate this connection."""
import asyncio
import hashlib
import json
import os
from pathlib import Path
import re

from .adapters.common import proxy_command, serial
from .environment import read_regular, recording_temp_root, verify_recording
from .scenarios import Untriggered


def sha(path):
    return hashlib.sha256(read_regular(path, None)).hexdigest()


def project_files(project):
    """Pin the synthetic historical cwd without permitting native writes to it."""
    result = {}
    for path in sorted(project.rglob('*')):
        if path.is_symlink() or not (path.is_dir() or path.is_file()):
            raise ValueError('seed project contains an unsupported filesystem entry')
        result[str(path.relative_to(project))] = 'directory' if path.is_dir() else sha(path)
    return result


def prepare_seed(source, root, provider):
    """Copy only verified saved data; keep source and copied identities explicit."""
    if provider == 'codex':
        from .existing_codex import prepare_seed as prepare_codex_seed
        return prepare_codex_seed(source, root)
    source, root = Path(source).resolve(strict=True), Path(root).resolve(strict=True)
    verify_recording(source)
    plan = json.loads(read_regular(source / 'plan.json'))
    report = json.loads(read_regular(source / 'report.json'))
    replay = json.loads(read_regular(source / 'replay-validation.json'))
    if (plan.get('provider') != provider or plan.get('driverProfile') != 'history'
            or plan.get('scenario', {}).get('parameters', {}).get('historyTarget') != 'child-messages'
            or report.get('status') != 'passed' or replay.get('status') != 'passed'
            or replay.get('fullConsumption') is not True):
        raise ValueError('existing-child seed requires passed native child history and strict replay')
    discoveries = [item['list_subagents'] for item in report.get('receipts', []) if 'list_subagents' in item]
    if len(discoveries) != 1 or not discoveries[0].get('agentIds'):
        raise ValueError('seed must identify one saved parent and its recorded children')
    session = discoveries[0]['sessionId']
    agents = discoveries[0]['agentIds']
    if not re.fullmatch(r'[0-9a-fA-F-]{36}', session) or len(set(agents)) != len(agents):
        raise ValueError('invalid seed identities')
    project = Path(plan['project']).resolve(strict=True)
    if project != source / 'project' or not project.is_relative_to(recording_temp_root()):
        raise ValueError('seed history scope must be its original isolated temporary project')
    # Native transcript cwd is evidence. Preserve both its bytes and project
    # key instead of rewriting it to pretend it belongs to the new case cwd.
    old_key = re.sub(r'[^a-zA-Z0-9]', '-', str(project))
    if len(old_key) > 200:
        raise ValueError('truncated native project keys are unsupported')
    saved = json.loads(read_regular(source / 'manifest.json')).get('savedFiles', {})
    prefix = Path('config/projects') / old_key
    selected = {name: digest for name, digest in saved.items()
                if Path(name) == prefix / (session + '.jsonl')
                or Path(name).is_relative_to(prefix / session / 'subagents')}
    main = str(prefix / (session + '.jsonl'))
    if main not in selected:
        raise ValueError('seed manifest lacks its parent transcript')
    for agent in agents:
        if not any(Path(name).name == 'agent-' + agent + '.jsonl' for name in selected):
            raise ValueError('discovered seed child lacks a verified transcript')
    copied = {}
    for name, digest in selected.items():
        relative = Path(name).relative_to(prefix)
        destination = Path('config/projects') / old_key / relative
        data = read_regular(source / name, None)
        if hashlib.sha256(data).hexdigest() != digest:
            raise ValueError('seed saved content changed before copying')
        target = root / destination
        target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        with target.open('xb') as stream:
            stream.write(data)
        target.chmod(0o600)
        copied[str(destination)] = {'source': name, 'sha256': digest}
    return {'provider': provider, 'sourceRoot': str(source), 'sourceProject': str(project),
            'projectFiles': project_files(project), 'sessionId': session, 'agentIds': agents,
            'sourceFiles': {name: sha(source / name) for name in
                            ('plan.json', 'manifest.json', 'report.json', 'replay-validation.json', 'raw/raw.jsonl')},
            'files': copied}


def verify_seed(plan, *, check_source=True):
    seed = plan.get('historySeed')
    if not isinstance(seed, dict) or seed.get('provider') not in ('claude', 'codex') or not seed.get('files'):
        raise ValueError('frozen existing-child seed required')
    root = Path(plan['root'])
    project = Path(seed.get('sourceProject', ''))
    temp_root = Path(plan['tempRoot']) if 'tempRoot' in plan else recording_temp_root()
    if (plan.get('historyProject') != str(project) or not project.is_absolute()
            or project != Path(seed.get('sourceRoot', '')) / 'project'
            or not project.is_relative_to(temp_root)):
        raise ValueError('history scope differs from the frozen source project')
    for name, value in seed['files'].items():
        path = Path(name)
        prefix = 'history-seed/config/sessions' if seed['provider'] == 'codex' else 'config/projects'
        if not path.is_relative_to(prefix) or '..' in path.parts or sha(root / path) != value['sha256']:
            raise ValueError('existing-child seed copy changed')
    if check_source:
        if project.resolve(strict=True) != project or project_files(project) != seed.get('projectFiles'):
            raise ValueError('source project changed during read-only observation')
        source = Path(seed['sourceRoot'])
        for name, digest in seed['sourceFiles'].items():
            if sha(source / name) != digest:
                raise ValueError('source recording identity changed')
        for value in seed['files'].values():
            if sha(source / value['source']) != value['sha256']:
                raise ValueError('source recording saved data changed')
    return seed


async def observe_claude(plan, report):
    from claude_agent_sdk import ClaudeSDKClient, ClaudeAgentOptions, Transport, list_subagents
    from .stream import json_messages
    # Replay owns frozen copies and their original source digests. It must not
    # require the old recording directory to remain available after capture.
    check_source = not bool(os.environ.get('AGENT_RECORDING_MOCK'))
    seed = verify_seed(plan, check_source=check_source)
    if '--resume' not in plan['args'] or plan['args'][plan['args'].index('--resume') + 1] != seed['sessionId']:
        raise ValueError('native resume must select the frozen seed parent')

    class RawTransport(Transport):
        def __init__(self): self.proc = None
        async def connect(self):
            self.proc = await asyncio.create_subprocess_exec(*proxy_command(plan), stdin=asyncio.subprocess.PIPE,
                         stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.DEVNULL)
        async def write(self, data):
            self.proc.stdin.write(data.encode()); await self.proc.stdin.drain()
        async def read_messages(self):
            async for value in json_messages(self.proc.stdout): yield value
        async def end_input(self):
            if self.proc and self.proc.stdin: self.proc.stdin.close()
        def is_ready(self): return self.proc is not None and self.proc.returncode is None
        async def close(self):
            await self.end_input()
            if self.proc and self.proc.returncode is None:
                try: await asyncio.wait_for(self.proc.wait(), 5)
                except asyncio.TimeoutError:
                    self.proc.terminate(); await asyncio.wait_for(self.proc.wait(), 5)
            if self.proc and self.proc.returncode:
                raise RuntimeError('existing-child observation transport failed')

    options = ClaudeAgentOptions(cwd=plan['historyProject'], resume=seed['sessionId'], forward_subagent_text=True)
    async with ClaudeSDKClient(options=options, transport=RawTransport()) as client:
        async def wait_observed():
            async for event in client.receive_messages():
                report['events'] += 1
                native = serial(event)
                data = native.get('data', native)
                if (data.get('session_id') == seed['sessionId'] and data.get('hook_name') == 'SessionStart:resume'
                        and data.get('hook_event') == 'SessionStart' and data.get('uuid')
                        and native.get('subtype', data.get('subtype')) in ('hook_started', 'hook_response')):
                    return native
        try:
            observed = await asyncio.wait_for(wait_observed(), 10)
        except asyncio.TimeoutError as exc:
            raise Untriggered('resume did not establish observed native parent before any input') from exc
        if observed is None:
            raise Untriggered('resume ended without observed native parent')
        report['receipts'].append({'existing-observation': observed})
        verify_seed(plan, check_source=check_source)
        agents = list_subagents(seed['sessionId'], directory=plan['historyProject'])
        if agents != seed['agentIds']:
            raise Untriggered('resumed observation did not discover the exact pre-existing children')
        report['receipts'].append({'existing-children': {'sessionId': seed['sessionId'], 'agentIds': agents,
                                                       'sourceFiles': seed['files']}})
    verify_seed(plan, check_source=check_source)
    require_observation_wire(plan, report)


def require_observation_wire(plan, report):
    frames = [json.loads(line) for line in read_regular(Path(plan['root']) / 'raw/raw.jsonl', None).splitlines()]
    requests = [frame['wire'] for frame in frames if frame['direction'] == 'c2s']
    if (len(requests) != 1 or requests[0].get('type') != 'control_request'
            or requests[0].get('request', {}).get('subtype') != 'initialize'):
        raise ValueError('existing-child observation must not submit new input or create children')
    seed = plan['historySeed']
    observed = next((item['existing-observation'] for item in report['receipts'] if 'existing-observation' in item), {})
    observed = observed.get('data', observed)
    matches = [frame for frame in frames if frame['direction'] == 's2c'
               and frame['wire'].get('uuid') == observed.get('uuid')
               and frame['wire'].get('session_id') == seed['sessionId']
               and frame['wire'].get('hook_name') == 'SessionStart:resume']
    if len(matches) != 1:
        raise ValueError('existing-child observer receipt lacks a unique native resume hook')
