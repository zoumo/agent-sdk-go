"""Resume an independently saved Codex parent, then discover existing children."""
import json
import os
from pathlib import Path

from .adapters.common import proxy_command, serial
from .environment import read_regular, recording_temp_root, verify_recording
from .existing_children import sha, project_files, verify_seed
from .history import pages


def prepare_seed(source, root):
    source, root = Path(source).resolve(strict=True), Path(root).resolve(strict=True)
    verify_recording(source)
    plan = json.loads(read_regular(source / 'plan.json'))
    report = json.loads(read_regular(source / 'report.json'))
    replay = json.loads(read_regular(source / 'replay-validation.json'))
    if (plan.get('provider') != 'codex' or plan.get('driverProfile') != 'history'
            or plan.get('scenario', {}).get('parameters', {}).get('historyTarget') != 'child-messages'
            or report.get('status') != 'passed' or replay.get('status') != 'passed'
            or replay.get('fullConsumption') is not True):
        raise ValueError('Codex seed requires passed native child history and strict replay')
    queries = [x['history-query'] for x in report['receipts'] if 'history-query' in x]
    children = [x for x in queries if x['method'] == 'thread/list' and x['params'].get('parentThreadId')]
    if len(children) != 1 or not children[0]['response'].get('data'):
        raise ValueError('Codex seed requires an unambiguous saved parent and child list')
    session = children[0]['params']['parentThreadId']
    agents = [x['id'] for x in children[0]['response']['data']]
    if len(set(agents)) != len(agents) or any(x.get('parentThreadId') != session for x in children[0]['response']['data']):
        raise ValueError('Codex seed child parent identity differs')
    project = Path(plan['project']).resolve(strict=True)
    if project != source / 'project' or not project.is_relative_to(recording_temp_root()):
        raise ValueError('seed must retain its isolated original project scope')
    saved = json.loads(read_regular(source / 'manifest.json')).get('savedFiles', {})
    copied, found = {}, set()
    for name, digest in saved.items():
        if not Path(name).is_relative_to('config/sessions'):
            continue
        data = read_regular(source / name, None)
        first = json.loads(data.splitlines()[0])
        identity = first.get('payload', {}).get('id') if first.get('type') == 'session_meta' else None
        if identity not in [session, *agents]:
            continue
        if identity in found or sha(source / name) != digest:
            raise ValueError('Codex saved seed is duplicated or changed')
        found.add(identity)
        # Immutable input evidence is separate from the native writable home.
        # Do not copy SQLite indexes containing absolute source rollout paths.
        for destination in [Path('history-seed') / name, Path(name)]:
            target = root / destination
            target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
            with target.open('xb') as stream: stream.write(data)
            target.chmod(0o600)
        copied[str(Path('history-seed') / name)] = {'source': name, 'sha256': digest}
    if found != {session, *agents}:
        raise ValueError('Codex seed manifest lacks hashed parent or child rollouts; record a fresh seed')
    return {'provider': 'codex', 'sourceRoot': str(source), 'sourceProject': str(project),
            'projectFiles': project_files(project), 'sessionId': session, 'agentIds': agents,
            'sourceFiles': {name: sha(source / name) for name in
                            ('plan.json', 'manifest.json', 'report.json', 'replay-validation.json', 'raw/raw.jsonl')},
            'files': copied}


