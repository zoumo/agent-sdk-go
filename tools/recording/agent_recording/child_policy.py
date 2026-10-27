"""The explicitly authorized one-child synthetic read case, without SDK imports."""
import json

CHILD_FILE = 'child-fixture.txt'
CHILD_TEXT = 'SYNTHETIC_CHILD_TOKEN\n'
CHILD_ROLE = 'fixture_reader'


def case_contract(story):
    background = story.endswith('007')
    return {'id': story + ('/one-child-background-completion' if background else '/one-child-read'),
            'scope': ('Confirm a native child completion after the root result.' if background else
                      'Confirm one native child identity and its authorized fixed-file read.'),
            'outOfScope': ['nested and parallel children', 'OS-enforced recursive resource limits',
                           'all filesystem reads', 'all user-story acceptance branches']}


def child_task(provider):
    tool = 'fixture_read' if provider == 'codex' else 'Read'
    return (f'Use {tool} exactly once to read only child-fixture.txt in the inherited working directory. '
            'Return exactly SYNTHETIC_CHILD_TOKEN. Do not create agents or descendants. '
            'Do not use shell, network, search, writes, or any other file or tool.')


def root_prompt(provider, story):
    task = json.dumps(child_task(provider))
    if provider == 'codex':
        launch = f'Use spawn_agent exactly once with agent_type fixture_reader and message exactly {task}. '
        finish = ('Wait for this one child with wait, then report its result. ' if story.endswith('006') else
                  'Return ROOT_RETURNED immediately after launch, without waiting or closing the child. ')
    else:
        background = 'false' if story.endswith('006') else 'true'
        launch = f'Use Agent exactly once with subagent_type fixture_reader, prompt exactly {task}, and run_in_background={background}. '
        finish = ('Wait for the child result, then report it. ' if story.endswith('006') else
                  'Return ROOT_RETURNED immediately after launch; do not wait or use TaskOutput. ')
    return launch + finish + 'The root must not read files, use shell/network/other tools, or create another child.'


def role_file():
    return ('name="fixture_reader"\n'
            'description="Read the single synthetic child fixture with fixture_read."\n'
            'developer_instructions=' + json.dumps(child_task('codex')) + '\n'
            '[features]\nshell_tool=false\napps=false\nplugins=false\nmemories=false\n')


def check_output(plan, obj, state):
    subtype, method = obj.get('subtype', ''), obj.get('method', '')
    if subtype.startswith('hook_') or method.startswith('hook/'):
        raise ValueError('unexpected hook output')
    if not plan.get('childCase'):
        if subtype.startswith('task_') or obj.get('parent_tool_use_id'):
            raise ValueError('unexpected child output outside selected child case')
        return
    if plan['provider'] == 'claude':
        anchors = state.setdefault('agentTools', set())
        parent = obj.get('parent_tool_use_id')
        if parent and parent not in state.setdefault('agentTools', set()):
            raise ValueError('child output lacks the selected native Agent tool anchor')
        for block in obj.get('message', {}).get('content', []) if isinstance(obj.get('message', {}).get('content'), list) else []:
            if block.get('type') != 'tool_use':
                continue
            tool = block.get('name')
            if tool == 'Agent' and not parent:
                anchors.add(block['id'])
                if len(state['agentTools']) > 1:
                    raise ValueError('second native Agent invocation outside one-child case')
            elif tool != 'Read' or not parent:
                raise ValueError('tool outside one-child read case')
        if subtype == 'task_started':
            tasks = state.setdefault('tasks', set())
            tasks.add(obj.get('task_id'))
            if len(tasks) > 1 or obj.get('tool_use_id') not in state.setdefault('agentTools', set()):
                raise ValueError('unexpected native task outside one-child case')
    else:
        params = obj.get('params') or {}
        item = params.get('item') or {}
        typ = item.get('type')
        if typ == 'collabAgentToolCall':
            tool = item.get('tool')
            allowed = {'spawnAgent', 'wait'} if plan['story'].endswith('006') else {'spawnAgent'}
            if tool not in allowed:
                raise ValueError('native collaboration operation outside selected child case')
            if tool == 'spawnAgent':
                spawns = state.setdefault('spawnItems', set())
                spawns.add(item.get('id'))
                if len(spawns) > 1:
                    raise ValueError('second native spawn outside one-child case')
        if typ in ('commandExecution', 'fileChange', 'mcpToolCall', 'webSearch'):
            raise ValueError('native execution tool outside one-child read case')
