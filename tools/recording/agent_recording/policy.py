"""Reviewed native launch policy, independent of the Go SDK and record tools."""
import json

SDK_VERSIONS = {'codex': ('openai-codex', '0.161.0'),
                'claude': ('claude-agent-sdk', '0.2.174')}
CODEX_SETTINGS = ['features.plugins=false', 'features.apps=false', 'features.enable_mcp_apps=false', 'features.skip_host_skill_discovery=true', 'skills.include_instructions=false', 'skills.bundled.enabled=false', 'features.memories=false', 'notify=[]', 'features.shell_snapshot=false', 'project_doc_max_bytes=0', 'features.shell_tool=false', 'features.stable_environment_tools=false', 'features.multi_agent_v2=false', 'agents.enabled=false', 'features.code_mode=false', 'features.js_repl=false', 'web_search="disabled"', 'approval_policy="on-request"', 'analytics.enabled=false', 'otel.exporter="none"', 'otel.trace_exporter="none"', 'otel.metrics_exporter="none"', 'features.default_mode_request_user_input=true', 'tools.update_plan.enabled=false', 'shell_environment_policy.inherit="none"']
SYSTEM = 'Perform only the supplied synthetic task. Use no external context or additional tools. Be concise.'
TOOLS = {'004': ['Read', 'Write'], '009': ['Write'], '010': ['Read'], '011': ['AskUserQuestion']}

def reviewed_versions(provider):
    return {'codex': ['0.161.0'], 'claude': ['2.1.199']}[provider]

def native_arguments(provider, story, child_case=False):
    if provider == 'codex':
        settings = list(CODEX_SETTINGS)
        if child_case:
            settings.remove('agents.enabled=false')
            settings += ['agents.enabled=true', 'features.multi_agent=true', 'agents.max_concurrent_threads_per_session=1', 'agents.max_depth=1', 'agents.default_subagent_reasoning_effort=\"low\"']
        return ['app-server', '--stdio'] + [v for item in settings for v in ['-c', item]]
    tools = ['Agent', 'Read'] if child_case else TOOLS.get(story[-3:], [])
    settings = json.dumps({'disableAllHooks': True, 'permissions': {'ask': tools}})
    args = ['--print', '--safe-mode', '--disable-slash-commands', '--strict-mcp-config',
            '--mcp-config', '{"mcpServers":{}}', '--no-chrome', '--setting-sources', '',
            '--settings', settings, '--tools', ','.join(tools), '--max-budget-usd', '1',
            '--max-turns', '4', '--system-prompt', SYSTEM,
            '--output-format', 'stream-json', '--verbose', '--input-format', 'stream-json',
            '--permission-prompt-tool', 'stdio', '--include-partial-messages']
    if story[-3:] != '019':
        args += ['--no-session-persistence']
    return args