def require_wire(plan, frames):
    requests = [f['wire'] for f in frames if f['direction'] == 'c2s']
    methods = [r.get('method') for r in requests]
    if (methods[:3] != ['initialize', 'initialized', 'thread/resume']
            or methods[-1:] != ['thread/unsubscribe'] or not methods[3:-1]
            or any(m != 'thread/list' for m in methods[3:-1])):
        raise ValueError('existing observation must resume once, query, and detach without new input')
    responses = {json.dumps(f['wire']['id']): f['wire'].get('result') for f in frames
                 if f['direction'] == 's2c' and 'id' in f['wire']}
    # Every dependent operation must follow its own successful response on the
    # original wire. SDK receipt order cannot substitute for native causality.
    positions = [(i, f['wire']) for i, f in enumerate(frames) if f['direction'] == 'c2s'
                 and f['wire'].get('method') != 'initialized']
    ids = [json.dumps(w.get('id')) for _, w in positions]
    if len(ids) != len(set(ids)) or 'null' in ids:
        raise ValueError('missing or repeated native request identity')
    for n, (position, request) in enumerate(positions):
        following = positions[n + 1][0] if n + 1 < len(positions) else len(frames)
        replies = [i for i, f in enumerate(frames) if f['direction'] == 's2c'
                   and f['wire'].get('id') == request['id'] and 'result' in f['wire']]
        if len(replies) != 1 or not position < replies[0] < following:
            raise ValueError('query or close precedes its native prerequisite response')
    seed = plan['historySeed']
    resumed = responses.get(json.dumps(requests[2]['id'])) or {}
    if (requests[2]['params'].get('threadId') != seed['sessionId']
            or requests[2]['params'].get('model') != plan['model']
            or resumed.get('thread', {}).get('id') != seed['sessionId']
            or resumed.get('model') != plan['model']):
        raise ValueError('resume identity or effective model differs')
    agents, cursor = [], None
    for request in requests[3:-1]:
        params = request['params']
        if (params != dict({'ancestorThreadId': seed['sessionId'], 'sourceKinds': ['subAgentThreadSpawn'], 'limit': 10},
                           **({'cursor': cursor} if cursor is not None else {}))):
            raise ValueError('discovery scope or native cursor differs')
        result = responses.get(json.dumps(request['id'])) or {}
        if 'nextCursor' not in result or not isinstance(result.get('data'), list):
            raise ValueError('discovery lacks native coverage evidence')
        for child in result['data']:
            if child.get('parentThreadId') != seed['sessionId']:
                raise ValueError('saved child relationship differs from seed')
            agents.append(child.get('id'))
        cursor = result['nextCursor']
    if cursor is not None or sorted(agents) != sorted(seed['agentIds']) or len(agents) != len(set(agents)):
        raise ValueError('discovered children differ from immutable pre-observation seed')
    closed = responses.get(json.dumps(requests[-1]['id'])) or {}
    if requests[-1]['params'] != {'threadId': seed['sessionId']} or closed.get('status') != 'unsubscribed':
        raise ValueError('resumed observation did not detach its native parent')


def observe(plan, report):
    from openai_codex.client import CodexClient, CodexConfig
    from pydantic import BaseModel, ConfigDict
    class NativeResponse(BaseModel):
        model_config = ConfigDict(extra='allow')
    check_source = not bool(os.environ.get('AGENT_RECORDING_MOCK'))
    seed = verify_seed(plan, check_source=check_source)
    config = CodexConfig(launch_args_override=tuple(proxy_command(plan)), cwd=plan['historyProject'],
                         client_name='official-python-story-recorder', client_title='Protocol recording')
    def reject(method, params):
        raise ValueError('unexpected live request in zero-input existing-child observation')
    with CodexClient(config=config, approval_handler=reject) as client:
        report['receipts'].append({'initialize': serial(client.initialize())})
        def request(method, params):
            response = serial(client.request(method, params, response_model=NativeResponse))
            report['receipts'].append({'existing-query': {'method': method, 'params': dict(params), 'response': response}})
            return response
        response = request('thread/resume', {'threadId': seed['sessionId'], 'model': plan['model'],
                                             'cwd': plan['historyProject']})
        if response.get('thread', {}).get('id') != seed['sessionId'] or response.get('model') != plan['model']:
            raise ValueError('native resume selected a different identity or model')
        children = pages(request, 'thread/list', {'ancestorThreadId': seed['sessionId'],
                                                 'sourceKinds': ['subAgentThreadSpawn'], 'limit': 10})
        if sorted(x['id'] for x in children) != sorted(seed['agentIds']):
            raise ValueError('resumed Session did not discover its pre-existing children')
        request('thread/unsubscribe', {'threadId': seed['sessionId']})
    verify_seed(plan, check_source=check_source)
    frames = [json.loads(line) for line in read_regular(Path(plan['root']) / 'raw/raw.jsonl', None).splitlines()]
    require_wire(plan, frames)
