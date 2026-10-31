"""Native transport arguments and reproducible installation defaults."""

import json

SDK_VERSIONS = {'codex': ('openai-codex', '0.161.0'),
                'claude': ('claude-agent-sdk', '0.2.174')}
SYSTEM = 'Complete the supplied synthetic task in the working directory. Be concise.'

DRIVER_PROFILES = frozenset((
    'continuation', 'structured-input', 'active-input', 'fixture-tools',
    'children', 'background-children', 'approval', 'deferred-approval',
    'questions', 'configuration-timing', 'interrupt', 'session-close',
    'history', 'topology', 'existing-children', 'remote-close'))


def driver_profile(plan):
    profile = plan.get('driverProfile')
    if profile not in DRIVER_PROFILES:
        raise ValueError('current simulation plan requires an explicit driver profile')
    return profile


def native_arguments(provider, story, child_case=False, profile=None):
    if provider == 'codex':
        settings = ['features.default_mode_request_user_input=true']
        if child_case or profile == 'topology':
            settings += ['agents.enabled=true', 'features.multi_agent=true']
        if profile == 'topology':
            settings += ['agents.max_depth=2']
        return ['app-server', '--stdio'] + [v for item in settings for v in ['-c', item]]
    args = ['--print', '--system-prompt', SYSTEM,
            '--output-format', 'stream-json', '--verbose', '--input-format', 'stream-json',
            '--permission-prompt-tool', 'stdio', '--include-partial-messages']
    if profile in ('topology', 'children', 'background-children'):
        args += ['--forward-subagent-text']
    # Native auto-approval would bypass the original permission handles these
    # flows explicitly need to observe. Ask without limiting available tools.
    ask = {'fixture-tools': ['Read', 'Write'], 'deferred-approval': ['Read'],
           'children': ['Agent', 'Read'], 'background-children': ['Agent', 'Read']}.get(profile)
    if ask:
        args += ['--settings', json.dumps({'permissions': {'ask': ask}})]
    persistent = profile == 'history'
    if not persistent:
        args += ['--no-session-persistence']
    return args
