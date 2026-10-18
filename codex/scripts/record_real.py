#!/usr/bin/env python3
"""Explicit bounded maintenance recording; never called by CI or replay."""
import argparse
import hashlib
import json
import os
import pathlib
import re
import shutil
import signal
import subprocess
import time
import tomllib


def guard(record, environ):
    if not record or environ.get('SDK_REAL_AGENT_E2E') != '1':
        raise ValueError('requires --record and SDK_REAL_AGENT_E2E=1')
    if environ.get('CI'):
        raise ValueError('real recording refused in CI')


def hook_states(home, config):
    """Disable only user hook identities; managed identities are never touched."""
    sources = [(home/'config.toml', config.get('hooks', {}))]
    if (home/'hooks.json').exists():
        sources.append((home/'hooks.json', json.loads((home/'hooks.json').read_text()).get('hooks', {})))
    labels = {'PreToolUse': 'pre_tool_use', 'PermissionRequest': 'permission_request',
              'PostToolUse': 'post_tool_use', 'PreCompact': 'pre_compact',
              'PostCompact': 'post_compact', 'SessionStart': 'session_start',
              'SessionEnd': 'session_end', 'UserPromptSubmit': 'user_prompt_submit',
              'SubagentStart': 'subagent_start', 'SubagentStop': 'subagent_stop',
              'Stop': 'stop', 'Interrupt': 'interrupt'}
    states = {}
    for source, events in sources:
        for event, groups in events.items():
            if event not in labels:
                if event != 'state':
                    raise ValueError('unknown user hook event')
                continue
            for gi, group in enumerate(groups):
                for hi, _ in enumerate(group.get('hooks', [])):
                    states[f'{source}:{labels[event]}:{gi}:{hi}'] = {'enabled': False}
    return states


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--record', action='store_true')
    p.add_argument('--cli', required=True, type=pathlib.Path)
    p.add_argument('--runner', required=True, type=pathlib.Path)
    p.add_argument('--session-root', required=True, type=pathlib.Path)
    a = p.parse_args()
    guard(a.record, os.environ)
    if not a.cli.is_absolute() or not a.runner.is_absolute() or not a.session_root.is_absolute():
        raise ValueError('absolute executable and isolated output paths required')
    version = subprocess.run([str(a.cli), '--version'], capture_output=True, timeout=10, check=True)
    if version.stdout.strip() != b'codex-cli 0.162.1':
        raise ValueError('pinned CLI version mismatch; no upgrade attempted')
    cli_hash = hashlib.sha256(a.cli.read_bytes()).hexdigest()
    # Exclusive root makes accidental invocation with the same ticket budget fail.
    a.session_root.mkdir(mode=0o700)
    home = pathlib.Path(os.environ.get('CODEX_HOME', str(pathlib.Path.home()/'.codex')))
    config = tomllib.loads((home/'config.toml').read_text())
    settings = {
        'features.plugins': 'false', 'features.apps': 'false',
        'features.enable_mcp_apps': 'false', 'features.recommended_plugins': 'false',
        'features.skip_host_skill_discovery': 'true', 'skills.include_instructions': 'false',
        'skills.bundled.enabled': 'false', 'features.memories': 'false',
        'features.goals': 'false', 'notify': '[]', 'features.shell_snapshot': 'false',
        'project_doc_max_bytes': '0', 'features.shell_tool': 'false',
        'features.stable_environment_tools': 'false', 'features.multi_agent_v2': 'false',
        'agents.enabled': 'false', 'features.code_mode': 'false', 'features.js_repl': 'false',
        'web_search': '"disabled"', 'approvals_reviewer': '"user"',
        'approval_policy': '"on-request"', 'analytics.enabled': 'false',
        'otel.exporter': '"none"', 'otel.trace_exporter': '"none"',
        'otel.metrics_exporter': '"none"', 'otel.log_user_prompt': 'false',
        'otel.log_agent_responses': 'false', 'features.default_mode_request_user_input': 'true',
        'features.request_permissions_tool': 'false', 'tools.update_plan.enabled': 'false',
    }
    states = hook_states(home, config)
    if states:
        settings['hooks.state'] = '{'+','.join(json.dumps(k)+'={enabled=false}' for k in states)+'}'
    # CLI paths split at dots literally. Names are config identifiers, never shell text.
    for section in ('mcp_servers', 'plugins'):
        for key in config.get(section, {}):
            if '.' in key:
                raise ValueError('config identifier needs a reviewed override strategy')
            settings[f'{section}.{key}.enabled'] = 'false'
    sdk_sha = subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip()
    total_start = time.monotonic()
    summary = []
    for story in ('s1', 's2'):
        remaining = 480 - (time.monotonic()-total_start)
        if remaining < 125:
            summary.append({'story': story, 'attempted': False, 'reason': 'total deadline'})
            break
        private = a.session_root/story
        private.mkdir(mode=0o700)
        project = private/'project'; project.mkdir(mode=0o700)
        args = ['app-server', '--stdio']
        overrides = dict(settings)
        overrides.update({'sqlite_home': json.dumps(str(private/'state')), 'log_dir': json.dumps(str(private/'logs'))})
        for key, value in overrides.items():
            args += ['-c', key+'='+value]
        cfg = {'executable': str(a.cli), 'args': args, 'dir': str(project), 'rawDir': str(private), 'seconds': 120}
        cfg_path = private/'launch.private.json'
        cfg_path.write_text(json.dumps(cfg)); cfg_path.chmod(0o600)
        env = os.environ.copy()
        env.update({'CODEX_RECORD_STORY': story, 'CODEX_RECORD_HOME': str(home), 'CODEX_RECORD_CLI_SHA256': cli_hash, 'CODEX_RECORD_SDK_SHA': sdk_sha})
        started = time.monotonic()
        with (private/'runner.private.log').open('wb') as log:
            os.chmod(private/'runner.private.log', 0o600)
            child = subprocess.Popen([str(a.runner), '--record', str(cfg_path)], env=env, cwd=project, stdout=log, stderr=log, start_new_session=True)
            try:
                status = child.wait(timeout=min(125, remaining))
            except subprocess.TimeoutExpired:
                os.killpg(child.pid, signal.SIGTERM)
                try:
                    child.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    os.killpg(child.pid, signal.SIGKILL); child.wait(timeout=5)
                status = -1
        row = {'story': story, 'attempted': True, 'exit': status, 'milliseconds': round((time.monotonic()-started)*1000), 'children': 0}
        summary.append(row)
        print(json.dumps(row), flush=True)
        transport_path = private/'transport.json'
        if not transport_path.exists() or not json.loads(transport_path.read_text()).get('cleanupComplete'):
            break
    (a.session_root/'budget.json').write_text(json.dumps({'roots': len([s for s in summary if s['attempted']]), 'children': 0, 'milliseconds': round((time.monotonic()-total_start)*1000), 'stories': summary}, indent=2)+'\n')
    print('Raw evidence remains private. Publication requires separate sanitization and offline replay.')


if __name__ == '__main__':
    main()
